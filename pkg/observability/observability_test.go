package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func credentialsSecret(name string, data map[string]string) *corev1.Secret {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "geass-system"}, Data: map[string][]byte{}}
	for key, value := range data {
		secret.Data[key] = []byte(value)
	}
	return secret
}

func configWith(external *geassv1alpha1.GeassExternalObservabilitySpec) *geassv1alpha1.GeassPlatformConfig {
	return &geassv1alpha1.GeassPlatformConfig{Spec: geassv1alpha1.GeassPlatformConfigSpec{
		Observability: &geassv1alpha1.GeassObservabilitySpec{External: external},
	}}
}

func TestNothingIsExternalUnlessTheConfigurationSaysSo(t *testing.T) {
	for name, config := range map[string]*geassv1alpha1.GeassPlatformConfig{
		"no configuration":     nil,
		"no observability":     {},
		"no external":          {Spec: geassv1alpha1.GeassPlatformConfigSpec{Observability: &geassv1alpha1.GeassObservabilitySpec{}}},
		"external, no signals": configWith(&geassv1alpha1.GeassExternalObservabilitySpec{}),
	} {
		t.Run(name, func(t *testing.T) {
			require.Nil(t, Metrics(config))
			require.Nil(t, Logs(config))
			metrics, err := MetricsQuery(context.Background(), nil, "geass-system", config)
			require.NoError(t, err)
			require.Nil(t, metrics, "the dashboard keeps reading the cluster's Prometheus")
			logs, err := LogsQuery(context.Background(), nil, "geass-system", config)
			require.NoError(t, err)
			require.Nil(t, logs, "the dashboard keeps reading the cluster's log store")
		})
	}
}

func TestAStoreIsOnlyWrittenToWhenOnlyItsWriteAddressIsSet(t *testing.T) {
	config := configWith(&geassv1alpha1.GeassExternalObservabilitySpec{
		Metrics: &geassv1alpha1.GeassExternalMetricsSpec{RemoteWriteURL: "https://mimir.example.com/api/v1/push"},
		Logs:    &geassv1alpha1.GeassExternalLogsSpec{PushURL: "https://logs.example.com"},
	})
	metrics, err := MetricsQuery(context.Background(), nil, "geass-system", config)
	require.NoError(t, err)
	require.Nil(t, metrics, "without a query address the dashboard still reads the cluster's own Prometheus")
	logs, err := LogsQuery(context.Background(), nil, "geass-system", config)
	require.NoError(t, err)
	require.Nil(t, logs)
}

func TestAddressesMustBePlainHTTPOrHTTPS(t *testing.T) {
	require.NoError(t, CheckURL("https://mimir.example.com/prometheus", false))
	require.NoError(t, CheckURL("http://vmselect.observability.svc:8481/select/12/prometheus", false))
	require.NoError(t, CheckURL("https://vm.example.com/api/v1/write?extra_label=cluster=a", true), "remote write may take a query")

	for raw, reason := range map[string]string{
		"ftp://example.com": "only http and https",
		"https://":          "a host is needed",
		"https://user:" + "secret@mimir.example.com/push":  "a password in the address would be written into the add-on's settings",
		"https://example.com/a b":                          "whitespace",
		"https://example.com/${GEASS_EXTERNAL_LOGS_TOKEN}": "the shipper would put its own secret into the address",
		"https://example.com/prometheus?x=1":               "a base address gets a path added to it",
		"https://example.com/prometheus#top":               "no fragment",
		"mimir.example.com":                                "a URL, with its scheme",
	} {
		require.Error(t, CheckURL(raw, false), reason)
	}
}

func TestTheMetricsTenantGoesInXScopeOrgIDUnlessAnotherHeaderIsNamed(t *testing.T) {
	headers, err := MetricsHeaders(&geassv1alpha1.GeassExternalMetricsSpec{Tenant: "rg-acme"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"X-Scope-OrgID": "rg-acme"}, headers)

	headers, err = MetricsHeaders(&geassv1alpha1.GeassExternalMetricsSpec{Tenant: "rg-acme", TenantHeader: "THANOS-TENANT"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"THANOS-TENANT": "rg-acme"}, headers, "as written")

	headers, err = MetricsHeaders(&geassv1alpha1.GeassExternalMetricsSpec{TenantHeader: "THANOS-TENANT"})
	require.NoError(t, err)
	require.Empty(t, headers, "no tenant, no header: the store takes it from the URL or the credentials")
}

func TestATenantHeaderThatPrometheusSetsItselfIsRefused(t *testing.T) {
	for _, name := range []string{"Authorization", "authorization", "Content-Type", "X-Prometheus-Remote-Write-Version", "User-Agent", "Host", "AccountID"} {
		_, err := MetricsHeaders(&geassv1alpha1.GeassExternalMetricsSpec{Tenant: "rg-acme", TenantHeader: name})
		require.Error(t, err, "%s: Prometheus refuses the whole configuration, and stops scraping", name)
	}
}

func TestTheLogsTenantIsAlwaysSentAsAccountIDAndProjectID(t *testing.T) {
	require.Equal(t, map[string]string{"AccountID": "42", "ProjectID": "7"}, LogsHeaders(&geassv1alpha1.GeassExternalLogsSpec{AccountID: 42, ProjectID: 7}))
	require.Equal(t, map[string]string{"AccountID": "0", "ProjectID": "0"}, LogsHeaders(&geassv1alpha1.GeassExternalLogsSpec{}),
		"the default tenant is said out loud, not left to whatever sits in front of the store")
	require.Equal(t, map[string]string{"AccountID": "4294967295", "ProjectID": "0"}, LogsHeaders(&geassv1alpha1.GeassExternalLogsSpec{AccountID: 4294967295}))
}

func TestCredentialsAreATokenOrAUsernameAndPassword(t *testing.T) {
	bearer, err := CredentialsOf(credentialsSecret("c", map[string]string{"token": "abc\n"}))
	require.NoError(t, err)
	require.Equal(t, AuthBearer, bearer.Kind())
	require.Equal(t, "Bearer abc", bearer.Authorization(), "a token saved from a file with its newline still works")
	require.Equal(t, map[string][]byte{"token": []byte("abc")}, bearer.Data())

	basic, err := CredentialsOf(credentialsSecret("c", map[string]string{"username": "acme", "password": "s3" + "cret"}))
	require.NoError(t, err)
	require.Equal(t, AuthBasic, basic.Kind())
	require.Equal(t, "Basic "+"YWNtZTpz"+"M2NyZXQ=", basic.Authorization())
	require.Equal(t, map[string][]byte{"username": []byte("acme"), "password": []byte("s3" + "cret")}, basic.Data())

	require.Equal(t, AuthNone, Credentials{}.Kind())
	require.Empty(t, Credentials{}.Authorization())
	require.Nil(t, Credentials{}.Data())
}

func TestCredentialsThatSayTwoThingsOrNothingAreRefused(t *testing.T) {
	for name, data := range map[string]map[string]string{
		"both":                {"token": "abc", "username": "acme", "password": "s3" + "cret"},
		"username only":       {"username": "acme"},
		"password only":       {"password": "s3" + "cret"},
		"nothing":             {"other": "x"},
		"a header in a token": {"token": "abc\r\nX-Scope-OrgID: someone-else"},
	} {
		_, err := CredentialsOf(credentialsSecret("c", data))
		require.ErrorIs(t, err, ErrInvalidCredentials, name)
	}
}

func fakeReader(t *testing.T, objects ...*corev1.Secret) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	builder := fake.NewClientBuilder().WithScheme(scheme)
	for _, object := range objects {
		builder = builder.WithObjects(object)
	}
	return builder.Build()
}

func TestTheQueryCarriesTheTenantAndTheCredentials(t *testing.T) {
	reader := fakeReader(t, credentialsSecret("rungeass-observability", map[string]string{"token": "t0ken"}))
	ref := &corev1.LocalObjectReference{Name: "rungeass-observability"}
	config := configWith(&geassv1alpha1.GeassExternalObservabilitySpec{
		Metrics: &geassv1alpha1.GeassExternalMetricsSpec{QueryURL: "https://mimir.example.com/prometheus/", Tenant: "rg-acme", CredentialRef: ref},
		Logs:    &geassv1alpha1.GeassExternalLogsSpec{QueryURL: "https://logs.example.com", AccountID: 12, ProjectID: 3, CredentialRef: ref},
	})

	metrics, err := MetricsQuery(context.Background(), reader, "geass-system", config)
	require.NoError(t, err)
	require.Equal(t, "https://mimir.example.com/prometheus", metrics.URL, "the trailing slash is dropped, so the API path is added once")
	require.Equal(t, "rg-acme", metrics.Header.Get("X-Scope-OrgID"))
	require.Equal(t, "Bearer t0ken", metrics.Header.Get("Authorization"))

	logs, err := LogsQuery(context.Background(), reader, "geass-system", config)
	require.NoError(t, err)
	require.Equal(t, "https://logs.example.com", logs.URL)
	require.Equal(t, "12", logs.Header.Get("AccountID"))
	require.Equal(t, "3", logs.Header.Get("ProjectID"))
	require.Equal(t, "Bearer t0ken", logs.Header.Get("Authorization"))
}

func TestAQueryWhoseCredentialsAreMissingIsAnErrorAndNotTheClustersOwnStore(t *testing.T) {
	config := configWith(&geassv1alpha1.GeassExternalObservabilitySpec{
		Metrics: &geassv1alpha1.GeassExternalMetricsSpec{QueryURL: "https://mimir.example.com/prometheus", CredentialRef: &corev1.LocalObjectReference{Name: "missing"}},
	})
	query, err := MetricsQuery(context.Background(), fakeReader(t), "geass-system", config)
	require.Nil(t, query)
	require.True(t, apierrors.IsNotFound(err), "%v", err)

	config.Spec.Observability.External.Metrics.CredentialRef = nil
	config.Spec.Observability.External.Metrics.TenantHeader = "Authorization"
	config.Spec.Observability.External.Metrics.Tenant = "rg-acme"
	_, err = MetricsQuery(context.Background(), fakeReader(t), "geass-system", config)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrInvalidCredentials))
}
