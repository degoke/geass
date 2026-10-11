package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Backups of MySQL and SQLite databases. They have no change log to archive, so a Job takes a full
// copy every night and the copies in the project's bucket are the restore points. Everything else
// follows the Postgres design: the same archive record, janitor, storage credentials, final backup
// on delete, and restore into a new database. See geassdatabase_dumpjobs.go for what the Jobs run.

const (
	labelBackupFor  = "geass.dev/backup-for"
	labelBackupRole = "geass.dev/backup-role"

	roleBackup  = "backup"
	roleFinal   = "final"
	roleRestore = "restore"

	backupJobRetries   int32 = 1
	backupJobDeadline  int64 = 3600
	restoreJobRetries  int32 = 2
	restoreJobDeadline int64 = 6 * 3600

	// firstBackupRetryInterval is how long a failed first backup waits before it is tried again.
	firstBackupRetryInterval = 10 * time.Minute

	messageRestoreFailed = "The restore could not be completed. Check that the backups are still available, then restore again"
)

func dumpCronJobName(db *geassv1alpha1.GeassDatabase) string { return db.Name + "-backup" }

// dumpOutcome is what a database's snapshot backups mean for its readiness.
type dumpOutcome struct {
	// waiting, when set, is why the database is not ready yet: its data is still being restored.
	waiting string
	// backups is whether backups are on, which decides if their status needs refreshing later.
	backups bool
	// failed is true when backups could not be brought up to date this time. The database is still
	// ready, because its data is fine, and is looked at again soon.
	failed bool
}

// dumpTarget finds how Jobs reach a MySQL or SQLite database, and whether it is running. A MySQL
// dump must come from the server's own image, so the image is read from its workload.
func (r *GeassDatabaseReconciler) dumpTarget(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (*dumpTarget, bool, error) {
	if databaseEngine(db) == geassv1alpha1.DatabaseEngineSQLite {
		deploy := &appsv1.Deployment{}
		if err := r.Get(ctx, client.ObjectKey{Name: db.Name, Namespace: wsNS}, deploy); err != nil {
			return nil, false, fmt.Errorf("the database is not running yet: %w", err)
		}
		target := &dumpTarget{host: fmt.Sprintf("%s.%s.svc", db.Name, wsNS), port: platform.SQLiteDefaultPort, image: platform.SQLiteImage}
		return target, deploymentReady(deploy, 1), nil
	}

	var sets appsv1.StatefulSetList
	release := mysqlChartName(db.Name)
	if err := r.List(ctx, &sets, client.InNamespace(wsNS), client.MatchingLabels{
		"app.kubernetes.io/instance": release, "app.kubernetes.io/component": "primary",
	}); err != nil {
		return nil, false, err
	}
	if len(sets.Items) == 0 {
		return nil, false, fmt.Errorf("the database is not running yet")
	}
	set := sets.Items[0]
	containers := set.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return nil, false, fmt.Errorf("the database is not running yet")
	}
	image := containers[0].Image
	for _, container := range containers {
		if container.Name == "mysql" {
			image = container.Image
		}
	}
	target := &dumpTarget{host: mysqlHost(db, wsNS), port: platform.MySQLDefaultPort, image: image, database: databaseName(db)}
	return target, set.Status.ReadyReplicas >= 1, nil
}

func dumpInputFor(db *geassv1alpha1.GeassDatabase, archive *geassv1alpha1.GeassBackupArchive, credentialsSecret string, target dumpTarget) dumpJobInput {
	return dumpJobInput{
		db: db, target: target, objectBase: platform.BackupObjectBaseURL(archive.Spec.Storage.Bucket, archive.Spec.Storage.Region),
		prefix:            platform.DumpObjectPrefix(archive.Spec.Project, archive.Spec.ServerName),
		credentialsSecret: credentialsSecret,
	}
}

func dumpJobSpec(in dumpJobInput, role string, retries int32, deadline int64) batchv1.JobSpec {
	return batchv1.JobSpec{
		BackoffLimit: &retries, ActiveDeadlineSeconds: &deadline,
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{labelBackupFor: in.db.Name, labelBackupRole: role}},
			Spec:       dumpPodSpec(in),
		},
	}
}

func (r *GeassDatabaseReconciler) newBackupJob(db *geassv1alpha1.GeassDatabase, wsNS, name, role string, in dumpJobInput) *batchv1.Job {
	retries, deadline := backupJobRetries, backupJobDeadline
	if role == roleRestore {
		retries, deadline = restoreJobRetries, restoreJobDeadline
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS},
		Spec:       dumpJobSpec(in, role, retries, deadline),
	}
	applyGeassLabels(job, db, "GeassDatabase")
	job.Labels[labelBackupFor], job.Labels[labelBackupRole] = db.Name, role
	return job
}

// jobFailedAt is when a Job gave up, or fallback when it does not say.
func jobFailedAt(job *batchv1.Job, fallback time.Time) time.Time {
	at := fallback
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && !condition.LastTransitionTime.IsZero() {
			at = condition.LastTransitionTime.Time
		}
	}
	return at
}

func jobFinished(job *batchv1.Job) (complete, failed bool) {
	for _, condition := range job.Status.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		switch condition.Type {
		case batchv1.JobComplete:
			complete = true
		case batchv1.JobFailed:
			failed = true
		}
	}
	return complete, failed
}

// reconcileDumpBackups restores a new database from backups, if it is one, and then keeps its
// snapshot backups going. It runs once the database itself is up. A refused or failed restore is
// an error written for end users. A problem with the backups themselves is not: it is logged and
// reported in the outcome, because the database works without them.
func (r *GeassDatabaseReconciler) reconcileDumpBackups(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (dumpOutcome, error) {
	var outcome dumpOutcome
	if db.Spec.RestoreFrom != nil && db.Status.RestoredAt == nil {
		waiting, err := r.reconcileDumpRestore(ctx, db, wsNS)
		if err != nil || waiting != "" {
			outcome.waiting = waiting
			return outcome, err
		}
	}
	if err := r.maintainDumpBackups(ctx, db, wsNS, &outcome); err != nil {
		backupProblem(ctx, db, err)
		outcome.failed = true
	}
	return outcome, nil
}

func (r *GeassDatabaseReconciler) maintainDumpBackups(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, outcome *dumpOutcome) error {
	plan, err := r.planBackups(ctx, db, wsNS)
	if err != nil {
		return err
	}
	if plan == nil {
		return r.windDownBackups(ctx, db, wsNS)
	}
	outcome.backups = true
	if plan.archive == nil {
		return r.reconcileDumpLifecycle(ctx, db, wsNS, plan, nil)
	}
	target, _, err := r.dumpTarget(ctx, db, wsNS)
	if err != nil {
		return err
	}
	return r.reconcileDumpLifecycle(ctx, db, wsNS, plan, target)
}

// reconcileDumpLifecycle keeps the nightly Job, the first backup, the archive record and the
// dashboard summary in step with the snapshots that exist. target may be nil only while there is no
// archive, because without one there is nothing to schedule.
func (r *GeassDatabaseReconciler) reconcileDumpLifecycle(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, plan *backupPlan, target *dumpTarget) error {
	if plan.archive == nil || target == nil {
		state, message := platform.DatabaseBackupState(plan.health)
		return r.setBackupStatus(ctx, db, &geassv1alpha1.GeassDatabaseBackupStatus{State: state, Message: message})
	}
	archive := plan.archive
	in := dumpInputFor(db, archive, plan.secretName, *target)
	if err := r.reconcileDumpSchedule(ctx, db, wsNS, in); err != nil {
		return err
	}

	keys, listed := r.listSnapshotKeys(ctx, db, plan)
	if listed {
		keys = r.pruneSnapshots(ctx, db, plan, keys)
	}
	points := platform.DumpRestorePoints(keys)
	failure, err := r.latestBackupFailure(ctx, db, wsNS, archive)
	if err != nil {
		return err
	}
	observed := dumpObservation(points, failure)
	if listed {
		if len(points) == 0 {
			if err := r.ensureFirstBackup(ctx, db, wsNS, archive, in); err != nil {
				return err
			}
		}
		if err := r.writeArchiveStatus(ctx, archive, observed, observed.lastSuccess); err != nil {
			return err
		}
	} else if db.Status.Backup != nil {
		// A bucket that could not be read says nothing about the backups in it, so the last known
		// state stands instead of flipping to something the data does not support.
		return nil
	}
	return r.setBackupStatus(ctx, db, backupStatusFor(plan.health, observed, archive.Name))
}

// dumpObservation describes snapshots as the shared status code expects: their range is the window,
// and the newest is both the last backup and the latest moment that can be restored.
func dumpObservation(points []geassv1alpha1.GeassRestorePoint, lastFailure *time.Time) observedBackups {
	observed := observedBackups{restorePoints: points, lastFailure: lastFailure}
	if len(points) > 0 {
		newest, oldest := points[0].Time.Time, points[len(points)-1].Time.Time
		observed.lastSuccess, observed.first = &newest, &oldest
	}
	return observed
}

// listSnapshotKeys reads the snapshots in the bucket, which is the truth: unlike anything the
// operator remembers, it reflects what was added, pruned or lost. listed is false when the bucket
// could not be read.
func (r *GeassDatabaseReconciler) listSnapshotKeys(ctx context.Context, db *geassv1alpha1.GeassDatabase, plan *backupPlan) ([]string, bool) {
	if plan.access == nil || plan.archive == nil {
		return nil, false
	}
	archive := plan.archive
	storage := r.backupStorage(plan.access.accessKey, plan.access.secretKey, plan.access.region)
	keys, err := storage.ListKeys(archive.Spec.Storage.Bucket, platform.DumpObjectPrefix(archive.Spec.Project, archive.Spec.ServerName))
	if err != nil {
		logf.FromContext(ctx).Info("Could not list backups", "database", db.Name, "reason", summarizeStorageError(err))
		return nil, false
	}
	return keys, true
}

// pruneSnapshots removes snapshots older than the archive's retention, never the newest, and
// returns the keys that are left. A snapshot that cannot be removed stays in the list because it is
// still there to restore from; failing to prune is not a reason to fail the database.
func (r *GeassDatabaseReconciler) pruneSnapshots(ctx context.Context, db *geassv1alpha1.GeassDatabase, plan *backupPlan, keys []string) []string {
	expired := platform.ExpiredDumpKeys(keys, plan.archive.Spec.RetentionDays, r.now())
	if len(expired) == 0 {
		return keys
	}
	storage := r.backupStorage(plan.access.accessKey, plan.access.secretKey, plan.access.region)
	removed := make(map[string]bool, len(expired))
	for _, key := range expired {
		if err := storage.DeleteObject(plan.archive.Spec.Storage.Bucket, key); err != nil {
			logf.FromContext(ctx).Info("Could not remove expired backup", "database", db.Name, "reason", summarizeStorageError(err))
			continue
		}
		removed[key] = true
	}
	remaining := make([]string, 0, len(keys))
	for _, key := range keys {
		if !removed[key] {
			remaining = append(remaining, key)
		}
	}
	return remaining
}

// latestBackupFailure is when the newest backup Job of this archive gave up, if any. Restores are
// not backups, and a failure from before the archive existed belongs to an earlier one.
func (r *GeassDatabaseReconciler) latestBackupFailure(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, archive *geassv1alpha1.GeassBackupArchive) (*time.Time, error) {
	var jobs batchv1.JobList
	if err := r.List(ctx, &jobs, client.InNamespace(wsNS), client.MatchingLabels{labelBackupFor: db.Name}); err != nil {
		return nil, err
	}
	var latest *time.Time
	for i := range jobs.Items {
		job := &jobs.Items[i]
		if job.Labels[labelBackupRole] == roleRestore {
			continue
		}
		if _, failed := jobFinished(job); !failed {
			continue
		}
		at := jobFailedAt(job, r.now())
		if at.Before(archive.CreationTimestamp.Time) {
			continue
		}
		if latest == nil || at.After(*latest) {
			failure := at
			latest = &failure
		}
	}
	return latest, nil
}

func (r *GeassDatabaseReconciler) reconcileDumpSchedule(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, in dumpJobInput) error {
	cron := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: dumpCronJobName(db), Namespace: wsNS}}
	history, failedHistory, startingDeadline := int32(1), int32(3), int64(3600)
	// The schedule is written in UTC, like the Postgres one, whatever zone the host runs in.
	timeZone := "Etc/UTC"
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cron, func() error {
		applyGeassLabels(cron, db, "GeassDatabase")
		cron.Labels[labelBackupFor] = db.Name
		cron.Spec = batchv1.CronJobSpec{
			Schedule: platform.DumpSchedule(db.UID),
			TimeZone: &timeZone,
			// Two dumps of one database at once would only slow both down.
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			StartingDeadlineSeconds:    &startingDeadline,
			SuccessfulJobsHistoryLimit: &history,
			FailedJobsHistoryLimit:     &failedHistory,
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{labelBackupFor: db.Name, labelBackupRole: roleBackup}},
				Spec:       dumpJobSpec(in, roleBackup, backupJobRetries, backupJobDeadline),
			},
		}
		return nil
	})
	return err
}

// ensureFirstBackup takes a backup right away, so a database is not unprotected until the first
// night. The Job is named after the archive and is kept. It is only asked for while there is no
// snapshot at all, and if it failed it is replaced after a while, so one bad moment (a database
// still starting, a storage hiccup) does not leave the database unprotected until the nightly run.
func (r *GeassDatabaseReconciler) ensureFirstBackup(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, archive *geassv1alpha1.GeassBackupArchive, in dumpJobInput) error {
	name := archive.Name + "-initial"
	job := &batchv1.Job{}
	err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: wsNS}, job)
	switch {
	case err == nil:
		_, failed := jobFinished(job)
		if !failed || r.now().Sub(jobFailedAt(job, job.CreationTimestamp.Time)) < firstBackupRetryInterval {
			return nil
		}
		if err := client.IgnoreNotFound(r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground))); err != nil {
			return err
		}
	case !apierrors.IsNotFound(err):
		return err
	}
	return client.IgnoreAlreadyExists(r.Create(ctx, r.newBackupJob(db, wsNS, name, roleBackup, in)))
}

// reconcileDumpRestore loads a snapshot into a new database. It returns why the database is not
// ready yet while that is going on. Once it finishes, RestoredAt records it, because a snapshot
// loaded a second time would overwrite everything written since.
func (r *GeassDatabaseReconciler) reconcileDumpRestore(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (string, error) {
	restore := db.Spec.RestoreFrom
	archive := &geassv1alpha1.GeassBackupArchive{}
	if err := r.Get(ctx, client.ObjectKey{Name: restore.Archive, Namespace: platform.SystemNamespace}, archive); err != nil {
		if apierrors.IsNotFound(err) {
			return "", platform.UserError("These backups are no longer available")
		}
		return "", err
	}
	if err := validateRestore(db, archive); err != nil {
		return "", err
	}
	prefix := platform.DumpObjectPrefix(archive.Spec.Project, archive.Spec.ServerName)
	id := restore.RestorePoint
	if id == "" {
		if len(archive.Status.RestorePoints) == 0 {
			return "", platform.UserError("These backups have no restore points yet")
		}
		id = archive.Status.RestorePoints[0].ID
	}

	name := db.Name + "-restore"
	job := &batchv1.Job{}
	err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: wsNS}, job)
	switch {
	case err == nil:
		complete, failed := jobFinished(job)
		if failed {
			return "", platform.UserError(messageRestoreFailed)
		}
		if complete {
			return "", r.markRestored(ctx, db)
		}
		return messageRestoringDatabase, nil
	case !apierrors.IsNotFound(err):
		return "", err
	}

	accessKey, secretKey, region, err := r.provisionBackupStorage(ctx, archive.Spec.Project, &archive.Spec.Storage)
	if err != nil {
		return "", platform.Userf("The backup storage is not available: %s", summarizeStorageError(err))
	}
	secretName := db.Name + restoreCredentialsSuffix
	if err := r.ensureWorkspaceSecret(ctx, db, wsNS, secretName, accessKey, secretKey, region); err != nil {
		return "", err
	}
	target, _, err := r.dumpTarget(ctx, db, wsNS)
	if err != nil {
		return "", err
	}
	in := dumpJobInput{
		db: db, target: *target, objectBase: platform.BackupObjectBaseURL(archive.Spec.Storage.Bucket, region), prefix: prefix,
		credentialsSecret: secretName, restoreKey: prefix + id + "." + platform.DumpFileExtension,
	}
	if err := client.IgnoreAlreadyExists(r.Create(ctx, r.newBackupJob(db, wsNS, name, roleRestore, in))); err != nil {
		return "", err
	}
	return messageRestoringDatabase, nil
}

func (r *GeassDatabaseReconciler) markRestored(ctx context.Context, db *geassv1alpha1.GeassDatabase) error {
	latest := &geassv1alpha1.GeassDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(db), latest); err != nil {
		return err
	}
	now := metav1.NewTime(r.now())
	latest.Status.RestoredAt = &now
	if err := r.Status().Update(ctx, latest); err != nil {
		return err
	}
	db.Status.RestoredAt = &now
	return nil
}

// finalizeDumpBackups is what deleting a MySQL or SQLite database does about its backups: one last
// snapshot, so nothing written since the previous one is lost, then the archive is retired so the
// snapshots stay restorable for a while. Like the Postgres version it never holds up deletion for
// long, and every failure ends with the archive retired and the snapshots that already exist.
func (r *GeassDatabaseReconciler) finalizeDumpBackups(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, live *geassv1alpha1.GeassBackupArchive) (time.Duration, error) {
	log := logf.FromContext(ctx)
	target, running, err := r.dumpTarget(ctx, db, wsNS)
	if err != nil || !running {
		log.Info("Skipped final backup because the database is not running", "database", db.Name)
		return 0, r.retireDumpArchive(ctx, db, live)
	}

	const jobSuffix = "-backup-final"
	name := db.Name + jobSuffix
	job := &batchv1.Job{}
	err = r.Get(ctx, client.ObjectKey{Name: name, Namespace: wsNS}, job)
	switch {
	case apierrors.IsNotFound(err):
		in := dumpInputFor(db, live, db.Name+backupCredentialsSuffix, *target)
		if createErr := r.Create(ctx, r.newBackupJob(db, wsNS, name, roleFinal, in)); createErr != nil {
			log.Info("Could not request final backup", "database", db.Name, "reason", createErr.Error())
			return 0, r.retireDumpArchive(ctx, db, live)
		}
		return finalBackupPollInterval, nil
	case err != nil:
		return 0, err
	}

	complete, failed := jobFinished(job)
	switch {
	case complete:
	case failed:
		log.Info("Final backup failed", "database", db.Name)
	case !job.CreationTimestamp.IsZero() && r.now().Sub(job.CreationTimestamp.Time) > platform.FinalBackupWait:
		log.Info("Gave up waiting for final backup", "database", db.Name)
	default:
		return finalBackupPollInterval, nil
	}
	return 0, r.retireDumpArchive(ctx, db, live)
}

// retireDumpArchive records the restore points that exist, then retires the archive. Reading the
// bucket is best effort: if it cannot be read the archive keeps what it last knew.
func (r *GeassDatabaseReconciler) retireDumpArchive(ctx context.Context, db *geassv1alpha1.GeassDatabase, archive *geassv1alpha1.GeassBackupArchive) error {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: platform.BackupStorageSecretName(archive.Spec.Storage.Bucket), Namespace: platform.SystemNamespace}, secret); err == nil {
		storage := r.backupStorage(
			secretValue(secret, platform.BackupSecretKeyAccessKeyID),
			secretValue(secret, platform.BackupSecretKeySecretAccessKey),
			secretValue(secret, platform.BackupSecretKeyRegion),
		)
		keys, listErr := storage.ListKeys(archive.Spec.Storage.Bucket, platform.DumpObjectPrefix(archive.Spec.Project, archive.Spec.ServerName))
		if listErr == nil {
			observed := dumpObservation(platform.DumpRestorePoints(keys), nil)
			if err := r.writeArchiveStatus(ctx, archive, observed, observed.lastSuccess); err != nil {
				return err
			}
		} else {
			logf.FromContext(ctx).Info("Could not list backups", "database", db.Name, "reason", summarizeStorageError(listErr))
		}
	}
	return retireArchive(ctx, r.Client, archive, r.now())
}

// isBackupJob is whether a Job belongs to a database's backups or restore, the only Jobs the
// database controller cares about.
func isBackupJob(obj client.Object) bool { return obj.GetLabels()[labelBackupFor] != "" }

// databaseForBackupJob maps a backup or restore Job to the database it belongs to, so the database
// reacts when one finishes instead of waiting for the next poll. Databases live in the system
// namespace and their Jobs in the database's own.
func databaseForBackupJob(_ context.Context, obj client.Object) []ctrl.Request {
	name := obj.GetLabels()[labelBackupFor]
	if name == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}}}
}
