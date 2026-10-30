---
title: "Registry isolation for organizations"
description: "Keep the registry login of the platform out of the builds of organizations, and keep their images apart with spec.tenancy.registry."
---

With [organizations](/docs/guides/organizations-and-workspaces/), the builds of an organization run in their own
namespace, `geass-build-<organization>`. But an organization that has no registry of its own builds with the registry
login of the **platform's** registry. Geass copies that login into the build namespace for the build. Geass also copies it
into each environment namespace of the organization, so that the pods can pull with it.

A build runs what the Dockerfile of the organization says, as root, with the login mounted. Thus the code of the
organization can read the login. Then that code can push to, and pull from, all that the login of the platform reaches.
This includes the images of the platform and the images of every other organization that went there. When the
organizations of a cluster do not trust each other, set `spec.tenancy.registry` on the
[GeassPlatformConfig](/docs/reference/resources/geass-platform-config/).

The setting is off until you set it. When it is off, nothing changes. The build Jobs, the copies of logins, and the pull
secrets are the same as before the setting existed. The tests of Geass compare this behavior with the release before the
setting.

:::caution[Run against plain registries, not Harbor or zot]
The tests of Geass check what the operator writes, refuses, and removes, against a fake API server. The setting also ran
on real K3s v1.36.4+k3s1 with real Kaniko (v1.23.2) builds. It ran against two `registry:2` (2.8.3) instances with
htpasswd logins: one for the platform, and one that an organization owns. The results were as follows:

- With the policy off, Geass gave the build of an organization a copy of the login of the platform. The build pushed into
  the registry of the platform, and the catalog of that registry then listed the image of the organization.
- With the policy on, an organization without its own registry made no build (`RegistryIsolated` `False`,
  `OwnRegistryRequired`).
- With the policy on, an organization with its own registry built into `<repository>/<project>/<service>` with its own
  login only. The Job mounted that login, and the image appeared in the registry of the organization. The catalog of the
  platform did not change.
- When the policy was turned on, Geass stopped a build that ran with the login of the platform. When the policy was off
  again, Geass gave the organization without a registry a build with the repository and the login of the platform.
- Geass refused each of these items with `PlatformCredential` or `ForeignRepository`, before it made a Job or a copy:
  - The login of the platform.
  - A copy of that login under a different Secret name.
  - The repository of the platform.
  - An image in that repository.
  - An organization registry that is equal to the repository of the platform.
- With the [sandboxed runtime](/docs/operations/sandboxed-runtime/) on, a build ran under gVisor `release-20261019.0`
  and pushed to the registry of the organization. An image service then pulled the result from that registry with the
  login of the organization.

**Not run:**

- Harbor and zot. The steps on this page follow the documentation of those projects.
- The check in the dashboard for organization registries that overlap. This check needs a signed-in admin.
- `AlwaysPullImages`.
- A service built from GitHub all the way to a running pod. This test needs a real GitHub token, because the clone of
  Geass sends `Authorization: Bearer <token>`. GitHub answered 401 to an invented token, even for a public repository.
  The builds in the earlier tests were `GeassBuild` objects written without a `gitCredentialRef`. These builds clone
  without a token, and the policy applies to them in the same way.
:::

## How a build gets a registry

Geass has no registry of its own. A build pushes to the first of these registries that is set:

| Registry | Set by | The login that the build pushes with, and that the pods pull with |
| --- | --- | --- |
| The registry of the service, `spec.build.registry.repository` of the [GeassApp](/docs/reference/resources/geass-app/), used as written | `kubectl` | The Secret that its `spec.build.registry.credentialRef` names, or none |
| The registry of the organization, `spec.registry` of its [GeassOrganization](/docs/reference/resources/geass-organization/) | An administrator of the whole organization, under **Settings** → **Registry**, or a platform administrator with `kubectl` | The login of the organization, the Secret `org-registry-<organization>` |
| The registry of the platform, `spec.registry` of the GeassPlatformConfig, used as written | A platform administrator | The login of the platform, the Secret `platform-registry` |

The registry of the platform is one repository and one login. A login of a plain `registry:2` (Docker's distribution)
with htpasswd can push and pull every repository. Most hosted registries give one login access to all of its account.
Thus Geass cannot give an organization a login for only a part of the registry of the platform, because no such login
exists.

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

`requireOwnRegistry` is the only policy, and it is the default. Thus `registry: {}` has the same effect. The policy
applies to these projects:

- The projects that belong to an organization, that is, projects with `spec.org` set. This is true whether or not
  organizations are on for sign-in.
- A project that a service or a build names, but that Geass cannot find.

The projects of no organization build, push, and pull exactly as before.

## What it does

**Geass never gives an organization's build the platform's login.** Geass gives the build the login of the
organization, or the login that its service names. A build whose login is the login of the platform fails before Geass
makes its Job or copies anything for it. The failure message is
`Registry isolation: the registry login … is the platform's`. Geass recognizes the login of the platform by its name: `platform-registry`, or the name that
`spec.registry.credentialRef` gives. Geass also recognizes it under any other name, as a Secret whose `.dockerconfigjson`
holds the same user and password, or the same token, for any registry. Geass refuses in the same way a login that it
saved for a different organization. Such a login is `org-registry-<other>`, or a Secret with the label
`geass.dev/org-build` and the name of a different organization.

**The platform's registry is no fallback.** An organization that has no registry of its own does not build. Its services
wait with `Registry isolation: the organization has no registry of its own, and an organization's builds are never given
the platform's registry login…`. They start to build when the administrator of the organization sets a registry.

**Each service has a repository of its own under the organization's.** A build of an organization pushes to
`<organization repository>/<project>/<service>`. For example, if the registry of the organization is
`ghcr.io/acme/builds`, Geass builds the service `api` of the project `shop` into `ghcr.io/acme/builds/shop/api`. The
service then runs as `ghcr.io/acme/builds/shop/api@sha256:…`. The registry must accept repositories three levels below
the repository that you set, and it must create them on the first push:

- Harbor, zot, the GitHub registry, the GitLab registry, and `registry:2` accept them.
- Docker Hub and Quay allow only `<namespace>/<repository>`, and they do not accept them.

A service that names a registry of its own keeps that registry, as written.

**An organization's namespaces never hold the platform's login.** A service has a pull secret,
`<service>-registry-pull`, in its environment namespace. If that pull secret holds the login of the platform, Geass
deletes it. After Geass builds the service into the registry of the organization, the pods of the service pull with the
login of the organization. Before a build of an organization starts, Geass deletes any copy of the login of the platform
that it left in `geass-build-<organization>`. A build that runs with the login of the platform when you turn on the
setting stops and fails. Geass deletes its Job and removes its copies.

**Parts of registries belong to someone, and an organization uses only its own.** The operator finds the owner of each
part:

| Part | Whose |
| --- | --- |
| The repository of the platform (`spec.registry.repository`) and all below it | The platform's |
| `<platform repository>/<organization>` and all below it | That organization's. One registry can hold the images of each organization beside the images of the platform, if it authorizes each login for its own path. See [Share one registry](#share-one-registry) |
| The registry of an organization and all below it | That organization's, with two exceptions. If it overlaps the repository of the platform outside `<platform repository>/<organization>`, or it overlaps the registry of a different organization, it is **contested**. Nobody can use what is in a contested part |
| All else, for example public images or a registry that nobody here claims | Nobody's. Any service can run it |

An image is in the part that holds it most closely. Geass compares one path segment at a time, on one spelling of the
reference. In that spelling, Docker Hub is `docker.io` with `library/` for its official images, the host is in lower
case, and there is no `:443`, no tag, and no digest.

The service of an organization cannot run an image in a part that is not its own. The build of an organization cannot
push to a repository in such a part. In this case, the service waits with `Registry isolation: the image … is in a part
of a registry that belongs to the platform or to another organization…`, and Geass does not write its Deployment. If a
Deployment was running, Geass leaves it as it was. The message never says whose part it is. Geass also refuses an image
named by its ID alone (`sha256:…`), because that image names no repository.

## Where it is enforced

**In the operator, on every pass.** The service controller decides before it writes anything new for a service. The
build controller decides before it makes the Job of a build or copies anything for it. It also decides on every pass of a
build that is running. Thus the policy applies within seconds to a service or a build that is made or changed in any
way: the dashboard, the API, the MCP server, or `kubectl`.

There is no admission webhook. The API server accepts a `kubectl apply` of a GeassApp or GeassBuild that the policy
refuses. Then the operator refuses it, and says why on its status. These changes reach every service immediately:

- You turn the setting on or off.
- While the setting is on, the registry of the platform or of any organization changes.

**In the dashboard, when an organization takes a registry.** Under the policy, **Settings** → **Registry** (and
`POST /api/registry/save`) answers `409` to some repositories, without saying whose they are:

- A repository that overlaps the repository of the platform outside `<platform repository>/<organization>`.
- A repository that overlaps the registry of a different organization.

The first organization that takes a repository keeps it. A platform administrator can still give two organizations
overlapping registries with `kubectl`. Also, two organizations can race the check. In both cases, both registries are
contested, and neither organization builds into its own. The operator says so on the services of both organizations.
While the policy is on, `GET /api/registry` answers `ownRequired: true`.

## Conditions and events

While the setting is on, Geass writes the `RegistryIsolated` condition on these objects:

- A [GeassApp](/docs/reference/resources/geass-app/#conditions) of the project of an organization, when it is deployed.
- A [GeassBuild](/docs/reference/resources/geass-build/) of such a project, before it starts and while it runs.

When you remove the setting, Geass removes the condition from a service, or from a build that has not finished. In other
cases the condition is not there.

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `Isolated` | Nothing of the platform or of a different organization is in use. |
| `False` | `OwnRegistryRequired` | The organization has no registry of its own. The service makes no build. |
| `False` | `PlatformCredential` | The login is the login of the platform, or of a different organization. The build fails. A service that names such a login makes no build. |
| `False` | `ForeignRepository` | One of these is true: the image, or the repository that the build pushes to, is in a part of a registry that is not the organization's. The registry of the organization is contested. The image is named by its ID alone. |

The message starts with `Registry isolation: `. This text is also the `Ready` message of the service and the
`status.failureReason` of a build. It does not count against the retry limit of a service. The service waits until the
registry of the organization, its image, or the setting changes. The first time Geass holds back a service or a build for
a reason, it records a `Warning` event `RegistryRefused` on that object.

```bash
kubectl -n geass-system get geassapps,geassbuilds \
  -o custom-columns='KIND:.kind,NAME:.metadata.name,ISOLATED:.status.conditions[?(@.type=="RegistryIsolated")].reason'
```

## Turning it on for a cluster that is running

1. **Give every organization a registry of its own first.** See [Share one registry](#share-one-registry). Make sure
   that each registry accepts the nested repositories that this page describes in What it does.
2. **Turn it on.** Geass examines every service again:
   - A service of an organization that has a registry builds again, into `<repository>/<project>/<service>`. The
     destination changed, so this build is a new build. Until that build succeeds, the service continues to run what it
     ran before.
   - A service of an organization that has no registry makes no build. It continues to run what it ran before.
   - Geass deletes the copies of the login of the platform in the namespaces of the organizations. It stops the running
     builds that hold that login. **A copy of a service that must pull its old image again cannot pull it**, for example
     because the copy moved to a different node. The login that it pulled with is gone. That copy waits in
     `ImagePullBackOff` until the new build of the service succeeds. Images that are public, or that are already on the
     node, still start.
3. Find the services and the builds that Geass holds back, with the command in Conditions and events. Ask their organizations to set a registry.

When you turn the setting off, all returns to how it was without the setting. Builds of organizations without a registry
use the login of the platform again. The builds of a service go back to the repository of the organization, as written.

## Share one registry

You can keep the images of every organization in one registry and still keep them apart. To do this, do these steps:

1. Run a registry that **authorizes each login for a path**.
2. Give each organization a login that can push and pull only under a path of its own.
3. Make that path the registry of the organization.

Geass then pushes the images of the organization under that path, and it never gives the organization anything else.

### Harbor

In [Harbor](https://goharbor.io), a project is a path with its own members and robot accounts:

1. Make a **private** project for each organization, with the name of the organization, for example `org-1a2b3c4d`.
2. Make a robot account for that project only, with push and pull on its repositories.
3. Set the registry of the organization to `harbor.example.com/org-1a2b3c4d`, with the name and the secret of the robot.
   The builds of the organization go to `harbor.example.com/org-1a2b3c4d/<project>/<service>`. Its pods pull with the
   login of the robot.

### zot

[zot](https://zotregistry.dev) authorizes users for each repository pattern with `accessControl`. If the repository of
the platform is `zot.example.com/platform`, the path of each organization is `zot.example.com/platform/<organization>`.
Geass already treats that path as the part of the organization:

```json title="zot config (http section, abbreviated)"
"accessControl": {
  "repositories": {
    "platform/org-1a2b3c4d/**": { "policies": [{ "users": ["org-1a2b3c4d"], "actions": ["read", "create", "update"] }] },
    "platform/**": { "policies": [{ "users": ["platform"], "actions": ["read", "create", "update"] }] }
  }
}
```

Check the pattern syntax, and which policy wins, in the zot documentation for your release.

### Setting an organization's registry as a platform administrator

The administrators of the organization can set the registry themselves under **Settings** → **Registry**. You can set it
for them, for example when a platform makes organizations for its customers:

```bash
ORG=org-1a2b3c4d
kubectl -n geass-system create secret docker-registry org-registry-$ORG \
  --docker-server=harbor.example.com --docker-username='robot$org-1a2b3c4d+geass' --docker-password='…'
kubectl -n geass-system label secret org-registry-$ORG geass.dev/managed-by=geass geass.dev/org-build=$ORG
kubectl -n geass-system patch geassorganization $ORG --type merge \
  -p '{"spec":{"registry":{"repository":"harbor.example.com/'$ORG'","credentialRef":{"name":"org-registry-'$ORG'"}}}}'
```

The label says whose login it is, so Geass cannot give it to the service of a different organization. The dashboard
checks for overlaps only when an organization saves its own registry. Make sure that the registries you set do not
overlap each other or the repository of the platform. If they overlap, both are contested.

## What the registry has to be

Kaniko tries HTTPS first. In tests, Kaniko v1.23.2 changed to plain HTTP for an RFC 1918 address, and it pushed over
plain HTTP to a name that ends with `.local`. It did not change to plain HTTP for a public address. A build that pushed to
a plain-HTTP registry on a public address failed with `Get "https://…:5001/v2/": http: server gave HTTP response to HTTPS client`.

The build namespace of an organization reaches no private address. An [`allow`](/docs/guides/outbound-limits/) for a
private range did not open one. The build could not reach a registry on the private address of the machine
(`connection refused`). Thus a registry for the builds of organizations needs these two things:

- An address that the builds can reach.
- A certificate that the Kaniko image trusts.

The test registries had names that end with `.local`. These names pointed at the public address of the machine in the
cluster DNS and on the node. Each test registry had an `allow` for that address on its ports, and an entry for plain HTTP
in the containerd configuration of the node. Geass passes no `--insecure` flag to Kaniko.

## What it does not cover

- **A node's image cache.** Geass refuses a service of an organization that names the image of a different
  organization. But the kubelet starts an image that it already holds without a request to the registry, whatever pull
  secret the pod has. On a shared cluster, also make the kubelet or the API server do a check. You can use one of these:
  - The `AlwaysPullImages` admission plugin. With K3s, set
    `--kube-apiserver-arg=enable-admission-plugins=NodeRestriction,AlwaysPullImages`. This plugin makes the kubelet pull
    the image of every pod with the login of that pod each time the pod starts.
  - The `KubeletEnsureSecretPulledImages` feature of the kubelet. It is alpha since Kubernetes 1.33.
- **A registry that cannot keep logins apart.** The policy keeps the login of the platform away from organizations. It
  cannot make one login of a registry safe to share. The login of each organization must reach only its own path.
- **What an organization's own login can do.** That login belongs to the organization. Geass does not stop the builds
  of the organization when they push to any location that the login reaches. Geass also does not stop its services when
  they run images from a registry that no organization here claims.
- **A pull secret a service names itself** (`spec.source.image.pullSecret`, a Secret in its own namespace). Geass uses
  this pull secret as it is.
- **Databases, object storage, backups and Geass's own pods** do not use a build registry. The policy does not affect
  them.
- **The dashboard has no form for the setting.** Set it with `kubectl`.
- **Outbound limits.** The builds of an organization push to its own registry. If your
  [outbound limits](/docs/guides/outbound-limits/) list `allowedPorts`, list the port of the registry of each
  organization. For most registries, this port is 443. If you do not, the builds cannot push.

## Related

- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/#builds-and-the-registry)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/), [Outbound limits](/docs/guides/outbound-limits/), and [Noisy-neighbour limits](/docs/guides/tenant-limits/): the other settings for a cluster that organizations share
