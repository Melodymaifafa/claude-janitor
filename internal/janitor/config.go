package janitor

import "time"

// Config holds the tunable knobs for one scan-and-kill pass.
type Config struct {
	// IdleThreshold: a session whose transcript has not been written for longer
	// than this is considered abandoned. Default 120 min.
	IdleThreshold time.Duration

	// ScanInterval: how often the scheduler (MEL-93) re-runs a pass. The
	// kill-logic slice only records it; the scheduler consumes it. Default 30 min.
	ScanInterval time.Duration

	// PairBefore/PairAfter bound the transcript-birth vs process-start gap when
	// pairing desktop sessions: birth must fall in [start-PairBefore,
	// start+PairAfter]. Measured on-box: transcripts appear 4-14s after process
	// start; PairBefore only absorbs clock rounding. Defaults 15s / 120s.
	PairBefore time.Duration
	PairAfter  time.Duration

	// DryRun: when true, print "would kill X" and never actually kill.
	DryRun bool

	// ProjectsDir overrides the transcript root (default ~/.claude/projects).
	ProjectsDir string
}

// Defaults returns the reference configuration.
func Defaults() Config {
	return Config{
		IdleThreshold: 120 * time.Minute,
		ScanInterval:  30 * time.Minute,
		PairBefore:    15 * time.Second,
		PairAfter:     120 * time.Second,
		DryRun:        false,
	}
}
