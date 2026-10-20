#!/usr/bin/env bash
# Builds a three-server K3s cluster in Docker, the way `geass --join-server` makes one, runs the operator on it, and
# takes down the server the operator runs on. It checks what the cluster promises when it has three control plane
# servers:
#
#   - the API keeps answering from the servers that are left, and the cluster's data is still there;
#   - the operator starts on another server, because its image is on all of them, and takes over;
#   - the lost server comes back and rejoins;
#   - a Secret written through the first server, which made the encryption key, is read by the servers that joined, by
#     the servers that are left when the first is lost, and by a server that was restarted;
#   - with two of three servers gone the cluster stops, which is what a majority means.
#
# It does not run the installer (a container has no systemd, and the installer is for a machine), so the servers are
# started with the configuration the installer writes: hack/k3sconfig prints the first server's file, and the second and
# third servers get it with `server` and `token` added, as the installer's join does. That includes secrets encryption, so
# the checks also cover what it adds to a cluster of three: the key reaching the servers that joined, and Secrets that stay
# readable when the server that made it is lost. What else the installer does is covered by its own unit tests.
#
#   hack/k3s-ha-test.sh
#
# It needs Docker, Go (to build the operator) and the rancher/k3s image. It removes everything it made.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
K3S_IMAGE="${K3S_IMAGE:-rancher/k3s:v1.31.5-k3s1}"
PREFIX="${HA_PREFIX:-geass-ha}"
NETWORK="${PREFIX}-net"
# Fixed addresses: a container that is stopped and started gets a new one from Docker, and a server whose address changed
# is not the etcd member it was. A real machine keeps its address.
SUBNET="${HA_SUBNET:-172.29.77.0/24}"
address() { echo "${SUBNET%.0/24}.1$1"; }
OPERATOR_IMAGE="${PREFIX}-operator:dev"
WORK="$(mktemp -d)"
SERVERS=(1 2 3)

# Every K3s server in a container takes about 30 of the host's inotify instances (the kubelet, containerd and cAdvisor
# each watch files), and a user may have 128 by default. Where there are fewer than 256, the third server runs as an etcd
# and API server member without a kubelet (--disable-agent): it votes in the cluster's quorum, which is what is being
# tested, and it needs none of that. Set HA_THIRD_ETCD_ONLY=0 to insist on three full servers, or =1 to force this.
INOTIFY="$(cat /proc/sys/fs/inotify/max_user_instances 2>/dev/null || echo 0)"
if [ -z "${HA_THIRD_ETCD_ONLY:-}" ]; then
  if [ "$INOTIFY" -lt 256 ]; then HA_THIRD_ETCD_ONLY=1; else HA_THIRD_ETCD_ONLY=0; fi
fi
if [ "$HA_THIRD_ETCD_ONLY" = 1 ]; then EXPECT_NODES=2; else EXPECT_NODES=3; fi

pass=0
fail=0
log() { printf '\n== %s\n' "$*" >&2; }
check() { # check "what" command...
  local what="$1"; shift
  if "$@" >/dev/null 2>&1; then printf '  ok   %s\n' "$what" >&2; pass=$((pass + 1)); else printf '  FAIL %s\n' "$what" >&2; fail=$((fail + 1)); fi
}
name() { echo "${PREFIX}-$1"; }
kc() { local n="$1"; shift; docker exec "$(name "$n")" kubectl "$@"; }

# What K3s says about secrets encryption on a server. The output is read into a variable first: `grep -q` can end a pipe
# early, which `pipefail` would then count as a failure.
encryption_status() { docker exec "$(name "$1")" k3s secrets-encrypt status 2>&1; }
encryption_on() { local out; out="$(encryption_status "$1")" || return 1; grep -q 'Encryption Status: Enabled' <<<"$out"; }
encryption_agrees() { local out; out="$(encryption_status "$1")" || return 1; grep -q 'All hashes match' <<<"$out"; }
secret_value() { kc "$1" get secret survives-secret -n default -o jsonpath='{.data.value}' | base64 -d; }

cleanup() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    for n in "${SERVERS[@]}"; do
      if docker inspect "$(name "$n")" >/dev/null 2>&1; then
        echo "---- $(name "$n"): $(docker inspect -f '{{.State.Status}} (exit {{.State.ExitCode}})' "$(name "$n")"), last lines of its log" >&2
        docker logs --tail 8 "$(name "$n")" 2>&1 | cut -c1-220 >&2
      fi
    done
  fi
  if [ -n "${HA_KEEP:-}" ]; then echo "HA_KEEP is set: the servers (${PREFIX}-1..3) and their network are left for you to look at" >&2; rm -rf "$WORK"; return; fi
  for n in "${SERVERS[@]}"; do docker rm -fv "$(name "$n")" >/dev/null 2>&1 || true; done
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
  docker rmi "$OPERATOR_IMAGE" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

# Every container is removed with its volumes (rm -fv): the K3s image declares four anonymous ones, which a plain `docker rm`
# leaves behind at a few gigabytes a container.
# K3s stops its own process when its database has no majority, and systemd starts it again, which is what a real server does and
# what --restart unless-stopped is for. A server stopped with `docker stop` stays stopped.
start_server() { # start_server N [extra docker args...] -- k3s args...
  local n="$1"; shift
  local docker_args=()
  while [ "$1" != "--" ]; do docker_args+=("$1"); shift; done
  shift
  docker run -d --name "$(name "$n")" --hostname "$(name "$n")" --network "$NETWORK" --ip "$(address "$n")" --restart unless-stopped --privileged --tmpfs /run --tmpfs /var/run \
    "${docker_args[@]}" "$K3S_IMAGE" "$@" >/dev/null
}

# start_again brings stopped servers back, and starts one again if it exits. On a host that is close to its inotify limit
# (see above) a server that is started can die in its first seconds, because the host has none left for it at that moment.
start_again() { # start_again N...
  local n attempt
  for n in "$@"; do
    for attempt in 1 2 3 4 5; do
      docker start "$(name "$n")" >/dev/null
      sleep 20
      if [ "$(docker inspect -f '{{.State.Running}}' "$(name "$n")")" = true ]; then break; fi
      echo "  $(name "$n") exited right after it started (attempt $attempt); starting it again" >&2
    done
  done
}

wait_for() { # wait_for "what" seconds command...
  local what="$1" seconds="$2"; shift 2
  local deadline=$((SECONDS + seconds))
  until "$@" >/dev/null 2>&1; do
    if [ "$SECONDS" -ge "$deadline" ]; then echo "timed out waiting for $what" >&2; return 1; fi
    sleep 2
  done
}

nodes_ready() { # nodes_ready <via server> <count>
  [ "$(kc "$1" get nodes --no-headers 2>/dev/null | grep -c ' Ready')" -ge "$2" ]
}

# The operator's pods that are running and ready. A pod that was on a server that is gone stays in the list as Terminating
# until that server comes back, so it is not counted.
operator_pods() { # operator_pods <via server>: "node" for each running, ready pod
  kc "$1" -n geass-system get pods -l control-plane=controller-manager --no-headers -o wide 2>/dev/null |
    awk '$2 == "1/1" && $3 == "Running" {print $7}'
}

operator_node() { operator_pods "$1" | head -1; }

operator_ready() { # operator_ready <via server> <not on node>
  local node
  node="$(operator_node "$1")"
  [ -n "$node" ] && [ "$node" != "${2:-}" ]
}

log "Building the operator image"
mkdir -p "$WORK/img"
(cd "$ROOT" && CGO_ENABLED=0 GOOS=linux go build -o "$WORK/img/manager" ./cmd/main.go)
printf 'FROM gcr.io/distroless/static:nonroot\nCOPY manager /manager\nUSER 65532:65532\nENTRYPOINT ["/manager"]\n' > "$WORK/img/Dockerfile"
docker build -q -t "$OPERATOR_IMAGE" "$WORK/img" >/dev/null

log "Starting the first server (K3s with embedded etcd and the configuration file the installer writes)"
SERVER_SETTINGS="$WORK/server-settings.yaml"
(cd "$ROOT" && go run ./hack/k3sconfig) > "$SERVER_SETTINGS"
docker network create --subnet "$SUBNET" "$NETWORK" >/dev/null
start_server 1 -v "$SERVER_SETTINGS:/etc/rancher/k3s/config.yaml:ro" -- server --cluster-init --tls-san "$(name 1)" --write-kubeconfig-mode 644
wait_for "the first server" 180 nodes_ready 1 1
TOKEN="$(docker exec "$(name 1)" cat /var/lib/rancher/k3s/server/token)"

log "Joining two more servers with the file the installer writes (the first server's settings, server and token)"
for n in 2 3; do
  agent_args=()
  if [ "$n" = 3 ] && [ "$HA_THIRD_ETCD_ONLY" = 1 ]; then
    agent_args=(--disable-agent)
    echo "  server 3 runs without a kubelet: this host allows $INOTIFY inotify instances per user, too few for a third kubelet" >&2
  fi
  { cat "$SERVER_SETTINGS"; printf 'server: https://%s:6443\ntoken: %s\n' "$(name 1)" "$TOKEN"; } > "$WORK/config-$n.yaml"
  chmod 600 "$WORK/config-$n.yaml"
  start_server "$n" -v "$WORK/config-$n.yaml:/etc/rancher/k3s/config.yaml:ro" -- server --write-kubeconfig-mode 644 "${agent_args[@]}"
  if [ "${#agent_args[@]}" -gt 0 ]; then
    wait_for "server $n to answer" 240 docker exec "$(name "$n")" kubectl get --raw /readyz
  else
    wait_for "server $n to join" 240 nodes_ready 1 "$n"
  fi
done
check "the servers that run pods are ready" nodes_ready 1 "$EXPECT_NODES"
check "they are control plane nodes holding etcd" \
  test "$(kc 1 get nodes -l node-role.kubernetes.io/control-plane,node-role.kubernetes.io/etcd --no-headers | wc -l)" = "$EXPECT_NODES"
check "the third server answers the API with the cluster's data" docker exec "$(name 3)" kubectl get nodes

log "Putting the operator's image on every server, as the installer does, and starting the operator"
for n in "${SERVERS[@]}"; do
  if [ "$n" = 3 ] && [ "$HA_THIRD_ETCD_ONLY" = 1 ]; then continue; fi
  docker save "$OPERATOR_IMAGE" | docker exec -i "$(name "$n")" ctr images import - >/dev/null
done
sed "s#image: controller:latest#image: ${OPERATOR_IMAGE}#" "$ROOT/dist/install.yaml" | docker exec -i "$(name 1)" kubectl apply --server-side --force-conflicts -f - >/dev/null
docker exec -i "$(name 1)" kubectl apply -f - >/dev/null <<EOF
apiVersion: v1
kind: Secret
metadata: {name: geass-dashboard-auth, namespace: geass-system}
type: Opaque
stringData:
  users: '[{"username":"admin","password":"ha-test-password-not-used","role":"admin"}]'
EOF
wait_for "the operator" 240 operator_ready 1 ""
FIRST_NODE="$(operator_node 1)"
echo "  the operator runs on ${FIRST_NODE}" >&2

# Data that has to survive: an object only the cluster's database holds.
kc 1 create configmap survives -n default --from-literal=before=loss >/dev/null
# A Secret written through server 1, which made the encryption key, for the servers that joined to read.
kc 1 create secret generic survives-secret -n default --from-literal=value=encrypted-before-loss >/dev/null
check "secrets encryption is on, on the first server" encryption_on 1
check "and the servers agree on it" encryption_agrees 1
for n in 2 3; do
  check "server $n reads a Secret that server 1 wrote" test "$(secret_value "$n")" = encrypted-before-loss
done

log "A server that is away for a moment rejoins and catches up: server 3 is stopped, the cluster is written to, and it comes back"
docker stop -t 0 "$(name 3)" >/dev/null
check "the cluster keeps answering with one of three servers down" wait_for "the API" 60 kc 1 get nodes
check "and accepts a write" kc 1 create configmap while-away -n default --from-literal=written=without-the-third
start_again 3
check "server 3 rejoins and answers the API" wait_for "server 3" 180 docker exec "$(name 3)" kubectl get --raw /readyz
check "it has what was written while it was away" \
  wait_for "the write that was made without it" 120 docker exec "$(name 3)" kubectl get configmap while-away -n default
check "and it still reads the Secret, after the restart" test "$(secret_value 3)" = encrypted-before-loss

log "Stopping ${FIRST_NODE}, the server the operator runs on"
LOST="${FIRST_NODE#"${PREFIX}-"}"
VIA=1
[ "$LOST" = "1" ] && VIA=2
STOPPED_AT=$SECONDS
docker stop -t 0 "$(name "$LOST")" >/dev/null
check "the API answers from a server that is left" wait_for "the API" 60 kc "$VIA" get nodes
check "the cluster's data is still there" test "$(kc "$VIA" get configmap survives -n default -o jsonpath='{.data.before}')" = "loss"
check "a Secret written before the loss is still readable from a server that is left" test "$(secret_value "$VIA")" = encrypted-before-loss
check "a write is accepted by the two servers that are left" kc "$VIA" create configmap after-loss -n default --from-literal=during=outage
echo "  the API answered ${SECONDS}s into the outage window (started at ${STOPPED_AT}s)" >&2

log "Waiting for the operator to start on another server"
if wait_for "the operator on another server" 300 operator_ready "$VIA" "$FIRST_NODE"; then
  echo "  the operator is ready again on $(operator_node "$VIA"), $((SECONDS - STOPPED_AT))s after the server was stopped" >&2
  pass=$((pass + 1)); echo "  ok   the operator is running on another server" >&2
else
  fail=$((fail + 1)); echo "  FAIL the operator is running on another server" >&2
fi
check "the operator holds the leader lock again" \
  test -n "$(kc "$VIA" -n geass-system get lease -o jsonpath='{.items[*].spec.holderIdentity}')"

# The server that was lost is left down for the rest of the test. On a host that is close to its inotify limit (see above) a
# server with a kubelet that is stopped cannot always be started again. What is stopped and started from here on is server 3.
log "Without a majority: the lost server is still down and server 3 is stopped too, so one of three is left"
docker stop -t 0 "$(name 3)" >/dev/null
sleep 20
if docker exec "$(name "$VIA")" timeout 15 kubectl get nodes >/dev/null 2>&1; then
  fail=$((fail + 1)); echo "  FAIL the cluster stopped answering without a majority" >&2
else
  pass=$((pass + 1)); echo "  ok   the cluster stops answering without a majority, which is what a majority means" >&2
fi
start_again 3
check "and recovers when the second server returns" wait_for "the majority" 300 kc "$VIA" get nodes

log "Removing a server for good: its node is deleted, from another server, while the others hold the majority"
kc "$VIA" delete node "${FIRST_NODE}" >/dev/null
check "the cluster keeps answering with the server gone" wait_for "the API" 60 kc "$VIA" get nodes
# The log is read into a variable first: `grep -q` stops at the first match, and the pipe closing under `docker logs` fails the
# whole pipeline when pipefail is on, which reports a match as a miss.
member_removed() {
  local logs
  logs="$(docker logs "$(name "$VIA")" 2>&1)"
  grep -qiE 'removing etcd member|removed member' <<<"$logs"
}
check "K3s took it out of etcd's membership" wait_for "the member to be removed" 90 member_removed

log "Result: ${pass} passed, ${fail} failed"
[ "$fail" -eq 0 ]
