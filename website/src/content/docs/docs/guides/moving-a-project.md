---
title: "Moving a project to another cluster"
description: "Export a project as a bundle and import it on another cluster, for example a dedicated one. See what moves, what you enter again, and how data follows."
---

You can make a project again on another cluster. For example, a customer on a shared cluster moves to a dedicated
cluster, or you replace a cluster. Two API calls do the move:

- **Export** reads a project and gives you a file, the **bundle**.
- **Import**, on the other cluster, makes a new project from the bundle.

The move changes nothing on the source cluster. Nothing that the import makes runs until you deploy it. The dashboard
and the `geass` command line have no page or command for this feature yet. Use `curl`, as this page shows.

The bundle holds the **definition** of the project: its services, databases, and buckets, with their settings,
hostnames, and schedules. It holds no secret values, and no data from databases or buckets.

:::caution
Keep the original project until you have checked the copy. This feature is new. It ran one time between two real
clusters, but no real database backup was restored with it. See [What was tested](#what-was-tested).
:::

## What moves and what does not

| Part of the project | In the bundle? | What you do |
| --- | --- | --- |
| The project: name, environments, region, required server tags, plain shared variables, pull request settings, GitHub reporting, network isolation, and limits | Yes | Check them on the new project |
| Services: source, port, replicas, size, scaling, healthchecks, hostnames, schedules, and plain variables | Yes, as drafts | Deploy them |
| In-cluster databases: engine, version, size, number of instances, and whether backups are on | Yes, as empty drafts | Move their data, and then deploy. See [Moving the data](#moving-the-data). |
| Buckets on the object storage of the cluster | Yes, as empty drafts | Copy the objects, and then deploy |
| References from a service to a database or bucket of the project | Yes, pointed at the new database or bucket | Nothing |
| **Secret values**: service secrets, secret shared variables, and any value that looks like a secret | No. Each is **named** in the bundle | Enter them again |
| Registry logins and other credentials that a service uses | No. Named | Make them again on the target |
| Default hostnames from the source cluster | No | The target gives new ones when you deploy |
| Your own domains | Yes | Point DNS at the new cluster |
| The GitHub connection and builds | No. Services keep their repository and branch | Connect the new project to GitHub |
| Backup storage and cloud connections (AWS, PlanetScale) | No, because they hold credentials | Make them again |
| External databases and buckets in AWS or PlanetScale | No. Named. Their data stays where it is | Connect them again |
| Logical databases on a shared database server | No. Named | Make a database on the target, and move the data with the tools of the engine |
| Webhooks | No. Named by ID and host only | Add them again |
| API tokens and deploy hooks | No. Counted | Make new ones |
| Which other projects can reach this project | No. Named | Allow them again |
| The organization and workspace | Never | The project goes to the organization and workspace of the importer |
| Builds, deployment history, logs, metrics, usage, and certificates | No | They stay on the source cluster |

Geass treats a value as a secret when its name or content says so:

- The name ends with `PASSWORD`, `SECRET`, `TOKEN`, or `KEY`, or contains `CREDENTIAL`, `DSN`, `PRIVATE`, `AUTH`, and a
  few more.
- The content is a URL with a password, a private key, or the shape of some well-known tokens.

:::caution
Read the bundle before you send it anywhere. A secret under an innocent name, or in the code of a function, is in the
bundle as it is in the project.
:::

## Who can do it

- **Export**: an administrator who can see the project. On a cluster with organizations, that is an organization
  admin, a workspace admin for a project in the workspace, or a platform admin. A viewer, an observer, and a project
  token cannot export.
- **Import**: an administrator of the target cluster, who then owns the new project. The project goes to the
  organization and workspace of the importer, within its project limit. A project that a platform admin imports belongs
  to no organization. To give it to one, a platform admin calls `POST /api/projects/{project}/assign`.

## Export

```bash
curl -X POST https://geass.shared.example/api/v1/projects/proj-1a2b3c4d/export \
  -H "Authorization: Bearer $SOURCE_TOKEN" -o morning-beach-bundle.tar.gz
```

The bundle is a `tar.gz` file of readable YAML files. Do not edit the files. The import checks a checksum of each file.
Use the import options to change the name or the region.

To see what an export leaves for you to do, without the file, add `?summary=true`:

```bash
curl -X POST "https://geass.shared.example/api/v1/projects/proj-1a2b3c4d/export?summary=true" \
  -H "Authorization: Bearer $SOURCE_TOKEN"
```

| Field | Holds |
| --- | --- |
| `toReenter` | Each value to enter again: the service, the name, and the reason |
| `notIncluded` | What the bundle does not carry, each with what to do |
| `data` | For each database and bucket: whether backups are on, when the last backup finished, the backup bucket, and how to move the data |
| `notes` | What Geass changed on the way out, for example a default hostname that it dropped |
| `entries` | The files and their checksums |

An export only reads. It changes nothing on the source cluster.

## Import

Start with a dry run. A dry run does the same checks as the import and changes nothing:

```bash
curl -X POST "https://geass.dedicated.example/api/v1/projects/import?dryRun=true" \
  -H "Authorization: Bearer $TARGET_TOKEN" -H "Content-Type: application/gzip" \
  --data-binary @morning-beach-bundle.tar.gz
```

The response is `{"dryRun": true, "ok": true, "plan": {...}}`. The plan holds these fields:

| Field | Holds |
| --- | --- |
| `project` | The name, region, and environments of the new project |
| `create` | Every resource that the import makes, as a draft |
| `blockers` | What stops the import. The import refuses while there is a blocker |
| `warnings` | What the new project lacks. The import works, but the project does not run correctly until you fix it |
| `toReenter`, `notMoved`, `data` | As in the export summary |
| `next` | What to do after the import, in order |

Then send the same request without `dryRun`. Put the options in the address, or in a JSON body:
`{"bundle": "<the bundle's base64>", "dryRun": false, "name": "", "region": "", "keepProjectId": false}`.

| Option | Meaning |
| --- | --- |
| `name` | The name of the new project. The default is the name in the bundle |
| `region` | Put the project in this region |
| `keepProjectId` | Keep the old project ID. Platform admins only. See [Moving the data](#moving-the-data) |
| `dryRun` | Only report what the import would make |

The response is `201` with the ID of the new project, what the import made, and the rest of the plan:

```json
{
  "project": {"id": "proj-9f8e7d6c", "name": "morning-beach"},
  "created": [{"kind": "apps", "name": "quiet-river", "displayName": "storefront", "environment": "production", "from": "web"}],
  "toReenter": [{"kind": "service", "id": "quiet-river", "name": "storefront", "item": "SESSION_KEY", "reason": "a secret value: Geass does not export secret values"}]
}
```

An import does these things:

- It makes a **new project** with a new ID. It never changes an existing project. Each service, database, and bucket
  gets a new ID and keeps its old name as its display name.
- It makes **drafts**. A draft uses no CPU and does not start.
- It **refuses** and makes nothing when there is a blocker. If a step fails, the import removes what it made.

### What stops an import

| `code` | Meaning |
| --- | --- |
| `invalid-name`, `project-name-taken` | The name is too long or has nothing usable in it, or a project that you can see has the name. The import lowercases the name and removes dashes at the ends, so check the name in the plan |
| `keep-project-id-refused`, `project-id-taken` | You cannot use `keepProjectId`, or the ID exists on the cluster |
| `environment-name`, `bucket-name` | An environment or bucket name is too long with the new project ID |
| `project-limit` | The organization of the importer has all the projects that it can have |
| `region-missing` | No server of the target is in the region. Tag a server, or import with another `region` |
| `tag-missing`, `tag-not-reserved` | The project requires a server tag that no server has, or that is not reserved for the project. Geass never drops such a requirement, for example `hipaa` |
| `object-storage-missing` | The project has buckets, and the target has no object storage |
| `host-unavailable` | A hostname is not valid, is the dashboard hostname, is used by another service, or is outside the domains of the organization |

### What the new project lacks

Warnings do not stop an import:

| `code` | Meaning |
| --- | --- |
| `github-not-connected`, `github-app-missing` | Services build from a repository, and the project is not connected to GitHub, or the cluster has no GitHub App |
| `registry-missing` | A service builds from a repository, and the cluster has no registry |
| `tag-missing` | A resource asks for a server tag that no server has |
| `serverless-missing` | A service sleeps when idle, and the cluster does not have the serverless add-on |
| `ha-needs-nodes` | A database is replicated, and the cluster has fewer than three healthy servers |
| `backup-storage-missing` | Databases have backups on, and the project has no backup storage |
| `domain-dns` | A hostname is your own domain. Point DNS at the new cluster |

The target platform can require a sandbox for the databases of an organization. The dry run does not check this. If an
engine cannot run in the sandbox, the import refuses with the message of the platform.

## After the import

The `next` field of the plan lists these steps for your project. Do them in this order:

1. If a service builds from a repository, connect the new project to GitHub. Open **Settings** → **GitHub** of the
   project, and click **Connect to GitHub**. See [GitHub and builds](/docs/guides/github-and-builds/).
2. Enter the secrets again. `toReenter` names each one. Use the **Variables** tab of each service, and **Settings** →
   **Shared Variables** of the project. See [Environment variables](/docs/guides/environment-variables/).
3. If databases have backups on, select backup storage. See
   [Backups and restore](/docs/guides/backups-and-restore/#choose-where-backups-are-stored).
4. Deploy the databases. If you keep their data, move it first. See [Moving the data](#moving-the-data).
5. Deploy the buckets, and copy the objects into them.
6. Deploy the services. **Deploy changes** deploys services before databases. So if the services need the databases,
   deploy the databases first.
7. Point DNS for your own domains at the new cluster.
8. Check the new project. Only then delete the original. Geass never deletes the source.

## Moving the data

A bundle holds no data. The databases and buckets that an import makes are empty.

### Databases

To move the data of a database, restore its backups on the target:

1. **On the source**, make sure that the database has backups on and that a backup finished. The `data` field of the
   export tells you when. Postgres, and MariaDB with high availability, can restore to any recent moment. MySQL, SQLite,
   and a single-server MariaDB back up once a day. For these engines, stop the writes and wait for a backup first. See
   [Backups and restore](/docs/guides/backups-and-restore/).
2. **On the target**, a platform admin imports the project with `keepProjectId=true`. Geass restores backups only into
   the project with the same ID.
3. **On the target**, a platform admin uses **Recover databases from a bucket**, under **Usage**. Enter the backup bucket
   from the `data` field, its region, and the AWS key that Geass used for backups. See
   [Disaster recovery](/docs/operations/disaster-recovery/#recover-databases-from-a-bucket).
4. **Restore** each database into a new database. See
   [Restore to a new database](/docs/guides/backups-and-restore/#restore-to-a-new-database). A restore makes a new
   database next to the empty one, with a new password.
5. **Reference** the restored database from each service, and delete the empty database that the import made.

If the target cannot keep the ID, use the tools of the engine instead, for example `pg_dump` and `pg_restore`, or
`mysqldump`. An admin gets the connection details with `GET /api/v1/databases/{name}/connection?reveal=true`. A
database is reachable only from inside its cluster. So run the tools from the **Console** tab of a service on each
side.

Redis in Geass is a cache with no persistence. There is nothing to move.

### Buckets

Geass has no call to download or upload the objects of a bucket. A bucket on the object storage of the cluster is
reachable only from inside its cluster. Copy the objects with an S3 client, for example `rclone sync` or
`aws s3 sync --endpoint-url`. Run the client from the **Console** tab of a service on each side. A service that
references a bucket has its login in its variables. See
[Object storage](/docs/guides/object-storage/#credentials-and-how-apps-receive-them).

A bucket in an AWS account does not move. Make its connection again.

Services in Geass have no disks of their own. So there is nothing else to move.

## Limits

A bundle is at most 16 MiB, and at most 64 MiB when expanded. It has at most 500 files, of at most 1 MiB each. Function
code is at most 256 KiB. Geass refuses a larger import with `413`.

Geass reads a bundle as untrusted input, and checks every field. Nothing signs a bundle, so the checksums only catch
damage. Geass refuses a bundle that names a credential, an unknown field, or a newer format.

Geass logs each export and import with who did it, the project, and the counts. It never logs a secret value or name.

## What was tested

Automated tests cover the bundle format, every refusal, every blocker and warning, and the access rules across two
organizations. On 2026-10-28, a project with services, a Postgres database, and a bucket moved between two real
single-server clusters. The import made drafts with new names and no secret values. After the deploy, the services ran
with the new database and bucket.

These cases did **not** run:

- A move with a multi-server or shared source cluster, or with a project token.
- A restore of a real database backup into the imported project, and a recovery from a real bucket.
- The sandbox check for databases.

## Under the hood

The import records who imported the project, when, and from which project, in the annotations
`geass.dev/imported-by`, `geass.dev/imported-at`, and `geass.dev/imported-from` on the new `GeassProject`. The login of
an in-cluster bucket is in the Secret `<bucket ID>-connection` in `geass-system`. To reach a database or the object
storage from outside the cluster, use `kubectl port-forward`.

## Related

- [Projects and environments](/docs/guides/projects-and-environments/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Disaster recovery](/docs/operations/disaster-recovery/)
- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [REST API](/docs/reference/rest-api/)
