#!/usr/bin/env bash
# release-completions.sh OUTDIR PULSE [ARGS...]
#
# Writes the shell completion scripts the release archives ship, by
# running `PULSE [ARGS...] completion <shell>` once per supported shell:
#
#   OUTDIR/pulse.bash   bash
#   OUTDIR/_pulse       zsh   (the name zsh's fpath autoloader expects)
#   OUTDIR/pulse.fish   fish
#
# PULSE must be a binary that runs on the HOST (release-dist.sh builds
# one; cross-compiled release binaries cannot run here). The scripts are
# platform-independent, so one host-generated set goes into every
# archive. Each file is byte-equal to the leaf's stdout — pinned by
# TestCompletion_ShippedScriptsMatchLeaf in cmd/pulse. The shell list
# mirrors internal/cli.CompletionShells(); the filenames are packaging.
# Portable to bash 3.2 (macOS) and GNU userlands.
set -euo pipefail

if [ "$#" -lt 2 ] || [ -z "${1:-}" ]; then
	echo "usage: $0 OUTDIR PULSE [ARGS...]" >&2
	exit 2
fi

OUTDIR="$1"
shift

mkdir -p "$OUTDIR"

for pair in bash:pulse.bash zsh:_pulse fish:pulse.fish; do
	shell="${pair%%:*}"
	file="${pair#*:}"
	"$@" completion "$shell" >"$OUTDIR/$file"
	if [ ! -s "$OUTDIR/$file" ]; then
		echo "release-completions: empty $shell script" >&2
		exit 1
	fi
done
