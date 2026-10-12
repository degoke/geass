import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { formatBytes, formatCPU, formatPercent, usingOfReserved } from "@/lib/usage";
import { CapacityBar, DataTable, Muted, PageSection } from "@/components/geass-ui";

/** Usage per project, read live from the API and refreshed while the page is open. */
export function useUsage() {
  return useQuery({
    queryKey: ["usage"],
    queryFn: () => api("/api/usage"),
    refetchInterval: 30000,
  });
}

function UsageBar({ fraction }) {
  const value = Math.min(100, Math.round((fraction || 0) * 100));
  return value > 0 ? <CapacityBar value={value} className="geass-node-capacity" /> : "—";
}

function cells(row, actual) {
  return {
    cpu: usingOfReserved(actual ? row.cpuUsingMillis : undefined, row.cpuReservedMillis, formatCPU),
    memory: usingOfReserved(actual ? row.memoryUsingBytes : undefined, row.memoryReservedBytes, formatBytes),
    storage: usingOfReserved(actual ? row.storageUsingBytes : undefined, row.storageReservedBytes, formatBytes),
  };
}

/**
 * What each project takes of the cluster. "Reserved" is what its pods ask for, the same figure that decides
 * whether something new fits; "using" is what they consume now, shown when Prometheus is installed.
 * Pass `projectName` to show only that project, with the rest of the cluster for comparison.
 */
export function UsagePanel({ projectName, title = "Usage by project" }) {
  const usage = useUsage();
  const report = usage.data;

  if (usage.isPending) return <Muted size="sm">Loading usage…</Muted>;
  if (usage.isError) return <Muted size="sm">{usage.error.message}</Muted>;

  const actual = Boolean(report?.actualAvailable);
  const projects = (report?.projects || []).filter((row) => !projectName || row.name === projectName);
  const rows = projects.map((row) => ({
    key: row.name,
    name: row.displayName || row.name,
    region: row.region || "—",
    pods: row.pods,
    ...cells(row, actual),
    cpuShare: row.shareOfCpu,
    memoryShare: row.shareOfMemory,
    share: `${formatPercent(row.shareOfCpu)} CPU · ${formatPercent(row.shareOfMemory)} memory`,
  }));
  if (!projectName && report?.platform) {
    rows.push({
      key: "__platform",
      name: "Geass and system services",
      region: "—",
      pods: report.platform.pods,
      ...cells(report.platform, actual),
      cpuShare: report.cpuAllocatableMillis ? report.platform.cpuReservedMillis / report.cpuAllocatableMillis : 0,
      memoryShare: report.memoryAllocatableBytes ? report.platform.memoryReservedBytes / report.memoryAllocatableBytes : 0,
      share: "",
    });
  }

  return (
    <PageSection
      title={title}
      description={actual
        ? "Reserved is what a project's services ask for. The first figure is what they are using right now."
        : "Reserved is what a project's services ask for. Live use appears once monitoring is installed."}
    >
      <p className="rw-rail-body">
        The cluster has {formatCPU(report?.cpuAllocatableMillis)} and {formatBytes(report?.memoryAllocatableBytes)} of memory across {report?.nodes ?? 0} {report?.nodes === 1 ? "node" : "nodes"}.
      </p>
      <DataTable
        columns={[
          { label: "Project", key: "name" },
          { label: "Region", key: "region" },
          { label: "Services running", key: "pods" },
          { label: "CPU", key: "cpu" },
          { label: "Memory", key: "memory" },
          { label: "Storage", key: "storage" },
          { label: "Share of its capacity", align: "right", render: (row) => (row.share ? <span title={row.share}><UsageBar fraction={Math.max(row.cpuShare, row.memoryShare)} /></span> : "—") },
        ]}
        rows={rows}
        getRowKey={(row) => row.key}
        empty="No projects yet."
      />
      {!projectName && report?.regions?.length ? (
        <>
          <p className="rw-rail-body">By region</p>
          <DataTable
            columns={[
              { label: "Region", key: "name" },
              { label: "Nodes", key: "nodes" },
              { label: "CPU reserved", render: (row) => `${formatCPU(row.cpuReservedMillis)} of ${formatCPU(row.cpuAllocatableMillis)}` },
              { label: "Memory reserved", render: (row) => `${formatBytes(row.memoryReservedBytes)} of ${formatBytes(row.memoryAllocatableBytes)}` },
              { label: "", align: "right", render: (row) => <UsageBar fraction={row.cpuAllocatableMillis ? row.cpuReservedMillis / row.cpuAllocatableMillis : 0} /> },
            ]}
            rows={report.regions}
            getRowKey={(row) => row.name}
          />
        </>
      ) : null}
    </PageSection>
  );
}
