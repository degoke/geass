package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

const deliveryID = "72d3162e-cc78-11e3-81ab-4c9367dc0958"

// deliveryFixture is one service that builds org/shared and a build that has finished, so that a push has something
// to start again.
func deliveryFixture(t *testing.T) (*Server, githubapp.Config, client.Client) {
	t.Helper()
	cfg := testGitHubAppConfig(t)
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev,
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
			Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{Repository: "org/shared", Branch: "main"}},
		},
	}
	build := &geassv1alpha1.GeassBuild{
		ObjectMeta: metav1.ObjectMeta{Name: "api-build", Namespace: platform.SystemNamespace, Labels: platform.GitBuildLabels(app)},
		Spec:       geassv1alpha1.GeassBuildSpec{App: "api", Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev},
		Status:     geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildSucceeded},
	}
	c := newFakeClient(testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg), app, build)
	return &Server{Client: c}, cfg, c
}

func pushWithDelivery(t *testing.T, cfg githubapp.Config, delivery string, logs *[]string) *http.Request {
	t.Helper()
	req := signedGitHubWebhook(t, cfg.WebhookSecret, map[string]any{
		"repository": map[string]string{"full_name": "org/shared"},
		"ref":        "refs/heads/main",
	})
	if delivery != "" {
		req.Header.Set("X-GitHub-Delivery", delivery)
	}
	if logs != nil {
		logger := funcr.New(func(prefix, args string) { *logs = append(*logs, prefix+" "+args) }, funcr.Options{})
		req = req.WithContext(ctrllog.IntoContext(req.Context(), logger))
	}
	return req
}

func TestTheDeliveryIdIsInTheAnswerAndInEveryLogLineTheHandlerWrites(t *testing.T) {
	srv, cfg, _ := deliveryFixture(t)
	var logs []string

	rec := httptest.NewRecorder()
	srv.handleGitHubWebhook(rec, pushWithDelivery(t, cfg, deliveryID, &logs))

	require.Equal(t, http.StatusOK, rec.Code)
	var answer map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer))
	require.Equal(t, map[string]any{"accepted": true, "triggered": float64(1), "delivery": deliveryID}, answer)
	require.NotEmpty(t, logs, "the handler says what it did")
	for _, line := range logs {
		require.Contains(t, line, deliveryID, "every line names the delivery it is about")
	}
	require.Contains(t, strings.Join(logs, "\n"), "Processed GitHub push webhook")
}

func TestEveryKindOfAnswerCarriesTheDeliveryId(t *testing.T) {
	srv, cfg, _ := deliveryFixture(t)
	body := func(event string, payload any) string {
		req := signedGitHubWebhook(t, cfg.WebhookSecret, payload)
		req.Header.Set("X-GitHub-Event", event)
		req.Header.Set("X-GitHub-Delivery", deliveryID)
		rec := httptest.NewRecorder()
		srv.handleGitHubWebhook(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, event)
		return rec.Body.String()
	}

	require.JSONEq(t, `{"accepted":true,"event":"ping","delivery":"`+deliveryID+`"}`, body("ping", map[string]any{"zen": "x"}))
	require.JSONEq(t, `{"accepted":true,"ignored":true,"delivery":"`+deliveryID+`"}`, body("issues", map[string]any{"action": "opened"}))
	require.JSONEq(t, `{"accepted":true,"triggered":0,"delivery":"`+deliveryID+`"}`, body("push", map[string]any{"ref": "refs/heads/main"}))
	require.JSONEq(t, `{"accepted":true,"environments":null,"delivery":"`+deliveryID+`"}`, body("pull_request", map[string]any{"action": "opened"}))
}

func TestAnAnswerHasNoDeliveryWhenTheSenderGaveNone(t *testing.T) {
	srv, cfg, _ := deliveryFixture(t)

	rec := httptest.NewRecorder()
	srv.handleGitHubWebhook(rec, pushWithDelivery(t, cfg, "", nil))

	require.JSONEq(t, `{"accepted":true,"triggered":1}`, rec.Body.String())
}

func TestADeliveryIdThatIsNotAnIdIsLeftOut(t *testing.T) {
	srv, cfg, _ := deliveryFixture(t)
	for name, bad := range map[string]string{
		"spaces":     "not an id",
		"a quote":    `abc"def`,
		"a newline":  "abc\ndef",
		"a slash":    "abc/def",
		"a script":   "<script>alert(1)</script>",
		"too long":   strings.Repeat("a", maxDeliveryIDLength+1),
		"only space": "   ",
	} {
		t.Run(name, func(t *testing.T) {
			var logs []string
			req := pushWithDelivery(t, cfg, "", &logs)
			req.Header["X-Github-Delivery"] = []string{bad}

			rec := httptest.NewRecorder()
			srv.handleGitHubWebhook(rec, req)

			require.Equal(t, http.StatusOK, rec.Code, "the delivery is still processed")
			require.NotContains(t, rec.Body.String(), "delivery")
			if strings.TrimSpace(bad) != "" {
				for _, line := range logs {
					require.NotContains(t, line, strings.TrimSpace(bad)[:min(len(strings.TrimSpace(bad)), 8)], "what is not an id is not logged")
				}
			}
		})
	}
}

func TestARedeliveryWithTheSameIdIsProcessedAgain(t *testing.T) {
	// A manual redelivery from the app's settings page keeps the delivery's id. It is how someone retries a push that
	// the dashboard failed to handle, so it must do what the delivery did: the id is never used to skip one.
	srv, cfg, c := deliveryFixture(t)
	build := func() geassv1alpha1.GeassBuild {
		var b geassv1alpha1.GeassBuild
		require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "api-build", Namespace: platform.SystemNamespace}, &b))
		return b
	}

	rec := httptest.NewRecorder()
	srv.handleGitHubWebhook(rec, pushWithDelivery(t, cfg, deliveryID, nil))
	require.Contains(t, rec.Body.String(), `"triggered":1`)
	started := build()
	require.Equal(t, geassv1alpha1.GeassBuildPending, started.Status.Phase, "the first delivery started the build again")

	// The build ran and finished, and the same delivery arrives again.
	started.Status.Phase = geassv1alpha1.GeassBuildSucceeded
	require.NoError(t, c.Status().Update(context.Background(), &started))
	rec = httptest.NewRecorder()
	srv.handleGitHubWebhook(rec, pushWithDelivery(t, cfg, deliveryID, nil))

	require.Contains(t, rec.Body.String(), `"triggered":1`, "it is handled as the new event it is")
	require.Contains(t, rec.Body.String(), deliveryID)
	require.Equal(t, geassv1alpha1.GeassBuildPending, build().Status.Phase, "and starts the build again")
}

func TestARefusedDeliveryIsLoggedWithItsIdToo(t *testing.T) {
	srv, cfg, _ := deliveryFixture(t)
	var logs []string
	req := pushWithDelivery(t, cfg, deliveryID, &logs)
	req.Header.Set("X-Hub-Signature-256", "sha256=wrong")

	rec := httptest.NewRecorder()
	srv.handleGitHubWebhook(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Len(t, logs, 1)
	require.Contains(t, logs[0], deliveryID)
	require.Contains(t, logs[0], "signature does not match")
	require.NotContains(t, logs[0], cfg.WebhookSecret, "the secret is never logged")
}
