package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func saveRegistry(t *testing.T, srv *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, postForm("/api/settings/registry/save", form))
	return rec
}

func storedRegistry(t *testing.T, c client.Client) geassv1alpha1.GeassPlatformRegistrySpec {
	t.Helper()
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	return config.Spec.Registry
}

func TestTheRegistrySettingStoresTheRepositoryAndALoginBuildsCanUse(t *testing.T) {
	ctx := context.Background()
	c := newFakeClient()
	srv := &Server{Client: c}

	rec := saveRegistry(t, srv, url.Values{"repository": {"ghcr.io/acme/builds"}, "username": {"bot"}, "password": {"s3cret"}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	registry := storedRegistry(t, c)
	require.Equal(t, "ghcr.io/acme/builds", registry.Repository)
	require.Equal(t, &corev1.LocalObjectReference{Name: platform.PlatformRegistrySecretName}, registry.CredentialRef)

	var secret corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.PlatformRegistrySecretName, Namespace: platform.SystemNamespace}, &secret))
	require.Equal(t, corev1.SecretTypeDockerConfigJson, secret.Type, "Kaniko mounts it as config.json and a pod pulls with a copy of it")
	var login struct {
		Auths map[string]struct{ Username, Password string } `json:"auths"`
	}
	require.NoError(t, json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &login))
	require.Equal(t, "bot", login.Auths["ghcr.io"].Username, "the login is for the registry the repository is on")
	require.Equal(t, "s3cret", login.Auths["ghcr.io"].Password)
}

func TestTheRegistrySettingNeverReturnsTheLogin(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	saveRegistry(t, srv, url.Values{"repository": {"ghcr.io/acme/builds"}, "username": {"bot"}, "password": {"s3cret"}})

	rec := httptest.NewRecorder()
	srv.handleAPI(rec, httptest.NewRequest(http.MethodGet, "/api/settings/registry", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"repository":"ghcr.io/acme/builds","configured":true,"hasLogin":true}`, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "s3cret")
}

func TestARegistryThatNeedsNoLoginIsFine(t *testing.T) {
	c := newFakeClient()

	rec := saveRegistry(t, &Server{Client: c}, url.Values{"repository": {"registry.internal:5000/builds"}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, geassv1alpha1.GeassPlatformRegistrySpec{Repository: "registry.internal:5000/builds"}, storedRegistry(t, c))
}

func TestAChangeOfRepositoryCanKeepTheLogin(t *testing.T) {
	c := newFakeClient()
	srv := &Server{Client: c}
	saveRegistry(t, srv, url.Values{"repository": {"ghcr.io/acme/builds"}, "username": {"bot"}, "password": {"s3cret"}})

	rec := saveRegistry(t, srv, url.Values{"repository": {"ghcr.io/acme/other"}, "keepExisting": {"on"}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	registry := storedRegistry(t, c)
	require.Equal(t, "ghcr.io/acme/other", registry.Repository)
	require.NotNil(t, registry.CredentialRef, "the login stays when the form says to keep it")

	saveRegistry(t, srv, url.Values{"repository": {"ghcr.io/acme/other"}})
	require.Nil(t, storedRegistry(t, c).CredentialRef, "and goes when it does not")
	require.Error(t, c.Get(context.Background(), client.ObjectKey{Name: platform.PlatformRegistrySecretName, Namespace: platform.SystemNamespace}, &corev1.Secret{}), "the Secret goes with it")
}

func TestARegistryLoginThatTheOperatorMadeIsNotDeleted(t *testing.T) {
	ctx := context.Background()
	theirs := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "registry-creds", Namespace: platform.SystemNamespace}, Type: corev1.SecretTypeDockerConfigJson}
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{Registry: geassv1alpha1.GeassPlatformRegistrySpec{
			Repository: "ghcr.io/acme/geass-builds", CredentialRef: &corev1.LocalObjectReference{Name: "registry-creds"},
		}},
	}
	c := newFakeClient(theirs, config)

	rec := httptest.NewRecorder()
	(&Server{Client: c}).handleAPI(rec, postForm("/api/settings/registry/clear", url.Values{}))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, geassv1alpha1.GeassPlatformRegistrySpec{}, storedRegistry(t, c))
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(theirs), &corev1.Secret{}), "a Secret Geass did not make is only unlinked")
}

func TestTheRegistrySettingRefusesWhatCannotBeARepository(t *testing.T) {
	for name, form := range map[string]url.Values{
		"empty":                    {"repository": {""}},
		"spaces":                   {"repository": {"ghcr.io/acme/my builds"}},
		"a digest":                 {"repository": {"ghcr.io/acme/builds@sha256:abc"}},
		"no registry or namespace": {"repository": {"builds"}},
		"a username alone":         {"repository": {"ghcr.io/acme/builds"}, "username": {"bot"}},
		"a password alone":         {"repository": {"ghcr.io/acme/builds"}, "password": {"s3cret"}},
	} {
		t.Run(name, func(t *testing.T) {
			c := newFakeClient()

			rec := saveRegistry(t, &Server{Client: c}, form)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Error(t, c.Get(context.Background(), client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &geassv1alpha1.GeassPlatformConfig{}), "nothing is saved")
		})
	}
}

func TestAProjectTokenCannotChangeTheRegistry(t *testing.T) {
	require.False(t, func() bool { _, ok := projectTokenRoute(http.MethodPost, "/api/settings/registry/save"); return ok }(),
		"the registry login is a platform setting, and a deploy token reaches only its own services")
}

func platformConfigWithRegistry(repository string, loginSecret string) *geassv1alpha1.GeassPlatformConfig {
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassPlatformConfigSpec{Registry: geassv1alpha1.GeassPlatformRegistrySpec{Repository: repository}},
	}
	if loginSecret != "" {
		config.Spec.Registry.CredentialRef = &corev1.LocalObjectReference{Name: loginSecret}
	}
	return config
}

func platformConfigUpdateFails() interceptor.Funcs {
	return interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, isConfig := obj.(*geassv1alpha1.GeassPlatformConfig); isConfig {
				return apierrors.NewConflict(schema.GroupResource{Group: "geass.geass.dev", Resource: "geassplatformconfigs"}, obj.GetName(), nil)
			}
			return c.Update(ctx, obj, opts...)
		},
	}
}

func TestAFailedSaveOfTheRegistryLeavesTheLoginAsItWas(t *testing.T) {
	ctx := context.Background()
	key := client.ObjectKey{Name: platform.PlatformRegistrySecretName, Namespace: platform.SystemNamespace}
	form := url.Values{"repository": {"ghcr.io/acme/other"}, "username": {"bot"}, "password": {"new-password"}}

	t.Run("a login that was there is put back", func(t *testing.T) {
		old := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}, Type: corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{"ghcr.io":{"auth":"old"}}}`)}}
		srv, c := serverIntercepting(t, platformConfigUpdateFails(), platformConfigWithRegistry("ghcr.io/acme/builds", key.Name), old)

		rec := httptest.NewRecorder()
		srv.handleAPI(rec, postForm("/api/settings/registry/save", form))

		require.Equal(t, http.StatusBadRequest, rec.Code)
		var got corev1.Secret
		require.NoError(t, c.Get(ctx, key, &got))
		require.Equal(t, old.Data, got.Data, "the config still names the old repository, so the login still is the old one")
	})

	t.Run("a login that was not there is not left", func(t *testing.T) {
		srv, c := serverIntercepting(t, platformConfigUpdateFails(), platformConfigWithRegistry("", ""))

		rec := httptest.NewRecorder()
		srv.handleAPI(rec, postForm("/api/settings/registry/save", form))

		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Error(t, c.Get(ctx, key, &corev1.Secret{}))
	})
}

func TestClearingTheRegistryReportsALoginThatCouldNotBeDeleted(t *testing.T) {
	login := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: platform.PlatformRegistrySecretName, Namespace: platform.SystemNamespace}, Type: corev1.SecretTypeDockerConfigJson}
	srv, c := serverIntercepting(t, interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if _, isSecret := obj.(*corev1.Secret); isSecret {
				return errors.New("the API server is busy")
			}
			return c.Delete(ctx, obj, opts...)
		},
	}, platformConfigWithRegistry("ghcr.io/acme/builds", platform.PlatformRegistrySecretName), login)

	rec := httptest.NewRecorder()
	srv.handleAPI(rec, postForm("/api/settings/registry/clear", url.Values{}))

	require.Equal(t, http.StatusBadRequest, rec.Code, "an orphaned credential is not reported as success")
	require.Contains(t, rec.Body.String(), "login could not be removed")
	require.Equal(t, geassv1alpha1.GeassPlatformRegistrySpec{}, storedRegistry(t, c), "the registry itself is cleared")
}
