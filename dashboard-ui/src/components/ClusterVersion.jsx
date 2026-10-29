import { Badge, DataTable, PageSection } from "@/components/geass-ui";
import { managedFor } from "@/lib/managed";

/** How an upgrade of the workers' K3s is going, in the words and tones the rest of the page uses. */
const UPGRADE_PHASES = {
  Complete: { label: "Up to date", tone: "success" },
  Upgrading: { label: "Upgrading", tone: "warning" },
  Waiting: { label: "Waiting", tone: "warning" },
  WaitingForControlPlane: { label: "Waiting for the control plane", tone: "warning" },
  Failed: { label: "Failed", tone: "danger" },
};

/** A release as a person reads it. "dev" is a build that is not a release, and an empty one was never recorded. */
export function releaseLabel(value) {
  if (!value) return "unknown";
  if (value === "dev") return "development build";
  return value;
}

/** The rows of the version table: what the cluster runs, and the upgrade of its workers while one is asked for. */
export function versionRows(release) {
  if (!release) return [];
  const rows = [
    {
      component: "Geass",
      version: releaseLabel(release.geass),
      detail: release.schemaLevel ? `Data schema level ${release.schemaLevel}` : "",
    },
    {
      component: "K3s (control plane)",
      version: release.k3s || "unknown",
      detail: release.k3s ? "" : "No control plane node reports a K3s version.",
    },
  ];
  if (release.upgrade?.phase) {
    const phase = UPGRADE_PHASES[release.upgrade.phase] || { label: release.upgrade.phase, tone: "neutral" };
    rows.push({
      component: "K3s on workers (target)",
      version: release.upgrade.targetVersion || "—",
      detail: release.upgrade.message || "",
      phase,
    });
  }
  return rows;
}

/**
 * What the Version section says about updating. A platform that runs this Geass for the person updates Geass and K3s
 * itself, so it says who does, and does not tell the person to run the installer.
 */
export function versionDescription(data) {
  const geass = managedFor(data, "upgrades");
  const k3s = managedFor(data, "k3sVersion");
  if (geass && k3s) return `Updating Geass and the K3s version is managed by ${geass.by} and cannot be changed here.`;
  if (geass) return `Updating Geass is managed by ${geass.by} and cannot be changed here.`;
  if (k3s) return `The K3s version is managed by ${k3s.by} and cannot be changed here.`;
  return "Update by running a newer installer on the control plane.";
}

/** Which Geass and K3s the cluster runs. Updating either is described in the upgrade guide. */
export function ClusterVersion({ data }) {
  const rows = versionRows(data?.platform?.release);
  if (!rows.length) return null;
  return (
    <PageSection title="Version" description={versionDescription(data)}>
      <DataTable
        columns={[
          { label: "Component", key: "component" },
          { label: "Version", key: "version" },
          { label: "Detail", key: "detail" },
          {
            label: "Status",
            align: "right",
            render: (row) => (row.phase ? <Badge tone={row.phase.tone}>{row.phase.label}</Badge> : null),
          },
        ]}
        rows={rows}
        getRowKey={(row) => row.component}
      />
    </PageSection>
  );
}
