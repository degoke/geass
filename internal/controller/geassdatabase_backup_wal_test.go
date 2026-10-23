package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/platform"
)

func backupNeeding(namespace, name, id, begin, end string, stopped time.Time) *cnpgv1.Backup {
	backup := backupObject(namespace, name, testDBName, cnpgv1.BackupPhaseCompleted, id, stopped)
	backup.Status.BeginWal, backup.Status.EndWal = begin, end
	return backup
}

func archivedWal(segments ...string) []string {
	keys := make([]string, 0, len(segments))
	for _, segment := range segments {
		keys = append(keys, platform.BarmanLogPrefix(testProjectName, testDBServerName)+platform.WalDirectory(segment)+segment+".gz")
	}
	return keys
}

// A copy in the bucket is a restore point only while the files it needs are too. One taken before archiving worked is
// complete in the bucket and can never be restored: recovery fails for ever with "first needed WAL ... not found".
func TestACopyWhoseLogFilesNeverReachedTheArchiveIsNotARestorePoint(t *testing.T) {
	now := utcSeconds(time.Now())
	cluster := healthyCluster(testDevTargetNS)
	env, plan := lifecycleEnv(t, cluster,
		backupNeeding(testDevTargetNS, "orders-early", "20260310T020000", "000000010000000000000008", "000000010000000000000009", now.Add(-time.Hour)),
		backupNeeding(testDevTargetNS, "orders-later", "20260310T040000", "00000001000000000000000B", "00000001000000000000000B", now.Add(-30*time.Minute)),
	)
	// The first file of the early copy was never archived; the later copy has all of its.
	env.storage.allKeys = archivedWal("000000010000000000000009", "00000001000000000000000B")

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	require.Equal(t, []string{"20260310T040000"}, restorePointIDs(env.archive(plan.archive.Name)), "only the copy that can be restored is listed")
	require.Equal(t, geassv1alpha1.BackupStateProtected, env.reload().Status.Backup.State)
}

func TestWithNoCopyThatCanBeRestoredTheDashboardDoesNotSayProtected(t *testing.T) {
	now := utcSeconds(time.Now())
	cluster := healthyCluster(testDevTargetNS)
	env, plan := lifecycleEnv(t, cluster,
		backupNeeding(testDevTargetNS, "orders-early", "20260310T020000", "000000010000000000000008", "000000010000000000000009", now.Add(-time.Hour)),
	)
	env.storage.allKeys = archivedWal("000000010000000000000009")

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	require.Empty(t, restorePointIDs(env.archive(plan.archive.Name)))
	status := env.reload().Status.Backup
	require.Equal(t, geassv1alpha1.BackupStateAtRisk, status.State)
	require.Contains(t, status.Message, "cannot be restored")
	require.Nil(t, status.LastBackupTime, "a copy that cannot be restored is not a last backup")
	require.Nil(t, status.RecoveryWindowStart)
}

func TestACopyThatNamesNoLogFilesOrThatJustFinishedIsTrusted(t *testing.T) {
	now := utcSeconds(time.Now())
	cluster := healthyCluster(testDevTargetNS)
	env, plan := lifecycleEnv(t, cluster,
		backupObject(testDevTargetNS, "orders-old-record", testDBName, cnpgv1.BackupPhaseCompleted, "20260310T020000", now.Add(-time.Hour)), // no WAL fields
		backupNeeding(testDevTargetNS, "orders-just-now", "20260310T040000", "00000001000000000000000B", "00000001000000000000000C", now.Add(-30*time.Second)),
	)

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	require.Equal(t, []string{"20260310T040000", "20260310T020000"}, restorePointIDs(env.archive(plan.archive.Name)),
		"an older record that says nothing of its files, and a copy whose last file has not had time to arrive, are not doubted")
}

// A copy taken from a standby finishes while its last log file is still the one being written, and on a database with
// nothing else going on that file reaches the archive when archive_timeout, five minutes, ends. A live run on K3s showed it:
// the file arrived four and a half minutes after the copy was listed. A copy that is missing it sooner than that is a copy
// that is on its way, and is looked at again soon so that it is not left saying "Protected" for a regular look's wait.
func TestACopyWhoseLastLogFileIsStillOnItsWayIsTrustedAndLookedAtAgainSoon(t *testing.T) {
	now := utcSeconds(time.Now())
	cluster := healthyCluster(testDevTargetNS)
	env, plan := lifecycleEnv(t, cluster,
		backupNeeding(testDevTargetNS, "orders-standby", "20260310T020000", "000000010000000000000003", "000000010000000000000004", now.Add(-4*time.Minute)),
	)
	env.storage.allKeys = archivedWal("000000010000000000000003")

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	require.Equal(t, []string{"20260310T020000"}, restorePointIDs(env.archive(plan.archive.Name)), "four minutes is inside archive_timeout")
	require.True(t, env.r.backupLook.wanted(env.db), "it is not yet known to be restorable, so it is looked at again within a minute")

	// The file arrives. The next look finds it, and the database goes back to its regular looks.
	env.storage.allKeys = archivedWal("000000010000000000000003", "000000010000000000000004")
	env.r.clock = func() time.Time { return now.Add(6 * time.Minute) }
	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))
	require.Equal(t, []string{"20260310T020000"}, restorePointIDs(env.archive(plan.archive.Name)))
	require.False(t, env.r.backupLook.wanted(env.db), "a verified copy needs no extra looks")
}

func TestACopyStillMissingItsLastLogFileAfterArchiveTimeoutIsNotARestorePoint(t *testing.T) {
	now := utcSeconds(time.Now())
	cluster := healthyCluster(testDevTargetNS)
	env, plan := lifecycleEnv(t, cluster,
		backupNeeding(testDevTargetNS, "orders-standby", "20260310T020000", "000000010000000000000003", "000000010000000000000004", now.Add(-8*time.Minute)),
	)
	env.storage.allKeys = archivedWal("000000010000000000000003")

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	require.Empty(t, restorePointIDs(env.archive(plan.archive.Name)), "eight minutes is past archive_timeout with a file still missing")
}

func TestABucketThatCannotBeReadDoesNotHideRestorePoints(t *testing.T) {
	now := utcSeconds(time.Now())
	cluster := healthyCluster(testDevTargetNS)
	env, plan := lifecycleEnv(t, cluster,
		backupNeeding(testDevTargetNS, "orders-early", "20260310T020000", "000000010000000000000008", "000000010000000000000009", now.Add(-time.Hour)),
	)
	env.storage.listErr = errors.New("the bucket is not reachable")

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan),
		"not knowing is not a reason to fail the database's backups or to hide a copy")

	require.Equal(t, []string{"20260310T020000"}, restorePointIDs(env.archive(plan.archive.Name)))
}

func automaticBackups(t *testing.T, env *backupEnv) []cnpgv1.Backup {
	t.Helper()
	var backups cnpgv1.BackupList
	require.NoError(t, env.client.List(context.Background(), &backups, client.InNamespace(env.wsNS), client.MatchingLabels{labelAutomaticBackup: testDBName}))
	return backups.Items
}

// With no copy that can be restored, the archive proven to work now, and no attempt on its way, Geass takes a new copy
// itself instead of leaving the database unprotected until the next night's.
func TestWithNoCopyThatCanBeRestoredANewOneIsTakenOnceArchivingWorks(t *testing.T) {
	now := utcSeconds(time.Now())
	cluster := healthyCluster(testDevTargetNS)
	early := backupNeeding(testDevTargetNS, "orders-early", "20260310T020000", "000000010000000000000008", "000000010000000000000009", env0Stopped(now))
	env, plan := lifecycleEnv(t, cluster, early)
	attempt := early.Status.StoppedAt.Time
	env.storage.allKeys = archivedWal("000000010000000000000009", "00000001000000000000000A")
	// The files that arrived after the attempt are what proves archiving works now.
	env.storage.modified = map[string]time.Time{}
	for _, key := range env.storage.allKeys {
		env.storage.modified[key] = attempt.Add(time.Minute)
	}
	env.r.clock = func() time.Time { return attempt.Add(10 * time.Minute) }

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	taken := automaticBackups(t, env)
	require.Len(t, taken, 1)
	require.Equal(t, testDBName, taken[0].Spec.Cluster.Name)
	require.Equal(t, cnpgv1.BackupMethodBarmanObjectStore, taken[0].Spec.Method)
	require.Equal(t, testDBName, taken[0].Labels[labelAutomaticBackup], "it is known as one Geass took on its own")

	// One that is on its way is waited for, and not piled on.
	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))
	require.Len(t, automaticBackups(t, env), 1)
}

func env0Stopped(now time.Time) time.Time { return now.Add(-time.Hour) }

func TestNoNewCopyIsTakenWhileArchivingStillDoesNotWork(t *testing.T) {
	now := utcSeconds(time.Now())
	cluster := healthyCluster(testDevTargetNS)
	early := backupNeeding(testDevTargetNS, "orders-early", "20260310T020000", "000000010000000000000008", "000000010000000000000009", now.Add(-time.Hour))
	env, plan := lifecycleEnv(t, cluster, early)
	attempt := early.Status.StoppedAt.Time
	env.storage.allKeys = archivedWal("000000010000000000000009")
	env.storage.modified = map[string]time.Time{env.storage.allKeys[0]: attempt.Add(-time.Minute)} // older than the attempt
	env.r.clock = func() time.Time { return attempt.Add(10 * time.Minute) }

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	require.Empty(t, automaticBackups(t, env), "a copy taken now would be as useless as the last")
}

func TestNoNewCopyIsTakenRightAfterAnAttemptOrWhenThereAreFewerReasonsThanCopies(t *testing.T) {
	now := utcSeconds(time.Now())
	cluster := healthyCluster(testDevTargetNS)
	early := backupNeeding(testDevTargetNS, "orders-early", "20260310T020000", "000000010000000000000008", "000000010000000000000009", now.Add(-time.Hour))
	env, plan := lifecycleEnv(t, cluster, early)
	attempt := early.Status.StoppedAt.Time
	env.storage.allKeys = archivedWal("000000010000000000000009")
	env.storage.modified = map[string]time.Time{env.storage.allKeys[0]: attempt.Add(time.Minute)}

	env.r.clock = func() time.Time { return attempt.Add(time.Minute) }
	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))
	require.Empty(t, automaticBackups(t, env), "the last attempt only just ended")

	// Each copy Geass takes is as useless as the first: it stops after a few instead of filling the bucket.
	for i := range maxAutomaticBackups {
		failed := backupObject(testDevTargetNS, fmt.Sprintf("orders-again-%d", i), testDBName, cnpgv1.BackupPhaseFailed, "", attempt.Add(-time.Duration(i)*time.Hour))
		failed.Labels = map[string]string{labelAutomaticBackup: testDBName}
		require.NoError(t, env.client.Create(context.Background(), failed))
	}
	env.r.clock = func() time.Time { return attempt.Add(time.Hour) }
	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))
	require.Len(t, automaticBackups(t, env), maxAutomaticBackups)
}

func TestADatabaseWhoseFirstCopyIsStillOnItsWayTakesNoOtherCopy(t *testing.T) {
	cluster := healthyCluster(testDevTargetNS, func(c *cnpgv1.Cluster) { c.Status.LastSuccessfulBackup, c.Status.FirstRecoverabilityPoint = "", "" })
	env, plan := lifecycleEnv(t, cluster)
	env.storage.allKeys = archivedWal("000000010000000000000009")
	env.r.clock = func() time.Time { return time.Now().Add(time.Hour) }

	require.NoError(t, env.r.reconcileBackupLifecycle(context.Background(), env.db, env.wsNS, cluster, plan))

	require.Empty(t, automaticBackups(t, env), "the schedule asks for the first one; nothing has gone wrong yet")
	require.True(t, env.r.backupLook.wanted(env.db), "but it is looked at again soon")
}

func TestARestoreThatKeepsFailingSaysSoAndStillCountsAsRestoring(t *testing.T) {
	require.Equal(t, messageRestoringDatabase, restoringMessage(0))
	require.Equal(t, messageRestoringDatabase, restoringMessage(1))
	message := restoringMessage(5)
	require.Contains(t, message, "failed 5 times")
	require.Contains(t, message, "earlier restore point")
	require.False(t, platform.ReconcileFailureShouldCount(message), "a restore that is waiting is not a database that failed to be made")

	env := newBackupEnv(t)
	require.Zero(t, env.r.recoveryFailures(context.Background(), env.db, env.wsNS))
	for i, failed := range []int32{2, 3} {
		job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("orders-%d-full-recovery", i), Namespace: env.wsNS,
			Labels: map[string]string{cnpgClusterLabel: testDBName, cnpgJobRoleLabel: cnpgFullRecoveryRole},
		}}
		job.Status.Failed = failed
		require.NoError(t, env.client.Create(context.Background(), job))
	}
	other := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "someone-elses", Namespace: env.wsNS, Labels: map[string]string{cnpgClusterLabel: "inventory", cnpgJobRoleLabel: cnpgFullRecoveryRole}}}
	other.Status.Failed = 9
	require.NoError(t, env.client.Create(context.Background(), other))
	require.EqualValues(t, 5, env.r.recoveryFailures(context.Background(), env.db, env.wsNS))
}
