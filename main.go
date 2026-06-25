package main

import (
	"fmt"
	"os"
)

// main dispatches subcommands. Each subcommand lives in its own file so the
// parallel MEL-92 (kill-logic / `run`) slice and this MEL-93 (scheduler) slice
// touch different files and merge cleanly.
func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "install":
		err = cmdInstall(args)
	case "uninstall":
		err = cmdUninstall(args)
	case "run":
		err = cmdRun(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "claude-janitor: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "claude-janitor: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `claude-janitor - reclaim memory by killing idle Claude Code sessions

Usage:
  claude-janitor run [--dry-run]   scan + kill idle sessions once
  claude-janitor install           register the periodic scan on the OS scheduler
  claude-janitor uninstall         remove the scheduled job

Run "claude-janitor <command> --help" for command flags.
`)
}
