//go:build linux

package janitor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBirthTimeIsNotMtime is the point of the statx work: on a filesystem that
// records a creation time, appending to a file must move mtime and leave btime
// alone. If btime tracked mtime we would be back to the 2026-07-30 failure
// mode, where "created N minutes ago" was mistaken for "idle N minutes".
//
// On a filesystem with no creation time the test skips: absence is a supported
// outcome (birthTime returns zero and the caller degrades), not a failure.
func TestBirthTimeIsNotMtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local_deadbeef.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	bt := birthTime(path, fi)
	if bt.IsZero() {
		t.Skipf("no creation time under %s -- birthTime correctly returned zero", dir)
	}
	if d := time.Since(bt); d < -time.Minute || d > time.Minute {
		t.Fatalf("btime %v is not close to now (off by %v)", bt, d)
	}

	// Append later, the way a transcript grows on every message.
	time.Sleep(1100 * time.Millisecond)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n{}"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	ft, err := statTimes(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ft.mtime.After(bt) {
		t.Fatalf("mtime %v did not move past btime %v after an append", ft.mtime, bt)
	}
	if !ft.btime.Equal(bt) {
		t.Fatalf("btime moved on append: %v -> %v (statx is reporting mtime, not creation)", bt, ft.btime)
	}
	t.Logf("ok under %s: btime %v stayed put while mtime advanced to %v", dir, bt, ft.mtime)
}

// TestBirthTimeMissingPath: statx failures degrade to zero, never panic.
func TestBirthTimeMissingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	if bt := birthTime(path, nil); !bt.IsZero() {
		t.Fatalf("expected zero time for a missing path, got %v", bt)
	}
}

// TestBirthTimeAcrossFilesystems records which mounts on this box answer
// STATX_BTIME. It asserts nothing about support -- it exists so a test run is
// self-documenting evidence for design.md open item #3.
func TestBirthTimeAcrossFilesystems(t *testing.T) {
	for _, dir := range []string{t.TempDir(), "/dev/shm", os.TempDir()} {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Logf("%-12s skipped (not present)", dir)
			continue
		}
		f, err := os.CreateTemp(dir, "local_*.json")
		if err != nil {
			t.Logf("%-12s skipped (%v)", dir, err)
			continue
		}
		name := f.Name()
		f.Close()
		defer os.Remove(name)
		fi, err := os.Stat(name)
		if err != nil {
			continue
		}
		if bt := birthTime(name, fi); bt.IsZero() {
			t.Logf("%-12s NO btime -- B-class pairing degrades here", dir)
		} else {
			t.Logf("%-12s btime OK (%v)", dir, bt.Format(time.RFC3339Nano))
		}
	}
}
