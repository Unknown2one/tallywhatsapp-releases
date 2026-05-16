//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

// dataRoot mirrors the Windows path layout under a developer-friendly
// home directory so we can iterate on Mac/Linux. Production never hits
// this path.
func dataRoot() string {
	if root := os.Getenv("TALLYWA_DATA"); root != "" {
		return root
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".tallywa")
}
