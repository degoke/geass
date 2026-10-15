#!/usr/bin/env sh
# Installs the geass command line (and with it the MCP server, `geass mcp`).
#
#   curl -sfL https://github.com/degoke/geass/releases/latest/download/install-cli.sh | sh
#
# GEASS_VERSION picks a release (default: latest), GEASS_INSTALL_DIR where to put it (default:
# /usr/local/bin when writable, otherwise ~/.local/bin), GEASS_REPO another fork. GEASS_DOWNLOAD_BASE replaces the place
# the release files are downloaded from (a mirror, or a directory with a file:// address); it must hold
# geass-cli-<os>-<arch> and SHA256SUMS.
set -eu

REPO="${GEASS_REPO:-degoke/geass}"
VERSION="${GEASS_VERSION:-latest}"

case "$(uname -s)" in
Linux) OS=linux ;;
Darwin) OS=darwin ;;
*)
	echo "unsupported system: $(uname -s) (on Windows, download geass-cli-windows-amd64.exe from the release page)" >&2
	exit 1
	;;
esac

case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*)
	echo "unsupported architecture: $(uname -m)" >&2
	exit 1
	;;
esac

NAME="geass-cli-${OS}-${ARCH}"

if [ -n "${GEASS_DOWNLOAD_BASE:-}" ]; then
	BASE="${GEASS_DOWNLOAD_BASE%/}"
elif [ "$VERSION" = "latest" ]; then
	BASE="https://github.com/${REPO}/releases/latest/download"
else
	BASE="https://github.com/${REPO}/releases/download/${VERSION}"
fi

if [ -n "${GEASS_INSTALL_DIR:-}" ]; then
	DIR="$GEASS_INSTALL_DIR"
elif [ -w /usr/local/bin ]; then
	DIR=/usr/local/bin
else
	DIR="$HOME/.local/bin"
fi
mkdir -p "$DIR"

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

TMP="$(mktemp)"
SUMS="$(mktemp)"
trap 'rm -f "$TMP" "$SUMS"' EXIT

echo "Downloading ${BASE}/${NAME}"
# -f fails on an HTTP error instead of saving the error page, -S still prints why curl failed.
if ! curl -fsSL "${BASE}/${NAME}" -o "$TMP" || ! curl -fsSL "${BASE}/SHA256SUMS" -o "$SUMS"; then
	echo "Could not download ${BASE}/${NAME}" >&2
	echo "Check that the release exists (GEASS_VERSION is ${VERSION}) and has ${NAME} and SHA256SUMS files," >&2
	echo "and that this machine can reach github.com over HTTPS." >&2
	exit 1
fi
verify "$TMP" "$NAME" "$SUMS"
echo "Checked ${NAME} against SHA256SUMS"

chmod 755 "$TMP"
mv "$TMP" "$DIR/geass"

echo "Installed $DIR/geass"
case ":$PATH:" in
*":$DIR:"*) ;;
*) echo "Add $DIR to your PATH to run it." ;;
esac
echo "Next: geass login --url https://your-geass-address"
