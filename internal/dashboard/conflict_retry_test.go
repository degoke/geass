package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// conflictOnce returns a fake client whose first Update of a Project or App fails with the
// conflict a controller write between the dashboard's read and write would cause.
func conflictOnce(t *testing.T, objects ...client.Object) (client.Client, *atomic.Int32) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	var conflicts atomic.Int32
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			switch obj.(type) {
			case *geassv1alpha1.GeassProject, *geassv1alpha1.GeassApp, *geassv1alpha1.GeassDatabase:
				if conflicts.Add(1) == 1 {
					return apierrors.NewConflict(schema.GroupResource{Group: "geass.geass.dev", Resource: "projects"}, obj.GetName(), nil)
				}
			}
			return c.Update(ctx, obj, opts...)
		},
	}).Build()
	return c, &conflicts
}

func postForm(path string, form url.Values) *http.Request {
	req := withOrigin(httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return req
}

func TestSecretSharedVariableSaveSurvivesAControllerWriteConflict(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: []string{"production"}},
	}
	c, conflicts := conflictOnce(t, project)
	srv := &Server{Client: c}

	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, postForm("/projects/payments/variables/save", url.Values{"environment": {"production"}, "name": {"API_KEY"}, "value": {"s3cret"}, "secret": {"on"}}))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.GreaterOrEqual(t, conflicts.Load(), int32(2), "the first update must conflict and be retried")
	var updated geassv1alpha1.GeassProject
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &updated))
	require.Len(t, updated.Spec.SharedVariables, 1, "the retry must not duplicate the variable")
	require.Equal(t, "API_KEY", updated.Spec.SharedVariables[0].Name)
	require.NotNil(t, updated.Spec.SharedVariables[0].SecretRef)
}

func TestSharedVariableDeleteSurvivesAControllerWriteConflict(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			Environments:    []string{"production"},
			SharedVariables: []geassv1alpha1.GeassSharedVariable{{Name: "LOG_LEVEL", Environment: "production", Value: "info"}},
		},
	}
	c, _ := conflictOnce(t, project)
	srv := &Server{Client: c}

	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, postForm("/projects/payments/variables/delete", url.Values{"environment": {"production"}, "name": {"LOG_LEVEL"}}))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var updated geassv1alpha1.GeassProject
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &updated))
	require.Empty(t, updated.Spec.SharedVariables)
}

func TestAppVariableHandlersSurviveAControllerWriteConflict(t *testing.T) {
	ctx := context.Background()
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project:     testProjectName,
			Environment: "production",
			Source:      imageAppSource("nginx:alpine"),
			Env:         []corev1.EnvVar{{Name: "DATABASE_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "db-conn"}, Key: "uri"}}}},
		},
	}
	c, _ := conflictOnce(t, app)
	srv := &Server{Client: c}
	key := client.ObjectKey{Name: "web", Namespace: platform.SystemNamespace}

	rec := httptest.NewRecorder()
	srv.handleAppRoutes(rec, withAdminSession(t, srv, postForm("/apps/web/config/set", url.Values{"key": {"APP_ENV"}, "value": {"staging"}})))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, key, &got))
	require.Equal(t, "staging", got.Spec.ConfigData["APP_ENV"])

	rec = httptest.NewRecorder()
	srv.handleAppRoutes(rec, withAdminSession(t, srv, postForm("/apps/web/env/delete", url.Values{"key": {"DATABASE_URL"}})))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, c.Get(ctx, key, &got))
	require.Empty(t, got.Spec.Env)

	rec = httptest.NewRecorder()
	srv.handleAppRoutes(rec, withAdminSession(t, srv, postForm("/apps/web/env/delete", url.Values{"key": {"DATABASE_URL"}})))
	require.Equal(t, http.StatusBadRequest, rec.Code, "removing a variable that is already gone is reported, not silently accepted")
	require.Contains(t, rec.Body.String(), "variable was not found")
}

func TestUpsertSecretKeyKeepsEveryKeyWhenRequestsRace(t *testing.T) {
	ctx := context.Background()
	c := newFakeClient()
	srv := &Server{Client: c}
	key := client.ObjectKey{Name: "payments-shared-secrets", Namespace: platform.SystemNamespace}

	names := []string{"one", "two", "three", "four", "five", "six"}
	errs := make(chan error, len(names))
	start := make(chan struct{})
	for _, name := range names {
		go func() {
			<-start
			errs <- srv.upsertSecretKey(ctx, key, "production__"+name, name+"-value")
		}()
	}
	close(start)
	for range names {
		require.NoError(t, <-errs)
	}

	var secret corev1.Secret
	require.NoError(t, c.Get(ctx, key, &secret))
	require.Len(t, secret.Data, len(names), "a request that loses the creation race must add its key, not fail or overwrite")
	for _, name := range names {
		require.Equal(t, []byte(name+"-value"), secret.Data["production__"+name])
	}
}

func TestEnvironmentCreateAndArchiveSurviveAControllerWriteConflict(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			Environments:    []string{"production"},
			SharedVariables: []geassv1alpha1.GeassSharedVariable{{Name: "LOG_LEVEL", Environment: "staging", Value: "debug"}},
		},
	}
	stagingApp := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "staging-web", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "staging", Source: imageAppSource("nginx")},
	}
	stagingDB := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "staging-db", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Environment: "staging", Engine: geassv1alpha1.DatabaseEnginePostgres},
	}
	productionApp := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "production-web", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx")},
	}
	otherProjectApp := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "other-web", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: "other", Environment: "staging", Source: imageAppSource("nginx")},
	}
	c, _ := conflictOnce(t, project, stagingApp, stagingDB, productionApp, otherProjectApp)
	srv := &Server{Client: c}
	key := client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}

	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, postForm("/projects/payments/environments/create", url.Values{"environment": {"staging"}}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got geassv1alpha1.GeassProject
	require.NoError(t, c.Get(ctx, key, &got))
	require.Equal(t, []string{"production", "staging"}, got.Spec.Environments)

	rec = httptest.NewRecorder()
	srv.handleProjectRoutes(rec, postForm("/projects/payments/environments/create", url.Values{"environment": {"staging"}}))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "environment already exists")

	rec = httptest.NewRecorder()
	srv.handleProjectRoutes(rec, postForm("/projects/payments/environments/archive", url.Values{"environment": {"staging"}, "confirmName": {"staging"}}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, c.Get(ctx, key, &got))
	require.Equal(t, []string{"production"}, got.Spec.Environments)
	require.Empty(t, got.Spec.SharedVariables, "archiving an environment removes its shared variables")
	require.Contains(t, rec.Body.String(), `"deleted":2`, "the environment's app and database are reported as deleted")
	var remaining geassv1alpha1.GeassAppList
	require.NoError(t, c.List(ctx, &remaining, client.InNamespace(platform.SystemNamespace)))
	names := make([]string, 0, len(remaining.Items))
	for _, app := range remaining.Items {
		names = append(names, app.Name)
	}
	require.ElementsMatch(t, []string{"production-web", "other-web"}, names,
		"only the archived environment's services go; other environments and other projects are untouched")
	var dbs geassv1alpha1.GeassDatabaseList
	require.NoError(t, c.List(ctx, &dbs, client.InNamespace(platform.SystemNamespace)))
	require.Empty(t, dbs.Items)

	rec = httptest.NewRecorder()
	srv.handleProjectRoutes(rec, postForm("/projects/payments/environments/archive", url.Values{"environment": {"production"}, "confirmName": {"production"}}))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "retain at least one environment")
}

func TestDeployRightAfterCreatingDraftsSurvivesAControllerWriteConflict(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: []string{"production"}},
	}
	// A template creates drafts; the controller then adds finalizers, so the first dashboard write conflicts.
	draftApp := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine")},
	}
	platform.InitializePendingCreated(&draftApp.ObjectMeta)
	draftDB := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: "production",
			Engine: geassv1alpha1.DatabaseEnginePostgres, Placement: geassv1alpha1.DatabasePlacementInCluster,
		},
	}
	platform.InitializePendingCreated(&draftDB.ObjectMeta)
	c, conflicts := conflictOnce(t, project, draftApp, draftDB, roomyTestNode())
	srv := &Server{Client: c}

	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, postForm("/projects/payments/deploy", url.Values{"environment": {"production"}}))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.GreaterOrEqual(t, conflicts.Load(), int32(2), "the first write conflicted and was retried")
	var app geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "web", Namespace: platform.SystemNamespace}, &app))
	require.True(t, app.Spec.Deploy.Enabled, "the app was deployed")
	var db geassv1alpha1.GeassDatabase
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "db", Namespace: platform.SystemNamespace}, &db))
	require.True(t, db.Spec.Deploy.Enabled, "the database was deployed")
	require.Contains(t, rec.Body.String(), `"deployed":2`)
}
