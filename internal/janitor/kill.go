package janitor

import (
	"strings"

	"github.com/shirou/gopsutil/v3/process"
)

// killProcessTree kills the process tree rooted at pid (children first, then the
// process), then also kills the disclaimer wrapper parent, matching the Mac
// reference's kill_session. gopsutil Kill() sends SIGKILL on POSIX and calls
// TerminateProcess on Windows -- both are the non-ignorable kill the
// SIGTERM-ignoring background processes require (design.md §5).
//
// Returns the pids it killed (or would kill, when dryRun). It never errors out
// the whole pass for a single failed kill -- a process may have already exited.
func killProcessTree(pid int32, dryRun bool) []int32 {
	p, err := process.NewProcess(pid)
	if err != nil {
		return nil
	}

	var killed []int32
	parentPID := wrapperParent(p)

	// Recurse into the tree, killing leaves before their parents.
	killed = append(killed, killTreeRecursive(p, dryRun)...)

	// Also kill the disclaimer wrapper parent (reference kill_session step 2):
	// only when it is a real parent (pid > 1) and not the same process.
	if parentPID > 1 && parentPID != pid {
		if killOne(parentPID, dryRun) {
			killed = append(killed, parentPID)
		}
	}
	return killed
}

// wrapperParent returns the parent pid only when the parent is the desktop
// app's disclaimer wrapper. Terminal sessions are parented by the user's
// shell -- killing that would take out their terminal tab -- so any
// non-disclaimer parent returns -1 (nothing above the session is killed).
func wrapperParent(p *process.Process) int32 {
	parent, err := p.Parent()
	if err != nil || parent == nil {
		return -1
	}
	cmd, err := parent.Cmdline()
	if err != nil || !isDisclaimerWrapper(cmd) {
		return -1
	}
	return parent.Pid
}

// isDisclaimerWrapper matches the desktop app's disclaimer helper in a command
// line. Separators and case are normalized first, the way mentionsClaudeBinary
// does it, so a Windows parent written with backslashes is recognized too.
func isDisclaimerWrapper(cmd string) bool {
	cmd = strings.ToLower(strings.ReplaceAll(cmd, "\\", "/"))
	return strings.Contains(cmd, "helpers/disclaimer")
}

func killTreeRecursive(p *process.Process, dryRun bool) []int32 {
	var killed []int32
	if children, err := p.Children(); err == nil {
		for _, c := range children {
			killed = append(killed, killTreeRecursive(c, dryRun)...)
		}
	}
	if killOne(p.Pid, dryRun) {
		killed = append(killed, p.Pid)
	}
	return killed
}

// killOne kills a single pid (SIGKILL / TerminateProcess) unless dryRun.
// Returns true if the kill was issued (or would be, in dry-run).
func killOne(pid int32, dryRun bool) bool {
	if dryRun {
		return true
	}
	p, err := process.NewProcess(pid)
	if err != nil {
		return false
	}
	// gopsutil Kill() == SIGKILL on POSIX, TerminateProcess on Windows.
	if err := p.Kill(); err != nil {
		return false
	}
	return true
}
