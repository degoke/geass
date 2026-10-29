import { projectResources } from "@/lib/project-resources";
import { resourceDisplayName, resourceName } from "@/lib/api";
import { databaseHasVolume } from "@/lib/capacity";
import { databaseVersion } from "@/lib/dashboard";
import { resourceHref } from "@/lib/resource-links";
import { functionSourceLabel } from "@/lib/functions";
import { backupSpec, backupWarning, isSnapshotDatabase, supportsBackups } from "@/lib/backups";
import { isResourceDeleting, pendingChangeCount, pendingChangeKinds, resourceStatus, usesDeployWorkflow } from "@/lib/services";
import { ProjectCanvas } from "@/components/ProjectCanvas";
import { ResourceDialog } from "@/components/ResourceDialog";
import { ProjectChrome } from "@/layout/ProjectChrome";

function storageLabel(value) {
  return String(value || "").replace(/(\d)\s*Gi$/, "$1 GiB").replace(/(\d)\s*Mi$/, "$1 MiB").replace(/(\d)\s*Ti$/, "$1 TiB");
}

/** The line under a database's name: where it lives, how big it is and whether it is backed up. */
function databaseSource(item) {
  if (item.spec?.placement === "External") return item.spec?.provider || "External";
  const parts = [];
  if (databaseHasVolume(item.spec?.engine) && item.spec?.storageSize) parts.push(storageLabel(item.spec.storageSize));
  if (supportsBackups(item) && backupSpec(item).enabled) parts.push(isSnapshotDatabase(item) ? "daily backup" : "continuous backup");
  return parts.join(" · ") || item.spec?.engine || "Managed";
}

export function WorkspacePage({ project, data, environment }) {
  const name = resourceName(project);
  const resources = projectResources(data, name, environment);
  // Connection secrets identify which database or bucket a service references through its env.
  const secretOwners = new Map();
  [...resources.databases, ...resources.stores].forEach((item) => {
    const secret = item.status?.connectionSecret;
    if (secret) secretOwners.set(secret, resourceName(item));
  });
  const referencedResources = (app) => {
    const refs = new Set();
    for (const variable of app.spec?.env || []) {
      const owner = secretOwners.get(variable.valueFrom?.secretKeyRef?.name);
      if (owner) refs.add(owner);
    }
    return [...refs];
  };

  const rows = [
    ...resources.apps.map((item) => {
      const isFunction = Boolean(item.spec?.source?.function);
      return {
        item,
        kind: "Service",
        sourceKind: isFunction ? "function" : item.spec?.source?.git ? "git" : "image",
        meta: isFunction ? "function" : "app",
        source: (isFunction ? functionSourceLabel(item.spec.source.function) : "")
          || (item.spec?.source?.git?.repository
            ? `${item.spec.source.git.repository}${item.spec.source.git.branch ? `@${item.spec.source.git.branch}` : ""}`
            : "")
          || item.spec?.source?.image?.image
          || item.spec?.source?.image
          || "Docker",
        endpoint: item.spec?.ingress?.host || "",
        linkTo: referencedResources(item),
      };
    }),
    ...resources.databases.map((item) => ({
      item,
      kind: item.spec?.engine || "Database",
      meta: `${String(item.spec?.engine || "database").toLowerCase()}${databaseVersion(data, item) ? ` ${databaseVersion(data, item)}` : ""}`,
      source: databaseSource(item),
      warning: isResourceDeleting(item) ? "" : backupWarning(item),
    })),
    ...resources.logical.map((item) => ({
      item,
      kind: "Logical DB",
      meta: "logical db",
      source: "Managed",
      linkTo: item.spec?.serverRef ? [item.spec.serverRef] : [],
    })),
    ...resources.stores.map((item) => ({ item, kind: "Bucket", meta: "object store", source: item.spec?.engine || "Object storage" })),
  ].map(({ item, kind, meta, source, sourceKind, endpoint, linkTo, warning }) => {
    const kinds = usesDeployWorkflow(item) ? pendingChangeKinds(item) : [];
    return {
      key: `${kind}-${resourceName(item)}`,
      resourceName: resourceName(item),
      linkTo: linkTo || [],
      name: resourceDisplayName(item),
      href: resourceHref(name, environment, item, kind).split("?")[0],
      kind,
      source: typeof source === "string" ? source : "Managed",
      sourceKind: sourceKind || "",
      meta: meta || "",
      endpoint: endpoint || "",
      warning: warning || "",
      status: resourceStatus(item),
      pendingKinds: kinds,
      pendingCount: kinds.length ? pendingChangeCount(item) : 0,
      isNew: usesDeployWorkflow(item) && !item.spec?.deploy?.enabled,
      isDeleting: isResourceDeleting(item),
    };
  });

  return (
    <ProjectChrome>
      <div className="rw-project-canvas-shell">
        <ProjectCanvas
          projectKey={name}
          rows={rows}
          environment={environment}
        />
      </div>
      <ResourceDialog project={name} environment={environment} data={data} />
    </ProjectChrome>
  );
}
