import { useState } from "react";
import { Database } from "lucide-react";
import { resourceName } from "@/lib/api";
import {
  archiveDisplayName,
  deletedDatabaseArchives,
  formatDateTime,
  projectHasBackupHistory,
} from "@/lib/backups";
import { BackupStorageChooser } from "@/components/BackupStorageChooser";
import { RestoreDatabaseDialog } from "@/components/RestoreDatabaseDialog";
import { Button, Muted } from "@/components/geass-ui";

/** Project-level backups: where they are stored, and databases that were deleted but can still be restored. */
export function ProjectBackups({ project, data, mutable, reload }) {
  const name = resourceName(project);
  const deleted = deletedDatabaseArchives(data, name);
  const showDeleted = deleted.length > 0 || projectHasBackupHistory(data, name);
  const [restoring, setRestoring] = useState(""); // name of the backups being restored
  const restoringArchive = deleted.find((entry) => resourceName(entry) === restoring) || null;

  return (
    <div className="rw-backup">
      <p className="rw-rail-body">
        Geass backs up Postgres, MySQL and SQLite databases to storage in your own AWS account. If a database&apos;s data
        is lost or the database is deleted, you can restore it to a new database.
      </p>

      <h3 className="rw-settings-subtitle">Backup storage</h3>
      <BackupStorageChooser project={project} data={data} reload={reload} mutable={mutable} />

      {showDeleted ? (
        <>
          <h3 className="rw-settings-subtitle">Recently deleted databases</h3>
          {deleted.length ? (
            <div className="rw-rail-stack">
              {deleted.map((archive) => (
                <div className="rw-rail-list-row" key={resourceName(archive)}>
                  <Database size={16} aria-hidden="true" />
                  <span className="rw-rail-list-title">{archiveDisplayName(archive)}</span>
                  {archive.spec?.environment ? <span className="rw-vars-kind">{archive.spec.environment}</span> : null}
                  <span className="rw-backup-meta">
                    Deleted · restorable until {formatDateTime(archive.spec?.deleteAfter)}
                  </span>
                  {mutable ? (
                    <span className="rw-list-row-end">
                      <Button type="button" size="sm" variant="outline" onClick={() => setRestoring(resourceName(archive))}>
                        Restore
                      </Button>
                    </span>
                  ) : null}
                </div>
              ))}
            </div>
          ) : (
            <Muted size="sm" className="rw-settings-empty-inline">Databases you delete stay restorable for 7 days</Muted>
          )}
        </>
      ) : null}

      <RestoreDatabaseDialog
        archive={restoringArchive}
        data={data}
        reload={reload}
        onClose={() => setRestoring("")}
      />
    </div>
  );
}
