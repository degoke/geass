import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { Menu } from "@mantine/core";
import { MoreVertical, Plus } from "lucide-react";
import { action, api } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser, alertError, notify } from "@/lib/notify";
import { useRefreshCompute, useServers } from "@/lib/compute";
import { defaultRegion, describeTags, platformRegions } from "@/lib/placement";
import { ClusterBackup } from "@/components/ClusterBackup";
import { RecoverBackups } from "@/components/RecoverBackups";
import { ClusterVersion } from "@/components/ClusterVersion";
import { useHidden } from "@/lib/options";
import { managedFor } from "@/lib/managed";
import { HAReadinessSection } from "@/components/HAReadinessSection";
import { NodePlacementDialog } from "@/components/NodePlacementDialog";
import { ServerHostKeysDialog } from "@/components/ServerHostKeysDialog";
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

const PHASE_LABEL = {
  Pending: "Waiting",
  Creating: "Creating machine",
  Joining: "Joining",
  Failed: "Failed",
  Deleting: "Removing",
};

/** What a server that has no node yet is doing, in a word, and the colour that goes with it. */
function serverStatus(server) {
  const failed = server.phase === "Failed";
  return {
    status: PHASE_LABEL[server.phase] || server.phase,
    tone: failed ? "danger" : server.phase === "Deleting" ? "neutral" : "warning",
  };
}

/** Who cordoned a node, when it was Geass: Geass then undoes it itself, so the button is off. */
const CORDONED_BY = { upgrade: "to upgrade it", pool: "to empty it" };

/**
 * What can be done to a server. A server that failed to join shows Retry in the row, because that is what to do next.
 * The rest are in a menu, so a row stays narrow however many apply.
 */
function RowActions({ row, manage, working, onEdit, onToggleCordon, onRetry, onHostKeys, onRemove }) {
  const cordonVerb = row.cordoned ? "Uncordon" : "Cordon";
  const retry = manage && row.server?.canRetry;
  const items = [];
  if (row.joined) items.push({ key: "edit", label: "Edit", onClick: onEdit });
  if (row.joined && manage) {
    items.push({
      key: "cordon",
      label: cordonVerb,
      onClick: onToggleCordon,
      disabled: Boolean(row.cordonedBy),
      title: row.cordonedBy ? "Geass cordoned this server and uncordons it when it is done." : row.cordoned ? "Let new services start here again." : "Keep new services off this server. What runs here stays.",
    });
  }
  if (row.server?.canUpdateHostKeys && manage) items.push({ key: "keys", label: "Update host keys", onClick: onHostKeys });
  if (row.removable && manage) items.push({ key: "remove", label: "Remove", onClick: onRemove, danger: true });
  return (
    <span className="cp-row-actions">
      {retry ? (
        <Button size="xs" variant="outline" loading={working === `retry:${row.name}`} onClick={onRetry} aria-label={`Retry ${row.serverName}`}>Retry</Button>
      ) : null}
      {items.length ? (
        <Menu position="bottom-end" withinPortal>
          <Menu.Target>
            <button type="button" className="rw-icon-btn" aria-label={`Actions for ${row.name}`} disabled={Boolean(working) && working.endsWith(`:${row.name}`)}>
              <MoreVertical size={15} />
            </button>
          </Menu.Target>
          <Menu.Dropdown>
            {items.map((item) => (
              <Menu.Item key={item.key} color={item.danger ? "red" : undefined} disabled={item.disabled} title={item.title} onClick={item.onClick}>
                {item.label}
              </Menu.Item>
            ))}
          </Menu.Dropdown>
        </Menu>
      ) : null}
    </span>
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
  const [hostKeys, setHostKeys] = useState(null);
  const [working, setWorking] = useState("");
  const servers = useServers().data || [];
  const refreshCompute = useRefreshCompute();
  const cpuUsed = capacity.cpuAllocatableMillis ? Math.min(100, Math.round((capacity.cpuRequestedMillis / capacity.cpuAllocatableMillis) * 100)) : 0;
  const memUsed = capacity.memoryAllocatableBytes ? Math.min(100, Math.round((capacity.memoryRequestedBytes / capacity.memoryAllocatableBytes) * 100)) : 0;
  const summaryRows = capacity.known ? [
    { metric: "CPU available", value: capacity.cpuAvailable, detail: `${capacity.cpuRequested} requested of ${capacity.cpuAllocatable}`, load: cpuUsed },
    { metric: "Memory available", value: capacity.memoryAvailable, detail: `${capacity.memoryRequested} requested of ${capacity.memoryAllocatable}`, load: memUsed },
    { metric: "Healthy nodes", value: String(capacity.healthyNodes ?? 0), detail: `${capacity.schedulableNodes ?? 0} schedulable`, load: 0 },
  ] : [];
  const serverOfNode = new Map(servers.filter((server) => server.node).map((server) => [server.node, server]));
  const nodeRows = (capacity.nodes || []).map((node) => {
    const pressureScore = Math.min(100, (node.pressure?.length || 0) * 34);
    const load = node.cpuUsedPercent ?? pressureScore;
    const server = serverOfNode.get(node.name);
    const failed = server && server.phase === "Failed";
    return {
      name: node.name,
      serverName: server?.name || node.name,
      role: node.role,
      cpu: node.cpuAllocatable,
      memory: node.memoryAllocatable,
      k3s: node.k3sVersion || "—",
      region: node.region ? (node.zone ? `${node.region} · ${node.zone}` : node.region) : "—",
      tags: describeTags(node.tags) || "—",
      pool: node.pool || "",
      ephemeral: Boolean(node.ephemeral),
      // A server that was added on its own can be removed again; the control plane and a pool's nodes cannot.
      removable: server ? server.canRemove : node.role !== "control-plane" && !node.pool,
      joined: true,
      cordoned: !node.schedulable,
      cordonedBy: node.cordonedBy || "",
      server,
      raw: node,
      pressure: node.pressure?.length ? node.pressure.join(", ") : "—",
      load,
      status: failed ? "Failed" : !node.ready ? "Not ready" : !node.schedulable ? "Cordoned" : "Ready",
      tone: failed || !node.ready ? "danger" : !node.schedulable ? "warning" : "success",
      message: failed ? server.message : "",
    };
  });
  // A server that has not joined, or failed to, has no node, so it is a row of its own. This is where its error is.
  const nodeNames = new Set(nodeRows.map((row) => row.name));
  servers
    .filter((server) => !server.joined && !nodeNames.has(server.name))
    .forEach((server) => {
      nodeRows.push({
        name: server.name,
        serverName: server.name,
        role: server.role,
        cpu: "—",
        memory: "—",
        k3s: "—",
        region: "—",
        tags: "—",
        pool: server.pool || "",
        ephemeral: false,
        removable: server.canRemove,
        joined: false,
        cordoned: false,
        cordonedBy: "",
        server,
        raw: null,
        pressure: "—",
        load: 0,
        message: server.message,
        ...serverStatus(server),
      });
    });

  const refreshServers = () => Promise.all([reload?.(), refreshCompute()]);
  const runOnServer = async (key, path, done) => {
    setWorking(key);
    try {
      await action(path);
      notify(done, "success");
      await refreshServers();
    } catch (error) {
      alertError(error.message);
    } finally {
      setWorking("");
    }
  };
  const toggleCordon = (row) => {
    const verb = row.cordoned ? "uncordon" : "cordon";
    return runOnServer(`${verb}:${row.name}`, `/nodes/${encodeURIComponent(row.name)}/${verb}`, row.cordoned ? `${row.name} takes new services again` : `${row.name} takes no new services`);
  };
  const retryServer = (row) => runOnServer(`retry:${row.name}`, `/servers/${encodeURIComponent(row.serverName)}/retry`, `Retrying ${row.serverName}`);

  const removeServer = async () => {
    setRemoveBusy(true);
    setRemoveError("");
    try {
      await action(`/servers/${encodeURIComponent(removing.serverName)}/delete`, { confirmName: removing.serverName });
      notify(`Removing ${removing.serverName}`, "success");
      setRemoving(null);
      await Promise.all([reload?.(), refreshCompute()]);
    } catch (error) {
      setRemoveError(
        error.status === 404
          ? `Geass did not add ${removing.serverName} as a server, so it cannot remove it here. Remove it where it was set up.`
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
      <ClusterBackup mutable={mutable} managed={managedFor(data, "installer")} />
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
            {
              label: "Status",
              render: (row) => (
                <div className="cp-node-status">
                  <Badge tone={row.tone}>{row.status}</Badge>
                  {row.cordonedBy ? <Muted size="xs">Cordoned by Geass {CORDONED_BY[row.cordonedBy]}</Muted> : null}
                  {row.message ? <Muted size="xs" className="cp-node-message">{row.message}</Muted> : null}
                </div>
              ),
            },
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
              label: "",
              align: "right",
              render: (row) => (mutable ? (
                <RowActions
                  row={row}
                  manage={!hideServers}
                  working={working}
                  onEdit={() => setEditing(row.raw)}
                  onToggleCordon={() => toggleCordon(row)}
                  onRetry={() => retryServer(row)}
                  onHostKeys={() => setHostKeys(row.server)}
                  onRemove={() => { setRemoveError(""); setRemoving(row); }}
                />
              ) : null),
            },
          ]}
          rows={nodeRows}
          getRowKey={(row) => row.name}
          empty="No servers are visible yet."
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
      <ServerHostKeysDialog server={hostKeys} onClose={() => setHostKeys(null)} onSaved={refreshServers} />
      <AddServerSheet opened={addingServer && !hideServers} onClose={() => setAddingServer(false)} />
      <ConfirmDelete
        opened={Boolean(removing)}
        title="Remove server?"
        name={removing?.serverName || ""}
        confirmLabel="Remove server"
        busy={removeBusy}
        error={removeError}
        onClose={() => setRemoving(null)}
        onConfirm={removeServer}
      >
        <p>
          This removes the server <strong>{removing?.serverName}</strong> from the cluster. A machine Geass created at a cloud is deleted there; a server you added yourself is only taken out.
        </p>
        {removing ? <ServerImpact name={removing.serverName} /> : null}
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
