import { Text } from "@mantine/core";
import { Check, Clock, GitBranch, MoreHorizontal } from "lucide-react";
import { formatRelativeTime } from "@/lib/services";
import { Button, Muted } from "@/components/geass-ui";

export function DeployStatusBadge({ badge }) {
  const tone =
    badge === "ACTIVE" ? "rw-deploy-badge-active"
      : badge === "FAILED" ? "rw-deploy-badge-failed"
        : badge === "SKIPPED" ? "rw-deploy-badge-skipped"
          : "rw-deploy-badge-removed";
  return <span className={`rw-deploy-badge ${tone}`}>{badge}</span>;
}

export function DeployActiveCard({ row, onOpen, onViewLogs }) {
  return (
    <div className="rw-deploy-active-card">
      <button type="button" className="rw-deploy-active-main" onClick={() => onOpen?.(row.id)}>
        <DeployStatusBadge badge={row.badge} />
        <span className="rw-deploy-active-icon" aria-hidden><GitBranch size={16} /></span>
        <div className="rw-deploy-active-copy">
          <Text size="sm" fw={600} lineClamp={2}>{row.title}</Text>
          <Muted size="xs">
            {row.at ? formatRelativeTime(row.at) : "—"}{row.meta ? ` via ${row.meta.split(" · ")[0] || row.meta}` : ""}
          </Muted>
        </div>
        <span className="rw-deploy-active-menu" aria-hidden><MoreHorizontal size={18} /></span>
      </button>
      <div className="rw-deploy-active-actions">
        <Button variant="outline" size="xs" onClick={() => (onViewLogs || onOpen)?.(row.id)}>View logs</Button>
      </div>
      {row.outcomeOk && row.outcome ? (
        <div className="rw-deploy-card-outcome rw-deploy-active-outcome">
          <Check size={14} className="rw-deploy-outcome-ok" aria-hidden />
          <Muted size="sm">{row.outcome}</Muted>
        </div>
      ) : null}
    </div>
  );
}

export function DeployHistoryRow({ row, onOpen }) {
  return (
    <button type="button" className="rw-deploy-history-row" onClick={() => onOpen?.(row.id)}>
      <div className="rw-deploy-history-row-line">
        <DeployStatusBadge badge={row.badge} />
        <span className="rw-deploy-history-row-icon" aria-hidden><GitBranch size={15} /></span>
        <Text size="sm" fw={500} className="rw-deploy-history-row-title" lineClamp={1}>{row.title}</Text>
        <Muted size="xs" className="rw-deploy-history-row-time">
          {row.at ? formatRelativeTime(row.at) : ""}{row.meta ? ` via ${row.meta.split(" · ")[0] || row.meta}` : ""}
        </Muted>
        <span className="rw-deploy-history-row-menu" aria-hidden><MoreHorizontal size={18} /></span>
      </div>
      {row.detail || row.outcome ? (
        <div className="rw-deploy-history-row-sub">
          <Clock size={12} aria-hidden />
          <Muted size="xs">{row.detail || row.outcome}</Muted>
        </div>
      ) : null}
    </button>
  );
}
