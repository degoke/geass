package dashboard

import (
	"context"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// An organization's own registry. Its builds push there with a login of its own, and run in a namespace that holds that login
// and not the platform's (see platform.OrgBuildPolicy). Without one they use the platform's registry, unless the platform's
// spec.tenancy.registry is set, under which they do not build at all (see platform.RegistryIsolation). It is set by an
// administrator of the whole organization, who is the only one to see or change it.

const orgRegistryWhat = "The registry"

// requireOrgRegistryAdmin refuses everyone but an administrator of a whole organization, signed in: it is a login that can
// push images.
func (s *Server) requireOrgRegistryAdmin(w http.ResponseWriter, r *http.Request) (Tenant, bool) {
	if !s.requireSessionAdmin(w, r, orgRegistryWhat) || !requireOrgContext(w, r) {
		return Tenant{}, false
	}
	tenant, _ := tenantOf(r.Context())
	if tenant.Workspace != "" {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: "Only an administrator of the whole organization sets its registry."})
		return Tenant{}, false
	}
	return tenant, true
}

// handleAPIOrgRegistry serves GET /api/registry: where the organization's builds push, and whether they log in. It never
// returns the login.
func (s *Server) handleAPIOrgRegistry(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireOrgRegistryAdmin(w, r)
	if !ok {
		return
	}
	org, err := s.organization(r.Context(), tenant.Org)
	if err != nil || org == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load the registry"})
		return
	}
	registry := org.Spec.Registry
	config, _ := s.platformConfig(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"repository":         registryOf(registry),
		"configured":         registry != nil && registry.Repository != "",
		"hasLogin":           registry != nil && registry.CredentialRef != nil && registry.CredentialRef.Name != "",
		"platformRepository": config.Spec.Registry.Repository != "",
		// Under spec.tenancy.registry the organization's builds use its own registry or none: the platform's is no fallback.
		"ownRequired": platform.RegistryPolicyOf(&config) != "",
	})
}

func registryOf(registry *geassv1alpha1.GeassPlatformRegistrySpec) string {
	if registry == nil {
		return ""
	}
	return registry.Repository
}

// handleOrgRegistry serves the organization's changes to its registry.
func (s *Server) handleOrgRegistry(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/registry") || !parseFormOrRedirect(w, r, "/registry") {
		return
	}
	tenant, ok := s.requireOrgRegistryAdmin(w, r)
	if !ok {
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/registry/") {
	case verbSave:
		s.orgRegistrySave(w, r, tenant.Org)
	case "clear":
		s.orgRegistryClear(w, r, tenant.Org)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) orgRegistrySave(w http.ResponseWriter, r *http.Request, orgName string) {
	repository := strings.TrimSpace(r.FormValue("repository"))
	if repository == "" || !validImageReference(repository) || strings.Contains(repository, "@") || !strings.Contains(repository, "/") {
		orgFail(w, http.StatusBadRequest, "enter the repository images are pushed to, like ghcr.io/acme/builds")
		return
	}
	switch conflict, err := s.orgRegistryConflicts(r.Context(), orgName, repository); {
	case err != nil:
		orgFail(w, http.StatusInternalServerError, "could not check the registry")
		return
	case conflict:
		// Who holds it is not said: one organization does not learn another's registry from this.
		orgFail(w, http.StatusConflict, "that repository overlaps a part of a registry that belongs to the platform or to another organization: choose another")
		return
	}
	username, password := strings.TrimSpace(r.FormValue("username")), r.FormValue("password")
	keep := formFlag(r, "keepExisting")
	if (username == "") != (password == "") {
		orgFail(w, http.StatusBadRequest, "enter both a username and a password, or neither")
		return
	}
	ctx := asSystem(r.Context())
	secretName := platform.OrgRegistrySecretName(orgName)
	rollback := func() {}
	var credential *corev1.LocalObjectReference
	var staleLogin string
	switch {
	case username != "":
		login, err := platform.DockerConfigJSON(platform.RegistryServer(repository), username, password)
		if err != nil {
			orgFail(w, http.StatusBadRequest, err.Error())
			return
		}
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: systemNamespace, Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue, platform.LabelOrgBuild: orgName}},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: login},
		}
		undo, err := s.persistSecretWithRollback(ctx, secret)
		if err != nil {
			orgFail(w, http.StatusInternalServerError, "could not save the registry login")
			return
		}
		rollback = undo
		credential = &corev1.LocalObjectReference{Name: secretName}
	case keep:
		// The login stays as it is, for a change of repository alone.
	default:
		staleLogin = secretName
	}
	err := s.mutateOrganization(r, orgName, func(org *geassv1alpha1.GeassOrganization) error {
		next := &geassv1alpha1.GeassPlatformRegistrySpec{Repository: repository}
		switch {
		case credential != nil:
			next.CredentialRef = credential
		case keep && org.Spec.Registry != nil:
			next.CredentialRef = org.Spec.Registry.CredentialRef
		}
		org.Spec.Registry = next
		return nil
	})
	if err != nil {
		rollback()
		s.finishOrgChange(w, err, nil)
		return
	}
	if staleLogin != "" {
		_ = s.deleteSystemSecret(ctx, staleLogin)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) orgRegistryClear(w http.ResponseWriter, r *http.Request, orgName string) {
	err := s.mutateOrganization(r, orgName, func(org *geassv1alpha1.GeassOrganization) error {
		org.Spec.Registry = nil
		return nil
	})
	if err != nil {
		s.finishOrgChange(w, err, nil)
		return
	}
	_ = s.deleteSystemSecret(asSystem(r.Context()), platform.OrgRegistrySecretName(orgName))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// orgRegistryConflicts reports whether, under spec.tenancy.registry, the organization may not take the repository as its
// registry: it overlaps the platform's repository outside the organization's own path in it, or another organization's
// registry. The first organization to take a repository keeps it; one that takes an overlapping one some other way (with
// kubectl, or in a race with this check) makes both unusable, and the operator says so on their services. Without the
// setting anything goes, as before.
func (s *Server) orgRegistryConflicts(ctx context.Context, orgName, repository string) (bool, error) {
	config, err := s.platformConfig(ctx)
	if err != nil || platform.RegistryPolicyOf(&config) == "" {
		return false, err
	}
	var orgs geassv1alpha1.GeassOrganizationList
	if err := s.Client.List(asSystem(ctx), &orgs, client.InNamespace(systemNamespace)); err != nil {
		return false, err
	}
	claims := platform.NewRegistryClaims(config.Spec.Registry.Repository, orgs.Items)
	return claims.WouldConflict(config.Spec.Registry.Repository, orgName, repository), nil
}

// removeOrganizationRegistry takes the login of an organization that is gone out of the system namespace.
func (s *Server) removeOrganizationRegistry(ctx context.Context, org string) {
	_ = s.deleteSystemSecret(asSystem(ctx), platform.OrgRegistrySecretName(org))
}
