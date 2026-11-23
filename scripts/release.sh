#!/usr/bin/env bash
# Build AgentBar, sign it with a Developer ID, and optionally notarize + publish.
#
#   ./scripts/release.sh                                   build + sign + verify
#   ./scripts/release.sh --notarize --profile agentbar-notary
#   ./scripts/release.sh --notarize --profile agentbar-notary --publish
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLIST="$ROOT/macos/Info.plist"
APP="$ROOT/dist/AgentBar.app"

VERSION=""
NOTARIZE=0
PUBLISH=0
PROFILE="${NOTARY_PROFILE:-slirc-notary}"
IDENTITY="${CODESIGN_IDENTITY:-Developer ID Application: Mauricio Da Cunha Wolff (HX4V85BM62)}"

usage() {
  cat <<'EOF'
Usage: ./scripts/release.sh [options]

  (no options)        build, Developer ID sign, verify, zip
  --notarize          submit to Apple, wait, staple, re-zip, verify Gatekeeper
  --publish           create the GitHub release v<version> with the zip attached
EOF
  cat <<'EOF'

Options:
  --version <x.y.z>   override CFBundleShortVersionString from macos/Info.plist
  --profile <name>    notarytool keychain profile (or set NOTARY_PROFILE)
  --identity <name>   codesign identity (or set CODESIGN_IDENTITY)
  -h, --help          this text
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2:?--version needs a value}"; shift 2 ;;
    --notarize) NOTARIZE=1; shift ;;
    --profile) PROFILE="${2:?--profile needs a value}"; shift 2 ;;
    --publish) PUBLISH=1; shift ;;
    --identity) IDENTITY="${2:?--identity needs a value}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ -z "$VERSION" ]; then
  VERSION="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$PLIST")"
fi
ZIP="$ROOT/dist/AgentBar-$VERSION-macOS.zip"

echo "==> AgentBar $VERSION"
echo "    identity: $IDENTITY"

echo "==> go test ./..."
(cd "$ROOT/collector" && go test ./...)

echo "==> build-app.sh"
"$ROOT/macos/build-app.sh"

HELPER="$APP/Contents/Helpers/agentbar-collector"
if [ ! -f "$HELPER" ]; then
  echo "collector missing at $HELPER" >&2
  exit 1
fi

# Inside out: nested helper and Sparkle.framework first, then the bundle.
echo "==> codesign collector"
codesign --force --options runtime --timestamp --sign "$IDENTITY" "$HELPER"

SPARKLE="$APP/Contents/Frameworks/Sparkle.framework"
if [ -d "$SPARKLE" ]; then
  echo "==> codesign Sparkle.framework"
  for item in \
    "$SPARKLE/Versions/B/XPCServices/Installer.xpc" \
    "$SPARKLE/Versions/B/XPCServices/Downloader.xpc" \
    "$SPARKLE/Versions/B/Updater.app" \
    "$SPARKLE/Versions/B/Autoupdate" \
    "$SPARKLE"
  do
    if [ -e "$item" ]; then
      codesign --force --options runtime --timestamp --sign "$IDENTITY" "$item"
    fi
  done
fi

echo "==> codesign app"
codesign --force --options runtime --timestamp --sign "$IDENTITY" "$APP"

echo "==> codesign --verify --deep --strict"
codesign --verify --deep --strict --verbose=2 "$APP"

echo "==> zip"
rm -f "$ZIP"
ditto -c -k --keepParent "$APP" "$ZIP"
echo "    $ZIP"

if [ "$NOTARIZE" -eq 1 ]; then
  if [ -z "$PROFILE" ]; then
    echo "notarization needs NOTARY_PROFILE or --profile <name>" >&2
    echo "create one with: xcrun notarytool store-credentials <name> \\" >&2
    echo "  --apple-id <you@example.com> --team-id HX4V85BM62 --password <app-specific-password>" >&2
    exit 1
  fi

  echo "==> notarytool submit ($PROFILE)"
  xcrun notarytool submit "$ZIP" --keychain-profile "$PROFILE" --wait

  echo "==> stapler staple"
  xcrun stapler staple "$APP"
  xcrun stapler validate "$APP"

  echo "==> re-zip stapled app"
  rm -f "$ZIP"
  ditto -c -k --keepParent "$APP" "$ZIP"

  echo "==> spctl"
  spctl -a -vvv -t exec "$APP"
fi

SIGN_UPDATE="$ROOT/macos/.build/artifacts/sparkle/Sparkle/bin/sign_update"
if [ -x "$SIGN_UPDATE" ]; then
  echo "==> sparkle sign_update + docs/appcast.xml"
  BUILD_NUM="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$PLIST")"
  SIG_OUT="$("$SIGN_UPDATE" --account agentbar "$ZIP")"
  ED_SIG="$(printf '%s\n' "$SIG_OUT" | sed -n 's/.*sparkle:edSignature="\([^"]*\)".*/\1/p')"
  ZIP_LEN="$(printf '%s\n' "$SIG_OUT" | sed -n 's/.*length="\([^"]*\)".*/\1/p')"
  PUB_DATE="$(LC_ALL=C date -u '+%a, %d %b %Y %H:%M:%S +0000')"
  if [ -n "$ED_SIG" ] && [ -n "$ZIP_LEN" ]; then
    cat > "$ROOT/docs/appcast.xml" <<EOF
<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle" xmlns:dc="http://purl.org/dc/elements/1.1/">
  <channel>
    <title>AgentBar</title>
    <link>https://agentbar.fyi/</link>
    <description>AgentBar updates</description>
    <language>en</language>
    <item>
      <title>AgentBar $VERSION</title>
      <pubDate>$PUB_DATE</pubDate>
      <sparkle:version>$BUILD_NUM</sparkle:version>
      <sparkle:shortVersionString>$VERSION</sparkle:shortVersionString>
      <sparkle:minimumSystemVersion>13.0</sparkle:minimumSystemVersion>
      <enclosure
        url="https://github.com/bitbonsai/agentbar/releases/download/v$VERSION/AgentBar-$VERSION-macOS.zip"
        sparkle:edSignature="$ED_SIG"
        length="$ZIP_LEN"
        type="application/octet-stream" />
    </item>
  </channel>
</rss>
EOF
    echo "    updated $ROOT/docs/appcast.xml"
  fi
fi

if [ "$PUBLISH" -eq 1 ]; then
  echo "==> gh release create v$VERSION"
  gh release create "v$VERSION" "$ZIP" \
    --repo bitbonsai/agentbar \
    --title "AgentBar $VERSION" \
    --notes "AgentBar $VERSION. Notarized macOS build attached."
fi

echo "==> Done"
echo "    app: $APP"
echo "    zip: $ZIP"
