package main

import (
	"path/filepath"
	"testing"
)

// Which uninstall invocations ask for the ownership check (MEL-267).
// install.sh relies on --match-binary turning it on.
func TestParseSchedulerFlagsMatchBinary(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		match bool
	}{
		{"plain uninstall keeps label-only removal", nil, false},
		{"explicit --binary implies the check", []string{"--binary", "/opt/cj"}, true},
		{"--match-binary alone checks the running binary", []string{"--match-binary"}, true},
		{"what install.sh passes", []string{"--binary", "/opt/cj", "--match-binary"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, fs, err := parseSchedulerFlags("uninstall", tc.args)
			if err != nil || fs == nil {
				t.Fatalf("parse: fs=%v err=%v", fs, err)
			}
			if cfg.MatchBinaryPath != tc.match {
				t.Errorf("MatchBinaryPath = %v, want %v", cfg.MatchBinaryPath, tc.match)
			}
			if cfg.BinaryPath == "" {
				t.Error("BinaryPath empty: the check would have nothing to compare against")
			}
		})
	}
}

// A relative --binary (e.g. install.sh run with PREFIX=stage) must come out
// absolute, or uninstall could never match the absolute path the job records.
func TestParseSchedulerFlagsMakesBinaryAbsolute(t *testing.T) {
	for _, name := range []string{"install", "uninstall"} {
		cfg, fs, err := parseSchedulerFlags(name, []string{"--binary", filepath.Join("stage", "bin", "cj")})
		if err != nil || fs == nil {
			t.Fatalf("%s: parse: fs=%v err=%v", name, fs, err)
		}
		if !filepath.IsAbs(cfg.BinaryPath) {
			t.Errorf("%s: BinaryPath = %q, want absolute", name, cfg.BinaryPath)
		}
	}
}

// Install has no use for the check, so it must reject the flag rather than
// quietly accept it.
func TestParseSchedulerFlagsInstallRejectsMatchBinary(t *testing.T) {
	if _, _, err := parseSchedulerFlags("install", []string{"--match-binary"}); err == nil {
		t.Fatal("install accepted --match-binary")
	}
}
