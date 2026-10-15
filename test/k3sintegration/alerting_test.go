//go:build k3sintegration

package k3sintegration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/controller"
	"github.com/degoke/geass/pkg/alerts"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

// sink records what is sent to it: it stands in for Slack and for a mail server, on the host, where the pods of the
// cluster in Docker can reach it.
type sink struct {
	mu       sync.Mutex
	messages []string
}

func (s *sink) add(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
}

func (s *sink) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.messages...)
}

func (s *sink) containing(parts ...string) []string {
	var found []string
	for _, message := range s.all() {
		match := true
		for _, part := range parts {
			match = match && strings.Contains(message, part)
		}
		if match {
			found = append(found, message)
		}
	}
	return found
}

// dockerHost is the address of the machine the k3s in Docker is on, as the pods see it.
func dockerHost(t *testing.T) string {
	t.Helper()
	if host := os.Getenv("K3S_HOST_IP"); host != "" {
		return host
	}
	out, err := exec.Command("docker", "network", "inspect", "bridge", "-f", "{{(index .IPAM.Config 0).Gateway}}").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "172.17.0.1"
	}
	return strings.TrimSpace(string(out))
}

func listenOnAll(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func portOf(listener net.Listener) int { return listener.Addr().(*net.TCPAddr).Port }

// serveSlack records every post as "<path> <body>".
func serveSlack(t *testing.T, received *sink) net.Listener {
	t.Helper()
	listener := listenOnAll(t)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received.add(r.URL.Path + " " + string(body))
		_, _ = w.Write([]byte("ok"))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return listener
}

// serveMail is a mail server that takes whatever it is given, and records each message.
func serveMail(t *testing.T, received *sink) net.Listener {
	t.Helper()
	listener := listenOnAll(t)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				reader := bufio.NewReader(conn)
				say := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
				say("220 sink ESMTP")
				var data strings.Builder
				inData := false
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimRight(line, "\r\n")
					if inData {
						if line == "." {
							inData = false
							received.add(data.String())
							data.Reset()
							say("250 queued")
							continue
						}
						data.WriteString(line + "\n")
						continue
					}
					switch upper := strings.ToUpper(line); {
					case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
						say("250 sink")
					case upper == "DATA":
						inData = true
						say("354 go")
					case upper == "QUIT":
						say("221 bye")
						return
					default:
						say("250 ok")
					}
				}
			}()
		}
	}()
	return listener
}

// fixtureLabel marks the apps and pods this test makes.
const fixtureLabel = "geass.dev/it-alerting"

// runSuffix makes the names of a run's apps and pods its own. Alertmanager remembers what it has told a channel for
// twelve hours, by the alert's labels, and keeps an alert group for minutes after its alerts end: a second run on the
// same cluster that used the names of the first would be taken for the first, and not be announced.
func runSuffix() string { return fmt.Sprintf("%d", time.Now().Unix()%100000) }

// resetAlertingFixtures removes what a run of this test makes in the cluster, so that a second run starts clean and the
// sinks of the first, whose ports are in the Secrets, are not what the second sends to.
func resetAlertingFixtures(ctx context.Context, c client.Client) {
	pod := func(namespace string) client.Object {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "crasher", Namespace: namespace}}
	}
	// What a run made is labelled, and named with the run's own suffix: see runSuffix.
	mine := client.MatchingLabels{fixtureLabel: "true"}
	_ = c.DeleteAllOf(ctx, &geassv1alpha1.GeassApp{}, client.InNamespace(platform.SystemNamespace), mine)
	for _, namespace := range []string{"p1-production", "p1-pr-1", "default"} {
		_ = c.DeleteAllOf(ctx, &corev1.Pod{}, client.InNamespace(namespace), mine)
	}
	for _, object := range []client.Object{
		&geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: alerts.ChannelsSecretName, Namespace: platform.SystemNamespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: alerts.SMTPSecretName, Namespace: platform.SystemNamespace}},
		&geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace}},
		&geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace}},
		&geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: platform.SystemNamespace}},
		&monitoringv1.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: "geass-it-operator", Namespace: platform.MonitoringTargetNS}},
		&corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: "geass-it-operator", Namespace: platform.MonitoringTargetNS}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "geass-it-operator", Namespace: platform.MonitoringTargetNS}},
		pod("p1-production"), pod("default"),
	} {
		_ = client.IgnoreNotFound(c.Delete(ctx, object))
	}
}

// prometheusGet asks Prometheus a question through the API server's proxy, which is how a test outside the cluster
// reaches a Service in it.
func prometheusGet(t *testing.T, kube kubernetes.Interface, path string, params map[string]string) []byte {
	t.Helper()
	out, err := kube.CoreV1().Services(platform.MonitoringTargetNS).ProxyGet("http", platform.MonitoringPrometheusService, "9090", path, params).DoRaw(context.Background())
	if err != nil {
		t.Logf("Prometheus %s: %v", path, err)
		return nil
	}
	return out
}

// prometheusAlerts lists the alerts Prometheus holds, by name and state, with their labels.
func prometheusAlerts(t *testing.T, kube kubernetes.Interface) []struct {
	Labels map[string]string `json:"labels"`
	State  string            `json:"state"`
} {
	t.Helper()
	var body struct {
		Data struct {
			Alerts []struct {
				Labels map[string]string `json:"labels"`
				State  string            `json:"state"`
			} `json:"alerts"`
		} `json:"data"`
	}
	if raw := prometheusGet(t, kube, "/api/v1/alerts", nil); raw != nil {
		_ = json.Unmarshal(raw, &body)
	}
	return body.Data.Alerts
}

// TestAlertingOnK3s follows an alert through the whole of what Geass puts in the cluster for it, against a real k3s:
// the monitoring add-on with Alertmanager, the alert rules Prometheus has to accept, the AlertmanagerConfig that the
// channels become, and a problem that the operator reports on its way to Slack and to a mailbox, filtered the way each
// channel asked. The operator runs in this process, so Prometheus scrapes it here; Slack and the mail server are
// sinks on the host.
func TestAlertingOnK3s(t *testing.T) {
	if os.Getenv("K3S_INTEGRATION") != "true" {
		t.Skip("set K3S_INTEGRATION=true to run the k3s alerting integration test (hack/k3s-up.sh starts a k3s)")
	}
	cfg := k3sRestConfig(t)
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, geassv1alpha1.AddToScheme, helmv1.AddToScheme, monitoringv1.AddToScheme, monitoringv1alpha1.AddToScheme, cnpgv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(false)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	collector := controller.NewProblemCollector()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, Cache: controller.CacheOptions(), Metrics: metricsserver.Options{BindAddress: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := (&controller.GeassClusterReconciler{Client: mgr.GetClient(), Scheme: scheme}).SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	if err := (&controller.AlertRoutingReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Add(&controller.ProblemExporter{Client: mgr.GetClient(), Collector: collector, Interval: 5 * time.Second}); err != nil {
		t.Fatal(err)
	}
	go func() {
		if err := mgr.Start(ctx); err != nil {
			t.Errorf("manager: %v", err)
		}
	}()

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	kube := kubernetes.NewForConfigOrDie(cfg)
	host := dockerHost(t)
	resetAlertingFixtures(ctx, c)
	t.Cleanup(func() { resetAlertingFixtures(context.Background(), c) })

	// ---- the places alerts go: Slack, and a mail server ----
	slack, mail := &sink{}, &sink{}
	slackAddr := serveSlack(t, slack)
	mailAddr := serveMail(t, mail)
	slackURL := func(path string) string { return fmt.Sprintf("http://%s:%d%s", host, portOf(slackAddr), path) }
	// A Slack that is not there: its port was open and is now closed.
	closed := listenOnAll(t)
	deadURL := fmt.Sprintf("http://%s:%d/nowhere", host, portOf(closed))
	_ = closed.Close()

	// ---- what Prometheus scrapes from this process ----
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	metricsAddr := listenOnAll(t)
	metricsServer := &http.Server{Handler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{})}
	go func() { _ = metricsServer.Serve(metricsAddr) }()
	t.Cleanup(func() { _ = metricsServer.Close() })

	// ---- the cluster, with monitoring (and so Alertmanager) the only add-on ----
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platform.SystemNamespace}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	if err := c.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "geass-token", Namespace: platform.SystemNamespace}, StringData: map[string]string{"token": "x"}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	enabled, disabled := true, false
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{
			Version: "v1", ServerURL: "https://127.0.0.1:6443",
			TokenSecretRef: corev1.SecretReference{Name: "geass-token", Namespace: platform.SystemNamespace},
			Addons: geassv1alpha1.GeassClusterAddonsSpec{
				CertManager: geassv1alpha1.GeassClusterCertManagerAddon{Enabled: &disabled},
				Monitoring:  geassv1alpha1.GeassClusterMonitoringAddon{Enabled: &enabled, Profile: "lite"},
				Longhorn:    geassv1alpha1.GeassClusterLonghornAddon{Enabled: &disabled},
				Logging:     geassv1alpha1.GeassClusterLoggingAddon{Enabled: &disabled},
				Serverless:  geassv1alpha1.GeassClusterServerlessAddon{Enabled: &disabled},
			},
		},
	}
	if err := c.Create(ctx, cluster); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	// ---- Prometheus has to accept every rule Geass writes ----
	eventually(t, 20*time.Minute, "Prometheus to load and evaluate every alert rule", func() (bool, string) {
		var body struct {
			Data struct {
				Groups []struct {
					Name  string `json:"name"`
					Rules []struct {
						Name      string `json:"name"`
						Health    string `json:"health"`
						LastError string `json:"lastError"`
					} `json:"rules"`
				} `json:"groups"`
			} `json:"data"`
		}
		raw := prometheusGet(t, kube, "/api/v1/rules", nil)
		if raw == nil || json.Unmarshal(raw, &body) != nil {
			return false, "Prometheus is not answering yet"
		}
		loaded := map[string]int{}
		for _, group := range body.Data.Groups {
			for _, rule := range group.Rules {
				if rule.Health == "err" || rule.LastError != "" {
					t.Fatalf("Prometheus rejected the rule %s: %s", rule.Name, rule.LastError)
				}
				if rule.Health != "ok" {
					return false, fmt.Sprintf("the rule %s has not been evaluated yet", rule.Name)
				}
				loaded[group.Name]++
			}
		}
		for group, want := range map[string]int{"geass-problems": 6, "geass-infrastructure": 6, "geass-operator": 7} {
			if loaded[group] != want {
				return false, fmt.Sprintf("group %s has %d of its %d rules loaded", group, loaded[group], want)
			}
		}
		return true, ""
	})

	// ---- the project and what runs in it, and the scrape of this process ----
	for _, object := range []client.Object{
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "geass-it-operator", Namespace: platform.MonitoringTargetNS, Labels: map[string]string{"app": "geass-it-operator"}},
			Spec:       corev1.ServiceSpec{ClusterIP: corev1.ClusterIPNone, Ports: []corev1.ServicePort{{Name: "metrics", Port: int32(portOf(metricsAddr)), TargetPort: intstr.FromInt32(int32(portOf(metricsAddr)))}}},
		},
		&corev1.Endpoints{
			ObjectMeta: metav1.ObjectMeta{Name: "geass-it-operator", Namespace: platform.MonitoringTargetNS, Labels: map[string]string{"app": "geass-it-operator"}},
			Subsets: []corev1.EndpointSubset{{
				Addresses: []corev1.EndpointAddress{{IP: host}},
				Ports:     []corev1.EndpointPort{{Name: "metrics", Port: int32(portOf(metricsAddr))}},
			}},
		},
		&monitoringv1.ServiceMonitor{
			ObjectMeta: metav1.ObjectMeta{Name: "geass-it-operator", Namespace: platform.MonitoringTargetNS},
			Spec: monitoringv1.ServiceMonitorSpec{
				Selector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "geass-it-operator"}},
				// HonorLabels as the operator's own monitor has it: the namespace label of the project info has to survive.
				Endpoints: []monitoringv1.Endpoint{{Port: "metrics", Interval: "10s", HonorLabels: true}},
			},
		},
	} {
		if err := c.Create(ctx, object); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
	}

	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{DisplayName: "Morning Beach", ClusterRef: corev1.LocalObjectReference{Name: "default"}, Environments: []string{"production", "staging"}},
	}
	if err := c.Create(ctx, project); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	// pr-1 is an environment made for a pull request, which fails as a matter of course: nothing in it is an alert.
	project.Status.Environments = []geassv1alpha1.GeassProjectEnvironmentStatus{
		{Name: "production", Namespace: "p1-production"}, {Name: "staging", Namespace: "p1-staging"}, {Name: "pr-1", Namespace: "p1-pr-1"},
	}
	project.Status.PullRequests = []geassv1alpha1.GeassPullRequestEnvironment{{Number: 1, Repository: "acme/web", Branch: "fix", Environment: "pr-1", OpenedAt: metav1.Now()}}
	if err := c.Status().Update(ctx, project); err != nil {
		t.Fatal(err)
	}

	// ---- the channels an admin would set up ----
	smtpSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: alerts.SMTPSecretName, Namespace: platform.SystemNamespace}, StringData: map[string]string{alerts.SMTPPasswordKey: "unused"}}
	channelsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: alerts.ChannelsSecretName, Namespace: platform.SystemNamespace},
		StringData: map[string]string{"aaaaaaa1": slackURL("/everything"), "ccccccc3": slackURL("/other-project"), "ddddddd4": slackURL("/paused"), "eeeeeee5": deadURL},
	}
	for _, secret := range []*corev1.Secret{smtpSecret, channelsSecret} {
		if err := c.Create(ctx, secret); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
	}
	platformConfig := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			DashboardURL: "https://geass.example.com",
			Alerting: &geassv1alpha1.GeassAlertingSpec{
				SMTP: &geassv1alpha1.GeassAlertSMTP{
					Host: host, Port: int32(portOf(mailAddr)), Security: "none", From: "Geass <alerts@example.com>",
				},
				Channels: []geassv1alpha1.GeassAlertChannel{
					{ID: "aaaaaaa1", Name: "everything", Type: "slack"},
					{ID: "bbbbbbb2", Name: "production deploys", Type: "email", Recipients: []string{"ops@example.com"}, Events: []string{"deploy.failed"}, Environments: []string{"production"}},
					{ID: "ccccccc3", Name: "another project", Type: "slack", Projects: []string{"other-project"}},
					{ID: "ddddddd4", Name: "paused", Type: "slack", Disabled: true},
					{ID: "eeeeeee5", Name: "dead", Type: "slack", Projects: []string{"nobody"}},
				},
			},
		},
	}
	if err := c.Create(ctx, platformConfig); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	am := &alerts.Alertmanager{
		BaseURL: strings.TrimRight(cfg.Host, "/") + "/api/v1/namespaces/" + platform.MonitoringTargetNS + "/services/http:" + platform.MonitoringAlertmanagerService + ":9093/proxy",
	}
	if httpClient, err := rest.HTTPClientFor(cfg); err == nil {
		am.HTTP = httpClient
	}

	// ---- the operator turns the channels into Alertmanager's configuration ----
	eventually(t, 10*time.Minute, "Alertmanager to load the channels' receivers", func() (bool, string) {
		if err := am.Ready(ctx); err != nil {
			return false, err.Error()
		}
		for _, id := range []string{"aaaaaaa1", "bbbbbbb2", "ccccccc3"} {
			if ok, err := am.HasReceiver(ctx, alerts.ReceiverName(platform.SystemNamespace, id, false)); err != nil || !ok {
				return false, "the receiver of channel " + id + " is not loaded"
			}
		}
		if ok, _ := am.HasReceiver(ctx, alerts.ReceiverName(platform.SystemNamespace, "eeeeeee5", true)); !ok {
			return false, "the test receiver of the dead channel is not loaded"
		}
		// The paused channel has a receiver for tests only.
		if ok, _ := am.HasReceiver(ctx, alerts.ReceiverName(platform.SystemNamespace, "ddddddd4", true)); !ok {
			return false, "the test receiver of the paused channel is not loaded"
		}
		if ok, _ := am.HasReceiver(ctx, alerts.ReceiverName(platform.SystemNamespace, "ddddddd4", false)); ok {
			t.Fatal("a paused channel has a receiver for real alerts")
		}
		return true, ""
	})

	// Alertmanager's counts are for as long as it has run, so what a test changes is what it is judged by.
	baseline, err := am.Counters(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// ---- a test reaches the channel it is for, paused or not, and no other ----
	for id, want := range map[string]string{"aaaaaaa1": "/everything", "ccccccc3": "/other-project", "ddddddd4": "/paused"} {
		before := len(slack.containing(want, "Test alert from Geass"))
		if err := am.PostTest(ctx, id, "channel "+id, time.Now()); err != nil {
			t.Fatal(err)
		}
		eventually(t, 2*time.Minute, "the test for channel "+id+" to arrive", func() (bool, string) {
			return len(slack.containing(want, "Test alert from Geass")) > before, "nothing yet"
		})
	}
	if err := am.PostTest(ctx, "bbbbbbb2", "production deploys", time.Now()); err != nil {
		t.Fatal(err)
	}
	eventually(t, 2*time.Minute, "the test email to arrive", func() (bool, string) {
		return len(mail.containing("Subject: [Geass] Test alert from Geass")) > 0, "nothing yet"
	})
	for path, others := range map[string][]string{"/everything": {"/other-project", "/paused"}, "/other-project": {"/everything", "/paused"}, "/paused": {"/everything", "/other-project"}} {
		if got := len(slack.containing(path, "Test alert from Geass")); got != 1 {
			t.Fatalf("channel %s got %d tests, want 1 (and none meant for %v)", path, got, others)
		}
	}
	// The count the dashboard watches to know a test went out: it went up for each of them.
	counters, err := am.Counters(ctx)
	if err != nil || counters["slack"].Sent-baseline["slack"].Sent < 3 {
		t.Fatalf("Alertmanager's count of Slack requests should have gone up by three: before %+v, after %+v, %v", baseline["slack"], counters["slack"], err)
	}

	// ---- a channel that cannot be reached is a test that fails, and Alertmanager says so ----
	before, err := am.Counters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := am.PostTest(ctx, "eeeeeee5", "dead", time.Now()); err != nil {
		t.Fatal(err)
	}
	eventually(t, 2*time.Minute, "Alertmanager to fail to send to a Slack that is not there", func() (bool, string) {
		logs, err := kube.CoreV1().Pods(platform.MonitoringTargetNS).GetLogs("alertmanager-"+platform.MonitoringAlertmanagerService+"-0", &corev1.PodLogOptions{Container: "alertmanager", SinceSeconds: ptr(int64(300))}).DoRaw(ctx)
		if err != nil {
			return false, err.Error()
		}
		failedNow, _ := am.Counters(ctx)
		failed := false
		for _, line := range strings.Split(string(logs), "\n") {
			failed = failed || (strings.Contains(line, "test-eeeeeee5") && strings.Contains(line, "Notify attempt failed"))
		}
		return failed && failedNow["slack"].Failed > before["slack"].Failed, "no failure yet"
	})

	// ---- a second test of a channel is not held back by the first: Alertmanager keeps a group for minutes ----
	if err := am.PostTest(ctx, "aaaaaaa1", "channel aaaaaaa1", time.Now()); err != nil {
		t.Fatal(err)
	}
	eventually(t, 2*time.Minute, "a second test of the same channel to arrive at once", func() (bool, string) {
		return len(slack.containing("/everything", "Test alert from Geass")) == 2, fmt.Sprintf("%d so far", len(slack.containing("/everything", "Test alert from Geass")))
	})

	// ---- a Slack address that is changed is the one that is used from then on ----
	var live corev1.Secret
	if err := c.Get(ctx, client.ObjectKeyFromObject(channelsSecret), &live); err != nil {
		t.Fatal(err)
	}
	// From here on the channel that heard everything is at this address, not at /everything.
	const rotated = "/rotated"
	live.Data["aaaaaaa1"] = []byte(slackURL(rotated))
	if err := c.Update(ctx, &live); err != nil {
		t.Fatal(err)
	}
	rotatedAt := time.Now()
	eventually(t, 6*time.Minute, "a test to reach the Slack address the channel was changed to", func() (bool, string) {
		if err := am.PostTest(ctx, "aaaaaaa1", "channel aaaaaaa1", time.Now()); err != nil {
			return false, err.Error()
		}
		time.Sleep(10 * time.Second)
		return len(slack.containing(rotated, "Test alert from Geass")) > 0, "still the old address"
	})
	t.Logf("a changed Slack address took %s to take effect", time.Since(rotatedAt).Round(time.Second))

	// ---- problems: two services fail to deploy, one in production and one in staging ----
	suffix := runSuffix()
	webName, apiName, crasher := "web-"+suffix, "api-"+suffix, "crasher-"+suffix
	failed := func(name, environment string) *geassv1alpha1.GeassApp {
		return &geassv1alpha1.GeassApp{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: platform.SystemNamespace, Labels: map[string]string{fixtureLabel: "true"},
				Annotations: map[string]string{"geass.dev/display-name": name},
			},
			Spec: geassv1alpha1.GeassAppSpec{
				Project: "p1", Environment: geassv1alpha1.GeassEnvironment(environment),
				Source: geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "nginx:alpine"}},
				Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
			},
		}
	}
	apps := []*geassv1alpha1.GeassApp{failed(webName, "production"), failed(apiName, "staging")}
	setReady := func(ready bool) {
		for _, app := range apps {
			if err := c.Get(ctx, client.ObjectKeyFromObject(app), app); err != nil {
				t.Fatal(err)
			}
			status, reason := metav1.ConditionTrue, "AppReady"
			if !ready {
				status, reason = metav1.ConditionFalse, platform.ConditionReasonProvisionFailed
			}
			app.Status.Conditions = []metav1.Condition{{Type: platform.ConditionReady, Status: status, Reason: reason, LastTransitionTime: metav1.Now()}}
			if err := c.Status().Update(ctx, app); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, app := range apps {
		if err := c.Create(ctx, app); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
	}
	setReady(false)

	eventually(t, 6*time.Minute, "the operator's problems to reach Prometheus as alerts", func() (bool, string) {
		firing := map[string]bool{}
		for _, alert := range prometheusAlerts(t, kube) {
			if alert.Labels["alertname"] == "GeassDeployFailed" && alert.State == "firing" {
				firing[alert.Labels["resource"]+"/"+alert.Labels["environment"]+"/"+alert.Labels["project_name"]] = true
			}
		}
		return firing[webName+"/production/Morning Beach"] && firing[apiName+"/staging/Morning Beach"], fmt.Sprintf("firing: %v", firing)
	})
	eventually(t, 8*time.Minute, "the failed deploys to reach Slack", func() (bool, string) {
		return len(slack.containing(rotated, "Deploy failed", webName+" (Morning Beach / production)")) > 0 &&
			len(slack.containing(rotated, "Deploy failed", apiName+" (Morning Beach / staging)")) > 0, "not both yet"
	})
	eventually(t, 8*time.Minute, "the production deploy failure to reach the mailbox", func() (bool, string) {
		return len(mail.containing("Subject: [Geass] Deploy failed", webName+" (Morning Beach / production)")) > 0, "nothing yet"
	})

	// Each channel heard what it asked for and nothing else.
	if got := mail.containing(apiName + " (Morning Beach / staging)"); len(got) > 0 {
		t.Fatalf("the production-only mailbox was told about staging: %v", got)
	}
	for _, path := range []string{"/other-project", "/paused"} {
		if got := slack.containing(path, "Deploy failed"); len(got) > 0 {
			t.Fatalf("channel %s heard a problem it should not: %v", path, got)
		}
	}
	if got := slack.containing(rotated, "Deploy failed", "<!channel>"); len(got) > 0 {
		t.Fatalf("unescaped markup reached Slack: %v", got)
	}

	// ---- a pod that crashes: in a project's namespace it belongs to the project, and anywhere else it is the cluster's ----
	crash := func(namespace string) {
		if namespace != "default" {
			if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil && !apierrors.IsAlreadyExists(err) {
				t.Fatal(err)
			}
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: crasher, Namespace: namespace, Labels: map[string]string{fixtureLabel: "true"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "c", Image: "rancher/mirrored-library-busybox:1.36.1", ImagePullPolicy: corev1.PullIfNotPresent, Command: []string{"sh", "-c", "exit 1"},
			}}, RestartPolicy: corev1.RestartPolicyAlways},
		}
		if err := c.Create(ctx, pod); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
	}
	crash("p1-production")
	crash("p1-pr-1")
	crash("default")
	eventually(t, 10*time.Minute, "crashing pods to be alerts, in a project and out of one", func() (bool, string) {
		project, system := false, false
		for _, alert := range prometheusAlerts(t, kube) {
			switch alert.Labels["alertname"] {
			case "GeassPodCrashLooping":
				project = alert.Labels["project"] == "p1" && alert.Labels["project_name"] == "Morning Beach" &&
					alert.Labels["environment"] == "production" && alert.Labels["resource"] == crasher &&
					alert.Labels["geass_kind"] == "workload.crashlooping" && alert.Labels["path"] == "/projects/p1"
			case "GeassSystemPodCrashLooping":
				system = alert.Labels["resource"] == crasher && alert.Labels["project"] == ""
			}
		}
		return project && system, fmt.Sprintf("project alert: %v, system alert: %v", project, system)
	})

	// A pull request's copy crashes as a matter of course. Its pod is crash-looping as far as Kubernetes says, and neither rule that
	// looks for crash loops, the project's or the platform's, has it.
	var rules monitoringv1.PrometheusRule
	if err := c.Get(ctx, client.ObjectKey{Name: "geass-alerts", Namespace: platform.MonitoringTargetNS}, &rules); err != nil {
		t.Fatal(err)
	}
	query := func(expr string) []string {
		var body struct {
			Data struct {
				Result []struct {
					Metric map[string]string `json:"metric"`
				} `json:"result"`
			} `json:"data"`
		}
		_ = json.Unmarshal(prometheusGet(t, kube, "/api/v1/query", map[string]string{"query": expr}), &body)
		namespaces := make([]string, 0, len(body.Data.Result))
		for _, result := range body.Data.Result {
			namespaces = append(namespaces, result.Metric["namespace"])
		}
		return namespaces
	}
	eventually(t, 5*time.Minute, "Kubernetes to say that the pull request's pod is crash-looping", func() (bool, string) {
		got := query(`kube_pod_container_status_waiting_reason{namespace="p1-pr-1",reason="CrashLoopBackOff"} == 1`)
		return len(got) > 0, "not yet"
	})
	checked := 0
	for _, group := range rules.Spec.Groups {
		for _, rule := range group.Rules {
			if rule.Alert != "GeassPodCrashLooping" && rule.Alert != "GeassSystemPodCrashLooping" {
				continue
			}
			checked++
			if got := query(rule.Expr.String()); slices.Contains(got, "p1-pr-1") {
				t.Fatalf("%s has the pull request's pod: %v", rule.Alert, got)
			}
		}
	}
	if checked != 2 {
		t.Fatalf("looked at %d of the two crash loop rules", checked)
	}

	// ---- the problems end, and the channels are told ----
	setReady(true)
	eventually(t, 12*time.Minute, "the resolved messages to reach Slack and the mailbox", func() (bool, string) {
		return len(slack.containing(rotated, "Deploy failed", webName+" (Morning Beach / production) (resolved)")) > 0 &&
			len(mail.containing("Subject: [Geass] Resolved: Deploy failed")) > 0, "not yet"
	})
}

func ptr[T any](v T) *T { return &v }
