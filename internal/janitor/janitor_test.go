package janitor

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFindTranscript verifies the projects/*/<uuid>.jsonl glob.
func TestFindTranscript(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "-Users-x-proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	uuid := "12345678-1234-1234-1234-1234567890ab"
	tf := filepath.Join(proj, uuid+".jsonl")
	if err := os.WriteFile(tf, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	j := New(Config{ProjectsDir: root}, &bytes.Buffer{})
	if got := j.findTranscript(uuid); got != tf {
		t.Errorf("findTranscript = %q, want %q", got, tf)
	}
	if got := j.findTranscript("00000000-0000-0000-0000-000000000000"); got != "" {
		t.Errorf("expected empty for missing uuid, got %q", got)
	}
}

// TestStatTimesIdleVsActive verifies mtime drives the idle/active decision and
// that birthtime falls back to mtime when unavailable.
func TestStatTimesIdleVsActive(t *testing.T) {
	dir := t.TempDir()
	idle := filepath.Join(dir, "idle.jsonl")
	active := filepath.Join(dir, "active.jsonl")
	for _, p := range []string{idle, active} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(idle, old, old); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Now().Add(-120 * time.Minute)

	ftIdle, err := statTimes(idle)
	if err != nil {
		t.Fatal(err)
	}
	if !ftIdle.mtime.Before(cutoff) {
		t.Error("3h-old file should be before cutoff (idle)")
	}
	if ftIdle.btime.IsZero() {
		t.Error("btime should fall back to mtime, never zero")
	}

	ftActive, err := statTimes(active)
	if err != nil {
		t.Fatal(err)
	}
	if ftActive.mtime.Before(cutoff) {
		t.Error("just-written file should be after cutoff (active)")
	}
}

// TestSessionUUIDFromPath verifies local_<uuid>.json parsing.
func TestSessionUUIDFromPath(t *testing.T) {
	got := sessionUUIDFromPath("/a/b/local_12345678-1234-1234-1234-1234567890ab.json")
	want := "12345678-1234-1234-1234-1234567890ab"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// TestFindSessionJSONs verifies recursive local_*.json discovery.
func TestFindSessionJSONs(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "ab", "cd")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(nested, "local_x.json")
	if err := os.WriteFile(want, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// noise that must be ignored
	_ = os.WriteFile(filepath.Join(nested, "other.json"), []byte("{}"), 0o644)

	got := findSessionJSONs(root)
	if len(got) != 1 || got[0] != want {
		t.Errorf("findSessionJSONs = %v, want [%s]", got, want)
	}
}

// TestScanDesktopMissingDirNoPanic verifies a missing B-class dir is skipped
// (logged), not fatal.
func TestScanDesktopMissingDirNoPanic(t *testing.T) {
	var buf bytes.Buffer
	j := New(Config{SessionsDir: "/nonexistent/path/xyz", ProjectsDir: t.TempDir()}, &buf)
	var res Result
	j.scanDesktop(nil, time.Now(), time.Now().Add(-2*time.Hour), &res)
	if res.Killed != 0 {
		t.Errorf("expected 0 killed for missing dir, got %d", res.Killed)
	}
}
