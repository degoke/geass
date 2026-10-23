package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

var externalNow = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func externalGitHubConnection(installations ...geassv1alpha1.GeassGitHubInstallation) *geassv1alpha1.GeassGitHubConnection {
	webhook := githubapp.ExternalWebhookSecretName("rungeass")
	return &geassv1alpha1.GeassGitHubConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "rungeass", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassGitHubConnectionSpec{
			SecretRef:        corev1.LocalObjectReference{Name: webhook},
			WebhookSecretRef: &corev1.LocalObjectReference{Name: webhook},
			External:         &geassv1alpha1.GeassGitHubExternalApp{Provider: "rungeass", AppSlug: "rungeass", Installations: installations},
		},
	}
}

func forwardingSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: githubapp.ExternalWebhookSecretName("rungeass"), Namespace: platform.SystemNamespace,
			Labels: map[string]string{githubapp.LabelGitHubConnection: "rungeass"}},
		Data: map[string][]byte{githubapp.SecretKeyWebhookSecret: []byte("shared-with-this-cluster")},
	}
}

func externalTokenSecret(id int64, token string, expires time.Time) *corev1.Secret {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: githubapp.ExternalTokenSecretName("rungeass", id), Namespace: platform.SystemNamespace,
		Labels: map[string]string{githubapp.LabelGitHubConnection: "rungeass"}}}
	githubapp.SetExternalToken(secret, token, expires, externalNow)
	return secret
}

func githubConnectionReconciler(t *testing.T, objects ...client.Object) (*GeassGitHubConnectionReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&geassv1alpha1.GeassGitHubConnection{}).Build()
	return &GeassGitHubConnectionReconciler{Client: c, Scheme: scheme, Now: func() time.Time { return externalNow }}, c
}

func reconcileGitHubConnection(t *testing.T, r *GeassGitHubConnectionReconciler, name string) (ctrl.Result, geassv1alpha1.GeassGitHubConnection) {
	t.Helper()
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}})
	require.NoError(t, err)
	var stored geassv1alpha1.GeassGitHubConnection
	require.NoError(t, r.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &stored))
	return result, stored
}

func TestAnExternalConnectionSaysWhichInstallationsHaveATokenAndUntilWhen(t *testing.T) {
	expires := externalNow.Add(45 * time.Minute)
	r, _ := githubConnectionReconciler(t,
		externalGitHubConnection(geassv1alpha1.GeassGitHubInstallation{ID: 42, Account: "acme"}, geassv1alpha1.GeassGitHubInstallation{ID: 43, Account: "beta"}),
		forwardingSecret(), externalTokenSecret(42, "ghs_acme", expires))

	result, stored := reconcileGitHubConnection(t, r, "rungeass")

	require.True(t, stored.Status.Ready, "the forwarding secret is there")
	require.Equal(t, []geassv1alpha1.GeassGitHubInstallationStatus{
		{ID: 42, TokenState: githubapp.TokenValid, TokenExpiresAt: &metav1.Time{Time: expires}},
		{ID: 43, TokenState: githubapp.TokenMissing},
	}, normalizeInstallations(stored.Status.Installations))
	tokens := apimeta.FindStatusCondition(stored.Status.Conditions, githubapp.ConditionTokensReady)
	require.NotNil(t, tokens)
	require.Equal(t, metav1.ConditionFalse, tokens.Status)
	require.Equal(t, "TokenMissing", tokens.Reason)
	require.Contains(t, tokens.Message, "43 (beta)")
	require.NotContains(t, tokens.Message, "42 (acme)")
	require.Equal(t, expires.Add(-githubapp.TokenRenewalWindow).Sub(externalNow)+time.Second, result.RequeueAfter,
		"looked at again when the token is due for renewal")
	requireNoTokenIn(t, stored)
}

func TestTheTokensConditionFollowsTheClock(t *testing.T) {
	app := &geassv1alpha1.GeassGitHubExternalApp{Provider: "rungeass", Installations: []geassv1alpha1.GeassGitHubInstallation{{ID: 42, Account: "acme"}}}
	expires := externalNow.Add(30 * time.Minute)
	held := map[int64]*githubapp.InstallationToken{42: {Token: "ghs_x", ExpiresAt: expires}}
	for name, tc := range map[string]struct {
		at     time.Time
		status metav1.ConditionStatus
		reason string
		next   time.Time
	}{
		"well before it expires":      {externalNow, metav1.ConditionTrue, "TokensValid", expires.Add(-githubapp.TokenRenewalWindow)},
		"due for renewal":             {expires.Add(-5 * time.Minute), metav1.ConditionFalse, "TokenExpiringSoon", expires.Add(-githubapp.TokenUsableMargin)},
		"with less than a minute":     {expires.Add(-30 * time.Second), metav1.ConditionFalse, "TokenExpired", time.Time{}},
		"after it expired":            {expires.Add(time.Hour), metav1.ConditionFalse, "TokenExpired", time.Time{}},
		"at the moment it is renewed": {expires.Add(-githubapp.TokenRenewalWindow), metav1.ConditionFalse, "TokenExpiringSoon", expires.Add(-githubapp.TokenUsableMargin)},
	} {
		report := externalTokens(app, held, tc.at)
		require.Equal(t, tc.status, report.status, name)
		require.Equal(t, tc.reason, report.reason, name)
		require.Equal(t, tc.next, report.next, name)
		require.NotContains(t, report.message, "ghs_x", name)
	}
	soon := externalTokens(app, held, expires.Add(-5*time.Minute))
	require.Contains(t, soon.message, "nothing waits yet", "a late renewal is said to be harmless while the token lasts")
	gone := externalTokens(app, held, expires.Add(time.Hour))
	require.Contains(t, gone.message, "wait until rungeass gives a new one")
}

func TestAnExternalConnectionWithNoInstallationsIsFine(t *testing.T) {
	r, _ := githubConnectionReconciler(t, externalGitHubConnection(), forwardingSecret())

	result, stored := reconcileGitHubConnection(t, r, "rungeass")

	require.True(t, stored.Status.Ready)
	tokens := apimeta.FindStatusCondition(stored.Status.Conditions, githubapp.ConditionTokensReady)
	require.Equal(t, metav1.ConditionTrue, tokens.Status)
	require.Equal(t, "NoInstallations", tokens.Reason)
	require.Zero(t, result.RequeueAfter)
}

func TestAnExternalConnectionWithoutItsForwardingSecretIsNotReady(t *testing.T) {
	empty := forwardingSecret()
	empty.Data = nil
	for name, objects := range map[string][]client.Object{
		"no Secret":    {externalGitHubConnection()},
		"an empty one": {externalGitHubConnection(), empty},
	} {
		r, _ := githubConnectionReconciler(t, objects...)

		_, stored := reconcileGitHubConnection(t, r, "rungeass")

		require.False(t, stored.Status.Ready, name)
		ready := apimeta.FindStatusCondition(stored.Status.Conditions, platform.ConditionReady)
		require.Equal(t, "SecretUnavailable", ready.Reason, name)
		require.Contains(t, ready.Message, "refuses them", name)
	}
}

func TestAProjectsConnectionIsJudgedAsBefore(t *testing.T) {
	r, _ := githubConnectionReconciler(t,
		&geassv1alpha1.GeassGitHubConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "shop-github", Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: "shop-github-token"}},
		},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "shop-github-token", Namespace: platform.SystemNamespace}})

	result, stored := reconcileGitHubConnection(t, r, "shop-github")

	require.Equal(t, ctrl.Result{}, result)
	require.True(t, stored.Status.Ready)
	require.Empty(t, stored.Status.Installations)
	require.Nil(t, apimeta.FindStatusCondition(stored.Status.Conditions, githubapp.ConditionTokensReady))
	require.Equal(t, "ConnectionReady", apimeta.FindStatusCondition(stored.Status.Conditions, platform.ConditionReady).Reason)
}

func TestATokenSecretOfAConnectionWakesTheConnection(t *testing.T) {
	require.Equal(t, "rungeass", connectionOfTokenSecret(context.Background(), externalTokenSecret(42, "t", externalNow))[0].Name)
	require.Empty(t, connectionOfTokenSecret(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "other"}}))
}

// A build that needs the token of an external installation, before the provider has given one, waits and says why; it
// is not failed. Once the token is there it runs, and its clone reads that token.
func TestABuildWaitsForTheProvidersTokenAndThenRuns(t *testing.T) {
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildPending, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.Revision, b.Spec.WaitForCI = "0123456789abcdef0123456789abcdef01234567", false
		b.Spec.ConnectionRef = &corev1.LocalObjectReference{Name: "shop-github"}
		b.Spec.GitCredentialRef = &corev1.LocalObjectReference{Name: "shop-github-token"}
		b.Status.StartedAt, b.Status.CompletedAt, b.Status.ImageDigest = nil, nil, ""
	})
	projectSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "shop-github-token", Namespace: platform.SystemNamespace}}
	githubapp.SetExternalConnection(projectSecret, "rungeass", geassv1alpha1.GeassGitHubInstallation{ID: 42, Account: "acme"})
	r, c, requests := buildTestReconciler(t, build, projectSecret,
		&geassv1alpha1.GeassGitHubConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "shop-github", Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: "shop-github-token"}},
		},
		externalGitHubConnection(geassv1alpha1.GeassGitHubInstallation{ID: 42, Account: "acme"}))

	result := reconcileBuild(t, r, "api-build-1")

	waiting := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildPending, waiting.Status.Phase, "waiting for a token is not a failure")
	require.Contains(t, waiting.Status.FailureReason, "no token for GitHub installation 42 (acme) yet")
	require.Contains(t, waiting.Status.FailureReason, "rungeass")
	require.Equal(t, 30*time.Second, result.RequeueAfter, "and it is tried again")
	require.False(t, jobExists(t, c, "api-build-1-kaniko"))
	require.Zero(t, requests.count.Load(), "Geass never asks GitHub for a token of an external installation")

	require.NoError(t, c.Create(context.Background(), externalTokenSecret(42, "ghs_fromrungeass", time.Now().Add(time.Hour))))
	reconcileBuild(t, r, "api-build-1")

	running := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildRunning, running.Status.Phase)
	require.Empty(t, running.Status.FailureReason)
	require.True(t, jobExists(t, c, "api-build-1-kaniko"))
	var stored corev1.Secret
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(projectSecret), &stored))
	require.Equal(t, "ghs_fromrungeass", string(stored.Data["token"]), "the clone reads the project's Secret, which now has the provider's token")
	require.Zero(t, requests.count.Load())
}

func TestABuildWhoseExternalInstallationWasTakenAwayFails(t *testing.T) {
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildPending, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.Revision, b.Spec.WaitForCI = "0123456789abcdef0123456789abcdef01234567", false
		b.Spec.ConnectionRef = &corev1.LocalObjectReference{Name: "shop-github"}
		b.Spec.GitCredentialRef = &corev1.LocalObjectReference{Name: "shop-github-token"}
		b.Status.StartedAt, b.Status.CompletedAt, b.Status.ImageDigest = nil, nil, ""
	})
	projectSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "shop-github-token", Namespace: platform.SystemNamespace}}
	githubapp.SetExternalConnection(projectSecret, "rungeass", geassv1alpha1.GeassGitHubInstallation{ID: 42, Account: "acme"})
	r, c, _ := buildTestReconciler(t, build, projectSecret, externalGitHubConnection())

	reconcileBuild(t, r, "api-build-1")

	failed := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildFailed, failed.Status.Phase)
	require.Contains(t, failed.Status.FailureReason, "connect the project to GitHub again")
}

// normalizeInstallations drops the monotonic clock and location from the times, which a round trip through the fake
// client does not keep, so that two statuses compare by what they say.
func normalizeInstallations(in []geassv1alpha1.GeassGitHubInstallationStatus) []geassv1alpha1.GeassGitHubInstallationStatus {
	out := make([]geassv1alpha1.GeassGitHubInstallationStatus, len(in))
	for i, entry := range in {
		out[i] = entry
		if entry.TokenExpiresAt != nil {
			out[i].TokenExpiresAt = &metav1.Time{Time: entry.TokenExpiresAt.UTC()}
		}
	}
	return out
}

func requireNoTokenIn(t *testing.T, connection geassv1alpha1.GeassGitHubConnection) {
	t.Helper()
	for _, condition := range connection.Status.Conditions {
		require.False(t, strings.Contains(condition.Message, "ghs_"), "a condition never carries a token: %s", condition.Message)
	}
}
