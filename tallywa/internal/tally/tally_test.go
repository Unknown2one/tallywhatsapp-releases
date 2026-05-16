package tally

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestDetect_FindsTallyDirViaEnvOverride(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tally.exe"), "fake")
	writeFile(t, filepath.Join(dir, "tally.ini"), "")

	t.Setenv("TALLYWA_TALLY_DIR", dir)

	got, err := Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].InstallDir != dir {
		t.Errorf("InstallDir = %q, want %q", got[0].InstallDir, dir)
	}
}

func TestDetect_NoTallyReturnsErrNotInstalled(t *testing.T) {
	t.Setenv("TALLYWA_TALLY_DIR", filepath.Join(t.TempDir(), "nonexistent"))
	if _, err := Detect(); err != ErrNotInstalled {
		t.Errorf("err = %v, want ErrNotInstalled", err)
	}
}

func TestPatchIni_AddsUserTDLAndTDLLines(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "tally.ini")
	writeFile(t, ini, "Default Companies = No\r\nDefault Server = Yes\r\n")

	tdl := filepath.Join(dir, "TDL", "voucher_send.tdl")
	writeFile(t, tdl, "; sample tdl")

	changed, err := PatchIni(&Install{IniPath: ini, InstallDir: dir}, []string{tdl})
	if err != nil {
		t.Fatalf("PatchIni: %v", err)
	}
	if !changed {
		t.Errorf("changed = false, want true")
	}

	got, _ := os.ReadFile(ini)
	if !strings.Contains(string(got), "User TDL = Yes") {
		t.Errorf("missing User TDL line: %s", got)
	}
	if !strings.Contains(string(got), "TDL = "+tdl) {
		t.Errorf("missing TDL line: %s", got)
	}

	// Original lines preserved.
	if !strings.Contains(string(got), "Default Companies = No") {
		t.Errorf("original key dropped: %s", got)
	}

	// Backup created.
	if _, err := os.Stat(ini + ".bak"); err != nil {
		t.Errorf("backup not created: %v", err)
	}
}

func TestPatchIni_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "tally.ini")
	writeFile(t, ini, "")

	tdl := filepath.Join(dir, "TDL", "voucher_send.tdl")
	writeFile(t, tdl, "; sample tdl")

	if _, err := PatchIni(&Install{IniPath: ini}, []string{tdl}); err != nil {
		t.Fatalf("first PatchIni: %v", err)
	}
	first, _ := os.ReadFile(ini)

	changed, err := PatchIni(&Install{IniPath: ini}, []string{tdl})
	if err != nil {
		t.Fatalf("second PatchIni: %v", err)
	}
	if changed {
		t.Errorf("idempotent run reported changed=true")
	}
	second, _ := os.ReadFile(ini)
	if string(first) != string(second) {
		t.Errorf("file content changed across idempotent runs")
	}
}

func TestPatchIni_PromotesUserTDLFromNo(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "tally.ini")
	writeFile(t, ini, "User TDL = No\r\n")

	tdl := filepath.Join(dir, "TDL", "x.tdl")
	writeFile(t, tdl, "")

	changed, err := PatchIni(&Install{IniPath: ini}, []string{tdl})
	if err != nil || !changed {
		t.Fatalf("PatchIni: changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(ini)
	if !strings.Contains(string(got), "User TDL = Yes") {
		t.Errorf("did not flip User TDL to Yes: %s", got)
	}
}

func TestPatchIni_RejectsRelativeTDLPath(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "tally.ini")
	writeFile(t, ini, "")

	if _, err := PatchIni(&Install{IniPath: ini}, []string{"relative/path.tdl"}); err == nil {
		t.Errorf("expected error for relative path")
	}
}

func TestPatchIni_CreatesIniIfMissing(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "tally.ini")
	tdl := filepath.Join(dir, "x.tdl")
	writeFile(t, tdl, "")

	if _, err := os.Stat(ini); !os.IsNotExist(err) {
		t.Fatalf("precondition: ini should not exist, got %v", err)
	}
	changed, err := PatchIni(&Install{IniPath: ini}, []string{tdl})
	if err != nil {
		t.Fatalf("PatchIni: %v", err)
	}
	if !changed {
		t.Errorf("changed = false, want true")
	}
	got, _ := os.ReadFile(ini)
	if !strings.Contains(string(got), "TDL = "+tdl) {
		t.Errorf("missing TDL line in fresh ini: %s", got)
	}
}

func TestUnpatchIni_RemovesOurTDLsAndFlipsBack(t *testing.T) {
	dir := t.TempDir()
	tdlDir := filepath.Join(dir, "TDL")
	tdl := filepath.Join(tdlDir, "x.tdl")
	writeFile(t, tdl, "")
	otherTDL := filepath.Join(dir, "Other", "y.tdl")
	writeFile(t, otherTDL, "")

	ini := filepath.Join(dir, "tally.ini")
	content := "User TDL = Yes\r\nTDL = " + tdl + "\r\nTDL = " + otherTDL + "\r\n"
	writeFile(t, ini, content)

	changed, err := UnpatchIni(&Install{IniPath: ini}, tdlDir)
	if err != nil {
		t.Fatalf("UnpatchIni: %v", err)
	}
	if !changed {
		t.Errorf("changed = false, want true")
	}
	got, _ := os.ReadFile(ini)
	if strings.Contains(string(got), tdl) {
		t.Errorf("our TDL should have been removed: %s", got)
	}
	if !strings.Contains(string(got), otherTDL) {
		t.Errorf("third-party TDL should be preserved: %s", got)
	}
	// Other TDL still present so User TDL stays Yes.
	if !strings.Contains(string(got), "User TDL = Yes") {
		t.Errorf("User TDL should remain Yes when others present: %s", got)
	}
}

func TestUnpatchIni_FlipsBackToNoWhenAllRemoved(t *testing.T) {
	dir := t.TempDir()
	tdlDir := filepath.Join(dir, "TDL")
	tdl := filepath.Join(tdlDir, "x.tdl")
	writeFile(t, tdl, "")

	ini := filepath.Join(dir, "tally.ini")
	writeFile(t, ini, "User TDL = Yes\r\nTDL = "+tdl+"\r\n")

	if _, err := UnpatchIni(&Install{IniPath: ini}, tdlDir); err != nil {
		t.Fatalf("UnpatchIni: %v", err)
	}
	got, _ := os.ReadFile(ini)
	if !strings.Contains(string(got), "User TDL = No") {
		t.Errorf("expected User TDL = No after removing the only TDL: %s", got)
	}
}

func TestUnpatchIni_NoOpWhenNothingToRemove(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "tally.ini")
	writeFile(t, ini, "Some Other Setting = Yes\r\n")

	changed, err := UnpatchIni(&Install{IniPath: ini}, filepath.Join(dir, "TDL"))
	if err != nil {
		t.Fatalf("UnpatchIni: %v", err)
	}
	if changed {
		t.Errorf("changed = true on no-op")
	}
}

// Comments and blank lines must survive the patch unchanged.
func TestPatchIni_PreservesCommentsAndOrdering(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "tally.ini")
	original := "; my comment\r\n\r\nDefault Companies = No\r\n; another\r\nDefault Server = Yes\r\n"
	writeFile(t, ini, original)

	tdl := filepath.Join(dir, "x.tdl")
	writeFile(t, tdl, "")

	if _, err := PatchIni(&Install{IniPath: ini}, []string{tdl}); err != nil {
		t.Fatalf("PatchIni: %v", err)
	}
	got, _ := os.ReadFile(ini)
	for _, want := range []string{
		"; my comment",
		"Default Companies = No",
		"; another",
		"Default Server = Yes",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q after patch:\n%s", want, got)
		}
	}
}
