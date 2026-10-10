package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Applying changes that do not fit the cluster must answer once, with the capacity message.
func TestApplyChangesAnswersOnceWhenTheClusterIsFull(t *testing.T) {
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: []string{"production"}},
	}
	draft := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine"),
		},
	}
	// No nodes report capacity, so nothing fits.
	srv := &Server{Client: newFakeClient(project, draft)}

	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, postForm("/projects/"+testProjectName+"/deploy", url.Values{"environment": {"production"}}))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	decoder := json.NewDecoder(strings.NewReader(rec.Body.String()))
	var first map[string]string
	require.NoError(t, decoder.Decode(&first))
	require.NotEmpty(t, first["error"])
	require.False(t, decoder.More(), "a second JSON document was written after the first: %q", rec.Body.String())
}
