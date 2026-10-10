package dashboard

import (
	"context"
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

func scaleApp(srv *Server, replicas string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.handleAppRoutes(rec, postForm("/apps/api/scale", url.Values{"replicas": {replicas}}))
	return rec
}

func scaleFixture() *geassv1alpha1.GeassApp {
	return &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine"),
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
	}
}

func TestScaleRejectsReplicaCountsThatAreNotWholeNumbersInRange(t *testing.T) {
	for _, value := range []string{"3abc", "-1", "1.5", "", "abc", "1001", "4294967297", "2147483648", "99999999999999999999"} {
		app := scaleFixture()
		srv := &Server{Client: newFakeClient(app)}

		rec := scaleApp(srv, value)

		require.Equal(t, http.StatusBadRequest, rec.Code, "%q: %s", value, rec.Body.String())
		var saved geassv1alpha1.GeassApp
		require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKeyFromObject(app), &saved))
		require.Nil(t, saved.Spec.Replicas, "%q must not be saved", value)
	}
}

func TestScaleAcceptsTheLimitsAndSurvivesAControllerWriteConflict(t *testing.T) {
	app := scaleFixture()
	c, conflicts := conflictOnce(t, app)
	srv := &Server{Client: c}

	rec := scaleApp(srv, "1000")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.GreaterOrEqual(t, conflicts.Load(), int32(2), "the first write conflicted and was retried")
	var saved geassv1alpha1.GeassApp
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(app), &saved))
	require.NotNil(t, saved.Spec.Replicas)
	require.Equal(t, int32(1000), *saved.Spec.Replicas)
	require.Equal(t, pendingChangeSettings, saved.Annotations[platform.AppPendingChangesAnnotation])

	rec = scaleApp(srv, "0")
	require.Equal(t, http.StatusOK, rec.Code, "zero replicas pauses the service and is allowed")
}
