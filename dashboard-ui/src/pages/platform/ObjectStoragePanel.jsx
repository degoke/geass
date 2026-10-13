import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Group, Modal, Text } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { RefreshCw, Trash2 } from "lucide-react";
import { action, api, condition, resourceName } from "@/lib/api";
import { resourceHref } from "@/lib/resource-links";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { capacityFits, cpuMillis, memoryBytes, sizeLabel, storageSizeOptions } from "@/lib/capacity";
import {
  CLUSTER_SERVER_ENGINES,
  clusterObjectStoreServer,
  clusterProjectBuckets,
  defaultServerStorage,
  objectStorageServer,
  serverStorageSize,
  storageBytes,
  storageSizeProblem,
} from "@/lib/object-storage";
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
  Select,
} from "@/components/geass-ui";
import { SettingsAlert, SettingsFormActions, SettingsPanel } from "@/layout/SettingsShell";

const REMOVE_CONFIRM_TEXT = "remove";

/** The server people would switch to: there are two, so it is the one that is not in use. */
function otherEngine(engine) {
  return CLUSTER_SERVER_ENGINES.find((entry) => entry !== engine) || "";
}

function engineLabel(data, engine) {
  return objectStorageServer(data, engine)?.label || engine || "Object storage";
}

function conditionMessage(store) {
  const conditions = store?.status?.conditions || [];
  const ready = conditions.find((item) => item.type === "Ready");
  return ready?.message || ready?.reason || "";
}

function isCapacityMessage(message) {
  return /scale up the cluster|not enough capacity|cannot be scheduled|memory available/i.test(message);
}

function isStorageUnavailableMessage(message) {
  return /storage is unavailable/i.test(message);
}

function userFacingStatus(ready, store) {
  if (ready) return "Active";
  const raw = conditionMessage(store).trim();
  if (!raw) return "Setting up";
  if (isCapacityMessage(raw)) return "Waiting for capacity";
  if (isStorageUnavailableMessage(raw)) return "Storage unavailable";
  if (/storage is starting|service is starting/i.test(raw)) return "Starting";
  if (/failed to start/i.test(raw)) return "Failed";
  return "Setting up";
}

/** A volume size in the words the size pickers use ("10 GB"), so the two never disagree. */
function storageText(size) {
  return size ? sizeLabel(storageSizeOptions(size, ""), size, size) : "";
}

/**
 * The same size options the database forms use, for a server's volume. Nothing below `floor` is offered, because a
 * volume cannot shrink, but `floor` itself always is. An empty size means the default, which the backend chooses.
 */
function storageOptionElements(size, floor = "") {
  const options = storageSizeOptions(size, floor);
  if (floor && !options.some((option) => option.value === floor)) {
    options.push({ value: floor, label: storageText(floor) });
    options.sort((a, b) => storageBytes(a.value) - storageBytes(b.value));
  }
  const elements = options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>);
  return size ? elements : [<option key="default" value="">Default</option>, ...elements];
}

function ObjectStorageTabs({ view, onChange }) {
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

function ObjectStorageRemoveSection({
  data,
  serverName,
  provider,
  nextEngine,
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
        alertUser("Object storage removed");
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

  const switchNote = `To switch to ${nextEngine}, delete every in-cluster bucket, remove ${provider}, then set up ${nextEngine}. Data is not copied from one to the other.`;

  return (
    <SettingsPanel className="rw-cf-disconnect rw-storage-remove">
      <h3 className="rw-settings-subtitle">Remove object storage</h3>
      <p className="rw-rail-body">
        Removes {provider} from the cluster and deletes <strong>all</strong> data on the server&apos;s volume. You must
        delete every in-cluster project bucket first; Geass will not remove them for you.
      </p>
      <p className="rw-rail-body">{switchNote}</p>
      {bucketsBlockRemoval && (
        <SettingsAlert tone="danger">
          {projectBuckets.length} project bucket{projectBuckets.length === 1 ? "" : "s"} still use this server. Delete
          them from each project before removing {provider}.
        </SettingsAlert>
      )}
      <DangerZone>
        <Button type="button" variant="danger" onClick={() => setRemoveModalOpen(true)}>
          <Trash2 size={15} /> Remove {provider}
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
        title="Remove object storage?"
        centered
        classNames={{ content: "rw-danger-modal" }}
      >
        <Text size="sm" component="div" className="rw-storage-remove-modal">
          <p>
            This removes the shared {provider} server from your cluster and deletes <strong>all</strong> data on its
            volume. It cannot be undone.
          </p>
          <ul>
            <li>
              Delete <strong>all</strong> in-cluster project buckets that use this server before you can remove it.
              Geass does not delete project buckets automatically.
            </li>
            <li>Projects will not be able to add new in-cluster buckets until you set up object storage again.</li>
            <li>External S3 buckets and connections are not changed.</li>
          </ul>
          <p>{switchNote}</p>
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

function ObjectStorageManagePanel({ server, serverName, ready, data, reload }) {
  const [view, setView] = useState("details");
  const engine = server?.spec?.engine || "";
  const provider = engineLabel(data, engine);
  const logs = useQuery({
    queryKey: ["cluster-object-storage-logs", serverName],
    queryFn: () => api(`/api/object-stores/${serverName}/logs`),
    enabled: view === "logs",
    refetchInterval: view === "logs" && !ready ? 5000 : false,
  });
  const projectBuckets = clusterProjectBuckets(data);
  const bucketsBlockRemoval = projectBuckets.length > 0;
  const capacity = data?.platform?.capacity || {};
  const cpuSizes = data?.platform?.capacity?.cpuSizes || [];
  const memorySizes = data?.platform?.capacity?.memorySizes || [];
  const req = server?.spec?.resources?.requests || {};
  const startsAt = objectStorageServer(data, engine) || {};
  const currentCpu = req.cpu || startsAt.cpu || "";
  const currentMemory = req.memory || startsAt.memory || "";
  const statusMessage = conditionMessage(server).trim();
  const capacityBlocked = !ready && isCapacityMessage(statusMessage);
  const storageBlocked = !ready && isStorageUnavailableMessage(statusMessage);
  const currentStorage = serverStorageSize(server);
  const defaultStorage = defaultServerStorage(data);
  // Unknown counts as resizable; the backend makes the same call and the cluster has the last word.
  const resizable = data?.platform?.objectStorageResizable !== false;

  const [cpu, setCpu] = useState(currentCpu);
  const [memory, setMemory] = useState(currentMemory);
  const [saving, setSaving] = useState(false);
  const [storage, setStorage] = useState(currentStorage || defaultStorage);
  const [savingStorage, setSavingStorage] = useState(false);

  useEffect(() => {
    setCpu(currentCpu);
    setMemory(currentMemory);
  }, [currentCpu, currentMemory]);

  useEffect(() => {
    setStorage(currentStorage || defaultStorage);
  }, [currentStorage, defaultStorage]);

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
  const sizeUnchanged = currentCpu === cpu && currentMemory === memory;

  const saveSize = (event) => {
    event.preventDefault();
    setSaving(true);
    action(`/object-stores/${serverName}/update`, { project: "", environment: "", cpu, memory })
      .then(() => {
        reload();
        alertUser(`Size updated. ${provider} will restart with the new limits.`);
      })
      .catch((error) => alertUser(error.message))
      .finally(() => setSaving(false));
  };

  // Where the volume stands now is what a new size is compared with. It can only grow.
  const storageChanged = storage !== (currentStorage || defaultStorage);
  const storageProblem = storageSizeProblem(storage);
  const storageShrinks = Boolean(currentStorage) && storageBytes(storage) < storageBytes(currentStorage);

  const saveStorage = (event) => {
    event.preventDefault();
    if (storageShrinks) {
      alertUser(`storage can be increased but not decreased (currently ${currentStorage})`);
      return;
    }
    setSavingStorage(true);
    action(`/object-stores/${serverName}/update`, { project: "", environment: "", storageSize: storage })
      .then(() => {
        reload();
        alertUser(`Storage will be increased to ${storageText(storage)}.`);
      })
      .catch((error) => alertUser(error.message))
      .finally(() => setSavingStorage(false));
  };

  const detailItems = [
    { label: "Provider", value: provider },
    { label: "Status", value: userFacingStatus(ready, server) },
    {
      label: "CPU",
      value: sizeLabel(cpuSizes, req.cpu, currentCpu),
    },
    {
      label: "Memory",
      value: sizeLabel(memorySizes, req.memory, currentMemory),
    },
    { label: "Storage", value: storageText(currentStorage) || "Not set yet" },
    {
      label: "Project buckets",
      value: projectBuckets.length ? `${projectBuckets.length} in use` : "None yet",
    },
  ];

  return (
    <SettingsPanel className="rw-storage-manage">
      <ObjectStorageTabs view={view} onChange={setView} />
      <div className="rw-storage-tab-panel" role="tabpanel">
        {view === "details" && (
          <>
            {capacityBlocked && (
              <div className="rw-storage-capacity-alert">
                <SettingsAlert tone="warning">{statusMessage}</SettingsAlert>
              </div>
            )}
            {storageBlocked && (
              <div className="rw-storage-capacity-alert">
                <SettingsAlert tone="danger">
                  The server&apos;s storage is unavailable, so {provider} can&apos;t start. Check that your cluster&apos;s
                  nodes are healthy.
                </SettingsAlert>
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
              description={`Change CPU and memory for the ${provider} server. Geass applies the new size and restarts the server when needed.`}
            >
              <FormStack className="rw-storage-form" onSubmit={saveSize}>
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
            <PageSection
              plain
              title="Storage"
              description={
                resizable
                  ? `The disk that holds the ${provider} server's data. You can make it bigger, but not smaller.`
                  : "This cluster's storage can't be resized."
              }
            >
              <FormStack className="rw-storage-form" onSubmit={saveStorage}>
                <Field label="Size" description={currentStorage ? `Currently ${storageText(currentStorage)}.` : undefined}>
                  <Select
                    value={storage}
                    disabled={!resizable}
                    onChange={(event) => setStorage(event.target.value)}
                    aria-label="Storage size"
                  >
                    {storageOptionElements(storage, currentStorage)}
                  </Select>
                </Field>
                {storageProblem && <Muted size="sm">{storageProblem}</Muted>}
                {canMutate(data) && (
                  <SettingsFormActions>
                    <Button
                      type="submit"
                      loading={savingStorage}
                      disabled={!resizable || !storageChanged || Boolean(storageProblem) || !storage}
                    >
                      Save storage
                    </Button>
                  </SettingsFormActions>
                )}
              </FormStack>
            </PageSection>
            <ObjectStorageRemoveSection
              data={data}
              serverName={serverName}
              provider={provider}
              nextEngine={otherEngine(engine)}
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

function ObjectStorageSetupPanel({ engine, data, reload }) {
  const startsAt = objectStorageServer(data, engine) || {};
  const provider = engineLabel(data, engine);
  const nextEngine = otherEngine(engine);
  const capacity = data?.platform?.capacity || {};
  const [cpu, setCpu] = useState(startsAt.cpu || "");
  const [memory, setMemory] = useState(startsAt.memory || "");
  const [storage, setStorage] = useState(defaultServerStorage(data));
  const [creating, setCreating] = useState(false);
  // Unknown counts as resizable; this is about volumes made from now on.
  const expandable = data?.platform?.storageExpandable !== false;
  const storageProblem = storageSizeProblem(storage);
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

  const setUp = () => {
    setCreating(true);
    action("/object-stores/create", {
      cluster: "on",
      engine,
      placement: "InCluster",
      cpu,
      memory,
      ...(storage ? { storageSize: storage } : {}),
    })
      .then(() => {
        reload();
        alertUser(`${provider} server created`);
      })
      .catch((error) => alertUser(error.message))
      .finally(() => setCreating(false));
  };

  return (
    <SettingsPanel>
      <FormStack className="rw-storage-form">
        <p className="rw-rail-body">In-cluster buckets stay disabled until this server exists.</p>
        <p className="rw-rail-body">
          You choose once for the whole cluster. To switch to {nextEngine} later, delete every in-cluster bucket, remove{" "}
          {provider}, then set up {nextEngine}. Data is not copied from one to the other.
        </p>
        <SizeFields
          cpu={cpu}
          memory={memory}
          onChange={(key, value) => {
            if (key === "cpu") setCpu(value);
            else setMemory(value);
          }}
          data={data}
        />
        <Field
          label="Storage"
          description={
            expandable
              ? "The disk that holds the server's data. You can make it bigger later, but not smaller."
              : "This cluster's storage can't be resized later, so choose the size you need now."
          }
        >
          <Select value={storage} onChange={(event) => setStorage(event.target.value)} aria-label="Storage">
            {storageOptionElements(storage)}
          </Select>
        </Field>
        {storageProblem && <Muted size="sm">{storageProblem}</Muted>}
        {!fits && (
          <Muted size="sm">
            The cluster does not have enough capacity. <AppLink href="/cluster">Open cluster capacity</AppLink> before
            setting up {provider}.
          </Muted>
        )}
        <SettingsFormActions>
          <Button loading={creating} disabled={!canMutate(data) || !fits || Boolean(storageProblem)} onClick={setUp}>
            Set up {provider} server
          </Button>
        </SettingsFormActions>
      </FormStack>
    </SettingsPanel>
  );
}

/**
 * The cluster's object storage: its manage panel once a server exists, and the form to set one up, for the
 * engine named by `setupEngine`, until then.
 */
export function ObjectStoragePanel({ data, reload, setupEngine }) {
  const server = clusterObjectStoreServer(data);
  if (server) {
    return (
      <ObjectStorageManagePanel
        server={server}
        serverName={resourceName(server)}
        ready={condition(server) === "True"}
        data={data}
        reload={reload}
      />
    );
  }
  if (!setupEngine) {
    return null;
  }
  return <ObjectStorageSetupPanel key={setupEngine} engine={setupEngine} data={data} reload={reload} />;
}
