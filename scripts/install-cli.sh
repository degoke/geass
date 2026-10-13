#!/usr/bin/env sh
# Installs the geass command line.
#
#   curl -sfL https://github.com/degoke/geass/releases/latest/download/install-cli.sh | sh
#
# GEASS_VERSION picks a release (default: latest), GEASS_INSTALL_DIR where to put it (default:
# /usr/local/bin when writable, otherwise ~/.local/bin), GEASS_REPO another fork.
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

if [ "$VERSION" = "latest" ]; then
	URL="https://github.com/${REPO}/releases/latest/download/geass-cli-${OS}-${ARCH}"
else
	URL="https://github.com/${REPO}/releases/download/${VERSION}/geass-cli-${OS}-${ARCH}"
fi

if [ -n "${GEASS_INSTALL_DIR:-}" ]; then
	DIR="$GEASS_INSTALL_DIR"
elif [ -w /usr/local/bin ]; then
	DIR=/usr/local/bin
else
	DIR="$HOME/.local/bin"
fi
mkdir -p "$DIR"

TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

echo "Downloading ${URL}"
# -f fails on an HTTP error instead of saving the error page, -S still prints why curl failed.
if ! curl -fsSL "$URL" -o "$TMP"; then
	echo "Could not download ${URL}" >&2
	echo "Check that the release exists (GEASS_VERSION is ${VERSION}) and has a geass-cli-${OS}-${ARCH} file," >&2
	echo "and that this machine can reach github.com over HTTPS." >&2
	exit 1
fi
chmod +x "$TMP"
mv "$TMP" "$DIR/geass"

echo "Installed $DIR/geass"
case ":$PATH:" in
*":$DIR:"*) ;;
*) echo "Add $DIR to your PATH to run it." ;;
esac
echo "Next: geass login --url https://your-geass-address"
