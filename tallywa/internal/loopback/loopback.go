// Package loopback implements the localhost-only HTTP server that the
// Tally COM DLL talks to. Two security properties matter:
//
//  1. Bind only to 127.0.0.1 so no external host can reach the API even
//     if a firewall rule is misconfigured.
//  2. Require HMAC-signed requests with timestamp + nonce so other
//     processes on the same machine cannot send WhatsApp messages from
//     the user's account.
//
// The HMAC scheme:
//
//	StringToSign = METHOD + "\n" + PATH + "\n" + TIMESTAMP + "\n" +
//	               NONCE  + "\n" + hex(sha256(body))
//	Signature    = hex(hmac_sha256(secret, StringToSign))
//
// Headers carry timestamp/nonce/signature. Timestamps must be within
// MaxClockSkew of server time. Nonces are remembered for NonceCacheTTL
// to defeat replay.
package loopback

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	HeaderTimestamp = "X-TallyWA-Timestamp"
	HeaderNonce     = "X-TallyWA-Nonce"
	HeaderSignature = "X-TallyWA-Signature"

	// MaxClockSkew bounds the difference between client and server time.
	// 5 minutes is generous enough for misconfigured machines yet tight
	// enough to make replay attacks expensive.
	MaxClockSkew = 5 * time.Minute

	// NonceCacheTTL is how long a nonce is remembered. Must be ≥
	// MaxClockSkew × 2 so a replayed request can't slip through after
	// the cache forgets it.
	NonceCacheTTL = 15 * time.Minute

	// MaxBodyBytes caps request bodies. Our payloads are tiny JSON
	// (recipient, file path, message text). PDFs are referenced by path,
	// never inlined. Anything larger is suspicious.
	MaxBodyBytes = 1 << 20 // 1 MiB
)

// Errors. Exported so that tests and integration code can branch on them.
var (
	ErrMissingHeaders   = errors.New("loopback: missing auth headers")
	ErrTimestampInvalid = errors.New("loopback: timestamp invalid")
	ErrTimestampSkew    = errors.New("loopback: timestamp outside allowed skew")
	ErrNonceReplay      = errors.New("loopback: nonce replay detected")
	ErrSignatureBad     = errors.New("loopback: signature mismatch")
)

// GenerateSecret returns a fresh 32-byte random secret. Stored once per
// install — the DLL reads it from the registry, the service holds it in
// memory.
func GenerateSecret() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("loopback: generate secret: %w", err)
	}
	return b, nil
}

// Sign produces the canonical signature for a request. Used by the DLL
// (transliterated to C#) and by tests.
func Sign(secret []byte, method, path string, ts time.Time, nonce string, body []byte) string {
	bodyHash := sha256.Sum256(body)
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "%s\n%s\n%d\n%s\n%s",
		method, path, ts.Unix(), nonce, hex.EncodeToString(bodyHash[:]))
	return hex.EncodeToString(mac.Sum(nil))
}

// Authenticator implements the HMAC middleware with bounded nonce memory.
type Authenticator struct {
	Secret []byte
	Now    func() time.Time

	mu     sync.Mutex
	nonces map[string]time.Time
}

// NewAuthenticator returns an authenticator with sane defaults.
func NewAuthenticator(secret []byte) *Authenticator {
	return &Authenticator{
		Secret: secret,
		Now:    time.Now,
		nonces: make(map[string]time.Time),
	}
}

// Middleware wraps an http.Handler and rejects unsigned or malformed
// requests with 401. Successful requests have their body buffered and
// re-attached so downstream handlers can read it normally.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := a.authenticate(r); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Authenticator) authenticate(r *http.Request) error {
	tsHeader := r.Header.Get(HeaderTimestamp)
	nonce := r.Header.Get(HeaderNonce)
	sig := r.Header.Get(HeaderSignature)
	if tsHeader == "" || nonce == "" || sig == "" {
		return ErrMissingHeaders
	}

	tsUnix, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return ErrTimestampInvalid
	}
	ts := time.Unix(tsUnix, 0)
	now := a.Now()
	if ts.Before(now.Add(-MaxClockSkew)) || ts.After(now.Add(MaxClockSkew)) {
		return ErrTimestampSkew
	}

	body, err := readAndRestore(r)
	if err != nil {
		return err
	}

	expected := Sign(a.Secret, r.Method, r.URL.Path, ts, nonce, body)
	// hmac.Equal is constant-time. Don't replace with == comparison.
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return ErrSignatureBad
	}

	if !a.rememberNonce(nonce, now) {
		return ErrNonceReplay
	}
	return nil
}

// rememberNonce returns true if this nonce was unseen, false if a replay.
// Cleanup is lazy — runs every call but is O(map size) which is tiny in
// practice (thousands of entries even at peak).
func (a *Authenticator) rememberNonce(nonce string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := now.Add(-NonceCacheTTL)
	for k, t := range a.nonces {
		if t.Before(cutoff) {
			delete(a.nonces, k)
		}
	}
	if _, seen := a.nonces[nonce]; seen {
		return false
	}
	a.nonces[nonce] = now
	return true
}

// readAndRestore reads the body within MaxBodyBytes and refills r.Body
// so downstream handlers see an unconsumed stream.
func readAndRestore(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	limited := http.MaxBytesReader(nil, r.Body, MaxBodyBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("loopback: read body: %w", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

// Server runs the loopback HTTP listener.
type Server struct {
	handler  http.Handler
	listener net.Listener
	srv      *http.Server
}

// New creates an unstarted server. handler is the wrapped router (typically
// the result of Authenticator.Middleware(router)).
func New(handler http.Handler) *Server {
	return &Server{handler: handler}
}

// Start binds to 127.0.0.1 on a random free port and begins serving.
// Returns once the listener is open so callers can publish the port to
// the registry before any incoming request races them.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("loopback: listen: %w", err)
	}
	s.listener = ln
	s.srv = &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		// Serve returns ErrServerClosed on graceful shutdown; ignore it.
		_ = s.srv.Serve(ln)
	}()
	return nil
}

// Addr returns the bound TCP address (host:port). Empty string before Start.
func (s *Server) Addr() string {
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// Port is the convenience accessor used by registry publishing.
func (s *Server) Port() int {
	if s.listener == nil {
		return 0
	}
	return s.listener.Addr().(*net.TCPAddr).Port
}

// Shutdown stops the server gracefully. ctx bounds how long to wait for
// in-flight requests.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}
