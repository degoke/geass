import { useMemo, useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Group, Modal, Text } from "@mantine/core";
import { useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  BarChart3,
  Box,
  Braces,
  Check,
  Database,
  GitBranch,
  HardDrive,
  KeyRound,
  Layers,
  Lock,
  Network,
  Plug,
  Settings,
  ShieldCheck,
  Variable,
  Webhook,
} from "lucide-react";
import { action, resourceDisplayName, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { normalizeProjectName } from "@/lib/project-names";
import { projectResources } from "@/lib/project-resources";
import { defaultRegion, platformRegions } from "@/lib/placement";
import { RailCopyField } from "@/components/rail-controls";
import { ApiTokens } from "@/components/ApiTokens";
import { PREnvironments } from "@/components/PREnvironments";
import { ProjectBackups } from "@/components/ProjectBackups";
import { ProjectCompliance } from "@/components/ProjectCompliance";
import { ProjectNetwork } from "@/components/ProjectNetwork";
import { ProjectWebhooks } from "@/components/ProjectWebhooks";
import { ServiceMetrics } from "@/components/ServiceMetrics";
import { UsagePanel } from "@/components/UsagePanel";
import { Button, Field, Input, Select, Switch, Textarea } from "@/components/geass-ui";
import {
  SettingsAlert,
  SettingsDivider,
  SettingsFormActions,
  SettingsPanel,
  SettingsSection,
  SettingsShell,
} from "@/layout/SettingsShell";

const SECTIONS = [
  { id: "general", label: "General", icon: Settings },
  { id: "usage", label: "Usage", icon: BarChart3 },
  { id: "environments", label: "Environments", icon: Layers },
  { id: "variables", label: "Shared Variables", icon: Variable },
  { id: "webhooks", label: "Webhooks", icon: Webhook },
  { id: "backups", label: "Backups", icon: ShieldCheck },
  { id: "network", label: "Network", icon: Network },
  { id: "compliance", label: "Compliance", icon: Lock },
  { id: "tokens", label: "Tokens", icon: KeyRound },
  { id: "integrations", label: "Integrations", icon: Plug },
  { id: "danger", label: "Danger", icon: AlertTriangle, danger: true },
];

export function ProjectSettingsPage({ project, data, reload, environment: workspaceEnvironment }) {
  const name = resourceName(project);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [section, setSection] = useState("general");
  const [displayName, setDisplayName] = useState(project.spec?.displayName || name);
  const [region, setRegion] = useState(project.spec?.region || "");
  const [newEnvironment, setNewEnvironment] = useState("");
  const [confirmDelete, setConfirmDelete] = useState("");
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [templateName, setTemplateName] = useState("");
  const [templateDescription, setTemplateDescription] = useState("");
  const [templateEnvironment, setTemplateEnvironment] = useState(workspaceEnvironment || project.spec?.environments?.[0] || "");
  const [templateResult, setTemplateResult] = useState(null);
  const [archiveTarget, setArchiveTarget] = useState("");
  const [removeTarget, setRemoveTarget] = useState(null);
  const [busy, setBusy] = useState(false);
  const githubEnvironment = workspaceEnvironment || project.spec?.environments?.[0] || "";
  const [variable, setVariable] = useState({ environment: githubEnvironment, name: "", value: "", secret: false });
  const mutable = canMutate(data);
  const regions = platformRegions(data);
  const hasDatabases = (projectResources(data, name).databases || []).some((db) => db.spec?.placement !== "External");
  const deleteConfirmLabel = project.spec?.displayName || name;
  const deleteConfirmTarget = useMemo(
    () => normalizeProjectName(deleteConfirmLabel) || name,
    [deleteConfirmLabel, name],
  );
  const deleteConfirmed = normalizeProjectName(confirmDelete) === deleteConfirmTarget;
  const environmentCount = (project.spec?.environments || []).length;

  const deleteProject = () => {
    setDeleting(true);
    action(`/projects/${name}/settings/delete`, { confirmName: confirmDelete })
      .then(() => {
        setDeleteModalOpen(false);
        queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] });
        alertUser("Project deleted");
        navigate({ to: "/projects" });
      })
      .catch((error) => alertUser(error.message))
      .finally(() => setDeleting(false));
  };

  const variables = project.spec?.sharedVariables || [];
  const resources = projectResources(data, name, workspaceEnvironment);

  const githubLinked = Boolean(project.spec?.githubConnectionRef?.name);
  const githubReady = Boolean(data?.platform?.hasGitHubApp);
  const allResources = projectResources(data, name);
  const serviceRows = [
    ...allResources.apps.map((item) => ({ item, segment: "apps", label: "Service", icon: Box })),
    ...allResources.databases.map((item) => ({ item, segment: "databases", label: item.spec?.engine || "Database", icon: Database })),
    ...allResources.logical.map((item) => ({ item, segment: "logical-databases", label: "Logical database", icon: Database })),
    ...allResources.stores.map((item) => ({ item, segment: "object-stores", label: "Bucket", icon: HardDrive })),
  ];

  const runBusy = (promise, message) => {
    setBusy(true);
    return promise
      .then(() => { reload(); if (message) alertUser(message); return true; })
      .catch((error) => { alertUser(error.message); return false; })
      .finally(() => setBusy(false));
  };

  const saveAsTemplate = (event) => {
    event.preventDefault();
    setTemplateResult(null);
    runBusy(
      action("/templates/create", {
        project: name,
        environment: templateEnvironment,
        name: templateName,
        description: templateDescription,
      }).then((result) => {
        setTemplateResult(result);
        setTemplateName("");
        setTemplateDescription("");
      }),
      "Template saved",
    );
  };

  const connectGitHub = () => {
    runBusy(
      action(`/projects/${name}/github/install`, { environment: githubEnvironment }).then((result) => {
        if (result?.url) window.location.assign(result.url);
      }),
    );
  };

  const disconnectGitHub = () => runBusy(
    action(`/projects/${name}/github/disconnect`, { environment: githubEnvironment }),
    "GitHub disconnected from this project",
  );

  const archiveResourceCount = archiveTarget
    ? (() => {
      const scoped = projectResources(data, name, archiveTarget);
      return scoped.apps.length + scoped.databases.length + scoped.logical.length + scoped.stores.length;
    })()
    : 0;

  const archiveEnvironment = () => runBusy(
    action(`/projects/${name}/environments/archive`, { environment: archiveTarget, confirmName: archiveTarget }),
    `Environment ${archiveTarget} deleted`,
  ).then((ok) => { if (ok) setArchiveTarget(""); });

  const removeResource = () => {
    const target = removeTarget;
    if (!target) return undefined;
    const id = resourceName(target.item);
    return runBusy(action(`/${target.segment}/${id}/delete`, { confirmName: id }), "Resource deleted")
      .then((ok) => { if (ok) setRemoveTarget(null); });
  };

  const nav = SECTIONS.map((item) => ({
    ...item,
    active: section === item.id,
    onClick: () => setSection(item.id),
  }));

  const resourceTotal =
    resources.apps.length
    + resources.databases.length
    + resources.logical.length
    + resources.stores.length;

  const copyProjectId = () => {
    navigator.clipboard?.writeText(name).then(
      () => alertUser("Copied to clipboard"),
      () => alertUser("Could not copy"),
    );
  };

  return (
    <SettingsShell title="Project Settings" nav={nav}>
      {section === "general" && (
        <>
          <SettingsSection title="Project Info" flat>
            <form
              className="rw-rail-form"
              onSubmit={(event) => {
                event.preventDefault();
                const values = { displayName, environments: project.spec?.environments || [] };
                if (regions.length) values.region = region;
                action(`/projects/${name}/settings/save`, values)
                  .then(() => { reload(); alertUser("Project settings saved"); })
                  .catch((error) => alertUser(error.message));
              }}
            >
              <Field label="Name">
                <Input value={displayName} onChange={(event) => setDisplayName(event.target.value)} aria-label="Project name" />
              </Field>
              <Field label="Description">
                <Textarea disabled placeholder="Optional description" aria-label="Project description" rows={3} />
              </Field>
              {regions.length ? (
                <Field
                  label="Region"
                  description={hasDatabases
                    ? "Fixed, because this project has databases and their data stays on the nodes of the region they were created in."
                    : "This project only runs on nodes in its region. Services restart in the new region when it changes."}
                >
                  <Select value={region} onChange={(event) => setRegion(event.target.value)} disabled={!mutable || hasDatabases} aria-label="Project region">
                    <option value="">{defaultRegion(data) ? `Cluster default (${defaultRegion(data)})` : "Any region"}</option>
                    {regions.map((entry) => <option key={entry.name} value={entry.name}>{entry.name}</option>)}
                  </Select>
                </Field>
              ) : null}
              <RailCopyField label="Project ID" value={name} onCopy={copyProjectId} />
              <SettingsFormActions>
                <Button type="submit" disabled={!mutable}>Update</Button>
              </SettingsFormActions>
            </form>
          </SettingsSection>
          <SettingsDivider />
          <SettingsSection title="Generate Template from Project" flat>
            <p className="rw-rail-body">
              Save the services and in-cluster databases of one environment as a reusable template. Variable values are copied,
              secret values are not: secrets are generated fresh each time the template is deployed.
            </p>
            <form className="rw-rail-form" onSubmit={saveAsTemplate}>
              <Field label="Template name">
                <Input required value={templateName} onChange={(event) => setTemplateName(event.target.value)} placeholder="My stack" aria-label="Template name" />
              </Field>
              <Field label="Description">
                <Input value={templateDescription} onChange={(event) => setTemplateDescription(event.target.value)} placeholder="Optional" aria-label="Template description" />
              </Field>
              {(project.spec?.environments || []).length > 1 ? (
                <Field label="Environment">
                  <Select value={templateEnvironment} onChange={(event) => setTemplateEnvironment(event.target.value)} aria-label="Template environment">
                    {(project.spec?.environments || []).map((env) => <option key={env}>{env}</option>)}
                  </Select>
                </Field>
              ) : null}
              <SettingsFormActions>
                <Button type="submit" variant="outline" disabled={!mutable || busy}>Generate template</Button>
                {templateResult ? <Link to="/templates" className="rw-settings-text-btn">View in Templates</Link> : null}
              </SettingsFormActions>
            </form>
            {templateResult?.skipped?.length ? (
              <p className="rw-rail-hint">Not included: {templateResult.skipped.join("; ")}.</p>
            ) : null}
          </SettingsSection>
          <SettingsDivider />
          <SettingsSection title="Visibility" flat>
            <p className="rw-rail-body">
              This project is private. Only members of the Geass workspace can access it.
            </p>
            <SettingsFormActions>
              <Button type="button" variant="outline" disabled>Change visibility</Button>
            </SettingsFormActions>
          </SettingsSection>
        </>
      )}

      {section === "usage" && (
        <>
          <UsagePanel projectName={name} title="What this project takes of the cluster" />
          <SettingsDivider />
          <SettingsSection title="Current Usage" flat>
            <div className="rw-settings-usage-grid">
              <SettingsPanel className="rw-settings-usage-lines">
                <div className="rw-settings-usage-line">
                  <span>Services</span>
                  <strong>{resources.apps.length}</strong>
                </div>
                <div className="rw-settings-usage-line">
                  <span>Databases</span>
                  <strong>{resources.databases.length + resources.logical.length}</strong>
                </div>
                <div className="rw-settings-usage-line">
                  <span>Object storage</span>
                  <strong>{resources.stores.length}</strong>
                </div>
              </SettingsPanel>
              <SettingsPanel className="rw-settings-usage-total">
                <span className="rw-settings-usage-total-label">Resources</span>
                <span className="rw-settings-usage-total-value">{resourceTotal}</span>
              </SettingsPanel>
            </div>
          </SettingsSection>
          <SettingsDivider />
          <SettingsSection title="Details" flat>
            <ServiceMetrics scope="projects" name={name} environment={workspaceEnvironment} />
          </SettingsSection>
        </>
      )}

      {section === "environments" && (
        <SettingsSection title="Environments" flat>
          <p className="rw-rail-body">Each environment is an isolated namespace for resources and variables.</p>
          <div className="rw-rail-stack">
            {(project.spec?.environments || []).map((env) => (
              <div className="rw-rail-list-row" key={env}>
                <Check size={16} className="rw-settings-env-check" aria-hidden="true" />
                <span className="rw-rail-list-title">{env}</span>
                {(project.status?.pullRequests || []).some((entry) => entry.environment === env) ? (
                  <span className="rw-vars-kind">Pull request</span>
                ) : null}
                {mutable && environmentCount > 1 ? (
                  <button type="button" className="rw-settings-text-btn is-danger rw-list-row-end" onClick={() => setArchiveTarget(env)}>
                    Delete
                  </button>
                ) : null}
              </div>
            ))}
          </div>
          <form
            className="rw-rail-form rw-rail-form-inline"
            onSubmit={(event) => {
              event.preventDefault();
              action(`/projects/${name}/environments/create`, { environment: newEnvironment })
                .then(() => { reload(); setNewEnvironment(""); alertUser("Environment created"); })
                .catch((error) => alertUser(error.message));
            }}
          >
            <Input
              value={newEnvironment}
              onChange={(event) => setNewEnvironment(event.target.value)}
              placeholder="staging"
              aria-label="New environment"
            />
            <Button type="submit" disabled={!mutable}>New Environment</Button>
          </form>
          <SettingsDivider />
          <PREnvironments project={project} data={data} mutable={mutable} reload={reload} />
        </SettingsSection>
      )}

      {section === "variables" && (
        <SettingsSection title="Shared Variables" flat>
          <p className="rw-rail-body">Variables that can be referenced by multiple services within an environment.</p>
          <SettingsPanel className="rw-rail-var-box">
            <button type="button" className="rw-rail-var-head">
              <span>{variable.environment}</span>
              <span className="rw-settings-var-count">
                {variables.filter((entry) => entry.environment === variable.environment).length} variables
              </span>
              <Braces size={16} className="rw-settings-var-braces" aria-hidden="true" />
            </button>
            <div className="rw-rail-var-body">
              <Select
                className="rw-rail-var-env-select"
                value={variable.environment}
                onChange={(event) => setVariable({ ...variable, environment: event.target.value })}
                aria-label="Variable environment"
              >
                {(project.spec?.environments || []).map((env) => <option key={env}>{env}</option>)}
              </Select>
              <form
                className="rw-rail-var-form"
                onSubmit={(event) => {
                  event.preventDefault();
                  action(`/projects/${name}/variables/save`, { ...variable, secret: variable.secret ? "on" : "" })
                    .then(() => { reload(); setVariable({ ...variable, name: "", value: "" }); alertUser("Shared variable saved"); })
                    .catch((error) => alertUser(error.message));
                }}
              >
                <Input
                  required
                  value={variable.name}
                  onChange={(event) => setVariable({ ...variable, name: event.target.value })}
                  placeholder="VARIABLE_NAME"
                  aria-label="Variable name"
                />
                <Input
                  required
                  type={variable.secret ? "password" : "text"}
                  value={variable.value}
                  onChange={(event) => setVariable({ ...variable, value: event.target.value })}
                  placeholder="VALUE or ${{REF}}"
                  aria-label="Variable value"
                />
                <Button type="submit" disabled={!mutable}>Add</Button>
              </form>
              <div className="rw-settings-var-secret">
                <Switch
                  label="Secret"
                  checked={variable.secret}
                  onChange={(event) => setVariable({ ...variable, secret: event.currentTarget.checked })}
                />
              </div>
              <div className="rw-settings-var-list">
                {variables.length ? variables.map((entry) => (
                  <div className="rw-settings-var-row" key={`${entry.environment}-${entry.name}`}>
                    <code>{entry.name}</code>
                    <span className="rw-settings-var-meta">{entry.environment}</span>
                    {mutable ? (
                      <button
                        type="button"
                        className="rw-settings-text-btn is-danger"
                        onClick={() => action(`/projects/${name}/variables/delete`, { environment: entry.environment, name: entry.name })
                          .then(() => { reload(); alertUser("Shared variable deleted"); })}
                      >
                        Remove
                      </button>
                    ) : null}
                  </div>
                )) : (
                  <p className="rw-settings-empty-inline">No shared variables yet</p>
                )}
              </div>
            </div>
          </SettingsPanel>
        </SettingsSection>
      )}

      {section === "webhooks" && (
        <SettingsSection title="Webhooks" flat>
          <ProjectWebhooks project={project} mutable={mutable} reload={reload} />
        </SettingsSection>
      )}

      {section === "backups" && (
        <SettingsSection title="Backups" flat>
          <ProjectBackups project={project} data={data} mutable={mutable} reload={reload} />
        </SettingsSection>
      )}

      {section === "network" && (
        <SettingsSection title="Network" flat>
          <ProjectNetwork project={project} data={data} mutable={mutable} reload={reload} />
        </SettingsSection>
      )}

      {section === "compliance" && (
        <SettingsSection title="Compliance" flat>
          <ProjectCompliance project={project} data={data} mutable={mutable} reload={reload} />
        </SettingsSection>
      )}

      {section === "tokens" && (
        <SettingsSection title="Tokens" flat>
          <ApiTokens scope="project" project={project} projects={[project]} mutable={mutable} />
        </SettingsSection>
      )}

      {section === "integrations" && (
        <SettingsSection title="Integrations" flat>
          <p className="rw-rail-body">Connect this project to the tools that build and expose your services.</p>
          <SettingsPanel className="rw-integration-card">
            <GitBranch size={20} aria-hidden="true" />
            <div className="rw-integration-copy">
              <strong>GitHub</strong>
              <span>
                {githubLinked
                  ? "Connected. Repositories can be deployed as services, and pushes build automatically."
                  : githubReady
                    ? "Link a GitHub account to deploy repositories from the canvas."
                    : "GitHub is not set up for this cluster yet."}
              </span>
            </div>
            {githubLinked ? (
              <Button variant="outline" disabled={!mutable || busy} onClick={disconnectGitHub}>Disconnect</Button>
            ) : githubReady ? (
              <Button disabled={!mutable || busy} onClick={connectGitHub}>Connect to GitHub</Button>
            ) : (
              <Button variant="outline" component={Link} to="/settings/connectors">Set up GitHub</Button>
            )}
          </SettingsPanel>
          <SettingsPanel className="rw-integration-card">
            <Plug size={20} aria-hidden="true" />
            <div className="rw-integration-copy">
              <strong>Cloud connectors</strong>
              <span>Cloudflare domains, AWS, and PlanetScale are configured once for the whole cluster.</span>
            </div>
            <Button variant="outline" component={Link} to="/settings/connectors">Open connectors</Button>
          </SettingsPanel>
        </SettingsSection>
      )}

      {section === "danger" && (
        <>
        <SettingsSection title="Manage Services" flat>
          <SettingsAlert tone="danger">
            Deleting a service permanently removes its data and deployments from the environment it lives in.
          </SettingsAlert>
          {serviceRows.length ? (
            <div className="rw-rail-stack">
              {serviceRows.map((row) => {
                const RowIcon = row.icon;
                return (
                  <div className="rw-rail-list-row" key={`${row.segment}-${resourceName(row.item)}`}>
                    <RowIcon size={16} aria-hidden="true" />
                    <span className="rw-rail-list-title">{resourceDisplayName(row.item)}</span>
                    <span className="rw-vars-kind">{row.label}{row.item.spec?.environment ? ` · ${row.item.spec.environment}` : ""}</span>
                    <button type="button" className="rw-settings-text-btn is-danger rw-list-row-end" disabled={!mutable} onClick={() => setRemoveTarget(row)}>
                      Remove
                    </button>
                  </div>
                );
              })}
            </div>
          ) : (
            <p className="rw-settings-empty-inline">This project has no services.</p>
          )}
        </SettingsSection>
        <SettingsDivider />
        <SettingsSection title="Delete Project" flat>
          <SettingsAlert tone="danger">
            Deleting the project removes its services, databases, buckets, and environment namespaces. This can&apos;t be undone.
          </SettingsAlert>
          <p className="rw-rail-body">
            This project has {resourceTotal} {resourceTotal === 1 ? "resource" : "resources"} across its environments.
          </p>
          <Field
            label={`Type ${deleteConfirmLabel} to delete`}
            description="Use the project name shown in the sidebar (spacing and capitalization are ignored)."
          >
            <Input
              value={confirmDelete}
              onChange={(event) => setConfirmDelete(event.currentTarget.value)}
              autoComplete="off"
              placeholder={deleteConfirmLabel}
              aria-label="Confirm project name"
              disabled={!mutable || deleting}
            />
          </Field>
          <SettingsFormActions>
            <Button
              variant="danger"
              disabled={!mutable || !deleteConfirmed || deleting}
              onClick={() => setDeleteModalOpen(true)}
            >
              Delete
            </Button>
          </SettingsFormActions>
          <Modal
            opened={deleteModalOpen}
            onClose={() => { if (!deleting) setDeleteModalOpen(false); }}
            title="Delete project permanently?"
            centered
          >
            <Text size="sm" mb="md">
              This will permanently delete <strong>{deleteConfirmLabel}</strong> and all of its resources
              ({resourceTotal} {resourceTotal === 1 ? "resource" : "resources"} across {environmentCount}{" "}
              {environmentCount === 1 ? "environment" : "environments"}), including services, databases, buckets,
              and environment namespaces. This action cannot be undone.
            </Text>
            <Group justify="flex-end" gap="sm">
              <Button type="button" variant="outline" disabled={deleting} onClick={() => setDeleteModalOpen(false)}>
                Cancel
              </Button>
              <Button type="button" variant="danger" loading={deleting} onClick={deleteProject}>
                Delete permanently
              </Button>
            </Group>
          </Modal>
        </SettingsSection>
        </>
      )}

      <Modal opened={Boolean(archiveTarget)} onClose={() => { if (!busy) setArchiveTarget(""); }} title="Delete environment?" centered>
        <Text size="sm" mb="md">
          This permanently deletes the <strong>{archiveTarget}</strong> environment
          {archiveResourceCount ? ` and its ${archiveResourceCount} ${archiveResourceCount === 1 ? "resource" : "resources"}` : ""}
          , including their running workloads and the environment&apos;s shared variables. This can&apos;t be undone.
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button type="button" variant="outline" disabled={busy} onClick={() => setArchiveTarget("")}>Cancel</Button>
          <Button type="button" variant="danger" loading={busy} onClick={archiveEnvironment}>Delete environment</Button>
        </Group>
      </Modal>

      <Modal opened={Boolean(removeTarget)} onClose={() => { if (!busy) setRemoveTarget(null); }} title="Delete resource permanently?" centered>
        <Text size="sm" mb="md">
          This will permanently delete <strong>{removeTarget ? resourceDisplayName(removeTarget.item) : ""}</strong> and its data.
          This action cannot be undone.
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button type="button" variant="outline" disabled={busy} onClick={() => setRemoveTarget(null)}>Cancel</Button>
          <Button type="button" variant="danger" loading={busy} onClick={removeResource}>Delete permanently</Button>
        </Group>
      </Modal>
    </SettingsShell>
  );
}
