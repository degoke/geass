import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Popover, Stack, Text } from "@mantine/core";
import { action } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { countProjectPendingChanges, projectPendingItems } from "@/lib/services";
import { Button } from "@/components/geass-ui";

export function ProjectPendingBanner({ projectName, environment, data, reload }) {
  const navigate = useNavigate();
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [deploying, setDeploying] = useState(false);
  const items = projectPendingItems(data, projectName, environment);
  const total = countProjectPendingChanges(data, projectName, environment);
  if (!total || !canMutate(data)) return null;
  const label = total === 1 ? "Apply 1 change" : `Apply ${total} changes`;

  const deployAll = async () => {
    if (deploying) return;
    setDeploying(true);
    try {
      const result = await action(`/projects/${projectName}/deploy`, { environment });
      await reload?.();
      setDetailsOpen(false);
      const deployed = Number(result?.deployed ?? 0);
      if (deployed === 0) {
        alertUser("No pending changes were deployed. Check the environment filter and resource settings.");
        return;
      }
      navigate({
        to: `/projects/${projectName}`,
        search: environment ? { environment } : undefined,
      });
      alertUser(deployed === 1 ? "Deployed 1 change" : `Deployed ${deployed} changes`);
    } catch (error) {
      alertUser(error.message);
    } finally {
      setDeploying(false);
    }
  };

  return (
    <div className="rw-project-pending-banner">
      <span className="rw-project-pending-banner-text">{label}</span>
      <div className="rw-project-pending-banner-actions">
        <Popover
          opened={detailsOpen}
          onChange={setDetailsOpen}
          position="bottom"
          withArrow
          shadow="md"
          width={380}
          classNames={{ dropdown: "rw-project-pending-details" }}
        >
          <Popover.Target>
            <Button size="sm" variant="outline" onClick={() => setDetailsOpen((open) => !open)}>
              Details
            </Button>
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
              <Button size="sm" fullWidth onClick={deployAll}>
                Deploy all
              </Button>
            </div>
          </Popover.Dropdown>
        </Popover>
        <Button size="sm" loading={deploying} disabled={deploying} onClick={deployAll}>
          Deploy
        </Button>
      </div>
    </div>
  );
}
