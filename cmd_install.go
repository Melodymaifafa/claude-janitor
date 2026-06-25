package main

import (
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
		binary   = fs.String("binary", "", "path to the claude-janitor binary to schedule (default: this executable)")
		logPath  = fs.String("log", "", "log file path (default: ~/.claude/logs/claude-janitor.log)")
	)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return scheduler.Config{}, nil, nil
		}
		return scheduler.Config{}, nil, err
	}

	binPath := *binary
	if binPath == "" {
		self, err := os.Executable()
		if err != nil {
			return scheduler.Config{}, nil, fmt.Errorf("cannot resolve own path (pass --binary): %w", err)
		}
		if abs, err := filepath.Abs(self); err == nil {
			self = abs
		}
		binPath = self
	}

	return scheduler.Config{
		BinaryPath:      binPath,
		RunArgs:         []string{"run"}, // README CLI contract; MEL-92 owns `run`
		IntervalMinutes: *interval,
		Label:           *label,
		LogPath:         *logPath,
	}, fs, nil
}
