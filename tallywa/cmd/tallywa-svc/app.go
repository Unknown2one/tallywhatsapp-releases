package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tallywa/tallywa/internal/activation"
	"github.com/tallywa/tallywa/internal/fingerprint"
	"github.com/tallywa/tallywa/internal/handshake"
	"github.com/tallywa/tallywa/internal/httpapi"
	"github.com/tallywa/tallywa/internal/license"
	"github.com/tallywa/tallywa/internal/loopback"
	"github.com/tallywa/tallywa/internal/outbox"
	"github.com/tallywa/tallywa/internal/tally"
	"github.com/tallywa/tallywa/internal/whatsapp"
)

// App is the live process state. svc.Run/RunDebug calls Run on it.
type App struct {
	logger    *slog.Logger
	version   string
	issuerURL string
	pubKey    ed25519.PublicKey

	dataDir     string
	queuePath   string
	licensePath string
}

// buildApp validates configuration that we can check before attempting to
// open files or sockets. Failures here are operator errors; failures in
// Run are runtime/transient.
func buildApp(logger *slog.Logger, version, issuerURL, pubKeyB64 string) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	a := &App{
		logger:      logger,
		version:     version,
		issuerURL:   issuerURL,
		dataDir:     dataRoot(),
		licensePath: license.DefaultPath(),
	}
	a.queuePath = filepath.Join(a.dataDir, "outbox.db")

	if pubKeyB64 != "" {
		raw, err := base64.StdEncoding.DecodeString(pubKeyB64)
		if err != nil {
			return nil, fmt.Errorf("decode license public key: %w", err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("license public key size = %d, want %d",
				len(raw), ed25519.PublicKeySize)
		}
		a.pubKey = raw
	} else {
		logger.Warn("no license public key embedded; activation will be unavailable")
	}
	return a, nil
}

// Run brings up subsystems in dependency order, blocks until ctx is
// cancelled, then unwinds. It is the only method svc.App requires.
func (a *App) Run(ctx context.Context) error {
	if err := os.MkdirAll(a.dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// 1. Open the durable outbox. Survives crashes; recovers on open.
	box, err := outbox.Open(outbox.Options{
		Path:   a.queuePath,
		Logger: a.logger.With("component", "outbox"),
	})
	if err != nil {
		return fmt.Errorf("open outbox: %w", err)
	}
	defer func() {
		if cErr := box.Close(); cErr != nil {
			a.logger.Error("outbox.Close", "err", cErr)
		}
	}()

	// 2. Bring up the WhatsApp socket. Session is encrypted SQLite under
	// %ProgramData%\TallyWhatsApp\session — survives reboots, requires
	// re-pair only if the phone-side companion gets revoked.
	meow, err := whatsapp.NewMeowClient(whatsapp.MeowOptions{
		SessionDir: filepath.Join(a.dataDir, "session"),
		Logger:     a.logger.With("component", "whatsapp"),
	})
	if err != nil {
		return fmt.Errorf("init whatsapp: %w", err)
	}
	defer func() {
		if cErr := meow.Close(); cErr != nil {
			a.logger.Error("whatsapp.Close", "err", cErr)
		}
	}()

	if err := meow.Start(ctx); err != nil {
		// Connection failures shouldn't keep the service down — the user
		// may simply be offline at boot. Log and continue; whatsmeow's
		// internal reconnect loop will keep trying.
		a.logger.Warn("whatsapp.Start", "err", err)
	}
	var client whatsapp.Client = meow

	// 3. HMAC secret for the loopback API. Persisted across restarts.
	secret, err := handshake.LoadOrCreateSecret(loopback.GenerateSecret)
	if err != nil {
		return fmt.Errorf("load HMAC secret: %w", err)
	}
	auth := loopback.NewAuthenticator(secret)

	// 4. Build the HTTP state.
	state := &httpapi.State{
		Outbox:      box,
		Client:      client,
		Logger:      a.logger.With("component", "httpapi"),
		Version:     a.version,
		Started:     time.Now(),
		LicensePath: a.licensePath,
	}

	// 5. Load the existing license, if any. A failure to read or verify
	// is non-fatal: the service starts in unactivated mode and the user
	// re-pastes their token. This is the recovery path for license.dat
	// corruption or a fingerprint change.
	if a.pubKey != nil {
		state.Activator = activation.New(a.issuerURL, a.pubKey)
		a.tryLoadLicense(state)
	}

	// 6. Bind the loopback server. We bind FIRST, then publish the port
	// — there's a tiny window where the port is open but unannounced;
	// that's fine because the port is unguessable from outside this
	// machine and HMAC blocks unauthenticated calls anyway.
	router := httpapi.Routes(state)
	srv := loopback.New(auth.Middleware(router))
	if err := srv.Start(); err != nil {
		return fmt.Errorf("start loopback: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	a.logger.Info("loopback listening", "addr", srv.Addr())

	// 7. Publish handshake values so the COM DLL inside Tally's process
	// can find us.
	if err := handshake.PublishPort(srv.Port()); err != nil {
		a.logger.Warn("handshake.PublishPort", "err", err)
	}
	if err := handshake.PublishVersion(a.version); err != nil {
		a.logger.Warn("handshake.PublishVersion", "err", err)
	}

	// 7a. Self-heal: re-run the tally.ini patcher on every startup. This
	// covers three cases the install-time custom action can't:
	//   - Customer installed Tally AFTER our product.
	//   - Customer reinstalled Tally and clobbered tally.ini.
	//   - Customer hand-edited tally.ini and dropped our entries.
	// Errors here are non-fatal — the service still works; only the
	// in-Tally button stops working. We log loudly so support can spot it.
	go a.selfHealTallyIni()

	// 8. Start the outbox worker.
	sender := &whatsapp.Sender{Client: client, Logger: a.logger.With("component", "sender")}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := box.Run(ctx, sender); err != nil && !errors.Is(err, context.Canceled) {
			a.logger.Error("outbox worker exited", "err", err)
		}
	}()

	a.logger.Info("service ready",
		"version", a.version,
		"data_dir", a.dataDir,
		"loopback_port", srv.Port(),
		"licensed", state.LoadLicense() != nil,
	)

	<-ctx.Done()
	a.logger.Info("shutdown signalled, draining...")

	// Worker stops on ctx cancellation; wait for it before returning so
	// the outbox isn't closed mid-write by the deferred Close().
	wg.Wait()
	a.logger.Info("shutdown complete")
	return nil
}

// tryLoadLicense reads license.dat, verifies it against the embedded
// public key + this machine's fingerprint, and stores it on success. All
// failure modes are logged but non-fatal.
func (a *App) tryLoadLicense(state *httpapi.State) {
	raw, err := license.Read(a.licensePath)
	if errors.Is(err, license.ErrNotActivated) {
		a.logger.Info("no license on disk; service is in trial/unactivated state",
			"path", a.licensePath)
		return
	}
	if err != nil {
		a.logger.Warn("license.Read failed", "path", a.licensePath, "err", err)
		return
	}

	fp, err := fingerprint.Compute()
	if err != nil {
		a.logger.Warn("fingerprint.Compute failed", "err", err)
		return
	}

	lic, err := (&license.Verifier{PublicKey: a.pubKey}).Verify(raw, fp)
	if err != nil {
		a.logger.Warn("license verify failed", "err", err,
			"hint", "fingerprint may have changed (new motherboard, new MAC) — user should re-activate")
		return
	}

	state.StoreLicense(lic)
	a.logger.Info("license loaded",
		"edition", lic.Edition,
		"customer_email", lic.CustomerEmail,
		"issued_at", lic.IssuedAt.Format(time.RFC3339),
	)
}

// selfHealTallyIni re-applies the tally.ini patch on every service start.
// Idempotent: a no-op when the entries are already present and pointing at
// our installed TDL files. Runs in a goroutine so a slow disk doesn't
// block the loopback server from accepting connections.
func (a *App) selfHealTallyIni() {
	exe, err := os.Executable()
	if err != nil {
		a.logger.Warn("self-heal: locate exe", "err", err)
		return
	}
	tdlDir := filepath.Join(filepath.Dir(exe), "TDL")
	entries, err := os.ReadDir(tdlDir)
	if err != nil {
		a.logger.Warn("self-heal: read TDL dir", "dir", tdlDir, "err", err)
		return
	}
	var tdls []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.EqualFold(filepath.Ext(e.Name()), ".tdl") {
			continue
		}
		tdls = append(tdls, filepath.Join(tdlDir, e.Name()))
	}
	if len(tdls) == 0 {
		a.logger.Warn("self-heal: no TDL files alongside binary", "dir", tdlDir)
		return
	}

	installs, err := tally.Detect()
	if errors.Is(err, tally.ErrNotInstalled) {
		a.logger.Info("self-heal: no Tally install detected; will retry on next service start")
		return
	}
	if err != nil {
		a.logger.Warn("self-heal: detect Tally", "err", err)
		return
	}
	for _, inst := range installs {
		changed, err := tally.PatchIni(inst, tdls)
		if err != nil {
			a.logger.Warn("self-heal: patch tally.ini", "ini", inst.IniPath, "err", err)
			continue
		}
		if changed {
			a.logger.Info("self-heal: patched tally.ini", "ini", inst.IniPath)
		}
	}
}
