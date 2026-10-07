package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maicuigua/claude-janitor/internal/scheduler"
)

// cmdInstall registers the periodic scan job on the current OS's scheduler.
// MEL-93 slice. Does not touch kill logic (MEL-92 owns `run`).
func cmdInstall(args []string) error {
	cfg, fs, err := parseSchedulerFlags("install", args)
	if err != nil {
		return err
	}
	if fs == nil { // -h handled
		return nil
	}

	inst, err := scheduler.For()
	if err != nil {
		return err
	}
	if err := inst.Install(cfg); err != nil {
		return err
	}
	fmt.Printf("installed: %s\n", inst.Describe(cfg))
	fmt.Printf("scan command: %s %v\n", cfg.BinaryPath, cfg.RunArgs)
	return nil
}

// cmdUninstall removes the scheduled job installed by cmdInstall.
func cmdUninstall(args []string) error {
	cfg, fs, err := parseSchedulerFlags("uninstall", args)
	if err != nil {
		return err
	}
	if fs == nil {
		return nil
	}

	inst, err := scheduler.For()
	if err != nil {
		return err
	}
	if err := inst.Uninstall(cfg); err != nil {
		// Declining to touch somebody else's job is the safe outcome, not a
		// failure: with --binary / --match-binary we were asked to remove one
		// specific install's job, and this is not it (MEL-267).
		var mismatch *scheduler.JobBinaryMismatchError
		if errors.As(err, &mismatch) {
			fmt.Printf("left the scheduled job alone: %v\n", mismatch)
			return nil
		}
		return err
	}
	fmt.Printf("uninstalled scheduler job %q\n", cfg.Label)
	return nil
}

// parseSchedulerFlags builds a scheduler.Config from shared install/uninstall
// flags. Returns (cfg, fs, nil); fs is nil when -h was handled (caller returns).
func parseSchedulerFlags(name string, args []string) (scheduler.Config, *flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var (
		interval = fs.Int("interval", scheduler.DefaultIntervalMinutes, "scan interval in minutes")
		label    = fs.String("label", scheduler.DefaultLabel, "scheduler job label (use a distinct value to avoid clobbering an existing job)")
		binary   = fs.String("binary", "", "path to the claude-janitor binary to schedule (default: this executable); on uninstall, only remove the job if it runs this exact path")
		logPath  = fs.String("log", "", "log file path (default: ~/.claude/logs/claude-janitor.log)")
	)
	// --match-binary asks out loud for what an explicit --binary already implies
	// on uninstall. Scripts should pass it anyway: a binary built before MEL-267
	// accepts --binary (an install flag it always had) yet still removes the job
	// by label alone, whereas it rejects an unknown flag before touching anything.
	// Install has no use for it, so only uninstall defines it.
	var matchBinary bool
	if name == "uninstall" {
		fs.BoolVar(&matchBinary, "match-binary", false, "only remove the job if it runs --binary (default: this executable); implied by an explicit --binary")
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return scheduler.Config{}, nil, nil
		}
		return scheduler.Config{}, nil, err
	}

	// An explicit --binary names one specific install, so uninstall must not
	// take out a job belonging to some other copy that shares the label. A
	// defaulted path means "whatever is running", which carries no such claim,
	// so the old label-only behavior stays unless --match-binary asks (MEL-267).
	binaryExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "binary" {
			binaryExplicit = true
		}
	})

	binPath := *binary
	if binPath == "" {
		self, err := os.Executable()
		if err != nil {
			return scheduler.Config{}, nil, fmt.Errorf("cannot resolve own path (pass --binary): %w", err)
		}
		binPath = self
	}
	// Absolute either way: the scheduler runs the job from another directory,
	// and on uninstall a relative --binary could never match the absolute path
	// the job records, so the check would leave this install's own job behind.
	if abs, err := filepath.Abs(binPath); err == nil {
		binPath = abs
	}

	return scheduler.Config{
		BinaryPath:      binPath,
		RunArgs:         []string{"run"}, // README CLI contract; MEL-92 owns `run`
		IntervalMinutes: *interval,
		Label:           *label,
		LogPath:         *logPath,
		MatchBinaryPath: binaryExplicit || matchBinary,
	}, fs, nil
}
