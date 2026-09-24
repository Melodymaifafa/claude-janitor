package janitor

import (
	"bytes"
	"fmt"
	"math/rand"
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

// TestPairingNeverKillsAWritingSession is the whole point of the ticket, as a
// property rather than a case list: across thousands of random session batches
// -- random launch spacing, random transcript creation lag, random survivors,
// and the transcripts of exited siblings left lying around as the real machine
// leaves them -- a process whose OWN transcript was just written must never be
// selected for kill.
//
// Generation stays inside the regime the design supports: batches where the
// transcripts were born in the same order the processes started. Outside it the
// birth order itself lies, and pairing.go documents that no timestamp-only rule
// can recover the truth there.
func TestPairingNeverKillsAWritingSession(t *testing.T) {
	rng := rand.New(rand.NewSource(20260924))
	j := New(Config{ProjectsDir: t.TempDir(), DryRun: true}, &bytes.Buffer{})
	now := time.Now()
	cutoff := now.Add(-120 * time.Minute)
	base := now.Add(-4 * time.Hour)

	checked := 0
	for iter := 0; iter < 4000; iter++ {
		sessions := 2 + rng.Intn(4)

		// Launch the batch, each session's transcript born after its own start.
		// Zero spacing and a coarse birth-time grid are deliberate: they produce
		// the equal-timestamp groups where no order exists to be read.
		grid := time.Duration(1+rng.Intn(6)) * time.Second
		starts := make([]time.Time, sessions)
		births := make([]time.Time, sessions)
		at := base
		for i := 0; i < sessions; i++ {
			at = at.Add(time.Duration(rng.Intn(21)) * time.Second)
			starts[i] = at
			born := at.Add(time.Duration(4+rng.Intn(11)) * time.Second)
			births[i] = born.Truncate(grid)
		}
		inverted := false
		for i := 1; i < sessions; i++ {
			if births[i].Before(births[i-1]) {
				inverted = true
			}
		}
		if inverted {
			continue // outside the supported regime; see the doc comment
		}

		// Some sessions are still writing; some exited and left a stale transcript.
		alive := make([]bool, sessions)
		writing := make([]bool, sessions)
		var procs []procInfo
		var trs []transcriptInfo
		for i := 0; i < sessions; i++ {
			alive[i] = rng.Intn(2) == 0
			writing[i] = alive[i] && rng.Intn(2) == 0
			mtime := births[i].Add(time.Duration(rng.Intn(60)) * time.Second) // long stale
			if writing[i] {
				mtime = now.Add(-time.Duration(rng.Intn(30)) * time.Second)
			}
			// A random name key: on disk, path order tells you nothing about
			// creation order, and a generator whose paths happen to sort in launch
			// order would hide exactly that.
			trs = append(trs, transcriptInfo{
				btime: births[i],
				mtime: mtime,
				path:  fmt.Sprintf("/p/%04d-sess-%02d.jsonl", rng.Intn(10000), i),
			})
			if alive[i] {
				procs = append(procs, bgProc(int32(1000+i), starts[i]))
			}
		}
		if len(procs) == 0 {
			continue
		}

		verdicts := j.pairDesktop(procs, trs)
		for i := 0; i < sessions; i++ {
			if !writing[i] {
				continue
			}
			checked++
			c, ok := verdicts[int32(1000+i)].decisive()
			if ok && !c.tr.mtime.After(cutoff) {
				t.Fatalf("iter %d: session %d is still writing but was judged idle\nstarts=%v\nbirths=%v\nalive=%v writing=%v\nchosen=%s mtime=%v",
					iter, i, starts, births, alive, writing, c.tr.path, c.tr.mtime)
			}
		}
	}
	if checked < 500 {
		t.Fatalf("only %d active sessions exercised; the generator is not covering the case", checked)
	}
	t.Logf("checked %d active sessions across random batches", checked)
}

// TestPairDesktopEqualStartsAreInterchangeable: two desktop processes whose start
// timestamps land in the same millisecond carry no launch order at all -- process
// ids are not launch order on any platform. So neither may be condemned while
// either transcript of the pair is still being written; once both go quiet, both
// processes are collectable again.
//
// Inputs are built in memory so this runs identically on every OS.
func TestPairDesktopEqualStartsAreInterchangeable(t *testing.T) {
	now := time.Now()
	cutoff := now.Add(-120 * time.Minute)
	start := now.Add(-4 * time.Hour)

	build := func(secondMtime time.Time) map[int32]pairVerdict {
		j := New(Config{ProjectsDir: t.TempDir(), DryRun: true}, &bytes.Buffer{})
		trs := []transcriptInfo{
			{btime: start.Add(9 * time.Second), mtime: start.Add(30 * time.Second), path: "/p/first.jsonl"},
			{btime: start.Add(12 * time.Second), mtime: secondMtime, path: "/p/second.jsonl"},
		}
		// Identical createdAt: gopsutil reports whole milliseconds.
		procs := []procInfo{bgProc(950001, start), bgProc(950002, start)}
		return j.pairDesktop(procs, trs)
	}

	// One of the two is still writing -- nobody may be judged idle.
	for pid, v := range build(now) {
		c, ok := v.decisive()
		if ok && !c.tr.mtime.After(cutoff) {
			t.Errorf("PID=%d judged idle on %s while a sibling transcript is being written",
				pid, c.tr.path)
		}
	}

	// Both quiet -- both must still be collectable, or a same-millisecond batch
	// would leak forever.
	for pid, v := range build(start.Add(40 * time.Second)) {
		c, ok := v.decisive()
		if !ok || c.tr.mtime.After(cutoff) {
			t.Errorf("PID=%d not collectable although every candidate transcript is stale (ok=%v)", pid, ok)
		}
	}
}

// TestPairDesktopEqualBirthTimesAreInterchangeable: the mirror of the case above
// on the transcript side. A coarse filesystem timestamp can give two transcripts
// the same birth time, and the sort then has only their paths to go on -- which
// says nothing about who created them. So while either is being written, neither
// of the two processes may be condemned; once both go quiet, both are collectable.
func TestPairDesktopEqualBirthTimesAreInterchangeable(t *testing.T) {
	now := time.Now()
	cutoff := now.Add(-120 * time.Minute)
	base := now.Add(-4 * time.Hour)
	born := base.Add(10 * time.Second) // one timestamp for both transcripts

	build := func(secondMtime time.Time) map[int32]pairVerdict {
		j := New(Config{ProjectsDir: t.TempDir(), DryRun: true}, &bytes.Buffer{})
		trs := []transcriptInfo{
			{btime: born, mtime: base.Add(30 * time.Second), path: "/p/aaa.jsonl"},
			{btime: born, mtime: secondMtime, path: "/p/bbb.jsonl"},
		}
		procs := []procInfo{bgProc(960001, base), bgProc(960002, base.Add(4*time.Second))}
		return j.pairDesktop(procs, trs)
	}

	for pid, v := range build(now) {
		c, ok := v.decisive()
		if ok && !c.tr.mtime.After(cutoff) {
			t.Errorf("PID=%d judged idle on %s while a same-birth-time transcript is being written",
				pid, c.tr.path)
		}
	}

	for pid, v := range build(base.Add(40 * time.Second)) {
		c, ok := v.decisive()
		if !ok || c.tr.mtime.After(cutoff) {
			t.Errorf("PID=%d not collectable although every candidate transcript is stale (ok=%v)", pid, ok)
		}
	}
}
