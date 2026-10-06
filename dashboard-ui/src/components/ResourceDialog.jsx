import { useEffect, useState } from "react";
import { Group, Stack, Text } from "@mantine/core";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ArrowLeft, ArrowRight } from "lucide-react";
import { action, list, resourceName } from "@/lib/api";
import { alertUser } from "@/lib/notify";
import { availableConnections } from "@/lib/project-resources";
import {
  capacityFits, defaultSizeForKind, liveEstimate, sizeLabel,
} from "@/lib/capacity";
import { AppLink } from "@/components/AppLink";
import { SizeFields } from "@/components/SizeFields";
import {
  Button, ChoiceCard, Field, Input, ResourceModal, Select, Switch,
} from "@/components/geass-ui";

export function ResourceDialog({ project, environment, data }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const platform = data?.platform || {};
  const currentProject = list(data, "projects").find((item) => resourceName(item) === project);
  const githubReady = Boolean(currentProject?.spec?.githubConnectionRef?.name) && platform.hasGitHubApp;
  const servers = list(data, "databases").filter((item) => item.spec?.project === project && item.spec?.environment === environment && (item.spec?.engine === "Postgres" || item.spec?.engine === "MySQL" || !item.spec?.engine));
  const awsConnections = availableConnections(data, "AWS", project);
  const planetConnections = availableConnections(data, "PlanetScale", project);
  const minioAvailable = Boolean(platform.minioAvailable);
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState(1);
  const [type, setType] = useState("service");
  const [kind, setKind] = useState("app");
  const [form, setForm] = useState({ name: "", image: "nginx:alpine", repository: "", branch: "main", engine: "Postgres", placement: "InCluster", provider: "", mode: "Create", host: "", username: "", password: "", databaseName: "", server: "", bucket: "", connectionRef: "", highAvailability: false, createBucket: true, cpu: "100m", memory: "128Mi", replicas: 1, autoscaling: false, maxReplicas: 3, targetCPU: 50 });
  const set = (key, value) => setForm((current) => ({ ...current, [key]: value }));
  const emptyForm = { name: "", image: "nginx:alpine", repository: "", branch: "main", engine: "Postgres", placement: "InCluster", provider: "", mode: "Create", host: "", username: "", password: "", databaseName: "", server: "", bucket: "", connectionRef: "", highAvailability: false, createBucket: true, cpu: "100m", memory: "128Mi", replicas: 1, autoscaling: false, maxReplicas: 3, targetCPU: 50 };
  useEffect(() => {
    const openDialog = () => { setStep(1); setType("service"); setKind("app"); setForm(emptyForm); setOpen(true); };
    window.addEventListener("geass:open-resource", openDialog);
    return () => window.removeEventListener("geass:open-resource", openDialog);
  }, []);
  const types = [
    { value: "service", label: "Service", description: "GitHub repository or Docker image" },
    { value: "database", label: "Database", description: "Postgres, MySQL, SQLite, Redis, PlanetScale, AWS" },
    { value: "bucket", label: "Bucket", description: "In-cluster or AWS S3" },
  ];
  const options = {
    service: [
      { value: "app", label: "Docker image", description: "Pull and run a container image", enabled: true },
      { value: "github", label: "GitHub repository", description: githubReady ? "Build from GitHub, then deploy when you are ready" : "Connect GitHub in project settings first", enabled: githubReady },
    ],
    database: [
      { value: "postgres", label: "PostgreSQL", description: "In-cluster Postgres", enabled: true, engine: "Postgres", placement: "InCluster" },
      { value: "mysql", label: "MySQL", description: "In-cluster MySQL", enabled: true, engine: "MySQL", placement: "InCluster" },
      { value: "sqlite", label: "SQLite", description: "In-cluster SQLite", enabled: true, engine: "SQLite", placement: "InCluster" },
      { value: "redis", label: "Redis", description: "In-cluster Redis", enabled: true, engine: "Redis", placement: "InCluster" },
      { value: "planetscale", label: "PlanetScale", description: planetConnections.length ? "Create or connect a PlanetScale database" : "Add a PlanetScale connection in cluster settings", enabled: planetConnections.length > 0, engine: "MySQL", placement: "External", provider: "PlanetScale" },
      { value: "awsdb", label: "AWS", description: awsConnections.length ? "Connect an AWS database" : "Add an AWS connection in cluster settings", enabled: awsConnections.length > 0, engine: "Postgres", placement: "External", provider: "AWS" },
      { value: "logical", label: "Logical database", description: servers.length ? "Create a database inside an existing server" : "Create a database server first", enabled: servers.length > 0 },
    ],
    bucket: [
      { value: "minio", label: "In-cluster bucket", description: minioAvailable ? "Create a bucket on the cluster MinIO server" : "Set up the MinIO server in cluster settings first", enabled: minioAvailable, placement: "InCluster" },
      { value: "s3", label: "AWS S3", description: awsConnections.length ? "Create or connect an S3 bucket" : "Add an AWS connection in cluster settings", enabled: awsConnections.length > 0, placement: "External" },
    ],
  };
  const submit = (event) => {
    event.preventDefault();
    const values = { project, environment, name: form.name };
    let endpoint = "/apps/create";
    if (type === "service" && kind === "app") {
      endpoint = "/apps/create";
      Object.assign(values, { image: form.image, port: "80", name: form.name, cpu: form.cpu, memory: form.memory, replicas: String(form.replicas || 1), autoscaling: form.autoscaling ? "on" : "", maxReplicas: String(form.maxReplicas || 3), targetCPU: String(form.targetCPU || 50) });
    } else if (type === "service" && kind === "github") {
      endpoint = "/apps/create";
      Object.assign(values, { source: "git", repository: form.repository, branch: form.branch || "main", name: form.name, cpu: form.cpu, memory: form.memory, replicas: String(form.replicas || 1), autoscaling: form.autoscaling ? "on" : "", maxReplicas: String(form.maxReplicas || 3), targetCPU: String(form.targetCPU || 50) });
    } else if (kind === "logical") {
      endpoint = "/logical-databases/create";
      Object.assign(values, { server: form.server || (servers[0] && resourceName(servers[0])), database: form.databaseName || form.name });
    } else if (type === "database") {
      endpoint = "/databases/create";
      const option = options.database.find((item) => item.value === kind);
      Object.assign(values, {
        engine: form.engine || option?.engine || "Postgres",
        placement: option?.placement || "InCluster",
        provider: option?.provider || "",
        mode: form.mode,
        host: form.host,
        username: form.username,
        password: form.password,
        databaseName: form.databaseName,
        connectionRef: form.connectionRef || (option?.provider === "PlanetScale" ? planetConnections[0] && resourceName(planetConnections[0]) : option?.provider === "AWS" ? awsConnections[0] && resourceName(awsConnections[0]) : ""),
        highAvailability: form.highAvailability ? "on" : "",
        cpu: form.cpu,
        memory: form.memory,
      });
    } else {
      endpoint = "/object-stores/create";
      Object.assign(values, {
        engine: kind === "s3" ? "S3" : "MinIO",
        placement: kind === "s3" ? "External" : "InCluster",
        bucket: form.bucket || form.name,
        createBucket: form.createBucket ? "on" : "",
        connectionRef: kind === "s3" ? (form.connectionRef || (awsConnections[0] && resourceName(awsConnections[0]))) : "",
      });
    }
    action(endpoint, values).then(() => {
      queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] });
      setOpen(false);
      if (type === "service" && values.name) {
        navigate({ to: `/projects/${project}/apps/${values.name}?environment=${environment}` });
        alertUser("Service created as a draft. Change settings or variables, then deploy.");
        return;
      }
      alertUser("Resource created");
    }).catch((error) => alertUser(error.message));
  };
  const haDisabled = !platform.haReady;
  const selectedKind = options[type]?.find((item) => item.value === kind);
  const estimate = liveEstimate(data, type, kind, form);
  const capacity = platform.capacity || {};
  const fits = capacityFits(capacity, estimate);
  return (
    <ResourceModal
      open={open}
      onOpenChange={(next) => { setOpen(next); if (next) setStep(1); }}
      title={step === 1 ? "Add a resource" : step === 2 ? "Choose a source" : type === "service" ? "Create a draft service" : "Configure resource"}
      description={type === "service" && step === 3 ? "Creating saves a skeleton. You can change settings and variables before you deploy." : `Resources are created in ${environment} and reconciled by the Geass controllers.`}
    >
        <Stack gap="md" component="form" onSubmit={step < 3 ? (event) => { event.preventDefault(); setStep(step + 1); } : submit}>
          {step === 1 && (
            <Stack gap="xs">
              {types.map((row) => (
                <ChoiceCard
                  key={row.value}
                  selected={type === row.value}
                  title={row.label}
                  description={row.description}
                  onClick={() => {
                    setType(row.value);
                    const nextKind = options[row.value].find((item) => item.enabled)?.value || options[row.value][0].value;
                    setKind(nextKind);
                    setForm((current) => ({ ...current, ...defaultSizeForKind(row.value, nextKind) }));
                  }}
                />
              ))}
            </Stack>
          )}
          {step === 2 && (
            <Stack gap="xs">
              {options[type].map((row) => (
                <ChoiceCard
                  key={row.value}
                  selected={kind === row.value}
                  disabled={!row.enabled}
                  title={row.label}
                  description={row.description}
                  onClick={() => {
                    if (!row.enabled) return;
                    setKind(row.value);
                    setForm((current) => ({
                      ...current,
                      ...defaultSizeForKind(type, row.value),
                      ...(row.engine ? { engine: row.engine } : {}),
                      ...(row.placement ? { placement: row.placement } : {}),
                      ...(row.provider ? { provider: row.provider } : {}),
                    }));
                  }}
                />
              ))}
            </Stack>
          )}
          {step === 3 && (
            <Stack gap="md">
              <Field label="Name"><Input required value={form.name} onChange={(event) => set("name", event.target.value)} placeholder="api" /></Field>
              {kind === "app" && <Field label="Container image"><Input required value={form.image} onChange={(event) => set("image", event.target.value)} /></Field>}
              {kind === "github" && <>
                <Field label="Repository"><Input required value={form.repository} onChange={(event) => set("repository", event.target.value)} placeholder="org/app" /></Field>
                <Field label="Branch"><Input value={form.branch} onChange={(event) => set("branch", event.target.value)} /></Field>
              </>}
              {type === "database" && kind !== "logical" && kind !== "planetscale" && kind !== "awsdb" && kind !== "sqlite" && (
                <Switch
                  label="High availability"
                  description={haDisabled ? "Requires 3 healthy cluster nodes" : undefined}
                  disabled={haDisabled}
                  checked={form.highAvailability}
                  onChange={(event) => set("highAvailability", event.currentTarget.checked)}
                />
              )}
              {(type === "service" || (type === "database" && kind !== "logical" && kind !== "planetscale" && kind !== "awsdb")) && (
                <>
                  <SizeFields cpu={form.cpu} memory={form.memory} onChange={set} data={data} />
                  {type === "service" && (
                    <>
                      <Field label="Replicas"><Input type="number" min="1" value={form.replicas} onChange={(event) => set("replicas", Number(event.target.value) || 1)} /></Field>
                      <Switch label="Autoscaling" checked={form.autoscaling} onChange={(event) => set("autoscaling", event.currentTarget.checked)} />
                      {form.autoscaling && <>
                        <Field label="Scale up to"><Input type="number" min={form.replicas || 1} value={form.maxReplicas} onChange={(event) => set("maxReplicas", Number(event.target.value) || 3)} /></Field>
                        <Field label="CPU target %"><Input type="number" min="1" max="100" value={form.targetCPU} onChange={(event) => set("targetCPU", Number(event.target.value) || 50)} /></Field>
                      </>}
                    </>
                  )}
                </>
              )}
              {kind === "planetscale" && <>
                <Field label="Mode"><Select value={form.mode} onChange={(event) => set("mode", event.target.value)}><option>Create</option><option>Connect</option></Select></Field>
                {form.mode === "Connect" && <>
                  <Field label="Host"><Input value={form.host} onChange={(event) => set("host", event.target.value)} /></Field>
                  <Field label="Username"><Input value={form.username} onChange={(event) => set("username", event.target.value)} /></Field>
                  <Field label="Password"><Input type="password" value={form.password} onChange={(event) => set("password", event.target.value)} /></Field>
                </>}
                <Field label="Database name"><Input value={form.databaseName} onChange={(event) => set("databaseName", event.target.value)} placeholder="app" /></Field>
                {planetConnections.length > 0 && <Field label="PlanetScale connection"><Select value={form.connectionRef || resourceName(planetConnections[0])} onChange={(event) => set("connectionRef", event.target.value)}>{planetConnections.map((item) => <option key={resourceName(item)}>{resourceName(item)}</option>)}</Select></Field>}
              </>}
              {kind === "awsdb" && <>
                <Field label="Engine"><Select value={form.engine} onChange={(event) => set("engine", event.target.value)}><option>Postgres</option><option>MySQL</option></Select></Field>
                <Field label="Host"><Input required value={form.host} onChange={(event) => set("host", event.target.value)} /></Field>
                <Field label="Username"><Input required value={form.username} onChange={(event) => set("username", event.target.value)} /></Field>
                <Field label="Password"><Input type="password" required value={form.password} onChange={(event) => set("password", event.target.value)} /></Field>
                <Field label="Database name"><Input value={form.databaseName} onChange={(event) => set("databaseName", event.target.value)} /></Field>
                {awsConnections.length > 0 && <Field label="AWS connection"><Select value={form.connectionRef || resourceName(awsConnections[0])} onChange={(event) => set("connectionRef", event.target.value)}>{awsConnections.map((item) => <option key={resourceName(item)}>{resourceName(item)}</option>)}</Select></Field>}
              </>}
              {kind === "logical" && <>
                <Field label="Server"><Select value={form.server || (servers[0] && resourceName(servers[0]))} onChange={(event) => set("server", event.target.value)}>{servers.map((item) => <option key={resourceName(item)}>{resourceName(item)}</option>)}</Select></Field>
                <Field label="Database name"><Input required value={form.databaseName} onChange={(event) => set("databaseName", event.target.value)} /></Field>
              </>}
              {kind === "minio" && <Field label="Bucket name"><Input value={form.bucket} onChange={(event) => set("bucket", event.target.value)} placeholder={form.name || "uploads"} /></Field>}
              {kind === "s3" && <>
                <Field label="Bucket name"><Input value={form.bucket} onChange={(event) => set("bucket", event.target.value)} placeholder={form.name || "uploads"} /></Field>
                {awsConnections.length > 0 && <Field label="AWS connection"><Select value={form.connectionRef || resourceName(awsConnections[0])} onChange={(event) => set("connectionRef", event.target.value)}>{awsConnections.map((item) => <option key={resourceName(item)}>{resourceName(item)}</option>)}</Select></Field>}
              </>}
              {estimate && (
                <Text size="sm" c={fits ? "dimmed" : "red"}>
                  {estimate.cpuMillis || estimate.memoryBytes
                    ? `Each copy gets ${sizeLabel(data?.platform?.capacity?.cpuSizes || [], form.cpu, form.cpu)} and ${sizeLabel(data?.platform?.capacity?.memorySizes || [], form.memory, form.memory)}${estimate.replicas > 1 ? ` · ${estimate.replicas} copies` : ""}. Cluster has ${capacity.known ? `${capacity.cpuAvailable} CPU and ${capacity.memoryAvailable} memory` : "unknown capacity"} available.`
                    : "This resource does not consume in-cluster CPU or memory."}
                  {type === "service"
                    ? !fits && <> Scale up the cluster before deploying. <AppLink href="/cluster">View cluster capacity</AppLink></>
                    : !fits && <> Scale up the cluster before creating it. <AppLink href="/cluster">View cluster capacity</AppLink></>}
                </Text>
              )}
            </Stack>
          )}
          <Group className="resource-dialog-footer" justify="flex-end" gap="sm">
            {step > 1 && <Button type="button" variant="outline" onClick={() => setStep(step - 1)} leftSection={<ArrowLeft size={15} />}>Back</Button>}
            <Button type="submit" disabled={(step === 2 && selectedKind && !selectedKind.enabled) || (step === 3 && type !== "service" && !fits)} rightSection={step < 3 ? <ArrowRight size={15} /> : null}>{step < 3 ? "Continue" : type === "service" ? "Create service" : "Create resource"}</Button>
          </Group>
        </Stack>
    </ResourceModal>
  );
}
