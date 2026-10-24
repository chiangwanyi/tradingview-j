#!/bin/bash
# ---------------------------------------------------------------------------
# build.sh -- the macOS counterpart of build.cmd, targeting macOS 12
# (Monterey) and newer.
#
# The three rules of the version mechanism are the same as in build.cmd,
# because version_test.go pins them: the version is read from ./VERSION and
# nowhere else, it is injected with -X main.version, and the artifact is named
# tv-capture-v<version> and lands in THIS folder.
#
# Like build.cmd it is one entry, two builds: the replay page (npm run build
# inside web/) first, then the binary. Either stage failing stops the run with a
# non-zero exit, so a half-built product is never reported as a success.
#
# What differs from build.cmd:
#   - no resource step: go-winres and rsrc_*.syso are Windows only, so there
#     is no exe "Details" tab to fill in here
#   - the artifact has no extension; double-clicking it in Finder opens
#     Terminal and runs the menu
#   - CGO is off: no dylib from Homebrew is needed at runtime, and either
#     architecture can be cross-built from either machine (see --arch)
#
# English here for the same reason build.cmd is English (see README.md, Build section):
# build output must not depend on the terminal's locale. The program itself
# still prints Chinese.
# ---------------------------------------------------------------------------
set -euo pipefail

cd "$(dirname "$0")"

usage() {
	cat <<'EOF'
Build tv-capture for macOS 12+ (Monterey and newer).

Usage: ./build.sh [options]

  --arch native|amd64|arm64|universal
                native     host architecture only (default, fastest)
                amd64      Intel
                arm64      Apple Silicon
                universal  one file carrying both slices, for handing it
                           to someone whose Mac is not known
  --no-web      skip the frontend stage and build the binary only (the replay
                page then keeps whatever was built last time)
  -h, --help    this text

Both stages run by default: npm run build inside web/ first (menu 3 serves that
output at runtime), then the Go build. A failure in either one exits non-zero.

The version comes from ./VERSION. The artifact (tv-capture-v<version>) lands
in this folder, next to config.json, so it keeps reading data/ and raw/ from
here -- moving it elsewhere means giving it a fresh, empty data folder.
EOF
}

die() {
	echo
	echo "$1"
	exit 1
}

ARCH="native"
SKIP_WEB=0
while [ $# -gt 0 ]; do
	case "$1" in
	--arch)
		[ $# -ge 2 ] || die "--arch needs a value: native, amd64, arm64 or universal"
		ARCH="$2"
		shift 2
		;;
	--arch=*)
		ARCH="${1#--arch=}"
		shift
		;;
	--no-web)
		SKIP_WEB=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		echo "Unknown option: $1"
		echo
		usage
		exit 1
		;;
	esac
done

case "$ARCH" in
native | amd64 | arm64 | universal) ;;
*) die "--arch must be native, amd64, arm64 or universal (got: $ARCH)" ;;
esac

# ==== version: the one and only source is the VERSION file of this folder ====
# Only the first line counts, and the file is stored with CRLF (.gitattributes),
# so trim both line endings and stray blanks. version_test.go trims the same
# way; if these two disagree, "changed VERSION but nothing happened" comes back.
# (the trailing "|| true" is load-bearing: with pipefail a missing VERSION makes
# head fail, and set -e would then kill the script before it can say why)
VER="$(head -n 1 VERSION 2>/dev/null | tr -d '[:space:]' || true)"
if [ -z "$VER" ]; then
	die "Cannot read the VERSION file - it should hold one line, e.g. 0.14.0."
fi
if ! printf '%s' "$VER" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
	die "VERSION = $VER, which is not three numeric segments (e.g. 0.14.0)."
fi

echo
echo "=== Building tv-capture-v$VER ($ARCH) ==="
echo

# ==== 0/5 toolchain sanity =================================================
if ! command -v go >/dev/null 2>&1; then
	die "'go' was not found. Install Go and make sure it is on PATH."
fi

# Go 1.27 raised the minimum from macOS 11 to macOS 13, so a newer toolchain
# silently produces a binary that will not start on Monterey. That failure is
# invisible on the build machine (it runs a newer OS), so say it out loud.
GOVER="$(go env GOVERSION 2>/dev/null || true)"
GOMINOR="$(printf '%s' "$GOVER" | sed -n 's/^go1\.\([0-9][0-9]*\).*/\1/p')"
if [ -n "$GOMINOR" ] && [ "$GOMINOR" -gt 26 ]; then
	echo "warning: $GOVER is newer than go1.26."
	echo "         Go 1.27 dropped macOS 12 support, so this binary will need"
	echo "         macOS 13 or newer. Build with a go1.26.x toolchain to stay"
	echo "         compatible with Monterey."
	echo
fi

HOST_ARCH="$(go env GOHOSTARCH)"
case "$ARCH" in
native) TARGET_ARCH="$HOST_ARCH" ;;
*) TARGET_ARCH="$ARCH" ;;
esac

# lipo and file say x86_64 where Go says amd64; the check below compares
# against the Mach-O spelling, not the Go one.
case "$TARGET_ARCH" in
amd64) EXPECT_ARCH="x86_64" ;;
*) EXPECT_ARCH="$TARGET_ARCH" ;;
esac

if [ "$ARCH" = "universal" ] && ! command -v lipo >/dev/null 2>&1; then
	die "'lipo' was not found - it ships with the Xcode command line tools.
Install them once with: xcode-select --install"
fi

# ==== 1/5 frontend: the page menu 3 serves at runtime ======================
# web/dist is NOT embedded: Go cannot //go:embed across directories, so menu 3
# serves that folder from disk when it starts -- which means a stale one
# silently shows yesterday's page. That is why it is a real stage here and not
# advice in the report. --no-web skips it and leaves the last build in place.
if [ "$SKIP_WEB" = "1" ]; then
	echo "frontend: skipped (--no-web), menu 3 replays whatever was built last time"
	echo
else
	[ -f web/package.json ] ||
		die "web/package.json is missing - the frontend sources are not here. Restore them, or run with --no-web."
	command -v npm >/dev/null 2>&1 ||
		die "'npm' was not found - the replay page is built with it. Install Node.js and put it on PATH, or run with --no-web."
	if [ ! -d web/node_modules ]; then
		echo "frontend: web/node_modules is missing, running npm install once ..."
		if ! (cd web && npm install); then
			echo
			echo "Frontend setup failed: npm install returned an error (see above)."
			echo "The binary was not built."
			exit 1
		fi
	fi
	echo "frontend: npm run build ..."
	if ! (cd web && npm run build); then
		echo
		echo "Frontend build failed - a type error in web/src fails here too,"
		echo "before vite ever runs."
		echo "The binary was NOT built, so no half-new product was left behind."
		exit 1
	fi
	echo "frontend OK"
	echo
fi

# Whether menu 3 will actually find a page. Read after the stage above, so that
# --no-web still reports the truth about what is on disk.
if [ -d web/dist ] && [ -n "$(ls -A web/dist 2>/dev/null)" ]; then
	WEB_OK=yes
else
	WEB_OK=no
fi

# ==== 2/5 build ============================================================
# CGO off is what makes one binary portable: no Homebrew dylib, no SDK needed
# on the target machine, and amd64/arm64 are built from the same host. Nothing
# in this program imports "C" (only internal/logx branches on windows), so
# there is no cgo code to lose.
export CGO_ENABLED=0
export GOOS=darwin
LDFLAGS="-X main.version=$VER"
OUT="tv-capture-v$VER"

build_one() {
	local arch="$1" out="$2"
	if ! GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" -o "$out" .; then
		echo
		echo "Build failed for $arch - see the error above."
		exit 1
	fi
}

# Old artifact goes first. Measured (2026-10-21, go1.26.9): `go build -o` does
# nothing at all when the result is already in the build cache - it never looks
# at what is on disk. Build universal, then plain ./build.sh, and the two-slice
# file stays there while the script happily reports "native". Deleting it makes
# "what is in this folder" always mean "what this run produced".
rm -f "$OUT"

if [ "$ARCH" = "universal" ]; then
	TMP="$(mktemp -d "${TMPDIR:-/tmp}/tv-capture-build.XXXXXX")"
	trap 'rm -rf "$TMP"' EXIT
	echo "building darwin/amd64 ..."
	build_one amd64 "$TMP/amd64"
	echo "building darwin/arm64 ..."
	build_one arm64 "$TMP/arm64"
	echo "merging into one universal file ..."
	lipo -create -output "$OUT" "$TMP/amd64" "$TMP/arm64"
else
	build_one "$TARGET_ARCH" "$OUT"
fi
chmod +x "$OUT"

# ==== 3/5 check the artifact really is what was asked for ==================
# lipo -archs is one short line for both cases; file(1) wraps a universal file
# over several lines, which reads badly in a report.
GOT=""
if command -v lipo >/dev/null 2>&1; then
	GOT="$(lipo -archs "$OUT" 2>/dev/null | tr '\n' ' ' | sed 's/[[:space:]]*$//')"
fi
has_arch() {
	case " $GOT " in
	*" $1 "*) return 0 ;;
	*) return 1 ;;
	esac
}
if [ -n "$GOT" ]; then
	if [ "$ARCH" = "universal" ]; then
		if ! has_arch x86_64 || ! has_arch arm64; then
			die "expected an amd64+arm64 universal file, lipo reports: $GOT"
		fi
	elif ! has_arch "$EXPECT_ARCH"; then
		die "expected $EXPECT_ARCH, but the file on disk is: $GOT"
	fi
fi

# ==== 4/5 ad-hoc signature =================================================
# arm64 refuses to exec an unsigned Mach-O. Go signs its own output on
# darwin/arm64, but lipo rewrites the file and throws that signature away, so
# every build gets an explicit ad-hoc signature. This is not a Developer ID:
# Gatekeeper still asks for a right-click -> Open the first time the file is
# carried to another Mac.
SIGNED=no
if command -v codesign >/dev/null 2>&1; then
	if codesign --force --sign - "$OUT" >/dev/null 2>&1; then
		SIGNED=yes
	else
		echo "warning: ad-hoc signing failed - on Apple Silicon this binary"
		echo "         may be killed on startup."
	fi
fi

# ==== report ===============================================================
echo
echo "Build OK: $OUT"
echo "  - version $VER comes from the VERSION file: injected with -X main.version,"
echo "    and it is also what the banner and /api/analysis report"
if [ -n "$GOT" ]; then
	echo "  - architectures: $GOT"
elif command -v file >/dev/null 2>&1; then
	echo "  - $(file -b "$OUT" | head -n 1)"
fi
# The minimum OS is baked into the toolchain, not into the host, so read it
# back from the Mach-O instead of assuming: this is the line that answers
# "will it still start on macOS 12?".
if command -v otool >/dev/null 2>&1; then
	MINOS="$(otool -l "$OUT" 2>/dev/null | awk '/minos/ {print $2; exit}')"
	if [ -n "$MINOS" ]; then
		echo "  - minimum macOS: $MINOS"
		MINOS_MAJOR="${MINOS%%.*}"
		case "$MINOS_MAJOR" in
		'' | *[!0-9]*) ;;
		*)
			if [ "$MINOS_MAJOR" -gt 12 ]; then
				echo "    warning: this needs macOS $MINOS_MAJOR+, so it will NOT start"
				echo "             on macOS 12 - rebuild with a go1.26.x toolchain."
			fi
			;;
		esac
	fi
fi
if [ "$SIGNED" = "yes" ]; then
	echo "  - signed ad-hoc (no Developer ID): on another Mac, right-click ->"
	echo "    Open the first time, or run xattr -d com.apple.quarantine $OUT"
fi
echo "  - built in place: it reads config.json from this folder and keeps using"
echo "    the data/ and raw/ folders here"
if [ "$WEB_OK" = "yes" ]; then
	if [ "$SKIP_WEB" = "1" ]; then
		echo "  - web/dist is present but untouched (--no-web): menu 3 replays the"
		echo "    page from an earlier build"
	else
		echo "  - web/dist was rebuilt in this same run, so menu 3 (replay) serves"
		echo "    the current page"
	fi
else
	echo "  - web/dist is missing: menu 3 will say the page is not built yet"
fi
echo
echo "Run it: ./$OUT"
echo
