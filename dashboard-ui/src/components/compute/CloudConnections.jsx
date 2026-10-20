import { useEffect, useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
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
import { Alert, Callout, ConfirmDelete, Optional, Required } from "@/components/compute/ComputeBits";
import { SettingsFormActions, SettingsSection, SettingsSheet } from "@/layout/SettingsShell";

const EXTRA_PURPOSES = ["Storage", "Database"];

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
    provider.fields.forEach((field) => {
      const value = String(credentials[field.key] || "").trim();
      if (value) typed[field.key] = value;
    });
    if (!editing) {
      const problem = nameProblem(name, "connection", 50);
      if (problem) {
        setError(problem);
        return;
      }
      const missing = provider.fields.filter((field) => !field.optional && !typed[field.key]);
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
      {editing ? (
        <p className="rw-rail-body">
          Change the default region or replace credentials. Leave a credential empty to keep the one that is stored: only what you type is sent.
        </p>
      ) : (
        <p className="rw-rail-body">
          Geass uses a connection to create machines at the cloud, so pools can add nodes and servers can be made from the dashboard. Machines are created in
          your account and the cloud bills you for them.
        </p>
      )}
      {!editing ? (
        <Field label={<>Cloud<Required /></>} description={provider?.summary || "Choose the cloud to create machines at."}>
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
        <Field label={<>Name<Required /></>} description="Lowercase letters, numbers and hyphens. Pools and servers refer to the connection by this name.">
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
          {provider.fields.map((field) => (
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
              {field.secret ? <span className="cp-credential-note">Stored in a Secret that only the Geass operator reads. It is never shown again.</span> : null}
            </Field>
          ))}
          {allowsExtra ? (
            <Field
              label="Use this connection for"
              description="It always creates machines. For AWS it can also hold buckets (Storage) and databases (Database)."
            >
              <div className="cp-check">
                {EXTRA_PURPOSES.map((purpose) => (
                  <Checkbox
                    key={purpose}
                    label={purpose}
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
function ConnectionCard({ connection, providers, mutable, canEdit, verifying, onVerify, onEdit, onRemove }) {
  const warnings = connectionWarnings(connection);
  const checking = !connection.verifiedAt && !connection.message;
  const use = connectionUse(connection);
  return (
    <li className="cp-conn">
      <div className="cp-conn-head">
        <div className="cp-conn-title">
          <strong>{connection.name}</strong>
          <span className="cp-conn-cloud">{providerLabel(providers, connection.provider)}</span>
          {(connection.purposes || []).map((purpose) => <Badge key={purpose}>{purpose}</Badge>)}
        </div>
        {checking ? (
          <Badge tone="neutral">Checking</Badge>
        ) : (
          <Badge tone={connection.available ? "success" : "danger"}>{connection.available ? "Available" : "Not available"}</Badge>
        )}
      </div>
      <p className="cp-conn-line">
        {connection.project ? `Project ${connection.project}` : "Whole cluster"}
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

/** Connections to clouds: what each is for, whether the cloud accepts it, what depends on it. */
export function CloudConnections({ mutable }) {
  const connections = useConnections();
  const providersQuery = useProviders();
  const providers = providersQuery.data || [];
  const refresh = useRefreshCompute();
  const [sheet, setSheet] = useState(null);
  const [removing, setRemoving] = useState(null);
  const [removeBusy, setRemoveBusy] = useState(false);
  const [removeError, setRemoveError] = useState("");
  const [verifying, setVerifying] = useState("");
  const rows = connections.data || [];

  const verify = async (connection) => {
    setVerifying(connection.name);
    try {
      await action(`/connections/${connection.name}/verify`);
      notify(`Asking the cloud about ${connection.name} again`, "success");
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
      notify(`Removed the connection ${removing.name}`, "success");
      setRemoving(null);
      await refresh();
    } catch (failure) {
      setRemoveError(failure.message);
    } finally {
      setRemoveBusy(false);
    }
  };

  const canRotate = (connection) => Boolean(providerOf(providers, connection.provider));
  const use = removing ? connectionUse(removing) : "";

  return (
    <>
      <SettingsSection
        title="Cloud connections"
        flat
        actions={mutable ? (
          <Button type="button" leftSection={<Plus size={15} />} onClick={() => setSheet({ mode: "create" })} disabled={providersQuery.isPending}>
            Connect a cloud
          </Button>
        ) : null}
      >
        <p className="rw-rail-body">
          Connections to clouds. Geass creates machines at a cloud with a connection, for the <Link to="/pools">pools</Link> that add nodes when the cluster is
          full and for servers added one at a time. The credentials are kept in a Secret that only the operator reads.
        </p>
        {connections.isError ? <Alert tone="danger">{connections.error.message}</Alert> : null}
        {connections.isPending ? <Muted size="sm">Loading connections…</Muted> : rows.length ? (
          <ul className="cp-conn-list">
            {rows.map((row) => (
              <ConnectionCard
                key={row.name}
                connection={row}
                providers={providers}
                mutable={mutable}
                canEdit={canRotate(row)}
                verifying={verifying === row.name}
                onVerify={() => verify(row)}
                onEdit={() => setSheet({ mode: "edit", connection: row })}
                onRemove={() => { setRemoveError(""); setRemoving(row); }}
              />
            ))}
          </ul>
        ) : (
          <p className="cp-empty">
            No cloud is connected. {mutable ? "Connect one to let Geass create machines and add nodes when the cluster is full." : "An admin can connect one."}
          </p>
        )}
      </SettingsSection>

      <SettingsSheet
        opened={Boolean(sheet)}
        onClose={() => setSheet(null)}
        title={sheet?.mode === "edit" ? `Edit ${sheet.connection.name}` : "Connect a cloud"}
      >
        {sheet ? (
          providersQuery.isError ? (
            <Alert tone="danger">{providersQuery.error.message}</Alert>
          ) : (
            <ConnectionForm
              key={sheet.mode === "edit" ? sheet.connection.name : "new"}
              providers={providers}
              connection={sheet.connection}
              existingNames={rows.map((row) => row.name)}
              onDone={() => setSheet(null)}
              onCancel={() => setSheet(null)}
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
        blocked={use ? `${removing.name} is used by ${use}. Delete those first, so that their machines are deleted at the cloud while the connection can still do it.` : ""}
        onClose={() => setRemoving(null)}
        onConfirm={remove}
      >
        <p>
          Removing <strong>{removing?.name}</strong> deletes the connection and the credentials stored for it. Geass can no longer create
          machines there. It cannot be undone, but you can connect the cloud again.
        </p>
        {!use ? (
          <Callout>
            A connection is refused while a pool or a machine uses it: the machines could no longer be deleted at the cloud, and would keep billing you.
          </Callout>
        ) : null}
      </ConfirmDelete>
    </>
  );
}
