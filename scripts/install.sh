#!/usr/bin/env sh
# Downloads the Geass installer for this machine and runs it.
#
#   curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh
#
# GEASS_VERSION picks a release (default: latest), GEASS_REPO another fork. GEASS_DOWNLOAD_BASE replaces the place the
# release files are downloaded from (a mirror, or a directory with a file:// address); it must hold geass-linux-<arch>
# and SHA256SUMS. What follows the script's name goes to the installer: ... | sudo sh -s -- --reinstall --yes
set -eu

REPO="${GEASS_REPO:-degoke/geass}"
VERSION="${GEASS_VERSION:-latest}"

detect_arch() {
	case "$(uname -m)" in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	*)
		echo "unsupported architecture: $(uname -m)" >&2
		exit 1
		;;
	esac
}

# Checks a downloaded file against the release's SHA256SUMS: verify FILE NAME SUMS, where NAME is the name SUMS lists it by.
verify() {
	want="$(awk -v n="$2" '$2 == n || $2 == "*" n { print $1; exit }' "$3")"
	if [ -z "$want" ]; then
		echo "$2 is not listed in SHA256SUMS, so the download cannot be checked." >&2
		exit 1
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		got="$(sha256sum "$1" | awk '{ print $1 }')"
	elif command -v shasum >/dev/null 2>&1; then
		got="$(shasum -a 256 "$1" | awk '{ print $1 }')"
	else
		echo "Neither sha256sum nor shasum is installed, so the download cannot be checked." >&2
		exit 1
	fi
	if [ "$got" != "$want" ]; then
		echo "The download of $2 does not match SHA256SUMS (expected $want, got $got)." >&2
		echo "Nothing was installed. Try again; if it keeps failing, report it at https://github.com/${REPO}/issues." >&2
		exit 1
	fi
}

# Everything runs from main, called on the last line, so that sh has read the whole script before it does
# anything. This script usually arrives on sh's standard input (curl ... | sudo sh), and the installer it
# starts inherits that input.
main() {
	ARCH="$(detect_arch)"
	NAME="geass-linux-${ARCH}"

	if [ -n "${GEASS_DOWNLOAD_BASE:-}" ]; then
		BASE="${GEASS_DOWNLOAD_BASE%/}"
	elif [ "$VERSION" = "latest" ]; then
		BASE="https://github.com/${REPO}/releases/latest/download"
	else
		BASE="https://github.com/${REPO}/releases/download/${VERSION}"
	fi

	TMP="$(mktemp)"
	SUMS="$(mktemp)"
	# The installer runs as a child, not through exec, so this cleanup runs when it ends: exec would replace
	# this shell and leave the downloaded binary in the temporary directory.
	trap 'rm -f "$TMP" "$SUMS"' EXIT
	trap 'exit 129' HUP
	trap 'exit 130' INT
	trap 'exit 143' TERM

	echo "Downloading Geass from ${BASE}/${NAME}"
	# -f fails on an HTTP error instead of saving the error page, -S still prints why curl failed.
	if ! curl -fsSL "${BASE}/${NAME}" -o "$TMP" || ! curl -fsSL "${BASE}/SHA256SUMS" -o "$SUMS"; then
		echo "Could not download Geass from ${BASE}" >&2
		echo "Check that the release exists (GEASS_VERSION is ${VERSION}) and has ${NAME} and SHA256SUMS files," >&2
		echo "and that this machine can reach github.com over HTTPS." >&2
		exit 1
	fi
	verify "$TMP" "$NAME" "$SUMS"
	echo "Checked ${NAME} against SHA256SUMS"

	chmod 755 "$TMP"
	# With set -e, a failed install ends this script with the installer's own exit status.
	"$TMP" "$@"
}

main "$@"
