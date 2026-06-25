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

- **A — terminal sessions**: list processes whose command line contains
  `--resume <uuid>`; find `~/.claude/projects/*/<uuid>.jsonl`; if its mtime is
  older than `IDLE_MIN` (120) → kill the process **and its wrapper parent**.
- **B — desktop-app background sessions**: these run with
  `--output-format stream-json` and **no** `--resume`, so the command line has
  no uuid. Instead, iterate the desktop app's `local_<uuid>.json` files; for any
  whose mtime is older than `IDLE_MIN`, reverse-match it to a running process
  whose **start time** is within `MATCH_TOL` (240s) **before** the json's
  birth/creation time, and kill that. Active sessions have fresh mtime, so they
  never enter this loop → no false kills.
- Both classes use **SIGKILL (-9)**, not SIGTERM — the background processes
  ignore TERM.

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

### B. Desktop-app background sessions — the per-session JSON

This is the **only genuinely OS-divergent path** and the main residual risk.

| OS | Path | Confidence |
|----|------|-----------|
| macOS | `~/Library/Application Support/Claude/claude-code-sessions/<a>/<b>/local_<uuid>.json` | **VERIFIED** — 395 files present; two-level UUID nesting confirmed |
| Linux | `~/.config/Claude/claude-code-sessions/**/local_<uuid>.json` | **BEST KNOWN — confirm on target OS** — Claude desktop on Linux uses `~/.config/Claude/`; the `claude-code-sessions` subtree is assumed identical to Mac |
| Windows native (MSIX) | `%LOCALAPPDATA%\Packages\Claude_pzs8sxrjxfjjc\LocalCache\Roaming\Claude\claude-code-sessions\**\local_<uuid>.json` | **BEST KNOWN — confirm on target OS** — MSIX virtualizes `%APPDATA%\Claude` into this package-local path; the visible `%APPDATA%\Claude` may be a redirected view |

Implementation rule: make the B-class root a **per-OS lookup with an override
flag** (`--sessions-dir`), and at startup verify the dir exists; if not, log and
skip B-class cleanup rather than crash. The implementer confirms the Linux/Windows
roots by `ls` on a real box before trusting them.

**Process identification (B):** processes with `--output-format stream-json` and
no `--resume`. Reverse-match idle `local_<uuid>.json` (mtime > threshold) to the
running process whose `CreateTime()` falls within `MATCH_TOL` before the json's
creation time. `gopsutil` `CreateTime()` gives the process start time on all 3 OSes
(RESEARCHED) — this replaces the Mac-only `ps -axo lstart` + `date -j` parsing.

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

Residual unknown carried into implementation: the native-Windows **B-class
desktop path** (MSIX virtualization, §2). The native CLI `--resume` path (A) is
high-confidence; B needs on-box confirmation.

---

## 4. Cross-platform implementation of the 4 core steps

| Step | macOS | Linux | Windows native |
|------|-------|-------|----------------|
| **1. Find dead sessions** | glob `~/.claude/projects/*/<uuid>.jsonl` + desktop `local_*.json`; idle = mtime older than threshold | same, `~/.claude/...` + `~/.config/Claude/...` | same, `%USERPROFILE%\.claude\...` + MSIX desktop path | 
| **2. Map session → process** | `gopsutil` `process.Processes()`, filter on `CmdlineSlice()` (`--resume`/`stream-json`); B-class reverse-match via `CreateTime()` | same | same |
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

Logic is unchanged from the Mac script; only the syscalls differ:

- **Idle = jsonl/json mtime older than `IDLE_MIN` (120 min).** mtime is read via
  Go `os.Stat()` → `FileInfo.ModTime()` on all 3 OSes (replaces Mac `stat -f '%m'`
  / `find -mmin`). **VERIFIED** the Mac files carry usable mtime.
- **B-class needs file *creation* time** (birthtime) for the reverse-match. macOS
  exposes birthtime (`stat -f '%B'`, **VERIFIED**); Windows exposes it natively;
  Linux exposes it via `statx` on ext4/xfs but **not universally**. **Decision:**
  where birthtime is missing/zero, fall back to mtime as the match anchor — for an
  *idle* json (written once at creation), mtime ≈ creation time, so the fallback is
  safe. **BEST KNOWN — confirm on target OS** for the Linux birthtime path.
- **SIGTERM-ignoring background processes → SIGKILL.** `gopsutil` `Kill()` sends
  SIGKILL on POSIX and calls `TerminateProcess` (unconditional, no catchable
  signal) on Windows — both are the non-ignorable kill, matching the Mac `-9`
  behavior (RESEARCHED).
- `MATCH_TOL` (240s) and the "process must not be born after the json" guard
  carry over unchanged.

---

## Open items for implementation (not blockers)

1. Confirm Linux `~/.config/Claude/claude-code-sessions/` layout on a real Linux box.
2. Confirm Windows native MSIX desktop session path (`Claude_pzs8sxrjxfjjc` package id may change by version).
3. Confirm Linux birthtime availability per filesystem; mtime fallback covers the gap.

None block the language/path/Windows decisions above — they are on-box
verifications for the slice that implements B-class on each OS.

---

## Sources

- Claude Code `.claude` directory layout: https://code.claude.com/docs/en/claude-directory
- Claude Code config file locations: https://inventivehq.com/knowledge-base/claude/where-configuration-files-are-stored
- Claude Code on Windows (native vs WSL): https://code.claude.com/docs/en/setup , https://claudelab.net/en/articles/claude-code/claude-code-windows-native-wsl2-complete-guide
- Claude Desktop data dir / Windows MSIX path: https://exchangepedia.com/2026/04/claudetools-claude-desktop-powershell-module.html
- gopsutil process API (Children/CreateTime/Kill): https://pkg.go.dev/github.com/shirou/gopsutil/v3/process
- goreleaser (Homebrew/Scoop/Winget, GitHub Actions): https://goreleaser.com/ , https://goreleaser.com/customization/ci/actions/
- systemd timers vs cron vs Windows Task Scheduler: https://xtom.com/blog/systemd-vs-cron-linux-task-scheduling/ , https://cronjobpro.com/blog/windows-task-scheduler
