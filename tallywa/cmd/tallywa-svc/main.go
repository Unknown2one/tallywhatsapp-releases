// Command tallywa-svc is the long-running connector that the Tally COM
// DLL talks to. It is the heart of the desktop product.
//
// Lifecycle:
//
//  1. Built once. Embedded license public key (LicensePublicKey, set via
//     -ldflags) and version baked into the binary.
//  2. Installed as a Windows Service called TallyWhatsAppConnector.
//  3. SCM starts the service automatically at boot (and on failure,
//     thanks to "sc failure" actions configured by the installer).
//  4. The service binds a loopback HTTP API on a free port, publishes
//     the port and HMAC secret to HKLM\SOFTWARE\TallyWhatsApp, loads
//     license.dat if present, and runs the outbox worker forever.
//  5. The Tally COM DLL inside Tally's process reads the registry and
//     POSTs signed requests over loopback. The tray UI does the same.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tallywa/tallywa/internal/svc"
)

// ServiceName must match what the WiX installer uses with `sc create`.
// Changing this orphans every existing installation.
const ServiceName = "TallyWhatsAppConnector"

// Build-time variables. CI sets these via -ldflags:
//
//	-ldflags "-X main.Version=2.0.1 \
//	          -X main.LicensePublicKey=$BASE64_PUB \
//	          -X main.DefaultIssuerURL=https://script.google.com/macros/s/.../exec"
//
// Empty LicensePublicKey is allowed for dev builds; activation simply
// won't work and the service logs a warning.
var (
	Version          = "dev"
	LicensePublicKey = ""
	DefaultIssuerURL = "https://script.google.com/macros/s/AKfycbxBmiVpDrBzbfH572ITzT8TEtZ3b2hYR9MgKHF-7Zt-oiIOUhW-ned5Wf0-hi7QQHrD/exec"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tallywa-svc:", err)
		os.Exit(1)
	}
}

func run() error {
	debug := flag.Bool("debug", false, "run in foreground for development (skip SCM)")
	issuerURL := flag.String("issuer-url", DefaultIssuerURL, "license issuer base URL")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	app, err := buildApp(logger, Version, *issuerURL, LicensePublicKey)
	if err != nil {
		return fmt.Errorf("buildApp: %w", err)
	}

	// IsWindowsService is the canonical check: if SCM started us, run as
	// a service; if a developer launched us from a console, run debug.
	// The --debug flag forces debug even when SCM started us, which is
	// occasionally useful when remoting in.
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("svc.IsWindowsService: %w", err)
	}
	if !isSvc || *debug {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return svc.RunDebug(ctx, app, logger)
	}
	return svc.Run(svc.Config{Name: ServiceName}, app)
}
