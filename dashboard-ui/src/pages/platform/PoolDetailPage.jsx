import { useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Trash2 } from "lucide-react";
import { action } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { notify } from "@/lib/notify";
import {
  absoluteTime,
  ageOf,
  formatSizeSummary,
  nodeIsStuck,
  poolCost,
  poolCostCap,
  poolPhase,
  poolSize,
  poolSource,
  poolUsesFallback,
  relativeTime,
  useConnections,
  useProviders,
  usePool,
  useRefreshCompute,
} from "@/lib/compute";
import { Button, DataTable, Empty, Muted, PageSection } from "@/components/geass-ui";
import { Alert, Callout, ComputePage, ConfirmDelete, NodePhaseBadge, PhaseBadge } from "@/components/compute/ComputeBits";
import { PoolForm } from "@/components/compute/PoolForm";
import { PoolProblems, useResume } from "@/pages/platform/PoolsPage";

function Stat({ label, children, detail, title }) {
  return (
    <div className="cp-stat">
      <dt>{label}</dt>
      <dd title={title}>
        {children}
        {detail ? <small>{detail}</small> : null}
      </dd>
    </div>
  );
}

function When({ value, never = "Never" }) {
  if (!value) return never;
  return <span title={absoluteTime(value)}>{relativeTime(value)}</span>;
}

export function PoolDetailPage({ name, data }) {
  const mutable = canMutate(data);
  const query = usePool(name);
  const providersQuery = useProviders();
  const connections = useConnections();
  const providers = providersQuery.data || [];
  const navigate = useNavigate();
  const refresh = useRefreshCompute();
  const { resume, resuming } = useResume();
  const [deleting, setDeleting] = useState(false);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteError, setDeleteError] = useState("");
  const pool = query.data;

  if (query.isError && query.error?.status === 404) {
    return (
      <ComputePage>
        <Empty
          title="Pool not found"
          description={`There is no pool called ${name}.`}
          action={<Link to="/pools" className="cp-name-link">Back to pools</Link>}
        />
      </ComputePage>
    );
  }
  if (query.isError) {
    return (
      <ComputePage>
        <Link to="/pools" className="cp-back">Pools</Link>
        <Alert tone="danger">{query.error.message}</Alert>
      </ComputePage>
    );
  }
  if (query.isPending || providersQuery.isPending || connections.isPending) {
    return (
      <ComputePage>
        <Link to="/pools" className="cp-back">Pools</Link>
        <Muted size="sm">Loading pool…</Muted>
      </ComputePage>
    );
  }

  const phase = poolPhase(pool);
  const members = pool.members || [];
  const cloud = Boolean(pool.connection);
  const cap = poolCostCap(pool);

  const destroy = async () => {
    setDeleteBusy(true);
    setDeleteError("");
    try {
      await action(`/pools/${pool.name}/delete`, { confirmName: pool.name });
      notify(`Deleted ${pool.name}`, "success");
      setDeleting(false);
      await refresh();
      navigate({ to: "/pools" });
    } catch (failure) {
      setDeleteError(failure.message);
    } finally {
      setDeleteBusy(false);
    }
  };

  return (
    <ComputePage>
      <Link to="/pools" className="cp-back">Pools</Link>
      <div className="cp-head">
        <div>
          <div className="cp-title-row">
            <h1>{pool.name}</h1>
            <PhaseBadge pool={pool} />
          </div>
          <p className="cp-subtitle">
            {poolSource(pool, providers)}{pool.region ? ` · ${pool.region}` : ""}{cloud && poolSize(pool) ? ` · ${poolSize(pool)}` : ""}
          </p>
        </div>
        {mutable && phase === "Paused" ? (
          <div className="cp-head-actions">
            <Button loading={resuming === pool.name} onClick={() => resume(pool)}>Resume</Button>
          </div>
        ) : null}
      </div>

      <div className="cp-stack">
        <PoolProblems pools={[pool]} mutable={mutable} onResume={resume} resuming={resuming} linkToPool={false} />
        {pool.suspended ? (
          <Alert tone="warning" title="Scaling is suspended">
            <p className="cp-alert-text">The pool keeps its nodes, and they keep costing.</p>
          </Alert>
        ) : null}

        <PageSection title="Status">
          {pool.message && phase !== "Paused" && phase !== "Degraded" ? <p className="rw-rail-body">{pool.message}</p> : null}
          <dl className="cp-grid">
            <Stat label="Nodes" detail={`${pool.nodes} exist · ${pool.minNodes} to ${pool.maxNodes} allowed · ${pool.autoscaling?.enabled ? "autoscaling" : "fixed size"}`}>
              {pool.readyNodes} ready of {pool.desiredNodes} wanted
            </Stat>
            <Stat label="Pods waiting for room">
              {pool.pendingPods}
            </Stat>
            {cloud ? (
              <Stat
                label="Cost now"
                detail={cap
                  ? `Up to ${cap} at ${pool.maxNodes} nodes`
                  : pool.activeSize ? "The cloud states no price for this size" : "Known once the pool has looked up its size"}
              >
                {poolCost(pool) || "—"}
              </Stat>
            ) : null}
            {cloud ? (
              <Stat
                label="Size in use"
                detail={poolUsesFallback(pool)
                  ? `A fallback is in use (step ${pool.fallbackStep}). It goes back to the first choice after an hour.`
                  : formatSizeSummary(pool.activeSize)}
              >
                {poolSize(pool) || "—"}
              </Stat>
            ) : null}
            <Stat label="Last added a node"><When value={pool.lastScaleUp} /></Stat>
            <Stat label="Last removed a node"><When value={pool.lastScaleDown} /></Stat>
            {cloud ? (
              <Stat label="Failed machines in a row" detail={pool.lastFailure || undefined}>
                {pool.consecutiveFailures || "None"}
              </Stat>
            ) : null}
          </dl>
        </PageSection>

        <PageSection
          title="Nodes"
          description={members.length ? undefined : "No nodes now."}
          plain={!members.length}
        >
          {members.length ? (
          <div className="cp-table is-medium">
          <DataTable
            columns={[
              {
                label: "Node",
                render: (node) => (
                  <div>
                    <strong>{node.name}</strong>
                    {node.message ? <span className={`cp-cell-sub${nodeIsStuck(node) ? " is-problem" : ""}`}>{node.message}</span> : null}
                  </div>
                ),
              },
              { label: "Phase", render: (node) => <NodePhaseBadge node={node} /> },
              { label: "Machine", render: (node) => (node.instanceId ? <span className="cp-mono">{node.instanceId}</span> : "—") },
              { label: "Address", render: (node) => (node.address ? <span className="cp-mono">{node.address}</span> : "—") },
              { label: "Size", render: (node) => node.size || "—" },
              { label: "Region", render: (node) => node.region || "—" },
              { label: "Age", align: "right", render: (node) => <span className="cp-num" title={absoluteTime(node.createdAt)}>{ageOf(node.createdAt)}</span> },
            ]}
            rows={members}
            getRowKey={(node) => node.name}
            empty={null}
          />
          </div>
          ) : null}
        </PageSection>

        <PageSection title="Settings" description={mutable ? undefined : "Only an admin can change these."}>
          <PoolForm key={pool.name} pool={pool} providers={providers} connections={connections.data || []} mutable={mutable} />
        </PageSection>

        {mutable ? (
          <PageSection title="Delete pool">
            <p className="cp-danger-note">
              {cloud
                ? `Deletes the pool's ${pool.nodes} ${pool.nodes === 1 ? "node" : "nodes"} and their machines at the cloud.`
                : `Removes the pool's ${pool.nodes} ${pool.nodes === 1 ? "node" : "nodes"} from the cluster. Your servers are not deleted.`}
            </p>
            <Button variant="danger" leftSection={<Trash2 size={15} />} onClick={() => { setDeleteError(""); setDeleting(true); }}>Delete pool</Button>
          </PageSection>
        ) : null}
      </div>

      <ConfirmDelete
        opened={deleting}
        title="Delete pool?"
        name={pool.name}
        confirmLabel="Delete pool"
        busy={deleteBusy}
        error={deleteError}
        onClose={() => setDeleting(false)}
        onConfirm={destroy}
      >
        <p>
          This deletes the pool <strong>{pool.name}</strong> and its {pool.nodes} {pool.nodes === 1 ? "node" : "nodes"}
          {cloud ? " and their machines at the cloud" : ""}. This can't be undone.
        </p>
        <Callout tone="danger">
          {cloud
            ? "Everything on the machines' disks is lost."
            : "Services on these nodes restart elsewhere if there is room."}
        </Callout>
      </ConfirmDelete>
    </ComputePage>
  );
}
