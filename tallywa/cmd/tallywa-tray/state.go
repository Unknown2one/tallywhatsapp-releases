package main

import "time"

// sleep wraps time.Sleep so callers don't import time everywhere.
// Argument is milliseconds.
func sleep(ms int) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

// uiState maps the service-side states to a small enum the tray cares
// about (icon colour + label). The dashboard receives the full state.
type uiState int

const (
	stateUnknown uiState = iota
	stateServiceDown
	stateNotActivated
	stateAwaitingQR
	stateConnecting
	stateConnected
	stateLoggedOut
)

func (s uiState) label() string {
	switch s {
	case stateServiceDown:
		return "service not running"
	case stateNotActivated:
		return "not activated"
	case stateAwaitingQR:
		return "scan QR in dashboard"
	case stateConnecting:
		return "connecting…"
	case stateConnected:
		return "connected"
	case stateLoggedOut:
		return "logged out"
	}
	return "starting…"
}

func (s uiState) tooltip() string {
	switch s {
	case stateConnected:
		return "WhatsApp connected · ready"
	case stateAwaitingQR:
		return "open dashboard to scan QR"
	case stateNotActivated:
		return "open dashboard to enter your activation code"
	}
	return s.label()
}
