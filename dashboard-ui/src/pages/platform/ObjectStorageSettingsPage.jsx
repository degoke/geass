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
    tagline: "Broad S3 coverage.",
    badge: { label: "Recommended", tone: "success" },
    strengths: [
      "Versioning, object lock, tagging and bucket policies",
      "Suits bigger or busier workloads",
      "Apache-2.0 license",
    ],
  },
  Garage: {
    icon: Warehouse,
    tagline: "A small S3 server for small nodes.",
    badge: { label: "Lightweight", tone: "neutral" },
    strengths: [
      "About 6 MB of memory when idle",
      "One small binary",
      "Plain uploads, downloads and presigned links",
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
        text: "Broad: multipart uploads, presigned links, conditional writes, server-side encryption, versioning, "
          + "object lock, tagging and bucket policies.",
      },
      Garage: {
        text: "Core S3: upload, download, list, delete, multipart uploads and presigned links. "
          + "No versioning, tagging, object lock or bucket policies; limited server-side encryption.",
      },
    },
  },
  {
    label: "Best for",
    values: {
      SeaweedFS: { text: "Less common S3 features, bigger or growing workloads." },
      Garage: { text: "Small clusters, low-memory nodes, plain uploads." },
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
  "Each project gets logins that reach only its own buckets.",
  "Apps cannot change bucket settings such as versioning or policies.",
  "One server on one node; its volume is replicated when the cluster has replicated storage.",
  "One choice for the whole cluster. Switching means deleting the buckets and their data first.",
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

  const openSetup = (nextEngine) => {
    setSetupEngine(nextEngine);
    setSetupOpen(true);
  };

  const providerPicker = !hasClusterBackend ? (
    <SettingsSection title={embedded ? "Object storage" : undefined} flat={embedded}>
      <div className="rw-storage-choose">
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
    <PlatformPage title="Object storage">
      {providerPicker}
      {activeBackend}
      {setupSheet}
    </PlatformPage>
  );
}
