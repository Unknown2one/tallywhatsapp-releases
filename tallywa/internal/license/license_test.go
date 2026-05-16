package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"
)

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	return pub, priv
}

func sampleLicense() *License {
	return &License{
		LicenseID:        "lic_abc123",
		CustomerEmail:    "test@example.com",
		Fingerprint:      "fingerprint-aaa",
		Edition:          EditionPro,
		Features:         []string{FeatureSales, FeatureReceipt, FeatureLedger},
		MaxReactivations: 3,
		IssuedAt:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestSignAndVerify_RoundTrip(t *testing.T) {
	pub, priv := keypair(t)
	signer := &Signer{PrivateKey: priv}
	verifier := &Verifier{PublicKey: pub}

	raw, err := signer.Sign(sampleLicense())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	got, err := verifier.Verify(raw, "fingerprint-aaa")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.LicenseID != "lic_abc123" {
		t.Errorf("LicenseID = %q, want lic_abc123", got.LicenseID)
	}
	if !got.HasFeature(FeatureLedger) {
		t.Errorf("expected ledger feature")
	}
	if got.HasFeature(FeatureWhitelabel) {
		t.Errorf("did not expect whitelabel feature")
	}
}

func TestVerify_FingerprintMismatch(t *testing.T) {
	pub, priv := keypair(t)
	raw, _ := (&Signer{PrivateKey: priv}).Sign(sampleLicense())

	_, err := (&Verifier{PublicKey: pub}).Verify(raw, "different-pc")
	if err != ErrFingerprintMismatch {
		t.Errorf("err = %v, want ErrFingerprintMismatch", err)
	}
}

func TestVerify_TamperedPayload(t *testing.T) {
	pub, priv := keypair(t)
	raw, _ := (&Signer{PrivateKey: priv}).Sign(sampleLicense())

	// Flip a byte inside the JSON payload (after the 5-byte header).
	tampered := append([]byte(nil), raw...)
	tampered[10] ^= 0xFF

	_, err := (&Verifier{PublicKey: pub}).Verify(tampered, "fingerprint-aaa")
	if err != ErrSignatureMismatch {
		t.Errorf("err = %v, want ErrSignatureMismatch", err)
	}
}

func TestVerify_WrongKey(t *testing.T) {
	_, priv := keypair(t)
	raw, _ := (&Signer{PrivateKey: priv}).Sign(sampleLicense())

	otherPub, _ := keypair(t)
	_, err := (&Verifier{PublicKey: otherPub}).Verify(raw, "fingerprint-aaa")
	if err != ErrSignatureMismatch {
		t.Errorf("err = %v, want ErrSignatureMismatch", err)
	}
}

func TestDecode_TruncatedFile(t *testing.T) {
	_, err := (&Verifier{PublicKey: make(ed25519.PublicKey, ed25519.PublicKeySize)}).
		Verify([]byte{1, 2, 3}, "fp")
	if err != ErrInvalidFormat {
		t.Errorf("err = %v, want ErrInvalidFormat", err)
	}
}

func TestDecode_UnknownVersion(t *testing.T) {
	pub := make(ed25519.PublicKey, ed25519.PublicKeySize)
	bogus := make([]byte, 1+4+10+ed25519.SignatureSize)
	bogus[0] = 99 // unsupported version byte
	_, err := (&Verifier{PublicKey: pub}).Verify(bogus, "fp")
	if err == nil || err == ErrSignatureMismatch {
		t.Errorf("err = %v, want format error", err)
	}
}

func TestReadWrite_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "license.dat")

	want := []byte("signed-bytes-here")
	if err := Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("Read = %q, want %q", got, want)
	}
}

func TestRead_NotActivated(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "missing"))
	if err != ErrNotActivated {
		t.Errorf("err = %v, want ErrNotActivated", err)
	}
}
