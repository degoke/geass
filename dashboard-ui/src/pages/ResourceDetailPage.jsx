import { useEffect, useState } from "react";
import { Stack } from "@mantine/core";
import { MetricsBarChart } from "@/components/MetricsBarChart";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { ArrowLeft, Box, Database, HardDrive, RefreshCw, Terminal } from "lucide-react";
import { action, api, list, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { pendingChangeCopy, resourceStatus } from "@/lib/services";
import { sizeLabel } from "@/lib/capacity";
import { CodePanel } from "@/components/CodePanel";
import { PendingChangesBanner } from "@/components/PendingChangesBanner";
import { PageFrame } from "@/layout/PageFrame";
import { ResourceSettings } from "@/components/ResourceSettings";
import { Status } from "@/components/Status";
import {
  Button,
  Checkbox,
  DataTable,
  DetailTabs,
  Empty,
  Field,
  FormStack,
  Input,
  KeyValueTable,
  MetricPanel,
  Muted,
  PageSection,
  PageStack,
  PageHeader,
  Select,
  Switch,
  Textarea,
} from "@/components/geass-ui";

export function ResourceDetailPage({ project, data, kind, name, reload }) {
  const navigate = useNavigate();
  const search = useRouterState({ select: (state) => state.location.search || {} });
  const view = search.view || "overview";
  const key = { apps: "apps", databases: "databases", "object-stores": "objectStores", "logical-databases": "logicalDatabases" }[kind];
  const item = list(data, key).find((entry) => resourceName(entry) === name);
  const [query, setQuery] = useState("SELECT 1");
  const [output, setOutput] = useState("");
  const [variable, setVariable] = useState({ key: "", value: "", secret: false });
  const [sharedRefs, setSharedRefs] = useState([]);
  const runtime = useQuery({ queryKey: ["runtime", kind, name], queryFn: () => api(`/api/apps/${name}/runtime`), enabled: kind === "apps" && view === "console" });
  const logs = useQuery({ queryKey: ["logs", name], queryFn: () => api(`/api/apps/${name}/logs`), enabled: kind === "apps" && view === "logs" });
  useEffect(() => {
    if (!item) return;
    setSharedRefs(item.spec?.sharedVariableRefs || []);
  }, [item, name]);
  if (!item) return <Empty title="Resource not found" description="It may still be reconciling." action={<Button onClick={() => navigate({ to: `/projects/${resourceName(project)}` })}>Back to workspace</Button>} />;
  const href = (next) => `/projects/${resourceName(project)}/${kind}/${name}?view=${next}`;
  const title = resourceName(item);
  const isApp = kind === "apps";
  const isDatabase = kind === "databases" || kind === "logical-databases";
  const tabs = ["overview", ...(isApp ? [...(canMutate(data) ? ["logs"] : []), "metrics", "deployments", "variables", "console"] : []), ...(isDatabase ? ["console", "monitor"] : []), "settings"];
  const sourceImage = typeof item.spec?.source?.image === "string" ? item.spec.source.image : item.spec?.source?.image?.image;
  const deployments = list(data, "deployments").filter((entry) => entry.spec?.app === name);
  const shared = (project.spec?.sharedVariables || []).filter((entry) => entry.environment === item.spec?.environment);
  const saveVariable = (event) => {
    event.preventDefault();
    const path = variable.secret ? `/apps/${name}/secrets/set` : `/apps/${name}/config/set`;
    action(path, variable).then(() => { reload(); alertUser("Variable saved. Deploy to apply it."); setVariable({ key: "", value: "", secret: false }); }).catch((error) => alertUser(error.message));
  };
  const attachShared = (event) => {
    event.preventDefault();
    action(`/apps/${name}/shared-variables/save`, { sharedVariable: sharedRefs }).then(() => { reload(); alertUser("Shared variables saved. Deploy to apply them."); }).catch((error) => alertUser(error.message));
  };
  const sourceSummary = item.spec?.source?.git?.repository || sourceImage || item.spec?.engine || "Managed resource";
  const status = resourceStatus(item);
  const endpoint = item.status?.host || item.status?.endpoint || item.status?.url || "Appears when ready";
  const overviewRows = [
    { label: "Project", value: item.spec?.project },
    { label: "Environment", value: item.spec?.environment },
    { label: "Source", value: item.spec?.source?.git?.repository || sourceImage || item.spec?.engine || item.spec?.placement || "—" },
    ...(isApp ? [{ label: "Replicas", value: `${item.spec?.replicas ?? 1}${item.spec?.autoscaling?.maxReplicas > 1 ? ` · autoscale to ${item.spec.autoscaling.maxReplicas}` : ""}` }] : []),
    ...((isApp || (kind === "databases" && item.spec?.placement !== "External"))
      ? [{
        label: "Size",
        value: `${sizeLabel(data?.platform?.capacity?.cpuSizes || [], item.spec?.resources?.requests?.cpu, item.spec?.resources?.requests?.cpu || "0.1 CPU")} · ${sizeLabel(data?.platform?.capacity?.memorySizes || [], item.spec?.resources?.requests?.memory, item.spec?.resources?.requests?.memory || "128 MB")}`,
      }]
      : []),
    { label: "Namespace", value: item.status?.targetNamespace || "Pending" },
    { label: "Endpoint", value: endpoint },
  ];

  const KindIcon = isApp ? Box : isDatabase ? Database : HardDrive;

  return (
    <PageFrame
      header={
        <PageHeader
          leading={
            <>
              <Button
                variant="ghost"
                size="icon"
                aria-label="Back to resources"
                onClick={() => navigate({ to: `/projects/${resourceName(project)}`, search: item.spec?.environment ? { environment: item.spec.environment } : undefined })}
              >
                <ArrowLeft size={16} />
              </Button>
              <KindIcon size={22} strokeWidth={1.75} aria-hidden />
            </>
          }
          title={title}
          description={sourceSummary}
          actions={
            <>
              <Status value={status} />
              {isApp && !pendingChangeCopy(item) && item.spec?.deploy?.enabled && canMutate(data) && (
                <Button variant="outline" onClick={() => action(`/apps/${name}/deploy`).then(() => { reload(); alertUser("Redeploy requested"); }).catch((error) => alertUser(error.message))}>Redeploy</Button>
              )}
            </>
          }
        />
      }
      notice={isApp ? <PendingChangesBanner item={item} name={name} reload={reload} data={data} /> : null}
      nav={<DetailTabs value={view} tabs={tabs} hrefFor={(tab) => href(tab)} />}
    >
      {view === "overview" && (
        <MetricPanel label="Overview">
          <KeyValueTable items={overviewRows} />
        </MetricPanel>
      )}
      {view === "logs" && (
        <PageSection
          title="Logs"
          description="Recent container output for this service."
          actions={<Button variant="outline" size="sm" onClick={() => logs.refetch()} leftSection={<RefreshCw size={14} />}>Refresh</Button>}
        >
          <CodePanel code={logs.data?.lines} language="bash" empty="No logs yet." />
        </PageSection>
      )}
      {view === "metrics" && (
        <Stack gap="sm">
          {(data?.metrics || []).length ? (
            <>
              {(data.metrics || []).map((metric) => (
                <MetricPanel key={metric.title} label={metric.title} value={metric.value} />
              ))}
              <MetricPanel label="Usage">
                <MetricsBarChart metrics={data.metrics} caption="Source: Prometheus" />
              </MetricPanel>
            </>
          ) : (
            <MetricPanel label="Metrics">
              <Muted size="sm">Metrics refresh from Prometheus once the service is scraping.</Muted>
            </MetricPanel>
          )}
        </Stack>
      )}
      {view === "deployments" && (
        <PageSection title="Deployments" description="Recorded deploy and image changes.">
          <DataTable
            columns={[
              { label: "Change", key: "change" },
              { label: "Source", key: "source" },
              { label: "Image", key: "image" },
              { label: "Phase", key: "phase", align: "right" },
            ]}
            rows={deployments.map((entry) => ({
              change: entry.spec?.changeTitle || "—",
              source: entry.spec?.source || "—",
              image: entry.spec?.image || "—",
              phase: entry.status?.phase || "—",
            }))}
            getRowKey={(row, index) => `${row.change}-${index}`}
            empty="No deployments recorded yet."
          />
        </PageSection>
      )}
      {view === "variables" && (
        <PageStack>
          {canMutate(data) ? (
            <>
              <PageSection title="Environment variables" description="Applied on the next deploy.">
                <FormStack onSubmit={saveVariable}>
                  <Field label="Name"><Input required value={variable.key} onChange={(event) => setVariable({ ...variable, key: event.target.value })} placeholder="DATABASE_URL" /></Field>
                  <Field label="Value"><Input required type={variable.secret ? "password" : "text"} value={variable.value} onChange={(event) => setVariable({ ...variable, value: event.target.value })} /></Field>
                  <Switch label="Store as secret" checked={variable.secret} onChange={(event) => setVariable({ ...variable, secret: event.currentTarget.checked })} />
                  <Button type="submit">Add variable</Button>
                </FormStack>
              </PageSection>
              <PageSection title="Project variables" description="Reference shared values from the project.">
                <FormStack onSubmit={attachShared}>
                  {shared.length ? (
                    <Checkbox.Group value={sharedRefs} onChange={setSharedRefs}>
                      <Stack gap="xs">
                        {shared.map((entry) => (
                          <Checkbox
                            key={entry.name}
                            value={entry.name}
                            label={`${entry.name}${entry.secretRef ? " (secret)" : ""}`}
                          />
                        ))}
                      </Stack>
                    </Checkbox.Group>
                  ) : <Muted size="sm">No project-level variables in this environment.</Muted>}
                  {shared.length > 0 && <Button type="submit">Reference selected variables</Button>}
                </FormStack>
              </PageSection>
            </>
          ) : (
            <PageSection title="Environment variables" description="Viewer role cannot change variables.">
              <Muted size="sm">Your viewer role cannot change variables.</Muted>
            </PageSection>
          )}
        </PageStack>
      )}
      {view === "console" && (
        <PageSection title={isApp ? "Console" : "Query"} description={isApp ? "Open a shell in a running pod." : "Run SQL against this database."}>
          {!canMutate(data) ? (
            <Muted size="sm">Your viewer role cannot open consoles or run queries.</Muted>
          ) : isApp ? (
            <FormStack onSubmit={(event) => { event.preventDefault(); const target = event.target.target.value; const command = event.target.command.value || "sh"; action(`/apps/${name}/console/create`, { target, command }).then(() => alertUser("Console session created")).catch((error) => alertUser(error.message)); }}>
              <Field label="Pod"><Select name="target">{(runtime.data?.pods || []).map((pod) => <option key={pod.name} value={`${pod.name}|${pod.container}`}>{pod.name}</option>)}</Select></Field>
              <Field label="Command"><Input name="command" defaultValue="sh" /></Field>
              <Button type="submit"><Terminal size={14} /> Open console</Button>
            </FormStack>
          ) : (
            <FormStack onSubmit={(event) => { event.preventDefault(); action(`/databases/${name}/query`, { query }).then((result) => setOutput(result.output || "")).catch((error) => alertUser(error.message)); }}>
              <Field label="Query"><Textarea rows={6} value={query} onChange={(event) => setQuery(event.target.value)} /></Field>
              <Button type="submit">Run query</Button>
              {output && <CodePanel code={output} language="sql" />}
            </FormStack>
          )}
        </PageSection>
      )}
      {view === "monitor" && (
        <Stack gap="sm">
          {(data?.metrics || []).length ? (
            <>
              {(data.metrics || []).map((metric) => (
                <MetricPanel key={metric.title} label={metric.title} value={metric.value} />
              ))}
              <MetricPanel label="Usage">
                <MetricsBarChart metrics={data.metrics} />
              </MetricPanel>
            </>
          ) : (
            <MetricPanel label="Monitor">
              <Muted size="sm">No metrics available yet.</Muted>
            </MetricPanel>
          )}
        </Stack>
      )}
      {view === "settings" && <ResourceSettings item={item} kind={kind} name={name} title={title} data={data} project={project} reload={reload} navigate={navigate} />}
    </PageFrame>
  );
}
