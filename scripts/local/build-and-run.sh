#!/usr/bin/env bash
# Builds the relai CLI and the RelaiBar menu bar app from this checkout, installs
# both on this Mac, and launches RelaiBar.
#
#   scripts/local/build-and-run.sh          # CLI + menu bar app
#   scripts/local/build-and-run.sh --cli    # CLI only
#   scripts/local/build-and-run.sh --app    # menu bar app only
#
# The CLI goes to $RELAI_BIN_DIR (default ~/.local/bin).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN_DIR="${RELAI_BIN_DIR:-$HOME/.local/bin}"

want_cli=1
want_app=1
case "${1:-}" in
  --cli) want_app=0 ;;
  --app) want_cli=0 ;;
  "") ;;
  -h|--help) sed -n '2,10p' "${BASH_SOURCE[0]}"; exit 0 ;;
  *) echo "unknown option: $1 (try --help)" >&2; exit 2 ;;
esac

if [ "$want_cli" = 1 ]; then
  command -v go >/dev/null || { echo "go is not installed" >&2; exit 1; }
  version="dev"
  if git -C "$ROOT" rev-parse --git-dir >/dev/null 2>&1; then
    version="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)"
    version="${version#v}" # release builds are stamped without the "v"
  fi
  echo "==> building relai CLI ($version)"
  mkdir -p "$BIN_DIR"
  (cd "$ROOT" && go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$BIN_DIR/relai" ./cmd/relai)
  echo "    installed $BIN_DIR/relai"
  case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) echo "    note: $BIN_DIR is not on your PATH" ;;
  esac
  "$BIN_DIR/relai" version | sed 's/^/    /'
  # Another relai earlier on PATH (for example the Homebrew one) would hide this build.
  first="$(command -v relai || true)"
  if [ -n "$first" ] && [ "$first" != "$BIN_DIR/relai" ]; then
    echo "    warning: 'relai' on your PATH is $first, not this build."
    echo "             Run \"$BIN_DIR/relai\" directly, or remove the other one"
    echo "             (Homebrew: brew uninstall --cask relai)."
  fi
fi

if [ "$want_app" = 1 ]; then
  command -v swift >/dev/null || { echo "swift is not installed (xcode-select --install)" >&2; exit 1; }
  echo "==> building RelaiBar"
  # Launch it without agent-profile variables from this shell (for example
  # CLAUDE_CONFIG_DIR from a claude-p session), or RelaiBar would read the wrong folders.
  env -u CLAUDE_CONFIG_DIR -u CODEX_HOME "$ROOT/macos/RelaiBar/build-app.sh" --install
fi

echo "==> done"
