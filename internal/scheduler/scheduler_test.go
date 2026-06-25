package scheduler

import "testing"

func TestConfigResolveDefaults(t *testing.T) {
	got := Config{}.resolve()
	if got.Label != DefaultLabel {
		t.Errorf("Label = %q, want %q", got.Label, DefaultLabel)
	}
	if got.IntervalMinutes != DefaultIntervalMinutes {
		t.Errorf("IntervalMinutes = %d, want %d", got.IntervalMinutes, DefaultIntervalMinutes)
	}
	if len(got.RunArgs) != 1 || got.RunArgs[0] != "run" {
		t.Errorf("RunArgs = %v, want [run]", got.RunArgs)
	}
}

func TestUnitBaseReplacesDots(t *testing.T) {
	if got := unitBase("com.maicuigua.claude-janitor"); got != "com-maicuigua-claude-janitor" {
		t.Errorf("unitBase = %q", got)
	}
}

func TestCronStripAndInstallRoundTrip(t *testing.T) {
	label := "com.maicuigua.claude-janitor"
	user := "# user line\n0 0 * * * echo hi\n"

	// Simulate an install block appended to an existing crontab.
	withBlock := user +
		cronBeginMarker(label) + "\n" +
		"@reboot /bin/cj run\n" +
		"*/30 * * * * /bin/cj run\n" +
		cronEndMarker(label) + "\n"

	stripped := stripBlock(withBlock, label)
	// User lines must survive; our block must be gone.
	if !contains(stripped, "# user line") || !contains(stripped, "0 0 * * * echo hi") {
		t.Errorf("user lines lost: %q", stripped)
	}
	if contains(stripped, "claude-janitor") || contains(stripped, "/bin/cj") {
		t.Errorf("block not removed: %q", stripped)
	}
}

func TestCronStripUnrelatedLabelUntouched(t *testing.T) {
	keep := cronBeginMarker("other.label") + "\n*/5 * * * * other\n" + cronEndMarker("other.label") + "\n"
	got := stripBlock(keep, "com.maicuigua.claude-janitor")
	if !contains(got, "other") {
		t.Errorf("unrelated block wrongly stripped: %q", got)
	}
}

func TestTaskCommandQuotesSpaces(t *testing.T) {
	cfg := Config{BinaryPath: `C:\Program Files\cj.exe`, RunArgs: []string{"run"}}
	got := taskCommand(cfg)
	want := `"C:\Program Files\cj.exe" run`
	if got != want {
		t.Errorf("taskCommand = %q, want %q", got, want)
	}
}

func TestShellJoinQuotesSpaces(t *testing.T) {
	if got := shellJoin([]string{"/a b/cj", "run"}); got != `"/a b/cj" run` {
		t.Errorf("shellJoin = %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
