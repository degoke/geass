#!/usr/bin/env bash
# Loses a control plane's disk, and rebuilds the cluster from the copy in S3 that K3s sent before. It is the test of
# `etcd-snapshots` and of the runbook in the documentation (operations/disaster-recovery): the commands under "restore"
# below are the commands that the runbook gives, and what it checks is what the runbook promises.
#
#   - K3s, told to send snapshots by the file the installer writes (hack/etcdsnapdropin prints it), sends them to a bucket
#     whose access key can use that bucket and nothing else (the policy that the documentation gives), including a snapshot
#     big enough to be uploaded in parts, and its retention keeps to the number that file sets;
#   - the operator keeps the Secret that K3s reads in step with the cluster, and says when copies are arriving
#     (test/k3sintegration/etcdsnapshots_test.go, before the wipe);
#   - with the machine gone, a new one restores the cluster from the bucket with only the bucket's key and the server token:
#     what the cluster held is back, and so are the settings, so copies go on arriving with nothing to set up again;
#   - the token matters: a restore with another one does not give the cluster back.
#
# It runs K3s as an etcd and API server only (--disable-agent): the host's limit on inotify instances is a few dozen too few
# for a second kubelet, and the control plane's data is what is being restored. What a worker does afterwards is not tested.
#
#   hack/k3s-restore-test.sh
#
# It needs Docker, Go, kubectl, and the K3s and MinIO images. It removes everything it made (HA_KEEP=1 keeps it).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
K3S_IMAGE="${K3S_IMAGE:-rancher/k3s:v1.36.5-k3s1}"
MINIO_IMAGE="${MINIO_IMAGE:-quay.io/minio/minio:RELEASE.2024-12-18T13-15-44Z}"
MC_IMAGE="${MC_IMAGE:-quay.io/minio/mc:RELEASE.2024-11-21T17-21-54Z}"
PREFIX="${DR_PREFIX:-geass-dr}"
NETWORK="${PREFIX}-net"
SUBNET="${DR_SUBNET:-172.29.88.0/24}"
address() { echo "${SUBNET%.0/24}.$1"; }
MINIO_IP="$(address 2)"
OLD_IP="$(address 10)"
NEW_IP="$(address 11)"
API_PORT="${DR_API_PORT:-6445}"
BUCKET="geass-dr-state"
ENDPOINT="${MINIO_IP}:9000"
KEY_ID="stateuser"
KEY_SECRET="state-user-secret-1"
ROOT_USER="drroot"
ROOT_PASSWORD="drroot-password-1"
# K3s keeps this many, which is what the test overrides the installer's 14 with to see the number in effect.
TEST_RETENTION=3
WORK="$(mktemp -d)"
DROPIN="$WORK/dropin"
KUBECONFIG_FILE="$WORK/kubeconfig"
export KUBECONFIG="$KUBECONFIG_FILE"

pass=0
fail=0
log() { printf '\n== %s\n' "$*" >&2; }
check() { # check "what" command...
  local what="$1"; shift
  if "$@" >/dev/null 2>&1; then printf '  ok   %s\n' "$what" >&2; pass=$((pass + 1)); else printf '  FAIL %s\n' "$what" >&2; fail=$((fail + 1)); fi
}
name() { echo "${PREFIX}-$1"; }
mc() { docker run --rm --network "$NETWORK" -v "$WORK:/work" --entrypoint /bin/sh "$MC_IMAGE" -c "$1"; }

cleanup() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    for n in old reset new minio; do
      if docker inspect "$(name "$n")" >/dev/null 2>&1; then
        echo "---- $(name "$n"): $(docker inspect -f '{{.State.Status}} (exit {{.State.ExitCode}})' "$(name "$n")"), last lines of its log" >&2
        docker logs --tail 12 "$(name "$n")" 2>&1 | cut -c1-240 >&2
      fi
    done
  fi
  if [ -n "${HA_KEEP:-}" ]; then echo "HA_KEEP is set: ${PREFIX}-* and their network are left for you to look at (work directory $WORK)" >&2; return; fi
  # The new server shares the reset's volumes: it goes first and without -v, so the volumes go once, with the reset.
  docker rm -f "$(name new)" >/dev/null 2>&1 || true
  for n in old reset badtoken minio; do docker rm -fv "$(name "$n")" >/dev/null 2>&1 || true; done
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

wait_api() { # wait_api CONTAINER: until the API server answers, through the published port
  local ready=
  for _ in $(seq 1 60); do
    if docker exec "$1" kubectl get --raw /readyz >/dev/null 2>&1; then ready=1; break; fi
    sleep 3
  done
  [ -n "$ready" ] || { echo "the API server of $1 did not answer" >&2; return 1; }
  docker exec "$1" cat /etc/rancher/k3s/k3s.yaml | sed "s#https://127.0.0.1:6443#https://127.0.0.1:${API_PORT}#" > "$KUBECONFIG_FILE"
  for _ in $(seq 1 30); do kubectl get --raw /readyz >/dev/null 2>&1 && return 0; sleep 2; done
  echo "the API server of $1 does not answer on the published port" >&2
  return 1
}

# What a K3s that was set up with `etcd-snapshots enable` runs with: the installer's file, from the code. The second file is
# the test's, and is read after it: a snapshot every minute so that the test does not wait half a day, and a retention small
# enough to be seen at work.
mkdir -p "$DROPIN"
(cd "$ROOT" && go run ./hack/etcdsnapdropin) > "$DROPIN/50-geass-etcd-snapshots.yaml"
cat > "$DROPIN/60-test.yaml" <<EOF
etcd-snapshot-schedule-cron: "* * * * *"
etcd-snapshot-retention: ${TEST_RETENTION}
EOF
(cd "$ROOT" && go run ./hack/etcdsnapdropin --policy "$BUCKET") > "$WORK/policy.json"

# The Go test of the operator's side, in one of its two phases. Its output is shown indented, and whether it passed is returned.
run_phase() { # run_phase before|after [VAR=value...]
  local phase="$1"; shift
  local rc=0
  (cd "$ROOT" && env DR_PHASE="$phase" DR_ENDPOINT="$ENDPOINT" DR_BUCKET="$BUCKET" DR_ACCESS_KEY="$KEY_ID" DR_SECRET_KEY="$KEY_SECRET" \
    K3S_INTEGRATION=true "$@" go test -tags=k3sintegration ./test/k3sintegration/ -run TestEtcdSnapshotsOnK3s -count=1 -v -timeout 15m) \
    >"$WORK/go-$phase.log" 2>&1 || rc=$?
  sed 's/^/  | /' "$WORK/go-$phase.log" >&2
  return "$rc"
}

# phase_ok "what" before|after [VAR=value...]: like check, for a Go phase, whose output is shown and not thrown away
phase_ok() {
  local what="$1"; shift
  if run_phase "$@"; then printf '  ok   %s\n' "$what" >&2; pass=$((pass + 1)); else printf '  FAIL %s\n' "$what" >&2; fail=$((fail + 1)); fi
}

# check_within SECONDS "what" command...: until it is true, or it is not
check_within() {
  local seconds="$1" what="$2"; shift 2
  local deadline=$((SECONDS + seconds))
  until "$@" >/dev/null 2>&1; do
    if [ "$SECONDS" -ge "$deadline" ]; then printf '  FAIL %s\n' "$what" >&2; fail=$((fail + 1)); return 0; fi
    sleep 5
  done
  printf '  ok   %s\n' "$what" >&2; pass=$((pass + 1))
}

start_k3s() { # start_k3s NAME IP [docker args...] -- k3s args...
  local n="$1" ip="$2"; shift 2
  local docker_args=()
  while [ "$1" != "--" ]; do docker_args+=("$1"); shift; done
  shift
  docker run -d --name "$(name "$n")" --hostname "$(name "$n")" --network "$NETWORK" --ip "$ip" --privileged --tmpfs /run --tmpfs /var/run \
    -e K3S_KUBECONFIG_MODE=644 "${docker_args[@]}" "$K3S_IMAGE" "$@" >/dev/null
}

# What a restore is told: where the copies are, with the bucket's key, and the old server token. They go in a configuration file
# that is read for this one run, as the runbook says, and not on the command line, where the process list shows them. K3s cannot use
# the Secret that the cluster keeps for this: the cluster is what is being restored.
restore_config() { # restore_config DIR TOKEN
  mkdir -p "$1"
  {
    echo 'etcd-s3: true'
    echo "etcd-s3-endpoint: \"${ENDPOINT}\""
    echo "etcd-s3-bucket: \"${BUCKET}\""
    echo 'etcd-s3-folder: "geass"'
    echo "etcd-s3-access-key: \"${KEY_ID}\""
    echo "etcd-s3-secret-key: \"${KEY_SECRET}\""
    echo 'etcd-s3-insecure: true'
    echo "token: \"$2\""
  } > "$1/90-geass-restore.yaml"
}

log "a bucket, and an access key that can use it and nothing else"
docker network create --subnet "$SUBNET" "$NETWORK" >/dev/null
docker run -d --name "$(name minio)" --network "$NETWORK" --ip "$MINIO_IP" -e MINIO_ROOT_USER="$ROOT_USER" -e MINIO_ROOT_PASSWORD="$ROOT_PASSWORD" \
  "$MINIO_IMAGE" server /data >/dev/null
for _ in $(seq 1 30); do
  mc "mc alias set dr http://${ENDPOINT} ${ROOT_USER} ${ROOT_PASSWORD}" >/dev/null 2>&1 && break
  sleep 2
done
mc "mc alias set dr http://${ENDPOINT} ${ROOT_USER} ${ROOT_PASSWORD} >/dev/null && mc mb dr/${BUCKET} && mc mb dr/other-bucket \
  && mc admin policy create dr state-only /work/policy.json && mc admin user add dr ${KEY_ID} ${KEY_SECRET} \
  && mc admin policy attach dr state-only --user ${KEY_ID}" >&2
if mc "mc alias set u http://${ENDPOINT} ${KEY_ID} ${KEY_SECRET} >/dev/null && mc mb u/new-bucket" >/dev/null 2>&1; then
  printf '  FAIL the key can make a bucket\n' >&2; fail=$((fail + 1))
else
  printf '  ok   the key cannot make a bucket\n' >&2; pass=$((pass + 1))
fi
if mc "mc alias set u http://${ENDPOINT} ${KEY_ID} ${KEY_SECRET} >/dev/null && mc ls u/other-bucket" >/dev/null 2>&1; then
  printf '  FAIL the key can read another bucket\n' >&2; fail=$((fail + 1))
else
  printf '  ok   the key cannot read another bucket\n' >&2; pass=$((pass + 1))
fi

log "a control plane that sends its snapshots there, and the operator's side of it"
start_k3s old "$OLD_IP" -p "127.0.0.1:${API_PORT}:6443" -v "$DROPIN:/etc/rancher/k3s/config.yaml.d:ro" -- server --cluster-init --disable-agent --tls-san 127.0.0.1
wait_api "$(name old)"
kubectl apply --server-side --force-conflicts -f <(cd "$ROOT" && kubectl kustomize config/crd) >/dev/null
check "K3s started with the installer's file, with no Secret yet" docker exec "$(name old)" kubectl get --raw /readyz
phase_ok "the operator made K3s's settings and saw copies arrive" before

log "what the cluster holds before the disk is lost, including a snapshot big enough to be uploaded in parts"
kubectl create namespace dr-marker >/dev/null
kubectl -n dr-marker create secret generic marker --from-literal=value=before-the-disk-was-lost >/dev/null
for i in $(seq 1 24); do
  head -c 700000 /dev/urandom | base64 -w0 > "$WORK/big-$i"
  kubectl -n dr-marker create configmap "big-$i" --from-file=data="$WORK/big-$i" >/dev/null
done
check "the cluster's state is over 16 MiB" test "$(docker exec "$(name old)" sh -c 'du -sk /var/lib/rancher/k3s/server/db/etcd | cut -f1')" -gt 16000

log "the number of copies kept in the bucket is the one K3s was told, after more than that many have been taken"
scheduled() { docker exec "$(name old)" k3s etcd-snapshot ls 2>/dev/null | awk '/s3:\/\/.*\/etcd-snapshot-/ {print $1}'; }
: > "$WORK/seen"
for _ in $(seq 1 120); do
  scheduled >> "$WORK/seen"
  [ "$(sort -u "$WORK/seen" | wc -l)" -gt "$TEST_RETENTION" ] && break
  sleep 10
done
check "more than ${TEST_RETENTION} scheduled copies were taken ($(sort -u "$WORK/seen" | wc -l) seen)" test "$(sort -u "$WORK/seen" | wc -l)" -gt "$TEST_RETENTION"
check_within 120 "the bucket holds ${TEST_RETENTION} scheduled copies, the retention that was set, and not K3s's default of 5" test "$(scheduled | wc -l)" = "$TEST_RETENTION"

log "the last copy before the disk is lost"
docker exec "$(name old)" k3s etcd-snapshot save --name geass-final >/dev/null 2>&1
FINAL="$(docker exec "$(name old)" k3s etcd-snapshot ls 2>/dev/null | awk '/^geass-final/ && /s3:/ {print $1}')"
check "a snapshot of that size reached the bucket with the scoped key (named ${FINAL:-none})" test -n "$FINAL"
TOKEN="$(docker exec "$(name old)" cat /var/lib/rancher/k3s/server/token)"

log "the machine is lost: its container and every volume of it"
docker rm -fv "$(name old)" >/dev/null
check "nothing of the old control plane is left" bash -c "! docker inspect $(name old)"

log "restore: another token does not give the cluster back"
restore_config "$WORK/restore-wrong" "not-the-token"
docker run --name "$(name badtoken)" --hostname "$(name badtoken)" --network "$NETWORK" --ip "$(address 12)" --privileged --tmpfs /run --tmpfs /var/run \
  -v "$WORK/restore-wrong:/etc/rancher/k3s/config.yaml.d:ro" \
  "$K3S_IMAGE" server --cluster-init --disable-agent --cluster-reset --cluster-reset-restore-path="$FINAL" >"$WORK/badtoken.log" 2>&1 || true
# The words are the ones the documentation gives, so that whoever meets them under stress knows what they mean.
check "a restore with another token is refused (bootstrap data already found and encrypted with different token)" grep -q "encrypted with different token" "$WORK/badtoken.log"
docker rm -fv "$(name badtoken)" >/dev/null 2>&1 || true

log "restore: a new machine, the bucket's key and the server token"
# These are the commands of the runbook. K3s installed and not yet started, then the reset with the copy named, then K3s started.
restore_config "$WORK/restore" "$TOKEN"
docker run --name "$(name reset)" --hostname "$(name new)" --network "$NETWORK" --ip "$NEW_IP" --privileged --tmpfs /run --tmpfs /var/run \
  -v "$WORK/restore:/etc/rancher/k3s/config.yaml.d:ro" \
  "$K3S_IMAGE" server --cluster-init --disable-agent --cluster-reset --cluster-reset-restore-path="$FINAL" >"$WORK/reset.log" 2>&1 || true
check "K3s restored the copy from the bucket" grep -q "S3 download complete" "$WORK/reset.log"
check "and said to start it again without --cluster-reset" grep -q "restart without --cluster-reset" "$WORK/reset.log"

# Started again on the same data with the installer's file, which is what `etcd-snapshots enable` puts back. It has the same address as
# the reset ran at: K3s records it as the member's, and a different one is a member that is not in the cluster.
start_k3s new "$NEW_IP" -p "127.0.0.1:${API_PORT}:6443" --volumes-from "$(name reset)" -v "$DROPIN:/etc/rancher/k3s/config.yaml.d:ro" -- server --cluster-init --disable-agent --tls-san 127.0.0.1
wait_api "$(name new)"
check "the API server is back" kubectl get --raw /readyz
check "the token on the new machine is the old one" test "$(docker exec "$(name new)" cat /var/lib/rancher/k3s/server/token)" = "$TOKEN"
check "a secret from before is back" test "$(kubectl -n dr-marker get secret marker -o jsonpath='{.data.value}' | base64 -d)" = "before-the-disk-was-lost"
check "and the 24 big objects" test "$(kubectl -n dr-marker get configmaps -o name | grep -c big-)" = 24
check "the settings that K3s sends copies by came back with it" test "$(kubectl -n kube-system get secret geass-etcd-snapshots -o jsonpath='{.type}')" = "etcd.k3s.cattle.io/s3-config-secret"

log "the operator finds the cluster as it was, and copies arriving from the new machine"
phase_ok "the restored cluster still names the bucket and its copies arrive from the new machine" after DR_NEW_HOST="$(name new)" DR_EXPECT_COPY="$FINAL"

echo >&2
printf '%d passed, %d failed\n' "$pass" "$fail" >&2
[ "$fail" -eq 0 ]
