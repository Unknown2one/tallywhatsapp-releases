package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// tokenAlphabet excludes 0/O/1/I to make hand-typing reliable.
const tokenAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// generateToken produces a TWA-XXXX-XXXX-XXXX code. ~60 bits of entropy
// in the random portion is plenty for an offline-signed-once issuer.
func generateToken() (string, error) {
	groups := make([]string, 3)
	for i := range groups {
		buf := make([]byte, 4)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		runes := make([]byte, 4)
		for j, b := range buf {
			runes[j] = tokenAlphabet[int(b)%len(tokenAlphabet)]
		}
		groups[i] = string(runes)
	}
	return "TWA-" + strings.Join(groups, "-"), nil
}

// Token is a row in the tokens table. The Redemptions slice is the audit
// trail of every fingerprint this token has signed for; in normal use
// it has exactly one entry.
type Token struct {
	Code              string
	CustomerEmail     string
	Plan              string
	Edition           string
	Features          []string
	MaxRedemptions    int
	RedemptionsUsed   int
	Redemptions       []Redemption
	RazorpayPaymentID string
	CreatedAt         time.Time
	LastRedeemedAt    *time.Time
}

// Redemption records one activation event.
type Redemption struct {
	Fingerprint string    `json:"fingerprint"`
	LicenseID   string    `json:"license_id"`
	RedeemedAt  time.Time `json:"redeemed_at"`
}

// Store wraps the issuer SQLite database.
type Store struct {
	db *sql.DB
}

// OpenStore opens or creates the SQLite file and runs the schema.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite single-writer; serialize at the driver.
	if _, err := db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA foreign_keys = ON;
		CREATE TABLE IF NOT EXISTS tokens (
			code                TEXT    PRIMARY KEY,
			customer_email      TEXT    NOT NULL,
			plan                TEXT    NOT NULL,
			edition             TEXT    NOT NULL,
			features            TEXT    NOT NULL,
			max_redemptions     INTEGER NOT NULL,
			redemptions_used    INTEGER NOT NULL DEFAULT 0,
			redemptions         TEXT    NOT NULL DEFAULT '[]',
			razorpay_payment_id TEXT    UNIQUE,
			created_at          DATETIME NOT NULL,
			last_redeemed_at    DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_tokens_email ON tokens(customer_email);
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// CreateToken persists a freshly minted token. Returns it on success.
func (s *Store) CreateToken(ctx context.Context, t *Token) error {
	if t.Code == "" {
		return errors.New("CreateToken: empty code")
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	featuresJSON, _ := json.Marshal(t.Features)
	redemptionsJSON, _ := json.Marshal(t.Redemptions)
	var paymentID interface{}
	if t.RazorpayPaymentID != "" {
		paymentID = t.RazorpayPaymentID
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tokens
		(code, customer_email, plan, edition, features, max_redemptions,
		 redemptions_used, redemptions, razorpay_payment_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Code, t.CustomerEmail, t.Plan, t.Edition, string(featuresJSON),
		t.MaxRedemptions, t.RedemptionsUsed, string(redemptionsJSON),
		paymentID, t.CreatedAt,
	)
	return err
}

// FindByPaymentID returns the token issued for a given Razorpay payment,
// or sql.ErrNoRows. Used for webhook idempotency.
func (s *Store) FindByPaymentID(ctx context.Context, paymentID string) (*Token, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT code FROM tokens WHERE razorpay_payment_id = ?`, paymentID)
	var code string
	if err := row.Scan(&code); err != nil {
		return nil, err
	}
	return s.GetToken(ctx, code)
}

// GetToken fetches a single token by code. Returns sql.ErrNoRows if missing.
func (s *Store) GetToken(ctx context.Context, code string) (*Token, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT code, customer_email, plan, edition, features, max_redemptions,
		       redemptions_used, redemptions, COALESCE(razorpay_payment_id, ''),
		       created_at, last_redeemed_at
		FROM tokens WHERE code = ?`, code)
	var t Token
	var featuresJSON, redemptionsJSON string
	var lastRedeemed sql.NullTime
	if err := row.Scan(
		&t.Code, &t.CustomerEmail, &t.Plan, &t.Edition, &featuresJSON,
		&t.MaxRedemptions, &t.RedemptionsUsed, &redemptionsJSON,
		&t.RazorpayPaymentID, &t.CreatedAt, &lastRedeemed,
	); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(featuresJSON), &t.Features)
	_ = json.Unmarshal([]byte(redemptionsJSON), &t.Redemptions)
	if lastRedeemed.Valid {
		v := lastRedeemed.Time
		t.LastRedeemedAt = &v
	}
	return &t, nil
}

// RedeemResult is what the Redeem handler returns. ExistingFingerprint is
// set only when the same fingerprint is redeeming again (idempotent path)
// — the caller re-signs the same license so the customer can recover from
// a lost license.dat without consuming a redemption.
type RedeemResult struct {
	Token                *Token
	IsNewRedemption      bool
	ExistingRedemption   *Redemption // non-nil if fingerprint already redeemed
	RedemptionsRemaining int
}

// Redeem atomically claims a redemption for the given fingerprint.
// The signing of the license itself happens outside the store — we do
// not want the signing key in the database layer.
func (s *Store) Redeem(ctx context.Context, code, fingerprint, licenseID string) (*RedeemResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	t, err := s.getTokenTx(ctx, tx, code)
	if err != nil {
		return nil, err
	}

	// Idempotent re-redeem: same fingerprint, same token. Recover for
	// customers who reformatted but kept the same hardware.
	for _, r := range t.Redemptions {
		if r.Fingerprint == fingerprint {
			rcopy := r
			return &RedeemResult{
				Token:                t,
				IsNewRedemption:      false,
				ExistingRedemption:   &rcopy,
				RedemptionsRemaining: t.MaxRedemptions - t.RedemptionsUsed,
			}, tx.Commit()
		}
	}

	if t.RedemptionsUsed >= t.MaxRedemptions {
		return nil, ErrTokenExhausted
	}

	now := time.Now().UTC()
	t.Redemptions = append(t.Redemptions, Redemption{
		Fingerprint: fingerprint,
		LicenseID:   licenseID,
		RedeemedAt:  now,
	})
	t.RedemptionsUsed++
	t.LastRedeemedAt = &now

	redemptionsJSON, _ := json.Marshal(t.Redemptions)
	if _, err := tx.ExecContext(ctx, `
		UPDATE tokens
		SET redemptions = ?, redemptions_used = ?, last_redeemed_at = ?
		WHERE code = ?`,
		string(redemptionsJSON), t.RedemptionsUsed, now, code,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &RedeemResult{
		Token:                t,
		IsNewRedemption:      true,
		RedemptionsRemaining: t.MaxRedemptions - t.RedemptionsUsed,
	}, nil
}

// ErrTokenExhausted is returned when all redemptions have been consumed.
var ErrTokenExhausted = errors.New("issuer: token has no redemptions remaining")

func (s *Store) getTokenTx(ctx context.Context, tx *sql.Tx, code string) (*Token, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT code, customer_email, plan, edition, features, max_redemptions,
		       redemptions_used, redemptions, COALESCE(razorpay_payment_id, ''),
		       created_at, last_redeemed_at
		FROM tokens WHERE code = ?`, code)
	var t Token
	var featuresJSON, redemptionsJSON string
	var lastRedeemed sql.NullTime
	if err := row.Scan(
		&t.Code, &t.CustomerEmail, &t.Plan, &t.Edition, &featuresJSON,
		&t.MaxRedemptions, &t.RedemptionsUsed, &redemptionsJSON,
		&t.RazorpayPaymentID, &t.CreatedAt, &lastRedeemed,
	); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(featuresJSON), &t.Features)
	_ = json.Unmarshal([]byte(redemptionsJSON), &t.Redemptions)
	if lastRedeemed.Valid {
		v := lastRedeemed.Time
		t.LastRedeemedAt = &v
	}
	return &t, nil
}
