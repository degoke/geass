package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	mariadbv1 "github.com/degoke/geass/pkg/mariadb/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	mariaPhysicalPrefix = "payments/orders-1a2b3c4d/physical/"
	mariaBinlogPrefix   = "payments/orders-1a2b3c4d/binlogs/"
)

// mariaEnv is a MariaDB database that runs with high availability and is backed up, with the operator's database in the
// cluster and ready.
func mariaEnv(t *testing.T, highlyAvailable bool, mutate ...func(*backupEnvConfig)) (*backupEnv, *mariadbv1.MariaDB) {
	t.Helper()
	size := resource.MustParse("20Gi")
	primary := "orders-0"
	mdb := &mariadbv1.MariaDB{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: testDevTargetNS},
		Status: mariadbv1.MariaDBStatus{
			CurrentPrimary: &primary,
			Conditions:     []metav1.Condition{{Type: mariadbv1.ConditionReady, Status: metav1.ConditionTrue}},
		},
	}
	all := make([]func(*backupEnvConfig), 0, 1+len(mutate))
	all = append(all, func(c *backupEnvConfig) {
		c.backup = &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: 14}
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) {
			three := int32(3)
			db.Spec.Engine, db.Spec.Version, db.Spec.StorageSize = geassv1alpha1.DatabaseEngineMariaDB, "", &size
			if highlyAvailable {
				db.Spec.HighAvailability, db.Spec.Instances = true, &three
			}
		})
		c.extraObjects = append(c.extraObjects, mdb)
	})
	return newBackupEnv(t, append(all, mutate...)...), mdb
}

func (e *backupEnv) maintainMaria(mdb *mariadbv1.MariaDB) dumpOutcome {
	e.t.Helper()
	var outcome dumpOutcome
	require.NoError(e.t, e.r.maintainMariaDBBackups(context.Background(), e.db, e.wsNS, "", platform.NewPlacement("eu", nil).WithRequired([]string{"hipaa"}), mdb, &outcome))
	return outcome
}

func (e *backupEnv) physicalBackup(name string) *mariadbv1.PhysicalBackup {
	e.t.Helper()
	var backup mariadbv1.PhysicalBackup
	require.NoError(e.t, e.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: e.wsNS}, &backup))
	return &backup
}

func (e *backupEnv) pitr(name string) *mariadbv1.PointInTimeRecovery {
	e.t.Helper()
	var pitr mariadbv1.PointInTimeRecovery
	require.NoError(e.t, e.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: e.wsNS}, &pitr))
	return &pitr
}

func physicalKey(at time.Time) string {
	return mariaPhysicalPrefix + "physicalbackup-" + at.UTC().Format("20060102150405") + ".xb.gz"
}

func TestAReplicatedMariaDBIsBackedUpToAnyMomentAndOneServerIsCopiedDaily(t *testing.T) {
	ha, _ := mariaEnv(t, true)
	require.Equal(t, platform.BackupKindContinuous, databaseBackupKind(ha.db))
	require.True(t, mariadbContinuousBackups(ha.db))

	one, _ := mariaEnv(t, false)
	require.Equal(t, platform.BackupKindSnapshot, databaseBackupKind(one.db))
	require.False(t, mariadbContinuousBackups(one.db))
}

func TestMariaDBBackupsAreMadeFromTheArchiveAndAskTheOperatorForTheLogs(t *testing.T) {
	env, mdb := mariaEnv(t, true)

	outcome := env.maintainMaria(mdb)

	require.True(t, outcome.backups)
	require.True(t, outcome.pending, "the database is told to archive its logs a reconcile after the object it names exists")
	archive := env.archive(testDBServerName)
	require.True(t, archive.Spec.Source.HighAvailability, "a restore has to be replicated, because the logs are replayed on a replicating database")
	require.EqualValues(t, 3, *archive.Spec.Source.Instances)

	backup := env.physicalBackup(testDBName + "-backup")
	require.Equal(t, testDBName, backup.Spec.MariaDBRef.Name)
	require.Equal(t, platform.DumpSchedule(testDBUID), backup.Spec.Schedule.Cron)
	require.True(t, backup.Spec.Schedule.Suspend, "the first copy is held until the database archives its logs and has settled")
	require.Nil(t, backup.Spec.Schedule.Immediate)
	require.Equal(t, "PreferReplica", backup.Spec.Target)
	require.Equal(t, "gzip", backup.Spec.Compression)
	require.Equal(t, 14*24*time.Hour, backup.Spec.MaxRetention.Duration, "the operator removes copies older than the retention")
	require.True(t, *backup.Spec.PodAffinity)
	require.NotEmpty(t, backup.Spec.Tolerations, "the copy can run on the compliance nodes the project requires")
	s3 := backup.Spec.Storage.S3
	require.Equal(t, testBackupBucket, s3.Bucket)
	require.Equal(t, "s3.eu-west-1.amazonaws.com", s3.Endpoint)
	require.True(t, s3.TLS.Enabled)
	require.Equal(t, mariaPhysicalPrefix, s3.Prefix, "copies go under the archive's physical folder")
	require.Equal(t, testBackupSecretOrd, s3.AccessKeyIDSecretKeyRef.Name)
	require.Equal(t, platform.BackupSecretKeyAccessKeyID, s3.AccessKeyIDSecretKeyRef.Key)
	require.Equal(t, testBackupSecretOrd, s3.SecretAccessKeySecretKeyRef.Name)
	require.Equal(t, "20Gi", backup.Spec.StagingStorage.PersistentVolumeClaim.Resources.Requests.Storage().String(), "a copy is staged on a volume as large as the database")

	pitr := env.pitr(testDBName + "-pitr")
	require.Equal(t, testDBName+"-backup", pitr.Spec.PhysicalBackupRef.Name)
	require.Equal(t, mariaBinlogPrefix, pitr.Spec.Storage.S3.Prefix)
	require.Equal(t, testBackupBucket, pitr.Spec.Storage.S3.Bucket)

	cron := env.job2cron(testDBName + "-binlog-flush")
	require.Equal(t, "*/5 * * * *", cron.Spec.Schedule)
	require.Equal(t, batchv1.ForbidConcurrent, cron.Spec.ConcurrencyPolicy)
	container := cron.Spec.JobTemplate.Spec.Template.Spec.Containers[0]
	require.Equal(t, platform.MariaDBImage, container.Image)
	require.Contains(t, container.Command[2], "FLUSH BINARY LOGS")
	require.Contains(t, container.Command[2], "--user=root")
	require.Equal(t, "orders-primary.payments-dev.svc", mustEnv(t, container, "DB_HOST"), "the logs of the primary are the ones archived")
	var password *corev1.EnvVar
	for i := range container.Env {
		if container.Env[i].Name == "MYSQL_PWD" {
			password = &container.Env[i]
		}
	}
	require.NotNil(t, password)
	require.Equal(t, testDBName+"-bootstrap", password.ValueFrom.SecretKeyRef.Name)
	require.Equal(t, map[string]string{platform.LabelRegion: "eu", platform.TagKey("hipaa"): "true"}, cron.Spec.JobTemplate.Spec.Template.Spec.NodeSelector)

	status := env.reload().Status.Backup
	require.Equal(t, geassv1alpha1.BackupStateStarting, status.State, "nothing has been copied yet")
}

func (e *backupEnv) job2cron(name string) *batchv1.CronJob {
	e.t.Helper()
	var cron batchv1.CronJob
	require.NoError(e.t, e.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: e.wsNS}, &cron))
	return &cron
}

func TestMariaDBArchivesItsLogsOnlyOnceTheObjectItNamesExists(t *testing.T) {
	env, mdb := mariaEnv(t, true)
	ctx := context.Background()
	require.False(t, env.r.mariadbArchivesLogs(ctx, env.db, env.wsNS), "a database never refers to an object that is not there")

	env.maintainMaria(mdb)
	require.True(t, env.r.mariadbArchivesLogs(ctx, env.db, env.wsNS))

	spec := mariadbSpec(env.db, "", platform.Placement{}, 3)
	require.Nil(t, spec.PointInTimeRecoveryRef, "the spec builder does not decide it: ensureMariaDB reads the cluster")

	off := env.db.DeepCopy()
	off.Spec.Backup = &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: false}
	require.False(t, env.r.mariadbArchivesLogs(ctx, off, env.wsNS), "turning backups off stops the archive before its object is removed")
}

func TestAMariaDBTheOperatorRunsIsToldToArchiveAndKeepsWhatItWasStartedFrom(t *testing.T) {
	env, _ := mariaEnv(t, true, func(c *backupEnvConfig) { c.extraObjects = nil })
	ctx := context.Background()
	require.NoError(t, env.client.Create(ctx, &mariadbv1.PointInTimeRecovery{ObjectMeta: metav1.ObjectMeta{Name: testDBName + "-pitr", Namespace: env.wsNS}}))
	bootstrap := &mariadbv1.BootstrapFrom{PointInTimeRecoveryRef: &mariadbv1.LocalRef{Name: testDBName + "-restore-source"}}

	mdb, err := env.r.ensureMariaDB(ctx, env.db, env.wsNS, "", platform.Placement{}, 3, bootstrap)
	require.NoError(t, err)
	require.Equal(t, testDBName+"-pitr", mdb.Spec.PointInTimeRecoveryRef.Name)
	require.Equal(t, testDBName+"-restore-source", mdb.Spec.BootstrapFrom.PointInTimeRecoveryRef.Name)

	// It is fixed with the database: the restore is over and nothing asks for it again.
	mdb, err = env.r.ensureMariaDB(ctx, env.db, env.wsNS, "", platform.Placement{}, 3, nil)
	require.NoError(t, err)
	require.NotNil(t, mdb.Spec.BootstrapFrom, "the operator refuses to change what a database was made from")
	require.Equal(t, testDBName+"-pitr", mdb.Spec.PointInTimeRecoveryRef.Name)

	// Backups turned off: the database stops archiving.
	off := env.db.DeepCopy()
	off.Spec.Backup = &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: false}
	mdb, err = env.r.ensureMariaDB(ctx, off, env.wsNS, "", platform.Placement{}, 3, nil)
	require.NoError(t, err)
	require.Nil(t, mdb.Spec.PointInTimeRecoveryRef)
}

func TestMariaDBBackupStatusComesFromTheCopiesInTheBucketAndTheOperatorsLastRecoverableTime(t *testing.T) {
	env, mdb := mariaEnv(t, true)
	env.maintainMaria(mdb)
	now := utcSeconds(time.Now())
	oldest, newest, recoverable := now.Add(-48*time.Hour), now.Add(-2*time.Hour), now.Add(-20*time.Minute)
	env.storage.allKeys = []string{physicalKey(oldest), physicalKey(newest), mariaPhysicalPrefix + "index.yaml"}
	pitr := env.pitr(testDBName + "-pitr")
	pitr.Status.LastRecoverableTime = timePtr(metav1.NewTime(recoverable))
	require.NoError(t, env.client.Update(context.Background(), pitr))

	outcome := env.maintainMaria(mdb)

	require.True(t, outcome.backups)
	archive := env.archive(testDBServerName)
	require.Len(t, archive.Status.RestorePoints, 2, "only copies are restore points")
	require.True(t, archive.Status.RestorePoints[0].Time.Time.Equal(newest), "newest first")
	require.True(t, archive.Status.RecoveryWindowStart.Time.Equal(oldest), "the window starts at the oldest copy")
	require.True(t, archive.Status.RecoveryWindowEnd.Time.Equal(recoverable), "and ends where the operator says the logs reach")
	require.True(t, archive.Status.LastBackupTime.Time.Equal(newest))
	status := env.reload().Status.Backup
	require.Equal(t, geassv1alpha1.BackupStateProtected, status.State)
}

func TestMariaDBWindowEndsAtTheNewestCopyUntilTheOperatorSaysMore(t *testing.T) {
	env, mdb := mariaEnv(t, true)
	env.maintainMaria(mdb)
	newest := utcSeconds(time.Now().Add(-3 * time.Hour))
	env.storage.allKeys = []string{physicalKey(newest)}

	env.maintainMaria(mdb)

	require.True(t, env.archive(testDBServerName).Status.RecoveryWindowEnd.Time.Equal(newest))
}

func TestMariaDBBinaryLogsOlderThanTheOldestCopyAreRemovedAndTheOthersKept(t *testing.T) {
	env, mdb := mariaEnv(t, true)
	env.maintainMaria(mdb)
	now := utcSeconds(time.Now())
	oldest := now.Add(-10 * 24 * time.Hour)
	oldLog, newLog := mariaBinlogPrefix+"server-10/orders-bin.000001.gz", mariaBinlogPrefix+"server-10/orders-bin.000009.gz"
	inventory := mariaBinlogPrefix + "index.yaml"
	env.storage.allKeys = []string{physicalKey(oldest), physicalKey(now.Add(-time.Hour)), oldLog, newLog, inventory}
	env.storage.modified = map[string]time.Time{oldLog: oldest.Add(-48 * time.Hour), newLog: now.Add(-30 * time.Minute), inventory: oldest.Add(-72 * time.Hour)}

	env.maintainMaria(mdb)

	require.Equal(t, []string{oldLog}, env.storage.deleted, "a log that no copy kept can use is removed, and the inventory never is")
}

func TestMariaDBBinaryLogsAreNotTouchedWhenTheCopiesCannotBeRead(t *testing.T) {
	env, mdb := mariaEnv(t, true)
	env.maintainMaria(mdb)
	env.storage.allKeys = []string{mariaBinlogPrefix + "server-10/orders-bin.000001.gz"}
	env.storage.modified = map[string]time.Time{mariaBinlogPrefix + "server-10/orders-bin.000001.gz": time.Now().Add(-1000 * time.Hour)}
	env.storage.listErr = context.DeadlineExceeded

	env.maintainMaria(mdb)

	require.Empty(t, env.storage.deleted)
}

// settle is a database that has been told to archive its logs, and has been ready for as long as the first copy waits.
func (e *backupEnv) settle(mdb *mariadbv1.MariaDB) {
	e.t.Helper()
	mdb.Spec.PointInTimeRecoveryRef = &mariadbv1.LocalRef{Name: testDBName + "-pitr"}
	e.maintainMaria(mdb) // the database is seen to archive: the wait begins
	backup := e.physicalBackup(testDBName + "-backup")
	require.NotEmpty(e.t, backup.Annotations[annotationBackupArmed])
	require.True(e.t, backup.Spec.Schedule.Suspend, "it has only just begun to archive")

	later := e.r.now().Add(mariadbArmDelay + time.Second)
	e.r.clock = func() time.Time { return later }
}

func TestTheFirstMariaDBCopyIsTakenOnceTheDatabaseArchivesItsLogsAndHasSettled(t *testing.T) {
	env, mdb := mariaEnv(t, true)

	outcome := env.maintainMaria(mdb)
	backup := env.physicalBackup(testDBName + "-backup")
	require.True(t, backup.Spec.Schedule.Suspend, "a copy taken before the database archives its logs cannot be replayed to")
	require.Empty(t, backup.Annotations[annotationBackupArmed], "the database has not been told yet")
	require.True(t, outcome.pending)

	env.settle(mdb)
	notReady := mdb.DeepCopy()
	notReady.Status.Conditions = nil
	env.maintainMaria(notReady)
	require.True(t, env.physicalBackup(testDBName+"-backup").Spec.Schedule.Suspend, "it rolls the servers after it is told, and is not settled until it is ready again")

	outcome = env.maintainMaria(mdb)

	backup = env.physicalBackup(testDBName + "-backup")
	require.False(t, backup.Spec.Schedule.Suspend)
	require.Contains(t, backup.Spec.Schedule.OnDemand, "first-", "the first copy is asked for now")
	require.Equal(t, "orders-0", backup.Annotations[annotationBackupPrimary])
	require.False(t, outcome.pending, "nothing is waited for any more")

	env.maintainMaria(mdb)
	require.Equal(t, backup.Spec.Schedule.OnDemand, env.physicalBackup(testDBName+"-backup").Spec.Schedule.OnDemand, "once")
}

func TestAMariaDBBackupAsksForANewBaselineWhenThePrimaryChanges(t *testing.T) {
	env, mdb := mariaEnv(t, true)
	env.settle(mdb)
	env.maintainMaria(mdb)
	backup := env.physicalBackup(testDBName + "-backup")
	first := backup.Spec.Schedule.OnDemand
	require.Contains(t, first, "first-")

	env.maintainMaria(mdb)
	require.Equal(t, first, env.physicalBackup(testDBName+"-backup").Spec.Schedule.OnDemand, "nothing changed")

	// A failover: the logs of the new primary do not continue from a copy of the old one's.
	failover := "orders-1"
	mdb.Status.CurrentPrimary = &failover
	env.maintainMaria(mdb)
	backup = env.physicalBackup(testDBName + "-backup")
	require.Contains(t, backup.Spec.Schedule.OnDemand, "orders-1-", "another copy is asked for, from the new primary")
	require.Equal(t, "orders-1", backup.Annotations[annotationBackupPrimary])
	asked := backup.Spec.Schedule.OnDemand

	env.maintainMaria(mdb)
	require.Equal(t, asked, env.physicalBackup(testDBName+"-backup").Spec.Schedule.OnDemand, "and not again for the same primary")
	require.False(t, env.physicalBackup(testDBName+"-backup").Spec.Schedule.Suspend)

	// The schedule stays on through a spell when the database is not ready.
	notReady := mdb.DeepCopy()
	notReady.Status.Conditions = nil
	env.maintainMaria(notReady)
	require.False(t, env.physicalBackup(testDBName+"-backup").Spec.Schedule.Suspend, "a nightly copy is not lost to a failover")
}

func TestAMariaDBBackupJobThatFailedIsReportedUntilALaterOneWorks(t *testing.T) {
	env, mdb := mariaEnv(t, true)
	env.maintainMaria(mdb)
	now := utcSeconds(time.Now())
	owner := metav1.OwnerReference{APIVersion: "k8s.mariadb.com/v1alpha1", Kind: "PhysicalBackup", Name: testDBName + "-backup", UID: "u"}
	failed := func(name string, at time.Time) *batchv1.Job {
		when := metav1.NewTime(at)
		return &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: env.wsNS, OwnerReferences: []metav1.OwnerReference{owner}},
			Status:     batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, LastTransitionTime: when}}},
		}
	}
	require.NoError(t, env.client.Create(context.Background(), failed("orders-backup-1", now.Add(-time.Hour))))
	env.storage.allKeys = []string{physicalKey(now.Add(-26 * time.Hour))}

	env.maintainMaria(mdb)
	require.Equal(t, geassv1alpha1.BackupStateAtRisk, env.reload().Status.Backup.State, "the last copy failed")

	when := metav1.NewTime(now.Add(-10 * time.Minute))
	good := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-backup-2", Namespace: env.wsNS, OwnerReferences: []metav1.OwnerReference{owner}},
		Status:     batchv1.JobStatus{Succeeded: 1, Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: when}}},
	}
	require.NoError(t, env.client.Create(context.Background(), good))
	env.storage.allKeys = append(env.storage.allKeys, physicalKey(now.Add(-10*time.Minute)))

	env.maintainMaria(mdb)
	require.Equal(t, geassv1alpha1.BackupStateProtected, env.reload().Status.Backup.State, "a copy that worked afterwards clears it")

	// A job that is not the backup's, such as the copy used to build a replica, is not a backup failure.
	other := failed("orders-recovery-1", now)
	other.OwnerReferences = []metav1.OwnerReference{{APIVersion: "k8s.mariadb.com/v1alpha1", Kind: "PhysicalBackup", Name: testDBName + "-recovery", UID: "v"}}
	require.NoError(t, env.client.Create(context.Background(), other))
	env.maintainMaria(mdb)
	require.Equal(t, geassv1alpha1.BackupStateProtected, env.reload().Status.Backup.State)
}

func TestAnUnreadableBucketLeavesTheLastKnownMariaDBBackupStatusAlone(t *testing.T) {
	env, mdb := mariaEnv(t, true)
	env.storage.allKeys = []string{physicalKey(utcSeconds(time.Now().Add(-time.Hour)))}
	env.maintainMaria(mdb)
	before := env.reload().Status.Backup
	require.Equal(t, geassv1alpha1.BackupStateProtected, before.State)

	env.storage.listErr = context.DeadlineExceeded
	env.db = env.reload() // a reconcile starts from what the cluster holds
	env.maintainMaria(mdb)

	require.Equal(t, before, env.reload().Status.Backup)
}

func TestMariaDBWithoutBackupStorageSaysItNeedsSome(t *testing.T) {
	env, mdb := mariaEnv(t, true, func(c *backupEnvConfig) { c.storage = nil })

	outcome := env.maintainMaria(mdb)

	require.True(t, outcome.backups)
	require.Equal(t, geassv1alpha1.BackupStateNeedsStorage, env.reload().Status.Backup.State)
	require.False(t, env.exists(&mariadbv1.PhysicalBackup{}, env.wsNS, testDBName+"-backup"), "nothing is set up without somewhere to put it")
}

func TestTurningMariaDBBackupsOffRemovesTheObjectsAndRetiresTheArchive(t *testing.T) {
	env, mdb := mariaEnv(t, true)
	env.maintainMaria(mdb)
	require.True(t, env.exists(&mariadbv1.PointInTimeRecovery{}, env.wsNS, testDBName+"-pitr"))
	env.db.Spec.Backup = &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: false}

	env.maintainMaria(mdb)

	for name, object := range map[string]client.Object{
		testDBName + "-backup": &mariadbv1.PhysicalBackup{}, testDBName + "-pitr": &mariadbv1.PointInTimeRecovery{}, testDBName + "-binlog-flush": &batchv1.CronJob{},
	} {
		require.False(t, env.exists(object, env.wsNS, name), name)
	}
	require.NotNil(t, env.archive(testDBServerName).Spec.DeleteAfter, "earlier restore points stay usable for a while")
	require.Nil(t, env.reload().Status.Backup)
}

func TestMariaDBBackupsDispatchByTopology(t *testing.T) {
	ctx := context.Background()
	ha, mdb := mariaEnv(t, true)
	outcome, err := ha.r.reconcileMariaDBBackups(ctx, ha.db, ha.wsNS, "", platform.Placement{}, mdb)
	require.NoError(t, err)
	require.True(t, outcome.backups)
	require.True(t, ha.exists(&mariadbv1.PhysicalBackup{}, ha.wsNS, testDBName+"-backup"), "a replicated database is copied physically")
	require.False(t, ha.exists(&batchv1.CronJob{}, ha.wsNS, testDBName+"-backup"))

	one, mdb := mariaEnv(t, false)
	_, err = one.r.reconcileMariaDBBackups(ctx, one.db, one.wsNS, "", platform.Placement{}, mdb)
	require.NoError(t, err)
	require.True(t, one.exists(&batchv1.CronJob{}, one.wsNS, testDBName+"-backup"), "one server has the daily dump")
	require.False(t, one.exists(&mariadbv1.PhysicalBackup{}, one.wsNS, testDBName+"-backup"))
}

func TestAMariaDBBackupProblemDoesNotTakeTheDatabaseOutOfService(t *testing.T) {
	env, mdb := mariaEnv(t, true)
	// Making the copy fails: the API server refuses the object.
	env.r.Client = failingCreate(env.client, &mariadbv1.PhysicalBackup{})

	outcome, err := env.r.reconcileMariaDBBackups(context.Background(), env.db, env.wsNS, "", platform.Placement{}, mdb)

	require.NoError(t, err, "the database works without backups")
	require.True(t, outcome.failed, "and is looked at again soon")
	require.Empty(t, outcome.waiting)
}

// --- deleting a replicated MariaDB database -------------------------------------------------

func mariaDeletingEnv(t *testing.T, ready bool, deletedAgo time.Duration) (*backupEnv, *mariadbv1.MariaDB) {
	t.Helper()
	deletedAt := metav1.NewTime(time.Now().Add(-deletedAgo))
	env, mdb := mariaEnv(t, true, func(c *backupEnvConfig) {
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) {
			db.Finalizers, db.DeletionTimestamp = []string{platform.FinalizerDatabase}, &deletedAt
		})
	})
	if !ready {
		mdb.Status.Conditions = nil
		require.NoError(t, env.client.Update(context.Background(), mdb))
	}
	return env, mdb
}

func TestDeletingAReplicatedMariaDBTakesAFinalPhysicalCopyFirst(t *testing.T) {
	env, mdb := mariaDeletingEnv(t, true, 0)
	// It was backed up while it ran.
	env.maintainMaria(mdb)

	requeue, err := env.reconcile()
	require.NoError(t, err)
	require.Positive(t, requeue, "deletion comes back to check on the final copy")
	require.False(t, env.databaseGone(), "the database stays until its last copy is safe")
	final := env.physicalBackup(testDBName + "-final")
	require.Nil(t, final.Spec.Schedule, "the final copy is taken once, and has no schedule")
	require.Nil(t, final.Spec.MaxRetention)
	require.Equal(t, testBackupBucket, final.Spec.Storage.S3.Bucket)
	require.Equal(t, mariaPhysicalPrefix, final.Spec.Storage.S3.Prefix)
	require.Nil(t, env.archive(testDBServerName).Spec.DeleteAfter, "the archive is not retired until the final copy has finished")
	require.True(t, env.exists(&mariadbv1.MariaDB{}, env.wsNS, testDBName), "nothing is removed while it runs")

	// Still running: no giving up yet.
	requeue, err = env.reconcile()
	require.NoError(t, err)
	require.Positive(t, requeue)
	require.False(t, env.databaseGone())

	stopped := utcSeconds(time.Now())
	env.storage.allKeys = []string{physicalKey(stopped.Add(-24 * time.Hour)), physicalKey(stopped)}
	final = env.physicalBackup(testDBName + "-final")
	final.Status.Conditions = []metav1.Condition{{Type: mariadbv1.ConditionComplete, Status: metav1.ConditionTrue, Reason: "JobComplete"}}
	require.NoError(t, env.client.Update(context.Background(), final))

	_, err = env.reconcile()
	require.NoError(t, err)
	require.True(t, env.databaseGone())
	require.False(t, env.exists(&mariadbv1.MariaDB{}, env.wsNS, testDBName), "the database goes with it")
	for _, name := range []string{"-backup", "-final", "-pitr", "-binlog-flush"} {
		require.False(t, env.exists(&mariadbv1.PhysicalBackup{}, env.wsNS, testDBName+name) && name != "-pitr" && name != "-binlog-flush")
	}
	require.False(t, env.exists(&mariadbv1.PointInTimeRecovery{}, env.wsNS, testDBName+"-pitr"))
	archive := env.archive(testDBServerName)
	require.NotNil(t, archive.Spec.DeleteAfter)
	require.WithinDuration(t, time.Now().Add(platform.DeletedBackupRetention), archive.Spec.DeleteAfter.Time, time.Minute)
	require.Len(t, archive.Status.RestorePoints, 2, "the final copy is a restore point of the deleted database")
	require.True(t, archive.Status.RecoveryWindowEnd.Time.Equal(stopped), "the window ends at the newest copy when the operator never said more")
	require.Empty(t, env.storage.purged, "backups outlive the database")
}

func TestDeletionDoesNotWaitForeverForAFinalPhysicalCopy(t *testing.T) {
	env, mdb := mariaDeletingEnv(t, true, 0)
	env.maintainMaria(mdb)
	_, err := env.reconcile()
	require.NoError(t, err)
	final := env.physicalBackup(testDBName + "-final")
	final.CreationTimestamp = metav1.NewTime(time.Now().Add(-platform.FinalBackupWait - time.Minute))
	require.NoError(t, env.client.Update(context.Background(), final))

	_, err = env.reconcile()

	require.NoError(t, err)
	require.True(t, env.databaseGone(), "a copy that never finishes cannot leave a database stuck deleting")
	require.NotNil(t, env.archive(testDBServerName).Spec.DeleteAfter)
}

func TestDeletionSkipsTheFinalPhysicalCopyWhenTheDatabaseIsNotRunning(t *testing.T) {
	env, mdb := mariaDeletingEnv(t, false, 0)
	env.maintainMaria(mdb)

	_, err := env.reconcile()

	require.NoError(t, err)
	require.True(t, env.databaseGone())
	require.NotNil(t, env.archive(testDBServerName).Spec.DeleteAfter)
	require.False(t, env.exists(&mariadbv1.PhysicalBackup{}, env.wsNS, testDBName+"-final"))
}

func TestDeletingAMariaDBRemovesItsStagingVolumes(t *testing.T) {
	env, mdb := mariaDeletingEnv(t, false, 0)
	env.maintainMaria(mdb)
	for _, name := range []string{testDBName + "-backup-staging", testDBName + "-final-staging"} {
		require.NoError(t, env.client.Create(context.Background(), &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: env.wsNS}}))
	}

	_, err := env.reconcile()

	require.NoError(t, err)
	require.False(t, env.exists(&corev1.PersistentVolumeClaim{}, env.wsNS, testDBName+"-backup-staging"))
	require.False(t, env.exists(&corev1.PersistentVolumeClaim{}, env.wsNS, testDBName+"-final-staging"))
}

// --- restoring --------------------------------------------------------------------------------

func replicatedSource(points ...string) *geassv1alpha1.GeassBackupArchive {
	source := snapshotSource(geassv1alpha1.DatabaseEngineMariaDB, points...)
	three := int32(3)
	source.Spec.Source.DatabaseName = testDBName
	source.Spec.Source.HighAvailability, source.Spec.Source.Instances = true, &three
	end := metav1.NewTime(time.Now())
	start := metav1.NewTime(time.Now().Add(-72 * time.Hour))
	source.Status.RecoveryWindowStart, source.Status.RecoveryWindowEnd = &start, &end
	return source
}

func mariaRestoreEnv(t *testing.T, source *geassv1alpha1.GeassBackupArchive, restore geassv1alpha1.GeassDatabaseRestoreSpec, highAvailable bool) *backupEnv {
	t.Helper()
	restore.Archive = source.Name
	env, _ := mariaEnv(t, highAvailable, func(c *backupEnvConfig) {
		c.backup = nil
		c.extraObjects = append(c.extraObjects, source)
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) { db.Spec.RestoreFrom = &restore })
	})
	return env
}

func TestRestoringAReplicatedMariaDBToAMomentReadsTheSourcesCopiesAndLogs(t *testing.T) {
	source := replicatedSource("20261011T020000Z", "20261010T020000Z")
	at := metav1.NewTime(time.Now().Add(-5 * time.Hour).Truncate(time.Second))
	env := mariaRestoreEnv(t, source, geassv1alpha1.GeassDatabaseRestoreSpec{Time: &at}, true)

	bootstrap, err := env.r.mariadbRestoreBootstrap(context.Background(), env.db, env.wsNS, "")

	require.NoError(t, err)
	require.NotNil(t, bootstrap)
	require.Equal(t, testDBName+"-restore-source", bootstrap.PointInTimeRecoveryRef.Name)
	require.True(t, bootstrap.TargetRecoveryTime.Time.Equal(at.Time), "the operator restores the nearest copy and replays the logs to this moment")
	require.Equal(t, "20Gi", bootstrap.StagingStorage.PersistentVolumeClaim.Resources.Requests.Storage().String())

	// What it reads is the source's folders, in the database's own namespace, with credentials made for the restore.
	copies := env.physicalBackup(testDBName + "-restore-source")
	require.True(t, copies.Spec.Schedule.Suspend, "it only says where the copies are, and is never run")
	require.Equal(t, "payments/legacy-9f8e7d6c/physical/", copies.Spec.Storage.S3.Prefix)
	require.Equal(t, testDBName+"-restore-credentials", copies.Spec.Storage.S3.AccessKeyIDSecretKeyRef.Name)
	logs := env.pitr(testDBName + "-restore-source")
	require.Equal(t, testDBName+"-restore-source", logs.Spec.PhysicalBackupRef.Name)
	require.Equal(t, "payments/legacy-9f8e7d6c/binlogs/", logs.Spec.Storage.S3.Prefix)
	require.NotNil(t, env.secret(env.wsNS, testDBName+"-restore-credentials"))
}

func TestRestoringAReplicatedMariaDBToARestorePointTargetsTheMomentOfTheCopy(t *testing.T) {
	source := replicatedSource("20261011T020000Z", "20261010T020000Z")
	env := mariaRestoreEnv(t, source, geassv1alpha1.GeassDatabaseRestoreSpec{RestorePoint: "20261010T020000Z"}, true)

	bootstrap, err := env.r.mariadbRestoreBootstrap(context.Background(), env.db, env.wsNS, "")

	require.NoError(t, err)
	require.True(t, bootstrap.TargetRecoveryTime.Time.Equal(time.Date(2026, 10, 10, 2, 0, 0, 0, time.UTC)))
}

func TestRestoringAReplicatedMariaDBWithoutAChoiceRestoresEverythingArchived(t *testing.T) {
	source := replicatedSource("20261011T020000Z")
	env := mariaRestoreEnv(t, source, geassv1alpha1.GeassDatabaseRestoreSpec{}, true)

	bootstrap, err := env.r.mariadbRestoreBootstrap(context.Background(), env.db, env.wsNS, "")

	require.NoError(t, err)
	require.Nil(t, bootstrap.TargetRecoveryTime, "no target means the last moment the logs reach")
}

func TestARestoredReplicatedMariaDBHasToBeReplicated(t *testing.T) {
	source := replicatedSource("20261011T020000Z")
	env := mariaRestoreEnv(t, source, geassv1alpha1.GeassDatabaseRestoreSpec{}, false)

	_, err := env.r.mariadbRestoreBootstrap(context.Background(), env.db, env.wsNS, "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "has to have high availability too")
}

func TestARestoreIsRefusedForAMomentOutsideTheWindow(t *testing.T) {
	source := replicatedSource("20261011T020000Z")
	late := metav1.NewTime(time.Now().Add(time.Hour))
	env := mariaRestoreEnv(t, source, geassv1alpha1.GeassDatabaseRestoreSpec{Time: &late}, true)

	_, err := env.r.mariadbRestoreBootstrap(context.Background(), env.db, env.wsNS, "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "later than the newest recoverable moment")
}

func TestARestoreOfABackupThatIsGoneSaysSo(t *testing.T) {
	source := replicatedSource("20261011T020000Z")
	env := mariaRestoreEnv(t, source, geassv1alpha1.GeassDatabaseRestoreSpec{}, true)
	require.NoError(t, env.client.Delete(context.Background(), source))

	_, err := env.r.mariadbRestoreBootstrap(context.Background(), env.db, env.wsNS, "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "no longer available")
}

func TestAMariaDBThatIsNotARestoreHasNoBootstrap(t *testing.T) {
	env, _ := mariaEnv(t, true)
	bootstrap, err := env.r.mariadbRestoreBootstrap(context.Background(), env.db, env.wsNS, "")
	require.NoError(t, err)
	require.Nil(t, bootstrap)

	// Nor does one that was restored already: the operator would be asked for the same restore twice.
	source := replicatedSource("20261011T020000Z")
	done := mariaRestoreEnv(t, source, geassv1alpha1.GeassDatabaseRestoreSpec{}, true)
	now := metav1.NewTime(time.Now())
	done.db.Status.RestoredAt = &now
	bootstrap, err = done.r.mariadbRestoreBootstrap(context.Background(), done.db, done.wsNS, "")
	require.NoError(t, err)
	require.Nil(t, bootstrap)
}

func TestARestoredReplicatedMariaDBIsMarkedRestoredOnceItServes(t *testing.T) {
	source := replicatedSource("20261011T020000Z")
	env := mariaRestoreEnv(t, source, geassv1alpha1.GeassDatabaseRestoreSpec{}, true)

	mdb := &mariadbv1.MariaDB{Spec: mariadbv1.MariaDBSpec{BootstrapFrom: &mariadbv1.BootstrapFrom{}}}

	waiting, err := env.r.reconcileMariaDBRestore(context.Background(), env.db, env.wsNS, mdb)

	require.NoError(t, err)
	require.Empty(t, waiting)
	require.NotNil(t, env.reload().Status.RestoredAt, "a restore loaded a second time would overwrite what was written since")

	// A database the backups were not restored into holds nothing of them, and is never called restored.
	empty := mariaRestoreEnv(t, source, geassv1alpha1.GeassDatabaseRestoreSpec{}, true)
	_, err = empty.r.reconcileMariaDBRestore(context.Background(), empty.db, empty.wsNS, &mariadbv1.MariaDB{})
	require.Error(t, err)
	require.Nil(t, empty.reload().Status.RestoredAt)
}

func TestRestoringBackupsOfOneMariaDBServerIsLoadedByAJob(t *testing.T) {
	source := snapshotSource(geassv1alpha1.DatabaseEngineMariaDB, "20261011T020000Z")
	source.Spec.Source.DatabaseName = testDBName
	env := mariaRestoreEnv(t, source, geassv1alpha1.GeassDatabaseRestoreSpec{}, true)

	waiting, err := env.r.reconcileMariaDBRestore(context.Background(), env.db, env.wsNS, &mariadbv1.MariaDB{})

	require.NoError(t, err)
	require.Equal(t, messageRestoringDatabase, waiting, "the daily copies of one server are SQL text, loaded into the new database")
	job := env.job(testDBName + "-restore")
	require.Contains(t, job.Spec.Template.Spec.Containers[0].Command[2], "| mariadb --host")
	require.Nil(t, env.reload().Status.RestoredAt)
}

// failingCreate is a client that refuses to create objects of the kind of the one given, as an API server that does not
// serve the kind, or an admission webhook that is down, would.
func failingCreate(c client.Client, kind client.Object) client.Client {
	watching, ok := c.(client.WithWatch)
	if !ok {
		panic("a fake client watches")
	}
	return interceptor.NewClient(watching, interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if reflect.TypeOf(obj) == reflect.TypeOf(kind) {
				return errors.New("the API server is not available")
			}
			return c.Create(ctx, obj, opts...)
		},
	})
}

// A physical copy holds the logins of the server it was taken from, and the operator reaches a restored server as root
// with the password of the new database's Secret. A restored database with a password of its own sat at "Initializing"
// with "Access denied for user 'root'" in its log. Found on K3s.
func TestARestoredMariaDBGetsThePasswordTheCopyCarries(t *testing.T) {
	source := replicatedSource("20261011T020000Z")
	source.Spec.Database = "books"
	env := mariaRestoreEnv(t, source, geassv1alpha1.GeassDatabaseRestoreSpec{}, true)
	ctx := context.Background()

	require.NoError(t, env.r.inheritMariaDBCredentials(ctx, env.db))
	require.NoError(t, env.r.reconcileBootstrapSecret(ctx, env.db, env.wsNS))

	require.Equal(t, "books", env.db.Annotations[annotationCredentialsOrigin], "the original's name, when the archive says nothing else")
	require.Equal(t, "books-password", string(env.secret(env.wsNS, testDBName+"-bootstrap").Data["password"]), "not orders-password, which the copy does not hold")
	require.Equal(t, "books", env.reload().Annotations[annotationCredentialsOrigin], "kept on the database, for the rest of its life")

	// What the archive says wins, so a restore of a restore is given what the copy it came from carries.
	chained := replicatedSource("20261011T020000Z")
	chained.Spec.Source.CredentialsOrigin = "books"
	chained.Spec.Database = "small-haven"
	again := mariaRestoreEnv(t, chained, geassv1alpha1.GeassDatabaseRestoreSpec{}, true)
	require.NoError(t, again.r.inheritMariaDBCredentials(ctx, again.db))
	require.Equal(t, "books", credentialsOrigin(again.db))

	// A restored database records it in its own backups, for the restore of its restore.
	restored := env.reload()
	require.Equal(t, "books", backupSourceOf(restored).CredentialsOrigin)
	plain, _ := mariaEnv(t, true)
	require.Equal(t, testDBName, backupSourceOf(plain.db).CredentialsOrigin, "a database that was not restored carries its own")
}

func TestOnlyARestoreFromAPhysicalCopyInheritsAPassword(t *testing.T) {
	ctx := context.Background()
	plain, _ := mariaEnv(t, true)
	require.NoError(t, plain.r.inheritMariaDBCredentials(ctx, plain.db))
	require.Empty(t, plain.db.Annotations[annotationCredentialsOrigin])
	require.Equal(t, testDBName, credentialsOrigin(plain.db))

	// Daily SQL copies are loaded into logins of the new database's own.
	dumps := snapshotSource(geassv1alpha1.DatabaseEngineMariaDB, "20261011T020000Z")
	snapshotRestore := mariaRestoreEnv(t, dumps, geassv1alpha1.GeassDatabaseRestoreSpec{}, true)
	require.NoError(t, snapshotRestore.r.inheritMariaDBCredentials(ctx, snapshotRestore.db))
	require.Empty(t, snapshotRestore.db.Annotations[annotationCredentialsOrigin])

	// A backup that is gone is said so when the restore begins, not here.
	gone := replicatedSource("20261011T020000Z")
	missing := mariaRestoreEnv(t, gone, geassv1alpha1.GeassDatabaseRestoreSpec{}, true)
	require.NoError(t, missing.client.Delete(ctx, gone))
	require.NoError(t, missing.r.inheritMariaDBCredentials(ctx, missing.db))
}
