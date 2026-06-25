package scheduler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

// systemdInstaller implements the Linux primary path: a systemd *user* timer
// (.service + .timer) enabled with `systemctl --user enable --now`. Chosen over
// cron because Persistent=true reruns a missed tick at next boot and journald
// gives per-job logs -- the closest match to launchd RunAtLoad + StartInterval
// (docs/design.md §4).
type systemdInstaller struct{}

// unitBase is the file stem for the .service/.timer pair, derived from the
// label. systemd unit names can't contain dots beyond the suffix, so dots in
// the label become dashes.
func unitBase(label string) string {
	b := make([]rune, 0, len(label))
	for _, r := range label {
		if r == '.' {
			r = '-'
		}
		b = append(b, r)
	}
	return string(b)
}

const systemdServiceTemplate = `[Unit]
Description=claude-janitor periodic idle-session scan ({{.Label}})

[Service]
Type=oneshot
ExecStart={{.ExecStart}}
StandardOutput=append:{{.LogPath}}
StandardError=append:{{.LogPath}}
`

const systemdTimerTemplate = `[Unit]
Description=claude-janitor scan timer ({{.Label}})

[Timer]
OnBootSec=1min
OnUnitActiveSec={{.IntervalMinutes}}min
Persistent=true

[Install]
WantedBy=timers.target
`

type systemdData struct {
	Label           string
	ExecStart       string
	IntervalMinutes int
	LogPath         string
}

// unitDir returns ~/.config/systemd/user, where user units live.
func (systemdInstaller) unitDir() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func (s systemdInstaller) defaultLogPath() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "logs", "claude-janitor.log"), nil
}

func (s systemdInstaller) Install(cfg Config) error {
	cfg = cfg.resolve()
	base := unitBase(cfg.Label)

	logPath := cfg.LogPath
	if logPath == "" {
		var err error
		if logPath, err = s.defaultLogPath(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("systemd: create log dir: %w", err)
	}

	dir, err := s.unitDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("systemd: create unit dir: %w", err)
	}

	data := systemdData{
		Label:           cfg.Label,
		ExecStart:       shellJoin(append([]string{cfg.BinaryPath}, cfg.RunArgs...)),
		IntervalMinutes: cfg.IntervalMinutes,
		LogPath:         logPath,
	}
	if err := renderToFile(filepath.Join(dir, base+".service"), systemdServiceTemplate, data); err != nil {
		return err
	}
	if err := renderToFile(filepath.Join(dir, base+".timer"), systemdTimerTemplate, data); err != nil {
		return err
	}

	if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemd: daemon-reload failed: %w: %s", err, bytes.TrimSpace(out))
	}
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", base+".timer").CombinedOutput(); err != nil {
		return fmt.Errorf("systemd: enable --now failed: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func (s systemdInstaller) Uninstall(cfg Config) error {
	cfg = cfg.resolve()
	base := unitBase(cfg.Label)

	// Stop + disable; ignore errors (timer may already be gone).
	_ = exec.Command("systemctl", "--user", "disable", "--now", base+".timer").Run()

	dir, err := s.unitDir()
	if err != nil {
		return err
	}
	for _, suffix := range []string{".timer", ".service"} {
		if err := os.Remove(filepath.Join(dir, base+suffix)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("systemd: remove %s: %w", base+suffix, err)
		}
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

func (s systemdInstaller) Describe(cfg Config) string {
	cfg = cfg.resolve()
	base := unitBase(cfg.Label)
	dir, _ := s.unitDir()
	return fmt.Sprintf("systemd user timer %s.timer in %s (every %d min + OnBoot, Persistent)",
		base, dir, cfg.IntervalMinutes)
}

// systemdAvailable reports whether systemctl exists AND a user-bus session is
// reachable. If not, the Linux path falls back to crontab (see scheduler.For).
func systemdAvailable() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	// `systemctl --user` needs a running user manager; this probe fails on
	// containers/minimal hosts where only cron is available.
	return exec.Command("systemctl", "--user", "is-system-running").Run() == nil ||
		exec.Command("systemctl", "--user", "show-environment").Run() == nil
}

// renderToFile renders a text/template to a 0644 file.
func renderToFile(path, tmplText string, data any) error {
	tmpl, err := template.New(filepath.Base(path)).Parse(tmplText)
	if err != nil {
		return fmt.Errorf("scheduler: parse template %s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("scheduler: render %s: %w", path, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("scheduler: write %s: %w", path, err)
	}
	return nil
}

// shellJoin quotes args containing spaces so ExecStart / cron lines stay valid.
func shellJoin(args []string) string {
	var buf bytes.Buffer
	for i, a := range args {
		if i > 0 {
			buf.WriteByte(' ')
		}
		if bytes.ContainsAny([]byte(a), " \t") {
			buf.WriteByte('"')
			buf.WriteString(a)
			buf.WriteByte('"')
		} else {
			buf.WriteString(a)
		}
	}
	return buf.String()
}
