//go:build windows

package license

import (
	"os"
	"path/filepath"
)

// DefaultPath returns the canonical location of license.dat on Windows.
// %ProgramData% is machine-scope and survives user account changes, which
// matches the per-PC license model.
func DefaultPath() string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData`
	}
	return filepath.Join(root, "TallyWhatsApp", "license.dat")
}
