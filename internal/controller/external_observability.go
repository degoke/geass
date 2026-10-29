package controller

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/observability"
	"github.com/degoke/geass/pkg/platform"
)

// The stores outside the cluster that the platform configuration names (observability.external) are sent to as well
// as the stores in the cluster: Prometheus remote-writes every sample it keeps to the metrics store, and the log shipper
// sends every line it collects to the log store with a second output. Both keep sending to the cluster's own stores.
//
// Credentials are never written into the add-ons' values, which are kept in a HelmChart that anyone who can read those
// sees. Geass copies the keys it uses from the Secret the configuration names into a Secret beside the add-on, and the
// values name that copy: Prometheus's operator reads it for Prometheus, and the shipper has it as environment variables.
//
// It is background work, like the add-ons it changes. A store that is named wrongly, or whose credentials cannot be
// read, is logged and left out, so that a mistake in it never stops the cluster's own metrics or logs.

// externalRemoteWriteName names the remote write of the metrics store in Prometheus's configuration.
const externalRemoteWriteName = "geass-external"

// The environment variables the log shipper reads the log store's credentials from.
const (
	externalLogsTokenEnv    = "GEASS_EXTERNAL_LOGS_TOKEN"
	externalLogsUsernameEnv = "GEASS_EXTERNAL_LOGS_USERNAME"
	externalLogsPasswordEnv = "GEASS_EXTERNAL_LOGS_PASSWORD"
)

// externalLogsCredentialsAnnotation is on the shipper's pods: the resourceVersion of the copy of the log store's
// credentials. The shipper reads its environment only when it starts, so new credentials have to start new pods.
const externalLogsCredentialsAnnotation = "geass.dev/external-logs-credentials"

// externalSink is a store outside the cluster that an add-on sends to: where, with which headers, and how it signs in.
type externalSink struct {
	url     string
	headers map[string]string
	auth    observability.AuthKind
	// secret is the copy of the credentials beside the add-on, and secretVersion its resourceVersion. Both are empty
	// when the store takes no credentials.
	secret        string
	secretVersion string
}

// platformConfig reads the platform configuration, or nil when there is none.
func (r *GeassClusterReconciler) platformConfig(ctx context.Context) (*geassv1alpha1.GeassPlatformConfig, error) {
	config := &geassv1alpha1.GeassPlatformConfig{}
	err := r.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, config)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return config, nil
}

// externalMetricsSink is the metrics store Prometheus remote-writes to, with its credentials copied beside Prometheus
// first, or nil when none is named or the one named cannot be used. An error is the API server's.
func (r *GeassClusterReconciler) externalMetricsSink(ctx context.Context) (*externalSink, error) {
	config, err := r.platformConfig(ctx)
	if err != nil {
		return nil, err
	}
	spec := observability.Metrics(config)
	if spec == nil || spec.RemoteWriteURL == "" {
		return nil, r.removeCredentialsCopy(ctx, platform.MonitoringTargetNS, observability.MetricsCredentialsSecret)
	}
	headers, err := observability.MetricsHeaders(spec)
	if err == nil {
		err = observability.CheckURL(spec.RemoteWriteURL, true)
	}
	if err != nil {
		// The address is not logged: one that was refused for holding a password would put it in the log.
		logf.FromContext(ctx).Error(err, "Ignored the external metrics store: Prometheus does not send to it")
		return nil, r.removeCredentialsCopy(ctx, platform.MonitoringTargetNS, observability.MetricsCredentialsSecret)
	}
	return r.externalSinkWith(ctx, "metrics", platform.MonitoringTargetNS, observability.MetricsCredentialsSecret,
		spec.RemoteWriteURL, headers, spec.CredentialRef)
}

// externalLogsSink is the log store the shipper sends to, with its credentials copied beside the shipper first, or nil
// when none is named or the one named cannot be used. An error is the API server's.
func (r *GeassClusterReconciler) externalLogsSink(ctx context.Context) (*externalSink, error) {
	config, err := r.platformConfig(ctx)
	if err != nil {
		return nil, err
	}
	spec := observability.Logs(config)
	if spec == nil || spec.PushURL == "" {
		return nil, r.removeCredentialsCopy(ctx, platform.LoggingTargetNS, observability.LogsCredentialsSecret)
	}
	if err := observability.CheckURL(spec.PushURL, false); err != nil {
		logf.FromContext(ctx).Error(err, "Ignored the external log store: the log shipper does not send to it")
		return nil, r.removeCredentialsCopy(ctx, platform.LoggingTargetNS, observability.LogsCredentialsSecret)
	}
	return r.externalSinkWith(ctx, "logs", platform.LoggingTargetNS, observability.LogsCredentialsSecret,
		spec.PushURL, observability.LogsHeaders(spec), spec.CredentialRef)
}

func (r *GeassClusterReconciler) externalSinkWith(ctx context.Context, store, namespace, copyName, address string,
	headers map[string]string, ref *corev1.LocalObjectReference) (*externalSink, error) {
	credentials, err := observability.LoadCredentials(ctx, r.Client, platform.SystemNamespace, ref)
	if apierrors.IsNotFound(err) || errors.Is(err, observability.ErrInvalidCredentials) {
		logf.FromContext(ctx).Error(err, "Ignored the external store: its credentials could not be read", "store", store, "secret", ref.Name)
		return nil, r.removeCredentialsCopy(ctx, namespace, copyName)
	}
	if err != nil {
		return nil, err
	}
	sink := &externalSink{url: address, headers: headers, auth: credentials.Kind()}
	if sink.auth == observability.AuthNone {
		return sink, r.removeCredentialsCopy(ctx, namespace, copyName)
	}
	version, err := r.placeCredentialsCopy(ctx, namespace, copyName, credentials)
	if err != nil {
		return nil, err
	}
	sink.secret, sink.secretVersion = copyName, version
	return sink, nil
}

// placeCredentialsCopy writes the keys the credentials use into the Secret name beside an add-on, and returns its
// resourceVersion. The add-on's namespace is made when its chart has not made it yet, so that the add-on is installed
// with the store from the start and not one pass later.
func (r *GeassClusterReconciler) placeCredentialsCopy(ctx context.Context, namespace, name string, credentials observability.Credentials) (string, error) {
	if err := ensureNamespace(ctx, r.Client, namespace, map[string]string{platform.LabelManagedBy: platform.ManagedByValue}); err != nil {
		return "", err
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if secret.CreationTimestamp.IsZero() {
			secret.Type = corev1.SecretTypeOpaque
		}
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels[platform.LabelManagedBy] = platform.ManagedByValue
		secret.Data = credentials.Data()
		return nil
	}); err != nil {
		return "", err
	}
	return secret.ResourceVersion, nil
}

// removeCredentialsCopy deletes the copy of a store's credentials beside an add-on, when Geass made it.
func (r *GeassClusterReconciler) removeCredentialsCopy(ctx context.Context, namespace, name string) error {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, secret); err != nil {
		return client.IgnoreNotFound(err)
	}
	if secret.Labels[platform.LabelManagedBy] != platform.ManagedByValue {
		return nil
	}
	return client.IgnoreNotFound(r.Delete(ctx, secret))
}

// withRemoteWrite adds the remote write to the metrics store to the monitoring chart's values. Without a store the
// values are returned as they are, byte for byte, so that a cluster that names none sees no change.
func withRemoteWrite(values string, sink *externalSink) (string, error) {
	if sink == nil {
		return values, nil
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(values), &parsed); err != nil {
		return "", fmt.Errorf("monitoring values: %w", err)
	}
	prometheus, _ := parsed["prometheus"].(map[string]any)
	if prometheus == nil {
		prometheus = map[string]any{}
	}
	spec, _ := prometheus["prometheusSpec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
	}
	spec["remoteWrite"] = []any{remoteWriteOf(sink)}
	prometheus["prometheusSpec"] = spec
	parsed["prometheus"] = prometheus
	merged, err := yaml.Marshal(parsed)
	if err != nil {
		return "", fmt.Errorf("monitoring values: %w", err)
	}
	return string(merged), nil
}

// remoteWriteOf is the Prometheus operator's RemoteWriteSpec for the store. The credentials are references to keys of
// the copy beside Prometheus, which the operator reads and keeps up to date when the copy changes.
func remoteWriteOf(sink *externalSink) map[string]any {
	entry := map[string]any{"name": externalRemoteWriteName, "url": sink.url}
	if len(sink.headers) > 0 {
		headers := map[string]any{}
		for name, value := range sink.headers {
			headers[name] = value
		}
		entry["headers"] = headers
	}
	key := func(name string) map[string]any { return map[string]any{"name": sink.secret, "key": name} }
	switch sink.auth {
	case observability.AuthBasic:
		entry["basicAuth"] = map[string]any{"username": key(observability.KeyUsername), "password": key(observability.KeyPassword)}
	case observability.AuthBearer:
		entry["authorization"] = map[string]any{"type": "Bearer", "credentials": key(observability.KeyToken)}
	}
	return entry
}

// externalLogsOutput is the shipper's second output, to the log store: the same lines, with the same fields, as the
// output to the store in the cluster, and the tenant and the credentials. The credentials are environment variables,
// which the shipper expands when it reads its configuration. Empty without a store.
func externalLogsOutput(sink *externalSink) string {
	if sink == nil {
		return ""
	}
	address, err := url.Parse(sink.url)
	if err != nil {
		return ""
	}
	tls := address.Scheme == "https"
	port := address.Port()
	switch {
	case port != "":
	case tls:
		port = "443"
	default:
		port = "80"
	}
	lines := []string{
		"Name http",
		"Match kube.*",
		"Host " + address.Hostname(),
		"Port " + port,
		"URI " + strings.TrimRight(address.EscapedPath(), "/") + logStoreInsertURI,
		"Format json_lines",
		"Json_date_key date",
		"Json_date_format iso8601",
	}
	if tls {
		lines = append(lines, "tls On", "tls.verify On")
	}
	names := make([]string, 0, len(sink.headers))
	for name := range sink.headers {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		lines = append(lines, "Header "+name+" "+sink.headers[name])
	}
	switch sink.auth {
	case observability.AuthBasic:
		lines = append(lines, "http_User ${"+externalLogsUsernameEnv+"}", "http_Passwd ${"+externalLogsPasswordEnv+"}")
	case observability.AuthBearer:
		lines = append(lines, "Header Authorization Bearer ${"+externalLogsTokenEnv+"}")
	}
	lines = append(lines, "Retry_Limit 5")
	var b strings.Builder
	b.WriteString("    [OUTPUT]\n")
	for _, line := range lines {
		b.WriteString("        " + line + "\n")
	}
	return b.String()
}

// externalLogsEnvironment is the shipper's environment for the log store's credentials, read from the copy beside it,
// and the annotation that starts new pods when the copy changes. Empty without credentials.
func externalLogsEnvironment(sink *externalSink) string {
	if sink == nil || sink.secret == "" {
		return ""
	}
	variables := map[string]string{externalLogsTokenEnv: observability.KeyToken}
	if sink.auth == observability.AuthBasic {
		variables = map[string]string{externalLogsUsernameEnv: observability.KeyUsername, externalLogsPasswordEnv: observability.KeyPassword}
	}
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	slices.Sort(names)
	var b strings.Builder
	b.WriteString("env:\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  - name: %s\n    valueFrom:\n      secretKeyRef:\n        name: %s\n        key: %s\n", name, sink.secret, variables[name])
	}
	fmt.Fprintf(&b, "podAnnotations:\n  %s: %q\n", externalLogsCredentialsAnnotation, sink.secretVersion)
	return b.String()
}

// isCredentialsCopy says whether the object is one of the copies of a store's credentials beside an add-on.
func isCredentialsCopy(object client.Object) bool {
	switch {
	case object.GetNamespace() == platform.MonitoringTargetNS:
		return object.GetName() == observability.MetricsCredentialsSecret
	case object.GetNamespace() == platform.LoggingTargetNS:
		return object.GetName() == observability.LogsCredentialsSecret
	default:
		return false
	}
}

// clustersForExternalCredentials asks for every cluster when a store's credentials change, or a copy of them does, so
// that new credentials are copied (and the shipper started with them) at once, and a copy someone changed is put back.
// Every other Secret asks for nothing.
func (r *GeassClusterReconciler) clustersForExternalCredentials(ctx context.Context, object client.Object) []reconcile.Request {
	if isCredentialsCopy(object) {
		return r.clustersForNodeChange(ctx, object)
	}
	if object.GetNamespace() != platform.SystemNamespace {
		return nil
	}
	config, err := r.platformConfig(ctx)
	if err != nil || config == nil {
		return nil
	}
	for _, ref := range []*corev1.LocalObjectReference{credentialRefOfMetrics(config), credentialRefOfLogs(config)} {
		if ref != nil && ref.Name == object.GetName() {
			return r.clustersForNodeChange(ctx, object)
		}
	}
	return nil
}

func credentialRefOfMetrics(config *geassv1alpha1.GeassPlatformConfig) *corev1.LocalObjectReference {
	if spec := observability.Metrics(config); spec != nil {
		return spec.CredentialRef
	}
	return nil
}

func credentialRefOfLogs(config *geassv1alpha1.GeassPlatformConfig) *corev1.LocalObjectReference {
	if spec := observability.Logs(config); spec != nil {
		return spec.CredentialRef
	}
	return nil
}

// externalObservabilityChanged lets through the platform configuration only when what it says of the stores outside
// the cluster changed: every other setting in it is someone else's to act on.
func externalObservabilityChanged() predicate.Predicate {
	isThePlatform := func(object client.Object) bool {
		return object.GetName() == platform.HAReadinessName && object.GetNamespace() == platform.SystemNamespace
	}
	observabilityOf := func(object client.Object) *geassv1alpha1.GeassObservabilitySpec {
		if config, ok := object.(*geassv1alpha1.GeassPlatformConfig); ok {
			return config.Spec.Observability
		}
		return nil
	}
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return isThePlatform(e.Object) && observabilityOf(e.Object) != nil },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return isThePlatform(e.ObjectNew) && !equality.Semantic.DeepEqual(observabilityOf(e.ObjectOld), observabilityOf(e.ObjectNew))
		},
		DeleteFunc:  func(e event.DeleteEvent) bool { return isThePlatform(e.Object) && observabilityOf(e.Object) != nil },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}
