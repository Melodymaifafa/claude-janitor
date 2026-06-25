package main

import "fmt"

// cmdRun is the scan-and-kill entry point that the scheduler invokes on each
// tick (`claude-janitor run`).
//
// PLACEHOLDER ONLY. The real implementation is MEL-92's deliverable (the core
// kill logic), built on a parallel branch and not yet merged here. This stub
// exists so the MEL-93 scheduler slice compiles, runs, and can be verified
// end-to-end on its own. At integration time MEL-92's `run` replaces this file.
func cmdRun(args []string) error {
	fmt.Println("claude-janitor run: scan/kill not implemented in this slice (MEL-92).")
	return nil
}
