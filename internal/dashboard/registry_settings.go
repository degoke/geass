package dashboard

import (
	"context"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const registrySettingsPath = "/settings/registry"

// The platform registry is where the images of repository builds are pushed, for every service that does not
// name a registry of its own. Geass has no registry to default to, so builds do not run until this is set. The
// repository is used as it is written, as the destination image of the build: a service that wants its own
// repository or tag sets spec.build.registry on itself.

// handlePlatformRegistrySave sets the platform's build registry. The login is optional (a registry that
// needs none is fine). When there is one it is stored as a docker-registry Secret, which Kaniko pushes with
// and which is copied into a service's namespace for its pods to pull with.
func (s *Server) handlePlatformRegistrySave(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, registrySettingsPath) || !parseFormOrRedirect(w, r, registrySettingsPath) {
		return
	}
	fail := func(message string) { redirectProbe(w, r, registrySettingsPath, dashboardLiteralError, message) }
	repository := strings.TrimSpace(r.FormValue("repository"))
	if repository == "" || !validImageReference(repository) || strings.Contains(repository, "@") || !strings.Contains(repository, "/") {
		fail("enter the repository images are pushed to, like ghcr.io/acme/builds")
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	keepExisting := r.FormValue("keepExisting") == "on" || r.FormValue("keepExisting") == boolTrue
	if (username == "") != (password == "") {
		fail("enter both a username and a password, or neither")
		return
	}

	config, err := s.platformConfig(r.Context())
	if err != nil {
		fail("could not load platform config")
		return
	}
	creating := config.Name == ""
	if creating {
		config = geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: systemNamespace}}
	}

	// A login that is not wanted any more is deleted after the config stops naming it, and one that was just
	// written is put back as it was if the config cannot be saved, so the two do not disagree.
	var staleLogin string
	rollbackLogin := func() {}
	switch {
	case username != "":
		login, err := platform.DockerConfigJSON(platform.RegistryServer(repository), username, password)
		if err != nil {
			fail(err.Error())
			return
		}
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platform.PlatformRegistrySecretName, Namespace: systemNamespace, Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: login},
		}
		rollback, err := s.persistSecretWithRollback(r.Context(), secret)
		if err != nil {
			fail("could not save the registry login")
			return
		}
		rollbackLogin = rollback
		config.Spec.Registry.CredentialRef = &corev1.LocalObjectReference{Name: platform.PlatformRegistrySecretName}
	case keepExisting && config.Spec.Registry.CredentialRef != nil:
		// The login stays as it is, for a change of repository alone.
	default:
		staleLogin = dropPlatformRegistryLogin(&config)
	}
	config.Spec.Registry.Repository = repository

	if creating {
		err = s.Client.Create(r.Context(), &config)
	} else {
		err = s.Client.Update(r.Context(), &config)
	}
	if err != nil {
		rollbackLogin()
		fail("could not save the registry")
		return
	}
	if err := s.deleteSystemSecret(r.Context(), staleLogin); err != nil {
		fail("the registry was saved, but its old login could not be removed")
		return
	}
	redirect(w, r, registrySettingsPath)
}

// handlePlatformRegistryClear takes the registry and its login off the platform. Services that name their own
// registry are not affected, and the ones that used this one wait for a registry until one is set again.
func (s *Server) handlePlatformRegistryClear(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, registrySettingsPath) || !parseFormOrRedirect(w, r, registrySettingsPath) {
		return
	}
	config, err := s.platformConfig(r.Context())
	if err != nil {
		redirectProbe(w, r, registrySettingsPath, dashboardLiteralError, "could not load platform config")
		return
	}
	if config.Name != "" {
		staleLogin := dropPlatformRegistryLogin(&config)
		config.Spec.Registry = geassv1alpha1.GeassPlatformRegistrySpec{}
		if err := s.Client.Update(r.Context(), &config); err != nil {
			redirectProbe(w, r, registrySettingsPath, dashboardLiteralError, "could not clear the registry")
			return
		}
		if err := s.deleteSystemSecret(r.Context(), staleLogin); err != nil {
			redirectProbe(w, r, registrySettingsPath, dashboardLiteralError, "the registry was cleared, but its login could not be removed")
			return
		}
	}
	redirect(w, r, registrySettingsPath)
}

// dropPlatformRegistryLogin takes the login off the config and says which Secret to delete once that is saved:
// only one the dashboard made. One an operator created with kubectl and named on the platform configuration is
// theirs, and only the reference to it goes.
func dropPlatformRegistryLogin(config *geassv1alpha1.GeassPlatformConfig) string {
	ref := config.Spec.Registry.CredentialRef
	config.Spec.Registry.CredentialRef = nil
	if ref != nil && ref.Name == platform.PlatformRegistrySecretName {
		return ref.Name
	}
	return ""
}

// persistSecretWithRollback writes a Secret and returns what puts it back as it was (its earlier contents, or no Secret at
// all) for a caller whose next step fails, so that what a configuration names and what is stored do not disagree.
func (s *Server) persistSecretWithRollback(ctx context.Context, secret *corev1.Secret) (rollback func(), err error) {
	previous := &corev1.Secret{}
	existed := s.Client.Get(ctx, client.ObjectKeyFromObject(secret), previous) == nil
	if err := s.persistSecret(ctx, secret); err != nil {
		return func() {}, err
	}
	return func() {
		if existed {
			_ = s.persistSecret(ctx, &corev1.Secret{ObjectMeta: secret.ObjectMeta, Data: previous.Data})
		} else {
			_ = s.Client.Delete(ctx, secret)
		}
	}, nil
}

// deleteSystemSecret deletes a Secret of the system namespace that the configuration no longer names; one that is not
// there is not an error. An empty name is nothing to delete.
func (s *Server) deleteSystemSecret(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	err := s.Client.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace}})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// handleAPIRegistrySettings says where builds push and whether they log in. It never returns the login.
func (s *Server) handleAPIRegistrySettings(w http.ResponseWriter, r *http.Request) {
	config, err := s.platformConfig(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load registry settings"})
		return
	}
	registry := config.Spec.Registry
	writeJSON(w, http.StatusOK, map[string]any{
		"repository": registry.Repository,
		"configured": registry.Repository != "",
		"hasLogin":   registry.CredentialRef != nil && registry.CredentialRef.Name != "",
	})
}
