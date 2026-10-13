import { useMemo, useState } from "react";
import { Group, Menu, Modal, Stack, Text } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Braces, Database, Eye, EyeOff, HardDrive, Link2, MoreVertical, Plus, Search, X } from "lucide-react";
import { action, api, list, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { Button, Checkbox, Field, Input, Muted, Select, Switch, Textarea } from "@/components/geass-ui";

const MASK = "••••••••";
const HINT_KEY = "geass-variables-connect-hint";

function readHintDismissed() {
  try {
    return localStorage.getItem(HINT_KEY) === "1";
  } catch {
    return false;
  }
}

/** KEY=VALUE lines (dotenv style) to a plain object. Blank lines and # comments are skipped. */
export function parseEnvText(text) {
  const values = {};
  for (const rawLine of String(text || "").split("\n")) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;
    const index = line.indexOf("=");
    if (index <= 0) throw new Error(`Line "${line}" must look like KEY=value`);
    const key = line.slice(0, index).trim().replace(/^export\s+/, "");
    let value = line.slice(index + 1).trim();
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }
    if (!key) throw new Error(`Line "${line}" is missing a name`);
    values[key] = value;
  }
  return values;
}

function envText(config) {
  return Object.entries(config || {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([key, value]) => `${key}=${value}`)
    .join("\n");
}

function buildRows(payload) {
  const rows = [];
  const config = payload?.config || {};
  Object.keys(config).forEach((key) => rows.push({ id: `config:${key}`, name: key, kind: "config", value: config[key] }));
  (payload?.secrets || []).forEach((key) => rows.push({ id: `secret:${key}`, name: key, kind: "secret" }));
  (payload?.env || []).forEach((entry) => {
    rows.push({ id: `env:${entry.name}`, name: entry.name, kind: entry.kind === "secret" ? "reference" : "literal" });
  });
  (payload?.sharedVariableRefs || []).forEach((key) => rows.push({ id: `shared:${key}`, name: key, kind: "shared" }));
  return rows.sort((a, b) => a.name.localeCompare(b.name));
}

const KIND_LABEL = {
  config: "Variable",
  secret: "Secret",
  reference: "Reference",
  literal: "Variable",
  shared: "Shared",
};

export function ResourceVariables({ item, name, project, data, reload }) {
  const queryClient = useQueryClient();
  const mutable = canMutate(data);
  const environment = item.spec?.environment;
  const variables = useQuery({ queryKey: ["variables", name], queryFn: () => api(`/api/apps/${name}/variables`) });
  const [filter, setFilter] = useState("");
  const [searching, setSearching] = useState(false);
  const [adding, setAdding] = useState(false);
  const [draft, setDraft] = useState({ key: "", value: "", secret: false });
  const [revealed, setRevealed] = useState({});
  const [editing, setEditing] = useState("");
  const [editValue, setEditValue] = useState("");
  const [rawOpen, setRawOpen] = useState(false);
  const [rawText, setRawText] = useState("");
  const [sharedOpen, setSharedOpen] = useState(false);
  const [sharedRefs, setSharedRefs] = useState([]);
  const [connectOpen, setConnectOpen] = useState(false);
  const [connectTarget, setConnectTarget] = useState("");
  const [hintDismissed, setHintDismissed] = useState(readHintDismissed);
  const [busy, setBusy] = useState(false);

  const rows = useMemo(() => buildRows(variables.data), [variables.data]);
  const visibleRows = useMemo(() => {
    const query = filter.trim().toLowerCase();
    return query ? rows.filter((row) => row.name.toLowerCase().includes(query)) : rows;
  }, [rows, filter]);

  const projectShared = (project?.spec?.sharedVariables || []).filter((entry) => entry.environment === environment);
  const targets = useMemo(() => {
    const projectName = item.spec?.project;
    const sameScope = (entry) => entry.spec?.project === projectName && entry.spec?.environment === environment;
    const databases = list(data, "databases").filter(sameScope).map((entry) => ({
      value: `database:${resourceName(entry)}`,
      label: `${entry.metadata?.annotations?.["geass.dev/display-name"] || resourceName(entry)} (${entry.spec?.engine || "Database"})`,
      icon: Database,
      ready: Boolean(entry.status?.connectionSecret),
    }));
    const stores = list(data, "objectStores").filter(sameScope).map((entry) => ({
      value: `object-store:${resourceName(entry)}`,
      label: `${entry.metadata?.annotations?.["geass.dev/display-name"] || resourceName(entry)} (Bucket)`,
      icon: HardDrive,
      ready: Boolean(entry.status?.connectionSecret),
    }));
    return [...databases, ...stores];
  }, [data, item.spec?.project, environment]);

  const refresh = async () => {
    await Promise.all([variables.refetch(), reload?.()]);
  };

  const run = async (path, values, message) => {
    setBusy(true);
    try {
      await action(path, values);
      await refresh();
      if (message) alertUser(message);
      return true;
    } catch (error) {
      alertUser(error.message);
      return false;
    } finally {
      setBusy(false);
    }
  };

  const addVariable = async (event) => {
    event.preventDefault();
    const path = draft.secret ? `/apps/${name}/secrets/set` : `/apps/${name}/config/set`;
    const ok = await run(path, { key: draft.key.trim(), value: draft.value }, "Variable saved. Deploy to apply it.");
    if (ok) {
      setDraft({ key: "", value: "", secret: false });
      setAdding(false);
    }
  };

  const saveEdit = async (row) => {
    const ok = await run(`/apps/${name}/config/set`, { key: row.name, value: editValue }, "Variable saved. Deploy to apply it.");
    if (ok) setEditing("");
  };

  const removeRow = (row) => {
    if (row.kind === "config") return run(`/apps/${name}/config/delete`, { key: row.name }, "Variable removed. Deploy to apply it.");
    if (row.kind === "secret") return run(`/apps/${name}/secrets/delete`, { key: row.name }, "Secret removed. Deploy to apply it.");
    if (row.kind === "shared") {
      const next = (variables.data?.sharedVariableRefs || []).filter((ref) => ref !== row.name);
      return run(`/apps/${name}/shared-variables/save`, { sharedVariable: next }, "Shared variable detached. Deploy to apply it.");
    }
    return run(`/apps/${name}/env/delete`, { key: row.name }, "Variable removed. Deploy to apply it.");
  };

  const openRaw = () => {
    setRawText(envText(variables.data?.config));
    setRawOpen(true);
  };

  const saveRaw = async () => {
    let values;
    try {
      values = parseEnvText(rawText);
    } catch (error) {
      alertUser(error.message);
      return;
    }
    const ok = await run(`/apps/${name}/config/raw`, { configJSON: JSON.stringify(values) }, "Variables saved. Deploy to apply them.");
    if (ok) setRawOpen(false);
  };

  const openShared = () => {
    setSharedRefs(variables.data?.sharedVariableRefs || []);
    setSharedOpen(true);
  };

  const saveShared = async () => {
    const ok = await run(`/apps/${name}/shared-variables/save`, { sharedVariable: sharedRefs }, "Shared variables saved. Deploy to apply them.");
    if (ok) setSharedOpen(false);
  };

  const connect = async () => {
    const [kind, target] = connectTarget.split(":");
    if (!kind || !target) return;
    const ok = await run(`/apps/${name}/attach`, { kind, name: target }, "Connection added. Deploy to apply it.");
    if (ok) setConnectOpen(false);
  };

  const dismissHint = () => {
    setHintDismissed(true);
    try {
      localStorage.setItem(HINT_KEY, "1");
    } catch {
      /* ignore */
    }
  };

  if (!mutable) {
    return <Muted size="sm">Your viewer role cannot view or change variables.</Muted>;
  }

  return (
    <div className="rw-vars">
      <div className="rw-vars-head">
        <div className="rw-vars-count">
          <strong>{rows.length}</strong> Service {rows.length === 1 ? "Variable" : "Variables"}
          {searching ? (
            <Input
              autoFocus
              className="rw-vars-search"
              value={filter}
              placeholder="Filter variables"
              aria-label="Filter variables"
              onChange={(event) => setFilter(event.currentTarget.value)}
              onBlur={() => { if (!filter) setSearching(false); }}
            />
          ) : (
            <button type="button" className="rw-icon-btn" aria-label="Search variables" onClick={() => setSearching(true)}>
              <Search size={15} />
            </button>
          )}
        </div>
        <div className="rw-vars-actions">
          <Button variant="ghost" size="sm" leftSection={<Link2 size={14} />} onClick={openShared} disabled={!projectShared.length && !(variables.data?.sharedVariableRefs || []).length}>
            Shared Variable
          </Button>
          <Button variant="ghost" size="sm" leftSection={<Braces size={14} />} onClick={openRaw}>
            Raw Editor
          </Button>
          <Button variant="outline" size="sm" leftSection={<Plus size={14} />} onClick={() => setAdding(true)}>
            New Variable
          </Button>
        </div>
      </div>

      {!hintDismissed && targets.length ? (
        <div className="rw-vars-hint">
          <Link2 size={15} aria-hidden />
          <span>
            Trying to connect a database?{" "}
            <button type="button" className="rw-vars-link" onClick={() => { setConnectTarget(targets.find((t) => t.ready)?.value || targets[0].value); setConnectOpen(true); }}>
              Add Reference
            </button>
          </span>
          <button type="button" className="rw-icon-btn" aria-label="Dismiss" onClick={dismissHint}>
            <X size={14} />
          </button>
        </div>
      ) : null}

      {adding ? (
        <form className="rw-vars-new" onSubmit={addVariable}>
          <Input
            required
            autoFocus
            value={draft.key}
            onChange={(event) => setDraft({ ...draft, key: event.currentTarget.value })}
            placeholder="VARIABLE_NAME"
            aria-label="Variable name"
          />
          <Input
            required
            type={draft.secret ? "password" : "text"}
            value={draft.value}
            onChange={(event) => setDraft({ ...draft, value: event.currentTarget.value })}
            placeholder="value"
            aria-label="Variable value"
          />
          <Switch label="Secret" checked={draft.secret} onChange={(event) => setDraft({ ...draft, secret: event.currentTarget.checked })} />
          <Button type="submit" size="sm" loading={busy}>Add</Button>
          <Button type="button" size="sm" variant="ghost" onClick={() => { setAdding(false); setDraft({ key: "", value: "", secret: false }); }}>Cancel</Button>
        </form>
      ) : null}

      <div className="rw-vars-list">
        {variables.isPending ? <Muted size="sm" className="rw-vars-empty">Loading variables…</Muted> : null}
        {variables.isError ? <Muted size="sm" className="rw-vars-empty">{variables.error.message}</Muted> : null}
        {!variables.isPending && !variables.isError && !visibleRows.length ? (
          <Muted size="sm" className="rw-vars-empty">
            {rows.length ? "No variables match that filter." : "No variables yet. Add one, or connect a database or bucket."}
          </Muted>
        ) : null}
        {visibleRows.map((row) => {
          const canReveal = row.kind === "config";
          const isRevealed = Boolean(revealed[row.id]);
          const isEditing = editing === row.id;
          return (
            <div className="rw-vars-row" key={row.id}>
              <Braces size={14} className="rw-vars-row-icon" aria-hidden />
              <code className="rw-vars-name">{row.name}</code>
              <span className="rw-vars-kind">{KIND_LABEL[row.kind]}</span>
              {isEditing ? (
                <form
                  className="rw-vars-edit"
                  onSubmit={(event) => { event.preventDefault(); saveEdit(row); }}
                >
                  <Input autoFocus value={editValue} onChange={(event) => setEditValue(event.currentTarget.value)} aria-label={`Value for ${row.name}`} />
                  <Button type="submit" size="xs" loading={busy}>Save</Button>
                  <Button type="button" size="xs" variant="ghost" onClick={() => setEditing("")}>Cancel</Button>
                </form>
              ) : (
                <code className="rw-vars-value">{canReveal && isRevealed ? row.value : MASK}</code>
              )}
              {canReveal && !isEditing ? (
                <button
                  type="button"
                  className="rw-icon-btn"
                  aria-label={isRevealed ? `Hide ${row.name}` : `Show ${row.name}`}
                  onClick={() => setRevealed({ ...revealed, [row.id]: !isRevealed })}
                >
                  {isRevealed ? <EyeOff size={14} /> : <Eye size={14} />}
                </button>
              ) : null}
              <Menu position="bottom-end" withinPortal>
                <Menu.Target>
                  <button type="button" className="rw-icon-btn" aria-label={`Actions for ${row.name}`}>
                    <MoreVertical size={15} />
                  </button>
                </Menu.Target>
                <Menu.Dropdown>
                  {row.kind === "config" ? (
                    <Menu.Item onClick={() => { setEditing(row.id); setEditValue(row.value || ""); }}>Edit</Menu.Item>
                  ) : null}
                  <Menu.Item color="red" onClick={() => removeRow(row)}>
                    {row.kind === "shared" ? "Detach" : "Delete"}
                  </Menu.Item>
                </Menu.Dropdown>
              </Menu>
            </div>
          );
        })}
      </div>
      <Muted size="xs" className="rw-vars-foot">Variable changes are applied the next time you deploy. Secret values are never shown.</Muted>

      <Modal opened={rawOpen} onClose={() => setRawOpen(false)} title="Raw Editor" centered size="lg">
        <Stack gap="sm">
          <Text size="sm" c="dimmed">
            One KEY=value per line. Saving replaces all plain variables for this service. Secrets are managed separately and are not shown here.
          </Text>
          <Textarea rows={12} value={rawText} onChange={(event) => setRawText(event.currentTarget.value)} spellCheck={false} aria-label="Raw variables" />
          <Group justify="flex-end" gap="sm">
            <Button variant="outline" onClick={() => setRawOpen(false)}>Cancel</Button>
            <Button loading={busy} onClick={saveRaw}>Update variables</Button>
          </Group>
        </Stack>
      </Modal>

      <Modal opened={sharedOpen} onClose={() => setSharedOpen(false)} title="Shared variables" centered>
        <Stack gap="sm">
          <Text size="sm" c="dimmed">Reference variables defined in project settings for the {environment} environment.</Text>
          {projectShared.length ? (
            <Checkbox.Group value={sharedRefs} onChange={setSharedRefs}>
              <Stack gap="xs">
                {projectShared.map((entry) => (
                  <Checkbox key={entry.name} value={entry.name} label={`${entry.name}${entry.secretRef ? " (secret)" : ""}`} />
                ))}
              </Stack>
            </Checkbox.Group>
          ) : (
            <Muted size="sm">No shared variables in this environment yet. Add them under Project Settings.</Muted>
          )}
          <Group justify="flex-end" gap="sm">
            <Button variant="outline" onClick={() => setSharedOpen(false)}>Cancel</Button>
            <Button loading={busy} onClick={saveShared}>Save</Button>
          </Group>
        </Stack>
      </Modal>

      <Modal opened={connectOpen} onClose={() => setConnectOpen(false)} title="Add reference" centered>
        <Stack gap="sm">
          <Text size="sm" c="dimmed">
            Adds the connection as environment variables: DATABASE_URL or REDIS_URL for a database, and S3_ENDPOINT, S3_ACCESS_KEY_ID, S3_SECRET_ACCESS_KEY and S3_BUCKET for a bucket.
          </Text>
          <Field label="Resource">
            <Select value={connectTarget} onChange={(event) => setConnectTarget(event.currentTarget.value)} aria-label="Resource to connect">
              {targets.map((target) => (
                <option key={target.value} value={target.value} disabled={!target.ready}>
                  {target.label}{target.ready ? "" : " — not ready"}
                </option>
              ))}
            </Select>
          </Field>
          <Group justify="flex-end" gap="sm">
            <Button variant="outline" onClick={() => setConnectOpen(false)}>Cancel</Button>
            <Button loading={busy} disabled={!targets.find((target) => target.value === connectTarget)?.ready} onClick={connect}>Add reference</Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  );
}
