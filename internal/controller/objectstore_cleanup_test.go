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
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

// otherProject is a second ready project that shares the first one's cluster.
func otherProject(name string) *geassv1alpha1.GeassProject {
	project, _ := readyProjectCluster(name, "dev")
	return project
}

// Every project's buckets live on one MinIO server where names are global. Two projects that both ask for
// "uploads" must get two buckets, each reachable only with its own project's user.
func TestProjectsWithTheSameBucketNameGetDifferentBuckets(t *testing.T) {
	ctx := context.Background()
	s3, requests := minioStub(t)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	payments := deployedBucketIn(t, "assets", deployTestProject, minioBucketSpec(deployTestBucket))
	billing := deployedBucketIn(t, "files", "billing", minioBucketSpec(deployTestBucket))
	objects := append([]client.Object{project, otherProject("billing"), cluster, payments, billing}, readyClusterMinIOFixtures(s3.URL)...)
	r, c := newObjectStoreReconciler(t, s3.Client(), objects...)

	for _, store := range []*geassv1alpha1.GeassObjectStore{payments, billing} {
		_, err := r.Reconcile(ctx, reconcileRequest(store))
		require.NoError(t, err)
	}

	require.Contains(t, requests.all(), "PUT /payments.dev.uploads")
	require.Contains(t, requests.all(), "PUT /billing.dev.uploads")
	require.NotContains(t, requests.all(), "PUT /uploads", "no bucket is created under the bare name")

	for store, want := range map[*geassv1alpha1.GeassObjectStore]string{payments: "payments.dev.uploads", billing: "billing.dev.uploads"} {
		var connection corev1.Secret
		ns, err := platform.ProjectNamespace(store.Spec.Project, "dev")
		require.NoError(t, err)
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: store.Name + "-connection", Namespace: ns}, &connection))
		require.Equal(t, want, secretValue(&connection, platform.ConnectionKeyBucket))
	}

	// The second store's reconcile renders both users into the chart, each allowed only its own bucket.
	var chart helmv1.HelmChart
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ClusterMinIOChartName, Namespace: platform.HelmChartNamespace}, &chart))
	require.Contains(t, chart.Spec.ValuesContent, "arn:aws:s3:::payments.dev.uploads")
	require.Contains(t, chart.Spec.ValuesContent, "arn:aws:s3:::billing.dev.uploads")
	require.NotContains(t, chart.Spec.ValuesContent, "arn:aws:s3:::uploads")
}

// A name that fits only without the project prefix is refused with what the limit is, not left to fail on
// the MinIO server.
func TestMinIOBucketNameTooLongForItsProjectIsRefused(t *testing.T) {
	ctx := context.Background()
	s3, requests := minioStub(t)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, minioBucketSpec("a-very-long-bucket-name-that-fits-on-its-own-but-not-here"))
	objects := append([]client.Object{project, cluster, store}, readyClusterMinIOFixtures(s3.URL)...)
	r, c := newObjectStoreReconciler(t, s3.Client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	status := readyStoreStatus(t, c)
	require.Contains(t, readyConditionMessage(status.Conditions), "too long")
	require.Empty(t, requests.all(), "nothing is created on the server")
}

// startedProvisioning reconciles a deployed MinIO bucket once, while the MinIO chart has not been installed yet,
// so its bucket and user exist but it is not ready.
func startedProvisioning(t *testing.T) (*GeassObjectStoreReconciler, client.Client, *geassv1alpha1.GeassObjectStore, *requestLog) {
	t.Helper()
	s3, requests := minioStub(t)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, minioBucketSpec(deployTestBucket))
	objects := append([]client.Object{project, cluster, store}, runningClusterMinIO(s3.URL)...)
	r, c := newObjectStoreReconciler(t, s3.Client(), objects...)

	_, err := r.Reconcile(context.Background(), reconcileRequest(store))
	require.NoError(t, err)
	status := readyStoreStatus(t, c)
	require.Empty(t, status.ConnectionSecret, "the store never became ready")
	require.Contains(t, requests.all(), "PUT /"+deployTestMinIOBucket)
	return r, c, store, requests
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
// on the MinIO server, its user and its Secrets, and the user's entry in the chart values.
func TestDeletingAMinIOBucketThatNeverBecameReadyRemovesWhatItCreated(t *testing.T) {
	ctx := context.Background()
	r, c, store, requests := startedProvisioning(t)

	var started geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(store), &started))
	require.Equal(t, "true", started.Annotations[platform.AnnotationProvisioningStarted], "the store records that it began creating things")

	deleteStoreAndReconcile(t, r, c, store)

	require.Contains(t, requests.all(), "DELETE /"+deployTestMinIOBucket, "the bucket is removed from the MinIO server")
	require.True(t, storeIsGone(t, c, store))
	for _, key := range []client.ObjectKey{
		{Name: minioUserSecretName(deployTestStore), Namespace: platform.SystemNamespace},
		{Name: deployTestStore + "-connection", Namespace: projectNamespaceName(t)},
	} {
		require.True(t, apierrors.IsNotFound(c.Get(ctx, key, &corev1.Secret{})), "%s is removed", key)
	}
	var chart helmv1.HelmChart
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ClusterMinIOChartName, Namespace: platform.HelmChartNamespace}, &chart))
	require.NotContains(t, chart.Spec.ValuesContent, "geass-"+deployTestStore, "the deleted bucket's user and policy leave the chart values")
}

// A deployed bucket that stopped before it created anything, here because MinIO is not set up, has nothing to
// clean up, so deleting it must not wait on MinIO.
func TestDeletingADeployedBucketThatCreatedNothingDoesNotTouchMinIO(t *testing.T) {
	ctx := context.Background()
	s3, requests := minioStub(t)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, minioBucketSpec(deployTestBucket))
	r, c := newObjectStoreReconciler(t, s3.Client(), project, cluster, store)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)
	status := readyStoreStatus(t, c)
	require.Contains(t, readyConditionMessage(status.Conditions), "MinIO server")

	deleteStoreAndReconcile(t, r, c, store)

	require.True(t, storeIsGone(t, c, store))
	require.Empty(t, requests.all())
}

// A bucket renamed after it was deployed is a pending change. What exists on the server is still the deployed
// name, so that is the bucket to remove, and the one its user must stay allowed to use meanwhile.
func TestCleanupUsesTheDeployedBucketNotAPendingRename(t *testing.T) {
	ctx := context.Background()
	s3, requests := minioStub(t)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, minioBucketSpec(deployTestBucket))
	store.Status.ConnectionSecret = deployTestStore + "-connection"
	store.Spec.Buckets = []string{"renamed"}
	platform.MarkPendingChange(&store.ObjectMeta, platform.PendingChangeSettings)
	userSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: minioUserSecretName(deployTestStore), Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{platform.ConnectionKeyAccessKey: []byte("assets"), platform.ConnectionKeySecretKey: []byte("secret")},
	}
	objects := append([]client.Object{project, cluster, store, userSecret}, readyClusterMinIOFixtures(s3.URL)...)
	r, c := newObjectStoreReconciler(t, s3.Client(), objects...)

	var server geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ClusterMinIOName, Namespace: platform.SystemNamespace}, &server))
	values, err := r.clusterMinIOValues(ctx, &server)
	require.NoError(t, err)
	require.Contains(t, values, "arn:aws:s3:::"+deployTestMinIOBucket, "the user keeps the bucket that exists")
	require.NotContains(t, values, "payments.dev.renamed")

	deleteStoreAndReconcile(t, r, c, store)

	require.Contains(t, requests.all(), "DELETE /"+deployTestMinIOBucket)
	require.NotContains(t, requests.all(), "DELETE /payments.dev.renamed")
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

func TestDeletingAMinIOBucketThatNeverBecameReadyIsNotHeldByAnUnreachableServer(t *testing.T) {
	ctx := context.Background()
	r, c, store, _ := startedProvisioning(t)
	var server geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ClusterMinIOName, Namespace: platform.SystemNamespace}, &server))
	server.Status.Endpoint = "http://127.0.0.1:1"
	require.NoError(t, c.Status().Update(ctx, &server))

	deleteStoreAndReconcile(t, r, c, store)

	require.True(t, storeIsGone(t, c, store))
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

// One store whose bucket name can no longer be stored is reported by its own reconcile. It must not stop the
// chart values being rendered, which would stop every other bucket, and the MinIO server, from updating.
func TestAStoreWithAnUnstorableBucketNameDoesNotBreakTheChartValues(t *testing.T) {
	ctx := context.Background()
	s3, _ := minioStub(t)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	good := deployedBucketIn(t, "files", deployTestProject, minioBucketSpec(deployTestBucket))
	bad := deployedBucketIn(t, "big", deployTestProject, minioBucketSpec(strings.Repeat("a", 60)))
	minio := readyClusterMinIOFixtures(s3.URL)
	objects := make([]client.Object, 0, 6+len(minio))
	for _, store := range []*geassv1alpha1.GeassObjectStore{good, bad} {
		objects = append(objects, store, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: minioUserSecretName(store.Name), Namespace: platform.SystemNamespace},
			Data:       map[string][]byte{platform.ConnectionKeyAccessKey: []byte(store.Name), platform.ConnectionKeySecretKey: []byte("secret")},
		})
	}
	objects = append(objects, project, cluster)
	objects = append(objects, minio...)
	r, c := newObjectStoreReconciler(t, s3.Client(), objects...)
	var server geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ClusterMinIOName, Namespace: platform.SystemNamespace}, &server))

	values, err := r.clusterMinIOValues(ctx, &server)

	require.NoError(t, err)
	require.Contains(t, values, "arn:aws:s3:::payments.dev.uploads")
	require.NotContains(t, values, "geass-big")
}

// Deleting never falls back to a bucket that merely shares the resource's name: such a bucket was not created
// by this store and may hold another store's data.
func TestDeletingNeverRemovesABucketNamedAfterTheResource(t *testing.T) {
	s3, requests := minioStub(t)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, minioBucketSpec(strings.Repeat("a", 60)))
	store.Status.ConnectionSecret = deployTestStore + "-connection"
	objects := append([]client.Object{project, cluster, store}, readyClusterMinIOFixtures(s3.URL)...)
	r, c := newObjectStoreReconciler(t, s3.Client(), objects...)

	deleteStoreAndReconcile(t, r, c, store)

	require.True(t, storeIsGone(t, c, store))
	require.Empty(t, requests.all(), "no bucket is removed when none can have been created")

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

// With nothing pending the store is its own deployed spec, so it is not copied for every chart render.
func TestDeployedObjectStoreCopiesOnlyWhenASnapshotReplacesTheSpec(t *testing.T) {
	store := deployedProjectBucket(t, minioBucketSpec(deployTestBucket))
	require.Same(t, store, deployedObjectStore(store))

	store.Spec.Buckets = []string{"renamed"}
	platform.MarkPendingChange(&store.ObjectMeta, platform.PendingChangeSettings)
	deployed := deployedObjectStore(store)
	require.NotSame(t, store, deployed)
	require.Equal(t, []string{deployTestBucket}, deployed.Spec.Buckets)
	require.Equal(t, []string{"renamed"}, store.Spec.Buckets, "the store itself is untouched")
}
