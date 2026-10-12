import { useState } from "react";
import { Stack } from "@mantine/core";
import { MetricsBarChart } from "@/components/MetricsBarChart";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { ArrowLeft, Box, Database, HardDrive, RefreshCw } from "lucide-react";
import { action, api, list, resourceDisplayName, resourceName } from "@/lib/api";
import { EditableResourceTitle } from "@/components/EditableResourceTitle";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { pendingChangeCopy, provisionFailed, resourceStatus, retryProvisionPathForKind, usesDeployWorkflow, userFacingReadyMessage } from "@/lib/services";
import { sizeLabel } from "@/lib/capacity";
import { functionSourceLabel } from "@/lib/functions";
import { CodePanel } from "@/components/CodePanel";
import { PendingChangesBanner } from "@/components/PendingChangesBanner";
import { ResourceDeploymentsView } from "@/components/ResourceDeploymentsView";
import { ResourceVariables } from "@/components/ResourceVariables";
import { PageFrame } from "@/layout/PageFrame";
import { ResourceSettings } from "@/components/ResourceSettings";
import { FunctionEditor } from "@/components/FunctionEditor";
import { ServiceConsole } from "@/components/ServiceConsole";
import { DATABASE_CHARTS, ServiceMetrics } from "@/components/ServiceMetrics";
import { ServiceSettings } from "@/components/ServiceSettings";
import { Status } from "@/components/Status";
import {
  Button,
  DetailTabs,
  Empty,
  Field,
  FormStack,
  KeyValueTable,
  MetricPanel,
  Muted,
  PageSection,
  PageHeader,
  Textarea,
} from "@/components/geass-ui";

export function ResourceDetailPage({ project, data, kind, name, reload, embedded = false, onClose }) {
  const navigate = useNavigate();
  const search = useRouterState({ select: (state) => state.location.search || {} });
  const view = search.view || (embedded ? "deployments" : "overview");
  const key = { apps: "apps", databases: "databases", "object-stores": "objectStores", "logical-databases": "logicalDatabases" }[kind];
  const item = list(data, key).find((entry) => resourceName(entry) === name);
  const [query, setQuery] = useState("SELECT 1");
  const [output, setOutput] = useState("");
  const logs = useQuery({ queryKey: ["logs", name], queryFn: () => api(`/api/apps/${name}/logs`), enabled: kind === "apps" && view === "logs" });
  if (!item) return <Empty title="Resource not found" description="It may still be reconciling." action={<Button onClick={() => navigate({ to: `/projects/${resourceName(project)}` })}>Back to workspace</Button>} />;
  const deploymentId = search.deployment;
  const projectSearch = item.spec?.environment ? { environment: item.spec.environment } : {};
  const href = (next) => {
    const params = new URLSearchParams({ view: next, ...projectSearch });
    if (next === "deployments" && deploymentId) params.set("deployment", deploymentId);
    return `/projects/${resourceName(project)}/${kind}/${name}?${params.toString()}`;
  };
  const openDeployment = (id) => {
    navigate({
      to: `/projects/${resourceName(project)}/${kind}/${name}`,
      search: { view: "deployments", deployment: id, ...projectSearch },
    });
  };
  const title = resourceDisplayName(item);
  const resourceId = resourceName(item);
  const isApp = kind === "apps";
  const isDatabase = kind === "databases" || kind === "logical-databases";
  const isFunction = isApp && Boolean(item.spec?.source?.function);
  const tabs = [
    "deployments",
    ...(isFunction ? ["code"] : []),
    ...(!embedded ? ["overview"] : []),
    ...(isApp ? ["variables", "metrics", "console"] : []),
    ...(isDatabase ? ["console", "monitor"] : []),
    "settings",
  ];
  const sourceImage = typeof item.spec?.source?.image === "string" ? item.spec.source.image : item.spec?.source?.image?.image;
  const deployments = list(data, "deployments").filter((entry) => entry.spec?.app === name);
  const sourceSummary = item.spec?.source?.function
    ? functionSourceLabel(item.spec.source.function)
    : item.spec?.source?.git?.repository || sourceImage || item.spec?.engine || "Managed resource";
  const status = resourceStatus(item);
  const statusDetail = userFacingReadyMessage(item);
  const pending = pendingChangeCopy(item);
  const drawerSubtitle = embedded && (status === "Draft" || pending) ? undefined : sourceSummary;
  const pageSubtitle = statusDetail && status !== "True" && status !== "Ready" ? statusDetail : sourceSummary;
  const endpoint = item.status?.host || item.status?.endpoint || item.status?.url || "Appears when ready";
  const backToProject = () => {
    if (embedded && onClose) {
      onClose();
      return;
    }
    navigate({ to: `/projects/${resourceName(project)}`, search: item.spec?.environment ? { environment: item.spec.environment } : undefined });
  };
  const overviewRows = [
    { label: "Project", value: item.spec?.project },
    { label: "Environment", value: item.spec?.environment },
    { label: "Source", value: item.spec?.source?.function ? functionSourceLabel(item.spec.source.function) : item.spec?.source?.git?.repository || sourceImage || item.spec?.engine || item.spec?.placement || "—" },
    ...(isApp ? [{ label: "Replicas", value: `${item.spec?.replicas ?? 1}${item.spec?.autoscaling?.maxReplicas > 1 ? ` · autoscale to ${item.spec.autoscaling.maxReplicas}` : ""}` }] : []),
    ...((isApp || (kind === "databases" && item.spec?.placement !== "External"))
      ? [{
        label: "Size",
        value: `${sizeLabel(data?.platform?.capacity?.cpuSizes || [], item.spec?.resources?.requests?.cpu, item.spec?.resources?.requests?.cpu || "0.1 CPU")} · ${sizeLabel(data?.platform?.capacity?.memorySizes || [], item.spec?.resources?.requests?.memory, item.spec?.resources?.requests?.memory || "128 MB")}`,
      }]
      : []),
    { label: "Endpoint", value: endpoint },
  ];

  const KindIcon = isApp ? Box : isDatabase ? Database : HardDrive;

  return (
    <PageFrame
      className={embedded ? "rw-resource-drawer-page" : undefined}
      header={
        <PageHeader
          leading={
            <>
              <Button
                variant="ghost"
                size="icon"
                aria-label="Back to canvas"
                onClick={backToProject}
              >
                <ArrowLeft size={16} />
              </Button>
              <KindIcon size={22} strokeWidth={1.75} aria-hidden />
            </>
          }
          title={
            <EditableResourceTitle
              kind={kind}
              name={resourceId}
              value={title}
              canEdit={canMutate(data)}
              onSaved={reload}
            />
          }
          description={embedded ? drawerSubtitle : pageSubtitle}
          actions={
            <>
              <Status value={status} />
              {provisionFailed(item) && canMutate(data) && (
                <Button
                  variant="outline"
                  onClick={() => action(retryProvisionPathForKind(kind, name)).then(() => { reload(); alertUser("Retrying provisioning"); }).catch((error) => alertUser(error.message))}
                >
                  Retry
                </Button>
              )}
              {isApp && !pendingChangeCopy(item) && item.spec?.deploy?.enabled && canMutate(data) && (
                <Button variant="outline" onClick={() => action(`/apps/${name}/deploy`).then(() => { reload(); alertUser("Redeploy requested"); }).catch((error) => alertUser(error.message))}>Redeploy</Button>
              )}
            </>
          }
        />
      }
      notice={!embedded && usesDeployWorkflow(item) ? <PendingChangesBanner item={item} kind={kind} name={name} reload={reload} data={data} /> : null}
      nav={<DetailTabs value={view} tabs={tabs} hrefFor={(tab) => href(tab)} labels={{ code: "Source Code" }} />}
    >
      {view === "overview" && (
        <MetricPanel label="Overview">
          {statusDetail && status !== "True" && status !== "Ready" && !(embedded && (status === "Draft" || pending)) ? (
            <Muted size="sm" className="rw-resource-status-hint">{statusDetail}</Muted>
          ) : null}
          <KeyValueTable items={overviewRows} />
        </MetricPanel>
      )}
      {view === "deployments" && (
        <ResourceDeploymentsView
          item={item}
          kind={kind}
          name={name}
          deployments={deployments}
          onOpenDeployment={openDeployment}
        />
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
      {view === "metrics" && isApp && (
        <ServiceMetrics scope="apps" name={name} />
      )}
      {view === "code" && isFunction && (
        <FunctionEditor item={item} name={name} data={data} reload={reload} />
      )}
      {view === "variables" && isApp && (
        <ResourceVariables item={item} name={name} project={project} data={data} reload={reload} />
      )}
      {view === "console" && isApp && (
        <ServiceConsole item={item} name={name} data={data} />
      )}
      {view === "console" && !isApp && (
        <PageSection title="Query" description="Run SQL against this database.">
          {!canMutate(data) ? (
            <Muted size="sm">Your viewer role cannot run queries.</Muted>
          ) : (
            <FormStack onSubmit={(event) => { event.preventDefault(); action(`/databases/${name}/query`, { query }).then((result) => setOutput(result.output || "")).catch((error) => alertUser(error.message)); }}>
              <Field label="Query"><Textarea rows={6} value={query} onChange={(event) => setQuery(event.target.value)} /></Field>
              <Button type="submit">Run query</Button>
              {output && <CodePanel code={output} language="sql" />}
            </FormStack>
          )}
        </PageSection>
      )}
      {view === "monitor" && kind === "databases" && (
        <ServiceMetrics scope="databases" name={name} charts={DATABASE_CHARTS} />
      )}
      {view === "monitor" && kind !== "databases" && (
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
      {view === "settings" && (isApp ? (
        <ServiceSettings
          item={item}
          name={name}
          data={data}
          project={project}
          reload={reload}
          navigate={navigate}
          onClose={embedded ? onClose : undefined}
        />
      ) : (
        <ResourceSettings
          item={item}
          kind={kind}
          name={name}
          data={data}
          project={project}
          reload={reload}
          navigate={navigate}
          onClose={embedded ? onClose : undefined}
        />
      ))}
    </PageFrame>
  );
}
