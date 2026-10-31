package platform

import (
	"context"
	"regexp"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const (
	// ClusterIssuerName is the cert-manager ClusterIssuer that every ingress Geass creates asks for
	// (cert-manager.io/cluster-issuer). The operator keeps it in place, so the name is shared rather
	// than written out where the ingresses are built.
	ClusterIssuerName = "letsencrypt-prod"
	// ClusterIssuerACMEServer is Let's Encrypt's production directory.
	ClusterIssuerACMEServer = "https://acme-v02.api.letsencrypt.org/directory"
	// ClusterIssuerAccountKeySecret holds the ACME account's private key. cert-manager creates it in its
	// own namespace the first time the issuer registers.
	ClusterIssuerAccountKeySecret = "letsencrypt-prod-account-key"
	// ClusterIssuerIngressClass is the ingress class that solves HTTP-01 challenges: k3s ships Traefik.
	ClusterIssuerIngressClass = "traefik"

	// ConditionClusterIssuerReady is the GeassCluster condition that says whether certificates can be
	// issued. It is informational: a cluster without an issuer still runs workloads.
	ConditionClusterIssuerReady = "ClusterIssuerReady"
	// ClusterIssuerReasonWaitingForCertManager and ClusterIssuerReasonRegistering are the ClusterIssuerReady reasons of an
	// issuer that is on its way, not broken.
	ClusterIssuerReasonWaitingForCertManager = "WaitingForCertManager"
	ClusterIssuerReasonRegistering           = "IssuerRegistering"

	// ACMEEmailMaxLength is the longest spec.addons.certManager.acmeEmail the GeassCluster CRD accepts.
	ACMEEmailMaxLength = 254
)

// acmeEmailPattern is the CRD's pattern for spec.addons.certManager.acmeEmail, without the empty value it also allows.
var acmeEmailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// ValidACMEEmail reports whether the CRD accepts a non-empty contact address.
func ValidACMEEmail(email string) bool {
	return len(email) <= ACMEEmailMaxLength && acmeEmailPattern.MatchString(email)
}

// ClusterIssuerGVK is the cert-manager kind. The Go types are not a dependency, so the issuer is handled
// as unstructured.
var ClusterIssuerGVK = schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "ClusterIssuer"}

// ClusterIssuer is the issuer Geass wants: ACME over HTTP-01, solved through the Traefik ingress class.
// It carries the managed-by label, which is how the operator tells it from an issuer of the same name that
// somebody created by hand and must not be overwritten, and the name of the GeassCluster that owns it, since
// the issuer is cluster-wide and only one GeassCluster may decide what it says.
//
// Nested values are []any and map[string]any, as unstructured requires: anything else cannot be deep
// copied.
func ClusterIssuer(email, owner string) *unstructured.Unstructured {
	issuer := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"acme": map[string]any{
				"email":               email,
				"server":              ClusterIssuerACMEServer,
				"privateKeySecretRef": map[string]any{"name": ClusterIssuerAccountKeySecret},
				"solvers": []any{map[string]any{
					"http01": map[string]any{
						"ingress": map[string]any{"ingressClassName": ClusterIssuerIngressClass},
					},
				}},
			},
		},
	}}
	issuer.SetGroupVersionKind(ClusterIssuerGVK)
	issuer.SetName(ClusterIssuerName)
	issuer.SetLabels(map[string]string{LabelManagedBy: ManagedByValue, LabelCluster: owner})
	return issuer
}

// ClusterIssuerOwner is the name of the GeassCluster that created the issuer, or "" when none is recorded.
func ClusterIssuerOwner(issuer *unstructured.Unstructured) string {
	return issuer.GetLabels()[LabelCluster]
}

// ClusterIssuerManaged reports whether the operator created the issuer, as opposed to it having been
// created by hand.
func ClusterIssuerManaged(issuer *unstructured.Unstructured) bool {
	return issuer.GetLabels()[LabelManagedBy] == ManagedByValue
}

// ClusterIssuerReadyCondition reads the Ready condition cert-manager keeps on an issuer. For an ACME issuer
// it turns True once the account has been registered with the ACME server, and says why when it has not,
// for instance when the server refuses the contact email. found is false until cert-manager has reported
// anything.
func ClusterIssuerReadyCondition(issuer *unstructured.Unstructured) (status metav1.ConditionStatus, message string, found bool) {
	conditions, _, _ := unstructured.NestedSlice(issuer.Object, "status", "conditions")
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok || condition["type"] != "Ready" {
			continue
		}
		value, _ := condition["status"].(string)
		reported, _ := condition["message"].(string)
		return metav1.ConditionStatus(value), reported, true
	}
	return metav1.ConditionUnknown, "", false
}

// clusterIssuerManagedFields are the fields of spec.acme that Geass sets. Only these are compared and
// written: anything else on the issuer, such as a field the API server defaults or a CRD upgrade adds, is
// not Geass's, and treating it as drift would rewrite the issuer on every pass.
var clusterIssuerManagedFields = []string{"email", "server", "privateKeySecretRef", "solvers"}

// ClusterIssuerSpecCurrent reports whether the fields Geass sets on an issuer are the ones wanted, so an
// unchanged issuer is left alone and a changed one, such as a new email, is corrected.
func ClusterIssuerSpecCurrent(existing, desired *unstructured.Unstructured) bool {
	for _, field := range clusterIssuerManagedFields {
		have, _, _ := unstructured.NestedFieldNoCopy(existing.Object, "spec", "acme", field)
		want, _, _ := unstructured.NestedFieldNoCopy(desired.Object, "spec", "acme", field)
		if !equality.Semantic.DeepEqual(have, want) {
			return false
		}
	}
	return true
}

// MergeClusterIssuerSpec copies the fields Geass sets from the wanted issuer onto an existing one and leaves
// every other field as it was.
func MergeClusterIssuerSpec(existing, desired *unstructured.Unstructured) error {
	for _, field := range clusterIssuerManagedFields {
		want, found, err := unstructured.NestedFieldCopy(desired.Object, "spec", "acme", field)
		if err != nil || !found {
			return err
		}
		if err := unstructured.SetNestedField(existing.Object, want, "spec", "acme", field); err != nil {
			return err
		}
	}
	return nil
}

// ClusterIssuerProblem is what the clusters report is wrong with their certificate issuer, or "" when
// nothing is. It reads the ClusterIssuerReady condition that the operator keeps, so the dashboard and the
// operator can both explain a certificate that is never issued without going to cert-manager themselves.
func ClusterIssuerProblem(clusters []geassv1alpha1.GeassCluster) string {
	for i := range clusters {
		condition := meta.FindStatusCondition(clusters[i].Status.Conditions, ConditionClusterIssuerReady)
		if condition != nil && condition.Status == metav1.ConditionFalse {
			return condition.Message
		}
	}
	return ""
}

// ClusterIssuerProblemFor reads the clusters from the API and reports what is wrong with their certificate
// issuer, or "" when nothing is or when that cannot be told. It is what the dashboard and the operator add
// to a message about HTTPS not coming up.
func ClusterIssuerProblemFor(ctx context.Context, reader client.Reader) string {
	var clusters geassv1alpha1.GeassClusterList
	if err := reader.List(ctx, &clusters, client.InNamespace(SystemNamespace)); err != nil {
		return ""
	}
	return ClusterIssuerProblem(clusters.Items)
}
