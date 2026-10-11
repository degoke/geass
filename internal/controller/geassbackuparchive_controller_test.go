package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// archiveEnv is a project with one backed-up database whose archive has been created.
func archiveEnv(t *testing.T, mutate ...func(*backupEnvConfig)) (*backupEnv, *GeassBackupArchiveReconciler, *geassv1alpha1.GeassBackupArchive) {
	t.Helper()
	env := newBackupEnv(t, mutate...)
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NotNil(t, plan.archive)
	env.storage.constructed, env.storage.ensuredBuckets, env.storage.userRequests = nil, nil, nil
	reconciler := &GeassBackupArchiveReconciler{Client: env.client, Scheme: env.r.Scheme, newBackupStorage: env.storage.factory()}
	return env, reconciler, plan.archive
}

func (e *backupEnv) retire(archive *geassv1alpha1.GeassBackupArchive, deleteAfter time.Time) {
	e.t.Helper()
	live := e.archive(archive.Name)
	live.Spec.DeleteAfter = timePtr(metav1.NewTime(deleteAfter))
	require.NoError(e.t, e.client.Update(context.Background(), live))
}

func reconcileArchive(t *testing.T, r *GeassBackupArchiveReconciler, archive *geassv1alpha1.GeassBackupArchive) time.Duration {
	t.Helper()
	result, err := r.Reconcile(context.Background(), requestFor(archive))
	require.NoError(t, err)
	return result.RequeueAfter
}

func TestArchiveOfALiveDatabaseIsLeftAlone(t *testing.T) {
	env, r, archive := archiveEnv(t)
	reconcileArchive(t, r, archive)
	require.Nil(t, env.archive(archive.Name).Spec.DeleteAfter)
	require.Empty(t, env.storage.purged)
}

func TestArchiveWhoseDatabaseIsGoneIsRetiredNotPurged(t *testing.T) {
	env, r, archive := archiveEnv(t)
	require.NoError(t, env.client.Delete(context.Background(), env.reload()))

	reconcileArchive(t, r, archive)

	live := env.archive(archive.Name)
	require.NotNil(t, live.Spec.DeleteAfter, "a database that vanished without its controller running must still leave a restore window")
	require.WithinDuration(t, time.Now().Add(platform.DeletedBackupRetention), live.Spec.DeleteAfter.Time, time.Minute)
	require.Empty(t, env.storage.purged, "retiring is not purging")
}

func TestArchiveWhoseDatabaseNameWasReusedIsRetired(t *testing.T) {
	env, r, archive := archiveEnv(t)
	live := env.reload()
	require.NoError(t, env.client.Delete(context.Background(), live))
	reborn := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: platform.SystemNamespace, UID: "99999999-0000-4000-8000-000000000009"},
		Spec:       live.Spec,
	}
	require.NoError(t, env.client.Create(context.Background(), reborn))

	reconcileArchive(t, r, archive)
	require.NotNil(t, env.archive(archive.Name).Spec.DeleteAfter, "a new database with the same name does not own the old one's backups")
}

func TestArchiveOfADatabaseBeingDeletedIsLeftToTheDatabaseController(t *testing.T) {
	env, r, archive := archiveEnv(t)
	live := env.reload()
	now := metav1.Now()
	live.Finalizers, live.DeletionTimestamp = []string{platform.FinalizerDatabase}, &now
	// The fake client refuses to set a deletion timestamp by update, so recreate the object.
	require.NoError(t, env.client.Delete(context.Background(), live))
	deleting := live.DeepCopy()
	deleting.ResourceVersion = ""
	require.NoError(t, env.client.Create(context.Background(), deleting))

	reconcileArchive(t, r, archive)
	require.Nil(t, env.archive(archive.Name).Spec.DeleteAfter, "the database controller is still taking its final backup")
}

func TestRetiredArchiveWaitsForItsDeadline(t *testing.T) {
	env, r, archive := archiveEnv(t)
	env.retire(archive, time.Now().Add(72*time.Hour))

	requeue := reconcileArchive(t, r, archive)

	require.InDelta(t, (72 * time.Hour).Seconds(), requeue.Seconds(), 60, "the janitor wakes up at the deadline instead of polling")
	require.Empty(t, env.storage.purged)
	require.True(t, env.exists(&geassv1alpha1.GeassBackupArchive{}, platform.SystemNamespace, archive.Name))
}

func TestRetiredArchivePastItsDeadlineIsPurgedAndForgotten(t *testing.T) {
	env, r, archive := archiveEnv(t)
	env.retire(archive, time.Now().Add(-time.Minute))

	reconcileArchive(t, r, archive)

	require.Equal(t, []string{testBackupBucket + "/" + testProjectName + "/" + testDBServerName + "/"}, env.storage.purged,
		"exactly this database's folder, with a trailing slash so a sibling that shares a name prefix is untouched")
	require.Equal(t, []fakeClientCreds{{"AKIAROOT", "root-secret", testBackupRegion}}, env.storage.constructed,
		"purging uses the project's own AWS credentials, which can list versions; the scoped backup user cannot")
	require.False(t, env.exists(&geassv1alpha1.GeassBackupArchive{}, platform.SystemNamespace, archive.Name), "the record goes once the data is gone")
}

func TestPurgeFailureKeepsTheArchiveAndRetries(t *testing.T) {
	env, r, archive := archiveEnv(t)
	env.retire(archive, time.Now().Add(-time.Minute))
	env.storage.purge = errString("AWS S3 403 Forbidden: AccessDenied")

	requeue := reconcileArchive(t, r, archive)

	require.Equal(t, archivePurgeRetryInterval, requeue)
	kept := env.archive(archive.Name)
	condition := findCondition(kept.Status.Conditions, archivePurgedCondition)
	require.NotNil(t, condition, "a stuck purge must be visible, not silent")
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Contains(t, condition.Message, "AccessDenied")
}

func TestPurgeWithoutTheAWSConnectionKeepsTheArchive(t *testing.T) {
	env, r, archive := archiveEnv(t)
	env.retire(archive, time.Now().Add(-time.Minute))
	require.NoError(t, env.client.Delete(context.Background(), &geassv1alpha1.GeassCloudConnection{ObjectMeta: metav1.ObjectMeta{Name: testAWSConnection, Namespace: platform.SystemNamespace}}))

	requeue := reconcileArchive(t, r, archive)

	require.Equal(t, archivePurgeRetryInterval, requeue)
	require.Empty(t, env.storage.purged)
	require.True(t, env.exists(&geassv1alpha1.GeassBackupArchive{}, platform.SystemNamespace, archive.Name), "without credentials the data cannot be deleted, so the record must stay to remember it")
	require.NotNil(t, findCondition(env.archive(archive.Name).Status.Conditions, archivePurgedCondition))
}

func TestPurgeCredentialsMustBelongToTheArchivesProject(t *testing.T) {
	env, r, archive := archiveEnv(t)
	env.retire(archive, time.Now().Add(-time.Minute))
	connection := &geassv1alpha1.GeassCloudConnection{}
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testAWSConnection, Namespace: platform.SystemNamespace}, connection))
	connection.Spec.Project = "someone-elses-project"
	require.NoError(t, env.client.Update(context.Background(), connection))

	reconcileArchive(t, r, archive)
	require.Empty(t, env.storage.purged, "another project's credentials must never be used to delete this project's data")
}

func TestMissingArchiveIsIgnored(t *testing.T) {
	_, r, archive := archiveEnv(t)
	gone := archive.DeepCopy()
	gone.Name = "never-existed"
	require.Zero(t, reconcileArchive(t, r, gone))
}

func TestArchivesForDatabaseMapsDatabaseEventsToItsArchives(t *testing.T) {
	env, r, archive := archiveEnv(t)
	other := archive.DeepCopy()
	other.ObjectMeta = metav1.ObjectMeta{Name: "inventory-77777777", Namespace: platform.SystemNamespace}
	other.Spec.Database, other.Spec.DatabaseUID, other.Spec.ServerName = "inventory", "77777777-0000-4000-8000-000000000007", "inventory-77777777"
	require.NoError(t, env.client.Create(context.Background(), other))

	requests := r.archivesForDatabase(context.Background(), env.db)
	names := make([]string, 0, len(requests))
	for _, request := range requests {
		names = append(names, request.Name)
	}
	require.Equal(t, []string{archive.Name}, names)
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
