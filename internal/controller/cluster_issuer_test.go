package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

// Spelled out on purpose: if the implementation's group, version or kind were mistyped, a fixture built
// from the same constants would hide it.
var testClusterIssuerGVK = schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "ClusterIssuer"}

// issuerCalls counts what the issuer step writes, and can make the API misbehave.
type issuerCalls struct {
	issuerCreates int
	issuerUpdates int
	statusWrites  int
	// noMatch makes the API server not know the ClusterIssuer kind, as before cert-manager's CRDs exist.
	noMatch bool
	// failGet, failCreate and failUpdate make reads and writes of the issuer fail.
	failGet    error
	failCreate error
	failUpdate error
}

func isClusterIssuer(obj client.Object) bool {
	u, ok := obj.(*unstructured.Unstructured)
	return ok && u.GroupVersionKind() == testClusterIssuerGVK
}

func (i *issuerCalls) funcs() interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if i.failGet != nil && isClusterIssuer(obj) {
				return i.failGet
			}
			if i.noMatch && isClusterIssuer(obj) {
				return &meta.NoKindMatchError{GroupKind: testClusterIssuerGVK.GroupKind(), SearchedVersions: []string{"v1"}}
			}
			return cl.Get(ctx, key, obj, opts...)
		},
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if isClusterIssuer(obj) {
				i.issuerCreates++
				if i.failCreate != nil {
					return i.failCreate
				}
			}
			return cl.Create(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if isClusterIssuer(obj) {
				i.issuerUpdates++
				if i.failUpdate != nil {
					return i.failUpdate
				}
			}
			return cl.Update(ctx, obj, opts...)
		},
		SubResourceUpdate: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if _, ok := obj.(*geassv1alpha1.GeassCluster); ok {
				i.statusWrites++
			}
			return cl.SubResource(subResourceName).Update(ctx, obj, opts...)
		},
	}
}

type issuerFixture struct {
	r *GeassClusterReconciler
	// c is what the reconciler uses and counts; direct is for the test to play cert-manager and
	// other writers without being counted.
	c      client.Client
	direct client.Client
	calls  *issuerCalls
	// cluster is the GeassCluster the reconciler is given; it is also stored.
	cluster *geassv1alpha1.GeassCluster
	// certManagerReady is what reconcileAddons would have told the issuer step.
	certManagerReady bool
}

// reconcile runs the issuer step for the fixture's cluster.
func (f *issuerFixture) reconcile() time.Duration {
	return f.r.reconcileClusterIssuer(context.Background(), f.cluster, f.certManagerReady)
}

func issuerCluster(email string) *geassv1alpha1.GeassCluster {
	off := false
	return &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "geass", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{Addons: geassv1alpha1.GeassClusterAddonsSpec{
			// cert-manager is left at its default, which is on.
			CertManager: geassv1alpha1.GeassClusterCertManagerAddon{ACMEEmail: email},
			Monitoring:  geassv1alpha1.GeassClusterMonitoringAddon{Enabled: &off},
		}},
	}
}

func newIssuerFixture(t *testing.T, cluster *geassv1alpha1.GeassCluster, certManagerInstalled bool, objects ...client.Object) *issuerFixture {
	t.Helper()
	scheme := longhornTestScheme(t)
	calls := &issuerCalls{}
	all := append([]client.Object{cluster}, objects...)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(all...).WithStatusSubresource(&geassv1alpha1.GeassCluster{}).Build()
	c := interceptor.NewClient(base, calls.funcs())
	f := &issuerFixture{r: &GeassClusterReconciler{Client: c, Scheme: scheme}, c: c, direct: base, calls: calls, cluster: cluster, certManagerReady: certManagerInstalled}
	if certManagerInstalled {
		markCertManagerInstalled(t, c)
	}
	return f
}

// markCertManagerInstalled fakes what k3s's helm controller does once the chart is up: a finished install
// job and a ready cert-manager pod.
func markCertManagerInstalled(t *testing.T, c client.Client) {
	t.Helper()
	ctx := context.Background()
	const job = "helm-install-geass-cert-manager"
	chart := &helmv1.HelmChart{ObjectMeta: metav1.ObjectMeta{Name: platform.CertManagerChartName, Namespace: platform.HelmChartNamespace}}
	chart.Status.JobName = job
	require.NoError(t, c.Create(ctx, chart))
	require.NoError(t, c.Create(ctx, &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: job, Namespace: platform.HelmChartNamespace},
		Status:     batchv1.JobStatus{Succeeded: 1},
	}))
	require.NoError(t, c.Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "geass-cert-manager-0", Namespace: platform.CertManagerTargetNS,
			Labels: map[string]string{"app.kubernetes.io/instance": platform.CertManagerChartName},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}))
}

func (f *issuerFixture) issuer(t *testing.T) *unstructured.Unstructured {
	t.Helper()
	issuer := &unstructured.Unstructured{}
	issuer.SetGroupVersionKind(testClusterIssuerGVK)
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: "letsencrypt-prod"}, issuer))
	return issuer
}

func (f *issuerFixture) noIssuer(t *testing.T) {
	t.Helper()
	issuer := &unstructured.Unstructured{}
	issuer.SetGroupVersionKind(testClusterIssuerGVK)
	err := f.c.Get(context.Background(), client.ObjectKey{Name: "letsencrypt-prod"}, issuer)
	require.True(t, apierrors.IsNotFound(err), "no ClusterIssuer was expected, got err=%v", err)
}

func (f *issuerFixture) condition(t *testing.T) *metav1.Condition {
	t.Helper()
	return f.conditionOf(t, f.cluster)
}

func (f *issuerFixture) conditionOf(t *testing.T, cluster *geassv1alpha1.GeassCluster) *metav1.Condition {
	t.Helper()
	latest := &geassv1alpha1.GeassCluster{}
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(cluster), latest))
	return meta.FindStatusCondition(latest.Status.Conditions, platform.ConditionClusterIssuerReady)
}

func (f *issuerFixture) issuerEmail(t *testing.T) string {
	t.Helper()
	email, _, err := unstructured.NestedString(f.issuer(t).Object, "spec", "acme", "email")
	require.NoError(t, err)
	return email
}

// certManagerReports is cert-manager reporting on the issuer, as it does after it has looked at the ACME
// account. It writes around the counting client, so the counts are only ever the reconciler's.
func certManagerReports(t *testing.T, f *issuerFixture, status, message string) {
	t.Helper()
	issuer := f.issuer(t)
	issuer.Object["status"] = map[string]any{"conditions": []any{
		map[string]any{"type": "Ready", "status": status, "message": message},
	}}
	require.NoError(t, f.direct.Update(context.Background(), issuer))
}

// handMadeIssuer is an issuer somebody created themselves: no managed-by label, and a solver of their own.
func handMadeIssuer(ready bool) *unstructured.Unstructured {
	status := "False"
	if ready {
		status = "True"
	}
	issuer := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"acme": map[string]any{
			"email":   "ops@theirs.dev",
			"server":  "https://acme-staging-v02.api.letsencrypt.org/directory",
			"solvers": []any{map[string]any{"dns01": map[string]any{"cloudflare": map[string]any{}}}},
		}},
		"status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": status}}},
	}}
	issuer.SetGroupVersionKind(testClusterIssuerGVK)
	issuer.SetName("letsencrypt-prod")
	return issuer
}

func TestTheIssuerIsCreatedOnceCertManagerIsReady(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), true)

	require.Equal(t, clusterIssuerRetry, f.reconcile(),
		"cert-manager has not registered the account yet, so look again soon")

	issuer := f.issuer(t)
	require.Equal(t, "letsencrypt-prod", issuer.GetName(), "the name every ingress annotation points at")
	require.Equal(t, "admin@acme.dev", f.issuerEmail(t))
	require.True(t, platform.ClusterIssuerManaged(issuer), "marked as Geass's, so it is never confused with a hand-made one")
	server, _, _ := unstructured.NestedString(issuer.Object, "spec", "acme", "server")
	require.Equal(t, "https://acme-v02.api.letsencrypt.org/directory", server)
	solvers, _, _ := unstructured.NestedSlice(issuer.Object, "spec", "acme", "solvers")
	require.Equal(t, []any{map[string]any{"http01": map[string]any{"ingress": map[string]any{"ingressClassName": "traefik"}}}}, solvers)

	condition := f.condition(t)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, issuerReasonRegistering, condition.Reason)
}

func TestTheConditionTurnsTrueWhenCertManagerAcceptsTheAcmeAccount(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), true)
	f.reconcile()
	certManagerReports(t, f, "True", "The ACME account was registered with the ACME server")

	require.Zero(t, f.reconcile(), "settled: the issuer is watched, so nothing asks to be looked at again")

	condition := f.condition(t)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, issuerReasonReady, condition.Reason)
}

func TestReconcilingAnUpToDateIssuerWritesNothing(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), true)
	f.reconcile()
	certManagerReports(t, f, "True", "")
	f.reconcile()
	creates, updates, statusWrites := f.calls.issuerCreates, f.calls.issuerUpdates, f.calls.statusWrites

	for range 3 {
		f.reconcile()
	}

	require.Equal(t, creates, f.calls.issuerCreates, "idempotent: no second issuer")
	require.Equal(t, updates, f.calls.issuerUpdates, "an issuer that is already right is not rewritten")
	require.Equal(t, statusWrites, f.calls.statusWrites, "and the condition is not rewritten, which would make the cluster reconcile again")
}

func TestNothingIsCreatedWhileCertManagerIsNotReady(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), false)

	require.Equal(t, clusterIssuerRetry, f.reconcile())

	f.noIssuer(t)
	condition := f.condition(t)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, issuerReasonCertManager, condition.Reason)
}

func TestTheIssuerWaitsUntilTheApiServerServesCertManagersKinds(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), true)
	f.calls.noMatch = true

	require.Equal(t, clusterIssuerRetry, f.reconcile(),
		"the chart is up but its CRDs are not served yet: wait, do not fail")
	require.Zero(t, f.calls.issuerCreates)
	require.Equal(t, issuerReasonCertManager, f.condition(t).Reason)

	f.calls.noMatch = false
	f.reconcile()
	f.issuer(t)
}

func TestWithoutAnEmailNoIssuerIsCreatedAndTheConditionSaysWhatToDo(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster(""), true)

	require.Zero(t, f.reconcile(), "nothing to retry: setting the email changes the cluster, which reconciles it")

	f.noIssuer(t)
	require.Zero(t, f.calls.issuerCreates)
	condition := f.condition(t)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, issuerReasonEmailMissing, condition.Reason)
	require.Contains(t, condition.Message, "spec.addons.certManager.acmeEmail", "says where to set it")
	require.Contains(t, condition.Message, "GEASS_ACME_EMAIL", "and how the installer sets it")
	require.Contains(t, condition.Message, "certificates cannot be issued", "and what it costs")
}

func TestABlankEmailIsTreatedAsNoEmail(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("   "), true)

	f.reconcile()

	f.noIssuer(t)
	require.Equal(t, issuerReasonEmailMissing, f.condition(t).Reason, "an issuer with a blank contact would only be refused by Let's Encrypt")
}

func TestAClusterWithoutAnEmailIsStillReadyAndSaysWhyHttpsIsMissing(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster(""), true)

	result, err := f.r.Reconcile(context.Background(), reconcileRequest(f.cluster))
	require.NoError(t, err, "a missing email is reported, not failed on")

	latest := &geassv1alpha1.GeassCluster{}
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(f.cluster), latest))
	require.True(t, conditionIsTrue(latest.Status.Conditions, platform.ConditionReady), "the cluster runs workloads without an issuer")
	require.True(t, conditionIsTrue(latest.Status.Conditions, platform.ConditionAddonsReady))
	condition := meta.FindStatusCondition(latest.Status.Conditions, platform.ConditionClusterIssuerReady)
	require.NotNil(t, condition, "the cluster's own status write must not have wiped the issuer's condition")
	require.Equal(t, issuerReasonEmailMissing, condition.Reason)
	f.noIssuer(t)
	require.Zero(t, result.RequeueAfter, "a ready cluster is not made to reconcile on a timer for the sake of its issuer")
}

func TestAnIssuerIsCreatedByAFullReconcileOfAClusterThatHasAnEmail(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), true)

	result, err := f.r.Reconcile(context.Background(), reconcileRequest(f.cluster))
	require.NoError(t, err)

	require.Equal(t, "admin@acme.dev", f.issuerEmail(t))
	require.Equal(t, clusterIssuerRetry, result.RequeueAfter, "the account is still being registered")
	latest := &geassv1alpha1.GeassCluster{}
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(f.cluster), latest))
	require.True(t, conditionIsTrue(latest.Status.Conditions, platform.ConditionReady))
	require.NotNil(t, meta.FindStatusCondition(latest.Status.Conditions, platform.ConditionClusterIssuerReady))
}

func TestAnIssuerSomebodyCreatedIsNeverOverwritten(t *testing.T) {
	for name, email := range map[string]string{"with an email": "admin@acme.dev", "without one": ""} {
		t.Run(name, func(t *testing.T) {
			f := newIssuerFixture(t, issuerCluster(email), true, handMadeIssuer(true))

			require.Zero(t, f.reconcile())

			require.Zero(t, f.calls.issuerUpdates, "it may be a wildcard DNS-01 issuer that certificates depend on")
			require.Zero(t, f.calls.issuerCreates)
			require.Equal(t, "ops@theirs.dev", f.issuerEmail(t))
			solvers, _, _ := unstructured.NestedSlice(f.issuer(t).Object, "spec", "acme", "solvers")
			require.Contains(t, solvers[0], "dns01")
			require.False(t, platform.ClusterIssuerManaged(f.issuer(t)), "and it is not claimed")
			condition := f.condition(t)
			require.Equal(t, metav1.ConditionTrue, condition.Status)
			require.Equal(t, issuerReasonExternal, condition.Reason, "no complaint about a missing email: certificates can be issued")
		})
	}
}

func TestANewEmailReachesTheIssuerThatGeassMade(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("old@acme.dev"), true)
	f.reconcile()
	certManagerReports(t, f, "True", "")
	f.reconcile()
	require.Zero(t, f.calls.issuerUpdates)

	f.cluster.Spec.Addons.CertManager.ACMEEmail = "new@acme.dev"
	require.Equal(t, clusterIssuerRetry, f.reconcile(),
		"cert-manager registers the new contact, and what it reports now is about the old one")

	require.Equal(t, 1, f.calls.issuerUpdates)
	require.Equal(t, "new@acme.dev", f.issuerEmail(t))
	require.True(t, platform.ClusterIssuerManaged(f.issuer(t)))
}

func TestTakingTheEmailAwayDoesNotDeleteTheIssuerCertificatesRenewThrough(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), true)
	f.reconcile()
	certManagerReports(t, f, "True", "")

	f.cluster.Spec.Addons.CertManager.ACMEEmail = ""
	f.reconcile()

	require.Equal(t, "admin@acme.dev", f.issuerEmail(t), "left exactly as it was")
	require.Equal(t, issuerReasonReady, f.condition(t).Reason, "it still works, so that is what is reported")
}

func TestAnIssuerThatCertManagerRefusesIsReportedWithTheReason(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("admin@example.com"), true)
	f.reconcile()
	certManagerReports(t, f, "False", `Failed to register ACME account: contact email has forbidden domain "example.com"`)

	require.Equal(t, clusterIssuerRetry, f.reconcile(),
		"watch for it to recover once the email is fixed")

	condition := f.condition(t)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, issuerReasonNotReady, condition.Reason)
	require.Contains(t, condition.Message, `forbidden domain "example.com"`, "what the CA said, which is what the operator has to act on")
}

func TestAWebhookThatIsNotServingYetIsShownAndRetriedNotFailed(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), true)
	f.calls.failCreate = errors.New(`Internal error occurred: failed calling webhook "webhook.cert-manager.io": ` +
		`Post "https://geass-cert-manager-webhook.cert-manager.svc:443/validate": dial tcp 10.43.7.21:443: connect: connection refused`)

	require.Equal(t, clusterIssuerRetry, f.reconcile())

	f.noIssuer(t)
	condition := f.condition(t)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, issuerReasonApplyFailed, condition.Reason)
	require.Contains(t, condition.Message, "Could not create the letsencrypt-prod ClusterIssuer")
	require.NotContains(t, condition.Message, "10.43", "in-cluster addresses are for the log: this message is repeated on the dashboard")
	require.NotContains(t, condition.Message, "svc")

	f.calls.failCreate = errors.New("another attempt, another error text")
	statusWrites := f.calls.statusWrites
	f.reconcile()
	require.Equal(t, statusWrites, f.calls.statusWrites, "the message is the same each time, so retrying does not rewrite the condition")

	f.calls.failCreate = nil
	f.reconcile()
	require.Equal(t, "admin@acme.dev", f.issuerEmail(t), "and it is made once the webhook answers")
}

func TestARaceWithAnotherWriterLeavesTheConditionAloneAndTriesAgain(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), true)
	f.reconcile()
	before := f.condition(t)

	f.calls.failCreate = apierrors.NewAlreadyExists(schema.GroupResource{Group: "cert-manager.io", Resource: "clusterissuers"}, "letsencrypt-prod")
	require.NoError(t, f.direct.Delete(context.Background(), f.issuer(t)))
	require.Equal(t, clusterIssuerRetry, f.reconcile())
	require.Equal(t, before.Reason, f.condition(t).Reason, "nothing was learned, so nothing is written")
}

func TestWithCertManagerSwitchedOffNoIssuerIsCreatedAndTheConditionGoes(t *testing.T) {
	off := false
	cluster := issuerCluster("admin@acme.dev")
	cluster.Spec.Addons.CertManager.Enabled = &off
	cluster.Status.Conditions = []metav1.Condition{{
		Type: platform.ConditionClusterIssuerReady, Status: metav1.ConditionFalse, Reason: issuerReasonCertManager,
		Message: "left over from when cert-manager was on", LastTransitionTime: metav1.Now(),
	}}
	f := newIssuerFixture(t, cluster, false, handMadeIssuer(true))
	require.NotNil(t, f.condition(t), "the fixture starts with a condition to clear")

	require.Zero(t, f.reconcile(), "nothing to wait for")

	require.Zero(t, f.calls.issuerCreates+f.calls.issuerUpdates, "somebody else runs cert-manager, and its issuers")
	require.Nil(t, f.condition(t), "a condition about an issuer nobody manages would only mislead")
}

func TestTheIssuerConditionDoesNotDisturbTheClustersOwnConditions(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), true)
	cluster := f.cluster
	cluster.Status.Conditions = []metav1.Condition{{
		Type: platform.ConditionReady, Status: metav1.ConditionTrue, Reason: "ClusterReady", Message: "Geass cluster is ready", LastTransitionTime: metav1.Now(),
	}}
	require.NoError(t, f.c.Status().Update(context.Background(), cluster))

	f.reconcile()

	latest := &geassv1alpha1.GeassCluster{}
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKeyFromObject(cluster), latest))
	require.True(t, conditionIsTrue(latest.Status.Conditions, platform.ConditionReady))
	require.NotNil(t, meta.FindStatusCondition(latest.Status.Conditions, platform.ConditionClusterIssuerReady))
}

func TestAFieldGeassDoesNotSetIsNotMistakenForDrift(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("old@acme.dev"), true)
	f.reconcile()
	issuer := f.issuer(t)
	// What an API server default, a CRD upgrade or somebody's own tweak can leave on the issuer.
	require.NoError(t, unstructured.SetNestedField(issuer.Object, "ISRG Root X1", "spec", "acme", "preferredChain"))
	require.NoError(t, f.direct.Update(context.Background(), issuer))

	require.Equal(t, clusterIssuerRetry, f.reconcile(), "still registering: nothing reported yet")
	require.Zero(t, f.calls.issuerUpdates, "a field that is not Geass's is not a reason to rewrite the issuer, pass after pass")

	f.cluster.Spec.Addons.CertManager.ACMEEmail = "new@acme.dev"
	f.reconcile()
	require.Equal(t, 1, f.calls.issuerUpdates)
	require.Equal(t, "new@acme.dev", f.issuerEmail(t))
	chain, _, _ := unstructured.NestedString(f.issuer(t).Object, "spec", "acme", "preferredChain")
	require.Equal(t, "ISRG Root X1", chain, "and when it is rewritten, what is not Geass's survives")
}

func otherCluster(email string) *geassv1alpha1.GeassCluster {
	cluster := issuerCluster(email)
	cluster.Name = "other"
	return cluster
}

func TestAnIssuerAnotherGeassClusterKeepsIsLeftAlone(t *testing.T) {
	other := otherCluster("b@acme.dev")
	f := newIssuerFixture(t, issuerCluster("a@acme.dev"), true, other)
	f.reconcile()
	certManagerReports(t, f, "True", "")
	f.reconcile()
	f.calls.issuerUpdates = 0

	require.Zero(t, f.r.reconcileClusterIssuer(context.Background(), other, true))

	require.Zero(t, f.calls.issuerUpdates, "two clusters that disagree would otherwise undo each other for ever")
	require.Equal(t, "a@acme.dev", f.issuerEmail(t))
	condition := f.conditionOf(t, other)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, issuerReasonOtherCluster, condition.Reason)
	require.Contains(t, condition.Message, "GeassCluster geass", "and says whose it is")
}

func TestAnIssuerWhoseOwnerIsGoneIsTakenOver(t *testing.T) {
	other := otherCluster("b@acme.dev")
	f := newIssuerFixture(t, issuerCluster("a@acme.dev"), true, other)
	f.reconcile()
	require.NoError(t, f.direct.Delete(context.Background(), f.cluster))

	f.r.reconcileClusterIssuer(context.Background(), other, true)

	require.Equal(t, "b@acme.dev", f.issuerEmail(t), "deleting the first cluster must not strand the issuer")
	require.Equal(t, "other", platform.ClusterIssuerOwner(f.issuer(t)))
}

func TestTheConditionsClockOnlyRestartsWhenItsStatusChanges(t *testing.T) {
	anHourAgo := metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second))
	cluster := issuerCluster("admin@acme.dev")
	cluster.Status.Conditions = []metav1.Condition{{
		Type: platform.ConditionClusterIssuerReady, Status: metav1.ConditionFalse, Reason: issuerReasonCertManager,
		Message: "Waiting for cert-manager", LastTransitionTime: anHourAgo,
	}}
	f := newIssuerFixture(t, cluster, true)
	require.NotNil(t, f.condition(t), "the fixture starts with a condition")

	f.reconcile()
	registering := f.condition(t)
	require.Equal(t, issuerReasonRegistering, registering.Reason, "the reason moved on")
	require.True(t, registering.LastTransitionTime.Equal(&anHourAgo), "but it has been False since the hour ago, and that is what the time says")

	certManagerReports(t, f, "True", "")
	f.reconcile()
	ready := f.condition(t)
	require.Equal(t, metav1.ConditionTrue, ready.Status)
	require.True(t, ready.LastTransitionTime.After(anHourAgo.Time), "a change of status is a transition")
}

func TestAnIssuerThatIsNotReadyAndSaysNothingIsReportedWithoutADanglingColon(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), true)
	f.reconcile()
	certManagerReports(t, f, "Unknown", "")

	f.reconcile()

	condition := f.condition(t)
	require.Equal(t, issuerReasonNotReady, condition.Reason)
	require.Equal(t, "cert-manager reports the letsencrypt-prod ClusterIssuer is not ready", condition.Message)
}

func TestAFailedUpdateIsRetriedAndItsErrorStaysInTheLog(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("old@acme.dev"), true)
	f.reconcile()
	certManagerReports(t, f, "True", "")
	f.reconcile()
	f.calls.failUpdate = errors.New("admission webhook denied the request: dial tcp 10.43.7.21:443")

	f.cluster.Spec.Addons.CertManager.ACMEEmail = "new@acme.dev"
	require.Equal(t, clusterIssuerRetry, f.reconcile())

	require.Equal(t, 1, f.calls.issuerUpdates, "the write was tried")
	require.Equal(t, "old@acme.dev", f.issuerEmail(t))
	condition := f.condition(t)
	require.Equal(t, issuerReasonApplyFailed, condition.Reason)
	require.Contains(t, condition.Message, "Could not update the letsencrypt-prod ClusterIssuer")
	require.NotContains(t, condition.Message, "10.43")
}

func TestAConflictOnUpdateLeavesTheConditionAloneAndTriesAgain(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("old@acme.dev"), true)
	f.reconcile()
	certManagerReports(t, f, "True", "")
	f.reconcile()
	before := *f.condition(t)
	f.calls.failUpdate = apierrors.NewConflict(schema.GroupResource{Group: "cert-manager.io", Resource: "clusterissuers"}, "letsencrypt-prod", errors.New("the object has been modified"))

	f.cluster.Spec.Addons.CertManager.ACMEEmail = "new@acme.dev"
	require.Equal(t, clusterIssuerRetry, f.reconcile())

	require.Equal(t, before, *f.condition(t), "somebody else wrote the issuer; the next pass reads what they wrote")
}

func TestAnIssuerThatCannotBeReadIsRetriedWithoutBlamingAnything(t *testing.T) {
	f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), true)
	f.calls.failGet = errors.New("etcdserver: request timed out")

	require.Equal(t, clusterIssuerRetry, f.reconcile())

	require.Zero(t, f.calls.issuerCreates, "not knowing whether the issuer exists is not a reason to create it")
	require.Nil(t, f.condition(t), "and a passing failure is not worth a condition")
}

func TestTheOperatorSaysWhenItMayNotManageIssuers(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "cert-manager.io", Resource: "clusterissuers"}, "letsencrypt-prod", errors.New("no RBAC policy matched"))
	for name, arrange := range map[string]func(*issuerCalls){
		"on reading": func(c *issuerCalls) { c.failGet = forbidden },
		"on writing": func(c *issuerCalls) { c.failCreate = forbidden },
	} {
		t.Run(name, func(t *testing.T) {
			f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), true)
			arrange(f.calls)

			require.Equal(t, clusterIssuerRetry, f.reconcile())

			condition := f.condition(t)
			require.NotNil(t, condition, "an operator that is not allowed to do this is not a thing to leave to the log")
			require.Equal(t, metav1.ConditionFalse, condition.Status)
			require.Equal(t, issuerReasonAccessDenied, condition.Reason)
			require.Contains(t, condition.Message, "clusterissuers.cert-manager.io")
		})
	}
}

func TestTheIssuerWatchIsStartedOnceCertManagerIsReady(t *testing.T) {
	started := 0
	notReady := newIssuerFixture(t, issuerCluster("admin@acme.dev"), false)
	notReady.r.watchClusterIssuers = func() error { started++; return nil }
	notReady.reconcile()
	require.Zero(t, started, "the kind is cert-manager's: it is not watched before cert-manager is there")

	f := newIssuerFixture(t, issuerCluster("admin@acme.dev"), true)
	f.r.watchClusterIssuers = func() error { started++; return nil }
	for range 3 {
		f.reconcile()
	}
	require.Equal(t, 1, started, "watched once, however many times the cluster reconciles")
}

func TestAWatchThatCouldNotBeStartedIsTriedAgain(t *testing.T) {
	attempts := 0
	// No email and no issuer: nothing else would ask to be looked at again.
	f := newIssuerFixture(t, issuerCluster(""), true)
	f.r.watchClusterIssuers = func() error {
		attempts++
		if attempts == 1 {
			return errors.New("the cache is not running")
		}
		return nil
	}

	require.Equal(t, clusterIssuerRetry, f.reconcile(), "without the watch a deleted issuer would go unnoticed, so try again")
	require.Zero(t, f.reconcile(), "and once it is running there is nothing to retry")
	require.Zero(t, f.reconcile())
	require.Equal(t, 2, attempts)
}

func TestOnlyTheIssuerTheIngressesAskForIsWatched(t *testing.T) {
	named := func(name string) client.Object {
		issuer := &unstructured.Unstructured{}
		issuer.SetGroupVersionKind(testClusterIssuerGVK)
		issuer.SetName(name)
		return issuer
	}
	require.True(t, isGeassClusterIssuer(named("letsencrypt-prod")))
	require.False(t, isGeassClusterIssuer(named("selfsigned")), "somebody else's issuers are not Geass's business")
}
