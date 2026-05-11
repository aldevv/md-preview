#!/usr/bin/env sh
# mdp standalone installer (Go binary).
#
# Strategy:
#   1. If `go` is on PATH, build from source via go install — fastest, no
#      network round-trip past the module proxy.
#   2. Else, download a prebuilt release binary from GitHub for the
#      detected OS/arch.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/aldevv/md-preview/main/install.sh | sh
#
# Override target prefix:
#   PREFIX=$HOME/.local sh install.sh   # default
#   PREFIX=/usr/local   sh install.sh   # system-wide (needs sudo)
#
# Override release version (when downloading):
#   MDP_VERSION=v0.2.0 sh install.sh    # default: latest

set -eu

PREFIX="${PREFIX:-$HOME/.local}"
BIN="$PREFIX/bin"
TARGET="$BIN/mdp"
MODULE="github.com/aldevv/md-preview"
REPO="aldevv/md-preview"

mkdir -p "$BIN"

installed_version() {
    if [ -f "$TARGET" ] && [ ! -L "$TARGET" ] && [ -x "$TARGET" ]; then
        "$TARGET" version 2>/dev/null || true
    fi
}

INSTALLED="$(installed_version)"
if [ -n "${MDP_VERSION:-}" ] && [ "$INSTALLED" = "$MDP_VERSION" ]; then
    echo "[mdp] already at $INSTALLED — nothing to do"
    exit 0
fi

# macOS install(1) follows symlinks and would overwrite md-preview.nvim's
# Python script at the symlink's target; rm first to be safe.
rm -f "$TARGET"

# ── Path 1: build from source via Go ────────────────────────────────────
if command -v go >/dev/null 2>&1; then
    echo "[mdp] building from source via go install (${MDP_VERSION:-latest})"
    GOBIN="$BIN" go install "$MODULE/cmd/mdp@${MDP_VERSION:-latest}"
else
    # ── Path 2: download prebuilt release ───────────────────────────────
    command -v curl >/dev/null 2>&1 || { echo "mdp install: curl or go is required" >&2; exit 1; }
    command -v tar  >/dev/null 2>&1 || { echo "mdp install: tar is required" >&2; exit 1; }

    OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
    ARCH="$(uname -m)"
    case "$ARCH" in
        x86_64|amd64)  ARCH=amd64 ;;
        aarch64|arm64) ARCH=arm64 ;;
        *) echo "mdp install: unsupported arch $ARCH" >&2; exit 1 ;;
    esac
    case "$OS" in
        linux|darwin) ;;
        *) echo "mdp install: unsupported OS $OS" >&2; exit 1 ;;
    esac

    VERSION="${MDP_VERSION:-}"
    if [ -z "$VERSION" ]; then
        VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
            | sed -n 's/.*"tag_name": "\([^"]*\)".*/\1/p' | head -n1)"
        if [ -z "$VERSION" ]; then
            echo "mdp install: could not resolve latest release. Set MDP_VERSION=vX.Y.Z." >&2
            exit 1
        fi
    fi

    URL="https://github.com/$REPO/releases/download/$VERSION/mdp_${OS}_${ARCH}.tar.gz"
    echo "[mdp] downloading $URL"
    TMP="$(mktemp -d)"
    trap 'rm -rf "$TMP"' EXIT
    curl -fsSL "$URL" | tar -xzf - -C "$TMP"
    install -m 0755 "$TMP/mdp" "$TARGET"
fi

NEW="$("$TARGET" version 2>/dev/null || echo "(unknown)")"
if [ -n "$INSTALLED" ] && [ "$INSTALLED" != "$NEW" ]; then
    echo "[mdp] upgraded $INSTALLED -> $NEW ($TARGET)"
else
    echo "[mdp] installed $NEW ($TARGET)"
fi

case ":${PATH-}:" in
    *":$BIN:"*) ;;
    *) echo "[mdp] note: $BIN is not in PATH — add it to your shell rc:"
       echo "       export PATH=\"$BIN:\$PATH\"" ;;
esac

echo "[mdp] done. Try: mdp help"
