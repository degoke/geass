import { useMemo, useState } from "react";
import {
  AlertTriangle,
  Database,
  Gauge,
  GitBranch,
  HardDrive,
  Settings,
  ShieldCheck,
} from "lucide-react";
import { action, resourceDisplayName, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { availableConnections } from "@/lib/project-resources";
import { ceilingOf, databaseHasVolume, defaultSizeForKind, defaultStorageForEngine, storageSizeOptions } from "@/lib/capacity";
import { supportsBackups } from "@/lib/backups";
import { projectRegion, tagsInRegion } from "@/lib/placement";
import { RunOnField } from "@/components/RunOnField";
import { deployedStorageSize, settingsSavedMessage } from "@/lib/services";
import { SizeFields } from "@/components/SizeFields";
import { RailCopyField } from "@/components/rail-controls";
import { DatabaseBackups } from "@/components/DatabaseBackups";
import { ResourceDangerSection } from "@/components/ResourceDangerSection";
import {
  Button,
  Field,
  FormStack,
  Input,
  Muted,
  Select,
  Switch,
} from "@/components/geass-ui";
import {
  ResourceSettingsBlock,
  ResourceSettingsLayout,
  ResourceSettingsSection,
  resourceSectionVisible,
} from "@/layout/ResourceSettingsLayout";
import {
  SettingsFormActions,
  SettingsPanel,
} from "@/layout/SettingsShell";
function storageDescription(deployed, expandable, locked) {
  if (locked) return "This database's storage can't be resized, so its size is fixed.";
  if (!expandable && !deployed) {
    return "Disk for each instance. This cluster's storage can't be resized, so choose carefully: the size can't be changed after deploying.";
  }
  return deployed
    ? "Disk for each instance. Once deployed it can be increased but not decreased."
    : "Disk for each instance. You can increase it after deploying, but not decrease it.";
}

export function ResourceSettings({ item, kind, name, data, project, reload, navigate, onClose }) {
  const defaults = defaultSizeForKind(kind === "apps" ? "service" : "database", (item.spec?.engine || "postgres").toLowerCase());
  const [cpu, setCpu] = useState(item.spec?.resources?.requests?.cpu || defaults.cpu);
  const [memory, setMemory] = useState(item.spec?.resources?.requests?.memory || defaults.memory);
  // Only a service may use more than it reserves. A database always uses what it reserves.
  const [cpuMax, setCpuMax] = useState(kind === "apps" ? ceilingOf(item.spec?.resources, "cpu") : "");
  const [memoryMax, setMemoryMax] = useState(kind === "apps" ? ceilingOf(item.spec?.resources, "memory") : "");
  const [replicas, setReplicas] = useState(item.spec?.replicas || 1);
  const [autoscaling, setAutoscaling] = useState(Boolean(item.spec?.autoscaling?.maxReplicas > 1));
  const [maxReplicas, setMaxReplicas] = useState(item.spec?.autoscaling?.maxReplicas || 3);
  const [targetCPU, setTargetCPU] = useState(item.spec?.autoscaling?.targetCPUUtilization || 50);
  const [version, setVersion] = useState(item.spec?.version || "");
  const [runOn, setRunOn] = useState(item.spec?.runOn?.tags || []);
  const [storageSize, setStorageSize] = useState(item.spec?.storageSize || defaultStorageForEngine(item.spec?.engine, data?.platform?.defaultStorage));
  const deployedStorage = deployedStorageSize(item);
  // Unknown counts as resizable; the backend makes the same call and the cluster has the last word.
  const storageExpandable = data?.platform?.storageExpandable !== false;
  // Resizing depends on the class a database's own volume was created with, which can differ
  // between databases in one cluster (one made before Longhorn was installed, say).
  const storageLocked = Boolean(deployedStorage) && Boolean(data?.platform?.fixedSizeDatabases?.includes(name));
  const shownStorage = storageLocked ? deployedStorage : storageSize;
  const storageOptions = storageLocked
    ? storageSizeOptions(deployedStorage, deployedStorage).filter((option) => option.value === deployedStorage)
    : storageSizeOptions(storageSize, deployedStorage);
  const [bucketName, setBucketName] = useState(item.spec?.buckets?.[0] || "");
  const [createBucket, setCreateBucket] = useState(Boolean(item.spec?.createBucket));
  const [connectionRef, setConnectionRef] = useState(item.spec?.connectionRef?.name || "");
  const isApp = kind === "apps";
  const isGitSource = Boolean(item.spec?.source?.git);
  const [containerImage, setContainerImage] = useState(
    typeof item.spec?.source?.image === "string"
      ? item.spec.source.image
      : item.spec?.source?.image?.image || "",
  );
  const [repository, setRepository] = useState(item.spec?.source?.git?.repository || "");
  const [branch, setBranch] = useState(item.spec?.source?.git?.branch || "main");
  const displayLabel = resourceDisplayName(item);
  const isBucket = kind === "object-stores";
  const isLogical = kind === "logical-databases";
  const isExternalBucket = isBucket && (item.spec?.placement === "External" || item.spec?.engine === "S3");
  const awsConnections = availableConnections(data, "AWS", item.spec?.project);
  const inCluster = kind === "apps" || (kind === "databases" && item.spec?.placement !== "External");
  const showScale = (isApp || (inCluster && !isBucket && !isLogical));
  const showDatabase = kind === "databases";
  const showBackups = kind === "databases" && supportsBackups(item);
  const showStorage = showDatabase && inCluster && databaseHasVolume(item.spec?.engine);
  const showBucket = isBucket;
  const showSource = isApp;
  const showLogical = isLogical;

  const sections = useMemo(() => {
    const items = [
      {
        id: "general",
        label: "General",
        icon: Settings,
        keywords: ["general", "project", "environment", "resource", name, displayLabel],
      },
    ];
    if (showBucket) {
      items.push({
        id: "bucket",
        label: "Bucket",
        icon: HardDrive,
        keywords: ["bucket", "aws", "connection", bucketName, item.spec?.engine],
      });
    }
    if (showSource) {
      items.push({
        id: "source",
        label: "Source",
        icon: GitBranch,
        keywords: ["source", "image", "github", "repository", "branch", containerImage, repository],
      });
    }
    if (showDatabase) {
      items.push({
        id: "database",
        label: "Database",
        icon: Database,
        keywords: ["database", "version", item.spec?.engine],
      });
    }
    if (showBackups) {
      items.push({
        id: "backups",
        label: "Backups",
        icon: ShieldCheck,
        keywords: ["backups", "backup", "restore", "recovery", "restore point", "retention", "protect", "storage"],
      });
    }
    if (showLogical) {
      items.push({
        id: "logical",
        label: "Logical DB",
        icon: Database,
        keywords: ["logical", "server", item.spec?.serverRef, item.spec?.databaseName],
      });
    }
    if (showScale) {
      items.push({
        id: "scale",
        label: "Scale",
        icon: Gauge,
        keywords: ["scale", "cpu", "memory", "replicas", "autoscaling"],
      });
    }
    items.push({
      id: "danger",
      label: "Danger",
      icon: AlertTriangle,
      danger: true,
      keywords: ["danger", "delete", "remove"],
    });
    return items;
  }, [showBucket, showDatabase, showBackups, showSource, showLogical, showScale, name, displayLabel, bucketName, containerImage, repository, item.spec?.engine, item.spec?.serverRef, item.spec?.databaseName]);

  if (!canMutate(data)) {
    return (
      <div className="rw-resource-settings-shell rw-rail-settings">
        <Muted size="sm">Your viewer role can inspect this resource but cannot change it.</Muted>
      </div>
    );
  }

  const save = (event) => {
    event.preventDefault();
    const values = { project: item.spec?.project, environment: item.spec?.environment };
    if (kind === "databases") values.version = version;
    if (kind === "apps") {
      Object.assign(values, {
        cpu,
        memory,
        cpuMax,
        memoryMax,
        replicas: String(replicas),
        autoscaling: autoscaling ? "on" : "",
        maxReplicas: String(maxReplicas),
        targetCPU: String(targetCPU),
      });
      if (isGitSource) {
        Object.assign(values, { repository, branch });
      } else {
        Object.assign(values, { image: containerImage });
      }
    }
    if (kind === "databases" && inCluster) Object.assign(values, { cpu, memory });
    // A database keeps its data on the nodes it was first deployed to, so the choice closes then.
    if (kind === "databases" && inCluster && !item.spec?.deploy?.enabled && (runOn.length || tagsInRegion(data, projectRegion(project, data)).length)) {
      values.runOnTags = runOn.join(",");
    }
    if (kind === "databases" && showStorage && !storageLocked) values.storageSize = storageSize;
    if (isBucket) {
      Object.assign(values, {
        bucket: bucketName || name,
        createBucket: createBucket ? "on" : "",
      });
      if (isExternalBucket && connectionRef) values.connectionRef = connectionRef;
    }
    action(`/${kind}/${name}/update`, values)
      .then((result) => {
        reload();
        alertUser(settingsSavedMessage(result));
      })
      .catch((error) => alertUser(error.message));
  };

  const copyResourceId = () => {
    navigator.clipboard?.writeText(name).then(
      () => alertUser("Copied to clipboard"),
      () => alertUser("Could not copy"),
    );
  };

  return (
    <ResourceSettingsLayout sections={sections}>
      {(visibleSections, filter) => {
        const show = (id, extra = []) => {
          const def = sections.find((entry) => entry.id === id);
          return visibleSections.some((entry) => entry.id === id)
            && resourceSectionVisible(filter, [...(def?.keywords || []), ...extra]);
        };

        return (
          <>
            {show("general") && (
              <ResourceSettingsSection id="general" icon={Settings} title="General">
                <ResourceSettingsBlock
                  title="Resource"
                  description="Where this resource lives in your project. IDs are used in APIs and delete confirmation."
                >
                  <SettingsPanel>
                    <div className="rw-settings-usage-lines">
                      <div className="rw-settings-usage-line">
                        <span>Project</span>
                        <strong>{item.spec?.project || "—"}</strong>
                      </div>
                      <div className="rw-settings-usage-line">
                        <span>Environment</span>
                        <strong>{item.spec?.environment || "—"}</strong>
                      </div>
                      {isBucket ? (
                        <div className="rw-settings-usage-line">
                          <span>Engine</span>
                          <strong>{item.spec?.engine || "—"}</strong>
                        </div>
                      ) : null}
                    </div>
                  </SettingsPanel>
                </ResourceSettingsBlock>
                <ResourceSettingsBlock title="Resource ID">
                  <RailCopyField label="ID" value={name} onCopy={copyResourceId} aria-label="Resource ID" />
                </ResourceSettingsBlock>
              </ResourceSettingsSection>
            )}

            {show("source") && (
              <ResourceSettingsSection id="source" icon={GitBranch} title="Source">
                <FormStack className="rw-rail-form" onSubmit={save}>
                  <ResourceSettingsBlock
                    title={isGitSource ? "GitHub repository" : "Container image"}
                    description="Set how this service is built and run. Save here, then deploy from the Deployments tab."
                  >
                    <SettingsPanel>
                      {isGitSource ? (
                        <>
                          <Field label="Repository">
                            <Input
                              required
                              value={repository}
                              onChange={(event) => setRepository(event.target.value)}
                              placeholder="org/app"
                            />
                          </Field>
                          <Field label="Branch">
                            <Input value={branch} onChange={(event) => setBranch(event.target.value)} placeholder="main" />
                          </Field>
                        </>
                      ) : (
                        <Field label="Image">
                          <Input
                            required
                            value={containerImage}
                            onChange={(event) => setContainerImage(event.target.value)}
                            placeholder="nginx:alpine"
                          />
                        </Field>
                      )}
                    </SettingsPanel>
                  </ResourceSettingsBlock>
                  <SettingsFormActions>
                    <Button type="submit">Save changes</Button>
                  </SettingsFormActions>
                </FormStack>
              </ResourceSettingsSection>
            )}

            {show("logical") && (
              <ResourceSettingsSection id="logical" icon={Database} title="Logical database">
                <ResourceSettingsBlock
                  title="Placement"
                  description="This database is created inside an existing database server."
                >
                  <SettingsPanel>
                    <div className="rw-settings-usage-lines">
                      <div className="rw-settings-usage-line">
                        <span>Server</span>
                        <strong>{item.spec?.serverRef || "—"}</strong>
                      </div>
                      <div className="rw-settings-usage-line">
                        <span>Database name</span>
                        <strong>{item.spec?.databaseName || "—"}</strong>
                      </div>
                    </div>
                  </SettingsPanel>
                </ResourceSettingsBlock>
              </ResourceSettingsSection>
            )}

            {show("bucket") && (
              <ResourceSettingsSection id="bucket" icon={HardDrive} title="Bucket">
                <FormStack className="rw-rail-form" onSubmit={save}>
                  <ResourceSettingsBlock
                    title="Bucket name"
                    description="The object store name used when this bucket is deployed."
                  >
                    <SettingsPanel>
                      <Field label="Name">
                        <Input
                          required
                          value={bucketName}
                          onChange={(event) => setBucketName(event.target.value)}
                          placeholder={name}
                        />
                      </Field>
                    </SettingsPanel>
                  </ResourceSettingsBlock>
                  {isExternalBucket && awsConnections.length > 0 && (
                    <ResourceSettingsBlock
                      title="AWS connection"
                      description="Credentials used to manage this bucket in your cloud account."
                    >
                      <SettingsPanel>
                        <Field label="Connection">
                          <Select
                            value={connectionRef || resourceName(awsConnections[0])}
                            onChange={(event) => setConnectionRef(event.target.value)}
                          >
                            {awsConnections.map((entry) => (
                              <option key={resourceName(entry)} value={resourceName(entry)}>
                                {resourceName(entry)}
                              </option>
                            ))}
                          </Select>
                        </Field>
                      </SettingsPanel>
                    </ResourceSettingsBlock>
                  )}
                  {isExternalBucket && (
                    <ResourceSettingsBlock
                      title="Create bucket"
                      description="When enabled, Geass creates the S3 bucket in the connected account on deploy."
                    >
                      <SettingsPanel className="rw-resource-settings-toggle-row">
                        <Switch
                          label="Create bucket in AWS"
                          checked={createBucket}
                          onChange={(event) => setCreateBucket(event.currentTarget.checked)}
                        />
                      </SettingsPanel>
                    </ResourceSettingsBlock>
                  )}
                  <SettingsFormActions>
                    <Button type="submit">Save changes</Button>
                  </SettingsFormActions>
                </FormStack>
              </ResourceSettingsSection>
            )}

            {show("database") && (
              <ResourceSettingsSection id="database" icon={Database} title="Database">
                <FormStack className="rw-rail-form" onSubmit={save}>
                  <ResourceSettingsBlock
                    title="Version"
                    description="Database engine version applied on the next deploy."
                  >
                    <SettingsPanel>
                      <Field label="Version">
                        <Input value={version} onChange={(event) => setVersion(event.target.value)} />
                      </Field>
                    </SettingsPanel>
                  </ResourceSettingsBlock>
                  {inCluster && (
                    <ResourceSettingsBlock title="Resources" description="CPU and memory for the database workload.">
                      <SettingsPanel>
                        <SizeFields
                          cpu={cpu}
                          memory={memory}
                          onChange={(key, value) => {
                            if (key === "cpu") setCpu(value);
                            else setMemory(value);
                          }}
                          data={data}
                        />
                      </SettingsPanel>
                    </ResourceSettingsBlock>
                  )}
                  {showStorage && (
                    <ResourceSettingsBlock
                      title="Storage"
                      description={storageDescription(deployedStorage, storageExpandable, storageLocked)}
                    >
                      <SettingsPanel>
                        <Field label="Size">
                          <Select
                            value={shownStorage}
                            disabled={storageLocked}
                            onChange={(event) => setStorageSize(event.target.value)}
                          >
                            {storageOptions.map((option) => (
                              <option key={option.value} value={option.value}>{option.label}</option>
                            ))}
                          </Select>
                        </Field>
                      </SettingsPanel>
                    </ResourceSettingsBlock>
                  )}
                  <SettingsFormActions>
                    <Button type="submit">Save changes</Button>
                  </SettingsFormActions>
                </FormStack>
              </ResourceSettingsSection>
            )}

            {show("backups") && (
              <ResourceSettingsSection id="backups" icon={ShieldCheck} title="Backups">
                <DatabaseBackups item={item} name={name} data={data} reload={reload} />
              </ResourceSettingsSection>
            )}

            {show("scale") && (
              <ResourceSettingsSection id="scale" icon={Gauge} title="Scale">
                <FormStack className="rw-rail-form" onSubmit={save}>
                  <ResourceSettingsBlock
                    title="Resources"
                    description="Sizing for this workload. Changes apply after you deploy."
                  >
                    <SettingsPanel>
                      <SizeFields
                        bursts={kind === "apps"}
                        cpu={cpu}
                        memory={memory}
                        cpuMax={cpuMax}
                        memoryMax={memoryMax}
                        onChange={(key, value) => {
                          if (key === "cpu") setCpu(value);
                          else if (key === "memory") setMemory(value);
                          else if (key === "cpuMax") setCpuMax(value);
                          else if (key === "memoryMax") setMemoryMax(value);
                        }}
                        data={data}
                      />
                    </SettingsPanel>
                  </ResourceSettingsBlock>
                  {kind === "databases" && inCluster && (
                    <RunOnField
                      data={data}
                      region={projectRegion(project, data)}
                      value={runOn}
                      onChange={setRunOn}
                      disabled={Boolean(item.spec?.deploy?.enabled)}
                      note={item.spec?.deploy?.enabled
                        ? "A database keeps its data on the nodes it was first deployed to, so this can no longer change."
                        : "Chosen before the first deploy. After that the database stays on its nodes."}
                    />
                  )}
                  {kind === "apps" && (
                    <>
                      <ResourceSettingsBlock title="Replicas" description="How many instances run for this service.">
                        <SettingsPanel>
                          <Field label="Replicas">
                            <Input
                              type="number"
                              min="1"
                              value={replicas}
                              onChange={(event) => setReplicas(Number(event.target.value) || 1)}
                            />
                          </Field>
                        </SettingsPanel>
                      </ResourceSettingsBlock>
                      <ResourceSettingsBlock
                        title="Autoscaling"
                        description="Scale out when average CPU crosses your target."
                      >
                        <SettingsPanel className="rw-resource-settings-toggle-row">
                          <Switch
                            label="Enable autoscaling"
                            checked={autoscaling}
                            onChange={(event) => setAutoscaling(event.currentTarget.checked)}
                          />
                        </SettingsPanel>
                        {autoscaling ? (
                          <SettingsPanel className="rw-resource-settings-panel-spaced">
                            <Field label="Scale up to">
                              <Input
                                type="number"
                                min={replicas || 1}
                                value={maxReplicas}
                                onChange={(event) => setMaxReplicas(Number(event.target.value) || 3)}
                              />
                            </Field>
                            <Field label="CPU target %">
                              <Input
                                type="number"
                                min="1"
                                max="100"
                                value={targetCPU}
                                onChange={(event) => setTargetCPU(Number(event.target.value) || 50)}
                              />
                            </Field>
                          </SettingsPanel>
                        ) : null}
                      </ResourceSettingsBlock>
                    </>
                  )}
                  <SettingsFormActions>
                    <Button type="submit">Save changes</Button>
                  </SettingsFormActions>
                </FormStack>
              </ResourceSettingsSection>
            )}

            {show("danger") && (
              <ResourceDangerSection
                item={item}
                kind={kind}
                name={name}
                project={project}
                reload={reload}
                navigate={navigate}
                onClose={onClose}
              />
            )}
          </>
        );
      }}
    </ResourceSettingsLayout>
  );
}
