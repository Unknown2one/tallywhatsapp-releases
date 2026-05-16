//go:build windows

package tally

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// candidateDirs returns plausible TallyPrime install paths on Windows.
// We probe registry first (most reliable), then well-known filesystem
// locations as a backup.
//
// The TALLYWA_TALLY_DIR override is exclusive — when set we ONLY look
// at that directory. This is what support engineers want when fixing a
// quirky install and what tests need for determinism on a developer
// machine that might have a real TallyPrime locally.
func candidateDirs() []string {
	if env := os.Getenv("TALLYWA_TALLY_DIR"); env != "" {
		return []string{env}
	}

	var dirs []string
	dirs = append(dirs, registryCandidates()...)

	// Filesystem fallbacks. Order matters — TallyPrime is the modern
	// product and what the README targets; older Tally.ERP9 keeps the
	// same TDL contract for our purposes.
	for _, base := range programFilesRoots() {
		dirs = append(dirs,
			filepath.Join(base, "TallyPrime"),
			filepath.Join(base, "Tally Solutions", "TallyPrime"),
			filepath.Join(base, "Tally.ERP9"),
		)
	}
	if sysDrive := os.Getenv("SystemDrive"); sysDrive != "" {
		dirs = append(dirs,
			filepath.Join(sysDrive+`\`, "TallyPrime"),
			filepath.Join(sysDrive+`\`, "Tally"),
		)
	}
	return dirs
}

func programFilesRoots() []string {
	var out []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		if v := os.Getenv(env); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// registryCandidates pulls install dirs from registry keys that Tally
// sometimes (but not always) writes. Best-effort; missing keys are silent.
func registryCandidates() []string {
	var out []string
	keys := []struct {
		root registry.Key
		path string
		val  string
	}{
		// TallyPrime usually creates these.
		{registry.LOCAL_MACHINE, `SOFTWARE\TallyPrime`, "InstallPath"},
		{registry.LOCAL_MACHINE, `SOFTWARE\TallyPrime`, "Path"},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\TallyPrime`, "InstallPath"},
		// ERP9 era.
		{registry.LOCAL_MACHINE, `SOFTWARE\Tally Solutions Pvt. Ltd.\Tally`, "InstallPath"},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Tally Solutions Pvt. Ltd.\Tally`, "InstallPath"},
	}
	for _, k := range keys {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE|registry.WOW64_64KEY)
		if err != nil {
			continue
		}
		v, _, err := key.GetStringValue(k.val)
		key.Close()
		if err == nil && v != "" {
			out = append(out, v)
		}
	}
	return out
}
