//go:build darwin

package janitor

import (
	"os"
	"path/filepath"
)

// platformSessionsDir: macOS desktop-app session root.
// VERIFIED on this Mac (design.md §2.B): two-level UUID nesting under
// ~/Library/Application Support/Claude/claude-code-sessions/.
func platformSessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "Claude", "claude-code-sessions")
}
