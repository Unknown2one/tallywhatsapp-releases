package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"

	"github.com/tallywa/tallywa/internal/outbox"
)

// State enumerates the high-level connection states the tray UI cares about.
// The actual whatsmeow socket has more nuance, but for the user it's these.
type State string

const (
	StateDisconnected State = "disconnected" // service starting, never connected
	StateConnecting   State = "connecting"   // socket dialling
	StateAwaitingQR   State = "awaiting_qr"  // not paired, QR is on offer
	StateConnected    State = "connected"    // ready to send
	StateLoggedOut    State = "logged_out"   // device kicked from phone, needs re-pair
)

// MeowClient is the whatsmeow-backed Client implementation. It owns the
// session database, the long-lived socket, and a small piece of state
// the tray UI polls (QR string + connection state).
type MeowClient struct {
	logger      *slog.Logger
	sessionDir  string
	dbPath      string
	dbContainer *sqlstore.Container
	device      *whatsmeow.Client

	// state and qr are read by API handlers and written by the event
	// loop. Atomic values let the read path stay lock-free; we only
	// take the mutex when (re)creating the underlying *whatsmeow.Client.
	state atomic.Value // State
	qr    atomic.Value // string

	mu       sync.RWMutex
	cancelFn context.CancelFunc
	wg       sync.WaitGroup
}

// MeowOptions configures the meow client.
type MeowOptions struct {
	// SessionDir is where the whatsmeow SQLite session lives. Created if
	// missing. In production this is %ProgramData%\TallyWhatsApp\session.
	SessionDir string
	Logger     *slog.Logger
}

// NewMeowClient opens the session database and prepares the underlying
// whatsmeow client without yet connecting. Call Start to connect.
func NewMeowClient(opts MeowOptions) (*MeowClient, error) {
	if opts.SessionDir == "" {
		return nil, errors.New("whatsapp: SessionDir required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if err := os.MkdirAll(opts.SessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("whatsapp: create session dir: %w", err)
	}
	dbPath := filepath.Join(opts.SessionDir, "whatsapp.db")
	dsn := "file:" + dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"

	dbLogger := waLog.Stdout("WAStore", "WARN", true)
	container, err := sqlstore.New(context.Background(), "sqlite", dsn, dbLogger)
	if err != nil {
		return nil, fmt.Errorf("whatsapp: open store: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		_ = container.Close()
		return nil, fmt.Errorf("whatsapp: get device: %w", err)
	}
	if deviceStore == nil {
		deviceStore = container.NewDevice()
	}

	clientLogger := waLog.Stdout("WAClient", "WARN", true)
	wmClient := whatsmeow.NewClient(deviceStore, clientLogger)
	if wmClient == nil {
		_ = container.Close()
		return nil, errors.New("whatsapp: failed to construct whatsmeow client")
	}

	c := &MeowClient{
		logger:      opts.Logger,
		sessionDir:  opts.SessionDir,
		dbPath:      dbPath,
		dbContainer: container,
		device:      wmClient,
	}
	c.state.Store(StateDisconnected)
	c.qr.Store("")

	wmClient.AddEventHandler(c.handleEvent)
	return c, nil
}

// Start brings up the WhatsApp socket. If already paired, it just
// reconnects. If not paired, it begins emitting QR codes for the tray
// UI to display.
//
// Start returns once the connection process has begun. The actual
// connection completes asynchronously; observe State() and PhoneNumber()
// to know when it's ready.
func (c *MeowClient) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancelFn != nil {
		return errors.New("whatsapp: already started")
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.cancelFn = cancel

	if c.device.Store.ID == nil {
		// Not paired: get the QR channel BEFORE Connect (whatsmeow
		// requires this order so we don't miss the first code).
		qrChan, err := c.device.GetQRChannel(runCtx)
		if err != nil {
			cancel()
			c.cancelFn = nil
			return fmt.Errorf("whatsapp: get qr channel: %w", err)
		}
		c.state.Store(StateAwaitingQR)

		c.wg.Add(1)
		go c.consumeQRChannel(qrChan)
	} else {
		c.state.Store(StateConnecting)
	}

	if err := c.device.Connect(); err != nil {
		cancel()
		c.cancelFn = nil
		c.state.Store(StateDisconnected)
		return fmt.Errorf("whatsapp: connect: %w", err)
	}
	return nil
}

// Stop disconnects the socket and waits for the QR consumer goroutine.
// Safe to call multiple times.
func (c *MeowClient) Stop() {
	c.mu.Lock()
	cancel := c.cancelFn
	c.cancelFn = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if c.device != nil && c.device.IsConnected() {
		c.device.Disconnect()
	}
	c.wg.Wait()
	c.state.Store(StateDisconnected)
}

// Close stops the client and releases the database. Must not call Start
// after Close.
func (c *MeowClient) Close() error {
	c.Stop()
	if c.dbContainer != nil {
		return c.dbContainer.Close()
	}
	return nil
}

// Logout tells WhatsApp to forget this device. Used by the tray UI's
// "re-pair from a different phone" flow. The HTTP handler chases this
// with Reconnect so the user gets a fresh QR without restarting.
func (c *MeowClient) Logout(ctx context.Context) error {
	if c.device == nil {
		return nil
	}
	if err := c.device.Logout(ctx); err != nil {
		return fmt.Errorf("whatsapp: logout: %w", err)
	}
	c.state.Store(StateLoggedOut)
	c.qr.Store("")
	return nil
}

// Reconnect tears down any in-flight pairing state and arms a fresh QR
// channel. Idempotent; safe to call when already connected (no-op) and
// when not connected (re-pairs). The caller passes a long-lived context
// — we don't bind goroutines to the request ctx because the QR consumer
// has to outlive the HTTP request.
func (c *MeowClient) Reconnect(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Stop the previous QR consumer (if any) and drop the existing socket.
	// whatsmeow's auto-reconnect is fine when paired, but we want a clean
	// state machine when re-pairing — fresh QR channel, no half-alive
	// goroutines from the previous session.
	if c.cancelFn != nil {
		c.cancelFn()
		c.cancelFn = nil
	}
	if c.device.IsConnected() {
		c.device.Disconnect()
	}
	c.wg.Wait()

	runCtx, cancel := context.WithCancel(context.Background())
	c.cancelFn = cancel

	if c.device.Store.ID == nil {
		// Unpaired — usual case after Logout. Open a new QR channel
		// before Connect so we never miss the first QR push.
		qrChan, err := c.device.GetQRChannel(runCtx)
		if err != nil {
			cancel()
			c.cancelFn = nil
			return fmt.Errorf("whatsapp: get qr channel: %w", err)
		}
		c.state.Store(StateAwaitingQR)
		c.wg.Add(1)
		go c.consumeQRChannel(qrChan)
	} else {
		// Still paired — caller hit Reconnect without logging out.
		// Just dial.
		c.state.Store(StateConnecting)
	}

	if err := c.device.Connect(); err != nil {
		cancel()
		c.cancelFn = nil
		c.state.Store(StateDisconnected)
		return fmt.Errorf("whatsapp: connect: %w", err)
	}
	return nil
}

// State returns the current connection state. Safe under concurrent calls.
func (c *MeowClient) State() State {
	v := c.state.Load()
	if v == nil {
		return StateDisconnected
	}
	return v.(State)
}

// QR returns the current pending pairing QR string, or "" if not awaiting.
func (c *MeowClient) QR() string {
	v := c.qr.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

// Connected reports whether the socket is up AND the device is paired.
// This is what the outbox worker checks before attempting a send.
func (c *MeowClient) Connected() bool {
	if c.device == nil {
		return false
	}
	return c.device.IsConnected() && c.device.IsLoggedIn()
}

// PhoneNumber returns the user's WhatsApp number once paired.
func (c *MeowClient) PhoneNumber() string {
	if c.device == nil || c.device.Store.ID == nil {
		return ""
	}
	return c.device.Store.ID.User
}

// SendText delivers a text message. Returns outbox.ErrPermanent for
// inputs that will never succeed (bad JID, malformed recipient).
func (c *MeowClient) SendText(ctx context.Context, recipient, message string) error {
	if !c.Connected() {
		return errors.New("whatsapp: not connected")
	}
	jid, err := parseRecipient(recipient)
	if err != nil {
		return fmt.Errorf("%w: %v", outbox.ErrPermanent, err)
	}
	msg := &waProto.Message{Conversation: proto.String(message)}
	if _, err := c.device.SendMessage(ctx, jid, msg); err != nil {
		return classifySendError(err)
	}
	return nil
}

// SendFile delivers a file to recipient with optional caption. The
// MIME type is inferred from extension; unknown extensions are sent as
// generic documents (the right call for Tally's PDFs and XLSX exports).
func (c *MeowClient) SendFile(ctx context.Context, recipient, filePath, caption string) error {
	if !c.Connected() {
		return errors.New("whatsapp: not connected")
	}
	jid, err := parseRecipient(recipient)
	if err != nil {
		return fmt.Errorf("%w: %v", outbox.ErrPermanent, err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		// File doesn't exist or can't be read. Tally exports go to a
		// known directory; if it's missing, retrying later won't help.
		return fmt.Errorf("%w: read file %q: %v", outbox.ErrPermanent, filePath, err)
	}

	mediaType, mimeType := classifyMedia(filePath)
	upload, err := c.device.Upload(ctx, data, mediaType)
	if err != nil {
		return classifySendError(err)
	}

	msg := buildMediaMessage(mediaType, mimeType, caption, filePath, upload, uint64(len(data)))
	if msg == nil {
		return fmt.Errorf("%w: unsupported media type", outbox.ErrPermanent)
	}
	if _, err := c.device.SendMessage(ctx, jid, msg); err != nil {
		return classifySendError(err)
	}
	return nil
}

// --------- internals ---------

// handleEvent is called by whatsmeow on every wire event. We only care
// about the lifecycle ones — message events (incoming chat, etc) are
// ignored because this is a send-only product surface.
func (c *MeowClient) handleEvent(evt any) {
	switch v := evt.(type) {
	case *events.Connected:
		c.state.Store(StateConnected)
		c.qr.Store("")
		c.logger.Info("whatsapp connected", "phone", c.PhoneNumber())
	case *events.Disconnected:
		// whatsmeow auto-reconnects internally; mark connecting so the
		// tray shows "reconnecting…" rather than scaring the user with a
		// red badge for transient blips.
		if c.State() == StateConnected {
			c.state.Store(StateConnecting)
		}
		c.logger.Warn("whatsapp socket disconnected; auto-reconnect engaged")
	case *events.LoggedOut:
		c.state.Store(StateLoggedOut)
		c.qr.Store("")
		c.logger.Warn("whatsapp logged out", "reason", v.Reason)
	case *events.PairSuccess:
		c.state.Store(StateConnecting) // fully connected fires Connected next
		c.qr.Store("")
		c.logger.Info("whatsapp paired", "phone", v.ID.User)
	case *events.StreamError, *events.ConnectFailure, *events.ClientOutdated:
		c.logger.Error("whatsapp stream error", "evt", fmt.Sprintf("%T", v))
	}
}

// consumeQRChannel forwards QR codes from whatsmeow into the atomic QR
// slot. Empties the slot when pairing succeeds or the channel closes.
func (c *MeowClient) consumeQRChannel(ch <-chan whatsmeow.QRChannelItem) {
	defer c.wg.Done()
	for evt := range ch {
		switch evt.Event {
		case "code":
			c.qr.Store(evt.Code)
			c.state.Store(StateAwaitingQR)
		case "success":
			c.qr.Store("")
			// Connected event will fire shortly; don't pre-empt it.
		case "timeout":
			c.qr.Store("")
			c.logger.Warn("whatsapp pairing QR timed out; user did not scan in time")
		case "err-client-outdated":
			c.qr.Store("")
			c.logger.Error("whatsmeow library outdated; update tallywa-svc")
		default:
			c.logger.Warn("whatsapp qr event", "event", evt.Event)
		}
	}
}

// parseRecipient turns a Tally-supplied recipient string into a JID.
// Tally sends "919999999999" — 12-digit phone with country code, no plus.
// We also accept full JIDs ("9111@s.whatsapp.net") to keep the door open
// for group sending later.
func parseRecipient(s string) (types.JID, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return types.JID{}, errors.New("recipient is empty")
	}
	if strings.Contains(s, "@") {
		jid, err := types.ParseJID(s)
		if err != nil {
			return types.JID{}, fmt.Errorf("invalid JID %q: %w", s, err)
		}
		return jid, nil
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return types.JID{}, fmt.Errorf("recipient %q must be digits only", s)
		}
	}
	return types.JID{User: s, Server: types.DefaultUserServer}, nil
}

// classifyMedia maps file extensions to whatsmeow's media classifier.
// PDFs (the dominant Tally output) are documents; images are inlined.
func classifyMedia(path string) (whatsmeow.MediaType, string) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case "jpg", "jpeg":
		return whatsmeow.MediaImage, "image/jpeg"
	case "png":
		return whatsmeow.MediaImage, "image/png"
	case "webp":
		return whatsmeow.MediaImage, "image/webp"
	case "mp4":
		return whatsmeow.MediaVideo, "video/mp4"
	case "ogg":
		return whatsmeow.MediaAudio, "audio/ogg; codecs=opus"
	case "pdf":
		return whatsmeow.MediaDocument, "application/pdf"
	case "xlsx":
		return whatsmeow.MediaDocument, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "xls":
		return whatsmeow.MediaDocument, "application/vnd.ms-excel"
	case "doc":
		return whatsmeow.MediaDocument, "application/msword"
	case "docx":
		return whatsmeow.MediaDocument, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "csv":
		return whatsmeow.MediaDocument, "text/csv"
	case "txt":
		return whatsmeow.MediaDocument, "text/plain"
	default:
		return whatsmeow.MediaDocument, "application/octet-stream"
	}
}

// buildMediaMessage assembles the right protobuf payload for the chosen
// media type. Returns nil for unsupported combinations.
func buildMediaMessage(mt whatsmeow.MediaType, mime, caption, filePath string, up whatsmeow.UploadResponse, fileLen uint64) *waProto.Message {
	msg := &waProto.Message{}
	switch mt {
	case whatsmeow.MediaImage:
		msg.ImageMessage = &waProto.ImageMessage{
			Caption:       proto.String(caption),
			Mimetype:      proto.String(mime),
			URL:           &up.URL,
			DirectPath:    &up.DirectPath,
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(fileLen),
		}
	case whatsmeow.MediaVideo:
		msg.VideoMessage = &waProto.VideoMessage{
			Caption:       proto.String(caption),
			Mimetype:      proto.String(mime),
			URL:           &up.URL,
			DirectPath:    &up.DirectPath,
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(fileLen),
		}
	case whatsmeow.MediaDocument:
		fileName := filepath.Base(filePath)
		msg.DocumentMessage = &waProto.DocumentMessage{
			Title:         proto.String(fileName),
			FileName:      proto.String(fileName),
			Caption:       proto.String(caption),
			Mimetype:      proto.String(mime),
			URL:           &up.URL,
			DirectPath:    &up.DirectPath,
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    proto.Uint64(fileLen),
		}
	default:
		return nil
	}
	return msg
}

// classifySendError decides whether a whatsmeow error is permanent.
// Network/connection errors are transient (let the outbox retry).
// Schema errors (bad recipient, encoding issues) are permanent — no
// amount of retry will fix them.
func classifySendError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not connected"),
		strings.Contains(msg, "websocket"),
		strings.Contains(msg, "context deadline"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "timeout"):
		return err // transient
	case strings.Contains(msg, "invalid jid"),
		strings.Contains(msg, "no such recipient"),
		strings.Contains(msg, "not on whatsapp"):
		return fmt.Errorf("%w: %v", outbox.ErrPermanent, err)
	}
	// Default: treat unknown errors as transient. Better to retry a few
	// times and dead-letter than to throw away a real send because of a
	// classifier miss. The outbox's MaxAttempts caps the damage.
	return err
}

// keep go vet quiet about unused url import (used only in media URLs above).
var _ = url.Parse
