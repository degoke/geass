package platform

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestCanonicalRepositorySpellsOneRepositoryOneWay(t *testing.T) {
	for image, want := range map[string]string{
		"nginx":                                "docker.io/library/nginx",
		"nginx:alpine":                         "docker.io/library/nginx",
		"library/nginx@sha256:abc":             "docker.io/library/nginx",
		"docker.io/library/nginx:1":            "docker.io/library/nginx",
		"index.docker.io/acme/api":             "docker.io/acme/api",
		"registry-1.docker.io/acme/api:2":      "docker.io/acme/api",
		"GHCR.io/Acme/Builds:latest":           "ghcr.io/acme/builds",
		"ghcr.io:443/acme/builds":              "ghcr.io/acme/builds",
		"registry.example.com:5000/a/b:tag":    "registry.example.com:5000/a/b",
		"localhost/acme/api@sha256:0123":       "localhost/acme/api",
		"  ghcr.io/acme/builds/shop/api:v1/  ": "ghcr.io/acme/builds/shop/api",
		"":                                     "",
	} {
		require.Equal(t, want, CanonicalRepository(image), image)
	}
}

func TestAnOrganizationsImagesGoUnderItsRepositoryAServiceAtATime(t *testing.T) {
	require.Equal(t, "ghcr.io/acme/builds/shop/api", NamespacedImage("ghcr.io/acme/builds", "shop", "api"))
	require.Equal(t, "harbor.example.com/org-1/shop/api", NamespacedImage("harbor.example.com/org-1/", "shop", "api"))
	require.Equal(t, "registry.example.com:5000/acme/shop/api", NamespacedImage("registry.example.com:5000/acme", "shop", "api"), "a port is not a tag")
	require.Equal(t, "ghcr.io/acme/builds/shop/api:edge", NamespacedImage("ghcr.io/acme/builds:edge", "shop", "api"), "the organization's tag is kept")
}

func org(name, registry string) geassv1alpha1.GeassOrganization {
	o := geassv1alpha1.GeassOrganization{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: SystemNamespace}}
	if registry != "" {
		o.Spec.Registry = &geassv1alpha1.GeassPlatformRegistrySpec{Repository: registry}
	}
	return o
}

func TestEachPartOfARegistryBelongsToOneOrganizationOrToThePlatform(t *testing.T) {
	claims := NewRegistryClaims("registry.example.com/platform", []geassv1alpha1.GeassOrganization{
		org("org-a", "ghcr.io/acme/builds"),
		org("org-b", "registry.example.com/platform/org-b"),
		org("org-c", ""),
	})

	for name, tc := range map[string]struct {
		org, image string
		allowed    bool
	}{
		"its own registry":                                 {"org-a", "ghcr.io/acme/builds/shop/api@sha256:1", true},
		"its own registry, spelled otherwise":              {"org-a", "GHCR.IO:443/acme/builds/shop/api:v1", true},
		"another organization's registry":                  {"org-c", "ghcr.io/acme/builds/shop/api@sha256:1", false},
		"the platform's repository":                        {"org-a", "registry.example.com/platform@sha256:1", false},
		"below the platform's repository":                  {"org-a", "registry.example.com/platform/web", false},
		"its own path in the platform's repository":        {"org-c", "registry.example.com/platform/org-c/shop/api", true},
		"another's path in the platform's repository":      {"org-c", "registry.example.com/platform/org-b/shop/api", false},
		"its own registry inside the platform's":           {"org-b", "registry.example.com/platform/org-b/shop/api", true},
		"a public image nobody claims":                     {"org-a", "nginx:alpine", true},
		"another repository of the platform's registry":    {"org-a", "registry.example.com/elsewhere/api", true},
		"a repository whose name only starts like a claim": {"org-c", "ghcr.io/acme/builds-old/api", true},
		"a project that cannot be found owns nothing":      {"", "registry.example.com/platform/org-c/x", false},
		"nor may it take the platform's":                   {"", "registry.example.com/platform", false},
		"but it may run a public image":                    {"", "nginx", true},
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.allowed, claims.Allows(tc.org, tc.image))
		})
	}
}

func TestARegistryThatOverlapsAnothersIsNobodys(t *testing.T) {
	claims := NewRegistryClaims("registry.example.com/team/platform", []geassv1alpha1.GeassOrganization{
		org("org-victim", "ghcr.io/victim/builds"),
		org("org-squatter", "ghcr.io/victim/builds/shop"),
		org("org-platform-squatter", "registry.example.com/team/platform/web"),
		org("org-around-platform", "registry.example.com/team"),
		org("org-fine", "ghcr.io/fine"),
	})

	// Neither of two overlapping registries can be used, by either organization: the one that came second gets nothing
	// out of claiming inside the first, and nobody may run what is in either.
	require.True(t, claims.OwnContested("org-victim"))
	require.True(t, claims.OwnContested("org-squatter"))
	require.False(t, claims.Allows("org-squatter", "ghcr.io/victim/builds/shop/api"))
	require.False(t, claims.Allows("org-victim", "ghcr.io/victim/builds/shop/api"))
	require.False(t, claims.Allows("org-squatter", "ghcr.io/victim/builds/web/api"))

	// A registry inside the platform's repository, outside the organization's own path in it, or around it, is not the
	// organization's either, and what the platform keeps there stays the platform's.
	require.True(t, claims.OwnContested("org-platform-squatter"))
	require.False(t, claims.Allows("org-platform-squatter", "registry.example.com/team/platform/web/api"))
	require.True(t, claims.OwnContested("org-around-platform"))
	require.False(t, claims.Allows("org-around-platform", "registry.example.com/team/platform@sha256:1"))
	require.False(t, claims.Allows("org-around-platform", "registry.example.com/team/other"), "a contested registry is nobody's to use")
	require.False(t, claims.Allows("org-fine", "registry.example.com/team/other"), "and nobody else's either")
	require.True(t, claims.Allows("org-fine", "registry.example.com/team/platform/org-fine/api"), "an organization's own path in the platform's repository is held more closely")

	require.False(t, claims.OwnContested("org-fine"))
	require.True(t, claims.Allows("org-fine", "ghcr.io/fine/shop/api"))

	// What the dashboard asks before an organization takes a registry.
	claims = NewRegistryClaims("registry.example.com/platform", []geassv1alpha1.GeassOrganization{org("org-victim", "ghcr.io/victim/builds"), org("org-fine", "ghcr.io/fine")})
	require.True(t, claims.WouldConflict("registry.example.com/platform", "org-new", "ghcr.io/fine/shop"))
	require.True(t, claims.WouldConflict("registry.example.com/platform", "org-new", "ghcr.io/victim"), "a registry around another's")
	require.True(t, claims.WouldConflict("registry.example.com/platform", "org-new", "registry.example.com/platform/org-fine"))
	require.False(t, claims.WouldConflict("registry.example.com/platform", "org-new", "registry.example.com/platform/org-new"))
	require.False(t, claims.WouldConflict("registry.example.com/platform", "org-fine", "ghcr.io/fine/other"), "its own registry is no conflict")
	require.False(t, claims.WouldConflict("registry.example.com/platform", "org-new", "ghcr.io/new"))
}

func TestWithoutAPlatformRepositoryOnlyOrganizationsClaim(t *testing.T) {
	claims := NewRegistryClaims("", []geassv1alpha1.GeassOrganization{org("org-a", "ghcr.io/acme")})
	require.True(t, claims.Allows("org-a", "ghcr.io/acme/x"))
	require.False(t, claims.Allows("org-b", "ghcr.io/acme/x"))
	require.True(t, claims.Allows("org-b", "registry.example.com/platform/x"))
}

func TestALoginIsFoundUnderAnyName(t *testing.T) {
	platformLogin, err := DockerConfigJSON("registry.example.com", "platform-bot", "p4"+"ss")
	require.NoError(t, err)
	sameLoginOtherServer, err := DockerConfigJSON("mirror.example.com", "platform-bot", "p4"+"ss")
	require.NoError(t, err)
	orgLogin, err := DockerConfigJSON("registry.example.com", "acme-bot", "0rg")
	require.NoError(t, err)

	require.True(t, HoldsLoginOf(platformLogin, platformLogin))
	require.True(t, HoldsLoginOf(sameLoginOtherServer, platformLogin), "the same user and password for another server")
	require.True(t, HoldsLoginOf([]byte(`{"auths":{"x":{"username":"platform-bot","password":"`+"p4"+"ss"+`"}},"credHelpers":{}}`), platformLogin))
	require.True(t, HoldsLoginOf([]byte(" PLATFORM \n"), []byte("PLATFORM")), "a file that is not JSON is compared as it is")
	require.True(t, HoldsLoginOf([]byte(`{"auths":{"a":{"identitytoken":"t0k"}}}`), []byte(`{"auths":{"b":{"identitytoken":"t0k"}}}`)))
	require.False(t, HoldsLoginOf(orgLogin, platformLogin))
	require.False(t, HoldsLoginOf(nil, platformLogin))
	require.False(t, HoldsLoginOf(orgLogin, nil))
}

func isolationClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func platformConfigWith(tenancy *geassv1alpha1.GeassPlatformTenantIsolationSpec) *geassv1alpha1.GeassPlatformConfig {
	return &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: HAReadinessName, Namespace: SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			Registry: geassv1alpha1.GeassPlatformRegistrySpec{Repository: "registry.example.com/platform", CredentialRef: &corev1.LocalObjectReference{Name: "platform-registry"}},
			Tenancy:  tenancy,
		},
	}
}

func requireOwnRegistry() *geassv1alpha1.GeassPlatformTenantIsolationSpec {
	return &geassv1alpha1.GeassPlatformTenantIsolationSpec{Registry: &geassv1alpha1.GeassTenantRegistrySpec{}}
}

func project(name, org string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{Org: org}}
}

func TestTheRegistryPolicyAppliesOnlyWhenSetAndOnlyToOrganizations(t *testing.T) {
	ctx := context.Background()
	acme := org("org-a", "ghcr.io/acme/builds")

	isolation, err := ResolveRegistryIsolation(ctx, isolationClient(t, platformConfigWith(nil), project("shop", "org-a"), &acme), "shop")
	require.NoError(t, err)
	require.Nil(t, isolation, "off by default")

	isolation, err = ResolveRegistryIsolation(ctx, isolationClient(t, platformConfigWith(requireOwnRegistry()), project("shop", ""), &acme), "shop")
	require.NoError(t, err)
	require.Nil(t, isolation, "a project of no organization is the platform's")

	isolation, err = ResolveRegistryIsolation(ctx, isolationClient(t, project("shop", "org-a"), &acme), "shop")
	require.NoError(t, err)
	require.Nil(t, isolation, "no platform configuration, no setting")

	isolation, err = ResolveRegistryIsolation(ctx, isolationClient(t, platformConfigWith(requireOwnRegistry()), project("shop", "org-a"), &acme), "shop")
	require.NoError(t, err)
	require.NotNil(t, isolation)
	require.Equal(t, "org-a", isolation.Org)
	require.NotNil(t, isolation.Organization)
	require.Equal(t, geassv1alpha1.RegistryPolicyRequireOwnRegistry, RegistryPolicyOf(platformConfigWith(requireOwnRegistry())), "an empty setting is the default policy")

	isolation, err = ResolveRegistryIsolation(ctx, isolationClient(t, platformConfigWith(requireOwnRegistry())), "gone")
	require.NoError(t, err)
	require.NotNil(t, isolation, "a project that cannot be found is held to it")
	require.Empty(t, isolation.Org)
}

func refusalReason(t *testing.T, err error) string {
	t.Helper()
	var refusal *RegistryRefusedError
	require.True(t, errors.As(err, &refusal), "%v", err)
	require.Contains(t, err.Error(), MessageRegistryIsolation+": ")
	return refusal.Reason
}

func TestAnOrganizationsServiceBuildsIntoItsOwnRegistryAndNeverThePlatforms(t *testing.T) {
	ctx := context.Background()
	platformLogin, err := DockerConfigJSON("registry.example.com", "platform-bot", "p4"+"ss")
	require.NoError(t, err)
	platformSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "platform-registry", Namespace: SystemNamespace}, Data: map[string][]byte{corev1.DockerConfigJsonKey: platformLogin}}
	copied := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "a-copy", Namespace: SystemNamespace}, Data: map[string][]byte{corev1.DockerConfigJsonKey: platformLogin}}
	otherOrgs := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "their-login", Namespace: SystemNamespace, Labels: map[string]string{LabelOrgBuild: "org-b"}}}
	acme := org("org-a", "ghcr.io/acme/builds")
	acme.Spec.Registry.CredentialRef = &corev1.LocalObjectReference{Name: OrgRegistrySecretName("org-a")}
	app := func(repository, credential string) *geassv1alpha1.GeassApp {
		a := &geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: SystemNamespace}, Spec: geassv1alpha1.GeassAppSpec{Project: "shop"}}
		a.Spec.Build.Registry.Repository = repository
		if credential != "" {
			a.Spec.Build.Registry.CredentialRef = &corev1.LocalObjectReference{Name: credential}
		}
		return a
	}
	c := isolationClient(t, platformConfigWith(requireOwnRegistry()), project("shop", "org-a"), &acme, platformSecret, copied, otherOrgs)
	isolation, err := ResolveRegistryIsolation(ctx, c, "shop")
	require.NoError(t, err)

	repository, credential, err := isolation.BuildRegistry(ctx, c, app("", ""))
	require.NoError(t, err)
	require.Equal(t, "ghcr.io/acme/builds/shop/api", repository, "under the organization's repository, a service at a time")
	require.Equal(t, OrgRegistrySecretName("org-a"), credential.Name)

	repository, credential, err = isolation.BuildRegistry(ctx, c, app("ghcr.io/acme/elsewhere", ""))
	require.NoError(t, err)
	require.Equal(t, "ghcr.io/acme/elsewhere", repository, "a service's own registry is used as it is written")
	require.Nil(t, credential)

	for name, tc := range map[string]struct {
		app    *geassv1alpha1.GeassApp
		reason string
	}{
		"the platform's login by name":            {app("ghcr.io/acme/elsewhere", "platform-registry"), RegistryReasonPlatformCredential},
		"the platform's login under another name": {app("ghcr.io/acme/elsewhere", "a-copy"), RegistryReasonPlatformCredential},
		"another organization's saved login":      {app("ghcr.io/acme/elsewhere", OrgRegistrySecretName("org-b")), RegistryReasonPlatformCredential},
		"a login labelled another organization's": {app("ghcr.io/acme/elsewhere", "their-login"), RegistryReasonPlatformCredential},
		"pushing into the platform's repository":  {app("registry.example.com/platform/api", ""), RegistryReasonForeignRepository},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := isolation.BuildRegistry(ctx, c, tc.app)
			require.Equal(t, tc.reason, refusalReason(t, err))
			require.NotContains(t, err.Error(), "p4"+"ss")
		})
	}

	// Without a registry of its own the organization does not build: the platform's is not a fallback.
	bare := org("org-a", "")
	c = isolationClient(t, platformConfigWith(requireOwnRegistry()), project("shop", "org-a"), &bare, platformSecret)
	isolation, err = ResolveRegistryIsolation(ctx, c, "shop")
	require.NoError(t, err)
	_, _, err = isolation.BuildRegistry(ctx, c, app("", ""))
	require.Equal(t, RegistryReasonOwnRegistryRequired, refusalReason(t, err))
	require.Contains(t, err.Error(), "Settings > Registry")
	require.NotContains(t, err.Error(), "registry.example.com/platform", "an organization is not told the platform's repository")
}

func TestAnOrganizationsServiceNamesTheRepositoryOfItsImage(t *testing.T) {
	isolation := &RegistryIsolation{Org: "org-a", Claims: NewRegistryClaims("registry.example.com/platform", nil)}
	require.Equal(t, RegistryReasonForeignRepository, refusalReason(t, isolation.CheckImage("sha256:"+strings.Repeat("a", 64))), "an image ID names no repository")
	require.NoError(t, isolation.CheckImage("nginx@sha256:"+strings.Repeat("a", 64)))
}
