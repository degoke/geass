package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func functionTestServer(t *testing.T, objects ...client.Object) (*Server, client.Client) {
	t.Helper()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: []string{"production"}},
	}
	c := newFakeClient(append([]client.Object{project}, objects...)...)
	return &Server{Client: c}, c
}

func createFunction(t *testing.T, srv *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	form.Set("project", testProjectName)
	form.Set("environment", "production")
	form.Set("source", "function")
	form.Set("draft", "on")
	rec := httptest.NewRecorder()
	srv.handleAppCreate(rec, withAdminSession(t, srv, postForm("/apps/create", form)))
	return rec
}

func onlyApp(t *testing.T, c client.Client) geassv1alpha1.GeassApp {
	t.Helper()
	var apps geassv1alpha1.GeassAppList
	require.NoError(t, c.List(context.Background(), &apps, client.InNamespace(platform.SystemNamespace)))
	require.Len(t, apps.Items, 1)
	return apps.Items[0]
}

func TestCreatingAFunctionStartsAsADraftWithStarterCode(t *testing.T) {
	srv, c := functionTestServer(t)

	rec := createFunction(t, srv, url.Values{"runtime": {"python"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	app := onlyApp(t, c)
	require.Nil(t, app.Spec.Source.Image, "a function has no image source")
	require.Nil(t, app.Spec.Source.Git)
	require.NotNil(t, app.Spec.Source.Function)
	require.Equal(t, "python", app.Spec.Source.Function.Runtime)
	require.Equal(t, platform.FunctionStarterCode("python"), app.Spec.Source.Function.Code)
	require.False(t, app.Spec.Deploy.Enabled, "it waits for the user to deploy")
	require.Equal(t, int32(8080), app.Spec.Port)
}

func TestCreatingAFunctionDefaultsToBunAndRejectsBadRuntimes(t *testing.T) {
	srv, c := functionTestServer(t)
	require.Equal(t, http.StatusOK, createFunction(t, srv, url.Values{}).Code)
	require.Equal(t, "bun", onlyApp(t, c).Spec.Source.Function.Runtime)

	srv, c = functionTestServer(t)
	rec := createFunction(t, srv, url.Values{"runtime": {"cobol"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var apps geassv1alpha1.GeassAppList
	require.NoError(t, c.List(context.Background(), &apps))
	require.Empty(t, apps.Items, "an invalid function must not be created")
}

func TestSavingFunctionCodeMarksAPendingChange(t *testing.T) {
	srv, c := functionTestServer(t)
	require.Equal(t, http.StatusOK, createFunction(t, srv, url.Values{"runtime": {"node"}}).Code)
	app := onlyApp(t, c)

	update := func(form url.Values) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.handleAppRoutes(rec, withAdminSession(t, srv, postForm("/apps/"+app.Name+"/update", form)))
		return rec
	}

	rec := update(url.Values{"functionCode": {"console.log('v2')"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	updated := onlyApp(t, c)
	require.Equal(t, "console.log('v2')", updated.Spec.Source.Function.Code)
	require.Equal(t, "node", updated.Spec.Source.Function.Runtime, "saving code keeps the runtime")
	require.NotEmpty(t, updated.Annotations[platform.AppPendingChangesAnnotation], "the edit waits for a deploy")

	require.Equal(t, http.StatusBadRequest, update(url.Values{"functionCode": {""}}).Code, "empty code is refused")
	require.Equal(t, http.StatusBadRequest, update(url.Values{"functionRuntime": {"php"}}).Code, "unknown runtimes are refused")
	require.Equal(t, "console.log('v2')", onlyApp(t, c).Spec.Source.Function.Code, "refused edits change nothing")

	rec = update(url.Values{"image": {"nginx:alpine"}})
	require.Equal(t, http.StatusOK, rec.Code)
	got := onlyApp(t, c)
	require.Nil(t, got.Spec.Source.Image, "posting an image must not turn a function into an image service")
	require.NotNil(t, got.Spec.Source.Function)
}

func TestFunctionsCannotBeRolledBack(t *testing.T) {
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "hello", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: "production",
			Source: geassv1alpha1.GeassAppSource{Function: &geassv1alpha1.GeassAppFunctionSource{Runtime: "bun", Code: "x"}},
		},
	}
	revision := &geassv1alpha1.GeassDeployment{
		ObjectMeta: metav1.ObjectMeta{Name: "hello-1", Namespace: platform.SystemNamespace, Labels: map[string]string{platform.LabelApp: "hello"}},
		Spec:       geassv1alpha1.GeassDeploymentSpec{App: "hello", Image: "function:bun"},
	}
	srv, c := functionTestServer(t, app, revision)

	rec := httptest.NewRecorder()
	srv.handleAppRoutes(rec, withAdminSession(t, srv, postForm("/apps/hello/rollback", url.Values{"revision": {"hello-1"}})))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "cannot be rolled back")
	var got geassv1alpha1.GeassApp
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "hello", Namespace: platform.SystemNamespace}, &got))
	require.Nil(t, got.Spec.Source.Image, "a rollback must not replace code with a bogus image reference")
}

func TestViewersDoNotReceiveFunctionCode(t *testing.T) {
	source := geassv1alpha1.GeassAppSource{Function: &geassv1alpha1.GeassAppFunctionSource{Runtime: "bun", Code: "const key = 'hunter2'"}}
	redacted := redactAppSource(source)
	require.Equal(t, "bun", redacted.Function.Runtime, "viewers still see what kind of service it is")
	require.Empty(t, redacted.Function.Code)
	require.NotEmpty(t, source.Function.Code, "redaction must not modify the stored object")
	require.False(t, strings.Contains(redacted.Function.Code, "hunter2"))
}
