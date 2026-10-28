---
title: "Moving a project to another cluster"
description: "Export a project as a bundle and import it as a new project on another cluster, such as a dedicated one. What moves, what you enter again, and how data follows."
---

A project can leave one cluster and be made again on another: a customer on a shared cluster that buys a dedicated one, a test cluster that becomes production, or a cluster you are replacing. Two calls do it. **Export** reads a project and gives you a file, the **bundle**. **Import**, on the other cluster, makes a new project from the bundle. Nothing is changed on the cluster the project came from, and nothing the import makes runs until you deploy it.

The bundle holds the project's **definition**: its services, databases and buckets, their settings, hostnames and schedules. It holds no secret value and none of the data in the databases and buckets. This page says exactly what is and is not in it, and how to move the rest.

:::caution
This feature is new and has only been tried against fakes of the cluster and a real API server, not against two real clusters, and no real database backup has been restored by it. [What was tested](#what-was-tested) lists what ran and what did not. Move a project you can afford to try first, and keep the original until you have checked the copy.
:::

## What moves and what does not

| Part of the project | In the bundle? | What you do |
| --- | --- | --- |
| The project: its name, environments, region, required node tags, shared variables with plain values, pull request environment settings, GitHub reporting, network isolation, project and environment limits | Yes | Check them on the new project |
| Services: source (image, repository and branch, or a function's code), port, replicas, resources, scaling, probes, hostnames, schedules, plain configuration and variables | Yes, as drafts | Deploy them |
| Databases that run in the cluster: engine, version, size, resources, how many instances, whether backups are on | Yes, as drafts, empty | Move their data (see [Moving the data](#moving-the-data)), then deploy |
| Buckets on the cluster's object storage | Yes, as drafts, empty | Copy the objects, then deploy |
| A variable that attaches a service to a database or bucket of the project | Yes, pointed at the new database or bucket | Nothing |
| **Secret values**: a service's secrets, secret shared variables, and any value that looks like a secret | No. Each is **named** in the bundle | Enter them again |
| Registry logins, image pull secrets, build secrets, ConfigMaps and Secrets a service mounts or reads variables from | No. Named | Make them again on the target |
| The default hostname the source platform gave a service | No | The target gives one when the service is deployed |
| Hostnames you chose (your own domains) | Yes | Point DNS at the new cluster; the certificate is issued there once DNS does |
| The GitHub connection and builds | No. A service keeps its repository and branch | Connect the new project to GitHub |
| Backup storage and cloud connections (AWS, PlanetScale) | No: they hold credentials | Make them again |
| External databases and buckets in your AWS or PlanetScale account | No. Named. Their data stays where it is | Make the connection and connect them again |
| Logical databases on a shared database server | No. Named | Make a database on a server of the target and move its data with the engine's tools |
| Webhooks | No. Named, by id and host only (an address can carry a token) | Add them again |
| API tokens and deploy hooks | No. Counted | Make new ones |
| Which other projects may reach this one | No. Named | Allow them again once both projects are there |
| The organization and workspace the project belongs to | Never. The bundle names neither | The importer's organization and workspace are used |
| Builds, deployment history, logs, metrics, usage, certificates | No | They belong to the cluster that made them |

A value is treated as a secret when its **name** says so (it has `PASSWORD`, `SECRET`, `TOKEN`, `KEY` at the end, `CREDENTIAL`, `DSN`, `PRIVATE`, `AUTH` and a few more in it) or its **content** does (a URL with a password in it, a private key, and the shapes of some well-known tokens). It is left out of the bundle and listed. This is a rule of thumb, not a guarantee: a secret kept under an innocent name in plain configuration, or inside a function's code, is in the bundle exactly as it is in the project. Read the bundle before you send it anywhere.

## Who may do it

- **Export**: an administrator who can see the project. On a cluster that holds organizations, that is the organization's administrators, a workspace administrator for a project in the workspace, or the platform's. A viewer, an observer and a token that belongs to one project cannot.
- **Import**: an administrator of the cluster that receives it, who then owns the new project: an organization's project goes to the organization (and the workspace) of whoever imports. The organization's limit on projects applies.

The person who exports and the person who imports can be two people on two clusters, each with their own token. A platform administrator can do both for any project. A project that a platform administrator imports belongs to no organization, as one the platform makes does; give it to one with `POST /api/projects/{project}/assign`, which is a platform administrator's call as well.

## Export

```bash
curl -X POST https://geass.shared.example/api/v1/projects/proj-1a2b3c4d/export \
  -H "Authorization: Bearer $SOURCE_TOKEN" -o morning-beach-bundle.tar.gz
```

The answer is the bundle, a gzip-compressed tar you can open with `tar -xzf`. Its files are YAML you can read: `geass-bundle.json` (the manifest), `project.yaml`, and one file for each service, database and bucket. Do not edit them: the manifest holds a checksum of each, and an import refuses a bundle that does not match it. The import's options (a new name, another region) cover what people change.

To see what an export would leave for you to do, without the file, add `?summary=true`. The answer is the manifest as JSON:

```bash
curl -X POST "https://geass.shared.example/api/v1/projects/proj-1a2b3c4d/export?summary=true" \
  -H "Authorization: Bearer $SOURCE_TOKEN"
```

| Field | Holds |
| --- | --- |
| `toReenter` | Every value to enter again: the service, the name of the secret or variable, and why |
| `notIncluded` | What a bundle does not carry, as the table above says, each with what to do |
| `data` | For each database and bucket: that its data is not in the bundle, whether its backups are on, when the last finished, the archive's name and the bucket they are in, and how to move it |
| `notes` | Plain sentences about what was changed on the way out, such as a default hostname that was dropped |
| `entries` | The files and their checksums |

An export only reads. It writes nothing to the source cluster and changes no object on it. The operator's log records it (see [What is logged](#what-is-logged)).

## Import

Send the bundle to the target cluster. **Start with a dry run.** It runs the same checks as the import, says what would be made and what the cluster lacks, and changes nothing:

```bash
curl -X POST "https://geass.dedicated.example/api/v1/projects/import?dryRun=true" \
  -H "Authorization: Bearer $TARGET_TOKEN" -H "Content-Type: application/gzip" \
  --data-binary @morning-beach-bundle.tar.gz
```

The answer is `{"dryRun": true, "ok": true, "plan": {...}}`. The plan holds:

| Field | Holds |
| --- | --- |
| `project` | The name, region and environments of the project that would be made, and its id when you ask to keep the old one |
| `create` | Every object it would make, as a `draft` |
| `blockers` | What stops the import. Each has a `code`, what it is about, and a sentence. The import refuses while there is one |
| `warnings` | What the new project will lack: it is made, and will not work until you put it right |
| `toReenter`, `notMoved`, `data` | As in the manifest |
| `next` | What to do after the import, in order |

Then import it, with the same request without `dryRun`. The options are in the address, or in a JSON body (`{"bundle": "<the bundle's base64>", "dryRun": false, "name": "", "region": "", "keepProjectId": false}`):

| Option | Meaning |
| --- | --- |
| `name` | The new project's name. The default is the name in the bundle |
| `region` | Put the project in this region instead of the bundle's |
| `keepProjectId` | Make the project under the id it had. Only for the platform's administrators, and see [Moving the data](#moving-the-data) for why you would |
| `dryRun` | Only say what would be made |

The answer is `201` with the id of the new project, what was made, and the plan's `warnings`, `toReenter`, `notMoved`, `data` and `next`, with the names of the new objects in place of the old ones:

```json
{
  "project": {"id": "proj-9f8e7d6c", "name": "morning-beach"},
  "created": [{"kind": "apps", "name": "quiet-river", "displayName": "storefront", "environment": "production", "from": "web"}],
  "toReenter": [{"kind": "service", "id": "quiet-river", "name": "storefront", "item": "SESSION_KEY", "reason": "a secret value: Geass does not export secret values"}]
}
```

What an import does:

- It makes a **new project**. It never changes an existing one. The project gets a new id, so it cannot clash with another project, including one of another organization. The services, databases and buckets get new names too (a service name is unique on the whole cluster); each keeps the name people knew it by as its display name.
- It makes **drafts**. A draft service, database or bucket is saved and not running: it uses no CPU and does not start. No secret is made. You put what the services need in place, then deploy.
- It **refuses**, and makes nothing, when a blocker stands in the way. If a step fails half way, what it made is removed again, the project last.
- It records on the new project who imported it, when, and from which project: the annotations `geass.dev/imported-by`, `geass.dev/imported-at` and `geass.dev/imported-from`.

### What stops an import

| `code` | Meaning |
| --- | --- |
| `invalid-name`, `project-name-taken` | The name is not 1 to 63 letters, numbers and dashes, or a project you can see already has it. Projects of other organizations are not looked at and not revealed |
| `keep-project-id-refused`, `project-id-taken` | `keepProjectId` was asked for by someone who may not, or the id exists on the cluster |
| `environment-name`, `bucket-name` | An environment or a bucket name is too long once the new project's id is in front of it |
| `project-limit` | The importer's organization has all the projects it may have |
| `region-missing` | No node of the target is in the project's region. Tag a node, or import with another `region` |
| `tag-missing`, `tag-not-reserved` | The project requires a node tag that no node has, or that nodes carry without being reserved for it. A requirement of this kind, such as `hipaa`, is never dropped silently |
| `object-storage-missing` | The project has buckets and the target has no object storage |
| `host-unavailable` | A hostname is not valid, is the dashboard's, is served by another service (this includes the source project, if the target is the same cluster), or is outside the domains the importer's organization may use |

### What the new project lacks

Warnings do not stop an import:

| `code` | Meaning |
| --- | --- |
| `github-not-connected`, `github-app-missing` | Services build from a repository, and the new project is not connected to GitHub; or the cluster has no GitHub App |
| `registry-missing` | A service builds from a repository, names no registry of its own, and the cluster has none |
| `tag-missing` | A service, database or bucket asks for a node tag no node has |
| `serverless-missing` | A service scales to zero, and the cluster does not have the serverless add-on |
| `ha-needs-nodes` | A database is replicated and the cluster has fewer than three healthy nodes |
| `backup-storage-missing` | Databases have backups on, and the new project has nowhere to store them |
| `domain-dns` | A hostname is your own domain: DNS must point at the new cluster |

The dry run does not check one thing the import does: whether an engine can run in the sandbox that the target's platform may require of an organization's databases. The import checks it before it makes the first object of that kind, and refuses with the platform's message.

## After the import, in this order

The plan's `next` lists these for the project it describes.

1. **Connect the new project to GitHub**, if any service builds from a repository (`POST /api/v1/projects/{project}/github/install`). See [GitHub and builds](/docs/guides/github-and-builds/).
2. **Enter the secrets again.** `toReenter` names each one. A service's secret: `POST /api/v1/apps/{name}/secrets/set` with `key` and `value`. A value that was left out for looking like a secret but is plain configuration: `POST /api/v1/apps/{name}/config/set`. A shared variable: `POST /api/v1/projects/{project}/variables/save`. See [Environment variables](/docs/guides/environment-variables/).
3. **Choose backup storage** for the project, if databases have backups on: [Backups and restore](/docs/guides/backups-and-restore/#choose-where-backups-are-stored).
4. **Deploy the databases** (`POST /api/v1/databases/{name}/deploy`), and move their data if you are keeping it.
5. **Deploy the buckets**, and copy the objects into them.
6. **Deploy the services** (`POST /api/v1/apps/{name}/deploy`). Deploying a whole environment at once, `POST /api/v1/projects/{project}/deploy`, starts its services before its databases, so deploy the databases first when the services need them.
7. **Point DNS** for your own domains at the new cluster, check the project, and only then delete the original on the source cluster. Geass never deletes the source.

## Moving the data

A bundle holds no data. The databases an import makes are **empty**, and so are its buckets.

### Databases

Geass has no copy of a database's data inside the bundle. What it has is the backups it already makes to a bucket in your AWS account, and a way for another cluster to find them. Moving a database's data uses those, by hand:

1. **On the source**, make sure the database has backups on and a backup has finished (`data` in the export says when). Postgres, and MariaDB with high availability, can be restored to any recent moment. MySQL, SQLite and a MariaDB that runs as one server are copied once a day, and what was written since the last copy is lost: stop what writes to the database first and wait for a copy. See [Backups and restore](/docs/guides/backups-and-restore/).
2. **On the target**, import the project with `keepProjectId=true` (a platform administrator). Backups are filed under the project's id, and Geass restores them only into the project with that id. With a new id, the backups cannot be restored into the new project.
3. **On the target**, a platform administrator uses **Recover databases from a bucket** (under **Usage**) with the bucket named in the export's `data`, its region, and the AWS key Geass used for backups. It makes a record of each database's backups in the project, which is already there. See [Disaster recovery](/docs/operations/disaster-recovery/#recover-databases-from-a-bucket).
4. **Restore** each database into a new database (`POST /api/v1/databases/restore` with the archive and a moment or restore point). A restore never replaces: it makes a new database next to the empty one the import made, with a new password and connection secret.
5. **Attach** each service to the restored database (`POST /api/apps/{name}/attach` with `kind=database` and the restored database's name), then delete the empty database the import made.

If the target is not meant to keep the id (a copy, or a project that moves in as a new project on a cluster that holds organizations), the data cannot follow this way. Use the engine's own tools instead (`pg_dump` and `pg_restore`, `mysqldump`) with the connection details of both databases (`GET /api/v1/databases/{name}/connection?reveal=true`, for an administrator). A database is reachable only from inside its cluster, so run the tools from a service's console on each side, or through `kubectl port-forward`.

Redis in Geass is a cache with no persistence: there is nothing to move.

### Buckets

Geass has no call that downloads or uploads the objects of a bucket. A bucket on the cluster's object storage is reachable only from inside its cluster, and its login is in the Secret `<bucket's id>-connection` in `geass-system`, which only someone who can run `kubectl` against the cluster can read. Copy the objects with an S3 client (`rclone sync`, `aws s3 sync --endpoint-url`) that can reach both: from a service's console on each side, or through `kubectl port-forward` to the object storage service. See [Object storage](/docs/guides/object-storage/#credentials-and-how-apps-receive-them).

A bucket in an AWS account is not a bucket of the cluster and does not move: only its connection has to be made again.

### Volumes

Services in Geass have no volumes. The volumes of databases and of the cluster's object storage server hold the data described above. Geass offers no way to export a volume.

## Limits

An archive is at most 16 MiB, 64 MiB when expanded, with at most 500 files of at most 1 MiB each (a function's code is at most 256 KiB). An import over a limit is refused with `413`. The limits are about ten times what a large project needs.

A bundle is read as untrusted input. Nothing signs it, so the checksums only catch damage. Every field is checked as if it were typed by the person importing, and a bundle that names a Secret, a ConfigMap, a registry login or a connection of the cluster that reads it, which an export never writes, is refused with `400`. An unknown field is refused too: a bundle made by a newer format is told so, and is not half read.

## What is logged

The operator logs one line for each export, planned import, refused import and import, with who did it, the project, and how many services, databases and buckets and how many values to enter again. It logs no value and no name of a secret key. The same lines show when an import failed and was rolled back. A refused or rolled-back import leaves no object behind.

## What was tested

These ran, and passed:

- The bundle: writing and reading it back, the same bytes for the same project, every refusal (not a gzip, not a tar, a link or a path that leaves the directory, a file that does not match its checksum or is not in the manifest, too large and a decompression bomb, a newer format, secrets other than excluded, an unknown field, and every reference to a Secret, ConfigMap, login or connection a bundle must not carry), and that no secret value, no organization and no cluster name reaches a file.
- The planner: every blocker and warning in the tables above, against a description of the target.
- The routes, against a fake cluster with two organizations: who may export and import, that another organization is told a project is not there, a dry run changes nothing, names and ids, a refused import changes nothing, a failure half way removes what was made, a bundle with a Secret named in it is refused, the logged lines, and that the new project belongs to whoever imported it. The routes are in the suite that tries every route as another organization.
- Against a real API server (envtest): the objects an import makes are accepted by the CRDs, and the project controller makes the new project's environments and their network policies.

These did **not** run, and nothing here says they work:

- A move between two real clusters, or on K3s. No cluster was available.
- Restoring any database from a real backup into the imported project, and recovering from a real bucket. The steps under [Databases](#moving-the-data) use features that exist and are documented, and nothing here tried them together.
- Deploying what an import made, and a service reading a variable attached to a restored database.
- The dashboard's page for any of this: there is none yet, only the API.
- The command line and the MCP server: `geass api` prints JSON and cannot receive the bundle, so use `curl` as above.
- The sandbox check for a database on a platform with `spec.tenancy.sandbox` (the import has the check; no test makes it refuse).

## Related

- [Projects and environments](/docs/guides/projects-and-environments/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Disaster recovery](/docs/operations/disaster-recovery/)
- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [REST API](/docs/reference/rest-api/)
