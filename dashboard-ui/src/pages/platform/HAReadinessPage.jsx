import { useQueryClient } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { action, list } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { Button, KeyValueTable, PageSection } from "@/components/geass-ui";
import { PlatformPage } from "@/layout/PlatformPage";

export function HAReadinessPage({ data }) {
  const queryClient = useQueryClient();
  const report = list(data, "haReadiness")[0];
  const healthy = report?.status?.healthyNodes ?? data?.platform?.healthyNodes ?? 0;
  return (
    <PlatformPage title="HA readiness" description="High availability databases need at least three healthy nodes.">
      <PageSection title="Cluster HA" description="Run a check after changing nodes or storage.">
        <KeyValueTable
          items={[
            { label: "Healthy nodes", value: String(healthy) },
            { label: "Ready for HA", value: data?.platform?.haReady ? "Yes" : "No" },
          ]}
        />
        <Button mt="md" disabled={!canMutate(data)} onClick={() => action("/ha-readiness/check").then(() => queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] }))}>
          <RefreshCw size={15} /> Run readiness check
        </Button>
      </PageSection>
    </PlatformPage>
  );
}
