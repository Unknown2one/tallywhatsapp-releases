package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tallywa/tallywa/internal/handshake"
	"github.com/tallywa/tallywa/internal/loopback"
)

// dashboardServer is the localhost HTTP server the tray exposes to the
// user's browser. It serves a static HTML/JS bundle and proxies API
// calls to the loopback service, signing each one with HMAC. The page
// itself never sees the secret.
type dashboardServer struct {
	listener net.Listener
	server   *http.Server
	logger   *slog.Logger

	ready    atomic.Bool
	doneCh   chan struct{}
	doneOnce sync.Once

	// Cached service state for the tray icon poller. The dashboard reads
	// this through /status — the tray reads it directly via Snapshot().
	stateMu sync.RWMutex
	state   serviceSnapshot
}

// serviceSnapshot is the small JSON the dashboard polls. We deliberately
// keep it boring — anything fancier risks the dashboard going stale
// when fields are added on the service side.
type serviceSnapshot struct {
	Reachable          bool      `json:"reachable"`
	WhatsAppState      string    `json:"whatsapp_state"`
	WhatsAppPhone      string    `json:"whatsapp_phone,omitempty"`
	HasQR              bool      `json:"has_qr"`
	Activated          bool      `json:"activated"`
	LicenseEdition     string    `json:"license_edition,omitempty"`
	LicenseEmail       string    `json:"license_email,omitempty"`
	ServiceVersion     string    `json:"service_version,omitempty"`
	QueueDepth         int       `json:"queue_depth"`
	QueuePending       int       `json:"queue_pending"`
	QueueSending       int       `json:"queue_sending"`
	QueueSent          int       `json:"queue_sent"`
	QueueDead          int       `json:"queue_dead"`
	UpdatedAt          time.Time `json:"updated_at"`
	LastError          string    `json:"last_error,omitempty"`
}

func newDashboardServer(logger *slog.Logger) (*dashboardServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("dashboard listen: %w", err)
	}
	d := &dashboardServer{
		listener: ln,
		logger:   logger,
		doneCh:   make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", d.handleIndex)
	mux.HandleFunc("/status", d.handleStatus)
	mux.HandleFunc("/proxy/", d.handleProxy)
	d.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return d, nil
}

func (d *dashboardServer) URL() string {
	return "http://127.0.0.1:" + strconv.Itoa(portFromAddr(d.listener.Addr())) + "/"
}

func (d *dashboardServer) Ready() bool { return d.ready.Load() }
func (d *dashboardServer) Done() <-chan struct{} { return d.doneCh }

func (d *dashboardServer) Serve() error {
	d.ready.Store(true)
	go d.statusLoop()
	err := d.server.Serve(d.listener)
	d.doneOnce.Do(func() { close(d.doneCh) })
	return err
}

func (d *dashboardServer) Shutdown(ctx context.Context) error {
	d.doneOnce.Do(func() { close(d.doneCh) })
	return d.server.Shutdown(ctx)
}

// Snapshot returns the cached service state. Used by the tray icon
// poller without re-hitting the service every tick.
func (d *dashboardServer) Snapshot() serviceSnapshot {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()
	return d.state
}

// statusLoop pulls /api/status from the service every few seconds and
// caches the result. Called in its own goroutine from Serve.
func (d *dashboardServer) statusLoop() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	d.refreshStatus() // immediate first poll
	for {
		select {
		case <-d.doneCh:
			return
		case <-t.C:
			d.refreshStatus()
		}
	}
}

func (d *dashboardServer) refreshStatus() {
	snap := serviceSnapshot{UpdatedAt: time.Now()}

	body, status, err := d.callService(context.Background(), http.MethodGet, "/api/status", nil)
	if err != nil {
		snap.LastError = err.Error()
		d.stateMu.Lock()
		d.state = snap
		d.stateMu.Unlock()
		return
	}
	if status >= 200 && status < 300 {
		var raw struct {
			State         string `json:"state"`
			HasQR         bool   `json:"has_qr"`
			PhoneNumber   string `json:"phone_number"`
			Connected     bool   `json:"connected"`
			Licensed      bool   `json:"licensed"`
			Edition       string `json:"edition"`
			CustomerEmail string `json:"customer_email"`
			Version       string `json:"version"`
			Queue         struct {
				Pending int `json:"pending"`
				Sending int `json:"sending"`
				Sent    int `json:"sent"`
				Dead    int `json:"dead"`
			} `json:"queue"`
		}
		if err := json.Unmarshal(body, &raw); err == nil {
			snap.Reachable = true
			snap.WhatsAppState = raw.State
			snap.WhatsAppPhone = raw.PhoneNumber
			snap.HasQR = raw.HasQR
			snap.Activated = raw.Licensed
			snap.LicenseEdition = raw.Edition
			snap.LicenseEmail = raw.CustomerEmail
			snap.QueueDepth = raw.Queue.Pending + raw.Queue.Sending
			snap.QueuePending = raw.Queue.Pending
			snap.QueueSending = raw.Queue.Sending
			snap.QueueSent = raw.Queue.Sent
			snap.QueueDead = raw.Queue.Dead
			snap.ServiceVersion = raw.Version
		}
	} else {
		snap.LastError = "service returned " + strconv.Itoa(status)
	}

	d.stateMu.Lock()
	d.state = snap
	d.stateMu.Unlock()
}

// toUIState collapses the snapshot to the small enum the tray icon uses.
func (s serviceSnapshot) toUIState() uiState {
	if !s.Reachable {
		return stateServiceDown
	}
	if !s.Activated {
		return stateNotActivated
	}
	switch s.WhatsAppState {
	case "connected":
		return stateConnected
	case "awaiting_qr":
		return stateAwaitingQR
	case "logged_out":
		return stateLoggedOut
	case "connecting", "disconnected":
		return stateConnecting
	}
	return stateUnknown
}

// handleIndex serves the embedded HTML dashboard.
func (d *dashboardServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(dashboardHTML))
}

// handleStatus exposes the cached snapshot to the dashboard JS so the
// page can repaint without going through /proxy on every tick.
func (d *dashboardServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(d.Snapshot())
}

// handleProxy forwards /proxy/<path> to the service at <path>, signing
// it with HMAC. We never expose the secret to the browser; the JS just
// calls e.g. POST /proxy/api/whatsapp/logout with no auth headers and
// this handler signs server-side.
func (d *dashboardServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	const prefix = "/proxy"
	if r.URL.Path == prefix || r.URL.Path == prefix+"/" {
		http.Error(w, "missing target path", http.StatusBadRequest)
		return
	}
	target := r.URL.Path[len(prefix):]
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	respBody, status, err := d.callService(r.Context(), r.Method, target, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(respBody)
}

// callService is the HMAC-signing reverse proxy primitive. Used by both
// the status poller and the /proxy handler.
//
// The path argument is the *raw* string sent on the wire — query string
// included if present. We sign the URL.Path portion only (stripping the
// query) to match the service's authentication, which works off
// r.URL.Path.
func (d *dashboardServer) callService(ctx context.Context, method, target string, body []byte) ([]byte, int, error) {
	port, secret, _, err := handshake.Load()
	if err != nil {
		return nil, 0, err
	}

	// Split path from query. The signature only covers the path; the
	// query is forwarded but not authenticated, which mirrors the
	// service's middleware.
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid target: %w", err)
	}

	full := "http://127.0.0.1:" + strconv.Itoa(port) + target
	req, err := http.NewRequestWithContext(ctx, method, full, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	if body != nil && len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	ts := time.Now()
	nonce, err := newNonce()
	if err != nil {
		return nil, 0, err
	}
	sig := loopback.Sign(secret, method, parsed.Path, ts, nonce, body)
	req.Header.Set(loopback.HeaderTimestamp, strconv.FormatInt(ts.Unix(), 10))
	req.Header.Set(loopback.HeaderNonce, nonce)
	req.Header.Set(loopback.HeaderSignature, sig)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, 0, err
	}
	return respBody, resp.StatusCode, nil
}

func newNonce() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("nonce: " + err.Error())
	}
	return hex.EncodeToString(b[:]), nil
}
