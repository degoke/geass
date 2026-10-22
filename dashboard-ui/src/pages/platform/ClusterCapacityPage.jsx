import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { action } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser, notify } from "@/lib/notify";
import { useRefreshCompute } from "@/lib/compute";
import { defaultRegion, describeTags, platformRegions } from "@/lib/placement";
import { ClusterBackup } from "@/components/ClusterBackup";
import { RecoverBackups } from "@/components/RecoverBackups";
import { ClusterVersion } from "@/components/ClusterVersion";
import { useHidden } from "@/lib/options";
import { HAReadinessSection } from "@/components/HAReadinessSection";
import { NodePlacementDialog } from "@/components/NodePlacementDialog";
import { AddServerSheet } from "@/components/compute/AddServerSheet";
import { Callout, ConfirmDelete } from "@/components/compute/ComputeBits";
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

/** Whether the cluster's network plugin acts on network policies, which project isolation depends on. Absent until the check has run. */
function NetworkPolicySection({ data }) {
  const cluster = (data?.clusters?.items || [])[0];
  const condition = (cluster?.status?.conditions || []).find((item) => item.type === "NetworkPolicyEnforced");
  if (!condition) return null;
  const tone = condition.status === "True" ? "success" : condition.status === "False" ? "danger" : "warning";
  const label = condition.status === "True" ? "Enforced" : condition.status === "False" ? "Not enforced" : "Unknown";
  return (
    <PageSection title="Network isolation" description="Whether the cluster acts on network policies. Isolation between projects, and the other protections Geass writes as policies, only hold when it does.">
      <Badge tone={tone}>{label}</Badge>
      <Muted size="sm">{condition.message}</Muted>
    </PageSection>
  );
}

export function ClusterCapacityPage({ data, reload, embedded = false }) {
  const capacity = data?.platform?.capacity || {};
  const mutable = canMutate(data);
  const hideUpgrade = useHidden("upgrade");
  const hideServers = useHidden("servers");
  const [editing, setEditing] = useState(null);
  const [addingServer, setAddingServer] = useState(false);
  const [removing, setRemoving] = useState(null);
  const [removeBusy, setRemoveBusy] = useState(false);
  const [removeError, setRemoveError] = useState("");
  const refreshCompute = useRefreshCompute();
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
      k3s: node.k3sVersion || "—",
      region: node.region ? (node.zone ? `${node.region} · ${node.zone}` : node.region) : "—",
      tags: describeTags(node.tags) || "—",
      pool: node.pool || "",
      ephemeral: Boolean(node.ephemeral),
      // A server that was added on its own can be removed again; the control plane and a pool's nodes cannot.
      removable: node.role !== "control-plane" && !node.pool,
      raw: node,
      pressure: node.pressure?.length ? node.pressure.join(", ") : "—",
      load,
      status: !node.ready ? "Not ready" : !node.schedulable ? "Unschedulable" : "Ready",
      tone: !node.ready ? "danger" : !node.schedulable ? "warning" : "success",
    };
  });

  const removeServer = async () => {
    setRemoveBusy(true);
    setRemoveError("");
    try {
      await action(`/servers/${removing.name}/delete`, { confirmName: removing.name });
      notify(`Removing the server ${removing.name}. Its services move elsewhere.`, "success");
      setRemoving(null);
      await Promise.all([reload?.(), refreshCompute()]);
    } catch (error) {
      setRemoveError(
        error.status === 404
          ? `Geass did not add ${removing.name} as a server, so it cannot remove it here. Remove it where it was set up.`
          : error.message,
      );
    } finally {
      setRemoveBusy(false);
    }
  };
  const hasEphemeral = nodeRows.some((row) => row.ephemeral);

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
      <NetworkPolicySection data={data} />
      {hideUpgrade ? null : <ClusterVersion data={data} />}
      <ClusterBackup mutable={mutable} />
      <RecoverBackups mutable={mutable} />
      <PageSection
        title="Nodes"
        description="Per-node allocatable resources and pressure."
        actions={mutable && !hideServers ? (
          <Button size="sm" variant="outline" leftSection={<Plus size={14} />} onClick={() => setAddingServer(true)}>Add server</Button>
        ) : null}
      >
        <div className="cp-badge-fit">
        <DataTable
          columns={[
            { label: "Node", key: "name" },
            { label: "Role", key: "role" },
            {
              label: "Pool",
              render: (row) => (row.pool ? (
                <div className="cp-pool-cell">
                  <Link to={`/pools/${row.pool}`} className="cp-name-link">{row.pool}</Link>
                  {row.ephemeral ? <Badge>Ephemeral</Badge> : null}
                </div>
              ) : "—"),
            },
            { label: "K3s", key: "k3s" },
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
                <span className="cp-row-actions">
                  <Button size="xs" variant="outline" onClick={() => setEditing(row.raw)} aria-label={`Edit ${row.name}`}>Edit</Button>
                  {row.removable && !hideServers ? (
                    <Button size="xs" variant="outline" onClick={() => { setRemoveError(""); setRemoving(row); }} aria-label={`Remove ${row.name}`}>Remove</Button>
                  ) : null}
                </span>
              ) : null),
            },
          ]}
          rows={nodeRows}
          getRowKey={(row) => row.name}
          empty="No Kubernetes nodes are visible yet."
        />
        </div>
        {hasEphemeral ? (
          <Muted size="sm">
            Ephemeral nodes belong to a pool that keeps databases and volumes off them, and they may be removed when the load goes.
          </Muted>
        ) : null}
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
      <AddServerSheet opened={addingServer && !hideServers} onClose={() => setAddingServer(false)} />
      <ConfirmDelete
        opened={Boolean(removing)}
        title="Remove server?"
        name={removing?.name || ""}
        confirmLabel="Remove server"
        busy={removeBusy}
        error={removeError}
        onClose={() => setRemoving(null)}
        onConfirm={removeServer}
      >
        <p>
          This removes the server <strong>{removing?.name}</strong> from the cluster. Its services move to other nodes, or wait for room.
          A server that Geass created at a cloud has its machine deleted there. One you added yourself is not deleted, only taken out of the cluster.
        </p>
        <Callout tone="danger">
          Nodes of a pool and the control plane cannot be removed here. Data on this server may be lost if it was the only place that held it.
        </Callout>
      </ConfirmDelete>
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
