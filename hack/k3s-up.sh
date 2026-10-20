#!/usr/bin/env bash
# Starts a throwaway k3s in Docker and installs Geass's CRDs into it, so the k3s integration tests have a real
# k3s to run against: its Helm controller (which installs the add-ons), its Traefik, and a current Kubernetes.
# It does not touch your own kubeconfig. Prints the path of the kubeconfig it wrote.
# K3s starts with the configuration file the installer writes (hack/k3sconfig prints it from pkg/installer): secrets
# encryption and log rotation. So the tests run on a cluster set up the way a real one is, and a change to what the installer
# writes reaches them. A k3s that is already running is reused as it is: after a change, remove it first with hack/k3s-down.sh.
#
#   hack/k3s-up.sh                     # starts it, prints "export KUBECONFIG=..."
#   eval "$(hack/k3s-up.sh)"           # and uses it in this shell
#   hack/k3s-down.sh                   # removes it
set -euo pipefail

NAME="${K3S_NAME:-geass-k3s}"
IMAGE="${K3S_IMAGE:-rancher/k3s:v1.31.5-k3s1}"
API_PORT="${K3S_API_PORT:-6444}"
HTTP_PORT="${K3S_HTTP_PORT:-18080}"
DIR="${K3S_DIR:-${TMPDIR:-/tmp}/${NAME}}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

mkdir -p "$DIR"
(cd "$ROOT" && go run ./hack/k3sconfig) > "$DIR/config.yaml"
if ! docker ps --format '{{.Names}}' | grep -qx "$NAME"; then
  docker rm -fv "$NAME" >/dev/null 2>&1 || true
  docker run -d --name "$NAME" --privileged --tmpfs /run --tmpfs /var/run \
    -p "127.0.0.1:${API_PORT}:6443" -p "127.0.0.1:${HTTP_PORT}:80" \
    -v "$DIR/config.yaml:/etc/rancher/k3s/config.yaml:ro" \
    -e K3S_KUBECONFIG_MODE=644 "$IMAGE" server --tls-san 127.0.0.1 >/dev/null
fi

echo "Waiting for k3s to be ready..." >&2
for _ in $(seq 1 60); do
  docker exec "$NAME" kubectl get nodes 2>/dev/null | grep -q ' Ready' && break
  sleep 3
done
docker exec "$NAME" kubectl get nodes 2>/dev/null | grep -q ' Ready' || { echo "k3s did not become ready" >&2; exit 1; }

KUBECONFIG_FILE="$DIR/kubeconfig"
docker exec "$NAME" cat /etc/rancher/k3s/k3s.yaml | sed "s#https://127.0.0.1:6443#https://127.0.0.1:${API_PORT}#" > "$KUBECONFIG_FILE"

echo "Waiting for k3s's Helm controller and Traefik..." >&2
for _ in $(seq 1 60); do
  KUBECONFIG="$KUBECONFIG_FILE" kubectl -n kube-system get pods 2>/dev/null | grep -q 'traefik.* Running' && break
  sleep 3
done

KUBECONFIG="$KUBECONFIG_FILE" kubectl apply --server-side --force-conflicts -f <(
  cd "$ROOT" && KUBECONFIG="$KUBECONFIG_FILE" kubectl kustomize config/crd
) >&2

echo "export KUBECONFIG=${KUBECONFIG_FILE}"
echo "export K3S_HTTP_ADDR=127.0.0.1:${HTTP_PORT}"
echo "export K3S_INTEGRATION=true"
