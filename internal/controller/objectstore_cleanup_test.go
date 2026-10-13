package controller

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// otherProject is a second ready project that shares the first one's cluster.
func otherProject(name string) *geassv1alpha1.GeassProject {
	project, _ := readyProjectCluster(name, "dev")
	return project
}

// Every project's buckets live on one server where names are global. Two projects that both ask for "uploads"
// must get two buckets, each reachable only with its own project's login.
func TestProjectsWithTheSameBucketNameGetDifferentBuckets(t *testing.T) {
	forEachEngine(t, func(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, server *fakeObjectStore) {
		ctx := context.Background()
		project, cluster := readyProjectCluster(deployTestProject, "dev")
		payments := deployedBucketIn(t, "assets", deployTestProject, clusterBucketSpec(engine, deployTestBucket))
		billing := deployedBucketIn(t, "files", "billing", clusterBucketSpec(engine, deployTestBucket))
		objects := append([]client.Object{project, otherProject("billing"), cluster, payments, billing}, runningClusterServer(engine)...)
		r, c := newObjectStoreReconciler(t, server.client(), objects...)

		for _, store := range []*geassv1alpha1.GeassObjectStore{payments, billing} {
			_, err := r.Reconcile(ctx, reconcileRequest(store))
			require.NoError(t, err)
		}

		requireBucketCreated(t, engine, server, "payments.dev.uploads")
		requireBucketCreated(t, engine, server, "billing.dev.uploads")
		require.NotContains(t, server.log.all(), "PUT /uploads", "no bucket is created under the bare name")
		require.False(t, server.hasGarageBucket("uploads"))

		for store, want := range map[*geassv1alpha1.GeassObjectStore]string{payments: "payments.dev.uploads", billing: "billing.dev.uploads"} {
			var connection corev1.Secret
			ns, err := platform.ProjectNamespace(store.Spec.Project, "dev")
			require.NoError(t, err)
			require.NoError(t, c.Get(ctx, client.ObjectKey{Name: store.Name + "-connection", Namespace: ns}, &connection))
			require.Equal(t, want, secretValue(&connection, platform.ConnectionKeyBucket))
		}
	})
}

// A name that fits only without the project prefix is refused with what the limit is, not left to fail on
// the server.
func TestBucketNameTooLongForItsProjectIsRefused(t *testing.T) {
	ctx := context.Background()
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, "a-very-long-bucket-name-that-fits-on-its-own-but-not-here"))
	objects := append([]client.Object{project, cluster, store}, runningClusterServer(geassv1alpha1.ObjectStoreEngineSeaweedFS)...)
	r, c := newObjectStoreReconciler(t, server.client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	status := readyStoreStatus(t, c)
	require.Contains(t, readyConditionMessage(status.Conditions), "too long")
	require.Empty(t, server.log.all(), "nothing is created on the server")
}

// startedProvisioning reconciles a deployed bucket once while the server cannot make its login, so its bucket
// exists but it never becomes ready. The login works again before the test deletes the bucket.
func startedProvisioning(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, server *fakeObjectStore) (*GeassObjectStoreReconciler, client.Client, *geassv1alpha1.GeassObjectStore) {
	t.Helper()
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(engine, deployTestBucket))
	objects := append([]client.Object{project, cluster, store}, runningClusterServer(engine)...)
	r, c := newObjectStoreReconciler(t, server.client(), objects...)

	server.failKeyCreation = true
	_, err := r.Reconcile(context.Background(), reconcileRequest(store))
	require.NoError(t, err)
	server.failKeyCreation = false
	status := readyStoreStatus(t, c)
	require.Empty(t, status.ConnectionSecret, "the store never became ready")
	requireBucketCreated(t, engine, server, deployTestServerBucket)
	return r, c, store
}

func deleteStoreAndReconcile(t *testing.T, r *GeassObjectStoreReconciler, c client.Client, store *geassv1alpha1.GeassObjectStore) {
	t.Helper()
	ctx := context.Background()
	var current geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(store), &current))
	require.NoError(t, c.Delete(ctx, &current))
	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)
}

func storeIsGone(t *testing.T, c client.Client, store *geassv1alpha1.GeassObjectStore) bool {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKeyFromObject(store), &geassv1alpha1.GeassObjectStore{})
	return apierrors.IsNotFound(err)
}

// Deleting a bucket while it is still being set up must remove what was already created for it: the bucket
// on the server, its login and its Secrets.
func TestDeletingABucketThatNeverBecameReadyRemovesWhatItCreated(t *testing.T) {
	forEachEngine(t, func(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, server *fakeObjectStore) {
		ctx := context.Background()
		r, c, store := startedProvisioning(t, engine, server)

		var started geassv1alpha1.GeassObjectStore
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(store), &started))
		require.Equal(t, "true", started.Annotations[platform.AnnotationProvisioningStarted], "the store records that it began creating things")

		deleteStoreAndReconcile(t, r, c, store)

		require.True(t, storeIsGone(t, c, store))
		if engine == geassv1alpha1.ObjectStoreEngineGarage {
			require.False(t, server.hasGarageBucket(deployTestServerBucket), "the bucket is removed from the server")
		} else {
			require.Contains(t, server.log.all(), "DELETE /"+deployTestServerBucket, "the bucket is removed from the server")
		}
		require.False(t, server.hasIAMUser("geass-"+deployTestStore))
		require.False(t, server.hasGarageKey("geass-"+deployTestStore))
		require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: deployTestStore + "-connection", Namespace: projectNamespaceName(t)}, &corev1.Secret{})))
	})
}

// A ready bucket is removed with its login, and the login stops working.
func TestDeletingAReadyBucketRemovesItsLogin(t *testing.T) {
	forEachEngine(t, func(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, server *fakeObjectStore) {
		ctx := context.Background()
		project, cluster := readyProjectCluster(deployTestProject, "dev")
		store := deployedProjectBucket(t, clusterBucketSpec(engine, deployTestBucket))
		objects := append([]client.Object{project, cluster, store}, runningClusterServer(engine)...)
		r, c := newObjectStoreReconciler(t, server.client(), objects...)
		_, err := r.Reconcile(ctx, reconcileRequest(store))
		require.NoError(t, err)
		requireBucketUserCreated(t, engine, server, deployTestStore)

		deleteStoreAndReconcile(t, r, c, store)

		require.True(t, storeIsGone(t, c, store))
		require.False(t, server.hasIAMUser("geass-"+deployTestStore))
		require.False(t, server.hasGarageKey("geass-"+deployTestStore))
	})
}

// Found by testing on a live cluster. A bucket deleted while the server is itself being removed still has its bucket and
// login on that server, and must remove them: the server is gone only once its own deletion has finished, which waits
// for the buckets. Treating the server as already gone left both behind.
func TestDeletingABucketWhileTheServerIsBeingRemovedStillCleansUpOnTheServer(t *testing.T) {
	forEachEngine(t, func(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, server *fakeObjectStore) {
		ctx := context.Background()
		project, cluster := readyProjectCluster(deployTestProject, "dev")
		store := deployedProjectBucket(t, clusterBucketSpec(engine, deployTestBucket))
		objects := append([]client.Object{project, cluster, store}, runningClusterServer(engine)...)
		r, c := newObjectStoreReconciler(t, server.client(), objects...)
		_, err := r.Reconcile(ctx, reconcileRequest(store))
		require.NoError(t, err)
		requireBucketUserCreated(t, engine, server, deployTestStore)

		// The server is asked to go. It cannot finish while a bucket is on it, so it is still there.
		var clusterServer geassv1alpha1.GeassObjectStore
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace}, &clusterServer))
		clusterServer.Finalizers = []string{objectStoreFinalizer}
		require.NoError(t, c.Update(ctx, &clusterServer))
		require.NoError(t, c.Delete(ctx, &clusterServer))

		deleteStoreAndReconcile(t, r, c, store)

		require.True(t, storeIsGone(t, c, store))
		if engine == geassv1alpha1.ObjectStoreEngineGarage {
			require.False(t, server.hasGarageBucket(deployTestServerBucket), "the bucket is removed from the server")
		} else {
			require.Contains(t, server.log.all(), "DELETE /"+deployTestServerBucket, "the bucket is removed from the server")
		}
		require.False(t, server.hasIAMUser("geass-"+deployTestStore), "its login is removed")
		require.False(t, server.hasGarageKey("geass-"+deployTestStore), "its login is removed")
	})
}

// A server on its way out takes no new buckets.
func TestABucketIsNotMadeOnAServerThatIsBeingRemoved(t *testing.T) {
	ctx := context.Background()
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, deployTestBucket))
	objects := append([]client.Object{project, cluster, store}, runningClusterServer(geassv1alpha1.ObjectStoreEngineSeaweedFS)...)
	objects[len(objects)-2].(*geassv1alpha1.GeassObjectStore).Finalizers = []string{objectStoreFinalizer}
	r, c := newObjectStoreReconciler(t, server.client(), objects...)
	var clusterServer geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace}, &clusterServer))
	require.NoError(t, c.Delete(ctx, &clusterServer))

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	require.Equal(t, platform.MessageSetUpObjectStorage, readyConditionMessage(readyStoreStatus(t, c).Conditions))
	require.Empty(t, server.log.all(), "nothing is created on a server that is going away")
}

// A deployed bucket that stopped before it created anything, here because the cluster has no object storage,
// has nothing to clean up, so deleting it must not wait on a server.
func TestDeletingADeployedBucketThatCreatedNothingDoesNotTouchTheServer(t *testing.T) {
	ctx := context.Background()
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, deployTestBucket))
	r, c := newObjectStoreReconciler(t, server.client(), project, cluster, store)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)
	status := readyStoreStatus(t, c)
	require.Contains(t, readyConditionMessage(status.Conditions), "Set up object storage")

	deleteStoreAndReconcile(t, r, c, store)

	require.True(t, storeIsGone(t, c, store))
	require.Empty(t, server.log.all())
}

// A bucket renamed after it was deployed is a pending change. What exists on the server is still the deployed
// name, so that is the bucket to remove.
func TestCleanupUsesTheDeployedBucketNotAPendingRename(t *testing.T) {
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, deployTestBucket))
	store.Status.ConnectionSecret = deployTestStore + "-connection"
	store.Spec.Buckets = []string{"renamed"}
	platform.MarkPendingChange(&store.ObjectMeta, platform.PendingChangeSettings)
	objects := append([]client.Object{project, cluster, store}, runningClusterServer(geassv1alpha1.ObjectStoreEngineSeaweedFS)...)
	r, c := newObjectStoreReconciler(t, server.client(), objects...)

	deleteStoreAndReconcile(t, r, c, store)

	require.Contains(t, server.log.all(), "DELETE /"+deployTestServerBucket)
	require.NotContains(t, server.log.all(), "DELETE /payments.dev.renamed")
	require.True(t, storeIsGone(t, c, store))
}

func awsBucketFixtures(t *testing.T) (*geassv1alpha1.GeassObjectStore, []client.Object) {
	t.Helper()
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	connection := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "aws", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassCloudConnectionSpec{
			Provider:  geassv1alpha1.CloudProviderAWS,
			Project:   deployTestProject,
			SecretRef: corev1.LocalObjectReference{Name: "aws-creds"},
		},
	}
	creds := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			platform.SecretKeyAccessKeyID:     []byte("AKIAROOT"),
			platform.SecretKeySecretAccessKey: []byte("root-secret"),
		},
	}
	store := deployedProjectBucket(t, geassv1alpha1.GeassObjectStoreSpec{
		Engine:        geassv1alpha1.ObjectStoreEngineS3,
		Placement:     geassv1alpha1.ObjectStorePlacementExternal,
		ConnectionRef: &corev1.LocalObjectReference{Name: "aws"},
		Region:        "eu-north-1",
		CreateBucket:  true,
		Buckets:       []string{"acme-uploads"},
	})
	return store, []client.Object{project, cluster, connection, creds, store}
}

// The AWS bucket is created before its IAM user. If the user cannot be created the store never becomes ready,
// but the bucket is already in the customer's account, so deleting the store has to remove it.
func TestDeletingAnAWSBucketWhoseUserWasNeverCreatedRemovesTheBucket(t *testing.T) {
	ctx := context.Background()
	requests := &requestLog{}
	store, objects := awsBucketFixtures(t)
	r, c := newObjectStoreReconciler(t, &http.Client{Transport: awsTransport{log: requests, failIAM: "CreateUser"}}, objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)
	status := readyStoreStatus(t, c)
	require.False(t, platform.IsConditionTrue(status.Conditions, platform.ConditionReady))
	require.Empty(t, status.ConnectionSecret)
	require.Contains(t, requests.all(), "S3 PUT acme-uploads.s3.eu-north-1.amazonaws.com", "the bucket was created")

	// The AWS side recovers before the deletion is processed.
	r.HTTP = &http.Client{Transport: awsTransport{log: requests}}
	deleteStoreAndReconcile(t, r, c, store)

	require.Contains(t, requests.all(), "S3 DELETE acme-uploads.s3.eu-north-1.amazonaws.com", "the bucket is removed from the account")
	require.Contains(t, requests.all(), "IAM DeleteUser")
	require.True(t, storeIsGone(t, c, store))
}

// An AWS store that could not even read its connection created nothing, so deleting it needs no AWS call.
func TestDeletingAnAWSBucketThatCreatedNothingMakesNoAWSCalls(t *testing.T) {
	ctx := context.Background()
	requests := &requestLog{}
	store, objects := awsBucketFixtures(t)
	withoutCreds := make([]client.Object, 0, len(objects))
	for _, obj := range objects {
		if secret, ok := obj.(*corev1.Secret); ok && secret.Name == "aws-creds" {
			continue
		}
		withoutCreds = append(withoutCreds, obj)
	}
	r, c := newObjectStoreReconciler(t, &http.Client{Transport: awsTransport{log: requests}}, withoutCreds...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)
	status := readyStoreStatus(t, c)
	require.Contains(t, readyConditionMessage(status.Conditions), "AWS credentials are unavailable")

	deleteStoreAndReconcile(t, r, c, store)

	require.True(t, storeIsGone(t, c, store))
	require.Empty(t, requests.all())
}

// A store that never became ready was never usable. If the credentials that could not create anything cannot
// clean up either, which is what a missing IAM or S3 permission does, deleting the store must not hang on it.
func TestDeletingAnAWSBucketThatNeverBecameReadyIsNotHeldByFailedCleanup(t *testing.T) {
	ctx := context.Background()
	requests := &requestLog{}
	store, objects := awsBucketFixtures(t)
	r, c := newObjectStoreReconciler(t, &http.Client{Transport: awsTransport{log: requests, failIAM: "CreateUser"}}, objects...)
	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	r.HTTP = &http.Client{Transport: awsTransport{log: requests, failIAM: "ListAccessKeys"}}
	deleteStoreAndReconcile(t, r, c, store)

	require.Contains(t, requests.all(), "IAM ListAccessKeys", "cleanup was attempted")
	require.True(t, storeIsGone(t, c, store))
}

func TestDeletingABucketThatNeverBecameReadyIsNotHeldByAnUnreachableServer(t *testing.T) {
	forEachEngine(t, func(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, server *fakeObjectStore) {
		r, c, store := startedProvisioning(t, engine, server)
		r.HTTP = &http.Client{Transport: failingTransport{}}

		deleteStoreAndReconcile(t, r, c, store)

		require.True(t, storeIsGone(t, c, store))
	})
}

// Once a bucket has been ready it holds data, so deletion waits until the bucket is removed.
func TestDeletingAReadyClusterBucketWaitsForCleanup(t *testing.T) {
	ctx := context.Background()
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, deployTestBucket))
	store.Status.ConnectionSecret = deployTestStore + "-connection"
	objects := append([]client.Object{project, cluster, store}, runningClusterServer(geassv1alpha1.ObjectStoreEngineSeaweedFS)...)
	r, c := newObjectStoreReconciler(t, &http.Client{Transport: failingTransport{}}, objects...)
	var current geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(store), &current))
	require.NoError(t, c.Delete(ctx, &current))

	_, err := r.Reconcile(ctx, reconcileRequest(store))

	require.Error(t, err)
	require.False(t, storeIsGone(t, c, store), "the finalizer stays until cleanup succeeds")
	require.Empty(t, server.log.all())
}

// Deleting never falls back to a bucket that merely shares the resource's name: such a bucket was not created
// by this store and may hold another store's data.
func TestDeletingNeverRemovesABucketNamedAfterTheResource(t *testing.T) {
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, strings.Repeat("a", 60)))
	store.Status.ConnectionSecret = deployTestStore + "-connection"
	objects := append([]client.Object{project, cluster, store}, runningClusterServer(geassv1alpha1.ObjectStoreEngineSeaweedFS)...)
	r, c := newObjectStoreReconciler(t, server.client(), objects...)

	deleteStoreAndReconcile(t, r, c, store)

	require.True(t, storeIsGone(t, c, store))
	require.Empty(t, server.log.all(), "no bucket is removed when none can have been created")

	aws, awsObjects := awsBucketFixtures(t)
	aws.Spec.Buckets = nil
	aws.Status.ConnectionSecret = deployTestStore + "-connection"
	awsRequests := &requestLog{}
	r, c = newObjectStoreReconciler(t, &http.Client{Transport: awsTransport{log: awsRequests}}, awsObjects...)

	deleteStoreAndReconcile(t, r, c, aws)

	require.True(t, storeIsGone(t, c, aws))
	require.Contains(t, awsRequests.all(), "IAM DeleteUser")
	for _, request := range awsRequests.all() {
		require.NotContains(t, request, "S3 DELETE", "no AWS bucket is deleted")
	}
}

// Once a store has been ready its bucket holds data, so deletion waits until the bucket is removed.
func TestDeletingAReadyAWSBucketWaitsForCleanup(t *testing.T) {
	ctx := context.Background()
	requests := &requestLog{}
	store, objects := awsBucketFixtures(t)
	store.Status.ConnectionSecret = deployTestStore + "-connection"
	r, c := newObjectStoreReconciler(t, &http.Client{Transport: awsTransport{log: requests, failIAM: "ListAccessKeys"}}, objects...)
	var current geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(store), &current))
	require.NoError(t, c.Delete(ctx, &current))

	_, err := r.Reconcile(ctx, reconcileRequest(store))

	require.Error(t, err)
	require.False(t, storeIsGone(t, c, store), "the finalizer stays until cleanup succeeds")
}

// With nothing pending the store is its own deployed spec, so it is not copied on every pass.
func TestDeployedObjectStoreCopiesOnlyWhenASnapshotReplacesTheSpec(t *testing.T) {
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, deployTestBucket))
	require.Same(t, store, deployedObjectStore(store))

	store.Spec.Buckets = []string{"renamed"}
	platform.MarkPendingChange(&store.ObjectMeta, platform.PendingChangeSettings)
	deployed := deployedObjectStore(store)
	require.NotSame(t, store, deployed)
	require.Equal(t, []string{deployTestBucket}, deployed.Spec.Buckets)
	require.Equal(t, []string{"renamed"}, store.Spec.Buckets, "the store itself is untouched")
}
