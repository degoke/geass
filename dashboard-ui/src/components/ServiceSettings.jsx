import { useMemo, useState } from "react";
import { AlertTriangle, ExternalLink, Gauge, GitBranch, Globe, Hammer, Rocket, Settings } from "lucide-react";
import { action } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { settingsSavedMessage } from "@/lib/services";
import { projectRegion, tagsInRegion } from "@/lib/placement";
import { RunOnField } from "@/components/RunOnField";
import { defaultSizeForKind } from "@/lib/capacity";
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
import { SettingsFormActions, SettingsPanel } from "@/layout/SettingsShell";

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
  const [replicas, setReplicas] = useState(spec.replicas || 1);
  const [autoscaling, setAutoscaling] = useState(Boolean(spec.autoscaling?.maxReplicas > 1));
  const [maxReplicas, setMaxReplicas] = useState(spec.autoscaling?.maxReplicas || 3);
  const [targetCPU, setTargetCPU] = useState(spec.autoscaling?.targetCPUUtilization || 50);
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
      { id: "deploy", label: "Deploy", icon: Rocket, keywords: ["deploy", "start command", "healthcheck", "restart", "metrics", "args", "working directory"] },
      { id: "general", label: "General", icon: Settings, keywords: ["general", "project", "environment", "id", name] },
      { id: "danger", label: "Danger", icon: AlertTriangle, danger: true, keywords: ["danger", "delete", "remove"] },
  ], [name]);

  if (!canMutate(data)) {
    return (
      <div className="rw-resource-settings-shell rw-rail-settings">
        <Muted size="sm">Your viewer role can inspect this service but cannot change it.</Muted>
      </div>
    );
  }

  const save = (section, values) => (event) => {
    event?.preventDefault();
    setSaving(section);
    action(`/apps/${name}/update`, values)
      .then((result) => { reload?.(); alertUser(settingsSavedMessage(result)); })
      .catch((error) => alertUser(error.message))
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
                    <ResourceSettingsBlock title="Function runtime" description="The language runtime that runs your code. Edit the code itself in the Source Code tab.">
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
                      <ResourceSettingsBlock title="Source repo" description="The GitHub repository this service is built from.">
                        <SettingsPanel>
                          <Field label="Repository">
                            <Input required value={repository} onChange={(event) => setRepository(event.currentTarget.value)} placeholder="org/app" />
                          </Field>
                          <Field label="Root directory" description="Used for build and deploy steps. Leave empty for the repository root.">
                            <Input value={context} onChange={(event) => setContext(event.currentTarget.value)} placeholder="/apps/api" />
                          </Field>
                        </SettingsPanel>
                      </ResourceSettingsBlock>
                      <ResourceSettingsBlock
                        title="Branch connected to production"
                        description="Pushes to this branch are built and deployed automatically through the GitHub connection."
                      >
                        <SettingsPanel>
                          <Field label="Branch">
                            <Input value={branch} onChange={(event) => setBranch(event.currentTarget.value)} placeholder="main" />
                          </Field>
                        </SettingsPanel>
                      </ResourceSettingsBlock>
                      <ResourceSettingsBlock title="Wait for CI" description="Trigger deployments after all GitHub Actions checks have completed successfully.">
                        <ToggleRow label="Wait for CI" checked={waitForCI} onChange={setWaitForCI} />
                      </ResourceSettingsBlock>
                    </>
                  ) : (
                    <ResourceSettingsBlock title="Container image" description="The registry image this service runs. Save here, then deploy.">
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
                  <ResourceSettingsBlock title="Public networking" description="Access your service over HTTP with a domain.">
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
                      <Muted size="sm">
                        No public domain yet. Add one below, or connect Cloudflare in platform settings for automatic domains.
                      </Muted>
                    )}
                    <SettingsPanel className="rw-resource-settings-panel-spaced">
                      <Field label="Custom domain" description="Point a DNS record at your cluster, then enter the hostname. Applies instantly.">
                        <Input value={host} onChange={(event) => setHost(event.currentTarget.value)} placeholder="api.example.com" />
                      </Field>
                      <Field label="Port" description="The port your service listens on. Defaults to 8080.">
                        <Input type="number" min="1" max="65535" value={port} onChange={(event) => setPort(event.currentTarget.value)} placeholder="8080" />
                      </Field>
                    </SettingsPanel>
                    <ToggleRow label="HTTPS" description="Serve this domain over TLS. Applies instantly." checked={tls} onChange={setTls} />
                    <ToggleRow label="Verify DNS" description="Check that the hostname resolves before reporting the domain as ready. Applies instantly." checked={dnsVerification} onChange={setDnsVerification} />
                  </ResourceSettingsBlock>
                  {saveButton("networking")}
                </FormStack>
                <ResourceSettingsBlock title="Private networking" description="Other services in this environment can reach this service on the cluster network.">
                  {privateHost ? (
                    <RailCopyField label="Address" value={`${privateHost}:${servicePort}`} onCopy={() => copyText(`${privateHost}:${servicePort}`)} aria-label="Private address" />
                  ) : (
                    <Muted size="sm">The private address appears once this service has been deployed.</Muted>
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
                    replicas: String(replicas),
                    autoscaling: autoscaling ? "on" : "",
                    maxReplicas: String(maxReplicas),
                    targetCPU: String(targetCPU),
                    ...(runOnShown ? { runOnTags: runOn.join(",") } : {}),
                  })}
                >
                  <ResourceSettingsBlock title="Replicas" description="How many instances of this service run." live>
                    <SettingsPanel>
                      <Field label="Replicas">
                        <Input type="number" min="1" value={replicas} onChange={(event) => setReplicas(Number(event.currentTarget.value) || 1)} />
                      </Field>
                    </SettingsPanel>
                  </ResourceSettingsBlock>
                  <ResourceSettingsBlock title="Resources" description="CPU and memory allocated to each replica.">
                    <SettingsPanel>
                      <SizeFields
                        cpu={cpu}
                        memory={memory}
                        onChange={(key, value) => (key === "cpu" ? setCpu(value) : setMemory(value))}
                        data={data}
                      />
                    </SettingsPanel>
                  </ResourceSettingsBlock>
                  <RunOnField data={data} region={region} value={runOn} onChange={setRunOn} note="Applies on deploy." />
                  <ResourceSettingsBlock title="Autoscaling" description="Scale out when average CPU crosses your target." live>
                    <ToggleRow label="Enable autoscaling" checked={autoscaling} onChange={setAutoscaling} />
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
                    <ResourceSettingsBlock title="Builder" description="Geass builds your repository with a Dockerfile using BuildKit.">
                      <SettingsPanel>
                        <Field label="Dockerfile path" description="Path to your Dockerfile in the repository. Defaults to Dockerfile at the root directory.">
                          <Input value={dockerfile} onChange={(event) => setDockerfile(event.currentTarget.value)} placeholder="/apps/api/Dockerfile" />
                        </Field>
                      </SettingsPanel>
                    </ResourceSettingsBlock>
                    <ResourceSettingsBlock title="Watch paths" description="Gitignore-style rules that decide which changed files trigger a new deployment. One pattern per line." live>
                      <SettingsPanel>
                        <Textarea rows={4} value={watchPatterns} onChange={(event) => setWatchPatterns(event.currentTarget.value)} placeholder={"/src/**\n!/docs/**"} spellCheck={false} aria-label="Watch paths" />
                      </SettingsPanel>
                    </ResourceSettingsBlock>
                    <ResourceSettingsBlock title="Build cache" description="Reuse image layers between builds.">
                      <ToggleRow label="Use build cache" checked={buildCache} onChange={setBuildCache} />
                    </ResourceSettingsBlock>
                    {saveButton("build")}
                  </FormStack>
                ) : (
                  <ResourceSettingsBlock title="Builder">
                    <Muted size="sm">
                      {fn ? "Functions run your code directly on the runtime, so there is nothing to build." : "This service runs a prebuilt container image, so there is nothing to build."}
                    </Muted>
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
                  <ResourceSettingsBlock title="Custom start command" description="Overrides the image entrypoint when new deployments start. Leave empty to use the image default.">
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
                  <ResourceSettingsBlock title="Healthcheck path" description="Endpoint called before a deploy completes to make sure the new deployment is live. Leave empty to skip.">
                    <SettingsPanel>
                      <Field label="Path">
                        <Input value={healthPath} onChange={(event) => setHealthPath(event.currentTarget.value)} placeholder="/healthz" />
                      </Field>
                    </SettingsPanel>
                  </ResourceSettingsBlock>
                  <ResourceSettingsBlock title="Restart policy" description="Configure what to do when the process exits.">
                    <SettingsPanel>
                      <Field label="Policy">
                        <Input value="Always" disabled readOnly aria-label="Restart policy" />
                      </Field>
                    </SettingsPanel>
                  </ResourceSettingsBlock>
                  <ResourceSettingsBlock title="Metrics" description="Expose Prometheus metrics from /metrics. Requires Prometheus Operator in the cluster." live>
                    <ToggleRow label="Enable metrics" checked={metrics} onChange={setMetrics} />
                  </ResourceSettingsBlock>
                  {saveButton("deploy")}
                </FormStack>
              </ResourceSettingsSection>
            )}

            {show("general") && (
              <ResourceSettingsSection id="general" icon={Settings} title="General">
                <ResourceSettingsBlock title="Resource" description="Where this service lives in your project.">
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
