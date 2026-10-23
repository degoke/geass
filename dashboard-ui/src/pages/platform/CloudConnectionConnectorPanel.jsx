import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { action, api, list, projectDisplayName, resourceName } from "@/lib/api";
import { canMutate, isPlatformSession, sessionScopes } from "@/lib/dashboard";
import { alertUser, alertError } from "@/lib/notify";
import { Button, Field, FormStack, Input, Muted, Select } from "@/components/geass-ui";
import { ConnectionList } from "@/components/compute/CloudConnections";
import { isDataConnection, useConnections, useRefreshCompute } from "@/lib/compute";
import { SettingsFormActions } from "@/layout/SettingsShell";

const EMPTY_FORM = {
  name: "",
  project: "",
  org: "",
  accessKeyId: "",
  secretAccessKey: "",
  region: "us-east-1",
  token: "",
  organization: "",
};

export function CloudConnectionConnectorPanel({ data, reload, provider, inSheet = false }) {
  const connections = useConnections();
  const refreshCompute = useRefreshCompute();
  // The ones that only create servers are listed under Server clouds.
  const rows = (connections.data || []).filter((row) => row.provider === provider && isDataConnection(row.purposes));
  const projects = list(data, "projects");
  const projectNames = Object.fromEntries(projects.map((project) => [resourceName(project), projectDisplayName(project)]));
  const [form, setForm] = useState(EMPTY_FORM);
  // With organizations on, a connection can be made for all of an organization's projects instead of one project.
  const orgsOn = sessionScopes(data).length > 0 && isPlatformSession(data);
  const orgs = useQuery({ queryKey: ["orgs"], queryFn: () => api("/api/orgs"), enabled: orgsOn });
  const orgItems = orgs.data?.items || [];
  const orgNames = Object.fromEntries(orgItems.map((org) => [org.name, org.displayName]));
  // Whose a connection is: a project's, or all the projects of an organization.
  const scopeOf = (row) => {
    if (row.project) return projectNames[row.project] || row.project;
    if (row.org) return `All of ${orgNames[row.org] || row.org}`;
    return "Platform";
  };

  const resetForm = () => setForm(EMPTY_FORM);

  const onSubmit = (event) => {
    event.preventDefault();
    action("/cloud-connections/create", { ...form, provider })
      .then(() => {
        reload();
        refreshCompute();
        alertUser("Connection saved");
        resetForm();
      })
      .catch((error) => alertError(error.message));
  };

  return (
    <>
      {connections.isError ? <Muted size="sm" c="red.4">{connections.error.message}</Muted> : connections.isPending ? <Muted size="sm">Loading connections…</Muted> : (
        <ConnectionList rows={rows} mutable={canMutate(data)} scopeOf={scopeOf} empty={`No ${provider} connections yet.`} />
      )}
      <FormStack onSubmit={onSubmit} style={{ marginTop: inSheet ? 16 : 24 }}>
        <Field label="Name">
          <Input
            required
            value={form.name}
            onChange={(event) => setForm({ ...form, name: event.target.value })}
            placeholder={provider === "AWS" ? "prod-aws" : "prod-planetscale"}
          />
        </Field>
        <Field label="Project">
          <Select
            required={!orgsOn || !form.org}
            value={form.project}
            aria-label="Project"
            onChange={(event) => setForm({ ...form, project: event.currentTarget.value, org: event.currentTarget.value ? "" : form.org })}
          >
            <option value="">{orgsOn ? "None (use an organization)" : "Choose a project"}</option>
            {projects.map((project) => <option key={resourceName(project)} value={resourceName(project)}>{projectDisplayName(project)}</option>)}
          </Select>
        </Field>
        {orgsOn ? (
          <Field label="Or an organization">
            <Select value={form.org} disabled={Boolean(form.project)} aria-label="Organization" onChange={(event) => setForm({ ...form, org: event.currentTarget.value })}>
              <option value="">None</option>
              {orgItems.map((org) => <option key={org.name} value={org.name}>{org.displayName}</option>)}
            </Select>
          </Field>
        ) : null}
        {provider === "AWS" ? (
          <>
            <Field label="Access key ID">
              <Input required value={form.accessKeyId} onChange={(event) => setForm({ ...form, accessKeyId: event.target.value })} />
            </Field>
            <Field label="Secret access key">
              <Input required type="password" value={form.secretAccessKey} onChange={(event) => setForm({ ...form, secretAccessKey: event.target.value })} />
            </Field>
            <Field label="Region">
              <Input value={form.region} onChange={(event) => setForm({ ...form, region: event.target.value })} />
            </Field>
          </>
        ) : (
          <>
            <Field label="Organization">
              <Input required value={form.organization} onChange={(event) => setForm({ ...form, organization: event.target.value })} />
            </Field>
            <Field label="Service token">
              <Input required type="password" value={form.token} onChange={(event) => setForm({ ...form, token: event.target.value })} />
            </Field>
          </>
        )}
        <SettingsFormActions>
          <Button type="submit" disabled={!canMutate(data)}>Save connection</Button>
        </SettingsFormActions>
      </FormStack>
    </>
  );
}
