# claude-janitor

Cross-platform CLI (Go) that kills idle Claude Code session processes (idle > 2h by default) to reclaim memory. It never deletes session files, so conversations stay resumable.

**This repo is the single source of truth for janitor logic.** The launchd job on this Mac runs the binary built and installed from here (`~/.local/bin/claude-janitor`, via `claude-janitor install`). Do not fork the logic into standalone scripts; the old `~/.claude/scripts/claude-session-janitor.*` reference scripts are retired (kept only as `.bak` for rollback).

## Commands

- Build: `go build ./...` — Test: `go test ./...`
- `claude-janitor run [--dry-run]` — one scan-and-kill pass
- `claude-janitor install / uninstall` — register/remove the OS scheduler job
- Deploy on this Mac: `go build -o ~/.local/bin/claude-janitor . && ~/.local/bin/claude-janitor install`

## Idle detection — do not regress this

Idleness is judged ONLY by the session transcript's mtime (`~/.claude/projects/*/<id>.jsonl`, updated on every message). Desktop sessions with no id in argv are paired to transcripts by birth time (see janitor.go header and docs/design.md §2.B). Never judge desktop sessions by the app's `local_*.json` mtime — that file is rewritten only on UI events, never during task execution; using it killed active sessions mid-task (2026-07-30 incident). `--resume` appears in both space and `=` forms; handle both.

## Workflow

Linear project `claude-janitor` (team MEL) is the source of truth for current work. Agents have Linear MCP access.

- Pick up tickets from Linear. Move to `Running` on start; `In Review` + assign Melody when done and verified (never straight to `Done`).
- Keep work scoped to the active ticket unless asked otherwise.
- Commit messages include the Linear ID when the work maps to a ticket (e.g. `MEL-141 Add ...`).
