import { useState } from "react";
import { Text } from "@mantine/core";
import { ChevronDown, ChevronRight, Globe } from "lucide-react";
import { isReady } from "@/lib/api";
import { formatRelativeTime, railwayDeploymentLayout, resourceStatus } from "@/lib/services";
import { DeployActiveCard, DeployHistoryRow } from "@/components/deployment-ui";
import { Muted, Switch } from "@/components/geass-ui";

export function ResourceDeploymentsView({ item, kind, name, deployments, onOpenDeployment }) {
  const [hideSkipped, setHideSkipped] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(true);

  const { active, history } = railwayDeploymentLayout(item, deployments);
  const historyRows = hideSkipped ? history.filter((row) => !row.skipped) : history;
  const ready = isReady(item);
  const status = resourceStatus(item);
  const offline = kind === "apps" && !active && !history.length && !ready && status !== "Setting up" && status !== "Failed";
  const showServiceBar = kind === "apps";
  const host = String(item?.status?.url || "").replace(/^https?:\/\//, "").replace(/\/$/, "");
  const barLabel = host || (offline ? "Service is offline" : ready ? "No public domain" : "Service is starting");
  const replicas = item?.spec?.replicas ?? 1;

  if (!active && !historyRows.length && !showServiceBar) {
    return (
      <Muted size="sm" className="rw-deploy-empty">
        No deploys yet. Use <strong>Deploy</strong> on the project bar or <strong>Retry</strong> after a failure.
      </Muted>
    );
  }

  return (
    <div className="rw-deployments-railway">
      {showServiceBar ? (
        <div className="rw-deploy-service-bar">
          <Globe size={16} aria-hidden />
          {host ? (
            <a href={item.status.url} target="_blank" rel="noreferrer" className="rw-deploy-service-bar-link">{barLabel}</a>
          ) : (
            <Text size="sm" fw={500}>{barLabel}</Text>
          )}
          <Muted size="xs" className="rw-deploy-service-bar-meta">
            {item?.spec?.environment || "production"} · {replicas} {replicas === 1 ? "Replica" : "Replicas"}
          </Muted>
        </div>
      ) : null}

      {offline && !active && !historyRows.length ? (
        <Muted size="sm" className="rw-deploy-offline-hint">Deploy from the project bar to publish this service.</Muted>
      ) : null}

      {active ? (
        <DeployActiveCard row={active} onOpen={onOpenDeployment} onViewLogs={onOpenDeployment} />
      ) : null}

      {historyRows.length ? (
        <div className="rw-deploy-history">
          <button
            type="button"
            className="rw-deploy-history-toggle"
            onClick={() => setHistoryOpen((open) => !open)}
            aria-expanded={historyOpen}
          >
            {historyOpen ? <ChevronDown size={14} aria-hidden /> : <ChevronRight size={14} aria-hidden />}
            <Text size="xs" fw={600} tt="uppercase" c="dimmed">History ({historyRows.length})</Text>
            <span className="rw-deploy-history-spacer" />
            <Switch
              label="Hide skipped"
              checked={hideSkipped}
              onChange={(event) => setHideSkipped(event.currentTarget.checked)}
              onClick={(event) => event.stopPropagation()}
            />
          </button>
          {historyOpen ? (
            <div className="rw-deploy-history-list">
              {historyRows.map((row) => (
                <DeployHistoryRow key={row.id} row={row} onOpen={onOpenDeployment} />
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
