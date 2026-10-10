import { useState } from "react";
import { HardDrive, Layers, Warehouse } from "lucide-react";
import { condition, list } from "@/lib/api";
import { Button } from "@/components/geass-ui";
import { SettingsSection, SettingsSheet } from "@/layout/SettingsShell";
import { PlatformPage } from "@/layout/PlatformPage";
import { MinIOObjectStoragePanel } from "@/pages/platform/MinIOObjectStoragePanel";

const STATUS_LABEL = {
  connected: "Active",
  pending: "Pending",
  disconnected: "Not set up",
  unavailable: "Coming soon",
};

const CLUSTER_PROVIDER = {
  id: "minio",
  icon: HardDrive,
  name: "MinIO",
};

function ObjectStorageProviderCard({
  icon: Icon,
  name,
  description,
  status = "disconnected",
  enabled = true,
  actionLabel,
  onConfigure,
}) {
  return (
    <div
      className={`rw-settings-integration-card is-${status}${enabled ? "" : " is-disabled"}`}
      data-connection-status={status}
    >
      <div className="rw-settings-integration-icon" aria-hidden="true">
        <Icon size={22} />
      </div>
      <div className="rw-settings-integration-body">
        <div className="rw-settings-integration-heading">
          <h3>{name}</h3>
          <span className={`rw-integration-status is-${status}`}>{STATUS_LABEL[status] || status}</span>
        </div>
        <p>{description}</p>
        {enabled ? (
          <Button type="button" variant="outline" onClick={onConfigure} aria-label={`${actionLabel} ${name}`}>
            {actionLabel}
          </Button>
        ) : null}
      </div>
    </div>
  );
}

function clusterMinIOServer(data) {
  const items = list(data, "objectStores").filter(
    (item) => !item.spec?.project && item.spec?.engine !== "S3" && item.spec?.placement !== "External",
  );
  return items[0];
}

export function ObjectStorageSettingsPage({ data, reload, embedded = false }) {
  const [setupOpen, setSetupOpen] = useState(false);
  const server = clusterMinIOServer(data);
  const ready = server ? condition(server) === "True" : false;
  const minioStatus = !server ? "disconnected" : ready ? "connected" : "pending";
  const hasClusterBackend = Boolean(server);

  const storageDescription = hasClusterBackend
    ? "MinIO serves in-cluster project buckets. Other backends stay disabled until you remove it."
    : "Choose one cluster-wide backend. Only one can be installed at a time.";

  const providerPicker = !hasClusterBackend ? (
    <SettingsSection
      title={embedded ? "Object storage" : undefined}
      description={embedded ? storageDescription : undefined}
      flat={embedded}
    >
      <div className="rw-settings-connectors-grid">
        <ObjectStorageProviderCard
          icon={CLUSTER_PROVIDER.icon}
          name={CLUSTER_PROVIDER.name}
          status={minioStatus}
          enabled
          description="S3-compatible object storage for in-cluster project buckets."
          actionLabel="Set up"
          onConfigure={() => setSetupOpen(true)}
        />
        <ObjectStorageProviderCard
          icon={Warehouse}
          name="Garage SFS"
          status="unavailable"
          enabled={false}
          description="Distributed object storage for self-hosted S3-compatible buckets."
        />
        <ObjectStorageProviderCard
          icon={Layers}
          name="SeaweedFS"
          status="unavailable"
          enabled={false}
          description="Fast blob storage with optional S3 gateway for project buckets."
        />
      </div>
    </SettingsSection>
  ) : null;

  const activeBackend = hasClusterBackend ? (
    <SettingsSection
      title={embedded ? "Object storage" : undefined}
      description={embedded ? storageDescription : undefined}
      flat={embedded && !providerPicker}
      actions={(
        <span className={`rw-integration-status is-${minioStatus}`} title={`${CLUSTER_PROVIDER.name} cluster backend`}>
          {CLUSTER_PROVIDER.name} · {STATUS_LABEL[minioStatus]}
        </span>
      )}
    >
      <div className="rw-storage-active">
        <MinIOObjectStoragePanel data={data} reload={reload} showSetup={false} />
      </div>
    </SettingsSection>
  ) : null;

  const setupSheet = (
    <SettingsSheet
      opened={setupOpen}
      onClose={() => setSetupOpen(false)}
      title="MinIO"
      description="Provision a MinIO server for project buckets across the cluster."
    >
      <MinIOObjectStoragePanel data={data} reload={() => { reload(); setSetupOpen(false); }} showSetup />
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
