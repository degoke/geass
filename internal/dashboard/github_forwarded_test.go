package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

// externalObjects are an external connection that lists installation 42, its forwarding secret, and the project payments
// connected through it, as the dashboard leaves them once a project is connected.
func externalObjects() []client.Object {
	webhook := githubapp.ExternalWebhookSecretName("rungeass")
	projectSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "payments-github-token", Namespace: platform.SystemNamespace}}
	githubapp.SetExternalConnection(projectSecret, "rungeass", geassv1alpha1.GeassGitHubInstallation{ID: 42, Account: "acme"})
	return []client.Object{
		&geassv1alpha1.GeassGitHubConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "rungeass", Namespace: platform.SystemNamespace},
			Spec: geassv1alpha1.GeassGitHubConnectionSpec{
				SecretRef: corev1.LocalObjectReference{Name: webhook}, WebhookSecretRef: &corev1.LocalObjectReference{Name: webhook},
				External: &geassv1alpha1.GeassGitHubExternalApp{Provider: "rungeass", Installations: []geassv1alpha1.GeassGitHubInstallation{{ID: 42, Account: "acme"}}},
			},
		},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: webhook, Namespace: platform.SystemNamespace}, Data: map[string][]byte{githubapp.SecretKeyWebhookSecret: []byte(testForwardingSecret)}},
		&geassv1alpha1.GeassGitHubConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "payments-github", Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: "payments-github-token"}, WebhookSecretRef: &corev1.LocalObjectReference{Name: webhook}},
		},
		projectSecret,
	}
}

// forwardedServer is a dashboard whose service api builds org/api from the project payments, which is connected to
// GitHub through the provider's installation 42. It has no App of its own.
func forwardedServer(t *testing.T, builds ...client.Object) (http.Handler, client.Client) {
	t.Helper()
	app := gitApp()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"},
			GitHubConnectionRef: &corev1.LocalObjectReference{Name: "payments-github"},
		},
	}
	objects := append(append([]client.Object{project, app}, externalObjects()...), builds...)
	c := newFakeClient(objects...)
	srv := &Server{Client: c}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	return mux, c
}

func forwardedRequest(event string, body []byte, signature string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github/forwarded/rungeass", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", "0b5d7a2e-0000-4000-8000-000000000001")
	if signature != "" {
		req.Header.Set(githubapp.ForwardSignatureHeader, signature)
	}
	return req
}

func forward(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func signedForward(t *testing.T, event string, payload any, at time.Time) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return forwardedRequest(event, body, githubapp.SignForwarded(testForwardingSecret, at.Unix(), event, body))
}

func TestAForwardedPushBuildsWhatAPushFromGitHubBuilds(t *testing.T) {
	app := gitApp()
	handler, c := forwardedServer(t, operatorBuild(app, "api-build", time.Hour, geassv1alpha1.GeassBuildSucceeded))

	rec := forward(handler, signedForward(t, "push", pushPayload(), time.Now()))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"triggered":1`)
	require.Contains(t, rec.Body.String(), `"delivery":"0b5d7a2e-0000-4000-8000-000000000001"`)
	require.Equal(t, geassv1alpha1.GeassBuildPending, buildOf(t, c, "api-build").Status.Phase, "the service's build runs again")
}

// The same push, through GitHub's own webhook on a cluster with its own App and through the provider's on a cluster with
// none, does the same: the code that handles it is one.
func TestAForwardedEventAndADirectOneDoTheSame(t *testing.T) {
	for name, payload := range map[string]map[string]any{
		"a push to the branch":           pushPayload(),
		"a push to another branch":       func() map[string]any { p := pushPayload(); p["ref"] = "refs/heads/other"; return p }(),
		"a push that deleted the branch": func() map[string]any { p := pushPayload(); p["deleted"] = true; return p }(),
		"a push to another repository": func() map[string]any {
			p := pushPayload()
			p["repository"] = map[string]string{"full_name": "org/else"}
			return p
		}(),
		"a push of files it does not watch": func() map[string]any {
			p := pushPayload([]string{"docs/readme.md"})
			return p
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			watching := func(a *geassv1alpha1.GeassApp) { a.Spec.Source.Git.WatchPatterns = []string{"src/**"} }
			directApp, forwardedApp := gitApp(watching), gitApp(watching)
			direct, directClient, secret := pushServer(t, directApp, operatorBuild(directApp, "api-build", time.Hour, geassv1alpha1.GeassBuildSucceeded))
			handler, forwardedClient := forwardedServer(t, operatorBuild(forwardedApp, "api-build", time.Hour, geassv1alpha1.GeassBuildSucceeded))
			require.NoError(t, forwardedClient.Update(context.Background(), withWatch(t, forwardedClient)))

			directAnswer := push(t, direct, secret, payload)
			req := signedForward(t, "push", payload, time.Now())
			req.Header.Del("X-GitHub-Delivery") // GitHub's test request names none either
			forwarded := forward(handler, req)

			require.Equal(t, http.StatusOK, forwarded.Code, forwarded.Body.String())
			require.JSONEq(t, directAnswer, forwarded.Body.String())
			require.Equal(t, buildOf(t, directClient, "api-build").Status.Phase, buildOf(t, forwardedClient, "api-build").Status.Phase)
		})
	}
}

func withWatch(t *testing.T, c client.Client) *geassv1alpha1.GeassApp {
	t.Helper()
	var app geassv1alpha1.GeassApp
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "api", Namespace: platform.SystemNamespace}, &app))
	app.Spec.Source.Git.WatchPatterns = []string{"src/**"}
	return &app
}

func TestAForwardedPullRequestMakesTheSameEnvironmentAsOneFromGitHub(t *testing.T) {
	srv, c := prFixture(t, enabledPR(), externalObjects()...)
	project := projectOf(t, c)
	project.Spec.GitHubConnectionRef = &corev1.LocalObjectReference{Name: "payments-github"}
	require.NoError(t, c.Update(context.Background(), &project))
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	payload := prPayload("opened", 7, "feature-x")
	payload.Installation.ID = 42

	rec := forward(mux, signedForward(t, "pull_request", payload, time.Now()))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"environment":"pr-7"`)
	require.Equal(t, []string{"production", "pr-7"}, projectOf(t, c).Spec.Environments)

	closed := prPayload("closed", 7, "feature-x")
	closed.Installation.ID = 42
	require.Equal(t, http.StatusOK, forward(mux, signedForward(t, "pull_request", closed, time.Now())).Code)
	require.Equal(t, []string{"production"}, projectOf(t, c).Spec.Environments, "closing it takes the environment away again")
}

func TestAForwardedEventIsRefusedWithoutTheClustersSignature(t *testing.T) {
	body, err := json.Marshal(pushPayload())
	require.NoError(t, err)
	now := time.Now()
	good := githubapp.SignForwarded(testForwardingSecret, now.Unix(), "push", body)
	for name, tc := range map[string]struct {
		path, event, signature string
		body                   []byte
		status                 int
		says                   string
	}{
		"no signature":                   {"rungeass", "push", "", body, http.StatusUnauthorized, "not signed"},
		"another secret":                 {"rungeass", "push", githubapp.SignForwarded("not-the-clusters-secret-0123456789", now.Unix(), "push", body), body, http.StatusUnauthorized, "invalid webhook signature"},
		"GitHub's own signature":         {"rungeass", "push", hmacSHA256(testForwardingSecret, body), body, http.StatusUnauthorized, "invalid webhook signature"},
		"a changed body":                 {"rungeass", "push", good, append(append([]byte{}, body...), ' '), http.StatusUnauthorized, "invalid webhook signature"},
		"another event than was signed":  {"rungeass", "pull_request", good, body, http.StatusUnauthorized, "invalid webhook signature"},
		"a connection that is not there": {"nobody", "push", good, body, http.StatusUnauthorized, "invalid webhook signature"},
		"a signature from long ago":      {"rungeass", "push", githubapp.SignForwarded(testForwardingSecret, now.Add(-10*time.Minute).Unix(), "push", body), body, http.StatusUnauthorized, "too old"},
		"no event":                       {"rungeass", "", good, body, http.StatusBadRequest, "X-GitHub-Event"},
	} {
		t.Run(name, func(t *testing.T) {
			app := gitApp()
			handler, c := forwardedServer(t, operatorBuild(app, "api-build", time.Hour, geassv1alpha1.GeassBuildSucceeded))
			req := forwardedRequest(tc.event, tc.body, tc.signature)
			req.URL.Path = "/webhooks/github/forwarded/" + tc.path

			rec := forward(handler, req)

			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), tc.says)
			require.Equal(t, geassv1alpha1.GeassBuildSucceeded, buildOf(t, c, "api-build").Status.Phase, "nothing was built")
		})
	}
}

func TestAConnectionWithoutItsSecretRefusesLikeAWrongSignature(t *testing.T) {
	handler, c := forwardedServer(t)
	require.NoError(t, c.Delete(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: githubapp.ExternalWebhookSecretName("rungeass"), Namespace: platform.SystemNamespace}}))
	body, _ := json.Marshal(pushPayload())
	// Signed with nothing at all: an empty secret must never be one that matches.
	for _, secret := range []string{"", "geass-no-forwarding-secret"} {
		rec := forward(handler, forwardedRequest("push", body, githubapp.SignForwarded(secret, time.Now().Unix(), "push", body)))
		require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "invalid webhook signature")
	}
}

func TestACopyOfAForwardedEventIsNotDoneTwice(t *testing.T) {
	app := gitApp()
	handler, c := forwardedServer(t, operatorBuild(app, "api-build", time.Hour, geassv1alpha1.GeassBuildSucceeded))
	body, err := json.Marshal(pushPayload())
	require.NoError(t, err)
	signature := githubapp.SignForwarded(testForwardingSecret, time.Now().Unix(), "push", body)

	require.Equal(t, http.StatusOK, forward(handler, forwardedRequest("push", body, signature)).Code)
	markBuildSucceeded(t, c, "api-build")

	again := forward(handler, forwardedRequest("push", body, signature))
	require.Equal(t, http.StatusConflict, again.Code, again.Body.String())
	require.Contains(t, again.Body.String(), "already taken")
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, buildOf(t, c, "api-build").Status.Phase, "the copy built nothing")

	// The provider sending the event again, signed again, is a new delivery and is done, as GitHub redelivering one is.
	resent := forward(handler, forwardedRequest("push", body, githubapp.SignForwarded(testForwardingSecret, time.Now().Add(time.Second).Unix(), "push", body)))
	require.Equal(t, http.StatusOK, resent.Code, resent.Body.String())
}

func markBuildSucceeded(t *testing.T, c client.Client, name string) {
	t.Helper()
	build := buildOf(t, c, name)
	build.Status.Phase = geassv1alpha1.GeassBuildSucceeded
	require.NoError(t, c.Status().Update(context.Background(), &build))
}

func TestAForwardedEventLargerThanTheLimitIsRefused(t *testing.T) {
	handler, _ := forwardedServer(t)
	body := []byte(`{"pad":"` + strings.Repeat("a", githubWebhookMaxBody) + `"}`)

	rec := forward(handler, forwardedRequest("push", body, githubapp.SignForwarded(testForwardingSecret, time.Now().Unix(), "push", body)))

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestAForwardedEventOnlyCountsForTheInstallationItNames(t *testing.T) {
	for name, tc := range map[string]struct {
		installation any
		status       int
		says         string
	}{
		"an installation the connection does not list": {map[string]any{"id": 99}, http.StatusUnprocessableEntity, "not one of this connection's"},
		"no installation":                 {nil, http.StatusOK, `"ignored":true`},
		"an installation id of zero":      {map[string]any{"id": 0}, http.StatusOK, `"ignored":true`},
		"the installation of the project": {map[string]any{"id": 42}, http.StatusOK, `"triggered":1`},
	} {
		t.Run(name, func(t *testing.T) {
			app := gitApp()
			handler, _ := forwardedServer(t, operatorBuild(app, "api-build", time.Hour, geassv1alpha1.GeassBuildSucceeded))
			payload := pushPayload()
			if tc.installation == nil {
				delete(payload, "installation")
			} else {
				payload["installation"] = tc.installation
			}

			rec := forward(handler, signedForward(t, "push", payload, time.Now()))

			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), tc.says)
		})
	}
}

// With organizations off, a push from GitHub that names no installation still builds, as it always did. A forwarded
// event must name one, and only the projects connected to it count: the App it comes from serves many clusters.
func TestAForwardedEventNeverBuildsAProjectOfAnotherInstallation(t *testing.T) {
	app := gitApp()
	handler, c := forwardedServer(t, operatorBuild(app, "api-build", time.Hour, geassv1alpha1.GeassBuildSucceeded))
	// The connection lists another installation too, but the project is connected to 42.
	var connection geassv1alpha1.GeassGitHubConnection
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "rungeass", Namespace: platform.SystemNamespace}, &connection))
	connection.Spec.External.Installations = append(connection.Spec.External.Installations, geassv1alpha1.GeassGitHubInstallation{ID: 7, Account: "other"})
	require.NoError(t, c.Update(context.Background(), &connection))
	payload := pushPayload()
	payload["installation"] = map[string]any{"id": 7}

	rec := forward(handler, signedForward(t, "push", payload, time.Now()))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"triggered":0`)
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, buildOf(t, c, "api-build").Status.Phase)
}

func TestAForwardedPingIsAnswered(t *testing.T) {
	handler, _ := forwardedServer(t)

	rec := forward(handler, signedForward(t, "ping", map[string]any{"zen": "z", "hook_id": 1}, time.Now()))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"event":"ping"`)
}

// The forwarded webhook is reached without signing in: its signature is the credential. Every other method is refused.
func TestTheForwardedWebhookIsPublicAndTakesOnlyPost(t *testing.T) {
	handler, _ := forwardedServer(t)
	require.True(t, dashboardPublicPath(httptest.NewRequest(http.MethodPost, "/webhooks/github/forwarded/rungeass", nil)))

	rec := forward(handler, httptest.NewRequest(http.MethodGet, "/webhooks/github/forwarded/rungeass", nil))

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// failingAppLists is a cluster whose API server fails to list services while fail is set.
type failingAppLists struct {
	client.Client
	fail *bool
}

func (f failingAppLists) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, apps := list.(*geassv1alpha1.GeassAppList); apps && *f.fail {
		return errors.New("the API server is unwell")
	}
	return f.Client.List(ctx, list, opts...)
}

// A forwarded event Geass could not do (it answered 5xx) may be sent again as it was: only what was done is remembered.
func TestAForwardedEventThatFailedMayBeSentAgainAsItWas(t *testing.T) {
	app := gitApp()
	_, base := forwardedServer(t, operatorBuild(app, "api-build", time.Hour, geassv1alpha1.GeassBuildSucceeded))
	fail := true
	srv := &Server{Client: failingAppLists{Client: base, fail: &fail}}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	body, err := json.Marshal(pushPayload())
	require.NoError(t, err)
	signature := githubapp.SignForwarded(testForwardingSecret, time.Now().Unix(), "push", body)

	failed := forward(mux, forwardedRequest("push", body, signature))
	require.Equal(t, http.StatusInternalServerError, failed.Code, failed.Body.String())

	fail = false
	again := forward(mux, forwardedRequest("push", body, signature))
	require.Equal(t, http.StatusOK, again.Code, again.Body.String())
	require.Contains(t, again.Body.String(), `"triggered":1`)
	require.Equal(t, http.StatusConflict, forward(mux, forwardedRequest("push", body, signature)).Code, "once it was done, a copy is refused")
}
