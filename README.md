# claude-janitor

Cross-platform CLI that reclaims memory by killing idle Claude Code session
processes (default: idle > 2h). It does **not** delete session files — history
is kept so you can resume the conversation later.

Status: **design spike** (MEL-91). The architecture is locked in
[`docs/design.md`](docs/design.md); implementation follows in MEL-90's three
downstream slices (kill logic / scheduler install / packaging).

## What it does

Scans on a timer and kills two kinds of idle Claude Code sessions:

- **Terminal `--resume` sessions** — idle judged by the mtime of
  `~/.claude/projects/*/<uuid>.jsonl`.
- **Desktop-app background sessions** — idle judged by the mtime of the
  desktop app's per-session JSON; matched back to a running process by start
  time. These ignore `SIGTERM`, so they get `SIGKILL`.

## Planned commands

```
claude-janitor run [--dry-run]      # scan + kill once (--dry-run only prints)
claude-janitor install              # install the OS scheduled job
claude-janitor uninstall            # remove the scheduled job
```

Idle threshold and scan interval are configurable (defaults: 120 min / 30 min).

## Install

To be wired up via Homebrew / Scoop / Winget / install script (MEL-90 slice 3).

## Reference implementation

The proven Mac logic lives in `~/.claude/scripts/claude-session-janitor.sh`
(launchd, every 30 min). This repo ports it to a single Go binary that runs on
macOS, Linux, and Windows.
