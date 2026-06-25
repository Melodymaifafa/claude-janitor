//go:build linux

package janitor

import (
	"os"
	"path/filepath"
)

// platformSessionsDir: Linux desktop-app session root.
// BEST KNOWN - confirm on target OS (design.md §2.B, open item #1):
// Claude desktop on Linux uses ~/.config/Claude/; the claude-code-sessions
// subtree is assumed identical to Mac. Respects XDG_CONFIG_HOME when set.
func platformSessionsDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "Claude", "claude-code-sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "Claude", "claude-code-sessions")
}
