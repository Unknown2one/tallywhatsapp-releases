//go:build !windows

package svc

import (
	"context"
	"fmt"
	"log/slog"
)

// Run is a stub on non-Windows. Returns an error so a misbuild is loud.
func Run(_ Config, _ App) error {
	return fmt.Errorf("svc.Run: only supported on windows")
}

// RunDebug works on every platform — it's just app.Run with a logger.
func RunDebug(ctx context.Context, app App, logger *slog.Logger) error {
	logger.Info("svc: running in debug mode (non-windows)")
	err := app.Run(ctx)
	logger.Info("svc: debug run exited", "err", err)
	return err
}

// IsWindowsService is always false off Windows.
func IsWindowsService() (bool, error) { return false, nil }
