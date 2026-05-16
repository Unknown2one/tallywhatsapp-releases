// Command tallywa-installer-helper is the small CLI the WiX installer
// invokes for custom actions that are awkward to express in WiX itself
// — namely the tally.ini patcher and unpatcher.
//
// Subcommands:
//
//	patch-tally-ini   --tdl-dir <abs-path>
//	unpatch-tally-ini --tdl-dir <abs-path>
//	detect-tally
//
// All three return non-zero only on hard failure (a Detect call that
// returns ErrNotInstalled is reported on stdout and the process exits
// 0 — a fresh laptop with no Tally is not an installer error). This
// means a user who buys TallyWhatsApp before installing Tally still
// gets a clean install and the patcher self-heals on the next run.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tallywa/tallywa/internal/tally"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "patch-tally-ini":
		os.Exit(runPatch(os.Args[2:]))
	case "unpatch-tally-ini":
		os.Exit(runUnpatch(os.Args[2:]))
	case "detect-tally":
		os.Exit(runDetect(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `tallywa-installer-helper — custom-action helper for the WiX installer

Subcommands:
  patch-tally-ini   --tdl-dir <abs-path>   add our TDL files to every detected tally.ini
  unpatch-tally-ini --tdl-dir <abs-path>   remove our TDL entries from every detected tally.ini
  detect-tally                             print discovered TallyPrime installs as JSON

All subcommands exit 0 when no Tally is installed; the installer treats
that as a deferred patch (we'll re-run on next boot of the service).`)
}

func runDetect(args []string) int {
	fs := flag.NewFlagSet("detect-tally", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	installs, err := tally.Detect()
	if errors.Is(err, tally.ErrNotInstalled) {
		fmt.Println("[]")
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "detect:", err)
		return 1
	}
	out, _ := json.MarshalIndent(installs, "", "  ")
	fmt.Println(string(out))
	return 0
}

// runPatch finds every TallyPrime install, locates the TDL files we
// just laid down at --tdl-dir, and patches each tally.ini to load them.
// We patch every install we find — there's no "install for me only"
// option because the customer expects the buttons to appear in every
// company file Tally opens.
func runPatch(args []string) int {
	fs := flag.NewFlagSet("patch-tally-ini", flag.ContinueOnError)
	tdlDir := fs.String("tdl-dir", "", "absolute path to our TDL directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *tdlDir == "" || !filepath.IsAbs(*tdlDir) {
		fmt.Fprintln(os.Stderr, "patch-tally-ini: --tdl-dir must be an absolute path")
		return 2
	}
	tdls, err := listTDLs(*tdlDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "list tdls:", err)
		return 1
	}
	if len(tdls) == 0 {
		fmt.Fprintln(os.Stderr, "patch-tally-ini: no .tdl files in", *tdlDir)
		return 1
	}

	installs, err := tally.Detect()
	if errors.Is(err, tally.ErrNotInstalled) {
		fmt.Println("no TallyPrime install found; skipping patch")
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "detect:", err)
		return 1
	}

	hadFailure := false
	for _, inst := range installs {
		changed, err := tally.PatchIni(inst, tdls)
		if err != nil {
			fmt.Fprintf(os.Stderr, "patch %s: %v\n", inst.IniPath, err)
			hadFailure = true
			continue
		}
		if changed {
			fmt.Printf("patched %s\n", inst.IniPath)
		} else {
			fmt.Printf("already up to date: %s\n", inst.IniPath)
		}
	}
	if hadFailure {
		return 1
	}
	return 0
}

func runUnpatch(args []string) int {
	fs := flag.NewFlagSet("unpatch-tally-ini", flag.ContinueOnError)
	tdlDir := fs.String("tdl-dir", "", "absolute path to our TDL directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *tdlDir == "" || !filepath.IsAbs(*tdlDir) {
		fmt.Fprintln(os.Stderr, "unpatch-tally-ini: --tdl-dir must be an absolute path")
		return 2
	}

	installs, err := tally.Detect()
	if errors.Is(err, tally.ErrNotInstalled) {
		// Nothing to unpatch. Don't fail the uninstaller for this.
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "detect:", err)
		return 1
	}

	hadFailure := false
	for _, inst := range installs {
		changed, err := tally.UnpatchIni(inst, *tdlDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "unpatch %s: %v\n", inst.IniPath, err)
			hadFailure = true
			continue
		}
		if changed {
			fmt.Printf("cleaned %s\n", inst.IniPath)
		}
	}
	if hadFailure {
		return 1
	}
	return 0
}

// listTDLs returns absolute paths to every .tdl file in dir, ignoring
// dot-files. Sorted so the output is stable across machines (otherwise
// MSI minor-version diffs would churn).
func listTDLs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !strings.EqualFold(filepath.Ext(e.Name()), ".tdl") {
			continue
		}
		abs, err := filepath.Abs(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, abs)
	}
	return out, nil
}
