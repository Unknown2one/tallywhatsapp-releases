package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tallywa/tallywa/internal/license"
)

func newTestServer(t *testing.T) (*Server, *Store, ed25519.PublicKey, []byte) {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "issuer.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	rzpSecret := []byte("razorpay-test-secret")
	srv := NewServer(store, priv, rzpSecret, slog.Default())
	return srv, store, pub, rzpSecret
}

func mintForTest(t *testing.T, store *Store, plan string) *Token {
	t.Helper()
	spec := Plans[plan]
	tok, err := mintToken(context.Background(), store, mintArgs{
		Email: "test@example.com",
		Plan:  plan,
		Spec:  spec,
	})
	if err != nil {
		t.Fatalf("mintToken: %v", err)
	}
	return tok
}

func TestRedeem_HappyPath(t *testing.T) {
	srv, store, pub, _ := newTestServer(t)
	tok := mintForTest(t, store, "pro")

	rr := callRedeem(t, srv, tok.Code, "fingerprint-aaa")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp redeemResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.IsReactivation {
		t.Errorf("first redeem should not be a reactivation")
	}
	if resp.Edition != "pro" {
		t.Errorf("Edition = %q, want pro", resp.Edition)
	}
	if resp.RedemptionsRemaining != Plans["pro"].MaxRedemptions-1 {
		t.Errorf("RedemptionsRemaining = %d", resp.RedemptionsRemaining)
	}

	// Verify the returned bytes really are a valid signed license.
	signed, err := base64.StdEncoding.DecodeString(resp.License)
	if err != nil {
		t.Fatalf("decode license: %v", err)
	}
	verifier := &license.Verifier{PublicKey: pub}
	lic, err := verifier.Verify(signed, "fingerprint-aaa")
	if err != nil {
		t.Fatalf("verify license: %v", err)
	}
	if lic.Edition != "pro" {
		t.Errorf("license Edition = %q, want pro", lic.Edition)
	}
	if !lic.HasFeature("ledger") {
		t.Errorf("pro plan should include ledger feature")
	}
}

func TestRedeem_SameFingerprintIsIdempotentAndDoesNotConsume(t *testing.T) {
	srv, store, _, _ := newTestServer(t)
	tok := mintForTest(t, store, "lite")

	r1 := callRedeem(t, srv, tok.Code, "fingerprint-bbb")
	r2 := callRedeem(t, srv, tok.Code, "fingerprint-bbb")

	if r1.Code != http.StatusOK || r2.Code != http.StatusOK {
		t.Fatalf("statuses %d %d", r1.Code, r2.Code)
	}
	var resp1, resp2 redeemResponse
	_ = json.Unmarshal(r1.Body.Bytes(), &resp1)
	_ = json.Unmarshal(r2.Body.Bytes(), &resp2)

	if resp2.IsReactivation == false {
		t.Errorf("second redeem with same fingerprint should be flagged as reactivation/idempotent")
	}
	if resp1.RedemptionsRemaining != resp2.RedemptionsRemaining {
		t.Errorf("redemptions consumed on idempotent re-redeem: %d → %d",
			resp1.RedemptionsRemaining, resp2.RedemptionsRemaining)
	}
	if resp1.LicenseID != resp2.LicenseID {
		t.Errorf("LicenseID changed across idempotent redeems: %s vs %s",
			resp1.LicenseID, resp2.LicenseID)
	}
}

func TestRedeem_DifferentFingerprintsConsume(t *testing.T) {
	srv, store, _, _ := newTestServer(t)
	tok := mintForTest(t, store, "lite") // 3 redemptions

	for i, fp := range []string{"fp-1", "fp-2", "fp-3"} {
		rr := callRedeem(t, srv, tok.Code, fp)
		if rr.Code != http.StatusOK {
			t.Fatalf("redeem %d: status %d body=%s", i, rr.Code, rr.Body.String())
		}
	}
	rr := callRedeem(t, srv, tok.Code, "fp-4")
	if rr.Code != http.StatusConflict {
		t.Errorf("4th distinct redeem status = %d, want 409", rr.Code)
	}
}

func TestRedeem_UnknownToken(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	rr := callRedeem(t, srv, "TWA-XXXX-XXXX-XXXX", "fp")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestRedeem_MissingFields(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	rr := callRedeem(t, srv, "", "")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestRazorpayWebhook_HappyPath(t *testing.T) {
	srv, _, _, secret := newTestServer(t)

	body := []byte(`{
		"event": "payment.captured",
		"payload": {"payment": {"entity": {
			"id": "pay_TESTABC",
			"email": "buyer@example.com",
			"notes": {"plan": "pro"}
		}}}
	}`)
	rr := callWebhook(t, srv, secret, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
	if !strings.HasPrefix(resp["token"].(string), "TWA-") {
		t.Errorf("token = %v, want TWA-… prefix", resp["token"])
	}
}

func TestRazorpayWebhook_DuplicateIsIdempotent(t *testing.T) {
	srv, _, _, secret := newTestServer(t)
	body := []byte(`{
		"event": "payment.captured",
		"payload": {"payment": {"entity": {
			"id": "pay_DUP",
			"email": "buyer@example.com",
			"notes": {"plan": "pro"}
		}}}
	}`)

	r1 := callWebhook(t, srv, secret, body)
	r2 := callWebhook(t, srv, secret, body)
	if r1.Code != http.StatusOK || r2.Code != http.StatusOK {
		t.Fatalf("statuses %d %d", r1.Code, r2.Code)
	}
	var resp1, resp2 map[string]any
	_ = json.Unmarshal(r1.Body.Bytes(), &resp1)
	_ = json.Unmarshal(r2.Body.Bytes(), &resp2)
	if resp1["token"] != resp2["token"] {
		t.Errorf("duplicate webhook minted a new token: %v vs %v", resp1["token"], resp2["token"])
	}
	if resp2["status"] != "duplicate" {
		t.Errorf("second response status = %v, want duplicate", resp2["status"])
	}
}

func TestRazorpayWebhook_BadSignature(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	body := []byte(`{"event":"payment.captured"}`)

	req := httptest.NewRequest("POST", "/webhooks/razorpay", bytes.NewReader(body))
	req.Header.Set("X-Razorpay-Signature", "deadbeef")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestRazorpayWebhook_NonCapturedEventIgnored(t *testing.T) {
	srv, _, _, secret := newTestServer(t)
	body := []byte(`{"event":"order.paid"}`)
	rr := callWebhook(t, srv, secret, body)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (ignored)", rr.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["status"] != "ignored" {
		t.Errorf("status = %v, want ignored", resp["status"])
	}
}

// --------- helpers ---------

func callRedeem(t *testing.T, srv *Server, token, fp string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(redeemRequest{Token: token, Fingerprint: fp})
	req := httptest.NewRequest("POST", "/redeem", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	return rr
}

func callWebhook(t *testing.T, srv *Server, secret, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/webhooks/razorpay", bytes.NewReader(body))
	req.Header.Set("X-Razorpay-Signature", sig)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	return rr
}

// Make `io` and `fmt` used even if other tests change.
var _ = io.Discard
var _ = fmt.Sprintf
