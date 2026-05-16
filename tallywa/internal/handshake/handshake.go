// Package handshake publishes the loopback port and HMAC secret so the
// Tally COM DLL (running in Tally's process) can find and authenticate
// against the service. The DLL reads from HKLM at construction time.
//
// Why HKLM and not HKCU: the service runs as LocalSystem and the DLL
// runs in Tally (which can be any user). HKCU isn't visible across
// users, so the secret has to live machine-wide.
//
// Why not a file: file paths are user-locale-dependent and PROGRAMDATA
// is sometimes redirected. Registry keys are universal.
package handshake

const (
	// RegistryPath is the HKLM subkey owning all our handshake values.
	RegistryPath = `SOFTWARE\TallyWhatsApp`

	// ValuePort is a DWORD: the loopback TCP port the service is bound to.
	// Re-published every time the service starts because we bind to
	// port 0 (OS picks a free port).
	ValuePort = "Port"

	// ValueSecret is a hex-encoded 32-byte HMAC secret. Generated once at
	// install time, persisted across restarts. The DLL's signature
	// computation reads this value.
	ValueSecret = "Secret"

	// ValueVersion is the running service version (informational).
	ValueVersion = "Version"
)
