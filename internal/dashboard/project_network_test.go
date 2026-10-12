package dashboard

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func networkTestProject(name string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: []string{"production"}},
	}
}

func projectNetworkOf(t *testing.T, srv *Server) *geassv1alpha1.GeassProjectNetwork {
	t.Helper()
	var project geassv1alpha1.GeassProject
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &project))
	return project.Spec.Network
}

func TestProjectNetworkIsSavedWithTheProjectsItAllows(t *testing.T) {
	srv := &Server{Client: newFakeClient(networkTestProject(testProjectName), networkTestProject("web"), networkTestProject("admin"))}

	rec := apiPost(t, srv, "/projects/"+testProjectName+"/network", url.Values{
		"isolation":         {"Project"},
		"allowFromProjects": {"web", "admin", "web", testProjectName, " "},
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	network := projectNetworkOf(t, srv)
	require.Equal(t, geassv1alpha1.NetworkIsolationProject, network.Isolation)
	require.Equal(t, []string{"admin", "web"}, network.AllowFromProjects, "sorted, with repeats, blanks and the project itself dropped")
}

func TestAProjectCanBeOpenedAndClosedAgain(t *testing.T) {
	srv := &Server{Client: newFakeClient(networkTestProject(testProjectName))}

	require.Equal(t, http.StatusOK, apiPost(t, srv, "/projects/"+testProjectName+"/network", url.Values{"isolation": {"None"}}).Code)
	require.Equal(t, geassv1alpha1.NetworkIsolationNone, projectNetworkOf(t, srv).Isolation)

	require.Equal(t, http.StatusOK, apiPost(t, srv, "/projects/"+testProjectName+"/network", url.Values{"isolation": {"Project"}}).Code)
	require.Equal(t, geassv1alpha1.NetworkIsolationProject, projectNetworkOf(t, srv).Isolation)
}

func TestProjectNetworkRefusesWhatItCannotHonour(t *testing.T) {
	srv := &Server{Client: newFakeClient(networkTestProject(testProjectName))}

	for name, tt := range map[string]struct {
		form url.Values
		want string
	}{
		"no choice":        {url.Values{}, "private or open"},
		"unknown choice":   {url.Values{"isolation": {"Everyone"}}, "private or open"},
		"missing project":  {url.Values{"isolation": {"Project"}, "allowFromProjects": {"ghost"}}, `project "ghost" does not exist`},
		"too many allowed": {tooManyProjects(), "at most 20"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := apiPost(t, srv, "/projects/"+testProjectName+"/network", tt.form)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, errorOf(t, rec), tt.want)
			require.Nil(t, projectNetworkOf(t, srv), "a refused change leaves the project as it was")
		})
	}
}

func tooManyProjects() url.Values {
	form := url.Values{"isolation": {"Project"}}
	for i := range maxAllowedProjects + 1 {
		form.Add("allowFromProjects", "p"+string(rune('a'+i)))
	}
	return form
}

func TestProjectNetworkForAMissingProjectIsNotFound(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	require.Equal(t, http.StatusNotFound, apiPost(t, srv, "/projects/nope/network", url.Values{"isolation": {"Project"}}).Code)
}

func TestAProjectTokenCannotChangeWhoReachesItsServices(t *testing.T) {
	f := newTokenFixture(t)
	token := f.projectToken(t, "production")

	rec := f.do(bearer(token, http.MethodPost, "/api/projects/"+testProjectName+"/network", url.Values{"isolation": {"None"}}))

	require.Equal(t, http.StatusForbidden, rec.Code, "a deploy token must not be able to open its project to the whole cluster")
	require.Nil(t, projectNetworkOf(t, f.srv))
}
