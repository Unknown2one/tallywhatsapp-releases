// Package svc adapts a long-running app to the Windows Service Control
// Manager (SCM). It is intentionally tiny: SCM lifecycle is well-defined
// and we don't want a bespoke abstraction layer.
//
// Two modes:
//
//   - Service mode (the default when running under SCM): connects to SCM,
//     translates control commands to lifecycle calls.
//   - Debug mode (CLI flag during development): runs the same App in the
//     foreground and stops on Ctrl+C. Lets us iterate without installing
//     the service.
package svc

import "context"

// App is implemented by the application. Run should block until ctx is
// cancelled and return only when shutdown is complete.
type App interface {
	Run(ctx context.Context) error
}

// Config configures the SCM-side identity. Name must match what the
// installer used in `sc create`.
type Config struct {
	Name string
}
