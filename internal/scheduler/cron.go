package scheduler

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// cronInstaller is the Linux fallback used when systemd is absent (containers,
// minimal hosts). It manages a tagged block in the user's crontab:
//
//	# BEGIN claude-janitor <label>
//	@reboot <bin> run
//	*/30 * * * * <bin> run
//	# END claude-janitor <label>
//
// The tag markers let Uninstall remove exactly our lines and nothing else.
// cron has no native "Persistent missed run", so @reboot approximates launchd
// RunAtLoad (docs/design.md §4).
type cronInstaller struct{}

func cronBeginMarker(label string) string { return "# BEGIN claude-janitor " + label }
func cronEndMarker(label string) string   { return "# END claude-janitor " + label }

// readCrontab returns the current crontab. An empty crontab (exit 1, "no
// crontab for user") is treated as empty content, not an error.
func readCrontab() (string, error) {
	out, err := exec.Command("crontab", "-l").CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(out)), "no crontab") {
			return "", nil
		}
		return "", fmt.Errorf("cron: crontab -l failed: %w: %s", err, bytes.TrimSpace(out))
	}
	return string(out), nil
}

// writeCrontab installs content via `crontab -` (reads from stdin).
func writeCrontab(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cron: crontab - failed: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// stripBlock removes any existing claude-janitor block for the label, returning
// the remaining lines.
func stripBlock(crontab, label string) string {
	begin, end := cronBeginMarker(label), cronEndMarker(label)
	lines := strings.Split(crontab, "\n")
	var kept []string
	inBlock := false
	for _, ln := range lines {
		switch {
		case strings.TrimSpace(ln) == begin:
			inBlock = true
		case strings.TrimSpace(ln) == end:
			inBlock = false
		case !inBlock:
			kept = append(kept, ln)
		}
	}
	return strings.Join(kept, "\n")
}

func (c cronInstaller) Install(cfg Config) error {
	cfg = cfg.resolve()
	cmdLine := shellJoin(append([]string{cfg.BinaryPath}, cfg.RunArgs...))

	current, err := readCrontab()
	if err != nil {
		return err
	}
	stripped := strings.TrimRight(stripBlock(current, cfg.Label), "\n")

	var b strings.Builder
	if stripped != "" {
		b.WriteString(stripped)
		b.WriteString("\n")
	}
	b.WriteString(cronBeginMarker(cfg.Label))
	b.WriteString("\n")
	b.WriteString("@reboot " + cmdLine + "\n")
	fmt.Fprintf(&b, "*/%d * * * * %s\n", cfg.IntervalMinutes, cmdLine)
	b.WriteString(cronEndMarker(cfg.Label))
	b.WriteString("\n")

	return writeCrontab(b.String())
}

// blockLines returns the lines inside the claude-janitor block for the label,
// markers excluded. Empty when the block is absent.
func blockLines(crontab, label string) []string {
	begin, end := cronBeginMarker(label), cronEndMarker(label)
	var inside []string
	inBlock := false
	for _, ln := range strings.Split(crontab, "\n") {
		switch {
		case strings.TrimSpace(ln) == begin:
			inBlock = true
		case strings.TrimSpace(ln) == end:
			inBlock = false
		case inBlock:
			inside = append(inside, ln)
		}
	}
	return inside
}

// installedBinary reads the program out of our crontab block.
func (c cronInstaller) installedBinary(cfg Config) (string, bool, error) {
	current, err := readCrontab()
	if err != nil {
		return "", false, err
	}
	got, found := cronBlockBinary(current, cfg)
	return got, found, nil
}

// cronBlockBinary returns the program the block for cfg.Label runs. Uninstall
// strips the whole block, so every command line in it has to be checked, not
// just the first: when any entry runs something other than cfg.BinaryPath,
// that program is returned so the guard leaves the block alone. found=false
// when there is no block.
func cronBlockBinary(crontab string, cfg Config) (string, bool) {
	inside := blockLines(crontab, cfg.Label)
	if len(inside) == 0 {
		return "", false
	}
	got := ""
	for _, ln := range inside {
		cmd := cronLineCommand(ln)
		if cmd == "" {
			continue
		}
		prog := firstShellToken(cmd)
		if !sameBinaryPath(prog, cfg.BinaryPath) {
			return prog, true
		}
		if got == "" {
			got = prog
		}
	}
	return got, true
}

func (c cronInstaller) Uninstall(cfg Config) error {
	cfg = cfg.resolve()
	// Before rewriting the crontab: is this block ours? (MEL-267)
	if err := guardBinaryMatch(c, cfg); err != nil {
		return err
	}
	current, err := readCrontab()
	if err != nil {
		return err
	}
	stripped := strings.TrimRight(stripBlock(current, cfg.Label), "\n")
	if stripped == "" {
		// Nothing left -> clear the crontab entirely.
		return writeCrontab("")
	}
	return writeCrontab(stripped + "\n")
}

func (c cronInstaller) Describe(cfg Config) string {
	cfg = cfg.resolve()
	return fmt.Sprintf("crontab block %q (@reboot + every %d min, systemd fallback)",
		cfg.Label, cfg.IntervalMinutes)
}
