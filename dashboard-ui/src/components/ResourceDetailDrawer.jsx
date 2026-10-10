import { ResourceDetailPage } from "@/pages/ResourceDetailPage";

/** In-canvas resource panel (not a viewport drawer — stays below the project top bar). */
export function ResourceDetailDrawer({
  project,
  data,
  kind,
  name,
  reload,
  onClose,
  underNestedStack = false,
}) {
  return (
    <aside
      className={
        underNestedStack
          ? "rw-resource-drawer-panel rw-settings-sheet rw-resource-drawer-under-stack"
          : "rw-resource-drawer-panel rw-settings-sheet"
      }
      aria-label="Resource details"
    >
      <div className="rw-resource-drawer-body">
        <ResourceDetailPage
          project={project}
          data={data}
          kind={kind}
          name={name}
          reload={reload}
          embedded
          onClose={onClose}
        />
      </div>
    </aside>
  );
}
