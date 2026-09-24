// Package janitor implements the cross-platform scan-and-kill core of
// claude-janitor: find idle Claude Code session processes and kill their
// process trees. It never deletes session files -- history is preserved for
// resume.
//
// Idleness is always judged by the mtime of the session's transcript
// (~/.claude/projects/*/<id>.jsonl), which every message updates. Terminal
// sessions name their transcript via --resume <uuid>; desktop background
// sessions carry no id in argv, so each process is paired to a transcript born
// right after it started (measured 4-14s on a real Mac). That pairing must
// preserve launch order, must not touch a transcript a live terminal session
// already owns, and must never guess between several possible transcripts --
// see pairing.go, which owns all three rules and explains how each of them,
// when missing, killed an active session.
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
	"strings"
	"time"
)

// Result summarizes one pass.
type Result struct {
	Killed  int
	Spared  int // active sessions left running
	Skipped int // processes left alone: no transcript resolvable/pairable

	// Claimed counts desktop processes whose transcript was established by
	// direct evidence rather than by the timestamp window.
	Claimed int
}

// Janitor runs scan-and-kill passes.
type Janitor struct {
	cfg Config
	out io.Writer
	now func() time.Time

	// claim asks a desktop process which transcript it owns; see claim.go. It
	// is a field so tests can drive the decision logic without spawning
	// processes -- the real implementation has its own tests against real ones.
	claim func(procInfo) (string, bool)

	// stat reads a session file's timestamps. A field for one reason: no
	// filesystem on this Mac can produce a file without a birth time, so the
	// only way to test what the tool does on the Linux filesystems that cannot
	// report one is to let a test answer the way they do.
	stat func(string) (fileTimes, error)
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
	if cfg.SessionsDir == "" {
		cfg.SessionsDir = defaultSessionsDir()
	}
	if out == nil {
		out = os.Stdout
	}
	j := &Janitor{cfg: cfg, out: out, now: time.Now, stat: statTimes}
	j.claim = j.claimedSessionID
	return j
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
	// Claimed is worth printing: it is the count of desktop sessions judged on
	// what the session itself said it owns rather than on a timestamp guess, so
	// a pass that reports zero of them on a Mac full of desktop sessions is the
	// signal that the evidence chain stopped working.
	j.logf("done: killed %d, spared(active) %d, skipped %d, desktop-by-claim %d%s",
		res.Killed, res.Spared, res.Skipped, res.Claimed, suffix)
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
		ft, err := j.stat(tf)
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

// desktopDecision is what one pass concluded about one desktop process.
type desktopDecision struct {
	tr     transcriptInfo // the transcript whose mtime decides
	ok     bool           // false: nothing may be concluded, never kill
	direct bool           // the process named this transcript itself
	ncand  int            // candidates considered (timestamp route only)
	reason string         // why nothing may be concluded
}

// decideDesktop resolves every desktop process to the transcript whose mtime
// decides its fate, in two tiers of decreasing evidence.
//
//  1. THE PROCESS'S OWN CLAIM, WHEN IT IS THE ONLY ONE. A process names its
//     transcript through its environment and the desktop app's record of that
//     session (claim.go). Exact, with no timestamp in the chain, so it is
//     immune to both failure modes timestamps have: a batch whose transcripts
//     appeared out of order, and a dead sibling's transcript sitting in a
//     re-opened session's window. A claimed transcript is also withdrawn from
//     the pool, so no other process can be paired to it -- which repairs the
//     neighbours of a claiming process even when they claim nothing themselves.
//
//     EXCLUSIVITY IS PART OF THE EVIDENCE, NOT A DETAIL. A claim is an
//     environment variable, and an environment is COPIED INTO EVERY CHILD
//     PROCESS, so a program a desktop session started carries that session's id
//     and, if it starts a session of its own, names the parent's transcript as
//     its own. Measured on this Mac 2026-09-24: two host session ids, four live
//     processes each. Two processes naming one transcript is therefore a
//     routine occurrence, and it means exactly one thing -- the pass cannot
//     tell which of them owns it. Treating both as certain owners judges the
//     one that is really writing a DIFFERENT transcript by the silence of this
//     one and kills it mid-task, which is the failure this whole file exists to
//     prevent. So a contested transcript decides nobody: every claimant is left
//     alone, and they do NOT fall through to tier 2 either, because the
//     transcript one of them is really writing has already left the pool.
//
//  2. THE TIMESTAMP WINDOW. A process that claims nothing goes through the
//     order-preserving, no-guessing pairing in pairing.go unchanged, and is
//     killed only when every transcript it could own is stale. A third tier
//     that compared working directories was measured and rejected; claim.go
//     records the counterexample.
//
// Tier 1 needs no birth time -- only the mtime every filesystem reports -- so a
// transcript whose creation time this OS will not expose still resolves a claim
// and is only kept out of tier 2's pool (see listTranscripts).
func (j *Janitor) decideDesktop(bprocs []procInfo, trs []transcriptInfo) map[int32]desktopDecision {
	out := make(map[int32]desktopDecision, len(bprocs))
	byID := make(map[string]transcriptInfo, len(trs))
	for _, tr := range trs {
		byID[transcriptID(tr.path)] = tr
	}

	// Tier 1, first half: collect every claim before judging any of them. A
	// claim means nothing until it is known to be the only one on its
	// transcript, so no decision can be made while claims are still arriving.
	claimOf := make(map[int32]string, len(bprocs))
	claimants := make(map[string]int, len(bprocs))
	var rest []procInfo
	for _, p := range bprocs {
		id, ok := "", false
		if j.claim != nil {
			id, ok = j.claim(p)
		}
		if !ok {
			rest = append(rest, p)
			continue
		}
		claimOf[p.pid] = id
		claimants[id]++
	}

	// Tier 1, second half: judge each claimant now that the count is known.
	claimedPath := make(map[string]bool, len(claimOf))
	for _, p := range bprocs {
		id, claims := claimOf[p.pid]
		if !claims {
			continue
		}
		tr, known := byID[id]
		if known {
			// Withdrawn from the pool whatever the verdict below: somebody
			// among the claimants owns this transcript, so no process that
			// claims nothing may be paired to it.
			claimedPath[tr.path] = true
		}
		switch {
		case claimants[id] > 1:
			// Several processes name this transcript; see the exclusivity note
			// above. Indeterminate, so nobody here is killed.
			out[p.pid] = desktopDecision{reason: "several processes claim this transcript"}
		case !known:
			// The process named a transcript this pass cannot see: a live
			// terminal session holds it, or it has not been created yet. Either
			// way the timestamp route must not adopt the process, because its
			// real transcript is not among the candidates.
			out[p.pid] = desktopDecision{reason: "claims a transcript this pass cannot read"}
		default:
			out[p.pid] = desktopDecision{tr: tr, ok: true, direct: true}
		}
	}

	if len(rest) == 0 {
		return out
	}

	// A claimed transcript has a known owner and is withdrawn from the pool. So
	// is one whose birth time this OS does not expose: tier 2 pairs on birth
	// time, and such a file's btime is a stand-in for its mtime, which for an
	// append-forever transcript reads as "now" rather than creation.
	pool := make([]transcriptInfo, 0, len(trs))
	for _, tr := range trs {
		if claimedPath[tr.path] || !tr.hasBtime {
			continue
		}
		pool = append(pool, tr)
	}

	// Tier 2.
	paired := j.pairDesktop(rest, pool)
	for _, p := range rest {
		v := paired[p.pid]
		newest, ok := v.decisive()
		if !ok {
			out[p.pid] = desktopDecision{reason: unpairableReason(v)}
			continue
		}
		out[p.pid] = desktopDecision{tr: newest.tr, ok: true, ncand: len(v.candidates)}
	}
	return out
}

// scanDesktop handles B-class: desktop background sessions (--output-format
// stream-json, no --resume). decideDesktop names the transcript whose mtime
// judges each process; a process it cannot resolve is NEVER killed, because
// leaking one process beats killing an active session.
func (j *Janitor) scanDesktop(procs []procInfo, now, cutoff time.Time, res *Result) {
	var bprocs []procInfo
	for _, p := range procs {
		if resumeUUID(p.cmdline) != "" || !hasStreamJSON(p.cmdline) {
			continue // not a B-class desktop background session
		}
		bprocs = append(bprocs, p)
	}
	if len(bprocs) == 0 {
		return
	}

	trs, noBtime := j.listTranscripts(reservedUUIDs(procs))
	pairable := 0
	for _, tr := range trs {
		if tr.hasBtime {
			pairable++
		}
	}
	if noBtime > 0 && pairable == 0 {
		// e.g. Linux filesystems without birth-time support: the timestamp
		// route is impossible there, so it stays inert rather than guessing.
		// Sessions that name their own transcript are judged normally -- that
		// route reads mtime only.
		j.logf("transcript birth times unavailable (%d files); only sessions that name their own transcript are judged", noBtime)
	}

	decided := j.decideDesktop(bprocs, trs)
	for _, p := range bprocs {
		d := decided[p.pid]
		if !d.ok {
			res.Skipped++
			j.logf("skip (%s, never killed) PID=%d started=%s",
				d.reason, p.pid, p.createdAt.Format("01-02 15:04:05"))
			continue
		}
		if d.direct {
			res.Claimed++
		}
		if d.tr.mtime.After(cutoff) {
			res.Spared++
			continue
		}
		killed := killProcessTree(p.pid, j.cfg.DryRun)
		j.report("desktop", p.pid, decisionRef(d), now.Sub(d.tr.mtime), killed)
		res.Killed++
	}
}

// decisionRef describes what the kill decision was based on.
func decisionRef(d desktopDecision) string {
	if d.direct {
		return "claimed " + transcriptID(d.tr.path)
	}
	if d.ncand == 1 {
		return filepath.Base(d.tr.path) + " (window)"
	}
	return fmt.Sprintf("%d candidates, all stale", d.ncand)
}

// transcriptID is the session id a transcript path names.
func transcriptID(path string) string {
	return strings.ToLower(strings.TrimSuffix(filepath.Base(path), ".jsonl"))
}

// unpairableReason names why a process was left alone, so the ordinary
// re-opened-session case can be told apart from an ambiguous window.
func unpairableReason(v pairVerdict) string {
	if len(v.candidates) == 0 {
		return "unpairable"
	}
	return "owner not certain"
}

// transcriptInfo is one projects/*/*.jsonl candidate for B-class resolution.
type transcriptInfo struct {
	btime    time.Time
	mtime    time.Time
	path     string
	hasBtime bool // false: btime is a stand-in, so this file may not be paired
}

// listTranscripts stats every transcript once, skipping the ids in reserved
// (held by a live --resume session).
//
// A file whose birth time this OS will not expose (Linux filesystems without
// statx support) is RETURNED ALL THE SAME, marked unpairable and counted in
// noBtime. Dropping it here was wrong: it took the file out of the id lookup
// too, so a session that names its own transcript got "claims a transcript this
// pass cannot read" and the whole direct-evidence route went dark on those
// systems -- safe, but the fix did not exist there. A claim needs only the
// mtime, which every filesystem reports; it is the timestamp pairing that needs
// a real creation time, and decideDesktop keeps these files out of that pool.
func (j *Janitor) listTranscripts(reserved map[string]bool) (trs []transcriptInfo, noBtime int) {
	matches, err := filepath.Glob(filepath.Join(j.cfg.ProjectsDir, "*", "*.jsonl"))
	if err != nil {
		return nil, 0
	}
	for _, m := range matches {
		id := strings.ToLower(strings.TrimSuffix(filepath.Base(m), ".jsonl"))
		if reserved[id] {
			continue // a live terminal session owns this transcript
		}
		ft, err := j.stat(m)
		if err != nil {
			continue
		}
		if !ft.hasBtime {
			noBtime++
		}
		trs = append(trs, transcriptInfo{btime: ft.btime, mtime: ft.mtime, path: m, hasBtime: ft.hasBtime})
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
