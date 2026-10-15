#!/usr/bin/env bash
# Prints the CHANGELOG.md notes for a release, for the body of its GitHub release.
#
#   hack/release-notes.sh v0.2.0
#
# A release has its own section, "## [0.2.0] - 2026-11-02". A pre-release (v0.2.0-rc.1) has none: its notes are
# whatever is under [Unreleased]. A release with no notes is an error, so one is never published without them.
set -euo pipefail

cd "$(dirname "$0")/.."

changelog="${RELEASE_CHANGELOG:-CHANGELOG.md}"
tag="${1:?usage: hack/release-notes.sh vMAJOR.MINOR.PATCH}"
version="${tag#v}"

if [[ "$tag" == *-* ]]; then
	heading='Unreleased'
else
	heading="$version"
fi

notes="$(awk -v heading="$heading" '
	index($0, "## [" heading "]") == 1 { in_section = 1; next }
	/^## \[/ { in_section = 0 }
	/^\[[^]]+\]: / { in_section = 0 }
	in_section { print }
' "$changelog" | sed -e '/./,$!d' | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}')"

if [ -z "$notes" ]; then
	echo "release-notes: $changelog has no notes for [$heading]" >&2
	exit 1
fi
printf '%s\n' "$notes"
