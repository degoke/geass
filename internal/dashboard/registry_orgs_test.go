package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func TestAnOrganizationsAdministratorSetsItsOwnRegistryAndTheLoginStaysASecret(t *testing.T) {
	f := newMatrixFixture(t)
	admin := sessionIn(orgA, "", "admin")

	rec := f.do(admin, http.MethodPost, "/api/registry/save", form("repository", "ghcr.io/acme/builds", "username", "ci", "password", "hunter2-token"))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	org := f.org(t, orgA)
	require.NotNil(t, org.Spec.Registry)
	require.Equal(t, "ghcr.io/acme/builds", org.Spec.Registry.Repository)
	require.Equal(t, platform.OrgRegistrySecretName(orgA), org.Spec.Registry.CredentialRef.Name)

	var secret corev1.Secret
	require.NoError(t, f.base.Get(context.Background(), key(platform.OrgRegistrySecretName(orgA)), &secret))
	require.Equal(t, corev1.SecretTypeDockerConfigJson, secret.Type)
	require.Contains(t, string(secret.Data[corev1.DockerConfigJsonKey]), "ghcr.io")

	rec = f.do(admin, http.MethodGet, "/api/registry", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "hunter2", "the login never comes back")
	var view struct {
		Repository string `json:"repository"`
		Configured bool   `json:"configured"`
		HasLogin   bool   `json:"hasLogin"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	require.True(t, view.Configured && view.HasLogin)

	// Changing only the repository keeps the login, and saving none removes it.
	require.Equal(t, http.StatusOK, f.do(admin, http.MethodPost, "/api/registry/save", form("repository", "ghcr.io/acme/other", "keepExisting", "on")).Code)
	require.Equal(t, "ghcr.io/acme/other", f.org(t, orgA).Spec.Registry.Repository)
	require.NotNil(t, f.org(t, orgA).Spec.Registry.CredentialRef)
	require.Equal(t, http.StatusOK, f.do(admin, http.MethodPost, "/api/registry/save", form("repository", "ghcr.io/acme/other")).Code)
	require.Nil(t, f.org(t, orgA).Spec.Registry.CredentialRef)
	require.True(t, apierrors.IsNotFound(f.base.Get(context.Background(), key(platform.OrgRegistrySecretName(orgA)), &corev1.Secret{})), "the login that is no longer wanted is deleted")

	require.Equal(t, http.StatusOK, f.do(admin, http.MethodPost, "/api/registry/clear", form()).Code)
	require.Nil(t, f.org(t, orgA).Spec.Registry)
}

func TestNobodyButAnAdministratorOfTheWholeOrganizationTouchesItsRegistry(t *testing.T) {
	f := newMatrixFixture(t)
	save := form("repository", "ghcr.io/acme/builds", "username", "ci", "password", "tok")
	require.Equal(t, http.StatusOK, f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/registry/save", save).Code)
	before := f.org(t, orgA).Spec.Registry

	for name, who := range map[string]*dashboardSessionInfo{
		"a viewer":                  sessionIn(orgA, "", "viewer"),
		"a member of one workspace": sessionIn(orgA, wsA1, "admin"),
		"the platform":              platformSession,
	} {
		for _, path := range []string{"/api/registry/save", "/api/registry/clear"} {
			rec := f.do(who, http.MethodPost, path, form("repository", "evil.example/steal", "username", "x", "password", "y"))
			require.GreaterOrEqual(t, rec.Code, 400, name+" "+path)
		}
		require.GreaterOrEqual(t, f.do(who, http.MethodGet, "/api/registry", nil).Code, 400, name)
	}
	require.Equal(t, before, f.org(t, orgA).Spec.Registry, "it is as it was")
	require.Nil(t, f.org(t, orgB).Spec.Registry, "and nobody else's was set")

	// Another organization's administrator sets their own, which is all the route lets them reach.
	require.Equal(t, http.StatusOK, f.do(sessionIn(orgB, "", "admin"), http.MethodPost, "/api/registry/save", form("repository", "ghcr.io/beta/builds")).Code)
	require.Equal(t, "ghcr.io/beta/builds", f.org(t, orgB).Spec.Registry.Repository)
	require.Equal(t, before, f.org(t, orgA).Spec.Registry, "and A's is untouched")
	var secret corev1.Secret
	require.NoError(t, f.base.Get(context.Background(), key(platform.OrgRegistrySecretName(orgA)), &secret))
	require.True(t, apierrors.IsNotFound(f.base.Get(context.Background(), key(platform.OrgRegistrySecretName(orgB)), &corev1.Secret{})))
}

func TestARegistryThatIsNotARepositoryIsRefused(t *testing.T) {
	f := newMatrixFixture(t)
	admin := sessionIn(orgA, "", "admin")
	for name, pairs := range map[string][]string{
		"no slash":         {"repository", "ghcr.io"},
		"a digest":         {"repository", "ghcr.io/acme/a@sha256:abc"},
		"a username alone": {"repository", "ghcr.io/acme/a", "username", "u"},
		"a password alone": {"repository", "ghcr.io/acme/a", "password", "p"},
		"nothing at all":   {},
		"spaces":           {"repository", "ghcr.io/acme b/a"},
	} {
		require.Equal(t, http.StatusBadRequest, f.do(admin, http.MethodPost, "/api/registry/save", form(pairs...)).Code, name)
	}
	require.Nil(t, f.org(t, orgA).Spec.Registry)
}

func TestDeletingAnOrganizationDeletesItsRegistryLogin(t *testing.T) {
	f := newMatrixFixture(t)
	gamma := f.createOrg(t, "Gamma", "org_gamma")
	require.NoError(t, f.base.Create(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: platform.OrgRegistrySecretName(gamma), Namespace: platform.SystemNamespace}}))
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/orgs/"+gamma+"/delete", form("confirmName", gamma)).Code)
	require.True(t, apierrors.IsNotFound(f.base.Get(context.Background(), key(platform.OrgRegistrySecretName(gamma)), &corev1.Secret{})))
}

func TestUnderTheRegistryPolicyAnOrganizationCannotTakeAPartOfARegistryThatIsNotItsOwn(t *testing.T) {
	f := newMatrixFixture(t)
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, f.base.Get(context.Background(), key(platform.HAReadinessName), &config))
	config.Spec.Registry.Repository = "registry.platform.example/builds"
	config.Spec.Tenancy = &geassv1alpha1.GeassPlatformTenantIsolationSpec{Registry: &geassv1alpha1.GeassTenantRegistrySpec{}}
	require.NoError(t, f.base.Update(context.Background(), &config))
	adminA, adminB := sessionIn(orgA, "", "admin"), sessionIn(orgB, "", "admin")

	rec := f.do(adminA, http.MethodGet, "/api/registry", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"ownRequired":true`, "the organization is told its builds need a registry of its own")
	require.NotContains(t, rec.Body.String(), "registry.platform.example", "and not where the platform's is")

	require.Equal(t, http.StatusOK, f.do(adminA, http.MethodPost, "/api/registry/save", form("repository", "ghcr.io/acme/builds")).Code)
	for name, repository := range map[string]string{
		"A's registry":                          "ghcr.io/acme/builds",
		"inside A's registry":                   "ghcr.io/acme/builds/beta",
		"around A's registry":                   "ghcr.io/acme",
		"the platform's repository":             "registry.platform.example/builds",
		"A's path in the platform's repository": "registry.platform.example/builds/" + orgA,
	} {
		rec := f.do(adminB, http.MethodPost, "/api/registry/save", form("repository", repository))
		require.Equal(t, http.StatusConflict, rec.Code, name)
		require.NotContains(t, rec.Body.String(), orgA, "%s: whose it is is not said", name)
	}
	require.Nil(t, f.org(t, orgB).Spec.Registry, "nothing was saved")

	require.Equal(t, http.StatusOK, f.do(adminB, http.MethodPost, "/api/registry/save", form("repository", "registry.platform.example/builds/"+orgB)).Code, "its own path in the platform's repository")
	require.Equal(t, http.StatusOK, f.do(adminA, http.MethodPost, "/api/registry/save", form("repository", "ghcr.io/acme/builds/v2")).Code, "A moves inside what was its own")
}

func TestWithoutTheRegistryPolicyAnOrganizationTakesAnyRegistry(t *testing.T) {
	f := newMatrixFixture(t)
	require.Equal(t, http.StatusOK, f.do(sessionIn(orgA, "", "admin"), http.MethodPost, "/api/registry/save", form("repository", "ghcr.io/acme/builds")).Code)
	require.Equal(t, http.StatusOK, f.do(sessionIn(orgB, "", "admin"), http.MethodPost, "/api/registry/save", form("repository", "ghcr.io/acme/builds")).Code, "as before")
	rec := f.do(sessionIn(orgA, "", "admin"), http.MethodGet, "/api/registry", nil)
	require.Contains(t, rec.Body.String(), `"ownRequired":false`)
}
