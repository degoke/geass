package dashboard

import (
	"net/http"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/degoke/geass/pkg/platform"
)

// The contact address of the cluster's Let's Encrypt account (spec.addons.certManager.acmeEmail on the GeassCluster).
// Without it the operator creates no letsencrypt-prod issuer, so no HTTPS certificate is issued, the dashboard's own
// included. The installer sets it from GEASS_ACME_EMAIL; this is where it is set or changed afterwards, on the Domain page,
// which is where a domain that is waiting for its certificate is set up.

const acmeEmailSavePath = "/settings/domain/acme-email"

// What the certificate issuer is doing, in the words the page uses.
const (
	certificateIssuerMissing = "missing" // no address, so no issuer
	certificateIssuerWaiting = "waiting" // saved, and the operator or cert-manager has not finished
	certificateIssuerReady   = "ready"
	certificateIssuerFailing = "failing"
)

var certificateIssuerPendingReasons = map[string]bool{
	platform.ClusterIssuerReasonWaitingForCertManager: true,
	platform.ClusterIssuerReasonRegistering:           true,
}

type certificateSettings struct {
	Email   string `json:"email"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

// handleAPICertificateSettings says which address the cluster's certificates are requested with, and whether the issuer
// that uses it works. The cluster is the platform's, so only the platform's own users can read it.
func (s *Server) handleAPICertificateSettings(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformContext(w, r) {
		return
	}
	cluster, err := s.defaultGeassCluster(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load the cluster"})
		return
	}
	email := cluster.Spec.Addons.CertManager.ACMEEmail
	out := certificateSettings{}
	if s.sessionCanMutate(r) {
		out.Email = email
	}
	condition := meta.FindStatusCondition(cluster.Status.Conditions, platform.ConditionClusterIssuerReady)
	switch {
	case email == "":
		out.State = certificateIssuerMissing
	case condition == nil:
		out.State = certificateIssuerWaiting
	case condition.Status == metav1.ConditionTrue:
		out.State = certificateIssuerReady
	case condition.Status == metav1.ConditionUnknown, certificateIssuerPendingReasons[condition.Reason]:
		out.State, out.Message = certificateIssuerWaiting, condition.Message
	default:
		out.State, out.Message = certificateIssuerFailing, condition.Message
	}
	writeJSON(w, http.StatusOK, out)
}

// handleACMEEmailSave sets the address. The operator creates the issuer, or changes the address of the one it keeps, on
// its next look at the cluster.
func (s *Server) handleACMEEmailSave(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/settings/domain") || !requirePlatformContext(w, r) || !parseFormOrRedirect(w, r, "/settings/domain") {
		return
	}
	fail := func(message string) { redirectProbe(w, r, "/settings/domain", dashboardLiteralError, message) }
	email := strings.TrimSpace(r.FormValue("email"))
	if email == "" || !platform.ValidACMEEmail(email) {
		fail("enter an email address, like you@yourdomain.com")
		return
	}
	cluster, err := s.defaultGeassCluster(r.Context())
	if err != nil {
		fail("could not load the cluster")
		return
	}
	cluster.Spec.Addons.CertManager.ACMEEmail = email
	if err := s.Client.Update(r.Context(), cluster); err != nil {
		fail("could not save the email address")
		return
	}
	redirect(w, r, "/settings/domain")
}
