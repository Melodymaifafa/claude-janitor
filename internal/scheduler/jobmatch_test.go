package scheduler

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"text/template"
)

// fakeProber stands in for an installer so guardBinaryMatch can be tested
// without a real plist / unit file / crontab.
type fakeProber struct {
	path  string
	found bool
	err   error
}

func (f fakeProber) installedBinary(Config) (string, bool, error) {
	return f.path, f.found, f.err
}

func TestGuardBinaryMatch(t *testing.T) {
	want := "/opt/cj/bin/claude-janitor"
	cases := []struct {
		name     string
		cfg      Config
		prober   fakeProber
		mismatch bool
	}{
		{"check off, foreign job untouched by the check",
			Config{BinaryPath: want},
			fakeProber{path: "/elsewhere/claude-janitor", found: true}, false},
		{"nothing registered",
			Config{BinaryPath: want, MatchBinaryPath: true},
			fakeProber{found: false}, false},
		{"job runs our binary",
			Config{BinaryPath: want, MatchBinaryPath: true},
			fakeProber{path: want, found: true}, false},
		{"job runs our binary via an untidy path",
			Config{BinaryPath: want, MatchBinaryPath: true},
			fakeProber{path: "/opt/cj/./bin/../bin/claude-janitor", found: true}, false},
		{"job runs somebody else's binary",
			Config{BinaryPath: want, MatchBinaryPath: true},
			fakeProber{path: "/Users/me/.local/bin/claude-janitor", found: true}, true},
		{"job exists but program unreadable",
			Config{BinaryPath: want, MatchBinaryPath: true},
			fakeProber{path: "", found: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := guardBinaryMatch(tc.prober, tc.cfg)
			var mismatch *JobBinaryMismatchError
			switch {
			case tc.mismatch && !errors.As(err, &mismatch):
				t.Fatalf("want JobBinaryMismatchError, got %v", err)
			case !tc.mismatch && err != nil:
				t.Fatalf("want nil, got %v", err)
			}
		})
	}
}

func TestGuardBinaryMatchPropagatesProbeError(t *testing.T) {
	boom := errors.New("boom")
	err := guardBinaryMatch(fakeProber{err: boom}, Config{BinaryPath: "/a", MatchBinaryPath: true})
	if !errors.Is(err, boom) {
		t.Fatalf("probe error swallowed: %v", err)
	}
}

// A plist rendered by Install must be readable back by installedBinary --
// otherwise the gate would refuse to remove jobs we did create.
func TestLaunchdInstalledBinaryRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := Config{BinaryPath: "/opt/cj bin/claude-janitor", Label: "com.test.cj"}.resolve()
	writeTestPlist(t, home, cfg)

	got, found, err := launchdInstaller{}.installedBinary(cfg)
	if err != nil || !found {
		t.Fatalf("installedBinary = (%q, %v, %v)", got, found, err)
	}
	if got != cfg.BinaryPath {
		t.Errorf("program = %q, want %q", got, cfg.BinaryPath)
	}
}

func TestLaunchdInstalledBinaryNoPlist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, found, err := launchdInstaller{}.installedBinary(Config{Label: "com.test.absent"}.resolve())
	if err != nil || found || got != "" {
		t.Fatalf("installedBinary = (%q, %v, %v), want ('', false, nil)", got, found, err)
	}
}

// The gate has to sit in front of Uninstall, not merely exist: a foreign job
// must still be registered after the call. No launchctl runs on this path.
func TestLaunchdUninstallKeepsForeignJob(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	real := Config{BinaryPath: "/Users/me/.local/bin/claude-janitor", Label: "com.test.cj"}.resolve()
	writeTestPlist(t, home, real)
	plist := filepath.Join(home, "Library", "LaunchAgents", real.Label+".plist")
	before, err := os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}

	temp := real
	temp.BinaryPath = filepath.Join(t.TempDir(), "q", "bin", "claude-janitor")
	temp.MatchBinaryPath = true

	var mismatch *JobBinaryMismatchError
	if err := (launchdInstaller{}).Uninstall(temp); !errors.As(err, &mismatch) {
		t.Fatalf("Uninstall err = %v, want JobBinaryMismatchError", err)
	}
	after, err := os.ReadFile(plist)
	if err != nil {
		t.Fatalf("plist gone -- the real job was deleted: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("plist rewritten")
	}
	if mismatch.Got != real.BinaryPath || mismatch.Want != temp.BinaryPath {
		t.Errorf("error = %+v", mismatch)
	}
}

// Same wiring check for a path that needs no system daemon at all: systemd's
// Uninstall must refuse before it shells out to systemctl.
func TestSystemdUninstallKeepsForeignJobThenRemovesOurs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := Config{BinaryPath: "/Users/me/.local/bin/claude-janitor", Label: "com.test.cj"}.resolve()
	// Write the units the way Install does, without needing systemctl here.
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := systemdData{
		Label:           cfg.Label,
		ExecStart:       shellJoin(append([]string{cfg.BinaryPath}, cfg.RunArgs...)),
		IntervalMinutes: cfg.IntervalMinutes,
		LogPath:         filepath.Join(home, "cj.log"),
	}
	unit := filepath.Join(dir, unitBase(cfg.Label)+".service")
	if err := renderToFile(unit, systemdServiceTemplate, data); err != nil {
		t.Fatal(err)
	}
	if err := renderToFile(filepath.Join(dir, unitBase(cfg.Label)+".timer"), systemdTimerTemplate, data); err != nil {
		t.Fatal(err)
	}

	foreign := cfg
	foreign.BinaryPath = "/somewhere/else/claude-janitor"
	foreign.MatchBinaryPath = true
	var mismatch *JobBinaryMismatchError
	if err := (systemdInstaller{}).Uninstall(foreign); !errors.As(err, &mismatch) {
		t.Fatalf("Uninstall err = %v, want JobBinaryMismatchError", err)
	}
	if _, err := os.Stat(unit); err != nil {
		t.Fatalf("foreign unit removed: %v", err)
	}

	ours := cfg
	ours.MatchBinaryPath = true
	if err := (systemdInstaller{}).Uninstall(ours); err != nil {
		t.Fatalf("Uninstall of our own job failed: %v", err)
	}
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Fatalf("our unit survived: %v", err)
	}
}

func TestPlistFirstProgramArgument(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"absent key", `<plist><dict><key>Label</key><string>x</string></dict></plist>`, ""},
		{"empty array", `<plist><dict><key>ProgramArguments</key><array></array></dict></plist>`, ""},
		{"escaped path", `<plist><dict><key>ProgramArguments</key><array><string>/a&amp;b/cj</string><string>run</string></array></dict></plist>`, "/a&b/cj"},
		{"not xml", "garbage", ""},
		{"label before args", `<plist><dict><key>Label</key><string>com.x</string><key>ProgramArguments</key><array><string>/bin/cj</string></array></dict></plist>`, "/bin/cj"},
	}
	for _, tc := range cases {
		if got := plistFirstProgramArgument([]byte(tc.in)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFirstShellToken(t *testing.T) {
	cases := map[string]string{
		`/bin/cj run`:       "/bin/cj",
		`"/a b/cj" run`:     "/a b/cj",
		`  /bin/cj`:         "/bin/cj",
		``:                  "",
		`"/unterminated cj`: "",
	}
	for in, want := range cases {
		if got := firstShellToken(in); got != want {
			t.Errorf("firstShellToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnitExecStart(t *testing.T) {
	unit := "[Unit]\nDescription=x\n\n[Service]\nType=oneshot\nExecStart=\"/a b/cj\" run\n"
	if got := unitExecStart(unit); got != `"/a b/cj" run` {
		t.Errorf("unitExecStart = %q", got)
	}
	if got := unitExecStart("[Service]\nType=oneshot\n"); got != "" {
		t.Errorf("unitExecStart = %q, want empty", got)
	}
}

func TestCronLineCommand(t *testing.T) {
	cases := map[string]string{
		"@reboot /bin/cj run":        "/bin/cj run",
		"*/30 * * * * /bin/cj run":   "/bin/cj run",
		"# BEGIN claude-janitor x":   "",
		"":                           "",
		"*/30 * * * *":               "",
		`*/30 * * * * "/a b/cj" run`: `"/a b/cj" run`,
	}
	for in, want := range cases {
		if got := cronLineCommand(in); got != want {
			t.Errorf("cronLineCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCronInstalledBinaryReadsOurBlock(t *testing.T) {
	label := "com.test.cj"
	crontab := "# user line\n0 0 * * * echo hi\n" +
		cronBeginMarker(label) + "\n@reboot /opt/cj run\n*/30 * * * * /opt/cj run\n" +
		cronEndMarker(label) + "\n"
	inside := blockLines(crontab, label)
	if len(inside) != 2 {
		t.Fatalf("blockLines = %q", inside)
	}
	if got := firstShellToken(cronLineCommand(inside[0])); got != "/opt/cj" {
		t.Errorf("program = %q", got)
	}
	if len(blockLines(crontab, "other.label")) != 0 {
		t.Error("unrelated label matched a block")
	}
}

func TestSchtasksTaskToRun(t *testing.T) {
	out := "Folder: \\\r\nTaskName:      \\com.test.cj\r\nTask To Run:   C:\\cj\\cj.exe run\r\nStatus:        Ready\r\n"
	if got := schtasksTaskToRun(out); got != `C:\cj\cj.exe run` {
		t.Errorf("schtasksTaskToRun = %q", got)
	}
	if got := schtasksTaskToRun("Aufgabe:  C:\\cj.exe\r\n"); got != "" {
		t.Errorf("localized output should yield empty, got %q", got)
	}
}

func TestSameBinaryPathEmptyNeverMatches(t *testing.T) {
	if sameBinaryPath("", "") || sameBinaryPath("", "/a") || sameBinaryPath("/a", "") {
		t.Error("empty path matched")
	}
}

// writeTestPlist renders the production plist template into a temp HOME, so the
// parser is exercised against the exact bytes Install writes.
func writeTestPlist(t *testing.T, home string, cfg Config) {
	t.Helper()
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpl := template.Must(template.New("plist").Parse(launchdPlistTemplate))
	var buf bytes.Buffer
	data := launchdData{
		Label:            cfg.Label,
		ProgramArguments: append([]string{cfg.BinaryPath}, cfg.RunArgs...),
		IntervalSeconds:  cfg.IntervalMinutes * 60,
		LogPath:          filepath.Join(home, "cj.log"),
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, cfg.Label+".plist"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
