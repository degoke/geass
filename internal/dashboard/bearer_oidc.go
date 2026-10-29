package dashboard

import (
	"errors"
	"net/http"
	"time"

	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/oidcauth"
)

// A person who signed in at the identity provider can also call the API with an access token the provider issued to them (see
// oidcauth.VerifyAccessToken for what is accepted), when the platform's configuration turns it on (spec.auth.oidc.accessTokens).
// It is how a page that embeds the dashboard, and is signed in at the provider itself, acts as the person in the cluster: no
// Geass token is made or kept for it, and what the cluster records is the person.
//
// The token becomes the session the person would have signed in with, by the same rules (the role claim and the required
// claims, or the organizations and the platform claim), so it gives nothing more than signing in does. It is a bearer
// credential like an API token, and is held to the same limits: it is refused the routes that decide who can sign in or make a
// credential (see credentialIsBearer), and a stream or terminal it opened ends when it expires.

var (
	// errOIDCBearerDenied is a token the provider issued, for this cluster, whose person the configuration does not let in.
	errOIDCBearerDenied = errors.New("this access token does not give access here")
	// errOIDCBearerUnavailable is a token that could not be checked because what it is checked against could not be read.
	errOIDCBearerUnavailable = errors.New("could not verify the access token")
)

// oidcBearer marks a request that was authenticated by an identity provider's access token.
type oidcBearer struct {
	// Expires is when the token stops being good. A stream it opened ends then.
	Expires time.Time
}

type oidcBearerKey struct{}

// oidcBearerFromRequest returns the access token that authenticated this request, or nil.
func oidcBearerFromRequest(r *http.Request) *oidcBearer {
	bearer, _ := r.Context().Value(oidcBearerKey{}).(*oidcBearer)
	return bearer
}

// credentialIsBearer is whether the request is authenticated by a credential that was sent in a header (an API token, or an
// access token of the provider) and not by the session of a person who signed in at the dashboard. What the browser does by
// itself, such as sending a cookie, is not what it is about: a bearer credential is never attached by the browser, and it is
// never what may decide who can sign in.
func credentialIsBearer(r *http.Request) bool {
	return tokenFromRequest(r) != nil || oidcBearerFromRequest(r) != nil
}

// isOIDCBearerCandidate is whether a bearer credential is to be tried as the provider's access token: it is not shaped as one
// of Geass's own tokens, which are looked up as such and refused when they are not good.
func isOIDCBearerCandidate(presented string) bool {
	return !apitokens.LooksLike(presented)
}

// authenticateOIDCAccessToken turns a bearer credential into the session of the person it was issued to. It says
// errInvalidToken when the credential is not an access token this cluster takes (including when it takes none), and
// errOIDCBearerDenied when it is one but the configuration lets its person in nowhere.
func (s *Server) authenticateOIDCAccessToken(r *http.Request, presented string) (*dashboardSessionInfo, *oidcBearer, error) {
	ctx := r.Context()
	// The cheap things first, so that a credential that cannot be an access token costs one read of the configuration: only
	// while the provider is what decides who signs in, and only when it was turned on, is a credential that is not a Geass
	// token anything; and only a JWT of the right type is worth reading the client secret for.
	config, err := s.platformConfig(ctx)
	if err != nil {
		return nil, nil, errOIDCBearerUnavailable
	}
	if enabled := ssoSettingsOf(config); !enabled.External() || !enabled.AccessTokens || !oidcauth.PlausibleAccessToken(presented) {
		return nil, nil, errInvalidToken
	}
	settings, err := s.ssoSettings(ctx)
	if err != nil {
		return nil, nil, errOIDCBearerUnavailable
	}
	audience := settings.AccessTokenAudience
	if audience == "" {
		audience = settings.Config.ClientID
	}
	provider, err := s.ssoProvider(ctx, settings)
	if err != nil {
		return nil, nil, errOIDCBearerUnavailable
	}
	now := time.Now()
	token, err := provider.VerifyAccessToken(ctx, presented, audience, now)
	if err != nil {
		ctrllog.FromContext(ctx).V(1).Info("Refused a bearer credential that is not a valid access token", "reason", err.Error())
		return nil, nil, errInvalidToken
	}
	// The claims of a token are as they were when it was issued, and nothing asks the provider again. So it may last no longer
	// than a sign-in does (sessionMinutes): that is how long a role that changed, or a person who was removed, can go on being
	// honoured, and it keeps a token from outliving the memory of a sign-out (ssoRevocationKeep, which no session exceeds).
	if token.Expires.Sub(token.IssuedAt) > settings.sessionTTL() {
		ctrllog.FromContext(ctx).V(1).Info("Refused an access token that lives longer than a sign-in", "lifetime", token.Expires.Sub(token.IssuedAt).String())
		return nil, nil, errInvalidToken
	}
	// Who signed out, or was removed, after the token was issued is not honoured through it: the same record that ends the
	// sessions of a person (a sign-out here, a back-channel logout from the provider) ends it. A token says when it was issued
	// in whole seconds and a sign-out is noted to the millisecond, so a token is taken to be from before it only when its whole
	// second was: one issued in the second of the sign-out is the person signing in again.
	auth, err := s.authForRequest(r)
	if err != nil {
		return nil, nil, errOIDCBearerUnavailable
	}
	if issuedEnd := token.IssuedAt.UnixMilli() + time.Second.Milliseconds(); issuedEnd <= auth.ssoNotBefore || issuedEnd <= auth.ssoRevoked[token.Key] {
		return nil, nil, errInvalidToken
	}
	identity, err := settings.Config.AuthorizeAccessToken(token.Claims)
	if err != nil {
		return nil, nil, deniedBearer(err)
	}
	name := identity.Email
	if name == "" {
		// The actor in what is recorded is the person, and a token that has no address names them by their subject.
		name = token.Subject
	}
	claims := ssoSessionClaims{Email: name, Role: identity.Role, Identity: token.Key, IssuedMillis: token.IssuedAt.UnixMilli()}
	if settings.TenancyOn {
		// A token never makes an organization: that is something a person does by signing in, once.
		settings.AutoProvision = nil
		if refused := s.placeInOrganization(ctx, &claims, identity, settings); refused != "" {
			if refused == ssoUnavailable {
				return nil, nil, errOIDCBearerUnavailable
			}
			return nil, nil, &deniedError{reason: refused}
		}
	}
	role := normalizeDashboardRole(claims.Role)
	if role == "" {
		role = dashboardRoleViewer
	}
	session := &dashboardSessionInfo{
		Username: claims.Email, Role: role, CanMutate: role == dashboardRoleAdmin, CanReadSensitive: role == dashboardRoleAdmin,
		Identity: claims.Identity, PlatformAdmin: claims.Platform, Org: claims.Org, Workspace: claims.Workspace, Grants: claims.Grants,
	}
	return session, &oidcBearer{Expires: token.Expires}, nil
}

// deniedBearer is the refusal of a token whose person the configuration does not let in, with the code of why.
func deniedBearer(err error) error {
	var denied *oidcauth.Denied
	if errors.As(err, &denied) {
		return &deniedError{reason: denied.Reason}
	}
	return errOIDCBearerUnavailable
}

// deniedError is errOIDCBearerDenied with the code of why, which is safe to tell the caller.
type deniedError struct{ reason string }

func (e *deniedError) Error() string { return errOIDCBearerDenied.Error() + " (" + e.reason + ")" }
func (e *deniedError) Unwrap() error { return errOIDCBearerDenied }

// writeOIDCBearerError answers a request whose bearer credential was not accepted.
func writeOIDCBearerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidToken):
		writeJSON(w, http.StatusUnauthorized, map[string]string{dashboardLiteralError: errInvalidToken.Error()})
	case errors.Is(err, errOIDCBearerDenied):
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: err.Error()})
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{dashboardLiteralError: errOIDCBearerUnavailable.Error()})
	}
}
