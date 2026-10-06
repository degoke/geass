import { canMutate } from "@/lib/dashboard";
import { openResourceDialog } from "@/lib/services";
import { projectResources } from "@/lib/project-resources";
import { resourceName } from "@/lib/api";
import { resourceHref } from "@/lib/resource-links";
import { resourceStatus } from "@/lib/services";
import { ProjectCanvas } from "@/components/ProjectCanvas";
import { ResourceDialog } from "@/components/ResourceDialog";
import { ProjectChrome } from "@/layout/ProjectChrome";

export function WorkspacePage({ project, data, environment }) {
  const name = resourceName(project);
  const resources = projectResources(data, name, environment);
  const rows = [
    ...resources.apps.map((item) => ({
      item,
      kind: "Service",
      source: item.spec?.source?.git?.repository || item.spec?.source?.image?.image || item.spec?.source?.image || "Docker",
    })),
    ...resources.databases.map((item) => ({ item, kind: item.spec?.engine || "Database", source: item.spec?.engine || "Managed" })),
    ...resources.logical.map((item) => ({ item, kind: "Logical DB", source: "Managed" })),
    ...resources.stores.map((item) => ({ item, kind: "Bucket", source: item.spec?.engine || "Object storage" })),
  ].map(({ item, kind, source }) => ({
    key: `${kind}-${resourceName(item)}`,
    name: resourceName(item),
    href: resourceHref(name, environment, item, kind).split("?")[0],
    kind,
    source: typeof source === "string" ? source : "Managed",
    status: resourceStatus(item),
  }));

  return (
    <ProjectChrome>
      <div className="rw-project-canvas-shell">
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
