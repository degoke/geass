package platform

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// The expectations are spelled out rather than built from the constants: the name is what the ingresses
// ask for, and cert-manager reads the rest, so a typo in a constant must not be hidden by a fixture that
// shares it.
func TestTheClusterIssuerIsAcmeOverHTTP01ThroughTraefik(t *testing.T) {
	issuer := ClusterIssuer("admin@acme.dev", "geass")

	require.Equal(t, "cert-manager.io/v1", issuer.GetAPIVersion())
	require.Equal(t, "ClusterIssuer", issuer.GetKind())
	require.Equal(t, "letsencrypt-prod", issuer.GetName(), "the name every ingress annotation points at")
	require.Empty(t, issuer.GetNamespace(), "a ClusterIssuer is cluster-scoped")

	acme, found, err := unstructured.NestedMap(issuer.Object, "spec", "acme")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "admin@acme.dev", acme["email"])
	require.Equal(t, "https://acme-v02.api.letsencrypt.org/directory", acme["server"], "production, not staging")
	require.Equal(t, map[string]any{"name": "letsencrypt-prod-account-key"}, acme["privateKeySecretRef"])
	require.Equal(t, []any{map[string]any{
		"http01": map[string]any{"ingress": map[string]any{"ingressClassName": "traefik"}},
	}}, acme["solvers"])
}

func TestTheClusterIssuerCanBeCopiedAndTellsOperatorMadeFromHandMade(t *testing.T) {
	issuer := ClusterIssuer("admin@acme.dev", "geass")

	// unstructured panics on a value it cannot deep copy, such as a []string, and the client copies.
	require.NotPanics(t, func() { _ = issuer.DeepCopy() })

	require.True(t, ClusterIssuerManaged(issuer))
	handMade := &unstructured.Unstructured{Object: map[string]any{}}
	handMade.SetName("letsencrypt-prod")
	require.False(t, ClusterIssuerManaged(handMade), "an issuer without the label belongs to somebody else")
	handMade.SetLabels(map[string]string{LabelManagedBy: "someone-else"})
	require.False(t, ClusterIssuerManaged(handMade))
}

func issuerWithConditions(conditions ...any) *unstructured.Unstructured {
	issuer := ClusterIssuer("admin@acme.dev", "geass")
	if conditions != nil {
		issuer.Object["status"] = map[string]any{"conditions": conditions}
	}
	return issuer
}

func TestTheIssuersReadyConditionIsReadFromWhatCertManagerReports(t *testing.T) {
	_, _, found := ClusterIssuerReadyCondition(issuerWithConditions())
	require.False(t, found, "a new issuer has no status until cert-manager has looked at it")

	status, message, found := ClusterIssuerReadyCondition(issuerWithConditions(
		map[string]any{"type": "Ready", "status": "True", "message": "The ACME account was registered with the ACME server"},
	))
	require.True(t, found)
	require.Equal(t, metav1.ConditionTrue, status)
	require.Equal(t, "The ACME account was registered with the ACME server", message)

	status, message, found = ClusterIssuerReadyCondition(issuerWithConditions(
		map[string]any{"type": "Other", "status": "True"},
		map[string]any{"type": "Ready", "status": "False", "message": `contact email has forbidden domain "example.com"`},
	))
	require.True(t, found)
	require.Equal(t, metav1.ConditionFalse, status)
	require.Contains(t, message, "forbidden domain", "the reason the CA gave is what the operator needs to see")
}

func TestAnIssuerWithTheWantedSpecIsLeftAlone(t *testing.T) {
	desired := ClusterIssuer("admin@acme.dev", "geass")

	// What the API server returns: the same spec, and a status that cert-manager keeps.
	existing := desired.DeepCopy()
	existing.Object["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}
	existing.SetResourceVersion("42")
	require.True(t, ClusterIssuerSpecCurrent(existing, desired), "status and metadata are not the spec")

	require.False(t, ClusterIssuerSpecCurrent(ClusterIssuer("old@acme.dev", "geass"), desired), "a new email has to reach the issuer")

	dns := desired.DeepCopy()
	require.NoError(t, unstructured.SetNestedSlice(dns.Object, []any{map[string]any{"dns01": map[string]any{}}}, "spec", "acme", "solvers"))
	require.False(t, ClusterIssuerSpecCurrent(dns, desired), "a changed solver is a changed spec")
}

func TestOnlyTheFieldsGeassSetsDecideWhetherAnIssuerIsCurrent(t *testing.T) {
	desired := ClusterIssuer("admin@acme.dev", "geass")

	// A field the API server defaults, a CRD upgrade adds or somebody sets is not Geass's to judge.
	extra := desired.DeepCopy()
	require.NoError(t, unstructured.SetNestedField(extra.Object, "ISRG Root X1", "spec", "acme", "preferredChain"))
	require.NoError(t, unstructured.SetNestedField(extra.Object, int64(3), "spec", "acme", "someNewNumber"))
	require.True(t, ClusterIssuerSpecCurrent(extra, desired), "otherwise it would be rewritten on every pass")
}

func TestMergingTheWantedSpecKeepsWhatGeassDoesNotSet(t *testing.T) {
	existing := ClusterIssuer("old@acme.dev", "geass")
	require.NoError(t, unstructured.SetNestedField(existing.Object, "ISRG Root X1", "spec", "acme", "preferredChain"))
	desired := ClusterIssuer("new@acme.dev", "geass")

	require.NoError(t, MergeClusterIssuerSpec(existing, desired))

	require.True(t, ClusterIssuerSpecCurrent(existing, desired))
	email, _, _ := unstructured.NestedString(existing.Object, "spec", "acme", "email")
	require.Equal(t, "new@acme.dev", email)
	chain, _, _ := unstructured.NestedString(existing.Object, "spec", "acme", "preferredChain")
	require.Equal(t, "ISRG Root X1", chain)
	require.NotPanics(t, func() { _ = existing.DeepCopy() }, "what was merged in is still deep-copyable")

	// Merged into a spec that is not shaped as expected, it says so rather than writing into the wrong place.
	broken := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"acme": "not a map"}}}
	require.Error(t, MergeClusterIssuerSpec(broken, desired))
}

func TestTheIssuerRecordsWhichGeassClusterOwnsIt(t *testing.T) {
	issuer := ClusterIssuer("admin@acme.dev", "default")
	require.Equal(t, "default", ClusterIssuerOwner(issuer))
	require.True(t, ClusterIssuerManaged(issuer))

	handMade := &unstructured.Unstructured{Object: map[string]any{}}
	require.Empty(t, ClusterIssuerOwner(handMade))
}

func clusterWithConditions(conditions ...metav1.Condition) geassv1alpha1.GeassCluster {
	return geassv1alpha1.GeassCluster{Status: geassv1alpha1.GeassClusterStatus{Conditions: conditions}}
}

func TestOnlyAnIssuerThatIsNotReadyIsReportedAsAProblem(t *testing.T) {
	broken := metav1.Condition{Type: ConditionClusterIssuerReady, Status: metav1.ConditionFalse, Message: "No ACME contact email is configured"}
	fine := metav1.Condition{Type: ConditionClusterIssuerReady, Status: metav1.ConditionTrue, Message: "ready"}
	unrelated := metav1.Condition{Type: ConditionReady, Status: metav1.ConditionFalse, Message: "Geass cluster is not ready"}

	require.Equal(t, "No ACME contact email is configured", ClusterIssuerProblem([]geassv1alpha1.GeassCluster{clusterWithConditions(broken)}))
	require.Empty(t, ClusterIssuerProblem([]geassv1alpha1.GeassCluster{clusterWithConditions(fine)}))
	require.Empty(t, ClusterIssuerProblem([]geassv1alpha1.GeassCluster{clusterWithConditions(unrelated)}), "a cluster that is not ready is not an issuer problem")
	require.Empty(t, ClusterIssuerProblem([]geassv1alpha1.GeassCluster{clusterWithConditions()}), "silence is not a problem")
	require.Empty(t, ClusterIssuerProblem(nil))
	require.Equal(t, "No ACME contact email is configured",
		ClusterIssuerProblem([]geassv1alpha1.GeassCluster{clusterWithConditions(fine), clusterWithConditions(broken)}), "any cluster's problem is reported")
}

func TestTheIssuerProblemIsReadFromTheClustersInTheSystemNamespace(t *testing.T) {
	broken := metav1.Condition{Type: ConditionClusterIssuerReady, Status: metav1.ConditionFalse, Message: "No ACME contact email is configured", LastTransitionTime: metav1.Now()}
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	cluster := func(namespace string, conditions ...metav1.Condition) *geassv1alpha1.GeassCluster {
		return &geassv1alpha1.GeassCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "geass", Namespace: namespace},
			Status:     geassv1alpha1.GeassClusterStatus{Conditions: conditions},
		}
	}
	ctx := context.Background()

	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster(SystemNamespace, broken)).Build()
	require.Equal(t, "No ACME contact email is configured", ClusterIssuerProblemFor(ctx, reader))

	elsewhere := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster("somewhere-else", broken)).Build()
	require.Empty(t, ClusterIssuerProblemFor(ctx, elsewhere), "only the platform's own namespace is read")

	// A reader that cannot list (here, one that does not know the kind) is "nothing to say", not a failure.
	require.Empty(t, ClusterIssuerProblemFor(ctx, fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()))
}
