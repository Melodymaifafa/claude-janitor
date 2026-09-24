package janitor

import (
	"os"
	"path/filepath"
)

// defaultProjectsDir returns the transcript root, used by both session
// classes. Identical on all three OSes: ~/.claude/projects (Windows resolves
// ~ to %USERPROFILE%). See design.md §2.A.
func defaultProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}
