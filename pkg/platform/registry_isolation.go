package platform

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// With spec.tenancy.registry set, a project that belongs to an organization never builds with the platform's registry
// login, and its images are kept apart from the platform's and from every other organization's:
//
//   - An organization's build pushes to the organization's own registry (spec.registry of its GeassOrganization), under
//     <repository>/<project>/<service>, with the organization's login, or to a registry its service names, with the login the
//     service names. It is never given the platform's login, nor another organization's, and an organization that has no
//     registry of its own does not build. Geass has no registry of its own that could hand an organization a login for a
//     path of the platform's repository: the platform's registry is one login for the whole of it.
//   - Parts of registries belong to someone (see RegistryClaims): the platform's repository to the platform,
//     <platform repository>/<organization> and an organization's own registry to that organization. An organization's
//     service may not run an image, and its build may not push to a repository, that is in a part that is not its own.
//
// The projects of no organization build and pull as they did.

const (
	// ConditionRegistryIsolated is on a service, and on a build, of an organization's project while spec.tenancy.registry
	// is set, and says whether what it pushes, pulls and logs in with is the organization's own. It is not there otherwise.
	ConditionRegistryIsolated = "RegistryIsolated"
	// RegistryReasonIsolated is why it is: nothing of the platform's or of another organization's is used.
	RegistryReasonIsolated = "Isolated"
	// RegistryReasonOwnRegistryRequired is why an organization's service does not build: the organization has no registry
	// of its own, and the platform's login is not given to it.
	RegistryReasonOwnRegistryRequired = "OwnRegistryRequired"
	// RegistryReasonPlatformCredential is why a build does not run: the login it was given is the platform's, or another
	// organization's.
	RegistryReasonPlatformCredential = "PlatformCredential"
	// RegistryReasonForeignRepository is why a service or build does not go on: the image it runs, or the repository it
	// pushes to, is in a part of a registry that belongs to the platform or to another organization.
	RegistryReasonForeignRepository = "ForeignRepository"
	// EventRegistryRefused is the warning event a service or build gets when the registry policy starts to hold it back.
	EventRegistryRefused = "RegistryRefused"

	// MessageRegistryIsolation starts what a service or build says while the registry policy holds it back. It is not a
	// failure to give up on: it goes when the organization's registry, the service's image or the platform's setting changes.
	MessageRegistryIsolation = "Registry isolation"

	// orgRegistrySecretPrefix starts the name of every Secret that holds the login an organization saved for its registry.
	orgRegistrySecretPrefix = "org-registry-"
)

// RegistryRefusedError is why the registry policy holds an organization's service or build back. Reason is the reason of
// its RegistryIsolated condition.
type RegistryRefusedError struct {
	Reason string
	Detail string
}

func (e *RegistryRefusedError) Error() string { return MessageRegistryIsolation + ": " + e.Detail }

func refused(reason, format string, args ...any) *RegistryRefusedError {
	return &RegistryRefusedError{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// RegistryPolicyOf is spec.tenancy.registry.policy of a platform configuration, or "" when the setting is not there. A
// setting with no policy is requireOwnRegistry, the default the API server writes.
func RegistryPolicyOf(config *geassv1alpha1.GeassPlatformConfig) string {
	if config == nil || config.Spec.Tenancy == nil || config.Spec.Tenancy.Registry == nil {
		return ""
	}
	if policy := config.Spec.Tenancy.Registry.Policy; policy != "" {
		return policy
	}
	return geassv1alpha1.RegistryPolicyRequireOwnRegistry
}

// CanonicalRepository is the repository an image reference names, spelled one way so that two spellings of one
// repository compare equal: in lower case, without its tag or digest, with the registry's host first (Docker Hub as
// docker.io, its official images under library/, and no :443). It is empty for an empty reference.
func CanonicalRepository(image string) string {
	repository := strings.ToLower(strings.Trim(ImageRepository(strings.TrimRight(strings.TrimSpace(image), "/")), "/"))
	if repository == "" {
		return ""
	}
	host, rest, hasPath := strings.Cut(repository, "/")
	if !hasPath || (!strings.ContainsAny(host, ".:") && host != "localhost") {
		host, rest = "docker.io", repository
	}
	host = strings.TrimSuffix(host, ":443")
	switch host {
	case "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com":
		host = "docker.io"
	}
	if host == "docker.io" && !strings.Contains(rest, "/") {
		rest = "library/" + rest
	}
	return host + "/" + rest
}

// within reports whether a canonical repository is the other one or below it, a whole path segment at a time.
func within(repository, prefix string) bool {
	return repository == prefix || strings.HasPrefix(repository, prefix+"/")
}

func overlap(a, b string) bool { return within(a, b) || within(b, a) }

// NamespacedImage is where an organization's build of a service pushes in the organization's registry: the repository the
// organization set, then the project and the service, so that each service has a repository of its own under the
// organization's. A tag the organization's repository has is kept.
func NamespacedImage(repository, project, app string) string {
	base := ImageRepository(repository)
	image := strings.TrimRight(base, "/") + "/" + project + "/" + app
	if tag := strings.TrimPrefix(strings.TrimSpace(repository), base); strings.HasPrefix(tag, ":") {
		image += tag
	}
	return image
}

// registryClaim is a part of a registry that belongs to someone: everything at or below repository.
type registryClaim struct {
	repository string
	// org is the organization it belongs to, and "" the platform.
	org string
	// contested is a claim that overlaps one it may not: nobody may use what is in it.
	contested bool
}

// RegistryClaims are the parts of registries that belong to the platform and to each organization:
//
//   - the platform's repository (spec.registry.repository) is the platform's;
//   - <platform repository>/<organization> is the organization's, so that one registry can hold each organization's images
//     beside the platform's when it authorizes each for its own path;
//   - an organization's own registry (spec.registry.repository of its GeassOrganization) is the organization's, unless it
//     overlaps the platform's repository outside the organization's own path in it, or overlaps another organization's
//     registry. Such a registry is contested: neither the organization nor anyone else may use what is in it.
//
// An image is in the claim that holds it most closely. An image in no claim (a public image, a registry nobody here
// claims) is in nobody's, and anyone may run it.
type RegistryClaims struct {
	claims []registryClaim
	// own is the canonical repository of each organization's own registry, for the organizations that have one.
	own map[string]string
}

// NewRegistryClaims works out who owns what, from the platform's repository and the organizations.
func NewRegistryClaims(platformRepository string, orgs []geassv1alpha1.GeassOrganization) RegistryClaims {
	platformRepo := CanonicalRepository(platformRepository)
	claims := RegistryClaims{own: map[string]string{}}
	if platformRepo != "" {
		claims.claims = append(claims.claims, registryClaim{repository: platformRepo})
	}
	for i := range orgs {
		name := orgs[i].Name
		if platformRepo != "" {
			claims.claims = append(claims.claims, registryClaim{repository: platformRepo + "/" + name, org: name})
		}
		if registry := orgs[i].Spec.Registry; registry != nil {
			if repository := CanonicalRepository(registry.Repository); repository != "" {
				claims.own[name] = repository
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(claims.own)) {
		claims.claims = append(claims.claims, registryClaim{repository: claims.own[name], org: name, contested: claims.ownConflicts(platformRepo, name, claims.own[name])})
	}
	return claims
}

// ownConflicts reports whether a repository may not be the organization's own: it overlaps the platform's repository
// outside <platform repository>/<organization>, or the registry of another organization.
func (c RegistryClaims) ownConflicts(platformRepo, org, repository string) bool {
	if platformRepo != "" && overlap(repository, platformRepo) && !within(repository, platformRepo+"/"+org) {
		return true
	}
	for other, theirs := range c.own {
		if other != org && overlap(repository, theirs) {
			return true
		}
	}
	return false
}

// WouldConflict reports whether the organization may not take the repository as its own registry, as things are now: it
// overlaps the platform's repository outside the organization's path in it, or another organization's registry. Who
// overlaps it is not said, so that one organization does not learn another's registry from it.
func (c RegistryClaims) WouldConflict(platformRepository, org, repository string) bool {
	canonical := CanonicalRepository(repository)
	return canonical != "" && c.ownConflicts(CanonicalRepository(platformRepository), org, canonical)
}

// OwnContested reports whether the organization's own registry is contested, and so cannot be used.
func (c RegistryClaims) OwnContested(org string) bool {
	repository, ok := c.own[org]
	if !ok {
		return false
	}
	for _, claim := range c.claims {
		if claim.org == org && claim.repository == repository {
			return claim.contested
		}
	}
	return false
}

// Allows reports whether the organization may run, or push to, the image: it is in no claim, or the claim that holds it
// most closely is the organization's and is not contested. An empty organization owns nothing.
func (c RegistryClaims) Allows(org, image string) bool {
	repository := CanonicalRepository(image)
	if repository == "" {
		return true
	}
	longest, owners, contested := -1, map[string]bool{}, false
	for _, claim := range c.claims {
		if !within(repository, claim.repository) {
			continue
		}
		switch length := len(claim.repository); {
		case length > longest:
			longest, owners, contested = length, map[string]bool{claim.org: true}, claim.contested
		case length == longest:
			owners[claim.org] = true
			contested = contested || claim.contested
		}
	}
	if longest < 0 {
		return true
	}
	return org != "" && !contested && len(owners) == 1 && owners[org]
}

// RegistryIsolation is spec.tenancy.registry as it applies to one project that belongs to an organization.
type RegistryIsolation struct {
	// Project is the project, and Org the organization it names. Org is empty when the project cannot be found, which is
	// held to everything an organization is and owns nothing.
	Project string
	Org     string
	// Organization is the organization, or nil when it is not there: such a project has no registry of its own.
	Organization *geassv1alpha1.GeassOrganization
	// Platform is the platform's registry, whose login is never handed over.
	Platform geassv1alpha1.GeassPlatformRegistrySpec
	Claims   RegistryClaims
}

// ResolveRegistryIsolation is spec.tenancy.registry for the project's services and builds, or nil when the setting is off
// or the project belongs to no organization, in which case nothing changes and nothing else is read. A project that is
// named but cannot be found is held to it, owning nothing: it cannot be told whose it is.
func ResolveRegistryIsolation(ctx context.Context, c client.Reader, project string) (*RegistryIsolation, error) {
	var config geassv1alpha1.GeassPlatformConfig
	if err := c.Get(ctx, client.ObjectKey{Name: HAReadinessName, Namespace: SystemNamespace}, &config); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if RegistryPolicyOf(&config) == "" || project == "" {
		return nil, nil
	}
	isolation := &RegistryIsolation{Project: project, Platform: config.Spec.Registry}
	var p geassv1alpha1.GeassProject
	switch err := c.Get(ctx, client.ObjectKey{Name: project, Namespace: SystemNamespace}, &p); {
	case apierrors.IsNotFound(err):
	case err != nil:
		return nil, err
	case p.Spec.Org == "":
		return nil, nil
	default:
		isolation.Org = p.Spec.Org
	}
	var orgs geassv1alpha1.GeassOrganizationList
	if err := c.List(ctx, &orgs, client.InNamespace(SystemNamespace)); err != nil {
		return nil, err
	}
	for i := range orgs.Items {
		if isolation.Org != "" && orgs.Items[i].Name == isolation.Org {
			isolation.Organization = &orgs.Items[i]
		}
	}
	isolation.Claims = NewRegistryClaims(config.Spec.Registry.Repository, orgs.Items)
	return isolation, nil
}

// ownRegistry is the organization's own registry, or nil.
func (i *RegistryIsolation) ownRegistry() *geassv1alpha1.GeassPlatformRegistrySpec {
	if i.Organization == nil || i.Organization.Spec.Registry == nil || i.Organization.Spec.Registry.Repository == "" {
		return nil
	}
	return i.Organization.Spec.Registry
}

// BuildRegistry is where a service of the project pushes its builds, and the login it pushes with, or why it may not
// build. A service that names a registry of its own keeps it, with the login it names, as it always did. Otherwise the
// organization's registry is used, under <repository>/<project>/<service>, with the organization's login (or the one the
// service names). The platform's registry and login are never used: an organization with no registry of its own does
// not build.
func (i *RegistryIsolation) BuildRegistry(ctx context.Context, c client.Reader, app *geassv1alpha1.GeassApp) (string, *corev1.LocalObjectReference, error) {
	repository, credential := app.Spec.Build.Registry.Repository, app.Spec.Build.Registry.CredentialRef
	if repository == "" {
		own := i.ownRegistry()
		if own == nil {
			return "", nil, refused(RegistryReasonOwnRegistryRequired, "the organization has no registry of its own, and an organization's builds are never given the platform's registry login. An administrator of the organization sets one under Settings > Registry")
		}
		if i.Claims.OwnContested(i.Org) {
			return "", nil, refused(RegistryReasonForeignRepository, "the organization's registry %s overlaps a part of a registry that belongs to the platform or to another organization, so nothing is built into it. An administrator of the organization chooses another one under Settings > Registry", own.Repository)
		}
		repository = NamespacedImage(own.Repository, app.Spec.Project, app.Name)
		if credential == nil {
			credential = own.CredentialRef
		}
	}
	if err := i.CheckDestination(repository); err != nil {
		return "", nil, err
	}
	if err := i.CheckCredential(ctx, c, app.Namespace, credential); err != nil {
		return "", nil, err
	}
	return repository, credential, nil
}

// CheckDestination refuses a repository to push to that is in a part of a registry that is not the organization's.
func (i *RegistryIsolation) CheckDestination(repository string) error {
	if !i.Claims.Allows(i.Org, repository) {
		return refused(RegistryReasonForeignRepository, "the build would push to %s, which is in a part of a registry that belongs to the platform or to another organization. An organization's builds push only to its own registry", repository)
	}
	return nil
}

// CheckImage refuses an image for an organization's service that is in a part of a registry that is not the organization's,
// or that is named by its ID alone (sha256:…): that names no repository whose owner could be told, and a node that holds
// the image starts it without asking any registry.
func (i *RegistryIsolation) CheckImage(image string) error {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(image)), "sha256:") {
		return refused(RegistryReasonForeignRepository, "the image %s is named by its ID alone, which says nothing of whose it is. Name its repository", image)
	}
	if !i.Claims.Allows(i.Org, image) {
		return refused(RegistryReasonForeignRepository, "the image %s is in a part of a registry that belongs to the platform or to another organization, and an organization's service runs only images of its own registry or of nobody's here", image)
	}
	return nil
}

// CheckCredential refuses a registry login, named in the namespace, that an organization's build or pods may not be given:
// the platform's (by name, or a Secret that holds the same login), or one Geass saved for another organization. A login
// that is not there is not refused here: copying it fails, and says so.
func (i *RegistryIsolation) CheckCredential(ctx context.Context, c client.Reader, namespace string, ref *corev1.LocalObjectReference) error {
	if ref == nil || ref.Name == "" {
		return nil
	}
	platformLogin := refused(RegistryReasonPlatformCredential, "the registry login %s is the platform's, which an organization's build or service is never given. Name the organization's own login, or none", ref.Name)
	if ref.Name == PlatformRegistrySecretName || (i.Platform.CredentialRef != nil && ref.Name == i.Platform.CredentialRef.Name) {
		return platformLogin
	}
	otherOrg := refused(RegistryReasonPlatformCredential, "the registry login %s is another organization's, which this organization's build or service is never given", ref.Name)
	if strings.HasPrefix(ref.Name, orgRegistrySecretPrefix) && (i.Org == "" || ref.Name != OrgRegistrySecretName(i.Org)) {
		return otherOrg
	}
	var secret corev1.Secret
	switch err := c.Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: namespace}, &secret); {
	case apierrors.IsNotFound(err):
		return nil
	case err != nil:
		return err
	}
	if owner := secret.Labels[LabelOrgBuild]; owner != "" && owner != i.Org {
		return otherOrg
	}
	holds, err := i.HoldsPlatformLogin(ctx, c, secret.Data[corev1.DockerConfigJsonKey])
	if err != nil {
		return err
	}
	if holds {
		return platformLogin
	}
	return nil
}

// HoldsPlatformLogin reports whether a .dockerconfigjson holds a login of the platform's registry Secret, under any name:
// the same file, or the same user and password (or token) for any registry.
func (i *RegistryIsolation) HoldsPlatformLogin(ctx context.Context, c client.Reader, config []byte) (bool, error) {
	if len(config) == 0 {
		return false, nil
	}
	names := []string{PlatformRegistrySecretName}
	if ref := i.Platform.CredentialRef; ref != nil && ref.Name != "" && ref.Name != PlatformRegistrySecretName {
		names = append(names, ref.Name)
	}
	for _, name := range names {
		var secret corev1.Secret
		switch err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: SystemNamespace}, &secret); {
		case apierrors.IsNotFound(err):
			continue
		case err != nil:
			return false, err
		}
		if HoldsLoginOf(config, secret.Data[corev1.DockerConfigJsonKey]) {
			return true, nil
		}
	}
	return false, nil
}

// HoldsLoginOf reports whether the .dockerconfigjson candidate holds any login that of holds: it is the same file, or one
// of its registries has the same user and password, or the same token, as one of the other's. Either may be a file that is
// not JSON, which is compared as it is.
func HoldsLoginOf(candidate, of []byte) bool {
	if len(candidate) == 0 || len(of) == 0 {
		return false
	}
	if bytes.Equal(bytes.TrimSpace(candidate), bytes.TrimSpace(of)) {
		return true
	}
	theirs := registryLogins(of)
	for _, login := range registryLogins(candidate) {
		if slices.Contains(theirs, login) {
			return true
		}
	}
	return false
}

// registryLogins are the logins in a .dockerconfigjson: user:password, from "auth" or from "username" and "password", and
// the identity and registry tokens.
func registryLogins(config []byte) []string {
	var file struct {
		Auths map[string]struct {
			Auth          string `json:"auth"`
			Username      string `json:"username"`
			Password      string `json:"password"`
			IdentityToken string `json:"identitytoken"`
			RegistryToken string `json:"registrytoken"`
		} `json:"auths"`
	}
	if json.Unmarshal(config, &file) != nil {
		return nil
	}
	var logins []string
	for _, entry := range file.Auths {
		if decoded, err := base64.StdEncoding.DecodeString(entry.Auth); err == nil && len(decoded) > 0 {
			logins = append(logins, "login:"+string(decoded))
		}
		if entry.Username != "" || entry.Password != "" {
			logins = append(logins, "login:"+entry.Username+":"+entry.Password)
		}
		for _, token := range []string{entry.IdentityToken, entry.RegistryToken} {
			if token != "" {
				logins = append(logins, "token:"+token)
			}
		}
	}
	return logins
}
