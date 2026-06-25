package scheduler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

// launchdInstaller implements the macOS path: write a LaunchAgent plist to
// ~/Library/LaunchAgents/<label>.plist and `launchctl load` it. Mirrors the
// reference plist (RunAtLoad + StartInterval 1800s) from docs/design.md §0.
type launchdInstaller struct{}

const launchdPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>

    <key>ProgramArguments</key>
    <array>
{{range .ProgramArguments}}        <string>{{.}}</string>
{{end}}    </array>

    <key>StartInterval</key>
    <integer>{{.IntervalSeconds}}</integer>

    <key>RunAtLoad</key>
    <true/>

    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogPath}}</string>
</dict>
</plist>
`

type launchdData struct {
	Label            string
	ProgramArguments []string
	IntervalSeconds  int
	LogPath          string
}

// plistPath returns ~/Library/LaunchAgents/<label>.plist.
func (launchdInstaller) plistPath(label string) (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

func (l launchdInstaller) defaultLogPath() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "logs", "claude-janitor.log"), nil
}

func (l launchdInstaller) Install(cfg Config) error {
	cfg = cfg.resolve()

	logPath := cfg.LogPath
	if logPath == "" {
		var err error
		if logPath, err = l.defaultLogPath(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("launchd: create log dir: %w", err)
	}

	plistFile, err := l.plistPath(cfg.Label)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistFile), 0o755); err != nil {
		return fmt.Errorf("launchd: create LaunchAgents dir: %w", err)
	}

	data := launchdData{
		Label:            cfg.Label,
		ProgramArguments: append([]string{cfg.BinaryPath}, cfg.RunArgs...),
		IntervalSeconds:  cfg.IntervalMinutes * 60,
		LogPath:          logPath,
	}
	tmpl, err := template.New("plist").Parse(launchdPlistTemplate)
	if err != nil {
		return fmt.Errorf("launchd: parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("launchd: render plist: %w", err)
	}
	if err := os.WriteFile(plistFile, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("launchd: write plist: %w", err)
	}

	// Unload first (ignore error) so a re-install replaces cleanly, then load.
	_ = exec.Command("launchctl", "unload", plistFile).Run()
	if out, err := exec.Command("launchctl", "load", plistFile).CombinedOutput(); err != nil {
		return fmt.Errorf("launchd: launchctl load failed: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func (l launchdInstaller) Uninstall(cfg Config) error {
	cfg = cfg.resolve()
	plistFile, err := l.plistPath(cfg.Label)
	if err != nil {
		return err
	}
	// Unload if present; ignore "not loaded" errors.
	if _, statErr := os.Stat(plistFile); statErr == nil {
		_ = exec.Command("launchctl", "unload", plistFile).Run()
	}
	if err := os.Remove(plistFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("launchd: remove plist: %w", err)
	}
	return nil
}

func (l launchdInstaller) Describe(cfg Config) string {
	cfg = cfg.resolve()
	plistFile, _ := l.plistPath(cfg.Label)
	return fmt.Sprintf("launchd LaunchAgent %q at %s (every %d min + at login)",
		cfg.Label, plistFile, cfg.IntervalMinutes)
}
