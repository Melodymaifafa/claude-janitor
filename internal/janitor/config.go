package janitor

import "time"

// Config holds the tunable knobs for one scan-and-kill pass.
// Defaults mirror the proven Mac reference implementation
// (~/.claude/scripts/claude-session-janitor.sh): idle 120 min, interval 30 min.
type Config struct {
	// IdleThreshold: a session whose transcript/json has not been written for
	// longer than this is considered dead. Default 120 min.
	IdleThreshold time.Duration

	// ScanInterval: how often the scheduler (MEL-93) re-runs a pass. The
	// kill-logic slice only records it; the scheduler consumes it. Default 30 min.
	ScanInterval time.Duration

	// MatchTolerance: max allowed gap between a B-class json's creation time and
	// a candidate process start time when reverse-matching. Default 240s.
	MatchTolerance time.Duration

	// DryRun: when true, print "would kill X" and never actually kill.
	DryRun bool

	// ProjectsDir overrides the A-class transcript root (default ~/.claude/projects).
	ProjectsDir string

	// SessionsDir overrides the B-class desktop session root (per-OS default).
	SessionsDir string
}

// Defaults returns the reference-aligned configuration.
func Defaults() Config {
	return Config{
		IdleThreshold:  120 * time.Minute,
		ScanInterval:   30 * time.Minute,
		MatchTolerance: 240 * time.Second,
		DryRun:         false,
	}
}
