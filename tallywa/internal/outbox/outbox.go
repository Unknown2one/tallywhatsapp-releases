// Package outbox implements a durable, idempotent send queue backed by
// BoltDB. It is the heart of the "no message ever gets lost" guarantee:
// enqueue is synchronous, delivery is asynchronous and survives crashes,
// power loss, and WhatsApp disconnects.
//
// Concurrency model: a single Run goroutine claims and processes items.
// BoltDB serializes write transactions, so Enqueue is safe to call from
// any number of goroutines (every HTTP request from Tally lands here).
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	bolt "go.etcd.io/bbolt"
)

var (
	bucketItems = []byte("items")
	bucketIdem  = []byte("idem")
)

// ErrPermanent signals to the worker that the failure is non-retriable
// (e.g. invalid recipient, unsupported file type). The item goes straight
// to dead-letter without consuming retry attempts.
var ErrPermanent = errors.New("outbox: permanent failure")

// Status of an outbox item.
type Status string

const (
	StatusPending Status = "pending"
	StatusSending Status = "sending"
	StatusSent    Status = "sent"
	StatusDead    Status = "dead"
)

// Kind tells the Sender what to do with the payload.
type Kind string

const (
	KindText         Kind = "text"
	KindFile         Kind = "file"
	KindFileWithText Kind = "file_with_text"
)

// Item is a single queued message. JSON tags are the on-disk schema.
// Renaming a field breaks every existing queue file in the field — bump
// a schema version and migrate instead.
type Item struct {
	ID             string    `json:"id"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	Recipient      string    `json:"recipient"`
	Kind           Kind      `json:"kind"`
	Text           string    `json:"text,omitempty"`
	FilePath       string    `json:"file_path,omitempty"`
	Voucher        string    `json:"voucher,omitempty"`
	Status         Status    `json:"status"`
	Attempts       int       `json:"attempts"`
	MaxAttempts    int       `json:"max_attempts"`
	NextAttemptAt  time.Time `json:"next_attempt_at"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	LastError      string    `json:"last_error,omitempty"`
}

// Sender is the pluggable delivery interface. Production wiring uses
// whatsmeow; tests use a fake.
//
// Returning nil = success.
// Returning ErrPermanent = give up immediately.
// Any other error = transient, will be retried with backoff.
type Sender interface {
	Send(ctx context.Context, item *Item) error
}

// ReceiptCooldown is the minimum spacing between two consecutive Receipt
// vouchers leaving the queue. Receipts are typically batched at end of
// day and WhatsApp will rate-limit / risk-flag a burst, so we space them
// out at ~90 s (between the 60–120 s target the product spec calls for).
const ReceiptCooldown = 90 * time.Second

// Outbox is the durable queue handle.
type Outbox struct {
	db     *bolt.DB
	logger *slog.Logger

	now      func() time.Time
	schedule func(attempt int) time.Duration

	entropyMu sync.Mutex // guards ulid.Make-equivalent

	// receiptMu guards lastReceiptSentAt — read on every claim, written on
	// every successful receipt send. Hot path is read-only.
	receiptMu         sync.RWMutex
	lastReceiptSentAt time.Time

	mu     sync.Mutex
	closed bool
}

// Options configures the outbox. Path is required; everything else has
// sensible defaults.
type Options struct {
	Path     string
	Logger   *slog.Logger
	Now      func() time.Time
	Schedule func(attempt int) time.Duration
}

// Open creates or attaches to the queue file. Recovers items left in
// "sending" state from a previous crash by demoting them back to pending.
func Open(opts Options) (*Outbox, error) {
	if opts.Path == "" {
		return nil, errors.New("outbox: Path required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Schedule == nil {
		opts.Schedule = DefaultSchedule
	}

	db, err := bolt.Open(opts.Path, 0o644, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("outbox: open: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bucketItems); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists(bucketIdem)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	o := &Outbox{
		db:       db,
		logger:   opts.Logger,
		now:      opts.Now,
		schedule: opts.Schedule,
	}
	if err := o.recoverInflight(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return o, nil
}

// Close flushes and releases the queue file.
func (o *Outbox) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	o.closed = true
	return o.db.Close()
}

// Enqueue persists a new item. If IdempotencyKey is set and an item with
// the same key already exists, that existing item is returned unchanged
// (this is what makes Tally double-clicks safe).
func (o *Outbox) Enqueue(item *Item) (*Item, error) {
	if item.Recipient == "" {
		return nil, errors.New("outbox: Recipient required")
	}
	if item.Kind == "" {
		return nil, errors.New("outbox: Kind required")
	}
	now := o.now()
	if item.MaxAttempts == 0 {
		item.MaxAttempts = 8
	}
	item.Status = StatusPending
	item.Attempts = 0
	item.NextAttemptAt = now
	item.CreatedAt = now
	item.UpdatedAt = now

	var result *Item
	err := o.db.Update(func(tx *bolt.Tx) error {
		idem := tx.Bucket(bucketIdem)
		items := tx.Bucket(bucketItems)

		if item.IdempotencyKey != "" {
			if existingID := idem.Get([]byte(item.IdempotencyKey)); existingID != nil {
				if raw := items.Get(existingID); raw != nil {
					var found Item
					if err := json.Unmarshal(raw, &found); err == nil {
						result = &found
						return nil
					}
				}
			}
		}

		item.ID = o.newID(now)
		data, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if err := items.Put([]byte(item.ID), data); err != nil {
			return err
		}
		if item.IdempotencyKey != "" {
			if err := idem.Put([]byte(item.IdempotencyKey), []byte(item.ID)); err != nil {
				return err
			}
		}
		result = item
		return nil
	})
	return result, err
}

// Run blocks until ctx is cancelled, processing one item per tick. The
// 500ms tick rate is intentional: it keeps the queue lock-free in practice
// (no contention with Enqueue) and is fast enough that latency from
// Tally-click to WhatsApp-send is sub-second under normal load.
func (o *Outbox) Run(ctx context.Context, sender Sender) error {
	return o.RunWithInterval(ctx, sender, 500*time.Millisecond)
}

// RunWithInterval is the same as Run with a configurable tick. Used by
// tests that want fast iteration.
func (o *Outbox) RunWithInterval(ctx context.Context, sender Sender, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			o.Step(ctx, sender)
		}
	}
}

// Step processes at most one due item. Exported so tests can drive the
// state machine deterministically.
func (o *Outbox) Step(ctx context.Context, sender Sender) {
	item, err := o.claimNext()
	if err != nil {
		o.logger.Error("outbox: claim", "err", err)
		return
	}
	if item == nil {
		return
	}
	sendErr := sender.Send(ctx, item)
	if sendErr == nil {
		if err := o.markSent(item.ID); err != nil {
			o.logger.Error("outbox: mark sent", "id", item.ID, "err", err)
		}
		return
	}
	permanent := errors.Is(sendErr, ErrPermanent)
	if err := o.markFailed(item.ID, sendErr.Error(), permanent); err != nil {
		o.logger.Error("outbox: mark failed", "id", item.ID, "err", err)
	}
}

// Get returns an item by ID, or nil if not found. Mainly for diagnostics
// and the tray UI's "show last 20 sends" panel.
func (o *Outbox) Get(id string) (*Item, error) {
	var out *Item
	err := o.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bucketItems).Get([]byte(id))
		if raw == nil {
			return nil
		}
		var i Item
		if err := json.Unmarshal(raw, &i); err != nil {
			return err
		}
		out = &i
		return nil
	})
	return out, err
}

// Stats summarises queue contents for the tray UI badge.
type Stats struct {
	Pending int
	Sending int
	Sent    int
	Dead    int
}

func (o *Outbox) Stats() (Stats, error) {
	var s Stats
	err := o.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketItems).ForEach(func(_, v []byte) error {
			var item Item
			if err := json.Unmarshal(v, &item); err != nil {
				return nil
			}
			switch item.Status {
			case StatusPending:
				s.Pending++
			case StatusSending:
				s.Sending++
			case StatusSent:
				s.Sent++
			case StatusDead:
				s.Dead++
			}
			return nil
		})
	})
	return s, err
}

// List returns recent items, newest first, capped at limit (0 = all).
// Used by the dashboard activity log.
func (o *Outbox) List(limit int) ([]*Item, error) {
	var out []*Item
	err := o.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketItems).ForEach(func(_, v []byte) error {
			var item Item
			if err := json.Unmarshal(v, &item); err != nil {
				return nil
			}
			out = append(out, &item)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Requeue resets an item to pending so the worker picks it up again.
// Used by the dashboard "Resend" button. Sent and dead items can both
// be requeued; sending items are left alone (the worker is mid-flight).
// Attempts/error are preserved as history but the item gets a fresh
// NextAttemptAt of now.
func (o *Outbox) Requeue(id string) (*Item, error) {
	var out *Item
	err := o.mutateItem(id, func(item *Item) {
		if item.Status == StatusSending {
			return
		}
		item.Status = StatusPending
		item.NextAttemptAt = o.now()
		item.LastError = ""
		copy := *item
		out = &copy
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// claimNext finds the oldest pending item whose NextAttemptAt has elapsed
// and atomically marks it sending. Returns nil if there is nothing to do.
//
// Receipt vouchers are subject to ReceiptCooldown — we never claim a
// receipt sooner than ReceiptCooldown after the previous receipt's
// successful send. This protects WhatsApp from end-of-day batch bursts.
func (o *Outbox) claimNext() (*Item, error) {
	now := o.now()
	o.receiptMu.RLock()
	receiptReadyAt := o.lastReceiptSentAt.Add(ReceiptCooldown)
	o.receiptMu.RUnlock()
	receiptOnCooldown := now.Before(receiptReadyAt)

	var claimed *Item
	err := o.db.Update(func(tx *bolt.Tx) error {
		items := tx.Bucket(bucketItems)
		c := items.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var item Item
			if err := json.Unmarshal(v, &item); err != nil {
				continue
			}
			if item.Status != StatusPending {
				continue
			}
			if item.NextAttemptAt.After(now) {
				continue
			}
			if receiptOnCooldown && isReceipt(item.Voucher) {
				continue
			}
			item.Status = StatusSending
			item.UpdatedAt = now
			data, err := json.Marshal(&item)
			if err != nil {
				return err
			}
			if err := items.Put(k, data); err != nil {
				return err
			}
			claimed = &item
			return nil
		}
		return nil
	})
	return claimed, err
}

// isReceipt is intentionally permissive — the C# DLL normalises to
// lowercase "receipt" today, but TDL hand-edits could pass "Receipt".
func isReceipt(voucher string) bool {
	return strings.EqualFold(strings.TrimSpace(voucher), "receipt")
}

// NextReceiptReadyAt is the earliest moment the next receipt voucher
// will be eligible to leave the queue. Used by the dashboard to show
// the per-receipt ETA.
func (o *Outbox) NextReceiptReadyAt() time.Time {
	o.receiptMu.RLock()
	defer o.receiptMu.RUnlock()
	return o.lastReceiptSentAt.Add(ReceiptCooldown)
}

func (o *Outbox) markSent(id string) error {
	var sentItem *Item
	if err := o.mutateItem(id, func(item *Item) {
		item.Status = StatusSent
		item.LastError = ""
		copy := *item
		sentItem = &copy
	}); err != nil {
		return err
	}
	if sentItem != nil && isReceipt(sentItem.Voucher) {
		o.receiptMu.Lock()
		o.lastReceiptSentAt = o.now()
		o.receiptMu.Unlock()
	}
	return nil
}

func (o *Outbox) markFailed(id, errMsg string, permanent bool) error {
	return o.mutateItem(id, func(item *Item) {
		item.Attempts++
		item.LastError = errMsg
		if permanent || item.Attempts >= item.MaxAttempts {
			item.Status = StatusDead
			return
		}
		item.Status = StatusPending
		item.NextAttemptAt = o.now().Add(o.schedule(item.Attempts))
	})
}

func (o *Outbox) mutateItem(id string, fn func(*Item)) error {
	return o.db.Update(func(tx *bolt.Tx) error {
		items := tx.Bucket(bucketItems)
		raw := items.Get([]byte(id))
		if raw == nil {
			return nil
		}
		var item Item
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		fn(&item)
		item.UpdatedAt = o.now()
		data, err := json.Marshal(&item)
		if err != nil {
			return err
		}
		return items.Put([]byte(id), data)
	})
}

// recoverInflight runs once at Open. Items left in "sending" state mean
// the previous process died mid-send; we don't know if WhatsApp received
// the message or not. Demoting back to pending will trigger a retry; the
// idempotency key (voucher GUID + recipient) prevents duplicates at the
// app layer.
func (o *Outbox) recoverInflight() error {
	return o.db.Update(func(tx *bolt.Tx) error {
		items := tx.Bucket(bucketItems)
		now := o.now()
		return items.ForEach(func(k, v []byte) error {
			var item Item
			if err := json.Unmarshal(v, &item); err != nil {
				return nil
			}
			if item.Status != StatusSending {
				return nil
			}
			item.Status = StatusPending
			item.NextAttemptAt = now.Add(5 * time.Second)
			item.UpdatedAt = now
			data, err := json.Marshal(&item)
			if err != nil {
				return nil
			}
			return items.Put(k, data)
		})
	})
}

// newID generates a sortable ULID. ulid.Make is concurrency-safe.
func (o *Outbox) newID(now time.Time) string {
	o.entropyMu.Lock()
	defer o.entropyMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(now), ulid.DefaultEntropy()).String()
}

// DefaultSchedule returns exponential backoff with up to 20% jitter.
// Attempts: 1→~1s, 2→~5s, 3→~30s, 4→~2m, 5→~10m, 6+→~1h.
// Jitter prevents thundering-herd if many items become due at once.
func DefaultSchedule(attempt int) time.Duration {
	base := []time.Duration{
		1 * time.Second,
		5 * time.Second,
		30 * time.Second,
		2 * time.Minute,
		10 * time.Minute,
		1 * time.Hour,
	}
	idx := attempt - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(base) {
		idx = len(base) - 1
	}
	d := base[idx]
	jitter := time.Duration(rand.Int64N(int64(d) / 5))
	return d + jitter
}
