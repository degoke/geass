import { useState } from "react";
import { action } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { defaultRegion, describeTags, platformRegions } from "@/lib/placement";
import { HAReadinessSection } from "@/components/HAReadinessSection";
import { NodePlacementDialog } from "@/components/NodePlacementDialog";
import { UsagePanel } from "@/components/UsagePanel";
import { CLUSTER_CHARTS, OPERATOR_CHARTS, ServiceMetrics } from "@/components/ServiceMetrics";
import { Badge, Button, CapacityBar, DataTable, Field, Muted, PageSection, Select } from "@/components/geass-ui";
import { SettingsSection } from "@/layout/SettingsShell";
import { PlatformPage } from "@/layout/PlatformPage";

/** Where projects that do not choose a region run. Shown once the nodes belong to at least one region. */
function DefaultRegionSection({ data, reload, mutable }) {
  const regions = platformRegions(data);
  const current = defaultRegion(data);
  const [busy, setBusy] = useState(false);
  if (!regions.length) return null;
  const choose = (event) => {
    setBusy(true);
    action("/platform/default-region", { region: event.currentTarget.value })
      .then(() => { reload?.(); alertUser("Default region saved"); })
      .catch((error) => alertUser(error.message))
      .finally(() => setBusy(false));
  };
  return (
    <PageSection title="Regions" description="A region groups nodes that are close together. A project runs only on nodes in its region.">
      <Field label="Default region" description="Used by projects that do not choose a region. Without one, they may run on any node.">
        <Select value={current} onChange={choose} disabled={!mutable || busy} aria-label="Default region">
          <option value="">No default (any node)</option>
          {regions.map((region) => (
            <option key={region.name} value={region.name}>{region.name} · {region.readyNodes}/{region.nodes} nodes ready</option>
          ))}
        </Select>
      </Field>
    </PageSection>
  );
}

export function ClusterCapacityPage({ data, reload, embedded = false }) {
  const capacity = data?.platform?.capacity || {};
  const mutable = canMutate(data);
  const [editing, setEditing] = useState(null);
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
      region: node.region ? (node.zone ? `${node.region} · ${node.zone}` : node.region) : "—",
      tags: describeTags(node.tags) || "—",
      raw: node,
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
            { label: "Region", key: "region" },
            { label: "Tags", key: "tags" },
            { label: "CPU", key: "cpu" },
            { label: "Memory", key: "memory" },
            { label: "Pressure", key: "pressure" },
            {
              label: "Status",
              align: "right",
              render: (row) => <Badge tone={row.tone}>{row.status}</Badge>,
            },
            {
              label: "",
              align: "right",
              render: (row) => (mutable ? (
                <Button size="xs" variant="outline" onClick={() => setEditing(row.raw)} aria-label={`Edit ${row.name}`}>Edit</Button>
              ) : null),
            },
          ]}
          rows={nodeRows}
          getRowKey={(row) => row.name}
          empty="No Kubernetes nodes are visible yet."
        />
      </PageSection>
      <UsagePanel />
      <PageSection title="Cluster metrics" description="What services use across the cluster, and what the machines are doing.">
        <ServiceMetrics scope="cluster" name="cluster" charts={CLUSTER_CHARTS} />
      </PageSection>
      <PageSection title="Geass health" description="How the operator is doing: failed and slow reconciles, work waiting, and how the Kubernetes API server is answering it.">
        <ServiceMetrics scope="operator" name="operator" charts={OPERATOR_CHARTS} />
      </PageSection>
      <DefaultRegionSection data={data} reload={reload} mutable={mutable} />
      <HAReadinessSection data={data} />
      <NodePlacementDialog node={editing} data={data} reload={reload} onClose={() => setEditing(null)} />
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
