import { useState } from "react";
import { Group, Modal, Text } from "@mantine/core";
import { Link } from "@tanstack/react-router";
import { action, resourceName } from "@/lib/api";
import { alertUser } from "@/lib/notify";
import { availableConnections } from "@/lib/project-resources";
import { Button, Field, Muted, Select } from "@/components/geass-ui";
import { SettingsFormActions, SettingsPanel } from "@/layout/SettingsShell";

/**
 * Where a project keeps its database backups: the current choice, and a form to choose or change it.
 * Choosing storage takes effect immediately; it does not wait for a deploy.
 */
export function BackupStorageChooser({ project, data, reload, mutable = true }) {
  const name = resourceName(project);
  const current = project?.spec?.backupStorage || null;
  const connections = availableConnections(data, "AWS", name);
  const [editing, setEditing] = useState(false);
  const [choice, setChoice] = useState("");
  const [removing, setRemoving] = useState(false);
  const [busy, setBusy] = useState(false);

  const names = connections.map((entry) => resourceName(entry));
  const selected = names.includes(choice)
    ? choice
    : names.includes(current?.connectionRef?.name)
      ? current.connectionRef.name
      : names[0] || "";

  const save = (event) => {
    event.preventDefault();
    if (!selected) return;
    setBusy(true);
    action(`/projects/${name}/backup-storage`, { connection: selected })
      .then(() => {
        reload?.();
        setEditing(false);
        alertUser("Backup storage saved");
      })
      .catch((error) => alertUser(error.message))
      .finally(() => setBusy(false));
  };

  const remove = () => {
    setBusy(true);
    action(`/projects/${name}/backup-storage`, { remove: "on" })
      .then(() => {
        reload?.();
        setRemoving(false);
        setEditing(false);
        alertUser("Backup storage removed");
      })
      .catch((error) => alertUser(error.message))
      .finally(() => setBusy(false));
  };

  if (current && !editing) {
    return (
      <div className="rw-backup-storage">
        <SettingsPanel>
          <div className="rw-settings-usage-lines">
            <div className="rw-settings-usage-line">
              <span>AWS connection</span>
              <strong>{current.connectionRef?.name || "—"}</strong>
            </div>
            {current.bucket ? (
              <div className="rw-settings-usage-line">
                <span>Bucket</span>
                <strong>{current.bucket}</strong>
              </div>
            ) : null}
            {current.region ? (
              <div className="rw-settings-usage-line">
                <span>Region</span>
                <strong>{current.region}</strong>
              </div>
            ) : null}
          </div>
        </SettingsPanel>
        {mutable ? (
          <SettingsFormActions>
            <Button type="button" variant="outline" onClick={() => setEditing(true)}>Change</Button>
            <Button type="button" variant="outline" onClick={() => setRemoving(true)}>Remove</Button>
          </SettingsFormActions>
        ) : null}
        <Modal opened={removing} onClose={() => { if (!busy) setRemoving(false); }} title="Remove backup storage?" centered>
          <Text size="sm" mb="md">
            Backups that are already stored stay where they are. Databases that do not have backups yet cannot start
            them until you choose storage again.
          </Text>
          <Group justify="flex-end" gap="sm">
            <Button type="button" variant="outline" disabled={busy} onClick={() => setRemoving(false)}>Cancel</Button>
            <Button type="button" variant="danger" loading={busy} onClick={remove}>Remove storage</Button>
          </Group>
        </Modal>
      </div>
    );
  }

  if (!mutable) {
    return <Muted size="sm">Only people who can edit this project can see or change where backups are stored.</Muted>;
  }

  if (!connections.length) {
    return (
      <div className="rw-backup-storage">
        <p className="rw-rail-body">
          <strong>Connect an AWS account first.</strong> Backups are stored in your own AWS account. Add an AWS
          connection for this project in <Link to="/settings/connectors" className="rw-settings-text-btn">Connectors</Link>,
          then choose it here.
        </p>
        {editing ? (
          <SettingsFormActions>
            <Button type="button" variant="outline" onClick={() => setEditing(false)}>Cancel</Button>
          </SettingsFormActions>
        ) : null}
      </div>
    );
  }

  return (
    <form className="rw-rail-form rw-backup-storage" onSubmit={save}>
      <Field
        label="AWS connection"
        description={current ? "Databases that already have backups keep using their current storage." : undefined}
      >
        <Select value={selected} onChange={(event) => setChoice(event.currentTarget.value)} aria-label="AWS connection">
          {names.map((entry) => <option key={entry} value={entry}>{entry}</option>)}
        </Select>
      </Field>
      <p className="rw-rail-body">
        Geass will create a private bucket in this AWS account and a user that can only access it.
      </p>
      <SettingsFormActions>
        <Button type="submit" loading={busy}>Use this connection</Button>
        {editing ? <Button type="button" variant="outline" disabled={busy} onClick={() => setEditing(false)}>Cancel</Button> : null}
      </SettingsFormActions>
    </form>
  );
}
