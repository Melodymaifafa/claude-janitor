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
