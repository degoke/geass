// Package observability is what Geass knows of the metrics and log stores outside the cluster that the platform
// configuration names (spec.observability.external): which of them are in use, whether their addresses can be used, the
// credentials each one takes, and the headers every request to them carries. The controllers use it to send to the
// stores, and the dashboard to read from them.
package observability

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const (
	// DefaultTenantHeader carries the metrics tenant unless the configuration names another header. Mimir, Cortex and
	// Loki read it.
	DefaultTenantHeader = "X-Scope-OrgID"
	// HeaderAccountID and HeaderProjectID carry the VictoriaLogs tenant, on writes and on queries.
	HeaderAccountID = "AccountID"
	HeaderProjectID = "ProjectID"

	// KeyToken, KeyUsername and KeyPassword are the keys of a credentials Secret: a token for a bearer credential, or a
	// username and a password for basic authentication.
	KeyToken    = "token"
	KeyUsername = "username"
	KeyPassword = "password"

	// MetricsCredentialsSecret is the copy of the metrics store's credentials that Geass keeps beside Prometheus, in the
	// monitoring namespace, for Prometheus to read. LogsCredentialsSecret is the copy of the log store's beside the log
	// shipper, in the logging namespace.
	MetricsCredentialsSecret = "geass-external-metrics"
	LogsCredentialsSecret    = "geass-external-logs"
)

// ErrInvalidCredentials is a credentials Secret whose keys say nothing usable, or say two things at once.
var ErrInvalidCredentials = errors.New("the credentials Secret must have a token, or a username and a password")

// Metrics is the metrics store outside the cluster that the configuration names, or nil.
func Metrics(config *geassv1alpha1.GeassPlatformConfig) *geassv1alpha1.GeassExternalMetricsSpec {
	if external := externalOf(config); external != nil {
		return external.Metrics
	}
	return nil
}

// Logs is the log store outside the cluster that the configuration names, or nil.
func Logs(config *geassv1alpha1.GeassPlatformConfig) *geassv1alpha1.GeassExternalLogsSpec {
	if external := externalOf(config); external != nil {
		return external.Logs
	}
	return nil
}

func externalOf(config *geassv1alpha1.GeassPlatformConfig) *geassv1alpha1.GeassExternalObservabilitySpec {
	if config == nil || config.Spec.Observability == nil {
		return nil
	}
	return config.Spec.Observability.External
}

// CheckURL says whether a store's address can be used: http or https, with a host, and with no user or password in it,
// since credentials belong in the Secret and an address is written where anyone who reads the add-ons' settings sees it.
// It may not hold whitespace or "${", which the log shipper's configuration would read as more than an address. query
// says whether it may have a query string: a base address that Geass adds a path to may not.
func CheckURL(raw string, query bool) error {
	if strings.ContainsAny(raw, " \t\r\n") || strings.Contains(raw, "${") {
		return errors.New("the address may not contain whitespace or ${")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("the address is not a URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("the address must be http or https")
	}
	if parsed.Hostname() == "" {
		return errors.New("the address has no host")
	}
	if parsed.User != nil {
		return errors.New("the address may not hold a username or password: put them in the credentials Secret")
	}
	if parsed.Fragment != "" || (!query && (parsed.RawQuery != "" || parsed.ForceQuery)) {
		return errors.New("the address may not have a query or a fragment")
	}
	return nil
}

// reservedHeaders say how a request is made, or are set by Prometheus or by Geass on every request, so they cannot carry
// a tenant. Prometheus refuses a remote write that sets one, and a configuration it refuses stops all of its scraping.
var reservedHeaders = []string{
	"accept-encoding", "authorization", "connection", "content-encoding", "content-length", "content-type", "host",
	"keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade",
	"user-agent", "www-authenticate", "x-prometheus-remote-read-version", "x-prometheus-remote-write-version",
	strings.ToLower(HeaderAccountID), strings.ToLower(HeaderProjectID),
}

// MetricsHeaders are the headers every write to and query of the metrics store carries: the tenant, when there is one.
func MetricsHeaders(spec *geassv1alpha1.GeassExternalMetricsSpec) (map[string]string, error) {
	if spec == nil || spec.Tenant == "" {
		return nil, nil
	}
	name := spec.TenantHeader
	if name == "" {
		name = DefaultTenantHeader
	}
	if slices.Contains(reservedHeaders, strings.ToLower(name)) {
		return nil, fmt.Errorf("%s cannot carry the tenant: Prometheus or Geass sets it", name)
	}
	return map[string]string{http.CanonicalHeaderKey(name): spec.Tenant}, nil
}

// LogsHeaders are the headers every line sent to and every query of the log store carries: the VictoriaLogs tenant.
// Both are sent even when they are 0, which is VictoriaLogs' default, so what a line was written to is never left to a
// proxy in front of the store.
func LogsHeaders(spec *geassv1alpha1.GeassExternalLogsSpec) map[string]string {
	if spec == nil {
		return nil
	}
	return map[string]string{
		HeaderAccountID: strconv.FormatInt(spec.AccountID, 10),
		HeaderProjectID: strconv.FormatInt(spec.ProjectID, 10),
	}
}

// AuthKind is how a request signs in to a store.
type AuthKind string

const (
	AuthNone   AuthKind = ""
	AuthBasic  AuthKind = "basic"
	AuthBearer AuthKind = "bearer"
)

// Credentials are how Geass signs in to a store: a bearer token, or a username and a password. They are read from a
// Secret, and written nowhere but the copy of that Secret beside the add-on that sends.
type Credentials struct {
	Token    string
	Username string
	Password string
}

// Kind is how the credentials sign in.
func (c Credentials) Kind() AuthKind {
	switch {
	case c.Token != "":
		return AuthBearer
	case c.Username != "":
		return AuthBasic
	default:
		return AuthNone
	}
}

// Authorization is the value of the Authorization header that signs in with the credentials, or empty for none.
func (c Credentials) Authorization() string {
	switch c.Kind() {
	case AuthBearer:
		return "Bearer " + c.Token
	case AuthBasic:
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password))
	default:
		return ""
	}
}

// Data is the credentials as the data of a Secret, with only the keys they use.
func (c Credentials) Data() map[string][]byte {
	switch c.Kind() {
	case AuthBearer:
		return map[string][]byte{KeyToken: []byte(c.Token)}
	case AuthBasic:
		return map[string][]byte{KeyUsername: []byte(c.Username), KeyPassword: []byte(c.Password)}
	default:
		return nil
	}
}

// CredentialsOf reads a credentials Secret. One with a token and a username or password too is refused, because which
// of the two was meant cannot be told, and so is one with a username and no password, or a password and no username.
// One with none of the keys is refused as well: a Secret that is named is meant to say something.
func CredentialsOf(secret *corev1.Secret) (Credentials, error) {
	credentials := Credentials{
		Token:    strings.TrimSpace(string(secret.Data[KeyToken])),
		Username: string(secret.Data[KeyUsername]),
		Password: string(secret.Data[KeyPassword]),
	}
	basic := credentials.Username != "" || credentials.Password != ""
	switch {
	case credentials.Token != "" && basic:
		return Credentials{}, fmt.Errorf("%w, not both: Secret %s", ErrInvalidCredentials, secret.Name)
	case credentials.Token == "" && (credentials.Username == "" || credentials.Password == ""):
		return Credentials{}, fmt.Errorf("%w: Secret %s", ErrInvalidCredentials, secret.Name)
	case strings.ContainsAny(credentials.Token+credentials.Username+credentials.Password, "\r\n"):
		return Credentials{}, fmt.Errorf("%w on one line: Secret %s", ErrInvalidCredentials, secret.Name)
	}
	return credentials, nil
}

// LoadCredentials reads the credentials of the Secret ref names in the namespace. No ref means no credentials. A Secret
// that is not there is reported as the API server's not-found error.
func LoadCredentials(ctx context.Context, reader client.Reader, namespace string, ref *corev1.LocalObjectReference) (Credentials, error) {
	if ref == nil || ref.Name == "" {
		return Credentials{}, nil
	}
	var secret corev1.Secret
	if err := reader.Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: namespace}, &secret); err != nil {
		return Credentials{}, err
	}
	return CredentialsOf(&secret)
}

// Query is how the dashboard reads a store outside the cluster: its address, and the headers every query carries (the
// tenant, and the credentials).
type Query struct {
	URL    string
	Header http.Header
}

// MetricsQuery is the metrics store the dashboard reads, or nil when the configuration names none to read. An error
// says one is named and cannot be used, which the dashboard reports as metrics being unavailable: it never falls back
// to the cluster's Prometheus, which a dashboard outside the cluster cannot reach anyway.
func MetricsQuery(ctx context.Context, reader client.Reader, namespace string, config *geassv1alpha1.GeassPlatformConfig) (*Query, error) {
	spec := Metrics(config)
	if spec == nil || spec.QueryURL == "" {
		return nil, nil
	}
	if err := CheckURL(spec.QueryURL, false); err != nil {
		return nil, err
	}
	headers, err := MetricsHeaders(spec)
	if err != nil {
		return nil, err
	}
	return query(ctx, reader, namespace, spec.QueryURL, headers, spec.CredentialRef)
}

// LogsQuery is the log store the dashboard reads history from, or nil when the configuration names none to read. An
// error says one is named and cannot be used; as with metrics, the dashboard does not fall back to the cluster's store.
func LogsQuery(ctx context.Context, reader client.Reader, namespace string, config *geassv1alpha1.GeassPlatformConfig) (*Query, error) {
	spec := Logs(config)
	if spec == nil || spec.QueryURL == "" {
		return nil, nil
	}
	if err := CheckURL(spec.QueryURL, false); err != nil {
		return nil, err
	}
	return query(ctx, reader, namespace, spec.QueryURL, LogsHeaders(spec), spec.CredentialRef)
}

func query(ctx context.Context, reader client.Reader, namespace, address string, headers map[string]string, ref *corev1.LocalObjectReference) (*Query, error) {
	credentials, err := LoadCredentials(ctx, reader, namespace, ref)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	for name, value := range headers {
		header.Set(name, value)
	}
	if authorization := credentials.Authorization(); authorization != "" {
		header.Set("Authorization", authorization)
	}
	return &Query{URL: strings.TrimRight(address, "/"), Header: header}, nil
}
