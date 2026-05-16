//go:build windows

package handshake

import (
	"encoding/hex"
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// ErrNotPublished means the service hasn't written its handshake values
// yet. The tray polls and surfaces a "service starting" state instead of
// failing hard.
var ErrNotPublished = errors.New("handshake: service has not published port/secret")

// PublishPort writes the loopback port to the registry. Idempotent.
func PublishPort(port int) error {
	k, _, err := registry.CreateKey(
		registry.LOCAL_MACHINE,
		RegistryPath,
		registry.SET_VALUE|registry.WOW64_64KEY,
	)
	if err != nil {
		return fmt.Errorf("handshake: open key: %w", err)
	}
	defer k.Close()
	if err := k.SetDWordValue(ValuePort, uint32(port)); err != nil {
		return fmt.Errorf("handshake: set Port: %w", err)
	}
	return nil
}

// PublishVersion writes the service version (informational only).
func PublishVersion(v string) error {
	k, _, err := registry.CreateKey(
		registry.LOCAL_MACHINE,
		RegistryPath,
		registry.SET_VALUE|registry.WOW64_64KEY,
	)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(ValueVersion, v)
}

// LoadOrCreateSecret reads the HMAC secret, generating one on first run.
// Returns the raw bytes for use with hmac.New.
func LoadOrCreateSecret(generate func() ([]byte, error)) ([]byte, error) {
	k, _, err := registry.CreateKey(
		registry.LOCAL_MACHINE,
		RegistryPath,
		registry.QUERY_VALUE|registry.SET_VALUE|registry.WOW64_64KEY,
	)
	if err != nil {
		return nil, fmt.Errorf("handshake: open key: %w", err)
	}
	defer k.Close()

	if existing, _, err := k.GetStringValue(ValueSecret); err == nil && existing != "" {
		raw, decErr := hex.DecodeString(existing)
		if decErr == nil && len(raw) == 32 {
			return raw, nil
		}
		// Malformed — fall through and regenerate.
	}

	secret, err := generate()
	if err != nil {
		return nil, err
	}
	if len(secret) != 32 {
		return nil, fmt.Errorf("handshake: secret must be 32 bytes, got %d", len(secret))
	}
	if err := k.SetStringValue(ValueSecret, hex.EncodeToString(secret)); err != nil {
		return nil, fmt.Errorf("handshake: set Secret: %w", err)
	}
	return secret, nil
}

// Load returns the currently-published port + secret. Used by tray and
// support tooling that need to talk to the running service. Returns
// ErrNotPublished when either value is absent — the tray treats that as
// "service not running yet" rather than a hard error.
func Load() (port int, secret []byte, version string, err error) {
	k, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		RegistryPath,
		registry.QUERY_VALUE|registry.WOW64_64KEY,
	)
	if err != nil {
		return 0, nil, "", ErrNotPublished
	}
	defer k.Close()

	rawPort, _, err := k.GetIntegerValue(ValuePort)
	if err != nil {
		return 0, nil, "", ErrNotPublished
	}
	rawSecret, _, err := k.GetStringValue(ValueSecret)
	if err != nil || rawSecret == "" {
		return 0, nil, "", ErrNotPublished
	}
	secret, err = hex.DecodeString(rawSecret)
	if err != nil || len(secret) != 32 {
		return 0, nil, "", fmt.Errorf("handshake: secret malformed")
	}
	version, _, _ = k.GetStringValue(ValueVersion)
	return int(rawPort), secret, version, nil
}
