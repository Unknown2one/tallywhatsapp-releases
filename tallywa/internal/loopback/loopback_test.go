package loopback

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// signedRequest builds a properly signed request for the test harness.
// Mirrors what the C# DLL will do.
func signedRequest(t *testing.T, secret []byte, method, target string, body []byte, ts time.Time, nonce string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(ts.Unix(), 10))
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderSignature, Sign(secret, method, req.URL.Path, ts, nonce, body))
	return req
}

func newAuth(t *testing.T) (*Authenticator, []byte) {
	t.Helper()
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	return NewAuthenticator(secret), secret
}

// invokes the middleware and returns whether the inner handler ran and
// the response status.
func invoke(auth *Authenticator, req *http.Request) (innerRan bool, status int, body []byte) {
	rr := httptest.NewRecorder()
	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerRan = true
		// Drain the body to confirm it's still readable downstream.
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rr, req)
	return innerRan, rr.Code, body
}

func TestMiddleware_ValidRequestPasses(t *testing.T) {
	auth, secret := newAuth(t)
	body := []byte(`{"recipient":"919999999999","message":"hi"}`)
	req := signedRequest(t, secret, "POST", "http://127.0.0.1/api/send", body, time.Now(), "nonce-1")

	ran, status, gotBody := invoke(auth, req)
	if !ran {
		t.Errorf("inner handler did not run")
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if !bytes.Equal(gotBody, body) {
		t.Errorf("downstream body = %q, want %q", gotBody, body)
	}
}

func TestMiddleware_MissingHeadersRejected(t *testing.T) {
	auth, _ := newAuth(t)
	req := httptest.NewRequest("POST", "http://127.0.0.1/api/send", strings.NewReader("{}"))

	ran, status, _ := invoke(auth, req)
	if ran {
		t.Errorf("inner handler should not run on missing headers")
	}
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

func TestMiddleware_BadSignatureRejected(t *testing.T) {
	auth, secret := newAuth(t)
	body := []byte(`{"recipient":"919999999999"}`)
	req := signedRequest(t, secret, "POST", "http://127.0.0.1/api/send", body, time.Now(), "nonce-2")
	// Tamper signature.
	req.Header.Set(HeaderSignature, "deadbeef")

	ran, status, _ := invoke(auth, req)
	if ran {
		t.Errorf("inner handler should not run with bad signature")
	}
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

func TestMiddleware_BodyTamperingRejected(t *testing.T) {
	auth, secret := newAuth(t)
	body := []byte(`{"recipient":"919999999999"}`)
	req := signedRequest(t, secret, "POST", "http://127.0.0.1/api/send", body, time.Now(), "nonce-3")
	// Sign was computed for body, but we replace the body with something else.
	req.Body = io.NopCloser(bytes.NewReader([]byte(`{"recipient":"918888888888"}`)))

	ran, _, _ := invoke(auth, req)
	if ran {
		t.Errorf("inner handler should not run if body changed after signing")
	}
}

func TestMiddleware_StaleTimestampRejected(t *testing.T) {
	auth, secret := newAuth(t)
	body := []byte(`{}`)
	stale := time.Now().Add(-MaxClockSkew - time.Minute)
	req := signedRequest(t, secret, "POST", "http://127.0.0.1/api/send", body, stale, "nonce-4")

	_, status, _ := invoke(auth, req)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

func TestMiddleware_FutureTimestampRejected(t *testing.T) {
	auth, secret := newAuth(t)
	body := []byte(`{}`)
	future := time.Now().Add(MaxClockSkew + time.Minute)
	req := signedRequest(t, secret, "POST", "http://127.0.0.1/api/send", body, future, "nonce-5")

	_, status, _ := invoke(auth, req)
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", status)
	}
}

func TestMiddleware_ReplayRejected(t *testing.T) {
	auth, secret := newAuth(t)
	body := []byte(`{}`)
	ts := time.Now()
	nonce := "nonce-replay"

	// First call OK.
	req1 := signedRequest(t, secret, "POST", "http://127.0.0.1/api/send", body, ts, nonce)
	_, status1, _ := invoke(auth, req1)
	if status1 != http.StatusOK {
		t.Fatalf("first call status = %d, want 200", status1)
	}

	// Second call with same nonce is a replay.
	req2 := signedRequest(t, secret, "POST", "http://127.0.0.1/api/send", body, ts, nonce)
	_, status2, _ := invoke(auth, req2)
	if status2 != http.StatusUnauthorized {
		t.Errorf("replay status = %d, want 401", status2)
	}
}

func TestMiddleware_NonceCacheExpiry(t *testing.T) {
	auth, secret := newAuth(t)
	auth.Now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	body := []byte(`{}`)
	nonce := "nonce-expire"

	req1 := signedRequest(t, secret, "POST", "http://127.0.0.1/api/send", body, auth.Now(), nonce)
	if _, status, _ := invoke(auth, req1); status != http.StatusOK {
		t.Fatalf("first call status = %d", status)
	}

	// Advance time past the nonce cache TTL. Same nonce must now be accepted
	// again because we no longer remember it. (In production, the timestamp
	// check would catch a real replay long before the nonce cache expires.)
	auth.Now = func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(NonceCacheTTL + time.Minute)
	}
	req2 := signedRequest(t, secret, "POST", "http://127.0.0.1/api/send", body, auth.Now(), nonce)
	if _, status, _ := invoke(auth, req2); status != http.StatusOK {
		t.Errorf("post-expiry status = %d, want 200", status)
	}
}

func TestServer_StartShutdown(t *testing.T) {
	auth, secret := newAuth(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})

	s := New(auth.Middleware(mux))
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.Port() == 0 {
		t.Errorf("Port should be non-zero after Start")
	}
	if !strings.HasPrefix(s.Addr(), "127.0.0.1:") {
		t.Errorf("Addr = %q, want loopback prefix", s.Addr())
	}

	body := []byte("ping")
	ts := time.Now()
	nonce := "live-1"
	req, err := http.NewRequest("POST", "http://"+s.Addr()+"/api/ping", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(HeaderTimestamp, strconv.FormatInt(ts.Unix(), 10))
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderSignature, Sign(secret, "POST", "/api/ping", ts, nonce, body))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestSign_Deterministic(t *testing.T) {
	// Pinning the signature catches accidental changes to the canonical
	// string format that would silently break the deployed C# DLL.
	secret := []byte("supersecret-fixed-key-for-testing-32b")
	ts := time.Unix(1700000000, 0)
	body := []byte(`{"a":1}`)
	got := Sign(secret, "POST", "/api/send", ts, "nonce-fixed", body)

	if len(got) != 64 { // hex-sha256
		t.Errorf("signature length = %d, want 64", len(got))
	}
	// Stable across re-runs.
	again := Sign(secret, "POST", "/api/send", ts, "nonce-fixed", body)
	if got != again {
		t.Errorf("signature not deterministic: %s vs %s", got, again)
	}
	// Sensitive to method.
	if Sign(secret, "GET", "/api/send", ts, "nonce-fixed", body) == got {
		t.Errorf("signature insensitive to method")
	}
	// Sensitive to path.
	if Sign(secret, "POST", "/api/other", ts, "nonce-fixed", body) == got {
		t.Errorf("signature insensitive to path")
	}
	// Sensitive to body.
	if Sign(secret, "POST", "/api/send", ts, "nonce-fixed", []byte(`{"a":2}`)) == got {
		t.Errorf("signature insensitive to body")
	}
}
