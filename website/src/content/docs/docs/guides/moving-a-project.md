---
title: "Moving a project to another cluster"
description: "Export a project as a bundle and import it on another cluster, for example a dedicated one. See what moves, what you enter again, and how data follows."
---

A project can leave one cluster and be made again on another cluster. For example, a customer on a shared cluster buys a dedicated cluster, a test cluster becomes production, or you replace a cluster. Two calls do the move:

- **Export** reads a project and gives you a file, the **bundle**.
- **Import**, on the other cluster, makes a new project from the bundle.

The move changes nothing on the cluster that the project came from. Nothing that the import makes runs until you deploy it.

The bundle holds the **definition** of the project: its services, databases, and buckets, and their settings, hostnames, and schedules. The bundle holds no secret value, and none of the data in the databases and buckets. This page tells you exactly what is in the bundle and what is not, and how to move the rest.

:::caution
First, move a project that you can afford to try, and keep the original until you have checked the copy. This feature is new. It ran one time on two real K3s clusters (2026-10-28), but no real database backup has been restored with it. [What was tested](#what-was-tested) lists what ran and what did not.
:::

## What moves and what does not

| Part of the project | In the bundle? | What you do |
| --- | --- | --- |
| The project: its name, environments, region, required node tags, shared variables with plain values, pull request environment settings, GitHub reporting, network isolation, project and environment limits | Yes | Check them on the new project |
| Services: source (image, repository and branch, or the code of a function), port, replicas, resources, scaling, probes, hostnames, schedules, plain configuration, and variables | Yes, as drafts | Deploy them |
| Databases that run in the cluster: engine, version, size, resources, number of instances, and whether backups are on | Yes, as drafts, empty | Move their data (see [Moving the data](#moving-the-data)), then deploy |
| Buckets on the object storage of the cluster | Yes, as drafts, empty | Copy the objects, then deploy |
| A variable that attaches a service to a database or bucket of the project | Yes, pointed at the new database or bucket | Nothing |
| **Secret values**: the secrets of a service, secret shared variables, and any value that looks like a secret | No. Each is **named** in the bundle | Enter them again |
| Registry logins, image pull secrets, build secrets, and ConfigMaps and Secrets that a service mounts or reads variables from | No. Named | Make them again on the target |
| The default hostname that the source platform gave a service | No | The target gives one when the service is deployed |
| Hostnames that you chose (your own domains) | Yes | Point DNS at the new cluster. The new cluster issues the certificate after DNS points at it |
| The GitHub connection and builds | No. A service keeps its repository and branch | Connect the new project to GitHub |
| Backup storage and cloud connections (AWS, PlanetScale) | No, because they hold credentials | Make them again |
| External databases and buckets in your AWS or PlanetScale account | No. Named. Their data stays where it is | Make the connection and connect them again |
| Logical databases on a shared database server | No. Named | Make a database on a server of the target, and move its data with the tools of the engine |
| Webhooks | No. Named, by id and host only, because an address can carry a token | Add them again |
| API tokens and deploy hooks | No. Counted | Make new ones |
| Which other projects can reach this project | No. Named | Allow them again after both projects are there |
| The organization and workspace that the project belongs to | Never. The bundle names neither | Geass uses the organization and workspace of the importer |
| Builds, deployment history, logs, metrics, usage, certificates | No | They belong to the cluster that made them |

Geass treats a value as a secret when its **name** or its **content** says so:

- The name has `PASSWORD`, `SECRET`, `TOKEN`, or `KEY` at the end, or has `CREDENTIAL`, `DSN`, `PRIVATE`, `AUTH`, and a few more in it.
- The content is a URL with a password in it, a private key, or one of the shapes of some well-known tokens.

Geass leaves such a value out of the bundle and lists it. This rule is a rule of thumb, not a guarantee. A secret kept under an innocent name in plain configuration, or in the code of a function, is in the bundle exactly as it is in the project. Read the bundle before you send it anywhere.

## Who may do it

- **Export**: an administrator who can see the project. On a cluster that holds organizations, these administrators can export: the administrators of the organization, a workspace administrator for a project in the workspace, or the administrators of the platform. A viewer, an observer, and a token that belongs to one project cannot export.
- **Import**: an administrator of the cluster that receives the bundle. That administrator then owns the new project. A project of an organization goes to the organization (and the workspace) of the person who imports it. The limit of the organization on projects applies.

The person who exports and the person who imports can be two people on two clusters, each with their own token. A platform administrator can do both for any project. A project that a platform administrator imports belongs to no organization, the same as a project that the platform makes. To give the project to an organization, use `POST /api/projects/{project}/assign`. That call is also a call for a platform administrator only.

## Export

```bash
curl -X POST https://geass.shared.example/api/v1/projects/proj-1a2b3c4d/export \
  -H "Authorization: Bearer $SOURCE_TOKEN" -o morning-beach-bundle.tar.gz
```

The response is the bundle: a gzip-compressed tar that you can open with `tar -xzf`. Its files are YAML that you can read: `geass-bundle.json` (the manifest), `project.yaml`, and one file for each service, database, and bucket. Do not edit the files. The manifest holds a checksum of each file, and an import refuses a bundle that does not match the manifest. The manifest must be the first entry of the archive. An import refuses an archive that starts with anything else as "not a Geass project bundle". The import options cover what people change, for example a new name or another region.

To see what an export would leave for you to do, without the file, add `?summary=true`. The response is the manifest as JSON:

```bash
curl -X POST "https://geass.shared.example/api/v1/projects/proj-1a2b3c4d/export?summary=true" \
  -H "Authorization: Bearer $SOURCE_TOKEN"
```

| Field | Holds |
| --- | --- |
| `toReenter` | Every value to enter again: the service, the name of the secret or variable, and the reason |
| `notIncluded` | What a bundle does not carry, as the table above says, each with what to do |
| `data` | For each database and bucket: a statement that its data is not in the bundle, whether its backups are on, when the last backup finished, the name of the archive, the bucket that the backups are in, and how to move the data |
| `notes` | Plain sentences about what Geass changed on the way out, for example a default hostname that Geass dropped |
| `entries` | The files and their checksums |

An export only reads. An export writes nothing to the source cluster and changes no object on it. The log of the operator records the export (see [What is logged](#what-is-logged)).

## Import

Send the bundle to the target cluster. **Start with a dry run.** A dry run does the same checks as the import and changes nothing. The dry run tells you what the import would make and what the cluster lacks:

```bash
curl -X POST "https://geass.dedicated.example/api/v1/projects/import?dryRun=true" \
  -H "Authorization: Bearer $TARGET_TOKEN" -H "Content-Type: application/gzip" \
  --data-binary @morning-beach-bundle.tar.gz
```

The response is `{"dryRun": true, "ok": true, "plan": {...}}`. The plan holds these fields:

| Field | Holds |
| --- | --- |
| `project` | The name, region, and environments of the project that the import would make. Also its id, when you ask to keep the old id |
| `create` | Every object that the import would make, as a `draft` |
| `blockers` | What stops the import. Each blocker has a `code`, what it is about, and a sentence. The import refuses while there is a blocker |
| `warnings` | What the new project lacks. The import makes the project, but the project does not work until you correct the problem |
| `toReenter`, `notMoved`, `data` | As in the manifest |
| `next` | What to do after the import, in order |

Then import the bundle with the same request, without `dryRun`. Put the options in the address, or in a JSON body. The JSON body has this shape: `{"bundle": "<the bundle's base64>", "dryRun": false, "name": "", "region": "", "keepProjectId": false}`.

| Option | Meaning |
| --- | --- |
| `name` | The name of the new project. The default is the name in the bundle |
| `region` | Put the project in this region, not the region of the bundle |
| `keepProjectId` | Make the project under the id that it had. Only for the administrators of the platform. For the reason to use this option, see [Moving the data](#moving-the-data) |
| `dryRun` | Only report what the import would make |

The response is `201` with the id of the new project, what the import made, and the `warnings`, `toReenter`, `notMoved`, `data`, and `next` of the plan. The response uses the names of the new objects in place of the old names:

```json
{
  "project": {"id": "proj-9f8e7d6c", "name": "morning-beach"},
  "created": [{"kind": "apps", "name": "quiet-river", "displayName": "storefront", "environment": "production", "from": "web"}],
  "toReenter": [{"kind": "service", "id": "quiet-river", "name": "storefront", "item": "SESSION_KEY", "reason": "a secret value: Geass does not export secret values"}]
}
```

An import does these things:

- It makes a **new project**. It never changes an existing project. The project gets a new id, so it cannot clash with another project, including a project of another organization. The services, databases, and buckets also get new names. A service name is unique on the whole cluster. Each service, database, and bucket keeps the name that people knew it by as its display name.
- It makes **drafts**. A draft service, database, or bucket is saved and not running: it uses no CPU and does not start. The import makes no Secret. You put in place what the services need, and then you deploy.
- It **refuses**, and makes nothing, when a blocker stands in the way. If a step fails half way, the import removes what it made, and it removes the project last.
- It records on the new project who imported it, when, and from which project. It uses the annotations `geass.dev/imported-by`, `geass.dev/imported-at`, and `geass.dev/imported-from`.

### What stops an import

| `code` | Meaning |
| --- | --- |
| `invalid-name`, `project-name-taken` | The name is too long (70 characters were refused) or has nothing usable in it (`!!!` was refused). Or a project that you can see already has the name. The import lowers upper case and drops dashes at the ends without a refusal (`UPPER` became `upper`, `-bad-` became `bad`), so check the name in the plan. The import does not look at projects of other organizations, and does not reveal them |
| `keep-project-id-refused`, `project-id-taken` | Someone who does not have the permission asked for `keepProjectId`, or the id exists on the cluster |
| `environment-name`, `bucket-name` | An environment name or a bucket name is too long after the id of the new project is put in front of it |
| `project-limit` | The organization of the importer has all the projects that it can have |
| `region-missing` | No node of the target is in the region of the project. Tag a node, or import with another `region` |
| `tag-missing`, `tag-not-reserved` | The project requires a node tag that no node has, or that nodes carry without a reservation for the project. Geass never silently drops a requirement of this kind, for example `hipaa` |
| `object-storage-missing` | The project has buckets, and the target has no object storage |
| `host-unavailable` | A hostname is not valid, is the hostname of the dashboard, is served by another service, or is outside the domains that the organization of the importer can use. Another service includes the source project, if the target is the same cluster |

### What the new project lacks

Warnings do not stop an import:

| `code` | Meaning |
| --- | --- |
| `github-not-connected`, `github-app-missing` | Services build from a repository, and the new project is not connected to GitHub. Or the cluster has no GitHub App |
| `registry-missing` | A service builds from a repository, names no registry of its own, and the cluster has none |
| `tag-missing` | A service, database, or bucket asks for a node tag that no node has |
| `serverless-missing` | A service scales to zero, and the cluster does not have the serverless add-on |
| `ha-needs-nodes` | A database is replicated, and the cluster has fewer than three healthy nodes |
| `backup-storage-missing` | Databases have backups on, and the new project has nowhere to store them |
| `domain-dns` | A hostname is your own domain. DNS must point at the new cluster |

The dry run does not check one thing that the import checks. The target platform can require a sandbox for the databases of an organization. The import checks whether an engine can run in that sandbox, before it makes the first object of that kind. If the engine cannot run in the sandbox, the import refuses with the message of the platform.

## After the import, in this order

The `next` field of the plan lists these steps for the project that it describes.

1. **Connect the new project to GitHub**, if any service builds from a repository. Use `POST /api/v1/projects/{project}/github/install`. See [GitHub and builds](/docs/guides/github-and-builds/).
2. **Enter the secrets again.** `toReenter` names each one. Use these calls:
   - For the secret of a service, use `POST /api/v1/apps/{name}/secrets/set` with `key` and `value`.
   - For a value that Geass left out because it looks like a secret, but that is plain configuration, use `POST /api/v1/apps/{name}/config/set`.
   - For a shared variable, use `POST /api/v1/projects/{project}/variables/save`.

   See [Environment variables](/docs/guides/environment-variables/).
3. **Select backup storage** for the project, if databases have backups on. See [Backups and restore](/docs/guides/backups-and-restore/#choose-where-backups-are-stored).
4. **Deploy the databases** (`POST /api/v1/databases/{name}/deploy`). If you keep their data, move the data.
5. **Deploy the buckets**, and copy the objects into them.
6. **Deploy the services** (`POST /api/v1/apps/{name}/deploy`). A deploy of a whole environment at one time (`POST /api/v1/projects/{project}/deploy`) starts its services before its databases. So if the services need the databases, deploy the databases first.
7. **Point DNS** for your own domains at the new cluster. Check the project, and only then delete the original on the source cluster. Geass never deletes the source.

## Moving the data

A bundle holds no data. The databases that an import makes are **empty**, and the buckets that it makes are empty too.

### Databases

Geass has no copy of the data of a database in the bundle. But Geass already makes backups to a bucket in your AWS account, and another cluster has a way to find them. To move the data of a database, use those backups, by hand:

1. **On the source**, make sure that the database has backups on and that a backup has finished. The `data` field of the export tells you when. You can restore Postgres, and MariaDB with high availability, to any recent moment. Geass copies MySQL, SQLite, and a MariaDB that runs as one server once a day. For these engines, what was written since the last copy is lost. So first stop what writes to the database, and wait for a copy. See [Backups and restore](/docs/guides/backups-and-restore/).
2. **On the target**, a platform administrator imports the project with `keepProjectId=true`. Geass files backups under the id of the project, and restores them only into the project with that id. With a new id, Geass cannot restore the backups into the new project.
3. **On the target**, a platform administrator uses **Recover databases from a bucket**, under **Usage**. The administrator gives the bucket named in the `data` field of the export, its region, and the AWS key that Geass used for backups. Geass then makes a record of the backups of each database in the project, which is already there. See [Disaster recovery](/docs/operations/disaster-recovery/#recover-databases-from-a-bucket).
4. **Restore** each database into a new database. Use `POST /api/v1/databases/restore` with the archive and a moment or a restore point. A restore never replaces a database. A restore makes a new database next to the empty database that the import made, with a new password and connection secret.
5. **Attach** each service to the restored database. Use `POST /api/apps/{name}/attach` with `kind=database` and the name of the restored database. Then delete the empty database that the import made.

The target might not keep the id: for example, a copy, or a project that moves in as a new project on a cluster that holds organizations. In that case, the data cannot follow this way. Use the tools of the engine instead (`pg_dump` and `pg_restore`, `mysqldump`) with the connection details of both databases. An administrator gets these details with `GET /api/v1/databases/{name}/connection?reveal=true`. A database is reachable only from inside its cluster. So run the tools from the console of a service on each side, or through `kubectl port-forward`.

Redis in Geass is a cache with no persistence. There is nothing to move.

### Buckets

Geass has no call that downloads or uploads the objects of a bucket. A bucket on the object storage of the cluster is reachable only from inside its cluster. Its login is in the Secret `<bucket's id>-connection` in `geass-system`. Only someone who can run `kubectl` against the cluster can read that Secret. Copy the objects with an S3 client that can reach both buckets, for example `rclone sync` or `aws s3 sync --endpoint-url`. Run the client from the console of a service on each side, or through `kubectl port-forward` to the object storage service. See [Object storage](/docs/guides/object-storage/#credentials-and-how-apps-receive-them).

A bucket in an AWS account is not a bucket of the cluster, and it does not move. You only have to make its connection again.

### Volumes

Services in Geass have no volumes. The volumes of databases and of the object storage server of the cluster hold the data described above. Geass offers no way to export a volume.

## Limits

An archive is at most 16 MiB, and at most 64 MiB when expanded. An archive has at most 500 files, of at most 1 MiB each. The code of a function is at most 256 KiB. Geass refuses an import over a limit with `413`. The limits are about ten times what a large project needs.

Geass reads a bundle as untrusted input. Nothing signs a bundle, so the checksums only catch damage. Geass checks every field as if the person who imports typed it. An export never writes a reference to a Secret, a ConfigMap, a registry login, or a connection of the cluster that reads the bundle. Geass refuses a bundle that names one of these with `400`. Geass also refuses an unknown field. If a bundle has a newer format, Geass says so, and does not read half of the bundle.

## What is logged

The operator logs one line for each export, planned import, refused import, and import. Each line has who did it, the project, the number of services, databases, and buckets, and the number of values to enter again. The operator logs no value and no name of a secret key. The same lines show when an import failed and was rolled back. A refused or rolled-back import leaves no object behind.

## What was tested

These tests ran, and passed:

- The bundle. The tests checked these items:
  - Geass writes a bundle and reads it back.
  - The same project gives the same bytes.
  - Every refusal:
    - Not a gzip, and not a tar.
    - A link or a path that leaves the directory.
    - A file that does not match its checksum or is not in the manifest.
    - Too large, and a decompression bomb.
    - A newer format.
    - Secrets other than excluded.
    - An unknown field.
    - Every reference to a Secret, ConfigMap, login, or connection that a bundle must not carry.
  - No secret value, no organization, and no cluster name reaches a file.
- The planner: every blocker and warning in the tables above, against a description of the target.
- The routes, against a fake cluster with two organizations. The tests checked these items:
  - Who can export and import.
  - Another organization is told that a project is not there.
  - A dry run changes nothing.
  - Names and ids.
  - A refused import changes nothing.
  - A failure half way removes what the import made.
  - Geass refuses a bundle with a Secret named in it.
  - The logged lines.
  - The new project belongs to the person who imported it.

  The routes are in the suite that tries every route as another organization.
- Against a real API server (envtest): the CRDs accept the objects that an import makes. The project controller makes the environments of the new project and their network policies.
- **On two real K3s clusters on EC2 (2026-10-28, K3s v1.36.4, Geass built from this branch, a project with services, a Postgres with two rows, and an object store):**
  - An export gave a 2 KB bundle. None of the secret values set on the project could be found in the bundle. A secret kept under an innocent name, `GREETING`, was exported as it was, as this page says that it would be.
  - A dry run into a cluster with no object storage said `ok: false` with `object-storage-missing`, and created nothing. The same bundle without `dryRun` answered 409 with the plan.
  - After object storage was set up, a dry run said `ok: true`, and nothing was created.
  - The real import made the project, the database, the bucket, and three services as drafts under new names. The import made no pod and no Secret. The import rewrote the attach variables to the new database and bucket, and added the three annotations. The database arrived empty.
  - A repeat of the same import was refused (`host-unavailable`, `project-name-taken`). A region that the cluster lacks gave `region-missing`. `name` and `keepProjectId` worked, and a second `keepProjectId` gave `project-id-taken`.
  - These bodies were refused with 400, and they left the cluster as it was:
    - Truncated, random, empty, and non-tar bodies.
    - A changed file.
    - A newer format.
    - An unknown field.
    - A path outside the directory.
    - A bundle that names a Secret or ConfigMap of the importing cluster. For this test, the checksums were corrected, so only the rule refused the bundle.
  - These bodies were refused with 413: a 17 MiB body, a 23 MiB JSON body, a 70 MiB decompression bomb, and a 2 MiB file in a valid archive.
  - A viewer token was refused on both routes, including a dry run.
  - What the import made was then deployed. The service ran with its hostname, a database address and bucket of the new project, and none of the secret variables. The worker ran after its secret was entered again.
  - The own route of rungeass that calls these two routes ran against both clusters (not through a cell).

These did **not** run, and nothing here says that they work:

- A move on a cluster of more than one node, a shared cluster as the source, or a project token.
- A restore of any database from a real backup into the imported project, and a recovery from a real bucket. The steps under [Databases](#moving-the-data) use features that exist and are documented, but nothing here tried them together.
- A service that reads a variable attached to a *restored* database. A deployed service read the address of the new, empty database.
- The dashboard page for any of this: there is none yet, only the API.
- The command line and the MCP server: `geass api` prints JSON and cannot receive the bundle, so use `curl` as shown above.
- The sandbox check for a database on a platform with `spec.tenancy.sandbox`. The import has the check, but no test makes the check refuse.

## Related

- [Projects and environments](/docs/guides/projects-and-environments/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Disaster recovery](/docs/operations/disaster-recovery/)
- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [REST API](/docs/reference/rest-api/)
