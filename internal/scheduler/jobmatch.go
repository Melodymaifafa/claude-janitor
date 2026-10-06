package scheduler

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// This file holds the "is this job really mine?" check (MEL-267).
//
// Uninstall used to match on Config.Label alone. Every install uses the same
// default label, so uninstalling a copy that lived in a temp dir deleted the
// job belonging to the user's real install -- which is exactly what happened
// on 2026-10-05: the Mac's cleanup job vanished for 33 hours. When
// Config.MatchBinaryPath is set, Uninstall first reads which program the
// registered job actually runs and declines unless that program is
// Config.BinaryPath.

// JobBinaryMismatchError reports that a scheduled job exists under the
// requested label but runs a different program, so Uninstall left it alone.
// Callers that treat declining as the safe outcome (the CLI does) should match
// it with errors.As and not surface it as a failure.
type JobBinaryMismatchError struct {
	Label string // scheduler label that was asked for
	Want  string // Config.BinaryPath -- the binary the caller is removing
	Got   string // what the registered job runs; "" when it could not be read
}

func (e *JobBinaryMismatchError) Error() string {
	if e.Got == "" {
		return fmt.Sprintf("scheduler job %q exists but does not say which program it runs, so it was left alone (expected %s)",
			e.Label, e.Want)
	}
	return fmt.Sprintf("scheduler job %q runs %s, not %s", e.Label, e.Got, e.Want)
}

// jobProber reports which program the registered job runs. Each installer
// implements it against its own artifact (plist, unit file, crontab block,
// schtasks query). found=false means nothing is registered under the label.
// An artifact that exists but whose program cannot be read returns
// ("", true, nil) so the caller refuses rather than guesses.
type jobProber interface {
	installedBinary(cfg Config) (path string, found bool, err error)
}

// guardBinaryMatch runs at the top of every Uninstall, before anything is
// stopped or deleted. It returns nil when the caller did not ask for the check,
// when no job is registered (Uninstall is then a no-op anyway), or when the
// registered job runs cfg.BinaryPath.
func guardBinaryMatch(p jobProber, cfg Config) error {
	if !cfg.MatchBinaryPath {
		return nil
	}
	got, found, err := p.installedBinary(cfg)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if sameBinaryPath(got, cfg.BinaryPath) {
		return nil
	}
	return &JobBinaryMismatchError{Label: cfg.Label, Want: cfg.BinaryPath, Got: got}
}

// sameBinaryPath reports whether two recorded paths name the same executable.
// Empty never matches: "I could not tell" must not pass as "it matches".
func sameBinaryPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if pathEqual(ca, cb) {
		return true
	}
	// Best effort for the same file reached by different names, e.g. macOS
	// /var/folders/... vs /private/var/folders/..., or a symlinked bin dir.
	ra, errA := filepath.EvalSymlinks(ca)
	rb, errB := filepath.EvalSymlinks(cb)
	return errA == nil && errB == nil && pathEqual(ra, rb)
}

func pathEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// plistFirstProgramArgument pulls ProgramArguments[0] out of a launchd plist.
// Returns "" when the key is absent or the file does not parse, which the
// caller turns into "leave the job alone".
func plistFirstProgramArgument(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var (
		lastKey  string
		inKey    bool
		inArgs   bool
		inString bool
		buf      strings.Builder
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "key":
				inKey, buf = true, strings.Builder{}
			case "array":
				inArgs = lastKey == "ProgramArguments"
			case "string":
				if inArgs {
					inString, buf = true, strings.Builder{}
				}
			}
		case xml.CharData:
			if inKey || inString {
				buf.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "key":
				if inKey {
					lastKey, inKey = strings.TrimSpace(buf.String()), false
				}
			case "string":
				if inString {
					return strings.TrimSpace(buf.String())
				}
			case "array":
				if inArgs {
					return "" // ProgramArguments was empty
				}
			}
		}
	}
}

// firstShellToken returns the program part of a command line, honoring the
// double quotes shellJoin puts around paths containing spaces.
func firstShellToken(line string) string {
	s := strings.TrimLeft(line, " \t")
	if s == "" {
		return ""
	}
	if s[0] == '"' {
		if end := strings.IndexByte(s[1:], '"'); end >= 0 {
			return s[1 : 1+end]
		}
		return ""
	}
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// unitExecStart returns the value of the first ExecStart= line of a systemd
// unit file, or "".
func unitExecStart(text string) string {
	for _, ln := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(ln), "ExecStart="); ok {
			return v
		}
	}
	return ""
}

// cronLineCommand returns the command part of a crontab line: whatever follows
// an @keyword, or whatever follows the five time fields. "" for blanks and
// comments.
func cronLineCommand(line string) string {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return ""
	}
	fields := 5
	if strings.HasPrefix(s, "@") {
		fields = 1
	}
	for n := 0; n < fields; n++ {
		i := strings.IndexAny(s, " \t")
		if i < 0 {
			return ""
		}
		s = strings.TrimLeft(s[i:], " \t")
	}
	return s
}

// schtasksTaskToRun reads the "Task To Run:" value out of
// `schtasks /Query /FO LIST /V` output. Returns "" on a localized Windows
// where the label differs, which the caller turns into "leave it alone".
func schtasksTaskToRun(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		name, value, ok := strings.Cut(ln, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Task To Run") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
