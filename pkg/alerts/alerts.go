// Package alerts connects what an admin sets up in the dashboard to the cluster's Alertmanager.
//
// Geass does not send alerts itself. The operator says what is wrong by exporting one gauge for each problem (the
// metric geass_problem), Prometheus rules decide when a problem has lasted long enough to count, and Alertmanager
// groups the alerts and sends them. This package is the last step: it turns the channels in the platform
// configuration into an AlertmanagerConfig, and talks to Alertmanager to show what is firing and to send a test.
package alerts

import (
	"slices"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const (
	// ChannelsSecretName is the Secret of the system namespace that holds the secret part of each channel,
	// under the channel's id: for a Slack channel, its incoming webhook URL.
	ChannelsSecretName = "geass-alert-channels"
	// SMTPSecretName is the Secret the dashboard keeps the mail server's password in.
	SMTPSecretName = "geass-alert-smtp"
	// SMTPPasswordKey is the key of that Secret that holds the password.
	SMTPPasswordKey = "password"
	// AlertmanagerConfigName is the AlertmanagerConfig Geass keeps in the system namespace. It holds a receiver
	// and a route for each channel, and is removed when there are no channels.
	AlertmanagerConfigName = "geass-alerts"

	// TypeSlack is a channel that posts to a Slack incoming webhook, or to anything that takes the same JSON.
	TypeSlack = "slack"
	// TypeEmail is a channel that writes to a list of addresses through the mail server.
	TypeEmail = "email"

	// MaxChannels is how many channels Geass keeps. It matches the limit on the API type.
	MaxChannels = 20
	// MaxRecipients is how many addresses an email channel writes to.
	MaxRecipients = 20
	// DefaultSMTPPort is where STARTTLS is offered.
	DefaultSMTPPort = 587
	// ImplicitTLSPort is the one port Alertmanager connects to encrypted from the first byte.
	ImplicitTLSPort = 465

	// Security says how the connection to the mail server is protected.
	SecuritySTARTTLS = "starttls"
	SecurityTLS      = "tls"
	SecurityNone     = "none"
)

// Severity says how urgent an alert is. It colors a message and nothing more: channels are not routed by it.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
)

// Kind is one of the things Geass can alert about.
type Kind struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Kinds lists them in the order the dashboard shows them.
var Kinds = []Kind{
	{geassv1alpha1.AlertBuildFailed, "Build failed", "A build of a repository service failed."},
	{geassv1alpha1.AlertDeployFailed, "Deploy failed", "A service, function, database or bucket could not be deployed and Geass gave up on it."},
	{geassv1alpha1.AlertServiceDown, "Service down", "A service that should be running has had no ready replica for five minutes."},
	{geassv1alpha1.AlertBackupFailed, "Backups failing", "A database's backups are failing, so recent changes may not be recoverable."},
	{geassv1alpha1.AlertNodeUnhealthy, "Node not ready", "A node of the cluster is not ready, or could not be set up."},
	{geassv1alpha1.AlertWorkloadCrashLooping, "Crash loop", "A pod keeps crashing and being restarted."},
	{geassv1alpha1.AlertStorageFilling, "Storage filling", "A volume or a node's disk is nearly full."},
	{geassv1alpha1.AlertClusterIssue, "Cluster problem", "A cluster upgrade stopped, certificates cannot be issued, network policies are not enforced, or the operator is not doing its work."},
}

// ValidKind reports whether id is a kind a channel can subscribe to.
func ValidKind(id string) bool {
	return slices.ContainsFunc(Kinds, func(kind Kind) bool { return kind.ID == id })
}

// KindLabel is what the dashboard calls a kind.
func KindLabel(id string) string {
	for _, kind := range Kinds {
		if kind.ID == id {
			return kind.Label
		}
	}
	return id
}

// SMTP is the mail server email channels send through, with its password.
type SMTP struct {
	Host     string
	Port     int
	Security string
	Username string
	Password string
	From     string
	// PasswordSecret is the Secret the password is the "password" key of. Alertmanager reads it from there.
	PasswordSecret string
}

// Channel is one place alerts go to, with its secret.
type Channel struct {
	ID             string
	Name           string
	Type           string
	SlackURL       string
	Recipients     []string
	Events         []string
	Projects       []string
	Environments   []string
	NotifyResolved bool
	Disabled       bool
}

// Config is everything a configuration needs: the mail server, the channels, and the dashboard's address for links.
type Config struct {
	SMTP         *SMTP
	Channels     []Channel
	DashboardURL string
}

// Active reports whether any channel is on.
func (c Config) Active() bool {
	return slices.ContainsFunc(c.Channels, func(channel Channel) bool { return !channel.Disabled })
}

// Channel finds a channel by id.
func (c Config) Channel(id string) (Channel, bool) {
	for _, channel := range c.Channels {
		if channel.ID == id {
			return channel, true
		}
	}
	return Channel{}, false
}
