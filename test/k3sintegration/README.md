# k3s integration tests

These run the operator against a real k3s, because some things only exist there: k3s's Helm controller (which
installs the add-ons), its Traefik, and a current Kubernetes. A kind cluster has none of these.

```bash
eval "$(make -s k3s-up)"      # starts rancher/k3s in Docker, installs the CRDs, and sets KUBECONFIG
make test-k3s-serverless      # scale to zero and function schedules, end to end (about 15 minutes the first time)
make test-k3s-objectstorage   # the cluster's object storage, SeaweedFS and Garage (about 10 minutes the first time)
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
