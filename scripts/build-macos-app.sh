#!/usr/bin/env bash
# Builds dist/macos/Portside.app: the Swift menu bar app with the universal
# portside binary (daemon + CLI + embedded dashboard) inside Contents/Helpers.
#
#   scripts/build-macos-app.sh            # build the app
#   scripts/build-macos-app.sh --zip      # also write dist/macos/Portside-<version>.zip
#   SIGN_IDENTITY="Developer ID Application: ..." scripts/build-macos-app.sh
#
# Without SIGN_IDENTITY the app is signed ad hoc, which is enough to run it on
# this Mac but not to distribute it without Gatekeeper warnings.
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(git describe --tags --dirty 2>/dev/null | sed 's/^v//' || true)}"
VERSION="${VERSION:-0.1.0-dev}"
BUILD="$(date +%Y%m%d%H%M)"
OUT=dist/macos
APP="$OUT/Portside.app"
LDFLAGS="-s -w -X github.com/padmk/portside/internal/app.Version=$VERSION"

echo "==> dashboard"
if [ "${SKIP_UI:-0}" != 1 ]; then
  pnpm --dir web install --frozen-lockfile >/dev/null
  pnpm --dir web build >/dev/null
fi

echo "==> portside $VERSION (universal)"
mkdir -p "$OUT/tmp"
for arch in arm64 amd64; do
  CGO_ENABLED=0 GOOS=darwin GOARCH=$arch go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/tmp/portside-$arch" ./cmd/portside
done
lipo -create -output "$OUT/tmp/portside" "$OUT/tmp/portside-arm64" "$OUT/tmp/portside-amd64"

echo "==> app (universal)"
swift build --package-path macos -c release --arch arm64 --arch x86_64 >/dev/null
BIN="$(swift build --package-path macos -c release --arch arm64 --arch x86_64 --show-bin-path)/Portside"

echo "==> bundle"
if [ -e "$APP" ]; then
  # Keep the previous build recoverable.
  mv "$APP" "$OUT/tmp/Portside.app.previous-$BUILD"
fi
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Helpers" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/Portside"
cp "$OUT/tmp/portside" "$APP/Contents/Helpers/portside"
cp macos/Resources/AppIcon.icns macos/Resources/AppIconDark.png "$APP/Contents/Resources/"
sed -e "s/__VERSION__/${VERSION%%-*}/" -e "s/__BUILD__/$BUILD/" macos/Resources/Info.plist > "$APP/Contents/Info.plist"

echo "==> sign"
IDENTITY="${SIGN_IDENTITY:--}"
OPTS=()
[ "$IDENTITY" != "-" ] && OPTS=(--options runtime --timestamp)
codesign --force ${OPTS[@]+"${OPTS[@]}"} --sign "$IDENTITY" "$APP/Contents/Helpers/portside"
codesign --force ${OPTS[@]+"${OPTS[@]}"} --sign "$IDENTITY" "$APP"
codesign --verify --strict "$APP"

if [ "${1:-}" = "--zip" ]; then
  ditto -c -k --keepParent "$APP" "$OUT/Portside-$VERSION.zip"
  echo "==> $OUT/Portside-$VERSION.zip"
fi
echo "==> $APP"
