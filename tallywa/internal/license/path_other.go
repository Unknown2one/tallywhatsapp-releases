//go:build !windows

package license

import (
	"os"
	"path/filepath"
)

// DefaultPath on non-Windows builds points at a developer-friendly path so
// unit tests and local prototyping work. Production binary is Windows-only.
func DefaultPath() string {
	if root := os.Getenv("TALLYWA_DATA"); root != "" {
		return filepath.Join(root, "license.dat")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".tallywa", "license.dat")
}
