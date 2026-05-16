// Package activation contains the desktop-side counterpart to the issuer
// service. It collects the machine fingerprint, POSTs the redeem request,
// verifies the returned license against the embedded public key, and
// persists license.dat to disk.
//
// Failure modes are first-class: the tray UI distinguishes
// "wrong/used token" from "no internet" from "fingerprint changed".
//
// Apps Script compatibility:
//
//   - The redeem URL is built as <BaseURL>?action=redeem so a
//     deployment URL like .../exec can be used without modification.
//   - Apps Script web apps respond to POST with a 302 redirect to
//     googleusercontent.com. Go's default redirect handling switches
//     POST → GET on 302 (RFC compliant) which would silently drop the
//     body. We override CheckRedirect to preserve the POST method and
//     re-attach the body via Request.GetBody.
//   - Apps Script can't return non-200 HTTP status codes; errors come
//     back as 200 with {"error": "<sentinel>"} in the body. We map
//     both that pattern AND traditional HTTP-status-code errors to the
//     same Go sentinels, so a future migration to a real HTTP server
//     is a no-op.
package activation

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tallywa/tallywa/internal/fingerprint"
	"github.com/tallywa/tallywa/internal/license"
)

// Client redeems activation tokens against the issuer.
type Client struct {
	BaseURL     string
	HTTP        *http.Client
	PublicKey   ed25519.PublicKey
	Fingerprint func() (string, error) // overridable for tests
}

// New returns a client with sensible HTTP timeouts. Default 20-second
// total budget — enough for a slow Indian DSL link, short enough that
// the tray UI doesn't appear hung.
func New(baseURL string, publicKey ed25519.PublicKey) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP: &http.Client{
			Timeout: 20 * time.Second,
		},
		PublicKey:   publicKey,
		Fingerprint: fingerprint.Compute,
	}
}

// Result is what Activate returns to the tray UI.
type Result struct {
	License              *license.License
	RawSigned            []byte
	IsReactivation       bool
	RedemptionsRemaining int
}

// Public sentinel errors. The tray UI maps each to a localized message.
var (
	ErrTokenNotFound  = errors.New("activation: token not found")
	ErrTokenExhausted = errors.New("activation: token has no activations remaining")
	ErrIssuerOffline  = errors.New("activation: issuer unreachable")
	ErrTampered       = errors.New("activation: signed license failed verification")
)

// Activate redeems key, verifies the returned license, and writes the
// signed bytes to licensePath. Caller is responsible for hot-reloading
// the service's in-memory license state after this returns.
func (c *Client) Activate(ctx context.Context, key, licensePath string) (*Result, error) {
	fp, err := c.Fingerprint()
	if err != nil {
		return nil, fmt.Errorf("activation: fingerprint: %w", err)
	}
	body, err := json.Marshal(redeemRequest{
		Key:         strings.TrimSpace(strings.ToUpper(key)),
		Fingerprint: fp,
	})
	if err != nil {
		return nil, err
	}

	url := c.BaseURL
	if strings.Contains(url, "?") {
		url += "&action=redeem"
	} else {
		url += "?action=redeem"
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIssuerOffline, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// Traditional HTTP-status errors (real HTTP servers).
	switch resp.StatusCode {
	case http.StatusOK:
		// fall through, parse body
	case http.StatusNotFound:
		return nil, ErrTokenNotFound
	case http.StatusConflict:
		return nil, ErrTokenExhausted
	default:
		return nil, fmt.Errorf("activation: issuer returned %d: %s", resp.StatusCode, snippet(respBody))
	}

	var r redeemResponse
	if err := json.Unmarshal(respBody, &r); err != nil {
		return nil, fmt.Errorf("activation: parse response: %w (body: %s)", err, snippet(respBody))
	}

	// Apps Script-style errors (200 + error in body).
	if r.Error != "" {
		switch r.Error {
		case "token_not_found":
			return nil, ErrTokenNotFound
		case "token_exhausted", "token_revoked":
			return nil, ErrTokenExhausted
		default:
			return nil, fmt.Errorf("activation: issuer error: %s", r.Error)
		}
	}
	if r.License == "" {
		return nil, fmt.Errorf("activation: empty license in response: %s", snippet(respBody))
	}

	signed, err := base64.StdEncoding.DecodeString(r.License)
	if err != nil {
		return nil, fmt.Errorf("activation: decode license: %w", err)
	}

	verifier := &license.Verifier{PublicKey: c.PublicKey}
	lic, err := verifier.Verify(signed, fp)
	if err != nil {
		// If the issuer signed something we can't verify, the issuer is
		// either misconfigured or compromised. Either way: refuse to
		// persist it.
		return nil, fmt.Errorf("%w: %v", ErrTampered, err)
	}

	if err := license.Write(licensePath, signed); err != nil {
		return nil, fmt.Errorf("activation: write license: %w", err)
	}

	return &Result{
		License:              lic,
		RawSigned:            signed,
		IsReactivation:       r.IsReactivation,
		RedemptionsRemaining: r.RedemptionsRemaining,
	}, nil
}

type redeemRequest struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
}

type redeemResponse struct {
	Success              bool   `json:"success,omitempty"`
	Error                string `json:"error,omitempty"`
	License              string `json:"license,omitempty"`
	LicenseID            string `json:"license_id,omitempty"`
	Edition              string `json:"edition,omitempty"`
	IsReactivation       bool   `json:"is_reactivation,omitempty"`
	RedemptionsRemaining int    `json:"redemptions_remaining,omitempty"`
}

func snippet(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "…"
	}
	return string(b)
}
