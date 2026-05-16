// Package tally locates TallyPrime installations and patches tally.ini
// so our TDL files are loaded automatically. The installer calls into
// this package as a custom action; it is also exposed via a CLI for
// support staff who need to repair an install.
//
// The patcher is intentionally conservative:
//
//   - tally.ini is backed up to tally.ini.bak before any write.
//   - Existing keys are preserved verbatim — we never reflow whitespace
//     or comments.
//   - We add `User TDL = Yes` (case-insensitive check first) and one
//     `TDL = <path>` line per TDL file we ship.
//   - The patch is idempotent: running it twice produces the same file.
//
// Tally treats its INI as a flat keyed list, *not* a sectioned INI, and
// allows the same key to appear multiple times. That last bit is why we
// can't use Go's standard INI libraries.
package tally

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Errors callers can branch on.
var (
	ErrNotInstalled = errors.New("tally: TallyPrime installation not found")
	ErrIniMissing   = errors.New("tally: tally.ini not found in installation")
)

// Install describes a discovered TallyPrime installation.
type Install struct {
	// InstallDir contains tally.exe.
	InstallDir string
	// IniPath is the tally.ini that controls this install.
	IniPath string
	// Version reported by the installer if we can find it (best-effort).
	Version string
}

// Detect returns every TallyPrime installation on this machine. Multiple
// is rare but supported (some accountants run side-by-side versions).
//
// On non-Windows builds we look at TALLYWA_TALLY_DIR for development.
func Detect() ([]*Install, error) {
	var found []*Install
	for _, dir := range candidateDirs() {
		exe := filepath.Join(dir, "tally.exe")
		if !fileExists(exe) {
			continue
		}
		ini := filepath.Join(dir, "tally.ini")
		if !fileExists(ini) {
			// tally.exe present but tally.ini missing — odd but recoverable
			// by writing a fresh ini. Keep the install record.
			ini = filepath.Join(dir, "tally.ini")
		}
		found = append(found, &Install{
			InstallDir: dir,
			IniPath:    ini,
			Version:    bestEffortVersion(dir),
		})
	}
	if len(found) == 0 {
		return nil, ErrNotInstalled
	}
	return dedupeInstalls(found), nil
}

// PatchIni inserts our TDL entries into the install's tally.ini. tdlPaths
// must be absolute. Returns true if the file actually changed (so the
// caller can decide whether to prompt the user to restart Tally).
func PatchIni(install *Install, tdlPaths []string) (changed bool, err error) {
	if install == nil || install.IniPath == "" {
		return false, errors.New("tally: empty install")
	}
	for _, p := range tdlPaths {
		if !filepath.IsAbs(p) {
			return false, fmt.Errorf("tally: TDL path %q must be absolute", p)
		}
	}

	original, err := readIniOrEmpty(install.IniPath)
	if err != nil {
		return false, err
	}

	patched, changed := applyPatch(original, tdlPaths)
	if !changed {
		return false, nil
	}

	// Backup BEFORE we write. If the rename or write fails, the user has
	// a clean copy under tally.ini.bak.
	if fileExists(install.IniPath) {
		if err := backupFile(install.IniPath); err != nil {
			return false, fmt.Errorf("tally: backup tally.ini: %w", err)
		}
	}
	if err := writeAtomic(install.IniPath, patched); err != nil {
		return false, fmt.Errorf("tally: write tally.ini: %w", err)
	}
	return true, nil
}

// UnpatchIni removes any TDL lines pointing into the given directory.
// Used by the uninstaller. Idempotent.
func UnpatchIni(install *Install, tdlDir string) (changed bool, err error) {
	if install == nil || install.IniPath == "" {
		return false, errors.New("tally: empty install")
	}
	if !fileExists(install.IniPath) {
		return false, nil
	}
	abs, err := filepath.Abs(tdlDir)
	if err != nil {
		return false, err
	}
	abs = strings.ToLower(filepath.Clean(abs))

	original, err := readIniOrEmpty(install.IniPath)
	if err != nil {
		return false, err
	}
	patched, changed := stripTDLs(original, abs)
	if !changed {
		return false, nil
	}
	if err := backupFile(install.IniPath); err != nil {
		return false, err
	}
	if err := writeAtomic(install.IniPath, patched); err != nil {
		return false, err
	}
	return true, nil
}

// --------- INI editing internals ---------

// applyPatch returns the new file contents and whether anything changed.
// Algorithm:
//
//  1. Walk lines, recording whether `User TDL` is set to Yes and which
//     TDL paths are already present.
//  2. If `User TDL` is missing or set to No, append `User TDL = Yes`.
//  3. Append one `TDL = <path>` for each path not already present.
//
// We append rather than insert in-place because Tally's INI is order-
// insensitive and appending preserves user comments + custom keys at
// their original positions.
func applyPatch(content string, tdlPaths []string) (string, bool) {
	lines := splitLines(content)
	hasUserTDL := false
	userTDLValue := ""
	existingPaths := map[string]bool{}

	for _, line := range lines {
		key, val, ok := splitKV(line)
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "user tdl":
			hasUserTDL = true
			userTDLValue = strings.TrimSpace(val)
		case "tdl":
			existingPaths[normalizePath(val)] = true
		}
	}

	var additions []string
	if !hasUserTDL || !isYes(userTDLValue) {
		additions = append(additions, "User TDL = Yes")
	}
	for _, p := range tdlPaths {
		if existingPaths[normalizePath(p)] {
			continue
		}
		additions = append(additions, "TDL = "+p)
	}

	if len(additions) == 0 {
		return content, false
	}
	out := content
	if !strings.HasSuffix(out, "\n") && len(out) > 0 {
		out += "\r\n"
	}
	out += "\r\n;; Added by TallyWhatsApp installer — safe to remove\r\n"
	out += strings.Join(additions, "\r\n") + "\r\n"
	return out, true
}

// stripTDLs removes TDL lines whose paths live under the given directory
// and clears the `User TDL` if no other TDL lines remain.
func stripTDLs(content, dirAbsLower string) (string, bool) {
	lines := splitLines(content)
	out := make([]string, 0, len(lines))
	changed := false
	tdlsRemaining := 0

	for _, line := range lines {
		key, val, ok := splitKV(line)
		if ok && strings.EqualFold(strings.TrimSpace(key), "tdl") {
			candidate := strings.ToLower(filepath.Clean(strings.TrimSpace(val)))
			if strings.HasPrefix(candidate, dirAbsLower) {
				changed = true
				continue
			}
			tdlsRemaining++
		}
		out = append(out, line)
	}

	if tdlsRemaining == 0 {
		// Flip User TDL back to No so Tally doesn't waste startup
		// looking for files. Don't remove the key entirely — preserves
		// the user's edit history.
		for i, line := range out {
			if k, _, ok := splitKV(line); ok && strings.EqualFold(strings.TrimSpace(k), "user tdl") {
				if !strings.Contains(strings.ToLower(line), "no") {
					out[i] = "User TDL = No"
					changed = true
				}
			}
		}
	}

	if !changed {
		return content, false
	}
	return strings.Join(out, "\r\n"), true
}

// splitKV parses a `key = value` line. Returns ok=false for blank lines,
// comments, and malformed entries.
func splitKV(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	idx := strings.Index(line, "=")
	if idx < 0 {
		return "", "", false
	}
	return line[:idx], line[idx+1:], true
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	// Split on \n but keep \r — Tally writes CRLF on Windows.
	raw := strings.Split(s, "\n")
	for i, line := range raw {
		raw[i] = strings.TrimRight(line, "\r")
	}
	return raw
}

func normalizePath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ToLower(filepath.Clean(s))
}

func isYes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "y", "true", "1":
		return true
	}
	return false
}

// --------- file helpers ---------

func readIniOrEmpty(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func writeAtomic(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func backupFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path+".bak", data, 0o644)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dedupeInstalls(in []*Install) []*Install {
	seen := map[string]bool{}
	out := make([]*Install, 0, len(in))
	for _, ins := range in {
		key := strings.ToLower(filepath.Clean(ins.InstallDir))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ins)
	}
	return out
}

// bestEffortVersion is a placeholder. Reading file version info from
// tally.exe requires Win32 API calls; we return "" until that's wired up.
func bestEffortVersion(_ string) string { return "" }
