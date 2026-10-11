package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/platform"
)

func utcSeconds(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

func healthyCluster(namespace string, mutate ...func(*cnpgv1.Cluster)) *cnpgv1.Cluster {
	now := utcSeconds(time.Now())
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: namespace},
		Spec:       cnpgv1.ClusterSpec{Instances: 1},
		Status: cnpgv1.ClusterStatus{
			Phase: "Cluster in healthy state", ReadyInstances: 1, CurrentPrimary: testDBName + "-1",
			FirstRecoverabilityPoint: now.Add(-72 * time.Hour).Format(time.RFC3339),
			LastSuccessfulBackup:     now.Add(-time.Hour).Format(time.RFC3339),
			Conditions:               []metav1.Condition{{Type: cnpgv1.ConditionContinuousArchiving, Status: metav1.ConditionTrue}},
		},
	}
	for _, m := range mutate {
		m(cluster)
	}
	return cluster
}

func backupObject(namespace, name, cluster, phase, id string, stopped time.Time) *cnpgv1.Backup {
	started, stop := metav1.NewTime(stopped.Add(-time.Minute)), metav1.NewTime(stopped)
	return &cnpgv1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       cnpgv1.BackupSpec{Cluster: corev1.LocalObjectReference{Name: cluster}},
		Status:     cnpgv1.BackupStatus{Phase: phase, BackupID: id, StartedAt: &started, StoppedAt: &stop},
	}
}

func lifecycleEnv(t *testing.T, cluster *cnpgv1.Cluster, extra ...client.Object) (*backupEnv, *backupPlan) {
	t.Helper()
	env := newBackupEnv(t, func(c *backupEnvConfig) {
		c.extraObjects = append(c.extraObjects, cluster)
		c.extraObjects = append(c.extraObjects, extra...)
	})
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NotNil(t, plan.archive)
	return env, plan
}

func TestReconcileBackupLifecycleSchedulesBackupsAndMirrorsStatus(t *testing.T) {
	now := utcSeconds(time.Now())
	cluster := healthyCluster(testDevTargetNS)
	env, plan := lifecycleEnv(t, cluster,
		backupObject(testDevTargetNS, "orders-newest", testDBName, cnpgv1.BackupPhaseCompleted, "20260310T020000", now.Add(-time.Hour)),
		backupObject(testDevTargetNS, "orders-older", testDBName, cnpgv1.BackupPhaseCompleted, "20260309T020000", now.Add(-25*time.Hour)),
		backupObject(testDevTargetNS, "orders-expired", testDBName, cnpgv1.BackupPhaseCompleted, "20260301T020000", now.Add(-100*time.Hour)),
		backupObject(testDevTargetNS, "orders-failed", testDBName, cnpgv1.BackupPhaseFailed, "20260308T020000", now.Add(-30*time.Hour)),
		backupObject(testDevTargetNS, "someone-elses", "inventory", cnpgv1.BackupPhaseCompleted, "20260310T030000", now.Add(-time.Hour)),
	)

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	var scheduled cnpgv1.ScheduledBackup
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testDBName, Namespace: env.wsNS}, &scheduled))
	require.Equal(t, platform.BackupSchedule(testDBUID), scheduled.Spec.Schedule)
	require.Equal(t, testDBName, scheduled.Spec.Cluster.Name)
	require.NotNil(t, scheduled.Spec.Immediate)
	require.True(t, *scheduled.Spec.Immediate, "a backup must exist as soon as backups are on, not a day later")
	require.Equal(t, cnpgv1.BackupOwnerReferenceSelf, scheduled.Spec.BackupOwnerReference)
	require.Equal(t, cnpgv1.BackupMethodBarmanObjectStore, scheduled.Spec.Method)

	archive := env.archive(plan.archive.Name)
	require.Equal(t, []string{"20260310T020000", "20260309T020000"}, restorePointIDs(archive),
		"only completed backups of this database inside the recovery window, newest first")
	require.True(t, archive.Status.RecoveryWindowStart.Time.Equal(now.Add(-72*time.Hour)))
	require.True(t, archive.Status.LastBackupTime.Time.Equal(now.Add(-time.Hour)))
	require.NotNil(t, archive.Status.RecoveryWindowEnd)
	require.True(t, archive.Status.RecoveryWindowEnd.After(now.Add(-time.Hour)), "the window reaches past the last backup while changes are being archived")
	require.True(t, archive.Status.RecoveryWindowEnd.Time.Before(time.Now()), "but never to a moment that may not be archived yet")

	status := env.reload().Status.Backup
	require.NotNil(t, status)
	require.Equal(t, geassv1alpha1.BackupStateProtected, status.State)
	require.Equal(t, "Backups are running", status.Message)
	require.Equal(t, archive.Name, status.Archive)
	require.True(t, status.LastBackupTime.Time.Equal(now.Add(-time.Hour)))
	require.True(t, status.RecoveryWindowStart.Time.Equal(now.Add(-72*time.Hour)))
}

func TestReconcileBackupLifecycleIsIdempotent(t *testing.T) {
	cluster := healthyCluster(testDevTargetNS)
	env, plan := lifecycleEnv(t, cluster)
	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	var before cnpgv1.ScheduledBackup
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testDBName, Namespace: env.wsNS}, &before))
	archiveVersion := env.archive(plan.archive.Name).ResourceVersion
	dbVersion := env.reload().ResourceVersion

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	var after cnpgv1.ScheduledBackup
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testDBName, Namespace: env.wsNS}, &after))
	require.Equal(t, before.ResourceVersion, after.ResourceVersion, "an unchanged schedule must not be rewritten")
	require.Equal(t, archiveVersion, env.archive(plan.archive.Name).ResourceVersion, "unchanged status must not be rewritten or the controller would loop")
	require.Equal(t, dbVersion, env.reload().ResourceVersion)
}

func TestReconcileBackupLifecycleReportsStates(t *testing.T) {
	now := utcSeconds(time.Now())
	tests := map[string]struct {
		cluster *cnpgv1.Cluster
		want    geassv1alpha1.GeassDatabaseBackupState
	}{
		"first backup not finished": {
			cluster: healthyCluster(testDevTargetNS, func(c *cnpgv1.Cluster) { c.Status.LastSuccessfulBackup = ""; c.Status.FirstRecoverabilityPoint = "" }),
			want:    geassv1alpha1.BackupStateStarting,
		},
		"changes are not being archived": {
			cluster: healthyCluster(testDevTargetNS, func(c *cnpgv1.Cluster) {
				c.Status.Conditions = []metav1.Condition{{Type: cnpgv1.ConditionContinuousArchiving, Status: metav1.ConditionFalse}}
			}),
			want: geassv1alpha1.BackupStateAtRisk,
		},
		"latest backup failed": {
			cluster: healthyCluster(testDevTargetNS, func(c *cnpgv1.Cluster) { c.Status.LastFailedBackup = now.Format(time.RFC3339) }),
			want:    geassv1alpha1.BackupStateAtRisk,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			env, plan := lifecycleEnv(t, tt.cluster)
			require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, tt.cluster, plan))
			require.Equal(t, tt.want, env.reload().Status.Backup.State)
		})
	}
}

func TestReconcileBackupLifecycleWithoutStorageReportsNeedsStorageAndSchedulesNothing(t *testing.T) {
	cluster := healthyCluster(testDevTargetNS)
	env := newBackupEnv(t, func(c *backupEnvConfig) { c.storage = nil; c.extraObjects = append(c.extraObjects, cluster) })
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	status := env.reload().Status.Backup
	require.NotNil(t, status)
	require.Equal(t, geassv1alpha1.BackupStateNeedsStorage, status.State)
	require.Empty(t, status.Archive)
	require.False(t, env.exists(&cnpgv1.ScheduledBackup{}, env.wsNS, testDBName))
}

func TestReconcileBackupLifecycleWhenTurnedOffRetiresTheArchiveAndStopsTheSchedule(t *testing.T) {
	cluster := healthyCluster(testDevTargetNS)
	env, plan := lifecycleEnv(t, cluster)
	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))
	require.True(t, env.exists(&cnpgv1.ScheduledBackup{}, env.wsNS, testDBName))

	env.db.Spec.Backup.Enabled = false
	off, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.Nil(t, off)
	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, off))

	require.False(t, env.exists(&cnpgv1.ScheduledBackup{}, env.wsNS, testDBName))
	archive := env.archive(plan.archive.Name)
	require.NotNil(t, archive.Spec.DeleteAfter, "turning backups off must not instantly destroy restore points")
	require.WithinDuration(t, time.Now().Add(platform.DeletedBackupRetention), archive.Spec.DeleteAfter.Time, time.Minute)
	require.Nil(t, env.reload().Status.Backup)
	require.Empty(t, env.storage.purged, "the controller itself never deletes backup data; only the janitor does after the deadline")
}

func TestReconcileBackupLifecycleWithBackupsOffAndNothingToCleanIsANoop(t *testing.T) {
	cluster := healthyCluster(testDevTargetNS)
	env := newBackupEnv(t, func(c *backupEnvConfig) { c.backup = nil; c.extraObjects = append(c.extraObjects, cluster) })
	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, nil))
	require.Empty(t, env.archives())
}

func restorePointIDs(archive *geassv1alpha1.GeassBackupArchive) []string {
	ids := make([]string, 0, len(archive.Status.RestorePoints))
	for _, point := range archive.Status.RestorePoints {
		ids = append(ids, point.ID)
	}
	return ids
}

// --- deleting a database ----------------------------------------------------------------------

func deletingEnv(t *testing.T, objects ...client.Object) *backupEnv {
	t.Helper()
	return deletingEnvSince(t, 0, objects...)
}

// deletingEnvSince is a database whose deletion was requested deletedAgo ago.
func deletingEnvSince(t *testing.T, deletedAgo time.Duration, objects ...client.Object) *backupEnv {
	t.Helper()
	deletedAt := metav1.NewTime(time.Now().Add(-deletedAgo))
	env := newBackupEnv(t, func(c *backupEnvConfig) {
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) {
			db.Finalizers = []string{platform.FinalizerDatabase}
			db.DeletionTimestamp = &deletedAt
		})
		c.extraObjects = append(c.extraObjects, objects...)
	})
	return env
}

func (e *backupEnv) reconcile() (time.Duration, error) {
	e.t.Helper()
	result, err := e.r.Reconcile(context.Background(), requestFor(e.db))
	return result.RequeueAfter, err
}

func (e *backupEnv) databaseGone() bool {
	e.t.Helper()
	return !e.exists(&geassv1alpha1.GeassDatabase{}, platform.SystemNamespace, testDBName)
}

func TestDeletingADatabaseTakesAFinalBackupBeforeRemovingIt(t *testing.T) {
	env := deletingEnv(t, healthyCluster(testDevTargetNS))
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, healthyCluster(testDevTargetNS), plan))

	requeue, err := env.reconcile()
	require.NoError(t, err)
	require.Positive(t, requeue, "deletion must come back to check on the final backup")
	require.False(t, env.databaseGone(), "the database stays until its last backup is safe")
	require.True(t, env.exists(&cnpgv1.Cluster{}, env.wsNS, testDBName), "the data is not touched while the final backup runs")
	var final cnpgv1.Backup
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testDBName + "-final", Namespace: env.wsNS}, &final))
	require.Equal(t, testDBName, final.Spec.Cluster.Name)
	require.Equal(t, cnpgv1.BackupMethodBarmanObjectStore, final.Spec.Method)
	require.Nil(t, env.archive(plan.archive.Name).Spec.DeleteAfter, "the archive is not retired until the final backup has finished")

	// Still running: another reconcile neither creates a second backup nor gives up.
	requeue, err = env.reconcile()
	require.NoError(t, err)
	require.Positive(t, requeue)
	require.False(t, env.databaseGone())

	// The backup finishes.
	stopped := utcSeconds(time.Now())
	started := metav1.NewTime(stopped.Add(-time.Minute))
	stop := metav1.NewTime(stopped)
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKeyFromObject(&final), &final))
	final.Status = cnpgv1.BackupStatus{Phase: cnpgv1.BackupPhaseCompleted, BackupID: "20260310T101500", StartedAt: &started, StoppedAt: &stop}
	require.NoError(t, env.client.Update(context.Background(), &final))

	_, err = env.reconcile()
	require.NoError(t, err)
	require.True(t, env.databaseGone())
	require.False(t, env.exists(&cnpgv1.Cluster{}, env.wsNS, testDBName), "the cluster is removed after the final backup")
	require.False(t, env.exists(&cnpgv1.ScheduledBackup{}, env.wsNS, testDBName))
	require.False(t, env.exists(&cnpgv1.Backup{}, env.wsNS, testDBName+"-final"))
	require.False(t, env.exists(&corev1.Secret{}, env.wsNS, testBackupSecretOrd), "the database's copy of the credentials goes with it")

	archive := env.archive(plan.archive.Name)
	require.NotNil(t, archive.Spec.DeleteAfter)
	require.WithinDuration(t, time.Now().Add(platform.DeletedBackupRetention), archive.Spec.DeleteAfter.Time, time.Minute)
	require.Contains(t, restorePointIDs(archive), "20260310T101500", "the final backup is a restore point of the deleted database")
	require.True(t, archive.Status.RecoveryWindowEnd.Time.Equal(stopped), "a deleted database is recoverable up to its final backup")
	require.Empty(t, env.storage.purged, "backups survive the database; they are purged later by the janitor")
}

func TestDeletingADatabaseDoesNotWaitForeverForTheFinalBackup(t *testing.T) {
	stuck := backupObject(testDevTargetNS, testDBName+"-final", testDBName, cnpgv1.BackupPhaseRunning, "", time.Now())
	// The wait is measured from when the final backup started, not from when deletion was requested,
	// so a controller that was down for a while still gives the backup its full chance.
	stuck.CreationTimestamp = metav1.NewTime(time.Now().Add(-platform.FinalBackupWait - time.Minute))
	env := deletingEnv(t, healthyCluster(testDevTargetNS), stuck)
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	_, err = env.reconcile()
	require.NoError(t, err)
	require.True(t, env.databaseGone(), "a backup that never finishes must not leave the database stuck deleting")
	require.NotNil(t, env.archive(plan.archive.Name).Spec.DeleteAfter, "earlier backups stay restorable")
}

func TestDeletingADatabaseTakesTheFinalBackupEvenIfDeletionWasRequestedLongAgo(t *testing.T) {
	env := deletingEnvSince(t, time.Hour, healthyCluster(testDevTargetNS))
	_, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	requeue, err := env.reconcile()
	require.NoError(t, err)
	require.Positive(t, requeue)
	require.True(t, env.exists(&cnpgv1.Backup{}, env.wsNS, testDBName+"-final"), "a controller restart must not cost a database its last backup")
	require.False(t, env.databaseGone())
}

func TestDeletingADatabaseWhoseFinalBackupFailedStillCompletes(t *testing.T) {
	failed := backupObject(testDevTargetNS, testDBName+"-final", testDBName, cnpgv1.BackupPhaseFailed, "", time.Now())
	env := deletingEnv(t, healthyCluster(testDevTargetNS), failed)
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	_, err = env.reconcile()
	require.NoError(t, err)
	require.True(t, env.databaseGone())
	require.NotNil(t, env.archive(plan.archive.Name).Spec.DeleteAfter)
}

func TestDeletingADatabaseWhoseClusterNeverStartedRetiresItsArchiveWithoutABackup(t *testing.T) {
	env := deletingEnv(t)
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	_, err = env.reconcile()
	require.NoError(t, err)
	require.True(t, env.databaseGone())
	require.False(t, env.exists(&cnpgv1.Backup{}, env.wsNS, testDBName+"-final"))
	require.NotNil(t, env.archive(plan.archive.Name).Spec.DeleteAfter)
}

func TestDeletingADatabaseWhoseClusterIsUnhealthySkipsTheFinalBackup(t *testing.T) {
	unhealthy := healthyCluster(testDevTargetNS, func(c *cnpgv1.Cluster) {
		c.Status.Phase, c.Status.ReadyInstances, c.Status.CurrentPrimary = "Setting up primary", 0, ""
	})
	env := deletingEnv(t, unhealthy)
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	_, err = env.reconcile()
	require.NoError(t, err)
	require.True(t, env.databaseGone(), "there is nothing to back up and nothing to wait for")
	require.False(t, env.exists(&cnpgv1.Backup{}, env.wsNS, testDBName+"-final"))
	require.NotNil(t, env.archive(plan.archive.Name).Spec.DeleteAfter)
}

func TestDeletingADatabaseWithoutBackupsRemovesItImmediately(t *testing.T) {
	env := deletingEnv(t, healthyCluster(testDevTargetNS))
	live := env.reload()
	live.Spec.Backup = nil
	require.NoError(t, env.client.Update(context.Background(), live))
	requeue, err := env.reconcile()
	require.NoError(t, err)
	require.Zero(t, requeue)
	require.True(t, env.databaseGone())
	require.False(t, env.exists(&cnpgv1.Cluster{}, env.wsNS, testDBName))
	require.False(t, env.exists(&cnpgv1.Backup{}, env.wsNS, testDBName+"-final"), "no backups configured means no final backup")
	require.Empty(t, env.archives())
}

func TestDeletingAFormerlyBackedUpDatabaseStillBacksItUpFinally(t *testing.T) {
	// Backups were turned on, then the spec was changed in the dashboard but not yet deployed or
	// the flag was cleared by hand: what matters is that a live archive exists.
	env := deletingEnv(t, healthyCluster(testDevTargetNS))
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NotNil(t, plan.archive)
	live := env.reload()
	live.Spec.Backup = nil
	require.NoError(t, env.client.Update(context.Background(), live))

	requeue, err := env.reconcile()
	require.NoError(t, err)
	require.Positive(t, requeue)
	require.True(t, env.exists(&cnpgv1.Backup{}, env.wsNS, testDBName+"-final"), "a live archive is the signal that data is worth saving")
}

func TestDeletionIsNotBlockedWhenTheFinalBackupCannotBeRequested(t *testing.T) {
	env := deletingEnv(t, healthyCluster(testDevTargetNS))
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	// e.g. the environment's namespace is already terminating.
	env.r.Client = interceptor.NewClient(env.client.(client.WithWatch), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*cnpgv1.Backup); ok {
				return errors.New("namespace is being terminated")
			}
			return c.Create(ctx, obj, opts...)
		},
	})

	_, err = env.reconcile()
	require.NoError(t, err)
	require.True(t, env.databaseGone())
	require.NotNil(t, env.archive(plan.archive.Name).Spec.DeleteAfter)
}

func TestDeleteTargetResourcesRemovesBackupObjects(t *testing.T) {
	cluster := healthyCluster(testDevTargetNS)
	env, plan := lifecycleEnv(t, cluster,
		backupObject(testDevTargetNS, testDBName+"-final", testDBName, cnpgv1.BackupPhaseCompleted, "20260310T101500", time.Now()),
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: testDBName + "-restore-credentials", Namespace: testDevTargetNS}},
	)
	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	env.r.deleteTargetResources(context.Background(), env.db, env.wsNS)

	require.False(t, env.exists(&cnpgv1.ScheduledBackup{}, env.wsNS, testDBName))
	require.False(t, env.exists(&cnpgv1.Backup{}, env.wsNS, testDBName+"-final"))
	require.False(t, env.exists(&corev1.Secret{}, env.wsNS, testBackupSecretOrd))
	require.False(t, env.exists(&corev1.Secret{}, env.wsNS, testDBName+"-restore-credentials"))
	require.NotEmpty(t, env.archives(), "deleting the running resources must never delete the archive record")
	require.True(t, env.exists(&corev1.Secret{}, platform.SystemNamespace, platform.BackupStorageSecretName(testBackupBucket)), "bucket credentials are shared by other databases")
}

func TestDatabasesForProjectMapsOnlyBackupEnabledDatabases(t *testing.T) {
	other := func(name string, backup *geassv1alpha1.GeassDatabaseBackupSpec, project string) *geassv1alpha1.GeassDatabase {
		return &geassv1alpha1.GeassDatabase{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassDatabaseSpec{Project: project, Engine: geassv1alpha1.DatabaseEnginePostgres, Backup: backup},
		}
	}
	env := newBackupEnv(t, func(c *backupEnvConfig) {
		c.extraObjects = append(c.extraObjects,
			other("with-backups", &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true}, testProjectName),
			other("backups-off", &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: false}, testProjectName),
			other("no-backup-section", nil, testProjectName),
			other("elsewhere", &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true}, "another-project"),
		)
	})
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace}}

	requests := env.r.databasesForProject(context.Background(), project)
	names := make([]string, 0, len(requests))
	for _, request := range requests {
		names = append(names, request.Name)
	}
	require.ElementsMatch(t, []string{testDBName, "with-backups"}, names, "a project's storage changing only matters to databases that want backups")
}

// The operator reports its recoverability fields about half a minute after a backup finishes.
// Until then the window starts at the end of the oldest backup, which is what the field means.
func TestReconcileBackupLifecycleDerivesTheWindowBeforeTheOperatorReportsIt(t *testing.T) {
	now := utcSeconds(time.Now())
	cluster := healthyCluster(testDevTargetNS, func(c *cnpgv1.Cluster) {
		c.Status.FirstRecoverabilityPoint, c.Status.LastSuccessfulBackup = "", ""
	})
	env, plan := lifecycleEnv(t, cluster,
		backupObject(testDevTargetNS, "orders-first", testDBName, cnpgv1.BackupPhaseCompleted, "20260309T020000", now.Add(-25*time.Hour)),
		backupObject(testDevTargetNS, "orders-second", testDBName, cnpgv1.BackupPhaseCompleted, "20260310T020000", now.Add(-time.Hour)),
	)

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	archive := env.archive(plan.archive.Name)
	require.NotNil(t, archive.Status.RecoveryWindowStart)
	require.True(t, archive.Status.RecoveryWindowStart.Time.Equal(now.Add(-25*time.Hour)), "a restore is impossible before the oldest backup ends")
	require.True(t, archive.Status.LastBackupTime.Time.Equal(now.Add(-time.Hour)))
	status := env.reload().Status.Backup
	require.Equal(t, geassv1alpha1.BackupStateProtected, status.State)
	require.True(t, status.RecoveryWindowStart.Time.Equal(now.Add(-25*time.Hour)))
}

func TestADegradedPlanLeavesBackupsExactlyAsTheyWere(t *testing.T) {
	cluster := healthyCluster(testDevTargetNS)
	env, plan := lifecycleEnv(t, cluster)
	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))
	before := env.reload().Status.Backup
	require.NotNil(t, before)
	env.db = env.reload()

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, &backupPlan{degraded: true}))

	require.Equal(t, before, env.reload().Status.Backup, "a problem working out the plan says nothing about how the backups are doing")
	require.True(t, env.exists(&cnpgv1.ScheduledBackup{}, env.wsNS, testDBName), "the schedule stays")
	require.Nil(t, env.archive(plan.archive.Name).Spec.DeleteAfter, "a passing problem must not retire the archive")
}

func TestAFailureFromBeforeTheArchiveExistedIsNotCountedAgainstIt(t *testing.T) {
	env := newBackupEnv(t)
	now := utcSeconds(time.Now())
	archive := &geassv1alpha1.GeassBackupArchive{ObjectMeta: metav1.ObjectMeta{
		Name: testDBServerName, Namespace: platform.SystemNamespace, CreationTimestamp: metav1.NewTime(now.Add(-time.Hour)),
	}}
	failedAt := func(at time.Time) *cnpgv1.Cluster {
		return healthyCluster(testDevTargetNS, func(c *cnpgv1.Cluster) { c.Status.LastFailedBackup = at.Format(time.RFC3339) })
	}

	observed, err := env.r.observeBackups(context.Background(), env.db, env.wsNS, failedAt(now.Add(-2*time.Hour)), archive)
	require.NoError(t, err)
	require.Nil(t, observed.lastFailure, "the cluster remembers one failure across archives; this one belongs to an earlier archive")

	observed, err = env.r.observeBackups(context.Background(), env.db, env.wsNS, failedAt(now.Add(-30*time.Minute)), archive)
	require.NoError(t, err)
	require.NotNil(t, observed.lastFailure, "a failure of this archive's own backups still counts")
}

func TestAWindDownOfADatabaseThatNeverHadBackupsWritesNothing(t *testing.T) {
	cluster := healthyCluster(testDevTargetNS)
	env := newBackupEnv(t, func(c *backupEnvConfig) { c.backup = nil; c.extraObjects = append(c.extraObjects, cluster) })
	var writes int
	env.r.Client = interceptor.NewClient(env.client.(client.WithWatch), interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			writes++
			return c.Delete(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			writes++
			return c.Update(ctx, obj, opts...)
		},
		SubResourceUpdate: func(ctx context.Context, c client.Client, subResource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			writes++
			return c.SubResource(subResource).Update(ctx, obj, opts...)
		},
	})

	for range 3 {
		require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, nil))
	}
	require.Zero(t, writes, "a database with no backups must not send a delete for a schedule it never had, on every reconcile")
}

func TestAWindDownStillCleansUpWhenTheDatabaseHadBackups(t *testing.T) {
	cluster := healthyCluster(testDevTargetNS)
	env, plan := lifecycleEnv(t, cluster)
	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))
	env.db = env.reload()
	env.db.Spec.Backup.Enabled = false

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, nil))
	require.False(t, env.exists(&cnpgv1.ScheduledBackup{}, env.wsNS, testDBName))
	require.NotNil(t, env.archive(plan.archive.Name).Spec.DeleteAfter)
	require.Nil(t, env.reload().Status.Backup)
}
