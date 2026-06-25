// Command claude-janitor reclaims memory by killing idle Claude Code session
// processes. It never deletes session files -- conversation history is kept so
// you can resume later.
//
// CLI contract (see also docs/design.md "CLI contract" section): the default
// command and the explicit "run" subcommand both perform ONE scan-and-kill
// pass. The MEL-93 scheduler invokes "claude-janitor run" on its timer.
//
//	claude-janitor [run] [flags]   # one scan+kill pass
//	  --dry-run            print "would kill X", do not kill
//	  --idle-min N         idle threshold in minutes (default 120)
//	  --interval-min N     scan interval in minutes (default 30; consumed by scheduler)
//	  --projects-dir PATH  override A-class transcript root (~/.claude/projects)
//	  --sessions-dir PATH  override B-class desktop session root (per-OS default)
//
// "install" / "uninstall" (the OS scheduled job) are MEL-93's slice and are not
// implemented here.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/maicuigua/claude-janitor/internal/janitor"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	// Allow an optional leading "run" subcommand before the flags.
	if len(args) > 0 && args[0] == "run" {
		args = args[1:]
	}

	fs := flag.NewFlagSet("claude-janitor", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print what would be killed, do not kill")
	idleMin := fs.Int("idle-min", 120, "idle threshold in minutes")
	intervalMin := fs.Int("interval-min", 30, "scan interval in minutes (used by the scheduler)")
	projectsDir := fs.String("projects-dir", "", "override A-class transcript root")
	sessionsDir := fs.String("sessions-dir", "", "override B-class desktop session root")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg := janitor.Config{
		IdleThreshold:  time.Duration(*idleMin) * time.Minute,
		ScanInterval:   time.Duration(*intervalMin) * time.Minute,
		MatchTolerance: janitor.Defaults().MatchTolerance,
		DryRun:         *dryRun,
		ProjectsDir:    *projectsDir,
		SessionsDir:    *sessionsDir,
	}

	j := janitor.New(cfg, os.Stdout)
	if _, err := j.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "claude-janitor:", err)
		return 1
	}
	return 0
}
