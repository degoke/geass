package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func wholeProjectOf(t *testing.T, srv *Server) geassv1alpha1.GeassProject {
	t.Helper()
	var project geassv1alpha1.GeassProject
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &project))
	return project
}

func requireAmount(t *testing.T, got *resource.Quantity, want string) {
	t.Helper()
	require.NotNil(t, got)
	require.Zero(t, got.Cmp(resource.MustParse(want)), "want %s, was %s", want, got.String())
}

func TestAProjectIsUncappedUntilSomeoneSetsAQuota(t *testing.T) {
	srv := &Server{Client: newFakeClient(networkTestProject(testProjectName))}
	project := wholeProjectOf(t, srv)
	require.Nil(t, project.Spec.EnvironmentQuota)
	require.Nil(t, project.Spec.ProjectQuota)
}

func TestTheQuotaIsSavedAsSent(t *testing.T) {
	srv := &Server{Client: newFakeClient(networkTestProject(testProjectName))}

	rec := apiPost(t, srv, "/projects/"+testProjectName+"/quota", url.Values{
		"cpu": {"4"}, "memory": {" 8Gi "}, "storage": {"50Gi"}, "pods": {"20"},
		"projectCpu": {"10"}, "projectMemory": {"32Gi"}, "projectStorage": {"200Gi"}, "projectPods": {"60"},
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	project := wholeProjectOf(t, srv)
	environment := project.Spec.EnvironmentQuota
	requireAmount(t, environment.CPU, "4")
	requireAmount(t, environment.Memory, "8Gi")
	requireAmount(t, environment.Storage, "50Gi")
	require.Equal(t, int32(20), *environment.Pods)
	total := project.Spec.ProjectQuota
	requireAmount(t, total.CPU, "10")
	requireAmount(t, total.Memory, "32Gi")
	requireAmount(t, total.Storage, "200Gi")
	require.Equal(t, int32(60), *total.Pods)
}

func TestALimitThatIsNotSentStaysAndOneSentEmptyIsRemoved(t *testing.T) {
	srv := &Server{Client: newFakeClient(networkTestProject(testProjectName))}
	path := "/projects/" + testProjectName + "/quota"
	require.Equal(t, http.StatusOK, apiPost(t, srv, path, url.Values{"cpu": {"4"}, "memory": {"8Gi"}, "projectCpu": {"10"}}).Code)

	require.Equal(t, http.StatusOK, apiPost(t, srv, path, url.Values{"cpu": {"2"}}).Code)
	project := wholeProjectOf(t, srv)
	requireAmount(t, project.Spec.EnvironmentQuota.CPU, "2")
	requireAmount(t, project.Spec.EnvironmentQuota.Memory, "8Gi")
	requireAmount(t, project.Spec.ProjectQuota.CPU, "10")

	require.Equal(t, http.StatusOK, apiPost(t, srv, path, url.Values{"memory": {""}}).Code)
	project = wholeProjectOf(t, srv)
	require.Nil(t, project.Spec.EnvironmentQuota.Memory, "a limit sent empty is taken away")
	requireAmount(t, project.Spec.EnvironmentQuota.CPU, "2")
	requireAmount(t, project.Spec.ProjectQuota.CPU, "10")

	require.Equal(t, http.StatusOK, apiPost(t, srv, path, url.Values{}).Code)
	project = wholeProjectOf(t, srv)
	requireAmount(t, project.Spec.EnvironmentQuota.CPU, "2")
	requireAmount(t, project.Spec.ProjectQuota.CPU, "10")
}

func TestEmptyingEveryLimitRemovesTheQuotaOfThatKind(t *testing.T) {
	srv := &Server{Client: newFakeClient(networkTestProject(testProjectName))}
	path := "/projects/" + testProjectName + "/quota"
	require.Equal(t, http.StatusOK, apiPost(t, srv, path, url.Values{"cpu": {"4"}, "projectCpu": {"10"}}).Code)

	require.Equal(t, http.StatusOK, apiPost(t, srv, path, url.Values{"cpu": {""}, "memory": {""}, "storage": {""}, "pods": {""}}).Code)
	project := wholeProjectOf(t, srv)
	require.Nil(t, project.Spec.EnvironmentQuota, "there is one way to turn a quota off, and no way to cap a project at zero")
	requireAmount(t, project.Spec.ProjectQuota.CPU, "10")

	require.Equal(t, http.StatusOK, apiPost(t, srv, path, url.Values{"projectCpu": {""}}).Code)
	require.Nil(t, wholeProjectOf(t, srv).Spec.ProjectQuota)
}

func TestTheQuotaRefusesWhatItCannotHonour(t *testing.T) {
	srv := &Server{Client: newFakeClient(networkTestProject(testProjectName))}

	for name, tt := range map[string]struct {
		form url.Values
		want string
	}{
		"not an amount":           {url.Values{"cpu": {"lots"}}, "CPU limit must be an amount above zero"},
		"zero":                    {url.Values{"memory": {"0"}}, "memory limit must be an amount above zero"},
		"negative":                {url.Values{"storage": {"-5Gi"}}, "storage limit must be an amount above zero"},
		"pods not a count":        {url.Values{"pods": {"many"}}, "pod limit must be a whole number from 1 to"},
		"no pods":                 {url.Values{"pods": {"0"}}, "pod limit must be a whole number from 1 to"},
		"too many pods":           {url.Values{"pods": {"100001"}}, "pod limit must be a whole number from 1 to"},
		"a fraction":              {url.Values{"pods": {"2.5"}}, "pod limit must be a whole number from 1 to"},
		"project amount":          {url.Values{"projectCpu": {"lots"}}, "project CPU limit must be an amount above zero"},
		"project zero":            {url.Values{"projectMemory": {"0"}}, "project memory limit must be an amount above zero"},
		"project pods":            {url.Values{"projectPods": {"0"}}, "project pod limit must be a whole number from 1 to"},
		"one bad among good ones": {url.Values{"cpu": {"4"}, "projectStorage": {"-1"}}, "project storage limit must be an amount above zero"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := apiPost(t, srv, "/projects/"+testProjectName+"/quota", tt.form)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, errorOf(t, rec), tt.want)
			project := wholeProjectOf(t, srv)
			require.Nil(t, project.Spec.EnvironmentQuota, "a refused change leaves the project as it was, whatever else the request said")
			require.Nil(t, project.Spec.ProjectQuota)
		})
	}
}

func TestAQuotaForAMissingProjectIsNotFound(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	require.Equal(t, http.StatusNotFound, apiPost(t, srv, "/projects/nope/quota", url.Values{"cpu": {"1"}}).Code)
}

func TestAProjectTokenCannotRaiseItsOwnLimit(t *testing.T) {
	f := newTokenFixture(t)
	token := f.projectToken(t, "production")

	rec := f.do(bearer(token, http.MethodPost, "/api/projects/"+testProjectName+"/quota", url.Values{"cpu": {"1000"}, "projectCpu": {"1000"}}))

	require.Equal(t, http.StatusForbidden, rec.Code, "a deploy token must not be able to lift the cap on its own project")
	project := wholeProjectOf(t, f.srv)
	require.Nil(t, project.Spec.EnvironmentQuota)
	require.Nil(t, project.Spec.ProjectQuota)
}

func TestTheQuotaIsInTheAPIDescriptionForAdminsToChangeAndAnyoneToRead(t *testing.T) {
	levels := map[string]string{}
	for _, operation := range v1Operations {
		if operation.Path == "/projects/{name}/quota" {
			levels[operation.Method] = operation.Access
			if operation.Method == http.MethodPost {
				require.Len(t, operation.Fields, 8)
			}
		}
	}
	require.Equal(t, map[string]string{http.MethodPost: accessAdmin, http.MethodGet: accessViewer}, levels,
		"the endpoints are listed, so the CLI and the reference describe them")
}

func TestTheQuotaIsReadBackAsSet(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.workspaceToken(t, "admin")
	viewer := f.workspaceToken(t, "viewer")

	empty := f.do(bearer(viewer, http.MethodGet, "/api/v1/projects/"+testProjectName+"/quota", nil))
	require.Equal(t, http.StatusOK, empty.Code, empty.Body.String())
	require.JSONEq(t, `{}`, empty.Body.String(), "a project with no limits has none to show")

	set := f.do(bearer(admin, http.MethodPost, "/api/v1/projects/"+testProjectName+"/quota", nil))
	require.NotEqual(t, http.StatusNotFound, set.Code)
	require.Equal(t, http.StatusOK, f.do(bearer(admin, http.MethodPost, "/api/projects/"+testProjectName+"/quota",
		url.Values{"cpu": {"4"}, "projectCpu": {"10"}, "projectMemory": {"32Gi"}, "projectPods": {"60"}})).Code)

	rec := f.do(bearer(viewer, http.MethodGet, "/api/v1/projects/"+testProjectName+"/quota", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"environment":{"cpu":"4"},"project":{"cpu":"10","memory":"32Gi","pods":60}}`, rec.Body.String())
}

func TestTheQuotaReadShowsWhatTheEnvironmentsUse(t *testing.T) {
	project := networkTestProject(testProjectName)
	cpu, pods := resource.MustParse("3500m"), int32(12)
	project.Status.QuotaUsage = &geassv1alpha1.GeassQuotaUsage{CPU: &cpu, Pods: &pods}
	srv := &Server{Client: newFakeClient(project)}

	rec := httptest.NewRecorder()
	srv.handleAPIProjectQuota(rec, httptest.NewRequest(http.MethodGet, "/api/projects/"+testProjectName+"/quota", nil), testProjectName)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"used":{"cpu":"3500m","pods":12}}`, rec.Body.String())
}

func TestTheQuotaOfAMissingProjectIsNotFound(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	rec := httptest.NewRecorder()
	srv.handleAPIProjectQuota(rec, httptest.NewRequest(http.MethodGet, "/api/projects/nope/quota", nil), "nope")
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAProjectTokenCannotReadTheLimitsEither(t *testing.T) {
	f := newTokenFixture(t)
	token := f.projectToken(t, "production")
	for _, path := range []string{"/quota", "/network"} {
		rec := f.do(bearer(token, http.MethodGet, "/api/v1/projects/"+testProjectName+path, nil))
		require.Equal(t, http.StatusForbidden, rec.Code, "%s: a token for one environment has no business reading the project's settings", path)
	}
}
