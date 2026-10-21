package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/alerts"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/webhooks"
)

const slackSecretURL = "https://hooks.slack.com/services/T000/B000/SUPERSECRETTOKEN"

// fakeAlertmanager answers what the dashboard asks Alertmanager, and changes its counters the way the real one does
// when it is given a test alert.
type fakeAlertmanager struct {
	mu        sync.Mutex
	ready     bool
	loaded    bool // whether it has read the channels' receivers
	alerts    string
	sent      map[string]float64
	failed    map[string]float64
	outcome   string // what happens to a test: "sent", "failed", or "nothing"
	posted    []map[string]any
	receivers func() []string
}

func newFakeAlertmanager(t *testing.T, srv *Server) *fakeAlertmanager {
	t.Helper()
	fake := &fakeAlertmanager{ready: true, loaded: true, alerts: `[]`, sent: map[string]float64{}, failed: map[string]float64{}, outcome: "sent"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		switch {
		case r.URL.Path == "/-/ready":
			if !fake.ready {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte("OK"))
		case r.URL.Path == "/api/v2/status":
			var original strings.Builder
			if fake.loaded && fake.receivers != nil {
				for _, name := range fake.receivers() {
					original.WriteString("- name: " + name + "\n")
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"config": map[string]any{"original": original.String()}})
		case r.URL.Path == "/api/v2/alerts" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(fake.alerts))
		case r.URL.Path == "/api/v2/alerts" && r.Method == http.MethodPost:
			var body []map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			fake.posted = append(fake.posted, body...)
			integration := "slack"
			if channel, ok := body[0]["labels"].(map[string]any)[alerts.LabelTestChannel].(string); ok {
				if config, err := alerts.Load(asSystem(context.Background()), srv.Client, platform.SystemNamespace); err == nil {
					if found, ok := config.Channel(channel); ok && found.Type == alerts.TypeEmail {
						integration = "email"
					}
				}
			}
			switch fake.outcome {
			case "sent":
				fake.sent[integration]++
			case "failed":
				fake.sent[integration]++
				fake.failed[integration]++
			}
		case r.URL.Path == "/metrics":
			for _, integration := range []string{"slack", "email"} {
				_, _ = fmt.Fprintf(w, "alertmanager_notification_requests_total{integration=%q} %v\n", integration, fake.sent[integration])
				_, _ = fmt.Fprintf(w, "alertmanager_notification_requests_failed_total{integration=%q} %v\n", integration, fake.failed[integration])
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	srv.Alertmanager = &alerts.Alertmanager{BaseURL: server.URL, HTTP: server.Client()}
	return fake
}

// publicHosts makes every name the dashboard looks up resolve to a public address, so that no test needs DNS.
func publicHosts(t *testing.T) {
	t.Helper()
	previous := webhooks.LookupIP
	webhooks.LookupIP = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("93.184.216.34")}, nil }
	t.Cleanup(func() { webhooks.LookupIP = previous })
}

func quickAlertTests(t *testing.T) {
	t.Helper()
	config, delivery, step := alertTestConfigWait, alertTestDeliveryWait, alertTestStep
	alertTestConfigWait, alertTestDeliveryWait, alertTestStep = 150*time.Millisecond, 150*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { alertTestConfigWait, alertTestDeliveryWait, alertTestStep = config, delivery, step })
}

func newAlertFixture(t *testing.T) (tokenFixture, *fakeAlertmanager) {
	t.Helper()
	publicHosts(t)
	f := newTokenFixture(t)
	fake := newFakeAlertmanager(t, f.srv)
	fake.receivers = func() []string {
		var names []string
		var config monitoringv1alpha1.AlertmanagerConfig
		if err := f.srv.Client.Get(context.Background(), client.ObjectKey{Name: alerts.AlertmanagerConfigName, Namespace: platform.SystemNamespace}, &config); err == nil {
			for _, receiver := range config.Spec.Receivers {
				names = append(names, platform.SystemNamespace+"/"+alerts.AlertmanagerConfigName+"/"+receiver.Name)
			}
		}
		return names
	}
	return f, fake
}

func (f tokenFixture) alertGet(t *testing.T) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := f.asAdmin(t, browserRequest(http.MethodGet, "/api/settings/alerts"))
	var body map[string]any
	if rec.Code == http.StatusOK {
		body = decodeBody(t, rec)
	}
	return rec, body
}

func (f tokenFixture) alertPost(t *testing.T, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return f.asAdmin(t, postForm("/api/settings/alerts/"+path, form))
}

func (f tokenFixture) storedAlerting(t *testing.T) *geassv1alpha1.GeassAlertingSpec {
	t.Helper()
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	require.NotNil(t, config.Spec.Alerting)
	return config.Spec.Alerting
}

func (f tokenFixture) secretData(t *testing.T, name string) map[string][]byte {
	t.Helper()
	var secret corev1.Secret
	if err := f.srv.Client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &secret); err != nil {
		return nil
	}
	return secret.Data
}

// alertmanagerConfig is what the dashboard has put into the cluster for Alertmanager, or nil when there is none.
func (f tokenFixture) alertmanagerConfig(t *testing.T) *monitoringv1alpha1.AlertmanagerConfig {
	t.Helper()
	var config monitoringv1alpha1.AlertmanagerConfig
	err := f.srv.Client.Get(context.Background(), client.ObjectKey{Name: alerts.AlertmanagerConfigName, Namespace: platform.SystemNamespace}, &config)
	if client.IgnoreNotFound(err) != nil {
		require.NoError(t, err)
	}
	if err != nil {
		return nil
	}
	return &config
}

func receiverNames(config *monitoringv1alpha1.AlertmanagerConfig) []string {
	if config == nil {
		return nil
	}
	names := make([]string, 0, len(config.Spec.Receivers))
	for _, receiver := range config.Spec.Receivers {
		names = append(names, receiver.Name)
	}
	return names
}

func (f tokenFixture) saveSMTP(t *testing.T, change func(url.Values)) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"host": {"smtp.example.com"}, "port": {"587"}, "security": {"starttls"}, "username": {"geass"}, "password": {"s3cret"}, "from": {"Geass <alerts@example.com>"}}
	if change != nil {
		change(form)
	}
	return f.alertPost(t, "smtp/save", form)
}

func (f tokenFixture) saveChannel(t *testing.T, form url.Values) map[string]any {
	t.Helper()
	rec := f.alertPost(t, "channels/save", form)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return decodeBody(t, rec)
}

func slackForm(name string) url.Values {
	return url.Values{"name": {name}, "type": {"slack"}, "url": {slackSecretURL}, "notifyResolved": {"on"}}
}

func pointerTo[T any](v T) *T { return &v }

func TestOnlyASignedInAdminCanSeeOrChangeAlertSettings(t *testing.T) {
	f, _ := newAlertFixture(t)

	// Signed out.
	require.Equal(t, http.StatusUnauthorized, f.do(browserRequest(http.MethodGet, "/api/settings/alerts")).Code)
	require.Equal(t, http.StatusUnauthorized, f.do(postForm("/api/settings/alerts/channels/save", slackForm("ops"))).Code)

	// A viewer.
	viewer := func(req *http.Request) *http.Request {
		return withDashboardSession(t, f.srv, req, "viewer", dashboardRoleViewer)
	}
	require.Equal(t, http.StatusForbidden, f.do(viewer(browserRequest(http.MethodGet, "/api/settings/alerts"))).Code, "it names the mail server and the people who get mail")
	require.Equal(t, http.StatusForbidden, f.do(viewer(postForm("/api/settings/alerts/channels/save", slackForm("ops")))).Code)

	// A token, even one that can do anything an admin can: a token that could redirect alerts could also silence them.
	token := f.workspaceToken(t, "admin")
	read := f.do(bearer(token, http.MethodGet, "/api/settings/alerts", nil))
	require.Equal(t, http.StatusForbidden, read.Code, read.Body.String())
	write := bearer(token, http.MethodPost, "/api/settings/alerts/channels/save", slackForm("ops"))
	write.Header.Set("Accept", "application/json")
	rec := f.do(write)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "signed-in admin")
	require.Contains(t, f.do(bearer(f.projectToken(t, "production"), http.MethodGet, "/api/settings/alerts", nil)).Body.String(), "error")

	f.alertGet(t) // and an admin is let in
}

func TestAnEmptyInstallationHasNothingSetUpAndSaysWhetherAlertmanagerIsThere(t *testing.T) {
	f, fake := newAlertFixture(t)

	rec, body := f.alertGet(t)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Nil(t, body["smtp"])
	require.Equal(t, []any{}, body["channels"])
	require.Equal(t, []any{}, body["firing"])
	require.Equal(t, false, body["listening"])
	require.Equal(t, map[string]any{"ready": true}, body["alertmanager"])
	require.Len(t, body["kinds"], len(alerts.Kinds))
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	fake.ready = false
	_, body = f.alertGet(t)
	alertmanager := body["alertmanager"].(map[string]any)
	require.Equal(t, false, alertmanager["ready"])
	require.Contains(t, alertmanager["error"], "Alertmanager", "the page says why nothing can be sent")
	require.Equal(t, []any{}, body["firing"])
}

func TestWhatIsFiringAndHowDeliveryIsGoingAreShown(t *testing.T) {
	f, fake := newAlertFixture(t)
	fake.alerts = `[
	  {"labels":{"geass_kind":"deploy.failed","severity":"warning","project":"p1","project_name":"Morning Beach","environment":"staging","resource":"api","resource_kind":"service","path":"/projects/p1/apps/api"},"annotations":{"title":"Deploy failed","summary":"api (Morning Beach / staging)"},"startsAt":"2026-10-15T10:00:00Z"},
	  {"labels":{"geass_kind":"node.unhealthy","severity":"critical","resource":"worker-1","resource_kind":"node","path":"/cluster"},"annotations":{"title":"Node not ready","summary":"worker-1"},"startsAt":"2026-10-15T09:00:00Z"},
	  {"labels":{"alertname":"KubeCPUOvercommit","severity":"critical"},"annotations":{},"startsAt":"2026-10-15T09:00:00Z"}
	]`
	fake.sent["slack"], fake.failed["slack"] = 12, 3

	_, body := f.alertGet(t)

	firing := body["firing"].([]any)
	require.Len(t, firing, 2, "the cluster's own alerts are not part of what Geass shows")
	require.Equal(t, "Node not ready", firing[0].(map[string]any)["title"], "what is most urgent comes first")
	require.Equal(t, "Morning Beach", firing[1].(map[string]any)["projectName"])
	require.Equal(t, map[string]any{"sent": float64(12), "failed": float64(3)}, body["deliveries"].(map[string]any)["slack"])
}

func TestTheMailServerIsSavedWithItsPasswordInASecretAndNeverReturned(t *testing.T) {
	f, _ := newAlertFixture(t)

	rec := f.saveSMTP(t, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	smtp := f.storedAlerting(t).SMTP
	require.NotNil(t, smtp)
	require.Equal(t, "smtp.example.com", smtp.Host)
	require.EqualValues(t, 587, smtp.Port)
	require.Equal(t, "starttls", smtp.Security)
	require.Equal(t, "geass", smtp.Username)
	require.Equal(t, "Geass <alerts@example.com>", smtp.From)
	require.Equal(t, &corev1.LocalObjectReference{Name: alerts.SMTPSecretName}, smtp.CredentialRef)
	require.Equal(t, "s3cret", string(f.secretData(t, alerts.SMTPSecretName)[alerts.SMTPPasswordKey]))

	get, body := f.alertGet(t)
	require.NotContains(t, get.Body.String(), "s3cret")
	require.Equal(t, map[string]any{
		"host": "smtp.example.com", "port": float64(587), "security": "starttls", "username": "geass", "from": "Geass <alerts@example.com>", "hasPassword": true,
	}, body["smtp"])
}

func TestTheMailServerPasswordCanBeKeptWhenOtherFieldsChange(t *testing.T) {
	f, _ := newAlertFixture(t)
	f.saveSMTP(t, nil)

	rec := f.saveSMTP(t, func(v url.Values) {
		v.Set("host", "smtp.other.example.com")
		v.Set("port", "465")
		v.Set("security", "tls")
		v.Del("password")
		v.Set("keepPassword", "on")
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "s3cret", string(f.secretData(t, alerts.SMTPSecretName)[alerts.SMTPPasswordKey]), "it was not asked to change")
	smtp := f.storedAlerting(t).SMTP
	require.Equal(t, "smtp.other.example.com", smtp.Host)
	require.Equal(t, &corev1.LocalObjectReference{Name: alerts.SMTPSecretName}, smtp.CredentialRef, "and the settings still point at it")
}

func TestAPasswordKeptInASecretTheAdminMadeIsKeptWhereItIs(t *testing.T) {
	f, _ := newAlertFixture(t)
	// Set up with kubectl, in a Secret of its own.
	f.saveSMTP(t, nil)
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	config.Spec.Alerting.SMTP.CredentialRef = &corev1.LocalObjectReference{Name: "ses-smtp"}
	require.NoError(t, f.srv.Client.Update(context.Background(), &config))

	rec := f.saveSMTP(t, func(v url.Values) {
		v.Set("from", "alerts@example.com")
		v.Del("password")
		v.Set("keepPassword", "on")
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "ses-smtp", f.storedAlerting(t).SMTP.CredentialRef.Name, "the reference is theirs and is not replaced")
}

func TestTheMailServerNeedsAPasswordWhenThereIsAUsernameAndNothingToKeep(t *testing.T) {
	f, _ := newAlertFixture(t)

	rec := f.saveSMTP(t, func(v url.Values) { v.Del("password") })
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Enter the password for geass")

	rec = f.saveSMTP(t, func(v url.Values) { v.Del("password"); v.Set("keepPassword", "on") })
	require.Equal(t, http.StatusBadRequest, rec.Code, "keeping a password that was never saved is not a thing")
	require.Contains(t, rec.Body.String(), "Enter the password for geass")
	require.Nil(t, f.secretData(t, alerts.SMTPSecretName))
}

func TestAMailServerWithoutALoginDropsTheOldPassword(t *testing.T) {
	f, _ := newAlertFixture(t)
	f.saveSMTP(t, nil)

	rec := f.saveSMTP(t, func(v url.Values) {
		v.Set("username", "")
		v.Set("security", "none")
		v.Set("port", "25")
		v.Del("password")
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Nil(t, f.storedAlerting(t).SMTP.CredentialRef)
	require.Nil(t, f.secretData(t, alerts.SMTPSecretName), "a password nobody uses is not kept")
}

func TestMailServerSettingsAreCheckedAgainstWhatAlertmanagerCanDo(t *testing.T) {
	f, _ := newAlertFixture(t)
	for name, change := range map[string]func(url.Values){
		"no host":                       func(v url.Values) { v.Set("host", "") },
		"host with a scheme":            func(v url.Values) { v.Set("host", "smtp://smtp.example.com") },
		"port that is no port":          func(v url.Values) { v.Set("port", "abc") },
		"port out of range":             func(v url.Values) { v.Set("port", "70000") },
		"unknown security":              func(v url.Values) { v.Set("security", "ssl") },
		"tls on a port that is not 465": func(v url.Values) { v.Set("security", "tls") },
		"starttls on port 465":          func(v url.Values) { v.Set("port", "465") },
		"a password with no encryption": func(v url.Values) { v.Set("security", "none"); v.Set("port", "25") },
		"no sender":                     func(v url.Values) { v.Set("from", "") },
		"a sender that is no address":   func(v url.Values) { v.Set("from", "not an address") },
		"a password without a username": func(v url.Values) { v.Set("username", "") },
	} {
		rec := f.saveSMTP(t, change)
		require.Equal(t, http.StatusBadRequest, rec.Code, name+": "+rec.Body.String())
		require.Contains(t, rec.Body.String(), "error", name)
	}
	require.Nil(t, f.alertmanagerConfig(t), "nothing that was refused reaches Alertmanager")
}

func TestAMailServerOnAPrivateAddressIsRefusedBecauseAlertmanagerMakesTheConnection(t *testing.T) {
	f, _ := newAlertFixture(t)
	for _, host := range []string{"10.0.0.5", "127.0.0.1", "169.254.169.254"} {
		rec := f.saveSMTP(t, func(v url.Values) { v.Set("host", host) })
		require.Equal(t, http.StatusBadRequest, rec.Code, host)
		require.Contains(t, rec.Body.String(), host)
	}

	// A name that resolves to one is no better.
	previous := webhooks.LookupIP
	webhooks.LookupIP = func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP("10.1.2.3")}, nil }
	t.Cleanup(func() { webhooks.LookupIP = previous })
	rec := f.saveSMTP(t, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "10.1.2.3")
	require.Contains(t, rec.Body.String(), webhooks.AllowPrivateNetworksEnv, "and it says how an operator allows it")

	t.Setenv(webhooks.AllowPrivateNetworksEnv, "true")
	require.Equal(t, http.StatusOK, f.saveSMTP(t, nil).Code, "an operator who allows private networks can use one")
}

func TestTheMailServerCanBeRemovedAndTakesItsPasswordWithIt(t *testing.T) {
	f, _ := newAlertFixture(t)
	f.saveSMTP(t, nil)
	f.saveChannel(t, url.Values{"name": {"mail"}, "type": {"email"}, "recipients": {"ops@example.com"}, "notifyResolved": {"on"}})
	require.Contains(t, receiverNames(f.alertmanagerConfig(t)), "ch-"+f.storedAlerting(t).Channels[0].ID)

	rec := f.alertPost(t, "smtp/clear", url.Values{})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Nil(t, f.storedAlerting(t).SMTP)
	require.Nil(t, f.secretData(t, alerts.SMTPSecretName))
	require.Nil(t, f.alertmanagerConfig(t), "an email channel with no mail server has nothing to send through, so nothing is left to route")
}

func TestASlackChannelKeepsItsAddressInASecretAndNeverReturnsIt(t *testing.T) {
	f, _ := newAlertFixture(t)

	created := f.saveChannel(t, slackForm("#ops"))

	id, _ := created["id"].(string)
	require.Regexp(t, `^[a-z0-9]{8}$`, id)
	channels := f.storedAlerting(t).Channels
	require.Len(t, channels, 1)
	require.Equal(t, geassv1alpha1.GeassAlertChannel{ID: id, Name: "#ops", Type: "slack", NotifyResolved: pointerTo(true)}, channels[0])
	require.Equal(t, slackSecretURL, string(f.secretData(t, alerts.ChannelsSecretName)[id]))

	get, body := f.alertGet(t)
	require.NotContains(t, get.Body.String(), "SUPERSECRETTOKEN", "anyone with the address can post as the channel")
	require.NotContains(t, get.Body.String(), "/services/T000")
	channel := body["channels"].([]any)[0].(map[string]any)
	require.Equal(t, "hooks.slack.com", channel["target"], "the host alone, so a person can tell which one it is")
	require.Equal(t, true, channel["hasSecret"])
	require.Equal(t, true, body["listening"])
}

func TestSavingAChannelPutsItIntoAlertmanagersConfigurationAtOnce(t *testing.T) {
	f, _ := newAlertFixture(t)
	require.Nil(t, f.alertmanagerConfig(t))

	id := f.saveChannel(t, slackForm("#ops"))["id"].(string)

	config := f.alertmanagerConfig(t)
	require.NotNil(t, config, "not left for the operator's next look")
	require.ElementsMatch(t, []string{"geass-default", "ch-" + id, "test-" + id}, receiverNames(config))
	require.Len(t, config.OwnerReferences, 1, "it goes when the platform configuration does")

	// An email channel has nothing to send through until the mail server is set up, and is left out until then.
	mailID := f.saveChannel(t, url.Values{"name": {"mail"}, "type": {"email"}, "recipients": {"ops@example.com"}, "notifyResolved": {"on"}})["id"].(string)
	require.NotContains(t, receiverNames(f.alertmanagerConfig(t)), "ch-"+mailID)
	require.Equal(t, http.StatusOK, f.saveSMTP(t, nil).Code)
	require.Contains(t, receiverNames(f.alertmanagerConfig(t)), "ch-"+mailID, "and joins when the mail server is saved")
}

func TestAnEmailChannelStoresItsRecipientsAndEveryKindIsNoLimit(t *testing.T) {
	f, _ := newAlertFixture(t)
	form := url.Values{
		"name": {"On call"}, "type": {"email"}, "recipients": {"Ops@example.com, dev@example.com\nops@example.com"},
		"notifyResolved": {"on"},
	}
	for _, kind := range alerts.Kinds {
		form.Add("event", kind.ID)
	}

	f.saveChannel(t, form)

	channel := f.storedAlerting(t).Channels[0]
	require.Equal(t, []string{"Ops@example.com", "dev@example.com"}, channel.Recipients, "a repeat is dropped")
	require.Nil(t, channel.Events, "choosing everything is no limit, so that kinds added later reach the channel")
	require.Nil(t, f.secretData(t, alerts.ChannelsSecretName), "an email channel has no secret")
}

func TestAChannelCanBeLimitedToKindsProjectsAndEnvironments(t *testing.T) {
	f, _ := newAlertFixture(t)
	form := slackForm("#prod")
	form["event"] = []string{"build.failed", "service.down", "build.failed"}
	form["project"] = []string{testProjectName}
	form["environment"] = []string{"production"}
	form.Set("disabled", "on")
	form.Del("notifyResolved")

	id := f.saveChannel(t, form)["id"].(string)

	channel := f.storedAlerting(t).Channels[0]
	require.Equal(t, []string{"build.failed", "service.down"}, channel.Events)
	require.Equal(t, []string{testProjectName}, channel.Projects)
	require.Equal(t, []string{"production"}, channel.Environments)
	require.True(t, channel.Disabled)
	require.Equal(t, pointerTo(false), channel.NotifyResolved)
	_, body := f.alertGet(t)
	require.Equal(t, false, body["listening"], "a paused channel is not listening")
	require.ElementsMatch(t, []string{"geass-default", "test-" + id}, receiverNames(f.alertmanagerConfig(t)), "and is only there to be tried")
}

func TestChannelInputIsChecked(t *testing.T) {
	f, _ := newAlertFixture(t)
	with := func(change func(url.Values)) url.Values {
		form := slackForm("#ops")
		change(form)
		return form
	}
	for name, form := range map[string]url.Values{
		"no name":                  with(func(v url.Values) { v.Set("name", " ") }),
		"name too long":            with(func(v url.Values) { v.Set("name", strings.Repeat("n", 61)) }),
		"unknown type":             with(func(v url.Values) { v.Set("type", "pager") }),
		"slack without an address": with(func(v url.Values) { v.Del("url") }),
		"slack over http":          with(func(v url.Values) { v.Set("url", "http://hooks.slack.com/services/x") }),
		"slack at localhost":       with(func(v url.Values) { v.Set("url", "https://localhost/hook") }),
		"slack at a private host":  with(func(v url.Values) { v.Set("url", "https://10.0.0.5/hook") }),
		"slack at the metadata ip": with(func(v url.Values) { v.Set("url", "https://169.254.169.254/latest/meta-data") }),
		"unknown alert":            with(func(v url.Values) { v.Set("event", "disk.full") }),
		"the test kind":            with(func(v url.Values) { v.Set("event", "test") }),
		"unknown project":          with(func(v url.Values) { v.Set("project", "nope") }),
		"bad environment":          with(func(v url.Values) { v.Set("environment", "Prod Env") }),
		"email without recipients": {"name": {"mail"}, "type": {"email"}},
		"email with a bad address": {"name": {"mail"}, "type": {"email"}, "recipients": {"ops@example.com nonsense"}},
		"email with a template":    {"name": {"mail"}, "type": {"email"}, "recipients": {`"{{ .ExternalURL }}"@example.com`}},
	} {
		rec := f.alertPost(t, "channels/save", form)
		require.Equal(t, http.StatusBadRequest, rec.Code, name+": "+rec.Body.String())
	}
	require.Empty(t, f.secretData(t, alerts.ChannelsSecretName), "nothing was stored by the ones that failed")
	require.Nil(t, f.alertmanagerConfig(t))
}

func TestOnlyTwentyChannelsCanBeMade(t *testing.T) {
	f, _ := newAlertFixture(t)
	for i := range alerts.MaxChannels {
		f.saveChannel(t, slackForm("channel "+string(rune('a'+i))))
	}

	rec := f.alertPost(t, "channels/save", slackForm("one too many"))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "at most 20 channels")
	require.Len(t, f.secretData(t, alerts.ChannelsSecretName), alerts.MaxChannels, "the secret of the channel that was refused is not left behind")
}

func TestEditingAChannelKeepsItsAddressUnlessANewOneIsGiven(t *testing.T) {
	f, _ := newAlertFixture(t)
	id := f.saveChannel(t, slackForm("#ops"))["id"].(string)

	edit := slackForm("#ops-renamed")
	edit.Set("id", id)
	edit.Del("url")
	edit.Add("event", "build.failed")
	f.saveChannel(t, edit)

	channels := f.storedAlerting(t).Channels
	require.Len(t, channels, 1, "an edit is not a new channel")
	require.Equal(t, "#ops-renamed", channels[0].Name)
	require.Equal(t, []string{"build.failed"}, channels[0].Events)
	require.Equal(t, slackSecretURL, string(f.secretData(t, alerts.ChannelsSecretName)[id]))

	rotate := slackForm("#ops-renamed")
	rotate.Set("id", id)
	rotate.Set("url", "https://hooks.slack.com/services/T111/B111/NEWTOKEN")
	f.saveChannel(t, rotate)
	require.Equal(t, "https://hooks.slack.com/services/T111/B111/NEWTOKEN", string(f.secretData(t, alerts.ChannelsSecretName)[id]))
}

func TestAChannelsTypeCannotBeChangedAndAMissingChannelIsReported(t *testing.T) {
	f, _ := newAlertFixture(t)
	id := f.saveChannel(t, slackForm("#ops"))["id"].(string)

	asEmail := url.Values{"id": {id}, "name": {"#ops"}, "type": {"email"}, "recipients": {"ops@example.com"}}
	rec := f.alertPost(t, "channels/save", asEmail)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "cannot be changed")

	missing := slackForm("#ghost")
	missing.Set("id", "00000000")
	rec = f.alertPost(t, "channels/save", missing)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "was not found")
}

func TestDeletingAChannelRemovesItsRouteBeforeItsSecretAndOnlyItsOwn(t *testing.T) {
	f, _ := newAlertFixture(t)
	first := f.saveChannel(t, slackForm("#one"))["id"].(string)
	second := f.saveChannel(t, slackForm("#two"))["id"].(string)

	rec := f.alertPost(t, "channels/delete", url.Values{"id": {first}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	channels := f.storedAlerting(t).Channels
	require.Len(t, channels, 1)
	require.Equal(t, second, channels[0].ID)
	data := f.secretData(t, alerts.ChannelsSecretName)
	require.NotContains(t, data, first)
	require.Contains(t, data, second)
	require.ElementsMatch(t, []string{"geass-default", "ch-" + second, "test-" + second}, receiverNames(f.alertmanagerConfig(t)),
		"nothing in the configuration refers to a secret that is gone, or the operator would drop all of it")

	require.Equal(t, http.StatusOK, f.alertPost(t, "channels/delete", url.Values{"id": {second}}).Code)
	require.Nil(t, f.secretData(t, alerts.ChannelsSecretName), "the last secret goes with the Secret")
	require.Nil(t, f.alertmanagerConfig(t), "and with the last channel there is nothing left to route")
	require.Equal(t, http.StatusBadRequest, f.alertPost(t, "channels/delete", url.Values{"id": {second}}).Code)
}

func TestWithoutTheMonitoringAddOnASavedChannelIsSavedAndSaysWhatIsMissing(t *testing.T) {
	// The AlertmanagerConfig kind comes with the monitoring add-on. A cluster without it cannot hold one.
	publicHosts(t)
	f := newTokenFixture(t)
	f.srv.Client = withoutAlertmanagerKind{f.srv.Client}

	rec := f.alertPost(t, "channels/save", slackForm("#ops"))

	// A failure leaves the form open, and sending it again would make the same channel twice: this is saved.
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := decodeBody(t, rec)
	require.Equal(t, true, body["ok"])
	require.NotEmpty(t, body["id"], "the page needs to know which channel it has saved")
	require.Contains(t, body["warning"], "monitoring add-on")
	require.Len(t, f.storedAlerting(t).Channels, 1, "what was typed is kept: it works once the add-on is there")
}

// failingAlertmanagerWrites is a client for a cluster whose API server refuses to change the AlertmanagerConfig.
type failingAlertmanagerWrites struct{ client.Client }

func (c failingAlertmanagerWrites) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if _, ok := obj.(*monitoringv1alpha1.AlertmanagerConfig); ok {
		return errors.New("the API server is busy")
	}
	return c.Client.Update(ctx, obj, opts...)
}

func (c failingAlertmanagerWrites) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if _, ok := obj.(*monitoringv1alpha1.AlertmanagerConfig); ok {
		return errors.New("the API server is busy")
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func TestASecretStaysWhileTheAlertmanagerConfigThatRefersToItCouldNotBeChanged(t *testing.T) {
	// The Prometheus operator drops the whole configuration when it refers to a secret that is not there.
	f, _ := newAlertFixture(t)
	id := f.saveChannel(t, slackForm("#ops"))["id"].(string)
	require.NotNil(t, f.alertmanagerConfig(t))
	f.srv.Client = failingAlertmanagerWrites{f.srv.Client}

	rec := f.alertPost(t, "channels/delete", url.Values{"id": {id}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, decodeBody(t, rec)["warning"], "could not be updated yet")
	require.Empty(t, f.storedAlerting(t).Channels, "the channel is gone")
	require.Contains(t, f.secretData(t, alerts.ChannelsSecretName), id, "but the configuration that still names its secret has not been changed")
	require.NotNil(t, f.alertmanagerConfig(t))
}

// withoutAlertmanagerKind is a client for a cluster the monitoring add-on is not installed in: the kind is not there.
type withoutAlertmanagerKind struct{ client.Client }

func (c withoutAlertmanagerKind) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*monitoringv1alpha1.AlertmanagerConfig); ok {
		return &meta.NoKindMatchError{GroupKind: monitoringv1alpha1.SchemeGroupVersion.WithKind("AlertmanagerConfig").GroupKind()}
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c withoutAlertmanagerKind) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*monitoringv1alpha1.AlertmanagerConfig); ok {
		return &meta.NoKindMatchError{GroupKind: monitoringv1alpha1.SchemeGroupVersion.WithKind("AlertmanagerConfig").GroupKind()}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

// ---- trying a channel ----

func TestATestGoesThroughAlertmanagerAndSaysItWasSent(t *testing.T) {
	quickAlertTests(t)
	f, fake := newAlertFixture(t)
	id := f.saveChannel(t, slackForm("#ops"))["id"].(string)

	rec := f.alertPost(t, "channels/test", url.Values{"id": {id}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, map[string]any{"ok": true, "error": ""}, decodeBody(t, rec))
	require.Len(t, fake.posted, 1)
	labels := fake.posted[0]["labels"].(map[string]any)
	require.Equal(t, id, labels[alerts.LabelTestChannel], "it names the channel, which is how it reaches that one and no other")
	require.Equal(t, "#ops", labels[alerts.LabelResource])
}

// Alertmanager's counts are for every channel that sends through Slack or the mail server. Its log says which receiver failed.
func TestATestIsJudgedByWhatAlertmanagerLogsAboutThatChannel(t *testing.T) {
	quickAlertTests(t)
	f, fake := newAlertFixture(t)
	id := f.saveChannel(t, slackForm("#ops"))["id"].(string)
	logs := ""
	f.srv.alertmanagerLog = func(context.Context, time.Time) (string, error) { return logs, nil }

	// Another channel is failing while this one is tried: the counts say failed, and the log says nothing against this one.
	fake.outcome = "failed"
	logs = `level=WARN msg="Notify attempt failed, will retry later" receiver=` + alerts.ReceiverName(systemNamespace, "zzzzzzz9", false) + " integration=slack[0]\n"
	rec := f.alertPost(t, "channels/test", url.Values{"id": {id}})
	require.Equal(t, map[string]any{"ok": true, "error": ""}, decodeBody(t, rec), "what another channel did is not this channel's result")

	// This one fails, whatever else was sent in the meantime.
	fake.outcome = "sent"
	logs = `level=WARN msg="Notify attempt failed, will retry later" receiver=` + alerts.ReceiverName(systemNamespace, id, true) + " integration=slack[0]\n"
	rec = f.alertPost(t, "channels/test", url.Values{"id": {id}})
	body := decodeBody(t, rec)
	require.Equal(t, false, body["ok"])
	require.Contains(t, body["error"], "could not post to Slack")
}

func TestATestToAPausedChannelStillWorks(t *testing.T) {
	quickAlertTests(t)
	f, fake := newAlertFixture(t)
	form := slackForm("#ops")
	form.Set("disabled", "on")
	id := f.saveChannel(t, form)["id"].(string)

	rec := f.alertPost(t, "channels/test", url.Values{"id": {id}})

	require.Equal(t, map[string]any{"ok": true, "error": ""}, decodeBody(t, rec))
	require.Len(t, fake.posted, 1)
}

func TestATestThatAlertmanagerCouldNotSendSaysSo(t *testing.T) {
	quickAlertTests(t)
	f, fake := newAlertFixture(t)
	fake.outcome = "failed"
	slackID := f.saveChannel(t, slackForm("#ops"))["id"].(string)
	require.Equal(t, http.StatusOK, f.saveSMTP(t, nil).Code)
	mailID := f.saveChannel(t, url.Values{"name": {"mail"}, "type": {"email"}, "recipients": {"ops@example.com"}, "notifyResolved": {"on"}})["id"].(string)

	slack := decodeBody(t, f.alertPost(t, "channels/test", url.Values{"id": {slackID}}))
	require.Equal(t, false, slack["ok"])
	require.Contains(t, slack["error"], "could not post to Slack")

	mail := decodeBody(t, f.alertPost(t, "channels/test", url.Values{"id": {mailID}}))
	require.Equal(t, false, mail["ok"])
	require.Contains(t, mail["error"], "could not send the email")
}

func TestATestWhoseOutcomeIsNotKnownYetSaysThatAndNotThatItWorked(t *testing.T) {
	quickAlertTests(t)
	f, fake := newAlertFixture(t)
	fake.outcome = "nothing"
	id := f.saveChannel(t, slackForm("#ops"))["id"].(string)

	body := decodeBody(t, f.alertPost(t, "channels/test", url.Values{"id": {id}}))

	require.Equal(t, false, body["ok"])
	require.Contains(t, body["error"], "has not reported sending it yet")
}

func TestATestWaitsForAlertmanagerToHaveLoadedTheChannel(t *testing.T) {
	quickAlertTests(t)
	f, fake := newAlertFixture(t)
	id := f.saveChannel(t, slackForm("#ops"))["id"].(string)
	fake.mu.Lock()
	fake.loaded = false
	fake.mu.Unlock()

	body := decodeBody(t, f.alertPost(t, "channels/test", url.Values{"id": {id}}))

	require.Equal(t, false, body["ok"])
	require.Contains(t, body["error"], "has not loaded this channel yet")
	require.Empty(t, fake.posted, "a test sent before that would be thrown away")
}

func TestATestWhenAlertmanagerIsNotThereSaysWhyAlertsCannotBeSent(t *testing.T) {
	quickAlertTests(t)
	f, fake := newAlertFixture(t)
	id := f.saveChannel(t, slackForm("#ops"))["id"].(string)
	fake.ready = false

	body := decodeBody(t, f.alertPost(t, "channels/test", url.Values{"id": {id}}))

	require.Equal(t, false, body["ok"])
	require.Contains(t, body["error"], "monitoring add-on")
}

func TestAChannelThatCannotSendIsNotTriedAndSaysWhy(t *testing.T) {
	quickAlertTests(t)
	f, fake := newAlertFixture(t)
	mailID := f.saveChannel(t, url.Values{"name": {"mail"}, "type": {"email"}, "recipients": {"ops@example.com"}, "notifyResolved": {"on"}})["id"].(string)

	body := decodeBody(t, f.alertPost(t, "channels/test", url.Values{"id": {mailID}}))
	require.Equal(t, false, body["ok"])
	require.Equal(t, "Set up the mail server first.", body["error"])
	require.Empty(t, fake.posted)

	require.Equal(t, http.StatusBadRequest, f.alertPost(t, "channels/test", url.Values{"id": {"00000000"}}).Code)
}

// miniSMTP answers EHLO and QUIT, which is all that checking a connection takes.
func miniSMTP(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = conn.Write([]byte("220 mini ESMTP\r\n"))
				buf := make([]byte, 256)
				for {
					n, err := conn.Read(buf)
					if err != nil {
						return
					}
					switch line := strings.ToUpper(string(buf[:n])); {
					case strings.HasPrefix(line, "EHLO"):
						_, _ = conn.Write([]byte("250 mini\r\n"))
					case strings.HasPrefix(line, "QUIT"):
						_, _ = conn.Write([]byte("221 bye\r\n"))
						return
					}
				}
			}()
		}
	}()
	return listener.Addr().String()
}

func TestTheMailServerCanBeCheckedAndNothingIsSent(t *testing.T) {
	address := miniSMTP(t)
	f, _ := newAlertFixture(t)
	// Whatever host is saved, the checker connects to the mini server, which is on the loopback where a saved host could not be.
	f.srv.SMTPChecker = &alerts.SMTPChecker{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, network, address)
	}}

	rec := f.alertPost(t, "smtp/test", url.Values{})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Save the mail server first")

	require.Equal(t, http.StatusOK, f.saveSMTP(t, func(v url.Values) {
		v.Set("port", "25")
		v.Set("security", "none")
		v.Set("username", "")
		v.Del("password")
	}).Code)
	rec = f.alertPost(t, "smtp/test", url.Values{})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, map[string]any{"ok": true}, decodeBody(t, rec))

	f.srv.SMTPChecker = &alerts.SMTPChecker{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: net.UnknownNetworkError("connection refused")}
	}}
	rec = f.alertPost(t, "smtp/test", url.Values{})
	require.Equal(t, http.StatusOK, rec.Code)
	body := decodeBody(t, rec)
	require.Equal(t, false, body["ok"])
	require.Contains(t, body["error"], "could not connect")
}

func TestSettingsSurviveAReadBackThroughJSON(t *testing.T) {
	// The page reads what it saved: the view must carry every field the form edits.
	f, _ := newAlertFixture(t)
	form := slackForm("#ops")
	form["event"] = []string{"node.unhealthy"}
	form["environment"] = []string{"production"}
	f.saveChannel(t, form)

	_, body := f.alertGet(t)
	channel := body["channels"].([]any)[0].(map[string]any)

	encoded, err := json.Marshal(channel)
	require.NoError(t, err)
	var view alertChannelView
	require.NoError(t, json.Unmarshal(encoded, &view))
	require.Equal(t, []string{"node.unhealthy"}, view.Events)
	require.Equal(t, []string{"production"}, view.Environments)
	require.Equal(t, []string{}, view.Projects)
	require.True(t, view.NotifyResolved)
}
