---
title: "GeassApp"
description: "Reference for the GeassApp resource: image, Git and function services, schedules, scaling, sleeping, ingress, variables, probes and the deploy workflow."
sidebar:
  order: 2
---

A `GeassApp` is a service: one container image, one Git repository built into an image, or one inline function, running in an environment of a [GeassProject](/docs/reference/resources/geass-project/). The operator turns it into a Deployment, a Service and, when you set a host, an Ingress in the project's namespace. A function with schedules also gets a CronJob for each, and a service can be set to [sleep when idle](/docs/guides/scale-to-zero/).

The dashboard is the normal way to create and change services. It writes the `GeassApp` for you and manages a draft and deploy workflow on top of it. You can also create one with `kubectl` and the operator runs it, with the caveats in [Editing by hand](#editing-by-hand). Every `GeassApp` lives in `geass-system`, and its name is unique across all projects.

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

Every example needs a project that is `Ready` and has the environment named in `spec.environment`. See [GeassProject](/docs/reference/resources/geass-project/).

### An image service

This service runs a prebuilt image with two replicas, a public host and HTTPS.

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

A Git service is built in the cluster with Kaniko and then deployed. It needs a [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/) and a registry that the cluster can push to and pull from. The registry comes from `spec.build.registry` or from the platform configuration. Geass has no registry of its own: a service without one waits with `no container registry is set for builds`.

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

A function runs inline source code on a managed runtime. In the default `http` mode your code must serve HTTP on the port in the `PORT` environment variable.

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

A function in `event` mode exports a handler and Geass serves HTTP for it. Each entry in `triggers` calls the function on a schedule, in UTC. See [Functions](/docs/guides/functions/).

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

This service stops after 10 minutes without a request and starts again on the next one. It needs the serverless add-on on the cluster and a public host.

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

This example sets most of the optional blocks. The Secret and connection names are placeholders for objects you create.

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

Required fields are marked. Every other field is optional. Fields that are standard Kubernetes types name the type and are not expanded.

### Identity and source

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.project` | string | none | Required, at least 1 character | The ID (`metadata.name`) of the owning [GeassProject](/docs/reference/resources/geass-project/). It is not the display name. |
| `spec.environment` | string | none | Required. Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 63 characters at most | An environment listed in the project's `spec.environments`. The namespace is `<project>-<environment>`. |
| `spec.source` | object | none | Required | Exactly one of `image`, `git` or `function`. The operator enforces this when the service is deployed. A draft may hold an incomplete source. |
| `spec.source.image.image` | string | none | Required inside `image` | A container image reference. The dashboard accepts references that match `^[A-Za-z0-9][A-Za-z0-9._/@:-]*$`. |
| `spec.source.image.pullSecret.name` | string | none | none | A `kubernetes.io/dockerconfigjson` Secret for pulling the image. It is resolved in the environment's namespace. |
| `spec.source.git.connectionRef.name` | string | none | Required inside `git`, and must not be empty on deploy | The [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/) to clone with, in `geass-system`. |
| `spec.source.git.repository` | string | none | Required, and must not be empty on deploy | The GitHub repository as `owner/name`. |
| `spec.source.git.branch` | string | none | Required, and must not be empty on deploy | The branch to build. |
| `spec.source.git.commit` | string | empty | none | Pins the build to one commit. When empty, each build resolves the head of `branch`. The dashboard never sets it. |
| `spec.source.git.dockerfile` | string | `Dockerfile` | none | The Dockerfile path, relative to `context`. A path that already starts with `context` is taken as relative to the repository root, and a leading slash is ignored. |
| `spec.source.git.context` | string | `.` | A path that leaves the repository is refused when the service is deployed | The root directory of the build: Kaniko builds in `/workspace/<context>`. A leading slash means the repository root, and `.` or empty is the root itself. The dashboard calls this field **Root directory**. |
| `spec.source.git.waitForCI` | boolean | `false` | none | Waits until every GitHub check run of the commit has completed successfully before building. |
| `spec.source.git.watchPatterns` | list of strings | empty | none | Which pushes build. Gitignore-style patterns, matched from the repository root against the files the push changed; `!` takes files back out. A push builds only when it changes a file they select. Empty means every push builds. A live setting: it applies the moment it is saved. The dashboard calls this field **Watch paths**. |
| `spec.source.function.runtime` | string | none | Required. `bun`, `node` or `python` | The language runtime. |
| `spec.source.function.code` | string | none | Required. 262144 characters at most | The program. In `http` mode it must serve HTTP on `PORT`. In `event` mode it must export a handler. |
| `spec.source.function.mode` | string | `http` | `http` or `event` | What `code` is. `http` is a program that serves HTTP itself. `event` is a handler, a function that is given an event for each request and returns the response, with Geass serving HTTP for it. |
| `spec.source.function.triggers` | list of objects | empty | At most 8, unique by `name` | Schedules that call the function. Each is one CronJob. |
| `spec.source.function.triggers[].name` | string | none | Required. `^[a-z0-9]([-a-z0-9]{0,30}[a-z0-9])?$`, so 1 to 32 characters | Names the schedule and its CronJob. |
| `spec.source.function.triggers[].schedule` | string | none | Required. 100 characters at most. Five fields, or `@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`, `@annually`, `@midnight` | A cron expression in UTC. Numbers, ranges, steps and lists work. Names such as `MON` are refused. |
| `spec.source.function.triggers[].path` | string | `/` | Starts with `/`. 256 characters at most | The path that is called. |
| `spec.source.function.triggers[].payload` | string | `{"trigger":"cron","name":"<name>"}` | Valid JSON. 4096 characters at most | The JSON body of the request. |

For a monorepo, set `context` to the folder of the service (`apps/api`) and keep `dockerfile` relative to it (`Dockerfile`). A `dockerfile` that is written in full from the repository root (`apps/api/Dockerfile`) still works. `watchPatterns` decide which pushes rebuild the service. They apply to pushes only: a deploy, and a change to a build setting, build regardless. See [Root directory and Watch paths](/docs/guides/github-and-builds/#root-directory-and-watch-paths) for the patterns.

A Git service resolves the branch to a commit through the GitHub API, clones it with the connection's token and builds the image from the Dockerfile. When the build succeeds, the Deployment runs the repository the build pushed to, at the digest the build recorded (`<repository>@sha256:…`). See [GeassBuild](/docs/reference/resources/geass-build/) for the build lifecycle and [GitHub and builds](/docs/guides/github-and-builds/) for the guide.

#### Functions

| Runtime | Image | File | Command in `http` mode | Command in `event` mode |
| --- | --- | --- | --- | --- |
| `bun` | `oven/bun:1-alpine` | `index.ts` | `bun run /function/index.ts` | `bun run /function/geass-shim.ts` |
| `node` | `node:22-alpine` | `index.mjs` | `node /function/index.mjs` | `node /function/geass-shim.mjs` |
| `python` | `python:3.12-alpine` | `main.py` | `python -u /function/main.py` | `python -u /function/geass-shim.py` |

The operator stores the code in the ConfigMap `<app>-function` and mounts it read-only at `/function`. In `event` mode the ConfigMap also holds the shim, a small program Geass keeps for each runtime. The shim serves HTTP, turns each request into an event, calls the `handler` your file exports (a function named `handler` in Python) and turns what it returns into the response. The container runs the shim, which imports your file. It sets `PORT` to `spec.port` unless `spec.env` already defines `PORT`. It also turns off the service account token for function pods, so your code gets no Kubernetes API access. A fingerprint of the runtime, the mode, the shim version and the code is added to the pod template as the annotation `geass.dev/function-hash`, so editing the code or the mode restarts the pods. `spec.build.command` replaces the command when set. In `event` mode that means the shim does not run. See [Functions](/docs/guides/functions/).

A function in `event` mode, or with `triggers`, also gets a random token. The operator keeps it in the Secret `<app>-trigger` (key `token`) in the environment namespace and gives it to the container as `GEASS_TRIGGER_TOKEN`, unless `spec.env` already defines that name. Each schedule's Job sends it in the header `x-geass-trigger-token`, which is how the shim tells a scheduled call from a visitor. The pod template carries the annotation `geass.dev/trigger-token-fingerprint`, so a token that is made again restarts the pods.

Each trigger becomes a CronJob in the environment namespace named `<app>-<trigger name>-<8 characters>` (52 characters at most), with the time zone `Etc/UTC`. At each time it fires, a Job runs `curl` and sends `POST <path>` to the function with the payload as the body and the headers `x-geass-trigger-token` and `x-geass-trigger-name`. The request goes through the scale-to-zero proxy when the service sleeps when idle, and straight to the Service otherwise. A run fails when the answer is `400` or above or does not arrive within 120 seconds, and is tried once more. Runs do not overlap, and a run that could not start within 300 seconds of its time is skipped. The triggers are part of the source, so they follow the [draft and deploy workflow](#the-draft-and-deploy-workflow): they start with the next deploy.

Two limits apply to the code. The raw code must be under 256 KB. The JSON-escaped code must also stay under 200 KB, because every deploy stores the whole spec, code included, in an annotation. Quotes and line breaks take extra space. The errors are `function code must be under 256 KB` and `function code is too large to deploy: it must stay under 200 KB once saved with the service (quotes and line breaks take extra space)`.

### Replicas, resources and placement

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.replicas` | integer | `1` | 0 or more | The Deployment's replica count. The dashboard's API accepts 0 to 1000, and the **Replicas** field in its form starts at 1. With 0 the service runs no pods and still reports ready. It does not apply while the service sleeps when idle. |
| `spec.port` | integer | `8080` | 1 or more | The container port, the Service port and the Ingress backend port. The container port is named `http`. |
| `spec.resources` | `corev1.ResourceRequirements` | 100m CPU and 128Mi memory, as requests and limits | none | CPU and memory of the container. The operator uses the default whenever `requests` is empty, and ignores `limits` in that case. The dashboard offers fixed sizes (0.1 to 2 CPU, 128 MB to 4 GB) and sets requests equal to limits. |
| `spec.autoscaling.maxReplicas` | integer | none | Required inside `autoscaling`. 1 or more | The upper bound. |
| `spec.autoscaling.minReplicas` | integer | `1` | 1 or more | The lower bound. It must not exceed `maxReplicas`. |
| `spec.autoscaling.targetCPUUtilization` | integer | none | 1 to 100 | The target average CPU utilization, measured against the container's CPU request. |
| `spec.runOn.tags` | list of strings | empty | At most 8 unique items. Each matches `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 40 characters at most | Node tags the service must run on. A node needs all of them. |
| `spec.scaleToZero.enabled` | boolean | none | Required inside `scaleToZero` | Lets the service sleep when it is idle and wakes it on the next request. |
| `spec.scaleToZero.idleSeconds` | integer | `300` | 30 to 86400 | How long the service goes without a request before it is stopped. |
| `spec.scaleToZero.maxReplicas` | integer | `3` | 1 to 100 | How many copies it may scale up to under load while awake. |

The operator creates a HorizontalPodAutoscaler named `<app>` only when `maxReplicas` is greater than 1 and `targetCPUUtilization` is set. Otherwise it deletes any existing one. An invalid `minReplicas` produces the error `autoscaling minReplicas must be between 1 and maxReplicas`. The Deployment's replica count is set from `spec.replicas` on every reconcile, except while the service sleeps when idle. The capacity check for a deploy counts the larger of `replicas` and `maxReplicas` of `autoscaling`.

The service runs in the project's region (the cluster's default region when the project has none, and on any node when neither is set), and `runOn.tags` narrows that to nodes that carry every tag. The tags the project requires, if any, are added to the service's own. The operator turns the placement into a node selector on `topology.kubernetes.io/region` and `tag.geass.dev/<tag>`, and adds tolerations so the service can use nodes that reserve those tags. When the cluster cannot place the service, the `Ready` message says why. See [Scaling and placement](/docs/guides/scaling-and-placement/) and [Compliance tags](/docs/guides/compliance-tags/).

#### Sleeping when idle

With `scaleToZero.enabled`, the service is stopped after `idleSeconds` without a request and started again by the next request to its public host, which is held for the few seconds it takes to start. While it sleeps, the scale-to-zero add-on decides how many copies run, from none up to `maxReplicas`. The operator then does not set the Deployment's replica count, so `spec.replicas` does not apply. It switches back to `spec.replicas` when you turn sleeping off.

The service sleeps only when all of these hold:

- The cluster's serverless add-on is installed (`spec.addons.serverless.enabled` on the [GeassCluster](/docs/reference/resources/geass-cluster/)).
- The service has a public host: `ingress.host` or a host in `ingress.rules`.
- `spec.autoscaling` is not set.

Otherwise the service runs always on, as it did before, and the `ScaleToZero` condition says why. After a deploy of a version that changes the pods, the operator keeps one copy running until a copy of the new version has started, so a version that cannot start fails the deploy and not a visitor. See [Sleep when idle](/docs/guides/scale-to-zero/) for the full behavior, and [Conditions](#conditions) for the messages.

### Public access

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.ingress.host` | string | empty | none | The public hostname. When empty, the operator creates no Ingress. |
| `spec.ingress.path` | string | `/` | none | The path prefix of the route. |
| `spec.ingress.tlsEnabled` | boolean | `false` | none | Serves the host over HTTPS with a certificate from cert-manager. |
| `spec.ingress.dnsVerification` | boolean | `false` | none | Checks that the host resolves and reports the result in `status.dnsVerified` and `status.dnsMessage`. |
| `spec.ingress.rules[].host` | string | none | Required inside a rule | The host of one route. |
| `spec.ingress.rules[].path` | string | `/` | none | The path prefix of that route. |

The Ingress is named `<app>`, uses the `traefik` class and sends traffic to the Service port `spec.port`. For a service that sleeps when idle it sends traffic to the Service `<app>-wake` on port 8080 instead, which is a name for the scale-to-zero proxy. With `tlsEnabled` it carries the annotations `cert-manager.io/cluster-issuer: letsencrypt-prod` and `traefik.ingress.kubernetes.io/router.entrypoints: websecure`, and uses the TLS Secret `<app>-tls`. Without it, the entrypoint is `web`. See [Domains and TLS](/docs/guides/domains-and-tls/).

`rules` replaces the `host` and `path` pair as the list of routes. The Ingress still exists only when `ingress.host` is not empty. The dashboard never sets `rules`.

:::note
When the platform has a ready Cloudflare connection and `ingress.host` is empty, the operator writes a generated hostname into `spec.ingress.host` and sets `tlsEnabled` to `false`. The name is `<app>-<project>.<root domain>` for `production` and `<app>-<project>-<environment>.<root domain>` for other environments. Clearing the host in the dashboard therefore brings the generated one back.
:::

### Variables and files

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.env` | list of `corev1.EnvVar` | empty | none | Environment variables. Values can come from the environment namespace's ConfigMaps and Secrets with `valueFrom`. The dashboard writes entries here when you use **Add Reference** to connect a database or bucket. |
| `spec.envFrom` | list of `corev1.EnvFromSource` | empty | none | ConfigMaps and Secrets in the environment namespace to load as environment variables. |
| `spec.sharedVariableRefs` | list of strings | empty | Each matches `^[A-Z_][A-Z0-9_]*$` | Names of the project's shared variables to inject. See the caution below. |
| `spec.configMapRefs` | list of `corev1.LocalObjectReference` | empty | none | ConfigMaps in the environment namespace to mount at `/config/refs/<name>`. |
| `spec.secretRefs` | list of `corev1.LocalObjectReference` | empty | none | Secrets in the environment namespace to mount at `/secrets/refs/<name>`. |
| `spec.configData` | map of strings | empty | none | Plain variables. The operator stores them in the ConfigMap `<app>-config`. The dashboard writes the variables you add under **Variables** here. |
| `spec.secretData` | map of strings | empty | none | Secret values stored in the resource itself. The dashboard does not use it any more. Prefer `secretRef`. |
| `spec.secretRef.name` | string | none | none | A Secret in `geass-system` that holds the service's secret values. The dashboard keeps one named `<app>-secrets`. When set, `secretData` is ignored. |

How the values reach the container:

| Source | In the container |
| --- | --- |
| `spec.env` | Environment variables. They win over every `envFrom` source. |
| `spec.envFrom` | Environment variables from your ConfigMaps and Secrets. |
| Shared variables | Environment variables from the ConfigMap `geass-shared-variables` and the Secret `geass-shared-secrets` in the namespace. |
| `spec.configData` | Environment variables from `<app>-config`, and files under `/config`. |
| `spec.secretData` or `spec.secretRef` | Environment variables from `<app>-secret`, and read-only files under `/secrets`. |
| `spec.configMapRefs` | Files under `/config/refs/<name>`. |
| `spec.secretRefs` | Files under `/secrets/refs/<name>`. |

The `envFrom` sources are ordered as `spec.envFrom`, shared variables, `<app>-config` and `<app>-secret`. When one key appears in several sources, the later source wins, so a service variable overrides a shared one of the same name.

Values in `spec.configData` and `spec.secretData` are copied into the namespace when the service is deployed. `spec.secretRef` is read from `geass-system` and copied into the Secret `<app>-secret` in the namespace.

:::caution
`spec.sharedVariableRefs` filters secret shared variables only. When the list is empty, the service gets every shared variable of its environment. When the list is not empty, the service gets the listed secret variables and the listed plain variables as individual entries, and it still receives the whole `geass-shared-variables` ConfigMap, so every plain shared variable of the environment reaches the container. Do not rely on `sharedVariableRefs` to keep a plain value away from a service.
:::

:::caution
Values in `spec.secretData` are readable by anyone who can read the `GeassApp`. Dashboard viewers do not see them, because the dashboard removes `secretData`, `secretRef`, `secretRefs`, `envFrom`, `configData` and the values of `env` for viewer sessions.
:::

### Where referenced objects must live

Fields that reference other objects resolve them in different namespaces. A wrong namespace is the most common reason a service does not start.

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
| `spec.metrics.enabled` | boolean | `false` | none | Creates a ServiceMonitor named `<app>-metrics` so Prometheus scrapes the service. It needs the Prometheus Operator CRDs in the cluster. |
| `spec.metrics.path` | string | `/metrics` | none | The HTTP path to scrape. |
| `spec.metrics.port` | string | `http` | none | The name of the Service port to scrape. |
| `spec.logs.archiveStoreRef.name` | string | none | none | An object store that receives the log of each build. See [GeassBuild](/docs/reference/resources/geass-build/). |
| `spec.logs.retentionHours` | integer | none | 1 or more | Stored, but nothing reads it. See the caution below. |

The operator creates only one Service port, named `http`. The ServiceMonitor endpoint refers to a Service port by name, so `http` is the only value of `metrics.port` that matches. See [Observability](/docs/guides/observability/).

:::caution
`spec.logs.retentionHours` is not read by the operator or the dashboard. The CRD describes it as the retention of archived logs, but nothing uses it. Only `archiveStoreRef` has an effect, and only for build logs.
:::

### Build and start command

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.build.command` | list of strings | empty | none | Replaces the image's entrypoint. For a function it replaces the command Geass runs, which in `event` mode is the shim. |
| `spec.build.args` | list of strings | empty | none | Replaces the image's arguments. |
| `spec.build.workingDir` | string | empty | none | The container's working directory. |
| `spec.build.registry.repository` | string | empty | none | The destination image reference for Git builds, used as written. When empty, the operator uses `spec.registry.repository` of the platform configuration. When neither is set, the service waits with `no container registry is set for builds`. |
| `spec.build.registry.credentialRef.name` | string | none | none | The Secret that holds registry credentials for the build. When empty and the service uses the platform's repository, the platform configuration's credentials are used. The nodes pull the image with a copy of the credential, the Secret `<app>-registry-pull` in the environment namespace. |
| `spec.build.cache` | boolean | `false` | none | Passes `--cache=true` to Kaniko. |
| `spec.build.argsFrom` | list of `corev1.LocalObjectReference` | empty | none | Stored, but nothing reads it. |

The dashboard splits **Command** and **Arguments** on whitespace, so it cannot express an argument that contains a space. Set `command` and `args` as lists with `kubectl` when you need that.

### Rollout and health

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.deploy.enabled` | boolean | `false` | none | Whether the operator creates and maintains the workload. `false` keeps the service as a draft. |
| `spec.deploy.restartPolicy` | string | `Always` | Only `Always` | The pod restart policy. `Always` is the only value Deployments support. |
| `spec.deploy.readinessProbe` | `corev1.Probe` | none | none | Decides whether the pod receives traffic. |
| `spec.deploy.livenessProbe` | `corev1.Probe` | none | none | Restarts an unhealthy container. |
| `spec.deploy.startupProbe` | `corev1.Probe` | none | none | Holds off the other probes until the container has started. |
| `spec.deploy.failureThreshold` | integer | none | 1 or more | The number of reconcile passes that find the Deployment not ready after which the operator reports `RolloutFailed`. Unset means never. |
| `spec.deploy.pauseOnFailure` | boolean | `false` | none | Sets `status.rolloutPaused` when the threshold is reached. |
| `spec.deploy.rollbackOnFailure` | boolean | `false` | none | Adds the `RollbackReady` condition when the threshold is reached. It does not roll anything back. |

The dashboard only edits the HTTP readiness probe, through **Healthcheck path**. It builds a probe that calls that path on `spec.port` every 10 seconds with a 2 second timeout and 3 failures allowed. Saving the **Deploy** section with an empty path removes `readinessProbe`, including a probe you set by hand. The other probes and the three fields after `startupProbe` are not in the dashboard.

:::caution
`failureThreshold`, `pauseOnFailure` and `rollbackOnFailure` only work together, and only partly.

- Each reconcile pass that finds the Deployment not ready adds one to `status.healthCheckFailures`. The operator does not measure time here. Passes start when the object changes, and the counter is part of the object, so a threshold can be reached within seconds. A ready Deployment resets the counter to zero.
- A pass does not count when a container of the service is stuck in a start failure (`CrashLoopBackOff`, `ImagePullBackOff`, `ErrImagePull`, `InvalidImageName`, `CreateContainerConfigError` or `CreateContainerError`) or the Deployment ran out of time to progress. The operator then reports `The service cannot start: …` (see [Conditions](#conditions)) and stops there. So `RolloutFailed` is reached by a service that is slow or never ready, for example one whose readiness probe fails or whose pod cannot be scheduled, and not by one that crashes or cannot pull its image.
- At the threshold the operator sets `Ready` to `False` with reason `RolloutFailed`.
- With `pauseOnFailure`, it also sets `status.rolloutPaused` to `true`. The operator then skips all workload work. No code clears the flag, because the step that would clear it is skipped while the flag is set. To resume, clear it yourself: `kubectl patch geassapp <name> -n geass-system --subresource=status --type=merge -p '{"status":{"rolloutPaused":false,"healthCheckFailures":0}}'`.
- With `rollbackOnFailure`, the operator only adds `RollbackReady` with reason `RollbackRequested`. There is no automatic rollback, and nothing removes that condition.
:::

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.targetNamespace` | string | The namespace the workload runs in. |
| `status.url` | string | The public URL, built from `spec.ingress.host`, the scheme and the path. Empty when there is no host. |
| `status.resolvedImage` | string | For Git services, the image of the latest successful build: the repository the build pushed to, without its tag, at the digest it recorded (`<repository>@sha256:…`). When set, the Deployment runs this value instead of `spec.source.image`. See [GitHub and builds](/docs/guides/github-and-builds/#how-a-build-runs). |
| `status.activeBuild` | string | The name of the [GeassBuild](/docs/reference/resources/geass-build/) whose image is deployed. |
| `status.currentBuild` | string | The name of the build for the service's current settings, which a push or a rebuild runs again. It is not always the newest, or the one that is deployed. |
| `status.dnsName` | string | The value of `spec.ingress.host`. |
| `status.traefikTarget` | string | The address of the Ingress, when Kubernetes reports one. |
| `status.dnsVerified` | boolean | Always `true` when `dnsVerification` is off. When it is on, `true` if the host resolves from the operator. |
| `status.dnsMessage` | string | Set only while `dnsVerification` is on: `DNS hostname is configured for verification` when the host resolves, `DNS hostname is required` when it does not. The second message is shown even when a host is set. |
| `status.healthCheckFailures` | integer | The count described under [Rollout and health](#rollout-and-health). |
| `status.rolloutPaused` | boolean | Whether the operator has paused the service. |
| `status.reconcileRetry` | object | `reconcileFailures`, `reconcileFailureStarted` and `reconcileFailureGeneration`, the retry bookkeeping. |
| `status.provision.activeProvisionAttempt` | string | The ID of the attempt in progress. |
| `status.provision.provisionAttempts` | list, at most 8 | The recent deploys, newest first. Each has `id`, `startedAt`, `completedAt`, `phase` (`Running`, `Succeeded` or `Failed`), `summary`, `errors` (at most 12 while running, 5 kept afterwards, each at most 500 characters), `buildRef` and `deploymentRef`. |
| `status.conditions` | list | `Ready`. `ScaleToZero`, only on a service that asks to sleep when idle. `RollbackReady`, for `rollbackOnFailure`. |

The dashboard starts an attempt each time you deploy. The operator adds the errors it meets and closes the attempt as `Succeeded` with the summary `Provisioned successfully`, or as `Failed`. An attempt that succeeded can still list errors. Those are temporary errors, such as a write conflict, that the operator retried.

### Conditions

| Type | Status | Reason | Message | Meaning |
| --- | --- | --- | --- | --- |
| `Ready` | `True` | `AppReady` | `Application is ready` | The Deployment has at least the desired number of available replicas. |
| `Ready` | `False` | `ReconcileError` | `Service is configured and waiting for deployment` | The service is a draft. Deploy it. |
| `Ready` | `False` | `ReconcileError` | `Service has pending changes and no last deployed snapshot` | The service is deployed and has pending changes, but `geass.dev/last-deployed` is missing. See [Troubleshooting](#troubleshooting). |
| `Ready` | `False` | `ReconcileError` | `Git source is waiting for a successful build` | The build has not succeeded yet. |
| `Ready` | `False` | `ReconcileError` | `Service is starting` | The Deployment exists but is not yet available. |
| `Ready` | `False` | `ReconcileError` | `The service cannot start: app is CrashLoopBackOff` | A container is in a state that does not clear without a change: `CrashLoopBackOff`, `ImagePullBackOff`, `ErrImagePull`, `InvalidImageName`, `CreateContainerConfigError` or `CreateContainerError`. When the Deployment ran out of time to progress, the text ends `it did not become ready in time` instead. Read the pod's events. |
| `Ready` | `False` | `ReconcileError` | `The new version of the service cannot start: …` | A service that sleeps when idle was deployed with a change to its pods, and the copy of the new version cannot start. The deploy fails, and not the first visitor. |
| `Ready` | `False` | `ReconcileError` | `The service was woken but its copy cannot start: …` or `The service was woken but has not started within 10m0s` | A request woke a sleeping service and its copy does not start. |
| `Ready` | `False` | `Waking` | `Starting after a request` | A sleeping service is starting after a request. It does not count against the retry limit. |
| `Ready` | `False` | `ReconcileError` | Project errors, such as `project "x" is not ready` | The project does not exist, is not ready, or has no such environment. |
| `Ready` | `False` | `ReconcileError` | Source errors, such as `one of spec.source.image, spec.source.git, or spec.source.function is required` | The source is invalid. |
| `Ready` | `False` | `ReconcileError` | Capacity or placement text, such as `There is no ready node in the eu region to run this service. Add a node there, or choose another region` | The cluster cannot place the service. |
| `Ready` | `False` | `ReconcileError` | An error from creating an object, for example `metrics are enabled but Prometheus Operator is not installed in this cluster` | A Kubernetes object could not be created or updated. |
| `Ready` | `False` | `RolloutFailed` | `Deployment failed the configured health-check threshold` | `spec.deploy.failureThreshold` was reached. |
| `Ready` | `False` | `ProvisionFailed` | `Provisioning failed after repeated errors. Update settings or platform configuration, then save or redeploy to try again.` | The operator gave up. See below. |
| `Ready` | `False` | `ReconcileError` | `Retrying provisioning` | You clicked **Retry**. The operator is trying again. |
| `RollbackReady` | `True` | `RollbackRequested` | `Rollback was requested after rollout failure` | `rollbackOnFailure` is on and the threshold was reached. Informational only. |
| `ScaleToZero` | `True` | `Active` | `Sleeps after idle, wakes on the next request` | The service sleeps when idle. The condition exists only when `spec.scaleToZero.enabled` is true. |
| `ScaleToZero` | `False` | `AddonNotInstalled` | `Scale to zero needs the serverless add-on: set spec.addons.serverless.enabled on the GeassCluster` | The service asked to sleep and runs always on. Turn the add-on on. |
| `ScaleToZero` | `False` | `NoPublicHost` | `Scale to zero needs a public host: it is the requests to it that wake the service` | Give the service a host. |
| `ScaleToZero` | `False` | `AutoscalingSet` | `Scale to zero decides how many copies run, so CPU autoscaling has to be turned off` | Remove `spec.autoscaling`. |

The operator retries a not ready service every 30 seconds. Waiting states (a draft, a pending build, a service that is starting, a pending change, a sleeping service that is waking) never count against it. Other errors count, including `The service cannot start: …`. After 5 in a row, or 3 minutes, the service becomes `ProvisionFailed` and the operator stops until `metadata.generation` changes. Some errors skip the count and fail at once, for example `source.git.connectionRef, repository, and branch are required`. For a deploy announced to webhooks, `ProvisionFailed` also sends the `deployment.failed` event. Click **Retry** on the service, or change its spec, to start again.

## How the dashboard maps to this resource

### Creating a service

On the **Resources** tab of a project, click **+ Add resource**. The choices that create a service are:

| Choice | What it creates |
| --- | --- |
| **GitHub Repository** | A draft with `source.git`. You pick a repository from the project's GitHub connection. The branch defaults to the repository's default branch, or `main`. |
| **Docker Image** | A draft with `source.image`. |
| **Function** | A draft with `source.function`. You pick a runtime, and the dashboard fills in starter code for a server (`mode: http`). It opens the **Code** tab. |
| **Template** | One draft per service and database of the template. A function keeps its mode and schedules. See [Templates](/docs/guides/templates/). |

The dashboard generates the resource name, such as `morning-beach`. With the REST API you can pass `name` yourself. A draft has `spec.deploy.enabled: false`. The dashboard sets `spec.port` to 8080, `spec.replicas` to 1 and `spec.resources` to 0.1 CPU and 128 MB. A draft created through the REST API with no source at all gets the image `nginx:alpine`. The command line does the same with `geass apps create`, which saves a draft; see [Command line](/docs/guides/command-line/).

### The draft and deploy workflow

The dashboard does not apply your edits straight away. It saves them and waits for you to deploy, so that one deploy changes everything at once.

1. A new service is a draft, shown with the status **Draft**. The top bar shows how many changes are pending (for example `2 pending`, or `Up to date`).
2. You edit **Settings** and **Variables**. Each save records what changed.
3. You click **Deploy changes** in the top bar to deploy every pending resource of the environment. The `N pending` button next to it lists what is waiting. **Redeploy** appears in the header of a deployed service's panel when it has no pending changes.

A deploy sets `spec.deploy.enabled` to `true`, clears the pending markers, stores a snapshot of the whole spec in `geass.dev/last-deployed`, stamps a new `geass.dev/deploy-revision`, starts a provision attempt and creates a [GeassDeployment](/docs/reference/resources/geass-deployment/) record. When the project has an enabled webhook, it also sends the `deployment.started` event. The deploy is refused with a capacity message when the cluster cannot fit the service.

The dashboard records state in annotations:

| Annotation | Meaning |
| --- | --- |
| `geass.dev/deploy-workflow` | `"true"` on services created by the dashboard. The dashboard shows the **Draft** status and counts pending changes only when it is set. |
| `geass.dev/pending-updates` | The number of saved changes that are not deployed. |
| `geass.dev/pending-changes` | The kinds of pending change, comma separated: `created`, `settings`, `variables`. |
| `geass.dev/last-deployed` | The whole spec at the last deploy, as JSON. |
| `geass.dev/deploy-revision` | A timestamp that changes on every deploy. It is copied to the pod template, so a deploy restarts the pods. |
| `geass.dev/webhook-state` | The last deployment event announced to webhooks, as `<revision>:<event>`. |
| `geass.dev/github-deployment` | Where the operator last reported a repository service's deploy to GitHub, as `<commit>:<GitHub deployment ID>:<state>:<deploy revision>`. The state is `created`, `in_progress`, `success` or `failure`. It is what keeps a deploy from being reported twice, and a deploy that succeeded is not reported again when the service restarts or scales. An ID of `0` is a service that was already running when it was first looked at, which is given a record without anything being sent. See [Reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github). |
| `geass.dev/display-name` | The label the dashboard shows. |

While `pending-updates` is greater than zero on a deployed service, the operator runs the snapshot in `geass.dev/last-deployed` and not the live spec. This keeps saved edits frozen until you deploy them. A few settings never replace the pods, so they use the live spec and take effect when you save: `replicas`, `autoscaling`, `scaleToZero`, `ingress` (all of it), `metrics`, `logs.retentionHours`, `deploy.failureThreshold`, `deploy.pauseOnFailure`, `deploy.rollbackOnFailure` and `source.git.watchPatterns`. Everything else waits for a deploy, including a function's mode and schedules. The dashboard labels live settings **Applies instantly** and tells you with `Saved. Applied now` or `Saved. Deploy to apply them`.

:::caution
While a service is a draft, the operator runs its cleanup on every reconcile. The cleanup deletes the Secret named by `spec.secretRef` in `geass-system`. A secret variable that you add to a draft with **New Variable** and **Secret** is therefore removed again, and the first deploy can fail with a message such as `secrets "<app>-secrets" not found`. Deploy the service first, then add secret variables and deploy again.
:::

### Settings tabs

Click a service on the **Resources** tab to open its panel. The panel has the tabs **Overview**, **Deployments**, **Code** (functions only), **Variables**, **Metrics**, **Console** and **Settings**. **Settings** has these sections.

| Section | Field | Spec field |
| --- | --- | --- |
| **Source** | **Image** | `source.image.image`. Saving replaces the whole `image` block, so `pullSecret` is lost. |
| **Source** | **Repository**, **Root directory**, **Branch**, **Wait for CI** | `source.git.repository`, `source.git.context`, `source.git.branch`, `source.git.waitForCI` |
| **Source** | **Runtime** | `source.function.runtime`. The code, the mode and the schedules are edited in the **Code** tab. |
| **Networking** | **Custom domain**, **HTTPS**, **Verify DNS**, **Port** | `ingress.host`, `ingress.tlsEnabled`, `ingress.dnsVerification`, `port` |
| **Scale** | **Replicas**, **Resources**, **Autoscaling** (**Scale up to**, **CPU target %**), **Sleep when idle** (**Sleep after (minutes)**), **Run on** (shown only when nodes carry tags) | `replicas`, `resources`, `autoscaling`, `scaleToZero.enabled` and `scaleToZero.idleSeconds`, `runOn.tags` |
| **Build** (Git only) | **Dockerfile path**, **Watch paths**, **Use build cache** | `source.git.dockerfile`, `source.git.watchPatterns`, `build.cache` |
| **Deploy** | **Command**, **Arguments**, **Working directory**, **Healthcheck path**, **Enable metrics** | `build.command`, `build.args`, `build.workingDir`, `deploy.readinessProbe`, `metrics.enabled`. **Restart policy** is read-only and shows `Always`. |
| **General** | Project, environment, **Service ID** | Read-only. The service ID is `metadata.name`. |
| **Danger** | **Delete service** | Deletes the `GeassApp`. You type the service ID to confirm. |

The **Variables** tab writes `spec.configData` (**New Variable**, **Raw Editor**), the Secret `<app>-secrets` with `spec.secretRef` (the **Secret** switch), `spec.sharedVariableRefs` (**Shared Variable**) and entries in `spec.env` (**Add Reference**, which adds `DATABASE_URL` or `REDIS_URL` from the `uri` key of a database's connection Secret, or `S3_ENDPOINT`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` and `S3_BUCKET` from the keys of a bucket's). See [Environment variables](/docs/guides/environment-variables/).

The **Code** tab of a function edits `source.function.code` and `source.function.mode` (the **Mode** menu has **Server (serves HTTP)** and **Handler (gets an event)**), and under **Schedules** it edits `source.function.triggers`. **Save** records a pending change ("Code saved. Deploy to apply it."), and so does **Save schedules** ("Schedules saved. Deploy to start them."). Changing the mode does not rewrite the code.

The **Console** tab opens a shell in a pod and records a [GeassConsoleSession](/docs/reference/resources/geass-console-session/). The **Deployments** tab lists the [GeassDeployment](/docs/reference/resources/geass-deployment/) records and the provision attempts.

The dashboard has no controls for `source.git.commit`, `source.image.pullSecret`, `ingress.rules`, `env` entries other than references, `envFrom`, `configMapRefs`, `secretRefs`, `secretData`, `build.registry`, `build.argsFrom`, `logs`, `scaleToZero.maxReplicas`, the `payload` of a schedule, the liveness and startup probes or the three rollout fields. You set them with `kubectl`.

Read-only dashboard users see a service with its host, source repository, commit, function code, variables and pull secret removed.

### Automation

The dashboard actions are also available to the REST API with an API token. Deploying uses `POST /api/apps/<name>/deploy`, and saving settings uses `POST /api/apps/<name>/update`. The versioned API has the same operations under `/api/v1`. See the [REST API](/docs/reference/rest-api/) reference.

The `geass` command line and the MCP server call that API. `geass apps create`, `geass deploy`, `geass status`, `geass env`, `geass scale`, `geass rollback` and `geass functions triggers` cover creating, deploying, reading and changing a service. See [Command line](/docs/guides/command-line/), [MCP server](/docs/guides/mcp-server/), the [CLI reference](/docs/reference/cli/) and the [MCP tools](/docs/reference/mcp-tools/).

## Editing by hand

You can create and edit a `GeassApp` with `kubectl`. Keep these points in mind.

- Create it in `geass-system` and give it a name that no other service, in any project, uses. The name also names the Deployment and Service in the namespace, and builds are named `<app>-build-<hash>`.
- A manifest with `spec.deploy.enabled: true` and no `geass.dev/*` annotations is deployed at once. The dashboard shows it. A deploy you make this way is not recorded as a dashboard deploy and sends no webhook events.
- If you later change a hand-made service in the dashboard, the dashboard marks a pending change, and the operator then needs `geass.dev/last-deployed` to know what to run. Without it the service reports `Service has pending changes and no last deployed snapshot`. Click **Deploy changes** to store a snapshot.
- On a service the dashboard created, `kubectl edit` of a deployed service has two modes. When `geass.dev/pending-updates` is `"0"`, the operator reads your edit. When it is greater than zero, the operator keeps running the snapshot and only live settings take effect. A dashboard deploy replaces the snapshot with the current spec, including your hand edits.
- The operator writes `status`, the finalizer, and sometimes `spec.ingress.host` (see the note under [Public access](#public-access)). The dashboard writes the annotations in the table above. Do not edit them.
- Use `kubectl patch` or `kubectl edit`. A `kubectl replace` with an old manifest overwrites the annotations and the finalizer.

To restart the pods of a deployed service without changing its spec, set a new deploy revision:

```bash
kubectl annotate geassapp storefront-web -n geass-system \
  geass.dev/deploy-revision="$(date -u +%Y-%m-%dT%H:%M:%SZ)" --overwrite
```

Deleting a `GeassApp` deletes its Deployment, Service, Ingress, HorizontalPodAutoscaler, ServiceMonitor, ConfigMaps and Secrets in the namespace, the schedule CronJobs, the `<app>-wake` Service and the HTTPScaledObject of a service that slept, its recorded [GeassDeployment](/docs/reference/resources/geass-deployment/) objects, and the Secret `<app>-secrets` in `geass-system`. Its builds and `<app>-active` record are garbage collected.

## What happens under the hood

For a deployed service the operator maintains these objects in the environment namespace `<project>-<environment>`, all named after the service:

| Object | Name | Notes |
| --- | --- | --- |
| Deployment | `<app>` | One container named `app`, with the container port `http`. Pod restart policy `Always`. |
| Service | `<app>` | ClusterIP with one port named `http`, from `spec.port` to the container's `http` port. |
| Ingress | `<app>` | Only when `spec.ingress.host` is set. |
| HorizontalPodAutoscaler | `<app>` | Only with autoscaling. |
| ConfigMap | `<app>-config` | `spec.configData`, when set. |
| ConfigMap | `<app>-function` | The function's code, for functions. In `event` mode it also holds the shim. |
| Secret | `<app>-secret` | The service's secret values, when set. |
| Secret | `<app>-registry-pull` | Git services only, when the build uses a registry credential: a copy of it, which the Deployment pulls the built image with. |
| Secret | `<app>-trigger` | A function's schedule token (key `token`), for a function in `event` mode or with triggers. |
| CronJob | `<app>-<trigger name>-<8 characters>` | One per trigger of a function. Labelled `geass.dev/function-trigger=<trigger name>`. |
| Service | `<app>-wake` | Only when the service sleeps when idle. An `ExternalName` Service for the scale-to-zero proxy, which the Ingress points at. |
| HTTPScaledObject | `<app>` | Only when the service sleeps when idle. It tells the add-on the hosts, paths, idle time and copy range. |
| ServiceMonitor | `<app>-metrics` | Only with `spec.metrics.enabled`. |

In `geass-system` the service owns the [GeassBuild](/docs/reference/resources/geass-build/) objects named `<app>-build-<hash>` and the [GeassDeployment](/docs/reference/resources/geass-deployment/) `<app>-active`. Those two have owner references to the service. The objects in the namespace do not, because Kubernetes does not allow an owner in another namespace, so the operator removes them itself.

The objects carry the labels `geass.dev/managed-by=geass`, `geass.dev/kind=GeassApp`, `geass.dev/name=<app>`, `geass.dev/system-namespace=geass-system`, `geass.dev/project=<project>` and `geass.dev/environment=<environment>`. The builds in `geass-system` carry `geass.dev/kind=GeassBuild` and also `geass.dev/app=<app>`, which is how the push webhook, the build actions and the status read find them. The `<app>-active` record carries `geass.dev/kind=GeassDeployment`, with the same `geass.dev/name`. Pods also carry `app.kubernetes.io/name=<app>` and `app.kubernetes.io/managed-by=geass`. Inside the cluster the service answers on `<app>.<namespace>.svc.cluster.local:<port>`.

The operator reconciles a service when the service changes, and when its Deployment's desired or available replica counts change, so the service's state follows its pods. It writes `status` only when the status changed.

When you change `spec.project` or `spec.environment`, the operator deletes the workload from the old namespace and creates it in the new one.

Inspect a service with these commands:

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

**The service stays `Service is configured and waiting for deployment`.** It is a draft. Click **Deploy changes**, or set `spec.deploy.enabled: true`.

**`Service has pending changes and no last deployed snapshot`.** The service is deployed, `geass.dev/pending-updates` is greater than zero, and `geass.dev/last-deployed` is missing. This happens when a service created with `kubectl` is edited in the dashboard. Click **Deploy changes**. The deploy stores the snapshot and clears the pending state.

**`project "..." does not exist`, `is not ready` or `has no ... environment`.** The service points at a project that is missing, not yet `Ready`, or that lacks the environment. Check `kubectl get geassproject <project> -n geass-system` and `spec.environments`. These errors count toward the retry limit. If the service reaches `ProvisionFailed` while it waited, click **Retry** once the project is ready.

**`This service needs about ... CPU and ... memory` or `There is no ready node in ...`.** The cluster cannot place the service. Lower `spec.resources` or `spec.replicas`, add a node, or change `spec.runOn.tags` or the project's region. A message that says nodes are reserved means the nodes that match are kept for work that asks for their tag. The operator checks capacity only when it first creates the Deployment, and the dashboard checks again each time you deploy. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**`Service is starting` does not go away.** The pods are not available, and no container is stuck in a start failure. Run `kubectl get pods -n <namespace> -l app.kubernetes.io/name=<app>` and `kubectl describe pod`. Common causes are a failing readiness probe (the probe path or port is wrong, or a function does not serve HTTP on `PORT`), a pod that cannot be scheduled, and a pod stuck in `ContainerCreating` with a mount error because a ConfigMap or Secret named in `configMapRefs` or `secretRefs` does not exist in the environment namespace.

**`The service cannot start: app is <reason>`.** A container of the service is stuck. The dashboard shows **Unavailable**, and after the retry limit (see [Conditions](#conditions)) **Failed** with `ProvisionFailed`. Fix the cause, then click **Retry** or change the spec. By reason:

- `ErrImagePull`, `ImagePullBackOff` or `InvalidImageName`: the image name is wrong, or the registry needs a pull Secret. `spec.source.image.pullSecret` must name a Secret in the environment namespace, not in `geass-system`. For a Git service, `status.resolvedImage` is `<repository>@sha256:…`: check that the Secret `<app>-registry-pull` exists in the environment namespace (it is made when the build uses a credential) and that the credential can pull from that repository.
- `CreateContainerConfigError` or `CreateContainerError`: a ConfigMap or Secret named in `env` or `envFrom`, or a key in it, does not exist in the environment namespace.
- `CrashLoopBackOff`: the process exits. Read `kubectl logs --previous` and the **Deploy Logs**.

**`Provisioning failed after repeated errors`.** The operator gave up after 5 counted errors or 3 minutes. The recent errors are in `status.provision.provisionAttempts[].errors`. Fix the cause, then click **Retry**, or change the spec.

**`source.git.connectionRef, repository, and branch are required`.** A Git service was deployed with an empty connection, repository or branch. This error fails at once, without retries. Connect the project to GitHub, choose a repository, then deploy again.

**`Git source is waiting for a successful build` does not go away.** Read the build: `kubectl get geassbuilds -n geass-system -l geass.dev/app=<app>`. See [GeassBuild](/docs/reference/resources/geass-build/#troubleshooting).

**A push to GitHub does not rebuild the service.** The webhook answers `"triggered":0` when no service matched. The service has to be deployed, build the pushed branch of the pushed repository, not pin a commit, watch a file the push changed (`watchPatterns`), and already have a build: `kubectl get geassbuilds -n geass-system -l geass.dev/app=<app>`. See [A push does not trigger a build](/docs/guides/github-and-builds/#troubleshooting). To build by hand, see [GeassBuild](/docs/reference/resources/geass-build/#re-run-a-build).

**A sleeping service never wakes, or never sleeps.** Read the `ScaleToZero` condition: `kubectl get geassapp <app> -n geass-system -o jsonpath='{.status.conditions[?(@.type=="ScaleToZero")]}'`. A reason other than `Active` means the service runs always on. See [Sleep when idle](/docs/guides/scale-to-zero/#troubleshooting).

**A function's schedule does not run.** Schedules start with the next deploy. Check that the service is deployed and not pending, then `kubectl get cronjobs -n <namespace> -l geass.dev/name=<app>` and `kubectl get jobs -n <namespace> -l geass.dev/function-trigger=<trigger name>`. See [Functions](/docs/guides/functions/#troubleshooting).

**`RolloutFailed`, and the service never recovers.** With `pauseOnFailure` the operator pauses and does not clear the flag by itself. Clear `status.rolloutPaused` as shown under [Rollout and health](#rollout-and-health).

**The service has no public URL, or the URL does not load.** Check, in order:

1. `spec.ingress.host` is set, and `kubectl get ingress <app> -n <namespace>` shows the host.
2. The DNS record of the host points at your cluster. With `dnsVerification` on, `status.dnsVerified` is `false` until it resolves.
3. For HTTPS, cert-manager and the `letsencrypt-prod` ClusterIssuer exist and are ready (`kubectl get clusterissuer letsencrypt-prod`), and `kubectl get certificate -n <namespace>` shows the certificate. The operator creates that issuer only when `spec.addons.certManager.acmeEmail` is set on the [GeassCluster](/docs/reference/resources/geass-cluster/); without it the cluster reports `ClusterIssuerReady: False` and no certificate is issued. See [Domains and TLS](/docs/guides/domains-and-tls/).

**`metrics are enabled but Prometheus Operator is not installed in this cluster`.** The cluster has no ServiceMonitor kind. Turn off **Enable metrics**, or install the monitoring add-on on the cluster.

**A secret variable disappeared from a draft.** See the caution under [The draft and deploy workflow](#the-draft-and-deploy-workflow). Deploy first, then add secret variables.

**A plain shared variable reaches a service that did not select it.** This is how the current code works. See the caution under [Variables and files](#variables-and-files).

## Related

- [Apps guide](/docs/guides/apps/) and [First app](/docs/getting-started/first-app/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [GeassBuild](/docs/reference/resources/geass-build/), [GeassDeployment](/docs/reference/resources/geass-deployment/), [GeassConsoleSession](/docs/reference/resources/geass-console-session/)
- [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/)
- [Functions](/docs/guides/functions/), [Sleep when idle](/docs/guides/scale-to-zero/), [Environment variables](/docs/guides/environment-variables/), [Domains and TLS](/docs/guides/domains-and-tls/)
- [Command line](/docs/guides/command-line/) and [MCP server](/docs/guides/mcp-server/)
- [Resources overview](/docs/reference/resources/overview/)
