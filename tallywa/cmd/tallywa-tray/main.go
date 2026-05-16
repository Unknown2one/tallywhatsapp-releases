// Package main is the TallyWhatsApp tray app. It runs in the user's
// session (not as a service) and provides:
//
//   - A system-tray icon with status colour and quit/open menu items.
//   - A loopback HTTP dashboard served on 127.0.0.1, opened in the user's
//     default browser, that talks back to the service via the tray's
//     HMAC-signing reverse proxy.
//   - Autostart at logon via HKCU\...\Run.
//
// The tray never holds the HMAC secret in HTML/JS — every API call goes
// through /proxy/* which signs server-side. The dashboard is therefore
// safe even though it's reachable on localhost: a malicious page on the
// same machine could open it but cannot forge signed requests because
// the secret never leaves the tray process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"fyne.io/systray"
)

var version = "dev" // set via -ldflags

func main() {
	var (
		debug     = flag.Bool("debug", false, "log to stderr instead of running silently")
		dashOnly  = flag.Bool("dashboard", false, "open the dashboard and exit (no tray)")
		noBrowser = flag.Bool("no-browser", false, "don't open the dashboard automatically")
	)
	flag.Parse()

	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	// Bind the dashboard server early so we can open the browser as soon
	// as systray is ready. Binding on 127.0.0.1:0 lets the OS pick a
	// free port; the URL is shown in the menu and opened on Open Dashboard.
	srv, err := newDashboardServer(logger)
	if err != nil {
		logger.Error("dashboard server", "err", err)
		fmt.Fprintln(os.Stderr, "TallyWhatsApp: failed to start tray ("+err.Error()+")")
		os.Exit(1)
	}

	if *dashOnly {
		fmt.Println(srv.URL())
		runDashboard(srv, logger, true)
		return
	}

	// Run dashboard in background.
	go runDashboard(srv, logger, false)

	if !*noBrowser {
		go openOnReady(srv, logger)
	}

	// systray.Run blocks the main goroutine until Quit is selected.
	systray.Run(func() { onReady(srv, logger) }, func() { onExit(logger) })
}

func runDashboard(srv *dashboardServer, logger *slog.Logger, blocking bool) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("dashboard server stopped", "err", err)
		}
	case <-ctx.Done():
	}
	_ = srv.Shutdown(context.Background())
	if blocking {
		os.Exit(0)
	}
}

// openOnReady waits a beat for the listener to come up, then launches
// the user's default browser at the dashboard URL. We don't block tray
// startup on this — if the browser fails to open, the menu still works.
func openOnReady(srv *dashboardServer, logger *slog.Logger) {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Ready() {
			if err := openBrowser(srv.URL()); err != nil {
				logger.Warn("open browser", "err", err)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	logger.Warn("dashboard server not ready in time")
}

// portFromAddr extracts the TCP port from a "host:port" listener addr.
func portFromAddr(addr net.Addr) int {
	if t, ok := addr.(*net.TCPAddr); ok {
		return t.Port
	}
	_, p, _ := net.SplitHostPort(addr.String())
	n, _ := strconv.Atoi(p)
	return n
}
