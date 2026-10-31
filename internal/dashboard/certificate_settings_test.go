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

func readCertificates(t *testing.T, srv *Server) certificateSettings {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, asAdmin(httptest.NewRequest(http.MethodGet, "/api/settings/certificates", nil)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out certificateSettings
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func storedACMEEmail(t *testing.T, c client.Client) string {
	t.Helper()
	cluster := &geassv1alpha1.GeassCluster{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "default", Namespace: platform.SystemNamespace}, cluster))
	return cluster.Spec.Addons.CertManager.ACMEEmail
}

func TestTheCertificateEmailIsSavedOnTheCluster(t *testing.T) {
	c := newFakeClient(snapshotCluster(nil))
	srv := &Server{Client: c}
	require.Equal(t, certificateSettings{State: certificateIssuerMissing}, readCertificates(t, srv))

	rec := httptest.NewRecorder()
	srv.handleAPI(rec, postForm("/api/settings/domain/acme-email", url.Values{"email": {" ops@acme.dev "}}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "ops@acme.dev", storedACMEEmail(t, c))
	require.Equal(t, certificateSettings{Email: "ops@acme.dev", State: certificateIssuerWaiting}, readCertificates(t, srv),
		"saved, and the operator has not made the issuer yet")
}

func TestAnAddressTheClusterWouldRefuseIsRefusedFirst(t *testing.T) {
	for _, email := range []string{"", "not-an-address", "two@at@signs.dev", "space in@acme.dev"} {
		c := newFakeClient(snapshotCluster(nil))
		rec := httptest.NewRecorder()
		(&Server{Client: c}).handleAPI(rec, postForm("/api/settings/domain/acme-email", url.Values{"email": {email}}))
		require.NotEqual(t, http.StatusOK, rec.Code, email)
		require.Contains(t, rec.Body.String(), "enter an email address", email)
		require.Empty(t, storedACMEEmail(t, c), email)
	}
}

func TestTheIssuerStateComesFromTheCondition(t *testing.T) {
	for reason, tc := range map[string]struct {
		status metav1.ConditionStatus
		state  string
	}{
		"IssuerReady":           {metav1.ConditionTrue, certificateIssuerReady},
		"IssuerNotReady":        {metav1.ConditionFalse, certificateIssuerFailing},
		"CertManagerAbsent":     {metav1.ConditionUnknown, certificateIssuerWaiting},
		"WaitingForCertManager": {metav1.ConditionFalse, certificateIssuerWaiting},
		"IssuerRegistering":     {metav1.ConditionFalse, certificateIssuerWaiting},
		"ACMEEmailMissing":      {metav1.ConditionFalse, certificateIssuerFailing},
	} {
		cluster := snapshotCluster(nil)
		cluster.Spec.Addons.CertManager.ACMEEmail = "ops@acme.dev"
		cluster.Status.Conditions = []metav1.Condition{{
			Type: platform.ConditionClusterIssuerReady, Status: tc.status, Reason: reason, Message: "the words of " + reason,
		}}
		got := readCertificates(t, &Server{Client: newFakeClient(cluster)})
		require.Equal(t, tc.state, got.State, reason)
		if tc.state != certificateIssuerReady {
			require.Equal(t, "the words of "+reason, got.Message, reason)
		}
	}
}

func TestAnOrganizationCannotReadOrSetTheCertificateEmail(t *testing.T) {
	c := newFakeClient(snapshotCluster(nil))
	srv := &Server{Client: c}
	org := func(r *http.Request) *http.Request {
		return asAdmin(r.WithContext(withTenant(r.Context(), Tenant{Org: "acme"})))
	}

	rec := httptest.NewRecorder()
	srv.handleAPI(rec, org(httptest.NewRequest(http.MethodGet, "/api/settings/certificates", nil)))
	require.Equal(t, http.StatusForbidden, rec.Code)

	rec = httptest.NewRecorder()
	srv.handleAPI(rec, org(postForm("/api/settings/domain/acme-email", url.Values{"email": {"ops@acme.dev"}})))
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Empty(t, storedACMEEmail(t, c))
}

func TestAViewerSeesTheIssuerStateButNotTheEmail(t *testing.T) {
	cluster := snapshotCluster(nil)
	cluster.Spec.Addons.CertManager.ACMEEmail = "ops@acme.dev"
	rec := httptest.NewRecorder()
	(&Server{Client: newFakeClient(cluster)}).handleAPI(rec, asViewer(httptest.NewRequest(http.MethodGet, "/api/settings/certificates", nil)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out certificateSettings
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Empty(t, out.Email)
	require.Equal(t, certificateIssuerWaiting, out.State, "a saved address is still told apart from none")
}
