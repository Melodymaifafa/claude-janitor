package janitor

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestJanitor returns a dry-run janitor rooted at a temp ProjectsDir and a
// helper that creates a transcript with a given birth and mtime (btime <=
// mtime, like a real append-only transcript). macOS birth time only moves
// DOWN: setting an older mtime drags birth along, and a later Chtimes lifts
// mtime back while birth stays -- which is exactly how we decouple the two.
func newTestJanitor(t *testing.T) (*Janitor, func(name string, btime, mtime time.Time) string) {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "-Users-x-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	j := New(Config{ProjectsDir: root, DryRun: true}, &bytes.Buffer{})
	mk := func(name string, btime, mtime time.Time) string {
		p := filepath.Join(proj, name)
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, btime, btime); err != nil { // drags birth down to btime
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil { // lifts mtime; birth stays
			t.Fatal(err)
		}
		return p
	}
	return j, mk
}

func bgProc(pid int32, createdAt time.Time) procInfo {
	return procInfo{
		pid:       pid,
		cmdline:   []string{"claude", "--output-format", "stream-json", "--verbose"},
		createdAt: createdAt,
	}
}

// TestFindTranscript verifies the projects/*/<uuid>.jsonl glob.
func TestFindTranscript(t *testing.T) {
	j, mk := newTestJanitor(t)
	uuid := "12345678-1234-1234-1234-1234567890ab"
	now := time.Now()
	tf := mk(uuid+".jsonl", now, now)

	if got := j.findTranscript(uuid); got != tf {
		t.Errorf("findTranscript = %q, want %q", got, tf)
	}
	if got := j.findTranscript("00000000-0000-0000-0000-000000000000"); got != "" {
		t.Errorf("expected empty for missing uuid, got %q", got)
	}
}

// TestStatTimesIdleVsActive verifies mtime drives the idle/active decision.
func TestStatTimesIdleVsActive(t *testing.T) {
	_, mk := newTestJanitor(t)
	now := time.Now()
	old := now.Add(-3 * time.Hour)
	idle := mk("idle.jsonl", old, old)
	active := mk("active.jsonl", now, now)

	cutoff := time.Now().Add(-120 * time.Minute)

	ftIdle, err := statTimes(idle)
	if err != nil {
		t.Fatal(err)
	}
	if !ftIdle.mtime.Before(cutoff) {
		t.Error("3h-old file should be before cutoff (idle)")
	}
	if ftIdle.btime.IsZero() {
		t.Error("btime should never be zero (falls back to mtime)")
	}

	ftActive, err := statTimes(active)
	if err != nil {
		t.Fatal(err)
	}
	if ftActive.mtime.Before(cutoff) {
		t.Error("just-written file should be after cutoff (active)")
	}
}

// TestScanDesktopIdleKilledActiveSpared: a paired process is killed only when
// its transcript's mtime is stale.
func TestScanDesktopIdleKilledActiveSpared(t *testing.T) {
	if !birthTimeSupported(t) {
		t.Skip("no birth-time support on this filesystem")
	}
	j, mk := newTestJanitor(t)
	now := time.Now()
	cutoff := now.Add(-120 * time.Minute)

	// Idle: born 3h ago, silent since. Active: born 1h ago, written just now.
	mk("idle-sess.jsonl", now.Add(-3*time.Hour), now.Add(-3*time.Hour))
	mk("active-sess.jsonl", now.Add(-1*time.Hour), now)

	// Each process started ~10s before its transcript was born.
	procs := []procInfo{
		bgProc(111111, now.Add(-3*time.Hour-10*time.Second)),
		bgProc(222222, now.Add(-1*time.Hour-10*time.Second)),
	}

	var res Result
	j.scanDesktop(procs, now, cutoff, &res)

	if res.Killed != 1 || res.Spared != 1 || res.Skipped != 0 {
		t.Errorf("got killed=%d spared=%d skipped=%d, want 1/1/0", res.Killed, res.Spared, res.Skipped)
	}
}

// TestScanDesktopUnpairableNeverKilled: a process with no transcript born near
// its start time (e.g. a re-opened session appending an old transcript) is
// skipped, never killed -- even if every transcript on disk is stale.
func TestScanDesktopUnpairableNeverKilled(t *testing.T) {
	if !birthTimeSupported(t) {
		t.Skip("no birth-time support on this filesystem")
	}
	j, mk := newTestJanitor(t)
	now := time.Now()
	cutoff := now.Add(-120 * time.Minute)

	// A stale transcript born far from the process start: the re-opened
	// session's new process must not adopt it.
	mk("old-sess.jsonl", now.Add(-5*time.Hour), now.Add(-5*time.Hour))

	procs := []procInfo{bgProc(111111, now.Add(-1*time.Hour))} // no transcript born within its window

	var res Result
	j.scanDesktop(procs, now, cutoff, &res)

	if res.Killed != 0 || res.Skipped != 1 {
		t.Errorf("got killed=%d skipped=%d, want killed=0 skipped=1", res.Killed, res.Skipped)
	}
}

// TestScanDesktopConflictClosestWins: two processes inside the pairing window
// of ONE transcript -- only the closer one claims it; the loser must be
// skipped, not killed with the same transcript's idle time (the old
// local-json design double-killed exactly this way).
func TestScanDesktopConflictClosestWins(t *testing.T) {
	if !birthTimeSupported(t) {
		t.Skip("no birth-time support on this filesystem")
	}
	j, mk := newTestJanitor(t)
	now := time.Now()
	cutoff := now.Add(-120 * time.Minute)

	born := now.Add(-3 * time.Hour)
	mk("only-sess.jsonl", born, born) // stale -> its true owner dies

	procs := []procInfo{
		bgProc(111111, born.Add(-5*time.Second)),   // gap ~5s -> wins the claim
		bgProc(222222, born.Add(-100*time.Second)), // gap ~100s -> loses, skipped
	}

	var res Result
	j.scanDesktop(procs, now, cutoff, &res)

	if res.Killed != 1 || res.Skipped != 1 {
		t.Errorf("got killed=%d skipped=%d, want killed=1 skipped=1", res.Killed, res.Skipped)
	}
}

// TestScanDesktopIgnoresNonBClass: --resume sessions and non-stream-json
// processes are not desktop candidates at all.
func TestScanDesktopIgnoresNonBClass(t *testing.T) {
	j, mk := newTestJanitor(t)
	now := time.Now()
	cutoff := now.Add(-120 * time.Minute)
	old := now.Add(-3 * time.Hour)
	mk("sess.jsonl", old, old)

	procs := []procInfo{
		{pid: 1, cmdline: []string{"claude", "--resume", "12345678-1234-1234-1234-1234567890ab"}, createdAt: old},
		{pid: 2, cmdline: []string{"claude"}, createdAt: old},
	}

	var res Result
	j.scanDesktop(procs, now, cutoff, &res)

	if res.Killed != 0 || res.Spared != 0 || res.Skipped != 0 {
		t.Errorf("non-B-class processes must be untouched, got %+v", res)
	}
}

// birthTimeSupported probes whether this filesystem exposes real birth times.
func birthTimeSupported(t *testing.T) bool {
	t.Helper()
	p := filepath.Join(t.TempDir(), "probe")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ft, err := statTimes(p)
	if err != nil {
		t.Fatal(err)
	}
	return ft.hasBtime
}
