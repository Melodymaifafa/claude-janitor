package janitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
