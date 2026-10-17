import { list, resourceName } from "@/lib/api";
import { formatRelativeTime } from "@/lib/services";

export const BACKUP_RETENTION_CHOICES = [7, 14, 30, 90];
export const DEFAULT_BACKUP_RETENTION_DAYS = 7;

const BACKED_UP_ENGINES = ["Postgres", "MySQL", "MariaDB", "SQLite"];
const SNAPSHOT_ENGINES = ["MySQL", "MariaDB", "SQLite"];

/** Postgres, MySQL, MariaDB and SQLite databases that run inside Geass have backups; caches and external databases do not. */
export function supportsBackups(item) {
  if (!item?.spec) return false;
  if (item.spec.placement === "External") return false;
  return !item.spec.engine || BACKED_UP_ENGINES.includes(item.spec.engine);
}

/**
 * MySQL, MariaDB and SQLite are backed up by a full copy every day, so they can only be restored to when a
 * copy was taken. Postgres archives every change and can be restored to any moment.
 */
export function isSnapshotEngine(engine) {
  return SNAPSHOT_ENGINES.includes(engine);
}

export function isSnapshotDatabase(item) {
  return isSnapshotEngine(item?.spec?.engine);
}

/** Backups made before other engines had them record no engine and are Postgres. */
export function isSnapshotArchive(archive) {
  return isSnapshotEngine(archive?.spec?.source?.engine);
}

export function backupSpec(item) {
  const spec = item?.spec?.backup;
  return {
    enabled: Boolean(spec?.enabled),
    retentionDays: Number(spec?.retentionDays) || DEFAULT_BACKUP_RETENTION_DAYS,
  };
}

/** Retention choices, plus the current value when it is not one of the usual ones. */
export function retentionOptions(current) {
  const days = Number(current);
  if (days > 0 && !BACKUP_RETENTION_CHOICES.includes(days)) {
    return [...BACKUP_RETENTION_CHOICES, days].sort((a, b) => a - b);
  }
  return BACKUP_RETENTION_CHOICES;
}

export function retentionLabel(days) {
  return `${days} ${days === 1 ? "day" : "days"}`;
}

/** A short warning for a Postgres database that is not protected, or "" when there is nothing to say. */
export function backupWarning(item) {
  if (!supportsBackups(item)) return "";
  if (!backupSpec(item).enabled) return "Not backed up";
  const state = item.status?.backup?.state;
  if (state === "NeedsStorage") return "Not backed up";
  if (state === "AtRisk") return "Backups at risk";
  return "";
}

/** Badge text and tone for a database's backup state. */
export function backupStateBadge(state) {
  switch (state) {
    case "Protected":
      return { label: "Protected", tone: "success" };
    case "AtRisk":
      return { label: "At risk", tone: "warning" };
    case "NeedsStorage":
      return { label: "Needs storage", tone: "warning" };
    case "Starting":
      return { label: "Starting", tone: "neutral" };
    default:
      return { label: "Unknown", tone: "neutral" };
  }
}

export function projectBackupStorage(data, projectName) {
  const project = list(data, "projects").find((item) => resourceName(item) === projectName);
  return project?.spec?.backupStorage || null;
}

function archiveTime(value) {
  const stamp = value ? new Date(value).getTime() : NaN;
  return Number.isNaN(stamp) ? 0 : stamp;
}

/**
 * The backups that belong to a database. While backups are on, its status names them. After they
 * are turned off the backups are kept for a while, and are found by matching the database.
 */
export function archiveForDatabase(data, item) {
  const archives = list(data, "backupArchives");
  const named = item?.status?.backup?.archive;
  if (named) {
    const found = archives.find((entry) => resourceName(entry) === named);
    if (found) return found;
  }
  const name = resourceName(item);
  const uid = item?.metadata?.uid;
  return archives
    .filter((entry) => entry.spec?.database === name && entry.spec?.project === item?.spec?.project)
    .filter((entry) => !uid || !entry.spec?.databaseUID || entry.spec.databaseUID === uid)
    .filter((entry) => !entry.spec?.deleteAfter || archiveTime(entry.spec.deleteAfter) > Date.now())
    .sort((a, b) => archiveTime(b.status?.lastBackupTime) - archiveTime(a.status?.lastBackupTime))[0] || null;
}

/** Backups of databases that no longer exist and can still be restored. */
export function deletedDatabaseArchives(data, projectName) {
  const databases = new Set(list(data, "databases").map((item) => resourceName(item)));
  return list(data, "backupArchives")
    .filter((entry) => entry.spec?.project === projectName)
    .filter((entry) => entry.spec?.deleteAfter && archiveTime(entry.spec.deleteAfter) > Date.now())
    .filter((entry) => !databases.has(entry.spec?.database))
    .sort((a, b) => archiveTime(b.spec?.deleteAfter) - archiveTime(a.spec?.deleteAfter));
}

/** True once a project has used backups, so the empty "deleted databases" note makes sense. */
export function projectHasBackupHistory(data, projectName) {
  if (projectBackupStorage(data, projectName)) return true;
  return list(data, "backupArchives").some((entry) => entry.spec?.project === projectName);
}

export function archiveDisplayName(archive) {
  return archive?.metadata?.annotations?.["geass.dev/display-name"]?.trim() || archive?.spec?.database || resourceName(archive);
}

export function parseTime(value) {
  const date = value ? new Date(value) : null;
  return date && !Number.isNaN(date.getTime()) ? date : null;
}

export function formatDateTime(value) {
  const date = parseTime(value);
  if (!date) return "";
  return date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/** "Oct 4, 2026, 3:04 PM (2 hours ago)" */
export function formatWhen(value) {
  const absolute = formatDateTime(value);
  if (!absolute) return "";
  const relative = formatRelativeTime(value);
  return relative ? `${absolute} (${relative})` : absolute;
}

/** Local "YYYY-MM-DDTHH:mm" value for a datetime-local input. */
export function toLocalInputValue(date) {
  const pad = (n) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/** Earliest and latest moments a database can be restored to, or null when it has no window. */
export function recoveryWindow(archive) {
  const start = parseTime(archive?.status?.recoveryWindowStart);
  const end = parseTime(archive?.status?.recoveryWindowEnd);
  if (!start || !end || end <= start) return null;
  return { start, end };
}

export function restorePoints(archive) {
  return archive?.status?.restorePoints || [];
}

/** Whether an archive has anything to restore from. */
export function canRestoreFrom(archive) {
  return Boolean(archive) && (restorePoints(archive).length > 0 || Boolean(archive.status?.lastBackupTime));
}
