package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// fakeSender records calls and lets the test script return values per attempt.
type fakeSender struct {
	mu      sync.Mutex
	calls   []*Item
	results []error // popped front-to-back
}

func (f *fakeSender) Send(_ context.Context, item *Item) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *item
	f.calls = append(f.calls, &cp)
	if len(f.results) == 0 {
		return nil
	}
	r := f.results[0]
	f.results = f.results[1:]
	return r
}

func (f *fakeSender) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func openTestOutbox(t *testing.T, now func() time.Time) *Outbox {
	t.Helper()
	o, err := Open(Options{
		Path: filepath.Join(t.TempDir(), "queue.db"),
		Now:  now,
		// Tight schedule so retry tests aren't slow.
		Schedule: func(attempt int) time.Duration { return time.Millisecond },
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = o.Close() })
	return o
}

func TestEnqueue_AssignsIDAndPersists(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	o := openTestOutbox(t, func() time.Time { return now })

	got, err := o.Enqueue(&Item{
		Recipient: "919999999999",
		Kind:      KindText,
		Text:      "hello",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got.ID == "" {
		t.Fatalf("ID not assigned")
	}
	if got.Status != StatusPending {
		t.Errorf("Status = %s, want pending", got.Status)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, now)
	}
}

func TestEnqueue_IdempotencyDedupes(t *testing.T) {
	o := openTestOutbox(t, time.Now)

	first, err := o.Enqueue(&Item{
		IdempotencyKey: "voucher-42-919999999999",
		Recipient:      "919999999999",
		Kind:           KindText,
		Text:           "first",
	})
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	second, err := o.Enqueue(&Item{
		IdempotencyKey: "voucher-42-919999999999",
		Recipient:      "919999999999",
		Kind:           KindText,
		Text:           "second-should-be-ignored",
	})
	if err != nil {
		t.Fatalf("second Enqueue: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("idempotency failed: %s != %s", first.ID, second.ID)
	}
	if second.Text != "first" {
		t.Errorf("returned item should be the original, got Text=%q", second.Text)
	}
}

func TestStep_HappyPath(t *testing.T) {
	now := time.Now()
	o := openTestOutbox(t, func() time.Time { return now })
	sender := &fakeSender{}

	item, _ := o.Enqueue(&Item{Recipient: "9111", Kind: KindText, Text: "hi"})
	o.Step(context.Background(), sender)

	if sender.callCount() != 1 {
		t.Fatalf("sender called %d times, want 1", sender.callCount())
	}
	got, _ := o.Get(item.ID)
	if got.Status != StatusSent {
		t.Errorf("Status = %s, want sent", got.Status)
	}
}

func TestStep_TransientFailureRetries(t *testing.T) {
	current := time.Now()
	o := openTestOutbox(t, func() time.Time { return current })
	sender := &fakeSender{results: []error{errors.New("network blip")}}

	item, _ := o.Enqueue(&Item{Recipient: "9111", Kind: KindText, Text: "hi"})

	o.Step(context.Background(), sender)
	got, _ := o.Get(item.ID)
	if got.Status != StatusPending {
		t.Fatalf("after transient failure: Status = %s, want pending", got.Status)
	}
	if got.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", got.Attempts)
	}
	if !got.NextAttemptAt.After(current) {
		t.Errorf("NextAttemptAt should be in the future")
	}

	// Advance time past backoff and step again — should succeed.
	current = current.Add(10 * time.Second)
	o.Step(context.Background(), sender)
	got, _ = o.Get(item.ID)
	if got.Status != StatusSent {
		t.Errorf("after retry: Status = %s, want sent", got.Status)
	}
}

func TestStep_PermanentFailureGoesDeadImmediately(t *testing.T) {
	o := openTestOutbox(t, time.Now)
	sender := &fakeSender{results: []error{ErrPermanent}}

	item, _ := o.Enqueue(&Item{Recipient: "9111", Kind: KindText, Text: "hi"})
	o.Step(context.Background(), sender)

	got, _ := o.Get(item.ID)
	if got.Status != StatusDead {
		t.Errorf("Status = %s, want dead", got.Status)
	}
	if got.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", got.Attempts)
	}
}

func TestStep_DeadAfterMaxAttempts(t *testing.T) {
	current := time.Now()
	o := openTestOutbox(t, func() time.Time { return current })

	// Pre-create a sender that always fails.
	sender := &fakeSender{}
	for i := 0; i < 5; i++ {
		sender.results = append(sender.results, errors.New("boom"))
	}

	item, _ := o.Enqueue(&Item{
		Recipient:   "9111",
		Kind:        KindText,
		Text:        "hi",
		MaxAttempts: 3,
	})

	for i := 0; i < 5; i++ {
		current = current.Add(time.Second)
		o.Step(context.Background(), sender)
	}

	got, _ := o.Get(item.ID)
	if got.Status != StatusDead {
		t.Errorf("Status = %s, want dead", got.Status)
	}
	if got.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", got.Attempts)
	}
	if sender.callCount() != 3 {
		t.Errorf("sender called %d times, want 3", sender.callCount())
	}
}

func TestRecoverInflight_SendingItemsResetOnReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queue.db")

	// Manually craft a "sending" item, simulating a crash mid-send.
	db, err := bolt.Open(path, 0o644, nil)
	if err != nil {
		t.Fatalf("bolt open: %v", err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		b, _ := tx.CreateBucketIfNotExists(bucketItems)
		_, _ = tx.CreateBucketIfNotExists(bucketIdem)
		stuck := Item{
			ID:        "01STUCK",
			Recipient: "9111",
			Kind:      KindText,
			Status:    StatusSending,
			CreatedAt: time.Now().Add(-time.Hour),
		}
		raw, _ := json.Marshal(&stuck)
		return b.Put([]byte(stuck.ID), raw)
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = db.Close()

	// Reopen via the package — should sweep stuck → pending.
	o, err := Open(Options{Path: path})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer o.Close()

	got, _ := o.Get("01STUCK")
	if got.Status != StatusPending {
		t.Errorf("Status = %s, want pending after recovery", got.Status)
	}
}

func TestRun_StopsOnContextCancel(t *testing.T) {
	o := openTestOutbox(t, time.Now)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- o.RunWithInterval(ctx, &fakeSender{}, time.Millisecond) }()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestStats(t *testing.T) {
	o := openTestOutbox(t, time.Now)

	for i := 0; i < 3; i++ {
		_, _ = o.Enqueue(&Item{Recipient: "9111", Kind: KindText, Text: "x"})
	}
	sender := &fakeSender{results: []error{ErrPermanent}}
	o.Step(context.Background(), sender)

	s, err := o.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if s.Pending+s.Dead != 3 {
		t.Errorf("total = %d, want 3", s.Pending+s.Dead)
	}
	if s.Dead != 1 {
		t.Errorf("Dead = %d, want 1", s.Dead)
	}
}

// Smoke test for concurrent enqueues. BoltDB serializes writes; we just
// want to make sure ID generation under contention doesn't collide.
func TestEnqueue_ConcurrentNoIDCollisions(t *testing.T) {
	o := openTestOutbox(t, time.Now)

	const n = 200
	var wg sync.WaitGroup
	var failures atomic.Int32
	ids := make(chan string, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			it, err := o.Enqueue(&Item{Recipient: "9111", Kind: KindText, Text: "x"})
			if err != nil {
				failures.Add(1)
				return
			}
			ids <- it.ID
		}()
	}
	wg.Wait()
	close(ids)
	if failures.Load() > 0 {
		t.Fatalf("%d failures", failures.Load())
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("duplicate ID %s", id)
		}
		seen[id] = true
	}
}
