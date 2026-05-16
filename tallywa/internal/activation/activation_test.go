package activation

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tallywa/tallywa/internal/license"
)

// fakeIssuer reproduces the issuer's /redeem behaviour without spinning
// up a real DB. Lets us drive each error path deterministically.
type fakeIssuer struct {
	priv               ed25519.PrivateKey
	expectFingerprint  string
	tokenStatus        int   // overrides 200 if non-zero
	signWithDifferent  bool  // if true, sign with a key the client doesn't trust
	respond            func(w http.ResponseWriter, r *http.Request)
	customLicense      *license.License
	redemptionsLeft    int
	isReactivation     bool
}

func (f *fakeIssuer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.respond != nil {
			f.respond(w, r)
			return
		}
		var req redeemRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if f.expectFingerprint != "" && req.Fingerprint != f.expectFingerprint {
			http.Error(w, "wrong fingerprint", http.StatusBadRequest)
			return
		}
		if f.tokenStatus != 0 {
			http.Error(w, "no", f.tokenStatus)
			return
		}
		signKey := f.priv
		if f.signWithDifferent {
			_, signKey, _ = ed25519.GenerateKey(rand.Reader)
		}
		lic := f.customLicense
		if lic == nil {
			lic = &license.License{
				LicenseID:        "lic_test",
				CustomerEmail:    "x@example.com",
				Fingerprint:      req.Fingerprint,
				Edition:          license.EditionPro,
				Features:         []string{license.FeatureSales, license.FeatureLedger},
				MaxReactivations: 3,
				IssuedAt:         time.Now(),
			}
		}
		signed, err := (&license.Signer{PrivateKey: signKey}).Sign(lic)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(redeemResponse{
			License:              base64.StdEncoding.EncodeToString(signed),
			LicenseID:            lic.LicenseID,
			Edition:              lic.Edition,
			IsReactivation:       f.isReactivation,
			RedemptionsRemaining: f.redemptionsLeft,
		})
	})
}

func newClient(t *testing.T, srv *httptest.Server, pub ed25519.PublicKey, fp string) *Client {
	t.Helper()
	c := New(srv.URL, pub)
	c.Fingerprint = func() (string, error) { return fp, nil }
	return c
}

func TestActivate_HappyPath(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fakeI := &fakeIssuer{priv: priv, expectFingerprint: "fp-abc", redemptionsLeft: 2}
	srv := httptest.NewServer(fakeI.handler())
	t.Cleanup(srv.Close)

	c := newClient(t, srv, pub, "fp-abc")
	licPath := filepath.Join(t.TempDir(), "license.dat")

	res, err := c.Activate(context.Background(), "TWA-AAAA-BBBB-CCCC", licPath)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if res.License.Edition != license.EditionPro {
		t.Errorf("Edition = %q, want pro", res.License.Edition)
	}
	if res.RedemptionsRemaining != 2 {
		t.Errorf("RedemptionsRemaining = %d, want 2", res.RedemptionsRemaining)
	}

	// File should be readable+verifiable independently.
	on_disk, err := license.Read(licPath)
	if err != nil {
		t.Fatalf("Read written license: %v", err)
	}
	if _, err := (&license.Verifier{PublicKey: pub}).Verify(on_disk, "fp-abc"); err != nil {
		t.Errorf("written license fails verification: %v", err)
	}
}

func TestActivate_TokenNotFound(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv := httptest.NewServer((&fakeIssuer{priv: priv, tokenStatus: http.StatusNotFound}).handler())
	t.Cleanup(srv.Close)

	c := newClient(t, srv, pub, "fp-abc")
	_, err := c.Activate(context.Background(), "TWA-XXXX", filepath.Join(t.TempDir(), "license.dat"))
	if !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("err = %v, want ErrTokenNotFound", err)
	}
}

func TestActivate_TokenExhausted(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv := httptest.NewServer((&fakeIssuer{priv: priv, tokenStatus: http.StatusConflict}).handler())
	t.Cleanup(srv.Close)

	c := newClient(t, srv, pub, "fp-abc")
	_, err := c.Activate(context.Background(), "TWA-XXXX", filepath.Join(t.TempDir(), "license.dat"))
	if !errors.Is(err, ErrTokenExhausted) {
		t.Errorf("err = %v, want ErrTokenExhausted", err)
	}
}

func TestActivate_IssuerOffline(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	c := New("http://127.0.0.1:1", pub) // port 1 is reserved/refused
	c.Fingerprint = func() (string, error) { return "fp", nil }
	c.HTTP.Timeout = time.Second

	_, err := c.Activate(context.Background(), "TWA-XXXX", filepath.Join(t.TempDir(), "license.dat"))
	if !errors.Is(err, ErrIssuerOffline) {
		t.Errorf("err = %v, want ErrIssuerOffline", err)
	}
}

func TestActivate_RejectsMisSignedLicense(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fakeI := &fakeIssuer{priv: priv, signWithDifferent: true}
	srv := httptest.NewServer(fakeI.handler())
	t.Cleanup(srv.Close)

	c := newClient(t, srv, pub, "fp-abc")
	licPath := filepath.Join(t.TempDir(), "license.dat")
	_, err := c.Activate(context.Background(), "TWA-XXXX", licPath)
	if !errors.Is(err, ErrTampered) {
		t.Errorf("err = %v, want ErrTampered", err)
	}
	// Critical: we must NOT have written the bad license to disk.
	if _, err := license.Read(licPath); !errors.Is(err, license.ErrNotActivated) {
		t.Errorf("license.dat should not exist after failed verify")
	}
}

func TestActivate_FingerprintBindingEnforced(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	// Issuer signs a license bound to a different fingerprint than the
	// client computed. The client's local Verify call must catch this.
	fakeI := &fakeIssuer{
		priv: priv,
		customLicense: &license.License{
			LicenseID:   "lic_evil",
			Fingerprint: "wrong-pc",
			Edition:     license.EditionPro,
			IssuedAt:    time.Now(),
		},
	}
	srv := httptest.NewServer(fakeI.handler())
	t.Cleanup(srv.Close)

	c := newClient(t, srv, pub, "fp-real")
	_, err := c.Activate(context.Background(), "TWA-XXXX", filepath.Join(t.TempDir(), "license.dat"))
	if !errors.Is(err, ErrTampered) {
		t.Errorf("err = %v, want ErrTampered (fingerprint mismatch)", err)
	}
}
