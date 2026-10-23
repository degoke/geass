import { useState } from "react";
import { Group, Modal, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Building2 } from "lucide-react";
import { action, api } from "@/lib/api";
import { canMutate, sessionScopes } from "@/lib/dashboard";
import { alertError, notify } from "@/lib/notify";
import { OrganizationIdP } from "@/components/OrganizationIdP";
import { Badge, Button, DataTable, Field, Input, Muted, Switch } from "@/components/geass-ui";
import { SettingsFormActions, SettingsSection, SettingsSheet } from "@/layout/SettingsShell";

/** Runs one change to an organization, then refreshes the list and says what happened. */
function useRun(refetch) {
  const [busy, setBusy] = useState("");
  const run = async (key, path, values, done) => {
    setBusy(key);
    try {
      await action(path, values);
      await refetch();
      if (done) notify(done, "success");
      return true;
    } catch (error) {
      alertError(error.message);
      return false;
    } finally {
      setBusy("");
    }
  };
  return [busy, run];
}

function NewOrganization({ refetch, onDone }) {
  const [displayName, setDisplayName] = useState("");
  const [idpID, setIdpID] = useState("");
  const [busy, run] = useRun(refetch);
  const submit = async (event) => {
    event.preventDefault();
    if (await run("create", "/orgs/create", { displayName: displayName.trim(), idpID: idpID.trim() }, "Organization created")) onDone();
  };
  return (
    <form onSubmit={submit}>
      <Stack gap="sm">
        <Field label="Name">
          <Input required value={displayName} aria-label="Organization name" placeholder="Acme" onChange={(event) => setDisplayName(event.currentTarget.value)} />
        </Field>
        <Field label="Id at the identity provider" description="How the sign-in token names it.">
          <Input required value={idpID} aria-label="Id at the identity provider" placeholder="org_acme" onChange={(event) => setIdpID(event.currentTarget.value)} />
        </Field>
        <SettingsFormActions>
          <Button type="submit" loading={busy === "create"}>Create organization</Button>
        </SettingsFormActions>
      </Stack>
    </form>
  );
}

/** What the platform sets for all of an organization's projects: how many, how much together, where, and which domains. */
function OrganizationPolicy({ org, refetch }) {
  const [maxProjects, setMaxProjects] = useState(org.maxProjects ?? "");
  const [cpu, setCpu] = useState(org.quota?.cpu || "");
  const [memory, setMemory] = useState(org.quota?.memory || "");
  const [storage, setStorage] = useState(org.quota?.storage || "");
  const [pods, setPods] = useState(org.quota?.pods ?? "");
  const [region, setRegion] = useState(org.region || "");
  const [tags, setTags] = useState((org.requiredTags || []).join(", "));
  const [domains, setDomains] = useState((org.allowedDomains || []).join(", "));
  const [busy, run] = useRun(refetch);
  const save = (event) => {
    event.preventDefault();
    return run(
      "policy",
      `/orgs/${org.name}/update`,
      {
        maxProjects: String(maxProjects).trim(),
        quotaCPU: cpu.trim(),
        quotaMemory: memory.trim(),
        quotaStorage: storage.trim(),
        quotaPods: String(pods).trim(),
        region: region.trim(),
        requiredTags: tags,
        allowedDomains: domains,
      },
      "Saved",
    );
  };
  return (
    <form onSubmit={save}>
      <Stack gap="sm">
        <Text fw={600} size="sm">Limits and policy</Text>
        <Muted size="sm">Empty means no limit. Lowering a limit removes nothing.</Muted>
        <Field label="Most projects" description="0 allows none.">
          <Input type="number" min="0" value={maxProjects} aria-label="Most projects" placeholder="No limit" onChange={(event) => setMaxProjects(event.currentTarget.value)} />
        </Field>
        <Group grow align="flex-end" gap="sm">
          <Field label="CPU in total">
            <Input value={cpu} aria-label="Total CPU" placeholder="16" onChange={(event) => setCpu(event.currentTarget.value)} />
          </Field>
          <Field label="Memory in total">
            <Input value={memory} aria-label="Total memory" placeholder="32Gi" onChange={(event) => setMemory(event.currentTarget.value)} />
          </Field>
          <Field label="Storage in total">
            <Input value={storage} aria-label="Total storage" placeholder="500Gi" onChange={(event) => setStorage(event.currentTarget.value)} />
          </Field>
          <Field label="Pods in total">
            <Input type="number" min="1" value={pods} aria-label="Total pods" placeholder="200" onChange={(event) => setPods(event.currentTarget.value)} />
          </Field>
        </Group>
        <Field label="Region" description="For projects that name no region.">
          <Input value={region} aria-label="Region" placeholder="eu" onChange={(event) => setRegion(event.currentTarget.value)} />
        </Field>
        <Field label="Required node tags" description="Comma separated. Workloads run only on nodes with all of them.">
          <Input value={tags} aria-label="Required node tags" placeholder="acme" onChange={(event) => setTags(event.currentTarget.value)} />
        </Field>
        <Field label="Allowed domains" description="Comma separated. Empty allows any free hostname.">
          <Input value={domains} aria-label="Allowed domains" placeholder="acme.com, acme.dev" onChange={(event) => setDomains(event.currentTarget.value)} />
        </Field>
        <SettingsFormActions>
          <Button type="submit" loading={busy === "policy"} disabled={Boolean(busy)}>Save policy</Button>
        </SettingsFormActions>
      </Stack>
    </form>
  );
}

function EditOrganization({ org, refetch, onClose }) {
  const [displayName, setDisplayName] = useState(org.displayName);
  const [idpID, setIdpID] = useState(org.idpID);
  const [workspaceName, setWorkspaceName] = useState("");
  const [workspaceID, setWorkspaceID] = useState("");
  const [confirm, setConfirm] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [busy, run] = useRun(refetch);
  const base = `/orgs/${org.name}`;

  const save = (event) => {
    event.preventDefault();
    return run("save", `${base}/update`, { displayName: displayName.trim(), idpID: idpID.trim() }, "Saved");
  };
  const addWorkspace = async (event) => {
    event.preventDefault();
    if (await run("workspace", `${base}/workspaces/create`, { displayName: workspaceName.trim(), idpID: workspaceID.trim() }, "Workspace added")) {
      setWorkspaceName("");
      setWorkspaceID("");
    }
  };
  const remove = async () => {
    if (await run("delete", `${base}/delete`, { confirmName: confirm }, "Organization deleted")) onClose();
  };

  return (
    <Stack gap="lg">
      <form onSubmit={save}>
        <Stack gap="sm">
          <Field label="Name">
            <Input required value={displayName} aria-label="Organization name" onChange={(event) => setDisplayName(event.currentTarget.value)} />
          </Field>
          <Field label="Id at the identity provider" description="Changing it changes who can sign in.">
            <Input required value={idpID} aria-label="Id at the identity provider" onChange={(event) => setIdpID(event.currentTarget.value)} />
          </Field>
          <SettingsFormActions>
            <Button type="submit" loading={busy === "save"} disabled={Boolean(busy)}>Save</Button>
          </SettingsFormActions>
        </Stack>
      </form>

      <OrganizationIdP org={org} refetch={refetch} />

      <OrganizationPolicy key={`${org.name}:${JSON.stringify([org.maxProjects, org.quota, org.region, org.requiredTags, org.allowedDomains])}`} org={org} refetch={refetch} />

      <Switch
        checked={org.suspended}
        disabled={Boolean(busy)}
        label={org.autoCreated && org.suspended ? "Waiting for approval" : "Suspended"}
        description={org.autoCreated && org.suspended
          ? "Nobody can sign in until you turn this off."
          : "Nobody can sign in and open sessions end. Projects keep running."}
        onChange={(event) => run("suspend", `${base}/update`, { suspended: event.currentTarget.checked ? "on" : "off" }, event.currentTarget.checked ? "Organization suspended" : "Organization open again")}
      />

      <Stack gap="xs">
        <Text fw={600} size="sm">Workspaces</Text>
        <DataTable
          columns={[
            { label: "Workspace", key: "displayName" },
            { label: "Id at the provider", key: "idpID" },
            { label: "Projects", key: "projects" },
            {
              label: "",
              align: "right",
              render: (row) => (
                <Button
                  size="xs"
                  variant="outline"
                  disabled={Boolean(busy) || row.projects > 0}
                  title={row.projects > 0 ? "Move its projects first" : undefined}
                  onClick={() => run("remove", `${base}/workspaces/${row.id}/delete`, {}, "Workspace removed")}
                  aria-label={`Remove ${row.displayName}`}
                >
                  Remove
                </Button>
              ),
            },
          ]}
          rows={org.workspaces || []}
          getRowKey={(row) => row.id}
          empty="No workspaces yet."
        />
        <form onSubmit={addWorkspace}>
          <Group grow align="flex-end" gap="sm">
            <Field label="New workspace">
              <Input required value={workspaceName} aria-label="Workspace name" placeholder="Payments team" onChange={(event) => setWorkspaceName(event.currentTarget.value)} />
            </Field>
            <Field label="Id at the provider">
              <Input required value={workspaceID} aria-label="Workspace id at the provider" placeholder="team_payments" onChange={(event) => setWorkspaceID(event.currentTarget.value)} />
            </Field>
            <Button type="submit" loading={busy === "workspace"} disabled={Boolean(busy)}>Add</Button>
          </Group>
        </form>
      </Stack>

      <Stack gap="xs">
        <Text fw={600} size="sm">Delete</Text>
        {org.projects > 0 ? (
          <Muted size="sm">It still has {org.projects} {org.projects === 1 ? "project" : "projects"}. Move them first.</Muted>
        ) : null}
        <div>
          <Button type="button" variant="outline" disabled={org.projects > 0 || Boolean(busy)} onClick={() => setDeleting(true)}>Delete organization</Button>
        </div>
      </Stack>

      <Modal opened={deleting} onClose={() => { if (!busy) setDeleting(false); }} title="Delete organization" centered>
        <Stack gap="sm">
          <Text size="sm">Type <strong>{org.name}</strong> to confirm.</Text>
          <Input value={confirm} aria-label="Confirm name" onChange={(event) => setConfirm(event.currentTarget.value)} />
          <Group justify="flex-end" gap="sm">
            <Button variant="outline" disabled={Boolean(busy)} onClick={() => setDeleting(false)}>Cancel</Button>
            <Button variant="danger" loading={busy === "delete"} disabled={confirm !== org.name} onClick={remove}>Delete</Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  );
}

/**
 * The organizations of the platform. Only the platform's administrators come here. Turning organizations on, and saying how
 * the sign-in token names them, is under Sign-in.
 */
export function OrganizationsPage({ data }) {
  const mutable = canMutate(data);
  const orgs = useQuery({ queryKey: ["orgs"], queryFn: () => api("/api/orgs"), staleTime: 0, refetchOnMount: "always" });
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState(() => {
    // Coming back from a test of an organization's own provider: its settings open again, to show what the test found.
    const back = typeof window === "undefined" ? "" : new URLSearchParams(window.location.search).get("org_idp_test") || "";
    if (back) window.history.replaceState(null, "", window.location.pathname);
    return back;
  });
  const on = sessionScopes(data).length > 0;
  const items = orgs.data?.items || [];
  const editingOrg = items.find((item) => item.name === editing);

  if (orgs.isLoading) return <Muted size="sm" className="rw-settings-empty-inline">Loading organizations…</Muted>;
  if (orgs.isError) return <Muted size="sm" className="rw-settings-empty-inline">{orgs.error.message}</Muted>;

  return (
    <>
      <SettingsSection
        title="Organizations"
        flat
        actions={mutable ? <Button leftSection={<Building2 size={14} />} onClick={() => setCreating(true)}>New organization</Button> : null}
      >
        {!on ? (
          <Muted size="sm" mb="sm">Organizations are off. Everyone who can sign in sees everything.</Muted>
        ) : null}
        <DataTable
          columns={[
            {
              label: "Organization",
              render: (row) => (
                <span>
                  {row.displayName}
                  <Muted size="xs">{(row.workspaces || []).length} {(row.workspaces || []).length === 1 ? "workspace" : "workspaces"} · {row.name}</Muted>
                </span>
              ),
            },
            { label: "Provider id", key: "idpID" },
            { label: "Projects", key: "projects" },
            { label: "", align: "right", render: (row) => (row.suspended ? <Badge tone="warning">{row.autoCreated ? "Waiting for approval" : "Suspended"}</Badge> : null) },
            {
              label: "",
              align: "right",
              render: (row) => (mutable ? <Button size="xs" variant="outline" onClick={() => setEditing(row.name)} aria-label={`Edit ${row.displayName}`}>Edit</Button> : null),
            },
          ]}
          rows={items}
          getRowKey={(row) => row.name}
          empty="No organizations yet."
        />
      </SettingsSection>

      <SettingsSheet opened={creating} onClose={() => setCreating(false)} title="New organization">
        <NewOrganization refetch={orgs.refetch} onDone={() => setCreating(false)} />
      </SettingsSheet>
      <SettingsSheet opened={Boolean(editingOrg)} onClose={() => setEditing("")} title={editingOrg?.displayName || "Organization"} description={editingOrg?.name}>
        {editingOrg ? <EditOrganization key={editingOrg.name} org={editingOrg} refetch={orgs.refetch} onClose={() => setEditing("")} /> : null}
      </SettingsSheet>
    </>
  );
}
