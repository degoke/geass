import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Group, Modal, Text } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { RefreshCw, Trash2 } from "lucide-react";
import { action, api, condition, list, resourceName } from "@/lib/api";
import { resourceHref } from "@/lib/resource-links";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { capacityFits, cpuMillis, memoryBytes, sizeLabel } from "@/lib/capacity";
import { AppLink } from "@/components/AppLink";
import { CodePanel } from "@/components/CodePanel";
import { SizeFields } from "@/components/SizeFields";
import {
  Button,
  DangerZone,
  Field,
  FormStack,
  Input,
  KeyValueTable,
  Muted,
  PageSection,
} from "@/components/geass-ui";
import { SettingsAlert, SettingsFormActions, SettingsPanel } from "@/layout/SettingsShell";

const CLUSTER_MINIO_NAME = "cluster-minio";
const REMOVE_CONFIRM_TEXT = "remove";

function conditionMessage(store) {
  const conditions = store?.status?.conditions || [];
  const ready = conditions.find((item) => item.type === "Ready");
  return ready?.message || ready?.reason || "";
}

function isCapacityMessage(message) {
  return /scale up the cluster|not enough capacity|cannot be scheduled|memory available/i.test(message);
}

function userFacingStatus(ready, store) {
  if (ready) return "Active";
  const raw = conditionMessage(store).trim();
  if (!raw) return "Setting up";
  if (isCapacityMessage(raw)) return "Waiting for capacity";
  if (/installing minio chart/i.test(raw)) return "Installing";
  if (/starting minio server|minio server is starting|service is starting/i.test(raw)) return "Starting";
  if (/failed to start/i.test(raw)) return "Failed";
  if (/\.svc|namespace|helmchart|geass-|kube-|secret|dial tcp|no such host|minio|controller/i.test(raw)) {
    return "Setting up";
  }
  return "Setting up";
}

function ClusterMinIOTabs({ view, onChange }) {
  const tabs = ["details", "manage", "logs"];
  const label = (tab) => {
    if (tab === "logs") return "Logs";
    if (tab === "manage") return "Manage";
    return "Details";
  };
  return (
    <div className="rw-tabs rw-storage-tabs" role="tablist" aria-label="Cluster object storage">
      {tabs.map((tab) => (
        <button
          key={tab}
          type="button"
          role="tab"
          aria-selected={view === tab}
          className={view === tab ? "rw-tab rw-tab-on rw-tab-btn" : "rw-tab rw-tab-btn"}
          onClick={() => onChange(tab)}
        >
          {label(tab)}
        </button>
      ))}
    </div>
  );
}

function ClusterMinIORemoveSection({
  data,
  serverName,
  projectBuckets,
  bucketsBlockRemoval,
  reload,
}) {
  const [removeModalOpen, setRemoveModalOpen] = useState(false);
  const [confirmRemove, setConfirmRemove] = useState("");
  const [removing, setRemoving] = useState(false);
  const removeConfirmed = confirmRemove.trim().toLowerCase() === REMOVE_CONFIRM_TEXT;

  const removeClusterStorage = () => {
    setRemoving(true);
    action(`/object-stores/${serverName}/delete`, { confirmName: serverName })
      .then(() => {
        setRemoveModalOpen(false);
        setConfirmRemove("");
        reload();
        alertUser("Cluster storage removed");
      })
      .catch((error) => alertUser(error.message))
      .finally(() => setRemoving(false));
  };

  if (!canMutate(data)) {
    return null;
  }

  const attachedBucketList = (
    <ul className="rw-storage-blocked-buckets">
      {projectBuckets.map((item) => {
        const name = resourceName(item);
        const project = item.spec?.project;
        const environment = item.spec?.environment;
        return (
          <li key={name}>
            <Link
              to={resourceHref(project, environment, item, "Bucket")}
              className="rw-storage-blocked-buckets-link"
            >
              {name}
            </Link>
            <span className="rw-storage-blocked-buckets-meta">
              {project} · {environment}
            </span>
          </li>
        );
      })}
    </ul>
  );

  return (
    <SettingsPanel className="rw-cf-disconnect rw-storage-remove">
      <h3 className="rw-settings-subtitle">Remove cluster storage</h3>
      <p className="rw-settings-section-desc">
        Uninstalls MinIO from the cluster. You must delete every in-cluster project bucket first—Geass will not remove
        them for you.
      </p>
      {bucketsBlockRemoval && (
        <SettingsAlert tone="danger">
          {projectBuckets.length} project bucket{projectBuckets.length === 1 ? "" : "s"} still use this server. Delete
          them from each project before removing MinIO.
        </SettingsAlert>
      )}
      <DangerZone>
        <Button type="button" variant="danger" onClick={() => setRemoveModalOpen(true)}>
          <Trash2 size={15} /> Remove MinIO
        </Button>
      </DangerZone>
      <Modal
        opened={removeModalOpen}
        onClose={() => {
          if (!removing) {
            setRemoveModalOpen(false);
            setConfirmRemove("");
          }
        }}
        title="Remove cluster object storage?"
        centered
        classNames={{ content: "rw-danger-modal" }}
      >
        <Text size="sm" component="div" className="rw-storage-remove-modal">
          <p>This uninstalls the shared MinIO server from your cluster.</p>
          <ul>
            <li>
              Delete <strong>all</strong> in-cluster project buckets that use this server before you can remove it.
              Geass does not delete project buckets automatically.
            </li>
            <li>Projects will not be able to add new in-cluster buckets until you set up cluster storage again.</li>
            <li>External S3 buckets and connections are not changed.</li>
          </ul>
          <p>You can set up MinIO again later from this page.</p>
        </Text>
        {bucketsBlockRemoval && (
          <>
            <div className="rw-storage-remove-modal-alert">
              <SettingsAlert tone="danger">Remove the buckets below, then try again.</SettingsAlert>
            </div>
            {attachedBucketList}
          </>
        )}
        {!bucketsBlockRemoval && (
          <Field label={`Type ${REMOVE_CONFIRM_TEXT} to confirm`} mt="md">
            <Input
              value={confirmRemove}
              onChange={(event) => setConfirmRemove(event.target.value)}
              autoComplete="off"
              placeholder={REMOVE_CONFIRM_TEXT}
              disabled={removing}
              aria-label="Confirm removal"
            />
          </Field>
        )}
        <Group justify="flex-end" gap="sm" mt="lg">
          <Button
            type="button"
            variant="outline"
            disabled={removing}
            onClick={() => {
              setRemoveModalOpen(false);
              setConfirmRemove("");
            }}
          >
            {bucketsBlockRemoval ? "Close" : "Cancel"}
          </Button>
          {!bucketsBlockRemoval && (
            <Button
              type="button"
              variant="danger"
              loading={removing}
              disabled={!removeConfirmed}
              onClick={removeClusterStorage}
            >
              Remove permanently
            </Button>
          )}
        </Group>
      </Modal>
    </SettingsPanel>
  );
}

function ClusterMinIOManagePanel({ server, serverName, ready, data, reload }) {
  const [view, setView] = useState("details");
  const logs = useQuery({
    queryKey: ["cluster-minio-logs"],
    queryFn: () => api(`/api/object-stores/${CLUSTER_MINIO_NAME}/logs`),
    enabled: view === "logs",
    refetchInterval: view === "logs" && !ready ? 5000 : false,
  });
  const projectBuckets = list(data, "objectStores").filter(
    (item) => item.spec?.project && item.spec?.engine !== "S3" && item.spec?.placement !== "External",
  );
  const bucketsBlockRemoval = projectBuckets.length > 0;
  const capacity = data?.platform?.capacity || {};
  const cpuSizes = data?.platform?.capacity?.cpuSizes || [];
  const memorySizes = data?.platform?.capacity?.memorySizes || [];
  const req = server?.spec?.resources?.requests || {};
  const statusMessage = conditionMessage(server).trim();
  const capacityBlocked = !ready && isCapacityMessage(statusMessage);

  const [cpu, setCpu] = useState(req.cpu || "250m");
  const [memory, setMemory] = useState(req.memory || "512Mi");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setCpu(req.cpu || "250m");
    setMemory(req.memory || "512Mi");
  }, [req.cpu, req.memory]);

  const live = {
    cpu,
    memory,
    cpuMillis: cpuMillis(cpu),
    memoryBytes: memoryBytes(memory),
    perCpuMillis: cpuMillis(cpu),
    perMemoryBytes: memoryBytes(memory),
    replicas: 1,
  };
  const fits = capacityFits(capacity, live);
  const sizeUnchanged = (req.cpu || "250m") === cpu && (req.memory || "512Mi") === memory;

  const saveSize = (event) => {
    event.preventDefault();
    setSaving(true);
    action(`/object-stores/${serverName}/update`, { project: "", environment: "", cpu, memory })
      .then(() => {
        reload();
        alertUser("Size updated. MinIO will restart with the new limits.");
      })
      .catch((error) => alertUser(error.message))
      .finally(() => setSaving(false));
  };

  const detailItems = [
    { label: "Provider", value: "MinIO" },
    { label: "Status", value: userFacingStatus(ready, server) },
    {
      label: "CPU",
      value: sizeLabel(cpuSizes, req.cpu, req.cpu || "250m"),
    },
    {
      label: "Memory",
      value: sizeLabel(memorySizes, req.memory, req.memory || "512Mi"),
    },
    {
      label: "Project buckets",
      value: projectBuckets.length ? `${projectBuckets.length} in use` : "None yet",
    },
  ];

  return (
    <SettingsPanel className="rw-storage-manage">
      <ClusterMinIOTabs view={view} onChange={setView} />
      <div className="rw-storage-tab-panel" role="tabpanel">
        {view === "details" && (
          <>
            {capacityBlocked && (
              <div className="rw-storage-capacity-alert">
                <SettingsAlert tone="warning">{statusMessage}</SettingsAlert>
              </div>
            )}
            <KeyValueTable items={detailItems} />
          </>
        )}
        {view === "manage" && (
          <div className="rw-storage-manage-stack">
            <PageSection
              plain
              title="Size"
              description="Change CPU and memory for the cluster MinIO server. Geass updates the deployment and restarts the pod when needed."
            >
              <FormStack onSubmit={saveSize}>
                <SizeFields
                  cpu={cpu}
                  memory={memory}
                  onChange={(key, value) => {
                    if (key === "cpu") setCpu(value);
                    else setMemory(value);
                  }}
                  data={data}
                />
                {!fits && (
                  <Muted size="sm">
                    The cluster does not have enough capacity for this size.{" "}
                    <AppLink href="/cluster">Open cluster capacity</AppLink> before saving.
                  </Muted>
                )}
                {canMutate(data) && (
                  <SettingsFormActions>
                    <Button type="submit" loading={saving} disabled={!fits || sizeUnchanged}>
                      Save size
                    </Button>
                  </SettingsFormActions>
                )}
              </FormStack>
            </PageSection>
            <ClusterMinIORemoveSection
              data={data}
              serverName={serverName}
              projectBuckets={projectBuckets}
              bucketsBlockRemoval={bucketsBlockRemoval}
              reload={reload}
            />
          </div>
        )}
        {view === "logs" && (
          <PageSection
            plain
            title="Service logs"
            actions={
              <Button
                variant="outline"
                size="sm"
                onClick={() => logs.refetch()}
                leftSection={<RefreshCw size={14} />}
                loading={logs.isFetching}
              >
                Refresh
              </Button>
            }
          >
            {logs.isError ? (
              <Muted size="sm">{logs.error?.message || "Could not load logs."}</Muted>
            ) : (
              <CodePanel code={logs.data?.lines} language="bash" empty="No logs yet." />
            )}
          </PageSection>
        )}
      </div>
    </SettingsPanel>
  );
}

export function MinIOObjectStoragePanel({ data, reload, showSetup = true }) {
  const items = list(data, "objectStores").filter(
    (item) => !item.spec?.project && item.spec?.engine !== "S3" && item.spec?.placement !== "External",
  );
  const server = items[0];
  const ready = condition(server) === "True";
  const capacity = data?.platform?.capacity || {};
  const [cpu, setCpu] = useState("250m");
  const [memory, setMemory] = useState("512Mi");
  const live = {
    cpu,
    memory,
    cpuMillis: cpuMillis(cpu),
    memoryBytes: memoryBytes(memory),
    perCpuMillis: cpuMillis(cpu),
    perMemoryBytes: memoryBytes(memory),
    replicas: 1,
  };
  const fits = capacityFits(capacity, live);
  const serverName = resourceName(server);

  if (server) {
    return <ClusterMinIOManagePanel server={server} serverName={serverName} ready={ready} data={data} reload={reload} />;
  }

  if (!showSetup) {
    return null;
  }

  return (
    <SettingsPanel>
      <FormStack>
        <p className="rw-rail-body">In-cluster buckets stay disabled until this server exists.</p>
        <SizeFields
          cpu={cpu}
          memory={memory}
          onChange={(key, value) => {
            if (key === "cpu") setCpu(value);
            else setMemory(value);
          }}
          data={data}
        />
        {!fits && (
          <Muted size="sm">
            The cluster does not have enough capacity. <AppLink href="/cluster">Open cluster capacity</AppLink> before
            creating MinIO.
          </Muted>
        )}
        <SettingsFormActions>
          <Button
            disabled={!canMutate(data) || !fits}
            onClick={() =>
              action("/object-stores/create", {
                cluster: "on",
                engine: "MinIO",
                placement: "InCluster",
                cpu,
                memory,
              })
                .then(() => {
                  reload();
                  alertUser("MinIO server created");
                })
                .catch((error) => alertUser(error.message))
            }
          >
            Set up MinIO server
          </Button>
        </SettingsFormActions>
      </FormStack>
    </SettingsPanel>
  );
}
