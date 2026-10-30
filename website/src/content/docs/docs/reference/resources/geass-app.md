---
title: "GeassApp"
description: "Reference for the GeassApp resource: image, Git and function services, schedules, scaling, sleeping, ingress, variables, probes, and the deploy workflow."
sidebar:
  order: 2
---

A `GeassApp` is a service. A service runs in an environment of a [GeassProject](/docs/reference/resources/geass-project/), and it is one of these:

- One container image.
- One Git repository that is built into an image.
- One inline function.

The operator changes the `GeassApp` into a Deployment and a Service in the project's namespace. When you set a host, the operator also creates an Ingress. A function with schedules also gets one CronJob for each schedule. You can set a service to [sleep when idle](/docs/guides/scale-to-zero/).

The dashboard is the normal way to create and change services. The dashboard writes the `GeassApp` for you, and it manages a draft and deploy workflow on top of the `GeassApp`. You can also create a `GeassApp` with `kubectl`, and the operator runs it. Read the caveats in [Editing by hand](#editing-by-hand). Every `GeassApp` is in `geass-system`, and its name is unique across all projects.

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassApp` |
| Plural | `geassapps` |
| Scope | Namespaced |
| Namespace | `geass-system` |
| Subresources | `status` |
| Finalizer | `geass.geass.dev/app` |
| Printer columns | `Environment` (`.spec.environment`), `Ready` (`.status.conditions[?(@.type=="Ready")].status`) |

## Examples

Every example needs a project that is `Ready` and that has the environment named in `spec.environment`. See [GeassProject](/docs/reference/resources/geass-project/).

### An image service

This service runs a prebuilt image with two replicas, a public host, and HTTPS.

```yaml title="storefront-web.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassApp
metadata:
  name: storefront-web
  namespace: geass-system
spec:
  project: storefront
  environment: production
  source:
    image:
      image: ghcr.io/acme/storefront:1.4.2
  port: 8080
  replicas: 2
  ingress:
    host: shop.example.com
    tlsEnabled: true
  configData:
    LOG_LEVEL: info
  deploy:
    enabled: true
```

### A Git service

Geass builds a Git service in the cluster with Kaniko, and then deploys it. A Git service needs a [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/) and a registry that the cluster can push to and pull from. The registry comes from `spec.build.registry` or from the platform configuration. Geass has no registry of its own. A service without a registry waits with `no container registry is set for builds`.

```yaml title="storefront-api.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassApp
metadata:
  name: storefront-api
  namespace: geass-system
spec:
  project: storefront
  environment: production
  source:
    git:
      connectionRef:
        name: storefront-github
      repository: acme/storefront
      branch: main
      dockerfile: Dockerfile
  build:
    registry:
      repository: registry.example.com/acme/storefront-api
      credentialRef:
        name: registry-credentials
    cache: true
  port: 3000
  deploy:
    enabled: true
```

### A function service

A function runs inline source code on a managed runtime. In the default `http` mode, your code must serve HTTP on the port in the `PORT` environment variable.

```yaml title="storefront-hello.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassApp
metadata:
  name: storefront-hello
  namespace: geass-system
spec:
  project: storefront
  environment: production
  source:
    function:
      runtime: node
      code: |
        import http from "node:http";

        const port = Number(process.env.PORT) || 8080;

        http
          .createServer((req, res) => {
            res.setHeader("content-type", "application/json");
            res.end(JSON.stringify({ message: "Hello", path: req.url }));
          })
          .listen(port, () => console.log("Listening on " + port));
  deploy:
    enabled: true
```

### A function with schedules

A function in `event` mode exports a handler, and Geass serves HTTP for it. Each entry in `triggers` calls the function on a schedule, in UTC. See [Functions](/docs/guides/functions/).

```yaml title="storefront-report.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassApp
metadata:
  name: storefront-report
  namespace: geass-system
spec:
  project: storefront
  environment: production
  source:
    function:
      runtime: node
      mode: event
      code: |
        export async function handler(event) {
          if (event.trigger === "cron") console.log("Run", event.triggerName);
          return { ok: true, path: event.path };
        }
      triggers:
        - name: nightly
          schedule: "0 3 * * *"
        - name: sync
          schedule: "*/15 * * * *"
          path: /sync
          payload: '{"full":true}'
  deploy:
    enabled: true
```

### A service that sleeps when idle

This service stops after 10 minutes without a request, and starts again on the next request. It needs the serverless add-on on the cluster and a public host.

```yaml title="storefront-docs.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassApp
metadata:
  name: storefront-docs
  namespace: geass-system
spec:
  project: storefront
  environment: production
  source:
    image:
      image: ghcr.io/acme/storefront-docs:1.0.0
  port: 8080
  ingress:
    host: docs.example.com
  scaleToZero:
    enabled: true
    idleSeconds: 600
    maxReplicas: 3
  deploy:
    enabled: true
```

### A tuned service

This example sets most of the optional blocks. The Secret and connection names are placeholders for objects that you create.

```yaml title="storefront-web-tuned.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassApp
metadata:
  name: storefront-web
  namespace: geass-system
spec:
  project: storefront
  environment: production
  source:
    image:
      image: ghcr.io/acme/storefront:1.4.2
      pullSecret:
        name: ghcr-pull
  port: 8080
  replicas: 2
  resources:
    requests:
      cpu: 250m
      memory: 256Mi
    limits:
      cpu: 250m
      memory: 256Mi
  autoscaling:
    minReplicas: 2
    maxReplicas: 6
    targetCPUUtilization: 60
  runOn:
    tags:
      - storage
  ingress:
    host: shop.example.com
    path: /
    tlsEnabled: true
    dnsVerification: true
  env:
    - name: DATABASE_URL
      valueFrom:
        secretKeyRef:
          name: cheerful-pine-connection
          key: uri
  sharedVariableRefs:
    - LOG_LEVEL
  configData:
    FEATURE_FLAGS: checkout-v2
  secretRef:
    name: storefront-web-secrets
  metrics:
    enabled: true
    path: /metrics
    port: http
  build:
    command: ["node", "server.js"]
    args: ["--port", "8080"]
    workingDir: /app
  deploy:
    enabled: true
    readinessProbe:
      httpGet:
        path: /healthz
        port: 8080
      periodSeconds: 10
      timeoutSeconds: 2
      failureThreshold: 3
    livenessProbe:
      httpGet:
        path: /healthz
        port: 8080
      initialDelaySeconds: 15
```

## Spec fields

Required fields are marked. Every other field is optional. For a field of a standard Kubernetes type, the table names the type and does not expand it.

### Identity and source

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.project` | string | none | Required, at least 1 character | The ID (`metadata.name`) of the owning [GeassProject](/docs/reference/resources/geass-project/). It is not the display name. |
| `spec.environment` | string | none | Required. Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 63 characters at most | An environment listed in the project's `spec.environments`. The namespace is `<project>-<environment>`. |
| `spec.source` | object | none | Required | Exactly one of `image`, `git`, or `function`. The operator enforces this rule when the service is deployed. A draft can hold an incomplete source. |
| `spec.source.image.image` | string | none | Required inside `image` | A container image reference. The dashboard accepts references that match `^[A-Za-z0-9][A-Za-z0-9._/@:-]*$`. |
| `spec.source.image.pullSecret.name` | string | none | none | A `kubernetes.io/dockerconfigjson` Secret to pull the image. It is resolved in the environment's namespace. |
| `spec.source.git.connectionRef.name` | string | none | Required inside `git`, and must not be empty on deploy | The [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/) to clone with, in `geass-system`. |
| `spec.source.git.repository` | string | none | Required, and must not be empty on deploy | The GitHub repository as `owner/name`. |
| `spec.source.git.branch` | string | none | Required, and must not be empty on deploy | The branch to build. |
| `spec.source.git.commit` | string | empty | none | Pins the build to one commit. When empty, each build resolves the head of `branch`. The dashboard never sets it. |
| `spec.source.git.dockerfile` | string | `Dockerfile` | none | The Dockerfile path, relative to `context`. A path that already starts with `context` is taken as relative to the repository root. A leading slash is ignored. |
| `spec.source.git.context` | string | `.` | A path that leaves the repository is refused when the service is deployed | The root directory of the build: Kaniko builds in `/workspace/<context>`. A leading slash means the repository root, and `.` or empty is the root itself. The dashboard calls this field **Root directory**. |
| `spec.source.git.waitForCI` | boolean | `false` | none | Before the build, waits until every GitHub check run of the commit has completed successfully. |
| `spec.source.git.watchPatterns` | list of strings | empty | none | Which pushes start a build. Gitignore-style patterns, matched from the repository root against the files that the push changed. `!` takes files back out. A push starts a build only when it changes a file that the patterns select. Empty means that every push starts a build. This is a live setting: it applies immediately when you save it. The dashboard calls this field **Watch paths**. |
| `spec.source.function.runtime` | string | none | Required. `bun`, `node`, or `python` | The language runtime. |
| `spec.source.function.code` | string | none | Required. 262144 characters at most | The program. In `http` mode, it must serve HTTP on `PORT`. In `event` mode, it must export a handler. |
| `spec.source.function.mode` | string | `http` | `http` or `event` | What `code` is. `http` is a program that serves HTTP itself. `event` is a handler: a function that gets an event for each request and returns the response. Geass serves HTTP for the handler. |
| `spec.source.function.triggers` | list of objects | empty | At most 8, unique by `name` | Schedules that call the function. Each schedule is one CronJob. |
| `spec.source.function.triggers[].name` | string | none | Required. `^[a-z0-9]([-a-z0-9]{0,30}[a-z0-9])?$`, so 1 to 32 characters | Names the schedule and its CronJob. |
| `spec.source.function.triggers[].schedule` | string | none | Required. 100 characters at most. Five fields, or `@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`, `@annually`, or `@midnight` | A cron expression in UTC. Numbers, ranges, steps, and lists work. Names such as `MON` are refused. |
| `spec.source.function.triggers[].path` | string | `/` | Starts with `/`. 256 characters at most | The path that is called. |
| `spec.source.function.triggers[].payload` | string | `{"trigger":"cron","name":"<name>"}` | Valid JSON. 4096 characters at most | The JSON body of the request. |

For a monorepo, set `context` to the folder of the service (`apps/api`), and keep `dockerfile` relative to it (`Dockerfile`). A `dockerfile` that is written in full from the repository root (`apps/api/Dockerfile`) still works. `watchPatterns` decide which pushes rebuild the service. They apply only to pushes: a deploy and a change to a build setting start a build regardless. See [Root directory and Watch paths](/docs/guides/github-and-builds/#root-directory-and-watch-paths) for the patterns.

A Git service resolves the branch to a commit through the GitHub API, clones the commit with the connection's token, and builds the image from the Dockerfile. When the build succeeds, the Deployment runs the repository that the build pushed to, at the digest that the build recorded (`<repository>@sha256:…`). See [GeassBuild](/docs/reference/resources/geass-build/) for the build lifecycle, and [GitHub and builds](/docs/guides/github-and-builds/) for the guide.

#### Functions

| Runtime | Image | File | Command in `http` mode | Command in `event` mode |
| --- | --- | --- | --- | --- |
| `bun` | `oven/bun:1-alpine` | `index.ts` | `bun run /function/index.ts` | `bun run /function/geass-shim.ts` |
| `node` | `node:22-alpine` | `index.mjs` | `node /function/index.mjs` | `node /function/geass-shim.mjs` |
| `python` | `python:3.12-alpine` | `main.py` | `python -u /function/main.py` | `python -u /function/geass-shim.py` |

The operator stores the code in the ConfigMap `<app>-function`, and mounts it read-only at `/function`. In `event` mode, the ConfigMap also holds the shim. The shim is a small program that Geass keeps for each runtime. The shim does these things:

- It serves HTTP.
- It changes each request into an event.
- It calls the `handler` that your file exports. In Python, this is a function named `handler`.
- It changes what the handler returns into the response.

The container runs the shim, which imports your file. The operator sets `PORT` to `spec.port`, unless `spec.env` or a selected shared variable already defines `PORT`. The operator also turns off the service account token for function pods, so your code gets no Kubernetes API access. The operator adds a fingerprint of the runtime, the mode, the shim version, and the code to the pod template, as the annotation `geass.dev/function-hash`. So when you edit the code or the mode, the pods restart. When `spec.build.command` is set, it replaces the command. In `event` mode, this means that the shim does not run. See [Functions](/docs/guides/functions/).

A function in `event` mode, or a function with `triggers`, also gets a random token. The operator keeps the token in the Secret `<app>-trigger` (key `token`) in the environment namespace. The operator gives the token to the container as `GEASS_TRIGGER_TOKEN`, unless `spec.env` already defines that name. The Job of each schedule sends the token in the header `x-geass-trigger-token`. The shim uses this header to tell a scheduled call from a visitor. The pod template has the annotation `geass.dev/trigger-token-fingerprint`, so when the token is made again, the pods restart.

Each trigger becomes a CronJob in the environment namespace, named `<app>-<trigger name>-<8 characters>` (52 characters at most), with the time zone `Etc/UTC`. Each time that the CronJob fires, a Job runs `curl` and sends `POST <path>` to the function. The body is the payload, and the headers are `x-geass-trigger-token` and `x-geass-trigger-name`. When the service sleeps when idle, the request goes through the scale-to-zero proxy. Otherwise, the request goes directly to the Service. A run fails when the answer is `400` or higher, or when no answer arrives within 120 seconds. A failed run is tried one more time. Runs do not overlap. If a run cannot start within 300 seconds of its time, the run is skipped. The triggers are part of the source, so they follow the [draft and deploy workflow](#the-draft-and-deploy-workflow): they start with the next deploy.

Two limits apply to the code:

- The raw code must be under 256 KB.
- The JSON-escaped code must also stay under 200 KB, because every deploy stores the whole spec, with the code, in an annotation. Quotes and line breaks take extra space.

The errors are `function code must be under 256 KB` and `function code is too large to deploy: it must stay under 200 KB once saved with the service (quotes and line breaks take extra space)`.

### Replicas, resources and placement

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.replicas` | integer | `1` | 0 or more | The Deployment's replica count. The dashboard's API accepts 0 to 1000, and the **Replicas** field in its form starts at 1. With 0, the service runs no pods and still reports ready. This field does not apply while the service sleeps when idle. |
| `spec.port` | integer | `8080` | 1 or more | The container port, the Service port, and the Ingress backend port. The container port is named `http`. |
| `spec.resources` | `corev1.ResourceRequirements` | 100m CPU and 128Mi memory, as requests and limits | none | CPU and memory of the container. The operator uses the default whenever `requests` is empty, and in that case it ignores `limits`. `requests` is what the capacity check, the scheduler, and the project quotas count. `limits` is the most that the container can use. A limit above the request lets the container burst while its server has room. It also makes the pod `Burstable`, so the pod is among the first that are stopped when the server runs out of memory. The dashboard offers fixed sizes (0.1 to 2 CPU, 128 MB to 4 GB) for each. By default, the dashboard sets the limit equal to the request. |
| `spec.autoscaling.maxReplicas` | integer | none | Required inside `autoscaling`. 1 or more | The upper bound. |
| `spec.autoscaling.minReplicas` | integer | `1` | 1 or more | The lower bound. It must not be more than `maxReplicas`. |
| `spec.autoscaling.targetCPUUtilization` | integer | none | 1 to 100 | The target average CPU utilization, measured against the container's CPU request. |
| `spec.runOn.tags` | list of strings | empty | At most 8 unique items. Each matches `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 40 characters at most | Node tags that the service must run on. A node needs all of them. |
| `spec.scaleToZero.enabled` | boolean | none | Required inside `scaleToZero` | Lets the service sleep when it is idle, and wakes it on the next request. |
| `spec.scaleToZero.idleSeconds` | integer | `300` | 30 to 86400 | How long the service goes without a request before it is stopped. |
| `spec.scaleToZero.maxReplicas` | integer | `3` | 1 to 100 | How many copies the service can scale up to under load while it is awake. |

The operator creates a HorizontalPodAutoscaler named `<app>` only when `maxReplicas` is greater than 1 and `targetCPUUtilization` is set. Otherwise, the operator deletes any existing HorizontalPodAutoscaler. An invalid `minReplicas` gives the error `autoscaling minReplicas must be between 1 and maxReplicas`. On every reconcile, the operator sets the Deployment's replica count from `spec.replicas`, except while the service sleeps when idle. The capacity check for a deploy counts the larger of `replicas` and the `maxReplicas` of `autoscaling`.

The service runs in the project's region. If the project has no region, the service runs in the cluster's default region. If neither is set, the service runs on any node. `runOn.tags` limits the placement to nodes that have every tag. If the project requires tags, they are added to the service's own tags. The operator changes the placement into a node selector on `topology.kubernetes.io/region` and `tag.geass.dev/<tag>`. The operator also adds tolerations, so that the service can use nodes that reserve those tags. When the cluster cannot place the service, the `Ready` message says why. See [Scaling and placement](/docs/guides/scaling-and-placement/) and [Compliance tags](/docs/guides/compliance-tags/).

#### Sleeping when idle

With `scaleToZero.enabled`, the service is stopped after `idleSeconds` without a request. The next request to its public host starts the service again. That request is held for the few seconds that the service takes to start. While the service sleeps, the scale-to-zero add-on decides how many copies run, from none up to `maxReplicas`. The operator then does not set the Deployment's replica count, so `spec.replicas` does not apply. When you turn sleeping off, the operator uses `spec.replicas` again.

The service sleeps only when all of these conditions are true:

- The cluster's serverless add-on is installed (`spec.addons.serverless.enabled` on the [GeassCluster](/docs/reference/resources/geass-cluster/)).
- The service has a public host: `ingress.host` or a host in `ingress.rules`.
- `spec.autoscaling` is not set.

Otherwise, the service is always on, the same as before, and the `ScaleToZero` condition says why. After a deploy of a version that changes the pods, the operator keeps one copy running until a copy of the new version starts. So if a version cannot start, the deploy fails, and a visitor does not see the failure. See [Sleep when idle](/docs/guides/scale-to-zero/) for the full behavior, and [Conditions](#conditions) for the messages.

### Public access

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.ingress.host` | string | empty | none | The public hostname. When empty, the operator creates no Ingress. |
| `spec.ingress.path` | string | `/` | none | The path prefix of the route. |
| `spec.ingress.tlsEnabled` | boolean | `false` | none | Serves the host over HTTPS with a certificate from cert-manager. |
| `spec.ingress.dnsVerification` | boolean | `false` | none | Checks that the host resolves, and reports the result in `status.dnsVerified` and `status.dnsMessage`. |
| `spec.ingress.rules[].host` | string | none | Required inside a rule | The host of one route. |
| `spec.ingress.rules[].path` | string | `/` | none | The path prefix of that route. |

The Ingress is named `<app>`, uses the `traefik` class, and sends traffic to the Service port `spec.port`. For a service that sleeps when idle, the Ingress sends traffic to the Service `<app>-wake` on port 8080 instead. That Service is a name for the scale-to-zero proxy. With `tlsEnabled`, the Ingress has the annotations `cert-manager.io/cluster-issuer: letsencrypt-prod` and `traefik.ingress.kubernetes.io/router.entrypoints: websecure`, and it uses the TLS Secret `<app>-tls`. Without `tlsEnabled`, the entrypoint is `web`. See [Domains and TLS](/docs/guides/domains-and-tls/).

`rules` replaces the `host` and `path` pair as the list of routes. The Ingress still exists only when `ingress.host` is not empty. The dashboard never sets `rules`.

:::note
When the platform has a ready Cloudflare connection and `ingress.host` is empty, the operator writes a generated hostname into `spec.ingress.host`, and sets `tlsEnabled` to `false`. The name is `<app>-<project>.<root domain>` for `production`, and `<app>-<project>-<environment>.<root domain>` for other environments. So if you clear the host in the dashboard, the generated host comes back.
:::

### Variables and files

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.env` | list of `corev1.EnvVar` | empty | none | Environment variables. With `valueFrom`, values can come from the ConfigMaps and Secrets of the environment namespace. The dashboard writes entries here when you use **Add Reference** to connect a database or a bucket. |
| `spec.envFrom` | list of `corev1.EnvFromSource` | empty | none | ConfigMaps and Secrets in the environment namespace to load as environment variables. |
| `spec.sharedVariableRefs` | list of strings | empty | Each matches `^[A-Z_][A-Z0-9_]*$` | Names of the project's shared variables to inject. See the caution below. |
| `spec.configMapRefs` | list of `corev1.LocalObjectReference` | empty | none | ConfigMaps in the environment namespace to mount at `/config/refs/<name>`. |
| `spec.secretRefs` | list of `corev1.LocalObjectReference` | empty | none | Secrets in the environment namespace to mount at `/secrets/refs/<name>`. |
| `spec.configData` | map of strings | empty | none | Plain variables. The operator stores them in the ConfigMap `<app>-config`. The variables that you add under **Variables** in the dashboard go here. |
| `spec.secretData` | map of strings | empty | none | Secret values stored in the resource itself. The dashboard does not use this field anymore. Prefer `secretRef`. |
| `spec.secretRef.name` | string | none | none | A Secret in `geass-system` that holds the service's secret values. The dashboard keeps one Secret named `<app>-secrets`. When this field is set, `secretData` is ignored. |

How the values reach the container:

| Source | In the container |
| --- | --- |
| `spec.env` | Environment variables. They take priority over every `envFrom` source. |
| `spec.envFrom` | Environment variables from your ConfigMaps and Secrets. |
| Shared variables | Environment variables from the ConfigMap `geass-shared-variables` and the Secret `geass-shared-secrets` in the namespace. |
| `spec.configData` | Environment variables from `<app>-config`, and files under `/config`. |
| `spec.secretData` or `spec.secretRef` | Environment variables from `<app>-secret`, and read-only files under `/secrets`. |
| `spec.configMapRefs` | Files under `/config/refs/<name>`. |
| `spec.secretRefs` | Files under `/secrets/refs/<name>`. |

The order of the `envFrom` sources is `spec.envFrom`, shared variables, `<app>-config`, and `<app>-secret`. When one key is in several sources, the later source wins. So a service variable overrides a shared variable with the same name.

When the service is deployed, the values in `spec.configData` and `spec.secretData` are copied into the namespace. `spec.secretRef` is read from `geass-system`, and copied into the Secret `<app>-secret` in the namespace.

:::caution
Do not rely on `sharedVariableRefs` to keep a plain value away from a service. `spec.sharedVariableRefs` filters only secret shared variables. When the list is empty, the service gets every shared variable of its environment. When the list is not empty, the service gets the listed secret variables and the listed plain variables as individual entries. The service also still gets the whole `geass-shared-variables` ConfigMap, so every plain shared variable of the environment reaches the container.
:::

:::caution
Anyone who can read the `GeassApp` can read the values in `spec.secretData`. Dashboard viewers do not see these values. For viewer sessions, the dashboard removes `secretData`, `secretRef`, `secretRefs`, `envFrom`, `configData`, and the values of `env`.
:::

### Where referenced objects must live

Fields that reference other objects resolve them in different namespaces. A wrong namespace is the most common reason that a service does not start.

| Field | The object must be in |
| --- | --- |
| `spec.source.image.pullSecret`, `spec.env[].valueFrom`, `spec.envFrom`, `spec.configMapRefs`, `spec.secretRefs` | The environment namespace, `<project>-<environment>`. |
| `spec.secretRef` | `geass-system`. |
| `spec.source.git.connectionRef` | `geass-system`. It names a `GeassGitHubConnection`. |
| `spec.build.registry.credentialRef` | `geass-system`. It names a Secret with the key `.dockerconfigjson`. |
| `spec.logs.archiveStoreRef` | `geass-system`. It names a [GeassObjectStore](/docs/reference/resources/geass-object-store/). |

### Metrics and logs

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.metrics.enabled` | boolean | `false` | none | Creates a ServiceMonitor named `<app>-metrics`, so that Prometheus scrapes the service. This needs the Prometheus Operator CRDs in the cluster. |
| `spec.metrics.path` | string | `/metrics` | none | The HTTP path to scrape. |
| `spec.metrics.port` | string | `http` | none | The name of the Service port to scrape. |
| `spec.logs.archiveStoreRef.name` | string | none | none | An object store that receives the log of each build. See [GeassBuild](/docs/reference/resources/geass-build/). |
| `spec.logs.retentionHours` | integer | none | 1 or more | Stored, but nothing reads it. See the caution below. |

The operator creates only one Service port, named `http`. The ServiceMonitor endpoint refers to a Service port by name, so `http` is the only value of `metrics.port` that matches. See [Observability](/docs/guides/observability/).

:::caution
Do not rely on `spec.logs.retentionHours`. The operator and the dashboard do not read it. The CRD describes it as the retention of archived logs, but nothing uses it. Only `archiveStoreRef` has an effect, and only for build logs.
:::

### Build and start command

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.build.command` | list of strings | empty | none | Replaces the image's entrypoint. For a function, it replaces the command that Geass runs. In `event` mode, that command is the shim. |
| `spec.build.args` | list of strings | empty | none | Replaces the image's arguments. |
| `spec.build.workingDir` | string | empty | none | The container's working directory. |
| `spec.build.registry.repository` | string | empty | none | The destination image reference for Git builds, used as written. When this field is empty, the operator uses the organization's registry, for a project of an organization that has one. Otherwise, the operator uses `spec.registry.repository` of the platform configuration. When none is set, the service waits with `no container registry is set for builds`. Under the platform's `spec.tenancy.registry`, an organization's service never uses the platform's registry. It builds into `<organization repository>/<project>/<service>`. A service that uses a part of a registry that is the platform's or another organization's is refused. See [Registry isolation for organizations](/docs/operations/registry-isolation/). |
| `spec.build.registry.credentialRef.name` | string | none | none | The Secret that holds registry credentials for the build. When this field is empty and the service uses the platform's repository, the credentials of the platform configuration are used. The nodes pull the image with a copy of the credential: the Secret `<app>-registry-pull` in the environment namespace. |
| `spec.build.cache` | boolean | `false` | none | Passes `--cache=true` to Kaniko. |
| `spec.build.argsFrom` | list of `corev1.LocalObjectReference` | empty | none | Stored, but nothing reads it. |

The dashboard splits **Command** and **Arguments** on whitespace, so it cannot express an argument that contains a space. If you need such an argument, set `command` and `args` as lists with `kubectl`.

### Rollout and health

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.deploy.enabled` | boolean | `false` | none | Whether the operator creates and maintains the workload. `false` keeps the service as a draft. |
| `spec.deploy.restartPolicy` | string | `Always` | Only `Always` | The pod restart policy. `Always` is the only value that Deployments support. |
| `spec.deploy.readinessProbe` | `corev1.Probe` | none | none | Decides whether the pod gets traffic. |
| `spec.deploy.livenessProbe` | `corev1.Probe` | none | none | Restarts an unhealthy container. |
| `spec.deploy.startupProbe` | `corev1.Probe` | none | none | Holds off the other probes until the container starts. |
| `spec.deploy.failureThreshold` | integer | none | 1 or more | The number of reconcile passes that find the Deployment not ready, after which the operator reports `RolloutFailed`. Unset means never. |
| `spec.deploy.pauseOnFailure` | boolean | `false` | none | Sets `status.rolloutPaused` when the threshold is reached. |
| `spec.deploy.rollbackOnFailure` | boolean | `false` | none | Adds the `RollbackReady` condition when the threshold is reached. It does not roll anything back. |

The dashboard edits only the HTTP readiness probe, through **Healthcheck path**. The dashboard builds a probe that calls that path on `spec.port` every 10 seconds, with a 2 second timeout and 3 failures allowed. If you save the **Deploy** section with an empty path, the dashboard removes `readinessProbe`, including a probe that you set by hand. The other probes, and the three fields after `startupProbe`, are not in the dashboard.

:::caution
`failureThreshold`, `pauseOnFailure`, and `rollbackOnFailure` work only together, and only partly.

- Each reconcile pass that finds the Deployment not ready adds one to `status.healthCheckFailures`. The operator does not measure time here. Passes start when the object changes, and the counter is part of the object. So a threshold can be reached within seconds. A ready Deployment sets the counter back to zero.
- A pass does not count in two cases. The first case is a container of the service that is stuck in a start failure (`CrashLoopBackOff`, `ImagePullBackOff`, `ErrImagePull`, `InvalidImageName`, `CreateContainerConfigError`, or `CreateContainerError`). The second case is a Deployment that ran out of time to progress. The operator then reports `The service cannot start: …` (see [Conditions](#conditions)) and stops there. So `RolloutFailed` is reached by a service that is slow or never ready, for example a service whose readiness probe fails or whose pod cannot be scheduled. A service that crashes or cannot pull its image does not reach `RolloutFailed`.
- At the threshold, the operator sets `Ready` to `False` with reason `RolloutFailed`.
- With `pauseOnFailure`, the operator also sets `status.rolloutPaused` to `true`. The operator then skips all workload work. No code clears the flag, because the step that would clear it is skipped while the flag is set. To resume, clear the flag yourself: `kubectl patch geassapp <name> -n geass-system --subresource=status --type=merge -p '{"status":{"rolloutPaused":false,"healthCheckFailures":0}}'`.
- With `rollbackOnFailure`, the operator only adds `RollbackReady` with reason `RollbackRequested`. There is no automatic rollback, and nothing removes that condition.
:::

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.targetNamespace` | string | The namespace that the workload runs in. |
| `status.url` | string | The public URL, built from `spec.ingress.host`, the scheme, and the path. Empty when there is no host. |
| `status.resolvedImage` | string | For Git services, the image of the latest successful build: the repository that the build pushed to, without its tag, at the digest that the build recorded (`<repository>@sha256:…`). When this field is set, the Deployment runs this value instead of `spec.source.image`. See [GitHub and builds](/docs/guides/github-and-builds/#how-a-build-runs). |
| `status.activeBuild` | string | The name of the [GeassBuild](/docs/reference/resources/geass-build/) whose image is deployed. |
| `status.currentBuild` | string | The name of the build for the service's current settings. A push or a rebuild runs this build again. It is not always the newest build, or the build that is deployed. |
| `status.dnsName` | string | The value of `spec.ingress.host`. |
| `status.traefikTarget` | string | The address of the Ingress, when Kubernetes reports one. |
| `status.dnsVerified` | boolean | Always `true` when `dnsVerification` is off. When it is on, `true` if the host resolves from the operator. |
| `status.dnsMessage` | string | Set only while `dnsVerification` is on. `DNS hostname is configured for verification` when the host resolves, and `DNS hostname is required` when it does not resolve. The second message shows even when a host is set. |
| `status.healthCheckFailures` | integer | The count described under [Rollout and health](#rollout-and-health). |
| `status.rolloutPaused` | boolean | Whether the operator paused the service. |
| `status.reconcileRetry` | object | `reconcileFailures`, `reconcileFailureStarted`, and `reconcileFailureGeneration`: the retry bookkeeping. |
| `status.provision.activeProvisionAttempt` | string | The ID of the attempt in progress. |
| `status.provision.provisionAttempts` | list, at most 8 | The recent deploys, newest first. Each has `id`, `startedAt`, `completedAt`, `phase` (`Running`, `Succeeded`, or `Failed`), `summary`, `errors`, `buildRef`, and `deploymentRef`. `errors` has at most 12 entries while the attempt runs, and 5 after it, each at most 500 characters. |
| `status.conditions` | list | `Ready`. `ScaleToZero`, only on a service that asks to sleep when idle. `OutOfMemory`, only while a copy was stopped for memory in the last hour. `RollbackReady`, for `rollbackOnFailure`. `Sandboxed`, only on a service of an organization's project while the platform's `spec.tenancy.sandbox` is set. `RegistryIsolated`, only on a service of an organization while the platform's `spec.tenancy.registry` is set. |

The dashboard starts an attempt each time that you deploy. The operator adds the errors that it meets. Then the operator closes the attempt as `Succeeded` with the summary `Provisioned successfully`, or as `Failed`. An attempt that succeeded can still list errors. These are temporary errors that the operator retried, for example a write conflict.

### Conditions

| Type | Status | Reason | Message | Meaning |
| --- | --- | --- | --- | --- |
| `Ready` | `True` | `AppReady` | `Application is ready` | The Deployment has at least the desired number of available replicas. |
| `Ready` | `False` | `ReconcileError` | `Service is configured and waiting for deployment` | The service is a draft. Deploy it. |
| `Ready` | `False` | `ReconcileError` | `Service has pending changes and no last deployed snapshot` | The service is deployed and has pending changes, but `geass.dev/last-deployed` is missing. See [Troubleshooting](#troubleshooting). |
| `Ready` | `False` | `ReconcileError` | `Git source is waiting for a successful build` | The build did not succeed yet. |
| `Ready` | `False` | `ReconcileError` | `Waiting for the sandboxed runtime: RuntimeClass "gvisor" is not in the cluster, ...` | The service, or the build that it waits for, belongs to an organization. It must run under the platform's sandboxed runtime, and the RuntimeClass of that runtime is missing. The operator does not write the Deployment, so nothing starts without the sandbox. A Deployment that was running stays as it was. This state does not count against the retry limit, and the service continues when the class appears. See [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/#when-the-runtimeclass-is-missing). |
| `Ready` | `False` | `ReconcileError` | `Registry isolation: …` | The platform's `spec.tenancy.registry` holds back this service of an organization. The organization has no registry of its own, or the service names a login or a registry, or runs an image, that is the platform's or another organization's. The `RegistryIsolated` condition says which. Nothing new is written for the service, and a Deployment that it had stays as it was. This state does not count against the retry limit. See [Registry isolation for organizations](/docs/operations/registry-isolation/). |
| `Ready` | `False` | `ReconcileError` | `Service is starting` | The Deployment exists but is not available yet. |
| `Ready` | `False` | `ReconcileError` | `Waiting for database postgres` | The service reads the connection Secret of a database that is still starting. For a bucket, the message is `Waiting for bucket …`. The copy of the service cannot be created until the Secret exists. This state does not count against the retry limit: the service is checked again, and it starts when the Secret is there. |
| `Ready` | `False` | `ReconcileError` | `The service cannot start: app is CrashLoopBackOff` | A container is in a state that does not clear without a change: `CrashLoopBackOff`, `ImagePullBackOff`, `ErrImagePull`, `InvalidImageName`, `CreateContainerConfigError`, or `CreateContainerError`. A `CreateContainerConfigError` for the connection Secret of a database or bucket that is still starting is a wait, and the service says so instead (`Waiting for database …`). When the Deployment ran out of time to progress, the text ends with `it did not become ready in time` instead. Read the pod's events. |
| `Ready` | `False` | `ReconcileError` | `Ran out of memory: a copy was stopped for …` | The kernel stopped a copy of the service because the copy used more memory than it could have, and the service is not ready. When this is the cause, this message replaces `Service is starting` and `The service cannot start: app is CrashLoopBackOff`. The text says what the container reserves and what it can use. This state does not count against the retry limit: the service is checked again until it is fixed or recovers. |
| `Ready` | `False` | `ReconcileError` | `The new version of the service cannot start: …` | A service that sleeps when idle was deployed with a change to its pods, and the copy of the new version cannot start. The deploy fails, not the first visitor. |
| `Ready` | `False` | `ReconcileError` | `The service was woken but its copy cannot start: …` or `The service was woken but has not started within 10m0s` | A request woke a sleeping service, and its copy does not start. |
| `Ready` | `False` | `Waking` | `Starting after a request` | A sleeping service is starting after a request. This state does not count against the retry limit. |
| `Ready` | `False` | `ReconcileError` | Project errors, such as `project "x" is not ready` | The project does not exist, is not ready, or has no such environment. |
| `Ready` | `False` | `ReconcileError` | Source errors, such as `one of spec.source.image, spec.source.git, or spec.source.function is required` | The source is not valid. |
| `Ready` | `False` | `ReconcileError` | Capacity or placement text, such as `There is no ready node in the eu region to run this service. Add a node there, or choose another region` | The cluster cannot place the service. |
| `Ready` | `False` | `ReconcileError` | An error from creating an object, for example `metrics are enabled but Prometheus Operator is not installed in this cluster` | A Kubernetes object could not be created or updated. |
| `Ready` | `False` | `RolloutFailed` | `Deployment failed the configured health-check threshold` | `spec.deploy.failureThreshold` was reached. |
| `Ready` | `False` | `ProvisionFailed` | `Provisioning failed after repeated errors. Update settings or platform configuration, then save or redeploy to try again.` | The operator gave up. See below. |
| `Ready` | `False` | `ReconcileError` | `Retrying provisioning` | You clicked **Retry**. The operator tries again. |
| `RollbackReady` | `True` | `RollbackRequested` | `Rollback was requested after rollout failure` | `rollbackOnFailure` is on, and the threshold was reached. For information only. |
| `OutOfMemory` | `True` | `OOMKilled` | The same `Ran out of memory: …` text | A copy of the service was stopped for memory in the last hour, whether or not the service is ready now. The condition is removed when no copy was stopped in the last hour. A service that never ran out of memory does not have this condition. |
| `Sandboxed` | `True` | `RuntimeClass` | `Runs under RuntimeClass gvisor` | The service's pods run under the platform's sandboxed runtime. |
| `Sandboxed` | `False` | `ReservedNodes` | `Runs without the sandbox, on nodes reserved for the organization (tag …)` | `skipOnReservedNodes` is on, and the pods can run only on nodes reserved for their own organization. |
| `Sandboxed` | `False` | `RuntimeClassMissing` | The same `Waiting for the sandboxed runtime: …` text | The RuntimeClass is missing, and the service waits for it. A `Warning` event `SandboxRuntimeClassMissing` is recorded when the wait begins. |
| `RegistryIsolated` | `True` | `Isolated` | `Uses no registry login of the platform's or of another organization's, and no image of theirs` | Only on a deployed service of an organization's project, while the platform's `spec.tenancy.registry` is set. |
| `RegistryIsolated` | `False` | `OwnRegistryRequired`, `PlatformCredential` or `ForeignRepository` | The same `Registry isolation: …` text | The service is held back. See the `Ready` row above. A `Warning` event `RegistryRefused` is recorded when the hold begins. |
| `ScaleToZero` | `True` | `Active` | `Sleeps after idle, wakes on the next request` | The service sleeps when idle. The condition exists only when `spec.scaleToZero.enabled` is true. |
| `ScaleToZero` | `False` | `AddonNotInstalled` | `Scale to zero needs the serverless add-on: set spec.addons.serverless.enabled on the GeassCluster` | The service asked to sleep, and it is always on. Turn the add-on on. |
| `ScaleToZero` | `False` | `NoPublicHost` | `Scale to zero needs a public host: it is the requests to it that wake the service` | Give the service a host. |
| `ScaleToZero` | `False` | `AutoscalingSet` | `Scale to zero decides how many copies run, so CPU autoscaling has to be turned off` | Remove `spec.autoscaling`. |

The operator retries a service that is not ready every 30 seconds. Waiting states never count against the retry limit. These are a draft, a pending build, a service that is starting, a pending change, and a sleeping service that is waking. Other errors count, including `The service cannot start: …`. After 5 errors in a row, or after 3 minutes, the service becomes `ProvisionFailed`. The operator then stops until `metadata.generation` changes. Some errors skip the count and fail immediately, for example `source.git.connectionRef, repository, and branch are required`. For a deploy that was announced to webhooks, `ProvisionFailed` also sends the `deployment.failed` event. To start again, click **Retry** on the service, or change its spec.

## How the dashboard maps to this resource

### Creating a service

On the **Resources** tab of a project, click **+ Add resource**. These choices create a service:

| Choice | What it creates |
| --- | --- |
| **GitHub Repository** | A draft with `source.git`. You select a repository from the project's GitHub connection. The branch defaults to the repository's default branch, or to `main`. |
| **Docker Image** | A draft with `source.image`. |
| **Function** | A draft with `source.function`. You select a runtime, and the dashboard fills in starter code for a server (`mode: http`). The dashboard opens the **Code** tab. |
| **Template** | One draft for each service and database of the template. A function keeps its mode and schedules. See [Templates](/docs/guides/templates/). |

The dashboard generates the resource name, for example `morning-beach`. With the REST API, you can pass `name` yourself. A draft has `spec.deploy.enabled: false`. The dashboard sets `spec.port` to 8080, `spec.replicas` to 1, and `spec.resources` to 0.1 CPU and 128 MB. A draft that you create through the REST API with no source gets the image `nginx:alpine`. The command line does the same with `geass apps create`, which saves a draft. See [Command line](/docs/guides/command-line/).

### The draft and deploy workflow

The dashboard does not apply your edits immediately. The dashboard saves them and waits for you to deploy, so that one deploy changes everything together.

1. A new service is a draft, with the status **Draft**. The top bar shows how many changes are pending, for example `2 pending` or `Up to date`.
2. You edit **Settings** and **Variables**. Each save records what changed.
3. To deploy every pending resource of the environment, click **Deploy changes** in the top bar. The `N pending` button next to it lists what is waiting. **Redeploy** shows in the header of a deployed service's panel when the service has no pending changes.

A deploy does these things:

- It sets `spec.deploy.enabled` to `true`.
- It clears the pending markers.
- It stores a snapshot of the whole spec in `geass.dev/last-deployed`.
- It stamps a new `geass.dev/deploy-revision`.
- It starts a provision attempt.
- It creates a [GeassDeployment](/docs/reference/resources/geass-deployment/) record.

When the project has an enabled webhook, the deploy also sends the `deployment.started` event. When the cluster cannot fit the service, the deploy is refused with a capacity message.

The dashboard records state in these annotations:

| Annotation | Meaning |
| --- | --- |
| `geass.dev/deploy-workflow` | `"true"` on services that the dashboard created. The dashboard shows the **Draft** status and counts pending changes only when this annotation is set. |
| `geass.dev/pending-updates` | The number of saved changes that are not deployed. |
| `geass.dev/pending-changes` | The kinds of pending change, separated by commas: `created`, `settings`, and `variables`. |
| `geass.dev/last-deployed` | The whole spec at the last deploy, as JSON. |
| `geass.dev/deploy-revision` | A timestamp that changes on every deploy. It is copied to the pod template, so a deploy restarts the pods. |
| `geass.dev/webhook-state` | The last deployment event announced to webhooks, as `<revision>:<event>`. |
| `geass.dev/github-deployment` | Where the operator last reported a repository service's deploy to GitHub, as `<commit>:<GitHub deployment ID>:<state>:<deploy revision>`. The state is `created`, `in_progress`, `success`, or `failure`. This annotation prevents a deploy from being reported two times. A deploy that succeeded is not reported again when the service restarts or scales. An ID of `0` is a service that was already running when the operator first looked at it. Such a service gets a record, and nothing is sent. See [Reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github). |
| `geass.dev/display-name` | The label that the dashboard shows. |

While `pending-updates` is greater than zero on a deployed service, the operator runs the snapshot in `geass.dev/last-deployed`, not the live spec. This keeps saved edits frozen until you deploy them. A few settings never replace the pods. These settings use the live spec, and they take effect when you save: `replicas`, `autoscaling`, `scaleToZero`, `ingress` (all of it), `metrics`, `logs.retentionHours`, `deploy.failureThreshold`, `deploy.pauseOnFailure`, `deploy.rollbackOnFailure`, and `source.git.watchPatterns`. Everything else waits for a deploy, including a function's mode and schedules. The dashboard labels live settings **Applies instantly**. When you save, the dashboard tells you `Saved. Applied now` or `Saved. Deploy to apply them`.

You can add secret variables to a draft. The Secret that `spec.secretRef` names stays in `geass-system` until the first deploy, and the deploy copies it into the namespace. The operator deletes that Secret, and `<app>-secrets`, only when you delete the service.

### Settings tabs

To open the panel of a service, click the service on the **Resources** tab. The panel has the tabs **Overview**, **Deployments**, **Code** (functions only), **Variables**, **Metrics**, **Console**, and **Settings**. **Settings** has these sections:

| Section | Field | Spec field |
| --- | --- | --- |
| **Source** | **Image** | `source.image.image`. A save replaces the whole `image` block, so `pullSecret` is lost. |
| **Source** | **Repository**, **Root directory**, **Branch**, **Wait for CI** | `source.git.repository`, `source.git.context`, `source.git.branch`, `source.git.waitForCI` |
| **Source** | **Runtime** | `source.function.runtime`. You edit the code, the mode, and the schedules in the **Code** tab. |
| **Networking** | **Custom domain**, **HTTPS**, **Verify DNS**, **Port** | `ingress.host`, `ingress.tlsEnabled`, `ingress.dnsVerification`, `port` |
| **Scale** | **Replicas**, **Resources**, **Autoscaling** (**Scale up to**, **CPU target %**), **Sleep when idle** (**Sleep after (minutes)**), **Run on** (shown only when nodes have tags) | `replicas`, `resources`, `autoscaling`, `scaleToZero.enabled` and `scaleToZero.idleSeconds`, `runOn.tags` |
| **Build** (Git only) | **Dockerfile path**, **Watch paths**, **Use build cache** | `source.git.dockerfile`, `source.git.watchPatterns`, `build.cache` |
| **Deploy** | **Command**, **Arguments**, **Working directory**, **Healthcheck path**, **Enable metrics** | `build.command`, `build.args`, `build.workingDir`, `deploy.readinessProbe`, `metrics.enabled`. **Restart policy** is read-only, and it shows `Always`. |
| **General** | Project, environment, **Service ID** | Read-only. The service ID is `metadata.name`. |
| **Danger** | **Delete service** | Deletes the `GeassApp`. To confirm, you enter the service ID. |

The **Variables** tab writes these fields:

- `spec.configData`, with **New Variable** and **Raw Editor**.
- The Secret `<app>-secrets` with `spec.secretRef`, with the **Secret** switch.
- `spec.sharedVariableRefs`, with **Shared Variable**.
- Entries in `spec.env`, with **Add Reference**. For a database, **Add Reference** adds `DATABASE_URL` or `REDIS_URL` from the `uri` key of the database's connection Secret. For a bucket, it adds `S3_ENDPOINT`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, and `S3_BUCKET` from the keys of the bucket's connection Secret.

See [Environment variables](/docs/guides/environment-variables/).

The **Code** tab of a function edits `source.function.code` and `source.function.mode`. The **Mode** menu has **Server (serves HTTP)** and **Handler (gets an event)**. Under **Schedules**, the tab edits `source.function.triggers`. **Save** records a pending change ("Code saved"), and **Save schedules** also records one ("Schedules saved"). To apply either change, deploy. A change of the mode does not rewrite the code.

The **Console** tab opens a shell in a pod, and records a [GeassConsoleSession](/docs/reference/resources/geass-console-session/). The **Deployments** tab lists the [GeassDeployment](/docs/reference/resources/geass-deployment/) records and the provision attempts.

The dashboard has no controls for these fields. You set them with `kubectl`:

- `source.git.commit`, `source.image.pullSecret`, and `ingress.rules`.
- `env` entries other than references, `envFrom`, `configMapRefs`, `secretRefs`, and `secretData`.
- `build.registry`, `build.argsFrom`, `logs`, and `scaleToZero.maxReplicas`.
- The `payload` of a schedule.
- The liveness and startup probes, and the three rollout fields.

For read-only dashboard users, the dashboard removes these items from a service: its host, source repository, commit, function code, variables, and pull secret.

### Automation

The dashboard actions are also available in the REST API with an API token. To deploy, use `POST /api/apps/<name>/deploy`. To save settings, use `POST /api/apps/<name>/update`. The versioned API has the same operations under `/api/v1`. See the [REST API](/docs/reference/rest-api/) reference.

The `geass` command line and the MCP server call that API. These commands create, deploy, read, and change a service: `geass apps create`, `geass deploy`, `geass status`, `geass env`, `geass scale`, `geass rollback`, and `geass functions triggers`. See [Command line](/docs/guides/command-line/), [MCP server](/docs/guides/mcp-server/), the [CLI reference](/docs/reference/cli/), and the [MCP tools](/docs/reference/mcp-tools/).

## Editing by hand

You can create and edit a `GeassApp` with `kubectl`. Keep these points in mind:

- Create the `GeassApp` in `geass-system`, and give it a name that no other service uses, in any project. The name also names the Deployment and Service in the namespace, and builds are named `<app>-build-<hash>`.
- A manifest with `spec.deploy.enabled: true` and no `geass.dev/*` annotations is deployed immediately. The dashboard shows it. A deploy that you make this way is not recorded as a dashboard deploy, and it sends no webhook events.
- If you later change a hand-made service in the dashboard, the dashboard marks a pending change. The operator then needs `geass.dev/last-deployed` to know what to run. Without this annotation, the service reports `Service has pending changes and no last deployed snapshot`. To store a snapshot, click **Deploy changes**.
- On a deployed service that the dashboard created, `kubectl edit` has two modes. When `geass.dev/pending-updates` is `"0"`, the operator reads your edit. When it is greater than zero, the operator continues to run the snapshot, and only live settings take effect. A dashboard deploy replaces the snapshot with the current spec, including your hand edits.
- The operator writes `status`, the finalizer, and sometimes `spec.ingress.host` (see the note under [Public access](#public-access)). The dashboard writes the annotations in the table above. Do not edit these fields and annotations.
- Use `kubectl patch` or `kubectl edit`. A `kubectl replace` with an old manifest overwrites the annotations and the finalizer.

To restart the pods of a deployed service without a change to its spec, set a new deploy revision:

```bash
kubectl annotate geassapp storefront-web -n geass-system \
  geass.dev/deploy-revision="$(date -u +%Y-%m-%dT%H:%M:%SZ)" --overwrite
```

When you delete a `GeassApp`, these objects are deleted:

- Its Deployment, Service, Ingress, HorizontalPodAutoscaler, ServiceMonitor, ConfigMaps, and Secrets in the namespace.
- The schedule CronJobs.
- The `<app>-wake` Service and the HTTPScaledObject of a service that slept.
- Its recorded [GeassDeployment](/docs/reference/resources/geass-deployment/) objects.
- The Secret `<app>-secrets` in `geass-system`.

Its builds and its `<app>-active` record are garbage collected.

## What happens under the hood

For a deployed service, the operator maintains these objects in the environment namespace `<project>-<environment>`. All of these objects are named after the service:

| Object | Name | Notes |
| --- | --- | --- |
| Deployment | `<app>` | One container named `app`, with the container port `http`. Pod restart policy `Always`. |
| Service | `<app>` | ClusterIP with one port named `http`, from `spec.port` to the container's `http` port. |
| Ingress | `<app>` | Only when `spec.ingress.host` is set. |
| HorizontalPodAutoscaler | `<app>` | Only with autoscaling. |
| ConfigMap | `<app>-config` | `spec.configData`, when set. |
| ConfigMap | `<app>-function` | The function's code, for functions. In `event` mode, it also holds the shim. |
| Secret | `<app>-secret` | The service's secret values, when set. |
| Secret | `<app>-registry-pull` | Git services only, when the build uses a registry credential. It is a copy of the credential, and the Deployment pulls the built image with it. Under the platform's `spec.tenancy.registry`, a copy that holds the platform's login is deleted from the namespace of an organization's service. Only the organization's own login is copied there. |
| Secret | `<app>-trigger` | A function's schedule token (key `token`), for a function in `event` mode or with triggers. |
| CronJob | `<app>-<trigger name>-<8 characters>` | One for each trigger of a function. Labeled `geass.dev/function-trigger=<trigger name>`. |
| Service | `<app>-wake` | Only when the service sleeps when idle. An `ExternalName` Service for the scale-to-zero proxy, which the Ingress points at. |
| HTTPScaledObject | `<app>` | Only when the service sleeps when idle. It tells the add-on the hosts, paths, idle time, and copy range. |
| ServiceMonitor | `<app>-metrics` | Only with `spec.metrics.enabled`. |

In `geass-system`, the service owns the [GeassBuild](/docs/reference/resources/geass-build/) objects named `<app>-build-<hash>` and the [GeassDeployment](/docs/reference/resources/geass-deployment/) `<app>-active`. These two kinds of objects have owner references to the service. The objects in the namespace do not have owner references, because Kubernetes does not allow an owner in another namespace. So the operator removes them itself.

The objects have the labels `geass.dev/managed-by=geass`, `geass.dev/kind=GeassApp`, `geass.dev/name=<app>`, `geass.dev/system-namespace=geass-system`, `geass.dev/project=<project>`, and `geass.dev/environment=<environment>`. The builds in `geass-system` have `geass.dev/kind=GeassBuild`, and also `geass.dev/app=<app>`. The push webhook, the build actions, and the status read use `geass.dev/app` to find the builds. The `<app>-active` record has `geass.dev/kind=GeassDeployment`, with the same `geass.dev/name`. Pods also have `app.kubernetes.io/name=<app>` and `app.kubernetes.io/managed-by=geass`. Inside the cluster, the service answers on `<app>.<namespace>.svc.cluster.local:<port>`.

The operator reconciles a service when the service changes. It also reconciles the service when the desired or available replica counts of its Deployment change, so the state of the service follows its pods. The operator writes `status` only when the status changed.

When you change `spec.project` or `spec.environment`, the operator deletes the workload from the old namespace, and creates it in the new namespace.

To inspect a service, use these commands:

```bash
kubectl get geassapps -n geass-system
kubectl get geassapp storefront-web -n geass-system -o yaml
kubectl get deploy,svc,ingress,hpa,cm,secret,cronjob -n storefront-production -l geass.dev/name=storefront-web
kubectl get pods -n storefront-production -l app.kubernetes.io/name=storefront-web
kubectl logs -n storefront-production -l app.kubernetes.io/name=storefront-web -c app --tail=100
# Builds and the <app>-active record, which the operator creates
kubectl get geassbuilds,geassdeployments -n geass-system -l geass.dev/name=storefront-web
# Deploy records, which the dashboard creates
kubectl get geassdeployments -n geass-system -l geass.dev/app=storefront-web
```

## Troubleshooting

**The service stays `Service is configured and waiting for deployment`.** The service is a draft. Click **Deploy changes**, or set `spec.deploy.enabled: true`.

**`Service has pending changes and no last deployed snapshot`.** The service is deployed, `geass.dev/pending-updates` is greater than zero, and `geass.dev/last-deployed` is missing. This happens when a service that was created with `kubectl` is edited in the dashboard. Click **Deploy changes**. The deploy stores the snapshot and clears the pending state.

**`project "..." does not exist`, `is not ready` or `has no ... environment`.** The service points at a project that is missing or not `Ready` yet, or that does not have the environment. Check `kubectl get geassproject <project> -n geass-system` and `spec.environments`. These errors count toward the retry limit. If the service reaches `ProvisionFailed` while it waited, click **Retry** after the project is ready.

**`This service needs about ... CPU and ... memory` or `There is no ready node in ...`.** The cluster cannot place the service. Lower `spec.resources` or `spec.replicas`, add a node, or change `spec.runOn.tags` or the project's region. If a message says that nodes are reserved, the matching nodes are kept for work that asks for their tag. The operator checks capacity only when it first creates the Deployment. The dashboard checks again each time that you deploy. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**`Service is starting` does not go away.** The pods are not available, and no container is stuck in a start failure. Run `kubectl get pods -n <namespace> -l app.kubernetes.io/name=<app>` and `kubectl describe pod`. These are the common causes:

- A readiness probe that fails. The probe path or port is wrong, or a function does not serve HTTP on `PORT`.
- A pod that cannot be scheduled.
- A pod stuck in `ContainerCreating` with a mount error. A ConfigMap or Secret named in `configMapRefs` or `secretRefs` does not exist in the environment namespace.

**`The service cannot start: app is <reason>`.** A container of the service is stuck. The dashboard shows **Unavailable**. After the retry limit (see [Conditions](#conditions)), the dashboard shows **Failed** with `ProvisionFailed`. Fix the cause, and then click **Retry** or change the spec. The cause depends on the reason:

- `ErrImagePull`, `ImagePullBackOff`, or `InvalidImageName`: the image name is wrong, or the registry needs a pull Secret. `spec.source.image.pullSecret` must name a Secret in the environment namespace, not in `geass-system`. For a Git service, `status.resolvedImage` is `<repository>@sha256:…`. Check that the Secret `<app>-registry-pull` exists in the environment namespace. This Secret is made when the build uses a credential. Also check that the credential can pull from that repository.
- `CreateContainerConfigError` or `CreateContainerError`: a ConfigMap or Secret named in `env` or `envFrom`, or a key in it, does not exist in the environment namespace. A service that reads the connection Secret of a database or bucket that is still starting is not in this state permanently. It says `Waiting for database …` until the Secret is made, and then it starts.
- `CrashLoopBackOff`: the process exits. Read `kubectl logs --previous` and the **Deploy Logs**. The container might have been stopped for memory: `kubectl describe pod` then says `OOMKilled`, exit code 137. In that case, the service says `Ran out of memory` instead. Raise the memory that the service can use, or the memory that it reserves.

**`Provisioning failed after repeated errors`.** The operator gave up after 5 counted errors or 3 minutes. The recent errors are in `status.provision.provisionAttempts[].errors`. Fix the cause, and then click **Retry** or change the spec.

**`source.git.connectionRef, repository, and branch are required`.** A Git service was deployed with an empty connection, repository, or branch. This error fails immediately, without retries. Connect the project to GitHub, select a repository, and then deploy again.

**`Git source is waiting for a successful build` does not go away.** Read the build: `kubectl get geassbuilds -n geass-system -l geass.dev/app=<app>`. See [GeassBuild](/docs/reference/resources/geass-build/#troubleshooting).

**A push to GitHub does not rebuild the service.** The webhook answers `"triggered":0` when no service matched. For a push to rebuild the service, all of these must be true:

- The service is deployed.
- The service builds the pushed branch of the pushed repository.
- The service does not pin a commit.
- The service watches a file that the push changed (`watchPatterns`).
- The service already has a build: `kubectl get geassbuilds -n geass-system -l geass.dev/app=<app>`.

See [A push does not trigger a build](/docs/guides/github-and-builds/#troubleshooting). To build by hand, see [GeassBuild](/docs/reference/resources/geass-build/#re-run-a-build).

**A sleeping service never wakes, or never sleeps.** Read the `ScaleToZero` condition: `kubectl get geassapp <app> -n geass-system -o jsonpath='{.status.conditions[?(@.type=="ScaleToZero")]}'`. A reason other than `Active` means that the service is always on. See [Sleep when idle](/docs/guides/scale-to-zero/#troubleshooting).

**A function's schedule does not run.** Schedules start with the next deploy. Check that the service is deployed and not pending. Then run `kubectl get cronjobs -n <namespace> -l geass.dev/name=<app>` and `kubectl get jobs -n <namespace> -l geass.dev/function-trigger=<trigger name>`. See [Functions](/docs/guides/functions/#troubleshooting).

**`RolloutFailed`, and the service never recovers.** With `pauseOnFailure`, the operator pauses, and it does not clear the flag automatically. Clear `status.rolloutPaused` as shown under [Rollout and health](#rollout-and-health).

**The service has no public URL, or the URL does not load.** Check these items, in order:

1. `spec.ingress.host` is set, and `kubectl get ingress <app> -n <namespace>` shows the host.
2. The DNS record of the host points at your cluster. With `dnsVerification` on, `status.dnsVerified` is `false` until the host resolves.
3. For HTTPS, cert-manager and the `letsencrypt-prod` ClusterIssuer exist and are ready (`kubectl get clusterissuer letsencrypt-prod`). Also, `kubectl get certificate -n <namespace>` shows the certificate. The operator creates that issuer only when `spec.addons.certManager.acmeEmail` is set on the [GeassCluster](/docs/reference/resources/geass-cluster/). Without it, the cluster reports `ClusterIssuerReady: False`, and no certificate is issued. See [Domains and TLS](/docs/guides/domains-and-tls/).

**`metrics are enabled but Prometheus Operator is not installed in this cluster`.** The cluster has no ServiceMonitor kind. Turn off **Enable metrics**, or install the monitoring add-on on the cluster.

**A plain shared variable reaches a service that did not select it.** This is how the current code works. See the caution under [Variables and files](#variables-and-files).

## Related

- [Apps guide](/docs/guides/apps/) and [First app](/docs/getting-started/first-app/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [GeassBuild](/docs/reference/resources/geass-build/), [GeassDeployment](/docs/reference/resources/geass-deployment/), [GeassConsoleSession](/docs/reference/resources/geass-console-session/)
- [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/)
- [Functions](/docs/guides/functions/), [Sleep when idle](/docs/guides/scale-to-zero/), [Environment variables](/docs/guides/environment-variables/), [Domains and TLS](/docs/guides/domains-and-tls/)
- [Command line](/docs/guides/command-line/) and [MCP server](/docs/guides/mcp-server/)
- [Resources overview](/docs/reference/resources/overview/)
