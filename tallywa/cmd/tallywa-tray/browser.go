package main

import (
	"os/exec"
	"runtime"
)

// openBrowser opens url in the user's default browser. We never block
// on the result — the dashboard menu item still works if this fails.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		// rundll32 is the most reliable approach on Windows; it doesn't
		// inherit our console (so the tray stays silent) and respects
		// the user's default browser association.
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
