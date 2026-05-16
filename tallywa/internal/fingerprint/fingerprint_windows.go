//go:build windows

package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Compute returns a stable SHA-256 hex of (machine GUID + first physical MAC).
//
// MachineGuid is created by Windows at install time and persists across reboots
// and user changes. Combining it with a physical MAC raises the bar against
// trivial cloning of a single registry value.
func Compute() (string, error) {
	guid, err := machineGUID()
	if err != nil {
		return "", fmt.Errorf("read machine guid: %w", err)
	}
	mac, err := primaryMAC()
	if err != nil {
		return "", fmt.Errorf("read primary mac: %w", err)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(guid + "|" + mac)))
	return hex.EncodeToString(sum[:]), nil
}

func machineGUID() (string, error) {
	k, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Cryptography`,
		registry.QUERY_VALUE|registry.WOW64_64KEY,
	)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return "", err
	}
	return v, nil
}

// primaryMAC returns the lowest-name physical interface MAC.
// Sorting by name keeps the result stable even if Windows reorders interfaces.
func primaryMAC() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	type cand struct {
		name string
		mac  string
	}
	var cands []cand
	for _, ifi := range ifaces {
		if ifi.HardwareAddr == nil || len(ifi.HardwareAddr) == 0 {
			continue
		}
		if ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		// Skip virtual adapters by common name prefixes. Not perfect, but
		// excludes the worst offenders (VirtualBox, VMware, Hyper-V, WSL).
		lower := strings.ToLower(ifi.Name)
		if strings.HasPrefix(lower, "vethernet") ||
			strings.Contains(lower, "virtual") ||
			strings.Contains(lower, "vpn") ||
			strings.Contains(lower, "loopback") ||
			strings.HasPrefix(lower, "wsl") {
			continue
		}
		cands = append(cands, cand{ifi.Name, ifi.HardwareAddr.String()})
	}
	if len(cands) == 0 {
		return "", fmt.Errorf("no physical network interface found")
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].name < cands[j].name })
	return cands[0].mac, nil
}
