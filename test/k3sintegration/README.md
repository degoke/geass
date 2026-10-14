# k3s integration tests

These run the operator against a real k3s, because some things only exist there: k3s's Helm controller (which
installs the add-ons), its Traefik, and a current Kubernetes. A kind cluster has none of these.

```bash
eval "$(make -s k3s-up)"      # starts rancher/k3s in Docker, installs the CRDs, and sets KUBECONFIG
make test-k3s-serverless      # scale to zero and function schedules, end to end (about 15 minutes the first time)
make test-k3s-objectstorage   # the cluster's object storage, SeaweedFS and Garage (about 10 minutes the first time)
make test-k3s-limits          # a project's node block and its quotas, per environment and for the whole project (about 4 minutes)
make k3s-down                 # removes it
```

`make k3s-up` does not touch your own kubeconfig; it writes its own under `/tmp/geass-k3s`. Ports 6444 (API)
and 18080 (ingress) are used and can be changed with `K3S_API_PORT` and `K3S_HTTP_PORT`.

`TestServerlessFunctionsOnK3s` starts the cluster, project and app controllers in the test process, turns on the
serverless add-on, and then follows an event function through: the add-on installing itself, the function
sleeping and waking on a request, a schedule waking it and proving itself with its token, a visitor not being
able to pretend to be a schedule, and the function being put back to always running.

`TestHelmChart*` need `K3S_INTEGRATION=true` and the same cluster.

`TestObjectStorageOnK3s` does the same for the cluster's object storage, once for SeaweedFS and once for Garage.
It checks that the server comes up hardened, that its network policy is enforced (k3s enforces them, a kind cluster
does not), that a pod in a project reaches the S3 port and nothing else, that each project's app can use its own
bucket and no other, that the data and the logins survive the server's pod being replaced, that a volume is not
resized where its class cannot be, that a removed bucket takes its login with it, and that the server refuses to go
while a bucket is on it and removes everything it made when it does. The test process is outside the cluster, so it
reaches the server through a port-forward it keeps pointed at the server's current pod.

`TestProjectLimitsOnK3s` follows what a project can opt into. It first shows that a pod can reach every node's kubelet,
both at the node's address and at its address on the pod network, then turns on `network.blockNodeAddresses` and shows
that it cannot, from any node, while DNS, the Kubernetes API on the control plane and the internet still answer. It
adds and removes a node object to show that the policy follows the nodes, then sets an environment quota and shows that
a service that would go over it reports it, that raising the cap lets it start, that a hand edit is put back and that
removing the quota removes what made it. It works on a single node, but the remote node is only probed when there are
two. Add an agent to the k3s that `make k3s-up` started:

```bash
TOKEN=$(docker exec geass-k3s cat /var/lib/rancher/k3s/server/node-token)
SERVER=$(docker inspect -f '{{.NetworkSettings.IPAddress}}' geass-k3s)
docker run -d --name geass-k3s-agent --privileged --tmpfs /run --tmpfs /var/run \
  -e K3S_URL=https://$SERVER:6443 -e K3S_TOKEN=$TOKEN rancher/k3s:v1.31.5-k3s1 agent
docker rm -f geass-k3s-agent   # when you are done
```

`TestNetworkPolicyCheck` runs the check that the cluster enforces network policies (the operator's `NetworkPolicyChecker`)
against whichever cluster the kubeconfig names, and says whether it found what that cluster should do: `Enforced` on k3s,
`NotEnforced` on a kind cluster, whose network plugin ignores policies (set `GEASS_NETCHECK_EXPECT=NotEnforced`). It needs
the operator's image on the cluster's control plane node, because the check runs it there, and names it in
`GEASS_NETCHECK_IMAGE`. Build one with `CGO_ENABLED=0 go build -o manager ./cmd/main.go` and a two-line Dockerfile on
`gcr.io/distroless/static:nonroot`, then import it: `docker save <image> | docker exec -i geass-k3s ctr images import -`
(`kind load docker-image` for kind). The test runs the check for a GeassCluster of its own and leaves any other in the
cluster alone.

`make test-k3s-ha` is different: it starts its own three-server K3s in Docker, so it needs no `make k3s-up`. It runs
the operator on it and takes down the server the operator is on, and checks that the API keeps answering, that the operator
starts on another server (because its image is on all of them), that the lost server rejoins, and that the cluster stops
without a majority. A host that allows few inotify instances per user (`fs.inotify.max_user_instances`, 128 by default)
cannot run three kubelets in containers, so the third server then runs as an etcd and API server only (`HA_THIRD_ETCD_ONLY`),
and a server that is started again can need more than one try. `HA_KEEP=1` leaves the servers up to look at.
