import { useMemo, useState } from "react";
import { Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Download, ExternalLink, RefreshCw } from "lucide-react";
import { api, list, resourceDisplayName, resourceName } from "@/lib/api";
import { CodePanel } from "@/components/CodePanel";
import { DeployStatusBadge } from "@/components/deployment-ui";
import { deploymentRowHeadline, formatRelativeTime, resourceDeploymentList } from "@/lib/services";
import { Button, DetailTabsButtons, Input, Muted } from "@/components/geass-ui";

const API_KIND = {
  apps: "apps",
  databases: "databases",
  "object-stores": "object-stores",
  "logical-databases": "logical-databases",
};

const LOG_TABS = [
  { id: "details", label: "Details" },
  { id: "build", label: "Build Logs" },
  { id: "deploy", label: "Deploy Logs" },
  { id: "network", label: "Network Logs" },
];

function formatLogTimestamp(value) {
  const date = value ? new Date(value) : null;
  if (!date || Number.isNaN(date.getTime())) return "";
  return date.toLocaleString(undefined, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    timeZoneName: "shortOffset",
  });
}

function sectionMap(sections) {
  const map = {};
  for (const section of sections) {
    if (section.id === "provision") map.details = section;
    else map[section.id] = section;
  }
  return map;
}

export function DeploymentLogDrawer({ kind, name, data, deploymentId, onClose }) {
  const [logTab, setLogTab] = useState("details");
  const [filter, setFilter] = useState("");
  const key = { apps: "apps", databases: "databases", "object-stores": "objectStores", "logical-databases": "logicalDatabases" }[kind];
  const item = list(data, key).find((entry) => resourceName(entry) === name);
  const serviceTitle = item ? resourceDisplayName(item) : name;
  const geassDeployments = kind === "apps" ? list(data, "deployments").filter((entry) => entry.spec?.app === name) : [];

  const row = useMemo(() => {
    const rows = resourceDeploymentList(item, geassDeployments);
    return rows.find((entry) => entry.id === deploymentId || entry.logId === deploymentId) || null;
  }, [item, geassDeployments, deploymentId]);

  const apiKind = API_KIND[kind];
  const logs = useQuery({
    queryKey: ["deployment-logs", apiKind, name, row?.logId],
    queryFn: () => api(`/api/${apiKind}/${name}/deployments/${encodeURIComponent(row.logId)}/logs`),
    enabled: Boolean(apiKind && name && row?.logId),
  });

  const sections = logs.data?.sections || [];
  const bySection = sectionMap(sections);

  const filterLines = (text) => {
    if (!text || !filter.trim()) return text;
    const query = filter.trim().toLowerCase();
    return text.split("\n").filter((line) => line.toLowerCase().includes(query)).join("\n");
  };

  const detailsBody = row ? (
    <div className="rw-deployment-details">
      {row.detail && !/update settings or platform configuration/i.test(row.detail) ? (
        <div className="rw-deployment-details-warn">
          <Muted size="sm">{row.detail}</Muted>
        </div>
      ) : null}
      <div className="rw-deployment-details-block">
        <Text size="xs" c="dimmed" tt="uppercase" fw={600}>Status</Text>
        <Muted size="sm">{row.outcome || row.statusLabel}</Muted>
      </div>
      {row.resolvedErrors?.length ? (
        <div className="rw-deployment-details-block">
          <Text size="xs" c="dimmed" tt="uppercase" fw={600}>Resolved during deploy</Text>
          <ul className="rw-deployment-resolved-list">
            {row.resolvedErrors.map((message, index) => (
              <li key={`${index}-${message}`}><Muted size="sm">{message}</Muted></li>
            ))}
          </ul>
        </div>
      ) : null}
      {row.meta ? (
        <div className="rw-deployment-details-block">
          <Text size="xs" c="dimmed" tt="uppercase" fw={600}>Source</Text>
          <Muted size="sm">{row.meta}</Muted>
        </div>
      ) : null}
      {row.legacy ? (
        <Muted size="xs">Limited history</Muted>
      ) : null}
    </div>
  ) : null;

  const logBody = (() => {
    if (logTab === "details") {
      const provision = sections.find((section) => section.id === "provision");
      if (provision?.lines) {
        return (
          <>
            {detailsBody}
            <CodePanel code={filterLines(provision.lines)} language="bash" empty="No provision log." />
          </>
        );
      }
      return detailsBody;
    }
    if (logTab === "network") {
      return <Muted size="sm">Network logs are not available yet.</Muted>;
    }
    const section = bySection[logTab];
    const emptyCopy = logTab === "build" ? "No build logs." : "No deploy logs.";
    return (
      <CodePanel
        code={filterLines(section?.lines) || (logs.isFetching ? "Loading logs…" : "")}
        language="bash"
        empty={emptyCopy}
      />
    );
  })();

  return (
    <aside className="rw-deployment-log-drawer-panel rw-settings-sheet" aria-label="Deployment logs">
      <header className="rw-settings-sheet-header rw-deployment-log-header">
        <div className="rw-deployment-log-header-top">
          <Button variant="ghost" size="icon" aria-label="Back to deployments" onClick={onClose}>
            <ArrowLeft size={16} />
          </Button>
          <div className="rw-deployment-log-header-main">
            <Text className="rw-settings-sheet-title">{serviceTitle}</Text>
            {row?.at ? (
              <Muted size="xs" className="rw-deployment-log-time">
                {formatLogTimestamp(row.at) || formatRelativeTime(row.at)}
              </Muted>
            ) : null}
          </div>
        </div>
        {row ? (
          <>
            <div className="rw-deployment-log-meta">
              <DeployStatusBadge badge={row.badge} />
            </div>
            <Text size="sm" fw={500} className="rw-deployment-log-title">{deploymentRowHeadline(row)}</Text>
          </>
        ) : null}
      </header>

      <div className="rw-deployment-log-drawer-body">
        {!row ? (
          <Muted size="sm">Deployment not found.</Muted>
        ) : (
          <>
            <DetailTabsButtons
              className="rw-deployment-log-tabs"
              value={logTab}
              tabs={LOG_TABS}
              onChange={setLogTab}
            />

            {logTab !== "details" ? (
              <div className="rw-deployment-log-toolbar">
                <Input
                  placeholder="Filter and search logs"
                  value={filter}
                  onChange={(event) => setFilter(event.currentTarget.value)}
                  className="rw-deployment-log-filter"
                />
                <Button variant="ghost" size="icon" aria-label="Refresh logs" onClick={() => logs.refetch()}>
                  <RefreshCw size={16} />
                </Button>
                <Button variant="ghost" size="icon" aria-label="Download logs" disabled>
                  <Download size={16} />
                </Button>
                <Button variant="ghost" size="icon" aria-label="Open logs" disabled>
                  <ExternalLink size={16} />
                </Button>
              </div>
            ) : null}

            <div className="rw-deployment-log-scroll">{logBody}</div>
          </>
        )}
      </div>
    </aside>
  );
}
