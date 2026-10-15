#!/usr/bin/env bash
# Removes the k3s that hack/k3s-up.sh started.
set -euo pipefail
NAME="${K3S_NAME:-geass-k3s}"
docker rm -fv "$NAME" >/dev/null 2>&1 || true
rm -rf "${K3S_DIR:-${TMPDIR:-/tmp}/${NAME}}"
echo "Removed $NAME" >&2
