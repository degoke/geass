package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func complianceTag(tag string) func(*corev1.Node) {
	return func(node *corev1.Node) {
		node.Labels[platform.TagKey(tag)] = "true"
		node.Spec.Taints = append(node.Spec.Taints, corev1.Taint{Key: platform.TagKey(tag), Value: platform.ComplianceTaintValue, Effect: corev1.TaintEffectNoSchedule})
	}
}

func requiredTagsOf(t *testing.T, srv *Server) []string {
	t.Helper()
	var project geassv1alpha1.GeassProject
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &project))
	return project.Spec.RequiredTags
}

func compliancePost(t *testing.T, srv *Server, tags ...string) (int, string) {
	t.Helper()
	rec := apiPost(t, srv, "/projects/"+testProjectName+"/compliance", url.Values{"requiredTags": tags})
	return rec.Code, rec.Body.String()
}

func TestAProjectCanRequireTagsThatExistAndAreReserved(t *testing.T) {
	srv := &Server{Client: newFakeClient(
		placementProject("eu"), placementCluster(),
		regionNode("a", "eu", complianceTag("hipaa")), regionNode("b", "eu"),
	)}

	code, body := compliancePost(t, srv, "HIPAA, hipaa")

	require.Equal(t, http.StatusOK, code, body)
	require.Equal(t, []string{"hipaa"}, requiredTagsOf(t, srv), "tags are lowercased and deduplicated")
}

func TestAProjectCanStopRequiringTags(t *testing.T) {
	project := placementProject("eu")
	project.Spec.RequiredTags = []string{"hipaa"}
	srv := &Server{Client: newFakeClient(project, placementCluster(), regionNode("a", "eu", complianceTag("hipaa")))}

	code, body := compliancePost(t, srv)

	require.Equal(t, http.StatusOK, code, body)
	require.Empty(t, requiredTagsOf(t, srv))
}

func TestRequiredTagsMustExistInTheProjectsRegion(t *testing.T) {
	srv := &Server{Client: newFakeClient(
		placementProject("eu"), placementCluster(),
		regionNode("a", "us", complianceTag("hipaa")), regionNode("b", "eu"),
	)}

	code, body := compliancePost(t, srv, "hipaa")

	require.Equal(t, http.StatusBadRequest, code, body)
	require.Contains(t, body, "No node in the eu region is tagged hipaa")
	require.Empty(t, requiredTagsOf(t, srv))
}

func TestRequiredTagsMustBeReservedOnEveryNodeThatCarriesThem(t *testing.T) {
	srv := &Server{Client: newFakeClient(
		placementProject("eu"), placementCluster(),
		regionNode("open", "eu", tagged("hipaa", false)), regionNode("closed", "eu", complianceTag("hipaa")),
	)}

	code, body := compliancePost(t, srv, "hipaa")

	require.Equal(t, http.StatusBadRequest, code, body)
	require.Contains(t, body, "open", "the node that would let other work in is named")
	require.NotContains(t, body, "closed")
	require.Empty(t, requiredTagsOf(t, srv))
}

func TestRequiredTagsAreLimitedAndValidated(t *testing.T) {
	srv := &Server{Client: newFakeClient(placementProject("eu"), placementCluster(), regionNode("a", "eu"))}

	code, body := compliancePost(t, srv, "a,b,c,d,e,f,g,h,i")
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, body, "at most 8")

	code, _ = compliancePost(t, srv, "bad_tag")
	require.Equal(t, http.StatusBadRequest, code)
}

func TestATagCannotBeRequiredWhileADatabaseKeepsItsDataElsewhere(t *testing.T) {
	provisioned := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Engine: geassv1alpha1.DatabaseEnginePostgres},
		Status:     geassv1alpha1.GeassDatabaseStatus{TargetNamespace: "shop-production"},
	}
	onHipaa := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "ledger", Namespace: platform.SystemNamespace, Annotations: map[string]string{
			platform.AnnotationNodePlacement: platform.NewPlacement("eu", nil).WithRequired([]string{"hipaa"}).Encode(),
		}},
		Spec:   geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Engine: geassv1alpha1.DatabaseEnginePostgres},
		Status: geassv1alpha1.GeassDatabaseStatus{TargetNamespace: "shop-production"},
	}
	notYetProvisioned := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "draft", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Engine: geassv1alpha1.DatabaseEnginePostgres},
	}
	nodes := []client.Object{placementProject("eu"), placementCluster(), regionNode("a", "eu", complianceTag("hipaa"))}

	t.Run("one on other nodes blocks it", func(t *testing.T) {
		srv := &Server{Client: newFakeClient(append(nodes, provisioned, onHipaa, notYetProvisioned)...)}
		code, body := compliancePost(t, srv, "hipaa")

		require.Equal(t, http.StatusBadRequest, code, body)
		require.Contains(t, body, "orders")
		require.NotContains(t, body, "ledger", "a database already on the required nodes is fine")
		require.NotContains(t, body, "draft", "a database not yet provisioned will be placed with the tags")
		require.Empty(t, requiredTagsOf(t, srv))
	})
	t.Run("databases already on the nodes do not", func(t *testing.T) {
		srv := &Server{Client: newFakeClient(append(nodes, onHipaa, notYetProvisioned)...)}
		code, body := compliancePost(t, srv, "hipaa")

		require.Equal(t, http.StatusOK, code, body)
	})
}

func TestAProjectTokenCannotChangeRequiredTags(t *testing.T) {
	f := newTokenFixture(t)
	token := f.projectToken(t, "production")

	rec := f.do(bearer(token, http.MethodPost, "/api/projects/"+testProjectName+"/compliance", url.Values{"requiredTags": {"hipaa"}}))

	require.Equal(t, http.StatusForbidden, rec.Code, "a deploy token must not be able to move a project's data")
	require.Empty(t, requiredTagsOf(t, f.srv))
}

func TestComplianceTagsAreAlwaysReservedOnTheNode(t *testing.T) {
	srv := &Server{Client: newFakeClient(regionNode("worker-1", ""))}

	rec := apiPost(t, srv, "/nodes/worker-1/placement", url.Values{
		"region": {"eu"}, "tags": {"hipaa,gpu"}, "compliance": {"hipaa"},
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	node := getNode(t, srv.Client, "worker-1")
	require.Equal(t, []corev1.Taint{{Key: platform.TagKey("hipaa"), Value: platform.ComplianceTaintValue, Effect: corev1.TaintEffectNoSchedule}}, node.Spec.Taints, "marking a tag compliance reserves it without a second switch")
	require.Contains(t, rec.Body.String(), "now a compliance tag", "restarting what already runs there is the admin's call, so they are told")
}

func TestATagThatAProjectRequiresIsForcedToBeAComplianceTag(t *testing.T) {
	project := placementProject("eu")
	project.Spec.RequiredTags = []string{"hipaa"}
	srv := &Server{Client: newFakeClient(project, regionNode("worker-1", "eu"))}

	rec := apiPost(t, srv, "/nodes/worker-1/placement", url.Values{"region": {"eu"}, "tags": {"hipaa"}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	node := getNode(t, srv.Client, "worker-1")
	require.Equal(t, platform.ComplianceTaintValue, node.Spec.Taints[0].Value, "forgetting the switch must not leave a required node open to other work")
}

func TestComplianceFlagNeedsTheTag(t *testing.T) {
	srv := &Server{Client: newFakeClient(regionNode("worker-1", ""))}
	rec := apiPost(t, srv, "/nodes/worker-1/placement", url.Values{"tags": {"gpu"}, "compliance": {"hipaa"}})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestRemovingTheLastNodeWithARequiredTagWarns(t *testing.T) {
	project := placementProject("eu")
	project.Spec.RequiredTags = []string{"hipaa"}
	srv := &Server{Client: newFakeClient(project, placementCluster(), regionNode("worker-1", "eu", complianceTag("hipaa")))}

	rec := apiPost(t, srv, "/nodes/worker-1/placement", url.Values{"region": {"eu"}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "no other node carries it")
}

func TestTheComplianceReportListsDatabasesThatFallShort(t *testing.T) {
	project := placementProject("eu")
	project.Spec.RequiredTags = []string{"hipaa"}
	pinned := func(name string, placement platform.Placement) *geassv1alpha1.GeassDatabase {
		return &geassv1alpha1.GeassDatabase{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Annotations: map[string]string{platform.AnnotationNodePlacement: placement.Encode()}},
			Spec:       geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Engine: geassv1alpha1.DatabaseEnginePostgres},
		}
	}
	legacy := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Engine: geassv1alpha1.DatabaseEnginePostgres},
		Status:     geassv1alpha1.GeassDatabaseStatus{TargetNamespace: "shop-production"},
	}
	draft := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "draft", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Engine: geassv1alpha1.DatabaseEnginePostgres},
	}
	srv := &Server{Client: newFakeClient(
		project, placementCluster(), regionNode("open", "eu", tagged("hipaa", false)),
		legacy, draft, pinned("ledger", platform.NewPlacement("eu", nil).WithRequired([]string{"hipaa"})),
	)}

	rec := httptest.NewRecorder()
	srv.handleAPIProjectCompliance(rec, httptest.NewRequest(http.MethodGet, "/api/projects/"+testProjectName+"/compliance", nil), testProjectName)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var report struct {
		RequiredTags []string             `json:"requiredTags"`
		Region       string               `json:"region"`
		Databases    []complianceDatabase `json:"databases"`
		OpenNodes    []string             `json:"openNodes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
	require.Equal(t, []string{"hipaa"}, report.RequiredTags)
	require.Equal(t, "eu", report.Region)
	require.Equal(t, []complianceDatabase{
		{Name: "draft", Compliant: true},
		{Name: "ledger", Compliant: true},
		{Name: "legacy", Compliant: false, Missing: []string{"hipaa"}},
	}, report.Databases)
	require.Equal(t, []string{"open"}, report.OpenNodes, "a node that carries the tag without being reserved is called out")
}

func TestTheComplianceReportForAMissingProjectIsNotFound(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	rec := httptest.NewRecorder()
	srv.handleAPIProjectCompliance(rec, httptest.NewRequest(http.MethodGet, "/api/projects/ghost/compliance", nil), "ghost")
	require.Equal(t, http.StatusNotFound, rec.Code)
}
