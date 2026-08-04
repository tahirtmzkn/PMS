#!/usr/bin/env bash
# Builds dist/pinginfomanager_<version>_<arch>.deb from the current source tree.
#
# Usage: packaging/build-deb.sh [--local] [version]
#
#   --local   compile with the host Go toolchain instead of in the container.
#
# By default the binary is compiled inside an Ubuntu 18.04 container (see
# Dockerfile.build), which is what makes one .deb work on 18.04 and every later
# release. --local is for checking the packaging itself: it needs no Docker, but
# the binary then requires the glibc of whatever machine built it, so on a 24.04
# desktop it will not run on 18.04. The Depends line reports the floor either
# way (see GLIBC_MIN below), so a --local package refuses to install where it
# could not run rather than installing and failing to start.
set -euo pipefail

APP=pinginfomanager
IMAGE=pinginfomanager-deb-builder:18.04
BUILD_MODE=docker

args=()
for arg in "$@"; do
	case "$arg" in
	--local) BUILD_MODE=local ;;
	--docker) BUILD_MODE=docker ;;
	-h | --help)
		# The header comment, up to the first non-comment line.
		sed -n '2,${/^[^#]/q;p;}' "$0"
		exit 0
		;;
	-*)
		echo "unknown option: $arg" >&2
		exit 2
		;;
	*) args+=("$arg") ;;
	esac
done

VERSION="${args[0]:-0.1.0}"
ARCH="$(dpkg --print-architecture)"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PKG_ROOT="build/deb"
BIN="$PKG_ROOT/usr/bin/$APP"
rm -rf "$PKG_ROOT" dist
mkdir -p "$PKG_ROOT/DEBIAN" \
	"$PKG_ROOT/usr/bin" \
	"$PKG_ROOT/usr/share/applications" \
	"$PKG_ROOT/usr/share/icons/hicolor/512x512/apps"

# -s -w drops the symbol table and DWARF debug info: measured at 1.0.1, the
# binary goes 31.4MB -> 23.2MB and the .deb 15.7MB -> 9.6MB. Only the packaged
# build does this; a plain `go build` for development keeps both. Panic stack
# traces still name functions and line numbers (Go reads those from the pclntab,
# which -s -w leaves alone, verified); what is lost is attaching a debugger
# (gdb/delve) to the installed binary.
LDFLAGS="-s -w"

# -trimpath rewrites the 600-odd absolute source paths the compiler otherwise
# embeds into module-relative ones, so a stack trace reads
# "fyne.io/fyne/v2@v2.8.0/app/cache.go" rather than naming a directory on
# whichever machine did the build. Three reasons it is on for the packaged build
# and not for development:
#   - the paths are meaningless to whoever runs the package: before this they
#     were the maintainer's home directory, and once the build moved into the
#     container they became the container's /src and /gomodcache;
#   - it removes the build *location* from the output, which is what makes the
#     container build byte-for-byte reproducible: two runs of it on the same
#     source produce the same binary (verified), so a release can be checked by
#     rebuilding it. Note this holds within one builder image only — a container
#     build and a --local build still differ, because cgo compiles against
#     whichever gcc and glibc the environment has;
#   - a public binary stops carrying the build machine's directory layout.
# It costs nothing here: source-level debugging of the packaged binary is
# already gone with -s -w, and a development `go build` keeps both.
#
# -tags x11 builds glfw with only its X11 backend, and it is what makes an 18.04
# package possible. glfw's Wayland backend needs WL_MARSHAL_FLAG_DESTROY, which
# arrived in libwayland 1.20 (2021); 18.04 has 1.16, so the default build — which
# compiles X11 *and* Wayland and picks between them at runtime — does not
# compile there at all, and could not be linked into a package that installs
# there even if it did, since wayland-client is a hard NEEDED entry and the
# symbol is missing from 18.04's copy. The X11-only binary drops the Wayland
# libraries entirely (verified with objdump/strings) and, on a Wayland session,
# runs through XWayland instead, which every mainstream Wayland desktop ships.
# The visible cost is confined to that case: no native Wayland surface, so
# fractional scaling goes through XWayland's scaling, and FYNE_PLATFORM=wayland
# has nothing to select. A development `go build` is left alone, so the default
# dual-backend build is still what gets exercised day to day on a modern desktop.
BUILDFLAGS=(-trimpath -tags x11)

if [[ "$BUILD_MODE" == local ]]; then
	echo "Building $APP binary with the host toolchain..."
	go build "${BUILDFLAGS[@]}" -ldflags="$LDFLAGS" -o "$BIN" .
else
	command -v docker >/dev/null || {
		echo "docker is required for the default build; pass --local to use the host toolchain" >&2
		exit 1
	}

	# The image gets the host's Go version, so a container build and a --local
	# build are never on different toolchains.
	GO_VERSION="$(go env GOVERSION)"
	GO_VERSION="${GO_VERSION#go}"

	echo "Building the Ubuntu 18.04 builder image (Go $GO_VERSION)..."
	docker build -t "$IMAGE" -f packaging/Dockerfile.build \
		--build-arg "GO_VERSION=$GO_VERSION" \
		--build-arg "GO_ARCH=$ARCH" \
		packaging

	# Modules are fetched here, where the cache is writable, so the container can
	# have it read-only and GOPROXY=off — it must not need the network, and it
	# must not leave root-owned files in the user's module cache.
	go mod download

	echo "Building $APP binary in $IMAGE..."
	# --user keeps the emitted binary owned by the invoking user rather than root.
	docker run --rm \
		--user "$(id -u):$(id -g)" \
		-v "$ROOT_DIR:/src" \
		-v "$(go env GOMODCACHE):/gomodcache:ro" \
		-e GOMODCACHE=/gomodcache \
		-e GOCACHE=/tmp/gocache \
		-e GOPROXY=off \
		-e GOFLAGS=-mod=mod \
		-e CGO_ENABLED=1 \
		-e HOME=/tmp \
		-w /src \
		"$IMAGE" \
		go build "${BUILDFLAGS[@]}" -ldflags="$LDFLAGS" -o "$BIN" .
fi

cp "packaging/$APP.desktop" "$PKG_ROOT/usr/share/applications/$APP.desktop"
cp assets/ping-pong.png "$PKG_ROOT/usr/share/icons/hicolor/512x512/apps/$APP.png"

# The oldest glibc this binary can run against, read back out of the binary
# instead of assumed. cgo links against whatever glibc compiled it, and a
# package that does not declare that floor installs cleanly and then dies at
# exec with "libc.so.6: version `GLIBC_2.38' not found" — which is exactly how
# the 1.0.1 package, built on 24.04, behaved on 22.04 (and would behave on any
# older release still). Stating it turns a
# confusing runtime failure into an apt error that names the real problem.
GLIBC_MIN=""
if command -v objdump >/dev/null; then
	GLIBC_MIN="$(objdump -T "$BIN" |
		grep -oE 'GLIBC_[0-9]+\.[0-9]+' |
		sort -uV | tail -1 | cut -d_ -f2)"
fi
if [[ -n "$GLIBC_MIN" ]]; then
	LIBC_DEP="libc6 (>= $GLIBC_MIN)"
	echo "Binary requires glibc >= $GLIBC_MIN"
else
	# No binutils to ask; better an unversioned dependency than a wrong one.
	LIBC_DEP="libc6"
	echo "objdump not found, leaving the libc6 dependency unversioned" >&2
fi

# Every shared library the binary needs. libGL is a hard ELF NEEDED entry; the
# X11 libraries are opened by glfw with dlopen at runtime, so dpkg cannot see
# them and they have to be listed by hand — `strings` on the binary lists the
# candidates. 1.0.1 listed neither libxext6 nor libxrender1, and got away with it
# only because a desktop Ubuntu already has them.
#
# The Wayland and xkbcommon packages that used to be here are gone with the
# -tags x11 build above: that binary references no libwayland-* or libxkbcommon
# at all (checked with objdump -p and strings), and depending on packages 18.04
# either lacks or ships too old would have blocked the install for nothing.
DEPENDS="$LIBC_DEP, libgl1"
DEPENDS="$DEPENDS, libx11-6, libxcursor1, libxext6, libxi6, libxinerama1"
DEPENDS="$DEPENDS, libxrandr2, libxrender1, libxxf86vm1"
DEPENDS="$DEPENDS, iputils-ping, snmp"

# Taken from the git identity, so a published package carries a real contact
# instead of "user@localhost"; falls back to the local user outside a checkout.
MAINTAINER_NAME="$(git config user.name 2>/dev/null || true)"
MAINTAINER_EMAIL="$(git config user.email 2>/dev/null || true)"
: "${MAINTAINER_NAME:=$(whoami)}"
: "${MAINTAINER_EMAIL:=$(whoami)@localhost}"

cat >"$PKG_ROOT/DEBIAN/control" <<EOF
Package: $APP
Version: $VERSION
Section: net
Priority: optional
Architecture: $ARCH
Depends: $DEPENDS
Maintainer: $MAINTAINER_NAME <$MAINTAINER_EMAIL>
Description: PingInfoManager - monitor network devices by ping and SNMP name
 GUI to monitor network device liveness via ICMP ping, with live
 success/fail/loss counters and a color-coded status table, green while a
 device's last ping answered and red while it didn't. Every device is also
 asked for its own SNMP hostname (snmpget), which is why the snmp package is
 a dependency; only the numeric OID is queried, so the MIB files
 (snmp-mibs-downloader) are not needed. The device list and the light/dark
 theme choice are kept in ~/.config/pinginfomanager/config.json.
EOF

mkdir -p dist
DEB_PATH="dist/${APP}_${VERSION}_${ARCH}.deb"
dpkg-deb --build --root-owner-group "$PKG_ROOT" "$DEB_PATH"

echo "Built $DEB_PATH"
