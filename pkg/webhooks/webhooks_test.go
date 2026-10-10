package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

const (
	testProject = "proj-1a2b3c4d"
	hookID      = "abcd1234"
	secret      = "whsec_test_secret"
)

func fixture(t *testing.T, hooks ...geassv1alpha1.GeassWebhook) (*Dispatcher, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProject, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{DisplayName: "marbled-willow", Environments: []string{"production"}, Webhooks: hooks},
	}
	signing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: SecretName(testProject), Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{hookID: []byte(secret)},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(project, signing).
		WithStatusSubresource(&geassv1alpha1.GeassProject{}).Build()
	d := NewDispatcher(c)
	d.HTTPClient = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	d.Backoff = []time.Duration{time.Millisecond, time.Millisecond}
	return d, c
}

func loadProject(t *testing.T, c client.Client) geassv1alpha1.GeassProject {
	t.Helper()
	var project geassv1alpha1.GeassProject
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: testProject, Namespace: platform.SystemNamespace}, &project))
	return project
}

func sampleEvent() Event {
	return Event{
		Type: EventDeploymentSucceeded, Environment: "production",
		Service:    &Service{Name: "little-frost", DisplayName: "api", Kind: "function", URL: "https://api.example.com"},
		Deployment: &Deployment{Revision: "r1", Image: "function:bun"},
	}
}

func TestDeliveryIsSignedAndCarriesTheEvent(t *testing.T) {
	var gotBody []byte
	var gotHeader http.Header
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotHeader = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	hook := geassv1alpha1.GeassWebhook{ID: hookID, URL: receiver.URL}
	d, c := fixture(t, hook)
	project := loadProject(t, c)

	delivery := d.DeliverAndRecord(context.Background(), &project, hook, sampleEvent(), 3)

	require.Empty(t, delivery.Error)
	require.Equal(t, int32(204), delivery.StatusCode)
	require.Equal(t, int32(1), delivery.Attempts)
	require.Equal(t, EventDeploymentSucceeded, gotHeader.Get("X-Geass-Event"))
	require.Equal(t, delivery.DeliveryID, gotHeader.Get("X-Geass-Delivery"))
	require.Equal(t, "application/json", gotHeader.Get("Content-Type"))

	// A receiver verifies the signature the same way: HMAC-SHA256 over "<timestamp>.<body>".
	signature := gotHeader.Get("X-Geass-Signature")
	timestamp := gotHeader.Get("X-Geass-Timestamp")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(gotBody)
	require.Equal(t, fmt.Sprintf("t=%s,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil))), signature)

	var body map[string]any
	require.NoError(t, json.Unmarshal(gotBody, &body))
	require.Equal(t, "deployment.succeeded", body["type"])
	require.Equal(t, "production", body["environment"])
	require.Equal(t, "marbled-willow", body["project"].(map[string]any)["displayName"])
	require.Equal(t, "little-frost", body["service"].(map[string]any)["name"])
	require.NotEmpty(t, body["id"])

	recorded := loadProject(t, c).Status.WebhookDeliveries
	require.Len(t, recorded, 1, "the outcome is stored on the project")
	require.Equal(t, delivery.DeliveryID, recorded[0].DeliveryID)
}

func TestTransientFailuresAreRetriedButClientErrorsAreNot(t *testing.T) {
	var calls atomic.Int32
	status := atomic.Int32{}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(int(status.Load()))
	}))
	defer receiver.Close()
	hook := geassv1alpha1.GeassWebhook{ID: hookID, URL: receiver.URL}
	d, c := fixture(t, hook)
	project := loadProject(t, c)

	status.Store(500)
	delivery := d.Deliver(context.Background(), &project, hook, sampleEvent(), 3)
	require.Equal(t, int32(3), calls.Load(), "a server error is retried up to the attempt limit")
	require.Equal(t, int32(3), delivery.Attempts)
	require.Contains(t, delivery.Error, "HTTP 500")

	calls.Store(0)
	status.Store(400)
	delivery = d.Deliver(context.Background(), &project, hook, sampleEvent(), 3)
	require.Equal(t, int32(1), calls.Load(), "a 4xx answer means the receiver rejected it; retrying will not help")
	require.Equal(t, int32(400), delivery.StatusCode)

	calls.Store(0)
	status.Store(429)
	d.Deliver(context.Background(), &project, hook, sampleEvent(), 2)
	require.Equal(t, int32(2), calls.Load(), "rate limiting is retried")

	calls.Store(0)
	status.Store(500)
	d.Deliver(context.Background(), &project, hook, sampleEvent(), 1)
	require.Equal(t, int32(1), calls.Load(), "the test button makes a single attempt")
}

func TestARetrySucceedsWhenTheReceiverRecovers(t *testing.T) {
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	hook := geassv1alpha1.GeassWebhook{ID: hookID, URL: receiver.URL}
	d, c := fixture(t, hook)
	project := loadProject(t, c)

	delivery := d.Deliver(context.Background(), &project, hook, sampleEvent(), 3)

	require.Empty(t, delivery.Error)
	require.Equal(t, int32(200), delivery.StatusCode)
	require.Equal(t, int32(3), delivery.Attempts)
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var internalHits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { internalHits.Add(1) }))
	defer internal.Close()
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer receiver.Close()
	hook := geassv1alpha1.GeassWebhook{ID: hookID, URL: receiver.URL}
	d, c := fixture(t, hook)
	project := loadProject(t, c)

	delivery := d.Deliver(context.Background(), &project, hook, sampleEvent(), 3)

	require.Equal(t, int32(302), delivery.StatusCode)
	require.Zero(t, internalHits.Load(), "a redirect must not turn a webhook into a request to another host")
	require.Equal(t, int32(1), delivery.Attempts)
}

func TestTheDefaultClientRefusesLoopbackAndMetadataAddresses(t *testing.T) {
	var hits atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer receiver.Close() // listens on 127.0.0.1
	hook := geassv1alpha1.GeassWebhook{ID: hookID, URL: receiver.URL}
	d, c := fixture(t, hook)
	d.HTTPClient = NewHTTPClient()
	project := loadProject(t, c)

	delivery := d.Deliver(context.Background(), &project, hook, sampleEvent(), 1)

	require.Zero(t, hits.Load(), "the loopback receiver must never be reached")
	require.Contains(t, delivery.Error, "refusing to connect")

	for _, address := range []string{"127.0.0.1:80", "[::1]:80", "169.254.169.254:80", "0.0.0.0:80", "[fe80::1]:80", "224.0.0.1:80"} {
		require.Error(t, blockUnsafeAddress("tcp", address, nil), address)
	}
	for _, address := range []string{"10.43.0.10:80", "192.168.1.5:443", "172.16.0.9:80", "100.64.1.1:80", "[fd00::1]:80"} {
		require.Error(t, blockUnsafeAddress("tcp", address, nil), "%s is a private address and refused unless the operator opts in", address)
	}
	require.NoError(t, blockUnsafeAddress("tcp", "93.184.216.34:443", nil), "public addresses are allowed")
}

func TestOperatorsCanAllowPrivateNetworksButNeverLoopbackOrMetadataAddresses(t *testing.T) {
	t.Setenv(AllowPrivateNetworksEnv, "true")

	for _, address := range []string{"10.43.0.10:80", "192.168.1.5:443", "172.16.0.9:80", "100.64.1.1:80", "[fd00::1]:80", "93.184.216.34:443"} {
		require.NoError(t, blockUnsafeAddress("tcp", address, nil), address)
	}
	for _, address := range []string{"127.0.0.1:80", "[::1]:80", "169.254.169.254:80", "0.0.0.0:80", "[fe80::1]:80", "224.0.0.1:80"} {
		require.Error(t, blockUnsafeAddress("tcp", address, nil), "%s stays blocked even when private networks are allowed", address)
	}
	require.NoError(t, ValidateURL("http://10.43.0.5:5678/webhook/abc"))
	require.Error(t, ValidateURL("http://169.254.169.254/latest/meta-data"))
}

func TestErrorsDoNotLeakTheWebhookURL(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close()) // nothing is listening, so the connection is refused
	hook := geassv1alpha1.GeassWebhook{ID: hookID, URL: "http://" + address + "/hooks/SECRET-TOKEN-123?key=abc"}
	d, c := fixture(t, hook)
	project := loadProject(t, c)

	delivery := d.Deliver(context.Background(), &project, hook, sampleEvent(), 1)

	require.NotEmpty(t, delivery.Error)
	require.NotContains(t, delivery.Error, "SECRET-TOKEN-123")
	require.NotContains(t, delivery.Error, "key=abc")
}

func TestEmitSkipsDisabledAndUnsubscribedWebhooks(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
	}))
	defer receiver.Close()
	d, c := fixture(t,
		geassv1alpha1.GeassWebhook{ID: "aaaaaaa1", URL: receiver.URL + "/all"},
		geassv1alpha1.GeassWebhook{ID: "aaaaaaa2", URL: receiver.URL + "/succeeded", Events: []string{EventDeploymentSucceeded}},
		geassv1alpha1.GeassWebhook{ID: "aaaaaaa3", URL: receiver.URL + "/failed", Events: []string{EventDeploymentFailed}},
		geassv1alpha1.GeassWebhook{ID: "aaaaaaa4", URL: receiver.URL + "/off", Disabled: true},
	)

	d.Emit(testProject, sampleEvent())

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return hits["/all"] == 1 && hits["/succeeded"] == 1
	}, 3*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	require.Zero(t, hits["/failed"], "a webhook only receives the events it subscribed to")
	require.Zero(t, hits["/off"], "a disabled webhook receives nothing")
	mu.Unlock()
	// The fake client does not enforce resourceVersion on status writes, so two simultaneous deliveries
	// can overwrite each other here; against a real API server record() retries on conflict.
	require.Eventually(t, func() bool { return len(loadProject(t, c).Status.WebhookDeliveries) >= 1 }, 3*time.Second, 10*time.Millisecond)
}

func TestOnlyTheNewestDeliveriesAreKept(t *testing.T) {
	d, c := fixture(t)
	for i := range MaxDeliveries + 5 {
		require.NoError(t, d.record(context.Background(), testProject, geassv1alpha1.GeassWebhookDelivery{
			WebhookID: hookID, Event: EventTest, DeliveryID: fmt.Sprintf("dlv_%02d", i), Time: metav1.Now(),
		}))
	}
	recorded := loadProject(t, c).Status.WebhookDeliveries
	require.Len(t, recorded, MaxDeliveries)
	require.Equal(t, fmt.Sprintf("dlv_%02d", MaxDeliveries+4), recorded[0].DeliveryID, "newest first")
}

func TestUnsignedWhenNoSecretIsStored(t *testing.T) {
	var header http.Header
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { header = r.Header.Clone() }))
	defer receiver.Close()
	hook := geassv1alpha1.GeassWebhook{ID: "unknown1", URL: receiver.URL}
	d, c := fixture(t, hook)
	project := loadProject(t, c)

	d.Deliver(context.Background(), &project, hook, sampleEvent(), 1)

	require.Empty(t, header.Get("X-Geass-Signature"), "no signing secret exists for this webhook")
}

func TestValidateURL(t *testing.T) {
	for _, ok := range []string{"https://example.com/hooks", "https://hooks.slack.com/services/T0/B0/xyz"} {
		require.NoError(t, ValidateURL(ok), ok)
	}
	for name, bad := range map[string]string{
		"empty":       "",
		"no scheme":   "example.com/hook",
		"ftp":         "ftp://example.com/hook",
		"userinfo":    "https://user:pass@example.com/",
		"fragment":    "https://example.com/#x",
		"localhost":   "http://localhost:8080/hook",
		"subdomain":   "http://app.localhost/hook",
		"loopback ip": "http://127.0.0.1/hook",
		"metadata":    "http://169.254.169.254/latest/meta-data",
		"ipv6 loop":   "http://[::1]/hook",
		"private ip":  "http://10.43.0.5:5678/webhook/abc",
		"private v6":  "http://[fd00::1]/hook",
		"shared addr": "http://100.64.0.1/hook",
		"too long":    "https://example.com/" + strings.Repeat("a", 2100),
	} {
		require.Error(t, ValidateURL(bad), name)
	}
}

func TestSignMatchesAnIndependentComputation(t *testing.T) {
	body := []byte(`{"a":1}`)
	mac := hmac.New(sha256.New, []byte("s3"))
	_, _ = mac.Write([]byte("1700000000." + string(body)))
	require.Equal(t, "t=1700000000,v1="+hex.EncodeToString(mac.Sum(nil)), Sign("s3", 1700000000, body))
}

func TestSecretsAndIdsAreRandom(t *testing.T) {
	a, err := NewSecret()
	require.NoError(t, err)
	b, _ := NewSecret()
	require.NotEqual(t, a, b)
	require.True(t, strings.HasPrefix(a, "whsec_"))
	id, err := NewID()
	require.NoError(t, err)
	require.Regexp(t, "^[0-9a-f]{8}$", id)
	require.True(t, ValidEvent(EventDeploymentFailed))
	require.False(t, ValidEvent(EventTest), "test is not subscribable")
}

func TestServiceEventDescribesFunctionsServicesAndGitApps(t *testing.T) {
	fn := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "hello", Annotations: map[string]string{platform.DisplayNameAnnotation: "Hello", platform.AppDeployRevisionAnnotation: "rev-9"}},
		Spec:       geassv1alpha1.GeassAppSpec{Environment: "staging", Source: geassv1alpha1.GeassAppSource{Function: &geassv1alpha1.GeassAppFunctionSource{Runtime: "node", Code: "x"}}},
		Status:     geassv1alpha1.GeassAppStatus{URL: "https://hello.example.com"},
	}
	event := ServiceEvent(EventDeploymentStarted, fn, "")
	require.Equal(t, "function", event.Service.Kind)
	require.Equal(t, "Hello", event.Service.DisplayName)
	require.Equal(t, "function:node", event.Deployment.Image)
	require.Equal(t, "rev-9", event.Deployment.Revision)
	require.Equal(t, "staging", event.Environment)

	git := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web"},
		Spec:       geassv1alpha1.GeassAppSpec{Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{Repository: "acme/web", Branch: "main", Commit: "abc123"}}},
		Status:     geassv1alpha1.GeassAppStatus{ResolvedImage: "registry/web@sha256:1"},
	}
	event = ServiceEvent(EventDeploymentSucceeded, git, "")
	require.Equal(t, "service", event.Service.Kind)
	require.Equal(t, "registry/web@sha256:1", event.Deployment.Image, "the built image wins over the repository name")
	require.Equal(t, "abc123", event.Deployment.Commit)
}
