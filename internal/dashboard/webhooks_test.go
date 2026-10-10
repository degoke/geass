package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/webhooks"
)

func webhookServer(t *testing.T, hooks ...geassv1alpha1.GeassWebhook) (*Server, client.Client) {
	t.Helper()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{DisplayName: "Payments", Environments: []string{"production"}, Webhooks: hooks},
	}
	c := newFakeClient(project)
	return &Server{Client: c}, c
}

func postWebhook(t *testing.T, srv *Server, operation string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, withAdminSession(t, srv, postForm("/projects/"+testProjectName+"/webhooks/"+operation, form)))
	return rec
}

func projectOf(t *testing.T, c client.Client) geassv1alpha1.GeassProject {
	t.Helper()
	var project geassv1alpha1.GeassProject
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &project))
	return project
}

func signingSecrets(t *testing.T, c client.Client) map[string]string {
	t.Helper()
	var secret corev1.Secret
	if err := c.Get(context.Background(), client.ObjectKey{Name: webhooks.SecretName(testProjectName), Namespace: platform.SystemNamespace}, &secret); err != nil {
		return nil
	}
	out := map[string]string{}
	for key, value := range secret.Data {
		out[key] = string(value)
	}
	return out
}

func TestCreatingAWebhookStoresTheSecretSeparatelyAndRevealsItOnce(t *testing.T) {
	srv, c := webhookServer(t)

	rec := postWebhook(t, srv, "save", url.Values{"url": {"https://hooks.example.com/geass"}, "event": {"deployment.failed", "deployment.failed"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var created struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.Regexp(t, "^[0-9a-f]{8}$", created.ID)
	require.True(t, strings.HasPrefix(created.Secret, "whsec_"), "the secret is shown to the user when the webhook is created")

	project := projectOf(t, c)
	require.Len(t, project.Spec.Webhooks, 1)
	hook := project.Spec.Webhooks[0]
	require.Equal(t, created.ID, hook.ID)
	require.Equal(t, "https://hooks.example.com/geass", hook.URL)
	require.Equal(t, []string{"deployment.failed"}, hook.Events, "duplicate events collapse")
	require.NotContains(t, mustJSON(t, project.Spec), created.Secret, "the signing secret must never be stored in the project spec")
	require.Equal(t, map[string]string{created.ID: created.Secret}, signingSecrets(t, c))
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func TestSubscribingToEveryEventMeansNoFilter(t *testing.T) {
	srv, c := webhookServer(t)
	rec := postWebhook(t, srv, "save", url.Values{"url": {"https://hooks.example.com/a"}, "event": webhooks.Events})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Empty(t, projectOf(t, c).Spec.Webhooks[0].Events, "new event types should reach a webhook that wanted everything")
}

func TestWebhookInputIsValidated(t *testing.T) {
	srv, c := webhookServer(t)
	for name, form := range map[string]url.Values{
		"localhost":     {"url": {"http://localhost:8080/hook"}},
		"loopback":      {"url": {"http://127.0.0.1/hook"}},
		"metadata":      {"url": {"http://169.254.169.254/latest"}},
		"private":       {"url": {"http://10.43.0.5/hook"}},
		"not a url":     {"url": {"nope"}},
		"credentials":   {"url": {"https://user:pw@example.com/"}},
		"unknown event": {"url": {"https://example.com/a"}, "event": {"deployment.exploded"}},
		"test event":    {"url": {"https://example.com/a"}, "event": {"test"}},
	} {
		t.Run(name, func(t *testing.T) {
			rec := postWebhook(t, srv, "save", form)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
	require.Empty(t, projectOf(t, c).Spec.Webhooks)
	require.Empty(t, signingSecrets(t, c), "a rejected webhook leaves no secret behind")
}

func TestUpdatingDisablingAndDeletingAWebhook(t *testing.T) {
	srv, c := webhookServer(t)
	var created struct{ ID string }
	require.NoError(t, json.Unmarshal(postWebhook(t, srv, "save", url.Values{"url": {"https://a.example.com/x"}}).Body.Bytes(), &created))
	other := struct{ ID string }{}
	require.NoError(t, json.Unmarshal(postWebhook(t, srv, "save", url.Values{"url": {"https://b.example.com/x"}}).Body.Bytes(), &other))
	require.Len(t, signingSecrets(t, c), 2)

	rec := postWebhook(t, srv, "save", url.Values{"id": {created.ID}, "url": {"https://a2.example.com/y"}, "event": {"deployment.started"}, "disabled": {"on"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "secret", "updating does not reveal or change the secret")
	hooks := projectOf(t, c).Spec.Webhooks
	require.Len(t, hooks, 2)
	require.Equal(t, "https://a2.example.com/y", hooks[0].URL)
	require.True(t, hooks[0].Disabled)
	require.Equal(t, []string{"deployment.started"}, hooks[0].Events)

	require.Equal(t, http.StatusBadRequest, postWebhook(t, srv, "save", url.Values{"id": {"deadbeef"}, "url": {"https://x.example.com/"}}).Code, "an unknown id is not silently created")

	require.Equal(t, http.StatusOK, postWebhook(t, srv, "delete", url.Values{"id": {created.ID}}).Code)
	hooks = projectOf(t, c).Spec.Webhooks
	require.Len(t, hooks, 1)
	require.Equal(t, other.ID, hooks[0].ID)
	require.Len(t, signingSecrets(t, c), 1, "deleting a webhook removes its secret")
	require.Equal(t, http.StatusBadRequest, postWebhook(t, srv, "delete", url.Values{"id": {created.ID}}).Code)

	require.Equal(t, http.StatusOK, postWebhook(t, srv, "delete", url.Values{"id": {other.ID}}).Code)
	require.Empty(t, signingSecrets(t, c), "the secret object goes away with its last webhook")
}

func TestRotatingASecretReplacesItAndRevealsTheNewOne(t *testing.T) {
	srv, c := webhookServer(t)
	var created struct{ ID, Secret string }
	require.NoError(t, json.Unmarshal(postWebhook(t, srv, "save", url.Values{"url": {"https://a.example.com/x"}}).Body.Bytes(), &created))

	rec := postWebhook(t, srv, "rotate", url.Values{"id": {created.ID}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var rotated struct{ Secret string }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rotated))
	require.NotEqual(t, created.Secret, rotated.Secret)
	require.Equal(t, rotated.Secret, signingSecrets(t, c)[created.ID])

	require.Equal(t, http.StatusNotFound, postWebhook(t, srv, "rotate", url.Values{"id": {"deadbeef"}}).Code)
}

func TestAProjectCannotHaveMoreThanTwentyWebhooks(t *testing.T) {
	hooks := make([]geassv1alpha1.GeassWebhook, maxProjectWebhooks)
	for i := range hooks {
		hooks[i] = geassv1alpha1.GeassWebhook{ID: strings.Repeat(string(rune('a'+i%26)), 8), URL: "https://example.com/" + string(rune('a'+i%26))}
	}
	srv, c := webhookServer(t, hooks...)
	rec := postWebhook(t, srv, "save", url.Values{"url": {"https://example.com/one-too-many"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "at most 20")
	require.Len(t, projectOf(t, c).Spec.Webhooks, maxProjectWebhooks)
	require.Empty(t, signingSecrets(t, c), "the secret created for the refused webhook is removed")
}

// receiver collects the requests a webhook endpoint gets.
type receiver struct {
	mu       sync.Mutex
	requests []received
	server   *httptest.Server
}

type received struct {
	header http.Header
	body   []byte
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	r := &receiver{}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := make([]byte, 4096)
		n, _ := req.Body.Read(body)
		r.mu.Lock()
		r.requests = append(r.requests, received{header: req.Header.Clone(), body: body[:n]})
		r.mu.Unlock()
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *receiver) first() received {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests[0]
}

// loopbackDispatcher reaches the test receiver on 127.0.0.1, which the production client refuses.
func loopbackDispatcher(c client.Client) *webhooks.Dispatcher {
	d := webhooks.NewDispatcher(c)
	d.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	d.Backoff = []time.Duration{time.Millisecond}
	return d
}

func TestSendingATestEventSignsItAndRecordsTheDelivery(t *testing.T) {
	rcv := newReceiver(t)
	srv, c := webhookServer(t, geassv1alpha1.GeassWebhook{ID: "abcd1234", URL: rcv.server.URL})
	require.NoError(t, c.Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: webhooks.SecretName(testProjectName), Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"abcd1234": []byte("whsec_known")},
	}))
	srv.Webhooks = loopbackDispatcher(c)

	rec := postWebhook(t, srv, "test", url.Values{"id": {"abcd1234"}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, rcv.count())
	got := rcv.first()
	require.Equal(t, "test", got.header.Get("X-Geass-Event"))
	timestamp := got.header.Get("X-Geass-Timestamp")
	require.NotEmpty(t, timestamp)
	ts, err := time.ParseDuration(timestamp + "s")
	require.NoError(t, err)
	require.Equal(t, webhooks.Sign("whsec_known", int64(ts.Seconds()), got.body), got.header.Get("X-Geass-Signature"))
	deliveries := projectOf(t, c).Status.WebhookDeliveries
	require.Len(t, deliveries, 1, "the test appears in the delivery log")
	require.Equal(t, "test", deliveries[0].Event)
	require.Equal(t, int32(200), deliveries[0].StatusCode)

	require.Equal(t, http.StatusNotFound, postWebhook(t, srv, "test", url.Values{"id": {"missing1"}}).Code)
}

func TestAnnouncingADeployMarksItAndNotifiesSubscribers(t *testing.T) {
	rcv := newReceiver(t)
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace, Annotations: map[string]string{platform.AppDeployRevisionAnnotation: "rev-1"}},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx")},
	}
	srv, c := webhookServer(t, geassv1alpha1.GeassWebhook{ID: "abcd1234", URL: rcv.server.URL})
	require.NoError(t, c.Create(context.Background(), app))
	srv.Webhooks = loopbackDispatcher(c)

	srv.announceDeployment(context.Background(), app)

	require.Eventually(t, func() bool { return rcv.count() == 1 }, 3*time.Second, 10*time.Millisecond)
	require.Equal(t, "deployment.started", rcv.first().header.Get("X-Geass-Event"))
	var got geassv1alpha1.GeassApp
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "web", Namespace: platform.SystemNamespace}, &got))
	require.Equal(t, "rev-1:deployment.started", got.Annotations[platform.AppWebhookStateAnnotation],
		"the deploy is marked so the controller reports its outcome")
}

func TestServicesWithoutSubscribersAreNotMarked(t *testing.T) {
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace, Annotations: map[string]string{platform.AppDeployRevisionAnnotation: "rev-1"}},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx")},
	}
	srv, c := webhookServer(t, geassv1alpha1.GeassWebhook{ID: "abcd1234", URL: "https://example.com/x", Disabled: true})
	require.NoError(t, c.Create(context.Background(), app))
	srv.Webhooks = loopbackDispatcher(c)

	srv.announceDeployment(context.Background(), app)

	var got geassv1alpha1.GeassApp
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "web", Namespace: platform.SystemNamespace}, &got))
	require.NotContains(t, got.Annotations, platform.AppWebhookStateAnnotation, "a disabled webhook is not a subscriber")

	noDispatcher := &Server{Client: c}
	noDispatcher.announceDeployment(context.Background(), app) // must not panic without a dispatcher
}

func TestViewersDoNotReceiveWebhookURLsOrDeliveries(t *testing.T) {
	data := &dashboardBootstrap{}
	data.Projects.Items = []geassv1alpha1.GeassProject{{
		Spec:   geassv1alpha1.GeassProjectSpec{Webhooks: []geassv1alpha1.GeassWebhook{{ID: "abcd1234", URL: "https://hooks.slack.com/services/T0/B0/SECRET"}}},
		Status: geassv1alpha1.GeassProjectStatus{WebhookDeliveries: []geassv1alpha1.GeassWebhookDelivery{{WebhookID: "abcd1234"}}},
	}}
	sanitizeDashboardForViewer(data)
	require.Empty(t, data.Projects.Items[0].Spec.Webhooks)
	require.Empty(t, data.Projects.Items[0].Status.WebhookDeliveries)
}
