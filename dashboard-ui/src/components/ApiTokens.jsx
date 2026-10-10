import { useState } from "react";
import { Group, Menu, Modal, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { Copy, KeyRound, MoreVertical, Plus } from "lucide-react";
import { action, api, resourceDisplayName, resourceName } from "@/lib/api";
import { alertUser } from "@/lib/notify";
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
  { value: "admin", label: "Full access" },
];

function describeToken(token, projects) {
  if (token.scope === "project") {
    const project = projects.find((item) => resourceName(item) === token.project);
    return `Project ${project ? project.spec?.displayName || resourceDisplayName(project) : token.project} · ${token.environment}`;
  }
  return `Workspace · ${token.role === "admin" ? "Full access" : "Read only"}`;
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
export function ApiTokens({ scope, project, projects = [], mutable }) {
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
  const [editing, setEditing] = useState(null); // null | { name, environment, role, expires }
  const [created, setCreated] = useState(null); // { token, item }
  const [revoking, setRevoking] = useState(null);
  const [busy, setBusy] = useState(false);

  const items = (tokens.data?.items || []).filter((item) => scope === "workspace" || item.scope === "project");

  const create = async (event) => {
    event.preventDefault();
    setBusy(true);
    try {
      const result = await action("/tokens/create", {
        name: editing.name,
        scope,
        expiresInDays: editing.expires,
        ...(scope === "project" ? { project: projectName, environment: editing.environment } : { role: editing.role }),
      });
      setEditing(null);
      setCreated(result);
      await tokens.refetch();
    } catch (error) {
      alertUser(error.message);
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
      alertUser("Token revoked");
    } catch (error) {
      alertUser(error.message);
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

  const origin = typeof window === "undefined" ? "https://geass.example.com" : window.location.origin;
  const usage = created?.item?.scope === "project"
    ? `export GEASS_TOKEN=${created.token}\ncurl -X POST -H "Authorization: Bearer $GEASS_TOKEN" ${origin}/api/token/deploy`
    : `export GEASS_TOKEN=${created?.token}\ncurl -H "Authorization: Bearer $GEASS_TOKEN" ${origin}/api/bootstrap`;

  return (
    <div className="rw-tokens">
      <p className="rw-rail-body">
        {scope === "project"
          ? "A project token can deploy, read logs, and edit variables in one environment of this project, and nothing else. Use it in CI."
          : "A workspace token acts like a dashboard user across every project. Prefer a project token when a script only needs to deploy."}
      </p>

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
                  {describeToken(token, projects)} · Created {formatRelativeTime(token.createdAt)}
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
            onClick={() => setEditing({ name: "", environment: environments[0] || "", role: "viewer", expires: "90" })}
          >
            New token
          </Button>
        </div>
      ) : null}

      <Modal opened={Boolean(editing)} onClose={() => { if (!busy) setEditing(null); }} title="New API token" centered>
        {editing ? (
          <form onSubmit={create}>
            <Stack gap="sm">
              <Field label="Name" description="Say where it is used, for example github-actions.">
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
                <Field label="Environment" description="The token can only touch services in this environment.">
                  <Select
                    value={editing.environment}
                    aria-label="Token environment"
                    onChange={(event) => setEditing({ ...editing, environment: event.target.value })}
                  >
                    {environments.map((env) => <option key={env}>{env}</option>)}
                  </Select>
                </Field>
              ) : (
                <Field label="Access" description="Read only can view everything but change nothing.">
                  <Select
                    value={editing.role}
                    aria-label="Token access"
                    onChange={(event) => setEditing({ ...editing, role: event.target.value })}
                  >
                    {ROLE_OPTIONS.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
                  </Select>
                </Field>
              )}
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
            Copy this token now. It is shown only once; if you lose it, revoke it and create another.
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
          Anything using <strong>{revoking?.name}</strong> stops working immediately. This cannot be undone.
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="outline" disabled={busy} onClick={() => setRevoking(null)}>Cancel</Button>
          <Button variant="danger" loading={busy} onClick={revoke}>Revoke token</Button>
        </Group>
      </Modal>
    </div>
  );
}
