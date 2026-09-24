package janitor

import (
	"strings"
	"testing"
	"time"
)

// Regression tests for the two pairing defects found in the MEL-231 R3 review.
// They assert the PID named in the log, not the killed/spared counts: in both
// cases the counts match correct behaviour exactly while the wrong session dies,
// so a count-only test can never catch either bug.

// TestReproConcurrentLaunchSwap: two desktop sessions started 5s apart, each
// transcript born 10s after its own process. Causally p1<->tr1 and p2<->tr2;
// tr1 is still being appended (active), tr2 has been silent 4h.
// Correct behaviour: kill p2, spare p1.
//
// Smallest-gap-first scored the crossing edge |10s-5s| = 5s below either true
// edge (10s) and swapped the pair, so the active session inherited the dead
// one's idle time.
func TestReproConcurrentLaunchSwap(t *testing.T) {
	if !birthTimeSupported(t) {
		t.Skip("no birth-time support on this filesystem")
	}
	j, buf, mk := newTestJanitor(t)
	now := time.Now()
	base := now.Add(-4 * time.Hour)

	p1start := base
	p2start := base.Add(5 * time.Second)

	mk("aaaaaaaa-1111-1111-1111-111111111111.jsonl", p1start.Add(10*time.Second), now)
	mk("bbbbbbbb-2222-2222-2222-222222222222.jsonl", p2start.Add(10*time.Second), p2start.Add(20*time.Second))

	procs := []procInfo{bgProc(900001, p1start), bgProc(900002, p2start)}
	cutoff := now.Add(-120 * time.Minute)
	var res Result
	j.scanDesktop(procs, now, cutoff, &res)

	log := buf.String()
	t.Logf("killed=%d spared=%d skipped=%d\nlog:\n%s", res.Killed, res.Spared, res.Skipped, log)

	if strings.Contains(log, "PID=900001") {
		t.Errorf("REGRESSION: the ACTIVE session (PID=900001) was selected for kill")
	}
	if !strings.Contains(log, "PID=900002") {
		t.Errorf("the IDLE session (PID=900002) was NOT selected for kill")
	}
}

// TestReproTerminalTranscriptStolen: a live terminal session's transcript falls
// inside a desktop process's pairing window and sits closer to its start time
// than the desktop session's own transcript. A transcript with a live owner must
// never be offered to a desktop process.
func TestReproTerminalTranscriptStolen(t *testing.T) {
	if !birthTimeSupported(t) {
		t.Skip("no birth-time support on this filesystem")
	}
	j, buf, mk := newTestJanitor(t)
	now := time.Now()
	base := now.Add(-4 * time.Hour)

	termUUID := "cccccccc-3333-3333-3333-333333333333"
	deskUUID := "dddddddd-4444-4444-4444-444444444444"

	deskStart := base
	mk(termUUID+".jsonl", base.Add(3*time.Second), base.Add(30*time.Second))
	mk(deskUUID+".jsonl", base.Add(10*time.Second), now)

	termProc := procInfo{
		pid:       900003,
		cmdline:   []string{"claude", "--resume", termUUID},
		createdAt: base.Add(-1 * time.Minute),
	}
	procs := []procInfo{bgProc(900004, deskStart), termProc}
	cutoff := now.Add(-120 * time.Minute)
	var res Result
	j.scanDesktop(procs, now, cutoff, &res)

	log := buf.String()
	t.Logf("killed=%d spared=%d skipped=%d\nlog:\n%s", res.Killed, res.Spared, res.Skipped, log)

	if strings.Contains(log, "PID=900004") {
		t.Errorf("REGRESSION: the ACTIVE desktop session (PID=900004) was selected for kill, paired to a terminal session's transcript")
	}
}

// TestPairDesktopKeepsLaunchOrder: the agent pool starts sessions in batches, so
// widen the swap case to three launched 4s apart with only the middle one still
// active. Order must hold across the whole batch, not just a pair.
func TestPairDesktopKeepsLaunchOrder(t *testing.T) {
	if !birthTimeSupported(t) {
		t.Skip("no birth-time support on this filesystem")
	}
	j, buf, mk := newTestJanitor(t)
	now := time.Now()
	base := now.Add(-4 * time.Hour)

	starts := []time.Time{base, base.Add(4 * time.Second), base.Add(8 * time.Second)}
	// Middle session is the active one; the other two went silent 4h ago.
	mtimes := []time.Time{starts[0].Add(30 * time.Second), now, starts[2].Add(30 * time.Second)}
	names := []string{
		"11111111-aaaa-aaaa-aaaa-aaaaaaaaaaaa.jsonl",
		"22222222-bbbb-bbbb-bbbb-bbbbbbbbbbbb.jsonl",
		"33333333-cccc-cccc-cccc-cccccccccccc.jsonl",
	}
	var procs []procInfo
	for i := range starts {
		mk(names[i], starts[i].Add(9*time.Second), mtimes[i])
		procs = append(procs, bgProc(int32(910001+i), starts[i]))
	}

	cutoff := now.Add(-120 * time.Minute)
	var res Result
	j.scanDesktop(procs, now, cutoff, &res)

	log := buf.String()
	t.Logf("killed=%d spared=%d skipped=%d\nlog:\n%s", res.Killed, res.Spared, res.Skipped, log)

	if strings.Contains(log, "PID=910002") {
		t.Errorf("REGRESSION: the ACTIVE session (PID=910002) was selected for kill")
	}
	for _, pid := range []string{"PID=910001", "PID=910003"} {
		if !strings.Contains(log, pid) {
			t.Errorf("idle session %s was NOT selected for kill", pid)
		}
	}
}

// TestScanDesktopReservedTranscriptLeavesProcessUnpairable: when the only
// transcript inside a desktop process's window is owned by a live --resume
// session, the desktop process must fall through to "unpairable" and survive,
// even though that transcript is long stale.
func TestScanDesktopReservedTranscriptLeavesProcessUnpairable(t *testing.T) {
	if !birthTimeSupported(t) {
		t.Skip("no birth-time support on this filesystem")
	}
	j, buf, mk := newTestJanitor(t)
	now := time.Now()
	base := now.Add(-4 * time.Hour)

	termUUID := "eeeeeeee-5555-5555-5555-555555555555"
	mk(termUUID+".jsonl", base.Add(8*time.Second), base.Add(20*time.Second))

	procs := []procInfo{
		bgProc(920001, base),
		{pid: 920002, cmdline: []string{"claude", "--resume", termUUID}, createdAt: base.Add(-time.Minute)},
	}
	cutoff := now.Add(-120 * time.Minute)
	var res Result
	j.scanDesktop(procs, now, cutoff, &res)

	log := buf.String()
	t.Logf("killed=%d spared=%d skipped=%d\nlog:\n%s", res.Killed, res.Spared, res.Skipped, log)

	if res.Killed != 0 || res.Skipped != 1 {
		t.Errorf("got killed=%d skipped=%d, want killed=0 skipped=1", res.Killed, res.Skipped)
	}
	if strings.Contains(log, "PID=920001") && !strings.Contains(log, "skip (unpairable") {
		t.Errorf("desktop process must be skipped, not killed, on a reserved transcript")
	}
}

// TestReservedUUIDsBothResumeForms: --resume is written both space-separated and
// with "=", and only the second form regressed historically.
func TestReservedUUIDsBothResumeForms(t *testing.T) {
	spaced := "aaaaaaaa-0000-0000-0000-000000000001"
	equals := "aaaaaaaa-0000-0000-0000-000000000002"
	held := reservedUUIDs([]procInfo{
		{pid: 1, cmdline: []string{"claude", "--resume", spaced}},
		{pid: 2, cmdline: []string{"claude", "--resume=" + equals}},
		{pid: 3, cmdline: []string{"claude", "--output-format", "stream-json"}},
	})
	if !held[spaced] || !held[equals] {
		t.Errorf("both --resume forms must reserve their transcript, got %v", held)
	}
	if len(held) != 2 {
		t.Errorf("a desktop process must reserve nothing, got %v", held)
	}
}
