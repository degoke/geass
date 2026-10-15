package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeAlertmanager answers the few calls the dashboard makes, and records what it is sent.
type fakeAlertmanager struct {
	ready    bool
	original string
	alerts   string
	metrics  string
	posted   []map[string]any
}

func (f *fakeAlertmanager) server(t *testing.T) *Alertmanager {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/-/ready":
			if !f.ready {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte("OK"))
		case r.URL.Path == "/api/v2/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"config": map[string]any{"original": f.original}})
		case r.URL.Path == "/api/v2/alerts" && r.Method == http.MethodGet:
			require.Equal(t, "true", r.URL.Query().Get("active"))
			require.Equal(t, "false", r.URL.Query().Get("silenced"), "a silenced alert is one somebody chose not to hear")
			_, _ = w.Write([]byte(f.alerts))
		case r.URL.Path == "/api/v2/alerts" && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			var batch []map[string]any
			require.NoError(t, json.Unmarshal(body, &batch))
			f.posted = append(f.posted, batch...)
		case r.URL.Path == "/metrics":
			_, _ = w.Write([]byte(f.metrics))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &Alertmanager{BaseURL: server.URL + "/", HTTP: server.Client()}
}

func TestAlertmanagerSaysWhetherItIsReady(t *testing.T) {
	fake := &fakeAlertmanager{}
	am := fake.server(t)
	require.ErrorContains(t, am.Ready(context.Background()), "HTTP 503")

	fake.ready = true
	require.NoError(t, am.Ready(context.Background()))

	gone := &Alertmanager{BaseURL: "http://127.0.0.1:1"}
	require.ErrorContains(t, gone.Ready(context.Background()), "could not reach Alertmanager")
}

func TestOnlyWhatGeassMakesIsShownAsFiring(t *testing.T) {
	fake := &fakeAlertmanager{alerts: `[
	  {"labels":{"geass_kind":"deploy.failed","severity":"critical","resource":"web","project":"p1"},"annotations":{"title":"Deploy failed","summary":"web"},"startsAt":"2026-10-15T10:00:00Z"},
	  {"labels":{"alertname":"KubeCPUOvercommit","severity":"warning"},"annotations":{},"startsAt":"2026-10-15T10:00:00Z"},
	  {"labels":{"geass_kind":"test","geass_test_channel":"aaaaaaa1"},"annotations":{},"startsAt":"2026-10-15T10:00:00Z"}
	]`}

	firing, err := fake.server(t).Firing(context.Background())

	require.NoError(t, err)
	require.Len(t, firing, 1, "not the cluster's own alerts, and not a test")
	require.Equal(t, "web", firing[0].Labels["resource"])
	require.Equal(t, "Deploy failed", firing[0].Annotations["title"])
	require.Equal(t, time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC), firing[0].StartsAt)
}

func TestNothingFiringIsAnEmptyListAndAnUnreadableAnswerIsAnError(t *testing.T) {
	fake := &fakeAlertmanager{alerts: `[]`}
	firing, err := fake.server(t).Firing(context.Background())
	require.NoError(t, err)
	require.NotNil(t, firing)
	require.Empty(t, firing)

	fake.alerts = `not json`
	_, err = fake.server(t).Firing(context.Background())
	require.ErrorContains(t, err, "could not be read")
}

func TestAReceiverIsLoadedWhenAlertmanagersConfigNamesIt(t *testing.T) {
	fake := &fakeAlertmanager{original: "receivers:\n- name: geass-system/geass-alerts/ch-aaaaaaa1\n  slack_configs: []\n- name: geass-system/geass-alerts/test-aaaaaaa1\n"}
	am := fake.server(t)

	loaded, err := am.HasReceiver(context.Background(), ReceiverName("geass-system", "aaaaaaa1", true))
	require.NoError(t, err)
	require.True(t, loaded)

	loaded, err = am.HasReceiver(context.Background(), ReceiverName("geass-system", "bbbbbbb2", true))
	require.NoError(t, err)
	require.False(t, loaded, "a channel added a moment ago is not in the configuration yet")

	loaded, err = am.HasReceiver(context.Background(), "geass-system/geass-alerts/ch-aaaaaaa")
	require.NoError(t, err)
	require.False(t, loaded, "a name that is only the start of another is not a match")
}

func TestATestAlertNamesItsChannelAndOnlyThat(t *testing.T) {
	fake := &fakeAlertmanager{}
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)

	require.NoError(t, fake.server(t).PostTest(context.Background(), "aaaaaaa1", "<!channel> ops\r\nBcc: x", now))

	require.Len(t, fake.posted, 1)
	labels := fake.posted[0]["labels"].(map[string]any)
	require.Equal(t, "aaaaaaa1", labels[LabelTestChannel])
	require.Equal(t, KindTest, labels[LabelKind])
	require.Equal(t, "channel", labels[LabelResourceKind])
	require.Equal(t, "!channel ops Bcc: x", labels[LabelResource], "a channel's name is made safe before it is a label")
	require.Equal(t, "Test alert from Geass", fake.posted[0]["annotations"].(map[string]any)["title"])
	require.Equal(t, "2026-10-15T10:00:00Z", fake.posted[0]["startsAt"])
	require.Equal(t, "2026-10-15T10:02:00Z", fake.posted[0]["endsAt"], "it ends by itself")
}

func TestEveryTestAlertIsItsOwnGroup(t *testing.T) {
	fake := &fakeAlertmanager{}
	am := fake.server(t)
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)

	require.NoError(t, am.PostTest(context.Background(), "aaaaaaa1", "ops", now))
	require.NoError(t, am.PostTest(context.Background(), "aaaaaaa1", "ops", now))

	require.Len(t, fake.posted, 2)
	id := func(posted map[string]any) any { return posted["labels"].(map[string]any)[LabelTestID] }
	require.NotEmpty(t, id(fake.posted[0]))
	require.NotEqual(t, id(fake.posted[0]), id(fake.posted[1]),
		"a second test of a channel must not join, or be taken for, the first: Alertmanager would hold it back for minutes")
}

func TestCountersAreReadByIntegration(t *testing.T) {
	fake := &fakeAlertmanager{metrics: `# HELP alertmanager_notification_requests_total The total number of attempted notification requests.
# TYPE alertmanager_notification_requests_total counter
alertmanager_notification_requests_total{integration="email"} 2
alertmanager_notification_requests_total{integration="slack"} 7
alertmanager_notification_requests_total{integration="webhook"} 0
# TYPE alertmanager_notification_requests_failed_total counter
alertmanager_notification_requests_failed_total{integration="slack"} 3
alertmanager_notification_requests_failed_total{integration="email"} 0
alertmanager_notifications_total{integration="slack"} 999
alertmanager_notification_requests_total{other="x",integration="pagerduty"} 1.5e1
some_other_metric{integration="slack"} 5
`}

	counters, err := fake.server(t).Counters(context.Background())

	require.NoError(t, err)
	require.Equal(t, Counter{Sent: 7, Failed: 3}, counters["slack"], "the count of messages is not the count of requests")
	require.Equal(t, Counter{Sent: 2}, counters["email"])
	require.Equal(t, Counter{Sent: 15}, counters["pagerduty"])
}

func TestTheAlertmanagerAddressCanBeChangedAndDefaultsToTheAddOns(t *testing.T) {
	t.Setenv(AlertmanagerURLEnv, "")
	require.Equal(t, "http://geass-kube-prometheus-stac-alertmanager.monitoring.svc:9093", DefaultAlertmanagerURL())

	t.Setenv(AlertmanagerURLEnv, "http://alertmanager.example.com:9093/")
	require.Equal(t, "http://alertmanager.example.com:9093", DefaultAlertmanagerURL())
}
