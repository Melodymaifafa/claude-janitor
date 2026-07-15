# claude-janitor

Cross-platform CLI that reclaims memory by killing idle Claude Code session
processes (default: idle > 2h). It does **not** delete session files — history
is kept so you can resume the conversation later.

It runs on macOS, Linux, and Windows from a single static Go binary, and can
register itself on your OS scheduler (launchd / systemd / Task Scheduler) to run
periodically.

## What it does

Each pass finds and kills two kinds of idle Claude Code sessions:

- **Terminal `--resume` sessions** — idle judged by the mtime of
  `~/.claude/projects/*/<uuid>.jsonl`.
- **Desktop-app background sessions** — idle judged by the mtime of the desktop
  app's per-session JSON, matched back to a running process by start time. These
  ignore `SIGTERM`, so they get `SIGKILL`.

Active sessions have a fresh transcript mtime, so they are never touched. Session
files are never deleted — you can always resume.

## Install

### One-line install (macOS / Linux)

From a source checkout (needs [Go](https://go.dev/dl); builds a static binary and
drops it on your PATH):

```sh
git clone https://github.com/maicuigua/claude-janitor && cd claude-janitor
./install.sh
```

By default it installs to `/usr/local/bin` (or `~/.local/bin` if that isn't
writable). Override with `PREFIX=/some/where ./install.sh`. Remove it with
`./install.sh --uninstall`.

Once binary releases are published (see **Publishing** below), the same script
also serves the download form:

```sh
curl -fsSL https://raw.githubusercontent.com/maicuigua/claude-janitor/main/install.sh | sh
```

### Homebrew (macOS / Linux) — after a release is published

```sh
brew install maicuigua/tap/claude-janitor
```

### Windows

Scoop:

```powershell
scoop bucket add maicuigua https://github.com/maicuigua/scoop-bucket
scoop install claude-janitor
```

Winget:

```powershell
winget install maicuigua.claude-janitor
```

Or grab the `.zip` for your architecture from the
[Releases](https://github.com/maicuigua/claude-janitor/releases) page and put
`claude-janitor.exe` somewhere on your `PATH`.

### Build from source (any OS)

```sh
go build -o claude-janitor .   # produces claude-janitor(.exe)
```

## Usage

```
claude-janitor [run] [flags]     # one scan + kill pass (default command)
claude-janitor install [flags]   # register the periodic scan on the OS scheduler
claude-janitor uninstall         # remove the scheduled job
claude-janitor version
```

Always start with a dry run — it prints what it *would* kill and kills nothing:

```sh
claude-janitor run --dry-run
```

### Scan flags (`run`)

| Flag | Default | Meaning |
|------|---------|---------|
| `--dry-run` | off | print what would be killed, kill nothing |
| `--idle-min N` | `120` | a session idle for more than N minutes is dead |
| `--interval-min N` | `30` | scan interval in minutes (recorded for the scheduler) |
| `--projects-dir PATH` | per-OS | override the terminal-transcript root |
| `--sessions-dir PATH` | per-OS | override the desktop-app session root |

### Run it on a schedule

`install` registers a recurring job using the native scheduler for your OS, and
runs a pass immediately (at login too):

```sh
claude-janitor install                 # every 30 min by default
claude-janitor install --interval 60   # every 60 min
claude-janitor uninstall               # remove it
```

| OS | Mechanism |
|----|-----------|
| macOS | launchd LaunchAgent (`~/Library/LaunchAgents/`), `StartInterval` + `RunAtLoad` |
| Linux | systemd user timer; falls back to a crontab line if systemd is absent |
| Windows | Task Scheduler (`schtasks`), every-N-min + at logon |

Install flags: `--interval N` (minutes), `--label NAME` (job label; use a
distinct value to avoid clobbering an existing job), `--binary PATH` (which
binary to schedule; defaults to the running one), `--log PATH` (defaults to
`~/.claude/logs/claude-janitor.log`).

## Publishing (maintainer)

Releases are cut by [goreleaser](https://goreleaser.com) via GitHub Actions on a
`v*` tag ([`.goreleaser.yaml`](.goreleaser.yaml),
[`.github/workflows/release.yml`](.github/workflows/release.yml)):

```sh
git tag v0.1.0 && git push origin v0.1.0
```

This always builds Mac/Linux/Windows archives + checksums and attaches them to a
GitHub Release. To *also* auto-update the package managers you must first create
the registry repos and provide tokens as GitHub Actions secrets:

- **Homebrew** — a `maicuigua/homebrew-tap` repo + `HOMEBREW_TAP_GITHUB_TOKEN`.
- **Scoop** — a `maicuigua/scoop-bucket` repo + `SCOOP_BUCKET_GITHUB_TOKEN`.
- **Winget** — a `maicuigua/winget-pkgs` fork + `WINGET_GITHUB_TOKEN` (opens a PR
  against `microsoft/winget-pkgs`).

Validate the pipeline locally without publishing:

```sh
goreleaser check
goreleaser release --snapshot --clean   # builds all binaries into ./dist
```

## Reference implementation

The proven Mac logic lives in `~/.claude/scripts/claude-session-janitor.sh`
(launchd, every 30 min). This repo ports it to a single Go binary; the full
cross-platform design is in [`docs/design.md`](docs/design.md).
