import { useMemo, useState } from "react";
import { Group, Menu, Modal, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Copy, KeyRound, MoreVertical, Plus } from "lucide-react";
import { action, api, resourceDisplayName, resourceName } from "@/lib/api";
import { dashboardSession, isPlatformSession, sessionScopes } from "@/lib/dashboard";
import { alertUser, alertError } from "@/lib/notify";
import { publicOrigin } from "@/lib/transport";
import { formatRelativeTime } from "@/lib/services";
import { Button, Field, Input, Muted, Select } from "@/components/geass-ui";

const EXPIRY_OPTIONS = [
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
  { value: "365", label: "1 year" },
  { value: "0", label: "No expiration" },
];

const ROLE_OPTIONS = [
  { value: "viewer", label: "Read only" },
  { value: "observer", label: "Read only, with logs" },
  { value: "admin", label: "Full access" },
];

const ROLE_LABELS = { admin: "Full access", observer: "Read only, with logs", viewer: "Read only" };

/** Where a token acts, when the platform has organizations: its organization (and workspace), or the whole install. */
function placeOf(token, names) {
  if (!names) return "";
  if (!token.org) return " · Whole platform";
  const org = names.orgs[token.org] || token.org;
  return ` · ${token.workspace ? `${org} / ${names.workspaces[token.workspace] || token.workspace}` : org}`;
}

function describeToken(token, projects, names) {
  if (token.scope === "service") {
    return `Deploy hook · ${token.app} · ${token.environment}`;
  }
  if (token.scope === "project") {
    const project = projects.find((item) => resourceName(item) === token.project);
    return `Project ${project ? project.spec?.displayName || resourceDisplayName(project) : token.project} · ${token.environment}`;
  }
  return `Workspace · ${ROLE_LABELS[token.role] || "Read only"}${placeOf(token, names)}`;
}

/**
 * The places a new token may act in, with what each is called. A platform administrator may pick any organization, or the
 * whole platform; a person who works in all of an organization may narrow a token to one of its workspaces; a person who
 * works in one workspace has no choice, so there is nothing to pick.
 */
function usePlaces({ data, enabled }) {
  const scopes = sessionScopes(data);
  const platform = isPlatformSession(data);
  const session = dashboardSession(data);
  const orgs = useQuery({ queryKey: ["orgs"], queryFn: () => api("/api/orgs"), enabled: enabled && platform && scopes.length > 0 });
  return useMemo(() => {
    if (!scopes.length) return { names: null, options: [] };
    const names = { orgs: {}, workspaces: {} };
    const options = [];
    if (platform) {
      options.push({ value: "", label: "The whole platform" });
      for (const org of orgs.data?.items || []) {
        names.orgs[org.name] = org.displayName;
        options.push({ value: `${org.name}|`, label: org.displayName });
        for (const workspace of org.workspaces || []) {
          names.workspaces[workspace.id] = workspace.displayName;
          options.push({ value: `${org.name}|${workspace.id}`, label: `${org.displayName} / ${workspace.displayName}` });
        }
      }
      return { names, options };
    }
    for (const scope of scopes) {
      names.orgs[scope.org] = scope.orgName || scope.org;
      if (scope.workspace) names.workspaces[scope.workspace] = scope.workspaceName || scope.workspace;
    }
    if (!session.workspace) {
      options.push({ value: "", label: `All of ${names.orgs[session.org] || "your organization"}` });
      for (const scope of scopes) {
        if (scope.org === session.org && scope.workspace) options.push({ value: `${scope.org}|${scope.workspace}`, label: scope.workspaceName || scope.workspace });
      }
    }
    return { names, options };
  }, [scopes, platform, session.org, session.workspace, orgs.data]);
}

/** The form fields that say where a token acts, from a picked place "org|workspace". Empty is the maker's own. */
function placeFields(place) {
  if (!place) return {};
  const [org, workspace] = place.split("|");
  return { org, ...(workspace ? { workspace } : {}) };
}

function expiryLabel(token) {
  if (!token.expiresAt) return "No expiration";
  if (token.expired) return "Expired";
  return `Expires ${formatRelativeTime(token.expiresAt)}`;
}

/**
 * API tokens let scripts, CI, and the CLI call Geass without a browser login.
 * scope "project" manages tokens limited to one environment of the project; scope "workspace" manages
 * tokens that act like a dashboard user and lists every token in the workspace.
 */
export function ApiTokens({ scope, project, projects = [], mutable, data }) {
  const projectName = project ? resourceName(project) : "";
  const environments = project?.spec?.environments || [];
  const queryKey = ["api-tokens", projectName || "all"];
  const tokens = useQuery({
    queryKey,
    queryFn: () => api(projectName ? `/api/tokens?project=${encodeURIComponent(projectName)}` : "/api/tokens"),
    enabled: mutable,
    staleTime: 0,
    refetchOnMount: "always",
  });
  const places = usePlaces({ data, enabled: mutable && scope === "workspace" });
  const [editing, setEditing] = useState(null); // null | { name, environment, role, expires, place }
  const [created, setCreated] = useState(null); // { token, item }
  const [revoking, setRevoking] = useState(null);
  const [busy, setBusy] = useState(false);

  const items = (tokens.data?.items || []).filter((item) => scope === "workspace" || item.scope === "project" || item.scope === "service");

  const create = async (event) => {
    event.preventDefault();
    setBusy(true);
    try {
      const result = await action("/tokens/create", {
        name: editing.name,
        scope,
        expiresInDays: editing.expires,
        ...(scope === "project" ? { project: projectName, environment: editing.environment } : { role: editing.role, ...placeFields(editing.place) }),
      });
      setEditing(null);
      setCreated(result);
      await tokens.refetch();
    } catch (error) {
      alertError(error.message);
    } finally {
      setBusy(false);
    }
  };

  const revoke = async () => {
    setBusy(true);
    try {
      await action("/tokens/revoke", { id: revoking.id });
      setRevoking(null);
      await tokens.refetch();
      alertUser("Removed token");
    } catch (error) {
      alertError(error.message);
    } finally {
      setBusy(false);
    }
  };

  const copy = (text, message) => {
    navigator.clipboard?.writeText(text).then(
      () => alertUser(message),
      () => alertUser("Could not copy"),
    );
  };

  const origin = publicOrigin() || "https://geass.example.com";
  const usage = created?.item?.scope === "project"
    ? `export GEASS_TOKEN=${created.token}\ncurl -X POST -H "Authorization: Bearer $GEASS_TOKEN" ${origin}/api/token/deploy`
    : `export GEASS_TOKEN=${created?.token}\ncurl -H "Authorization: Bearer $GEASS_TOKEN" ${origin}/api/bootstrap`;

  return (
    <div className="rw-tokens">
      {!mutable ? (
        <Muted size="sm" className="rw-settings-empty-inline">Only admins can manage API tokens.</Muted>
      ) : tokens.isLoading ? (
        <Muted size="sm" className="rw-settings-empty-inline">Loading tokens…</Muted>
      ) : tokens.isError ? (
        <Muted size="sm" className="rw-settings-empty-inline">{tokens.error.message}</Muted>
      ) : items.length ? (
        <div className="rw-rail-stack">
          {items.map((token) => (
            <div className={`rw-token-row${token.expired ? " is-expired" : ""}`} key={token.id}>
              <KeyRound size={16} aria-hidden="true" className="rw-webhook-icon" />
              <div className="rw-webhook-main">
                <div className="rw-webhook-url">
                  <span>{token.name}</span>
                  {token.expired ? <span className="rw-vars-kind">Expired</span> : null}
                </div>
                <div className="rw-webhook-meta">
                  {describeToken(token, projects, places.names)} · Created {formatRelativeTime(token.createdAt)}
                  {token.createdBy ? ` by ${token.createdBy}` : ""} ·{" "}
                  {token.lastUsedAt ? `Last used ${formatRelativeTime(token.lastUsedAt)}` : "Never used"} · {expiryLabel(token)}
                </div>
              </div>
              <Menu position="bottom-end" withinPortal>
                <Menu.Target>
                  <button type="button" className="rw-icon-btn" aria-label={`Actions for ${token.name}`}>
                    <MoreVertical size={15} />
                  </button>
                </Menu.Target>
                <Menu.Dropdown>
                  <Menu.Item color="red" onClick={() => setRevoking(token)}>Revoke</Menu.Item>
                </Menu.Dropdown>
              </Menu>
            </div>
          ))}
        </div>
      ) : (
        <Muted size="sm" className="rw-settings-empty-inline">No tokens yet.</Muted>
      )}

      {mutable ? (
        <div className="rw-webhook-actions">
          <Button
            leftSection={<Plus size={14} />}
            onClick={() => setEditing({ name: "", environment: environments[0] || "", role: "viewer", expires: "90", place: "" })}
          >
            New token
          </Button>
        </div>
      ) : null}

      <Modal opened={Boolean(editing)} onClose={() => { if (!busy) setEditing(null); }} title="New API token" centered>
        {editing ? (
          <form onSubmit={create}>
            <Stack gap="sm">
              <Field label="Name">
                <Input
                  required
                  autoFocus
                  maxLength={60}
                  value={editing.name}
                  aria-label="Token name"
                  onChange={(event) => setEditing({ ...editing, name: event.currentTarget.value })}
                />
              </Field>
              {scope === "project" ? (
                <Field label="Environment" description="The token works in this environment only.">
                  <Select
                    value={editing.environment}
                    aria-label="Token environment"
                    onChange={(event) => setEditing({ ...editing, environment: event.target.value })}
                  >
                    {environments.map((env) => <option key={env}>{env}</option>)}
                  </Select>
                </Field>
              ) : (
                <Field label="Access" description="With logs also reads logs and variable names.">
                  <Select
                    value={editing.role}
                    aria-label="Token access"
                    onChange={(event) => setEditing({ ...editing, role: event.target.value })}
                  >
                    {ROLE_OPTIONS.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                  </Select>
                </Field>
              )}
              {scope === "workspace" && places.options.length > 1 ? (
                <Field label="Acts in">
                  <Select
                    value={editing.place}
                    aria-label="Token scope"
                    onChange={(event) => setEditing({ ...editing, place: event.target.value })}
                  >
                    {places.options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                  </Select>
                </Field>
              ) : null}
              <Field label="Expiration">
                <Select
                  value={editing.expires}
                  aria-label="Token expiration"
                  onChange={(event) => setEditing({ ...editing, expires: event.target.value })}
                >
                  {EXPIRY_OPTIONS.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                </Select>
              </Field>
              <Group justify="flex-end" gap="sm">
                <Button type="button" variant="outline" disabled={busy} onClick={() => setEditing(null)}>Cancel</Button>
                <Button type="submit" loading={busy}>Create token</Button>
              </Group>
            </Stack>
          </form>
        ) : null}
      </Modal>

      <Modal opened={Boolean(created)} onClose={() => setCreated(null)} title="Your new token" centered size="lg">
        <Stack gap="sm">
          <Text size="sm">
            Copy this token now. It is shown only once.
          </Text>
          <div className="rw-webhook-secret">
            <code>{created?.token}</code>
            <button type="button" className="rw-icon-btn" aria-label="Copy token" onClick={() => copy(created.token, "Token copied")}>
              <Copy size={15} />
            </button>
          </div>
          <Text size="xs" c="dimmed">Send it as a Bearer token:</Text>
          <pre className="rw-token-usage">{usage}</pre>
          <Group justify="flex-end"><Button onClick={() => setCreated(null)}>Done</Button></Group>
        </Stack>
      </Modal>

      <Modal opened={Boolean(revoking)} onClose={() => { if (!busy) setRevoking(null); }} title="Revoke token?" centered>
        <Text size="sm" mb="md">
          Anything using <strong>{revoking?.name}</strong> stops working immediately.
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={busy} onClick={() => setRevoking(null)}>Cancel</Button>
          <Button variant="danger" loading={busy} onClick={revoke}>Revoke token</Button>
        </Group>
      </Modal>
    </div>
  );
}
