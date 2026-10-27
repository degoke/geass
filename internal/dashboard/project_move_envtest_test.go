package dashboard

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/controller"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/projectbundle"
)

// The import is tried against a real API server: its CRDs refuse what is not valid (names, patterns, enums, limits), which a fake
// client never does, and the project controller then makes the project's environments from what the import made. It needs the
// envtest binaries (make test sets KUBEBUILDER_ASSETS); without them it is skipped, not passed.
func TestAnImportedProjectIsAcceptedByARealAPIServerAndItsEnvironmentsAreMade(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("needs the envtest binaries: run it with make test, or set KUBEBUILDER_ASSETS")
	}
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases"), filepath.Join("..", "..", "config", "crd", "external")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := context.Background()

	// The cluster the project is moved to: a ready cluster, an object storage server, and a machine.
	require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platform.SystemNamespace}}))
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "dedicated", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassClusterSpec{Version: "v1", ServerURL: "https://cluster.example", TokenSecretRef: corev1.SecretReference{Name: "token"}},
	}
	require.NoError(t, c.Create(ctx, cluster))
	cluster.Status.Conditions = []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue, Reason: platform.ConditionReasonReady, Message: "ready", LastTransitionTime: metav1.Now()}}
	require.NoError(t, c.Status().Update(ctx, cluster))
	require.NoError(t, c.Create(ctx, &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassObjectStoreSpec{Engine: geassv1alpha1.ObjectStoreEngineSeaweedFS},
	}))

	// A project as it would be on the source cluster, made into a bundle in memory.
	size := resource.MustParse("5Gi")
	replicas := int32(2)
	source := projectbundle.Source{
		Project: geassv1alpha1.GeassProject{
			ObjectMeta: metav1.ObjectMeta{Name: "proj-1a2b3c4d"},
			Spec: geassv1alpha1.GeassProjectSpec{
				DisplayName: "morning-beach", ClusterRef: corev1.LocalObjectReference{Name: "shared"}, Org: "acme", Environments: []string{"production", "staging"},
				SharedVariables:  []geassv1alpha1.GeassSharedVariable{{Name: "REGION_NAME", Environment: "production", Value: "eu"}},
				PREnvironments:   &geassv1alpha1.GeassPREnvironments{Enabled: true, BaseEnvironment: "staging", MaxOpen: 3},
				Network:          &geassv1alpha1.GeassProjectNetwork{Isolation: geassv1alpha1.NetworkIsolationProject, BlockNodeAddresses: true},
				EnvironmentQuota: &geassv1alpha1.GeassEnvironmentQuota{Pods: ptrInt32(30)},
				ProjectQuota:     &geassv1alpha1.GeassProjectQuota{CPU: ptrQuantity("8")},
			},
		},
		Databases: []geassv1alpha1.GeassDatabase{{
			ObjectMeta: metav1.ObjectMeta{Name: "orders-db", Annotations: map[string]string{platform.DisplayNameAnnotation: "orders"}},
			Spec: geassv1alpha1.GeassDatabaseSpec{
				Project: "proj-1a2b3c4d", Environment: "production", Engine: geassv1alpha1.DatabaseEnginePostgres, Version: "16", StorageSize: &size,
				Backup: &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: 7},
			},
		}},
		Buckets: []geassv1alpha1.GeassObjectStore{{
			ObjectMeta: metav1.ObjectMeta{Name: "uploads"},
			Spec:       geassv1alpha1.GeassObjectStoreSpec{Project: "proj-1a2b3c4d", Environment: "production", Engine: geassv1alpha1.ObjectStoreEngineSeaweedFS, Buckets: []string{"uploads"}},
		}},
		Apps: []projectbundle.SourceApp{
			{App: geassv1alpha1.GeassApp{
				ObjectMeta: metav1.ObjectMeta{Name: "web", Annotations: map[string]string{platform.DisplayNameAnnotation: "storefront"}},
				Spec: geassv1alpha1.GeassAppSpec{
					Project: "proj-1a2b3c4d", Environment: "production", Source: imageAppSource("ghcr.io/acme/web:1.4"), Port: 3000, Replicas: &replicas,
					Ingress:    geassv1alpha1.GeassAppIngressSpec{Host: "shop.fixture.example", TLSEnabled: true},
					ConfigData: map[string]string{"LOG_LEVEL": "debug"},
					Env: []corev1.EnvVar{{Name: "DATABASE_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "orders-db-connection"}, Key: platform.ConnectionKeyURI}}}},
				},
			}, SecretKeys: []string{"SESSION_KEY"}},
			{App: geassv1alpha1.GeassApp{
				ObjectMeta: metav1.ObjectMeta{Name: "api"},
				Spec: geassv1alpha1.GeassAppSpec{
					Project: "proj-1a2b3c4d", Environment: "staging", Port: 8080,
					Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{
						ConnectionRef: corev1.LocalObjectReference{Name: "proj-1a2b3c4d-github"}, Repository: "acme/api", Branch: "main",
					}},
				},
			}},
			{App: geassv1alpha1.GeassApp{
				ObjectMeta: metav1.ObjectMeta{Name: "nightly"},
				Spec: geassv1alpha1.GeassAppSpec{
					Project: "proj-1a2b3c4d", Environment: "staging", Port: 8080,
					Source: geassv1alpha1.GeassAppSource{Function: &geassv1alpha1.GeassAppFunctionSource{
						Runtime: "node", Code: "require('http').createServer((q, s) => s.end('ok')).listen(process.env.PORT)\n",
						Triggers: []geassv1alpha1.GeassFunctionTrigger{{Name: "nightly", Schedule: "0 2 * * *"}},
					}},
				},
			}},
		},
		GeassRelease: "v0.0.0-test", Now: time.Now(),
	}
	bundle, err := projectbundle.Build(source)
	require.NoError(t, err)
	archive, err := bundle.Encode()
	require.NoError(t, err)

	srv := &Server{Client: ScopeClient(c), Metrics: &fakeMetrics{}, Kube: kubefake.NewClientset()}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	post := func(path string) *httptest.ResponseRecorder {
		req := withOrigin(httptest.NewRequest(http.MethodPost, path, bytes.NewReader(archive)))
		req.Header.Set("Content-Type", "application/gzip")
		req = req.WithContext(context.WithValue(req.Context(), sessionContextKey{}, platformSession))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	dry := post(moveImport + "?dryRun=true")
	require.Equal(t, http.StatusOK, dry.Code, dry.Body.String())
	require.True(t, decodeMove(t, dry).OK, "%+v", decodeMove(t, dry).Plan.Blockers)
	var none geassv1alpha1.GeassProjectList
	require.NoError(t, c.List(ctx, &none))
	require.Empty(t, none.Items, "a dry run makes nothing on a real API server either")

	rec := post(moveImport)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	answer := decodeMove(t, rec)

	// Everything the import made was accepted by the CRDs and is a draft.
	var project geassv1alpha1.GeassProject
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: answer.Project.ID, Namespace: platform.SystemNamespace}, &project))
	require.Equal(t, "dedicated", project.Spec.ClusterRef.Name)
	require.Equal(t, []string{"production", "staging"}, project.Spec.Environments)
	var apps geassv1alpha1.GeassAppList
	var databases geassv1alpha1.GeassDatabaseList
	var stores geassv1alpha1.GeassObjectStoreList
	require.NoError(t, c.List(ctx, &apps))
	require.NoError(t, c.List(ctx, &databases))
	require.NoError(t, c.List(ctx, &stores))
	require.Len(t, apps.Items, 3)
	require.Len(t, databases.Items, 1)
	require.Len(t, stores.Items, 2, "the object storage server and the project's bucket")
	for _, app := range apps.Items {
		require.Equal(t, answer.Project.ID, app.Spec.Project)
		require.False(t, app.Spec.Deploy.Enabled)
		if git := app.Spec.Source.Git; git != nil {
			require.Empty(t, git.ConnectionRef.Name, "the repository service is connected when the new project is")
		}
	}
	require.False(t, databases.Items[0].Spec.Deploy.Enabled)

	// The project controller makes the environments from it, as it does for any project.
	reconciler := &controller.GeassProjectReconciler{Client: c, Scheme: scheme}
	_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&project)})
	require.NoError(t, err)
	for _, environment := range []string{"production", "staging"} {
		var namespace corev1.Namespace
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: answer.Project.ID + "-" + environment}, &namespace))
		require.Equal(t, answer.Project.ID, namespace.Labels[platform.LabelProject])
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ProjectIsolationPolicyName, Namespace: namespace.Name}, &networkingv1.NetworkPolicy{}),
			"the imported project is isolated like any other from the first reconcile")
	}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(&project), &project))
	require.Len(t, project.Status.Environments, 2)
}

func ptrInt32(v int32) *int32 { return &v }

func ptrQuantity(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}
