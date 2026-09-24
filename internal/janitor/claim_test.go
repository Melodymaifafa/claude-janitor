package janitor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

// The two scenarios in this file are the ones timestamps cannot separate. Both
// run on REAL processes and REAL files: the transcripts are created in the
// order that produces the failure and carry the birth times the filesystem
// gives them, and the sessions are live processes whose environment is read the
// same way a real pass reads it. In-memory fixtures would prove nothing here,
// because what is under test is whether the evidence can be collected at all.
//
// Each scenario is run twice against the SAME code. The "before" arm points the
// janitor at an empty session-records directory, so no claim resolves and the
// decision falls back to the timestamp window -- which is precisely the tool's
// behaviour before MEL-237. The "after" arm supplies the records. The assertion
// is that the before arm kills the session that is actively writing and the
// after arm does not.

// fakeSessionEnv turns this test binary into a stand-in for a desktop session
// process: it sleeps for the given number of seconds and exits, running no
// tests, so realProc can copy it under the name "claude".
const fakeSessionEnv = "JANITOR_FAKE_SESSION_SECONDS"

func TestMain(m *testing.M) {
	if v := os.Getenv(fakeSessionEnv); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			os.Exit(2)
		}
		time.Sleep(time.Duration(n) * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// realProc starts a live process that looks like a desktop session: argv[0] is
// a binary named "claude", it carries --output-format stream-json and no
// --resume, its working directory is workdir, and its environment names hostID
// as the desktop app's session id. It returns the procInfo a real pass would
// build for it, with the start time and working directory read back off the
// running process rather than invented.
func realProc(t *testing.T, bindir, workdir, hostID string) procInfo {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the claim route is unsupported on Windows; there is nothing here to exercise")
	}
	exe := filepath.Join(bindir, "claude")
	if _, err := os.Stat(exe); err != nil {
		// A real, unsigned binary named "claude". Two things rule out the
		// obvious alternatives: macOS rewrites a shebang script's argv[0] to the
		// interpreter, hiding the name the session filter matches on, and it
		// refuses to show the argv or environment of an Apple-signed binary such
		// as /bin/sh to a non-root reader, which is the very thing under test.
		// This test binary is neither.
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(self)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// TestMain turns fakeSessionEnv into "sleep, then exit", so the copy stays
	// alive carrying the desktop-session flags in its own argv without ever
	// running a test.
	cmd := exec.Command(exe, "--output-format", "stream-json", "--verbose")
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), hostSessionEnvVar+"="+hostID, fakeSessionEnv+"=120")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	pid := int32(cmd.Process.Pid)
	p, err := process.NewProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := p.CreateTime()
	if err != nil {
		t.Fatal(err)
	}
	cmdline, err := p.CmdlineSlice()
	if err != nil {
		t.Fatal(err)
	}
	if !isClaudeSession(cmdline) || !hasStreamJSON(cmdline) {
		t.Fatalf("spawned process does not look like a desktop session: %v", cmdline)
	}
	return procInfo{pid: pid, cmdline: cmdline, createdAt: time.UnixMilli(ms)}
}

// writeTranscript creates a transcript NOW, so its birth time is real, and
// records cwd the way Claude Code does. The creation ORDER of these calls is
// what the inverted-birth-order scenario turns on, so callers must not reorder
// them. Birth time is never faked: on macOS os.Chtimes can only drag it down,
// and a stale-but-recently-born transcript is not a thing the filesystem can
// represent -- the scenarios shorten the idle threshold instead.
func writeTranscript(t *testing.T, projectDir, id, cwd string) string {
	t.Helper()
	p := filepath.Join(projectDir, id+".jsonl")
	line, err := json.Marshal(map[string]any{"sessionId": id, "cwd": cwd, "type": "user"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// writeSessionRecord writes the desktop app's record of one session: the file
// whose name is the host session id and whose cliSessionId names the transcript.
func writeSessionRecord(t *testing.T, sessionsDir, hostID, cliID, cwd string) {
	t.Helper()
	dir := filepath.Join(sessionsDir, "acct", "org")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"cliSessionId": cliID, "cwd": cwd, "sessionId": hostID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, hostID+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func touchNow(t *testing.T, path string) {
	t.Helper()
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
}

// claimScenario holds the directories one scenario runs in.
type claimScenario struct {
	projectDir  string
	sessionsDir string
	emptyDir    string
	workdir     string
	bindir      string
}

func newClaimScenario(t *testing.T) claimScenario {
	t.Helper()
	if !birthTimeSupported(t) {
		t.Skip("no birth-time support on this filesystem")
	}
	root := t.TempDir()
	s := claimScenario{
		projectDir:  filepath.Join(root, "projects", "-tmp-work"),
		sessionsDir: filepath.Join(root, "sessions"),
		emptyDir:    filepath.Join(root, "no-records"),
		workdir:     filepath.Join(root, "work"),
		bindir:      filepath.Join(root, "bin"),
	}
	for _, d := range []string{s.projectDir, s.sessionsDir, s.emptyDir, s.workdir, s.bindir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// run executes one desktop pass and returns which pids it decided to kill.
// Always dry-run: the processes are real and must survive the test.
func (s claimScenario) run(t *testing.T, sessionsDir string, idle, before, after time.Duration, procs []procInfo) map[int32]bool {
	t.Helper()
	buf := &bytes.Buffer{}
	j := New(Config{
		ProjectsDir:   filepath.Dir(s.projectDir),
		SessionsDir:   sessionsDir,
		IdleThreshold: idle,
		PairBefore:    before,
		PairAfter:     after,
		DryRun:        true,
	}, buf)

	now := time.Now()
	var res Result
	j.scanDesktop(procs, now, now.Add(-idle), &res)

	killed := map[int32]bool{}
	for _, p := range procs {
		if bytes.Contains(buf.Bytes(), []byte(fmt.Sprintf("would kill (desktop) PID=%d ", p.pid))) {
			killed[p.pid] = true
		}
	}
	t.Logf("pass log (sessionsDir=%q):\n%s", filepath.Base(sessionsDir), buf.String())
	return killed
}

// TestReproInvertedBirthOrderKillsActiveSession is failure mode 1: two sessions
// launched in a batch whose transcripts appeared in the opposite order, because
// the creation lag varies (measured 2.5s to 14.4s on this Mac) by more than the
// launch spacing. The birth order then says the opposite of the truth, the
// pairing hands the live session the dead one's transcript, and kills it
// mid-task. No timing rule can recover from this; the claim can.
func TestReproInvertedBirthOrderKillsActiveSession(t *testing.T) {
	s := newClaimScenario(t)
	const (
		hostActive = "local_11111111-1111-1111-1111-111111111111"
		hostIdle   = "local_22222222-2222-2222-2222-222222222222"
		idActive   = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
		idIdle     = "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"
	)

	// The active session starts FIRST.
	active := realProc(t, s.bindir, s.workdir, hostActive)
	time.Sleep(150 * time.Millisecond)
	idle := realProc(t, s.bindir, s.workdir, hostIdle)

	// ...but its transcript appears SECOND. That is the inversion.
	idleTr := writeTranscript(t, s.projectDir, idIdle, s.workdir)
	time.Sleep(150 * time.Millisecond)
	activeTr := writeTranscript(t, s.projectDir, idActive, s.workdir)

	writeSessionRecord(t, s.sessionsDir, hostActive, idActive, s.workdir)
	writeSessionRecord(t, s.sessionsDir, hostIdle, idIdle, s.workdir)

	// Let both transcripts age past a deliberately tiny idle threshold, then
	// write the active session's again -- it is mid-task. A real stale
	// transcript is hours old, and so is the process that owns it; a live
	// process cannot be aged, so the threshold moves instead of the clock.
	const threshold = 900 * time.Millisecond
	time.Sleep(threshold + 300*time.Millisecond)
	touchNow(t, activeTr)

	// Both sessions run in one directory, as every inversion observed in real
	// data did, so nothing but the claim can tell them apart.
	procs := []procInfo{active, idle}
	_ = idleTr

	before := s.run(t, s.emptyDir, threshold, 15*time.Second, 30*time.Second, procs)
	if !before[active.pid] {
		t.Fatalf("timestamps alone were expected to kill the active session %d, got killed=%v", active.pid, before)
	}

	after := s.run(t, s.sessionsDir, threshold, 15*time.Second, 30*time.Second, procs)
	if after[active.pid] {
		t.Errorf("the active session %d was killed despite claiming its own transcript", active.pid)
	}
	if !after[idle.pid] {
		t.Errorf("the idle session %d should still be collected, got killed=%v", idle.pid, after)
	}
}

// TestReproReopenedSessionWithOrphanInWindow is failure mode 2: a session
// re-opened from history keeps appending its ORIGINAL transcript, whose birth
// predates the new process, so that transcript cannot be a candidate at all. If
// a sibling that exited left one stale transcript born inside the window, the
// pass sees one process and one candidate and treats that as certain -- and the
// data is identical to a lone desktop session whose own transcript went quiet,
// which is the case the cleanup exists for. Refusing to kill on a single
// candidate would only make the tool inert. The claim tells them apart.
func TestReproReopenedSessionWithOrphanInWindow(t *testing.T) {
	s := newClaimScenario(t)
	const (
		hostReopened = "local_33333333-3333-3333-3333-333333333333"
		idReopened   = "cccccccc-3333-4333-8333-cccccccccccc"
		idOrphan     = "dddddddd-4444-4444-8444-dddddddddddd"
	)

	// The original transcript is born before the process that re-opens it, far
	// enough back to fall outside the window.
	const pairBefore = 200 * time.Millisecond
	reopenedTr := writeTranscript(t, s.projectDir, idReopened, s.workdir)
	time.Sleep(pairBefore + 400*time.Millisecond)

	reopened := realProc(t, s.bindir, s.workdir, hostReopened)

	// A sibling that already exited left exactly one transcript in the window.
	orphanTr := writeTranscript(t, s.projectDir, idOrphan, s.workdir)
	writeSessionRecord(t, s.sessionsDir, hostReopened, idReopened, s.workdir)

	const threshold = 900 * time.Millisecond
	time.Sleep(threshold + 300*time.Millisecond)
	touchNow(t, reopenedTr) // the re-opened session is working
	_ = orphanTr            // left stale on purpose

	procs := []procInfo{reopened}

	before := s.run(t, s.emptyDir, threshold, pairBefore, 30*time.Second, procs)
	if !before[reopened.pid] {
		t.Fatalf("timestamps alone were expected to adopt the orphan and kill the working session %d, got killed=%v", reopened.pid, before)
	}

	after := s.run(t, s.sessionsDir, threshold, pairBefore, 30*time.Second, procs)
	if after[reopened.pid] {
		t.Errorf("the re-opened session %d was killed although it claims a transcript it is still writing", reopened.pid)
	}
}

// TestClaimedSessionIDOnARealProcess checks the whole evidence chain end to end
// on this machine: a live process's environment, the desktop app's record of
// that session, and the transcript id it names.
func TestClaimedSessionIDOnARealProcess(t *testing.T) {
	s := newClaimScenario(t)
	const (
		hostID = "local_55555555-5555-5555-5555-555555555555"
		cliID  = "eeeeeeee-5555-4555-8555-eeeeeeeeeeee"
	)
	p := realProc(t, s.bindir, s.workdir, hostID)
	writeSessionRecord(t, s.sessionsDir, hostID, cliID, s.workdir)

	j := New(Config{ProjectsDir: filepath.Dir(s.projectDir), SessionsDir: s.sessionsDir}, &bytes.Buffer{})
	got, ok := j.claimedSessionID(p)
	if !ok {
		t.Fatalf("no claim resolved for a live process that carries %s", hostSessionEnvVar)
	}
	if got != cliID {
		t.Errorf("claimed session id = %q, want %q", got, cliID)
	}

	// Without the records the same process claims nothing, which is what makes
	// the "before" arm of the scenarios above the tool's earlier behaviour.
	jEmpty := New(Config{ProjectsDir: filepath.Dir(s.projectDir), SessionsDir: s.emptyDir}, &bytes.Buffer{})
	if _, ok := jEmpty.claimedSessionID(p); ok {
		t.Error("a claim resolved with no session records present")
	}
}

// TestProcEnvironOnThisPlatform records what this machine can actually do, so a
// platform that silently stops answering shows up as a failing test rather than
// as a tool that quietly went back to guessing.
func TestProcEnvironOnThisPlatform(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("reading another process's environment is not supported on %s", runtime.GOOS)
	}
	s := newClaimScenario(t)
	const hostID = "local_77777777-7777-7777-7777-777777777777"
	p := realProc(t, s.bindir, s.workdir, hostID)

	env, err := procEnviron(p.pid)
	if err != nil {
		t.Fatalf("procEnviron on an own-user process: %v", err)
	}
	if env[hostSessionEnvVar] != hostID {
		t.Errorf("%s = %q, want %q (read %d variables)", hostSessionEnvVar, env[hostSessionEnvVar], hostID, len(env))
	}
}

// oldTierOne is the tier 1 this file's fix replaced: every process that named a
// transcript was marked its direct owner, with no check that anybody else had
// named the same one. It is kept here as the "before" arm of the co-claim
// scenario, so that test shows a real difference on real data rather than
// asserting the new behaviour against itself.
func oldTierOne(j *Janitor, bprocs []procInfo, trs []transcriptInfo) map[int32]desktopDecision {
	byID := make(map[string]transcriptInfo, len(trs))
	for _, tr := range trs {
		byID[transcriptID(tr.path)] = tr
	}
	out := make(map[int32]desktopDecision, len(bprocs))
	for _, p := range bprocs {
		id, ok := j.claim(p)
		if !ok {
			continue
		}
		if tr, known := byID[id]; known {
			out[p.pid] = desktopDecision{tr: tr, ok: true, direct: true}
		}
	}
	return out
}

// TestCoClaimedTranscriptKillsNobody is the failure mode the claim itself
// introduced. A process's environment is copied into every child it starts, so
// the desktop session's id travels to programs the session launched; when one
// of those starts a session of its own, two live processes name the SAME
// transcript. Measured on this Mac 2026-09-24: two host session ids, four live
// processes each, so this is ordinary rather than exotic.
//
// Marking both the direct owner judges the one that is really writing a
// different transcript by the shared transcript's silence, and kills it
// mid-task -- the exact thing the ticket exists to stop, re-entering through
// the fix. Two namers mean the pass cannot tell which owns it, so it must
// conclude nothing about either.
func TestCoClaimedTranscriptKillsNobody(t *testing.T) {
	s := newClaimScenario(t)
	const (
		hostShared = "local_88888888-8888-8888-8888-888888888888"
		idShared   = "ffffffff-8888-4888-8888-ffffffffffff"
		idOwn      = "99999999-9999-4999-8999-999999999999"
	)

	// Two live sessions carrying one host session id: the desktop session and
	// the session a program it started launched with the inherited environment.
	owner := realProc(t, s.bindir, s.workdir, hostShared)
	time.Sleep(150 * time.Millisecond)
	inheritor := realProc(t, s.bindir, s.workdir, hostShared)

	sharedTr := writeTranscript(t, s.projectDir, idShared, s.workdir)
	time.Sleep(150 * time.Millisecond)
	ownTr := writeTranscript(t, s.projectDir, idOwn, s.workdir)
	writeSessionRecord(t, s.sessionsDir, hostShared, idShared, s.workdir)

	// The shared transcript goes quiet; the second session keeps writing its
	// own. A live process cannot be aged, so the threshold moves instead.
	const threshold = 900 * time.Millisecond
	time.Sleep(threshold + 300*time.Millisecond)
	touchNow(t, ownTr)
	_ = sharedTr

	procs := []procInfo{owner, inheritor}
	j := New(Config{
		ProjectsDir:   filepath.Dir(s.projectDir),
		SessionsDir:   s.sessionsDir,
		IdleThreshold: threshold,
		PairBefore:    15 * time.Second,
		PairAfter:     30 * time.Second,
		DryRun:        true,
	}, &bytes.Buffer{})
	trs, _ := j.listTranscripts(nil)
	cutoff := time.Now().Add(-threshold)

	// Before: both are certain owners of one stale transcript, so both die --
	// including the one whose own transcript was written a moment ago.
	before := oldTierOne(j, procs, trs)
	for _, p := range procs {
		d := before[p.pid]
		if !d.ok || d.tr.mtime.After(cutoff) {
			t.Fatalf("PID=%d was expected to be condemned by the shared transcript (ok=%v); the scenario no longer reproduces", p.pid, d.ok)
		}
		if transcriptID(d.tr.path) != idShared {
			t.Fatalf("PID=%d was judged on %s, want the shared transcript", p.pid, filepath.Base(d.tr.path))
		}
	}

	// After: contested, so nothing is concluded about either.
	after := j.decideDesktop(procs, trs)
	for _, p := range procs {
		d := after[p.pid]
		if d.ok {
			t.Errorf("PID=%d was judged on %s although another live process names the same transcript",
				p.pid, filepath.Base(d.tr.path))
		}
		if !strings.Contains(d.reason, "several processes claim") {
			t.Errorf("PID=%d skipped for %q, want the co-claim reason", p.pid, d.reason)
		}
	}

	// And the whole pass kills neither.
	if killed := s.run(t, s.sessionsDir, threshold, 15*time.Second, 30*time.Second, procs); len(killed) != 0 {
		t.Errorf("a pass killed %v; two processes naming one transcript must kill nobody", killed)
	}
}

// TestClaimResolvesWithoutBirthTimes covers the systems where the fix used to
// switch itself off. Listing the transcripts dropped every file whose creation
// time the OS will not report -- a Linux filesystem without statx support --
// before the id lookup was built, so a session that named its own transcript
// was told the pass could not read it, and the whole direct-evidence route went
// dark there. A claim needs only the mtime every filesystem reports.
//
// The processes and files here are real; what is simulated is the filesystem's
// answer about creation time, which is the one thing a Mac cannot produce.
func TestClaimResolvesWithoutBirthTimes(t *testing.T) {
	s := newClaimScenario(t)
	const (
		hostID   = "local_aaaaaaaa-9999-9999-9999-aaaaaaaaaaaa"
		idOwn    = "12121212-1212-4212-8212-121212121212"
		idOrphan = "13131313-1313-4313-8313-131313131313"
	)
	p := realProc(t, s.bindir, s.workdir, hostID)
	tr := writeTranscript(t, s.projectDir, idOwn, s.workdir)
	writeTranscript(t, s.projectDir, idOrphan, s.workdir) // nobody names this one
	writeSessionRecord(t, s.sessionsDir, hostID, idOwn, s.workdir)

	// A process that names nothing, started early enough that the unclaimed
	// transcript would land inside its window if the window were usable at all.
	quiet := bgProc(990001, time.Now().Add(-2*time.Second))

	const threshold = 900 * time.Millisecond
	time.Sleep(threshold + 300*time.Millisecond)

	// blindfold answers the way a filesystem with no creation time does.
	blindfold := func(j *Janitor) {
		j.stat = func(path string) (fileTimes, error) {
			ft, err := statTimes(path)
			ft.btime, ft.hasBtime = ft.mtime, false
			return ft, err
		}
	}

	pass := func(old bool) (Result, string) {
		buf := &bytes.Buffer{}
		j := New(Config{
			ProjectsDir:   filepath.Dir(s.projectDir),
			SessionsDir:   s.sessionsDir,
			IdleThreshold: threshold,
			PairBefore:    15 * time.Second,
			PairAfter:     30 * time.Second,
			DryRun:        true,
		}, buf)
		blindfold(j)
		if old {
			// The listing as it was: files with no creation time never reached
			// the caller, so the id lookup could not see them either.
			inner := j.stat
			j.stat = func(path string) (fileTimes, error) {
				ft, err := inner(path)
				if err == nil && !ft.hasBtime {
					err = errNoBirthTime
				}
				return ft, err
			}
		}
		now := time.Now()
		var res Result
		j.scanDesktop([]procInfo{p, quiet}, now, now.Add(-threshold), &res)
		return res, buf.String()
	}

	// Before: nothing was readable, so the session that names its own
	// transcript was skipped and the fix did not exist on those systems.
	if res, log := pass(true); res.Claimed != 0 || res.Killed != 0 {
		t.Fatalf("the old listing resolved a claim after all (claimed=%d killed=%d); the scenario no longer reproduces\n%s",
			res.Claimed, res.Killed, log)
	}

	// After: the claim resolves on mtime alone and the idle session is
	// collected, while the process that names nothing stays unjudged -- the
	// timestamp route must still refuse these files, whose birth time is a
	// stand-in for an mtime that on an append-forever transcript reads as "now".
	res, log := pass(false)
	if res.Claimed != 1 || res.Killed != 1 {
		t.Errorf("claim did not resolve without a birth time: claimed=%d killed=%d\n%s", res.Claimed, res.Killed, log)
	}
	if res.Skipped != 1 {
		t.Errorf("skipped=%d, want the process that names nothing left unjudged on files with no birth time\n%s", res.Skipped, log)
	}
	if !strings.Contains(log, "claimed "+idOwn[:8]) { // the log truncates the id
		t.Errorf("the pass did not name the claimed transcript %s\n%s", filepath.Base(tr), log)
	}
}

// errNoBirthTime stands in for the stat failure the old listing turned a
// missing creation time into: the file never reached the caller at all.
var errNoBirthTime = errors.New("no birth time")
