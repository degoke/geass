package controller

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/platform"
)

// A Postgres base copy can be restored only while the archive holds every log file it needs (see platform.WalSegments).
// The first backup after backups are turned on can finish while archiving does not work yet: CloudNativePG gives the
// database access to the new credentials a little after the backup is asked for, and the files archived in between never
// reach the bucket. The copy is complete in the bucket and the dashboard called it a restore point, but a restore from it
// failed for ever with "first needed WAL ... not found", and the new database stayed "restoring".
//
// So a copy is a restore point only once its log files are in the archive, and when there is no restore point at all
// and archiving is proven to work now, Geass takes a new copy itself instead of waiting for the next night's.

const (
	// walGrace is how long after a copy finished its last log file is given to reach the archive. The database archives a
	// file when it is full or when archive_timeout, five minutes in CloudNativePG, has passed since it was begun. A copy
	// taken from a standby (CloudNativePG does that when there is one) finishes while its last file is still the open one,
	// so on a database with no other activity that file reaches the archive about five minutes later, which a live run on
	// K3s showed: a file missing sooner than that, and a little more, is not yet a reason to doubt the copy.
	walGrace = 7 * time.Minute

	// walBackupRetryAfter is how long after the last attempt a new copy is taken for a database with no restore point, so
	// that one that is still running, or that has only just failed, is not piled on.
	walBackupRetryAfter = 3 * time.Minute

	// maxAutomaticBackups bounds the copies Geass takes on its own for one database: a fault that stays makes every copy
	// useless, and the dashboard says so instead.
	maxAutomaticBackups = 3

	labelAutomaticBackup = "geass.dev/automatic-backup"
)

// unusableBackup is a base copy in the bucket that cannot be restored, and the files it lacks.
type unusableBackup struct {
	ID      string
	Missing []string
}

// walVerdicts remembers which base copies have been seen to have all their log files. Files do not leave the archive
// before the copy does, so a yes stays a yes, and it saves a listing of the bucket for each copy at each look.
type walVerdicts struct{ ok sync.Map }

func (v *walVerdicts) isOK(key string) bool { _, ok := v.ok.Load(key); return ok }
func (v *walVerdicts) setOK(key string)     { v.ok.Store(key, struct{}{}) }

// verifyArchivedWal removes from the restore points the copies whose log files are not in the archive, and says which.
// A bucket that cannot be read leaves them as they are: not knowing is not a reason to hide a copy.
func (r *GeassDatabaseReconciler) verifyArchivedWal(ctx context.Context, plan *backupPlan, observed *observedBackups) {
	if plan == nil || plan.access == nil || plan.archive == nil || len(observed.restorePoints) == 0 {
		return
	}
	log := logf.FromContext(ctx)
	archive := plan.archive
	byID := make(map[string]*cnpgv1.Backup, len(observed.candidates))
	for i := range observed.candidates {
		byID[observed.candidates[i].Status.BackupID] = &observed.candidates[i]
	}
	storage := r.backupStorage(plan.access.accessKey, plan.access.secretKey, plan.access.region)
	logPrefix := platform.BarmanLogPrefix(archive.Spec.Project, archive.Spec.ServerName)

	usable := make([]geassv1alpha1.GeassRestorePoint, 0, len(observed.restorePoints))
	for _, point := range observed.restorePoints {
		backup := byID[point.ID]
		if backup == nil {
			usable = append(usable, point)
			continue
		}
		verdictKey := archive.Name + "/" + point.ID
		if r.walVerdicts.isOK(verdictKey) {
			usable = append(usable, point)
			continue
		}
		segments := platform.WalSegments(backup.Status.BeginWal, backup.Status.EndWal)
		if len(segments) == 0 {
			usable = append(usable, point)
			continue
		}
		if backup.Status.StoppedAt != nil && r.now().Sub(backup.Status.StoppedAt.Time) < walGrace {
			// Not yet known to be restorable, and looked at again soon, not at the next regular look.
			observed.unverified = true
			usable = append(usable, point)
			continue
		}
		missing, err := missingWalSegments(storage, archive.Spec.Storage.Bucket, logPrefix, segments)
		if err != nil {
			log.Error(err, "Could not check the log files of a backup", "backup", point.ID)
			observed.unverified = true
			usable = append(usable, point)
			continue
		}
		if len(missing) == 0 {
			r.walVerdicts.setOK(verdictKey)
			usable = append(usable, point)
			continue
		}
		observed.unusable = append(observed.unusable, unusableBackup{ID: point.ID, Missing: missing})
	}
	if len(observed.unusable) == 0 {
		return
	}
	observed.restorePoints = usable
	// The cluster's own record of its last successful backup is that of the unusable copy.
	observed.lastSuccess = nil
	if len(usable) == 0 {
		observed.first = nil
	} else if oldest := usable[len(usable)-1].Time.Time; observed.first != nil && observed.first.Before(oldest) {
		// Nothing can be restored from before the oldest copy that is left, so the window starts there.
		observed.first = &oldest
	}
	log.Info("Backups that cannot be restored were left out of the restore points", "database", plan.archive.Spec.Source.DatabaseName, "backups", len(observed.unusable))
}

// missingWalSegments lists the folder of the archive each file is in, once, and says which files are not there.
func missingWalSegments(storage backupStorageAPI, bucket, logPrefix string, segments []string) ([]string, error) {
	byDirectory := map[string][]string{}
	for _, segment := range segments {
		byDirectory[platform.WalDirectory(segment)] = append(byDirectory[platform.WalDirectory(segment)], segment)
	}
	var missing []string
	for directory, wanted := range byDirectory {
		keys, err := storage.ListKeys(bucket, logPrefix+directory)
		if err != nil {
			return nil, err
		}
		missing = append(missing, platform.MissingWalSegments(wanted, keys)...)
	}
	slices.Sort(missing)
	return missing, nil
}

// unrestorableMessage is what the dashboard says of backups that exist but cannot be restored, empty when there is no
// such problem: some restore point is usable, or nothing is wrong with the copies there are.
func (o observedBackups) unrestorableMessage() string {
	if len(o.restorePoints) > 0 || len(o.unusable) == 0 {
		return ""
	}
	return "A backup was taken before the database could save its log files, so it cannot be restored. A new one is being taken"
}

// retakeBackupIfNone takes a new base copy when the database has none that can be restored, the earlier attempts are
// over, and the archive is proven to work now: a log file reached the bucket after the last attempt. A copy taken at
// any other time may be as useless as the last. It takes at most maxAutomaticBackups of them.
func (r *GeassDatabaseReconciler) retakeBackupIfNone(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, plan *backupPlan, observed observedBackups) error {
	if plan == nil || plan.access == nil || plan.archive == nil || len(observed.restorePoints) > 0 {
		return nil
	}
	if len(observed.unusable) == 0 && observed.lastFailure == nil {
		return nil // the first backup is still on its way
	}
	var backups cnpgv1.BackupList
	if err := r.List(ctx, &backups, client.InNamespace(wsNS)); err != nil {
		return err
	}
	var latest time.Time
	automatic := 0
	for i := range backups.Items {
		backup := &backups.Items[i]
		if backup.Spec.Cluster.Name != db.Name {
			continue
		}
		if backup.Labels[labelAutomaticBackup] == db.Name {
			automatic++
		}
		if backup.Status.Phase != cnpgv1.BackupPhaseCompleted && backup.Status.Phase != cnpgv1.BackupPhaseFailed {
			return nil // one is on its way
		}
		at := backup.CreationTimestamp.Time
		if backup.Status.StoppedAt != nil && backup.Status.StoppedAt.After(at) {
			at = backup.Status.StoppedAt.Time
		}
		if at.After(latest) {
			latest = at
		}
	}
	if automatic >= maxAutomaticBackups || r.now().Sub(latest) < walBackupRetryAfter {
		return nil
	}
	storage := r.backupStorage(plan.access.accessKey, plan.access.secretKey, plan.access.region)
	objects, err := storage.ListObjects(plan.archive.Spec.Storage.Bucket, platform.BarmanLogPrefix(plan.archive.Spec.Project, plan.archive.Spec.ServerName))
	if err != nil {
		return err
	}
	archivedSince := false
	for _, object := range objects {
		if object.LastModified.After(latest) {
			archivedSince = true
			break
		}
	}
	if !archivedSince {
		return nil
	}
	backup := &cnpgv1.Backup{ObjectMeta: metav1.ObjectMeta{
		Name:      fmt.Sprintf("%s-again-%d", db.Name, r.now().Unix()),
		Namespace: wsNS,
		Labels:    map[string]string{labelAutomaticBackup: db.Name},
	}}
	backup.Spec.Cluster.Name = db.Name
	backup.Spec.Method = cnpgv1.BackupMethodBarmanObjectStore
	applyGeassLabels(backup, db, "GeassDatabase")
	// The database lives in the system namespace and the backup in the database's own, so the two cannot be owned: the
	// backup goes with its namespace, as the scheduled ones do.
	if err := setSameNamespaceOwner(db, backup, r.Scheme); err != nil {
		return err
	}
	logf.FromContext(ctx).Info("Taking a new backup of a database that has none it can be restored from", "database", db.Name)
	return client.IgnoreAlreadyExists(r.Create(ctx, backup))
}

// backupLookSoon is whether a database has no restore point yet, or has one that is not yet known to be restorable, which
// is when its backups are looked at again within a minute instead of every few minutes.
type backupLookSoon struct{ soon sync.Map }

func (b *backupLookSoon) set(db *geassv1alpha1.GeassDatabase, soon bool) {
	if soon {
		b.soon.Store(db.UID, struct{}{})
		return
	}
	b.soon.Delete(db.UID)
}

func (b *backupLookSoon) wanted(db *geassv1alpha1.GeassDatabase) bool {
	_, ok := b.soon.Load(db.UID)
	return ok
}

// recoveryFailures is how many times the job that rebuilds a restored database from its backup has failed. CloudNativePG
// makes a new job for each attempt and tries again for ever, so a restore that cannot succeed shows only as a database
// that never starts.
func (r *GeassDatabaseReconciler) recoveryFailures(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) int32 {
	var jobs batchv1.JobList
	if err := r.List(ctx, &jobs, client.InNamespace(wsNS), client.MatchingLabels{cnpgClusterLabel: db.Name, cnpgJobRoleLabel: cnpgFullRecoveryRole}); err != nil {
		return 0
	}
	var failed int32
	for i := range jobs.Items {
		failed += jobs.Items[i].Status.Failed
	}
	return failed
}

// restoringMessage is what a database that is being restored says. It begins with the usual words, which are not counted
// as a failure of the database, and once the rebuild has failed twice says so, because a restore that waits for ever on a
// backup it cannot read is otherwise silent.
func restoringMessage(failures int32) string {
	if failures < 2 {
		return messageRestoringDatabase
	}
	return fmt.Sprintf("%s. It has failed %d times to read what it needs from backup storage. If this goes on, delete this database and restore from an earlier restore point", messageRestoringDatabase, failures)
}
