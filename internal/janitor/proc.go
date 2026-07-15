package janitor

import (
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

// procInfo is the subset of process state the janitor needs, snapshotted once
// so we do not re-query gopsutil per comparison.
type procInfo struct {
	proc      *process.Process
	pid       int32
	cmdline   []string // CmdlineSlice
	createdAt time.Time
}

// claudeCmdRe-style matching: the reference greps for the claude-code binary
// path. gopsutil gives the full cmdline cross-platform, so we match on the
// "claude" executable token plus the distinguishing flags. We deliberately do
// NOT match on the Mac-only "claude-code/.../MacOS/claude" path so the same
// filter works on Linux/Windows.

// isClaudeSession reports whether a cmdline looks like a Claude Code session
// process (not the disclaimer wrapper, not the desktop app itself).
func isClaudeSession(cmdline []string) bool {
	if len(cmdline) == 0 {
		return false
	}
	joined := strings.Join(cmdline, " ")
	// The desktop "disclaimer" helper wrapper is never a session itself.
	if strings.Contains(joined, "Helpers/disclaimer") {
		return false
	}
	// Must invoke the claude CLI. The exe is "claude" (or claude.exe); guard
	// against unrelated processes that merely mention the word.
	return mentionsClaudeBinary(cmdline)
}

func mentionsClaudeBinary(cmdline []string) bool {
	exe := cmdline[0]
	// Normalize separators so windows backslash paths match too.
	exe = strings.ReplaceAll(exe, "\\", "/")
	base := exe
	if i := strings.LastIndex(exe, "/"); i >= 0 {
		base = exe[i+1:]
	}
	base = strings.TrimSuffix(base, ".exe")
	return base == "claude" || strings.Contains(exe, "claude-code/") || strings.Contains(exe, "/claude")
}

// resumeUUID extracts the uuid following a "--resume <uuid>" argument, or "".
func resumeUUID(cmdline []string) string {
	for i, a := range cmdline {
		if a == "--resume" && i+1 < len(cmdline) {
			return normalizeUUID(cmdline[i+1])
		}
		// Some shells join as "--resume=<uuid>".
		if strings.HasPrefix(a, "--resume=") {
			return normalizeUUID(strings.TrimPrefix(a, "--resume="))
		}
	}
	return ""
}

func normalizeUUID(s string) string {
	s = strings.TrimSpace(s)
	if isUUID(s) {
		return strings.ToLower(s)
	}
	return ""
}

// isUUID checks the canonical 8-4-4-4-12 hex shape (same as the reference regex).
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !isHex(byte(c)) {
				return false
			}
		}
	}
	return true
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// hasStreamJSON reports whether the cmdline carries --output-format stream-json
// (the B-class desktop background-session signature).
func hasStreamJSON(cmdline []string) bool {
	for i, a := range cmdline {
		if a == "--output-format" && i+1 < len(cmdline) && cmdline[i+1] == "stream-json" {
			return true
		}
		if a == "--output-format=stream-json" {
			return true
		}
	}
	return false
}

// snapshotProcesses lists all running processes once and returns those that
// look like Claude Code sessions, with cmdline + start time captured.
func snapshotProcesses() ([]procInfo, error) {
	procs, err := process.Processes()
	if err != nil {
		return nil, err
	}
	out := make([]procInfo, 0, 16)
	for _, p := range procs {
		cmd, err := p.CmdlineSlice()
		if err != nil || len(cmd) == 0 {
			continue
		}
		if !isClaudeSession(cmd) {
			continue
		}
		// CreateTime is unix milliseconds, cross-platform (design.md §5).
		ms, err := p.CreateTime()
		if err != nil {
			continue
		}
		out = append(out, procInfo{
			proc:      p,
			pid:       p.Pid,
			cmdline:   cmd,
			createdAt: time.UnixMilli(ms),
		})
	}
	return out, nil
}
