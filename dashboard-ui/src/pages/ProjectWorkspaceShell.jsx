import { useNavigate, useRouterState } from "@tanstack/react-router";
import { resourceName } from "@/lib/api";
import { DeploymentLogDrawer } from "@/components/DeploymentLogDrawer";
import { ResourceDetailDrawer } from "@/components/ResourceDetailDrawer";
import { WorkspacePage } from "@/pages/WorkspacePage";

export function ProjectWorkspaceShell({
  project,
  data,
  environment,
  resourceKind,
  resourceName: resourceId,
  reload,
}) {
  const navigate = useNavigate();
  const projectSlug = resourceName(project);
  const closeDrawer = () => navigate({
    to: `/projects/${projectSlug}`,
    search: environment ? { environment } : undefined,
  });

  const drawerOpen = Boolean(resourceKind && resourceId);
  const deploymentId = useRouterState({ select: (state) => state.location.search?.deployment });
  const stackOpen = Boolean(drawerOpen && deploymentId);

  const closeDeploymentLogs = () => {
    navigate({
      to: `/projects/${projectSlug}/${resourceKind}/${resourceId}`,
      search: { view: "deployments", ...(environment ? { environment } : {}) },
    });
  };

  return (
    <div className={stackOpen ? "rw-project-workspace rw-drawer-stack-open" : "rw-project-workspace"}>
      <WorkspacePage project={project} data={data} environment={environment} reload={reload} />
      {drawerOpen ? (
        <>
          <button
            type="button"
            className={stackOpen ? "rw-resource-drawer-overlay rw-resource-drawer-overlay-stacked" : "rw-resource-drawer-overlay"}
            aria-label="Close resource panel"
            onClick={closeDrawer}
          />
          <ResourceDetailDrawer
            project={project}
            data={data}
            kind={resourceKind}
            name={resourceId}
            reload={reload}
            onClose={closeDrawer}
            underNestedStack={stackOpen}
          />
          {stackOpen ? (
            <DeploymentLogDrawer
              kind={resourceKind}
              name={resourceId}
              data={data}
              deploymentId={deploymentId}
              onClose={closeDeploymentLogs}
            />
          ) : null}
        </>
      ) : null}
    </div>
  );
}
