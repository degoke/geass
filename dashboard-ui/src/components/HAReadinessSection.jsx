import { useQueryClient } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { action, list } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { Badge, Button, DataTable, KeyValueTable, Muted, PageSection } from "@/components/geass-ui";

/** The checks behind "Ready for HA", in the order the readiness report makes them, with what each one is called here. */
const CHECKS = [
  ["NodesReady", "Healthy nodes"],
  ["StorageReady", "Storage for databases"],
  ["ClusterReady", "Cluster"],
  ["AddonsReady", "Add-ons"],
  ["Ready", "Ready for HA"],
];

const RESULT = {
  True: { label: "Passed", tone: "success" },
  False: { label: "Not passed", tone: "danger" },
};

/** One row for each condition of the readiness report: what it checks, whether it passed, and what it needs. */
export function haCheckRows(report, healthy) {
  const conditions = report?.status?.conditions || [];
  const known = new Set(CHECKS.map(([type]) => type));
  const labelled = CHECKS.map(([type, label]) => ({ type, label, condition: conditions.find((entry) => entry.type === type) }));
  const others = conditions.filter((entry) => !known.has(entry.type)).map((condition) => ({ type: condition.type, label: condition.type, condition }));
  return [...labelled, ...others]
    .filter((row) => row.condition)
    .map(({ type, label, condition }) => {
      const result = RESULT[condition.status] || { label: "Unknown", tone: "warning" };
      return {
        key: type,
        label,
        result,
        // The message states the requirement. For the nodes it is followed by how many there are now, and the last check
        // is every other one at once.
        detail: type === "NodesReady" ? `${condition.message}. Now: ${healthy}.` : type === "Ready" ? "Every check above must pass." : condition.message,
        since: condition.lastTransitionTime ? new Date(condition.lastTransitionTime).toLocaleString() : "—",
      };
    });
}

/**
 * The readiness report that decides whether a highly available database may start: the summary, and every check
 * behind it with whether it passed. A check that did not pass is what to fix.
 */
export function HAReadinessChecks({ data }) {
  const queryClient = useQueryClient();
  const report = list(data, "haReadiness")[0];
  const healthy = report?.status?.healthyNodes ?? data?.platform?.healthyNodes ?? 0;
  const rows = haCheckRows(report, healthy);
  return (
    <>
      <KeyValueTable
        items={[
          { label: "Healthy nodes", value: String(healthy) },
          { label: "Ready for HA", value: data?.platform?.haReady ? "Yes" : "No" },
        ]}
      />
      {rows.length ? (
        <DataTable
          columns={[
            { label: "Check", key: "label" },
            { label: "Result", render: (row) => <Badge tone={row.result.tone}>{row.result.label}</Badge> },
            { label: "What it needs", key: "detail" },
            { label: "Since", key: "since" },
          ]}
          rows={rows}
          getRowKey={(row) => row.key}
        />
      ) : (
        <Muted size="sm">No checks yet. Click Run readiness check.</Muted>
      )}
      <Button
        mt="md"
        disabled={!canMutate(data)}
        onClick={() => action("/ha-readiness/check").then(() => queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] }))}
        leftSection={<RefreshCw size={15} />}
      >
        Run readiness check
      </Button>
    </>
  );
}

export function HAReadinessSection({ data }) {
  return (
    <PageSection title="HA readiness" description="Needs at least three healthy nodes.">
      <HAReadinessChecks data={data} />
    </PageSection>
  );
}
