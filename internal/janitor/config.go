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

	// PairBefore/PairAfter bound the transcript-birth vs process-start gap for
	// the sessions that make no direct claim (see claim.go): birth must fall in
	// [start-PairBefore, start+PairAfter]. Defaults 15s / 120s.
	//
	// MEL-237 measured the real lag over 2001 desktop sessions on this Mac: 1972
	// transcripts appeared within 15s of their session starting, 11 more within
	// 30s, and NOT ONE between 30s and 120s. That says a 30s window would lose
	// nothing -- FOR DESKTOP SESSIONS. It is deliberately NOT applied, because
	// those are exactly the sessions that now name their own transcript and
	// never reach this window. The processes that do reach it are the
	// stream-json sessions something other than the desktop app started, and
	// they were never in that sample: a live one on this Mac had its two
	// candidate transcripts appear 40.7s and 67.5s after it started, so a 30s
	// window would have made it permanently uncollectable. Narrowing on evidence
	// drawn from the wrong population is a guess wearing a measurement's
	// clothes, so the window stays where it was and the fix comes from the
	// claim instead.
	PairBefore time.Duration
	PairAfter  time.Duration

	// DryRun: when true, print "would kill X" and never actually kill.
	DryRun bool

	// ProjectsDir overrides the transcript root (default ~/.claude/projects).
	ProjectsDir string

	// SessionsDir overrides where the desktop app keeps its per-session records,
	// which turn a process's claimed host session id into a transcript id (see
	// claim.go). Empty or absent means claims do not resolve and the desktop
	// scan degrades to the working-directory filter plus the timestamp window.
	SessionsDir string
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
