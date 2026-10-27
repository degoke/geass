package mailrelay

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// The relay's metrics, by organization. An organization label only ever holds the name of an organization the relay was
// configured with, or "unknown" for a sign-in with a username it does not know, so what anyone types cannot make series.

// Reasons a message, a recipient or a sign-in is refused, as the rejections metric and the usage API name them.
const (
	reasonRateMinute      = "rate_minute"
	reasonRateHour        = "rate_hour"
	reasonRateDay         = "rate_day"
	reasonRecipients      = "recipients"
	reasonSize            = "size"
	reasonSenderDomain    = "sender_domain"
	reasonRecipientDomain = "recipient_domain"
	reasonConnections     = "connections"
	reasonRemoved         = "organization_removed"
)

// Kinds of upstream failure.
const (
	// upstreamUnreachable: the upstream could not be connected to, did not offer TLS, or dropped the connection.
	upstreamUnreachable = "unreachable"
	// upstreamDeferred: it answered with a temporary failure (4xx).
	upstreamDeferred = "deferred"
	// upstreamRefused: it refused the message for good (5xx).
	upstreamRefused = "refused"
	// upstreamAuth: it refused the relay's own sign-in, which is the platform's to fix.
	upstreamAuth = "auth"
)

const unknownOrg = "unknown"

// orgLabel is the label, and the log key, that names an organization.
const orgLabel = "org"

type metrics struct {
	registry         *prometheus.Registry
	accepted         *prometheus.CounterVec
	recipients       *prometheus.CounterVec
	bytes            *prometheus.CounterVec
	rejections       *prometheus.CounterVec
	upstreamFailures *prometheus.CounterVec
	authFailures     *prometheus.CounterVec
	connections      *prometheus.GaugeVec
	upstreamDuration *prometheus.HistogramVec
	reloads          *prometheus.CounterVec
}

func newMetrics() *metrics {
	m := &metrics{
		registry: prometheus.NewRegistry(),
		accepted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "geass_mail_relay_messages_accepted_total",
			Help: "Messages the upstream took from the relay, by organization.",
		}, []string{orgLabel}),
		recipients: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "geass_mail_relay_recipients_accepted_total",
			Help: "Recipients of the messages the upstream took, by organization.",
		}, []string{orgLabel}),
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "geass_mail_relay_bytes_accepted_total",
			Help: "Bytes of the messages the upstream took, by organization.",
		}, []string{orgLabel}),
		rejections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "geass_mail_relay_rejections_total",
			Help: "Messages, recipients and connections the relay refused, by organization and reason (rate_minute, rate_hour, rate_day, recipients, size, sender_domain, recipient_domain, connections, organization_removed).",
		}, []string{orgLabel, "reason"}),
		upstreamFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "geass_mail_relay_upstream_failures_total",
			Help: "Messages the upstream did not take, by organization and kind (unreachable, deferred, refused, auth).",
		}, []string{orgLabel, "kind"}),
		authFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "geass_mail_relay_auth_failures_total",
			Help: "Failed sign-ins, by the organization whose username was given, or unknown.",
		}, []string{orgLabel}),
		connections: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "geass_mail_relay_connections",
			Help: "Connections signed in now, by organization.",
		}, []string{orgLabel}),
		upstreamDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "geass_mail_relay_upstream_duration_seconds",
			Help:    "How long handing one message to the upstream took, by result (accepted, or the kind of failure).",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
		}, []string{"result"}),
		reloads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "geass_mail_relay_config_reloads_total",
			Help: "Times the relay read its configuration, certificate or credentials anew, by result (ok, failed).",
		}, []string{"result"}),
	}
	m.registry.MustRegister(m.accepted, m.recipients, m.bytes, m.rejections, m.upstreamFailures, m.authFailures,
		m.connections, m.upstreamDuration, m.reloads,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}
