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

ARCH="$(detect_arch)"

if [ "$VERSION" = "latest" ]; then
	URL="https://github.com/${REPO}/releases/latest/download/geass-linux-${ARCH}"
else
	URL="https://github.com/${REPO}/releases/download/${VERSION}/geass-linux-${ARCH}"
fi

TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT

echo "Downloading Geass from ${URL}"
curl -sfL "$URL" -o "$TMP"
chmod +x "$TMP"
exec "$TMP"
