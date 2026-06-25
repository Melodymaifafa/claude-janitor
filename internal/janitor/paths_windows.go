//go:build windows

package janitor

import (
	"os"
	"path/filepath"
)

// platformSessionsDir: Windows native (MSIX) desktop-app session root.
// BEST KNOWN - confirm on target OS (design.md §2.B, open item #2):
// MSIX virtualizes %APPDATA%\Claude into a package-local LocalCache path.
// The package id (Claude_pzs8sxrjxfjjc) may change by version, so on-box
// confirmation is required; override with --sessions-dir if it differs.
func platformSessionsDir() string {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		local = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(local,
		"Packages", "Claude_pzs8sxrjxfjjc",
		"LocalCache", "Roaming", "Claude", "claude-code-sessions")
}
