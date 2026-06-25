// Package scheduler registers and removes the periodic scan job on each OS's
// native scheduler. It owns only step 4 of the design (docs/design.md §4):
// "when does the tool auto-run", NOT the scan/kill logic itself.
//
// The scheduler invokes the tool's own scan command. Per the CLI contract in
// README.md, that command is `<binary> run`. If MEL-92 (the parallel kill-logic
// slice) lands a different invocation, only RunArgs below needs to change.
package scheduler

import (
	"fmt"
	"os"
	"runtime"
)

// Config holds everything an Installer needs. Values are resolved by the CLI
// layer (current executable path, defaults) and passed in, so the scheduler
// package stays free of flag parsing.
type Config struct {
	// BinaryPath is the absolute path to the claude-janitor executable that the
	// scheduler will invoke on each tick.
	BinaryPath string

	// RunArgs are the arguments appended after BinaryPath. Default {"run"} per
	// the README CLI contract; MEL-92 owns the actual `run` implementation.
	RunArgs []string

	// IntervalMinutes is how often the scan runs. Default 30 (mirrors the Mac
	// reference plist's 1800s StartInterval).
	IntervalMinutes int

	// Label is the scheduler-visible identifier of the job. It MUST be unique
	// per install so tests never collide with a real production agent.
	// Default: "com.maicuigua.claude-janitor".
	Label string

	// LogPath is where the job's stdout/stderr go. Empty means the installer
	// picks a per-OS default under the user's home/.claude/logs.
	LogPath string
}

// Installer registers (Install) and deregisters (Uninstall) the periodic job.
// One implementation per OS, selected by For().
type Installer interface {
	// Install writes the scheduler artifact and activates it (run at
	// login/boot + every IntervalMinutes). Idempotent: a second Install
	// replaces the previous registration.
	Install(cfg Config) error
	// Uninstall deactivates and removes the artifact completely. Returns nil if
	// nothing was installed (safe to call repeatedly).
	Uninstall(cfg Config) error
	// Describe returns a human-readable, one-line summary of where the job
	// lives, for the CLI to print after install/uninstall.
	Describe(cfg Config) string
}

// DefaultLabel is the reverse-DNS identifier used when Config.Label is empty.
const DefaultLabel = "com.maicuigua.claude-janitor"

// DefaultIntervalMinutes mirrors the reference launchd plist (1800s).
const DefaultIntervalMinutes = 30

// For returns the Installer for the current OS, or an error on unsupported
// platforms. Linux picks systemd when available and falls back to crontab.
func For() (Installer, error) {
	switch runtime.GOOS {
	case "darwin":
		return launchdInstaller{}, nil
	case "linux":
		if systemdAvailable() {
			return systemdInstaller{}, nil
		}
		return cronInstaller{}, nil
	case "windows":
		return schtasksInstaller{}, nil
	default:
		return nil, fmt.Errorf("scheduler: unsupported OS %q", runtime.GOOS)
	}
}

// resolve fills in defaults for any unset Config field. Called by every
// installer at the top of Install/Uninstall so behavior is consistent.
func (c Config) resolve() Config {
	out := c
	if out.Label == "" {
		out.Label = DefaultLabel
	}
	if out.IntervalMinutes <= 0 {
		out.IntervalMinutes = DefaultIntervalMinutes
	}
	if len(out.RunArgs) == 0 {
		out.RunArgs = []string{"run"}
	}
	return out
}

// homeDir returns the user's home directory, used for default log paths.
func homeDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("scheduler: cannot resolve home dir: %w", err)
	}
	return h, nil
}
