// Command claude-janitor reclaims memory by killing idle Claude Code session
// processes. It never deletes session files -- conversation history is kept so
// you can resume later.
//
// CLI contract (see docs/design.md "CLI contract"):
//
//	claude-janitor [run] [flags]   # one scan+kill pass (MEL-92 core)
//	  --dry-run            print "would kill X", do not kill
//	  --idle-min N         idle threshold in minutes (default 120)
//	  --interval-min N     scan interval in minutes (default 30; consumed by scheduler)
//	  --projects-dir PATH  override A-class transcript root (~/.claude/projects)
//	  --sessions-dir PATH  override B-class desktop session root (per-OS default)
//	claude-janitor install [flags]     # register the periodic scan on the OS scheduler (MEL-93)
//	claude-janitor uninstall [flags]   # remove the scheduled job
//
// The scheduler installed by "install" invokes "claude-janitor run" on its timer.
package main

import (
	"fmt"
	"os"
)

// Overridden at build time via goreleaser ldflags (-X main.version=...).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	args := os.Args[1:]

	// install/uninstall/version/help are explicit subcommands. Everything else
	// -- no args, bare flags, or a leading "run" -- performs one scan-and-kill pass.
	if len(args) > 0 {
		switch args[0] {
		case "install":
			exitErr(cmdInstall(args[1:]))
			return
		case "uninstall":
			exitErr(cmdUninstall(args[1:]))
			return
		case "version", "--version", "-v":
			fmt.Printf("claude-janitor %s (commit %s, built %s)\n", version, commit, date)
			return
		case "-h", "--help", "help":
			usage()
			return
		}
	}

	os.Exit(cmdRun(args))
}

// exitErr prints a scheduler error and exits non-zero, or returns on success.
func exitErr(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "claude-janitor: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `claude-janitor - reclaim memory by killing idle Claude Code sessions

Usage:
  claude-janitor [run] [flags]     scan + kill idle sessions once
  claude-janitor install [flags]   register the periodic scan on the OS scheduler
  claude-janitor uninstall [flags] remove the scheduled job

Scan flags (run):
  --dry-run            print what would be killed, do not kill
  --idle-min N         idle threshold in minutes (default 120)
  --interval-min N     scan interval in minutes (default 30; used by scheduler)
  --projects-dir PATH  override A-class transcript root (~/.claude/projects)
  --sessions-dir PATH  override B-class desktop session root (per-OS default)

Run "claude-janitor install --help" for scheduler flags.
`)
}
