package janitor

import (
	"sort"
	"time"
)

// Desktop (B-class) process -> transcript assignment. This is the one place in
// the tool where a wrong answer kills a session that is still working, so the
// rules are spelled out rather than implied.
//
// A desktop session carries no session id in argv; the only link to its
// transcript is timing -- the transcript is created a few seconds AFTER the
// process starts (measured 4-14s on-box). Two properties follow, both
// load-bearing:
//
//  1. ORDER. Processes create their transcripts in the order they start, so the
//     assignment must not cross: among the transcripts it can reach, the i-th
//     process by start time takes the i-th transcript by birth time. Picking
//     the smallest gap first breaks this. With two sessions started Δ apart
//     whose transcripts appear d seconds later, the crossing edge |d−Δ| is
//     smaller than either true edge d whenever d > Δ > 0, so smallest-gap-first
//     swaps the pair every time -- and the active session, now holding the dead
//     one's transcript, is killed mid-task (MEL-231 R3 review, reproduced).
//
//  2. EXCLUSIVITY. A transcript belongs to exactly one process, and one already
//     owned by a live terminal (--resume) session belongs to no desktop process
//     at all -- see reservedUUIDs.
//
// Among all non-crossing assignments we take one of maximum size, so every
// process the evidence can place gets placed; among those, the one with the
// smallest total gap, so an implausible 100s pairing loses to a plausible 5s
// one. A process left unpaired is never killed.
//
// Residual limit, by design: when the creation lag d varies more than the
// launch spacing Δ (e.g. 14s for the first session, 4s for one started 5s
// later) the birth order itself inverts, and no rule reading only timestamps
// can recover the truth. Pairing then lands on the wrong transcript of the same
// batch. The tool accepts this: it can cost one wrongly-spared process, or a
// kill only when BOTH sessions of the batch are past the idle threshold.

// pairAssignment is the transcript chosen for one process.
type pairAssignment struct {
	tr  transcriptInfo
	gap time.Duration // |transcript birth − process start|
}

// pairScore ranks whole assignments: more pairs first, then smaller total gap.
type pairScore struct {
	pairs int
	total time.Duration
}

func (a pairScore) beats(b pairScore) bool {
	if a.pairs != b.pairs {
		return a.pairs > b.pairs
	}
	return a.total < b.total
}

// noPair marks a (process, transcript) combination outside the pairing window.
const noPair = time.Duration(-1)

// pairDesktop returns the chosen transcript per process pid. Processes absent
// from the result are unpairable and must never be killed.
func (j *Janitor) pairDesktop(procs []procInfo, trs []transcriptInfo) map[int32]pairAssignment {
	out := make(map[int32]pairAssignment, len(procs))
	if len(procs) == 0 || len(trs) == 0 {
		return out
	}

	// Sorting both sides by time makes "index-monotone" mean "order-preserving",
	// which is what the dynamic program below enforces.
	ps := make([]procInfo, len(procs))
	copy(ps, procs)
	sort.Slice(ps, func(a, b int) bool { return ps[a].createdAt.Before(ps[b].createdAt) })
	ts := make([]transcriptInfo, len(trs))
	copy(ts, trs)
	sort.Slice(ts, func(a, b int) bool { return ts[a].btime.Before(ts[b].btime) })

	n, m := len(ps), len(ts)
	gap := make([][]time.Duration, n)
	for i := range ps {
		gap[i] = make([]time.Duration, m)
		for k := range ts {
			gap[i][k] = noPair
			d := ts[k].btime.Sub(ps[i].createdAt)
			if d < -j.cfg.PairBefore || d > j.cfg.PairAfter {
				continue // outside the window: not a candidate at all
			}
			if d < 0 {
				d = -d
			}
			gap[i][k] = d
		}
	}

	// dp[i][k] = best score over the first i processes and first k transcripts.
	// Both indices only ever move forward, so no chosen pair can cross another.
	dp := make([][]pairScore, n+1)
	from := make([][]byte, n+1) // 'p' pair, 'i' leave process unpaired, 'k' leave transcript unused
	for i := 0; i <= n; i++ {
		dp[i] = make([]pairScore, m+1)
		from[i] = make([]byte, m+1)
	}
	for i := 1; i <= n; i++ {
		for k := 1; k <= m; k++ {
			// Ties keep the earlier branch, so an equally-good assignment that
			// pairs nothing wins over one that does: unpaired is never killed.
			best, choice := dp[i-1][k], byte('i')
			if dp[i][k-1].beats(best) {
				best, choice = dp[i][k-1], 'k'
			}
			if g := gap[i-1][k-1]; g != noPair {
				cand := pairScore{pairs: dp[i-1][k-1].pairs + 1, total: dp[i-1][k-1].total + g}
				if cand.beats(best) {
					best, choice = cand, 'p'
				}
			}
			dp[i][k], from[i][k] = best, choice
		}
	}

	for i, k := n, m; i > 0 && k > 0; {
		switch from[i][k] {
		case 'p':
			out[ps[i-1].pid] = pairAssignment{tr: ts[k-1], gap: gap[i-1][k-1]}
			i--
			k--
		case 'k':
			k--
		default:
			i--
		}
	}
	return out
}

// reservedUUIDs collects the transcript ids named by live --resume processes.
// Those transcripts have a known owner, so no desktop process may adopt one:
// doing so judges an active desktop session by a stranger's mtime and kills it
// (MEL-231 R3 review, reproduced).
func reservedUUIDs(procs []procInfo) map[string]bool {
	held := make(map[string]bool)
	for _, p := range procs {
		if uuid := resumeUUID(p.cmdline); uuid != "" {
			held[uuid] = true
		}
	}
	return held
}
