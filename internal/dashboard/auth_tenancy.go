package dashboard

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/oidcauth"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// tenancyView is what the settings page is told about organizations.
type tenancyView struct {
	Enabled        bool     `json:"enabled"`
	OrgsClaim      string   `json:"orgsClaim"`
	PlatformClaim  string   `json:"platformClaim"`
	PlatformValues []string `json:"platformValues"`
	// Organizations is how many are set up.
	Organizations int `json:"organizations"`
	// AutoProvision is when a sign-in makes an organization: "off", "pending" or "open", with what it may make.
	AutoProvision      string   `json:"autoProvision"`
	AutoProvisionAllow []string `json:"autoProvisionAllow"`
	AutoProvisionMax   int      `json:"autoProvisionMax"`
	// EnableProblem says what stands in the way of turning organizations on, or is empty when nothing does.
	EnableProblem string `json:"enableProblem,omitempty"`
}

func tenancySpecOf(config geassv1alpha1.GeassPlatformConfig) geassv1alpha1.GeassPlatformTenancySpec {
	if config.Spec.Auth == nil || config.Spec.Auth.Tenancy == nil {
		return geassv1alpha1.GeassPlatformTenancySpec{}
	}
	return *config.Spec.Auth.Tenancy
}

// tenancyEnableProblem says why organizations cannot be turned on yet, or "" when they can. They change what everyone who signs
// in through the provider sees, so they are turned on only when the provider decides who signs in, the token's claim
// has been seen to put someone in an organization that is set up (or to make them a platform administrator), and a
// password administrator is there to come back with if it turns out wrong.
func (s *Server) tenancyEnableProblem(ctx context.Context, settings ssoSettings, hasPasswordAdmin bool, orgs int) string {
	candidate := settings.Config
	switch {
	case !settings.External():
		return "Turn on sign-in through the identity provider first: it is what says who is in which organization."
	case !candidate.Tenancy.On():
		return "Name the claim of the token that lists a person's organizations."
	case !hasPasswordAdmin:
		return "Keep an admin who signs in with a password: it is the way back in when the claims are not what they should be."
	case orgs == 0 && candidate.Tenancy.PlatformClaim == "":
		return "Set up an organization first."
	}
	result := s.ssoTest(candidate.Fingerprint())
	if !s.ssoTestedOK(candidate.Fingerprint(), time.Now()) {
		return "Run Test sign-in with these settings first, as someone who is an administrator of an organization or of the platform. The test has to be of these exact settings and no older than " + oidcTestValidity.String() + "."
	}
	placed := result.Platform
	for _, org := range result.Orgs {
		if org.Name != "" && !org.Suspended {
			placed = true
		}
	}
	if !placed {
		return "The test sign-in put the person in no organization that is set up, and made them no platform administrator. Check the claim and the organizations' ids."
	}
	return ""
}

// handlePlatformAuthTenancy saves how the token names organizations, and turns them on or off. The claims cannot change while
// organizations are on, so that no edit can send people into places that are not what they were.
func (s *Server) handlePlatformAuthTenancy(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, ssoSettingsPath) || !parseFormOrRedirect(w, r, ssoSettingsPath) || !s.requireSessionAdmin(w, r, "Authentication settings") {
		return
	}
	fail := func(message string) { redirectProbe(w, r, ssoSettingsPath, dashboardLiteralError, message) }
	config, err := s.platformConfig(r.Context())
	if err != nil {
		fail("could not load platform config")
		return
	}
	if config.Name == "" || config.Spec.Auth == nil || config.Spec.Auth.OIDC == nil {
		fail("Set up the identity provider first.")
		return
	}
	current := tenancySpecOf(config)
	desired := *current.DeepCopy()
	if formHasValue(r, "orgsClaim") {
		desired.OrgsClaim = strings.TrimSpace(r.FormValue("orgsClaim"))
	}
	if formHasValue(r, "platformClaim") {
		desired.PlatformClaim = strings.TrimSpace(r.FormValue("platformClaim"))
	}
	if formHasValue(r, "platformValues") {
		desired.PlatformValues = splitList(r.FormValue("platformValues"))
	}
	if formHasValue(r, "enabled") {
		switch strings.ToLower(strings.TrimSpace(r.FormValue("enabled"))) {
		case "on", boolTrue:
			desired.Enabled = true
		case "off", "false":
			desired.Enabled = false
		default:
			fail("enabled must be on or off.")
			return
		}
	}
	if formHasValue(r, "autoProvision", "autoProvisionAllow", "autoProvisionMax") {
		var mode, allow, max *string
		if formHasValue(r, "autoProvision") {
			value := r.FormValue("autoProvision")
			mode = &value
		}
		if formHasValue(r, "autoProvisionAllow") {
			value := r.FormValue("autoProvisionAllow")
			allow = &value
		}
		if formHasValue(r, "autoProvisionMax") {
			value := r.FormValue("autoProvisionMax")
			max = &value
		}
		next, problem := autoProvisionFrom(mode, allow, max, desired.AutoProvision)
		if problem != "" {
			fail(problem)
			return
		}
		desired.AutoProvision = next
	}
	claimsChanged := desired.OrgsClaim != current.OrgsClaim || desired.PlatformClaim != current.PlatformClaim || !slices.Equal(desired.PlatformValues, current.PlatformValues)
	if current.Enabled && claimsChanged {
		fail("Turn organizations off before changing how the token names them: people are working in them now.")
		return
	}
	// Read with the client secret, as the test sign-in was: the settings it tested are told apart by it.
	settings, err := s.ssoSettings(r.Context())
	if err != nil {
		fail("could not load the sign-in settings")
		return
	}
	candidate := settings.Config
	candidate.Tenancy = oidcauth.Tenancy{OrgsClaim: desired.OrgsClaim, PlatformClaim: desired.PlatformClaim, PlatformValues: desired.PlatformValues}
	if err := candidate.Validate(); err != nil {
		fail(err.Error())
		return
	}
	if desired.Enabled && !current.Enabled {
		hasPasswordAdmin := false
		if auth, authErr := s.dashboardAuth(r); authErr == nil {
			hasPasswordAdmin = adminHasPassword(auth.users)
		}
		orgs, err := s.listOrganizations(r.Context())
		if err != nil {
			fail("could not read the organizations")
			return
		}
		settings.Config = candidate
		if problem := s.tenancyEnableProblem(r.Context(), settings, hasPasswordAdmin, len(orgs)); problem != "" {
			fail(problem)
			return
		}
	}
	if err := s.updatePlatformConfig(r.Context(), &config, func(c *geassv1alpha1.GeassPlatformConfig) {
		spec := desired
		c.Spec.Auth.Tenancy = &spec
	}); err != nil {
		fail("could not save how organizations are named")
		return
	}
	s.forgetSSOProvider()
	// People who signed in before have no place to work in (or too many): they sign in again, which gives each their own.
	if desired.Enabled != current.Enabled {
		if err := s.revokeAllSSOSessions(r.Context()); err != nil {
			fail("organizations were changed, but the earlier sessions of the identity provider could not be ended")
			return
		}
	}
	ctrllog.FromContext(r.Context()).Info("Changed organizations", "enabled", desired.Enabled, "orgsClaim", desired.OrgsClaim, "login", s.currentSession(r).Username)
	redirect(w, r, ssoSettingsPath)
}
