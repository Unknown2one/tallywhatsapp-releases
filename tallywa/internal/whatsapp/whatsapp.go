// Package whatsapp defines the abstraction the outbox uses to talk to
// WhatsApp. The real implementation (whatsmeow) lives behind this
// interface so the outbox, HTTP handlers, and tests don't have to know
// the internals.
package whatsapp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tallywa/tallywa/internal/outbox"
)

// Client is the contract between the outbox worker and any WhatsApp
// implementation.
type Client interface {
	// Connected reports whether we currently have an authenticated socket.
	// Used by /api/health to surface connection state to the tray.
	Connected() bool

	// PhoneNumber returns the user's WhatsApp number once paired, empty
	// before pairing.
	PhoneNumber() string

	// SendText delivers a plain text message.
	SendText(ctx context.Context, recipient, message string) error

	// SendFile delivers a file (PDF, image, doc) with optional caption.
	SendFile(ctx context.Context, recipient, filePath, caption string) error
}

// LifecycleClient is an optional capability bundle implemented by clients
// that own session state (the real whatsmeow client does; the test stub
// optionally does). The HTTP API surfaces these to the tray UI for the
// pairing/repair flows. Code that only needs to send treats Client as
// the lowest common denominator.
type LifecycleClient interface {
	Client
	// State returns a high-level enum the tray UI can render.
	State() State
	// QR returns the current pending pairing QR string, "" if none.
	QR() string
	// Logout asks WhatsApp to forget this device. Caller must Reconnect
	// (or restart the service) to re-pair.
	Logout(ctx context.Context) error
	// Reconnect re-arms the QR channel after a Logout, or re-dials the
	// socket when the previous session went stale. Idempotent.
	Reconnect(ctx context.Context) error
}

// Sender adapts a Client to the outbox.Sender interface. Splitting Kind
// dispatch out of the Client interface keeps Client small (one method
// per WhatsApp primitive) and concentrates outbox-shape knowledge here.
type Sender struct {
	Client Client
	Logger *slog.Logger
}

// Send dispatches an outbox item to the right Client method.
func (s *Sender) Send(ctx context.Context, item *outbox.Item) error {
	if !s.Client.Connected() {
		// Transient: keep retrying until the user re-pairs WhatsApp.
		return fmt.Errorf("whatsapp: not connected")
	}
	switch item.Kind {
	case outbox.KindText:
		return s.Client.SendText(ctx, item.Recipient, item.Text)
	case outbox.KindFile:
		return s.Client.SendFile(ctx, item.Recipient, item.FilePath, item.Text)
	case outbox.KindFileWithText:
		// File first, then text. This mirrors the existing flow in
		// Backend-Bridge/api/handlers.go and is the order Tally users
		// expect: PDF lands, then the explanatory message follows.
		if err := s.Client.SendFile(ctx, item.Recipient, item.FilePath, ""); err != nil {
			return err
		}
		return s.Client.SendText(ctx, item.Recipient, item.Text)
	default:
		return fmt.Errorf("%w: unknown kind %q", outbox.ErrPermanent, item.Kind)
	}
}

// StubClient is a Client that records calls and returns configurable
// errors. Used for local development before the whatsmeow wiring lands,
// and for tests. It also implements LifecycleClient so the same fixture
// can drive activation + control-endpoint tests.
type StubClient struct {
	mu        sync.Mutex
	connected bool
	phone     string
	state     State
	qr        string
	calls     []StubCall
	sendErr   error
	delay     time.Duration
}

// StubCall records a single Send invocation.
type StubCall struct {
	Method    string
	Recipient string
	Body      string
	FilePath  string
	At        time.Time
}

// NewStubClient returns a connected stub with the given phone number.
func NewStubClient(phone string) *StubClient {
	return &StubClient{connected: true, phone: phone, state: StateConnected}
}

func (s *StubClient) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

func (s *StubClient) PhoneNumber() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phone
}

// SetConnected toggles the stub's connection state. Tests use this to
// simulate WhatsApp logout/reconnect.
func (s *StubClient) SetConnected(c bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = c
	if c {
		s.state = StateConnected
	} else {
		s.state = StateDisconnected
	}
}

// SetState sets the lifecycle state directly. Useful for tray-UI tests
// that want to assert the right user-facing message for each state.
func (s *StubClient) SetState(st State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = st
}

// SetQR overrides the stub's QR string for tests.
func (s *StubClient) SetQR(qr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.qr = qr
}

// State implements LifecycleClient.
func (s *StubClient) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// QR implements LifecycleClient.
func (s *StubClient) QR() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.qr
}

// Logout implements LifecycleClient. Marks the stub disconnected.
func (s *StubClient) Logout(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = false
	s.phone = ""
	s.state = StateLoggedOut
	s.qr = ""
	return nil
}

// Reconnect implements LifecycleClient. Re-arms the awaiting-QR state
// so tests can drive the post-logout repair flow.
func (s *StubClient) Reconnect(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateAwaitingQR
	return nil
}

// SetSendError makes subsequent Send* return the given error. Use nil
// to clear.
func (s *StubClient) SetSendError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendErr = err
}

// Calls returns a snapshot of recorded calls.
func (s *StubClient) Calls() []StubCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]StubCall, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *StubClient) SendText(_ context.Context, recipient, message string) error {
	return s.record("text", recipient, message, "")
}

func (s *StubClient) SendFile(_ context.Context, recipient, filePath, caption string) error {
	return s.record("file", recipient, caption, filePath)
}

func (s *StubClient) record(method, recipient, body, file string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.sendErr != nil {
		return s.sendErr
	}
	s.calls = append(s.calls, StubCall{
		Method:    method,
		Recipient: recipient,
		Body:      body,
		FilePath:  file,
		At:        time.Now(),
	})
	return nil
}
