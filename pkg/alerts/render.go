package alerts

import (
	"context"
	"encoding/json"
	"net"
	"regexp"
	"strconv"
	"strings"

	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// How alerts are grouped and how often they are sent again. Alerts of one kind in one project and environment go
// out as one message after half a minute, so that a node that takes ten services with it is one message and not
// ten. A change to a group is sent after five minutes at the soonest, and a problem that is still there is
// mentioned again every twelve hours.
const (
	groupWait      = "30s"
	groupInterval  = "5m"
	repeatInterval = "12h"
	// defaultReceiver takes what no channel wants. It has no integrations, so it discards.
	defaultReceiver = "geass-default"
)

func receiverName(id string) string     { return "ch-" + id }
func testReceiverName(id string) string { return "test-" + id }

// ReceiverName is the name Alertmanager gives a channel's receiver once the Prometheus operator has put the
// AlertmanagerConfig in its configuration. The test alert's receiver is the one for tests.
func ReceiverName(namespace, id string, test bool) string {
	name := receiverName(id)
	if test {
		name = testReceiverName(id)
	}
	return namespace + "/" + AlertmanagerConfigName + "/" + name
}

// route is a route below the config's own. The API type holds them as raw JSON, because a route can contain routes.
type route struct {
	Receiver  string                       `json:"receiver"`
	Matchers  []monitoringv1alpha1.Matcher `json:"matchers,omitempty"`
	Continue  bool                         `json:"continue,omitempty"`
	GroupBy   []string                     `json:"groupBy,omitempty"`
	GroupWait string                       `json:"groupWait,omitempty"`
}

func raw(r route) apiextensionsv1.JSON {
	out, _ := json.Marshal(r)
	return apiextensionsv1.JSON{Raw: out}
}

func regexpOf(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, regexp.QuoteMeta(value))
	}
	return strings.Join(quoted, "|")
}

// matchers are what an alert has to carry to reach a channel. Alertmanager treats a label an alert lacks as
// empty, and anchors every regular expression, which is what gives the filters their meaning:
//
//   - Every alert Geass makes has a kind, so ".+" matches them all and nothing the cluster's own rules make.
//   - An alert about the cluster has no project, so a project filter, which needs one of the names, leaves it out.
//     That is the rule that a channel for one team's project does not hear about the cluster's nodes.
//   - An environment filter is written with an empty alternative, so an alert with no environment passes it:
//     a channel for production still hears that a node went down.
func matchers(channel Channel) []monitoringv1alpha1.Matcher {
	kind := ".+"
	if len(channel.Events) > 0 {
		kind = regexpOf(channel.Events)
	}
	out := []monitoringv1alpha1.Matcher{{Name: LabelKind, Value: kind, MatchType: monitoringv1alpha1.MatchRegexp}}
	if len(channel.Projects) > 0 {
		out = append(out, monitoringv1alpha1.Matcher{Name: LabelProject, Value: regexpOf(channel.Projects), MatchType: monitoringv1alpha1.MatchRegexp})
	}
	if len(channel.Environments) > 0 {
		out = append(out, monitoringv1alpha1.Matcher{Name: LabelEnvironment, Value: "(" + regexpOf(channel.Environments) + ")?", MatchType: monitoringv1alpha1.MatchRegexp})
	}
	return out
}

// baseOf is the dashboard's address for links, or "" when it has none or one that is not safe to put in a template.
func baseOf(dashboardURL string) string {
	if strings.ContainsAny(dashboardURL, "{}\"'<> ") {
		return ""
	}
	return strings.TrimRight(dashboardURL, "/")
}

// The messages. Everything they say comes from the alert: its labels (a name, a project, a path) and the annotations
// the rules give it. Nothing a person typed is written into one, because Alertmanager runs them as templates.

func slackTitle() string {
	return `{{ if eq .Status "firing" }}{{ if eq .CommonLabels.severity "critical" }}🔴{{ else }}🟠{{ end }}{{ else }}✅ Resolved:{{ end }} {{ .CommonAnnotations.title }}{{ if gt (len .Alerts) 1 }} ({{ len .Alerts }}){{ end }}`
}

func slackColor() string {
	return `{{ if eq .Status "resolved" }}#2eb67d{{ else if eq .CommonLabels.geass_kind "test" }}#8a8f98{{ else if eq .CommonLabels.severity "critical" }}#d93f3f{{ else }}#e8a33d{{ end }}`
}

func slackText() string {
	return `{{ range .Alerts.Firing }}• {{ .Annotations.summary }}
{{ end }}{{ range .Alerts.Resolved }}• {{ .Annotations.summary }} (resolved)
{{ end }}{{ with .CommonAnnotations.description }}
{{ . }}{{ end }}`
}

func slackLink(base string) string {
	if base == "" {
		return ""
	}
	return `{{ with index .Alerts 0 }}{{ if .Labels.path }}` + base + `{{ .Labels.path }}{{ end }}{{ end }}`
}

func emailSubject() string {
	return `[Geass] {{ if eq .Status "resolved" }}Resolved: {{ end }}{{ .CommonAnnotations.title }}{{ if gt (len .Alerts) 1 }} ({{ len .Alerts }} alerts){{ end }}`
}

func emailText(base string) string {
	link := ""
	if base != "" {
		link = `{{ if .Labels.path }}  ` + base + `{{ .Labels.path }}
{{ end }}`
	}
	return `{{ .CommonAnnotations.title }}

{{ range .Alerts.Firing }}• {{ .Annotations.summary }}
` + link + `{{ end }}{{ range .Alerts.Resolved }}• {{ .Annotations.summary }} (resolved)
{{ end }}{{ with .CommonAnnotations.description }}
{{ . }}
{{ end }}
--
You are getting this because a channel in Geass is set up to send alerts here. Change it under Settings, Alerts.
`
}

func emailHTML(base string) string {
	link := ""
	if base != "" {
		link = `{{ if .Labels.path }} <a href="` + base + `{{ .Labels.path }}">Open in Geass</a>{{ end }}`
	}
	return `<!doctype html><html><body style="margin:0;padding:16px;background:#f5f6f7;font-family:-apple-system,Segoe UI,Helvetica,Arial,sans-serif;color:#1d1f23">` +
		`<div style="max-width:560px;background:#fff;border-left:4px solid {{ if eq .Status "resolved" }}#2eb67d{{ else if eq .CommonLabels.severity "critical" }}#d93f3f{{ else }}#e8a33d{{ end }};border-radius:4px;padding:14px 16px">` +
		`<div style="font-size:16px;font-weight:600;margin-bottom:8px">{{ .CommonAnnotations.title }}</div>` +
		`<ul style="font-size:13px;padding-left:18px;margin:0">` +
		`{{ range .Alerts.Firing }}<li>{{ .Annotations.summary }}` + link + `</li>{{ end }}` +
		`{{ range .Alerts.Resolved }}<li>{{ .Annotations.summary }} (resolved)</li>{{ end }}` +
		`</ul>{{ with .CommonAnnotations.description }}<p style="font-size:13px;margin:10px 0 0">{{ . }}</p>{{ end }}</div>` +
		`<p style="font-size:12px;color:#6b7078;max-width:560px">You are getting this because a channel in Geass is set up to send alerts here. Change it under Settings, Alerts.</p>` +
		`</body></html>`
}

// A receiver that cannot be made says why, so that what a test says and what Render leaves out are one set of checks.
const (
	reasonNoSlackURL     = "No Slack webhook URL is saved for this channel."
	reasonNoMailServer   = "Set up the mail server first."
	reasonNoRecipients   = "This channel has no email addresses."
	reasonNoMailPassword = "The mail server has a username but no password is saved for it."
	reasonUnknownType    = "This channel is neither Slack nor email."
)

// buildReceiver makes the receiver of a channel, or says why it cannot be made.
func buildReceiver(config Config, channel Channel, name, base string, sendResolved bool) (monitoringv1alpha1.Receiver, string) {
	switch channel.Type {
	case TypeSlack:
		return slackReceiver(name, channel, base, sendResolved)
	case TypeEmail:
		return emailReceiver(name, channel, config.SMTP, base, sendResolved)
	}
	return monitoringv1alpha1.Receiver{}, reasonUnknownType
}

// slackReceiver is a receiver for a Slack channel. It is left out when the channel has no webhook URL saved: the
// Prometheus operator drops a whole AlertmanagerConfig that refers to a Secret key it cannot find, and one channel
// without its secret must not silence the others.
func slackReceiver(name string, channel Channel, base string, sendResolved bool) (monitoringv1alpha1.Receiver, string) {
	if channel.SlackURL == "" {
		return monitoringv1alpha1.Receiver{}, reasonNoSlackURL
	}
	return monitoringv1alpha1.Receiver{Name: name, SlackConfigs: []monitoringv1alpha1.SlackConfig{{
		APIURL: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: ChannelsSecretName}, Key: channel.ID,
		},
		// The host was checked when the address was saved, and that is all a person who types an address is held to:
		// a host that answered with a redirect would send Alertmanager, from inside the cluster, to wherever it names.
		HTTPConfig:   &monitoringv1alpha1.HTTPConfig{FollowRedirects: ptr.To(false)},
		SendResolved: ptr.To(sendResolved),
		Color:        slackColor(),
		Title:        slackTitle(),
		TitleLink:    slackLink(base),
		Text:         slackText(),
		Fallback:     `{{ .CommonAnnotations.title }}`,
		Footer:       "Geass",
	}}}, ""
}

// emailReceiver is a receiver for an email channel. It is left out when there is nothing to send through, for the
// reason a Slack receiver without its URL is.
func emailReceiver(name string, channel Channel, smtp *SMTP, base string, sendResolved bool) (monitoringv1alpha1.Receiver, string) {
	switch {
	case smtp == nil || smtp.Host == "":
		return monitoringv1alpha1.Receiver{}, reasonNoMailServer
	case len(channel.Recipients) == 0:
		return monitoringv1alpha1.Receiver{}, reasonNoRecipients
	case smtp.Username != "" && (smtp.Password == "" || smtp.PasswordSecret == ""):
		return monitoringv1alpha1.Receiver{}, reasonNoMailPassword
	}
	config := monitoringv1alpha1.EmailConfig{
		SendResolved: ptr.To(sendResolved),
		To:           strings.Join(channel.Recipients, ", "),
		From:         smtp.From,
		Smarthost:    net.JoinHostPort(smtp.Host, strconv.Itoa(smtp.Port)),
		RequireTLS:   ptr.To(smtp.Security != SecurityNone),
		Headers: []monitoringv1alpha1.KeyValue{
			{Key: "Subject", Value: emailSubject()},
			// Marks the message as sent by a machine, so that an out-of-office reply is not sent back to it.
			{Key: "Auto-Submitted", Value: "auto-generated"},
		},
		HTML: ptr.To(emailHTML(base)),
		Text: ptr.To(emailText(base)),
	}
	if smtp.Username != "" {
		config.AuthUsername = smtp.Username
		config.AuthPassword = &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: smtp.PasswordSecret}, Key: SMTPPasswordKey,
		}
	}
	return monitoringv1alpha1.Receiver{Name: name, EmailConfigs: []monitoringv1alpha1.EmailConfig{config}}, ""
}

// Render turns the channels into an AlertmanagerConfig, or nil when there is no channel that can send.
//
// Each channel has a receiver and a route. Routes are tried in order and several can take one alert, so that a
// channel hears what it asked for whatever the others do. A test alert names the one channel it is for, and its
// routes come first and stop the search, so it reaches that channel and no other. Test routes are made for paused
// channels too, so that a channel can be tried before it is turned on.
func Render(config Config) *monitoringv1alpha1.AlertmanagerConfig {
	base := baseOf(config.DashboardURL)
	var receivers []monitoringv1alpha1.Receiver
	var tests, channels []apiextensionsv1.JSON
	for _, channel := range config.Channels {
		build := func(name string, sendResolved bool) (monitoringv1alpha1.Receiver, string) {
			return buildReceiver(config, channel, name, base, sendResolved)
		}
		test, reason := build(testReceiverName(channel.ID), false)
		if reason != "" {
			continue
		}
		receivers = append(receivers, test)
		tests = append(tests, raw(route{
			Receiver: test.Name,
			Matchers: []monitoringv1alpha1.Matcher{{Name: LabelTestChannel, Value: channel.ID, MatchType: monitoringv1alpha1.MatchEqual}},
			// Every test is a group of its own, so that it is sent at once whatever was tested before it.
			GroupBy:   []string{LabelTestChannel, LabelTestID},
			GroupWait: "0s",
		}))
		if channel.Disabled {
			continue
		}
		live, _ := build(receiverName(channel.ID), channel.NotifyResolved)
		receivers = append(receivers, live)
		channels = append(channels, raw(route{Receiver: live.Name, Matchers: matchers(channel), Continue: true}))
	}
	if len(receivers) == 0 {
		return nil
	}
	return &monitoringv1alpha1.AlertmanagerConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      AlertmanagerConfigName,
			Namespace: platform.SystemNamespace,
			Labels:    map[string]string{platform.LabelManagedBy: platform.ManagedByValue},
		},
		Spec: monitoringv1alpha1.AlertmanagerConfigSpec{
			Route: &monitoringv1alpha1.Route{
				Receiver:       defaultReceiver,
				GroupBy:        []string{LabelKind, LabelProject, LabelEnvironment},
				GroupWait:      groupWait,
				GroupInterval:  groupInterval,
				RepeatInterval: repeatInterval,
				Routes:         append(tests, channels...),
			},
			Receivers: append([]monitoringv1alpha1.Receiver{{Name: defaultReceiver}}, receivers...),
		},
	}
}

// Untestable says why a channel cannot be sent a test, or "" when it can: what the channel needs to send is not there, so
// it has no receiver in the AlertmanagerConfig. It is the same check that leaves the receiver out.
func Untestable(config Config, id string) string {
	channel, ok := config.Channel(id)
	if !ok {
		return "That channel was not found."
	}
	_, reason := buildReceiver(config, channel, testReceiverName(id), "", false)
	return reason
}

// Sync makes the cluster's AlertmanagerConfig say what the platform configuration says: it makes or updates the
// object, and removes it when there is nothing to send. The error is a "no match" one when the monitoring add-on, which
// brings the kind, is not installed.
func Sync(ctx context.Context, c client.Client, namespace string) error {
	config, err := Load(ctx, c, namespace)
	if err != nil {
		return err
	}
	desired := Render(config)
	existing := &monitoringv1alpha1.AlertmanagerConfig{ObjectMeta: metav1.ObjectMeta{Name: AlertmanagerConfigName, Namespace: namespace}}
	if desired == nil {
		if err := c.Delete(ctx, existing); err != nil && client.IgnoreNotFound(err) != nil && !meta.IsNoMatchError(err) {
			return err
		}
		return nil
	}
	var owner geassv1alpha1.GeassPlatformConfig
	hasOwner := c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: namespace}, &owner) == nil
	_, err = controllerutil.CreateOrUpdate(ctx, c, existing, func() error {
		existing.Labels = desired.Labels
		existing.Spec = desired.Spec
		if hasOwner {
			return controllerutil.SetControllerReference(&owner, existing, c.Scheme())
		}
		return nil
	})
	return err
}
