#!/usr/bin/env bash
# Builds the Go collector and the SwiftUI menubar app, then assembles
# dist/AgentBar.app with the collector bundled under Contents/Helpers.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP="$ROOT/dist/AgentBar.app"
CONTENTS="$APP/Contents"

echo "==> Building collector"
rm -rf "$APP"
mkdir -p "$CONTENTS/MacOS" "$CONTENTS/Helpers"
(cd "$ROOT/collector" && go build -trimpath -ldflags "-s -w" -o "$CONTENTS/Helpers/agentbar-collector" .)

echo "==> Building AgentBar (release)"
swift build -c release --package-path "$ROOT/macos"
BIN="$(swift build -c release --package-path "$ROOT/macos" --show-bin-path)/AgentBar"
cp "$BIN" "$CONTENTS/MacOS/AgentBar"
cp "$ROOT/macos/Info.plist" "$CONTENTS/Info.plist"
chmod +x "$CONTENTS/MacOS/AgentBar" "$CONTENTS/Helpers/agentbar-collector"

if [ -d "$ROOT/macos/Resources" ]; then
  echo "==> Copying resources"
  mkdir -p "$CONTENTS/Resources"
  cp -R "$ROOT/macos/Resources/." "$CONTENTS/Resources/"
fi

echo "==> Signing (ad-hoc)"
codesign --force --deep --sign - "$APP" >/dev/null 2>&1 || echo "   ad-hoc signing skipped"

echo "==> Done"
echo "   app:       $APP"
echo "   binary:    $CONTENTS/MacOS/AgentBar"
echo "   collector: $CONTENTS/Helpers/agentbar-collector"
