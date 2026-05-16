// Package license implements offline license verification for the
// plug-and-play model: the issuer signs a payload with an Ed25519 private
// key; the service embeds the matching public key and verifies on every
// startup. After activation, the service never contacts the network for
// licensing.
//
// Wire format (little-endian section sizes intentionally avoided in favor
// of big-endian for portability):
//
//	+-----------+-----------------+-------------+----------------+
//	| version=1 | payload_len(4)  | payload     | signature(64)  |
//	+-----------+-----------------+-------------+----------------+
//
// payload is canonical JSON of License. signature is Ed25519 over payload.
package license

import (
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	formatVersion byte = 1

	// EditionLite/Pro/Business mirror the SKUs in the pricing model.
	EditionLite     = "lite"
	EditionPro      = "pro"
	EditionBusiness = "business"

	FeatureSales      = "sales"
	FeatureReceipt    = "receipt"
	FeatureLedger     = "ledger"
	FeaturePrint      = "print"
	FeatureMultiCo    = "multi_co"
	FeatureWhitelabel = "whitelabel"
)

// Sentinel errors. Callers branch on these to drive UX (ErrNotActivated
// flips the tray to the activation screen; ErrFingerprintMismatch shows a
// "license belongs to another PC" message).
var (
	ErrNotActivated        = errors.New("license: not activated")
	ErrInvalidFormat       = errors.New("license: invalid format")
	ErrSignatureMismatch   = errors.New("license: signature mismatch")
	ErrFingerprintMismatch = errors.New("license: fingerprint mismatch")
	ErrExpired             = errors.New("license: expired")
)

// License is the signed payload. Field tags are stable forever — renaming
// breaks every issued license in the field.
type License struct {
	LicenseID        string    `json:"license_id"`
	CustomerEmail    string    `json:"customer_email"`
	Fingerprint      string    `json:"fingerprint"`
	Edition          string    `json:"edition"`
	Features         []string  `json:"features"`
	MaxReactivations int       `json:"max_reactivations"`
	IssuedAt         time.Time `json:"issued_at"`
	// ExpiresAt zero-value means "never". Used for the optional
	// updates-subscription period, not for restricting message sending.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// HasFeature reports whether the licensed edition includes the named feature.
func (l *License) HasFeature(name string) bool {
	for _, f := range l.Features {
		if f == name {
			return true
		}
	}
	return false
}

// Verifier holds the embedded public key. In production the key is set at
// build time via -ldflags. Tests inject their own.
type Verifier struct {
	PublicKey ed25519.PublicKey
	// Now is overridable for tests; defaults to time.Now.
	Now func() time.Time
}

// Verify decodes, signature-checks, and binds the license to the calling
// machine's fingerprint. It does NOT enforce ExpiresAt for messaging — the
// caller decides whether expiry gates a feature or just shows a banner.
func (v *Verifier) Verify(raw []byte, fingerprint string) (*License, error) {
	if len(v.PublicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("license: verifier has invalid public key length %d", len(v.PublicKey))
	}
	payload, sig, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(v.PublicKey, payload, sig) {
		return nil, ErrSignatureMismatch
	}
	var lic License
	if err := json.Unmarshal(payload, &lic); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFormat, err)
	}
	if lic.Fingerprint != fingerprint {
		return nil, ErrFingerprintMismatch
	}
	return &lic, nil
}

// Signer holds the issuer-side private key. Lives only in the issuer service.
type Signer struct {
	PrivateKey ed25519.PrivateKey
}

// Sign serializes the license and produces the signed wire blob.
func (s *Signer) Sign(lic *License) ([]byte, error) {
	if len(s.PrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("license: signer has invalid private key length %d", len(s.PrivateKey))
	}
	payload, err := json.Marshal(lic)
	if err != nil {
		return nil, fmt.Errorf("license: marshal: %w", err)
	}
	sig := ed25519.Sign(s.PrivateKey, payload)
	return encode(payload, sig), nil
}

func encode(payload, sig []byte) []byte {
	out := make([]byte, 0, 1+4+len(payload)+len(sig))
	out = append(out, formatVersion)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	out = append(out, lenBuf[:]...)
	out = append(out, payload...)
	out = append(out, sig...)
	return out
}

func decode(raw []byte) (payload, sig []byte, err error) {
	if len(raw) < 1+4+ed25519.SignatureSize {
		return nil, nil, ErrInvalidFormat
	}
	if raw[0] != formatVersion {
		return nil, nil, fmt.Errorf("%w: unknown version %d", ErrInvalidFormat, raw[0])
	}
	plen := binary.BigEndian.Uint32(raw[1:5])
	if int(plen) > len(raw)-5-ed25519.SignatureSize {
		return nil, nil, ErrInvalidFormat
	}
	payload = raw[5 : 5+plen]
	sig = raw[5+plen : 5+plen+ed25519.SignatureSize]
	return payload, sig, nil
}

// Read returns the raw signed bytes from disk. Returns ErrNotActivated if
// the file does not exist so callers can branch cleanly.
func Read(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotActivated
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Write atomically writes the license to disk. Atomic write avoids a
// half-written file if the user pulls the plug mid-activation.
func Write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
