#!/usr/bin/env bash
# release-dist.sh VERSION [OUTDIR]
#
# Cross-compiles the pulse CLI for the six release targets
# (linux/darwin/windows x amd64/arm64), packages each binary with
# LICENSE and README.md, and writes SHA-256 sums of every archive to
# OUTDIR/checksums.txt.
#
#   pulse_<version>_<os>_<arch>.tar.gz   (linux, darwin)
#   pulse_<version>_windows_<arch>.zip   (binary named pulse.exe)
#
# OUTDIR defaults to `dist` and is wiped before the build. LDFLAGS may be
# supplied by the caller (`make dist` passes the Makefile's); when unset
# the script builds the same flags itself. Portable to bash 3.2 (macOS)
# and GNU userlands (ubuntu runners).
set -euo pipefail

if [ "$#" -lt 1 ] || [ -z "${1:-}" ]; then
	echo "usage: $0 VERSION [OUTDIR]" >&2
	exit 2
fi

VERSION="$1"
OUTDIR="${2:-dist}"
GO="${GO:-go}"
LDFLAGS="${LDFLAGS:--s -w -X github.com/frankbardon/pulse/internal/buildinfo.version=${VERSION}}"
TARGETS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

for f in LICENSE README.md; do
	if [ ! -f "$f" ]; then
		echo "release-dist: missing $f at repo root" >&2
		exit 1
	fi
done

rm -rf "$OUTDIR"
mkdir -p "$OUTDIR"
OUTDIR_ABS="$(cd "$OUTDIR" && pwd)"

STAGE_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/pulse-dist.XXXXXX")"
trap 'rm -rf "$STAGE_ROOT"' EXIT

for target in $TARGETS; do
	goos="${target%/*}"
	goarch="${target#*/}"
	name="pulse_${VERSION}_${goos}_${goarch}"
	stage="$STAGE_ROOT/$name"
	mkdir -p "$stage"

	bin="pulse"
	if [ "$goos" = "windows" ]; then
		bin="pulse.exe"
	fi

	echo "release-dist: building $goos/$goarch"
	CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
		"$GO" build -trimpath -ldflags "$LDFLAGS" -o "$stage/$bin" ./cmd/pulse
	cp LICENSE README.md "$stage/"

	if [ "$goos" = "windows" ]; then
		(cd "$stage" && zip -q "$OUTDIR_ABS/$name.zip" "$bin" LICENSE README.md)
	else
		tar -czf "$OUTDIR_ABS/$name.tar.gz" -C "$stage" "$bin" LICENSE README.md
	fi
done

if command -v sha256sum >/dev/null 2>&1; then
	SHA="sha256sum"
else
	SHA="shasum -a 256"
fi

(
	cd "$OUTDIR_ABS"
	# shellcheck disable=SC2086
	$SHA pulse_"${VERSION}"_*.tar.gz pulse_"${VERSION}"_*.zip >checksums.txt
)

echo "release-dist: wrote $(ls "$OUTDIR_ABS" | wc -l | tr -d ' ') files to $OUTDIR"
