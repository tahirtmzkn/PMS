#!/usr/bin/env bash
# Builds dist/pms_<version>_<arch>.deb from the current source tree.
# Usage: packaging/build-deb.sh [version]
set -euo pipefail

VERSION="${1:-0.1.0}"
ARCH="$(dpkg --print-architecture)"
APP=pms

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PKG_ROOT="build/deb"
rm -rf "$PKG_ROOT" dist
mkdir -p "$PKG_ROOT/DEBIAN" \
         "$PKG_ROOT/usr/bin" \
         "$PKG_ROOT/usr/share/applications" \
         "$PKG_ROOT/usr/share/icons/hicolor/512x512/apps"

echo "Building $APP binary..."
go build -o "$PKG_ROOT/usr/bin/$APP" .

cp packaging/pms.desktop "$PKG_ROOT/usr/share/applications/pms.desktop"
cp assets/ping-pong.png "$PKG_ROOT/usr/share/icons/hicolor/512x512/apps/pms.png"

cat > "$PKG_ROOT/DEBIAN/control" <<EOF
Package: $APP
Version: $VERSION
Section: net
Priority: optional
Architecture: $ARCH
Depends: libgl1, libx11-6, libxrandr2, libxcursor1, libxinerama1, libxi6, libxxf86vm1, iputils-ping
Maintainer: $(whoami) <$(whoami)@localhost>
Description: Ping Monitoring System
 GUI to monitor network device liveness via ICMP ping, with live
 success/fail counters and a color-coded status table.
EOF

mkdir -p dist
DEB_PATH="dist/${APP}_${VERSION}_${ARCH}.deb"
dpkg-deb --build --root-owner-group "$PKG_ROOT" "$DEB_PATH"

echo "Built $DEB_PATH"
