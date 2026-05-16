//go:build !windows

package main

// Autostart on non-Windows is a stub for development on Mac / Linux.
// The real product only ships for Windows.

func autostartEnabled() bool   { return false }
func enableAutostart() error   { return nil }
func disableAutostart() error  { return nil }
