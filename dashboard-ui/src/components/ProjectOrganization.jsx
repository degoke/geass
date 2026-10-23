import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { action, api, resourceName } from "@/lib/api";
import { canMutate, isPlatformSession, scopeLabel, sessionScopes } from "@/lib/dashboard";
import { alertError, notify } from "@/lib/notify";
import { Button, Field, Select } from "@/components/geass-ui";
import { SettingsFormActions } from "@/layout/SettingsShell";

/**
 * Whose a project is, once the platform has organizations on. The platform's administrators give it to an organization (and
 * a workspace of it) or take it back; everyone else is told whose it is. Nothing is shown while organizations are off.
 */
export function ProjectOrganization({ project, data, reload }) {
  const scopes = sessionScopes(data);
  const platform = isPlatformSession(data);
  const name = resourceName(project);
  const org = project.spec?.org || "";
  const workspace = project.spec?.workspace || "";
  const orgs = useQuery({ queryKey: ["orgs"], queryFn: () => api("/api/orgs"), enabled: platform && scopes.length > 0 });
  const [chosenOrg, setChosenOrg] = useState(org);
  const [chosenWorkspace, setChosenWorkspace] = useState(workspace);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setChosenOrg(org);
    setChosenWorkspace(workspace);
  }, [org, workspace]);

  if (!scopes.length) return null;

  if (!platform) {
    const here = scopes.find((scope) => scope.org === org && (scope.workspace || "") === workspace)
      || scopes.find((scope) => scope.org === org && !scope.workspace);
    return (
      <p className="rw-rail-body">
        This project belongs to {here ? scopeLabel({ ...here, workspace: workspace ? here.workspace : "" }) : "your organization"}.
      </p>
    );
  }

  const items = orgs.data?.items || [];
  const selected = items.find((item) => item.name === chosenOrg);
  const changed = chosenOrg !== org || (chosenOrg && chosenWorkspace !== workspace);
  const assign = async (values, done) => {
    setBusy(true);
    try {
      await action(`/projects/${name}/assign`, values);
      await reload?.();
      notify(done, "success");
    } catch (error) {
      alertError(error.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <form
      className="rw-rail-form"
      onSubmit={(event) => {
        event.preventDefault();
        assign({ org: chosenOrg, workspace: chosenOrg ? chosenWorkspace : "" }, chosenOrg ? "Project assigned" : "Removed from organization");
      }}
    >
      <p className="rw-rail-body">
        {org ? "Only its organization's people see it." : "Only platform administrators see it."}
      </p>
      <Field label="Organization">
        <Select
          value={chosenOrg}
          disabled={!canMutate(data) || orgs.isLoading}
          aria-label="Organization"
          onChange={(event) => {
            setChosenOrg(event.currentTarget.value);
            setChosenWorkspace("");
          }}
        >
          <option value="">None: the platform's</option>
          {items.map((item) => <option key={item.name} value={item.name}>{item.displayName}</option>)}
        </Select>
      </Field>
      {selected && (selected.workspaces || []).length ? (
        <Field label="Workspace" description="Without one, only the organization's administrators see it.">
          <Select value={chosenWorkspace} disabled={!canMutate(data)} aria-label="Workspace" onChange={(event) => setChosenWorkspace(event.currentTarget.value)}>
            <option value="">The organization as a whole</option>
            {selected.workspaces.map((item) => <option key={item.id} value={item.id}>{item.displayName}</option>)}
          </Select>
        </Field>
      ) : null}
      <SettingsFormActions>
        <Button type="submit" loading={busy} disabled={!canMutate(data) || !changed}>Assign</Button>
      </SettingsFormActions>
    </form>
  );
}
