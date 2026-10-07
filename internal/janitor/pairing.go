package janitor

import (
	"sort"
	"time"
)

// Desktop (B-class) process -> transcript pairing. This is the one place in the
// tool where a wrong answer kills a session that is still working, so the rules
// are spelled out rather than implied.
//
// A desktop session carries no session id in argv; the only link to its
// transcript is timing -- the transcript is created a few seconds AFTER the
// process starts (measured 4-14s on-box). Three rules follow, each of which was
// a live way to kill an active session before it existed (MEL-231 R3/R4 review,
// every case reproduced):
//
//  1. ORDER. Processes create their transcripts in the order they start, so a
//     pairing must not cross. Picking the smallest gap first breaks this: with
//     two sessions started Δ apart whose transcripts appear d seconds later, the
//     crossing edge |d−Δ| is smaller than either true edge d whenever d > Δ > 0,
//     so smallest-gap-first swaps them every time, and the active session --
//     now holding the dead one's transcript -- is killed mid-task.
//
//  2. EXCLUSIVITY. A transcript belongs to exactly one process, and one owned by
//     a live terminal (--resume) session belongs to no desktop process at all
//     (see reservedUUIDs).
//
//  3. NO INVENTED ORDER. Equal timestamps carry no order: process ids are not
//     launch order, and file paths are not creation order. Processes sharing a
//     start timestamp, and transcripts sharing a birth timestamp, are each
//     treated as interchangeable groups -- every member could be any member's --
//     so such a group is condemned only when none of its transcripts is being
//     written.
//
//  4. NO GUESSING. Transcripts are never deleted, so a window can hold more
//     transcripts than there are live processes -- the sessions launched in the
//     same batch that have since exited leave theirs behind. A rule that picks
//     the "most plausible" candidate then hands a survivor an orphan's
//     transcript and kills it once the orphan goes stale. So the pairing reports
//     EVERY transcript a process could own, and a process is killed only when
//     all of them are stale: idle whichever one is really its own. A process
//     that some maximum pairing leaves out entirely has no activity evidence at
//     all and is never killed.
//
// BOTH RESIDUAL LIMITS ARE CLOSED, and not here -- they needed information this
// file does not have, so claim.go supplies it and this file is simply not
// consulted for a session that names its own transcript (MEL-237).
//
//   - Inverted birth order. When the creation lag varies by more than the launch
//     spacing the birth order itself inverts, and this file lands on the wrong
//     transcript of the batch. Measured on this Mac: the lag runs 2.5s to 14.4s,
//     and 3 of the 18 real session pairs launched within 15s of each other had
//     their transcripts appear in the opposite order. It is the normal regime,
//     not an edge case.
//
//   - A re-opened session with an orphan in its window. A re-opened session
//     appends its ORIGINAL transcript, whose birth predates the new process, so
//     that transcript is out of window and cannot be a candidate. If a sibling
//     that exited left exactly one stale transcript born inside the window, this
//     code sees one process, one candidate, and treats it as certain. The data
//     is identical to the ordinary case of a single desktop session whose own
//     transcript went stale -- which is the case B-class cleanup exists for --
//     so refusing to kill on one candidate would not make the tool safer, it
//     would make it do nothing.
//
// What still holds for the processes that claim nothing: their birth order must
// agree with their launch order, because for them nothing but timestamps
// exists. A claimed transcript is withdrawn from the pool before this file
// runs, which also repairs the unclaiming neighbours of a claiming process.

// pairAssignment is one transcript a process could own.
type pairAssignment struct {
	tr  transcriptInfo
	gap time.Duration // |transcript birth − process start|
}

// pairVerdict is everything the pairing knows about one desktop process.
// A process is safe to judge only when forced is true: every maximum pairing
// gives it one of candidates, so it certainly owns one of them.
type pairVerdict struct {
	forced     bool
	candidates []pairAssignment
}

// decisive returns the transcript whose mtime the kill decision must use -- the
// NEWEST candidate, because if any transcript this process might own was just
// written, this may be the session that wrote it. ok is false when nothing may
// be concluded: no candidate at all, or some maximum pairing leaves the process
// out, meaning the evidence never places it.
func (v pairVerdict) decisive() (pairAssignment, bool) {
	if !v.forced || len(v.candidates) == 0 {
		return pairAssignment{}, false
	}
	newest := v.candidates[0]
	for _, c := range v.candidates[1:] {
		if c.tr.mtime.After(newest.tr.mtime) {
			newest = c
		}
	}
	return newest, true
}

// noPair marks a (process, transcript) combination outside the pairing window.
const noPair = time.Duration(-1)

// inPairWindow reports whether a transcript was born close enough to a
// process's start to be a candidate for it at all: birth must fall in
// [start-PairBefore, start+PairAfter].
func (j *Janitor) inPairWindow(tr transcriptInfo, p procInfo) bool {
	d := tr.btime.Sub(p.createdAt)
	return d >= -j.cfg.PairBefore && d <= j.cfg.PairAfter
}

// pairDesktop returns one verdict per desktop process, keyed by pid.
func (j *Janitor) pairDesktop(procs []procInfo, trs []transcriptInfo) map[int32]pairVerdict {
	out := make(map[int32]pairVerdict, len(procs))
	if len(procs) == 0 {
		return out
	}

	// Sorting both sides by time makes "index-monotone" mean "order-preserving",
	// which is what the two passes below enforce. pid and path only keep the sort
	// deterministic on exact ties; pid is NOT evidence of launch order, so equal
	// start times are un-ordered again after the matching (see the merge below).
	// gopsutil reports start times in whole milliseconds, so a batch really can
	// share one.
	ps := make([]procInfo, len(procs))
	copy(ps, procs)
	sort.Slice(ps, func(a, b int) bool {
		if !ps[a].createdAt.Equal(ps[b].createdAt) {
			return ps[a].createdAt.Before(ps[b].createdAt)
		}
		return ps[a].pid < ps[b].pid
	})

	// Drop transcripts outside every process's window. They can never be paired,
	// so this changes no answer, and it keeps the tables below sized by the
	// handful of live sessions rather than by the thousands of transcripts a
	// long-running machine accumulates.
	ts := make([]transcriptInfo, 0, len(ps)+1)
	for _, tr := range trs {
		for _, p := range ps {
			if j.inPairWindow(tr, p) {
				ts = append(ts, tr)
				break
			}
		}
	}
	sort.Slice(ts, func(a, b int) bool {
		if !ts[a].btime.Equal(ts[b].btime) {
			return ts[a].btime.Before(ts[b].btime)
		}
		return ts[a].path < ts[b].path
	})

	// Transcripts sharing a birth timestamp are un-ordered for the same reason
	// equal-start processes are: the path tie-break above is not evidence, and a
	// coarse filesystem timestamp coalesces files created close together. Each
	// index records its group, so a candidate drags in the whole group below.
	groupStart := make([]int, len(ts))
	groupEnd := make([]int, len(ts))
	for a := 0; a < len(ts); {
		b := a + 1
		for b < len(ts) && ts[b].btime.Equal(ts[a].btime) {
			b++
		}
		for i := a; i < b; i++ {
			groupStart[i], groupEnd[i] = a, b
		}
		a = b
	}

	n, m := len(ps), len(ts)
	for _, p := range ps {
		out[p.pid] = pairVerdict{}
	}
	if m == 0 {
		return out
	}

	gap := make([][]time.Duration, n)
	for i := range ps {
		gap[i] = make([]time.Duration, m)
		for k := range ts {
			gap[i][k] = noPair
			if !j.inPairWindow(ts[k], ps[i]) {
				continue // not a candidate at all
			}
			if d := ts[k].btime.Sub(ps[i].createdAt); d < 0 {
				gap[i][k] = -d
			} else {
				gap[i][k] = d
			}
		}
	}

	// pre[i][k] is the largest non-crossing pairing of the first i processes with
	// the first k transcripts; suf[i][k] the same for processes i.. against
	// transcripts k.. Both indices only move forward, which is what forbids a
	// crossing. K is the size of a maximum pairing.
	pre := make([][]int, n+1)
	suf := make([][]int, n+1)
	for i := 0; i <= n; i++ {
		pre[i] = make([]int, m+1)
		suf[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for k := 1; k <= m; k++ {
			best := pre[i-1][k]
			if pre[i][k-1] > best {
				best = pre[i][k-1]
			}
			if gap[i-1][k-1] != noPair && pre[i-1][k-1]+1 > best {
				best = pre[i-1][k-1] + 1
			}
			pre[i][k] = best
		}
	}
	for i := n - 1; i >= 0; i-- {
		for k := m - 1; k >= 0; k-- {
			best := suf[i+1][k]
			if suf[i][k+1] > best {
				best = suf[i][k+1]
			}
			if gap[i][k] != noPair && suf[i+1][k+1]+1 > best {
				best = suf[i+1][k+1] + 1
			}
			suf[i][k] = best
		}
	}
	total := pre[n][m]

	for i := 0; i < n; i++ {
		v := pairVerdict{forced: total > 0}
		taken := make([]bool, m)
		for k := 0; k < m; k++ {
			// Keep transcript k for process i when some maximum pairing uses that
			// very pair: everything before it plus this pair plus everything after
			// it still adds up to a maximum. Members of k's birth-time group are
			// interchangeable with it, so they come along.
			if gap[i][k] == noPair || pre[i][k]+1+suf[i+1][k+1] != total {
				continue
			}
			for g := groupStart[k]; g < groupEnd[k]; g++ {
				if taken[g] {
					continue
				}
				taken[g] = true
				v.candidates = append(v.candidates, pairAssignment{tr: ts[g], gap: gap[i][g]})
			}
		}
		// A maximum pairing that skips this process entirely means the evidence
		// never places it, so nothing may be concluded about it.
		for k := 0; k <= m; k++ {
			if pre[i][k]+suf[i+1][k] == total {
				v.forced = false
				break
			}
		}
		out[ps[i].pid] = v
	}

	// Processes sharing a start timestamp have identical windows, so every
	// ordering among them fits the evidence equally well and the sort above had
	// to invent one -- pid order is not launch order on any platform. Permuting
	// such a group only permutes its transcripts among its members, so the set a
	// member could own is the group's union. Giving each member that union means a
	// stale transcript can condemn the group only when none of them is being
	// written.
	for a := 0; a < n; {
		b := a + 1
		for b < n && ps[b].createdAt.Equal(ps[a].createdAt) {
			b++
		}
		if b-a > 1 {
			merged := make([]pairAssignment, 0, 2*(b-a))
			seen := make(map[string]bool, 2*(b-a))
			forced := true
			for i := a; i < b; i++ {
				v := out[ps[i].pid]
				if !v.forced {
					forced = false
				}
				for _, c := range v.candidates {
					if !seen[c.tr.path] {
						seen[c.tr.path] = true
						merged = append(merged, c)
					}
				}
			}
			for i := a; i < b; i++ {
				out[ps[i].pid] = pairVerdict{forced: forced, candidates: merged}
			}
		}
		a = b
	}
	return out
}

// reservedUUIDs collects the transcript ids named by live --resume processes.
// Those transcripts have a known owner, so no desktop process may adopt one:
// doing so judges an active desktop session by a stranger's mtime and kills it.
func reservedUUIDs(procs []procInfo) map[string]bool {
	held := make(map[string]bool)
	for _, p := range procs {
		if uuid := resumeUUID(p.cmdline); uuid != "" {
			held[uuid] = true
		}
	}
	return held
}
