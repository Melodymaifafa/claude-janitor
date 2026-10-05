#!/bin/sh
# claude-janitor one-command installer (macOS / Linux).
#
# One-liner (downloads the latest release archive for your platform):
#     curl -fsSL https://raw.githubusercontent.com/Melodymaifafa/claude-janitor/main/install.sh | sh
#
# From a source checkout (builds with Go):
#     ./install.sh
#     ./install.sh --uninstall     # remove the installed binary
#
# Install location: $PREFIX/bin (default /usr/local/bin if writable, else
# ~/.local/bin). Override with PREFIX=/some/where ./install.sh.
#
# Other overrides:
#     CLAUDE_JANITOR_RELEASE_URL   install this exact archive, skip the lookup
#     CLAUDE_JANITOR_BASE_URL      repo web base used for the release lookup
set -eu

BIN=claude-janitor
BASE_URL="${CLAUDE_JANITOR_BASE_URL:-https://github.com/Melodymaifafa/claude-janitor}"

log()  { printf '%s\n' "$*"; }
die()  { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

# --- scratch dir -------------------------------------------------------------
# The download path unpacks ~9 MB into a mktemp dir. Drop it on every exit path,
# so a failed download leaves nothing behind either. The trailing assignment
# keeps the handler's own status at 0, preserving the real exit code.
DL_TMP=""
cleanup() {
    if [ "${DL_TMP:-}" ] && [ -d "$DL_TMP" ]; then
        rm -rf "$DL_TMP"
    fi
    DL_TMP=""
}
trap cleanup EXIT HUP INT TERM

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

# --- release download helpers ------------------------------------------------
# goreleaser (.goreleaser.yaml) names archives
# claude-janitor_<version>_<os>_<arch>.tar.gz, with <version> the tag minus "v".
platform_suffix() {
    ps_os="$(uname -s)"
    case "$ps_os" in
        Darwin) ps_os=darwin ;;
        Linux)  ps_os=linux ;;
        *) die "no prebuilt binary for OS '$ps_os'. Build from source instead: git clone $BASE_URL && cd $BIN && ./install.sh" ;;
    esac
    ps_arch="$(uname -m)"
    case "$ps_arch" in
        x86_64|amd64)  ps_arch=amd64 ;;
        arm64|aarch64) ps_arch=arm64 ;;
        *) die "no prebuilt binary for CPU '$ps_arch'. Build from source instead: git clone $BASE_URL && cd $BIN && ./install.sh" ;;
    esac
    printf '%s_%s' "$ps_os" "$ps_arch"
}

# Latest tag, read off the redirect GitHub serves for /releases/latest.
# Fails (non-zero, no output) when the repo has no published release yet.
latest_tag() {
    lt_url="$(curl -fsSL -o /dev/null -w '%{url_effective}' "$BASE_URL/releases/latest")" || return 1
    case "$lt_url" in
        */releases/tag/?*) printf '%s' "${lt_url##*/releases/tag/}" ;;
        *) return 1 ;;
    esac
}

install_release() {
    ir_url="$1"
    log "downloading $ir_url"
    mkdir -p "$BINDIR"
    DL_TMP="$(mktemp -d)"
    curl -fsSL "$ir_url" -o "$DL_TMP/pkg.tar.gz" || die "download failed: $ir_url
  Check your network, or grab the archive by hand from $BASE_URL/releases
  and copy '$BIN' onto your PATH."
    tar -xzf "$DL_TMP/pkg.tar.gz" -C "$DL_TMP" || die "could not unpack the archive from $ir_url"
    ir_found="$(find "$DL_TMP" -type f -name "$BIN" | head -n1)"
    [ "$ir_found" ] || die "no '$BIN' binary inside the archive from $ir_url"
    install -m 0755 "$ir_found" "$TARGET"
    cleanup
}

# A source checkout has to be *this* project, not whatever directory the user
# happens to be standing in.
in_source_tree() {
    [ "${1:-}" ] || return 1
    [ -f "$1/go.mod" ] || return 1
    [ -f "$1/install.sh" ] || return 1
    grep -q "^module .*/$BIN\$" "$1/go.mod" 2>/dev/null
}

# --- pick the install path ---------------------------------------------------
# Piped into a shell (`curl ... | sh`) there is no script file: $0 is the shell
# itself, so dirname "$0" would point at the user's current directory, which has
# nothing to do with this project. Leave SCRIPT_DIR empty in that case and go
# down the download path -- never build whatever happens to sit in $PWD.
SCRIPT_DIR=""
case "$(basename -- "$0")" in
    sh|bash|dash|ksh|zsh|ash|busybox) ;;
    *)
        if [ -f "$0" ]; then
            SCRIPT_DIR="$(cd "$(dirname -- "$0")" && pwd)"
        fi
        ;;
esac

if [ "${CLAUDE_JANITOR_RELEASE_URL:-}" ]; then
    install_release "$CLAUDE_JANITOR_RELEASE_URL"
elif in_source_tree "$SCRIPT_DIR"; then
    command -v go >/dev/null 2>&1 || die "Go is required to build from source (https://go.dev/dl). Or set CLAUDE_JANITOR_RELEASE_URL to a release archive."
    log "building $BIN from source with $(go version | awk '{print $3}')..."
    mkdir -p "$BINDIR"
    ( cd "$SCRIPT_DIR" && go build -o "$TARGET" . ) || die "go build failed"
    chmod 0755 "$TARGET"
else
    suffix="$(platform_suffix)" || exit 1
    tag="$(latest_tag)" || die "could not find a published release at $BASE_URL/releases/latest (none published yet, or no network).
  Build from source instead:
    git clone $BASE_URL && cd $BIN && ./install.sh
  Or point CLAUDE_JANITOR_RELEASE_URL at a ${BIN}_<version>_$suffix.tar.gz archive."
    install_release "$BASE_URL/releases/download/$tag/${BIN}_${tag#v}_$suffix.tar.gz"
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
