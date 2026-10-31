package main

import (
	"runtime/debug"
	"testing"
)

// "claude-janitor version" must name the installed commit even without release
// ldflags, or nobody can tell an outdated install apart (MEL-532).
func TestBuildStamp(t *testing.T) {
	stamped := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: "6110e10c4f2a9e8d7b6a5f4e3d2c1b0a99887766"},
		{Key: "vcs.time", Value: "2026-10-31T09:12:00Z"},
	}}
	cases := []struct {
		name                 string
		commit, date         string
		info                 *debug.BuildInfo
		wantCommit, wantDate string
	}{
		{"ldflags win over the build info", "abc1234def", "2026-11-01T00:00:00Z", stamped, "abc1234def", "2026-11-01T00:00:00Z"},
		{"plain go build reads the vcs stamp", "none", "unknown", stamped, "6110e10", "2026-10-31T09:12:00Z"},
		{"no build info keeps the defaults", "none", "unknown", nil, "none", "unknown"},
		{"build info without vcs keeps the defaults", "none", "unknown", &debug.BuildInfo{}, "none", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, d := buildStamp(tc.commit, tc.date, tc.info)
			if c != tc.wantCommit || d != tc.wantDate {
				t.Errorf("buildStamp = (%q, %q), want (%q, %q)", c, d, tc.wantCommit, tc.wantDate)
			}
		})
	}
}
