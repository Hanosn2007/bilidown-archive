#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLIENT_DIR="$ROOT/client"
SERVER_DIR="$ROOT/server"
APP_DIR="$SERVER_DIR/Bilidown.app"
CONTENTS_DIR="$APP_DIR/Contents"
MACOS_DIR="$CONTENTS_DIR/MacOS"

cd "$CLIENT_DIR"
pnpm install --frozen-lockfile
pnpm build

rm -rf "$APP_DIR"
mkdir -p "$MACOS_DIR"

cd "$SERVER_DIR"
go build -o "$MACOS_DIR/bilidown-macos" .

cat > "$CONTENTS_DIR/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>bilidown-macos</string>
	<key>CFBundleIdentifier</key>
	<string>local.bilidown.archive</string>
	<key>CFBundleName</key>
	<string>Bilidown</string>
	<key>CFBundleDisplayName</key>
	<string>Bilidown</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>2.1.1-archive</string>
	<key>CFBundleVersion</key>
	<string>2.1.1</string>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<key>LSUIElement</key>
	<true/>
</dict>
</plist>
PLIST

chmod +x "$MACOS_DIR/bilidown-macos"

echo "Created $APP_DIR"
