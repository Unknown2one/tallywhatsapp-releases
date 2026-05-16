package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tallywa/tallywa/internal/activation"
	"github.com/tallywa/tallywa/internal/license"
	"github.com/tallywa/tallywa/internal/outbox"
	"github.com/tallywa/tallywa/internal/whatsapp"
)

func newTestState(t *testing.T) *State {
	t.Helper()
	o, err := outbox.Open(outbox.Options{Path: filepath.Join(t.TempDir(), "ob.db")})
	if err != nil {
		t.Fatalf("outbox.Open: %v", err)
	}
	t.Cleanup(func() { _ = o.Close() })
	s := &State{
		Outbox:  o,
		Client:  whatsapp.NewStubClient("919999999999"),
		Logger:  slog.Default(),
		Version: "test-1.0",
		Started: time.Now(),
	}
	s.StoreLicense(&license.License{
		LicenseID: "lic_test", Edition: license.EditionPro,
		Features: []string{license.FeatureSales, license.FeatureReceipt, license.FeatureLedger},
	})
	return s
}

func do(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		buf = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, buf)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestSendMessage_QueuesAndReturns200(t *testing.T) {
	s := newTestState(t)
	rr := do(t, Routes(s), "POST", "/api/send-message", sendMessageRequest{
		Recipient: "919999999999",
		Message:   "hi",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp sendResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if !resp.Success || resp.MessageID == "" {
		t.Errorf("resp = %+v", resp)
	}

	stats, _ := s.Outbox.Stats()
	if stats.Pending != 1 {
		t.Errorf("pending = %d, want 1", stats.Pending)
	}
}

func TestSendMessage_RejectsBadRecipient(t *testing.T) {
	s := newTestState(t)
	cases := []struct {
		name string
		body sendMessageRequest
	}{
		{"empty", sendMessageRequest{Recipient: "", Message: "hi"}},
		{"too short", sendMessageRequest{Recipient: "9111", Message: "hi"}},
		{"non-digit", sendMessageRequest{Recipient: "91999999999X", Message: "hi"}},
		{"missing message", sendMessageRequest{Recipient: "919999999999", Message: ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := do(t, Routes(s), "POST", "/api/send-message", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestSendFileWithMessage_DefaultIdempotencyDedupesDoubleClicks(t *testing.T) {
	s := newTestState(t)
	body := sendFileWithMessageRequest{
		Recipient:   "919999999999",
		FilePath:    `C:\TallyWhatsapp\Sales_INV-1.pdf`,
		Message:     "Invoice",
		VoucherType: "sale",
	}
	r1 := do(t, Routes(s), "POST", "/api/send-file-with-message", body)
	r2 := do(t, Routes(s), "POST", "/api/send-file-with-message", body)
	if r1.Code != http.StatusOK || r2.Code != http.StatusOK {
		t.Fatalf("statuses %d %d", r1.Code, r2.Code)
	}
	var resp1, resp2 sendResponse
	_ = json.Unmarshal(r1.Body.Bytes(), &resp1)
	_ = json.Unmarshal(r2.Body.Bytes(), &resp2)

	if resp1.MessageID != resp2.MessageID {
		t.Errorf("double-click produced different IDs: %s vs %s",
			resp1.MessageID, resp2.MessageID)
	}
	stats, _ := s.Outbox.Stats()
	if stats.Pending != 1 {
		t.Errorf("pending = %d, want 1 after dedup", stats.Pending)
	}
}

func TestSendFileWithMessage_ExplicitIdempotencyKeyTrumpsDefault(t *testing.T) {
	s := newTestState(t)
	body1 := sendFileWithMessageRequest{
		Recipient:      "919999999999",
		FilePath:       `C:\TallyWhatsapp\Sales_INV-1.pdf`,
		Message:        "first",
		IdempotencyKey: "voucher-42",
	}
	body2 := body1
	body2.Message = "second"
	body2.IdempotencyKey = "voucher-43"

	r1 := do(t, Routes(s), "POST", "/api/send-file-with-message", body1)
	r2 := do(t, Routes(s), "POST", "/api/send-file-with-message", body2)
	if r1.Code != http.StatusOK || r2.Code != http.StatusOK {
		t.Fatalf("statuses %d %d", r1.Code, r2.Code)
	}
	var resp1, resp2 sendResponse
	_ = json.Unmarshal(r1.Body.Bytes(), &resp1)
	_ = json.Unmarshal(r2.Body.Bytes(), &resp2)
	if resp1.MessageID == resp2.MessageID {
		t.Errorf("different idempotency keys should yield different IDs")
	}
}

func TestSendFile_RejectsRelativePath(t *testing.T) {
	s := newTestState(t)
	rr := do(t, Routes(s), "POST", "/api/send-file", sendFileRequest{
		Recipient: "919999999999",
		FilePath:  "relative/path.pdf",
	})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestHealth_ReportsConnectedAndLicensed(t *testing.T) {
	s := newTestState(t)
	rr := do(t, Routes(s), "GET", "/api/health", nil)
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["connected"] != true {
		t.Errorf("connected = %v, want true", resp["connected"])
	}
	if resp["licensed"] != true {
		t.Errorf("licensed = %v, want true", resp["licensed"])
	}
}

func TestStatus_IncludesQueueStats(t *testing.T) {
	s := newTestState(t)
	_ = do(t, Routes(s), "POST", "/api/send-message", sendMessageRequest{
		Recipient: "919999999999",
		Message:   "x",
	})
	rr := do(t, Routes(s), "GET", "/api/status", nil)
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	q, ok := resp["queue"].(map[string]any)
	if !ok {
		t.Fatalf("queue field missing or wrong type: %v", resp)
	}
	if q["pending"].(float64) != 1 {
		t.Errorf("pending = %v, want 1", q["pending"])
	}
}

func TestSendMessage_OutboxFailureReturns500(t *testing.T) {
	// Trigger the enqueue error path by closing the outbox before sending.
	s := newTestState(t)
	_ = s.Outbox.Close()
	rr := do(t, Routes(s), "POST", "/api/send-message", sendMessageRequest{
		Recipient: "919999999999",
		Message:   "x",
	})
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

// Smoke test that the Sender adapter works end-to-end against the stub.
func TestEndToEnd_QueueDrains(t *testing.T) {
	s := newTestState(t)
	stub := s.Client.(*whatsapp.StubClient)
	sender := &whatsapp.Sender{Client: stub, Logger: slog.Default()}

	for i := 0; i < 3; i++ {
		_ = do(t, Routes(s), "POST", "/api/send-message", sendMessageRequest{
			Recipient: "919999999999",
			Message:   "x",
		})
	}
	for i := 0; i < 5; i++ {
		s.Outbox.Step(t.Context(), sender)
	}
	if got := len(stub.Calls()); got < 1 {
		t.Errorf("stub recorded %d calls, want ≥1", got)
	}
}

// guard against a regression where decodeJSON accepted unknown fields.
func TestSendMessage_UnknownFieldsRejected(t *testing.T) {
	s := newTestState(t)
	req := httptest.NewRequest("POST", "/api/send-message",
		bytes.NewReader([]byte(`{"recipient":"919999999999","message":"x","mystery":1}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	Routes(s).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// Ensures that Send under permanent error from the WhatsApp client
// surfaces correctly to the outbox.
func TestSenderSurfacesPermanent(t *testing.T) {
	stub := whatsapp.NewStubClient("919999999999")
	stub.SetSendError(errors.New("malformed recipient"))

	o, err := outbox.Open(outbox.Options{Path: filepath.Join(t.TempDir(), "ob.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer o.Close()

	_, _ = o.Enqueue(&outbox.Item{
		Recipient:   "919999999999",
		Kind:        outbox.KindText,
		Text:        "hi",
		MaxAttempts: 1,
	})
	sender := &whatsapp.Sender{Client: stub, Logger: slog.Default()}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		o.Step(t.Context(), sender)
	}()
	wg.Wait()

	stats, _ := o.Stats()
	if stats.Pending+stats.Dead == 0 {
		t.Errorf("expected item to be retried or dead, got %+v", stats)
	}
}

// fakeIssuer mounts a tiny issuer-shaped HTTP server for activation tests.
func newFakeIssuer(t *testing.T, pub ed25519.PublicKey, priv ed25519.PrivateKey, status int, signWith ed25519.PrivateKey) *httptest.Server {
	t.Helper()
	signKey := priv
	if signWith != nil {
		signKey = signWith
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 0 && status != http.StatusOK {
			http.Error(w, "no", status)
			return
		}
		var req struct {
			Token       string `json:"token"`
			Fingerprint string `json:"fingerprint"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		lic := &license.License{
			LicenseID:        "lic_test",
			Fingerprint:      req.Fingerprint,
			Edition:          license.EditionPro,
			Features:         []string{license.FeatureSales, license.FeatureLedger},
			MaxReactivations: 3,
			IssuedAt:         time.Now(),
		}
		signed, _ := (&license.Signer{PrivateKey: signKey}).Sign(lic)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"license":               base64.StdEncoding.EncodeToString(signed),
			"license_id":            lic.LicenseID,
			"edition":               lic.Edition,
			"is_reactivation":       false,
			"redemptions_remaining": 2,
		})
	}))
	t.Cleanup(srv.Close)
	_ = pub // signature kept symmetric for callers
	return srv
}

func TestActivate_HappyPathStoresLicenseAndUnlocksSend(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	o, err := outbox.Open(outbox.Options{Path: filepath.Join(t.TempDir(), "ob.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = o.Close() })

	issuer := newFakeIssuer(t, pub, priv, 0, nil)
	licPath := filepath.Join(t.TempDir(), "license.dat")
	client := activation.New(issuer.URL, pub)
	client.Fingerprint = func() (string, error) { return "fp-aaa", nil }

	var notified atomic.Int32
	state := &State{
		Outbox:           o,
		Client:           whatsapp.NewStubClient("919999999999"),
		Activator:        client,
		LicensePath:      licPath,
		Logger:           slog.Default(),
		Version:          "test",
		Started:          time.Now(),
		OnLicenseUpdated: func(*license.License) { notified.Add(1) },
	}
	router := Routes(state)

	// Pre-condition: send is gated.
	rrPre := do(t, router, "POST", "/api/send-message", sendMessageRequest{
		Recipient: "919999999999",
		Message:   "hi",
	})
	if rrPre.Code != http.StatusPaymentRequired {
		t.Fatalf("pre-activation send status = %d, want 402", rrPre.Code)
	}

	// Activate.
	rrAct := do(t, router, "POST", "/api/activate", activateRequest{Token: "TWA-AAAA-BBBB-CCCC"})
	if rrAct.Code != http.StatusOK {
		t.Fatalf("activate status = %d body=%s", rrAct.Code, rrAct.Body.String())
	}
	if notified.Load() != 1 {
		t.Errorf("OnLicenseUpdated called %d times, want 1", notified.Load())
	}

	// Post-condition: license is loaded and send is unblocked.
	if state.LoadLicense() == nil {
		t.Fatalf("LoadLicense returned nil after activation")
	}
	rrPost := do(t, router, "POST", "/api/send-message", sendMessageRequest{
		Recipient: "919999999999",
		Message:   "hi",
	})
	if rrPost.Code != http.StatusOK {
		t.Errorf("post-activation send status = %d, want 200; body=%s", rrPost.Code, rrPost.Body.String())
	}

	// And the license file is on disk for next service start.
	if _, err := license.Read(licPath); err != nil {
		t.Errorf("license.Read after activate: %v", err)
	}
}

func TestActivate_TokenNotFound_Returns404(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	issuer := newFakeIssuer(t, pub, priv, http.StatusNotFound, nil)

	o, _ := outbox.Open(outbox.Options{Path: filepath.Join(t.TempDir(), "ob.db")})
	t.Cleanup(func() { _ = o.Close() })

	client := activation.New(issuer.URL, pub)
	client.Fingerprint = func() (string, error) { return "fp", nil }

	state := &State{
		Outbox:      o,
		Activator:   client,
		LicensePath: filepath.Join(t.TempDir(), "license.dat"),
		Logger:      slog.Default(),
		Started:     time.Now(),
	}
	rr := do(t, Routes(state), "POST", "/api/activate", activateRequest{Token: "TWA-XXXX"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestActivate_TamperedSignature_Returns500AndKeepsLicenseUnset(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, badPriv, _ := ed25519.GenerateKey(rand.Reader)
	issuer := newFakeIssuer(t, pub, priv, 0, badPriv) // signs with wrong key

	o, _ := outbox.Open(outbox.Options{Path: filepath.Join(t.TempDir(), "ob.db")})
	t.Cleanup(func() { _ = o.Close() })

	client := activation.New(issuer.URL, pub)
	client.Fingerprint = func() (string, error) { return "fp", nil }

	state := &State{
		Outbox:      o,
		Activator:   client,
		LicensePath: filepath.Join(t.TempDir(), "license.dat"),
		Logger:      slog.Default(),
		Started:     time.Now(),
	}
	rr := do(t, Routes(state), "POST", "/api/activate", activateRequest{Token: "TWA-XXXX"})
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if state.LoadLicense() != nil {
		t.Errorf("license should not be set when verification fails")
	}
}

// suppress unused-import warning
var _ = io.Discard

func TestWhatsAppQR_ReturnsPendingCode(t *testing.T) {
	s := newTestState(t)
	stub := s.Client.(*whatsapp.StubClient)
	stub.SetState(whatsapp.StateAwaitingQR)
	stub.SetQR("2@whatsapp/qr/code/abc123")

	rr := do(t, Routes(s), "GET", "/api/whatsapp/qr", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	qr, _ := resp["qr"].(string)
	// The handler renders the raw whatsmeow string into a PNG data URL
	// so the dashboard can plug it straight into <img src>. We just
	// assert the contract here, not the pixel content.
	if !strings.HasPrefix(qr, "data:image/png;base64,") {
		t.Errorf("qr = %q, want data:image/png;base64,...", qr)
	}
	if resp["state"] != "awaiting_qr" {
		t.Errorf("state = %v, want awaiting_qr", resp["state"])
	}
}

func TestWhatsAppQR_NoPendingReturnsCurrentState(t *testing.T) {
	s := newTestState(t)
	stub := s.Client.(*whatsapp.StubClient)
	stub.SetState(whatsapp.StateConnected)
	stub.SetQR("")

	rr := do(t, Routes(s), "GET", "/api/whatsapp/qr", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["qr"] != "" {
		t.Errorf("qr should be empty when connected, got %v", resp["qr"])
	}
	if resp["state"] != "connected" {
		t.Errorf("state = %v, want connected", resp["state"])
	}
}

func TestWhatsAppLogout_DisconnectsAndUnsetsPhone(t *testing.T) {
	s := newTestState(t)
	stub := s.Client.(*whatsapp.StubClient)
	if !stub.Connected() {
		t.Fatalf("precondition: stub should start connected")
	}
	rr := do(t, Routes(s), "POST", "/api/whatsapp/logout", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if stub.Connected() {
		t.Errorf("stub should be disconnected after logout")
	}
	if stub.State() != whatsapp.StateLoggedOut {
		t.Errorf("state = %v, want logged_out", stub.State())
	}
}

// Ensure the lifecycle endpoints fail gracefully when the bound client
// is a plain Client without lifecycle methods. Defensive — in the field,
// the meow client always implements LifecycleClient.
type plainClient struct{ *whatsapp.StubClient }

// plainClient hides the lifecycle methods by NOT embedding StubClient
// in a way that re-exports them. We can't easily remove methods via Go
// embedding, so we wrap with a shim that only implements Client.
type sendOnlyClient struct{ inner *whatsapp.StubClient }

func (c *sendOnlyClient) Connected() bool        { return c.inner.Connected() }
func (c *sendOnlyClient) PhoneNumber() string    { return c.inner.PhoneNumber() }
func (c *sendOnlyClient) SendText(ctx context.Context, recipient, message string) error {
	return c.inner.SendText(ctx, recipient, message)
}
func (c *sendOnlyClient) SendFile(ctx context.Context, recipient, filePath, caption string) error {
	return c.inner.SendFile(ctx, recipient, filePath, caption)
}

func TestWhatsAppEndpoints_ReturnUnavailableForNonLifecycleClient(t *testing.T) {
	s := newTestState(t)
	s.Client = &sendOnlyClient{inner: whatsapp.NewStubClient("9111")}

	rr := do(t, Routes(s), "GET", "/api/whatsapp/qr", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("qr status = %d, want 503", rr.Code)
	}
	rr = do(t, Routes(s), "POST", "/api/whatsapp/logout", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("logout status = %d, want 503", rr.Code)
	}
}
