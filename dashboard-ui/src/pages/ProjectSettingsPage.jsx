import { useState } from "react";
import { Link } from "@tanstack/react-router";
import {
  AlertTriangle,
  BarChart3,
  Braces,
  Check,
  GitBranch,
  Layers,
  Settings,
  Variable,
} from "lucide-react";
import { action, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { projectResources } from "@/lib/project-resources";
import { RailCopyField } from "@/components/rail-controls";
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
  { id: "github", label: "Integrations", icon: GitBranch },
  { id: "danger", label: "Danger", icon: AlertTriangle, danger: true },
];

export function ProjectSettingsPage({ project, data, reload, environment: workspaceEnvironment }) {
  const name = resourceName(project);
  const [section, setSection] = useState("general");
  const [displayName, setDisplayName] = useState(project.spec?.displayName || name);
  const [newEnvironment, setNewEnvironment] = useState("");
  const githubEnvironment = workspaceEnvironment || project.spec?.environments?.[0] || "";
  const [variable, setVariable] = useState({ environment: githubEnvironment, name: "", value: "", secret: false });
  const mutable = canMutate(data);
  const variables = project.spec?.sharedVariables || [];
  const envSearch = workspaceEnvironment ? { environment: workspaceEnvironment } : undefined;
  const resources = projectResources(data, name, workspaceEnvironment);

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
                action(`/projects/${name}/settings/save`, { displayName, environments: project.spec?.environments || [] })
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
              <RailCopyField label="Project ID" value={name} onCopy={copyProjectId} />
              <SettingsFormActions>
                <Button type="submit" disabled={!mutable}>Update</Button>
              </SettingsFormActions>
            </form>
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
          <SettingsSection
            title="Details"
            flat
            actions={
              <Button variant="outline" component={Link} to={`/projects/${name}/monitor`} search={envSearch}>
                Open metrics
              </Button>
            }
          >
            <p className="rw-rail-body">CPU, memory, and network charts live on the project metrics page.</p>
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

      {section === "github" && (
        <SettingsSection title="Integrations" flat>
          <div className="rw-rail-integration">
            <div className="rw-rail-integration-icon" aria-hidden="true">
              <GitBranch size={22} />
            </div>
            <div>
              <h3 className="rw-rail-integration-title">GitHub</h3>
              <p className="rw-rail-body">
                {project.spec?.githubConnectionRef?.name
                  ? `Connected as ${project.spec.githubConnectionRef.name}`
                  : "Connect Railway environments to production and preview deploys on GitHub."}
              </p>
              {mutable ? (
                <Button
                  variant="outline"
                  onClick={() => action(`/projects/${name}/github/install`, { environment: githubEnvironment })
                    .then((result) => { if (result.url) window.location.assign(result.url); else reload(); })
                    .catch((error) => alertUser(error.message))}
                >
                  {project.spec?.githubConnectionRef?.name ? "Manage connection" : "Connect to GitHub"}
                </Button>
              ) : null}
            </div>
          </div>
        </SettingsSection>
      )}

      {section === "danger" && (
        <SettingsSection title="Delete Project" flat>
          <SettingsAlert tone="danger">
            Deleting the project will permanently delete all data stored in the project services for all environments. This can&apos;t be undone.
          </SettingsAlert>
          <SettingsFormActions>
            <Button variant="danger" disabled>Delete Project</Button>
          </SettingsFormActions>
          <p className="rw-rail-body">Project deletion is not enabled in this build.</p>
        </SettingsSection>
      )}
    </SettingsShell>
  );
}
