//go:build windows

package svc

import (
	"context"
	"fmt"
	"log/slog"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
)

// Run dispatches to the SCM. It blocks until SCM stops the service.
// Call this from main when running as a Windows service.
func Run(cfg Config, app App) error {
	var elog elogger
	if opened, err := eventlog.Open(cfg.Name); err == nil {
		elog = opened
	} else {
		// Eventlog source not registered (installer didn't run, or dev box
		// without it). Fall back to the debug console logger; SCM dispatch
		// still works.
		elog = debug.New(cfg.Name)
	}
	defer elog.Close()

	handler := &serviceHandler{app: app, log: elog, cfg: cfg}
	if err := svc.Run(cfg.Name, handler); err != nil {
		return fmt.Errorf("svc: dispatch %q: %w", cfg.Name, err)
	}
	return nil
}

// RunDebug runs the App in the foreground for development. Cancel with
// ctx (typically wired to SIGINT in the calling main).
func RunDebug(ctx context.Context, app App, logger *slog.Logger) error {
	logger.Info("svc: running in debug mode")
	err := app.Run(ctx)
	logger.Info("svc: debug run exited", "err", err)
	return err
}

type elogger interface {
	Info(eid uint32, msg string) error
	Warning(eid uint32, msg string) error
	Error(eid uint32, msg string) error
	Close() error
}

type serviceHandler struct {
	app App
	log elogger
	cfg Config
}

// Execute is the SCM callback contract. We accept Stop and Shutdown.
// Pause/Continue intentionally not supported — there is no meaningful
// "paused" state for a message queue service; Stop is what the user wants.
func (h *serviceHandler) Execute(args []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- h.app.Run(ctx)
	}()

	status <- svc.Status{State: svc.Running, Accepts: accepted}
	_ = h.log.Info(1, fmt.Sprintf("%s started", h.cfg.Name))

	for {
		select {
		case req := <-r:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				_ = h.log.Info(1, fmt.Sprintf("%s stop requested (cmd=%d)", h.cfg.Name, req.Cmd))
				status <- svc.Status{State: svc.StopPending}
				cancel()
				err := <-done
				if err != nil && err != context.Canceled {
					_ = h.log.Error(1, fmt.Sprintf("%s exited with error: %v", h.cfg.Name, err))
				}
				status <- svc.Status{State: svc.Stopped}
				return false, 0
			default:
				_ = h.log.Warning(1, fmt.Sprintf("%s unexpected control %d", h.cfg.Name, req.Cmd))
			}
		case err := <-done:
			// App returned before SCM asked it to. That's a crash.
			if err != nil && err != context.Canceled {
				_ = h.log.Error(1, fmt.Sprintf("%s app exited unexpectedly: %v", h.cfg.Name, err))
			}
			status <- svc.Status{State: svc.Stopped}
			// Non-zero exit code so SCM's failure-actions kick in (restart).
			return false, 1
		}
	}
}

// IsWindowsService is a small wrapper so callers don't have to import the
// svc package directly to detect SCM context.
func IsWindowsService() (bool, error) {
	return svc.IsWindowsService()
}
