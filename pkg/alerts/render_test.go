package alerts

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// decodedRoute is a route below the config's own, read back from the JSON the API type holds it as.
type decodedRoute struct {
	Receiver  string                       `json:"receiver"`
	Matchers  []monitoringv1alpha1.Matcher `json:"matchers"`
	Continue  bool                         `json:"continue"`
	GroupBy   []string                     `json:"groupBy"`
	GroupWait string                       `json:"groupWait"`
}

func routesOf(t *testing.T, config *monitoringv1alpha1.AlertmanagerConfig) []decodedRoute {
	t.Helper()
	routes := make([]decodedRoute, 0, len(config.Spec.Route.Routes))
	for _, raw := range config.Spec.Route.Routes {
		var route decodedRoute
		require.NoError(t, json.Unmarshal(raw.Raw, &route))
		routes = append(routes, route)
	}
	return routes
}

// matches is Alertmanager's matching: a label the alert lacks is empty, and a regular expression has to match the whole value.
func matches(matchers []monitoringv1alpha1.Matcher, labels map[string]string) bool {
	for _, matcher := range matchers {
		value := labels[matcher.Name]
		switch matcher.MatchType {
		case monitoringv1alpha1.MatchEqual:
			if value != matcher.Value {
				return false
			}
		case monitoringv1alpha1.MatchRegexp:
			if !regexp.MustCompile("^(?:" + matcher.Value + ")$").MatchString(value) {
				return false
			}
		default:
			panic("a matcher type the routes do not use")
		}
	}
	return true
}

// receiversFor walks the routes the way Alertmanager does: in order, a match is used, and the search goes on only
// when the route says to continue.
func receiversFor(t *testing.T, config *monitoringv1alpha1.AlertmanagerConfig, labels map[string]string) []string {
	t.Helper()
	var receivers []string
	for _, route := range routesOf(t, config) {
		if !matches(route.Matchers, labels) {
			continue
		}
		receivers = append(receivers, route.Receiver)
		if !route.Continue {
			break
		}
	}
	return receivers
}

func slackChannel(id string, mutate ...func(*Channel)) Channel {
	channel := Channel{ID: id, Name: "channel " + id, Type: TypeSlack, SlackURL: "https://hooks.slack.com/services/T/B/" + id, NotifyResolved: true}
	for _, m := range mutate {
		m(&channel)
	}
	return channel
}

// fakeMailPassword is a stand-in for the mail server's password, which no test connects with.
const fakeMailPassword = "not-a-real-password"

// adminsMailSecret is the name of a Secret an admin keeps the mail password in, instead of the one Geass makes.
const adminsMailSecret = "ses-smtp"

func testSMTP() *SMTP {
	return &SMTP{Host: "smtp.example.com", Port: 587, Security: SecuritySTARTTLS, Username: "geass", Password: fakeMailPassword, From: "Geass <alerts@example.com>", PasswordSecret: SMTPSecretName}
}

func problemLabels(kind, project, environment string) map[string]string {
	labels := map[string]string{LabelKind: kind, LabelSeverity: "critical", LabelResource: "web"}
	if project != "" {
		labels[LabelProject] = project
	}
	if environment != "" {
		labels[LabelEnvironment] = environment
	}
	return labels
}

func TestNoChannelThatCanSendIsNoConfig(t *testing.T) {
	require.Nil(t, Render(Config{}))
	require.Nil(t, Render(Config{Channels: []Channel{slackChannel("aaaaaaa1", func(c *Channel) { c.SlackURL = "" })}}),
		"a Slack channel without its URL has nothing to send with")
	require.Nil(t, Render(Config{Channels: []Channel{{ID: "bbbbbbb2", Name: "mail", Type: TypeEmail, Recipients: []string{"a@example.com"}}}}),
		"email without a mail server has nothing to send through")
}

func TestEachChannelHearsExactlyWhatItAskedFor(t *testing.T) {
	config := Config{
		SMTP: testSMTP(),
		Channels: []Channel{
			slackChannel("aaaaaaa1"),
			{ID: "bbbbbbb2", Name: "mail", Type: TypeEmail, Recipients: []string{"ops@example.com"}, Events: []string{"deploy.failed", "service.down"}, Environments: []string{"production"}, NotifyResolved: true},
			slackChannel("ccccccc3", func(c *Channel) { c.Projects = []string{"p1"} }),
			slackChannel("ddddddd4", func(c *Channel) { c.Projects = []string{"other"}; c.Events = []string{"build.failed"} }),
			slackChannel("eeeeeee5", func(c *Channel) { c.Disabled = true }),
		},
	}
	rendered := Render(config)
	require.NotNil(t, rendered)
	live := func(id string) string { return receiverName(id) }

	cases := []struct {
		name   string
		labels map[string]string
		want   []string
	}{
		{"a deploy failure in production reaches the channels that want it",
			problemLabels("deploy.failed", "p1", "production"), []string{live("aaaaaaa1"), live("bbbbbbb2"), live("ccccccc3")}},
		{"the same in staging is left out of the production channel",
			problemLabels("deploy.failed", "p1", "staging"), []string{live("aaaaaaa1"), live("ccccccc3")}},
		{"a kind the channel did not ask for does not reach it",
			problemLabels("build.failed", "p1", "production"), []string{live("aaaaaaa1"), live("ccccccc3")}},
		{"a project's alert reaches the channel for that project and not another's",
			problemLabels("build.failed", "other", "production"), []string{live("aaaaaaa1"), live("ddddddd4")}},
		{"an alert about the cluster reaches the channels not limited to projects",
			problemLabels("node.unhealthy", "", ""), []string{live("aaaaaaa1")}},
		{"a channel for production still hears a cluster alert, which has no environment",
			problemLabels("deploy.failed", "", ""), []string{live("aaaaaaa1"), live("bbbbbbb2")}},
		{"what the cluster's own rules make is not Geass's alert",
			map[string]string{"alertname": "KubeCPUOvercommit", LabelSeverity: "warning"}, nil},
		{"a test goes to its channel and stops there",
			map[string]string{LabelKind: KindTest, LabelTestChannel: "ccccccc3"}, []string{testReceiverName("ccccccc3")}},
		{"a test can go to a paused channel",
			map[string]string{LabelKind: KindTest, LabelTestChannel: "eeeeeee5"}, []string{testReceiverName("eeeeeee5")}},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, receiversFor(t, rendered, tc.labels), tc.name)
	}
}

func TestAPausedChannelHasOnlyAReceiverForTests(t *testing.T) {
	rendered := Render(Config{Channels: []Channel{slackChannel("aaaaaaa1", func(c *Channel) { c.Disabled = true })}})

	names := make([]string, 0, len(rendered.Spec.Receivers))
	for _, receiver := range rendered.Spec.Receivers {
		names = append(names, receiver.Name)
	}
	require.ElementsMatch(t, []string{defaultReceiver, testReceiverName("aaaaaaa1")}, names)
	require.Empty(t, routesOf(t, rendered)[1:], "and no route for real alerts")
}

func TestTestRoutesComeFirstSoTheyStopTheSearch(t *testing.T) {
	rendered := Render(Config{Channels: []Channel{slackChannel("aaaaaaa1"), slackChannel("bbbbbbb2")}})

	routes := routesOf(t, rendered)
	require.Len(t, routes, 4)
	for _, route := range routes[:2] {
		require.True(t, strings.HasPrefix(route.Receiver, "test-"))
		require.False(t, route.Continue, "a test that went on would reach every channel that has no filter")
		require.Equal(t, "0s", route.GroupWait, "a person is waiting for it")
		require.Equal(t, []string{LabelTestChannel, LabelTestID}, route.GroupBy,
			"every test is a group of its own: one that joins a group that is already there waits for the group's next interval")
	}
	for _, route := range routes[2:] {
		require.True(t, strings.HasPrefix(route.Receiver, "ch-"))
		require.True(t, route.Continue, "every channel hears what it asked for, whatever the others do")
	}
}

func TestSecretsAreReferredToAndNeverCopied(t *testing.T) {
	rendered := Render(Config{
		SMTP: testSMTP(),
		Channels: []Channel{
			slackChannel("aaaaaaa1"),
			{ID: "bbbbbbb2", Name: "mail", Type: TypeEmail, Recipients: []string{"a@example.com", "b@example.com"}, NotifyResolved: false},
		},
	})
	byName := map[string]monitoringv1alpha1.Receiver{}
	for _, receiver := range rendered.Spec.Receivers {
		byName[receiver.Name] = receiver
	}

	slack := byName[receiverName("aaaaaaa1")].SlackConfigs[0]
	require.Equal(t, &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: ChannelsSecretName}, Key: "aaaaaaa1"}, slack.APIURL)
	require.True(t, *slack.SendResolved)
	require.False(t, *byName[testReceiverName("aaaaaaa1")].SlackConfigs[0].SendResolved, "a test is not announced as resolved when it ends")

	email := byName[receiverName("bbbbbbb2")].EmailConfigs[0]
	require.Equal(t, "a@example.com, b@example.com", email.To)
	require.Equal(t, "Geass <alerts@example.com>", email.From)
	require.Equal(t, "smtp.example.com:587", email.Smarthost)
	require.Equal(t, "geass", email.AuthUsername)
	require.Equal(t, &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: SMTPSecretName}, Key: SMTPPasswordKey}, email.AuthPassword)
	require.True(t, *email.RequireTLS)
	require.False(t, *email.SendResolved, "the channel asked not to hear when problems end")
	require.Contains(t, email.Headers, monitoringv1alpha1.KeyValue{Key: "Auto-Submitted", Value: "auto-generated"})

	encoded, err := json.Marshal(rendered)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), fakeMailPassword, "the mail password is in a Secret and nowhere else")
	require.NotContains(t, string(encoded), "hooks.slack.com", "so is the Slack URL")
}

func TestAPasswordKeptInASecretOfTheAdminsOwnIsReadFromThere(t *testing.T) {
	smtp := testSMTP()
	smtp.PasswordSecret = adminsMailSecret
	rendered := Render(Config{SMTP: smtp, Channels: []Channel{{ID: "bbbbbbb2", Name: "mail", Type: TypeEmail, Recipients: []string{"a@example.com"}, NotifyResolved: true}}})

	for _, receiver := range rendered.Spec.Receivers {
		for _, email := range receiver.EmailConfigs {
			require.Equal(t, adminsMailSecret, email.AuthPassword.Name)
		}
	}
}

func TestAChannelWithoutWhatItNeedsIsLeftOutSoItCannotBreakTheOthers(t *testing.T) {
	// The Prometheus operator drops a whole AlertmanagerConfig that refers to a Secret key it cannot find.
	config := Config{
		SMTP: &SMTP{Host: "smtp.example.com", Port: 587, Security: SecuritySTARTTLS, Username: "geass", From: "alerts@example.com"}, // no password saved
		Channels: []Channel{
			slackChannel("aaaaaaa1"),
			slackChannel("bbbbbbb2", func(c *Channel) { c.SlackURL = "" }),
			{ID: "ccccccc3", Name: "mail", Type: TypeEmail, Recipients: []string{"a@example.com"}, NotifyResolved: true},
			{ID: "ddddddd4", Name: "mail without addresses", Type: TypeEmail},
		},
	}
	rendered := Render(config)

	names := make([]string, 0, len(rendered.Spec.Receivers))
	for _, receiver := range rendered.Spec.Receivers {
		names = append(names, receiver.Name)
	}
	require.ElementsMatch(t, []string{defaultReceiver, receiverName("aaaaaaa1"), testReceiverName("aaaaaaa1")}, names)
}

func TestNoTextAPersonTypedIsWrittenIntoATemplate(t *testing.T) {
	// Alertmanager runs these as templates, so a channel called {{ ... }} would be code.
	rendered := Render(Config{
		DashboardURL: "https://geass.example.com",
		SMTP:         testSMTP(),
		Channels: []Channel{
			slackChannel("aaaaaaa1", func(c *Channel) { c.Name = "{{ .ExternalURL }} EVILNAME" }),
			{ID: "bbbbbbb2", Name: "{{ define \"x\" }} EVILMAIL", Type: TypeEmail, Recipients: []string{"a@example.com"}, NotifyResolved: true},
		},
	})

	encoded, err := json.Marshal(rendered)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "EVILNAME")
	require.NotContains(t, string(encoded), "EVILMAIL")
}

func TestLinksToTheDashboardAppearOnlyWhenItsAddressIsKnownAndSafe(t *testing.T) {
	render := func(url string) string {
		rendered := Render(Config{DashboardURL: url, SMTP: testSMTP(), Channels: []Channel{
			slackChannel("aaaaaaa1"),
			{ID: "bbbbbbb2", Name: "mail", Type: TypeEmail, Recipients: []string{"a@example.com"}, NotifyResolved: true},
		}})
		encoded, err := json.Marshal(rendered)
		require.NoError(t, err)
		return string(encoded)
	}
	require.Contains(t, render("https://geass.example.com/"), `https://geass.example.com{{ .Labels.path }}`, "the trailing slash is dropped")
	require.NotContains(t, render(""), ".Labels.path")
	require.NotContains(t, render(`https://x.example.com/{{ .Evil }}`), ".Labels.path", "an address with template syntax in it is not used")
}

func TestUnencryptedMailDoesNotRequireTLS(t *testing.T) {
	smtp := &SMTP{Host: "mail.internal", Port: 25, Security: SecurityNone, From: "alerts@example.com"}
	rendered := Render(Config{SMTP: smtp, Channels: []Channel{{ID: "bbbbbbb2", Name: "mail", Type: TypeEmail, Recipients: []string{"a@example.com"}, NotifyResolved: true}}})

	for _, receiver := range rendered.Spec.Receivers {
		for _, email := range receiver.EmailConfigs {
			require.False(t, *email.RequireTLS)
			require.Empty(t, email.AuthUsername)
			require.Nil(t, email.AuthPassword)
		}
	}
}

func TestUntestableSaysWhyAChannelCannotBeTried(t *testing.T) {
	config := Config{
		Channels: []Channel{
			slackChannel("aaaaaaa1"),
			slackChannel("bbbbbbb2", func(c *Channel) { c.SlackURL = "" }),
			{ID: "ccccccc3", Name: "mail", Type: TypeEmail, Recipients: []string{"a@example.com"}},
		},
	}
	require.Empty(t, Untestable(config, "aaaaaaa1"))
	require.Contains(t, Untestable(config, "bbbbbbb2"), "No Slack webhook URL")
	require.Equal(t, "Set up the mail server first.", Untestable(config, "ccccccc3"))
	require.Equal(t, "That channel was not found.", Untestable(config, "zzzzzzz9"))

	config.SMTP = &SMTP{Host: "smtp.example.com", Username: "geass"}
	require.Contains(t, Untestable(config, "ccccccc3"), "no password")
	config.SMTP = testSMTP()
	require.Empty(t, Untestable(config, "ccccccc3"))
	config.Channels[2].Recipients = nil
	require.Equal(t, "This channel has no email addresses.", Untestable(config, "ccccccc3"))
}

// What a test says a channel cannot do and what Render leaves out are one set of checks, so they cannot part.
func TestAChannelIsTestableExactlyWhenItHasAReceiver(t *testing.T) {
	smtp := func(change func(*SMTP)) *SMTP {
		server := testSMTP()
		if change != nil {
			change(server)
		}
		return server
	}
	mail := func(recipients ...string) Channel {
		return Channel{ID: "ccccccc3", Name: "mail", Type: TypeEmail, Recipients: recipients, NotifyResolved: true}
	}
	for name, config := range map[string]Config{
		"slack":                      {Channels: []Channel{slackChannel("ccccccc3")}},
		"slack without a URL":        {Channels: []Channel{slackChannel("ccccccc3", func(c *Channel) { c.SlackURL = "" })}},
		"unknown type":               {Channels: []Channel{{ID: "ccccccc3", Name: "x", Type: "pager", NotifyResolved: true}}},
		"mail":                       {SMTP: smtp(nil), Channels: []Channel{mail("a@example.com")}},
		"mail without server":        {Channels: []Channel{mail("a@example.com")}},
		"mail without host":          {SMTP: smtp(func(s *SMTP) { s.Host = "" }), Channels: []Channel{mail("a@example.com")}},
		"mail without addresses":     {SMTP: smtp(nil), Channels: []Channel{mail()}},
		"mail without the password":  {SMTP: smtp(func(s *SMTP) { s.Password = "" }), Channels: []Channel{mail("a@example.com")}},
		"mail without a secret name": {SMTP: smtp(func(s *SMTP) { s.PasswordSecret = "" }), Channels: []Channel{mail("a@example.com")}},
		"mail needing no login":      {SMTP: smtp(func(s *SMTP) { s.Username, s.Password, s.Security = "", "", SecurityNone }), Channels: []Channel{mail("a@example.com")}},
	} {
		t.Run(name, func(t *testing.T) {
			rendered := Render(config)
			hasReceiver := false
			if rendered != nil {
				for _, receiver := range rendered.Spec.Receivers {
					hasReceiver = hasReceiver || receiver.Name == testReceiverName("ccccccc3")
				}
			}
			require.Equal(t, hasReceiver, Untestable(config, "ccccccc3") == "", "untestable says %q", Untestable(config, "ccccccc3"))
		})
	}
}

// The host of the address was checked when it was saved. A host that answered with a redirect would send Alertmanager,
// which connects from inside the cluster, wherever it named.
func TestSlackIsNotFollowedThroughRedirects(t *testing.T) {
	rendered := Render(Config{Channels: []Channel{slackChannel("aaaaaaa1")}})
	require.NotNil(t, rendered)
	for _, receiver := range rendered.Spec.Receivers {
		for _, slack := range receiver.SlackConfigs {
			require.NotNil(t, slack.HTTPConfig, receiver.Name)
			require.NotNil(t, slack.HTTPConfig.FollowRedirects, receiver.Name)
			require.False(t, *slack.HTTPConfig.FollowRedirects, receiver.Name)
		}
	}
}

func TestReceiverNamesAreTheOnesTheOperatorGivesThem(t *testing.T) {
	require.Equal(t, "geass-system/geass-alerts/ch-aaaaaaa1", ReceiverName("geass-system", "aaaaaaa1", false))
	require.Equal(t, "geass-system/geass-alerts/test-aaaaaaa1", ReceiverName("geass-system", "aaaaaaa1", true))
}

// ---- Load and Sync, against a fake cluster ----

func alertingClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, monitoringv1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func platformWith(spec *geassv1alpha1.GeassAlertingSpec) *geassv1alpha1.GeassPlatformConfig {
	return &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassPlatformConfigSpec{DashboardURL: "https://geass.example.com", Alerting: spec},
	}
}

func secret(name string, data map[string]string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace}, StringData: data, Data: toBytes(data)}
}

func toBytes(in map[string]string) map[string][]byte {
	out := map[string][]byte{}
	for key, value := range in {
		out[key] = []byte(value)
	}
	return out
}

func TestLoadReadsTheSettingsAndTheSecretsTheyNameAndNothingElse(t *testing.T) {
	no := false
	c := alertingClient(t,
		platformWith(&geassv1alpha1.GeassAlertingSpec{
			SMTP: &geassv1alpha1.GeassAlertSMTP{Host: "smtp.example.com", Username: "geass", CredentialRef: &corev1.LocalObjectReference{Name: adminsMailSecret}, From: "alerts@example.com"},
			Channels: []geassv1alpha1.GeassAlertChannel{
				{ID: "aaaaaaa1", Name: "slack", Type: "slack"},
				{ID: "bbbbbbb2", Name: "mail", Type: "email", Recipients: []string{"a@example.com"}, NotifyResolved: &no, Disabled: true},
			},
		}),
		secret(adminsMailSecret, map[string]string{SMTPPasswordKey: fakeMailPassword}),
		secret(ChannelsSecretName, map[string]string{"aaaaaaa1": " https://hooks.slack.com/services/T/B/X \n"}),
	)

	config, err := Load(context.Background(), c, platform.SystemNamespace)

	require.NoError(t, err)
	require.Equal(t, "https://geass.example.com", config.DashboardURL)
	require.Equal(t, &SMTP{Host: "smtp.example.com", Port: 587, Security: SecuritySTARTTLS, Username: "geass", Password: fakeMailPassword, From: "alerts@example.com", PasswordSecret: adminsMailSecret}, config.SMTP,
		"the defaults are 587 and STARTTLS, and the password is read from the Secret the settings name")
	require.Len(t, config.Channels, 2)
	require.Equal(t, "https://hooks.slack.com/services/T/B/X", config.Channels[0].SlackURL, "whitespace around a pasted URL is dropped")
	require.True(t, config.Channels[0].NotifyResolved, "unset means yes")
	require.False(t, config.Channels[1].NotifyResolved)
	require.True(t, config.Channels[1].Disabled)
	require.True(t, config.Active(), "the first channel is on")
}

func TestLoadOfNothingConfiguredIsAnEmptyConfig(t *testing.T) {
	config, err := Load(context.Background(), alertingClient(t), platform.SystemNamespace)
	require.NoError(t, err)
	require.False(t, config.Active())

	config, err = Load(context.Background(), alertingClient(t, platformWith(nil)), platform.SystemNamespace)
	require.NoError(t, err)
	require.Empty(t, config.Channels)
	require.Equal(t, "https://geass.example.com", config.DashboardURL)
}

func TestSyncMakesUpdatesAndRemovesTheAlertmanagerConfig(t *testing.T) {
	ctx := context.Background()
	c := alertingClient(t, platformWith(&geassv1alpha1.GeassAlertingSpec{
		Channels: []geassv1alpha1.GeassAlertChannel{{ID: "aaaaaaa1", Name: "slack", Type: "slack"}},
	}), secret(ChannelsSecretName, map[string]string{"aaaaaaa1": "https://hooks.slack.com/services/T/B/X"}))
	key := client.ObjectKey{Name: AlertmanagerConfigName, Namespace: platform.SystemNamespace}
	get := func() (*monitoringv1alpha1.AlertmanagerConfig, error) {
		var got monitoringv1alpha1.AlertmanagerConfig
		err := c.Get(ctx, key, &got)
		return &got, err
	}

	require.NoError(t, Sync(ctx, c, platform.SystemNamespace))
	created, err := get()
	require.NoError(t, err)
	require.Equal(t, platform.ManagedByValue, created.Labels[platform.LabelManagedBy])
	require.Len(t, created.OwnerReferences, 1, "it goes when the platform configuration does")
	require.Equal(t, "GeassPlatformConfig", created.OwnerReferences[0].Kind)
	require.Len(t, created.Spec.Receivers, 3)

	// Another channel arrives, and what is there is changed, not made again.
	var platformConfig geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &platformConfig))
	platformConfig.Spec.Alerting.Channels = append(platformConfig.Spec.Alerting.Channels, geassv1alpha1.GeassAlertChannel{ID: "bbbbbbb2", Name: "second", Type: "slack"})
	require.NoError(t, c.Update(ctx, &platformConfig))
	require.NoError(t, c.Update(ctx, secret(ChannelsSecretName, map[string]string{"aaaaaaa1": "https://hooks.slack.com/services/T/B/X", "bbbbbbb2": "https://hooks.slack.com/services/T/B/Y"})))
	require.NoError(t, Sync(ctx, c, platform.SystemNamespace))
	updated, err := get()
	require.NoError(t, err)
	require.Len(t, updated.Spec.Receivers, 5)

	// Every channel removed, and the object goes.
	platformConfig.Spec.Alerting.Channels = nil
	require.NoError(t, c.Update(ctx, &platformConfig))
	require.NoError(t, Sync(ctx, c, platform.SystemNamespace))
	_, err = get()
	require.True(t, client.IgnoreNotFound(err) == nil && err != nil, "the config is gone: %v", err)
	require.NoError(t, Sync(ctx, c, platform.SystemNamespace), "and removing what is not there is fine")
}

func TestKindsAreInTheOrderTheDashboardShowsThem(t *testing.T) {
	ids := make([]string, 0, len(Kinds))
	for _, kind := range Kinds {
		ids = append(ids, kind.ID)
	}
	require.True(t, slices.Contains(ids, geassv1alpha1.AlertWorkloadCrashLooping))
	require.True(t, slices.Contains(ids, geassv1alpha1.AlertStorageFilling))
	require.Equal(t, geassv1alpha1.AlertBuildFailed, ids[0])
}

func orgProjectObject(name, org string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Org: org},
	}
}

func TestAnOrganizationsChannelHearsOnlyAboutItsOwnProjectsAndNeverTheClusterOrAnotherOrganization(t *testing.T) {
	config := Config{Channels: []Channel{
		slackChannel("aaaaaaa1"), // the platform's own: every project and the cluster
		slackChannel("bbbbbbb2", func(c *Channel) { c.Org = "org-aaaaaaaa"; c.Scope = []string{"acme-web", "acme-api"} }),
		slackChannel("ccccccc3", func(c *Channel) { c.Org = "org-bbbbbbbb"; c.Scope = []string{"beta-app"} }),
		slackChannel("ddddddd4", func(c *Channel) { c.Org = "org-cccccccc"; c.Scope = nil }),
	}}
	rendered := Render(config)
	require.NotNil(t, rendered)
	live := func(id string) string { return receiverName(id) }

	for name, tc := range map[string]struct {
		labels map[string]string
		want   []string
	}{
		"an alert about one of an organization's projects reaches it and the platform's":              {problemLabels("deploy.failed", "acme-web", "production"), []string{live("aaaaaaa1"), live("bbbbbbb2")}},
		"another organization's project reaches that organization only, and the platform":             {problemLabels("deploy.failed", "beta-app", "production"), []string{live("aaaaaaa1"), live("ccccccc3")}},
		"a project nobody's channel names reaches only the platform":                                  {problemLabels("deploy.failed", "orphan", "production"), []string{live("aaaaaaa1")}},
		"an alert about the cluster reaches no organization":                                          {problemLabels("node.unhealthy", "", ""), []string{live("aaaaaaa1")}},
		"a cluster alert with an environment still reaches no organization":                           {problemLabels("deploy.failed", "", "production"), []string{live("aaaaaaa1")}},
		"an organization with no projects hears nothing, not everything (it is not in any list here)": {problemLabels("deploy.failed", "acme-web", ""), []string{live("aaaaaaa1"), live("bbbbbbb2")}},
		"a project whose name only starts like one of the organization's is not its":                  {problemLabels("deploy.failed", "acme-web-2", "production"), []string{live("aaaaaaa1")}},
		"a project name with regular expression characters matches only itself":                       {problemLabels("deploy.failed", "acme.web", "production"), []string{live("aaaaaaa1")}},
	} {
		require.Equal(t, tc.want, receiversFor(t, rendered, tc.labels), name)
	}
}

func TestAnOrganizationsChannelCannotSendMailThroughThePlatform(t *testing.T) {
	config := Config{SMTP: testSMTP(), Channels: []Channel{
		{ID: "aaaaaaa1", Name: "mail", Type: TypeEmail, Recipients: []string{"a@example.com"}, Org: "org-aaaaaaaa", Scope: []string{"p"}, NotifyResolved: true},
	}}
	require.Nil(t, Render(config), "there is no receiver for it, so nothing is sent")
	require.Contains(t, Untestable(config, "aaaaaaa1"), "Slack")
}

func TestLoadRoutesAnOrganizationsChannelOnTheProjectsItHasNow(t *testing.T) {
	c := alertingClient(t,
		platformWith(&geassv1alpha1.GeassAlertingSpec{Channels: []geassv1alpha1.GeassAlertChannel{
			{ID: "aaaaaaa1", Name: "all of acme", Type: "slack", Org: "org-aaaaaaaa"},
			{ID: "bbbbbbb2", Name: "acme web", Type: "slack", Org: "org-aaaaaaaa", Projects: []string{"acme-web", "moved-away", "beta-app"}},
			{ID: "ccccccc3", Name: "platform", Type: "slack"},
		}}),
		secret(ChannelsSecretName, map[string]string{"aaaaaaa1": "https://hooks.slack.com/services/T/B/1", "bbbbbbb2": "https://hooks.slack.com/services/T/B/2", "ccccccc3": "https://hooks.slack.com/services/T/B/3"}),
		orgProjectObject("acme-web", "org-aaaaaaaa"), orgProjectObject("acme-api", "org-aaaaaaaa"),
		orgProjectObject("beta-app", "org-bbbbbbbb"), orgProjectObject("moved-away", "org-bbbbbbbb"), orgProjectObject("loner", ""),
	)
	config, err := Load(context.Background(), c, platform.SystemNamespace)
	require.NoError(t, err)

	byID := map[string]Channel{}
	for _, channel := range config.Channels {
		byID[channel.ID] = channel
	}
	require.ElementsMatch(t, []string{"acme-web", "acme-api"}, byID["aaaaaaa1"].Scope, "all the organization has, and not another's")
	require.Equal(t, []string{"acme-web"}, byID["bbbbbbb2"].Scope, "the ones it names that are still the organization's: a project given to another organization is gone from it")
	require.Empty(t, byID["ccccccc3"].Org, "the platform's channel is untouched")
	require.Empty(t, byID["ccccccc3"].Scope)
}
