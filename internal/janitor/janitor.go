// Package janitor implements the cross-platform scan-and-kill core of
// claude-janitor: find idle/dead Claude Code session processes and kill their
// process trees. It is a faithful Go port of the proven Mac reference
// (~/.claude/scripts/claude-session-janitor.sh), using gopsutil so the same
// codepath runs on macOS, Linux, and Windows (design.md §4). It never deletes
// session files -- history is preserved for resume.
package janitor

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Result summarizes one pass.
type Result struct {
	Killed  int
	Spared  int // active sessions left running
	Skipped int // session processes with no resolvable/idle transcript
}

// Janitor runs scan-and-kill passes.
type Janitor struct {
	cfg Config
	out io.Writer
	now func() time.Time
}

// New builds a Janitor, filling unset paths with per-OS defaults.
func New(cfg Config, out io.Writer) *Janitor {
	if cfg.IdleThreshold == 0 {
		cfg.IdleThreshold = Defaults().IdleThreshold
	}
	if cfg.MatchTolerance == 0 {
		cfg.MatchTolerance = Defaults().MatchTolerance
	}
	if cfg.ProjectsDir == "" {
		cfg.ProjectsDir = defaultProjectsDir()
	}
	if cfg.SessionsDir == "" {
		cfg.SessionsDir = defaultSessionsDir()
	}
	if out == nil {
		out = os.Stdout
	}
	return &Janitor{cfg: cfg, out: out, now: time.Now}
}

// Run executes a single scan-and-kill pass over both session classes.
func (j *Janitor) Run() (Result, error) {
	var res Result
	now := j.now()
	cutoff := now.Add(-j.cfg.IdleThreshold)

	procs, err := snapshotProcesses()
	if err != nil {
		return res, fmt.Errorf("list processes: %w", err)
	}

	j.scanTerminal(procs, cutoff, &res)
	j.scanDesktop(procs, now, cutoff, &res)

	suffix := ""
	if j.cfg.DryRun {
		suffix = " [dry-run]"
	}
	j.logf("done: killed %d, spared(active) %d, skipped %d%s",
		res.Killed, res.Spared, res.Skipped, suffix)
	return res, nil
}

// scanTerminal handles A-class: processes with --resume <uuid>; idle judged by
// the mtime of ~/.claude/projects/*/<uuid>.jsonl.
func (j *Janitor) scanTerminal(procs []procInfo, cutoff time.Time, res *Result) {
	for _, p := range procs {
		uuid := resumeUUID(p.cmdline)
		if uuid == "" {
			continue // not an A-class terminal session
		}
		tf := j.findTranscript(uuid)
		if tf == "" {
			res.Skipped++
			continue
		}
		ft, err := statTimes(tf)
		if err != nil {
			res.Skipped++
			continue
		}
		if ft.mtime.After(cutoff) {
			res.Spared++ // written recently -> active
			continue
		}
		killed := killProcessTree(p.pid, j.cfg.DryRun)
		j.report("terminal", p.pid, uuid, j.now().Sub(ft.mtime), killed)
		res.Killed++
	}
}

// scanDesktop handles B-class: background sessions with --output-format
// stream-json and no --resume. Idle judged by the mtime of the desktop app's
// local_<uuid>.json; reverse-matched to a process by start time.
func (j *Janitor) scanDesktop(procs []procInfo, now, cutoff time.Time, res *Result) {
	if j.cfg.SessionsDir == "" {
		return
	}
	if _, err := os.Stat(j.cfg.SessionsDir); err != nil {
		// Per design.md §2.B: log and skip rather than crash when the
		// B-class root does not exist on this box.
		j.logf("desktop sessions dir not found, skipping B-class: %s", j.cfg.SessionsDir)
		return
	}

	// Candidate background-session processes (stream-json, no --resume).
	candidates := make([]procInfo, 0, len(procs))
	for _, p := range procs {
		if resumeUUID(p.cmdline) != "" {
			continue
		}
		if !hasStreamJSON(p.cmdline) {
			continue
		}
		candidates = append(candidates, p)
	}

	jsons := findSessionJSONs(j.cfg.SessionsDir)
	used := make(map[int32]bool)

	for _, jf := range jsons {
		ft, err := statTimes(jf)
		if err != nil {
			continue
		}
		if !ft.mtime.Before(cutoff) {
			continue // mtime within threshold = active -> never touch
		}
		uuid := sessionUUIDFromPath(jf)

		// Find the process whose start time is closest before the json's
		// creation time, within MatchTolerance. (reference B step 2)
		best := int32(-1)
		bestDiff := j.cfg.MatchTolerance + time.Second
		for _, c := range candidates {
			if used[c.pid] {
				continue
			}
			// Process born clearly after the json -> not it (30s grace).
			if c.createdAt.After(ft.btime.Add(30 * time.Second)) {
				continue
			}
			diff := ft.btime.Sub(c.createdAt)
			if diff < 0 {
				diff = -diff
			}
			if diff <= j.cfg.MatchTolerance && diff < bestDiff {
				best = c.pid
				bestDiff = diff
			}
		}
		if best < 0 {
			continue // no live process for this idle json -> already gone
		}
		killed := killProcessTree(best, j.cfg.DryRun)
		j.report("desktop", best, uuid, now.Sub(ft.mtime), killed)
		used[best] = true // one process matches at most one json
		res.Killed++
	}
}

// findTranscript globs projects/*/<uuid>.jsonl and returns the first match.
func (j *Janitor) findTranscript(uuid string) string {
	if j.cfg.ProjectsDir == "" {
		return ""
	}
	matches, err := filepath.Glob(filepath.Join(j.cfg.ProjectsDir, "*", uuid+".jsonl"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// findSessionJSONs walks SessionsDir for local_*.json at any depth (the desktop
// app nests by two UUID levels on Mac; other OSes may differ).
func findSessionJSONs(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable subtrees, keep walking
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, "local_") && strings.HasSuffix(name, ".json") {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// sessionUUIDFromPath extracts the uuid from .../local_<uuid>.json.
func sessionUUIDFromPath(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".json")
	return strings.TrimPrefix(base, "local_")
}

func (j *Janitor) report(class string, pid int32, uuid string, idle time.Duration, killed []int32) {
	short := uuid
	if len(short) > 8 {
		short = short[:8]
	}
	idleMin := int(idle.Minutes())
	verb := "killed"
	if j.cfg.DryRun {
		verb = "[dry-run] would kill"
	}
	extra := ""
	if len(killed) > 1 {
		extra = fmt.Sprintf(" (tree+parent: %v)", killed)
	}
	j.logf("%s (%s) PID=%d uuid=%s (idle %d min)%s", verb, class, pid, short, idleMin, extra)
}

func (j *Janitor) logf(format string, args ...any) {
	ts := j.now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(j.out, "%s "+format+"\n", append([]any{ts}, args...)...)
}
