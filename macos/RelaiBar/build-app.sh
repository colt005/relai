#!/usr/bin/env bash
# Builds RelaiBar.app from the Swift package. Needs only the Xcode Command Line
# Tools (xcode-select --install), not full Xcode.
set -euo pipefail
cd "$(dirname "$0")"

echo "building…"
swift build -c release
BIN="$(swift build -c release --show-bin-path)/RelaiBar"

APP="build/RelaiBar.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/RelaiBar"
cp Support/Info.plist "$APP/Contents/Info.plist"

# Ad-hoc signature: enough to run on this Mac. Distribution needs a Developer ID.
codesign --force --sign - "$APP" >/dev/null

echo "built $APP"
if [ "${1:-}" = "--install" ]; then
  if pkill -x RelaiBar 2>/dev/null; then
    # Wait for it to exit, or `open` can fail with error -600 (process not found).
    for _ in $(seq 1 25); do pgrep -x RelaiBar >/dev/null || break; sleep 0.2; done
  fi
  rm -rf /Applications/RelaiBar.app
  cp -R "$APP" /Applications/
  open /Applications/RelaiBar.app
  echo "installed to /Applications and launched"
else
  echo "run it:      open $APP"
  echo "or install:  ./build-app.sh --install"
fi
