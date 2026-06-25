package scheduler

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// schtasksInstaller implements the Windows native path via schtasks.exe (no
// PowerShell module needed). schtasks attaches one trigger per /Create, so we
// register TWO tasks to mirror launchd's "every N min + at login"
// (docs/design.md §4):
//
//	<label>          -> /SC MINUTE /MO <interval>   (periodic scan)
//	<label> (Logon)  -> /SC ONLOGON                 (run at login, ~RunAtLoad)
//
// Both run the same `<bin> run` command. Uninstall deletes both.
type schtasksInstaller struct{}

// taskName is the periodic task. Backslashes make a Task Scheduler folder, so
// any dots in the label are kept (valid) but we avoid backslashes.
func (schtasksInstaller) taskName(label string) string {
	return strings.ReplaceAll(label, `\`, "-")
}

func (s schtasksInstaller) logonTaskName(label string) string {
	return s.taskName(label) + "-Logon"
}

// taskCommand is the program the task runs. schtasks /TR takes a single string;
// we quote the binary path so spaces survive.
func taskCommand(cfg Config) string {
	parts := append([]string{cfg.BinaryPath}, cfg.RunArgs...)
	quoted := make([]string, len(parts))
	for i, p := range parts {
		if strings.ContainsAny(p, " \t") {
			quoted[i] = `"` + p + `"`
		} else {
			quoted[i] = p
		}
	}
	return strings.Join(quoted, " ")
}

func (s schtasksInstaller) Install(cfg Config) error {
	cfg = cfg.resolve()
	cmd := taskCommand(cfg)

	// Periodic task: every N minutes. /F overwrites an existing task so install
	// is idempotent. /RL LIMITED runs as the current user without elevation.
	periodic := []string{
		"/Create", "/TN", s.taskName(cfg.Label),
		"/TR", cmd,
		"/SC", "MINUTE", "/MO", fmt.Sprintf("%d", cfg.IntervalMinutes),
		"/RL", "LIMITED", "/F",
	}
	if out, err := exec.Command("schtasks", periodic...).CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks: create periodic task failed: %w: %s", err, bytes.TrimSpace(out))
	}

	// Logon task: mirrors RunAtLoad.
	logon := []string{
		"/Create", "/TN", s.logonTaskName(cfg.Label),
		"/TR", cmd,
		"/SC", "ONLOGON",
		"/RL", "LIMITED", "/F",
	}
	if out, err := exec.Command("schtasks", logon...).CombinedOutput(); err != nil {
		// Roll back the periodic task so we don't leave a half-install.
		_ = exec.Command("schtasks", "/Delete", "/TN", s.taskName(cfg.Label), "/F").Run()
		return fmt.Errorf("schtasks: create logon task failed: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func (s schtasksInstaller) Uninstall(cfg Config) error {
	cfg = cfg.resolve()
	var firstErr error
	for _, name := range []string{s.taskName(cfg.Label), s.logonTaskName(cfg.Label)} {
		out, err := exec.Command("schtasks", "/Delete", "/TN", name, "/F").CombinedOutput()
		if err != nil {
			// "cannot find the file" / "does not exist" -> already gone, fine.
			low := strings.ToLower(string(out))
			if strings.Contains(low, "cannot find") || strings.Contains(low, "does not exist") {
				continue
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("schtasks: delete %s failed: %w: %s", name, err, bytes.TrimSpace(out))
			}
		}
	}
	return firstErr
}

func (s schtasksInstaller) Describe(cfg Config) string {
	cfg = cfg.resolve()
	return fmt.Sprintf("Windows Task Scheduler tasks %q + %q (every %d min + at logon)",
		s.taskName(cfg.Label), s.logonTaskName(cfg.Label), cfg.IntervalMinutes)
}
