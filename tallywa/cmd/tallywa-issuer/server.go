package main

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"

	"github.com/tallywa/tallywa/internal/license"
)

// Server is the HTTP-facing issuer. It signs license payloads with the
// issuer private key and returns them as base64 over JSON. The signing
// key only ever lives in this process's memory.
type Server struct {
	store          *Store
	signer         *license.Signer
	razorpaySecret []byte
	logger         *slog.Logger
}

// NewServer constructs the issuer. razorpaySecret may be empty during
// CLI-only usage (the webhook handler will return 503 in that case).
func NewServer(store *Store, priv ed25519.PrivateKey, razorpaySecret []byte, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		store:          store,
		signer:         &license.Signer{PrivateKey: priv},
		razorpaySecret: razorpaySecret,
		logger:         logger,
	}
}

// Routes returns an http.Handler with the public endpoints mounted.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", s.handleHealth)
	r.Post("/redeem", s.handleRedeem)
	r.Post("/webhooks/razorpay", s.handleRazorpayWebhook)
	return r
}

// --------- /healthz ---------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --------- /redeem ---------

type redeemRequest struct {
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint"`
}

type redeemResponse struct {
	License              string `json:"license"` // base64 of signed bytes
	LicenseID            string `json:"license_id"`
	Edition              string `json:"edition"`
	IsReactivation       bool   `json:"is_reactivation"`
	RedemptionsRemaining int    `json:"redemptions_remaining"`
}

func (s *Server) handleRedeem(w http.ResponseWriter, r *http.Request) {
	var req redeemRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	req.Token = strings.TrimSpace(strings.ToUpper(req.Token))
	req.Fingerprint = strings.TrimSpace(strings.ToLower(req.Fingerprint))
	if req.Token == "" || req.Fingerprint == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "token and fingerprint are required")
		return
	}

	tok, err := s.store.GetToken(r.Context(), req.Token)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "token_not_found", "no token matches that code")
		return
	}
	if err != nil {
		s.logger.Error("redeem: lookup", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}

	// LicenseID is generated up-front so the audit row records exactly
	// what we sign. ulid is sortable and trivially unique.
	licenseID := "lic_" + ulid.Make().String()
	res, err := s.store.Redeem(r.Context(), tok.Code, req.Fingerprint, licenseID)
	if errors.Is(err, ErrTokenExhausted) {
		writeError(w, http.StatusConflict, "token_exhausted", "all activations have been used")
		return
	}
	if err != nil {
		s.logger.Error("redeem: claim", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "claim failed")
		return
	}

	// On idempotent re-redeem we re-issue under the original LicenseID so
	// the customer's audit trail stays consistent.
	if res.ExistingRedemption != nil {
		licenseID = res.ExistingRedemption.LicenseID
	}

	lic := &license.License{
		LicenseID:        licenseID,
		CustomerEmail:    tok.CustomerEmail,
		Fingerprint:      req.Fingerprint,
		Edition:          tok.Edition,
		Features:         tok.Features,
		MaxReactivations: tok.MaxRedemptions,
		IssuedAt:         time.Now().UTC(),
	}
	signed, err := s.signer.Sign(lic)
	if err != nil {
		s.logger.Error("redeem: sign", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "sign failed")
		return
	}

	s.logger.Info("redeem: issued",
		"token", tok.Code,
		"license_id", licenseID,
		"edition", tok.Edition,
		"new_redemption", res.IsNewRedemption,
		"remaining", res.RedemptionsRemaining,
	)

	writeJSON(w, http.StatusOK, redeemResponse{
		License:              base64.StdEncoding.EncodeToString(signed),
		LicenseID:            licenseID,
		Edition:              tok.Edition,
		IsReactivation:       !res.IsNewRedemption,
		RedemptionsRemaining: res.RedemptionsRemaining,
	})
}

// --------- /webhooks/razorpay ---------

// razorpayEvent is the minimal envelope we care about. Razorpay sends a
// rich payload; we ignore everything except payment_id and notes.
type razorpayEvent struct {
	Event   string `json:"event"`
	Payload struct {
		Payment struct {
			Entity struct {
				ID    string            `json:"id"`
				Email string            `json:"email"`
				Notes map[string]string `json:"notes"`
			} `json:"entity"`
		} `json:"payment"`
	} `json:"payload"`
}

func (s *Server) handleRazorpayWebhook(w http.ResponseWriter, r *http.Request) {
	if len(s.razorpaySecret) == 0 {
		writeError(w, http.StatusServiceUnavailable, "webhook_disabled", "razorpay secret not configured")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if !VerifyRazorpaySignature(s.razorpaySecret, body, r.Header.Get("X-Razorpay-Signature")) {
		writeError(w, http.StatusUnauthorized, "bad_signature", "razorpay signature mismatch")
		return
	}
	var evt razorpayEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if evt.Event != "payment.captured" {
		// Acknowledge other events so Razorpay doesn't retry forever.
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "event": evt.Event})
		return
	}

	pid := evt.Payload.Payment.Entity.ID
	email := evt.Payload.Payment.Entity.Email
	plan := strings.ToLower(evt.Payload.Payment.Entity.Notes["plan"])
	if pid == "" || email == "" || plan == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "payment_id, email, plan are all required")
		return
	}

	// Idempotency: if we already minted a token for this payment, return
	// the existing one. Razorpay retries webhooks aggressively.
	if existing, err := s.store.FindByPaymentID(r.Context(), pid); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "duplicate",
			"token":  existing.Code,
		})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		s.logger.Error("webhook: find by payment", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}

	spec, ok := Plans[plan]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown_plan", fmt.Sprintf("plan %q is not configured", plan))
		return
	}

	tok, err := mintToken(r.Context(), s.store, mintArgs{
		Email:             email,
		Plan:              plan,
		Spec:              spec,
		RazorpayPaymentID: pid,
	})
	if err != nil {
		s.logger.Error("webhook: mint", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "mint failed")
		return
	}

	s.logger.Info("webhook: token minted",
		"payment_id", pid, "email", email, "plan", plan, "token", tok.Code)

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"token":  tok.Code,
	})
}

// --------- shared ---------

type mintArgs struct {
	Email             string
	Plan              string
	Spec              PlanSpec
	RazorpayPaymentID string
}

// mintToken creates a fresh token and persists it. Used by both the CLI
// and the webhook so there is one definition of "what a paid token looks
// like".
func mintToken(ctx context.Context, store *Store, a mintArgs) (*Token, error) {
	code, err := generateToken()
	if err != nil {
		return nil, err
	}
	tok := &Token{
		Code:              code,
		CustomerEmail:     a.Email,
		Plan:              a.Plan,
		Edition:           a.Spec.Edition,
		Features:          a.Spec.Features,
		MaxRedemptions:    a.Spec.MaxRedemptions,
		RazorpayPaymentID: a.RazorpayPaymentID,
		CreatedAt:         time.Now().UTC(),
	}
	if err := store.CreateToken(ctx, tok); err != nil {
		return nil, err
	}
	return tok, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error":   code,
		"message": msg,
	})
}
