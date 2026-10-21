package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/oidcauth"
)

// Organizations that make themselves. The platform can let the first sign-in of an organization's administrator create the
// organization in Geass, for a provider that only people you trust can make organizations in. It is never for any person
// who signs in: they have to administer the organization at the provider and have a verified address; the organization has
// to be one the platform said it may make; and there is a limit to how many can be made this way.

const (
	// annotationAutoCreated marks an organization that was made by a sign-in and not by a platform administrator.
	annotationAutoCreated = "geass.dev/auto-created"
	// annotationAutoCreatedBy names who made it, as the provider knows them.
	annotationAutoCreatedBy = "geass.dev/auto-created-by"

	autoProvisionPending     = "Pending"
	autoProvisionOpen        = "Open"
	defaultAutoProvisionMax  = 20
	maxAutoProvisionDisplay  = 100
	maxAutoProvisionPatterns = 50
)

// allowPattern is what an entry of the allow list may look like: the provider's id for an organization, or its start with a
// star at the end.
var allowPattern = regexp.MustCompile(`^[A-Za-z0-9._:@/-]{1,200}\*?$`)

// autoOrganizationName is the name of the organization made for an id of the provider. It is worked out from the provider and
// the id, so that two sign-ins at once make the one organization, and an id cannot be made twice.
func autoOrganizationName(issuer, idpID string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(issuer, "/") + "\x00" + idpID))
	return "org-" + hex.EncodeToString(sum[:4])
}

// autoProvisionAllows is whether the settings let an organization of the provider be made.
func autoProvisionAllows(spec *geassv1alpha1.GeassPlatformAutoProvisionSpec, idpID string) bool {
	if spec == nil {
		return false
	}
	if len(spec.Allow) == 0 {
		return spec.Mode == autoProvisionPending
	}
	return slices.ContainsFunc(spec.Allow, func(pattern string) bool {
		if prefix, star := strings.CutSuffix(pattern, "*"); star {
			return strings.HasPrefix(idpID, prefix)
		}
		return pattern == idpID
	})
}

// autoProvisionOrganizations makes the organizations the person administers at the provider that Geass does not know, when the
// settings allow. It is the dashboard's own work, done for a sign-in the provider has already vouched for. It reports what it
// did, so that a failure is not mistaken for there being nothing to make.
func (s *Server) autoProvisionOrganizations(ctx context.Context, settings ssoSettings, identity *oidcauth.Identity, orgs []geassv1alpha1.GeassOrganization) error {
	spec := settings.AutoProvision
	// A platform administrator does not need an organization, and an address the provider has not said is verified is not
	// one to make organizations for.
	if spec == nil || identity.Platform || !identity.EmailVerified {
		return nil
	}
	limit := int32(defaultAutoProvisionMax)
	if spec.Max != nil {
		limit = *spec.Max
	}
	made := 0
	for i := range orgs {
		if orgs[i].Annotations[annotationAutoCreated] == boolTrue {
			made++
		}
	}
	logger := ctrllog.FromContext(ctx)
	for _, member := range identity.Memberships {
		if member.Role != oidcauth.RoleAdmin || !autoProvisionAllows(spec, member.Org) {
			continue
		}
		if slices.ContainsFunc(orgs, func(org geassv1alpha1.GeassOrganization) bool { return org.Spec.IdPID == member.Org }) {
			continue
		}
		if int32(made) >= limit {
			logger.Info("Did not make an organization: the limit of organizations made by sign-in was reached", "limit", limit)
			return nil
		}
		display := member.Org
		if utf8.RuneCountInString(display) > maxAutoProvisionDisplay {
			display = string([]rune(display)[:maxAutoProvisionDisplay])
		}
		org := &geassv1alpha1.GeassOrganization{
			ObjectMeta: metav1.ObjectMeta{
				Name: autoOrganizationName(settings.Config.Issuer, member.Org), Namespace: systemNamespace,
				Annotations: map[string]string{annotationAutoCreated: boolTrue, annotationAutoCreatedBy: identity.Key},
			},
			Spec: geassv1alpha1.GeassOrganizationSpec{DisplayName: display, IdPID: member.Org, Suspended: spec.Mode == autoProvisionPending},
		}
		if err := s.Client.Create(asSystem(ctx), org); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
		made++
		logger.Info("Made an organization for a sign-in", "organization", org.Name, "idpID", member.Org, "mode", spec.Mode, "by", identity.Email)
	}
	return nil
}

// waitingForApproval is whether the person is an administrator, at the provider, of an organization that was made for a
// sign-in and is waiting to be approved: it is suspended until a platform administrator opens it.
func waitingForApproval(identity *oidcauth.Identity, orgs []geassv1alpha1.GeassOrganization) bool {
	for _, member := range identity.Memberships {
		for i := range orgs {
			if orgs[i].Spec.IdPID == member.Org && orgs[i].Spec.Suspended && orgs[i].Annotations[annotationAutoCreated] == boolTrue {
				return true
			}
		}
	}
	return false
}

// autoProvisionFrom reads the auto-provision form fields onto the tenancy settings, or says what is wrong with them. A field
// that is nil was not sent and stays as it was. Off takes the setting away.
func autoProvisionFrom(mode, allow, max *string, current *geassv1alpha1.GeassPlatformAutoProvisionSpec) (*geassv1alpha1.GeassPlatformAutoProvisionSpec, string) {
	next := &geassv1alpha1.GeassPlatformAutoProvisionSpec{}
	if current != nil {
		next = current.DeepCopy()
	}
	if mode != nil {
		switch strings.ToLower(strings.TrimSpace(*mode)) {
		case "off":
			return nil, ""
		case "pending":
			next.Mode = autoProvisionPending
		case "open":
			next.Mode = autoProvisionOpen
		default:
			return nil, "Organizations made by sign-in are off, pending approval, or open."
		}
	}
	if allow != nil {
		patterns := splitList(*allow)
		if len(patterns) > maxAutoProvisionPatterns {
			return nil, "At most " + strconv.Itoa(maxAutoProvisionPatterns) + " ids or prefixes can be allowed."
		}
		for _, pattern := range patterns {
			if !allowPattern.MatchString(pattern) {
				return nil, strconv.Quote(pattern) + " is not an id of the provider, or the start of one followed by *."
			}
		}
		next.Allow = patterns
	}
	if max != nil {
		if strings.TrimSpace(*max) == "" {
			next.Max = nil
		} else {
			value, err := strconv.Atoi(strings.TrimSpace(*max))
			if err != nil || value < 1 || value > 500 {
				return nil, "The most organizations made by sign-in is a whole number from 1 to 500."
			}
			count := int32(value)
			next.Max = &count
		}
	}
	if next.Mode == "" {
		return nil, "Choose whether organizations made by sign-in are pending approval or open."
	}
	if next.Mode == autoProvisionOpen && len(next.Allow) == 0 {
		return nil, "Open needs a list of the ids or prefixes that may be made, so that it is never every organization the provider has."
	}
	return next, ""
}
