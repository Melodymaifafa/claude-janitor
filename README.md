# claude-janitor

Cross-platform CLI that reclaims memory by killing idle Claude Code session
processes (default: idle > 2h). It does **not** delete session files — history
is kept so you can resume the conversation later.

It runs on macOS, Linux, and Windows from a single static Go binary, and can
register itself on your OS scheduler (launchd / systemd / Task Scheduler) to run
periodically.

## What it does

Each pass finds and kills two kinds of idle Claude Code sessions. Both are judged
by the same signal: the mtime of the session's transcript
(`~/.claude/projects/*/<uuid>.jsonl`), which every message updates.

- **Terminal `--resume` sessions** — the transcript uuid is in the command line.
- **Desktop-app background sessions** — no uuid in the command line, so each
  process is paired to the transcript born just after it started (measured 4–14 s
  on a real Mac). When several transcripts could be its own, it is killed only if
  every one of them is stale; when none can be, it is left alone rather than
  killed on a guess. These sessions ignore `SIGTERM`, so they get `SIGKILL`.

Active sessions have a fresh transcript mtime, so they are never touched. Session
files are never deleted — you can always resume.

## Install

### One-line install (macOS / Linux)

From a source checkout (needs [Go](https://go.dev/dl); builds a static binary and
drops it on your PATH):

```sh
git clone https://github.com/Melodymaifafa/claude-janitor && cd claude-janitor
./install.sh
```

By default it installs to `/usr/local/bin` (or `~/.local/bin` if that isn't
writable). Override with `PREFIX=/some/where ./install.sh`. Remove it with
`./install.sh --uninstall`.

Once binary releases are published (see **Publishing** below), the same script
also serves the download form:

```sh
curl -fsSL https://raw.githubusercontent.com/Melodymaifafa/claude-janitor/main/install.sh | sh
```

### Download a prebuilt binary (any OS)

Every `v*` tag publishes signed-off archives for macOS, Linux and Windows on
amd64 and arm64. Grab the one for your platform from the
[Releases](https://github.com/Melodymaifafa/claude-janitor/releases) page,
unpack it, and put `claude-janitor` (`claude-janitor.exe` on Windows) somewhere
on your `PATH`.

macOS ships the binary unsigned, so clear the quarantine flag once:

```sh
xattr -dr com.apple.quarantine /usr/local/bin/claude-janitor
```

### Package managers — not live yet

Homebrew, Scoop and Winget are wired up in the release pipeline but switched
off: they need registry repos and tokens that do not exist yet (see
**Publishing**). Once they are live these will work, and this section will say
so:

```sh
brew install Melodymaifafa/tap/claude-janitor          # macOS / Linux
```

```powershell
scoop bucket add Melodymaifafa https://github.com/Melodymaifafa/scoop-bucket
scoop install claude-janitor
winget install Melodymaifafa.claude-janitor
```

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
| `--projects-dir PATH` | `~/.claude/projects` | override the transcript root (both session kinds) |

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
GitHub Release, using only the automatic `GITHUB_TOKEN`.

The Homebrew / Scoop / Winget steps are off until their registry repos and
tokens exist. Each block in [`.goreleaser.yaml`](.goreleaser.yaml) carries a
`skip_upload` guard keyed on its own token, so a missing secret skips that one
publish step instead of failing the release — the binaries still ship and the
workflow still goes green. Adding a secret turns its step on; nothing else to
change. To switch them on, create under the `Melodymaifafa` account:

- **Homebrew** — a `Melodymaifafa/homebrew-tap` repo + `HOMEBREW_TAP_GITHUB_TOKEN`.
- **Scoop** — a `Melodymaifafa/scoop-bucket` repo + `SCOOP_BUCKET_GITHUB_TOKEN`.
- **Winget** — a `Melodymaifafa/winget-pkgs` fork + `WINGET_GITHUB_TOKEN` (opens a
  PR against `microsoft/winget-pkgs`).

Each token is a GitHub PAT with `repo` scope, set at
Settings → Secrets and variables → Actions on this repo.

Validate the pipeline locally without publishing:

```sh
goreleaser check
goreleaser release --snapshot --clean   # builds all binaries into ./dist
```

## Design

This repo is the single source of truth for the janitor logic — it started as a
port of a Mac-only shell script, which is now retired. The full cross-platform
design, including why idleness is judged by transcript mtime and nothing else,
is in [`docs/design.md`](docs/design.md).
