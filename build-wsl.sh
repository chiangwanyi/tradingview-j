#!/bin/bash
# ---------------------------------------------------------------------------
# build-wsl.sh -- produce the Windows exe from inside WSL2.
#
# Why this one exists next to build.cmd and build.sh:
#   build.cmd needs cmd.exe. build.sh cross-compiles for macOS and emits a
#   Mach-O that this machine cannot run. This one runs under WSL's Linux and
#   cross-compiles GOOS=windows/amd64, so the artifact is the very exe people
#   double-click -- built with a toolchain that lives here.
#
# The one non-obvious rule: the frontend CANNOT be built in place.
#   web/node_modules on /mnt/e was installed by Windows pnpm and carries only
#   win32-x64-msvc native binaries (rolldown, @tailwindcss/oxide,
#   lightningcss). Node refuses to load those on Linux with "Cannot find
#   native binding", and reinstalling there would break build.cmd. So web/ is
#   mirrored onto ext4, pnpm installs the linux-x64-gnu ones there, vite builds
#   there, and web/dist is copied back -- the exe reads that folder off disk at
#   runtime, so a stale one silently shows yesterday's page.
#
# The Go stage DOES run in place. The exe must stay in this folder: outDir and
# rawDir are resolved relative to the exe's own directory, so building it
# somewhere else means it reads someone else's data/ (see version_test.go).
#
# English here for the same reason build.cmd is English (see README.md, Build
# section): build output must not depend on the terminal's locale. The program
# itself still prints Chinese.
# ---------------------------------------------------------------------------
set -euo pipefail

cd "$(dirname "$0")"

usage() {
	cat <<'EOF'
Build tv-capture-v<version>.exe from inside WSL2 (cross-compiled for windows/amd64).

Usage: ./build-wsl.sh [options]

  --no-web      skip the frontend stage and build the exe only (menu 3 then
                replays whatever web/dist already holds)
  -h, --help    this text

Both stages run by default: the replay page first (in a mirrored tree on ext4,
because web/node_modules here belongs to Windows), then the Go build.

The version comes from ./VERSION and nowhere else. The exe lands in THIS
folder, beside config.json, so it reads data/ and raw/ exactly as before --
the artifact is meant to be copied to Windows and run there; it cannot be
executed from this Linux shell.
EOF
}

die() {
	echo
	echo "$1"
	exit 1
}

SKIP_WEB=0
while [ $# -gt 0 ]; do
	case "$1" in
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

# ==== 0/5 host guard ======================================================
# Catching the wrong shell early beats a confusing failure later: in Git Bash
# uname says MINGW64, and there build.cmd is the right entry point.
HOST_OS="$(uname -s)"
if [ "$HOST_OS" != "Linux" ]; then
	die "This script targets WSL2/Linux (uname reports: $HOST_OS).
On Windows run build.cmd; on macOS run build.sh."
fi

# ==== version: the one and only source is the VERSION file of this folder ====
# Same rules as build.cmd and build.sh, because version_test.go pins them: the
# version is read here, injected with -X main.version, and it names the exe.
# VERSION is stored CRLF (.gitattributes), so trim both line endings.
VER="$(head -n 1 VERSION 2>/dev/null | tr -d '[:space:]' || true)"
if [ -z "$VER" ]; then
	die "Cannot read the VERSION file - it should hold one line, e.g. 0.31.0."
fi
if ! printf '%s' "$VER" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
	die "VERSION = $VER, which is not three numeric segments (e.g. 0.31.0)."
fi

echo
echo "=== Building tv-capture-v$VER.exe (windows/amd64, from $HOST_OS) ==="
echo

# ==== 1/5 toolchain sanity =================================================
command -v go >/dev/null 2>&1 ||
	die "'go' was not found. Install Go and make sure it is on PATH."

# go-winres writes the version into the exe's Details tab. Same requirement as
# build.cmd: without it the properties page would lie about the version, so
# this is a hard stop rather than a warning.
if ! command -v go-winres >/dev/null 2>&1; then
	if [ -x "$HOME/go/bin/go-winres" ]; then
		PATH="$HOME/go/bin:$PATH"
	else
		die "'go-winres' was not found - it is what writes the version into the exe
file properties. Install it once:
    go install github.com/tc-hib/go-winres@latest
then make sure \$HOME/go/bin is on PATH and run this again."
	fi
fi

# ==== 2/5 frontend: the page menu 3 serves at runtime ======================
# Built in a mirror, never in place -- see the header comment.
WEB_MIRROR="${TV_WEB_MIRROR:-$HOME/.cache/tv-capture-web}"
# Measured on this machine: npmmirror streams 3.9MB/s, while the proxy reaching
# registry.npmjs.org took 15s for 940KB. Override with TV_WEB_REGISTRY if you
# build somewhere that prefers the origin.
WEB_REGISTRY="${TV_WEB_REGISTRY:-https://registry.npmmirror.com}"

if [ "$SKIP_WEB" = "1" ]; then
	echo "frontend: skipped (--no-web), the exe serves whatever web/dist holds"
	echo
else
	[ -f web/package.json ] ||
		die "web/package.json is missing - the frontend sources are not here. Restore them, or run with --no-web."
	command -v node >/dev/null 2>&1 ||
		die "'node' was not found - the replay page is built with it. Install Node.js and put it on PATH, or run with --no-web."

	if command -v pnpm >/dev/null 2>&1; then
		INSTALL=(pnpm install --frozen-lockfile --prefer-offline --registry="$WEB_REGISTRY")
		RUN_BUILD=(pnpm run build)
	elif command -v npm >/dev/null 2>&1; then
		INSTALL=(npm install)
		RUN_BUILD=(npm run build)
	else
		die "Neither 'pnpm' nor 'npm' was found - one of them builds the replay page."
	fi

	echo "frontend: mirroring web/ to $WEB_MIRROR ..."
	mkdir -p "$WEB_MIRROR"
	# --delete keeps the mirror honest; excluded paths are never pruned, so the
	# linux node_modules built below survives every run.
	rsync -a --delete --exclude 'node_modules/' --exclude 'dist/' web/ "$WEB_MIRROR/" ||
		die "Could not mirror web/ to $WEB_MIRROR - the frontend was not built and the exe was NOT rebuilt."

	echo "frontend: ${INSTALL[*]} ..."
	if ! (cd "$WEB_MIRROR" && "${INSTALL[@]}"); then
		echo
		echo "Frontend setup failed: dependency install returned an error (see above)."
		echo "The exe was NOT rebuilt, so no half-new product was left behind."
		exit 1
	fi

	echo "frontend: ${RUN_BUILD[*]} ..."
	if ! (cd "$WEB_MIRROR" && "${RUN_BUILD[@]}"); then
		echo
		echo "Frontend build failed - a type error in web/src fails here too,"
		echo "before vite ever runs."
		echo "The exe was NOT rebuilt, so no half-new product was left behind."
		exit 1
	fi

	[ -d "$WEB_MIRROR/dist" ] ||
		die "The frontend reported success but produced no dist/ in $WEB_MIRROR."

	# Back to the real tree: this is what the exe serves from disk.
	rsync -a --delete "$WEB_MIRROR/dist/" web/dist/ ||
		die "Could not copy dist back to web/dist - menu 3 would show a stale page."
	echo "frontend OK"
	echo
fi

# Whether menu 3 will find a page, read after the stage above so --no-web still
# reports the truth about what is on disk.
if [ -d web/dist ] && [ -n "$(ls -A web/dist 2>/dev/null)" ]; then
	WEB_OK=yes
else
	WEB_OK=no
fi

# ==== 3/5 resource object: the exe's Details tab ==========================
# Regenerated every run, exactly like build.cmd: rsrc_windows_amd64.syso is a
# compile-time snapshot and is gitignored, so a leftover one would carry the
# version of whichever build made it last.
echo "resource: go-winres make ..."
if ! go-winres make --in winres/winres.json --out rsrc --arch amd64 \
	--file-version="$VER" --product-version="$VER"; then
	echo
	echo "Resource generation failed - see the error above."
	echo "The exe was NOT built."
	exit 1
fi

# ==== 4/5 build: cross-compile, inject the version, name it after it =====
# No -trimpath here: build.cmd does not use it either, and the point of this
# script is to produce the same exe build.cmd would. CGO off is required for a
# cross-build anyway, and nothing in this program imports "C".
export CGO_ENABLED=0
export GOOS=windows
export GOARCH=amd64
OUT="tv-capture-v$VER.exe"

if ! go build -ldflags "-X main.version=$VER" -o "$OUT" .; then
	echo
	echo "Build failed - see the error above."
	if [ -f "$OUT" ]; then
		echo "NOTE: an $OUT is still on disk, but it is the PREVIOUS build."
	fi
	exit 1
fi

# ==== 5/5 report ==========================================================
echo
echo "Build OK: $OUT"
echo "  - cross-compiled on $HOST_OS for windows/amd64 (GOOS=windows, CGO_ENABLED=0)"
echo "  - version $VER comes from the VERSION file: injected with -X main.version,"
echo "    and written into the exe's file properties by go-winres"
echo "  - built in place: copy it to Windows next to config.json, and it will use"
echo "    the data/ and raw/ folders beside it"
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
if command -v file >/dev/null 2>&1; then
	echo "  - $(file -b "$OUT" | head -n 1)"
fi
echo "  - this file is a Windows PE and cannot run under $HOST_OS; verification of"
echo "    startup behaviour has to happen on Windows"
echo
