package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/platform"
)

const (
	managedTestProvider = "rungeass"
	managedTestValue    = "fixture-value"
)

// managedFixture is a cluster with everything managed mode keeps back set up, so that a write that got through would change
// something: an identity provider and organizations (one with a provider of its own), a dashboard domain, a Cloudflare
// connection, and a bucket for the copies of the cluster's state.
type managedFixture struct {
	orgIdPFixture
}

func newManagedFixture(t *testing.T, managed *geassv1alpha1.GeassPlatformManagedSpec) managedFixture {
	t.Helper()
	f := managedFixture{newOrgIdPFixture(t)}
	require.Equal(t, http.StatusOK, f.saveIdP(t, orgA).Code, "the organization's own provider is set up before managed mode is on")
	ctx := asSystem(context.Background())
	for _, object := range []client.Object{
		&geassv1alpha1.GeassCluster{
			ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace},
			Spec: geassv1alpha1.GeassClusterSpec{
				ServerURL: "https://control-plane.example.com:6443",
				EtcdSnapshots: &geassv1alpha1.GeassClusterEtcdSnapshotsSpec{
					Bucket: "fixture-bucket", CredentialsSecretRef: &corev1.LocalObjectReference{Name: platform.EtcdSnapshotCredentialsSecretName},
				},
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platform.EtcdSnapshotCredentialsSecretName, Namespace: platform.SystemNamespace},
			Data:       platform.EtcdSnapshotsCredentialsData(managedTestValue, managedTestValue),
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platform.PlatformCloudflareSecretName, Namespace: platform.SystemNamespace},
			Data:       map[string][]byte{platform.SecretKeyCloudflareAPIToken: []byte(managedTestValue), platform.SecretKeyCloudflareAccountID: []byte("acct")},
		},
	} {
		require.NoError(t, f.srv.Client.Create(ctx, object))
	}
	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) {
		c.Spec.RootDomain = "example.com"
		c.Spec.DashboardURL = "https://geass.example.com"
		c.Spec.CloudflareConnectionRef = &corev1.LocalObjectReference{Name: platform.PlatformCloudflareSecretName}
		c.Spec.CloudflareZoneID = "zone-1"
		c.Spec.Managed = managed
	})
	return f
}

// setManaged changes managed mode, the way the platform does: in the configuration object.
func (f managedFixture) setManaged(t *testing.T, managed *geassv1alpha1.GeassPlatformManagedSpec) {
	t.Helper()
	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) { c.Spec.Managed = managed })
}

// managedWrite is one write of the five things, as the dashboard sends it.
type managedWrite struct {
	name string
	path string
	form url.Values
	area string
	// network says that, with managed mode off, the write would call out (to Cloudflare), which a test does not allow.
	network bool
}

func (f managedFixture) writes() []managedWrite {
	return []managedWrite{
		{name: "save the identity provider", path: "/api/settings/auth/save", form: f.saveForm(nil), area: managedSignIn},
		{name: "switch how people sign in", path: "/api/settings/auth/mode", form: form("mode", platform.AuthModeLocal), area: managedSignIn},
		{name: "remove the identity provider", path: "/api/settings/auth/clear", form: form("confirm", "remove"), area: managedSignIn},
		{name: "the logout policy", path: "/api/settings/auth/logout-policy", form: form("revokeTokens", "off"), area: managedSignIn},
		{name: "organizations off", path: "/api/settings/auth/tenancy", form: form("enabled", "off"), area: managedSignIn},
		{name: "an organization's own provider: save", path: "/api/orgs/" + orgA + "/idp/save", form: form("displayName", "Acme again", "issuer", f.own.server.URL, "clientID", ssoTestClient, "roleClaim", "org_role", "roleMap", "owner=admin", "keepSecret", "on"), area: managedSignIn},
		{name: "an organization's own provider: turn on", path: "/api/orgs/" + orgA + "/idp/enable", form: form("enabled", "on"), area: managedSignIn},
		{name: "an organization's own provider: remove", path: "/api/orgs/" + orgA + "/idp/clear", form: form("confirm", orgA), area: managedSignIn},
		{name: "save the dashboard's domain", path: "/api/settings/domain/save", form: form("domain", "example.org", "subdomain", "dash"), area: managedDashboardDomain},
		{name: "check the dashboard's domain", path: "/api/settings/domain/verify", form: form(), area: managedDashboardDomain, network: true},
		{name: "connect Cloudflare", path: "/api/settings/cloudflare/save", form: form("apiToken", managedTestValue, "accountId", "acct", "zoneId", "zone-2", "zoneName", "example.org"), area: managedDashboardDomain, network: true},
		{name: "find Cloudflare zones", path: "/api/settings/cloudflare/discover", form: form("apiToken", managedTestValue), area: managedDashboardDomain, network: true},
		{name: "refresh Cloudflare zones", path: "/api/settings/cloudflare/refresh", form: form(), area: managedDashboardDomain, network: true},
		{name: "disconnect Cloudflare", path: "/api/settings/cloudflare/clear", form: form(), area: managedDashboardDomain},
		{name: "name a bucket for the cluster's state", path: "/api/settings/etcd-snapshots/save", form: form("bucket", "other-bucket", "accessKey", managedTestValue, "secretKey", managedTestValue), area: managedInstaller},
		{name: "stop naming the bucket", path: "/api/settings/etcd-snapshots/clear", form: form(), area: managedInstaller},
	}
}

// snapshot is everything the writes above could change, as JSON. The addresses of the test providers are replaced, because
// they differ from one fixture to the next.
func (f managedFixture) snapshot(t *testing.T) string {
	t.Helper()
	return f.snapshotOf(t, true)
}

// snapshotOf is snapshot, with or without the managed field itself, which is what two clusters that differ only in it do not share.
func (f managedFixture) snapshotOf(t *testing.T, withManaged bool) string {
	t.Helper()
	ctx := asSystem(context.Background())
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, f.srv.Client.Get(ctx, key(platform.HAReadinessName), &config))
	if !withManaged {
		config.Spec.Managed = nil
	}
	var cluster geassv1alpha1.GeassCluster
	require.NoError(t, f.srv.Client.Get(ctx, key(testClusterName), &cluster))
	var orgs geassv1alpha1.GeassOrganizationList
	require.NoError(t, f.srv.Client.List(ctx, &orgs))
	var secrets corev1.SecretList
	require.NoError(t, f.srv.Client.List(ctx, &secrets, client.InNamespace(platform.SystemNamespace)))
	versions := map[string]string{}
	for _, secret := range secrets.Items {
		versions[secret.Name] = secret.ResourceVersion + "/" + string(secret.Data[platform.PlatformAuthClientSecretKey])
	}
	raw, err := json.Marshal(map[string]any{"config": config, "cluster": cluster, "organizations": orgs.Items, "secrets": versions})
	require.NoError(t, err)
	return f.normalize(string(raw))
}

// idPChanged is the time an organization's provider was last changed, in Unix nanoseconds, which no two runs share.
var idPChanged = regexp.MustCompile(`"` + regexp.QuoteMeta(annotationIdPChanged) + `":"[0-9]+"`)

func (f managedFixture) normalize(text string) string {
	text = strings.ReplaceAll(text, f.idp.server.URL, "<provider>")
	text = strings.ReplaceAll(text, f.own.server.URL, "<organization-provider>")
	return idPChanged.ReplaceAllString(text, `"`+annotationIdPChanged+`":"<time>"`)
}

func (f managedFixture) asAdminPost(t *testing.T, write managedWrite) *httptest.ResponseRecorder {
	t.Helper()
	return f.asAdmin(t, postForm(write.path, write.form))
}

// noNetwork makes any call the server makes to another host fail the test.
func (f managedFixture) noNetwork(t *testing.T) *countingFailTransport {
	t.Helper()
	transport := &countingFailTransport{}
	f.srv.HTTPClient = &http.Client{Transport: transport}
	return transport
}

func decodeManagedError(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body
}

func TestManagedModeRefusesEveryWriteThePlatformKeeps(t *testing.T) {
	f := newManagedFixture(t, &geassv1alpha1.GeassPlatformManagedSpec{Enabled: true, Provider: managedTestProvider})
	network := f.noNetwork(t)
	before := f.snapshot(t)
	subjects := map[string]string{
		managedSignIn:          "Sign-in settings are managed by rungeass and cannot be changed here.",
		managedDashboardDomain: "The dashboard's domain and DNS are managed by rungeass and cannot be changed here.",
		managedInstaller:       "The backup of the cluster's state, which the installer sets up, is managed by rungeass and cannot be changed here.",
	}

	for _, write := range f.writes() {
		t.Run(write.name, func(t *testing.T) {
			rec := f.asAdminPost(t, write)

			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			body := decodeManagedError(t, rec)
			require.Equal(t, subjects[write.area], body["error"])
			require.Equal(t, managedCode, body["code"])
			require.Equal(t, write.area, body["area"])
			require.Equal(t, managedTestProvider, body["managedBy"])
			require.Equal(t, before, f.snapshot(t), "a refused write changed something")
		})
	}
	require.Zero(t, network.calls, "a refused write must not reach Cloudflare")
}

func TestManagedModeRefusesTheSameWritesThroughTheV1API(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) {
		c.Spec.Managed = &geassv1alpha1.GeassPlatformManagedSpec{Enabled: true, Provider: managedTestProvider}
	})
	token := f.workspaceToken(t, apitokens.RoleAdmin)
	before := f.config(t)

	for path, area := range map[string]string{
		"/api/v1/settings/auth/save":            managedSignIn,
		"/api/v1/settings/auth/mode":            managedSignIn,
		"/api/v1/settings/auth/clear":           managedSignIn,
		"/api/v1/settings/domain/save":          managedDashboardDomain,
		"/api/v1/settings/domain/verify":        managedDashboardDomain,
		"/api/v1/settings/cloudflare/clear":     managedDashboardDomain,
		"/api/v1/settings/etcd-snapshots/save":  managedInstaller,
		"/api/v1/settings/etcd-snapshots/clear": managedInstaller,
		"/api/v1/orgs/acme/idp/save":            managedSignIn,
	} {
		t.Run(path, func(t *testing.T) {
			rec := f.do(jsonRequest(token, http.MethodPost, path, map[string]any{"domain": "example.org", "mode": "local"}))

			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			body := decodeManagedError(t, rec)
			require.Equal(t, managedCode, body["code"], "the v1 API keeps the specific code")
			require.Equal(t, area, body["area"])
			require.Contains(t, body["error"], "managed by rungeass")
		})
	}
	require.Equal(t, before.Spec, f.config(t).Spec)
	require.Equal(t, before.ResourceVersion, f.config(t).ResourceVersion, "nothing was written")
}

func TestManagedModeNamesThePlatformWhenTheConfigurationDoesNot(t *testing.T) {
	f := newManagedFixture(t, &geassv1alpha1.GeassPlatformManagedSpec{Enabled: true})

	rec := f.asAdminPost(t, managedWrite{path: "/api/settings/domain/save", form: form("domain", "example.org", "subdomain", "dash")})

	require.Equal(t, http.StatusForbidden, rec.Code)
	body := decodeManagedError(t, rec)
	require.Equal(t, "The dashboard's domain and DNS are managed by the platform that runs this cluster and cannot be changed here.", body["error"])
	require.NotContains(t, body, "managedBy")
}

func TestManagedModeIsSwitchedOffByTheConfiguration(t *testing.T) {
	f := newManagedFixture(t, &geassv1alpha1.GeassPlatformManagedSpec{Enabled: true, Provider: managedTestProvider})
	write := managedWrite{path: "/api/settings/domain/save", form: form("domain", "example.org", "subdomain", "dash")}
	require.Equal(t, http.StatusForbidden, f.asAdminPost(t, write).Code)

	f.setManaged(t, &geassv1alpha1.GeassPlatformManagedSpec{Enabled: false, Provider: managedTestProvider})
	require.NotEqual(t, http.StatusForbidden, f.asAdminPost(t, write).Code, "off, with a provider named, is off")

	f.setManaged(t, nil)
	rec := f.asAdminPost(t, write)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "example.org", f.config(t).Spec.RootDomain, "the write went through")
}

func TestManagedModeOffAnswersExactlyAsBefore(t *testing.T) {
	type answer struct {
		status int
		body   string
	}
	run := func(t *testing.T, managed *geassv1alpha1.GeassPlatformManagedSpec) (answers []answer, after string, bootstrap string) {
		t.Helper()
		f := newManagedFixture(t, managed)
		f.noNetwork(t)
		for _, write := range f.writes() {
			if write.network {
				continue
			}
			rec := f.asAdminPost(t, write)
			answers = append(answers, answer{rec.Code, f.normalize(rec.Body.String())})
		}
		read := f.asAdmin(t, browserRequest(http.MethodGet, "/api/bootstrap"))
		require.Equal(t, http.StatusOK, read.Code)
		return answers, f.snapshotOf(t, false), f.normalize(read.Body.String())
	}

	withoutAnswers, withoutAfter, withoutBootstrap := run(t, nil)
	offAnswers, offAfter, offBootstrap := run(t, &geassv1alpha1.GeassPlatformManagedSpec{Enabled: false, Provider: managedTestProvider})

	require.NotEmpty(t, withoutAnswers)
	for _, a := range withoutAnswers {
		require.NotContains(t, a.body, managedCode)
		require.NotContains(t, a.body, "managed by")
	}
	require.Equal(t, withoutAnswers, offAnswers, "every write answers byte for byte as it does without the field")
	require.Equal(t, withoutAfter, offAfter, "and every object is left as it is without the field (the field itself aside)")
	require.NotContains(t, withoutBootstrap, `"managed"`, "an unmanaged cluster's bootstrap has no new key")
	// The platform's own configuration is echoed to the platform's administrators as it is, field included. What the dashboard
	// is told is platform.managed, and that is absent.
	echoed := `,"managed":{"provider":"` + managedTestProvider + `"}`
	require.Contains(t, offBootstrap, echoed)
	var told struct {
		Platform map[string]any `json:"platform"`
	}
	require.NoError(t, json.Unmarshal([]byte(offBootstrap), &told))
	require.NotContains(t, told.Platform, "managed")
	require.Equal(t, withoutBootstrap, strings.Replace(offBootstrap, echoed, "", 1), "byte for byte")
}

func TestManagedModeKeepsReadsAndEverythingElseWorking(t *testing.T) {
	f := newManagedFixture(t, &geassv1alpha1.GeassPlatformManagedSpec{Enabled: true, Provider: managedTestProvider})

	for _, path := range []string{"/api/settings/auth", "/api/settings/etcd-snapshots", "/api/settings/cloudflare", "/api/orgs", "/api/bootstrap"} {
		rec := f.asAdmin(t, browserRequest(http.MethodGet, path))
		require.Equal(t, http.StatusOK, rec.Code, "%s: %s", path, rec.Body.String())
	}
	require.Equal(t, f.idp.server.URL, f.view(t)["issuer"], "the sign-in settings are shown as they are")

	// What a platform does through the API goes on working: it makes and removes organizations and their workspaces.
	created := f.asAdmin(t, postForm("/api/orgs/create", form("displayName", "Gamma", "idpID", "org_gamma")))
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	for _, path := range []string{"/api/settings/registry/clear", "/api/settings/alerts/smtp/clear", "/api/settings/github/clear"} {
		rec := f.asAdmin(t, postForm(path, form("confirm", "x")))
		require.NotEqual(t, http.StatusForbidden, rec.Code, "%s is not one of the five: %s", path, rec.Body.String())
		require.NotContains(t, rec.Body.String(), managedCode)
	}
}

func TestManagedModeTellsThePlatformsAdministratorsInTheBootstrap(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeLocal)
	bootstrap := func(req *http.Request) map[string]any {
		t.Helper()
		rec := f.do(req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		platformInfo, ok := decodeBody(t, rec)["platform"].(map[string]any)
		require.True(t, ok)
		return platformInfo
	}
	asViewer := func() *http.Request {
		return withDashboardSession(t, f.srv, browserRequest(http.MethodGet, "/api/bootstrap"), "viewer", dashboardRoleViewer)
	}
	asAdmin := func() *http.Request {
		return withAdminSession(t, f.srv, browserRequest(http.MethodGet, "/api/bootstrap"))
	}
	require.NotContains(t, bootstrap(asAdmin()), "managed")

	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) {
		c.Spec.Managed = &geassv1alpha1.GeassPlatformManagedSpec{Enabled: true, Provider: managedTestProvider}
	})

	want := map[string]any{
		"enabled": true, "provider": managedTestProvider,
		"locked": []any{"installer", "upgrades", "k3sVersion", "signIn", "dashboardDomain"},
	}
	require.Equal(t, want, bootstrap(asAdmin())["managed"])
	// A read-only session is not told: its bootstrap never reads the platform's configuration (see
	// TestViewerBootstrapDoesNotReadPlatformConfig), and its pages are read-only anyway.
	require.NotContains(t, bootstrap(asViewer()), "managed")

	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) { c.Spec.Managed.Provider = "" })
	require.Equal(t, map[string]any{"enabled": true, "locked": want["locked"]}, bootstrap(asAdmin())["managed"])

	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) { c.Spec.Managed.Enabled = false })
	require.NotContains(t, bootstrap(asAdmin()), "managed")
}

// failingPlatformConfig is a client that cannot read the platform configuration.
type failingPlatformConfig struct {
	client.Client
}

func (c failingPlatformConfig) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*geassv1alpha1.GeassPlatformConfig); ok {
		return errors.New("the API server did not answer")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestManagedModeRefusesWhenItCannotTellWhetherItIsOn(t *testing.T) {
	f := newManagedFixture(t, nil)
	before := f.snapshot(t)
	f.srv.Client = failingPlatformConfig{f.srv.Client}

	rec := f.asAdminPost(t, managedWrite{path: "/api/settings/domain/save", form: form("domain", "example.org", "subdomain", "dash")})

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	require.Contains(t, decodeManagedError(t, rec)["error"], "could not check whether the platform manages this setting")
	f.srv.Client = f.srv.Client.(failingPlatformConfig).Client
	require.Equal(t, before, f.snapshot(t))

	// A write that is not one of the five does not read the configuration for this, and is not held up by it.
	f.srv.Client = failingPlatformConfig{f.srv.Client}
	other := f.asAdmin(t, postForm("/api/orgs/create", form("displayName", "Gamma", "idpID", "org_gamma")))
	require.NotContains(t, other.Body.String(), "could not check whether the platform manages")
}

func TestManagedAreaOf(t *testing.T) {
	for path, want := range map[string]string{
		"/settings/auth":                     managedSignIn,
		"/settings/auth/save":                managedSignIn,
		"/settings/auth/tenancy":             managedSignIn,
		"/orgs/org-a/idp/save":               managedSignIn,
		"/orgs/org-a/idp/enable":             managedSignIn,
		"/orgs/org-a/idp/clear":              managedSignIn,
		"/settings/domain/save":              managedDashboardDomain,
		"/settings/domain/verify":            managedDashboardDomain,
		"/settings/cloudflare/save":          managedDashboardDomain,
		"/settings/cloudflare/refresh":       managedDashboardDomain,
		"/settings/etcd-snapshots/save":      managedInstaller,
		"/settings/etcd-snapshots/clear":     managedInstaller,
		"/orgs/create":                       "",
		"/orgs/org-a/update":                 "",
		"/orgs/org-a/delete":                 "",
		"/orgs/org-a/workspaces/create":      "",
		"/orgs/org-a":                        "",
		"/settings/authority":                "",
		"/settings/domains":                  "",
		"/settings/alerts/smtp/save":         "",
		"/settings/registry/save":            "",
		"/settings/github/save":              "",
		"/platform/default-region":           "",
		"/tokens/create":                     "",
		"/login":                             "",
		"/projects/payments/settings/save":   "",
		"/pools/create":                      "",
		"/servers/create":                    "",
		"/connections/create":                "",
		"/cloud-connections/create":          "",
		"/auth/device/approve":               "",
		"/ha-readiness/check":                "",
		"/apps/api/settings/domain":          "",
		"/projects/idp/settings/auth/save":   "",
		"/orgs/org-a/workspaces/idp/delete":  "",
		"/nodes/worker-1/placement":          "",
		"/settings/auth-and-then-some/clear": "",
	} {
		area, ok := managedAreaOf(path)
		require.Equal(t, want != "", ok, path)
		require.Equal(t, want, area, path)
	}
}

func TestEveryManagedAreaIsWordedAndTold(t *testing.T) {
	require.Equal(t, []string{"installer", "upgrades", "k3sVersion", "signIn", "dashboardDomain"}, managedAreas)
	for _, area := range managedAreas {
		require.NotEmpty(t, managedSubjects[area], area)
		require.True(t, strings.HasSuffix(managedSubjects[area], " is") || strings.HasSuffix(managedSubjects[area], " are"), area)
	}
	require.Len(t, managedSubjects, len(managedAreas))
}
