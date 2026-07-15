#!/bin/sh
# claude-janitor one-command installer (macOS / Linux).
#
# From a source checkout:
#     ./install.sh                 # build with Go and install to a bin dir on PATH
#     ./install.sh --uninstall     # remove the installed binary
#
# Once binary releases exist (goreleaser + GitHub Actions, .goreleaser.yaml),
# the published one-liner becomes:
#     curl -fsSL https://raw.githubusercontent.com/maicuigua/claude-janitor/main/install.sh | sh
# and this script downloads the matching release archive instead of building.
# Set CLAUDE_JANITOR_RELEASE_URL to force the download path.
#
# Install location: $PREFIX/bin (default /usr/local/bin if writable, else
# ~/.local/bin). Override with PREFIX=/some/where ./install.sh.
set -eu

BIN=claude-janitor
REPO_RAW="https://raw.githubusercontent.com/maicuigua/claude-janitor/main"

log()  { printf '%s\n' "$*"; }
die()  { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

# --- resolve install dir -----------------------------------------------------
resolve_bindir() {
    if [ "${PREFIX:-}" ]; then
        printf '%s/bin' "$PREFIX"; return
    fi
    if [ -w /usr/local/bin ] 2>/dev/null; then
        printf '/usr/local/bin'; return
    fi
    printf '%s/.local/bin' "$HOME"
}

BINDIR="$(resolve_bindir)"
TARGET="$BINDIR/$BIN"

# --- uninstall ---------------------------------------------------------------
if [ "${1:-}" = "--uninstall" ]; then
    # Best-effort: also drop the scheduled job if the binary is still runnable.
    if command -v "$BIN" >/dev/null 2>&1; then
        "$BIN" uninstall >/dev/null 2>&1 || true
    fi
    if [ -e "$TARGET" ]; then
        rm -f "$TARGET" && log "removed $TARGET"
    else
        log "no binary at $TARGET (nothing to remove)"
    fi
    log "done. (scheduled job removed if it was registered)"
    exit 0
fi

mkdir -p "$BINDIR"

# --- build from source (in-repo) or download a release -----------------------
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ "${CLAUDE_JANITOR_RELEASE_URL:-}" ]; then
    log "downloading release: $CLAUDE_JANITOR_RELEASE_URL"
    tmp="$(mktemp -d)"
    curl -fsSL "$CLAUDE_JANITOR_RELEASE_URL" -o "$tmp/pkg.tar.gz" || die "download failed"
    tar -xzf "$tmp/pkg.tar.gz" -C "$tmp" || die "extract failed"
    found="$(find "$tmp" -type f -name "$BIN" | head -n1)"
    [ "$found" ] || die "no '$BIN' binary inside the archive"
    install -m 0755 "$found" "$TARGET"
    rm -rf "$tmp"
elif [ -f "$SCRIPT_DIR/go.mod" ]; then
    command -v go >/dev/null 2>&1 || die "Go is required to build from source (https://go.dev/dl). Or set CLAUDE_JANITOR_RELEASE_URL."
    log "building $BIN from source with $(go version | awk '{print $3}')..."
    ( cd "$SCRIPT_DIR" && go build -o "$TARGET" . ) || die "go build failed"
    chmod 0755 "$TARGET"
else
    die "run from a source checkout, or set CLAUDE_JANITOR_RELEASE_URL to a release archive"
fi

log "installed $BIN -> $TARGET"

# --- PATH hint ---------------------------------------------------------------
case ":$PATH:" in
    *":$BINDIR:"*) : ;;
    *) log ""; log "NOTE: $BINDIR is not on your PATH. Add it, e.g.:"
       log "  echo 'export PATH=\"$BINDIR:\$PATH\"' >> ~/.zshrc && source ~/.zshrc" ;;
esac

log ""
log "next steps:"
log "  $BIN run --dry-run   # see what it would kill, kill nothing"
log "  $BIN install         # register the periodic scan on your OS scheduler"
log "  $BIN uninstall       # remove the scheduled job"
