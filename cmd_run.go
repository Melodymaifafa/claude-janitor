package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/maicuigua/claude-janitor/internal/janitor"
)

// cmdRun is the scan-and-kill entry point (`claude-janitor run`, also the
// default when no subcommand is given). The scheduler invokes it on each tick.
// It wires the MEL-92 janitor core to the CLI flags and returns a process exit
// code.
func cmdRun(args []string) int {
	// Allow an optional leading "run" before the flags.
	if len(args) > 0 && args[0] == "run" {
		args = args[1:]
	}

	fs := flag.NewFlagSet("claude-janitor run", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print what would be killed, do not kill")
	idleMin := fs.Int("idle-min", 120, "idle threshold in minutes")
	intervalMin := fs.Int("interval-min", 30, "scan interval in minutes (used by the scheduler)")
	projectsDir := fs.String("projects-dir", "", "override transcript root (~/.claude/projects)")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	cfg := janitor.Config{
		IdleThreshold: time.Duration(*idleMin) * time.Minute,
		ScanInterval:  time.Duration(*intervalMin) * time.Minute,
		DryRun:        *dryRun,
		ProjectsDir:   *projectsDir,
	}

	j := janitor.New(cfg, os.Stdout)
	if _, err := j.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "claude-janitor:", err)
		return 1
	}
	return 0
}
