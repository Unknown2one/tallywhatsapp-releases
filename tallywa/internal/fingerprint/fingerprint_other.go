//go:build !windows

package fingerprint

import "fmt"

// Compute is a stub for non-Windows builds (development convenience only).
// Production binary is Windows-only.
func Compute() (string, error) {
	return "", fmt.Errorf("fingerprint: only supported on windows")
}
