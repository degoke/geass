import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Popover, Stack, Text } from "@mantine/core";
import { action } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser, alertError } from "@/lib/notify";
import { countProjectPendingChanges, projectPendingItems } from "@/lib/services";
import { Button } from "@/components/geass-ui";

/**
 * Pending changes and the one action that applies them, kept in the top bar so it is reachable from every
 * project page. Viewers see nothing: they cannot deploy.
 */
export function DeployControls({ projectName, environment, data, reload }) {
  const navigate = useNavigate();
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [deploying, setDeploying] = useState(false);
  if (!canMutate(data)) return null;
  const items = projectPendingItems(data, projectName, environment);
  const total = countProjectPendingChanges(data, projectName, environment);

  const deployAll = async () => {
    if (deploying || !total) return;
    setDeploying(true);
    try {
      const result = await action(`/projects/${projectName}/deploy`, { environment });
      await reload?.();
      setDetailsOpen(false);
      const deployed = Number(result?.deployed ?? 0);
      if (deployed === 0) {
        alertUser("Nothing was deployed");
        return;
      }
      navigate({
        to: `/projects/${projectName}`,
        search: environment ? { environment } : undefined,
      });
      alertUser(deployed === 1 ? "Deployed 1 change" : `Deployed ${deployed} changes`);
    } catch (error) {
      alertError(error.message);
    } finally {
      setDeploying(false);
    }
  };

  return (
    <div className="rw-deploy">
      <Popover
        opened={detailsOpen && total > 0}
        onChange={setDetailsOpen}
        position="bottom-end"
        shadow="md"
        width={380}
        classNames={{ dropdown: "rw-project-pending-details" }}
      >
        <Popover.Target>
          <button
            type="button"
            className={total ? "rw-deploy-pending has-changes" : "rw-deploy-pending"}
            disabled={!total}
            aria-label={total ? `${total} pending changes, show details` : "No pending changes"}
            onClick={() => setDetailsOpen((open) => !open)}
          >
            <span className="rw-deploy-mark" aria-hidden="true" />
            {total ? `${total} pending` : "Up to date"}
          </button>
        </Popover.Target>
        <Popover.Dropdown>
          <Text size="sm" fw={600} mb="xs">Pending changes</Text>
          <Stack gap={6} className="rw-project-pending-details-list">
            {items.map((row) => (
              <button
                key={`${row.segment}-${row.resourceId}`}
                type="button"
                className="rw-project-pending-details-row"
                onClick={() => {
                  setDetailsOpen(false);
                  navigate({
                    to: `/projects/${projectName}/${row.segment}/${row.resourceId}`,
                    search: environment ? { environment } : undefined,
                  });
                }}
              >
                <span className="rw-project-pending-details-name">
                  {row.name}
                  <span className="rw-project-pending-details-type">{row.typeLabel}</span>
                </span>
                <ul className="rw-project-pending-details-changes">
                  {row.changes.map((line) => (
                    <li key={line}>{line}</li>
                  ))}
                </ul>
              </button>
            ))}
          </Stack>
          <div className="rw-project-pending-details-foot">
            <Button size="sm" fullWidth loading={deploying} disabled={deploying} onClick={deployAll}>
              Deploy all
            </Button>
          </div>
        </Popover.Dropdown>
      </Popover>
      <button type="button" className="rw-deploy-btn" disabled={!total || deploying} onClick={deployAll}>
        {deploying ? "Deploying…" : "Deploy changes"}
      </button>
    </div>
  );
}
