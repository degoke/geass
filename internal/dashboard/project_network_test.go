package dashboard

import (
	"context"
	"encoding/json"
	"io"
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

func TestBlockingTheClustersMachinesIsOffUntilChosenAndSurvivesOtherChanges(t *testing.T) {
	srv := &Server{Client: newFakeClient(networkTestProject(testProjectName), networkTestProject("web"))}
	path := "/projects/" + testProjectName + "/network"

	require.Equal(t, http.StatusOK, apiPost(t, srv, path, url.Values{"isolation": {"Project"}}).Code)
	require.False(t, projectNetworkOf(t, srv).BlockNodeAddresses, "off unless someone turns it on")

	require.Equal(t, http.StatusOK, apiPost(t, srv, path, url.Values{"isolation": {"Project"}, "blockNodeAddresses": {"true"}}).Code)
	require.True(t, projectNetworkOf(t, srv).BlockNodeAddresses)

	require.Equal(t, http.StatusOK, apiPost(t, srv, path, url.Values{"isolation": {"Project"}, "allowFromProjects": {"web"}}).Code)
	network := projectNetworkOf(t, srv)
	require.True(t, network.BlockNodeAddresses, "a request that only changes who may call in must not switch the block off: older clients do not send it")
	require.Equal(t, []string{"web"}, network.AllowFromProjects)

	require.Equal(t, http.StatusOK, apiPost(t, srv, path, url.Values{"isolation": {"None"}}).Code)
	require.True(t, projectNetworkOf(t, srv).BlockNodeAddresses, "opening a project to the cluster says nothing about the machines it runs on")

	require.Equal(t, http.StatusOK, apiPost(t, srv, path, url.Values{"isolation": {"Project"}, "blockNodeAddresses": {"false"}}).Code)
	require.False(t, projectNetworkOf(t, srv).BlockNodeAddresses)
}

func TestTheNodeBlockMustBeTrueOrFalse(t *testing.T) {
	srv := &Server{Client: newFakeClient(networkTestProject(testProjectName))}

	rec := apiPost(t, srv, "/projects/"+testProjectName+"/network", url.Values{"isolation": {"Project"}, "blockNodeAddresses": {"maybe"}})

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, errorOf(t, rec), "must be true or false")
	require.Nil(t, projectNetworkOf(t, srv))
}

func TestANewProjectIsIsolatedAndKeptAwayFromTheMachines(t *testing.T) {
	srv := &Server{Client: newFakeClient(placementCluster())}

	rec := apiPost(t, srv, "/projects/create", url.Values{})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	var project geassv1alpha1.GeassProject
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: created["project"], Namespace: platform.SystemNamespace}, &project))

	require.NotNil(t, project.Spec.Network)
	require.Equal(t, geassv1alpha1.NetworkIsolationProject, project.Spec.Network.Isolation)
	require.True(t, project.Spec.Network.BlockNodeAddresses, "the block is on for what is created from now on, and is a choice a person can turn off")
}

func TestAProjectThatExistedBeforeTheBlockIsNotBlockedByIt(t *testing.T) {
	existing := networkTestProject(testProjectName)
	require.False(t, geassv1alpha1.ProjectBlocksNodeAddresses(existing), "no stored project gains the block by being read after an update")
	existing.Spec.Network = &geassv1alpha1.GeassProjectNetwork{Isolation: geassv1alpha1.NetworkIsolationProject}
	require.False(t, geassv1alpha1.ProjectBlocksNodeAddresses(existing), "and one that has a network setting but never said so is not blocked either")
}

func TestTheNetworkSettingIsReadBackAsItIs(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.workspaceToken(t, "admin")
	viewer := f.workspaceToken(t, "viewer")
	path := "/api/v1/projects/" + testProjectName + "/network"

	rec := f.do(bearer(viewer, http.MethodGet, path, nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"isolation":"Project","allowFromProjects":[],"blockNodeAddresses":false}`, rec.Body.String(),
		"a project nobody has configured is private and does not block the machines, as the stored object says")

	require.Equal(t, http.StatusOK, f.do(bearer(admin, http.MethodPost, "/api/projects/"+testProjectName+"/network",
		url.Values{"isolation": {"None"}, "blockNodeAddresses": {"true"}})).Code)
	rec = f.do(bearer(viewer, http.MethodGet, path, nil))
	require.JSONEq(t, `{"isolation":"None","allowFromProjects":[],"blockNodeAddresses":true}`, rec.Body.String())
}

func TestTheNetworkOfAMissingProjectIsNotFound(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	rec := httptest.NewRecorder()
	srv.handleAPIProjectNetwork(rec, httptest.NewRequest(http.MethodGet, "/api/projects/nope/network", nil), "nope")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// A program sends JSON, where a boolean is true or false. The handler underneath takes form fields, and a form leaves a
// checkbox out when it is off, so false has to be turned into a word or switching the block off would do nothing.
func TestAJSONBooleanSwitchesTheNodeBlockOnAndOff(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.workspaceToken(t, "admin")
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+testProjectName+"/network", io.NopCloser(strings.NewReader(body)))
		req.Header.Set("Authorization", "Bearer "+admin)
		req.Header.Set("Content-Type", "application/json")
		return f.do(req)
	}
	blocks := func() bool {
		network := wholeProjectOf(t, f.srv).Spec.Network
		return network != nil && network.BlockNodeAddresses
	}

	require.Equal(t, http.StatusOK, post(`{"isolation":"Project","blockNodeAddresses":true}`).Code)
	require.True(t, blocks())
	require.Equal(t, http.StatusOK, post(`{"isolation":"Project","blockNodeAddresses":false}`).Code)
	require.False(t, blocks(), "false switches it off: it is not 'not sent'")

	require.Equal(t, http.StatusOK, post(`{"isolation":"Project","blockNodeAddresses":true}`).Code)
	require.Equal(t, http.StatusOK, post(`{"isolation":"None"}`).Code)
	require.True(t, blocks(), "and a request that says nothing about it still leaves it as it was")

	require.Equal(t, http.StatusOK, post(`{"isolation":"Project","blockNodeAddresses":"false"}`).Code, "the words work too")
	require.False(t, blocks())
	require.Equal(t, http.StatusOK, post(`{"isolation":"Project","blockNodeAddresses":"on"}`).Code, "and so does what a checkbox sends")
	require.True(t, blocks())
}
