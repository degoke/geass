package controller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	deployTestProject = "payments"
	deployTestBucket  = "uploads"
	deployTestStore   = "assets"
	// deployTestMinIOBucket is what deployTestBucket is called on the MinIO server: project and environment first.
	deployTestMinIOBucket = "payments.dev.uploads"
)

// requestLog records the requests a stubbed S3 or IAM endpoint received, safe for a server goroutine.
type requestLog struct {
	mu    sync.Mutex
	items []string
}

func (l *requestLog) add(entry string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, entry)
}

func (l *requestLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.items...)
}

// minioStub stands in for the cluster MinIO server's S3 API.
func minioStub(t *testing.T) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add(r.Method + " " + r.URL.Path)
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`<ListBucketResult></ListBucketResult>`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log
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
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, helmv1.AddToScheme(scheme))
	return scheme
}

func newObjectStoreReconciler(t *testing.T, httpClient *http.Client, objects ...client.Object) (*GeassObjectStoreReconciler, client.Client) {
	t.Helper()
	scheme := objectStoreDeployScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassObjectStore{}, &geassv1alpha1.GeassProject{}, &geassv1alpha1.GeassCluster{}, &helmv1.HelmChart{}, &corev1.Pod{}, &batchv1.Job{}).
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

// minioBucketSpec is the spec of an in-cluster bucket resource.
func minioBucketSpec(buckets ...string) geassv1alpha1.GeassObjectStoreSpec {
	return geassv1alpha1.GeassObjectStoreSpec{
		Engine:    geassv1alpha1.ObjectStoreEngineMinIO,
		Placement: geassv1alpha1.ObjectStorePlacementInCluster,
		Buckets:   buckets,
	}
}

// readyClusterMinIOFixtures is a running cluster MinIO server: its resource, root credentials, an
// installed HelmChart and a ready pod, so a project bucket can finish in one reconcile.
func readyClusterMinIOFixtures(endpoint string) []client.Object {
	return append(runningClusterMinIO(endpoint), installedMinIOChart()...)
}

// runningClusterMinIO is the MinIO server's resource, its root credentials and a ready pod.
func runningClusterMinIO(endpoint string) []client.Object {
	server := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterMinIOName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Engine:    geassv1alpha1.ObjectStoreEngineMinIO,
			Placement: geassv1alpha1.ObjectStorePlacementInCluster,
		},
		Status: geassv1alpha1.GeassObjectStoreStatus{
			Endpoint:   endpoint,
			Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue, Reason: "ObjectStoreReady", Message: "ready"}},
		},
	}
	root := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: minioRootSecretName(server.Name), Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"rootUser": []byte("root"), "rootPassword": []byte("root-password")},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: platform.ClusterMinIOChartName + "-0", Namespace: platform.SystemNamespace,
			Labels: map[string]string{"app.kubernetes.io/instance": platform.ClusterMinIOChartName},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
	return []client.Object{server, root, pod}
}

const minioInstallJob = "helm-install-geass-minio-1"

// installedMinIOChart is the MinIO HelmChart after K3s ran its install job to completion.
func installedMinIOChart() []client.Object {
	chart := &helmv1.HelmChart{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterMinIOChartName, Namespace: platform.HelmChartNamespace},
		Status:     helmv1.HelmChartStatus{JobName: minioInstallJob},
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: minioInstallJob, Namespace: platform.HelmChartNamespace},
		Status:     batchv1.JobStatus{Succeeded: 1},
	}
	return []client.Object{chart, job}
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
func TestDeployedMinIOProjectBucketIsProvisioned(t *testing.T) {
	ctx := context.Background()
	s3, requests := minioStub(t)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, minioBucketSpec(deployTestBucket))
	objects := append([]client.Object{project, cluster, store}, readyClusterMinIOFixtures(s3.URL)...)
	r, c := newObjectStoreReconciler(t, s3.Client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	status := readyStoreStatus(t, c)
	require.NotContains(t, readyConditionMessage(status.Conditions), "waiting for deployment")
	require.True(t, platform.IsConditionTrue(status.Conditions, platform.ConditionReady), readyConditionMessage(status.Conditions))
	require.Equal(t, deployTestStore+"-connection", status.ConnectionSecret)
	require.Equal(t, projectNamespaceName(t), status.TargetNamespace)
	require.Contains(t, requests.all(), "PUT /"+deployTestMinIOBucket, "the bucket is created on the MinIO server")

	var connection corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: deployTestStore + "-connection", Namespace: projectNamespaceName(t)}, &connection))
	require.Equal(t, deployTestMinIOBucket, secretValue(&connection, platform.ConnectionKeyBucket), "apps read the stored name from the Secret")
	require.Equal(t, s3.URL, secretValue(&connection, platform.ConnectionKeyEndpoint))
	require.NotEmpty(t, secretValue(&connection, platform.ConnectionKeyAccessKey))
	require.NotEmpty(t, secretValue(&connection, platform.ConnectionKeySecretKey))
}

// Provisioning spans several reconciles: the bucket's user goes into the MinIO chart, and the bucket is ready
// only once that chart has been reinstalled. Every step on the way is progress, never "waiting for deployment".
func TestDeployedMinIOProjectBucketProgressesWhileTheChartInstalls(t *testing.T) {
	ctx := context.Background()
	s3, requests := minioStub(t)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, minioBucketSpec(deployTestBucket))
	objects := append([]client.Object{project, cluster, store}, runningClusterMinIO(s3.URL)...)
	r, c := newObjectStoreReconciler(t, s3.Client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	status := readyStoreStatus(t, c)
	require.False(t, platform.IsConditionTrue(status.Conditions, platform.ConditionReady))
	require.Contains(t, readyConditionMessage(status.Conditions), "Installing MinIO chart")
	require.Empty(t, status.ConnectionSecret, "the connection is published once provisioning has finished")
	require.Contains(t, requests.all(), "PUT /"+deployTestMinIOBucket)
	var userSecret corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: minioUserSecretName(deployTestStore), Namespace: platform.SystemNamespace}, &userSecret))
	var chart helmv1.HelmChart
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ClusterMinIOChartName, Namespace: platform.HelmChartNamespace}, &chart))
	require.Contains(t, chart.Spec.ValuesContent, "arn:aws:s3:::"+deployTestMinIOBucket, "the bucket's policy is rendered into the chart")
	require.Contains(t, chart.Spec.ValuesContent, `policy: "geass-`+deployTestStore+`"`)

	for _, obj := range installedMinIOChart() {
		switch installed := obj.(type) {
		case *helmv1.HelmChart:
			chart.Status = installed.Status
			require.NoError(t, c.Status().Update(ctx, &chart))
		case *batchv1.Job:
			require.NoError(t, c.Create(ctx, installed))
		}
	}
	_, err = r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	status = readyStoreStatus(t, c)
	require.True(t, platform.IsConditionTrue(status.Conditions, platform.ConditionReady), readyConditionMessage(status.Conditions))
	require.Equal(t, deployTestStore+"-connection", status.ConnectionSecret)
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
	s3, requests := minioStub(t)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	draft := deployedProjectBucket(t, minioBucketSpec(deployTestBucket))
	draft.Spec.Deploy.Enabled = false
	platform.InitializePendingCreated(&draft.ObjectMeta)
	objects := append([]client.Object{project, cluster, draft}, readyClusterMinIOFixtures(s3.URL)...)
	r, c := newObjectStoreReconciler(t, s3.Client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(draft))
	require.NoError(t, err)

	status := readyStoreStatus(t, c)
	require.False(t, platform.IsConditionTrue(status.Conditions, platform.ConditionReady))
	require.Equal(t, waitingForDeployMessage, readyConditionMessage(status.Conditions))
	require.Empty(t, status.ConnectionSecret)
	require.Empty(t, requests.all(), "no bucket is created for a draft")
}

// A bucket that was provisioned and then edited keeps running what was last deployed: the edit is pending and
// only reaches MinIO once it is deployed.
func TestProvisionedProjectBucketWithPendingEditsRunsItsDeployedSpec(t *testing.T) {
	ctx := context.Background()
	s3, requests := minioStub(t)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, minioBucketSpec(deployTestBucket))
	store.Status.ConnectionSecret = deployTestStore + "-connection"
	store.Spec.Buckets = []string{"renamed"}
	platform.MarkPendingChange(&store.ObjectMeta, platform.PendingChangeSettings)
	objects := append([]client.Object{project, cluster, store}, readyClusterMinIOFixtures(s3.URL)...)
	r, c := newObjectStoreReconciler(t, s3.Client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	require.Equal(t, []string{"PUT /" + deployTestMinIOBucket}, requests.all())
	status := readyStoreStatus(t, c)
	require.True(t, platform.IsConditionTrue(status.Conditions, platform.ConditionReady), readyConditionMessage(status.Conditions))
}

// An edited store that never provisioned and has no snapshot cannot say what to run.
func TestPendingEditsWithoutASnapshotAreReported(t *testing.T) {
	ctx := context.Background()
	s3, requests := minioStub(t)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := deployedProjectBucket(t, minioBucketSpec(deployTestBucket))
	delete(store.Annotations, platform.LastDeployedAnnotation)
	platform.MarkPendingChange(&store.ObjectMeta, platform.PendingChangeSettings)
	objects := append([]client.Object{project, cluster, store}, readyClusterMinIOFixtures(s3.URL)...)
	r, c := newObjectStoreReconciler(t, s3.Client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	status := readyStoreStatus(t, c)
	require.Contains(t, readyConditionMessage(status.Conditions), "no last deployed snapshot")
	require.Empty(t, requests.all())
}

// Stores that predate the draft/deploy workflow carry no annotation. They never waited for a deploy, so a
// legacy store that has not provisioned yet must go ahead as well.
func TestLegacyProjectBucketWithoutConnectionSecretIsProvisioned(t *testing.T) {
	ctx := context.Background()
	s3, requests := minioStub(t)
	project, cluster := readyProjectCluster(deployTestProject, "dev")
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: deployTestStore, Namespace: platform.SystemNamespace, Finalizers: []string{objectStoreFinalizer}},
		Spec:       minioBucketSpec(deployTestBucket),
	}
	store.Spec.Project = deployTestProject
	store.Spec.Environment = geassv1alpha1.EnvironmentDev
	objects := append([]client.Object{project, cluster, store}, readyClusterMinIOFixtures(s3.URL)...)
	r, c := newObjectStoreReconciler(t, s3.Client(), objects...)

	_, err := r.Reconcile(ctx, reconcileRequest(store))
	require.NoError(t, err)

	status := readyStoreStatus(t, c)
	require.True(t, platform.IsConditionTrue(status.Conditions, platform.ConditionReady), readyConditionMessage(status.Conditions))
	require.Contains(t, requests.all(), "PUT /"+deployTestMinIOBucket)
}
