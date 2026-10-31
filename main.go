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
//	  --projects-dir PATH  override transcript root (~/.claude/projects)
//	claude-janitor install [flags]     # register the periodic scan on the OS scheduler (MEL-93)
//	claude-janitor uninstall [flags]   # remove the scheduled job
//
// The scheduler installed by "install" invokes "claude-janitor run" on its timer.
package main

import (
	"fmt"
	"os"
	"runtime/debug"
)

// Overridden at build time via goreleaser ldflags (-X main.version=...).
// Builds without them fall back to Go's VCS stamp (see buildStamp).
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
			info, _ := debug.ReadBuildInfo()
			c, d := buildStamp(commit, date, info)
			fmt.Printf("claude-janitor %s (commit %s, built %s)\n", version, c, d)
			return
		case "-h", "--help", "help":
			usage()
			return
		}
	}

	os.Exit(cmdRun(args))
}

// buildStamp fills a commit or date that ldflags left at its default from the
// vcs.revision / vcs.time Go embeds when building a git checkout, so a plain
// "go build" still says which commit it is. info is nil when Go recorded none.
func buildStamp(commit, date string, info *debug.BuildInfo) (string, string) {
	if info == nil {
		return commit, date
	}
	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision" && commit == "none":
			commit = s.Value[:min(7, len(s.Value))]
		case s.Key == "vcs.time" && date == "unknown":
			date = s.Value
		}
	}
	return commit, date
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
  --projects-dir PATH  override transcript root (~/.claude/projects)

Run "claude-janitor install --help" for scheduler flags.
`)
}
