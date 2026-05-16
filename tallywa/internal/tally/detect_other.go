//go:build !windows

package tally

import "os"

// candidateDirs on non-Windows is a single dev-override path so we can
// integration-test the patcher on a Mac or Linux box.
func candidateDirs() []string {
	if dir := os.Getenv("TALLYWA_TALLY_DIR"); dir != "" {
		return []string{dir}
	}
	return nil
}
