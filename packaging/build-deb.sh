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
# -s -w drops the symbol table and DWARF debug info: measured at 1.0.1, the
# binary goes 31.4MB -> 23.2MB and the .deb 15.7MB -> 9.6MB. Only the packaged
# build does this; a plain `go build` for development keeps both. Panic stack
# traces still name functions and line numbers (Go reads those from the pclntab,
# which -s -w leaves alone, verified); what is lost is attaching a debugger
# (gdb/delve) to the installed binary.
go build -ldflags="-s -w" -o "$PKG_ROOT/usr/bin/$APP" .

cp packaging/pms.desktop "$PKG_ROOT/usr/share/applications/pms.desktop"
cp assets/ping-pong.png "$PKG_ROOT/usr/share/icons/hicolor/512x512/apps/pms.png"

# Taken from the git identity, so a published package carries a real contact
# instead of "user@localhost"; falls back to the local user outside a checkout.
MAINTAINER_NAME="$(git config user.name 2>/dev/null || true)"
MAINTAINER_EMAIL="$(git config user.email 2>/dev/null || true)"
: "${MAINTAINER_NAME:=$(whoami)}"
: "${MAINTAINER_EMAIL:=$(whoami)@localhost}"

cat > "$PKG_ROOT/DEBIAN/control" <<EOF
Package: $APP
Version: $VERSION
Section: net
Priority: optional
Architecture: $ARCH
Depends: libgl1, libx11-6, libxrandr2, libxcursor1, libxinerama1, libxi6, libxxf86vm1, iputils-ping, snmp
Maintainer: $MAINTAINER_NAME <$MAINTAINER_EMAIL>
Description: Ping Monitoring System
 GUI to monitor network device liveness via ICMP ping, with live
 success/fail/loss counters and a color-coded status table, green while a
 device's last ping answered and red while it didn't. Every device is also
 asked for its own SNMP hostname (snmpget), which is why the snmp package is
 a dependency; only the numeric OID is queried, so the MIB files
 (snmp-mibs-downloader) are not needed. The device list and the light/dark
 theme choice are kept in ~/.config/pms/config.json.
EOF

mkdir -p dist
DEB_PATH="dist/${APP}_${VERSION}_${ARCH}.deb"
dpkg-deb --build --root-owner-group "$PKG_ROOT" "$DEB_PATH"

echo "Built $DEB_PATH"
