package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	mysqlRelease = "geass-mysql-orders"
	dumpArchive  = testDBServerName // the archive carries the folder's name
)

func dumpWorkload(engine geassv1alpha1.GeassDatabaseEngine, ready bool) client.Object {
	readyReplicas := int32(0)
	if ready {
		readyReplicas = 1
	}
	one := int32(1)
	if engine == geassv1alpha1.DatabaseEngineSQLite {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: testDevTargetNS},
			Spec:       appsv1.DeploymentSpec{Replicas: &one},
			Status:     appsv1.DeploymentStatus{Replicas: one, ReadyReplicas: readyReplicas, AvailableReplicas: readyReplicas, UpdatedReplicas: one},
		}
	}
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: mysqlRelease + "-primary", Namespace: testDevTargetNS,
			Labels: map[string]string{"app.kubernetes.io/instance": mysqlRelease, "app.kubernetes.io/component": "primary"},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &one,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "mysql", Image: testMySQLImage}}}},
		},
		Status: appsv1.StatefulSetStatus{Replicas: one, ReadyReplicas: readyReplicas},
	}
}

// dumpEnv is a backed-up MySQL or SQLite database whose workload is running.
func dumpEnv(t *testing.T, engine geassv1alpha1.GeassDatabaseEngine, mutate ...func(*backupEnvConfig)) *backupEnv {
	t.Helper()
	size := resource.MustParse("5Gi")
	all := make([]func(*backupEnvConfig), 0, 1+len(mutate))
	all = append(all, func(c *backupEnvConfig) {
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.Engine, db.Spec.Version, db.Spec.StorageSize = engine, "", &size
			if engine == geassv1alpha1.DatabaseEngineSQLite {
				db.Spec.StorageSize = nil
			}
		})
		c.extraObjects = append(c.extraObjects, dumpWorkload(engine, true))
	})
	return newBackupEnv(t, append(all, mutate...)...)
}

func snapshotKey(ext string, at time.Time) string {
	return testDumpPrefix + at.UTC().Format("20060102T150405Z") + "." + ext
}

func (e *backupEnv) job(name string) *batchv1.Job {
	e.t.Helper()
	var job batchv1.Job
	require.NoError(e.t, e.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: e.wsNS}, &job))
	return &job
}

func (e *backupEnv) cronJob() *batchv1.CronJob {
	e.t.Helper()
	var cron batchv1.CronJob
	require.NoError(e.t, e.client.Get(context.Background(), client.ObjectKey{Name: testDBName + "-backup", Namespace: e.wsNS}, &cron))
	return &cron
}

func (e *backupEnv) finishJob(name string, conditionType batchv1.JobConditionType, at time.Time) {
	e.t.Helper()
	job := e.job(name)
	when := metav1.NewTime(at)
	job.Status.Conditions = []batchv1.JobCondition{{Type: conditionType, Status: corev1.ConditionTrue, LastTransitionTime: when}}
	if conditionType == batchv1.JobComplete {
		job.Status.Succeeded, job.Status.CompletionTime = 1, &when
	} else {
		job.Status.Failed = 2
	}
	// Job status is a subresource: a plain Update would silently drop it.
	require.NoError(e.t, e.client.Status().Update(context.Background(), job))
}

func (e *backupEnv) reconcileDump() dumpOutcome {
	e.t.Helper()
	outcome, err := e.r.reconcileDumpBackups(context.Background(), e.db, e.wsNS)
	require.NoError(e.t, err)
	return outcome
}

// --- what a backed-up database looks like ----------------------------------------------------

func TestMySQLArchiveRecordsWhatARestoreNeedsAndNothingItDoesNot(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NotNil(t, plan.archive)
	source := plan.archive.Spec.Source
	require.Equal(t, geassv1alpha1.DatabaseEngineMySQL, source.Engine)
	require.Empty(t, source.Version, "MySQL's version is chosen by the chart, so there is nothing to match on restore")
	require.Equal(t, testDBName, source.DatabaseName)
	require.True(t, source.StorageSize.Equal(resource.MustParse("5Gi")))
}

func TestSQLiteArchiveUsesItsOwnDefaultSizeAndHasNoDatabaseName(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineSQLite)
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	source := plan.archive.Spec.Source
	require.Equal(t, geassv1alpha1.DatabaseEngineSQLite, source.Engine)
	require.Empty(t, source.DatabaseName, "rqlite serves a single database")
	require.True(t, source.StorageSize.Equal(resource.MustParse("1Gi")), "SQLite databases default to 1Gi, not Postgres's 10Gi")
}

func TestRedisAndExternalDatabasesAreStillNotBackedUp(t *testing.T) {
	for name, mutate := range map[string]func(*geassv1alpha1.GeassDatabase){
		"redis": func(db *geassv1alpha1.GeassDatabase) { db.Spec.Engine = geassv1alpha1.DatabaseEngineRedis },
		"external mysql": func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.Engine = geassv1alpha1.DatabaseEngineMySQL
			db.Spec.Placement = geassv1alpha1.DatabasePlacementExternal
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := newBackupEnv(t, func(c *backupEnvConfig) { c.dbMutations = append(c.dbMutations, mutate) })
			plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
			require.NoError(t, err)
			require.Nil(t, plan)
		})
	}
}

// --- the schedule and the first backup -------------------------------------------------------

func TestMySQLBackupsAreScheduledAndTheFirstOneStartsAtOnce(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	outcome := env.reconcileDump()
	require.True(t, outcome.backups)
	require.Empty(t, outcome.waiting)

	cron := env.cronJob()
	require.Equal(t, platform.DumpSchedule(testDBUID), cron.Spec.Schedule)
	require.NotNil(t, cron.Spec.TimeZone)
	require.Equal(t, "Etc/UTC", *cron.Spec.TimeZone, "the schedule is written in UTC, whatever zone the control plane runs in")
	require.Equal(t, batchv1.ForbidConcurrent, cron.Spec.ConcurrencyPolicy, "two dumps of one database must never overlap")
	pod := cron.Spec.JobTemplate.Spec.Template.Spec
	require.Equal(t, testMySQLImage, pod.InitContainers[0].Image, "the dump runs in the database's own image, read from its workload")
	prefix, _ := envNamed(pod.Containers[0], "PREFIX")
	require.Equal(t, testDumpPrefix, prefix.Value)
	require.Equal(t, "https://"+testBackupBucket+".s3."+testBackupRegion+".amazonaws.com", mustEnv(t, pod.Containers[0], "S3_BASE"), "the bucket and region come from the archive")
	host, _ := envNamed(pod.InitContainers[0], "DB_HOST")
	require.Equal(t, "geass-mysql-orders.payments-dev.svc", host.Value)

	// Without this the database would sit unprotected until 02:xx the next night.
	initial := env.job(dumpArchive + "-initial")
	require.Equal(t, testDumpPrefix, mustEnv(t, initial.Spec.Template.Spec.Containers[0], "PREFIX"))
	require.Equal(t, "backup", initial.Labels[labelBackupRole])
	require.Equal(t, testDBName, initial.Labels[labelBackupFor])

	status := env.reload().Status.Backup
	require.Equal(t, geassv1alpha1.BackupStateStarting, status.State)
	require.Equal(t, "Taking the first backup", status.Message)
	require.Equal(t, dumpArchive, status.Archive)
}

func mustEnv(t *testing.T, container corev1.Container, name string) string {
	t.Helper()
	env, ok := envNamed(container, name)
	require.True(t, ok, name)
	return env.Value
}

func TestSQLiteBackupsAreScheduledInTheDatabasesOwnImage(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineSQLite)
	require.True(t, env.reconcileDump().backups)
	pod := env.cronJob().Spec.JobTemplate.Spec.Template.Spec
	require.Equal(t, platform.SQLiteImage, pod.InitContainers[0].Image)
	require.Equal(t, "orders.payments-dev.svc", mustEnv(t, pod.InitContainers[0], "DB_HOST"))
	require.Equal(t, "sql.gz", mustEnv(t, pod.Containers[0], "EXT"))
}

func TestReconcilingAgainDoesNotRepeatTheFirstBackupOrRewriteTheSchedule(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	env.reconcileDump()
	cronVersion := env.cronJob().ResourceVersion
	jobVersion := env.job(dumpArchive + "-initial").ResourceVersion
	dbVersion := env.reload().ResourceVersion

	env.reconcileDump()

	require.Equal(t, cronVersion, env.cronJob().ResourceVersion, "an unchanged schedule must not be rewritten")
	require.Equal(t, jobVersion, env.job(dumpArchive+"-initial").ResourceVersion)
	require.Equal(t, dbVersion, env.reload().ResourceVersion, "unchanged status must not be rewritten or the controller would loop")
	var jobs batchv1.JobList
	require.NoError(t, env.client.List(context.Background(), &jobs, client.InNamespace(env.wsNS)))
	require.Len(t, jobs.Items, 1)
}

func TestNoFirstBackupIsStartedWhenSnapshotsAlreadyExist(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	env.storage.allKeys = []string{snapshotKey("sql.gz", time.Now().Add(-time.Hour))}
	env.reconcileDump()
	var jobs batchv1.JobList
	require.NoError(t, env.client.List(context.Background(), &jobs, client.InNamespace(env.wsNS)))
	require.Empty(t, jobs.Items, "the schedule will do; a second copy now would only waste storage")
}

func TestNoFirstBackupIsStartedWhenTheBucketCannotBeListed(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	env.storage.listErr = errors.New("AccessDenied")
	env.reconcileDump() // asserts that a storage problem does not fail the database
	var jobs batchv1.JobList
	require.NoError(t, env.client.List(context.Background(), &jobs, client.InNamespace(env.wsNS)))
	require.Empty(t, jobs.Items, "an unreadable bucket is not the same as an empty one")
	require.True(t, env.exists(&batchv1.CronJob{}, env.wsNS, testDBName+"-backup"), "the schedule is still set up")
}

func TestMySQLBackupsWaitForTheDatabasesWorkload(t *testing.T) {
	env := newBackupEnv(t, func(c *backupEnvConfig) {
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) { db.Spec.Engine = geassv1alpha1.DatabaseEngineMySQL })
	})
	outcome, err := env.r.reconcileDumpBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err, "a backup problem must not make a database that works report as not ready")
	require.True(t, outcome.failed, "the dump image is read from the database's workload, which does not exist yet")
	require.Empty(t, outcome.waiting)
	require.False(t, env.exists(&batchv1.CronJob{}, env.wsNS, testDBName+"-backup"))
}

// --- what the dashboard is told --------------------------------------------------------------

func TestStatusComesFromTheSnapshotsInTheBucket(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	now := utcSeconds(time.Now())
	env.storage.allKeys = []string{
		snapshotKey("sql.gz", now.Add(-50*time.Hour)),
		snapshotKey("sql.gz", now.Add(-2*time.Hour)),
		snapshotKey("sql.gz", now.Add(-26*time.Hour)),
		testDumpPrefix + "notes.txt",
		"payments/another-database-99999999/dumps/" + now.Format("20060102T150405Z") + ".sql.gz",
	}
	env.reconcileDump()

	archive := env.archive(dumpArchive)
	require.Len(t, archive.Status.RestorePoints, 3)
	require.Equal(t, now.Add(-2*time.Hour).Format("20060102T150405Z"), archive.Status.RestorePoints[0].ID, "newest first")
	require.True(t, archive.Status.LastBackupTime.Time.Equal(now.Add(-2*time.Hour)))
	require.True(t, archive.Status.RecoveryWindowStart.Time.Equal(now.Add(-50*time.Hour)))
	require.True(t, archive.Status.RecoveryWindowEnd.Time.Equal(now.Add(-2*time.Hour)), "there is nothing newer to restore to")

	status := env.reload().Status.Backup
	require.Equal(t, geassv1alpha1.BackupStateProtected, status.State)
	require.Equal(t, "Backups are running", status.Message)
	require.True(t, status.LastBackupTime.Time.Equal(now.Add(-2*time.Hour)))
	require.True(t, status.RecoveryWindowStart.Time.Equal(now.Add(-50*time.Hour)))
}

func TestAFailedScheduledJobIsFoundByItsLabels(t *testing.T) {
	now := utcSeconds(time.Now())
	failed := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: "orders-backup-29000000", Namespace: testDevTargetNS,
			Labels: map[string]string{labelBackupFor: testDBName, labelBackupRole: "scheduled"},
		},
		Status: batchv1.JobStatus{Failed: 2, Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now.Add(-time.Hour))}}},
	}
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL, func(c *backupEnvConfig) { c.extraObjects = append(c.extraObjects, failed) })
	env.storage.allKeys = []string{snapshotKey("sql.gz", now.Add(-26*time.Hour))}
	env.reconcileDump()

	status := env.reload().Status.Backup
	require.Equal(t, geassv1alpha1.BackupStateAtRisk, status.State)
	require.Equal(t, "The latest backup failed", status.Message)
}

func TestAnOldFailureBeforeALaterSuccessIsForgotten(t *testing.T) {
	now := utcSeconds(time.Now())
	failed := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-backup-1", Namespace: testDevTargetNS, Labels: map[string]string{labelBackupFor: testDBName, labelBackupRole: "scheduled"}},
		Status:     batchv1.JobStatus{Failed: 2, Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now.Add(-30 * time.Hour))}}},
	}
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL, func(c *backupEnvConfig) { c.extraObjects = append(c.extraObjects, failed) })
	env.storage.allKeys = []string{snapshotKey("sql.gz", now.Add(-2*time.Hour))}
	env.reconcileDump()
	require.Equal(t, geassv1alpha1.BackupStateProtected, env.reload().Status.Backup.State)
}

func TestAnUnreadableBucketLeavesTheLastKnownStatusAlone(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	now := utcSeconds(time.Now())
	env.storage.allKeys = []string{snapshotKey("sql.gz", now.Add(-2*time.Hour))}
	env.reconcileDump()
	require.Len(t, env.archive(dumpArchive).Status.RestorePoints, 1)

	env.db = env.reload() // each reconcile starts from the database as the cluster has it now
	env.storage.listErr = errors.New("AccessDenied")
	env.reconcileDump()
	require.Len(t, env.archive(dumpArchive).Status.RestorePoints, 1, "a failed listing must not wipe the restore points")
	require.Equal(t, geassv1alpha1.BackupStateProtected, env.reload().Status.Backup.State)
}

func TestSnapshotDatabasesWithoutStorageSayTheyNeedIt(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL, func(c *backupEnvConfig) { c.storage = nil })
	outcome := env.reconcileDump()
	require.True(t, outcome.backups)
	require.Equal(t, geassv1alpha1.BackupStateNeedsStorage, env.reload().Status.Backup.State)
	require.False(t, env.exists(&batchv1.CronJob{}, env.wsNS, testDBName+"-backup"))
}

func TestTurningSnapshotBackupsOffStopsTheScheduleAndRetiresTheArchive(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	env.reconcileDump()
	require.True(t, env.exists(&batchv1.CronJob{}, env.wsNS, testDBName+"-backup"))

	env.db.Spec.Backup.Enabled = false
	outcome := env.reconcileDump()
	require.False(t, outcome.backups)
	require.False(t, env.exists(&batchv1.CronJob{}, env.wsNS, testDBName+"-backup"))
	require.NotNil(t, env.archive(dumpArchive).Spec.DeleteAfter, "earlier snapshots stay restorable for a while")
	require.Nil(t, env.reload().Status.Backup)
	require.Empty(t, env.storage.purged, "only the janitor deletes backup data")
}

// --- retention -------------------------------------------------------------------------------

func TestSnapshotsPastTheRetentionArePrunedByTheOperator(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	now := utcSeconds(time.Now())
	recent, week, old, ancient := snapshotKey("sql.gz", now.Add(-time.Hour)), snapshotKey("sql.gz", now.Add(-3*24*time.Hour)), snapshotKey("sql.gz", now.Add(-10*24*time.Hour)), snapshotKey("sql.gz", now.Add(-40*24*time.Hour))
	env.storage.allKeys = []string{recent, week, old, ancient}
	env.db.Spec.Backup.RetentionDays = 7
	env.reconcileDump()

	require.ElementsMatch(t, []string{old, ancient}, env.storage.deleted, "exactly the snapshots older than seven days")
	archive := env.archive(dumpArchive)
	require.Len(t, archive.Status.RestorePoints, 2, "pruned snapshots are no longer restore points")
	require.True(t, archive.Status.RecoveryWindowStart.Time.Equal(now.Add(-3*24*time.Hour)))
}

func TestTheNewestSnapshotIsKeptEvenWhenItIsPastTheRetention(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	now := utcSeconds(time.Now())
	env.storage.allKeys = []string{snapshotKey("sql.gz", now.Add(-40*24*time.Hour)), snapshotKey("sql.gz", now.Add(-35*24*time.Hour))}
	env.reconcileDump()
	require.Equal(t, []string{snapshotKey("sql.gz", now.Add(-40*24*time.Hour))}, env.storage.deleted, "backups that stopped a month ago must not lose their last copy")
	require.Len(t, env.archive(dumpArchive).Status.RestorePoints, 1)
}

func TestAPruneThatFailsDoesNotFailTheDatabaseOrHideTheSnapshots(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	now := utcSeconds(time.Now())
	env.storage.allKeys = []string{snapshotKey("sql.gz", now.Add(-time.Hour)), snapshotKey("sql.gz", now.Add(-10*24*time.Hour))}
	env.storage.deleteErr = errors.New("AccessDenied")
	env.reconcileDump() // asserts no error
	require.Len(t, env.archive(dumpArchive).Status.RestorePoints, 2, "a snapshot that could not be removed is still there to restore")
	require.Equal(t, geassv1alpha1.BackupStateProtected, env.reload().Status.Backup.State)
}

func TestNothingIsPrunedWhenTheBucketCouldNotBeListed(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	env.storage.allKeys = []string{snapshotKey("sql.gz", time.Now().Add(-100*24*time.Hour))}
	env.storage.listErr = errors.New("AccessDenied")
	env.reconcileDump()
	require.Empty(t, env.storage.deleted)
}

// --- restoring -------------------------------------------------------------------------------

func snapshotSource(engine geassv1alpha1.GeassDatabaseEngine, points ...string) *geassv1alpha1.GeassBackupArchive {
	size := resource.MustParse("5Gi")
	restorePoints := make([]geassv1alpha1.GeassRestorePoint, 0, len(points))
	for _, id := range points {
		at, _ := time.Parse("20060102T150405Z", id)
		restorePoints = append(restorePoints, geassv1alpha1.GeassRestorePoint{ID: id, Time: metav1.NewTime(at)})
	}
	name := "legacy-9f8e7d6c"
	source := geassv1alpha1.GeassBackupSource{Engine: engine, StorageSize: &size}
	if engine == geassv1alpha1.DatabaseEngineMySQL {
		source.DatabaseName = testDBName
	}
	return &geassv1alpha1.GeassBackupArchive{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassBackupArchiveSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev, Database: "legacy", DatabaseUID: "uid-legacy",
			ServerName: name, Storage: *defaultTestBackupStorage(), RetentionDays: 7, Source: source,
		},
		Status: geassv1alpha1.GeassBackupArchiveStatus{RestorePoints: restorePoints},
	}
}

func restoreEnv(t *testing.T, engine geassv1alpha1.GeassDatabaseEngine, restore geassv1alpha1.GeassDatabaseRestoreSpec, points ...string) (*backupEnv, *geassv1alpha1.GeassBackupArchive) {
	t.Helper()
	source := snapshotSource(engine, points...)
	restore.Archive = source.Name
	env := dumpEnv(t, engine, func(c *backupEnvConfig) {
		c.backup = nil
		c.extraObjects = append(c.extraObjects, source)
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) {
			size := resource.MustParse("5Gi")
			db.Spec.StorageSize, db.Spec.RestoreFrom = &size, &restore
		})
	})
	return env, source
}

func TestRestoringMySQLStartsAJobForTheChosenRestorePoint(t *testing.T) {
	env, source := restoreEnv(t, geassv1alpha1.DatabaseEngineMySQL, geassv1alpha1.GeassDatabaseRestoreSpec{RestorePoint: "20261010T020000Z"}, "20261011T020000Z", "20261010T020000Z")
	outcome := env.reconcileDump()
	require.Equal(t, messageRestoringDatabase, outcome.waiting, "the database is not ready until its data is back")

	job := env.job(testDBName + "-restore")
	key := mustEnv(t, job.Spec.Template.Spec.InitContainers[0], "KEY")
	require.Equal(t, "payments/"+source.Spec.ServerName+"/dumps/20261010T020000Z.sql.gz", key, "the folder is the source's, the file is the one chosen")
	require.Equal(t, "restore", job.Labels[labelBackupRole])
	accessKey, _ := envNamed(job.Spec.Template.Spec.InitContainers[0], platform.BackupSecretKeyAccessKeyID)
	require.Equal(t, "orders-restore-credentials", accessKey.ValueFrom.SecretKeyRef.Name, "a restore reads the source's bucket with its own copy of the credentials")
	restoreSecret := env.secret(env.wsNS, "orders-restore-credentials")
	require.Equal(t, "AKIABACKUPUSER", string(restoreSecret.Data[platform.BackupSecretKeyAccessKeyID]))
	require.Nil(t, env.reload().Status.RestoredAt)
}

func TestRestoringWithoutAChoiceUsesTheNewestSnapshot(t *testing.T) {
	env, _ := restoreEnv(t, geassv1alpha1.DatabaseEngineSQLite, geassv1alpha1.GeassDatabaseRestoreSpec{}, "20261011T020000Z", "20261010T020000Z")
	require.Equal(t, messageRestoringDatabase, env.reconcileDump().waiting)
	key := mustEnv(t, env.job(testDBName + "-restore").Spec.Template.Spec.InitContainers[0], "KEY")
	require.Contains(t, key, "/dumps/20261011T020000Z.sql.gz")
}

func TestARestoreThatFinishedMarksTheDatabaseRestoredAndNeverRunsAgain(t *testing.T) {
	env, _ := restoreEnv(t, geassv1alpha1.DatabaseEngineMySQL, geassv1alpha1.GeassDatabaseRestoreSpec{}, "20261011T020000Z")
	require.Equal(t, messageRestoringDatabase, env.reconcileDump().waiting)
	env.finishJob(testDBName+"-restore", batchv1.JobComplete, time.Now())

	outcome := env.reconcileDump()
	require.Empty(t, outcome.waiting, "once the data is back the database can become ready")
	require.NotNil(t, env.reload().Status.RestoredAt)

	// If the Job is lost (cleaned up by hand, or by a TTL), loading the old snapshot again would
	// overwrite everything written since.
	require.NoError(t, env.client.Delete(context.Background(), env.job(testDBName+"-restore")))
	env.db.Status.RestoredAt = env.reload().Status.RestoredAt
	outcome = env.reconcileDump()
	require.Empty(t, outcome.waiting)
	require.False(t, env.exists(&batchv1.Job{}, env.wsNS, testDBName+"-restore"), "a restored database must never be restored a second time")
}

func TestARestoreThatFailedIsReportedInPlainLanguage(t *testing.T) {
	env, _ := restoreEnv(t, geassv1alpha1.DatabaseEngineMySQL, geassv1alpha1.GeassDatabaseRestoreSpec{}, "20261011T020000Z")
	env.reconcileDump()
	env.finishJob(testDBName+"-restore", batchv1.JobFailed, time.Now())

	_, err := env.r.reconcileDumpBackups(context.Background(), env.db, env.wsNS)
	require.ErrorContains(t, err, "restore could not be completed")
	require.Nil(t, env.reload().Status.RestoredAt)
	for _, term := range []string{"Job", "pod", "Kubernetes", "mysqldump"} {
		require.NotContains(t, err.Error(), term)
	}
}

func TestUnsafeSnapshotRestoresAreRefusedBeforeAnythingRuns(t *testing.T) {
	at := metav1.NewTime(time.Now().Add(-time.Hour))
	tests := map[string]struct {
		engine  geassv1alpha1.GeassDatabaseEngine
		restore geassv1alpha1.GeassDatabaseRestoreSpec
		mutate  func(*backupEnvConfig)
		want    string
	}{
		"an unknown restore point": {engine: geassv1alpha1.DatabaseEngineMySQL, restore: geassv1alpha1.GeassDatabaseRestoreSpec{RestorePoint: "20990101T000000Z"}, want: "restore point is not available"},
		"an exact time":            {engine: geassv1alpha1.DatabaseEngineMySQL, restore: geassv1alpha1.GeassDatabaseRestoreSpec{Time: &at}, want: "only be restored to a restore point"},
		"another engine's backups": {engine: geassv1alpha1.DatabaseEngineSQLite, restore: geassv1alpha1.GeassDatabaseRestoreSpec{}, want: "same engine", mutate: func(c *backupEnvConfig) {
			c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) { db.Spec.Engine = geassv1alpha1.DatabaseEngineMySQL })
		}},
		"another project's backups": {engine: geassv1alpha1.DatabaseEngineMySQL, restore: geassv1alpha1.GeassDatabaseRestoreSpec{}, want: "another project", mutate: func(c *backupEnvConfig) {
			c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) { db.Spec.Project = "intruder" })
		}},
		"a different database name": {engine: geassv1alpha1.DatabaseEngineMySQL, restore: geassv1alpha1.GeassDatabaseRestoreSpec{}, want: "database name " + testDBName, mutate: func(c *backupEnvConfig) {
			c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) { db.Spec.DatabaseName = "other" })
		}},
		"a smaller disk": {engine: geassv1alpha1.DatabaseEngineMySQL, restore: geassv1alpha1.GeassDatabaseRestoreSpec{}, want: "at least 5Gi", mutate: func(c *backupEnvConfig) {
			c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) {
				small := resource.MustParse("1Gi")
				db.Spec.StorageSize = &small
			})
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			source := snapshotSource(tt.engine, "20261011T020000Z")
			tt.restore.Archive = source.Name
			mutations := []func(*backupEnvConfig){func(c *backupEnvConfig) {
				c.backup = nil
				c.extraObjects = append(c.extraObjects, source)
				c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) { db.Spec.RestoreFrom = &tt.restore })
			}}
			if tt.mutate != nil {
				mutations = append(mutations, tt.mutate)
			}
			env := dumpEnv(t, tt.engine, mutations...)
			// dumpEnv applies the engine of the *source*; the "another engine" case flips the database.
			_, err := env.r.reconcileDumpBackups(context.Background(), env.db, env.wsNS)
			require.ErrorContains(t, err, tt.want)
			require.False(t, env.exists(&batchv1.Job{}, env.wsNS, testDBName+"-restore"), "a refused restore must not start anything")
		})
	}
}

func TestRestoringFromPurgedBackupsSaysSo(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL, func(c *backupEnvConfig) {
		c.backup = nil
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.RestoreFrom = &geassv1alpha1.GeassDatabaseRestoreSpec{Archive: "gone-12345678"}
		})
	})
	_, err := env.r.reconcileDumpBackups(context.Background(), env.db, env.wsNS)
	require.ErrorContains(t, err, "backups are no longer available")
}

func TestRestoringBackupsThatHaveNoSnapshotsYetSaysSo(t *testing.T) {
	env, _ := restoreEnv(t, geassv1alpha1.DatabaseEngineMySQL, geassv1alpha1.GeassDatabaseRestoreSpec{})
	_, err := env.r.reconcileDumpBackups(context.Background(), env.db, env.wsNS)
	require.ErrorContains(t, err, "no restore points")
}

// --- deleting a snapshot-backed database -----------------------------------------------------

func dumpDeletingEnv(t *testing.T, engine geassv1alpha1.GeassDatabaseEngine, ready bool, deletedAgo time.Duration, objects ...client.Object) *backupEnv {
	t.Helper()
	deletedAt := metav1.NewTime(time.Now().Add(-deletedAgo))
	size := resource.MustParse("5Gi")
	return newBackupEnv(t, func(c *backupEnvConfig) {
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.Engine, db.Spec.Version, db.Spec.StorageSize = engine, "", &size
			db.Finalizers, db.DeletionTimestamp = []string{platform.FinalizerDatabase}, &deletedAt
		})
		c.extraObjects = append(c.extraObjects, dumpWorkload(engine, ready))
		c.extraObjects = append(c.extraObjects, objects...)
	})
}

func TestDeletingAMySQLDatabaseTakesAFinalBackupFirst(t *testing.T) {
	env := dumpDeletingEnv(t, geassv1alpha1.DatabaseEngineMySQL, true, 0)
	// The database was backed up while it ran.
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NoError(t, env.r.reconcileDumpLifecycle(context.Background(), env.db, env.wsNS, plan, &dumpTarget{host: "h", port: "3306", image: testMySQLImage, database: testDBName}))

	requeue, err := env.reconcile()
	require.NoError(t, err)
	require.Positive(t, requeue, "deletion comes back to check on the final backup")
	require.False(t, env.databaseGone(), "the database stays until its last backup is safe")
	final := env.job(testDBName + "-backup-final")
	require.Equal(t, "final", final.Labels[labelBackupRole])
	require.Equal(t, testMySQLImage, final.Spec.Template.Spec.InitContainers[0].Image)
	require.Nil(t, env.archive(dumpArchive).Spec.DeleteAfter, "the archive is not retired until the final backup has finished")
	require.True(t, env.exists(&batchv1.CronJob{}, env.wsNS, testDBName+"-backup"), "nothing is removed while the final backup runs")

	// Still running: no second Job, and no giving up yet.
	requeue, err = env.reconcile()
	require.NoError(t, err)
	require.Positive(t, requeue)

	stopped := utcSeconds(time.Now())
	env.storage.allKeys = []string{snapshotKey("sql.gz", stopped.Add(-24*time.Hour)), snapshotKey("sql.gz", stopped)}
	env.finishJob(testDBName+"-backup-final", batchv1.JobComplete, stopped)

	_, err = env.reconcile()
	require.NoError(t, err)
	require.True(t, env.databaseGone())
	require.False(t, env.exists(&batchv1.CronJob{}, env.wsNS, testDBName+"-backup"), "the schedule goes with the database")
	require.False(t, env.exists(&batchv1.Job{}, env.wsNS, testDBName+"-backup-final"), "so do its Jobs")
	archive := env.archive(dumpArchive)
	require.NotNil(t, archive.Spec.DeleteAfter)
	require.WithinDuration(t, time.Now().Add(platform.DeletedBackupRetention), archive.Spec.DeleteAfter.Time, time.Minute)
	require.Len(t, archive.Status.RestorePoints, 2, "the final backup is a restore point of the deleted database")
	require.True(t, archive.Status.RecoveryWindowEnd.Time.Equal(stopped))
	require.Empty(t, env.storage.purged, "backups outlive the database")
}

func TestDeletingASQLiteDatabaseTakesAFinalBackupFirst(t *testing.T) {
	env := dumpDeletingEnv(t, geassv1alpha1.DatabaseEngineSQLite, true, 0)
	_, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	requeue, err := env.reconcile()
	require.NoError(t, err)
	require.Positive(t, requeue)
	final := env.job(testDBName + "-backup-final")
	require.Equal(t, platform.SQLiteImage, final.Spec.Template.Spec.InitContainers[0].Image)
}

func TestDeletionDoesNotWaitForeverForAFinalSnapshot(t *testing.T) {
	stuck := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: testDBName + "-backup-final", Namespace: testDevTargetNS,
			Labels:            map[string]string{labelBackupFor: testDBName, labelBackupRole: "final"},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-platform.FinalBackupWait - time.Minute)),
		},
	}
	env := dumpDeletingEnv(t, geassv1alpha1.DatabaseEngineMySQL, true, 0, stuck)
	_, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	_, err = env.reconcile()
	require.NoError(t, err)
	require.True(t, env.databaseGone(), "a dump that never finishes must not leave the database stuck deleting")
	require.NotNil(t, env.archive(dumpArchive).Spec.DeleteAfter)
}

func TestDeletionContinuesWhenTheFinalSnapshotFailed(t *testing.T) {
	failed := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName + "-backup-final", Namespace: testDevTargetNS, Labels: map[string]string{labelBackupFor: testDBName, labelBackupRole: "final"}},
		Status:     batchv1.JobStatus{Failed: 2, Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}},
	}
	env := dumpDeletingEnv(t, geassv1alpha1.DatabaseEngineMySQL, true, 0, failed)
	_, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	_, err = env.reconcile()
	require.NoError(t, err)
	require.True(t, env.databaseGone())
	require.NotNil(t, env.archive(dumpArchive).Spec.DeleteAfter)
}

func TestDeletionSkipsTheFinalSnapshotWhenTheDatabaseIsNotRunning(t *testing.T) {
	env := dumpDeletingEnv(t, geassv1alpha1.DatabaseEngineMySQL, false, 0)
	_, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	_, err = env.reconcile()
	require.NoError(t, err)
	require.True(t, env.databaseGone(), "there is nothing to copy and nothing to wait for")
	require.False(t, env.exists(&batchv1.Job{}, env.wsNS, testDBName+"-backup-final"))
	require.NotNil(t, env.archive(dumpArchive).Spec.DeleteAfter)
}

func TestDeletionIsNotBlockedWhenTheFinalSnapshotCannotBeRequested(t *testing.T) {
	env := dumpDeletingEnv(t, geassv1alpha1.DatabaseEngineMySQL, true, 0)
	_, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	env.r.Client = interceptor.NewClient(env.client.(client.WithWatch), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*batchv1.Job); ok {
				return errors.New("namespace is being terminated")
			}
			return c.Create(ctx, obj, opts...)
		},
	})
	_, err = env.reconcile()
	require.NoError(t, err)
	require.True(t, env.databaseGone())
	require.NotNil(t, env.archive(dumpArchive).Spec.DeleteAfter)
}

func TestDeleteTargetResourcesRemovesTheBackupScheduleAndJobs(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	env.reconcileDump()
	other := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "someone-elses", Namespace: env.wsNS, Labels: map[string]string{labelBackupFor: "another-db"}}}
	require.NoError(t, env.client.Create(context.Background(), other))

	env.r.deleteTargetResources(context.Background(), env.db, env.wsNS)

	require.False(t, env.exists(&batchv1.CronJob{}, env.wsNS, testDBName+"-backup"))
	require.False(t, env.exists(&batchv1.Job{}, env.wsNS, dumpArchive+"-initial"))
	require.True(t, env.exists(&batchv1.Job{}, env.wsNS, "someone-elses"), "only this database's Jobs are removed")
	require.NotEmpty(t, env.archives(), "the archive record is never deleted with the database")
}

func TestJobsOfADatabaseWakeItUp(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: env.wsNS, Labels: map[string]string{labelBackupFor: testDBName}}}
	requests := databaseForBackupJob(context.Background(), job)
	require.Len(t, requests, 1)
	require.Equal(t, testDBName, requests[0].Name)
	require.Equal(t, platform.SystemNamespace, requests[0].Namespace, "databases live in the system namespace, their Jobs do not")
	require.Empty(t, databaseForBackupJob(context.Background(), &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: env.wsNS}}))
}

func TestAFailedFirstBackupIsTriedAgainAfterAWhile(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	env.reconcileDump()
	initial := dumpArchive + "-initial"

	// A failure a moment ago is left alone: it is reported, and retrying at once would only repeat it.
	env.finishJob(initial, batchv1.JobFailed, env.r.now().Add(-time.Minute))
	env.reconcileDump()
	complete, failed := jobFinished(env.job(initial))
	require.True(t, failed && !complete, "a fresh failure is not replaced yet")

	// Later the same database, still with no snapshot, gets a new first backup.
	env.finishJob(initial, batchv1.JobFailed, env.r.now().Add(-firstBackupRetryInterval-time.Minute))
	env.reconcileDump()
	replaced := env.job(initial)
	complete, failed = jobFinished(replaced)
	require.False(t, failed || complete, "the failed Job is replaced by a new one that has not run yet")
	require.Equal(t, testDumpPrefix, mustEnv(t, replaced.Spec.Template.Spec.Containers[0], "PREFIX"))
}

func TestAFirstBackupThatIsStillRunningOrDoneIsNotReplaced(t *testing.T) {
	env := dumpEnv(t, geassv1alpha1.DatabaseEngineMySQL)
	env.reconcileDump()
	initial := dumpArchive + "-initial"
	version := env.job(initial).ResourceVersion
	env.reconcileDump()
	require.Equal(t, version, env.job(initial).ResourceVersion, "a Job that is running is left alone")

	env.finishJob(initial, batchv1.JobComplete, env.r.now().Add(-time.Hour))
	version = env.job(initial).ResourceVersion
	env.reconcileDump()
	require.Equal(t, version, env.job(initial).ResourceVersion)
}

func TestAMySQLBackupProblemDoesNotTakeTheDatabaseOutOfService(t *testing.T) {
	env := newBackupEnv(t, func(c *backupEnvConfig) {
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) { db.Spec.Engine = geassv1alpha1.DatabaseEngineMySQL })
	})
	outcome, err := env.r.reconcileDumpBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.True(t, outcome.failed)

	result, err := env.r.setReadyWithBackups(context.Background(), env.db, env.wsNS, "host", "MySQL database is ready", outcome)
	require.NoError(t, err)
	require.Equal(t, backupRetryInterval, result.RequeueAfter, "the database is ready, and backups are looked at again soon")
	require.Equal(t, "True", conditionStatus(env.reload().Status.Conditions, platform.ConditionReady))

	result, err = env.r.setReadyWithBackups(context.Background(), env.db, env.wsNS, "host", "MySQL database is ready", dumpOutcome{backups: true})
	require.NoError(t, err)
	require.Equal(t, platform.BackupStatusRefreshInterval, result.RequeueAfter)
}

func TestARestoreProblemStillKeepsTheDatabaseFromBeingReady(t *testing.T) {
	// Only the backups are optional. A restore that cannot run is the database's own failure.
	env := newBackupEnv(t, func(c *backupEnvConfig) {
		c.backup = nil
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.Engine = geassv1alpha1.DatabaseEngineMySQL
			db.Spec.RestoreFrom = &geassv1alpha1.GeassDatabaseRestoreSpec{Archive: "gone"}
		})
	})
	_, err := env.r.reconcileDumpBackups(context.Background(), env.db, env.wsNS)
	require.ErrorContains(t, err, "no longer available")
}

func TestOnlyBackupJobsWakeTheDatabaseController(t *testing.T) {
	require.True(t, isBackupJob(&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{labelBackupFor: testDBName}}}))
	require.False(t, isBackupJob(&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "someone-elses-build", Labels: map[string]string{"app": "x"}}}))
	require.False(t, isBackupJob(&batchv1.Job{}))
}
