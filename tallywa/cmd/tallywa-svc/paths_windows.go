//go:build windows

package main

import (
	"os"
	"path/filepath"
)

// dataRoot returns the directory the service uses for license, queue, and
// logs. %ProgramData% is machine-scope and survives user account churn.
func dataRoot() string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData`
	}
	return filepath.Join(root, "TallyWhatsApp")
}
