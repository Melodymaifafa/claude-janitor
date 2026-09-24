package janitor

import "testing"

func TestResumeUUID(t *testing.T) {
	cases := []struct {
		name string
		cmd  []string
		want string
	}{
		{"space form", []string{"claude", "--resume", "12345678-1234-1234-1234-1234567890ab"}, "12345678-1234-1234-1234-1234567890ab"},
		{"equals form", []string{"claude", "--resume=12345678-1234-1234-1234-1234567890ab"}, "12345678-1234-1234-1234-1234567890ab"},
		{"uppercase normalized", []string{"claude", "--resume", "12345678-1234-1234-1234-1234567890AB"}, "12345678-1234-1234-1234-1234567890ab"},
		{"no resume", []string{"claude", "--output-format", "stream-json"}, ""},
		{"bad uuid", []string{"claude", "--resume", "not-a-uuid"}, ""},
		{"resume at end no value", []string{"claude", "--resume"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resumeUUID(c.cmd); got != c.want {
				t.Errorf("resumeUUID(%v) = %q, want %q", c.cmd, got, c.want)
			}
		})
	}
}

func TestHasStreamJSON(t *testing.T) {
	if !hasStreamJSON([]string{"claude", "--output-format", "stream-json"}) {
		t.Error("space form not detected")
	}
	if !hasStreamJSON([]string{"claude", "--output-format=stream-json"}) {
		t.Error("equals form not detected")
	}
	if hasStreamJSON([]string{"claude", "--resume", "x"}) {
		t.Error("false positive on non-stream cmdline")
	}
}

func TestIsClaudeSession(t *testing.T) {
	cases := []struct {
		name string
		cmd  []string
		want bool
	}{
		{"mac path", []string{"/Applications/Claude.app/Contents/Resources/claude-code/cli/MacOS/claude", "--resume", "x"}, true},
		{"bare claude", []string{"claude", "--resume", "x"}, true},
		{"windows exe", []string{`C:\Users\m\.local\bin\claude.exe`, "--resume", "x"}, true},
		{"disclaimer wrapper excluded", []string{"/x/Helpers/disclaimer", "claude"}, false},
		{"unrelated", []string{"/usr/bin/vim", "notes.txt"}, false},
		{"empty", []string{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isClaudeSession(c.cmd); got != c.want {
				t.Errorf("isClaudeSession(%v) = %v, want %v", c.cmd, got, c.want)
			}
		})
	}
}

func TestIsUUID(t *testing.T) {
	if !isUUID("12345678-1234-1234-1234-1234567890ab") {
		t.Error("valid uuid rejected")
	}
	if isUUID("12345678123412341234567890ab") {
		t.Error("uuid without dashes accepted")
	}
	if isUUID("zz345678-1234-1234-1234-1234567890ab") {
		t.Error("non-hex accepted")
	}
}

// TestIsDisclaimerWrapper: the desktop app's wrapper parent is the only parent
// the janitor may kill above a session, so it has to be recognized on all three
// OSes -- a Windows parent writes the same path with backslashes -- and a
// terminal session's shell parent must never match, or killing it would take out
// the user's terminal tab.
func TestIsDisclaimerWrapper(t *testing.T) {
	yes := []string{
		"/Applications/Claude.app/Contents/Frameworks/Claude Helpers/disclaimer --flag",
		`C:\Program Files\Claude\Helpers\disclaimer.exe --flag`,
		`C:\PROGRAM FILES\CLAUDE\HELPERS\DISCLAIMER.EXE`,
	}
	for _, c := range yes {
		if !isDisclaimerWrapper(c) {
			t.Errorf("should match the wrapper: %q", c)
		}
	}
	no := []string{
		"/bin/zsh -l",
		"node /usr/local/bin/claude --resume 12345678-1234-1234-1234-1234567890ab",
		"",
	}
	for _, c := range no {
		if isDisclaimerWrapper(c) {
			t.Errorf("must not match a non-wrapper parent: %q", c)
		}
	}
}
