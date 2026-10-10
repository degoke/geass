package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/templates"
)

func templateServer(t *testing.T, objects ...client.Object) (*Server, client.Client) {
	t.Helper()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: []string{"production", "staging"}},
	}
	c := newFakeClient(append([]client.Object{project}, objects...)...)
	return &Server{Client: c}, c
}

func postTemplate(t *testing.T, srv *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleTemplateRoutes(rec, withAdminSession(t, srv, postForm(path, form)))
	return rec
}

func listApps(t *testing.T, c client.Client) []geassv1alpha1.GeassApp {
	t.Helper()
	var apps geassv1alpha1.GeassAppList
	require.NoError(t, c.List(context.Background(), &apps, client.InNamespace(platform.SystemNamespace)))
	return apps.Items
}

func listDatabases(t *testing.T, c client.Client) []geassv1alpha1.GeassDatabase {
	t.Helper()
	var dbs geassv1alpha1.GeassDatabaseList
	require.NoError(t, c.List(context.Background(), &dbs, client.InNamespace(platform.SystemNamespace)))
	return dbs.Items
}

func TestDeployingATemplateCreatesWiredDrafts(t *testing.T) {
	srv, c := templateServer(t)

	rec := postTemplate(t, srv, "/templates/postgres-adminer/deploy", url.Values{"project": {testProjectName}, "environment": {"staging"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	dbs, apps := listDatabases(t, c), listApps(t, c)
	require.Len(t, dbs, 1)
	require.Len(t, apps, 1)
	db, app := dbs[0], apps[0]

	require.Equal(t, "staging", string(db.Spec.Environment), "resources land in the chosen environment")
	require.Equal(t, geassv1alpha1.DatabaseEnginePostgres, db.Spec.Engine)
	require.False(t, db.Spec.Deploy.Enabled, "templates create drafts; nothing runs until changes are applied")
	require.False(t, app.Spec.Deploy.Enabled)
	require.NotEmpty(t, app.Annotations[platform.AppPendingChangesAnnotation], "drafts appear in the Apply changes list")
	require.Equal(t, "postgres", db.Annotations[platform.DisplayNameAnnotation])
	require.Equal(t, "adminer", app.Annotations[platform.DisplayNameAnnotation])
	require.Equal(t, "adminer:4", app.Spec.Source.Image.Image)
	require.Equal(t, int32(8080), app.Spec.Port)

	require.Len(t, app.Spec.Env, 1)
	env := app.Spec.Env[0]
	require.Equal(t, "ADMINER_DEFAULT_SERVER", env.Name)
	require.Equal(t, db.Name+"-connection", env.ValueFrom.SecretKeyRef.Name, "the service points at the new database's connection secret")
	require.Equal(t, "host", env.ValueFrom.SecretKeyRef.Key)

	var body struct {
		Created []createdResource `json:"created"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Created, 2)
}

func TestGeneratedSecretsAreRandomPerDeploymentAndStoredAsSecrets(t *testing.T) {
	srv, c := templateServer(t)
	secretOf := func() (geassv1alpha1.GeassApp, string) {
		rec := postTemplate(t, srv, "/templates/hasura-postgres/deploy", url.Values{"project": {testProjectName}, "environment": {"production"}})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var body struct {
			Created []createdResource `json:"created"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		var found geassv1alpha1.GeassApp
		for _, resource := range body.Created {
			if resource.Kind == "apps" {
				found, _ = getApp(t, c, resource.Name) // the app created by this deployment, not an earlier one
			}
		}
		require.NotNil(t, found.Spec.SecretRef)
		var secret corev1.Secret
		require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: found.Spec.SecretRef.Name, Namespace: platform.SystemNamespace}, &secret))
		return found, string(secret.Data["HASURA_GRAPHQL_ADMIN_SECRET"])
	}

	app, first := secretOf()
	require.Regexp(t, "^[0-9a-f]{32}$", first)
	require.Equal(t, "true", app.Spec.ConfigData["HASURA_GRAPHQL_ENABLE_CONSOLE"], "plain variables are stored as service variables")
	require.NotContains(t, app.Spec.ConfigData, "HASURA_GRAPHQL_ADMIN_SECRET", "a generated secret must not be stored as a plain variable")

	_, second := secretOf()
	require.NotEqual(t, first, second, "every deployment gets its own secret")
}

func TestDeployingAFunctionTemplateCreatesAFunction(t *testing.T) {
	srv, c := templateServer(t)
	rec := postTemplate(t, srv, "/templates/hello-function/deploy", url.Values{"project": {testProjectName}, "environment": {"production"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	apps := listApps(t, c)
	require.Len(t, apps, 1)
	require.NotNil(t, apps[0].Spec.Source.Function)
	require.Equal(t, "bun", apps[0].Spec.Source.Function.Runtime)
	require.Nil(t, apps[0].Spec.Source.Image)
}

func TestDeployingToAMissingProjectOrEnvironmentCreatesNothing(t *testing.T) {
	srv, c := templateServer(t)
	for name, form := range map[string]url.Values{
		"unknown project":     {"project": {"nope"}, "environment": {"production"}},
		"unknown environment": {"project": {testProjectName}, "environment": {"qa"}},
		"missing environment": {"project": {testProjectName}},
	} {
		t.Run(name, func(t *testing.T) {
			rec := postTemplate(t, srv, "/templates/postgres-adminer/deploy", form)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Empty(t, listApps(t, c))
			require.Empty(t, listDatabases(t, c))
		})
	}
	require.Equal(t, http.StatusNotFound, postTemplate(t, srv, "/templates/not-a-template/deploy", url.Values{"project": {testProjectName}, "environment": {"production"}}).Code)
}

func TestAFailedTemplateDeployRemovesWhatItAlreadyCreated(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: []string{"production"}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, isApp := obj.(*geassv1alpha1.GeassApp); isApp {
				return context.DeadlineExceeded
			}
			return c.Create(ctx, obj, opts...)
		},
	}).Build()
	srv := &Server{Client: c}

	rec := postTemplate(t, srv, "/templates/postgres-adminer/deploy", url.Values{"project": {testProjectName}, "environment": {"production"}})

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, listDatabases(t, c), "the database created before the failure must be cleaned up")
}

func TestSavingAProjectAsATemplateNeverCopiesSecretValues(t *testing.T) {
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "scarlet-creek", Namespace: platform.SystemNamespace, Annotations: map[string]string{platform.DisplayNameAnnotation: "main db"}},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: "production", Engine: geassv1alpha1.DatabaseEnginePostgres,
			Placement: geassv1alpha1.DatabasePlacementInCluster, Version: "16",
		},
	}
	external := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "managed-db", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: "production", Engine: geassv1alpha1.DatabaseEnginePostgres,
			Placement: geassv1alpha1.DatabasePlacementExternal,
		},
	}
	bucket := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: "assets", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassObjectStoreSpec{Project: testProjectName, Environment: "production"},
	}
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "little-frost", Namespace: platform.SystemNamespace, Annotations: map[string]string{platform.DisplayNameAnnotation: "web"}},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: "production", Port: 3000,
			Source:     imageAppSource("ghcr.io/acme/web:1"),
			ConfigData: map[string]string{"LOG_LEVEL": "debug"},
			SecretRef:  &corev1.LocalObjectReference{Name: "little-frost-secrets"},
			Env: []corev1.EnvVar{
				{Name: "DATABASE_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "scarlet-creek-connection"}, Key: "uri"}}},
				{Name: "REGION", Value: "eu"},
				{Name: "FOREIGN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "unrelated"}, Key: "x"}}},
			},
		},
	}
	appSecrets := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "little-frost-secrets", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"API_TOKEN": []byte("super-secret-value")},
	}
	otherEnv := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "staging-app", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "staging", Source: imageAppSource("nginx")},
	}
	srv, c := templateServer(t, db, external, bucket, app, appSecrets, otherEnv)

	rec := postTemplate(t, srv, "/templates/create", url.Values{"project": {testProjectName}, "environment": {"production"}, "name": {"My stack"}, "description": {"web + db"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "super-secret-value", "secret values must never leave the cluster")

	var body struct {
		Template templates.Template `json:"template"`
		Skipped  []string           `json:"skipped"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "My stack", body.Template.Name)
	require.Len(t, body.Template.Resources, 2, "one database and one service; the other environment is not included")
	require.Len(t, body.Skipped, 2, "the external database and the bucket are reported, not silently dropped")

	var saved templates.Resource
	var savedDB templates.Resource
	for _, resource := range body.Template.Resources {
		if resource.Kind == templates.KindService {
			saved = resource
		} else {
			savedDB = resource
		}
	}
	require.Equal(t, "web", saved.Name)
	require.Equal(t, "ghcr.io/acme/web:1", saved.Image)
	require.Equal(t, int32(3000), saved.Port)
	require.Equal(t, "Postgres", savedDB.Engine)
	require.Equal(t, "16", savedDB.Version)
	vars := map[string]templates.Variable{}
	for _, variable := range saved.Variables {
		vars[variable.Name] = variable
	}
	require.Equal(t, "debug", vars["LOG_LEVEL"].Value)
	require.Equal(t, "eu", vars["REGION"].Value)
	require.Equal(t, templates.GenerateSecretKind, vars["API_TOKEN"].Generate, "a secret becomes a variable that is regenerated on deploy")
	require.Empty(t, vars["API_TOKEN"].Value)
	require.Equal(t, &templates.Reference{Resource: savedDB.Key, Key: "uri"}, vars["DATABASE_URL"].From, "the database connection becomes a reference, not a copied URL")
	require.NotContains(t, vars, "FOREIGN", "references to unrelated secrets are dropped")

	// The saved template shows up, can be deployed, and can be deleted; built-ins cannot be deleted.
	list, err := srv.listTemplates(context.Background())
	require.NoError(t, err)
	require.Equal(t, len(templates.Builtin())+1, len(list))
	require.Equal(t, body.Template.ID, list[len(list)-1].ID)

	deploy := postTemplate(t, srv, "/templates/"+body.Template.ID+"/deploy", url.Values{"project": {testProjectName}, "environment": {"staging"}})
	require.Equal(t, http.StatusOK, deploy.Code, deploy.Body.String())
	require.Len(t, listDatabases(t, c), 3, "the saved database template created a new database in staging")

	require.Equal(t, http.StatusBadRequest, postTemplate(t, srv, "/templates/echo-demo/delete", url.Values{}).Code)
	require.Equal(t, http.StatusOK, postTemplate(t, srv, "/templates/"+body.Template.ID+"/delete", url.Values{}).Code)
	list, err = srv.listTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, list, len(templates.Builtin()))
}

func TestSavingAnEmptyEnvironmentIsRefused(t *testing.T) {
	srv, _ := templateServer(t)
	rec := postTemplate(t, srv, "/templates/create", url.Values{"project": {testProjectName}, "environment": {"production"}, "name": {"Empty"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "no services")

	rec = postTemplate(t, srv, "/templates/create", url.Values{"project": {testProjectName}, "environment": {"production"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "name is required")
}

func TestSavedTemplatesCannotShadowBuiltinsOrBeTampered(t *testing.T) {
	good := templates.Template{ID: "mine", Name: "Mine", Resources: []templates.Resource{{Key: "web", Kind: templates.KindService, Image: "nginx"}}}
	shadow := good
	shadow.ID = "echo-demo"
	broken := good
	broken.ID = "broken"
	broken.Resources = []templates.Resource{{Key: "web", Kind: templates.KindService}}
	mismatch := good
	mismatch.ID = "other"

	store := func(name string, template templates.Template) *corev1.ConfigMap {
		data, err := json.Marshal(template)
		require.NoError(t, err)
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Labels: map[string]string{templateLabel: "true"}},
			Data:       map[string]string{templateDataKey: string(data)},
		}
	}
	srv, _ := templateServer(t,
		store("template-mine", good), store("template-echo-demo", shadow),
		store("template-broken", broken), store("template-mismatch", mismatch),
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "template-junk", Namespace: platform.SystemNamespace, Labels: map[string]string{templateLabel: "true"}}, Data: map[string]string{templateDataKey: "{not json"}},
	)

	list, err := srv.listTemplates(context.Background())
	require.NoError(t, err)
	ids := []string{}
	for _, template := range list {
		ids = append(ids, template.ID)
	}
	require.Contains(t, ids, "mine")
	require.Equal(t, 1, strings.Count(strings.Join(ids, ","), "echo-demo"), "a saved template must not replace a built-in")
	for _, hidden := range []string{"broken", "other"} {
		require.NotContains(t, ids, hidden)
	}
	echo, found, err := srv.findTemplate(context.Background(), "echo-demo")
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, echo.Builtin)
}

func TestViewersCannotBrowseTemplates(t *testing.T) {
	require.True(t, dashboardSensitiveRead("/api/templates"), "saved templates can contain variable values")
}

// Display names can be 120 characters, but template resource keys stop at 31.
func TestSavingAProjectWithLongServiceNamesProducesValidUniqueKeys(t *testing.T) {
	long := "customer-notification-email-dispatcher-for-the-european-region"
	objects := []client.Object{}
	for _, name := range []string{"first-service", "second-service", "third-service"} {
		objects = append(objects, &geassv1alpha1.GeassApp{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Annotations: map[string]string{platform.DisplayNameAnnotation: long}},
			Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine")},
		})
	}
	srv, _ := templateServer(t, objects...)

	rec := postTemplate(t, srv, "/templates/create", url.Values{"project": {testProjectName}, "environment": {"production"}, "name": {"Long names"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var reply struct {
		Template templates.Template `json:"template"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &reply))
	require.Len(t, reply.Template.Resources, 3)
	seen := map[string]bool{}
	for _, resource := range reply.Template.Resources {
		require.LessOrEqual(t, len(resource.Key), templates.MaxKeyLength, resource.Key)
		require.False(t, seen[resource.Key], "keys stay unique after trimming: %s", resource.Key)
		seen[resource.Key] = true
	}
	require.NoError(t, reply.Template.Validate())
}
