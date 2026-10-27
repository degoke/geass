package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// GeassTenantMail is how the pods of organizations' projects send mail on a cluster that organizations share. The
// outbound limits (spec.tenancy.egress) close the mail ports to the outside; this gives the organizations a way that the
// platform controls instead.
type GeassTenantMail struct {
	// Relay runs a mail relay in the cluster, which organizations' services submit mail to with credentials of their
	// organization's own, and which hands each message to the platform's mail service (the upstream) once it is within
	// the organization's limits. Unset means no relay.
	// +optional
	Relay *GeassMailRelaySpec `json:"relay,omitempty"`
}

// GeassMailRelayTLS is how the relay speaks TLS to its upstream.
// +kubebuilder:validation:Enum=StartTLS;TLS;None
type GeassMailRelayTLS string

const (
	// MailRelayTLSStartTLS connects in plain text and requires the upstream to offer STARTTLS before anything is sent,
	// which is how port 587 works. It is the default.
	MailRelayTLSStartTLS GeassMailRelayTLS = "StartTLS"
	// MailRelayTLSImplicit speaks TLS from the first byte, which is how port 465 works.
	MailRelayTLSImplicit GeassMailRelayTLS = "TLS"
	// MailRelayTLSNone sends in plain text: only for an upstream inside the cluster or on a private network.
	MailRelayTLSNone GeassMailRelayTLS = "None"
)

// GeassMailRelaySpec is the platform's mail relay. Geass runs it as the Deployment geass-mail-relay in the system
// namespace, from the operator's own image, gives each organization a login of its own (the Secret geass-mail in each
// environment of its projects), and holds each organization to its limits. A message is answered only once the upstream
// has taken it, so the relay keeps no queue.
// +kubebuilder:validation:XValidation:rule="!self.enabled || has(self.upstream)",message="an enabled relay needs an upstream"
type GeassMailRelaySpec struct {
	// Enabled runs the relay. Turning it off removes the relay and every login Geass made for it.
	// +optional
	Enabled bool `json:"enabled,omitempty"`
	// Upstream is the mail service the relay hands every message to: Amazon SES, Postmark, a company's relay.
	// +optional
	Upstream *GeassMailRelayUpstream `json:"upstream,omitempty"`
	// Limits are what each organization may send. An organization's own spec.mail.limits replaces each field it sets.
	// A field set in neither place has the relay's default: 60 messages a minute, 600 an hour, 2000 a day, 50 recipients
	// a message, 10Mi a message and 5 connections at once.
	// +optional
	Limits *GeassMailRelayLimits `json:"limits,omitempty"`
	// AllowedSenderDomains are the domains organizations may send from: both the envelope sender (MAIL FROM) and every
	// address of the From and Sender headers must be at one of them, or below one. An organization's own
	// spec.mail.allowedSenderDomains replaces the list. Unset, and with no list of the organization's, any sender is
	// taken, unless RequireSenderDomain is set.
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^([a-z0-9]([-a-z0-9]*[a-z0-9])?\.)+[a-z]{2,63}$`
	// +listType=set
	// +optional
	AllowedSenderDomains []string `json:"allowedSenderDomains,omitempty"`
	// RequireSenderDomain refuses every message of an organization that has no allowed sender domain, neither its own
	// nor the platform's, instead of taking any sender. Set it when each organization's domains have to be checked first
	// (the upstream only sends from verified domains, say).
	// +optional
	RequireSenderDomain bool `json:"requireSenderDomain,omitempty"`
	// DeniedRecipientDomains are domains the relay never sends to, for every organization: a recipient at one of them, or
	// below one, is refused.
	// +kubebuilder:validation:MaxItems=200
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^([a-z0-9]([-a-z0-9]*[a-z0-9])?\.)+[a-z]{2,63}$`
	// +listType=set
	// +optional
	DeniedRecipientDomains []string `json:"deniedRecipientDomains,omitempty"`
}

// GeassMailRelayUpstream is where the relay sends, and how it signs in there.
// +kubebuilder:validation:XValidation:rule="!(has(self.credentialRef) && has(self.tls) && self.tls == 'None')",message="credentialRef needs tls StartTLS or TLS: the upstream's password is never sent in plain text"
type GeassMailRelayUpstream struct {
	// Host is the upstream's host name or IPv4 address, such as email-smtp.eu-west-1.amazonaws.com. With TLS, its
	// certificate must be valid for this name and signed by a public authority.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9]([-a-zA-Z0-9]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([-a-zA-Z0-9]*[a-zA-Z0-9])?)*$`
	Host string `json:"host"`
	// Port is the upstream's port. Unset means 587, or 465 with TLS.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port int32 `json:"port,omitempty"`
	// TLS is StartTLS (the default: the upstream must offer STARTTLS, or nothing is sent), TLS (from the first byte, as on
	// port 465) or None (plain text, for an upstream inside the cluster or on a private network only).
	// +optional
	TLS GeassMailRelayTLS `json:"tls,omitempty"`
	// CredentialRef names a Secret in the system namespace with the keys username and password, which the relay signs in
	// to the upstream with (AUTH PLAIN, only over TLS). The relay reads it from a volume, so a new password in the Secret
	// is used within a minute or two, without a restart. Unset means the relay does not sign in.
	// +optional
	CredentialRef *corev1.LocalObjectReference `json:"credentialRef,omitempty"`
}

// GeassMailRelayLimits are what one organization may send through the relay. Rates count messages, however many
// recipients each has, which is why recipients per message are capped too; a message counts from the moment the relay
// takes its MAIL command, and is given back if it is not sent. A field left out takes the platform's value, or the
// relay's default.
type GeassMailRelayLimits struct {
	// MessagesPerMinute is the most messages in any 60 seconds. Zero sends nothing, which stops an organization's mail at
	// once.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100000
	// +optional
	MessagesPerMinute *int32 `json:"messagesPerMinute,omitempty"`
	// MessagesPerHour is the most messages in any 60 minutes.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000000
	// +optional
	MessagesPerHour *int32 `json:"messagesPerHour,omitempty"`
	// MessagesPerDay is the most messages in any 24 hours.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10000000
	// +optional
	MessagesPerDay *int32 `json:"messagesPerDay,omitempty"`
	// RecipientsPerMessage is the most recipients one message may have. A recipient past it is refused with 452, which
	// tells the sender to send it in another message.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	// +optional
	RecipientsPerMessage *int32 `json:"recipientsPerMessage,omitempty"`
	// MaxMessageSize is the largest message, headers and attachments included, such as "10Mi". At most 50Mi.
	// +optional
	MaxMessageSize *resource.Quantity `json:"maxMessageSize,omitempty"`
	// Connections is the most connections the organization may have signed in at once.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	// +optional
	Connections *int32 `json:"connections,omitempty"`
}

// GeassOrganizationMail is what an organization may send through the platform's mail relay
// (spec.tenancy.mail.relay on the GeassPlatformConfig), in place of the platform's.
type GeassOrganizationMail struct {
	// Limits replace the platform's relay limits, each field it sets for that field.
	// +optional
	Limits *GeassMailRelayLimits `json:"limits,omitempty"`
	// AllowedSenderDomains are the domains the organization may send from, in place of the platform's list: the envelope
	// sender and the addresses of the From and Sender headers must be at one of them, or below one. An empty list
	// replaces the platform's with none.
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^([a-z0-9]([-a-z0-9]*[a-z0-9])?\.)+[a-z]{2,63}$`
	// +listType=set
	// +optional
	AllowedSenderDomains []string `json:"allowedSenderDomains,omitempty"`
}
