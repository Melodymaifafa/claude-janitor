package janitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Direct ownership evidence for desktop (B-class) sessions.
//
// Timestamps can only ever say "these two events happened close together". Two
// failure modes follow from that and no timing rule can close either (MEL-231
// review; the reasoning is in pairing.go). This file replaces the guess with a
// claim the process itself makes, and with a second, weaker piece of evidence
// for the processes that make no claim.
//
// WHAT WAS MEASURED ON THIS MAC, 2026-09-24 (MEL-237). The two routes the
// ticket named were tested before anything was written:
//
//   - OPEN FILE HANDLE -- DEAD. Claude Code does not hold its transcript open.
//     Across 16 live claude processes, zero file descriptors pointed at any
//     .jsonl or anything under ~/.claude/projects, while six transcripts were
//     being appended at that moment; 30 consecutive lsof polls of one actively
//     written transcript found it open zero times. It opens, writes one line and
//     closes. Independently, gopsutil's OpenFiles() answers "not implemented
//     yet" on darwin (19/19 processes). The route is dead twice over.
//
//   - TRANSCRIPT CONTENT -- REJECTED, and this one was close. Every one of 300
//     sampled transcripts records a "cwd" within its first 2-6 lines, and
//     gopsutil reads a process's own cwd on macOS with no elevation (19/19), so
//     a candidate whose directory differs from the process's looked safe to
//     rule out. It is not. The directory a transcript records is the one the
//     session was GIVEN; a process's cwd is where the kernel says it is, and a
//     live process on this Mac sits in .../observer-sessions/4811 while the
//     transcript it writes records .../observer-sessions. The two are simply
//     different facts. A wrong rule-out removes the transcript a session is
//     actively writing and leaves only stale ones, which kills it -- the exact
//     failure this ticket exists to stop -- and the upside was only separating
//     projects, which the claim already does exactly. So it is not used.
//     (An earlier check that seemed to confirm the rule compared the app's
//     record of the directory against the transcript's; both are the app's own
//     view, so they agreed and proved nothing about the process.)
//
//   - PROCESS ENVIRONMENT -- WORKS, and is tier 1. A desktop session process
//     carries CLAUDE_CODE_HOST_SESSION_ID, the desktop app's own id for that
//     session, and the app's session record of that name states which transcript
//     the session writes. Chained, that names the transcript exactly, with no
//     timestamp anywhere in the chain. Verified end to end against live
//     sessions.
//
// WHY THIS IS NOT THE 2026-07-30 REGRESSION. That incident came from judging
// IDLENESS by the desktop session record's modification time -- a UI-event
// snapshot that stops moving while a task runs, so "idle" really meant "old"
// (design.md §0, and it stays forbidden). Here the record is read for one
// string, the transcript id. Idleness still comes from the transcript's own
// mtime and from nothing else; sidecarSessionID returns a string and never a
// time, so the banned signal cannot re-enter through this path. design.md §2.B
// already named this field as the VERIFIED statement of which transcript a
// desktop session owns.

// hostSessionEnvVar is the desktop app's id for a session, present in the
// environment of every session process the app launches.
const hostSessionEnvVar = "CLAUDE_CODE_HOST_SESSION_ID"

// claimedSessionID returns the transcript id this process states it owns, read
// from the process's own environment and the desktop app's record of that
// session. ok is false whenever any link is missing -- another user's process,
// a platform that will not show an environment, a session the app did not
// launch, a record that has not been written yet. A missing claim is never
// evidence of anything; the caller falls back to the weaker tiers.
func (j *Janitor) claimedSessionID(p procInfo) (string, bool) {
	env, err := procEnviron(p.pid)
	if err != nil || len(env) == 0 {
		return "", false
	}
	hostID := strings.TrimSpace(env[hostSessionEnvVar])
	if hostID == "" {
		return "", false
	}
	return j.sidecarSessionID(hostID)
}

// sidecarSessionID maps a desktop host session id to the transcript id it
// names. It returns a STRING and never a timestamp: the file's own mtime is the
// signal the 2026-07-30 incident came from and must not leave this function.
func (j *Janitor) sidecarSessionID(hostID string) (string, bool) {
	dir := j.cfg.SessionsDir
	if dir == "" {
		return "", false
	}
	// The app nests its records two levels deep (account, then organisation).
	matches, err := filepath.Glob(filepath.Join(dir, "*", "*", hostID+".json"))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		return "", false
	}
	var rec struct {
		CliSessionID string `json:"cliSessionId"`
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return "", false
	}
	id := strings.ToLower(strings.TrimSpace(rec.CliSessionID))
	if !isUUID(id) {
		return "", false
	}
	return id, true
}

// defaultSessionsDir locates the desktop app's per-session records.
//
// This is the only per-OS path the janitor reads outside ~/.claude, and it is
// deliberately optional: when the directory is absent or shaped differently the
// claim simply does not resolve and the scan degrades to the behaviour it had
// before. Nothing is killed because of a path that did not answer.
func defaultSessionsDir() string {
	if v := strings.TrimSpace(os.Getenv("CLAUDE_USER_DATA_DIR")); v != "" {
		return filepath.Join(v, "claude-code-sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude-code-sessions")
	case "windows":
		if v := strings.TrimSpace(os.Getenv("APPDATA")); v != "" {
			return filepath.Join(v, "Claude", "claude-code-sessions")
		}
		return filepath.Join(home, "AppData", "Roaming", "Claude", "claude-code-sessions")
	default:
		// Linux: the shipped desktop package builds this from the XDG config
		// dir (design.md "Open items" §1, read out of claude-desktop 2.7032.0).
		cfg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
		if cfg == "" {
			cfg = filepath.Join(home, ".config")
		}
		return filepath.Join(cfg, "Claude", "claude-code-sessions")
	}
}
