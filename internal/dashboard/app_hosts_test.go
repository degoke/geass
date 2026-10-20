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

func TestNormalizeAppHost(t *testing.T) {
	for raw, want := range map[string]string{
		"":                  "",
		"  ":                "",
		"shop.example.com":  "shop.example.com",
		"Shop.Example.COM":  "shop.example.com",
		"shop.example.com.": "shop.example.com",
		" shop.example.com": "shop.example.com",
		"*.example.com":     "*.example.com",
	} {
		got, err := normalizeAppHost(raw)
		require.NoError(t, err, raw)
		require.Equal(t, want, got, raw)
	}
	for _, raw := range []string{"https://shop.example.com", "shop.example.com:8080", "shop.example.com/path", "shop example.com", "-bad.example.com", "a..b.example.com", "bad_host.example.com"} {
		_, err := normalizeAppHost(raw)
		require.Error(t, err, "%q is not a hostname", raw)
	}
}

func serviceWithHost(name, host string) *geassv1alpha1.GeassApp {
	return &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: "production", Port: 8080,
			Source:  imageAppSource("nginx:alpine"),
			Ingress: geassv1alpha1.GeassAppIngressSpec{Host: host},
		},
	}
}

func saveHostOf(t *testing.T, srv *Server, app string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleAppRoutes(rec, postForm("/apps/"+app+"/update", form))
	return rec
}

func TestAServiceCannotTakeAHostnameAnotherServiceHas(t *testing.T) {
	srv := &Server{Client: newFakeClient(serviceWithHost("api", "api.example.com"), serviceWithHost("web", "web.example.com"))}

	rec := saveHostOf(t, srv, "web", url.Values{"host": {"API.example.com"}})

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), errHostInUse)
	require.NotContains(t, rec.Body.String(), `"api"`, "the other service is not named: it may be someone else's")
	var web geassv1alpha1.GeassApp
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: "web", Namespace: platform.SystemNamespace}, &web))
	require.Equal(t, "web.example.com", web.Spec.Ingress.Host, "nothing was saved")
}

func TestAHostnameInAnotherServicesRulesIsTakenToo(t *testing.T) {
	other := serviceWithHost("api", "")
	other.Spec.Ingress.Rules = []geassv1alpha1.GeassAppIngressRule{{Host: "Shop.Example.com", Path: "/"}}
	srv := &Server{Client: newFakeClient(other, serviceWithHost("web", "web.example.com"))}

	rec := saveHostOf(t, srv, "web", url.Values{"host": {"shop.example.com"}})

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), errHostInUse)
}

func TestAFreeHostnameIsSavedInLowercase(t *testing.T) {
	srv := &Server{Client: newFakeClient(serviceWithHost("web", "web.example.com"))}

	rec := saveHostOf(t, srv, "web", url.Values{"host": {"Shop.Example.com."}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var web geassv1alpha1.GeassApp
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: "web", Namespace: platform.SystemNamespace}, &web))
	require.Equal(t, "shop.example.com", web.Spec.Ingress.Host)
}

func TestAHostnameThatIsNotOneIsRefusedBeforeItBecomesAnIngress(t *testing.T) {
	srv := &Server{Client: newFakeClient(serviceWithHost("web", "web.example.com"))}

	rec := saveHostOf(t, srv, "web", url.Values{"host": {"https://shop.example.com/x"}})

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "not a valid hostname")
}

func TestAHostnameAServiceAlreadyHadDoesNotStopAnUnrelatedEdit(t *testing.T) {
	// Two services that share a hostname, as they could before the check existed.
	srv := &Server{Client: newFakeClient(serviceWithHost("api", "shared.example.com"), serviceWithHost("web", "shared.example.com"))}

	rec := saveHostOf(t, srv, "web", url.Values{"host": {"shared.example.com"}, "replicas": {"2"}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestANewServiceCannotTakeAHostnameInUse(t *testing.T) {
	srv := &Server{Client: newFakeClient(serviceWithHost("api", "api.example.com"))}
	form := url.Values{formFieldName: {testWorkerAppName}, "project": {testProjectName}, "environment": {"production"}, "image": {"ghcr.io/acme/worker:1"}, "host": {"api.example.com"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/apps/create", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	srv.handleAppCreate(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), errHostInUse)
	var apps geassv1alpha1.GeassAppList
	require.NoError(t, srv.Client.List(context.Background(), &apps, client.InNamespace(platform.SystemNamespace)))
	require.Len(t, apps.Items, 1, "the new service was not created")
}

func TestMovingAServiceNeedsARealTargetProjectAndEnvironment(t *testing.T) {
	target := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: []string{"production"}},
	}
	srv := &Server{Client: newFakeClient(serviceWithHost("web", "web.example.com"), target)}
	stays := func(t *testing.T) {
		t.Helper()
		var web geassv1alpha1.GeassApp
		require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: "web", Namespace: platform.SystemNamespace}, &web))
		require.Equal(t, testProjectName, web.Spec.Project, "it stays where it was")
		require.Equal(t, geassv1alpha1.GeassEnvironment("production"), web.Spec.Environment)
	}

	rec := saveHostOf(t, srv, "web", url.Values{"project": {"nowhere"}})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "does not exist")
	stays(t)

	rec = saveHostOf(t, srv, "web", url.Values{"project": {"other"}, "environment": {"staging"}})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "has no staging environment")
	stays(t)

	rec = saveHostOf(t, srv, "web", url.Values{"project": {"other"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var moved geassv1alpha1.GeassApp
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: "web", Namespace: platform.SystemNamespace}, &moved))
	require.Equal(t, "other", moved.Spec.Project, "a real target is allowed, so moving still works")
}

func TestAServiceCannotTakeTheDashboardsHostname(t *testing.T) {
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassPlatformConfigSpec{RootDomain: "example.com"},
	}
	srv := &Server{Client: newFakeClient(config, serviceWithHost("web", "web.example.com"))}

	rec := saveHostOf(t, srv, "web", url.Values{"host": {"Geass.Example.com"}})

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), errHostIsDashboard)
}
