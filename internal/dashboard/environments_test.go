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

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// An earlier removal can take the environment out of the project and then fail to delete what was in
// it. Deleting the environment again must finish the job instead of answering "not found".
func TestRemovingAnEnvironmentAgainDeletesWhatAnEarlierAttemptLeftBehind(t *testing.T) {
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: []string{"production"}}, // staging is already gone
	}
	leftover := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api-staging", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "staging", Source: imageAppSource("nginx:alpine")},
	}
	kept := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine")},
	}
	srv := &Server{Client: newFakeClient(project, leftover, kept)}

	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, postForm("/projects/"+testProjectName+"/environments/archive", url.Values{"environment": {"staging"}, "confirmName": {"staging"}}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var reply struct {
		Deleted int `json:"deleted"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &reply))
	require.Equal(t, 1, reply.Deleted)
	_, ok := getApp(t, srv.Client, "api-staging")
	require.False(t, ok, "the leftover service is deleted")
	_, ok = getApp(t, srv.Client, "api")
	require.True(t, ok, "other environments are untouched")

	rec = httptest.NewRecorder()
	srv.handleProjectRoutes(rec, postForm("/projects/"+testProjectName+"/environments/archive", url.Values{"environment": {"staging"}, "confirmName": {"staging"}}))
	require.Equal(t, http.StatusNotFound, rec.Code, "with nothing left, the environment really does not exist")

	_, err := srv.removeEnvironment(context.Background(), testProjectName, "production")
	require.ErrorIs(t, err, errLastEnvironment, "the only environment cannot be removed")
}
