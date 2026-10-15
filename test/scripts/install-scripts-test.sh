#!/usr/bin/env sh
# Tests scripts/install.sh and scripts/install-cli.sh against a release laid out in a directory (GEASS_DOWNLOAD_BASE
# with a file:// address), so nothing is downloaded and nothing is installed outside a temporary directory.
#
#   sh test/scripts/install-scripts-test.sh
set -eu

here="$(cd "$(dirname "$0")" && pwd)"
scripts="$here/../../scripts"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) echo "SKIP: unsupported architecture $(uname -m)"; exit 0 ;;
esac
case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) echo "SKIP: unsupported system $(uname -s)"; exit 0 ;;
esac

failures=0
pass() { echo "ok   - $1"; }
fail() { echo "FAIL - $1"; failures=$((failures + 1)); }

sha() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

# A release: a fake command line, a fake installer, and the checksums that list them.
release="$work/release"
mkdir -p "$release"
printf '#!/bin/sh\necho "fake geass cli $*"\n' >"$release/geass-cli-$os-$arch"
# shellcheck disable=SC2016 # the fake installer's own script, so its $* is meant to stay unexpanded here
printf '#!/bin/sh\necho "fake installer got: $*"\nexit "${FAKE_INSTALLER_EXIT:-0}"\n' >"$release/geass-linux-$arch"
(cd "$release" && sha geass-cli-* geass-linux-* >SHA256SUMS)
base="file://$release"

# --- the two scripts carry the same checksum check, because each has to stand alone (curl ... | sh): they must not drift
verify_of() { awk '/^verify\(\) \{/,/^\}/' "$1"; }
if [ -n "$(verify_of "$scripts/install.sh")" ] && [ "$(verify_of "$scripts/install.sh")" = "$(verify_of "$scripts/install-cli.sh")" ]; then
	pass "install.sh and install-cli.sh have the same verify function"
else
	fail "verify() differs between install.sh and install-cli.sh: change both"
fi

# --- install-cli.sh
dir="$work/bin1"
if out="$(GEASS_DOWNLOAD_BASE="$base" GEASS_INSTALL_DIR="$dir" sh "$scripts/install-cli.sh" 2>&1)"; then
	[ -x "$dir/geass" ] && [ "$("$dir/geass" hi)" = "fake geass cli hi" ] && pass "install-cli.sh installs a checked binary" || fail "install-cli.sh installed a binary that does not run"
	case "$out" in *"Checked geass-cli-$os-$arch against SHA256SUMS"*) pass "install-cli.sh says it checked" ;; *) fail "install-cli.sh did not say it checked: $out" ;; esac
else
	fail "install-cli.sh failed on a good release: $out"
fi

# A binary that was changed after the checksums were written is refused, and nothing is installed.
tampered="$work/tampered"
cp -R "$release" "$tampered"
printf '# changed\n' >>"$tampered/geass-cli-$os-$arch"
dir="$work/bin2"
if out="$(GEASS_DOWNLOAD_BASE="file://$tampered" GEASS_INSTALL_DIR="$dir" sh "$scripts/install-cli.sh" 2>&1)"; then
	fail "install-cli.sh accepted a binary that does not match SHA256SUMS"
else
	[ ! -e "$dir/geass" ] && pass "install-cli.sh refuses a mismatch and installs nothing" || fail "install-cli.sh left a file after a mismatch"
	case "$out" in *"does not match SHA256SUMS"*) pass "install-cli.sh names the mismatch" ;; *) fail "install-cli.sh gave no reason for the mismatch: $out" ;; esac
fi

# No checksums at all is refused too: a download that cannot be checked is not installed.
nosums="$work/nosums"
cp -R "$release" "$nosums"
rm "$nosums/SHA256SUMS"
dir="$work/bin3"
if GEASS_DOWNLOAD_BASE="file://$nosums" GEASS_INSTALL_DIR="$dir" sh "$scripts/install-cli.sh" >/dev/null 2>&1; then
	fail "install-cli.sh installed without a SHA256SUMS file"
else
	[ ! -e "$dir/geass" ] && pass "install-cli.sh refuses a release with no SHA256SUMS" || fail "install-cli.sh left a file without checksums"
fi

# A file the checksums do not list is refused.
unlisted="$work/unlisted"
cp -R "$release" "$unlisted"
grep -v "geass-cli-" "$release/SHA256SUMS" >"$unlisted/SHA256SUMS"
dir="$work/bin4"
if GEASS_DOWNLOAD_BASE="file://$unlisted" GEASS_INSTALL_DIR="$dir" sh "$scripts/install-cli.sh" >/dev/null 2>&1; then
	fail "install-cli.sh installed a file SHA256SUMS does not list"
else
	pass "install-cli.sh refuses a file SHA256SUMS does not list"
fi

# --- install.sh (Linux only: it installs a server)
if [ "$os" = linux ]; then
	if out="$(GEASS_DOWNLOAD_BASE="$base" sh "$scripts/install.sh" --reinstall --yes 2>&1)"; then
		case "$out" in *"fake installer got: --reinstall --yes"*) pass "install.sh runs the checked installer and passes its arguments on" ;; *) fail "install.sh did not pass its arguments on: $out" ;; esac
	else
		fail "install.sh failed on a good release: $out"
	fi

	if FAKE_INSTALLER_EXIT=7 GEASS_DOWNLOAD_BASE="$base" sh "$scripts/install.sh" >/dev/null 2>&1; then
		fail "install.sh hid the installer's failure"
	else
		code=$?
		[ "$code" = 7 ] && pass "install.sh exits with the installer's status" || fail "install.sh exited $code, not the installer's 7"
	fi

	printf '# changed\n' >>"$tampered/geass-linux-$arch"
	if out="$(GEASS_DOWNLOAD_BASE="file://$tampered" sh "$scripts/install.sh" 2>&1)"; then
		fail "install.sh ran an installer that does not match SHA256SUMS: $out"
	else
		case "$out" in *"fake installer"*) fail "install.sh ran the installer before the check passed" ;; *) pass "install.sh refuses a mismatch and runs nothing" ;; esac
	fi
fi

if [ "$failures" -ne 0 ]; then
	echo "$failures check(s) failed"
	exit 1
fi
echo "all install script checks passed"
