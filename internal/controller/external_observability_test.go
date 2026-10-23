package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/observability"
	"github.com/degoke/geass/pkg/platform"
)

const (
	testMetricsToken  = "metrics-t0ken-never-in-values"
	testLogsPassword  = "logs-passw0rd-never-in-values"
	testCredentialRef = "rungeass-observability"
)

func externalPlatformConfig(external *geassv1alpha1.GeassExternalObservabilitySpec) *geassv1alpha1.GeassPlatformConfig {
	return &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassPlatformConfigSpec{Observability: &geassv1alpha1.GeassObservabilitySpec{External: external}},
	}
}

func externalCredentials(name string, data map[string]string) *corev1.Secret {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace}, Data: map[string][]byte{}}
	for key, value := range data {
		secret.Data[key] = []byte(value)
	}
	return secret
}

// rungeassExternal is what a managed cluster is given: a Mimir-style metrics store with a tenant header and a bearer
// token, and a VictoriaLogs store with its tenant and a username and password.
func rungeassExternal() *geassv1alpha1.GeassExternalObservabilitySpec {
	return &geassv1alpha1.GeassExternalObservabilitySpec{
		Metrics: &geassv1alpha1.GeassExternalMetricsSpec{
			RemoteWriteURL: "https://metrics.rungeass.test/api/v1/push",
			QueryURL:       "https://metrics.rungeass.test/prometheus",
			Tenant:         "rg-acme",
			CredentialRef:  &corev1.LocalObjectReference{Name: testCredentialRef},
		},
		Logs: &geassv1alpha1.GeassExternalLogsSpec{
			PushURL:       "https://logs.rungeass.test/vl",
			QueryURL:      "https://logs.rungeass.test/vl",
			AccountID:     41,
			ProjectID:     2,
			CredentialRef: &corev1.LocalObjectReference{Name: "rungeass-logs"},
		},
	}
}

func remoteWritesOf(t *testing.T, values string) []map[string]any {
	t.Helper()
	spec := prometheusSpecOf(t, values)
	raw, ok := spec["remoteWrite"].([]any)
	if !ok {
		return nil
	}
	writes := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		writes = append(writes, entry.(map[string]any))
	}
	return writes
}

func TestWithoutAnExternalStoreTheMonitoringValuesAreUnchangedByteForByte(t *testing.T) {
	for name, values := range map[string]string{"lite": monitoringLiteValues, "full": monitoringPrometheusValues} {
		got, err := withRemoteWrite(values, nil)
		require.NoError(t, err)
		require.Equal(t, values, got, "%s: a cluster that names no store must not see its monitoring chart upgraded", name)
	}
}

func TestPrometheusRemoteWritesWithTheTenantHeaderAndATokenByReference(t *testing.T) {
	sink := &externalSink{
		url: "https://metrics.rungeass.test/api/v1/push", headers: map[string]string{"X-Scope-OrgID": "rg-acme"},
		auth: observability.AuthBearer, secret: observability.MetricsCredentialsSecret, secretVersion: "7",
	}
	values, err := withRemoteWrite(monitoringLiteValues, sink)
	require.NoError(t, err)

	writes := remoteWritesOf(t, values)
	require.Len(t, writes, 1)
	require.Equal(t, map[string]any{
		"name":    "geass-external",
		"url":     "https://metrics.rungeass.test/api/v1/push",
		"headers": map[string]any{"X-Scope-OrgID": "rg-acme"},
		"authorization": map[string]any{"type": "Bearer", "credentials": map[string]any{
			"name": observability.MetricsCredentialsSecret, "key": observability.KeyToken,
		}},
	}, writes[0])

	spec := prometheusSpecOf(t, values)
	require.Equal(t, "15d", spec["retention"], "Prometheus keeps its own copy as before")
	require.NotNil(t, spec["storageSpec"])
	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(values), &parsed))
	require.Equal(t, false, parsed["grafana"].(map[string]any)["enabled"], "the rest of the values survive the merge")
}

func TestPrometheusRemoteWritesWithBasicAuthenticationByReferenceAndWithoutCredentials(t *testing.T) {
	basic, err := withRemoteWrite(monitoringPrometheusValues, &externalSink{
		url: "https://vm.rungeass.test/insert/41/prometheus/api/v1/write", auth: observability.AuthBasic, secret: observability.MetricsCredentialsSecret,
	})
	require.NoError(t, err)
	write := remoteWritesOf(t, basic)[0]
	require.Equal(t, map[string]any{
		"username": map[string]any{"name": observability.MetricsCredentialsSecret, "key": observability.KeyUsername},
		"password": map[string]any{"name": observability.MetricsCredentialsSecret, "key": observability.KeyPassword},
	}, write["basicAuth"])
	require.NotContains(t, write, "headers", "no tenant, no header: this store has the tenant in its path")
	require.NotContains(t, write, "authorization")

	open, err := withRemoteWrite(monitoringPrometheusValues, &externalSink{url: "http://vm.observability.svc:8428/api/v1/write"})
	require.NoError(t, err)
	write = remoteWritesOf(t, open)[0]
	require.NotContains(t, write, "basicAuth")
	require.NotContains(t, write, "authorization")
}

func TestTheShipperHasOneOutputWithoutAnExternalStore(t *testing.T) {
	values := logShipperValues(nil, nil)
	require.Equal(t, 1, strings.Count(values, "[OUTPUT]"))
	parsed := chartValues(t, values)
	require.NotContains(t, parsed, "env")
	require.NotContains(t, parsed, "podAnnotations")
}

func TestTheShipperSendsTheSameLinesToTheExternalStoreWithItsTenantAndCredentialsByReference(t *testing.T) {
	sink := &externalSink{
		url: "https://logs.rungeass.test/vl/", headers: observability.LogsHeaders(&geassv1alpha1.GeassExternalLogsSpec{AccountID: 41, ProjectID: 2}),
		auth: observability.AuthBasic, secret: observability.LogsCredentialsSecret, secretVersion: "12",
	}
	parsed := chartValues(t, logShipperValues([]string{"clinic-production"}, sink))
	outputs := parsed["config"].(map[string]any)["outputs"].(string)

	require.Equal(t, 2, strings.Count(outputs, "[OUTPUT]"), "the store in the cluster keeps its output")
	require.Contains(t, outputs, "Host "+platform.LogStoreServiceName+"."+platform.LoggingTargetNS+".svc")
	external := outputs[strings.LastIndex(outputs, "[OUTPUT]"):]
	for _, line := range []string{
		"Name http", "Match kube.*", "Host logs.rungeass.test", "Port 443",
		"URI /vl" + logStoreInsertURI, "Format json_lines", "Json_date_key date",
		"tls On", "tls.verify On", "Header AccountID 41", "Header ProjectID 2",
		"http_User ${GEASS_EXTERNAL_LOGS_USERNAME}", "http_Passwd ${GEASS_EXTERNAL_LOGS_PASSWORD}", "Retry_Limit 5",
	} {
		require.Contains(t, external, "\n    "+line+"\n", line)
	}
	require.NotContains(t, external, "Authorization")

	require.Equal(t, []any{
		map[string]any{"name": "GEASS_EXTERNAL_LOGS_PASSWORD", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": observability.LogsCredentialsSecret, "key": "password"}}},
		map[string]any{"name": "GEASS_EXTERNAL_LOGS_USERNAME", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": observability.LogsCredentialsSecret, "key": "username"}}},
	}, parsed["env"])
	require.Equal(t, map[string]any{"geass.dev/external-logs-credentials": "12"}, parsed["podAnnotations"],
		"new credentials start new pods: the shipper reads its environment once")
	require.Contains(t, parsed["config"].(map[string]any)["inputs"].(string), "*_clinic-production_*",
		"a project that requires node tags is not collected, so it is not sent outside either")
}

func TestTheShipperSendsABearerTokenToAPlainHTTPStoreOnItsPort(t *testing.T) {
	sink := &externalSink{url: "http://victorialogs.observability.svc:9428", auth: observability.AuthBearer, secret: observability.LogsCredentialsSecret, secretVersion: "3"}
	parsed := chartValues(t, logShipperValues(nil, sink))
	outputs := parsed["config"].(map[string]any)["outputs"].(string)
	external := outputs[strings.LastIndex(outputs, "[OUTPUT]"):]

	require.Contains(t, external, "\n    Port 9428\n")
	require.Contains(t, external, "\n    URI "+logStoreInsertURI+"\n")
	require.NotContains(t, external, "tls On")
	require.Contains(t, external, "\n    Header Authorization Bearer ${GEASS_EXTERNAL_LOGS_TOKEN}\n")
	require.Equal(t, []any{map[string]any{"name": "GEASS_EXTERNAL_LOGS_TOKEN", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": observability.LogsCredentialsSecret, "key": "token"}}}}, parsed["env"])
}

func monitoredLoggingCluster() *geassv1alpha1.GeassCluster {
	on := true
	cluster := loggingCluster(true, 3)
	cluster.Spec.Addons.Monitoring.Enabled = &on
	return cluster
}

func TestAnExternalStoreIsSentToWithItsCredentialsCopiedBesideTheAddOns(t *testing.T) {
	r, c := longhornTestReconciler(t,
		externalPlatformConfig(rungeassExternal()),
		externalCredentials(testCredentialRef, map[string]string{"token": testMetricsToken, "unrelated": "kept out of the copy"}),
		externalCredentials("rungeass-logs", map[string]string{"username": "acme", "password": testLogsPassword}),
	)
	ctx := context.Background()
	_, _, err := r.reconcileAddons(ctx, monitoredLoggingCluster())
	require.NoError(t, err)

	monitoring, err := getHelmChartNamed(c, platform.MonitoringChartName)
	require.NoError(t, err)
	writes := remoteWritesOf(t, monitoring.Spec.ValuesContent)
	require.Len(t, writes, 1)
	require.Equal(t, "https://metrics.rungeass.test/api/v1/push", writes[0]["url"])
	require.Equal(t, map[string]any{"X-Scope-OrgID": "rg-acme"}, writes[0]["headers"])
	require.NotContains(t, monitoring.Spec.ValuesContent, testMetricsToken, "the token is never in the chart's values")

	shipper, err := getHelmChartNamed(c, platform.LogShipperChartName)
	require.NoError(t, err)
	require.Contains(t, shipper.Spec.ValuesContent, "Host logs.rungeass.test")
	require.Contains(t, shipper.Spec.ValuesContent, "Header AccountID 41")
	require.NotContains(t, shipper.Spec.ValuesContent, testLogsPassword, "the password is never in the chart's values")
	require.NotContains(t, shipper.Spec.ValuesContent, "acme\n")

	metricsCopy := &corev1.Secret{}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: observability.MetricsCredentialsSecret, Namespace: platform.MonitoringTargetNS}, metricsCopy))
	require.Equal(t, map[string][]byte{"token": []byte(testMetricsToken)}, metricsCopy.Data, "only the key in use is copied")
	require.Equal(t, platform.ManagedByValue, metricsCopy.Labels[platform.LabelManagedBy])
	logsCopy := &corev1.Secret{}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: observability.LogsCredentialsSecret, Namespace: platform.LoggingTargetNS}, logsCopy))
	require.Equal(t, map[string][]byte{"username": []byte("acme"), "password": []byte(testLogsPassword)}, logsCopy.Data)
	require.Contains(t, shipper.Spec.ValuesContent, `geass.dev/external-logs-credentials: "`+logsCopy.ResourceVersion+`"`)

	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.MonitoringTargetNS}, &corev1.Namespace{}),
		"the namespace is made if the chart has not made it yet, so the first install already sends")
}

func TestNewCredentialsAreCopiedAndStartTheShipperAgain(t *testing.T) {
	logs := externalCredentials("rungeass-logs", map[string]string{"token": "first"})
	r, c := longhornTestReconciler(t, externalPlatformConfig(&geassv1alpha1.GeassExternalObservabilitySpec{
		Logs: &geassv1alpha1.GeassExternalLogsSpec{PushURL: "https://logs.rungeass.test", CredentialRef: &corev1.LocalObjectReference{Name: "rungeass-logs"}},
	}), logs)
	ctx := context.Background()
	r.reconcileLogging(ctx, loggingCluster(true, 3))
	before, err := getHelmChartNamed(c, platform.LogShipperChartName)
	require.NoError(t, err)

	r.reconcileLogging(ctx, loggingCluster(true, 3))
	again, err := getHelmChartNamed(c, platform.LogShipperChartName)
	require.NoError(t, err)
	require.Equal(t, before.Spec.ValuesContent, again.Spec.ValuesContent, "nothing changed, so the shipper is not restarted")

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(logs), logs))
	logs.Data["token"] = []byte("second")
	require.NoError(t, c.Update(ctx, logs))
	r.reconcileLogging(ctx, loggingCluster(true, 3))

	after, err := getHelmChartNamed(c, platform.LogShipperChartName)
	require.NoError(t, err)
	require.NotEqual(t, before.Spec.ValuesContent, after.Spec.ValuesContent, "the annotation moved, so new pods read the new token")
	copied := &corev1.Secret{}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: observability.LogsCredentialsSecret, Namespace: platform.LoggingTargetNS}, copied))
	require.Equal(t, "second", string(copied.Data["token"]))
}

func TestCredentialsThatCannotBeReadLeaveTheStoreOutAndTheClustersOwnMonitoringAlone(t *testing.T) {
	for name, objects := range map[string][]client.Object{
		"missing Secret":   {externalPlatformConfig(rungeassExternal())},
		"ambiguous Secret": {externalPlatformConfig(rungeassExternal()), externalCredentials(testCredentialRef, map[string]string{"token": "t", "username": "u", "password": "p"})},
	} {
		t.Run(name, func(t *testing.T) {
			r, c := longhornTestReconciler(t, objects...)
			_, _, err := r.reconcileAddons(context.Background(), monitoredLoggingCluster())
			require.NoError(t, err, "a store named wrongly is never a reason the cluster is not ready")

			monitoring, err := getHelmChartNamed(c, platform.MonitoringChartName)
			require.NoError(t, err)
			require.Equal(t, monitoringLiteValues, monitoring.Spec.ValuesContent, "Prometheus is installed as if no store were named")
			shipper, err := getHelmChartNamed(c, platform.LogShipperChartName)
			require.NoError(t, err)
			require.Equal(t, logShipperValues(nil, nil), shipper.Spec.ValuesContent)
		})
	}
}

func TestATenantHeaderPrometheusSetsItselfLeavesTheStoreOut(t *testing.T) {
	external := rungeassExternal()
	external.Metrics.TenantHeader = "Authorization"
	r, c := longhornTestReconciler(t, externalPlatformConfig(external), externalCredentials(testCredentialRef, map[string]string{"token": testMetricsToken}))
	_, _, err := r.reconcileAddons(context.Background(), monitoredLoggingCluster())
	require.NoError(t, err)
	monitoring, err := getHelmChartNamed(c, platform.MonitoringChartName)
	require.NoError(t, err)
	require.Equal(t, monitoringLiteValues, monitoring.Spec.ValuesContent, "a configuration Prometheus refuses would stop all of its scraping")
}

func TestNoPlatformConfigurationInstallsTheAddOnsExactlyAsBefore(t *testing.T) {
	r, c := longhornTestReconciler(t)
	_, _, err := r.reconcileAddons(context.Background(), monitoredLoggingCluster())
	require.NoError(t, err)
	monitoring, err := getHelmChartNamed(c, platform.MonitoringChartName)
	require.NoError(t, err)
	require.Equal(t, monitoringLiteValues, monitoring.Spec.ValuesContent)
	shipper, err := getHelmChartNamed(c, platform.LogShipperChartName)
	require.NoError(t, err)
	require.Equal(t, logShipperValues(nil, nil), shipper.Spec.ValuesContent)
	err = c.Get(context.Background(), client.ObjectKey{Name: observability.MetricsCredentialsSecret, Namespace: platform.MonitoringTargetNS}, &corev1.Secret{})
	require.True(t, apierrors.IsNotFound(err))
}

func TestRemovingTheStoreRemovesItsRemoteWriteAndTheCopyOfItsCredentials(t *testing.T) {
	config := externalPlatformConfig(rungeassExternal())
	r, c := longhornTestReconciler(t, config,
		externalCredentials(testCredentialRef, map[string]string{"token": testMetricsToken}),
		externalCredentials("rungeass-logs", map[string]string{"token": "logs"}))
	ctx := context.Background()
	_, _, err := r.reconcileAddons(ctx, monitoredLoggingCluster())
	require.NoError(t, err)

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(config), config))
	config.Spec.Observability = nil
	require.NoError(t, c.Update(ctx, config))
	_, _, err = r.reconcileAddons(ctx, monitoredLoggingCluster())
	require.NoError(t, err)

	monitoring, err := getHelmChartNamed(c, platform.MonitoringChartName)
	require.NoError(t, err)
	require.Equal(t, monitoringLiteValues, monitoring.Spec.ValuesContent)
	for namespace, name := range map[string]string{platform.MonitoringTargetNS: observability.MetricsCredentialsSecret, platform.LoggingTargetNS: observability.LogsCredentialsSecret} {
		err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, &corev1.Secret{})
		require.True(t, apierrors.IsNotFound(err), "%s/%s: %v", namespace, name, err)
	}
}

func TestASecretOfTheSameNameThatGeassDidNotMakeIsNeverDeleted(t *testing.T) {
	theirs := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: observability.MetricsCredentialsSecret, Namespace: platform.MonitoringTargetNS}}
	r, c := longhornTestReconciler(t, theirs)
	require.NoError(t, r.removeCredentialsCopy(context.Background(), platform.MonitoringTargetNS, observability.MetricsCredentialsSecret))
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(theirs), &corev1.Secret{}))
}

func TestOnlyTheStoresCredentialsAndTheirCopiesAskForAReconcile(t *testing.T) {
	r, _ := longhornTestReconciler(t, replicaTestCluster(false), externalPlatformConfig(rungeassExternal()))
	ctx := context.Background()
	secret := func(namespace, name string) *corev1.Secret {
		return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	}

	require.Len(t, r.clustersForExternalCredentials(ctx, secret(platform.SystemNamespace, testCredentialRef)), 1)
	require.Len(t, r.clustersForExternalCredentials(ctx, secret(platform.SystemNamespace, "rungeass-logs")), 1)
	require.Len(t, r.clustersForExternalCredentials(ctx, secret(platform.MonitoringTargetNS, observability.MetricsCredentialsSecret)), 1)
	require.Len(t, r.clustersForExternalCredentials(ctx, secret(platform.LoggingTargetNS, observability.LogsCredentialsSecret)), 1)

	require.Empty(t, r.clustersForExternalCredentials(ctx, secret(platform.SystemNamespace, "geass-dashboard-auth")), "sessions are written all day")
	require.Empty(t, r.clustersForExternalCredentials(ctx, secret("shop-production", testCredentialRef)), "only the system namespace's")
	require.Empty(t, r.clustersForExternalCredentials(ctx, secret(platform.MonitoringTargetNS, observability.LogsCredentialsSecret)))
}

func TestOnlyAChangeToObservabilityInThePlatformConfigurationAsksForAReconcile(t *testing.T) {
	changed := externalObservabilityChanged()
	before := externalPlatformConfig(rungeassExternal())
	unrelated := before.DeepCopy()
	unrelated.Spec.RootDomain = "example.com"
	require.False(t, changed.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: unrelated}), "the domain is someone else's to act on")

	rotated := before.DeepCopy()
	rotated.Spec.Observability.External.Metrics.Tenant = "rg-other"
	require.True(t, changed.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: rotated}))

	other := rotated.DeepCopy()
	other.Name = "not-the-platform"
	require.False(t, changed.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: other}))
	require.True(t, changed.Delete(event.DeleteEvent{Object: before}))
	require.True(t, changed.Create(event.CreateEvent{Object: before}))
	plain := before.DeepCopy()
	plain.Spec.Observability = nil
	require.False(t, changed.Create(event.CreateEvent{Object: plain}), "a configuration that names no store has nothing to send")
}
