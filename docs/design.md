# claude-janitor — Cross-Platform Design (MEL-91 spike)

Status: **decided**. This doc locks the architecture so MEL-90's three downstream
slices (kill logic / scheduler install / packaging) can start without re-deciding.

Every section below ends in a concrete decision. Confidence is marked per claim:

- **VERIFIED** — checked on this Mac (`ls`/`stat`) or in the existing script.
- **RESEARCHED** — from official docs / library docs (cited at bottom).
- **BEST KNOWN — confirm on target OS** — plausible but not machine-verifiable
  from a Mac; the implementer must `ls` it on real Linux/Windows before relying.

---

## 0. Reference implementation (already proven on Mac)

Source: `~/.claude/scripts/claude-session-janitor.sh` (zsh) +
`~/Library/LaunchAgents/com.maicuigua.claude-janitor.plist` (launchd, every
1800s + RunAtLoad). The Go port must reproduce this logic exactly:

- **A — sessions with an id in argv**: command line carries `--resume <uuid>`
  (space or `=` form); find `~/.claude/projects/*/<uuid>.jsonl`; if its mtime is
  older than `IDLE_MIN` (120) → kill the process tree and its disclaimer
  wrapper parent (a non-disclaimer parent is never touched — for terminal
  sessions it is the user's shell).
- **B — desktop-app background sessions**: `--output-format stream-json`, no
  `--resume`, no uuid in argv. Pair each process to the transcript **born**
  4–14 s after the process start (window `[start−15 s, start+120 s]`, global
  greedy match, both sides exclusive); judge idleness by that transcript's
  mtime. Unpairable processes are never killed.
- Both classes use **SIGKILL (-9)**, not SIGTERM — the background processes
  ignore TERM.
- **Superseded (2026-07-30, do not reintroduce):** B-class originally judged
  idleness by the desktop app's `local_<uuid>.json` mtime. That file is a
  UI-event snapshot rewritten whole on create/reopen and never during task
  execution, so "idle N min" really meant "born N min ago": sessions running
  long tasks were killed mid-task at age 2 h, and the stale json re-matched
  neighbor processes on later passes (observed double-kills). The transcript
  is the only reliable activity signal.

---

## 1. Language decision — **Go** (not Python)

**Decision: Go.** Single static binary, `gopsutil` for all process work,
`goreleaser` for release.

Why Go wins for *this* tool:

- **Windows "one command installs it" only works with a single binary.** Windows
  users are the least likely to have Python/Node preinstalled. A Go binary has
  zero runtime dependency; Python+pipx forces the user to install Python first —
  exactly the friction MEL-90 wants gone. This is the deciding factor.
- **The hardest cross-platform part — process listing, start time, process
  tree, kill — is exactly what `gopsutil` abstracts** (`Processes()`,
  `CreateTime()`, `Children()`, `Kill()`), one API across macOS/Linux/Windows
  (RESEARCHED). In Python the psutil equivalent exists too, so this is a tie —
  but it removes Python's only advantage (less code).
- **`goreleaser` ships Homebrew + Scoop + Winget + raw binaries from one YAML +
  one GitHub Actions job** (RESEARCHED). It covers every acceptance-criteria
  install path in MEL-90 at once.

Python (psutil + pipx) is the runner-up — slightly less code, but the Windows
runtime-install barrier is disqualifying for the stated goal. **Not chosen.**

---

## 2. Session file paths + process identification (per OS)

### A. Terminal `--resume` sessions — the jsonl transcript

| OS | Path | Confidence |
|----|------|-----------|
| macOS | `~/.claude/projects/<url-encoded-project>/<uuid>.jsonl` | **VERIFIED** — 4654 files present; mtime/birthtime readable via `stat -f '%m %B'` |
| Linux | `~/.claude/projects/<url-encoded-project>/<uuid>.jsonl` | **RESEARCHED** — Claude Code uses the same `$HOME/.claude` layout on Linux |
| Windows native | `%USERPROFILE%\.claude\projects\<...>\<uuid>.jsonl` | **RESEARCHED** — `~/.claude` resolves to `%USERPROFILE%\.claude`; CLI config at `%USERPROFILE%\.claude.json` |

The project subfolder is the absolute project path with separators rewritten to
`-` (e.g. `/Users/x/proj` → `-Users-x-proj`). The janitor never parses this name;
it only globs `projects/*/<uuid>.jsonl` and reads mtime. **Same logic all 3 OSes.**

**Process identification (A):** find processes whose command line contains
`--resume <uuid>`, extract the uuid, look up that `<uuid>.jsonl`. Cross-platform
via `gopsutil` `process.CmdlineSlice()`.

### B. Desktop-app background sessions — transcript pairing by birth time

B-class sessions write the **same** `projects/*/<id>.jsonl` transcripts as
A-class (VERIFIED on Mac: the desktop app's `local_*.json` field
`cliSessionId` names the transcript, which appears 4–14 s after the session
process starts and is appended on every message). So B-class needs **no
OS-divergent path at all** — only a way to map process → transcript:

**Process identification (B):** processes with `--output-format stream-json`
and no `--resume`. Pair each process to the transcript whose **birth time**
falls in `[CreateTime−PairBefore, CreateTime+PairAfter]` (defaults 15 s /
120 s; measured creation lag 4–14 s). Matching is global-greedy by smallest
gap with both sides exclusive, so sessions started seconds apart each keep
their own transcript. A process with no pairable transcript (e.g. a re-opened
session appending its old transcript, whose birth predates the new process) is
**never killed** — leaking one process beats killing an active session.

Where the filesystem exposes no real birth time (some Linux setups: btime
needs statx and ext4/xfs), B-class pairing is deliberately inert — the pass
logs the degradation and touches nothing. A-class is unaffected.

**Superseded:** the earlier design reverse-matched the desktop app's
`local_<uuid>.json` (per-OS paths, `--sessions-dir` override) to processes by
start time. Killed active sessions mid-task — see §0. Do not reintroduce.

---

## 3. Windows target — **native Windows first; WSL is free**

**Decision: target native Windows** (single `.exe` + Task Scheduler + Windows
paths). WSL is **not** a separate target — a WSL environment *is* Linux, so the
Linux build already covers it.

Why native, not WSL-only:

- Claude Code runs **natively on Windows** since late 2025 (RESEARCHED); telling
  users "first install WSL" reintroduces the very setup friction MEL-90 removes.
- The Go binary cross-compiles to `windows/amd64` and `windows/arm64` for free,
  so supporting native costs ~nothing extra.
- A user who *does* run Claude Code inside WSL just installs the **Linux** binary
  inside that WSL distro and uses the Linux scheduler (systemd/cron). No special
  case, no extra slice.

Residual unknown carried into implementation: none path-related — B-class now
pairs against the same `~/.claude/projects` transcripts as A-class (§2.B), so
the MSIX desktop-path question is moot. Windows birth-time support for pairing
is native; only exotic filesystems degrade B-class to inert.

---

## 4. Cross-platform implementation of the 4 core steps

| Step | macOS | Linux | Windows native |
|------|-------|-------|----------------|
| **1. Find dead sessions** | glob `~/.claude/projects/*/*.jsonl`; idle = transcript mtime older than threshold | same, `~/.claude/...` | same, `%USERPROFILE%\.claude\...` | 
| **2. Map session → process** | `gopsutil` `process.Processes()`, filter on `CmdlineSlice()` (`--resume`/`stream-json`); B-class paired by transcript birth vs `CreateTime()` | same | same |
| **3. Kill process tree** | `gopsutil` `proc.Children()` recursively, then `proc.Kill()` (= SIGKILL); also kill the disclaimer wrapper parent | same (POSIX SIGKILL) | `proc.Kill()` → `TerminateProcess` under the hood; walk `Children()` for the tree | 
| **4. Install/uninstall scheduled job** | write+`launchctl load` a LaunchAgent plist | write a **systemd user timer** (`.service`+`.timer`, `systemctl --user enable --now`); **fallback to crontab** when systemd absent | `schtasks /Create` (trigger: ONLOGON + every-30-min) / `schtasks /Delete` |

Notes:

- One `gopsutil` codepath covers steps 1–3 on all OSes; only step 4 (the
  scheduler) is genuinely per-OS, isolated behind an `installer` interface with
  three implementations.
- **Linux step 4 = systemd user timer, cron fallback.** systemd user timers give
  `Persistent=true` (runs a missed job at next boot) and per-job journald logs —
  the closest match to launchd's `RunAtLoad` + StartInterval. On non-systemd
  hosts, fall back to a `*/30 * * * *` crontab line (RESEARCHED).
- **Windows step 4 = `schtasks.exe`** (CLI, no PowerShell module needed); use the
  "run as soon as possible after a missed start" option to mirror `RunAtLoad`.

---

## 5. Idle-detection logic (cross-platform equivalents)

- **Idle = transcript mtime older than `IDLE_MIN` (120 min).** mtime is read via
  Go `os.Stat()` → `FileInfo.ModTime()` on all 3 OSes (replaces Mac `stat -f '%m'`
  / `find -mmin`). **VERIFIED** the Mac files carry usable mtime, updated on
  every message.
- **B-class needs the transcript's *creation* time** (birthtime) as the pairing
  anchor. macOS exposes birthtime (**VERIFIED**); Windows exposes it natively;
  Linux exposes it via `statx` on ext4/xfs but **not universally**. **Decision:**
  where birthtime is missing, the transcript is excluded from pairing and
  B-class goes inert with a log line — a mtime fallback is NOT safe here,
  because an append-forever transcript's mtime is "now", not creation time.
- **SIGTERM-ignoring background processes → SIGKILL.** `gopsutil` `Kill()` sends
  SIGKILL on POSIX and calls `TerminateProcess` (unconditional, no catchable
  signal) on Windows — both are the non-ignorable kill, matching the Mac `-9`
  behavior (RESEARCHED).
- Pairing window `PairBefore`/`PairAfter` = 15 s / 120 s (measured transcript
  creation lag on-box: 4–14 s; the window leaves ~10× headroom without
  swallowing a neighbor session started minutes apart).

---

## Open items for implementation (not blockers)

1. Confirm Linux birthtime availability per filesystem (statx on ext4/xfs);
   until then B-class is inert on such boxes and A-class carries the load.
2. Consider a statx-based `ftime_linux.go` so Linux desktops get B-class pairing.

None block the language/path/Windows decisions above — they are on-box
verifications for the slice that implements B-class on each OS.

---

## CLI contract (MEL-92 — for MEL-93 scheduler alignment)

The kill-logic slice owns the CLI root and the scan-and-kill command. The
MEL-93 scheduler slice invokes this command on its timer.

- **Default command == `run`**: `claude-janitor` and `claude-janitor run` both
  perform exactly ONE scan-and-kill pass over A-class + B-class, then exit.
- The scheduler should call `claude-janitor run` (idempotent single pass).
- `install` / `uninstall` (the OS scheduled job) are **MEL-93's** to add at the
  same CLI root; they are not implemented in this slice.

Flags (all optional):

| Flag | Default | Meaning |
|------|---------|---------|
| `--dry-run` | off | print "would kill X", do not kill |
| `--idle-min N` | 120 | idle threshold in minutes |
| `--interval-min N` | 30 | scan interval in minutes — recorded only; the scheduler consumes it |
| `--projects-dir PATH` | `~/.claude/projects` | override the transcript root (both classes) |

Code layout: `main.go` (CLI root) + `internal/janitor/` (one gopsutil codepath
for steps 1–3, behind clean functions; per-OS bits isolated in build-tagged
`paths_*.go` / `ftime_*.go`). MEL-93 adds an `installer` interface + three
per-OS implementations at the same root; expect a minor merge at `main.go`.

---

## Sources

- Claude Code `.claude` directory layout: https://code.claude.com/docs/en/claude-directory
- Claude Code config file locations: https://inventivehq.com/knowledge-base/claude/where-configuration-files-are-stored
- Claude Code on Windows (native vs WSL): https://code.claude.com/docs/en/setup , https://claudelab.net/en/articles/claude-code/claude-code-windows-native-wsl2-complete-guide
- Claude Desktop data dir / Windows MSIX path: https://exchangepedia.com/2026/04/claudetools-claude-desktop-powershell-module.html
- gopsutil process API (Children/CreateTime/Kill): https://pkg.go.dev/github.com/shirou/gopsutil/v3/process
- goreleaser (Homebrew/Scoop/Winget, GitHub Actions): https://goreleaser.com/ , https://goreleaser.com/customization/ci/actions/
- systemd timers vs cron vs Windows Task Scheduler: https://xtom.com/blog/systemd-vs-cron-linux-task-scheduling/ , https://cronjobpro.com/blog/windows-task-scheduler
