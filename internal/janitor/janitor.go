// Package janitor implements the cross-platform scan-and-kill core of
// claude-janitor: find idle Claude Code session processes and kill their
// process trees. It never deletes session files -- history is preserved for
// resume.
//
// Idleness is always judged by the mtime of the session's transcript
// (~/.claude/projects/*/<id>.jsonl), which every message updates. Terminal
// sessions name their transcript via --resume <uuid>; desktop background
// sessions carry no id in argv, so each process is paired to the transcript
// born right after the process started (measured 4-14s on a real Mac).
//
// HISTORY -- do not reintroduce: an earlier design judged desktop sessions by
// the desktop app's local_<uuid>.json mtime. That file is a UI-event snapshot,
// rewritten whole on create/reopen and NEVER during task execution, so
// "idle N min" really meant "born N min ago": sessions running long tasks were
// killed mid-task at age 2h, and the stale json re-matched neighbor processes
// on later passes (double-kills). The 2026-07-30 incident report lives in the
// repo docs; the transcript is the only reliable activity signal.
package janitor

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Result summarizes one pass.
type Result struct {
	Killed  int
	Spared  int // active sessions left running
	Skipped int // processes left alone: no transcript resolvable/pairable
}

// Janitor runs scan-and-kill passes.
type Janitor struct {
	cfg Config
	out io.Writer
	now func() time.Time
}

// New builds a Janitor, filling unset knobs with defaults.
func New(cfg Config, out io.Writer) *Janitor {
	def := Defaults()
	if cfg.IdleThreshold == 0 {
		cfg.IdleThreshold = def.IdleThreshold
	}
	if cfg.PairBefore == 0 {
		cfg.PairBefore = def.PairBefore
	}
	if cfg.PairAfter == 0 {
		cfg.PairAfter = def.PairAfter
	}
	if cfg.ProjectsDir == "" {
		cfg.ProjectsDir = defaultProjectsDir()
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

	j.scanTerminal(procs, now, cutoff, &res)
	j.scanDesktop(procs, now, cutoff, &res)

	suffix := ""
	if j.cfg.DryRun {
		suffix = " [dry-run]"
	}
	j.logf("done: killed %d, spared(active) %d, skipped %d%s",
		res.Killed, res.Spared, res.Skipped, suffix)
	return res, nil
}

// scanTerminal handles A-class: processes with --resume <uuid>; the transcript
// is named by the uuid directly.
func (j *Janitor) scanTerminal(procs []procInfo, now, cutoff time.Time, res *Result) {
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
		j.report("terminal", p.pid, uuid, now.Sub(ft.mtime), killed)
		res.Killed++
	}
}

// scanDesktop handles B-class: desktop background sessions (--output-format
// stream-json, no --resume). Each process is paired to the transcript whose
// birth time falls within [start-PairBefore, start+PairAfter]; one transcript
// claims at most one process (smallest gap wins -- a re-opened session's
// process would otherwise steal a neighbor's transcript). Unpaired processes
// are NEVER killed: a re-opened session appends its old transcript, whose
// birth predates the new process, so it cannot pair -- leaking one process
// beats killing an active session.
func (j *Janitor) scanDesktop(procs []procInfo, now, cutoff time.Time, res *Result) {
	trs, noBtime := j.listTranscripts()

	// Collect every (process, transcript) combination whose birth/start gap
	// falls inside the pairing window, then greedily match globally by
	// smallest gap with both sides exclusive. Sessions started seconds apart
	// each still get their own transcript (per-process "closest wins" would
	// let both pick the same one and orphan the other).
	type cand struct {
		p     procInfo
		gap   time.Duration // |transcript birth - process start|
		mtime time.Time
		path  string
	}
	var bprocs []procInfo
	var cands []cand
	for _, p := range procs {
		if resumeUUID(p.cmdline) != "" || !hasStreamJSON(p.cmdline) {
			continue // not a B-class desktop background session
		}
		bprocs = append(bprocs, p)
		for _, tr := range trs {
			gap := tr.btime.Sub(p.createdAt)
			if gap < -j.cfg.PairBefore || gap > j.cfg.PairAfter {
				continue
			}
			if gap < 0 {
				gap = -gap
			}
			cands = append(cands, cand{p: p, gap: gap, mtime: tr.mtime, path: tr.path})
		}
	}

	if len(bprocs) > 0 && len(trs) == 0 && noBtime > 0 {
		// e.g. Linux filesystems without birth-time support: pairing is
		// impossible, so B-class is deliberately inert rather than guessing.
		j.logf("transcript birth times unavailable (%d files); desktop sessions left untouched", noBtime)
	}

	sort.Slice(cands, func(a, b int) bool { return cands[a].gap < cands[b].gap })
	paired := make(map[int32]cand, len(bprocs))
	claimed := make(map[string]bool, len(bprocs))
	for _, c := range cands {
		if _, ok := paired[c.p.pid]; ok {
			continue
		}
		if claimed[c.path] {
			continue
		}
		paired[c.p.pid] = c
		claimed[c.path] = true
	}

	for _, p := range bprocs {
		c, ok := paired[p.pid]
		if !ok {
			// A re-opened session appends its old transcript, whose birth
			// predates the new process, so it cannot pair. Leaking one
			// process beats killing an active session.
			res.Skipped++
			j.logf("skip (unpairable, never killed) PID=%d started=%s",
				p.pid, p.createdAt.Format("01-02 15:04:05"))
			continue
		}
		if c.mtime.After(cutoff) {
			res.Spared++
			continue
		}
		killed := killProcessTree(p.pid, j.cfg.DryRun)
		j.report("desktop", p.pid,
			fmt.Sprintf("%s pair-gap %ds", filepath.Base(c.path), int(c.gap.Seconds())),
			now.Sub(c.mtime), killed)
		res.Killed++
	}
}

// transcriptInfo is one projects/*/*.jsonl candidate for B-class pairing.
type transcriptInfo struct {
	btime time.Time
	mtime time.Time
	path  string
}

// listTranscripts stats every transcript once. Files without a real birth time
// (Linux fallback) are excluded from pairing -- their btime would equal mtime,
// which for an append-forever transcript is "now", not creation -- and counted
// in noBtime so the caller can log the degradation.
func (j *Janitor) listTranscripts() (trs []transcriptInfo, noBtime int) {
	matches, err := filepath.Glob(filepath.Join(j.cfg.ProjectsDir, "*", "*.jsonl"))
	if err != nil {
		return nil, 0
	}
	for _, m := range matches {
		ft, err := statTimes(m)
		if err != nil {
			continue
		}
		if !ft.hasBtime {
			noBtime++
			continue
		}
		trs = append(trs, transcriptInfo{btime: ft.btime, mtime: ft.mtime, path: m})
	}
	return trs, noBtime
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

func (j *Janitor) report(class string, pid int32, ref string, idle time.Duration, killed []int32) {
	if len(ref) > 30 {
		ref = ref[:30]
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
	j.logf("%s (%s) PID=%d %s (idle %d min)%s", verb, class, pid, ref, idleMin, extra)
}

func (j *Janitor) logf(format string, args ...any) {
	ts := j.now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(j.out, "%s "+format+"\n", append([]any{ts}, args...)...)
}
