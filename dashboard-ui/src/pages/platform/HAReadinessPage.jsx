import { PageSection } from "@/components/geass-ui";
import { HAReadinessChecks } from "@/components/HAReadinessSection";
import { PlatformPage } from "@/layout/PlatformPage";

export function HAReadinessPage({ data }) {
  return (
    <PlatformPage title="HA readiness" description="Needs at least three healthy nodes.">
      <PageSection title="Cluster HA">
        <HAReadinessChecks data={data} />
      </PageSection>
    </PlatformPage>
  );
}
