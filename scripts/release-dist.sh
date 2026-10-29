#!/usr/bin/env bash
# release-dist.sh [--target OS/ARCH]... [--no-checksums] VERSION [OUTDIR]
# release-dist.sh --checksums-only VERSION [OUTDIR]
#
# Cross-compiles the pulse CLI for the six release targets
# (linux/darwin/windows x amd64/arm64), packages each binary with
# LICENSE, THIRD_PARTY_NOTICES.md, README.md and the shell completion
# scripts (completions/pulse.bash, completions/_pulse,
# completions/pulse.fish), and writes SHA-256 sums of every archive to
# OUTDIR/checksums.txt.
#
#   pulse_<version>_<os>_<arch>.tar.gz   (linux, darwin)
#   pulse_<version>_windows_<arch>.zip   (binary named pulse.exe)
#
# With no option (`make dist`) it builds all six targets and the
# checksums in one run. release.yml splits the same work: one job per
# platform runs `--target OS/ARCH --no-checksums` (repeatable; any of the
# six), then the publish job gathers the six archives into OUTDIR and
# runs `--checksums-only`, which builds nothing, requires every target's
# archive to be present and writes the same checksums.txt.
#
# OUTDIR defaults to `dist` and is wiped before a build (never by
# --checksums-only). LDFLAGS may be
# supplied by the caller (`make dist` passes the Makefile's); when unset
# the script builds the same flags itself. The completion scripts are
# generated ONCE by a host build (scripts/release-completions.sh) — a
# cross-compiled binary cannot run here, and the scripts are
# platform-independent. Portable to bash 3.2 (macOS)
# and GNU userlands (ubuntu runners).
set -euo pipefail

ALL_TARGETS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"

usage() {
	echo "usage: $0 [--target OS/ARCH]... [--no-checksums] VERSION [OUTDIR]" >&2
	echo "       $0 --checksums-only VERSION [OUTDIR]" >&2
	exit 2
}

TARGETS=""
CHECKSUMS=1
CHECKSUMS_ONLY=0
while [ "$#" -gt 0 ]; do
	case "$1" in
	--target)
		[ "$#" -ge 2 ] || usage
		case " $ALL_TARGETS " in
		*" $2 "*) ;;
		*)
			echo "release-dist: unknown target $2 (one of: $ALL_TARGETS)" >&2
			exit 2
			;;
		esac
		TARGETS="${TARGETS:+$TARGETS }$2"
		shift 2
		;;
	--no-checksums)
		CHECKSUMS=0
		shift
		;;
	--checksums-only)
		CHECKSUMS_ONLY=1
		shift
		;;
	--) shift && break ;;
	-*) usage ;;
	*) break ;;
	esac
done
if [ "$CHECKSUMS_ONLY" -eq 1 ] && { [ -n "$TARGETS" ] || [ "$CHECKSUMS" -eq 0 ]; }; then
	echo "release-dist: --checksums-only takes no --target or --no-checksums" >&2
	exit 2
fi
TARGETS="${TARGETS:-$ALL_TARGETS}"

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ] || [ -z "${1:-}" ]; then
	usage
fi

VERSION="$1"
OUTDIR="${2:-dist}"
GO="${GO:-go}"
LDFLAGS="${LDFLAGS:--s -w -X github.com/frankbardon/pulse/internal/buildinfo.version=${VERSION}}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# archive_name OS/ARCH -> the release archive's file name for VERSION.
archive_name() {
	if [ "${1%/*}" = "windows" ]; then
		echo "pulse_${VERSION}_${1%/*}_${1#*/}.zip"
	else
		echo "pulse_${VERSION}_${1%/*}_${1#*/}.tar.gz"
	fi
}

# write_checksums DIR: SHA-256 of every archive into DIR/checksums.txt,
# after checking that each of the six targets' archives is there.
write_checksums() {
	for t in $ALL_TARGETS; do
		if [ ! -f "$1/$(archive_name "$t")" ]; then
			echo "release-dist: missing $(archive_name "$t") in $1" >&2
			exit 1
		fi
	done
	if command -v sha256sum >/dev/null 2>&1; then
		SHA="sha256sum"
	else
		SHA="shasum -a 256"
	fi
	(
		cd "$1"
		# shellcheck disable=SC2086
		$SHA pulse_"${VERSION}"_*.tar.gz pulse_"${VERSION}"_*.zip >checksums.txt
	)
}

if [ "$CHECKSUMS_ONLY" -eq 1 ]; then
	if [ ! -d "$OUTDIR" ]; then
		echo "release-dist: no $OUTDIR directory to checksum" >&2
		exit 1
	fi
	write_checksums "$(cd "$OUTDIR" && pwd)"
	echo "release-dist: wrote $OUTDIR/checksums.txt"
	exit 0
fi

for f in LICENSE THIRD_PARTY_NOTICES.md README.md; do
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

# Host build, used only to print the completion scripts.
echo "release-dist: generating completion scripts"
HOST_BIN="$STAGE_ROOT/host/pulse"
CGO_ENABLED=0 GOOS="$("$GO" env GOHOSTOS)" GOARCH="$("$GO" env GOHOSTARCH)" \
	"$GO" build -trimpath -ldflags "$LDFLAGS" -o "$HOST_BIN" ./cmd/pulse
COMPLETIONS="$STAGE_ROOT/completions"
./scripts/release-completions.sh "$COMPLETIONS" "$HOST_BIN"
COMPLETION_FILES="completions/pulse.bash completions/_pulse completions/pulse.fish"

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
	cp LICENSE THIRD_PARTY_NOTICES.md README.md "$stage/"
	cp -R "$COMPLETIONS" "$stage/completions"

	# shellcheck disable=SC2086
	if [ "$goos" = "windows" ]; then
		(cd "$stage" && zip -q "$OUTDIR_ABS/$name.zip" "$bin" LICENSE THIRD_PARTY_NOTICES.md README.md $COMPLETION_FILES)
	else
		tar -czf "$OUTDIR_ABS/$name.tar.gz" -C "$stage" "$bin" LICENSE THIRD_PARTY_NOTICES.md README.md $COMPLETION_FILES
	fi
done

if [ "$CHECKSUMS" -eq 1 ]; then
	write_checksums "$OUTDIR_ABS"
fi

echo "release-dist: wrote $(ls "$OUTDIR_ABS" | wc -l | tr -d ' ') files to $OUTDIR"
