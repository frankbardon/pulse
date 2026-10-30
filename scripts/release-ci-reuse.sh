#!/usr/bin/env bash
# release-ci-reuse.sh TESTED_SHA TAG_SHA
#
# Decides whether a passing ci.yml run on TESTED_SHA also covers the
# tagged commit TAG_SHA, so release.yml's `verify` job can skip re-running
# CI. Exit 0 = reusable, 1 = not reusable (run CI), 2 = usage or git error.
#
# Reusable when the two commits have the same tree, OR every path that
# differs between them is ADDED or MODIFIED (never deleted, renamed or
# changed in type: a test that checks a pointed-at file exists would see
# the deletion) and matches the allowlist below. Anything else, or any
# doubt, means CI runs: the default is safe.
#
# The allowlist holds ONLY paths that no test, go:embed directive, golden,
# doc generator, Makefile target or script reads, and that are not shipped
# in a release archive. Evidence, gathered by grepping the tree:
#   - go:embed directives all name package-local files under internal/
#     (skills, examples, guides, testdata, zoneinfo); none reaches docs/
#     or the repo root.
#   - mdBook (ci.yml `docs` job) builds docs/src only (docs/book.toml
#     `src = "src"`); its one {{#include}} reads internal/examples.
#   - Tests that read markdown by path: TestReferenceDocPointersResolve
#     walks .claude, internal/skills and docs/src; TestSkillsCoverAll-
#     CrossReferences stats skill-named skills/, docs/ and .claude/ paths
#     (existence only, hence no deletions); TestDocsGeneratedCurrent
#     regenerates docs/src/guide; cmd/pulse reads docs/src/cli/flags.md;
#     internal/processing walks docs/src.
#   - Tests that read docs/roadmap: internal/descriptor
#     guidance_exemptions_test.go reads docs/roadmap/units/*.md
#     frontmatter (status) and internal/integration/
#     formula_total_test.go reads docs/roadmap/fixtures/** — both are
#     EXCLUDED below.
#   - Whole-repo walks (zone-math, stride, format-version, panic-recovery,
#     paramread, iocore, fmagate, invariance, errors, types gates) read
#     .go files only. internal/tools/sites reads every tracked file, but
#     its grep-parity test compares two scanners with the same token and
#     binary rules over the same tree, so file content cannot split them.
#   - Makefile and scripts/ mention docs/internals only in comments;
#     release-dist.sh packages README.md, LICENSE and
#     THIRD_PARTY_NOTICES.md, so those are NOT allowlisted.
# A new test, embed or build step that reads an allowlisted path must
# remove it from this list in the same change.
#
# Portable to bash 3.2 (macOS) and GNU userlands (ubuntu runners).
set -euo pipefail

if [ "$#" -ne 2 ] || [ -z "$1" ] || [ -z "$2" ]; then
	echo "usage: $0 TESTED_SHA TAG_SHA" >&2
	exit 2
fi

tested="$1"
tag="$2"

for c in "$tested" "$tag"; do
	if ! git cat-file -e "${c}^{commit}" 2>/dev/null; then
		echo "release-ci-reuse: $c is not a commit in this clone" >&2
		exit 2
	fi
done

# allowlisted PATH -> 0 when nothing tested, embedded or built reads PATH.
allowlisted() {
	case "$1" in
	# Read by tests (see the evidence above): never allowlisted.
	docs/roadmap/units/* | docs/roadmap/fixtures/*) return 1 ;;
	# Roadmap prose: release notes, reviews, plans, TODO.
	docs/roadmap/*.md) return 0 ;;
	# Benchmark baselines, referenced from comments and prose only.
	docs/internals/*.md | docs/internals/*.txt) return 0 ;;
	# Repository community files; nothing reads or ships them.
	CONTRIBUTING.md | CODE_OF_CONDUCT.md | SECURITY.md) return 0 ;;
	*) return 1 ;;
	esac
}

if [ "$(git rev-parse "${tested}^{tree}")" = "$(git rev-parse "${tag}^{tree}")" ]; then
	echo "release-ci-reuse: $tested has the same tree as $tag"
	exit 0
fi

diff="$(git diff --no-renames --name-status "$tested" "$tag")"
reusable=0
while IFS="$(printf '\t')" read -r status path; do
	[ -n "$status" ] || continue
	case "$status" in
	A | M) ;;
	*)
		echo "release-ci-reuse: $path: status $status (only added or modified paths are reusable)"
		reusable=1
		continue
		;;
	esac
	if ! allowlisted "$path"; then
		echo "release-ci-reuse: $path: not in the allowlist"
		reusable=1
	fi
done <<EOF
$diff
EOF

if [ "$reusable" -eq 0 ]; then
	echo "release-ci-reuse: $tested differs from $tag only in allowlisted paths"
fi
exit "$reusable"
