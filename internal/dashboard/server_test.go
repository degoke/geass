package dashboard

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const testAppName = "demo"

type fakeMetrics struct {
	values map[string]string
}

func (f *fakeMetrics) QueryInstant(_ context.Context, query string) (string, error) {
	if v, ok := f.values[query]; ok {
		return v, nil
	}
	return "42", nil
}

func newFakeClient(objects ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = geassv1alpha1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestHandleAppsList(t *testing.T) {
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testAppName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Workspace: geassv1alpha1.WorkspaceDev,
			Image:     "nginx:alpine",
		},
	}
	srv := &Server{Client: newFakeClient(app)}

	req := httptest.NewRequest(http.MethodGet, "/apps", nil)
	rec := httptest.NewRecorder()
	srv.handleApps(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, testAppName)
	require.Contains(t, body, "hx-get=\"/apps\"")
}

func TestHandleAppCreateValidation(t *testing.T) {
	srv := &Server{Client: newFakeClient()}

	form := url.Values{}
	form.Set("name", "")
	form.Set("image", "")
	req := httptest.NewRequest(http.MethodPost, "/apps/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppCreate(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleAppCreateUpdateDelete(t *testing.T) {
	ctx := context.Background()
	c := newFakeClient()
	srv := &Server{Client: c}

	form := url.Values{}
	form.Set("name", testAppName)
	form.Set("workspace", "dev")
	form.Set("image", "nginx:alpine")
	form.Set("port", "8080")
	req := httptest.NewRequest(http.MethodPost, "/apps/create", strings.NewReader(form.Encode()))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppCreate(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	var app geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app))

	updateForm := url.Values{}
	updateForm.Set("workspace", "staging")
	updateForm.Set("image", "nginx:1.25")
	updateForm.Set("port", "9090")
	upReq := httptest.NewRequest(http.MethodPost, "/apps/demo/update", strings.NewReader(updateForm.Encode()))
	upReq = upReq.WithContext(ctx)
	upReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	upRec := httptest.NewRecorder()
	srv.handleAppUpdate(upRec, upReq, testAppName)
	require.Equal(t, http.StatusSeeOther, upRec.Code)

	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app))
	require.Equal(t, geassv1alpha1.WorkspaceStaging, app.Spec.Workspace)
	require.Equal(t, "nginx:1.25", app.Spec.Image)

	delForm := url.Values{}
	delForm.Set("_method", "DELETE")
	delReq := httptest.NewRequest(http.MethodPost, "/apps/demo", strings.NewReader(delForm.Encode()))
	delReq = delReq.WithContext(ctx)
	delReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	delRec := httptest.NewRecorder()
	srv.handleAppRoutes(delRec, delReq)
	require.Equal(t, http.StatusSeeOther, delRec.Code)

	err := c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app)
	require.Error(t, err)
}

func TestHandleDatabaseCRUD(t *testing.T) {
	ctx := context.Background()
	c := newFakeClient()
	srv := &Server{Client: c}

	form := url.Values{}
	form.Set("name", "orders")
	form.Set("workspace", "dev")
	req := httptest.NewRequest(http.MethodPost, "/databases/create", strings.NewReader(form.Encode()))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleDatabaseCreate(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	detailReq := httptest.NewRequest(http.MethodGet, "/databases/orders", nil)
	detailReq = detailReq.WithContext(ctx)
	detailRec := httptest.NewRecorder()
	srv.handleDatabaseRoutes(detailRec, detailReq)
	require.Equal(t, http.StatusOK, detailRec.Code)
	require.Contains(t, detailRec.Body.String(), "orders")

	delForm := url.Values{}
	delForm.Set("_method", "DELETE")
	delReq := httptest.NewRequest(http.MethodPost, "/databases/orders", strings.NewReader(delForm.Encode()))
	delReq = delReq.WithContext(ctx)
	delReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	delRec := httptest.NewRecorder()
	srv.handleDatabaseRoutes(delRec, delReq)
	require.Equal(t, http.StatusSeeOther, delRec.Code)
}

func TestHandleAppConfigAndSecrets(t *testing.T) {
	ctx := context.Background()
	c := newFakeClient()
	srv := &Server{Client: c}

	form := url.Values{}
	form.Set("name", testAppName)
	form.Set("workspace", "dev")
	form.Set("image", "nginx:alpine")
	req := httptest.NewRequest(http.MethodPost, "/apps/create", strings.NewReader(form.Encode()))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAppCreate(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	cfgForm := url.Values{}
	cfgForm.Set("key", "LOG_LEVEL")
	cfgForm.Set("value", "debug")
	cfgReq := httptest.NewRequest(http.MethodPost, "/apps/demo/config/set", strings.NewReader(cfgForm.Encode()))
	cfgReq = cfgReq.WithContext(ctx)
	cfgReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cfgReq.Header.Set(hxRequestHeader, hxRequestTrue)
	cfgRec := httptest.NewRecorder()
	srv.handleAppConfigSet(cfgRec, cfgReq, testAppName)
	require.Equal(t, http.StatusOK, cfgRec.Code)
	require.Contains(t, cfgRec.Body.String(), "LOG_LEVEL")
	require.Contains(t, cfgRec.Body.String(), "debug")

	var app geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app))
	require.Equal(t, "debug", app.Spec.ConfigData["LOG_LEVEL"])

	secForm := url.Values{}
	secForm.Set("key", "API_TOKEN")
	secForm.Set("value", "secret-value")
	secReq := httptest.NewRequest(http.MethodPost, "/apps/demo/secrets/set", strings.NewReader(secForm.Encode()))
	secReq = secReq.WithContext(ctx)
	secReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	secReq.Header.Set(hxRequestHeader, hxRequestTrue)
	secRec := httptest.NewRecorder()
	srv.handleAppSecretSet(secRec, secReq, testAppName)
	require.Equal(t, http.StatusOK, secRec.Code)
	require.Contains(t, secRec.Body.String(), "API_TOKEN")
	require.NotContains(t, secRec.Body.String(), "secret-value")

	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app))
	require.Equal(t, "secret-value", app.Spec.SecretData["API_TOKEN"])

	delCfg := url.Values{}
	delCfg.Set("key", "LOG_LEVEL")
	delCfgReq := httptest.NewRequest(http.MethodPost, "/apps/demo/config/delete", strings.NewReader(delCfg.Encode()))
	delCfgReq = delCfgReq.WithContext(ctx)
	delCfgReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	delCfgReq.Header.Set(hxRequestHeader, hxRequestTrue)
	delCfgRec := httptest.NewRecorder()
	srv.handleAppConfigDelete(delCfgRec, delCfgReq, testAppName)
	require.Equal(t, http.StatusOK, delCfgRec.Code)

	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testAppName, Namespace: platform.SystemNamespace}, &app))
	require.Nil(t, app.Spec.ConfigData)
}

func TestHandleClusterOverviewWithMetrics(t *testing.T) {
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{
			Version:   "v1",
			ServerURL: "https://127.0.0.1:6443",
		},
		Status: geassv1alpha1.GeassClusterStatus{
			Conditions: []metav1.Condition{{
				Type:   platform.ConditionAddonsReady,
				Status: metav1.ConditionTrue,
			}},
		},
	}
	srv := &Server{
		Client: newFakeClient(cluster),
		Metrics: &fakeMetrics{values: map[string]string{
			`count(kube_node_info)`: "3",
		}},
	}

	req := httptest.NewRequest(http.MethodGet, "/cluster", nil)
	rec := httptest.NewRecorder()
	srv.handleClusterOverview(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	body, _ := io.ReadAll(rec.Body)
	require.Contains(t, string(body), "default")
	require.Contains(t, string(body), "Nodes")
	require.Contains(t, string(body), "3")
}

func TestPrometheusClientParse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[{"value":[1,"12.5"]}]}}`))
	}))
	defer srv.Close()

	pc := &PrometheusClient{BaseURL: srv.URL}
	val, err := pc.QueryInstant(context.Background(), "up")
	require.NoError(t, err)
	require.Equal(t, "12.50", val)
}
