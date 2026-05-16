// Command tallywa-updater is the silent update agent. It is invoked
// once a day from a Scheduled Task installed alongside the service.
//
// Update protocol:
//
//  1. GET <manifest URL> — JSON describing the latest release.
//  2. Verify the manifest's Ed25519 signature with the embedded
//     UpdatePublicKey. A bad signature aborts the run; we never blindly
//     trust the network.
//  3. Compare semver. If newer, download the MSI to %TEMP%.
//  4. Verify the MSI's SHA-256 against the manifest. If we got a bad
//     blob (TLS strip + cache-poisoned mirror), abort.
//  5. Run `msiexec /i <msi> /qn /norestart`. The MSI is signed too
//     (EV cert), so Windows shows no UAC prompt for an upgrade and
//     the user notices nothing beyond a brief tray hiccup.
//
// Failure-mode policy: every error here is a no-op. The updater
// already-installed binary keeps working. We never partial-apply.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Build-time variables. Set via -ldflags by Build-Installer.ps1.
//
// UpdatePublicKey is base64(Ed25519 public key, 32 bytes). Empty means
// updates are disabled — the binary still ships in the MSI but exits
// quickly with a log line so the scheduled task doesn't spam errors.
var (
	Version            = "dev"
	UpdatePublicKey    = "" // base64
	DefaultManifestURL = "https://updates.variantstudio.in/stable/manifest.json"
)

func main() {
	if err := run(); err != nil {
		// Print to stderr but exit 0. The scheduled task's job is "try
		// once a day"; a failed run is not a Windows error.
		fmt.Fprintln(os.Stderr, "tallywa-updater:", err)
	}
}

func run() error {
	manifestURL := flag.String("manifest", DefaultManifestURL, "manifest URL")
	verbose := flag.Bool("verbose", false, "log to stderr")
	dryRun := flag.Bool("dry-run", false, "download + verify but skip msiexec")
	flag.Parse()

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	if UpdatePublicKey == "" {
		logger.Info("update public key is empty; updates disabled in this build")
		return nil
	}
	pub, err := base64.StdEncoding.DecodeString(UpdatePublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("update public key invalid: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	manifest, err := fetchManifest(ctx, *manifestURL, ed25519.PublicKey(pub), logger)
	if err != nil {
		return err
	}

	if !isNewer(manifest.Version, Version) {
		logger.Info("up to date", "current", Version, "latest", manifest.Version)
		return nil
	}
	logger.Info("update available", "current", Version, "latest", manifest.Version)

	msiPath, err := downloadMSI(ctx, manifest, logger)
	if err != nil {
		return err
	}
	defer os.Remove(msiPath)

	if *dryRun {
		logger.Info("dry-run: skipping msiexec", "msi", msiPath)
		return nil
	}
	return installMSI(ctx, msiPath, logger)
}

// updateManifest is the JSON contract between the build pipeline and
// every TallyWhatsApp install in the field.
//
// The signature covers the canonical message:
//
//	version + "\n" + msi_url + "\n" + msi_sha256 + "\n" + min_version
//
// Verifying against version+url+sha256+min_version stops a malicious
// CDN from rolling back to an older signed manifest (would-be downgrade
// attack). min_version is checked against UpdaterMinSupportedVersion
// below — any future protocol break sets it to bump out old updaters.
type updateManifest struct {
	Version    string `json:"version"`
	MsiURL     string `json:"msi_url"`
	MsiSHA256  string `json:"msi_sha256"`
	MinVersion string `json:"min_version"`
	Signature  string `json:"signature"` // base64
}

// UpdaterMinSupportedVersion is bumped only for protocol breaks. The
// build pipeline sets manifest.min_version to whatever the oldest
// updater that can safely process the current manifest is. If our
// build-time Version is older than that, we abstain — a newer updater
// will pull this manifest after the next user-driven install.
const UpdaterMinSupportedVersion = "0.0.0"

func fetchManifest(ctx context.Context, url string, pub ed25519.PublicKey, logger *slog.Logger) (*updateManifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tallywa-updater/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}
	var m updateManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("manifest parse: %w", err)
	}
	if !verifyManifest(&m, pub) {
		return nil, errors.New("manifest signature mismatch")
	}
	if m.MinVersion != "" && isNewer(m.MinVersion, Version) {
		logger.Info("manifest requires newer updater; abstaining", "min", m.MinVersion, "ours", Version)
		return nil, errors.New("updater older than manifest min_version")
	}
	return &m, nil
}

func verifyManifest(m *updateManifest, pub ed25519.PublicKey) bool {
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	canonical := []byte(m.Version + "\n" + m.MsiURL + "\n" + m.MsiSHA256 + "\n" + m.MinVersion)
	return ed25519.Verify(pub, canonical, sig)
}

func downloadMSI(ctx context.Context, m *updateManifest, logger *slog.Logger) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.MsiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "tallywa-updater/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch msi: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("msi http %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "tallywa-update-*.msi")
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	// Cap download size at 256 MiB. The MSI is 25-50 MiB; anything
	// larger than this is suspicious and we'd rather abort than fill the
	// user's disk on a poisoned manifest.
	limited := io.LimitReader(resp.Body, 256<<20)
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), limited); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, m.MsiSHA256) {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("msi sha256 mismatch: got %s, want %s", got, m.MsiSHA256)
	}
	logger.Info("msi downloaded", "path", tmp.Name(), "bytes", strconv.FormatInt(headerLen(resp), 10))
	return tmp.Name(), nil
}

func headerLen(r *http.Response) int64 {
	if cl := r.ContentLength; cl > 0 {
		return cl
	}
	return -1
}

func installMSI(ctx context.Context, msi string, logger *slog.Logger) error {
	logger.Info("running msiexec", "path", msi)
	// /qn = silent, /norestart = never reboot the user's PC mid-day.
	// REINSTALLMODE=amus forces overwrite of all files even when the
	// MSI version is identical (defensive — should never happen because
	// we already gated on isNewer).
	cmd := exec.CommandContext(ctx,
		"msiexec.exe",
		"/i", msi,
		"/qn",
		"/norestart",
		"REINSTALLMODE=amus",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("msiexec: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --------- semver helpers ---------

// isNewer reports whether `a` is strictly greater than `b` using a
// permissive semver compare. Non-numeric segments compare lexically.
//
// We don't pull in golang.org/x/mod/semver because the manifest's
// version may be a build stamp like "1.0.43" (no leading v) that the
// stricter parsers reject.
func isNewer(a, b string) bool {
	return cmpVersion(a, b) > 0
}

func cmpVersion(a, b string) int {
	as := splitVersion(a)
	bs := splitVersion(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var av, bv string
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		ai, aerr := strconv.Atoi(av)
		bi, berr := strconv.Atoi(bv)
		if aerr == nil && berr == nil {
			if ai != bi {
				if ai > bi {
					return 1
				}
				return -1
			}
			continue
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
}

func splitVersion(v string) []string {
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return nil
	}
	return strings.Split(v, ".")
}
