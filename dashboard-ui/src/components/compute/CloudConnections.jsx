import { useEffect, useMemo, useState } from "react";
import { Plus } from "lucide-react";
import { action } from "@/lib/api";
import { notify } from "@/lib/notify";
import {
  absoluteTime,
  connectionUse,
  connectionWarnings,
  nameProblem,
  providerLabel,
  providerOf,
  relativeTime,
  useConnections,
  useProviders,
  useRefreshCompute,
} from "@/lib/compute";
import { Badge, Button, Checkbox, Field, FormStack, Input, Muted, Select, Textarea } from "@/components/geass-ui";
import { Alert, ConfirmDelete, Optional, Required } from "@/components/compute/ComputeBits";
import { SettingsFormActions, SettingsSection, SettingsSheet } from "@/layout/SettingsShell";

const EXTRA_PURPOSES = ["Storage", "Database"];

/** What a connection is for, as people say it. The API's words are Compute, Storage and Database. */
const PURPOSE_LABEL = { Compute: "Servers", Storage: "Buckets", Database: "Databases" };

/** The cloud's name. Amazon's machine driver is called Amazon EC2, which a connection for buckets and databases is not. */
function cloudLabel(providers, connection) {
  if (connection.provider === "AWS" && !(connection.purposes || []).includes("Compute")) return "AWS";
  return providerLabel(providers, connection.provider);
}

/**
 * Connect a cloud, or change a connection. The form is made from what the provider says it needs: its credentials, one field
 * each, secret ones as password boxes that are never filled in again. Changing a connection sends only what was typed, so
 * one secret can be replaced without knowing the others.
 */
function ConnectionForm({ providers, connection, existingNames, onDone, onCancel }) {
  const editing = Boolean(connection);
  const sorted = useMemo(() => [...providers].sort((a, b) => a.label.localeCompare(b.label)), [providers]);
  const [providerId, setProviderId] = useState(connection?.provider || "");
  const [name, setName] = useState(connection?.name || "");
  const [nameTouched, setNameTouched] = useState(editing);
  const [region, setRegion] = useState(connection?.region || "");
  const [credentials, setCredentials] = useState({});
  const [extra, setExtra] = useState((connection?.purposes || []).filter((purpose) => EXTRA_PURPOSES.includes(purpose)));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const refresh = useRefreshCompute();
  const provider = providerOf(providers, providerId);
  // The form has one place for the connection's default region, whatever the cloud. A cloud that also keeps its region among its
  // credentials (Amazon does) would show a second box for it with the same name; the region the connection names is the one used.
  const credentialFields = (provider?.fields || []).filter((field) => field.key !== "region");

  // A new connection is named after its cloud until someone types a name of their own.
  useEffect(() => {
    if (editing || nameTouched || !providerId) return;
    const base = providerId.toLowerCase();
    let candidate = base;
    for (let n = 2; existingNames.includes(candidate); n += 1) candidate = `${base}-${n}`;
    setName(candidate);
  }, [editing, nameTouched, providerId, existingNames]);

  const allowsExtra = providerId === "AWS";
  const changePurposes = allowsExtra && editing
    ? EXTRA_PURPOSES.some((purpose) => extra.includes(purpose) !== (connection.purposes || []).includes(purpose))
    : false;

  const submit = async (event) => {
    event.preventDefault();
    setError("");
    if (!provider) {
      setError("Choose a cloud.");
      return;
    }
    const typed = {};
    credentialFields.forEach((field) => {
      const value = String(credentials[field.key] || "").trim();
      if (value) typed[field.key] = value;
    });
    if (!editing) {
      const problem = nameProblem(name, "connection", 50);
      if (problem) {
        setError(problem);
        return;
      }
      const missing = credentialFields.filter((field) => !field.optional && !typed[field.key]);
      if (missing.length) {
        setError(`${provider.label} needs ${missing.map((field) => field.label).join(", ")}.`);
        return;
      }
    }
    setBusy(true);
    try {
      if (editing) {
        const body = { ...typed };
        if (region.trim() !== (connection.region || "")) body.region = region.trim();
        if (changePurposes) body.purposes = ["Compute", ...extra];
        await action(`/connections/${connection.name}/update`, body);
        const replaced = Object.keys(typed).length > 0;
        if (replaced) {
          // The new credentials are only known to work once the cloud has accepted them.
          await action(`/connections/${connection.name}/verify`).catch(() => {});
        }
        notify(replaced ? "Credentials replaced. Geass is checking them with the cloud." : "Connection saved", "success");
      } else {
        await action("/connections/create", {
          name: name.trim(),
          provider: provider.id,
          region: region.trim(),
          purposes: ["Compute", ...(allowsExtra ? extra : [])],
          ...typed,
        });
        notify(`Connected ${provider.label}. Geass is checking the credentials with the cloud.`, "success");
      }
      await refresh({ again: true });
      onDone();
    } catch (failure) {
      setError(failure.message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <FormStack onSubmit={submit} className="cp-form">
      {!editing ? (
        <Field label={<>Cloud<Required /></>} description={provider?.summary}>
          <Select
            value={providerId}
            onChange={(event) => { setProviderId(event.currentTarget.value); setCredentials({}); setError(""); }}
            aria-label="Cloud"
            required
          >
            <option value="">Choose a cloud…</option>
            {sorted.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}
          </Select>
        </Field>
      ) : (
        <p className="cp-subtitle">{provider?.label || connection.provider}. {provider?.summary || ""}</p>
      )}
      {!editing ? (
        <Field label={<>Name<Required /></>} description="Lowercase letters, numbers and hyphens.">
          <Input
            value={name}
            onChange={(event) => { setName(event.currentTarget.value); setNameTouched(true); }}
            placeholder="hetzner"
            maxLength={50}
            autoComplete="off"
            spellCheck={false}
            aria-label="Connection name"
            required
          />
        </Field>
      ) : null}
      {provider ? (
        <>
          <Field label={<>Default region<Optional /></>} description={provider.regionHelp || "Used when a pool or server does not name a region."}>
            <Input
              value={region}
              onChange={(event) => setRegion(event.currentTarget.value)}
              autoComplete="off"
              spellCheck={false}
              aria-label="Default region"
            />
          </Field>
          {credentialFields.map((field) => (
            <Field
              key={field.key}
              label={<>{field.label}{field.optional || editing ? <Optional /> : <Required />}</>}
              description={field.help}
            >
              {field.multiline ? (
                <Textarea
                  className="cp-textarea-mono"
                  rows={6}
                  value={credentials[field.key] || ""}
                  onChange={(event) => setCredentials({ ...credentials, [field.key]: event.currentTarget.value })}
                  placeholder={editing ? "Unchanged" : ""}
                  autoComplete="off"
                  spellCheck={false}
                  aria-label={field.label}
                />
              ) : (
                <Input
                  className={field.secret ? "cp-secret-input" : undefined}
                  type={field.secret ? "password" : "text"}
                  value={credentials[field.key] || ""}
                  onChange={(event) => setCredentials({ ...credentials, [field.key]: event.currentTarget.value })}
                  placeholder={editing ? "Unchanged" : ""}
                  autoComplete={field.secret ? "new-password" : "off"}
                  spellCheck={false}
                  aria-label={field.label}
                />
              )}
            </Field>
          ))}
          {allowsExtra ? (
            <Field label="Also use for">
              <div className="cp-check">
                {EXTRA_PURPOSES.map((purpose) => (
                  <Checkbox
                    key={purpose}
                    label={PURPOSE_LABEL[purpose]}
                    checked={extra.includes(purpose)}
                    onChange={(event) => setExtra(event.currentTarget.checked ? [...extra, purpose] : extra.filter((item) => item !== purpose))}
                  />
                ))}
              </div>
            </Field>
          ) : null}
        </>
      ) : null}
      {error ? <Alert tone="danger">{error}</Alert> : null}
      <SettingsFormActions>
        <Button type="submit" loading={busy} disabled={!provider}>{editing ? "Save" : "Connect"}</Button>
        <Button type="button" variant="outline" disabled={busy} onClick={onCancel}>Cancel</Button>
      </SettingsFormActions>
    </FormStack>
  );
}

/** One connection: what it is, whether the cloud accepts it, what depends on it, and what can be done to it. */
function ConnectionCard({ connection, providers, scope, mutable, canEdit, verifying, onVerify, onEdit, onRemove }) {
  const warnings = connectionWarnings(connection);
  const checking = !connection.verifiedAt && !connection.message;
  const use = connectionUse(connection);
  return (
    <li className="cp-conn">
      <div className="cp-conn-head">
        <div className="cp-conn-title">
          <strong>{connection.name}</strong>
          <span className="cp-conn-cloud">{cloudLabel(providers, connection)}</span>
          {(connection.purposes || []).map((purpose) => <Badge key={purpose}>{PURPOSE_LABEL[purpose] || purpose}</Badge>)}
        </div>
        {checking ? (
          <Badge tone="neutral">Checking</Badge>
        ) : (
          <Badge tone={connection.available ? "success" : "danger"}>{connection.available ? "Available" : "Not available"}</Badge>
        )}
      </div>
      <p className="cp-conn-line">
        {scope || (connection.project ? connection.project : "Whole cluster")}
        {connection.region ? ` · default region ${connection.region}` : ""}
        {connection.account ? ` · account ${connection.account}` : ""}
        {connection.verifiedAt ? <> · {connection.available ? "checked" : "last accepted"} <span title={absoluteTime(connection.verifiedAt)}>{relativeTime(connection.verifiedAt)}</span></> : null}
      </p>
      {!connection.available && connection.message ? <p className="cp-conn-line is-problem">{connection.message}</p> : null}
      {warnings.length ? (
        <ul className="cp-warn-list">
          {warnings.map((warning) => <li key={warning}><strong>Warning:</strong> {warning}</li>)}
        </ul>
      ) : null}
      <p className="cp-conn-line">{use ? <>Used by {use}.</> : "Nothing uses it yet."}</p>
      {mutable ? (
        <div className="cp-conn-actions">
          <Button size="sm" variant="outline" loading={verifying} onClick={onVerify} aria-label={`Verify ${connection.name} now`}>Verify now</Button>
          {canEdit ? (
            <Button size="sm" variant="outline" onClick={onEdit} aria-label={`Edit or rotate credentials of ${connection.name}`}>Edit</Button>
          ) : null}
          <Button size="sm" variant="outline" onClick={onRemove} aria-label={`Remove ${connection.name}`}>Remove</Button>
        </div>
      ) : null}
    </li>
  );
}

/**
 * The connections in `rows`, each with what can be done to it: asking the cloud again, changing its credentials, removing it.
 * `scopeOf` says whose a connection is when that is not the whole cluster.
 */
export function ConnectionList({ rows, mutable, scopeOf, empty }) {
  const providersQuery = useProviders();
  const providers = providersQuery.data || [];
  const refresh = useRefreshCompute();
  const [editing, setEditing] = useState(null);
  const [removing, setRemoving] = useState(null);
  const [removeBusy, setRemoveBusy] = useState(false);
  const [removeError, setRemoveError] = useState("");
  const [verifying, setVerifying] = useState("");

  const verify = async (connection) => {
    setVerifying(connection.name);
    try {
      await action(`/connections/${connection.name}/verify`);
      notify(`Checking ${connection.name}`, "success");
      await refresh({ again: true });
    } catch (failure) {
      notify(failure.message, "error");
    } finally {
      setVerifying("");
    }
  };

  const remove = async () => {
    setRemoveBusy(true);
    setRemoveError("");
    try {
      await action(`/connections/${removing.name}/delete`, { confirmName: removing.name });
      notify(`Removed ${removing.name}`, "success");
      setRemoving(null);
      await refresh();
    } catch (failure) {
      setRemoveError(failure.message);
    } finally {
      setRemoveBusy(false);
    }
  };

  const use = removing ? connectionUse(removing) : "";

  return (
    <>
      {rows.length ? (
        <ul className="cp-conn-list">
          {rows.map((row) => (
            <ConnectionCard
              key={row.name}
              connection={row}
              providers={providers}
              scope={scopeOf ? scopeOf(row) : undefined}
              mutable={mutable}
              canEdit={Boolean(providerOf(providers, row.provider))}
              verifying={verifying === row.name}
              onVerify={() => verify(row)}
              onEdit={() => setEditing(row)}
              onRemove={() => { setRemoveError(""); setRemoving(row); }}
            />
          ))}
        </ul>
      ) : <p className="cp-empty">{empty}</p>}

      <SettingsSheet opened={Boolean(editing)} onClose={() => setEditing(null)} title={editing ? `Edit ${editing.name}` : ""}>
        {editing ? (
          providersQuery.isError ? (
            <Alert tone="danger">{providersQuery.error.message}</Alert>
          ) : (
            <ConnectionForm
              key={editing.name}
              providers={providers}
              connection={editing}
              existingNames={rows.map((row) => row.name)}
              onDone={() => setEditing(null)}
              onCancel={() => setEditing(null)}
            />
          )
        ) : null}
      </SettingsSheet>

      <ConfirmDelete
        opened={Boolean(removing)}
        title="Remove connection?"
        name={removing?.name || ""}
        confirmLabel="Remove connection"
        busy={removeBusy}
        error={removeError}
        blocked={use ? `${removing.name} is used by ${use}. Delete those first, so their machines and data are not left behind.` : ""}
        onClose={() => setRemoving(null)}
        onConfirm={remove}
      >
        <p>
          Remove <strong>{removing?.name}</strong> and its stored credentials.
        </p>
      </ConfirmDelete>
    </>
  );
}

/** Server clouds: the connections that let Geass create machines. They belong to the cluster. */
export function CloudConnections({ mutable }) {
  const connections = useConnections();
  const providersQuery = useProviders();
  const providers = providersQuery.data || [];
  const [creating, setCreating] = useState(false);
  const rows = (connections.data || []).filter((row) => (row.purposes || []).includes("Compute"));

  return (
    <>
      <SettingsSection
        title="Server clouds"
        flat
        actions={mutable ? (
          <Button type="button" leftSection={<Plus size={15} />} onClick={() => setCreating(true)} disabled={providersQuery.isPending}>
            Connect a cloud
          </Button>
        ) : null}
      >
        {connections.isError ? <Alert tone="danger">{connections.error.message}</Alert> : null}
        {connections.isPending ? <Muted size="sm">Loading connections…</Muted> : (
          <ConnectionList rows={rows} mutable={mutable} empty="No server cloud connected." />
        )}
      </SettingsSection>

      <SettingsSheet opened={creating} onClose={() => setCreating(false)} title="Connect a server cloud">
        {creating ? (
          providersQuery.isError ? (
            <Alert tone="danger">{providersQuery.error.message}</Alert>
          ) : (
            <ConnectionForm
              key="new"
              providers={providers}
              existingNames={(connections.data || []).map((row) => row.name)}
              onDone={() => setCreating(false)}
              onCancel={() => setCreating(false)}
            />
          )
        ) : null}
      </SettingsSheet>
    </>
  );
}
