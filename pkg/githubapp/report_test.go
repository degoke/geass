package githubapp

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// reportFixture is a project connected to GitHub, a GitHub that records what it is sent, and the Resolver between.
type reportFixture struct {
	gh       *fakeGitHub
	client   client.Client
	resolver *Resolver
	guard    *Guard
	clock    *clock
}

type fixtureOptions struct {
	reportingOff bool
	noConnection bool
}

func newReportFixture(t *testing.T, opts fixtureOptions) *reportFixture {
	t.Helper()
	gh := newFakeGitHub(t)
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: []string{"production"}},
	}
	objects := []client.Object{
		project,
		&geassv1alpha1.GeassGitHubConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "shop-github", Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: "shop-github-token"}},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "shop-github-token", Namespace: platform.SystemNamespace},
			Data:       map[string][]byte{"token": []byte("tok-from-secret")},
		},
		&geassv1alpha1.GeassPlatformConfig{
			ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassPlatformConfigSpec{DashboardURL: "https://geass.example.com/"},
		},
	}
	if !opts.noConnection {
		project.Spec.GitHubConnectionRef = &corev1.LocalObjectReference{Name: "shop-github"}
	}
	if opts.reportingOff {
		off := false
		project.Spec.GitHubReporting = &off
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassGitHubConnection{}).Build()
	target, err := url.Parse(gh.server.URL)
	require.NoError(t, err)
	guard, clock := newTestGuard()
	return &reportFixture{
		gh: gh, client: c, guard: guard, clock: clock,
		resolver: &Resolver{Client: c, HTTP: &http.Client{Transport: roundTripHostRewrite{target: target}}},
	}
}

func (f *reportFixture) postStatus(t *testing.T, key string) Result {
	t.Helper()
	return f.resolver.Do(context.Background(), f.guard, Report{
		Project: "shop", Repository: "acme/web", Capability: CapabilityStatuses, Key: key,
		Send: func(ctx context.Context, target *Target) error {
			return target.CreateCommitStatus(ctx, testSHA, CommitStatus{State: StatusSuccess, Context: "geass/build"})
		},
	})
}

func (f *reportFixture) reportingCondition(t *testing.T) *metav1.Condition {
	t.Helper()
	var connection geassv1alpha1.GeassGitHubConnection
	require.NoError(t, f.client.Get(context.Background(), client.ObjectKey{Name: "shop-github", Namespace: platform.SystemNamespace}, &connection))
	for i := range connection.Status.Conditions {
		if connection.Status.Conditions[i].Type == ConditionReporting {
			return &connection.Status.Conditions[i]
		}
	}
	return nil
}

const statusesPath = "/repos/acme/web/statuses/" + testSHA

func TestAReportIsSentWithTheProjectsConnectionToken(t *testing.T) {
	f := newReportFixture(t, fixtureOptions{})
	f.gh.answer(http.MethodPost, statusesPath, http.StatusCreated, map[string]any{})

	result := f.postStatus(t, "build/a")

	require.Equal(t, Sent, result.Outcome)
	require.True(t, result.Outcome.Done())
	require.Len(t, f.gh.all(), 1)
	require.Equal(t, "Bearer tok-from-secret", f.gh.all()[0].Auth)
	require.Equal(t, "https://geass.example.com", result.Target.DashboardURL)
	condition := f.reportingCondition(t)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionTrue, condition.Status, "the connection says GitHub takes the reports")
}

func TestAProjectWithReportingTurnedOffSendsNothing(t *testing.T) {
	f := newReportFixture(t, fixtureOptions{reportingOff: true})
	f.gh.answer(http.MethodPost, statusesPath, http.StatusCreated, map[string]any{})

	result := f.postStatus(t, "build/a")

	require.Equal(t, Skipped, result.Outcome)
	require.ErrorIs(t, result.Err, ErrReportingOff)
	require.False(t, result.Outcome.Done(), "nothing was reported, so nothing is recorded as reported")
	require.Zero(t, f.gh.count())
	require.Nil(t, f.reportingCondition(t), "and the connection is not touched")
}

func TestAProjectWithoutAGitHubConnectionSendsNothing(t *testing.T) {
	f := newReportFixture(t, fixtureOptions{noConnection: true})

	result := f.postStatus(t, "build/a")

	require.Equal(t, Skipped, result.Outcome)
	require.ErrorIs(t, result.Err, ErrNotConnected)
	require.Zero(t, f.gh.count())
}

func TestAProjectThatIsGoneOrARepositoryThatIsNotOneSendsNothing(t *testing.T) {
	f := newReportFixture(t, fixtureOptions{})
	result := f.resolver.Do(context.Background(), f.guard, Report{Project: "ghost", Repository: "acme/web", Capability: CapabilityStatuses, Key: "k",
		Send: func(context.Context, *Target) error { return nil }})
	require.Equal(t, Skipped, result.Outcome)

	result = f.resolver.Do(context.Background(), f.guard, Report{Project: "shop", Repository: "not a repository", Capability: CapabilityStatuses, Key: "k",
		Send: func(context.Context, *Target) error { return nil }})
	require.Equal(t, Skipped, result.Outcome)
	require.Zero(t, f.gh.count())
}

func TestAMissingPermissionIsNotRetriedAndSaysHowToFixIt(t *testing.T) {
	f := newReportFixture(t, fixtureOptions{})
	f.gh.forbid(http.MethodPost, statusesPath)

	first := f.postStatus(t, "build/a")

	require.Equal(t, Refused, first.Outcome)
	require.False(t, first.Outcome.Done(), "it is still owed: recording it as reported would stop it being sent once the permission is approved")
	require.Equal(t, RefusedBackoff, first.RetryAfter, "and the caller is told when to try it again")
	require.Equal(t, 1, f.gh.count())
	condition := f.reportingCondition(t)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, ReasonPermissionMissing, condition.Reason)
	require.Contains(t, condition.Message, "Commit statuses", "it names the permission")
	require.Contains(t, condition.Message, "Accept new permissions", "and how to approve it")
	require.Contains(t, condition.Message, "builds and deploys carry on")

	// Every later report of the kind is held back without asking GitHub.
	for i := range 5 {
		second := f.postStatus(t, "build/"+string(rune('b'+i)))
		require.Equal(t, Skipped, second.Outcome)
		require.False(t, second.Outcome.Done(), "a held-back report is owed too")
		require.Positive(t, second.RetryAfter, "and says when the hold-back ends")
		require.LessOrEqual(t, second.RetryAfter, RefusedBackoff)
	}
	require.Equal(t, 1, f.gh.count(), "no retry storm")

	// A report of another kind is not affected: it needs a different permission.
	f.gh.answer(http.MethodPost, "/repos/acme/web/deployments", http.StatusCreated, map[string]any{"id": 5})
	deployed := f.resolver.Do(context.Background(), f.guard, Report{Project: "shop", Repository: "acme/web", Capability: CapabilityDeployments, Key: "deploy/a",
		Send: func(ctx context.Context, target *Target) error {
			_, err := target.CreateDeployment(ctx, Deployment{Ref: testSHA, Environment: "production"})
			return err
		}})
	require.Equal(t, Sent, deployed.Outcome)
	require.Equal(t, metav1.ConditionFalse, f.reportingCondition(t).Status, "the connection still says statuses are refused")
}

func TestReportsResumeOnceThePermissionIsApproved(t *testing.T) {
	f := newReportFixture(t, fixtureOptions{})
	f.gh.forbid(http.MethodPost, statusesPath)
	require.Equal(t, Refused, f.postStatus(t, "build/a").Outcome)

	// The owner approves the permission, and the backoff runs out.
	f.gh.answer(http.MethodPost, statusesPath, http.StatusCreated, map[string]any{})
	f.clock.advance(RefusedBackoff + time.Second)
	result := f.postStatus(t, "build/b")

	require.Equal(t, Sent, result.Outcome)
	require.Equal(t, metav1.ConditionTrue, f.reportingCondition(t).Status, "the condition comes back")
}

func TestARefusalIsLoggedOnceNotOnEveryReport(t *testing.T) {
	f := newReportFixture(t, fixtureOptions{})
	f.gh.forbid(http.MethodPost, statusesPath)

	require.Equal(t, Refused, f.postStatus(t, "build/a").Outcome)
	// Held back, then asked again after the backoff, and refused again: the same problem.
	f.clock.advance(RefusedBackoff + time.Second)
	require.Equal(t, Refused, f.postStatus(t, "build/b").Outcome)

	require.False(t, f.guard.Once("shop-github|refused|"+CapabilityStatuses), "it was already logged the first time")
}

func TestAGitHubThatCannotBeReachedIsTriedAFewTimesAndThenGivenUpOn(t *testing.T) {
	f := newReportFixture(t, fixtureOptions{})
	f.gh.answer(http.MethodPost, statusesPath, http.StatusBadGateway, map[string]string{"message": "Bad Gateway"})

	first := f.postStatus(t, "build/a")
	require.Equal(t, Failed, first.Outcome)
	require.False(t, first.Outcome.Done(), "it may work later, so it is not recorded as reported")
	require.GreaterOrEqual(t, first.RetryAfter, RetrySpacing)
	require.Nil(t, f.reportingCondition(t), "an outage is not a missing permission")

	tooSoon := f.postStatus(t, "build/a")
	require.Equal(t, Failed, tooSoon.Outcome)
	require.Equal(t, 1, f.gh.count(), "not asked again before the gap")

	f.clock.advance(RetrySpacing)
	require.Equal(t, Failed, f.postStatus(t, "build/a").Outcome)
	f.clock.advance(RetrySpacing)
	require.Equal(t, Failed, f.postStatus(t, "build/a").Outcome)
	require.Equal(t, 3, f.gh.count())

	f.clock.advance(RetrySpacing)
	last := f.postStatus(t, "build/a")
	require.Equal(t, GaveUp, last.Outcome)
	require.False(t, last.Outcome.Done(), "a report that could not be made is left owed, not recorded as sent: GitHub would show the old state for ever")
	require.Equal(t, RefusedBackoff, last.RetryAfter, "it is tried again after a long pause, not on every pass")
	require.Equal(t, 3, f.gh.count())
}

func TestAReportGitHubRejectsIsNotRetried(t *testing.T) {
	f := newReportFixture(t, fixtureOptions{})
	f.gh.answer(http.MethodPost, statusesPath, http.StatusUnprocessableEntity, map[string]string{"message": "No commit found for SHA"})

	result := f.postStatus(t, "build/a")

	require.Equal(t, Rejected, result.Outcome)
	require.True(t, result.Outcome.Done())
	require.Equal(t, 1, f.gh.count())
	require.Nil(t, f.reportingCondition(t), "a request GitHub did not like says nothing about permissions")
	require.False(t, f.guard.Blocked("shop-github", CapabilityStatuses))
}

func TestARateLimitIsWaitedOutNotTakenForAMissingPermission(t *testing.T) {
	f := newReportFixture(t, fixtureOptions{})
	f.gh.answer(http.MethodPost, statusesPath, http.StatusTooManyRequests, map[string]string{"message": "slow down"})

	result := f.postStatus(t, "build/a")

	require.Equal(t, Failed, result.Outcome)
	require.False(t, f.guard.Blocked("shop-github", CapabilityStatuses))
	require.Nil(t, f.reportingCondition(t))
}

// makeInstallation turns the project's connection into one made through the GitHub App, whose tokens are made by GitHub.
func (f *reportFixture) makeInstallation(t *testing.T) {
	t.Helper()
	secret := &corev1.Secret{}
	require.NoError(t, f.client.Get(context.Background(), client.ObjectKey{Name: "shop-github-token", Namespace: platform.SystemNamespace}, secret))
	secret.Data["installation_id"] = []byte("42")
	require.NoError(t, f.client.Update(context.Background(), secret))
	appSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "platform-github-app", Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			SecretKeyAppID: []byte("12345"), SecretKeyClientID: []byte("c"), SecretKeyClientSecret: []byte("s"),
			SecretKeySlug: []byte("geass-example"), SecretKeyPrivateKey: []byte(testPrivateKeyPEM(t)), SecretKeyWebhookSecret: []byte("w"),
		},
	}
	require.NoError(t, f.client.Create(context.Background(), appSecret))
	config := &geassv1alpha1.GeassPlatformConfig{}
	require.NoError(t, f.client.Get(context.Background(), client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, config))
	config.Spec.GitHubAppRef = &corev1.LocalObjectReference{Name: "platform-github-app"}
	require.NoError(t, f.client.Update(context.Background(), config))
}

func TestAnInstallationTokenThatCannotBeMadeIsATransientFailureAndNothingIsSent(t *testing.T) {
	f := newReportFixture(t, fixtureOptions{})
	f.makeInstallation(t)
	f.gh.answer(http.MethodPost, "/app/installations/42/access_tokens", http.StatusInternalServerError, map[string]string{"message": "boom"})
	f.gh.answer(http.MethodPost, statusesPath, http.StatusCreated, map[string]any{})

	result := f.postStatus(t, "build/a")

	require.Equal(t, Failed, result.Outcome)
	require.GreaterOrEqual(t, result.RetryAfter, RetrySpacing)
	for _, request := range f.gh.all() {
		require.NotEqual(t, statusesPath, request.Path, "with no token there is nothing to report with")
	}
}

func TestTheTargetKnowsTheAppsNameWhenTheConnectionIsAnInstallation(t *testing.T) {
	f := newReportFixture(t, fixtureOptions{})
	f.makeInstallation(t)
	f.gh.answer(http.MethodPost, "/app/installations/42/access_tokens", http.StatusCreated, map[string]string{"token": "installation-token"})

	target, err := f.resolver.Resolve(context.Background(), "shop", "", "https://github.com/acme/web.git")

	require.NoError(t, err)
	require.Equal(t, "installation-token", target.Token, "a fresh installation token, not the stale one in the secret")
	require.Equal(t, "geass-example", target.AppSlug)
	require.Equal(t, "geass-example[bot]", target.BotLogin, "so only the app's own comments are edited")
	require.Equal(t, "acme/web", target.Repository)
}

func TestARefusalMessageNamesThePermissionAndTheWayToApproveIt(t *testing.T) {
	message := RefusalMessage(CapabilityComments, "geass-example")
	require.Contains(t, message, "Pull requests")
	require.Contains(t, message, "https://github.com/settings/apps/geass-example/permissions")
	require.True(t, strings.Contains(message, "Read and write"))
	require.Equal(t, "GitHub refused a report from Geass", RefusalMessage("nonsense", ""))
}
