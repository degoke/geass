import { useMemo, useState } from "react";
import { AlertTriangle, ExternalLink, Gauge, GitBranch, Globe, Hammer, Rocket, Settings } from "lucide-react";
import { action, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser, alertError } from "@/lib/notify";
import { outOfMemoryNotice, settingsSavedMessage } from "@/lib/services";
import { projectRegion, tagsInRegion } from "@/lib/placement";
import { DeployHook } from "@/components/DeployHook";
import { MemoryReserveHint } from "@/components/MemoryReserveHint";
import { RunOnField } from "@/components/RunOnField";
import { ceilingOf, defaultSizeForKind } from "@/lib/capacity";
import { FUNCTION_RUNTIMES } from "@/lib/functions";
import { SizeFields } from "@/components/SizeFields";
import { RailCopyField } from "@/components/rail-controls";
import { ResourceDangerSection } from "@/components/ResourceDangerSection";
import { Button, Field, FormStack, Input, Muted, Select, Switch, Textarea } from "@/components/geass-ui";
import {
  ResourceSettingsBlock,
  ResourceSettingsLayout,
  ResourceSettingsSection,
  resourceSectionVisible,
} from "@/layout/ResourceSettingsLayout";
import { SettingsAlert, SettingsFormActions, SettingsPanel } from "@/layout/SettingsShell";

function probePath(probe) {
  return probe?.httpGet?.path || "";
}

function copyText(value) {
  navigator.clipboard?.writeText(value).then(
    () => alertUser("Copied to clipboard"),
    () => alertUser("Could not copy"),
  );
}

function ToggleRow({ label, description, checked, onChange, disabled }) {
  return (
    <SettingsPanel className="rw-resource-settings-toggle-row">
      <Switch label={label} description={description} checked={checked} disabled={disabled} onChange={(event) => onChange(event.currentTarget.checked)} />
    </SettingsPanel>
  );
}

export function ServiceSettings({ item, name, data, project, reload, navigate, onClose }) {
  const spec = item.spec || {};
  const git = spec.source?.git;
  const fn = spec.source?.function;
  const defaults = defaultSizeForKind("service", "service");
  const [repository, setRepository] = useState(git?.repository || "");
  const [branch, setBranch] = useState(git?.branch || "main");
  const [context, setContext] = useState(git?.context || "");
  const [waitForCI, setWaitForCI] = useState(Boolean(git?.waitForCI));
  const [functionRuntime, setFunctionRuntime] = useState(fn?.runtime || "bun");
  const [image, setImage] = useState(
    typeof spec.source?.image === "string" ? spec.source.image : spec.source?.image?.image || "",
  );
  const [host, setHost] = useState(spec.ingress?.host || "");
  const [tls, setTls] = useState(Boolean(spec.ingress?.tlsEnabled));
  const [dnsVerification, setDnsVerification] = useState(Boolean(spec.ingress?.dnsVerification));
  const [port, setPort] = useState(spec.port ? String(spec.port) : "");
  const [cpu, setCpu] = useState(spec.resources?.requests?.cpu || defaults.cpu);
  const [memory, setMemory] = useState(spec.resources?.requests?.memory || defaults.memory);
  const [cpuMax, setCpuMax] = useState(ceilingOf(spec.resources, "cpu"));
  const [memoryMax, setMemoryMax] = useState(ceilingOf(spec.resources, "memory"));
  const [replicas, setReplicas] = useState(spec.replicas || 1);
  const [autoscaling, setAutoscaling] = useState(Boolean(spec.autoscaling?.maxReplicas > 1));
  const [maxReplicas, setMaxReplicas] = useState(spec.autoscaling?.maxReplicas || 3);
  const [targetCPU, setTargetCPU] = useState(spec.autoscaling?.targetCPUUtilization || 50);
  const [sleeps, setSleeps] = useState(Boolean(spec.scaleToZero?.enabled));
  const [idleMinutes, setIdleMinutes] = useState(Math.round((spec.scaleToZero?.idleSeconds || 300) / 60) || 5);
  const sleepCondition = (item?.status?.conditions || []).find((entry) => entry.type === "ScaleToZero");
  const [dockerfile, setDockerfile] = useState(git?.dockerfile || "");
  const [watchPatterns, setWatchPatterns] = useState((git?.watchPatterns || []).join("\n"));
  const [buildCache, setBuildCache] = useState(Boolean(spec.build?.cache));
  const [command, setCommand] = useState((spec.build?.command || []).join(" "));
  const [args, setArgs] = useState((spec.build?.args || []).join(" "));
  const [workingDir, setWorkingDir] = useState(spec.build?.workingDir || "");
  const [healthPath, setHealthPath] = useState(probePath(spec.deploy?.readinessProbe));
  const [metrics, setMetrics] = useState(Boolean(spec.metrics?.enabled));
  const [runOn, setRunOn] = useState(spec.runOn?.tags || []);
  const [saving, setSaving] = useState("");
  const region = projectRegion(project, data);
  // Only send the choice when there is one to make, so a cluster without node tags never touches it.
  const runOnShown = tagsInRegion(data, region).length > 0 || runOn.length > 0;

  const sections = useMemo(() => [
      { id: "source", label: "Source", icon: GitBranch, keywords: ["source", "image", "github", "repository", "branch", "root directory", "wait for ci", "auto deploy"] },
      { id: "networking", label: "Networking", icon: Globe, keywords: ["networking", "domain", "host", "tls", "https", "port", "private", "dns", "public"] },
      { id: "scale", label: "Scale", icon: Gauge, keywords: ["scale", "cpu", "memory", "replicas", "autoscaling", "limits"] },
      { id: "build", label: "Build", icon: Hammer, keywords: ["build", "dockerfile", "watch paths", "cache"] },
      { id: "deploy", label: "Deploy", icon: Rocket, keywords: ["deploy", "start command", "healthcheck", "restart", "metrics", "args", "working directory", "hook", "webhook", "url"] },
      { id: "general", label: "General", icon: Settings, keywords: ["general", "project", "environment", "id", name] },
      { id: "danger", label: "Danger", icon: AlertTriangle, danger: true, keywords: ["danger", "delete", "remove"] },
  ], [name]);

  if (!canMutate(data)) {
    return (
      <div className="rw-resource-settings-shell rw-rail-settings">
        <Muted size="sm">Your viewer role cannot change this service.</Muted>
      </div>
    );
  }

  const save = (section, values) => (event) => {
    event?.preventDefault();
    setSaving(section);
    action(`/apps/${name}/update`, values)
      .then((result) => { reload?.(); alertUser(settingsSavedMessage(result)); })
      .catch((error) => alertError(error.message))
      .finally(() => setSaving(""));
  };

  const publicUrl = item.status?.url || "";
  const namespace = item.status?.targetNamespace || "";
  const servicePort = spec.port || 8080;
  const privateHost = namespace ? `${name}.${namespace}.svc.cluster.local` : "";

  return (
    <ResourceSettingsLayout sections={sections}>
      {(visibleSections, filter) => {
        const show = (id, extra = []) => {
          const def = sections.find((entry) => entry.id === id);
          return visibleSections.some((entry) => entry.id === id)
            && resourceSectionVisible(filter, [...(def?.keywords || []), ...extra]);
        };
        const saveButton = (section) => (
          <SettingsFormActions>
            <Button type="submit" loading={saving === section}>Save changes</Button>
          </SettingsFormActions>
        );

        return (
          <>
            {show("source") && (
              <ResourceSettingsSection id="source" icon={GitBranch} title="Source">
                <FormStack
                  className="rw-rail-form"
                  onSubmit={save("source", fn
                    ? { functionRuntime }
                    : git
                      ? { repository, branch, context, waitForCI: waitForCI ? "on" : "" }
                      : { image })}
                >
                  {fn ? (
                    <ResourceSettingsBlock title="Function runtime">
                      <SettingsPanel>
                        <Field label="Runtime">
                          <Select value={functionRuntime} onChange={(event) => setFunctionRuntime(event.currentTarget.value)} aria-label="Function runtime">
                            {FUNCTION_RUNTIMES.map((option) => <option key={option.id} value={option.id}>{option.label}</option>)}
                          </Select>
                        </Field>
                      </SettingsPanel>
                    </ResourceSettingsBlock>
                  ) : git ? (
                    <>
                      <ResourceSettingsBlock title="Source repo">
                        <SettingsPanel>
                          <Field label="Repository">
                            <Input required value={repository} onChange={(event) => setRepository(event.currentTarget.value)} placeholder="org/app" />
                          </Field>
                          <Field label="Root directory" description="Leave empty for the repository root.">
                            <Input value={context} onChange={(event) => setContext(event.currentTarget.value)} placeholder="/apps/api" />
                          </Field>
                        </SettingsPanel>
                      </ResourceSettingsBlock>
                      <ResourceSettingsBlock
                        title="Branch connected to production"
                        description="Pushes to it deploy automatically."
                      >
                        <SettingsPanel>
                          <Field label="Branch">
                            <Input value={branch} onChange={(event) => setBranch(event.currentTarget.value)} placeholder="main" />
                          </Field>
                        </SettingsPanel>
                      </ResourceSettingsBlock>
                      <ResourceSettingsBlock title="Wait for CI" description="Deploy only after GitHub Actions checks pass.">
                        <ToggleRow label="Wait for CI" checked={waitForCI} onChange={setWaitForCI} />
                      </ResourceSettingsBlock>
                    </>
                  ) : (
                    <ResourceSettingsBlock title="Container image">
                      <SettingsPanel>
                        <Field label="Image">
                          <Input required value={image} onChange={(event) => setImage(event.currentTarget.value)} placeholder="nginx:alpine" />
                        </Field>
                      </SettingsPanel>
                    </ResourceSettingsBlock>
                  )}
                  {saveButton("source")}
                </FormStack>
              </ResourceSettingsSection>
            )}

            {show("networking") && (
              <ResourceSettingsSection id="networking" icon={Globe} title="Networking">
                <FormStack
                  className="rw-rail-form"
                  onSubmit={save("networking", {
                    host: host.trim(),
                    tls: tls ? "on" : "",
                    dnsVerification: dnsVerification ? "on" : "",
                    port: port.trim(),
                  })}
                >
                  <ResourceSettingsBlock title="Public networking">
                    {publicUrl ? (
                      <SettingsPanel className="rw-net-url">
                        <a href={publicUrl} target="_blank" rel="noreferrer" className="rw-net-url-link">
                          {publicUrl.replace(/^https?:\/\//, "")} <ExternalLink size={13} aria-hidden />
                        </a>
                        <Muted size="xs">
                          {item.status?.dnsVerified === false
                            ? (item.status?.dnsMessage || "Waiting for DNS")
                            : item.status?.traefikTarget ? `Routed via ${item.status.traefikTarget}` : "Routed through the cluster ingress"}
                        </Muted>
                      </SettingsPanel>
                    ) : (
                      <Muted size="sm">No public domain yet.</Muted>
                    )}
                    <SettingsPanel className="rw-resource-settings-panel-spaced">
                      <Field label="Custom domain" description="Point a DNS record at your cluster first.">
                        <Input value={host} onChange={(event) => setHost(event.currentTarget.value)} placeholder="api.example.com" />
                      </Field>
                      <Field label="Port" description="Defaults to 8080.">
                        <Input type="number" min="1" max="65535" value={port} onChange={(event) => setPort(event.currentTarget.value)} placeholder="8080" />
                      </Field>
                    </SettingsPanel>
                    <ToggleRow label="HTTPS" description="Applies instantly." checked={tls} onChange={setTls} />
                    <ToggleRow label="Verify DNS" description="Wait for the hostname to resolve before reporting it ready. Applies instantly." checked={dnsVerification} onChange={setDnsVerification} />
                  </ResourceSettingsBlock>
                  {saveButton("networking")}
                </FormStack>
                <ResourceSettingsBlock title="Private networking">
                  {privateHost ? (
                    <RailCopyField label="Address" value={`${privateHost}:${servicePort}`} onCopy={() => copyText(`${privateHost}:${servicePort}`)} aria-label="Private address" />
                  ) : (
                    <Muted size="sm">Available after the first deploy.</Muted>
                  )}
                </ResourceSettingsBlock>
              </ResourceSettingsSection>
            )}

            {show("scale") && (
              <ResourceSettingsSection id="scale" icon={Gauge} title="Scale">
                <FormStack
                  className="rw-rail-form"
                  onSubmit={save("scale", {
                    cpu,
                    memory,
                    // Sent empty when the service reserves all it may use, which is how the server is told to drop a ceiling.
                    cpuMax,
                    memoryMax,
                    replicas: String(replicas),
                    autoscaling: autoscaling ? "on" : "",
                    // The server turns autoscaling on for any maximum above one that comes with a target, so an
                    // off switch has to say there is no maximum.
                    maxReplicas: autoscaling ? String(maxReplicas) : "1",
                    targetCPU: String(targetCPU),
                    scaleToZero: sleeps ? "on" : "off",
                    ...(sleeps ? { idleSeconds: String(Math.max(1, idleMinutes) * 60) } : {}),
                    ...(runOnShown ? { runOnTags: runOn.join(",") } : {}),
                  })}
                >
                  <ResourceSettingsBlock title="Replicas" live>
                    <SettingsPanel>
                      <Field label="Replicas">
                        <Input type="number" min="1" value={replicas} onChange={(event) => setReplicas(Number(event.currentTarget.value) || 1)} />
                      </Field>
                    </SettingsPanel>
                  </ResourceSettingsBlock>
                  <ResourceSettingsBlock title="Resources">
                    {outOfMemoryNotice(item) ? <SettingsAlert tone="warning">{outOfMemoryNotice(item)}</SettingsAlert> : null}
                    <SettingsPanel>
                      <SizeFields
                        bursts
                        cpu={cpu}
                        memory={memory}
                        cpuMax={cpuMax}
                        memoryMax={memoryMax}
                        onChange={(key, value) => {
                          if (key === "cpu") setCpu(value);
                          else if (key === "memory") setMemory(value);
                          else if (key === "cpuMax") setCpuMax(value);
                          else setMemoryMax(value);
                        }}
                        data={data}
                      />
                      <MemoryReserveHint app={name} memory={memory} data={data} />
                    </SettingsPanel>
                  </ResourceSettingsBlock>
                  <RunOnField data={data} region={region} value={runOn} onChange={setRunOn} note="Applies on deploy." />
                  <ResourceSettingsBlock title="Autoscaling" live>
                    <ToggleRow
                      label="Enable autoscaling"
                      checked={autoscaling}
                      onChange={(value) => {
                        setAutoscaling(value);
                        if (value) setSleeps(false);
                      }}
                    />
                    {autoscaling ? (
                      <SettingsPanel className="rw-resource-settings-panel-spaced">
                        <Field label="Scale up to">
                          <Input type="number" min={replicas || 1} value={maxReplicas} onChange={(event) => setMaxReplicas(Number(event.currentTarget.value) || 3)} />
                        </Field>
                        <Field label="CPU target %">
                          <Input type="number" min="1" max="100" value={targetCPU} onChange={(event) => setTargetCPU(Number(event.currentTarget.value) || 50)} />
                        </Field>
                      </SettingsPanel>
                    ) : null}
                  </ResourceSettingsBlock>
                  <ResourceSettingsBlock
                    title="Sleep when idle"
                    description="Needs a public domain and the serverless add-on. The next request waits a few seconds while it wakes."
                    live
                  >
                    <ToggleRow
                      label="Sleep when idle"
                      checked={sleeps}
                      onChange={(value) => {
                        setSleeps(value);
                        if (value) setAutoscaling(false);
                      }}
                    />
                    {sleeps ? (
                      <SettingsPanel className="rw-resource-settings-panel-spaced">
                        <Field label="Sleep after (minutes)">
                          <Input type="number" min="1" max="1440" value={idleMinutes} onChange={(event) => setIdleMinutes(Number(event.currentTarget.value) || 5)} />
                        </Field>
                      </SettingsPanel>
                    ) : null}
                    {sleeps && sleepCondition && sleepCondition.status !== "True" ? <Muted size="sm">{sleepCondition.message}</Muted> : null}
                  </ResourceSettingsBlock>
                  {saveButton("scale")}
                </FormStack>
              </ResourceSettingsSection>
            )}

            {show("build") && (
              <ResourceSettingsSection id="build" icon={Hammer} title="Build">
                {git ? (
                  <FormStack
                    className="rw-rail-form"
                    onSubmit={save("build", { dockerfile, watchPatterns, buildCache: buildCache ? "on" : "" })}
                  >
                    <ResourceSettingsBlock title="Builder">
                      <SettingsPanel>
                        <Field label="Dockerfile path" description="Defaults to Dockerfile at the root directory.">
                          <Input value={dockerfile} onChange={(event) => setDockerfile(event.currentTarget.value)} placeholder="/apps/api/Dockerfile" />
                        </Field>
                      </SettingsPanel>
                    </ResourceSettingsBlock>
                    <ResourceSettingsBlock title="Watch paths" description="Gitignore-style patterns, one per line." live>
                      <SettingsPanel>
                        <Textarea rows={4} value={watchPatterns} onChange={(event) => setWatchPatterns(event.currentTarget.value)} placeholder={"/src/**\n!/docs/**"} spellCheck={false} aria-label="Watch paths" />
                      </SettingsPanel>
                    </ResourceSettingsBlock>
                    <ResourceSettingsBlock title="Build cache">
                      <ToggleRow label="Use build cache" checked={buildCache} onChange={setBuildCache} />
                    </ResourceSettingsBlock>
                    {saveButton("build")}
                  </FormStack>
                ) : (
                  <ResourceSettingsBlock title="Builder">
                    <Muted size="sm">Nothing to build.</Muted>
                  </ResourceSettingsBlock>
                )}
              </ResourceSettingsSection>
            )}

            {show("deploy") && (
              <ResourceSettingsSection id="deploy" icon={Rocket} title="Deploy">
                <FormStack
                  className="rw-rail-form"
                  onSubmit={save("deploy", {
                    command,
                    args,
                    workingDir,
                    readinessPath: healthPath.trim(),
                    metrics: metrics ? "on" : "",
                  })}
                >
                  <ResourceSettingsBlock title="Custom start command" description="Leave empty to use the image default.">
                    <SettingsPanel>
                      <Field label="Command">
                        <Input value={command} onChange={(event) => setCommand(event.currentTarget.value)} placeholder="node server.js" />
                      </Field>
                      <Field label="Arguments">
                        <Input value={args} onChange={(event) => setArgs(event.currentTarget.value)} placeholder="--port 8080" />
                      </Field>
                      <Field label="Working directory">
                        <Input value={workingDir} onChange={(event) => setWorkingDir(event.currentTarget.value)} placeholder="/app" />
                      </Field>
                    </SettingsPanel>
                  </ResourceSettingsBlock>
                  <ResourceSettingsBlock title="Healthcheck path" description="Leave empty to skip.">
                    <SettingsPanel>
                      <Field label="Path">
                        <Input value={healthPath} onChange={(event) => setHealthPath(event.currentTarget.value)} placeholder="/healthz" />
                      </Field>
                    </SettingsPanel>
                  </ResourceSettingsBlock>
                  <ResourceSettingsBlock title="Restart policy">
                    <SettingsPanel>
                      <Field label="Policy">
                        <Input value="Always" disabled readOnly aria-label="Restart policy" />
                      </Field>
                    </SettingsPanel>
                  </ResourceSettingsBlock>
                  <ResourceSettingsBlock title="Metrics" description="Serves /metrics. Needs Prometheus Operator." live>
                    <ToggleRow label="Enable metrics" checked={metrics} onChange={setMetrics} />
                  </ResourceSettingsBlock>
                  {saveButton("deploy")}
                </FormStack>
                <ResourceSettingsBlock title="Deploy hook">
                  <DeployHook project={resourceName(project)} environment={spec.environment} app={name} gitSource={Boolean(git)} />
                </ResourceSettingsBlock>
              </ResourceSettingsSection>
            )}

            {show("general") && (
              <ResourceSettingsSection id="general" icon={Settings} title="General">
                <ResourceSettingsBlock title="Resource">
                  <SettingsPanel>
                    <div className="rw-settings-usage-lines">
                      <div className="rw-settings-usage-line"><span>Project</span><strong>{spec.project || "—"}</strong></div>
                      <div className="rw-settings-usage-line"><span>Environment</span><strong>{spec.environment || "—"}</strong></div>
                    </div>
                  </SettingsPanel>
                </ResourceSettingsBlock>
                <ResourceSettingsBlock title="Service ID">
                  <RailCopyField label="ID" value={name} onCopy={() => copyText(name)} aria-label="Service ID" />
                </ResourceSettingsBlock>
              </ResourceSettingsSection>
            )}

            {show("danger") && (
              <ResourceDangerSection
                item={item}
                kind="apps"
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
