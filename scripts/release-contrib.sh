#!/usr/bin/env bash
# release-contrib.sh TAG [--push]
#
# Cuts the lockstep contrib tags for a root release tag TAG (vX.Y.Z or
# vX.Y.Z-pre). Every nested module under contrib/ (otelpulse, prompulse)
# is released at exactly the root version:
#
#   1. root tag TAG                    (pushed by a human; triggers release.yml)
#   2. release commit on top of TAG    (each contrib go.mod: require pulse TAG, tidied)
#   3. contrib/<module>/TAG            (one per module, all on the release commit)
#
# The release commit is DETACHED — it is never pushed to a branch, only
# reachable from the contrib tags — so branch protection on main is not
# involved and main keeps `require pulse v0.0.0` + `replace => ../..` for
# development. The `replace` stays in the tagged go.mod too: Go ignores a
# dependency module's replace directives, so consumers resolve the
# `require` against the published root tag.
#
# Without --push (the default) this is a DRY RUN: it builds and tests the
# release commit in a throwaway worktree and prints the tag plan, but
# creates no tag and pushes nothing. If TAG does not exist locally the
# dry run plans against HEAD. With --push, TAG must already exist on
# origin; the script then verifies each module resolves and builds
# against the published root tag with the replace dropped, creates the
# annotated contrib tags and pushes them atomically. Re-running after a
# successful push is a no-op. Portable to bash 3.2 (macOS) and GNU
# userlands (ubuntu runners).
set -euo pipefail

usage() {
	echo "usage: $0 TAG [--push]" >&2
	exit 2
}

[ "$#" -ge 1 ] || usage
TAG="$1"
PUSH=0
case "${2:-}" in
"") ;;
--push) PUSH=1 ;;
*) usage ;;
esac

if ! printf '%s\n' "$TAG" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'; then
	echo "release-contrib: $TAG is not a vX.Y.Z[-pre] tag" >&2
	exit 2
fi

GO="${GO:-go}"
ROOT_MODULE="github.com/frankbardon/pulse"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

MODULES=""
for gomod in contrib/*/go.mod; do
	[ -f "$gomod" ] || continue
	MODULES="$MODULES $(dirname "$gomod")"
done
if [ -z "$MODULES" ]; then
	echo "release-contrib: no contrib/*/go.mod found" >&2
	exit 1
fi

if git rev-parse -q --verify "refs/tags/$TAG^{commit}" >/dev/null; then
	BASE="$TAG"
elif [ "$PUSH" -eq 1 ]; then
	echo "release-contrib: root tag $TAG not found; tag and push the root module first" >&2
	exit 1
else
	BASE="HEAD"
	echo "release-contrib: dry run — root tag $TAG not found locally, planning against HEAD"
fi
BASE_SHA="$(git rev-parse "$BASE^{commit}")"

if [ "$PUSH" -eq 1 ]; then
	if [ -z "$(git ls-remote --tags origin "refs/tags/$TAG")" ]; then
		echo "release-contrib: root tag $TAG is not on origin; push it first" >&2
		exit 1
	fi
	existing=0
	for m in $MODULES; do
		if [ -n "$(git ls-remote --tags origin "refs/tags/$m/$TAG")" ]; then
			existing=$((existing + 1))
		fi
	done
	set -- $MODULES
	if [ "$existing" -eq "$#" ]; then
		echo "release-contrib: every contrib/*/$TAG tag is already on origin; nothing to do"
		exit 0
	elif [ "$existing" -ne 0 ]; then
		echo "release-contrib: only some contrib/*/$TAG tags exist on origin; resolve by hand" >&2
		exit 1
	fi
fi

WORK="$(mktemp -d "${TMPDIR:-/tmp}/pulse-contrib.XXXXXX")"
WT="$WORK/tree"
cleanup() {
	git -C "$ROOT" worktree remove --force "$WT" >/dev/null 2>&1 || true
	rm -rf "$WORK"
}
trap cleanup EXIT

git worktree add --quiet --detach "$WT" "$BASE_SHA"

for m in $MODULES; do
	echo "release-contrib: $m require $ROOT_MODULE $TAG"
	(
		cd "$WT/$m"
		"$GO" mod edit -require="$ROOT_MODULE@$TAG"
		"$GO" mod tidy
		"$GO" vet ./...
		"$GO" test -count=1 ./...
	)
done

if [ "$PUSH" -eq 1 ]; then
	# Resolve against the PUBLISHED root tag, not the in-repo replace:
	# what a consumer's `go get` sees. GOPROXY=direct avoids waiting on
	# the proxy to notice a minutes-old tag.
	for m in $MODULES; do
		echo "release-contrib: $m resolves $ROOT_MODULE@$TAG without replace"
		probe="$WORK/probe-$(basename "$m")"
		cp -R "$WT/$m" "$probe"
		(
			cd "$probe"
			"$GO" mod edit -dropreplace="$ROOT_MODULE"
			GOPROXY=direct GONOSUMDB="$ROOT_MODULE" GOFLAGS=-mod=mod "$GO" build ./...
		)
	done
fi

git -C "$WT" add -- $(for m in $MODULES; do printf '%s/go.mod %s/go.sum ' "$m" "$m"; done)
# One identity for the release commit AND the annotated tags: a CI runner
# has no git config, and `git tag -a` needs a tagger as much as `commit`
# needs an author.
GIT_ID=(
	-c "user.name=${GIT_AUTHOR_NAME:-github-actions[bot]}"
	-c "user.email=${GIT_AUTHOR_EMAIL:-41898282+github-actions[bot]@users.noreply.github.com}"
)
git -C "$WT" "${GIT_ID[@]}" commit --quiet --allow-empty -m "release: contrib modules require $ROOT_MODULE $TAG"
RELEASE_SHA="$(git -C "$WT" rev-parse HEAD)"

echo
echo "release-contrib: tag plan for $TAG"
echo "  1. $TAG -> $BASE_SHA (root)"
echo "  2. release commit $RELEASE_SHA (parent $BASE_SHA):"
git -C "$WT" --no-pager diff --stat "$BASE_SHA" "$RELEASE_SHA" | sed 's/^/       /'
git -C "$WT" --no-pager diff "$BASE_SHA" "$RELEASE_SHA" -- '*/go.mod' | grep -E "^[-+][[:space:]]*$ROOT_MODULE " | sed 's/^/       /' || true
n=3
for m in $MODULES; do
	echo "  $n. $m/$TAG -> $RELEASE_SHA"
	n=$((n + 1))
done

if [ "$PUSH" -eq 0 ]; then
	echo
	echo "release-contrib: dry run — no tag created, nothing pushed (pass --push to release)"
	exit 0
fi

refs=""
for m in $MODULES; do
	git "${GIT_ID[@]}" tag -a "$m/$TAG" -m "$m $TAG (lockstep with $TAG)" "$RELEASE_SHA"
	refs="$refs refs/tags/$m/$TAG"
done
# shellcheck disable=SC2086
git push --atomic origin $refs
echo "release-contrib: pushed$refs"
