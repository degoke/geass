import { canMutate } from "@/lib/dashboard";
import { openResourceDialog } from "@/lib/services";
import { projectResources } from "@/lib/project-resources";
import { resourceDisplayName, resourceName } from "@/lib/api";
import { databaseHasVolume } from "@/lib/capacity";
import { resourceHref } from "@/lib/resource-links";
import { functionSourceLabel } from "@/lib/functions";
import { backupWarning } from "@/lib/backups";
import { isResourceDeleting, pendingChangeCount, pendingChangeKinds, pendingKindsSummary, resourceStatus, usesDeployWorkflow } from "@/lib/services";
import { ProjectCanvas } from "@/components/ProjectCanvas";
import { ProjectPendingBanner } from "@/components/ProjectPendingBanner";
import { ResourceDialog } from "@/components/ResourceDialog";
import { ProjectChrome } from "@/layout/ProjectChrome";

export function WorkspacePage({ project, data, environment, reload }) {
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
    ...resources.apps.map((item) => ({
      item,
      kind: "Service",
      sourceKind: item.spec?.source?.function ? "function" : item.spec?.source?.git ? "git" : "image",
      source: item.spec?.ingress?.host
        || (item.spec?.source?.function ? functionSourceLabel(item.spec.source.function) : "")
        || item.spec?.source?.git?.repository
        || item.spec?.source?.image?.image
        || item.spec?.source?.image
        || "Docker",
      linkTo: referencedResources(item),
    })),
    ...resources.databases.map((item) => ({
      item,
      kind: item.spec?.engine || "Database",
      source: item.spec?.engine || "Managed",
      volume: item.spec?.placement !== "External" && item.spec?.storageSize && databaseHasVolume(item.spec?.engine) ? `${item.metadata?.name}-volume · ${item.spec.storageSize}` : "",
      warning: isResourceDeleting(item) ? "" : backupWarning(item),
    })),
    ...resources.logical.map((item) => ({
      item,
      kind: "Logical DB",
      source: "Managed",
      linkTo: item.spec?.serverRef ? [item.spec.serverRef] : [],
    })),
    ...resources.stores.map((item) => ({ item, kind: "Bucket", source: item.spec?.engine || "Object storage" })),
  ].map(({ item, kind, source, sourceKind, linkTo, volume, warning }) => {
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
      volume: volume || "",
      warning: warning || "",
      status: resourceStatus(item),
      pendingKinds: kinds,
      pendingCount: kinds.length ? pendingChangeCount(item) : 0,
      pendingLabel: kinds.length ? pendingKindsSummary(kinds) : "",
      isNew: usesDeployWorkflow(item) && !item.spec?.deploy?.enabled,
      isDeleting: isResourceDeleting(item),
    };
  });

  return (
    <ProjectChrome>
      <div className="rw-project-canvas-shell">
        <ProjectPendingBanner projectName={name} environment={environment} data={data} reload={reload} />
        <ProjectCanvas
          projectKey={name}
          rows={rows}
          environment={environment}
          canAdd={canMutate(data)}
          onAdd={openResourceDialog}
        />
      </div>
      <ResourceDialog project={name} environment={environment} data={data} />
    </ProjectChrome>
  );
}
