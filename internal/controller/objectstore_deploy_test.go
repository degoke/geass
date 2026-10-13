package controller

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	deployTestProject = "payments"
	deployTestBucket  = "uploads"
	deployTestStore   = "assets"
	// deployTestServerBucket is what deployTestBucket is called on the server: project and environment first.
	deployTestServerBucket = "payments.dev.uploads"
)

// objectStoreEngines are the servers a cluster's object storage can run on. A test of how a bucket is made
// runs against both, because they are driven through different APIs and must behave the same.
var objectStoreEngines = []geassv1alpha1.GeassObjectStoreEngine{
	geassv1alpha1.ObjectStoreEngineSeaweedFS,
	geassv1alpha1.ObjectStoreEngineGarage,
}

// forEachEngine runs a test once per server, each with a fresh fake of that server.
func forEachEngine(t *testing.T, test func(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, server *fakeObjectStore)) {
	t.Helper()
	for _, engine := range objectStoreEngines {
		t.Run(string(engine), func(t *testing.T) {
			test(t, engine, newFakeObjectStore(t, engine == geassv1alpha1.ObjectStoreEngineGarage))
		})
	}
}

// requireBucketCreated says the bucket was made on the fake server, in the way that server is asked to.
func requireBucketCreated(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, server *fakeObjectStore, bucket string) {
	t.Helper()
	if engine == geassv1alpha1.ObjectStoreEngineGarage {
		require.True(t, server.hasGarageBucket(bucket), "the bucket is made through the admin API")
		return
	}
	require.Contains(t, server.log.all(), "PUT /"+bucket, "the bucket is created on the server")
}

// requireBucketUserCreated says the bucket's login was made on the fake server.
func requireBucketUserCreated(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, server *fakeObjectStore, store string) {
	t.Helper()
	if engine == geassv1alpha1.ObjectStoreEngineGarage {
		require.True(t, server.hasGarageKey("geass-"+store))
		return
	}
	require.True(t, server.hasIAMUser("geass-"+store))
}

// awsTransport answers the S3 and IAM calls the AWS bucket path makes, whatever host they were sent to. failIAM
// names an IAM action that answers with a server error.
type awsTransport struct {
	log     *requestLog
	failIAM string
}

func (a awsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	respond := func(status int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}, nil
	}
	if req.URL.Host != "iam.amazonaws.com" {
		a.log.add("S3 " + req.Method + " " + req.URL.Host)
		if req.Method == http.MethodGet {
			return respond(http.StatusOK, `<ListBucketResult></ListBucketResult>`)
		}
		return respond(http.StatusOK, "")
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	form, err := url.ParseQuery(string(raw))
	if err != nil {
		return nil, err
	}
	action := form.Get("Action")
	a.log.add("IAM " + action)
	switch action {
	case a.failIAM:
		return respond(http.StatusInternalServerError, `<ErrorResponse><Error><Code>ServiceFailure</Code><Message>try later</Message></Error></ErrorResponse>`)
	case "CreateAccessKey":
		return respond(http.StatusOK, `<CreateAccessKeyResponse><CreateAccessKeyResult><AccessKey><AccessKeyId>AKIATEST</AccessKeyId><SecretAccessKey>iam-secret</SecretAccessKey></AccessKey></CreateAccessKeyResult></CreateAccessKeyResponse>`)
	case "ListAccessKeys":
		return respond(http.StatusOK, `<ListAccessKeysResponse><ListAccessKeysResult></ListAccessKeysResult></ListAccessKeysResponse>`)
	}
	return respond(http.StatusOK, "<Response/>")
}

func objectStoreDeployScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, storagev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	return scheme
}

func newObjectStoreReconciler(t *testing.T, httpClient *http.Client, objects ...client.Object) (*GeassObjectStoreReconciler, client.Client) {
	t.Helper()
	scheme := objectStoreDeployScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassObjectStore{}, &geassv1alpha1.GeassProject{}, &geassv1alpha1.GeassCluster{}, &appsv1.Deployment{}).
		Build()
	return &GeassObjectStoreReconciler{Client: c, Scheme: scheme, HTTP: httpClient}, c
}

// deployedProjectBucket is a project bucket as the dashboard's Deploy button leaves it: created as a draft,
// then switched on with its spec snapshotted and its pending changes cleared. Nothing has been provisioned
// for it yet, so status.connectionSecret is empty.
func deployedProjectBucket(t *testing.T, spec geassv1alpha1.GeassObjectStoreSpec) *geassv1alpha1.GeassObjectStore {
	t.Helper()
	return deployedBucketIn(t, deployTestStore, deployTestProject, spec)
}

func deployedBucketIn(t *testing.T, name, project string, spec geassv1alpha1.GeassObjectStoreSpec) *geassv1alpha1.GeassObjectStore {
	t.Helper()
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Finalizers: []string{objectStoreFinalizer}},
		Spec:       spec,
	}
	store.Spec.Project = project
	store.Spec.Environment = geassv1alpha1.EnvironmentDev
	platform.InitializePendingCreated(&store.ObjectMeta)
	store.Spec.Deploy.Enabled = true
	platform.ClearPendingChanges(&store.ObjectMeta)
	require.NoError(t, platform.SetLastDeployedJSON(&store.ObjectMeta, store.Spec))
	return store
}

// clusterBucketSpec is the spec of an in-cluster bucket resource on a server of this engine.
func clusterBucketSpec(engine geassv1alpha1.GeassObjectStoreEngine, buckets ...string) geassv1alpha1.GeassObjectStoreSpec {
	return geassv1alpha1.GeassObjectStoreSpec{
		Engine:    engine,
		Placement: geassv1alpha1.ObjectStorePlacementInCluster,
		Buckets:   buckets,
	}
}

// runningClusterServer is a ready cluster object storage server: its resource, with the in-cluster address of
// its S3 API, and its root login. The address is never reached; the fake answers whatever the reconciler asks.
func runningClusterServer(engine geassv1alpha1.GeassObjectStoreEngine) []client.Object {
	server := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Engine:    engine,
			Placement: geassv1alpha1.ObjectStorePlacementInCluster,
		},
		Status: geassv1alpha1.GeassObjectStoreStatus{
			Endpoint:   platform.ObjectStoreServerEndpoint(engine),
			Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue, Reason: "ObjectStoreReady", Message: "ready"}},
		},
	}
	root := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: objectStoreRootSecretName(server.Name), Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			rootKeyAccess: []byte("root-access"), rootKeySecret: []byte("root-secret"),
			rootKeyAdminToken: []byte("admin-token"), rootKeyRPCSecret: []byte("rpc-secret"), rootKeyMetricsToken: []byte("metrics-token"),
		},
	}
	return []client.Object{server, root}
}

func readyStoreStatus(t *testing.T, c client.Client) geassv1alpha1.GeassObjectStoreStatus {
	t.Helper()
	var got geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: deployTestStore, Namespace: platform.SystemNamespace}, &got))
	return got.Status
}

func projectNamespaceName(t *testing.T) string {
	t.Helper()
	ns, err := platform.ProjectNamespace(deployTestProject, "dev")
	require.NoError(t, err)
	return ns
}

// A bucket the dashboard just deployed has no connection secret yet, because the connection secret is what a
// successful first provision writes. It must still be provisioned.
func TestDeployedProjectBucketIsProvisioned(t *testing.T) {
	forEachEngine(t, func(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, server *fakeObjectStore) {
		ctx := context.Background()
		project, cluster := readyProjectCluster(deployTestProject, "dev")
		store := deployedProjectBucket(t, clusterBucketSpec(engine, deployTestBucket))
		objects := append([]client.Object{project, cluster, store}, runningClusterServer(engine)...)
		r, c := newObjectStoreReconciler(t, server.client(), objects...)

		_, err := r.Reconcile(ctx, reconcileRequest(store))
		require.NoError(t, err)

		status := readyStoreStatus(t, c)
		require.NotContains(t, readyConditionMessage(status.Conditions), "waiting for deployment")
		require.True(t, platform.IsConditionTrue(status.Conditions, platform.ConditionReady), readyConditionMessage(status.Conditions))
		require.Equal(t, deployTestStore+"-connection", status.ConnectionSecret)
		require.Equal(t, projectNamespaceName(t), status.TargetNamespace)
		requireBucketCreated(t, engine, server, deployTestServerBucket)
		requireBucketUserCreated(t, engine, server, deployTestStore)

		var connection corev1.Secret
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: deployTestStore + "-connection", Namespace: projectNamespaceName(t)}, &connection))
		require.Equal(t, deployTestServerBucket, secretValue(&connection, platform.ConnectionKeyBucket), "apps read the stored name from the Secret")
		require.Equal(t, platform.ObjectStoreServerEndpoint(engine), secretValue(&connection, platform.ConnectionKeyEndpoint))
		require.NotEmpty(t, secretValue(&connection, platform.ConnectionKeyAccessKey))
		require.NotEmpty(t, secretValue(&connection, platform.ConnectionKeySecretKey))
		require.NotEqual(t, "root-access", secretValue(&connection, platform.ConnectionKeyAccessKey), "an app is never given the server's own login")
		require.NotEqual(t, "root-secret", secretValue(&connection, platform.ConnectionKeySecretKey))
	})
}

// Every pass makes the bucket and its login again, and must change nothing: the keys an app already uses stay.
func TestProvisioningAgainKeepsTheKeysAnAppUses(t *testing.T) {
	forEachEngine(t, func(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, server *fakeObjectStore) {
		ctx := context.Background()
		project, cluster := readyProjectCluster(deployTestProject, "dev")
		store := deployedProjectBucket(t, clusterBucketSpec(engine, deployTestBucket))
		objects := append([]client.Object{project, cluster, store}, runningClusterServer(engine)...)
		r, c := newObjectStoreReconciler(t, server.client(), objects...)
		connectionKey := client.ObjectKey{Name: deployTestStore + "-connection", Namespace: projectNamespaceName(t)}

		_, err := r.Reconcile(ctx, reconcileRequest(store))
		require.NoError(t, err)
		var first corev1.Secret
		require.NoError(t, c.Get(ctx, connectionKey, &first))
		_, err = r.Reconcile(ctx, reconcileRequest(store))
		require.NoError(t, err)
		var second corev1.Secret
		require.NoError(t, c.Get(ctx, connectionKey, &second))

		require.Equal(t, first.Data, second.Data)
		creations := 0
		for _, request := range server.log.all() {
			if request == "IAM CreateAccessKey geass-"+deployTestStore || request == "ADMIN CreateKey" {
				creations++
			}
		}
		require.Equal(t, 1, creations, "one login was made, not one per pass")
	})
}

// A server that was reset has forgotten the logins it made. The next pass makes a new one and updates the Secret,
// where it would otherwise leave an app with keys that no longer work.
func TestAServerThatForgotALoginGetsANewKey(t *testing.T) {
	forEachEngine(t, func(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, server *fakeObjectStore) {
		ctx := context.Background()
		project, cluster := readyProjectCluster(deployTestProject, "dev")
		store := deployedProjectBucket(t, clusterBucketSpec(engine, deployTestBucket))
		objects := append([]client.Object{project, cluster, store}, runningClusterServer(engine)...)
		r, c := newObjectStoreReconciler(t, server.client(), objects...)
		connectionKey := client.ObjectKey{Name: deployTestStore + "-connection", Namespace: projectNamespaceName(t)}
		_, err := r.Reconcile(ctx, reconcileRequest(store))
		require.NoError(t, err)
		var before corev1.Secret
		require.NoError(t, c.Get(ctx, connectionKey, &before))

		server.forgetLogins()
		_, err = r.Reconcile(ctx, reconcileRequest(store))
		require.NoError(t, err)

		var after corev1.Secret
		require.NoError(t, c.Get(ctx, connectionKey, &after))
		require.NotEqual(t, secretValue(&before, platform.ConnectionKeyAccessKey), secretValue(&after, platform.ConnectionKeyAccessKey))
		require.NotEmpty(t, secretValue(&after, platform.ConnectionKeySecretKey))
		require.True(t, platform.IsConditionTrue(readyStoreStatus(t, c).Conditions, platform.ConditionReady))
	})
}

// A bucket's login may reach that bucket and no other: this is what keeps one project out of another's data.
func TestAProjectBucketsLoginIsLimitedToItsOwnBucket(t *testing.T) {
	forEachEngine(t, func(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, server *fakeObjectStore) {
		ctx := context.Background()
		project, cluster := readyProjectCluster(deployTestProject, "dev")
		payments := deployedBucketIn(t, "assets", deployTestProject, clusterBucketSpec(engine, deployTestBucket))
		billing := deployedBucketIn(t, "files", "billing", clusterBucketSpec(engine, deployTestBucket))
		objects := append([]client.Object{project, otherProject("billing"), cluster, payments, billing}, runningClusterServer(engine)...)
		r, _ := newObjectStoreReconciler(t, server.client(), objects...)

		for _, store := range []*geassv1alpha1.GeassObjectStore{payments, billing} {
			_, err := r.Reconcile(ctx, reconcileRequest(store))
			require.NoError(t, err)
		}

		if engine == geassv1alpha1.ObjectStoreEngineGarage {
			require.Equal(t, []string{"payments.dev.uploads"}, server.grantsOf("geass-assets"))
			require.Equal(t, []string{"billing.dev.uploads"}, server.grantsOf("geass-files"))
			return
		}
		require.Contains(t, server.policyFor("geass-assets"), "arn:aws:s3:::payments.dev.uploads")
		require.NotContains(t, server.policyFor("geass-assets"), "billing")
		require.Contains(t, server.policyFor("geass-files"), "arn:aws:s3:::billing.dev.uploads")
		require.NotContains(t, server.policyFor("geass-files"), "payments")
	})
}

// SeaweedFS does not know every AWS action name, and refuses a policy that names one it does not.
func TestSeaweedFSBucketPoliciesUseOnlyActionsItKnows(t *testing.T) {
	ctx := context.Background()
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, deployTestBucket))
	objects := append([]client.Object{project, cluster, store}, runningClusterServer(geassv1alpha1.ObjectStoreEngineSeaweedFS)...)
	r, _ := newObjectStoreReconciler(t, server.client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	policy := server.policyFor("geass-" + deployTestStore)
	for _, unknown := range []string{"ListBucketMultipartUploads", "ListMultipartUploadParts"} {
		require.NotContains(t, policy, unknown)
	}
	for _, needed := range []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:ListBucket", "s3:AbortMultipartUpload"} {
		require.Contains(t, policy, needed)
	}
}

// Garage has no version listing. Making and removing buckets must carry on without it.
func TestGarageBucketsAreRemovedAlthoughItCannotListVersions(t *testing.T) {
	ctx := context.Background()
	server := newFakeObjectStore(t, true)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineGarage, deployTestBucket))
	objects := append([]client.Object{project, cluster, store}, runningClusterServer(geassv1alpha1.ObjectStoreEngineGarage)...)
	r, c := newObjectStoreReconciler(t, server.client(), objects...)
	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	deleteStoreAndReconcile(t, r, c, store)

	require.True(t, storeIsGone(t, c, store))
	require.False(t, server.hasGarageBucket(deployTestServerBucket))
	require.False(t, server.hasGarageKey("geass-"+deployTestStore))
}

func TestDeployedAWSProjectBucketIsProvisioned(t *testing.T) {
	ctx := context.Background()
	requests := &requestLog{}
	store, objects := awsBucketFixtures(t)
	r, c := newObjectStoreReconciler(t, &http.Client{Transport: awsTransport{log: requests}}, objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	status := readyStoreStatus(t, c)
	require.NotContains(t, readyConditionMessage(status.Conditions), "waiting for deployment")
	require.True(t, platform.IsConditionTrue(status.Conditions, platform.ConditionReady), readyConditionMessage(status.Conditions))
	require.Equal(t, deployTestStore+"-connection", status.ConnectionSecret)
	require.Equal(t, "https://s3.eu-north-1.amazonaws.com", status.Endpoint)
	require.Contains(t, requests.all(), "S3 PUT acme-uploads.s3.eu-north-1.amazonaws.com", "the bucket is created in the AWS account")
	require.Contains(t, requests.all(), "IAM CreateUser")
	require.Contains(t, requests.all(), "IAM CreateAccessKey")

	var secret corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: deployTestStore + "-connection", Namespace: projectNamespaceName(t)}, &secret))
	require.Equal(t, "acme-uploads", secretValue(&secret, platform.ConnectionKeyBucket))
	require.Equal(t, "AKIATEST", secretValue(&secret, platform.ConnectionKeyAccessKey))
}

// A draft is still a draft: nothing is created until the user deploys it.
func TestDraftProjectBucketIsNotProvisionedUntilDeployed(t *testing.T) {
	ctx := context.Background()
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	draft := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, deployTestBucket))
	draft.Spec.Deploy.Enabled = false
	platform.InitializePendingCreated(&draft.ObjectMeta)
	objects := append([]client.Object{project, cluster, draft}, runningClusterServer(geassv1alpha1.ObjectStoreEngineSeaweedFS)...)
	r, c := newObjectStoreReconciler(t, server.client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(draft))
	require.NoError(t, err)

	status := readyStoreStatus(t, c)
	require.False(t, platform.IsConditionTrue(status.Conditions, platform.ConditionReady))
	require.Equal(t, waitingForDeployMessage, readyConditionMessage(status.Conditions))
	require.Empty(t, status.ConnectionSecret)
	require.Empty(t, server.log.all(), "no bucket is created for a draft")
}

// A bucket that was provisioned and then edited keeps running what was last deployed: the edit is pending and
// only reaches the server once it is deployed.
func TestProvisionedProjectBucketWithPendingEditsRunsItsDeployedSpec(t *testing.T) {
	ctx := context.Background()
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, deployTestBucket))
	store.Status.ConnectionSecret = deployTestStore + "-connection"
	store.Spec.Buckets = []string{"renamed"}
	platform.MarkPendingChange(&store.ObjectMeta, platform.PendingChangeSettings)
	objects := append([]client.Object{project, cluster, store}, runningClusterServer(geassv1alpha1.ObjectStoreEngineSeaweedFS)...)
	r, c := newObjectStoreReconciler(t, server.client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	require.Contains(t, server.log.all(), "PUT /"+deployTestServerBucket)
	require.NotContains(t, server.log.all(), "PUT /payments.dev.renamed")
	status := readyStoreStatus(t, c)
	require.True(t, platform.IsConditionTrue(status.Conditions, platform.ConditionReady), readyConditionMessage(status.Conditions))
}

// An edited store that never provisioned and has no snapshot cannot say what to run.
func TestPendingEditsWithoutASnapshotAreReported(t *testing.T) {
	ctx := context.Background()
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, deployTestBucket))
	delete(store.Annotations, platform.LastDeployedAnnotation)
	platform.MarkPendingChange(&store.ObjectMeta, platform.PendingChangeSettings)
	objects := append([]client.Object{project, cluster, store}, runningClusterServer(geassv1alpha1.ObjectStoreEngineSeaweedFS)...)
	r, c := newObjectStoreReconciler(t, server.client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	status := readyStoreStatus(t, c)
	require.Contains(t, readyConditionMessage(status.Conditions), "no last deployed snapshot")
	require.Empty(t, server.log.all())
}

// Stores that predate the draft/deploy workflow carry no annotation. They never waited for a deploy, so a
// legacy store that has not provisioned yet must go ahead as well.
func TestLegacyProjectBucketWithoutConnectionSecretIsProvisioned(t *testing.T) {
	ctx := context.Background()
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: deployTestStore, Namespace: platform.SystemNamespace, Finalizers: []string{objectStoreFinalizer}},
		Spec:       clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, deployTestBucket),
	}
	store.Spec.Project = deployTestProject
	store.Spec.Environment = geassv1alpha1.EnvironmentDev
	objects := append([]client.Object{project, cluster, store}, runningClusterServer(geassv1alpha1.ObjectStoreEngineSeaweedFS)...)
	r, c := newObjectStoreReconciler(t, server.client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	status := readyStoreStatus(t, c)
	require.True(t, platform.IsConditionTrue(status.Conditions, platform.ConditionReady), readyConditionMessage(status.Conditions))
	require.Contains(t, server.log.all(), "PUT /"+deployTestServerBucket)
}

// With no object storage server a bucket waits, and says what to do.
func TestProjectBucketWaitsForClusterObjectStorage(t *testing.T) {
	ctx := context.Background()
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, deployTestBucket))
	r, c := newObjectStoreReconciler(t, server.client(), project, cluster, store)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	require.Equal(t, platform.MessageSetUpObjectStorage, readyConditionMessage(readyStoreStatus(t, c).Conditions))
	require.Empty(t, server.log.all())
}

// A server that has not come up yet is not an error: the bucket says the server is starting.
func TestProjectBucketWaitsForTheServerToStart(t *testing.T) {
	ctx := context.Background()
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, deployTestBucket))
	objects := append([]client.Object{project, cluster, store}, runningClusterServer(geassv1alpha1.ObjectStoreEngineSeaweedFS)...)
	objects[len(objects)-2].(*geassv1alpha1.GeassObjectStore).Status.Conditions = nil
	r, c := newObjectStoreReconciler(t, server.client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	require.Equal(t, platform.MessageObjectStorageStarting, readyConditionMessage(readyStoreStatus(t, c).Conditions))
	require.Empty(t, server.log.all())
}

// A project that requires node tags must not have a bucket made on a server that may run elsewhere. If the project
// cannot be read, that cannot be known, so the bucket waits instead of being made.
func TestProjectBucketWaitsWhenItsProjectCannotBeRead(t *testing.T) {
	ctx := context.Background()
	server := newFakeObjectStore(t, false)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, clusterBucketSpec(geassv1alpha1.ObjectStoreEngineSeaweedFS, deployTestBucket))
	objects := append([]client.Object{project, cluster, store}, runningClusterServer(geassv1alpha1.ObjectStoreEngineSeaweedFS)...)
	c := fake.NewClientBuilder().WithScheme(objectStoreDeployScheme(t)).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassObjectStore{}).
		WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, isProject := obj.(*geassv1alpha1.GeassProject); isProject {
				return errors.New("the API server is busy")
			}
			return c.Get(ctx, key, obj, opts...)
		}}).Build()
	r := &GeassObjectStoreReconciler{Client: c, Scheme: objectStoreDeployScheme(t), HTTP: server.client()}

	_, err := r.reconcileProjectBucket(ctx, store, projectNamespaceName(t))
	require.NoError(t, err)

	require.Contains(t, readyConditionMessage(readyStoreStatus(t, c).Conditions), "busy")
	require.Empty(t, server.log.all(), "nothing was made on the server")
}
