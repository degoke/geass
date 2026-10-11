import { useEffect, useState } from "react";
import { History } from "lucide-react";
import { action, list, resourceName } from "@/lib/api";
import { alertUser } from "@/lib/notify";
import { formatRelativeTime } from "@/lib/services";
import {
  archiveForDatabase,
  backupSpec,
  backupStateBadge,
  canRestoreFrom,
  formatDateTime,
  formatWhen,
  isSnapshotDatabase,
  parseTime,
  recoveryWindow,
  restorePoints,
  retentionLabel,
  retentionOptions,
  toLocalInputValue,
} from "@/lib/backups";
import { BackupStorageChooser } from "@/components/BackupStorageChooser";
import { RestoreDatabaseDialog } from "@/components/RestoreDatabaseDialog";
import { Badge, Button, Field, FormStack, Input, Muted, Select, Switch } from "@/components/geass-ui";
import { ResourceSettingsBlock } from "@/layout/ResourceSettingsLayout";
import { SettingsAlert, SettingsFormActions, SettingsPanel } from "@/layout/SettingsShell";

const MINUTE = 60 * 1000;
const VISIBLE_POINTS = 5;

/** Backup status, settings and restore for one Postgres database. Rendered inside its settings section. */
export function DatabaseBackups({ item, name, data, reload }) {
  const settings = backupSpec(item);
  const status = item.status?.backup || null;
  const project = list(data, "projects").find((entry) => resourceName(entry) === item.spec?.project);
  const storage = project?.spec?.backupStorage || null;
  const archive = archiveForDatabase(data, item);
  const points = restorePoints(archive);
  const snapshot = isSnapshotDatabase(item);
  // Snapshot backups have no moments between copies to restore to.
  const recovery = snapshot ? null : recoveryWindow(archive);

  const [enabled, setEnabled] = useState(settings.enabled);
  const [days, setDays] = useState(String(settings.retentionDays));
  const [saving, setSaving] = useState(false);
  const [timeValue, setTimeValue] = useState("");
  const [showAll, setShowAll] = useState(false);
  const [restore, setRestore] = useState(null); // null | { target }

  useEffect(() => {
    setEnabled(settings.enabled);
    setDays(String(settings.retentionDays));
  }, [settings.enabled, settings.retentionDays]);

  const dirty = enabled !== settings.enabled || Number(days) !== settings.retentionDays;
  const badge = status ? backupStateBadge(status.state) : null;
  const lastBackup = archive?.status?.lastBackupTime || status?.lastBackupTime;
  const since = archive?.status?.recoveryWindowStart || status?.recoveryWindowStart;
  const needsStorage = status?.state === "NeedsStorage" || (settings.enabled && !storage);
  const restorable = canRestoreFrom(archive);
  const shownPoints = showAll ? points : points.slice(0, VISIBLE_POINTS);

  const save = (event) => {
    event.preventDefault();
    setSaving(true);
    action(`/databases/${name}/update`, {
      backups: enabled ? "on" : "off",
      backupRetentionDays: days,
    })
      .then(() => {
        reload();
        alertUser("Settings saved. Deploy to apply them.");
      })
      .catch((error) => alertUser(error.message))
      .finally(() => setSaving(false));
  };

  const restoreAtTime = (event) => {
    event.preventDefault();
    const chosen = parseTime(timeValue);
    if (!chosen) {
      alertUser("Enter a valid date and time");
      return;
    }
    if (recovery && (chosen < recovery.start || chosen > recovery.end)) {
      alertUser(`Choose a time between ${formatDateTime(recovery.start)} and ${formatDateTime(recovery.end)}`);
      return;
    }
    setRestore({ target: { type: "time", value: timeValue } });
  };

  return (
    <>
      <ResourceSettingsBlock title="Status">
        {!settings.enabled ? (
          <SettingsAlert tone="danger">
            Backups are off — this database has no recovery if its data is lost or deleted
          </SettingsAlert>
        ) : (
          <SettingsPanel>
            {status ? (
              <div className="rw-backup-status">
                <Badge tone={badge.tone}>{badge.label}</Badge>
                {status.message ? <span>{status.message}</span> : null}
              </div>
            ) : (
              <Muted size="sm">Backups start after you deploy this database.</Muted>
            )}
            {lastBackup ? <p className="rw-backup-line">Last backup {formatWhen(lastBackup)}</p> : null}
            {since && snapshot ? <p className="rw-backup-line">Oldest backup {formatWhen(since)}</p> : null}
            {since && !snapshot ? <p className="rw-backup-line">You can restore to any moment since {formatWhen(since)}</p> : null}
            {snapshot ? (
              <p className="rw-backup-line">
                A backup is taken once a day. If you restore, changes made since the backup was taken are not in the new database.
              </p>
            ) : null}
          </SettingsPanel>
        )}
      </ResourceSettingsBlock>

      <FormStack className="rw-rail-form" onSubmit={save}>
        <ResourceSettingsBlock
          title="Backup settings"
          description="Backups let you restore this database to a new one if its data is lost, damaged or deleted. Changes apply when you deploy."
        >
          <SettingsPanel className="rw-resource-settings-toggle-row">
            <Switch
              label="Back up this database"
              checked={enabled}
              onChange={(event) => setEnabled(event.currentTarget.checked)}
            />
          </SettingsPanel>
          <SettingsPanel className="rw-resource-settings-panel-spaced">
            <Field label="Keep backups for" description="How far back you can restore. Longer periods use more storage in your AWS account.">
              <Select value={days} disabled={!enabled} onChange={(event) => setDays(event.currentTarget.value)} aria-label="Keep backups for">
                {retentionOptions(days).map((choice) => (
                  <option key={choice} value={String(choice)}>{retentionLabel(choice)}</option>
                ))}
              </Select>
            </Field>
          </SettingsPanel>
        </ResourceSettingsBlock>
        <SettingsFormActions>
          <Button type="submit" loading={saving} disabled={!dirty}>Save changes</Button>
        </SettingsFormActions>
      </FormStack>

      {needsStorage && project ? (
        <ResourceSettingsBlock
          title="Choose where backups are stored"
          description="Backups need a place to go before they can protect this database."
        >
          <BackupStorageChooser project={project} data={data} reload={reload} />
        </ResourceSettingsBlock>
      ) : null}

      {archive || (settings.enabled && !needsStorage) ? (
        <ResourceSettingsBlock
          title="Restore"
          description="Restoring creates a new database next to this one from a backup. Your current database is not changed."
        >
          {archive?.spec?.deleteAfter ? (
            <Muted size="sm" mb="sm">
              {settings.enabled ? "These backups" : "Backups from before they were turned off"} are kept until{" "}
              {formatDateTime(archive.spec.deleteAfter)}.
            </Muted>
          ) : null}
          {!restorable ? (
            <Muted size="sm">Restore points appear after the first backup finishes.</Muted>
          ) : (
            <>
              <SettingsPanel>
                <div className="rw-backup-restore">
                  <div>
                    <Button type="button" variant="outline" onClick={() => setRestore({ target: { type: "latest" } })}>
                      {snapshot ? "Restore latest backup" : "Restore most recent state"}
                    </Button>
                  </div>
                  {recovery ? (
                    <form className="rw-backup-time-form" onSubmit={restoreAtTime}>
                      <Field
                        label="Restore to a specific time"
                        description={`Between ${formatDateTime(recovery.start)} and ${formatDateTime(recovery.end)}, in your local time`}
                      >
                        <Input
                          type="datetime-local"
                          className="rw-backup-time"
                          value={timeValue}
                          min={toLocalInputValue(new Date(Math.ceil(recovery.start.getTime() / MINUTE) * MINUTE))}
                          max={toLocalInputValue(new Date(Math.floor(recovery.end.getTime() / MINUTE) * MINUTE))}
                          onChange={(event) => setTimeValue(event.currentTarget.value)}
                          aria-label="Restore time"
                        />
                      </Field>
                      <Button type="submit" variant="outline" disabled={!timeValue}>Restore to this time</Button>
                    </form>
                  ) : null}
                </div>
              </SettingsPanel>
              {points.length ? (
                <>
                  <h4 className="rw-backup-subtitle">Restore points</h4>
                  <div className="rw-rail-stack">
                    {shownPoints.map((point) => (
                      <div className="rw-rail-list-row" key={point.id}>
                        <History size={16} aria-hidden="true" />
                        <span className="rw-rail-list-title">{formatDateTime(point.time)}</span>
                        <span className="rw-backup-meta">{formatRelativeTime(point.time)}</span>
                        <span className="rw-list-row-end">
                          <Button
                            type="button"
                            size="sm"
                            variant="outline"
                            onClick={() => setRestore({ target: { type: "point", id: point.id } })}
                          >
                            Restore
                          </Button>
                        </span>
                      </div>
                    ))}
                  </div>
                  {points.length > VISIBLE_POINTS ? (
                    <button type="button" className="rw-settings-text-btn" onClick={() => setShowAll((open) => !open)}>
                      {showAll ? "Show fewer" : `Show all ${points.length} restore points`}
                    </button>
                  ) : null}
                </>
              ) : null}
            </>
          )}
        </ResourceSettingsBlock>
      ) : null}

      <RestoreDatabaseDialog
        archive={restore ? archive : null}
        target={restore?.target}
        data={data}
        reload={reload}
        onClose={() => setRestore(null)}
      />
    </>
  );
}
