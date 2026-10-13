import { useState } from "react";
import { Layers, Warehouse } from "lucide-react";
import { condition } from "@/lib/api";
import { CLUSTER_SERVER_ENGINES, clusterObjectStoreServer, startingSizeLabel } from "@/lib/object-storage";
import { Badge, Button } from "@/components/geass-ui";
import { SettingsSection, SettingsSheet } from "@/layout/SettingsShell";
import { PlatformPage } from "@/layout/PlatformPage";
import { ObjectStoragePanel } from "@/pages/platform/ObjectStoragePanel";

const STATUS_LABEL = {
  connected: "Active",
  pending: "Pending",
  disconnected: "Not set up",
};

/** What an admin reads before choosing: one card per server. */
const PROVIDERS = {
  SeaweedFS: {
    icon: Layers,
    tagline: "Broad S3 coverage for apps that use more than the basics.",
    badge: { label: "Recommended", tone: "success" },
    reason: "Recommended because it works with the widest range of S3 clients.",
    strengths: [
      "Supports versioning, object lock, tagging and bucket policies",
      "Suits bigger or busier workloads, and apps you expect to grow",
      "Apache-2.0 license",
    ],
  },
  Garage: {
    icon: Warehouse,
    tagline: "A small, light S3 server for small nodes.",
    badge: { label: "Lightweight", tone: "neutral" },
    reason: "The lightweight choice for small clusters and low-memory nodes.",
    strengths: [
      "Uses about 6 MB of memory when idle",
      "One small binary",
      "Covers plain uploads, downloads and presigned links",
    ],
  },
};

/** Rows of the side-by-side comparison. `values` is keyed by engine; a function gets the dashboard data. */
const COMPARISON = [
  {
    label: "Memory when idle",
    values: {
      SeaweedFS: { text: "About 60 MB", note: (data) => startingSize(data, "SeaweedFS") },
      Garage: { text: "About 6 MB", note: (data) => startingSize(data, "Garage") },
    },
  },
  {
    label: "S3 coverage",
    values: {
      SeaweedFS: {
        text: "Broad: multipart uploads, presigned links, conditional writes (If-Match and If-None-Match), "
          + "server-side encryption, object versioning, object lock, object tagging and bucket policies.",
      },
      Garage: {
        text: "Core S3 only: upload, download, list, delete, multipart uploads and presigned links. "
          + "No object versioning, tagging, object lock or bucket policies, and server-side encryption is limited.",
      },
    },
  },
  {
    label: "Best for",
    values: {
      SeaweedFS: { text: "Apps that use less common S3 features, bigger or busier workloads, or that you expect to grow." },
      Garage: { text: "Small clusters and low-memory nodes, plain uploads and static assets." },
    },
  },
  {
    label: "Footprint",
    values: {
      SeaweedFS: { text: "One process running four components" },
      Garage: { text: "One small binary" },
    },
  },
  {
    label: "License",
    values: {
      SeaweedFS: { text: "Apache-2.0" },
      Garage: { text: "AGPL-3.0 (Geass runs it unmodified)" },
    },
  },
];

/** What is true whichever server is chosen, said once. */
const SAME_ON_BOTH = [
  "Each project gets its own logins, and they can only reach that project's buckets.",
  "Apps connect the usual way, with an S3 endpoint and access keys that Geass provides for each bucket, using path-style addressing.",
  "For now the server runs as one server on one node. Its data volume is kept on several nodes when your cluster has replicated storage, and on that one node otherwise. You choose its size at setup and can make it bigger later where the cluster's storage can be resized.",
  "You choose once for the whole cluster. Switching later means deleting the buckets and their data first.",
];

function startingSize(data, engine) {
  const size = startingSizeLabel(data, engine);
  return size ? `Starts at ${size}` : "";
}

function ObjectStorageProviderCard({ engine, onSetUp }) {
  const provider = PROVIDERS[engine];
  const Icon = provider.icon;
  return (
    <div className="rw-settings-integration-card rw-storage-provider" data-provider={engine}>
      <div className="rw-settings-integration-icon" aria-hidden="true">
        <Icon size={22} />
      </div>
      <div className="rw-settings-integration-body">
        <div className="rw-settings-integration-heading">
          <h3>{engine}</h3>
          <Badge tone={provider.badge.tone}>{provider.badge.label}</Badge>
        </div>
        <p>{provider.tagline}</p>
        <p className="rw-storage-provider-reason">{provider.reason}</p>
        <ul className="rw-storage-list">
          {provider.strengths.map((strength) => <li key={strength}>{strength}</li>)}
        </ul>
        <Button type="button" variant="outline" onClick={onSetUp} aria-label={`Set up ${engine}`}>
          Set up
        </Button>
      </div>
    </div>
  );
}

function ObjectStorageComparison({ data }) {
  const [first, second] = CLUSTER_SERVER_ENGINES;
  const cell = (value, engine) => {
    const note = value.note ? value.note(data) : "";
    return (
      <td key={engine} data-label={engine}>
        {value.text}
        {note ? <span className="rw-storage-compare-note">{note}</span> : null}
      </td>
    );
  };
  return (
    <div className="rw-storage-compare-block">
      <h3 className="rw-settings-subtitle">Compare {first} and {second}</h3>
      <p className="rw-rail-body">
        Both store your buckets on the cluster and speak S3. They differ in how much of S3 they cover and how much memory
        they use.
      </p>
      <div className="rw-storage-compare">
        <table className="rw-storage-compare-table">
          <thead>
            <tr>
              <th scope="col"><span className="rw-storage-sr">Compared</span></th>
              <th scope="col">{first}</th>
              <th scope="col">{second}</th>
            </tr>
          </thead>
          <tbody>
            {COMPARISON.map((row) => (
              <tr key={row.label}>
                <th scope="row">{row.label}</th>
                {CLUSTER_SERVER_ENGINES.map((engine) => cell(row.values[engine], engine))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="rw-storage-footnote">
        The table shows what each server supports. Geass gives each app a login limited to the objects in its own bucket,
        so an app cannot change bucket-level settings such as versioning or policies today.
      </p>
      <h3 className="rw-settings-subtitle">The same on both</h3>
      <ul className="rw-storage-list rw-storage-same">
        {SAME_ON_BOTH.map((line) => <li key={line}>{line}</li>)}
      </ul>
    </div>
  );
}

export function ObjectStorageSettingsPage({ data, reload, embedded = false }) {
  const [setupOpen, setSetupOpen] = useState(false);
  const [setupEngine, setSetupEngine] = useState(CLUSTER_SERVER_ENGINES[0]);
  const server = clusterObjectStoreServer(data);
  const engine = server?.spec?.engine || "";
  const ready = server ? condition(server) === "True" : false;
  const status = !server ? "disconnected" : ready ? "connected" : "pending";
  const statusLabel = STATUS_LABEL[status];
  const hasClusterBackend = Boolean(server);

  let storageDescription = "Choose one cluster-wide backend for project buckets. The choice is made once: switching later means deleting every in-cluster bucket and its data first.";
  if (hasClusterBackend) {
    storageDescription = `${engine} serves the in-cluster project buckets. To switch, delete every in-cluster bucket, remove it, then set up the other server.`;
  }

  const openSetup = (nextEngine) => {
    setSetupEngine(nextEngine);
    setSetupOpen(true);
  };

  const providerPicker = !hasClusterBackend ? (
    <SettingsSection title={embedded ? "Object storage" : undefined} flat={embedded}>
      <div className="rw-storage-choose">
        {embedded ? <p className="rw-rail-body">{storageDescription}</p> : null}
        <div className="rw-storage-providers">
          {CLUSTER_SERVER_ENGINES.map((name) => (
            <ObjectStorageProviderCard key={name} engine={name} onSetUp={() => openSetup(name)} />
          ))}
        </div>
        <ObjectStorageComparison data={data} />
      </div>
    </SettingsSection>
  ) : null;

  const activeBackend = hasClusterBackend ? (
    <SettingsSection
      title={embedded ? "Object storage" : undefined}
      description={embedded ? storageDescription : undefined}
      flat={embedded && !providerPicker}
      actions={(
        <span className={`rw-integration-status is-${status}`} title={`${engine} cluster backend`}>
          {engine} · {statusLabel}
        </span>
      )}
    >
      <div className="rw-storage-active">
        <ObjectStoragePanel data={data} reload={reload} />
      </div>
    </SettingsSection>
  ) : null;

  const setupSheet = (
    <SettingsSheet
      opened={setupOpen}
      onClose={() => setSetupOpen(false)}
      title={`Set up ${setupEngine}`}
      description={`Run ${setupEngine} as the object storage server for project buckets across the cluster.`}
    >
      <ObjectStoragePanel
        data={data}
        reload={() => { reload(); setSetupOpen(false); }}
        setupEngine={setupEngine}
      />
    </SettingsSheet>
  );

  if (embedded) {
    return (
      <>
        {providerPicker}
        {activeBackend}
        {setupSheet}
      </>
    );
  }

  return (
    <PlatformPage title="Object storage" description={storageDescription}>
      {providerPicker}
      {activeBackend}
      {setupSheet}
    </PlatformPage>
  );
}
