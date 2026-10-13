#!/usr/bin/env sh
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

# Everything runs from main, called on the last line, so that sh has read the whole script before it does
# anything. This script usually arrives on sh's standard input (curl ... | sudo sh), and the installer it
# starts inherits that input.
main() {
	ARCH="$(detect_arch)"

	if [ "$VERSION" = "latest" ]; then
		URL="https://github.com/${REPO}/releases/latest/download/geass-linux-${ARCH}"
	else
		URL="https://github.com/${REPO}/releases/download/${VERSION}/geass-linux-${ARCH}"
	fi

	TMP="$(mktemp)"
	# The installer runs as a child, not through exec, so this cleanup runs when it ends: exec would replace
	# this shell and leave the downloaded binary in the temporary directory.
	trap 'rm -f "$TMP"' EXIT
	trap 'exit 129' HUP
	trap 'exit 130' INT
	trap 'exit 143' TERM

	echo "Downloading Geass from ${URL}"
	# -f fails on an HTTP error instead of saving the error page, -S still prints why curl failed.
	if ! curl -fsSL "$URL" -o "$TMP"; then
		echo "Could not download Geass from ${URL}" >&2
		echo "Check that the release exists (GEASS_VERSION is ${VERSION}) and has a geass-linux-${ARCH} file," >&2
		echo "and that this machine can reach github.com over HTTPS." >&2
		exit 1
	fi
	chmod +x "$TMP"
	# With set -e, a failed install ends this script with the installer's own exit status.
	"$TMP"
}

main
