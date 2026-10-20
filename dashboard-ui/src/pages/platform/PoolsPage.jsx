import { useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { action } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { notify } from "@/lib/notify";
import {
  absoluteTime,
  poolCost,
  poolCostCap,
  poolNote,
  poolPhase,
  poolSize,
  poolSource,
  poolUsesFallback,
  relativeTime,
  totalPoolCost,
  useConnections,
  useProviders,
  usePools,
  useRefreshCompute,
} from "@/lib/compute";
import { Badge, Button, DataTable, Muted, PageSection } from "@/components/geass-ui";
import { Alert, ComputePage, PhaseBadge } from "@/components/compute/ComputeBits";
import { PoolForm } from "@/components/compute/PoolForm";
import { SettingsSheet } from "@/layout/SettingsShell";

/**
 * What a pool that has stopped, or is struggling, or has lost track of machines, needs to say: one alert for each pool that
 * has something to say, above the list. A pool that is paused or degraded and also has machines no node owns says both in one.
 */
export function PoolProblems({ pools, mutable, onResume, resuming, linkToPool = true }) {
  const alerts = [];
  pools.forEach((pool) => {
    const phase = poolPhase(pool);
    const orphans = pool.orphans || [];
    const paused = phase === "Paused";
    const degraded = phase === "Degraded";
    if (!paused && !degraded && !orphans.length) return;
    const open = linkToPool ? <Link className="cp-name-link" to={`/pools/${pool.name}`}>Open {pool.name}</Link> : null;
    const title = paused
      ? `${pool.name} is paused`
      : degraded
        ? `${pool.name} is degraded`
        : `${pool.name} has machines that no node owns`;
    alerts.push(
      <Alert
        key={pool.name}
        tone={paused ? "danger" : "warning"}
        title={title}
        actions={(
          <>
            {open}
            {paused && mutable ? (
              <Button size="sm" variant="outline" loading={resuming === pool.name} onClick={() => onResume(pool)}>Resume</Button>
            ) : null}
          </>
        )}
      >
        {paused ? (
          <>
            <p className="cp-alert-text">
              {pool.consecutiveFailures
                ? `${pool.consecutiveFailures} ${pool.consecutiveFailures === 1 ? "machine" : "machines"} in a row failed to become nodes, so the pool stopped adding machines. `
                : "The pool stopped adding machines after repeated failures. "}
              {pool.pausedUntil ? (
                <>It tries again <span title={absoluteTime(pool.pausedUntil)}>{relativeTime(pool.pausedUntil)}</span>.</>
              ) : null}
            </p>
            {pool.lastFailure ? <p className="cp-alert-text">Last failure: {pool.lastFailure}</p> : null}
            {mutable ? (
              <p className="cp-alert-text">
                Resume forgets the failures and lets the pool try again now. If the cause is not fixed, it pauses again after three more.
              </p>
            ) : null}
          </>
        ) : null}
        {degraded ? (
          <>
            <p className="cp-alert-text">{pool.message || "Something is wrong with the pool, but it is still trying."}</p>
            {pool.lastFailure && pool.lastFailure !== pool.message ? <p className="cp-alert-text">Last failure: {pool.lastFailure}</p> : null}
          </>
        ) : null}
        {orphans.length ? (
          <>
            <p className="cp-alert-text">
              {orphans.length} {orphans.length === 1 ? "machine" : "machines"} at the cloud carry this pool's labels and belong to no node:{" "}
              <span className="cp-mono">{orphans.join(", ")}</span>
            </p>
            <p className="cp-alert-text">
              {pool.orphanPolicy === "Delete"
                ? "Geass deletes a machine that has had no node for twenty minutes."
                : "Geass only reports them, because deleting a machine cannot be undone. They may still be billing you: delete them at the cloud, or set the pool to delete them."}
            </p>
          </>
        ) : null}
      </Alert>,
    );
  });
  if (!alerts.length) return null;
  return <div className="cp-alerts">{alerts}</div>;
}

/** Resumes a paused pool, and says what was asked. */
export function useResume() {
  const refresh = useRefreshCompute();
  const [resuming, setResuming] = useState("");
  const resume = async (pool) => {
    setResuming(pool.name);
    try {
      await action(`/pools/${pool.name}/resume`);
      notify(`Asked ${pool.name} to try again`, "success");
      await refresh();
    } catch (failure) {
      notify(failure.message, "error");
    } finally {
      setResuming("");
    }
  };
  return { resume, resuming };
}

function ready(pool) {
  return pool.readyNodes ?? 0;
}

export function PoolsPage({ data }) {
  const mutable = canMutate(data);
  const pools = usePools();
  const connections = useConnections();
  const providersQuery = useProviders();
  const providers = providersQuery.data || [];
  const navigate = useNavigate();
  const [adding, setAdding] = useState(false);
  const { resume, resuming } = useResume();
  const rows = pools.data || [];
  const totalNodes = rows.reduce((sum, pool) => sum + (pool.nodes || 0), 0);
  const readyNodes = rows.reduce((sum, pool) => sum + ready(pool), 0);
  const cost = totalPoolCost(rows);
  const formReady = !providersQuery.isPending && !connections.isPending;

  return (
    <ComputePage>
      <div className="cp-head">
        <div>
          <h1>Pools</h1>
        </div>
        {mutable ? (
          <div className="cp-head-actions">
            <Button leftSection={<Plus size={15} />} onClick={() => setAdding(true)}>Add pool</Button>
          </div>
        ) : null}
      </div>
      <p className="cp-intro">
        A pool adds nodes to the cluster when pods cannot start, and takes idle ones away. Its machines are created at a cloud
        with a <Link to="/settings/connectors">cloud connection</Link>, or are servers of your own that Geass joins when it needs room. A pool
        never grows past its maximum, which is the limit on what it can cost.
      </p>

      {pools.isError ? <Alert tone="danger">{pools.error.message}</Alert> : null}
      {pools.isPending ? <Muted size="sm">Loading pools…</Muted> : null}

      {!pools.isPending && !pools.isError ? (
        <>
          <PoolProblems pools={rows} mutable={mutable} onResume={resume} resuming={resuming} />
          {rows.length ? (
            <ul className="cp-facts">
              <li><strong>{rows.length}</strong> {rows.length === 1 ? "pool" : "pools"}</li>
              <li><strong>{readyNodes}</strong> of <strong>{totalNodes}</strong> {totalNodes === 1 ? "node" : "nodes"} ready</li>
              {cost ? <li>Costing about <strong>{cost}</strong> now</li> : null}
            </ul>
          ) : null}
          <PageSection title="Pools">
            <div className="cp-table">
            <DataTable
              columns={[
                {
                  label: "Pool",
                  render: (pool) => (
                    <div>
                      <Link className="cp-name-link" to={`/pools/${pool.name}`}>{pool.name}</Link>
                      <span className="cp-cell-sub">{poolSource(pool, providers)}{pool.region ? ` · ${pool.region}` : ""}</span>
                    </div>
                  ),
                },
                {
                  label: "Status",
                  render: (pool) => (
                    <div>
                      <PhaseBadge pool={pool} />
                      {poolNote(pool) ? <span className="cp-cell-sub is-clamp">{poolNote(pool)}</span> : null}
                    </div>
                  ),
                },
                {
                  label: "Nodes",
                  render: (pool) => (
                    <div className="cp-num">
                      {ready(pool)} ready of {pool.desiredNodes}
                      {pool.nodes !== ready(pool) ? <span className="cp-cell-sub">{pool.nodes} exist</span> : null}
                    </div>
                  ),
                },
                {
                  label: "Min to max",
                  render: (pool) => (
                    <div className="cp-num">
                      {pool.minNodes} to {pool.maxNodes}
                      <span className="cp-cell-sub">{pool.autoscaling?.enabled ? "Autoscaling" : "Fixed size"}{pool.suspended ? ", suspended" : ""}</span>
                    </div>
                  ),
                },
                {
                  label: "Size",
                  render: (pool) => (
                    <div>
                      {poolSize(pool) || "—"}
                      {poolUsesFallback(pool) ? <span className="cp-cell-sub">Fallback in use (step {pool.fallbackStep})</span> : null}
                    </div>
                  ),
                },
                {
                  label: "Cost per hour",
                  render: (pool) => (
                    <div className="cp-num">
                      {poolCost(pool) || "—"}
                      {poolCostCap(pool) ? <span className="cp-cell-sub">up to {poolCostCap(pool)}</span> : null}
                    </div>
                  ),
                },
                {
                  label: "Pending pods",
                  align: "right",
                  render: (pool) => (pool.pendingPods ? <Badge tone="warning">{pool.pendingPods}</Badge> : <span className="cp-num">0</span>),
                },
                {
                  label: "",
                  align: "right",
                  render: (pool) => (mutable && poolPhase(pool) === "Paused" ? (
                    <Button size="sm" variant="outline" loading={resuming === pool.name} onClick={() => resume(pool)} aria-label={`Resume ${pool.name}`}>Resume</Button>
                  ) : null),
                },
              ]}
              rows={rows}
              getRowKey={(pool) => pool.name}
              empty={(
                <div>
                  <p className="cp-empty">
                    No pools yet. {mutable
                      ? "Add one to let the cluster grow when pods cannot start. Nothing is created until you save it, and a pool never has more machines than its maximum."
                      : "An admin can add one."}
                  </p>
                  {mutable ? <Button leftSection={<Plus size={15} />} onClick={() => setAdding(true)}>Add pool</Button> : null}
                </div>
              )}
            />
            </div>
          </PageSection>
        </>
      ) : null}

      <SettingsSheet
        opened={adding}
        onClose={() => setAdding(false)}
        title="Add a pool"
        description="A pool adds nodes when pods cannot start, up to its maximum, and removes idle ones."
      >
        {adding ? (
          !formReady ? (
            <Muted size="sm">Loading…</Muted>
          ) : providersQuery.isError ? (
            <Alert tone="danger">{providersQuery.error.message}</Alert>
          ) : (
            <PoolForm
              providers={providers}
              connections={connections.data || []}
              onDone={(name) => { setAdding(false); navigate({ to: `/pools/${name}` }); }}
              onCancel={() => setAdding(false)}
            />
          )
        ) : null}
      </SettingsSheet>
    </ComputePage>
  );
}
