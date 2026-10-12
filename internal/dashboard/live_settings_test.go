package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// deployedService is a running service whose last deploy is recorded, as the dashboard leaves it.
func deployedService(t *testing.T) *geassv1alpha1.GeassApp {
	t.Helper()
	one := int32(1)
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: "production", Port: 8080, Replicas: &one,
			Source:  imageAppSource("nginx:alpine"),
			Ingress: geassv1alpha1.GeassAppIngressSpec{Host: "api.example.com"},
			Deploy:  geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
	}
	require.NoError(t, platform.SetLastDeployedSpec(app))
	return app
}

type settingsResult struct {
	Applied     string   `json:"applied"`
	LiveChanged bool     `json:"liveChanged"`
	Pending     []string `json:"pending"`
}

func saveAppSettings(t *testing.T, srv *Server, form url.Values) settingsResult {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleAppRoutes(rec, postForm("/apps/api/update", form))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var result settingsResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result), rec.Body.String())
	return result
}

func loadApp(t *testing.T, srv *Server) geassv1alpha1.GeassApp {
	t.Helper()
	var app geassv1alpha1.GeassApp
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: "api", Namespace: platform.SystemNamespace}, &app))
	return app
}

func TestLiveSettingsSaveWithoutBecomingPending(t *testing.T) {
	srv := &Server{Client: newFakeClient(deployedService(t))}

	result := saveAppSettings(t, srv, url.Values{"host": {"shop.example.com"}, "replicas": {"3"}, "metrics": {"on"}})

	require.Equal(t, settingsLive, result.Applied)
	require.True(t, result.LiveChanged)
	require.Empty(t, result.Pending)
	app := loadApp(t, srv)
	require.Equal(t, "shop.example.com", app.Spec.Ingress.Host)
	require.Equal(t, int32(3), *app.Spec.Replicas)
	require.True(t, app.Spec.Metrics.Enabled)
	require.Empty(t, app.Annotations[platform.AppPendingChangesAnnotation])
}

func TestSettingThatReplacesPodsWaitsForADeploy(t *testing.T) {
	srv := &Server{Client: newFakeClient(deployedService(t))}

	result := saveAppSettings(t, srv, url.Values{"port": {"9090"}})

	require.Equal(t, settingsNeedDeploy, result.Applied)
	require.False(t, result.LiveChanged)
	require.Equal(t, []string{pendingChangeSettings}, result.Pending)
	require.Equal(t, int32(9090), loadApp(t, srv).Spec.Port)
}

func TestMixedSaveAppliesTheLivePartAndHoldsTheRest(t *testing.T) {
	srv := &Server{Client: newFakeClient(deployedService(t))}

	result := saveAppSettings(t, srv, url.Values{"port": {"9090"}, "replicas": {"4"}})

	require.Equal(t, settingsNeedDeploy, result.Applied)
	require.True(t, result.LiveChanged, "the replica count is already in effect")
	require.Equal(t, []string{pendingChangeSettings}, result.Pending)
}

func TestLiveEditWhileADeployIsPendingKeepsItPending(t *testing.T) {
	srv := &Server{Client: newFakeClient(deployedService(t))}
	saveAppSettings(t, srv, url.Values{"port": {"9090"}})

	result := saveAppSettings(t, srv, url.Values{"replicas": {"2"}})

	require.Equal(t, settingsLive, result.Applied)
	require.Equal(t, []string{pendingChangeSettings}, result.Pending, "the port change is still waiting")
	require.Equal(t, "1", loadApp(t, srv).Annotations[platform.AppPendingUpdatesAnnotation], "and is not counted twice")
}

func TestPuttingASettingBackClearsPending(t *testing.T) {
	srv := &Server{Client: newFakeClient(deployedService(t))}
	saveAppSettings(t, srv, url.Values{"port": {"9090"}})

	result := saveAppSettings(t, srv, url.Values{"port": {"8080"}})

	require.Empty(t, result.Pending, "the service is exactly as deployed again")
	require.Empty(t, loadApp(t, srv).Annotations[platform.AppPendingChangesAnnotation])
}

func TestSavingAnUnchangedFormWritesNothingAndIsNotPending(t *testing.T) {
	srv := &Server{Client: newFakeClient(deployedService(t))}
	before := loadApp(t, srv)

	result := saveAppSettings(t, srv, url.Values{"port": {"8080"}, "replicas": {"1"}, "host": {"api.example.com"}})

	require.Equal(t, settingsUnchanged, result.Applied)
	require.Empty(t, result.Pending)
	require.Equal(t, before.ResourceVersion, loadApp(t, srv).ResourceVersion, "nothing was written")
}

func TestVariablesStillNeedADeploy(t *testing.T) {
	app := deployedService(t)
	srv := &Server{Client: newFakeClient(app)}
	platform.MarkPendingChange(&app.ObjectMeta, platform.PendingChangeVariables)
	require.NoError(t, srv.Client.Update(context.Background(), app))

	result := saveAppSettings(t, srv, url.Values{"replicas": {"2"}})

	require.Equal(t, []string{pendingChangeVariables}, result.Pending, "variables are untouched by a live edit")
}

func TestDatabaseBackupEditIsLiveAndRevertingARolloutEditClearsPending(t *testing.T) {
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-db", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: "production", Engine: geassv1alpha1.DatabaseEnginePostgres,
			Placement: geassv1alpha1.DatabasePlacementInCluster, Version: "16", Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true},
		},
	}
	require.NoError(t, platform.SetLastDeployedJSON(&db.ObjectMeta, db.Spec))
	srv := &Server{Client: newFakeClient(append(backupObjects(), db)...)}

	require.Equal(t, http.StatusOK, apiPost(t, srv, "/databases/orders-db/update", url.Values{"backups": {"on"}}).Code)
	saved, _ := getDatabase(t, srv.Client, "orders-db")
	require.Empty(t, saved.Annotations[platform.PendingChangesAnnotation])

	require.Equal(t, http.StatusOK, apiPost(t, srv, "/databases/orders-db/update", url.Values{"version": {"17"}}).Code)
	saved, _ = getDatabase(t, srv.Client, "orders-db")
	require.Equal(t, platform.PendingChangeSettings, saved.Annotations[platform.PendingChangesAnnotation])

	require.Equal(t, http.StatusOK, apiPost(t, srv, "/databases/orders-db/update", url.Values{"version": {"16"}}).Code)
	saved, _ = getDatabase(t, srv.Client, "orders-db")
	require.Empty(t, saved.Annotations[platform.PendingChangesAnnotation], "version is back to what is deployed")
}
