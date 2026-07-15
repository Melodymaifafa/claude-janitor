package janitor

import (
	"os"
	"path/filepath"
)

// defaultProjectsDir returns the A-class transcript root.
// Identical on all three OSes: ~/.claude/projects (Windows resolves ~ to
// %USERPROFILE%). See design.md §2.A.
func defaultProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// defaultSessionsDir resolves the B-class desktop-app session root. The
// implementation is per-OS (see paths_darwin.go / paths_linux.go /
// paths_windows.go) because this is the only genuinely OS-divergent path
// (design.md §2.B).
func defaultSessionsDir() string {
	return platformSessionsDir()
}
