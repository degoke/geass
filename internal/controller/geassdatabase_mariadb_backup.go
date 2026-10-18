/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	mariadbv1 "github.com/degoke/geass/pkg/mariadb/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// A MariaDB database with high availability is backed up like a Postgres one, to any moment, and not by the daily
// copies the other engines have. The operator takes a physical copy of a replica every night, the agent beside the
// primary archives its binary logs to the same bucket, and a restore starts a new replicated database from the copy
// nearest before the moment asked for and replays the logs up to it (the operator calls that point-in-time recovery).
//
// What the operator does not do, and Geass does here: it removes old physical copies by the retention it is given
// but never an archived log, so Geass removes the logs that no remaining copy can use; a log is only archived once the
// server has closed it, which a quiet database does not do for days, so a small job closes it every few minutes; and
// a copy taken before a failover does not reach the logs of the new primary, so a new copy is asked for when the
// primary changes.

const (
	mariadbBackupSuffix  = "-backup"
	mariadbPITRSuffix    = "-pitr"
	mariadbFlushSuffix   = "-binlog-flush"
	mariadbFinalSuffix   = "-final"
	mariadbRestoreSource = "-restore-source"

	// mariadbBinlogFlushSchedule closes the server's current binary log every five minutes, so that it is archived at
	// the next archive cycle and a restore can reach a moment that recent. The archive cycle is the operator's and is
	// ten minutes, so a database can be restored to a moment about fifteen minutes ago, not to one a second ago.
	mariadbBinlogFlushSchedule = "*/5 * * * *"

	// annotationBackupPrimary is on the physical backup and names the primary the last baseline was taken from.
	annotationBackupPrimary = "geass.dev/backup-primary"
	// annotationBackupArmed is when the database was first seen to archive its logs, and annotationBackupBaseline says
	// the first copy has been asked for.
	annotationBackupArmed    = "geass.dev/backup-armed"
	annotationBackupBaseline = "geass.dev/backup-baseline"

	// mariadbArmDelay is how long after a database is told to archive its logs it has to have been ready before the first
	// copy: the operator takes a few seconds to notice the change and begin to roll the servers, and a database that
	// still looks ready in that time is not settled.
	mariadbArmDelay = 45 * time.Second

	// mariadbBackupPending is how soon a database is looked at again while its backups are being set up.
	mariadbBackupPending = 15 * time.Second

	// mariadbBinlogPruneHour is the hour of the day (UTC) when archived logs that no kept copy can use are removed.
	mariadbBinlogPruneHour = 3
)

func mariadbBackupName(db *geassv1alpha1.GeassDatabase) string { return db.Name + mariadbBackupSuffix }
func mariadbPITRName(db *geassv1alpha1.GeassDatabase) string   { return db.Name + mariadbPITRSuffix }
func mariadbFlushName(db *geassv1alpha1.GeassDatabase) string  { return db.Name + mariadbFlushSuffix }
func mariadbFinalName(db *geassv1alpha1.GeassDatabase) string  { return db.Name + mariadbFinalSuffix }
func mariadbRestoreSourceName(db *geassv1alpha1.GeassDatabase) string {
	return db.Name + mariadbRestoreSource
}

// mariadbContinuousBackups is whether this database is backed up by the operator, and not by daily copies.
func mariadbContinuousBackups(db *geassv1alpha1.GeassDatabase) bool {
	return databaseEngine(db) == geassv1alpha1.DatabaseEngineMariaDB && databaseBackupKind(db) == platform.BackupKindContinuous
}

// mariadbBucket is a bucket and a folder of it, as the operator is told about it. The credentials are in a Secret of
// the database's namespace, which Geass keeps (ensureWorkspaceSecret). The endpoint is AWS's, unless a build for
// testing says otherwise.
func mariadbBucket(bucket, region, prefix, secret string) *mariadbv1.S3 {
	endpoint, tls := "s3."+region+".amazonaws.com", true
	if standInS3Endpoint != "" {
		endpoint, tls = standInS3Endpoint, false
	}
	return &mariadbv1.S3{
		Bucket: bucket, Endpoint: endpoint, Region: region, Prefix: prefix,
		AccessKeyIDSecretKeyRef:     mariadbv1.SecretKeyRef{Name: secret, Key: platform.BackupSecretKeyAccessKeyID},
		SecretAccessKeySecretKeyRef: mariadbv1.SecretKeyRef{Name: secret, Key: platform.BackupSecretKeySecretAccessKey},
		TLS:                         &mariadbv1.S3TLS{Enabled: tls},
	}
}

// stagingStorage is the volume a copy passes through on its way to or from the bucket, as large as the database.
func stagingStorage(db *geassv1alpha1.GeassDatabase, storageClass string) *mariadbv1.StagingStorage {
	return &mariadbv1.StagingStorage{PersistentVolumeClaim: &mariadbv1.PersistentVolumeClaimStorage{
		AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: databaseStorageQuantity(db)}},
		StorageClassName: stringPtrOrNil(storageClass),
	}}
}

// mariadbPhysicalBackupSpec is the nightly copy. The job runs next to the server it copies, so it can reach the volume;
// it prefers a replica, which leaves the primary alone while one is ready.
func mariadbPhysicalBackupSpec(db *geassv1alpha1.GeassDatabase, archive *geassv1alpha1.GeassBackupArchive, secret, storageClass string, placement platform.Placement) mariadbv1.PhysicalBackupSpec {
	retention := time.Duration(databaseBackupRetentionDays(db)) * 24 * time.Hour
	return mariadbv1.PhysicalBackupSpec{
		MariaDBRef: mariadbv1.MariaDBRef{Name: db.Name},
		// It is held until armMariaDBBackup says the database has settled: see there.
		Schedule: &mariadbv1.PhysicalBackupSchedule{Cron: platform.DumpSchedule(db.UID), Suspend: true},
		Target:   "PreferReplica",
		Storage: mariadbv1.PhysicalBackupStorage{S3: mariadbBucket(
			archive.Spec.Storage.Bucket, archive.Spec.Storage.Region, platform.PhysicalObjectPrefix(archive.Spec.Project, archive.Spec.ServerName), secret)},
		StagingStorage: stagingStorage(db, storageClass),
		Compression:    "gzip",
		MaxRetention:   &metav1.Duration{Duration: retention},
		PodAffinity:    utilptr.To(true),
		Tolerations:    jobPlacement(placement).Tolerations(),
	}
}

// mariadbPITRSpec archives the primary's binary logs next to the physical copies.
func mariadbPITRSpec(db *geassv1alpha1.GeassDatabase, archive *geassv1alpha1.GeassBackupArchive, secret string) mariadbv1.PointInTimeRecoverySpec {
	return mariadbv1.PointInTimeRecoverySpec{
		PhysicalBackupRef: mariadbv1.LocalRef{Name: mariadbBackupName(db)},
		Storage: mariadbv1.PhysicalBackupStorage{S3: mariadbBucket(
			archive.Spec.Storage.Bucket, archive.Spec.Storage.Region, platform.BinlogObjectPrefix(archive.Spec.Project, archive.Spec.ServerName), secret)},
		Compression: "gzip",
		// A moment past the last archived log restores to the last one, which Geass has already said is what the
		// window ends at, and a restore is not made to fail for the few minutes that may not have been archived yet.
		StrictMode: utilptr.To(false),
	}
}

// mariadbBinlogFlushJob closes the server's current binary log. It runs the MariaDB client from the server's own image
// as root, with the password from the database's Secret, against the primary's Service.
func mariadbBinlogFlush(db *geassv1alpha1.GeassDatabase, wsNS string, placement platform.Placement) *batchv1.CronJob {
	port := platform.MySQLDefaultPort
	pod := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{{
			Name: "flush", Image: platform.MariaDBImage,
			Command: []string{"bash", "-c", fmt.Sprintf(`set -euo pipefail
%s
mariadb --host="$DB_HOST" --port=%s --user=%s -e 'FLUSH BINARY LOGS'`, waitForServerScript(port), port, mysqlRootUser)},
			Env: []corev1.EnvVar{
				{Name: "DB_HOST", Value: mariadbHost(db, wsNS)},
				secretEnv("MYSQL_PWD", db.Name+"-bootstrap", "password"),
			},
		}},
	}
	jobPlacement(placement).ApplyToPodSpec(&pod)
	history, failedHistory, deadline, retries := int32(1), int32(1), int64(240), int32(0)
	startingDeadline := int64(120)
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: mariadbFlushName(db), Namespace: wsNS},
		Spec: batchv1.CronJobSpec{
			Schedule:                   mariadbBinlogFlushSchedule,
			TimeZone:                   utilptr.To("Etc/UTC"),
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			StartingDeadlineSeconds:    &startingDeadline,
			SuccessfulJobsHistoryLimit: &history,
			FailedJobsHistoryLimit:     &failedHistory,
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{labelBackupFor: db.Name, labelBackupRole: "binlog-flush"}},
				Spec:       batchv1.JobSpec{BackoffLimit: &retries, ActiveDeadlineSeconds: &deadline, Template: corev1.PodTemplateSpec{Spec: pod}},
			},
		},
	}
}

// reconcileMariaDBBackups restores a new database from backups, if it is one, and then keeps its backups going: the
// daily copies of a single server, or the physical copies and archived logs of a replicated one. It runs once the
// database itself is up. A refused or failed restore is an error written for end users. A problem with the backups
// themselves is not: it is logged and reported in the outcome, because the database works without them.
func (r *GeassDatabaseReconciler) reconcileMariaDBBackups(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string, placement platform.Placement, mdb *mariadbv1.MariaDB) (dumpOutcome, error) {
	var outcome dumpOutcome
	kind := databaseBackupKind(db)
	if kind == platform.BackupKindNone {
		return outcome, nil
	}
	if db.Spec.RestoreFrom != nil && db.Status.RestoredAt == nil {
		waiting, err := r.reconcileMariaDBRestore(ctx, db, wsNS, mdb)
		if err != nil || waiting != "" {
			outcome.waiting = waiting
			return outcome, err
		}
	}
	var err error
	if kind == platform.BackupKindSnapshot {
		err = r.maintainDumpBackups(ctx, db, wsNS, &outcome)
	} else {
		err = r.maintainMariaDBBackups(ctx, db, wsNS, storageClass, placement, mdb, &outcome)
	}
	if err != nil {
		backupProblem(ctx, db, err)
		outcome.failed = true
	}
	return outcome, nil
}

// maintainMariaDBBackups keeps the nightly copy, the archive of binary logs, the job that closes them, the archive
// record and the dashboard summary in step with what exists in the bucket. It runs once the database is up. A problem
// with backups is reported in the outcome and logged: the database works without them.
func (r *GeassDatabaseReconciler) maintainMariaDBBackups(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string, placement platform.Placement, mdb *mariadbv1.MariaDB, outcome *dumpOutcome) error {
	plan, err := r.planBackups(ctx, db, wsNS)
	if err != nil {
		return err
	}
	if plan == nil {
		return r.windDownMariaDBBackups(ctx, db, wsNS)
	}
	outcome.backups = true
	if plan.archive == nil {
		state, message := platform.DatabaseBackupState(plan.health)
		return r.setBackupStatus(ctx, db, &geassv1alpha1.GeassDatabaseBackupStatus{State: state, Message: message})
	}
	archive := plan.archive

	if err := r.ensureMariaDBBackupObjects(ctx, db, wsNS, storageClass, placement, plan, mdb); err != nil {
		return err
	}
	// The database is told to archive its logs a reconcile after the objects it names exist, and the first copy waits for
	// it to settle after that.
	if backup := r.physicalBackupOf(ctx, db, wsNS); backup == nil || backup.Spec.Schedule == nil || backup.Spec.Schedule.Suspend {
		outcome.pending = true
	}

	points, copies, listed := r.listPhysicalBackups(ctx, db, plan)
	if listed && r.now().UTC().Hour() == mariadbBinlogPruneHour {
		r.pruneBinlogs(ctx, db, plan, copies)
	}
	failure, err := r.latestPhysicalBackupFailure(ctx, db, wsNS)
	if err != nil {
		return err
	}
	observed := physicalObservation(points, failure)
	if !listed {
		// A bucket that could not be read says nothing about the backups in it, so the last known state stands.
		if db.Status.Backup != nil {
			return nil
		}
		return r.setBackupStatus(ctx, db, backupStatusFor(plan.health, observed, archive.Name))
	}
	end := r.recoverableUntil(ctx, db, wsNS, observed)
	if err := r.writeArchiveStatus(ctx, archive, observed, end); err != nil {
		return err
	}
	return r.setBackupStatus(ctx, db, backupStatusFor(plan.health, observed, archive.Name))
}

// physicalObservation describes the copies as the shared status code expects: the oldest is where the window starts and
// the newest is the last backup.
func physicalObservation(points []geassv1alpha1.GeassRestorePoint, lastFailure *time.Time) observedBackups {
	observed := observedBackups{restorePoints: points, lastFailure: lastFailure}
	if len(points) > 0 {
		newest, oldest := points[0].Time.Time, points[len(points)-1].Time.Time
		observed.lastSuccess, observed.first = &newest, &oldest
	}
	return observed
}

// recoverableUntil is the latest moment a restore reaches: what the operator says the archived logs reach, or, until it
// says anything, the newest copy. It is not raised to the newest copy's time when the operator's is earlier, as it is
// right after a new baseline copy: no log covers the moments between, and a restore to one of them would come up with
// older data. That copy is still a restore point of its own.
func (r *GeassDatabaseReconciler) recoverableUntil(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, observed observedBackups) *time.Time {
	pitr := &mariadbv1.PointInTimeRecovery{}
	if err := r.Get(ctx, client.ObjectKey{Name: mariadbPITRName(db), Namespace: wsNS}, pitr); err == nil && pitr.Status.LastRecoverableTime != nil {
		end := pitr.Status.LastRecoverableTime.Time
		return &end
	}
	return observed.lastBackup()
}

// ensureMariaDBBackupObjects makes the nightly copy, the archive of logs and the job that closes them.
func (r *GeassDatabaseReconciler) ensureMariaDBBackupObjects(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string, placement platform.Placement, plan *backupPlan, mdb *mariadbv1.MariaDB) error {
	archive := plan.archive
	backup := &mariadbv1.PhysicalBackup{ObjectMeta: metav1.ObjectMeta{Name: mariadbBackupName(db), Namespace: wsNS}}
	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, backup, func() error {
		applyGeassLabels(backup, db, "GeassDatabase")
		desired := mariadbPhysicalBackupSpec(db, archive, plan.secretName, storageClass, placement)
		if backup.ResourceVersion != "" {
			// Where the staging volume is made is fixed once the object exists, like the database's own volume.
			if live := backup.Spec.StagingStorage; live != nil && live.PersistentVolumeClaim != nil && desired.StagingStorage != nil {
				desired.StagingStorage.PersistentVolumeClaim.StorageClassName = live.PersistentVolumeClaim.StorageClassName
			}
			if backup.Spec.Schedule != nil {
				desired.Schedule.OnDemand = backup.Spec.Schedule.OnDemand
			}
		}
		if backup.Annotations == nil {
			backup.Annotations = map[string]string{}
		}
		r.armMariaDBBackup(backup, &desired, mdb)
		backup.Spec = desired
		return setSameNamespaceOwner(db, backup, r.Scheme)
	}); err != nil {
		return err
	}

	pitr := &mariadbv1.PointInTimeRecovery{ObjectMeta: metav1.ObjectMeta{Name: mariadbPITRName(db), Namespace: wsNS}}
	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, pitr, func() error {
		applyGeassLabels(pitr, db, "GeassDatabase")
		pitr.Spec = mariadbPITRSpec(db, archive, plan.secretName)
		return setSameNamespaceOwner(db, pitr, r.Scheme)
	}); err != nil {
		return err
	}

	flush := mariadbBinlogFlush(db, wsNS, placement)
	cron := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: flush.Name, Namespace: wsNS}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cron, func() error {
		applyGeassLabels(cron, db, "GeassDatabase")
		cron.Labels[labelBackupFor] = db.Name
		cron.Spec = flush.Spec
		return setSameNamespaceOwner(db, cron, r.Scheme)
	})
	return err
}

// physicalBackupOf is the nightly copy as the cluster holds it, or nil.
func (r *GeassDatabaseReconciler) physicalBackupOf(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) *mariadbv1.PhysicalBackup {
	backup := &mariadbv1.PhysicalBackup{}
	if err := r.Get(ctx, client.ObjectKey{Name: mariadbBackupName(db), Namespace: wsNS}, backup); err != nil {
		return nil
	}
	return backup
}

// armMariaDBBackup decides when the nightly copy starts, and when another is asked for.
//
// A copy only serves a restore to a moment if the logs that follow it are archived, and they are archived from the
// primary as it is once the database is told to. Telling it makes the operator roll the servers and move the primary,
// so a copy taken before that is of a primary whose logs were never archived: it is a restore point, and a restore
// to a moment after it does not replay anything. The copy is therefore held until the database has been told to
// archive and has settled, and the first is taken then. The same is true of any later change of primary: its logs are a
// new line that continues from a copy taken on it, so a copy is asked for when it changes.
func (r *GeassDatabaseReconciler) armMariaDBBackup(backup *mariadbv1.PhysicalBackup, desired *mariadbv1.PhysicalBackupSpec, mdb *mariadbv1.MariaDB) {
	now := r.now()
	armedAt, _ := time.Parse(time.RFC3339, backup.Annotations[annotationBackupArmed])
	if armedAt.IsZero() && mdb.Spec.PointInTimeRecoveryRef != nil {
		armedAt = now
		backup.Annotations[annotationBackupArmed] = armedAt.UTC().Format(time.RFC3339)
	}
	baselineTaken := backup.Annotations[annotationBackupBaseline] != ""
	settled := !armedAt.IsZero() && now.Sub(armedAt) >= mariadbArmDelay && mdb.ConditionTrue(mariadbv1.ConditionReady)
	// Once the first copy has been asked for the schedule stays on, whatever the database does next.
	desired.Schedule.Suspend = !baselineTaken && !settled
	if desired.Schedule.Suspend {
		return
	}
	primary := ""
	if mdb.Status.CurrentPrimary != nil {
		primary = *mdb.Status.CurrentPrimary
	}
	known := backup.Annotations[annotationBackupPrimary]
	switch {
	case !baselineTaken:
		desired.Schedule.OnDemand = "first-" + strconv.FormatInt(now.Unix(), 10)
		backup.Annotations[annotationBackupBaseline] = "taken"
		if primary != "" {
			backup.Annotations[annotationBackupPrimary] = primary
		}
	case primary != "" && known != primary:
		desired.Schedule.OnDemand = primary + "-" + strconv.FormatInt(now.Unix(), 10)
		backup.Annotations[annotationBackupPrimary] = primary
	}
}

// mariadbArchivesLogs is whether the database should be told to archive its binary logs: the object it names exists, and
// the backups are wanted. It is read by ensureMariaDB, so that the database never refers to what is not there.
func (r *GeassDatabaseReconciler) mariadbArchivesLogs(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) bool {
	if !mariadbContinuousBackups(db) || !databaseBackupsEnabled(db) {
		return false
	}
	return r.Get(ctx, client.ObjectKey{Name: mariadbPITRName(db), Namespace: wsNS}, &mariadbv1.PointInTimeRecovery{}) == nil
}

// listPhysicalBackups reads the copies in the bucket, which is the truth: it reflects what was added, pruned or lost.
func (r *GeassDatabaseReconciler) listPhysicalBackups(ctx context.Context, db *geassv1alpha1.GeassDatabase, plan *backupPlan) (points []geassv1alpha1.GeassRestorePoint, keys []string, listed bool) {
	if plan.access == nil || plan.archive == nil {
		return nil, nil, false
	}
	archive := plan.archive
	storage := r.backupStorage(plan.access.accessKey, plan.access.secretKey, plan.access.region)
	keys, err := storage.ListKeys(archive.Spec.Storage.Bucket, platform.PhysicalObjectPrefix(archive.Spec.Project, archive.Spec.ServerName))
	if err != nil {
		logf.FromContext(ctx).Info("Could not list backups", "database", db.Name, "reason", summarizeStorageError(err))
		return nil, nil, false
	}
	return platform.PhysicalRestorePoints(keys), keys, true
}

// pruneBinlogs removes the archived logs that no copy that is kept can use, given the copies that are kept. It runs once a
// day, in the hour after the nightly copy has been taken and the operator has removed the old ones: the logs are
// listed in full, and there is one for every five minutes of the retention. Failing to is not a reason to fail the
// database: the logs stay, and are looked at again the next day.
func (r *GeassDatabaseReconciler) pruneBinlogs(ctx context.Context, db *geassv1alpha1.GeassDatabase, plan *backupPlan, copies []string) {
	archive := plan.archive
	storage := r.backupStorage(plan.access.accessKey, plan.access.secretKey, plan.access.region)
	log := logf.FromContext(ctx)
	objects, err := storage.ListObjects(archive.Spec.Storage.Bucket, platform.BinlogObjectPrefix(archive.Spec.Project, archive.Spec.ServerName))
	if err != nil {
		log.Info("Could not list archived logs", "database", db.Name, "reason", summarizeStorageError(err))
		return
	}
	stored := make([]platform.StoredObject, len(objects))
	for i := range objects {
		stored[i] = platform.StoredObject{Key: objects[i].Key, Modified: objects[i].LastModified}
	}
	for _, key := range platform.ExpiredBinlogs(stored, copies) {
		if err := storage.DeleteObject(archive.Spec.Storage.Bucket, key); err != nil {
			log.Info("Could not remove an archived log", "database", db.Name, "reason", summarizeStorageError(err))
			return
		}
	}
}

// latestPhysicalBackupFailure is when the newest job of the nightly copy gave up, if one did after the last that
// finished, so that a copy that works again clears it. The operator keeps its jobs, and owns them by the backup.
func (r *GeassDatabaseReconciler) latestPhysicalBackupFailure(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (*time.Time, error) {
	var jobs batchv1.JobList
	if err := r.List(ctx, &jobs, client.InNamespace(wsNS)); err != nil {
		return nil, err
	}
	var failed, succeeded *time.Time
	for i := range jobs.Items {
		job := &jobs.Items[i]
		if !ownedBy(job, "PhysicalBackup", mariadbBackupName(db)) {
			continue
		}
		complete, didFail := jobFinished(job)
		switch {
		case didFail:
			at := jobFailedAt(job, r.now())
			if failed == nil || at.After(*failed) {
				failed = &at
			}
		case complete:
			at := job.CreationTimestamp.Time
			for _, condition := range job.Status.Conditions {
				if condition.Type == batchv1.JobComplete && !condition.LastTransitionTime.IsZero() {
					at = condition.LastTransitionTime.Time
				}
			}
			if succeeded == nil || at.After(*succeeded) {
				succeeded = &at
			}
		}
	}
	if failed != nil && (succeeded == nil || failed.After(*succeeded)) {
		return failed, nil
	}
	return nil, nil
}

func ownedBy(obj client.Object, kind, name string) bool {
	for _, owner := range obj.GetOwnerReferences() {
		if owner.Kind == kind && owner.Name == name {
			return true
		}
	}
	return false
}

// windDownMariaDBBackups handles a database that no longer wants backups: the copies and the archive of logs stop, and
// the archive record is retired so earlier restore points stay usable for a while. The database stops archiving first
// (ensureMariaDB reads whether the backups are wanted), so what it refers to is never removed under it.
func (r *GeassDatabaseReconciler) windDownMariaDBBackups(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) error {
	live, err := liveArchive(ctx, r.Client, db)
	if err != nil {
		return err
	}
	if live == nil && db.Status.Backup == nil {
		return nil
	}
	// The objects a restored database was started from stay: its spec still names them.
	if err := r.deleteMariaDBBackupObjects(ctx, db, wsNS, false); err != nil {
		return err
	}
	if live != nil {
		if err := retireArchive(ctx, r.Client, live, r.now()); err != nil {
			return err
		}
	}
	return r.setBackupStatus(ctx, db, nil)
}

// deleteMariaDBBackupObjects removes what the backups made in the cluster, never what they put in the bucket. A staging
// volume the operator made for the copies is not owned by anything that Geass deletes, so it goes too. The copy and the
// archive of logs that a restored database was started from are removed only with the database (withRestoreSource):
// the operator keeps them named in the database's spec for as long as it exists.
func (r *GeassDatabaseReconciler) deleteMariaDBBackupObjects(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, withRestoreSource bool) error {
	ignore := func(err error) error {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return nil
		}
		return err
	}
	objects := []client.Object{
		&batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: mariadbFlushName(db), Namespace: wsNS}},
		&mariadbv1.PointInTimeRecovery{ObjectMeta: metav1.ObjectMeta{Name: mariadbPITRName(db), Namespace: wsNS}},
		&mariadbv1.PhysicalBackup{ObjectMeta: metav1.ObjectMeta{Name: mariadbBackupName(db), Namespace: wsNS}},
		&mariadbv1.PhysicalBackup{ObjectMeta: metav1.ObjectMeta{Name: mariadbFinalName(db), Namespace: wsNS}},
	}
	if withRestoreSource {
		objects = append(objects,
			&mariadbv1.PointInTimeRecovery{ObjectMeta: metav1.ObjectMeta{Name: mariadbRestoreSourceName(db), Namespace: wsNS}},
			&mariadbv1.PhysicalBackup{ObjectMeta: metav1.ObjectMeta{Name: mariadbRestoreSourceName(db), Namespace: wsNS}},
		)
	}
	for _, object := range objects {
		if err := ignore(r.Delete(ctx, object, client.PropagationPolicy(metav1.DeletePropagationBackground))); err != nil {
			return err
		}
	}
	for _, name := range []string{mariadbBackupName(db), mariadbFinalName(db)} {
		claim := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name + "-staging", Namespace: wsNS}}
		if err := client.IgnoreNotFound(r.Delete(ctx, claim)); err != nil {
			return err
		}
	}
	return nil
}

// finalizeMariaDBBackups is what deleting a replicated MariaDB database does about its backups: one last physical copy,
// so what was written since the previous night is a restore point, then the archive record is retired so the copies and
// logs stay restorable for a while. It never holds up deletion for long: every failure ends with the archive retired and
// the restore points that already exist. It returns how long to wait before looking again, or zero.
func (r *GeassDatabaseReconciler) finalizeMariaDBBackups(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, live *geassv1alpha1.GeassBackupArchive) (time.Duration, error) {
	log := logf.FromContext(ctx)
	mdb := &mariadbv1.MariaDB{}
	if err := r.Get(ctx, client.ObjectKey{Name: db.Name, Namespace: wsNS}, mdb); err != nil || !r.mariadbServing(ctx, db, wsNS, mdb) {
		log.Info("Skipped final backup because the database is not running", "database", db.Name)
		return 0, r.retireMariaDBArchive(ctx, db, wsNS, live)
	}

	final := &mariadbv1.PhysicalBackup{}
	err := r.Get(ctx, client.ObjectKey{Name: mariadbFinalName(db), Namespace: wsNS}, final)
	switch {
	case apierrors.IsNotFound(err):
		// The copy is of the database at this moment, and is taken once: it has no schedule.
		secret := db.Name + backupCredentialsSuffix
		// The copy is staged on a volume of the database's own class, which in a project that requires node tags keeps
		// it on the nodes that carry them. The default class would not.
		storageClass, _, classErr := r.existingVolumeClass(ctx, db, wsNS)
		if classErr != nil {
			log.Info("Could not read the database's storage class for the final backup", "database", db.Name, "reason", classErr.Error())
			return 0, r.retireMariaDBArchive(ctx, db, wsNS, live)
		}
		spec := mariadbPhysicalBackupSpec(db, live, secret, storageClass, pinnedNodePlacement(db))
		spec.Schedule = nil
		spec.MaxRetention = nil
		final = &mariadbv1.PhysicalBackup{ObjectMeta: metav1.ObjectMeta{Name: mariadbFinalName(db), Namespace: wsNS}, Spec: spec}
		applyGeassLabels(final, db, "GeassDatabase")
		if createErr := r.Create(ctx, final); createErr != nil {
			log.Info("Could not request final backup", "database", db.Name, "reason", createErr.Error())
			return 0, r.retireMariaDBArchive(ctx, db, wsNS, live)
		}
		return finalBackupPollInterval, nil
	case err != nil:
		return 0, err
	}

	switch {
	case meta.IsStatusConditionTrue(final.Status.Conditions, mariadbv1.ConditionComplete):
	case !final.CreationTimestamp.IsZero() && r.now().Sub(final.CreationTimestamp.Time) > platform.FinalBackupWait:
		log.Info("Gave up waiting for final backup", "database", db.Name)
	default:
		return finalBackupPollInterval, nil
	}
	return 0, r.retireMariaDBArchive(ctx, db, wsNS, live)
}

// retireMariaDBArchive records the restore points that exist, then retires the archive. Reading the bucket is best
// effort: if it cannot be read the archive keeps what it last knew.
func (r *GeassDatabaseReconciler) retireMariaDBArchive(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, archive *geassv1alpha1.GeassBackupArchive) error {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: platform.BackupStorageSecretName(archive.Spec.Storage.Bucket), Namespace: platform.SystemNamespace}, secret); err == nil {
		storage := r.backupStorage(
			secretValue(secret, platform.BackupSecretKeyAccessKeyID),
			secretValue(secret, platform.BackupSecretKeySecretAccessKey),
			secretValue(secret, platform.BackupSecretKeyRegion),
		)
		keys, listErr := storage.ListKeys(archive.Spec.Storage.Bucket, platform.PhysicalObjectPrefix(archive.Spec.Project, archive.Spec.ServerName))
		if listErr == nil {
			observed := physicalObservation(platform.PhysicalRestorePoints(keys), nil)
			// The window ends where the operator last said the archived logs reached, or at the newest copy.
			if err := r.writeArchiveStatus(ctx, archive, observed, r.recoverableUntil(ctx, db, wsNS, observed)); err != nil {
				return err
			}
		} else {
			logf.FromContext(ctx).Info("Could not list backups", "database", db.Name, "reason", summarizeStorageError(listErr))
		}
	}
	return retireArchive(ctx, r.Client, archive, r.now())
}

// reconcileMariaDBRestore starts a database from another database's backups. A snapshot archive, which holds daily SQL
// copies, is loaded by a Job once the database is up. An archive of a replicated database is restored by the operator
// as the database is made, and is finished when the database is: RestoredAt records it, because a restore loaded a second
// time would overwrite what was written since. It returns why the database is not ready yet.
func (r *GeassDatabaseReconciler) reconcileMariaDBRestore(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, mdb *mariadbv1.MariaDB) (string, error) {
	archive := &geassv1alpha1.GeassBackupArchive{}
	if err := r.Get(ctx, client.ObjectKey{Name: db.Spec.RestoreFrom.Archive, Namespace: platform.SystemNamespace}, archive); err != nil {
		if apierrors.IsNotFound(err) {
			return "", platform.UserError("These backups are no longer available")
		}
		return "", err
	}
	if platform.ArchiveBackupKind(archive.Spec.Source) != platform.BackupKindContinuous {
		return r.reconcileDumpRestore(ctx, db, wsNS)
	}
	// The operator restores it as it makes the database, so a database that was not made from the backups holds nothing
	// of them and must not be called restored.
	if mdb.Spec.BootstrapFrom == nil {
		return "", platform.UserError("This database was not started from the backups, and cannot be restored into afterwards")
	}
	return "", r.markRestored(ctx, db)
}

// annotationCredentialsOrigin is on a database restored from a physical copy, and names the database whose generated
// password it carries.
const annotationCredentialsOrigin = "geass.dev/credentials-origin"

// credentialsOrigin is the name the database's generated password is derived from: its own, unless it was restored from
// a physical copy, which holds the logins of the server it was taken from.
func credentialsOrigin(db *geassv1alpha1.GeassDatabase) string {
	if origin := db.Annotations[annotationCredentialsOrigin]; origin != "" {
		return origin
	}
	return db.Name
}

// inheritMariaDBCredentials gives a database that is restored from a physical copy the password the copy carries. The
// operator reaches the restored server as root with the password of the new database's Secret, and the data holds the
// original's, so with a password of its own the database never gets past initializing. It must come before the Secret is
// made. A database restored from daily SQL copies loads its data into logins of its own, and keeps them.
func (r *GeassDatabaseReconciler) inheritMariaDBCredentials(ctx context.Context, db *geassv1alpha1.GeassDatabase) error {
	restore := db.Spec.RestoreFrom
	if restore == nil || db.Status.RestoredAt != nil || databaseEngine(db) != geassv1alpha1.DatabaseEngineMariaDB || db.Annotations[annotationCredentialsOrigin] != "" {
		return nil
	}
	archive := &geassv1alpha1.GeassBackupArchive{}
	if err := r.Get(ctx, client.ObjectKey{Name: restore.Archive, Namespace: platform.SystemNamespace}, archive); err != nil {
		// The restore reports a backup that is gone, with the words for it, when it begins.
		return client.IgnoreNotFound(err)
	}
	if platform.ArchiveBackupKind(archive.Spec.Source) != platform.BackupKindContinuous {
		return nil
	}
	origin := firstNonEmpty(archive.Spec.Source.CredentialsOrigin, archive.Spec.Database)
	latest := &geassv1alpha1.GeassDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(db), latest); err != nil {
		return err
	}
	base := latest.DeepCopy()
	if latest.Annotations == nil {
		latest.Annotations = map[string]string{}
	}
	latest.Annotations[annotationCredentialsOrigin] = origin
	if err := r.Patch(ctx, latest, client.MergeFrom(base)); err != nil {
		return err
	}
	if db.Annotations == nil {
		db.Annotations = map[string]string{}
	}
	db.Annotations[annotationCredentialsOrigin] = origin
	return nil
}

// correctRestoredPassword puts the password a restore carries into the database's Secret when the Secret was made with
// another before the origin was known (a stale read of the archive, or a manifest that named a backup made later). The
// operator reaches the restored server with the Secret's password, so a wrong one never gets past initializing. It
// can only be changed until the server exists: after that the data holds whatever it was started with.
func (r *GeassDatabaseReconciler) correctRestoredPassword(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) error {
	origin := db.Annotations[annotationCredentialsOrigin]
	if origin == "" || db.Status.RestoredAt != nil {
		return nil
	}
	if existing, err := r.existingMariaDB(ctx, db, wsNS); err != nil || existing != nil {
		return err
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: db.Name + "-bootstrap", Namespace: wsNS}, secret); err != nil {
		return client.IgnoreNotFound(err)
	}
	want := origin + "-password"
	if secretValue(secret, "password") == want {
		return nil
	}
	base := secret.DeepCopy()
	setSecretStrings(secret, map[string]string{"password": want})
	return r.Patch(ctx, secret, client.MergeFrom(base))
}

// mariadbRestoreBootstrap prepares what a database needs to start from another one's backups: a copy and an archive of
// logs that read the source's folders, in the database's own namespace, and the credentials for them. They are only read
// while the database is made, and are kept as long as the database is, because the operator keeps what it was made from
// in the database's spec. It returns the bootstrap for the database, or nil for a database that is not a restore.
func (r *GeassDatabaseReconciler) mariadbRestoreBootstrap(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string) (*mariadbv1.BootstrapFrom, error) {
	restore := db.Spec.RestoreFrom
	if restore == nil || db.Status.RestoredAt != nil || databaseEngine(db) != geassv1alpha1.DatabaseEngineMariaDB {
		return nil, nil
	}
	archive := &geassv1alpha1.GeassBackupArchive{}
	if err := r.Get(ctx, client.ObjectKey{Name: restore.Archive, Namespace: platform.SystemNamespace}, archive); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, platform.UserError("These backups are no longer available")
		}
		return nil, err
	}
	if platform.ArchiveBackupKind(archive.Spec.Source) != platform.BackupKindContinuous {
		return nil, nil
	}
	if err := validateRestore(db, archive); err != nil {
		return nil, err
	}
	accessKey, secretKey, region, err := r.provisionBackupStorage(ctx, archive.Spec.Project, &archive.Spec.Storage)
	if err != nil {
		return nil, platform.Userf("The backup storage is not available: %s", summarizeStorageError(err))
	}
	secretName := db.Name + restoreCredentialsSuffix
	if err := r.ensureWorkspaceSecret(ctx, db, wsNS, secretName, accessKey, secretKey, region); err != nil {
		return nil, err
	}
	source := archive.DeepCopy()
	source.Spec.Storage.Region = region

	// The copy is never run: it only says where the copies are, for the operator to read.
	backup := &mariadbv1.PhysicalBackup{ObjectMeta: metav1.ObjectMeta{Name: mariadbRestoreSourceName(db), Namespace: wsNS}}
	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, backup, func() error {
		applyGeassLabels(backup, db, "GeassDatabase")
		backup.Spec = mariadbv1.PhysicalBackupSpec{
			MariaDBRef: mariadbv1.MariaDBRef{Name: db.Name, WaitForIt: utilptr.To(false)},
			Schedule:   &mariadbv1.PhysicalBackupSchedule{Cron: "0 0 1 1 *", Suspend: true},
			Storage: mariadbv1.PhysicalBackupStorage{S3: mariadbBucket(
				source.Spec.Storage.Bucket, region, platform.PhysicalObjectPrefix(source.Spec.Project, source.Spec.ServerName), secretName)},
			Compression: "gzip",
		}
		return setSameNamespaceOwner(db, backup, r.Scheme)
	}); err != nil {
		return nil, err
	}
	pitr := &mariadbv1.PointInTimeRecovery{ObjectMeta: metav1.ObjectMeta{Name: mariadbRestoreSourceName(db), Namespace: wsNS}}
	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, pitr, func() error {
		applyGeassLabels(pitr, db, "GeassDatabase")
		pitr.Spec = mariadbv1.PointInTimeRecoverySpec{
			PhysicalBackupRef: mariadbv1.LocalRef{Name: mariadbRestoreSourceName(db)},
			Storage: mariadbv1.PhysicalBackupStorage{S3: mariadbBucket(
				source.Spec.Storage.Bucket, region, platform.BinlogObjectPrefix(source.Spec.Project, source.Spec.ServerName), secretName)},
			Compression: "gzip",
			// A restore to a chosen moment fails when the logs do not reach it, rather than coming up with older data
			// than was asked for. One to the latest state, or to a copy, has nothing to miss.
			StrictMode: utilptr.To(restore.Time != nil),
		}
		return setSameNamespaceOwner(db, pitr, r.Scheme)
	}); err != nil {
		return nil, err
	}

	bootstrap := &mariadbv1.BootstrapFrom{
		PointInTimeRecoveryRef: &mariadbv1.LocalRef{Name: mariadbRestoreSourceName(db)},
		StagingStorage:         stagingStorage(db, storageClass),
	}
	switch {
	case restore.Time != nil:
		bootstrap.TargetRecoveryTime = restore.Time.DeepCopy()
	case restore.RestorePoint != "":
		// A restore point is one physical copy: the moment it was taken, which the operator matches to the copy.
		at, ok := platform.ParseRestorePointTime(restore.RestorePoint)
		if !ok {
			return nil, platform.UserError("The chosen restore point is not available")
		}
		moment := metav1.NewTime(at)
		bootstrap.TargetRecoveryTime = &moment
	}
	return bootstrap, nil
}
