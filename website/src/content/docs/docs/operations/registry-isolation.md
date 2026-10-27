---
title: "Registry isolation for organizations"
description: "Keep the platform's registry login out of organizations' builds and their images apart: spec.tenancy.registry, what it refuses, sharing one registry."
---

With [organizations](/docs/guides/organizations-and-workspaces/), an organization's builds run in a namespace of their own,
`geass-build-<organization>`. But an organization that has no registry of its own builds with the **platform's** registry
login: Geass copies it into that namespace for the build, and into each of the organization's environment namespaces for
the pods to pull with. A build runs what the organization's Dockerfile says, as root, with the login mounted, so the
organization's code can read the login and then push to, and pull from, everything the platform's login reaches: the
platform's own images and every other organization's that went there. When the organizations of a cluster do not trust
each other, set `spec.tenancy.registry` on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/).

It is off until you set it. While it is off nothing changes: the build Jobs, the copies of logins and the pull secrets are
what they were before it existed, which Geass's tests check against the release before it.

:::caution[Not run against a real registry]
Geass's tests check what the operator writes, refuses and removes against a fake API server. No build has pushed to a real
registry under it, and the Harbor and zot steps below follow those projects' documentation and were not run here.
:::

## How a build gets a registry

Geass has no registry of its own. A build pushes to one of these, the first that is set:

| Registry | Set by | The login the build pushes with, and the pods pull with |
| --- | --- | --- |
| The service's own, `spec.build.registry.repository` of the [GeassApp](/docs/reference/resources/geass-app/), used as written | `kubectl` | The Secret its `spec.build.registry.credentialRef` names, or none |
| The organization's own, `spec.registry` of its [GeassOrganization](/docs/reference/resources/geass-organization/) | An administrator of the whole organization, under **Settings** → **Registry**, or a platform administrator with `kubectl` | The organization's, the Secret `org-registry-<organization>` |
| The platform's, `spec.registry` of the GeassPlatformConfig, used as written | A platform administrator | The platform's, the Secret `platform-registry` |

The platform's registry is one repository and one login. A login of a plain `registry:2` (Docker's distribution) with
htpasswd can push and pull every repository, and most hosted registries give one login access to everything of its
account. So Geass cannot hand an organization a login for a part of the platform's registry: there is none to hand.

## Turn it on

```yaml title="platform.yaml (spec.tenancy)"
spec:
  tenancy:
    registry:
      policy: requireOwnRegistry
```

```bash
kubectl -n geass-system patch geassplatformconfig platform --type merge \
  -p '{"spec":{"tenancy":{"registry":{"policy":"requireOwnRegistry"}}}}'
```

`requireOwnRegistry` is the only policy, and the default, so `registry: {}` is the same. It applies to the projects that
belong to an organization (their `spec.org` is set), whether or not organizations are on for sign-in, and to a project that
a service or build names but that cannot be found. The projects of no organization build, push and pull exactly as before.

## What it does

**An organization's build is never given the platform's login.** It is given the organization's own login, or the one its
service names. A build whose login is the platform's fails before its Job is made or anything is copied for it, with
`Registry isolation: the registry login … is the platform's`. The platform's login is recognized by its name
(`platform-registry`, or whatever `spec.registry.credentialRef` names) and under any other name: a Secret whose
`.dockerconfigjson` holds the same user and password, or the same token, for any registry. A login Geass saved for
another organization (`org-registry-<other>`, or a Secret labelled `geass.dev/org-build` with another organization's
name) is refused the same way.

**The platform's registry is no fallback.** An organization that has no registry of its own does not build: its services
wait with `Registry isolation: the organization has no registry of its own, and an organization's builds are never given
the platform's registry login…`, and start building when its administrator sets one.

**Each service has a repository of its own under the organization's.** An organization's build pushes to
`<organization repository>/<project>/<service>`, so with `ghcr.io/acme/builds` as the organization's registry, the service
`api` of the project `shop` is built into `ghcr.io/acme/builds/shop/api` and runs as
`ghcr.io/acme/builds/shop/api@sha256:…`. The registry has to accept repositories three levels below the one you set, made
on the first push: Harbor, zot, GitHub's registry, GitLab's and `registry:2` do; Docker Hub and Quay allow only
`<namespace>/<repository>` and do not. A service that names a registry of its own keeps it, as written.

**An organization's namespaces never hold the platform's login.** A pull secret of a service (`<service>-registry-pull`
in its environment namespace) that holds it is deleted; the service's pods then pull with the organization's login once
it is built into its registry. Before an organization's build starts, any copy of the platform's login that Geass left in
`geass-build-<organization>` is deleted, and a build that is running with the platform's login when the setting is turned
on is stopped (its Job deleted, its copies removed) and fails.

**Parts of registries belong to someone, and an organization uses only its own.** The operator works out who owns what:

| Part | Whose |
| --- | --- |
| The platform's repository (`spec.registry.repository`) and everything below it | The platform's |
| `<platform repository>/<organization>` and everything below it | That organization's: one registry can hold each organization's images beside the platform's when it authorizes each for its own path (see [Share one registry](#share-one-registry)) |
| An organization's own registry and everything below it | That organization's, unless it overlaps the platform's repository outside `<platform repository>/<organization>`, or overlaps another organization's registry: then it is **contested**, and nobody may use what is in it |
| Anything else (public images, a registry nobody here claims) | Nobody's: any service may run it |

An image is in the part that holds it most closely, compared one path segment at a time on one spelling of the reference
(Docker Hub as `docker.io` with `library/` for its official images, the host in lower case, no `:443`, no tag or digest).
An organization's service may not run an image, and its build may not push to a repository, in a part that is not its own:
the service waits with `Registry isolation: the image … is in a part of a registry that belongs to the platform or to
another organization…`, and its Deployment is not written (one that was running is left as it was). The message never says
whose part it is. An image named by its ID alone (`sha256:…`) is refused too, since it names no repository.

## Where it is enforced

**In the operator, on every pass.** The service controller decides before it writes anything new for a service, and the
build controller before it makes a build's Job or copies anything for it, and on every pass of a build that is running.
So a service or build made or changed any way at all (the dashboard, the API, the MCP server, `kubectl`) is held to it
within seconds. There is no admission webhook: `kubectl apply` of a GeassApp or GeassBuild that the policy refuses is
accepted by the API server, and the operator then refuses it and says why on its status. Turning the setting on or off,
and while it is on a change of the platform's registry or of any organization's registry, reaches every service at once.

**In the dashboard, when an organization takes a registry.** Under the policy, **Settings** → **Registry** (and
`POST /api/registry/save`) answers `409` to a repository that overlaps the platform's repository outside
`<platform repository>/<organization>`, or another organization's registry, without saying whose. The first organization
to take a repository keeps it. A platform administrator can still give two organizations overlapping registries with
`kubectl`, or two can race the check; both registries are then contested and neither organization builds into its own,
which the operator says on their services. `GET /api/registry` answers `ownRequired: true` while the policy is on.

## Conditions and events

The `RegistryIsolated` condition is written on a [GeassApp](/docs/reference/resources/geass-app/#conditions) of an
organization's project that is deployed, and on a [GeassBuild](/docs/reference/resources/geass-build/) of one before it
starts and while it runs, while the setting is on. It is taken off a service, or a build that has not finished, when the
setting goes, and is not there otherwise.

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `Isolated` | Nothing of the platform's or of another organization's is used. |
| `False` | `OwnRegistryRequired` | The organization has no registry of its own. The service makes no build. |
| `False` | `PlatformCredential` | The login is the platform's, or another organization's. The build fails; a service that names such a login makes no build. |
| `False` | `ForeignRepository` | The image, or the repository the build would push to, is in a part of a registry that is not the organization's, or the organization's own registry is contested, or the image is named by its ID alone. |

The message starts with `Registry isolation: `, which is also the service's `Ready` message and a build's
`status.failureReason`. It does not count against a service's retry limit: it waits for the organization's registry, its
image or the setting to change. A `Warning` event `RegistryRefused` is recorded on the service or build when it is first
held back for a reason.

```bash
kubectl -n geass-system get geassapps,geassbuilds \
  -o custom-columns='KIND:.kind,NAME:.metadata.name,ISOLATED:.status.conditions[?(@.type=="RegistryIsolated")].reason'
```

## Turning it on for a cluster that is running

1. **Give every organization a registry of its own first** (see [Share one registry](#share-one-registry)), and check that it
   accepts the nested repositories above.
2. **Turn it on.** Every service is looked at again:
   - A service of an organization that has a registry builds again, into `<repository>/<project>/<service>` (the
     destination changed, so it is a new build). Until that build succeeds it keeps running what it ran.
   - A service of an organization that has none makes no build and keeps running what it ran.
   - The copies of the platform's login in the organizations' namespaces are deleted, and running builds that hold it are
     stopped. **A copy of a service that has to pull its old image again (it moved to another node) cannot**, since the
     login it pulled with is gone: it waits in `ImagePullBackOff` until the service's new build succeeds. Images that are
     public, or already on the node, still start.
3. Look for services and builds that are held back (the command above), and ask their organizations to set a registry.

Turning it off puts everything back as it was without it: builds of organizations without a registry use the platform's
login again, and a service's builds go back to the organization's repository as written.

## Share one registry

To keep every organization's images in one registry and still keep them apart, run a registry that **authorizes each login
for a path**, give each organization a login that can push and pull only under a path of its own, and make that its
registry. Geass then pushes the organization's images under that path and never gives it anything else.

### Harbor

In [Harbor](https://goharbor.io), a project is a path with its own members and robot accounts:

1. Make a **private** project for each organization, named after the organization (`org-1a2b3c4d`).
2. Make a robot account for that project alone, with push and pull on its repositories.
3. Set the organization's registry to `harbor.example.com/org-1a2b3c4d` with the robot's name and secret. Its builds go to
   `harbor.example.com/org-1a2b3c4d/<project>/<service>`, and its pods pull with the robot's login.

### zot

[zot](https://zotregistry.dev) authorizes users per repository pattern with `accessControl`. With the platform's repository
at `zot.example.com/platform`, each organization's path is `zot.example.com/platform/<organization>`, which Geass already
treats as the organization's:

```json title="zot config (http section, abbreviated)"
"accessControl": {
  "repositories": {
    "platform/org-1a2b3c4d/**": { "policies": [{ "users": ["org-1a2b3c4d"], "actions": ["read", "create", "update"] }] },
    "platform/**": { "policies": [{ "users": ["platform"], "actions": ["read", "create", "update"] }] }
  }
}
```

Check the pattern syntax and which policy wins against the zot documentation for your release.

### Setting an organization's registry as a platform administrator

The organization's administrators can set it themselves under **Settings** → **Registry**. To set it for them, for example
when a platform makes organizations for its customers:

```bash
ORG=org-1a2b3c4d
kubectl -n geass-system create secret docker-registry org-registry-$ORG \
  --docker-server=harbor.example.com --docker-username='robot$org-1a2b3c4d+geass' --docker-password='…'
kubectl -n geass-system label secret org-registry-$ORG geass.dev/managed-by=geass geass.dev/org-build=$ORG
kubectl -n geass-system patch geassorganization $ORG --type merge \
  -p '{"spec":{"registry":{"repository":"harbor.example.com/'$ORG'","credentialRef":{"name":"org-registry-'$ORG'"}}}}'
```

The label says whose login it is, so no other organization's service can be given it. The dashboard checks for overlaps
only when an organization saves its own registry: make sure the ones you set do not overlap each other or the platform's
repository, or both are contested.

## What it does not cover

- **A node's image cache.** Geass refuses an organization's service that names another's image, but the kubelet starts an
  image it already holds without asking the registry, whatever pull secret the pod has. On a shared cluster, also have the
  kubelet or the API server check: the `AlwaysPullImages` admission plugin (with K3s,
  `--kube-apiserver-arg=enable-admission-plugins=NodeRestriction,AlwaysPullImages`), which makes every pod's image be
  pulled with that pod's own login each time it starts, or the kubelet's `KubeletEnsureSecretPulledImages` feature, alpha
  since Kubernetes 1.33.
- **A registry that cannot keep logins apart.** The policy keeps the platform's login away from organizations; it cannot
  make one login of a registry safe to share. Each organization's login has to reach only its own path.
- **What an organization's own login can do.** It is the organization's: Geass does not stop its builds pushing anywhere
  that login reaches, nor its services running images from a registry no organization here claims.
- **A pull secret a service names itself** (`spec.source.image.pullSecret`, a Secret in its own namespace) is used as it is.
- **Databases, object storage, backups and Geass's own pods** do not use a build registry and are not affected.
- **The dashboard has no form for the setting.** Set it with `kubectl`.
- **Outbound limits.** An organization's builds push to its own registry, so with [outbound
  limits](/docs/guides/outbound-limits/) that list `allowedPorts`, list the port of each organization's registry (443 for
  most), or its builds cannot push.

## Related

- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/#builds-and-the-registry)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/), [Outbound limits](/docs/guides/outbound-limits/) and [Noisy-neighbour limits](/docs/guides/tenant-limits/), the other settings for a cluster that organizations share
