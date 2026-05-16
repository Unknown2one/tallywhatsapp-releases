//go:build !windows

package handshake

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNotPublished means the service hasn't written its handshake values
// yet. Mirrors the Windows error for tray cross-platform compatibility.
var ErrNotPublished = errors.New("handshake: service has not published port/secret")

// On non-Windows the "registry" is a flat file under TALLYWA_DATA so we
// can develop and integration-test the wiring on a Mac or Linux box.
// Production never uses this path.

func devPath() string {
	root := os.Getenv("TALLYWA_DATA")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".tallywa")
	}
	return filepath.Join(root, "handshake.txt")
}

func readDev() (map[string]string, error) {
	data, err := os.ReadFile(devPath())
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[k] = v
	}
	return out, nil
}

func writeDev(m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(devPath()), 0o755); err != nil {
		return err
	}
	var sb strings.Builder
	for k, v := range m {
		fmt.Fprintf(&sb, "%s=%s\n", k, v)
	}
	return os.WriteFile(devPath(), []byte(sb.String()), 0o600)
}

func PublishPort(port int) error {
	m, err := readDev()
	if err != nil {
		return err
	}
	m[ValuePort] = strconv.Itoa(port)
	return writeDev(m)
}

func PublishVersion(v string) error {
	m, err := readDev()
	if err != nil {
		return err
	}
	m[ValueVersion] = v
	return writeDev(m)
}

func LoadOrCreateSecret(generate func() ([]byte, error)) ([]byte, error) {
	m, err := readDev()
	if err != nil {
		return nil, err
	}
	if existing := m[ValueSecret]; existing != "" {
		if raw, err := hex.DecodeString(existing); err == nil && len(raw) == 32 {
			return raw, nil
		}
	}
	secret, err := generate()
	if err != nil {
		return nil, err
	}
	if len(secret) != 32 {
		return nil, fmt.Errorf("handshake: secret must be 32 bytes, got %d", len(secret))
	}
	m[ValueSecret] = hex.EncodeToString(secret)
	if err := writeDev(m); err != nil {
		return nil, err
	}
	return secret, nil
}

// Load mirrors the Windows variant for the tray.
func Load() (port int, secret []byte, version string, err error) {
	m, err := readDev()
	if err != nil {
		return 0, nil, "", ErrNotPublished
	}
	rawPort := m[ValuePort]
	rawSecret := m[ValueSecret]
	if rawPort == "" || rawSecret == "" {
		return 0, nil, "", ErrNotPublished
	}
	port, err = strconv.Atoi(rawPort)
	if err != nil {
		return 0, nil, "", ErrNotPublished
	}
	secret, err = hex.DecodeString(rawSecret)
	if err != nil || len(secret) != 32 {
		return 0, nil, "", fmt.Errorf("handshake: secret malformed")
	}
	return port, secret, m[ValueVersion], nil
}
