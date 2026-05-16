// Package httpapi mounts the loopback HTTP routes the Tally COM DLL calls.
// Handlers are deliberately thin: they validate input and enqueue to the
// outbox. Actual delivery happens asynchronously in the outbox worker so
// Tally never blocks waiting for WhatsApp.
package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tallywa/tallywa/internal/activation"
	"github.com/tallywa/tallywa/internal/license"
	"github.com/tallywa/tallywa/internal/outbox"
	"github.com/tallywa/tallywa/internal/whatsapp"
)

// State is everything the handlers need to do their job. Pointers are
// safe to share between goroutines (chi spawns one per request).
//
// License is atomic so the activation endpoint can swap it in without
// blocking the hot send path.
type State struct {
	Outbox            *outbox.Outbox
	Client            whatsapp.Client
	License           atomic.Pointer[license.License]
	Activator         *activation.Client
	LicensePath       string
	OnLicenseUpdated  func(*license.License) // optional callback after Store
	Logger            *slog.Logger
	Version           string
	Started           time.Time
}

// LoadLicense returns the current license, or nil if not activated.
func (s *State) LoadLicense() *license.License {
	return s.License.Load()
}

// StoreLicense replaces the current license atomically and fires the
// optional callback.
func (s *State) StoreLicense(l *license.License) {
	s.License.Store(l)
	if s.OnLicenseUpdated != nil {
		s.OnLicenseUpdated(l)
	}
}

// Routes returns the configured router. Caller wraps it with the loopback
// HMAC middleware before serving.
func Routes(s *State) http.Handler {
	r := chi.NewRouter()
	r.Get("/api/health", s.handleHealth)
	r.Get("/api/status", s.handleStatus)
	r.Post("/api/activate", s.handleActivate)

	// WhatsApp lifecycle (paint-the-tray endpoints). Available pre- and
	// post-activation so the user can pair WhatsApp before pasting their
	// license, or vice versa, in either order.
	r.Get("/api/whatsapp/qr", s.handleWhatsAppQR)
	r.Post("/api/whatsapp/logout", s.handleWhatsAppLogout)
	r.Post("/api/whatsapp/reconnect", s.handleWhatsAppReconnect)

	// Send endpoints require an activated license. The middleware lives
	// inline so the activation endpoint above can run unprotected.
	r.Group(func(r chi.Router) {
		r.Use(s.requireLicense)
		r.Post("/api/send-message", s.handleSendMessage)
		r.Post("/api/send-file", s.handleSendFile)
		r.Post("/api/send-file-with-message", s.handleSendFileWithMessage)
	})

	r.Get("/api/queue/stats", s.handleQueueStats)
	r.Get("/api/queue/list", s.handleQueueList)
	r.Get("/api/queue/item/{id}", s.handleQueueItem)
	r.Post("/api/queue/item/{id}/resend", s.handleQueueResend)
	return r
}

// requireLicense rejects send-path requests if no license has been loaded.
// 402 Payment Required is the right semantic; the tray UI maps it to the
// activation pane.
func (s *State) requireLicense(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.LoadLicense() == nil {
			writeError(w, http.StatusPaymentRequired, "service is not activated; paste the activation token in the tray app")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --------- DTOs ---------

// sendMessageRequest mirrors the existing Backend-Bridge/api/models.go
// schema so the in-the-field C# DLL does not need to change.
type sendMessageRequest struct {
	Recipient      string `json:"recipient"`
	Message        string `json:"message"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type sendFileRequest struct {
	Recipient      string `json:"recipient"`
	FilePath       string `json:"file_path"`
	Caption        string `json:"caption"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type sendFileWithMessageRequest struct {
	Recipient      string `json:"recipient"`
	FilePath       string `json:"file_path"`
	Message        string `json:"message"`
	VoucherType    string `json:"voucher_type"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type sendResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	MessageID string `json:"message_id,omitempty"`
	Status    string `json:"status,omitempty"`
}

type errorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// --------- handlers ---------

func (s *State) handleHealth(w http.ResponseWriter, _ *http.Request) {
	authenticated := false
	if s.Client != nil {
		authenticated = s.Client.Connected()
	}
	connected := authenticated // alias for the existing DLL contract
	licensed := s.LoadLicense() != nil

	writeJSON(w, http.StatusOK, map[string]any{
		"status":        statusString(connected),
		"connected":     connected,
		"authenticated": authenticated,
		"licensed":      licensed,
		"version":       s.Version,
		"uptime_sec":    int(time.Since(s.Started).Seconds()),
	})
}

func (s *State) handleStatus(w http.ResponseWriter, _ *http.Request) {
	lic := s.LoadLicense()
	resp := map[string]any{
		"connected":     s.Client != nil && s.Client.Connected(),
		"authenticated": s.Client != nil && s.Client.Connected(),
		"version":       s.Version,
		"uptime_sec":    int(time.Since(s.Started).Seconds()),
		"licensed":      lic != nil,
	}
	if s.Client != nil {
		resp["phone_number"] = s.Client.PhoneNumber()
	}
	// Lifecycle-aware extras for the tray. The client may not implement
	// the richer interface (StubClient in tests doesn't); fall back to
	// the boolean Connected() in that case.
	if lc, ok := s.Client.(whatsapp.LifecycleClient); ok && lc != nil {
		resp["state"] = lc.State()
		resp["has_qr"] = lc.QR() != ""
	} else if s.Client != nil {
		if s.Client.Connected() {
			resp["state"] = "connected"
		} else {
			resp["state"] = "disconnected"
		}
		resp["has_qr"] = false
	}
	if lic != nil {
		resp["edition"] = lic.Edition
		resp["features"] = lic.Features
		resp["customer_email"] = lic.CustomerEmail
	}
	if stats, err := s.Outbox.Stats(); err == nil {
		resp["queue"] = map[string]int{
			"pending": stats.Pending,
			"sending": stats.Sending,
			"sent":    stats.Sent,
			"dead":    stats.Dead,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// activateRequest is the body the tray UI POSTs when the user pastes a token.
type activateRequest struct {
	Token string `json:"token"`
}

type activateResponse struct {
	Success              bool   `json:"success"`
	Edition              string `json:"edition,omitempty"`
	IsReactivation       bool   `json:"is_reactivation,omitempty"`
	RedemptionsRemaining int    `json:"redemptions_remaining,omitempty"`
	Message              string `json:"message,omitempty"`
}

// handleActivate is the only HTTP-callable way to install a license. It
// proxies to the activation client, which contacts the issuer, verifies
// the response, and persists license.dat.
func (s *State) handleActivate(w http.ResponseWriter, r *http.Request) {
	if s.Activator == nil {
		writeError(w, http.StatusServiceUnavailable, "activation client not configured")
		return
	}
	var req activateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	// Use a per-request context with a generous deadline. The tray UI
	// shows a spinner during this and the issuer call dominates latency.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	res, err := s.Activator.Activate(ctx, req.Token, s.LicensePath)
	if err != nil {
		s.Logger.Warn("activate: failed", "err", err)
		// Map sentinels onto helpful HTTP statuses for the tray UI.
		switch {
		case errors.Is(err, activation.ErrTokenNotFound):
			writeError(w, http.StatusNotFound, "We couldn't find that activation key. Check for typos and try again.")
		case errors.Is(err, activation.ErrTokenExhausted):
			writeError(w, http.StatusConflict, "This key has already been used the maximum number of times. Email support to reset it.")
		case errors.Is(err, activation.ErrIssuerOffline):
			writeError(w, http.StatusBadGateway, "We can't reach our activation server. Check your internet connection and try again.")
		case errors.Is(err, activation.ErrTampered):
			writeError(w, http.StatusInternalServerError, "Activation response failed verification. Please email support.")
		default:
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("activation failed: %v", err))
		}
		return
	}

	s.StoreLicense(res.License)
	s.Logger.Info("activate: succeeded",
		"license_id", res.License.LicenseID,
		"edition", res.License.Edition,
		"reactivation", res.IsReactivation,
		"remaining", res.RedemptionsRemaining,
	)

	writeJSON(w, http.StatusOK, activateResponse{
		Success:              true,
		Edition:              res.License.Edition,
		IsReactivation:       res.IsReactivation,
		RedemptionsRemaining: res.RedemptionsRemaining,
		Message:              "Activation successful — you're ready to send.",
	})
}

func (s *State) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	var req sendMessageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRecipient(req.Recipient); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	item, err := s.Outbox.Enqueue(&outbox.Item{
		IdempotencyKey: req.IdempotencyKey,
		Recipient:      normalizeRecipient(req.Recipient),
		Kind:           outbox.KindText,
		Text:           req.Message,
	})
	if err != nil {
		s.Logger.Error("send-message: enqueue", "err", err)
		writeError(w, http.StatusInternalServerError, "could not queue message")
		return
	}
	writeJSON(w, http.StatusOK, sendResponse{
		Success:   true,
		Message:   "Message queued",
		MessageID: item.ID,
		Status:    string(item.Status),
	})
}

func (s *State) handleSendFile(w http.ResponseWriter, r *http.Request) {
	var req sendFileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRecipient(req.Recipient); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateFilePath(req.FilePath); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	item, err := s.Outbox.Enqueue(&outbox.Item{
		IdempotencyKey: defaultIdemKey(req.IdempotencyKey, req.Recipient, req.FilePath),
		Recipient:      normalizeRecipient(req.Recipient),
		Kind:           outbox.KindFile,
		FilePath:       req.FilePath,
		Text:           req.Caption,
	})
	if err != nil {
		s.Logger.Error("send-file: enqueue", "err", err)
		writeError(w, http.StatusInternalServerError, "could not queue file")
		return
	}
	writeJSON(w, http.StatusOK, sendResponse{
		Success:   true,
		Message:   "File queued",
		MessageID: item.ID,
		Status:    string(item.Status),
	})
}

func (s *State) handleSendFileWithMessage(w http.ResponseWriter, r *http.Request) {
	var req sendFileWithMessageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRecipient(req.Recipient); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateFilePath(req.FilePath); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	item, err := s.Outbox.Enqueue(&outbox.Item{
		IdempotencyKey: defaultIdemKey(req.IdempotencyKey, req.Recipient, req.FilePath),
		Recipient:      normalizeRecipient(req.Recipient),
		Kind:           outbox.KindFileWithText,
		FilePath:       req.FilePath,
		Text:           req.Message,
		Voucher:        req.VoucherType,
	})
	if err != nil {
		s.Logger.Error("send-file-with-message: enqueue", "err", err)
		writeError(w, http.StatusInternalServerError, "could not queue file+message")
		return
	}
	writeJSON(w, http.StatusOK, sendResponse{
		Success:   true,
		Message:   "File and message queued",
		MessageID: item.ID,
		Status:    string(item.Status),
	})
}

func (s *State) handleQueueStats(w http.ResponseWriter, _ *http.Request) {
	stats, err := s.Outbox.Stats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *State) handleQueueItem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	item, err := s.Outbox.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil {
		writeError(w, http.StatusNotFound, "item not found")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// handleQueueList returns the most recent activity. The dashboard polls
// this for the in-UI activity log; we cap at 200 to keep payloads small.
func (s *State) handleQueueList(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	items, err := s.Outbox.List(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	view := make([]map[string]any, 0, len(items))
	for _, it := range items {
		view = append(view, map[string]any{
			"id":              it.ID,
			"recipient":       it.Recipient,
			"voucher":         it.Voucher,
			"kind":            it.Kind,
			"status":          it.Status,
			"text":            it.Text,
			"file_path":       it.FilePath,
			"attempts":        it.Attempts,
			"max_attempts":    it.MaxAttempts,
			"created_at":      it.CreatedAt,
			"updated_at":      it.UpdatedAt,
			"next_attempt_at": it.NextAttemptAt,
			"last_error":      it.LastError,
		})
	}
	resp := map[string]any{"items": view}
	if next := s.Outbox.NextReceiptReadyAt(); !next.IsZero() {
		resp["next_receipt_ready_at"] = next
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleQueueResend bumps an existing item back to pending so the worker
// re-sends it. Used by the "Resend" button on the dashboard activity log.
// We deliberately bypass idempotency: the user is asking explicitly.
func (s *State) handleQueueResend(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	item, err := s.Outbox.Requeue(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil {
		writeError(w, http.StatusNotFound, "item not found or currently sending")
		return
	}
	writeJSON(w, http.StatusOK, sendResponse{
		Success:   true,
		Message:   "Requeued for resend",
		MessageID: item.ID,
		Status:    string(item.Status),
	})
}

// handleWhatsAppQR returns the current pairing QR rendered as a PNG
// data URL. The tray dashboard plugs this directly into <img src>.
// We return an empty string for `qr` when there's nothing to render so
// the dashboard's poller can detect the awaiting → connected transition
// without parsing a 204.
func (s *State) handleWhatsAppQR(w http.ResponseWriter, _ *http.Request) {
	lc, ok := s.Client.(whatsapp.LifecycleClient)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "lifecycle not supported by current client")
		return
	}
	state := lc.State()
	raw := lc.QR()
	resp := map[string]any{
		"qr":    "",
		"state": string(state),
	}
	if raw != "" {
		dataURL, err := encodeQRDataURL(raw)
		if err != nil {
			s.Logger.Warn("qr: encode failed", "err", err)
		} else {
			resp["qr"] = dataURL
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleWhatsAppLogout asks WhatsApp to forget this device. Used when
// the customer wants to switch to a different phone or hands their PC
// to someone else. We immediately re-arm a fresh QR channel so the
// dashboard surfaces the new QR without a service restart.
func (s *State) handleWhatsAppLogout(w http.ResponseWriter, r *http.Request) {
	lc, ok := s.Client.(whatsapp.LifecycleClient)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "lifecycle not supported by current client")
		return
	}
	if err := lc.Logout(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Best-effort re-arm. If reconnect fails (e.g. offline), the dashboard
	// "Reconnect" button gives the user a manual retry.
	if err := lc.Reconnect(r.Context()); err != nil {
		s.Logger.Warn("post-logout reconnect failed", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleWhatsAppReconnect re-arms the WhatsApp socket / QR channel.
// Surfaced via a dashboard button for users stuck in logged_out after a
// previous logout where the auto-reconnect didn't catch.
func (s *State) handleWhatsAppReconnect(w http.ResponseWriter, r *http.Request) {
	lc, ok := s.Client.(whatsapp.LifecycleClient)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "lifecycle not supported by current client")
		return
	}
	if err := lc.Reconnect(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// --------- helpers ---------

func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("empty body")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	return nil
}

// validateRecipient enforces the same 12-digit-with-country-code rule as
// the existing C# DLL (TallyInterface.cs IsNumeric + Length==12). We
// stay strict so behaviour is unchanged for in-the-field installs.
func validateRecipient(r string) error {
	r = strings.TrimSpace(r)
	if r == "" {
		return errors.New("recipient is required")
	}
	if len(r) != 12 {
		return fmt.Errorf("recipient %q must be 12 digits including country code", r)
	}
	for _, c := range r {
		if c < '0' || c > '9' {
			return fmt.Errorf("recipient %q must be digits only", r)
		}
	}
	return nil
}

func normalizeRecipient(r string) string {
	return strings.TrimSpace(r)
}

func validateFilePath(p string) error {
	if strings.TrimSpace(p) == "" {
		return errors.New("file_path is required")
	}
	// Reject obviously broken inputs early so the outbox doesn't carry
	// garbage. We do NOT stat the file here — the file might be on a
	// network drive that's slow, and statting blocks the request thread.
	// The worker stats it when it picks the item up.
	if !filepath.IsAbs(p) {
		return fmt.Errorf("file_path %q must be absolute", p)
	}
	return nil
}

// defaultIdemKey synthesises an idempotency key from recipient+file when
// the caller didn't provide one. Tally users frequently double-click the
// WhatsApp button on a voucher; without this, that double-click sends
// the PDF twice. Window-bucketed to 5 minutes so a deliberate resend
// after 10 minutes still goes through.
func defaultIdemKey(supplied, recipient, filePath string) string {
	if supplied != "" {
		return supplied
	}
	if recipient == "" || filePath == "" {
		return ""
	}
	bucket := time.Now().UTC().Truncate(5 * time.Minute).Unix()
	h := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%d", recipient, filePath, bucket))
	return hex.EncodeToString(h[:16])
}

func statusString(connected bool) string {
	if connected {
		return "connected"
	}
	return "disconnected"
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Success: false, Error: msg})
}
