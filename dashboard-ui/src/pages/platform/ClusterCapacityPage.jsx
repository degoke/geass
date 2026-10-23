import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { action, api } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser, alertError, notify } from "@/lib/notify";
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
      .catch((error) => alertError(error.message))
      .finally(() => setBusy(false));
  };
  return (
    <PageSection title="Regions" description="A project runs only on nodes in its region.">
      <Field label="Default region">
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
    <PageSection title="Network isolation" description="Project isolation only holds when network policies are enforced.">
      <Badge tone={tone}>{label}</Badge>
      <Muted size="sm">{condition.message}</Muted>
    </PageSection>
  );
}

/** What is on a server and nowhere else, which is lost with it. Read when the dialog opens, from the cluster's volumes. */
function ServerImpact({ name }) {
  // null while it is being read, false when it could not be.
  const [impact, setImpact] = useState(null);
  useEffect(() => {
    let current = true;
    setImpact(null);
    api(`/api/servers/${encodeURIComponent(name)}/impact`)
      .then((answer) => { if (current) setImpact(answer); })
      .catch(() => { if (current) setImpact(false); });
    return () => { current = false; };
  }, [name]);

  if (impact === null) return <Muted size="sm">Checking what is stored on this server…</Muted>;
  if (impact === false) {
    return <Callout tone="danger">Could not check what is stored on this server. Data kept only here is lost when it is removed.</Callout>;
  }
  const volumes = impact.volumes || [];
  if (!volumes.length) return <Muted size="sm">Nothing is stored only on this server.</Muted>;
  return (
    <div className="cp-callout is-danger">
      Lost with this server:
      <ul>
        {volumes.map((volume) => (
          <li key={`${volume.namespace}/${volume.name}`}>
            <strong>{volume.name}</strong> <Muted size="sm" component="span">({volume.namespace})</Muted>
            {" "}
            {!volume.database
              ? "is a volume with one copy."
              : volume.otherCopies > 0
                ? `has a copy here. Its ${volume.otherCopies === 1 ? "other copy stays" : `${volume.otherCopies} other copies stay`} on other servers.`
                : "is a database whose only copy is on this server."}
          </li>
        ))}
      </ul>
    </div>
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
      notify(`Removing ${removing.name}`, "success");
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
        <PageSection title="Capacity unavailable">
          <Muted size="sm">{capacity.message || "No schedulable nodes have reported yet."}</Muted>
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
        <PageSection title="Capacity and node pressure">
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
            Ephemeral nodes may be removed when the load goes.
          </Muted>
        ) : null}
      </PageSection>
      <UsagePanel />
      <PageSection title="Cluster metrics">
        <ServiceMetrics scope="cluster" name="cluster" charts={CLUSTER_CHARTS} />
      </PageSection>
      <PageSection title="Geass health">
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
          This removes the server <strong>{removing?.name}</strong> from the cluster. A machine Geass created at a cloud is deleted there; a server you added yourself is only taken out.
        </p>
        {removing ? <ServerImpact name={removing.name} /> : null}
      </ConfirmDelete>
    </>
  );

  if (embedded) {
    return (
      <SettingsSection title="Usage">
        {body}
      </SettingsSection>
    );
  }

  return (
    <PlatformPage title="Cluster">
      {body}
    </PlatformPage>
  );
}
