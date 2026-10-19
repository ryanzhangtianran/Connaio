// Package daemon installs connaio-core as a root service (a LaunchDaemon on macOS), which enhanced
// mode (TUN) needs.
//
// Everything root touches is root-owned: the binary is copied out of the (user-writable) app
// bundle, and the daemon's data dir and socket live under /Library on macOS. The GUI sends configs as
// payloads instead of paths, so the root process never follows paths inside the user's home.
package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
)

// BootConfig is what the daemon runs while no GUI is attached: no TUN, nothing captured.
const BootConfig = "mixed-port: 7890\nmode: rule\nrules:\n  - MATCH,DIRECT\n"

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func fileSum(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sum(b), nil
}
