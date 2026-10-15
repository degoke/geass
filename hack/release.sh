#!/usr/bin/env bash
# Prepares and tags a Geass release. See docs/releasing.md for the whole process.
#
#   hack/release.sh prepare v0.2.0   # move CHANGELOG.md's Unreleased notes under the new version (commit it in a pull request)
#   hack/release.sh tag v0.2.0       # from an up to date main: create the annotated tag, and say how to publish it
#   hack/release.sh check v0.2.0     # only check the version is a release tag (the release workflow runs this too)
#
# Nothing here pushes: publishing a tag is what starts the release workflow, so that is left to the person releasing.
#
# RELEASE_REMOTE and RELEASE_BRANCH (default origin and main) say where releases are cut from.
set -euo pipefail

cd "$(dirname "$0")/.."

remote="${RELEASE_REMOTE:-origin}"
branch="${RELEASE_BRANCH:-main}"
repo_url="${RELEASE_REPO_URL:-https://github.com/degoke/geass}"
changelog="${RELEASE_CHANGELOG:-CHANGELOG.md}"

die() { echo "release: $*" >&2; exit 1; }

# A release tag is v<major>.<minor>.<patch>, optionally followed by -<pre-release> such as -rc.1. This is the one place
# the rule is written for shell: the release workflow calls `check`. pkg/release has the Go side, which accepts this
# and a little more.
semver='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$'

validate() {
	[[ "$1" =~ $semver ]] || die "'$1' is not a release tag: use vMAJOR.MINOR.PATCH, or vMAJOR.MINOR.PATCH-rc.1 for a pre-release"
}

prerelease() { [[ "$1" == *-* ]]; }

# The notes under "## [Unreleased]", without blank lines at the edges.
unreleased_notes() {
	awk '
		/^## \[Unreleased\]/ { in_section = 1; next }
		/^## \[/ { in_section = 0 }
		in_section { print }
	' "$changelog" | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}'
}

has_section() {
	grep -qE "^## \[${1#v}\] - " "$changelog"
}

prepare() {
	local tag="$1" version="${1#v}"
	prerelease "$tag" && die "a pre-release has no CHANGELOG section of its own: its notes are the Unreleased ones. Tag it with 'tag $tag'"
	has_section "$tag" && die "$changelog already has a section for $version"
	[ -n "$(unreleased_notes)" ] || die "$changelog has nothing under [Unreleased]: write what changed there first"

	local previous date link
	previous="$(grep -m1 -oE '^## \[[0-9][^]]*\]' "$changelog" | sed -E 's/^## \[(.*)\]$/\1/' || true)"
	date="$(date -u +%F)"
	if [ -n "$previous" ]; then
		link="$repo_url/compare/v$previous...$tag"
	else
		link="$repo_url/releases/tag/$tag"
	fi

	# The file is edited where it stands, so every other link definition in it is kept: the new section goes under
	# [Unreleased], and the [Unreleased] link is pointed at the new tag with the new version's link beside it.
	awk -v version="$version" -v date="$date" -v tag="$tag" -v repo="$repo_url" -v link="$link" '
		/^## \[Unreleased\]/ { print; print ""; print "## [" version "] - " date; next }
		/^\[Unreleased\]: / { print "[Unreleased]: " repo "/compare/" tag "...HEAD"; print "[" version "]: " link; linked = 1; next }
		{ print }
		END { if (!linked) { print ""; print "[Unreleased]: " repo "/compare/" tag "...HEAD"; print "[" version "]: " link } }
	' "$changelog" >"$changelog.new"
	mv "$changelog.new" "$changelog"

	echo "Moved the Unreleased notes under [$version] - $date in $changelog."
	echo "Next: read it, commit it in a pull request, merge it, then from an up to date $branch run: hack/release.sh tag $tag"
}

tag() {
	local tag="$1"
	git rev-parse --git-dir >/dev/null 2>&1 || die "not in a git checkout"
	[ -z "$(git status --porcelain)" ] || die "the working tree has changes: commit or stash them first"

	git fetch --quiet "$remote" "$branch" "+refs/tags/*:refs/tags/*" || die "could not fetch $remote"
	[ "$(git rev-parse HEAD)" = "$(git rev-parse "$remote/$branch")" ] ||
		die "HEAD is not $remote/$branch: releases are cut from the tip of $branch (git switch $branch && git pull)"

	! git rev-parse --verify --quiet "refs/tags/$tag" >/dev/null || die "the tag $tag already exists"
	[ -z "$(git ls-remote --tags "$remote" "refs/tags/$tag")" ] || die "the tag $tag already exists on $remote"

	if prerelease "$tag"; then
		[ -n "$(unreleased_notes)" ] || die "$changelog has nothing under [Unreleased], which is where a pre-release's notes come from, and the release workflow does not publish without notes: write them first"
	else
		has_section "$tag" || die "$changelog has no section for ${tag#v}: run 'hack/release.sh prepare $tag', and merge the result"
		[ -z "$(unreleased_notes)" ] || die "$changelog still has notes under [Unreleased], which this release's notes do not include (a change merged after the release pull request). Move those lines under [${tag#v}] in a pull request, merge it, then tag again"
	fi

	git tag -a "$tag" -m "Geass $tag"
	echo "Created the tag $tag on $(git rev-parse --short HEAD)."
	echo "Publish it to start the release workflow:  git push $remote $tag"
	echo "Undo it before publishing with:             git tag -d $tag"
	echo "Afterwards, make the new release the CRD compatibility baseline: make crd-baseline REF=$tag"
}

main() {
	[ "$#" -eq 2 ] || die "usage: hack/release.sh prepare|tag|check vMAJOR.MINOR.PATCH"
	validate "$2"
	case "$1" in
	prepare) prepare "$2" ;;
	tag) tag "$2" ;;
	check) ;;
	*) die "usage: hack/release.sh prepare|tag|check vMAJOR.MINOR.PATCH" ;;
	esac
}

main "$@"
