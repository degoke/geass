import { HAReadinessSection } from "@/components/HAReadinessSection";
import { Badge, CapacityBar, DataTable, Muted, PageSection } from "@/components/geass-ui";
import { SettingsSection } from "@/layout/SettingsShell";
import { PlatformPage } from "@/layout/PlatformPage";

export function ClusterCapacityPage({ data, embedded = false }) {
  const capacity = data?.platform?.capacity || {};
  const cpuUsed = capacity.cpuAllocatableMillis ? Math.min(100, Math.round((capacity.cpuRequestedMillis / capacity.cpuAllocatableMillis) * 100)) : 0;
  const memUsed = capacity.memoryAllocatableBytes ? Math.min(100, Math.round((capacity.memoryRequestedBytes / capacity.memoryAllocatableBytes) * 100)) : 0;
  const summaryRows = capacity.known ? [
    { metric: "CPU available", value: capacity.cpuAvailable, detail: `${capacity.cpuRequested} requested of ${capacity.cpuAllocatable}`, load: cpuUsed },
    { metric: "Memory available", value: capacity.memoryAvailable, detail: `${capacity.memoryRequested} requested of ${capacity.memoryAllocatable}`, load: memUsed },
    { metric: "Healthy nodes", value: String(capacity.healthyNodes ?? 0), detail: `${capacity.schedulableNodes ?? 0} schedulable`, load: 0 },
  ] : [];
  const nodeRows = (capacity.nodes || []).map((node) => {
    const pressureScore = Math.min(100, (node.pressure?.length || 0) * 34);
    const load = node.cpuUsedPercent ?? pressureScore;
    return {
      name: node.name,
      role: node.role,
      cpu: node.cpuAllocatable,
      memory: node.memoryAllocatable,
      pressure: node.pressure?.length ? node.pressure.join(", ") : "—",
      load,
      status: !node.ready ? "Not ready" : !node.schedulable ? "Unschedulable" : "Ready",
      tone: !node.ready ? "danger" : !node.schedulable ? "warning" : "success",
    };
  });

  const body = (
    <>
      {!capacity.known ? (
        <PageSection title="Capacity unavailable" description="Schedulable nodes must report before totals appear.">
          <Muted size="sm">{capacity.message || "Node capacity is unavailable until the cluster reports schedulable nodes."}</Muted>
        </PageSection>
      ) : (
        <PageSection title="Summary">
          <DataTable
            columns={[
              { label: "Metric", key: "metric" },
              { label: "Available", key: "value" },
              { label: "Requested / allocatable", key: "detail" },
              {
                label: "Load",
                align: "right",
                render: (row) => (row.load > 0 ? <CapacityBar value={row.load} className="geass-node-capacity" /> : "—"),
              },
            ]}
            rows={summaryRows}
            getRowKey={(row) => row.metric}
          />
        </PageSection>
      )}
      {capacity.issues?.length > 0 && (
        <PageSection title="Capacity and node pressure" description="Resolve these before scaling workloads.">
          <DataTable
            columns={[{ label: "Issue", key: "message" }]}
            rows={capacity.issues.map((issue, index) => ({ message: issue.message, key: `${issue.message}-${index}` }))}
            getRowKey={(row) => row.key}
          />
        </PageSection>
      )}
      <PageSection title="Nodes" description="Per-node allocatable resources and pressure.">
        <DataTable
          columns={[
            { label: "Node", key: "name" },
            { label: "Role", key: "role" },
            { label: "CPU", key: "cpu" },
            { label: "Memory", key: "memory" },
            { label: "Pressure", key: "pressure" },
            {
              label: "Status",
              align: "right",
              render: (row) => <Badge tone={row.tone}>{row.status}</Badge>,
            },
          ]}
          rows={nodeRows}
          getRowKey={(row) => row.name}
          empty="No Kubernetes nodes are visible yet."
        />
      </PageSection>
      <HAReadinessSection data={data} />
    </>
  );

  if (embedded) {
    return (
      <SettingsSection title="Usage" description="Cluster capacity and consumption across all projects.">
        {body}
      </SettingsSection>
    );
  }

  return (
    <PlatformPage title="Cluster" description="Compare allocatable CPU and memory to requests before creating resources.">
      {body}
    </PlatformPage>
  );
}
