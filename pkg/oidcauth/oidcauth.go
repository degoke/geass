// Package oidcauth lets Geass sign people in through an identity provider that speaks OpenID Connect (Better
// Auth, Keycloak, Authentik, Zitadel, Auth0, Okta and the like). The provider owns the people: who exists, who was
// invited, what organization or team they are in. Geass only checks the answer: it verifies the provider's signed ID
// token and turns one claim of it into one of its two roles.
//
// Everything that decides a sign-in lives here and is pure enough to test without a browser: building the redirect,
// exchanging the code (with PKCE), verifying the token, and mapping claims to a role.
package oidcauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/degoke/geass/pkg/netguard"
)

// The roles a sign-in can map to. Geass has no others for people: a token can also be an observer, but a person
// cannot.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

// DefaultScopes are asked for when Config.Scopes is empty.
var DefaultScopes = []string{oidc.ScopeOpenID, "email", "profile"}

// signingAlgorithms are the algorithms an ID token may be signed with: public-key ones only. A symmetric one could
// be forged by anybody who has the public key, and "none" is not a signature.
var signingAlgorithms = []string{"EdDSA", "ES256", "ES384", "ES512", "RS256", "RS384", "RS512", "PS256", "PS384", "PS512"}

const (
	// fetchTimeout bounds every request to the provider: discovery, keys and the token endpoint.
	fetchTimeout = 10 * time.Second
	// logoutTokenMaxAge is how old a back-channel logout token may be.
	logoutTokenMaxAge = 5 * time.Minute
)

// Config is what an admin sets up: which provider to trust and how its claims become a Geass role.
type Config struct {
	// Issuer is the provider's issuer URL, exactly as it appears in its discovery document.
	Issuer       string
	ClientID     string
	ClientSecret string
	// Scopes are asked for besides what DefaultScopes holds when empty.
	Scopes []string
	// RoleClaim is the claim of the ID token that holds the person's role at the provider: a name, or a dotted path
	// into nested claims ("org.role"). Its value may be one string, a list, or a comma-separated string (which is how
	// Better Auth stores a member with several roles).
	RoleClaim string
	// RoleMap says which Geass role each provider role is: {"owner": "admin", "member": "viewer"}. A person whose
	// roles map to nothing cannot sign in.
	RoleMap map[string]string
	// RequireClaim limits sign-in to people whose token has a claim with a value: {"organization_id": "org_123"}
	// lets only members of that organization in. The claim may be a string or a list that contains the value.
	RequireClaim map[string]string
	// Tenancy, when it names an organizations claim, makes the person's place in each organization of the provider
	// what the role is read from, in place of RoleClaim.
	Tenancy Tenancy
}

// Fingerprint identifies a configuration, so that a test sign-in can be tied to exactly the settings it tested.
func (c Config) Fingerprint() string {
	scopes := slices.Clone(c.Scopes)
	slices.Sort(scopes)
	encoded, _ := json.Marshal(struct {
		Issuer, ClientID, Secret, RoleClaim string
		Scopes                              []string
		RoleMap, RequireClaim               map[string]string
		Tenancy                             Tenancy
	}{strings.TrimRight(c.Issuer, "/"), c.ClientID, c.ClientSecret, c.RoleClaim, scopes, c.RoleMap, c.RequireClaim, c.Tenancy})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// Validate says what is missing or wrong in a configuration before anything connects.
func (c Config) Validate() error {
	issuer, err := url.Parse(strings.TrimSpace(c.Issuer))
	switch {
	case err != nil || issuer.Host == "" || issuer.RawQuery != "" || issuer.Fragment != "":
		return errors.New("the issuer must be a URL such as https://auth.example.com")
	case issuer.Scheme != "https" && (issuer.Scheme != "http" || !netguard.IsLoopbackHost(issuer.Hostname()) || !netguard.LoopbackAllowed()):
		return errors.New("the issuer must be an https URL")
	case strings.TrimSpace(c.ClientID) == "":
		return errors.New("the client ID is required")
	case strings.TrimSpace(c.RoleClaim) == "" && !c.Tenancy.On():
		return errors.New("name the claim that holds the person's role")
	case len(c.RoleMap) == 0:
		return errors.New("map at least one role of the provider to admin or viewer")
	}
	if err := netguard.CheckHost(issuer.Hostname()); err != nil {
		return err
	}
	for role, mapped := range c.RoleMap {
		if strings.TrimSpace(role) == "" || (mapped != RoleAdmin && mapped != RoleViewer) {
			return fmt.Errorf("role %q must map to %s or %s", role, RoleAdmin, RoleViewer)
		}
	}
	for claim, value := range c.RequireClaim {
		if strings.TrimSpace(claim) == "" || strings.TrimSpace(value) == "" {
			return errors.New("a required claim needs a name and a value")
		}
	}
	return c.Tenancy.validate()
}

// Key is the stable name of a person at a provider. It is made from the issuer and the subject, which the provider
// never reuses, and not from the email, which changes. It is what sessions and the tokens a person made are tied to.
func Key(issuer, subject string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(issuer, "/") + "\x00" + subject))
	return "sso:" + hex.EncodeToString(sum[:16])
}

// Identity is a person the provider vouched for and the role Geass gives them.
type Identity struct {
	Issuer  string
	Subject string
	// Key is Key(Issuer, Subject).
	Key   string
	Email string
	// Role is the Geass role the person has: with organizations on, the highest of the roles they have in them.
	Role string
	// Memberships are the organizations the token puts the person in, and Platform is whether it makes them an
	// administrator of the whole platform. Both are set only when the configuration reads organizations.
	Memberships []Membership
	Platform    bool
	// EmailVerified is whether the provider said, in so many words, that it has verified the address. A provider that
	// does not say leaves the person signed in, but not verified.
	EmailVerified bool
	// RefreshToken is what the provider gave to ask it again, if it gave one. Only Exchange sets it. It is a credential,
	// and is never shown, logged or kept except in what the dashboard keeps for refreshing a session.
	RefreshToken string
}

// Denied is a sign-in the provider allowed and Geass did not. Reason is a short fixed code, safe to put in a URL
// and to show; Detail says more for the admin testing the setup.
type Denied struct {
	Reason string
	Detail string
}

func (d *Denied) Error() string { return "sign-in denied: " + d.Detail }

// The reasons a sign-in is denied.
const (
	DeniedNoEmail         = "no_email"
	DeniedEmailUnverified = "email_unverified"
	DeniedRequiredClaim   = "required_claim"
	DeniedNoRole          = "no_role"
)

// Provider is a configured identity provider: its discovery document and keys are fetched once and kept, and keys
// are fetched again when a token arrives signed with one that is not known (which is how a rotation looks), at most
// once in keyRefetchCooldown, and when they are old (keyMaxAge). A key the provider withdraws stays trusted until the
// kept keys are replaced; that is acceptable because an ID token is taken from the provider's own token endpoint over
// TLS, and is tied to the sign-in by PKCE and the nonce.
type Provider struct {
	config   Config
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	// client makes every request to the provider, and keeps its connections between sign-ins.
	client *http.Client
}

// Discover connects to the issuer and reads what it publishes. It fails when the issuer cannot be reached, is a
// local address, or does not call itself what the configuration says.
func Discover(ctx context.Context, config Config) (*Provider, error) {
	config.Issuer = strings.TrimSpace(config.Issuer)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	client := netguard.Client(fetchTimeout)
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, client), config.Issuer)
	if err != nil {
		return nil, fmt.Errorf("could not read the provider's discovery document: %w", err)
	}
	keysURL, err := checkEndpoints(provider)
	if err != nil {
		return nil, err
	}
	return &Provider{
		config:   config,
		client:   client,
		provider: provider,
		verifier: oidc.NewVerifier(config.Issuer, newKeySet(keysURL, client), &oidc.Config{ClientID: config.ClientID, SupportedSigningAlgs: signingAlgorithms}),
	}, nil
}

// checkEndpoints refuses a provider whose discovery document sends Geass somewhere unsafe. The issuer was checked, but
// the document names the other addresses: the code, the client secret and the keys all travel to and from them, so a
// document that lists one over plain http (a provider behind a proxy that ends TLS, set up carelessly) would let anyone
// on the network read the secret or hand Geass keys of their own.
func checkEndpoints(provider *oidc.Provider) (keysURL string, err error) {
	var document struct {
		KeysURL string `json:"jwks_uri"`
	}
	if err := provider.Claims(&document); err != nil {
		return "", fmt.Errorf("could not read the provider's discovery document: %w", err)
	}
	endpoint := provider.Endpoint()
	for name, raw := range map[string]string{"authorization": endpoint.AuthURL, "token": endpoint.TokenURL, "keys": document.KeysURL} {
		if err := endpointAllowed(raw); err != nil {
			return "", fmt.Errorf("the provider's %s endpoint is not allowed: %w", name, err)
		}
	}
	return document.KeysURL, nil
}

// endpointAllowed is whether an address the provider published may be used: https, or plain http to this machine when
// the operator allows that (a developer's own provider).
func endpointAllowed(raw string) error {
	address, err := url.Parse(raw)
	switch {
	case err != nil || address.Host == "":
		return fmt.Errorf("%q is not an address", raw)
	case address.Scheme == "https":
		return nil
	case address.Scheme == "http" && netguard.IsLoopbackHost(address.Hostname()) && netguard.LoopbackAllowed():
		return nil
	}
	return fmt.Errorf("%q is not an https address", raw)
}

// Config is the configuration the provider was made from.
func (p *Provider) Config() Config { return p.config }

func (p *Provider) oauth(redirectURL string) *oauth2.Config {
	scopes := p.config.Scopes
	if len(scopes) == 0 {
		scopes = DefaultScopes
	} else if !slices.Contains(scopes, oidc.ScopeOpenID) {
		scopes = append([]string{oidc.ScopeOpenID}, scopes...)
	}
	return &oauth2.Config{
		ClientID:     p.config.ClientID,
		ClientSecret: p.config.ClientSecret,
		Endpoint:     p.provider.Endpoint(),
		RedirectURL:  redirectURL,
		Scopes:       scopes,
	}
}

// AuthCodeURL is where to send the browser to sign in. state ties the answer to this browser, nonce ties the token
// to this request, and verifier (from NewVerifier) is what proves, when the code is exchanged, that the one who
// asks is the one who started.
func (p *Provider) AuthCodeURL(redirectURL, state, nonce, verifier string) string {
	return p.oauth(redirectURL).AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))
}

// NewVerifier makes the secret half of a PKCE pair.
func NewVerifier() string { return oauth2.GenerateVerifier() }

// Exchange trades the code the provider sent back for a verified person. It checks the token's signature, issuer,
// audience, expiry and nonce, and then the rules of the configuration (Authorize).
func (p *Provider) Exchange(ctx context.Context, redirectURL, code, verifier, nonce string) (*Identity, error) {
	ctx = oidc.ClientContext(ctx, p.client)
	token, err := p.oauth(redirectURL).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("could not exchange the code: %w", err)
	}
	raw, _ := token.Extra("id_token").(string)
	if raw == "" {
		return nil, errors.New("the provider returned no ID token; is the openid scope allowed for this client?")
	}
	idToken, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("could not verify the ID token: %w", err)
	}
	if nonce == "" || idToken.Nonce != nonce {
		return nil, errors.New("the ID token does not belong to this sign-in")
	}
	// Everyone without a subject would share one identity, and one person's sign-out would end everybody's.
	if strings.TrimSpace(idToken.Subject) == "" {
		return nil, errors.New("the ID token names no subject")
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("could not read the ID token: %w", err)
	}
	identity, err := p.config.Authorize(claims)
	if err != nil {
		return nil, err
	}
	identity.Issuer, identity.Subject = idToken.Issuer, idToken.Subject
	identity.Key = Key(idToken.Issuer, idToken.Subject)
	identity.RefreshToken = token.RefreshToken
	return identity, nil
}

// ErrRefreshRevoked is what Refresh says when the provider no longer honours the refresh token: the person signed out
// there, was removed, or the grant was withdrawn. It is the provider's answer, and not a failure to reach it.
var ErrRefreshRevoked = errors.New("the provider no longer honours the session")

// Refreshed is what asking the provider again found.
type Refreshed struct {
	// Identity is who the person is now, by the claims of a new ID token with the configuration applied. It is nil when the
	// provider answered without an ID token, which it is allowed to: the session is still its own, and nothing is known of
	// what changed.
	Identity *Identity
	// RefreshToken is the token to use next time. It is the one asked with when the provider did not rotate it.
	RefreshToken string
}

// Refresh asks the provider again, with the refresh token it gave at sign-in, whether the person is still its own and what
// it now says of them. expectedKey is who the session is for: an answer about anybody else is refused. A refusal by the
// provider is ErrRefreshRevoked; a configuration that no longer lets the person in is a *Denied; anything else is a failure to
// ask, which says nothing about the person.
func (p *Provider) Refresh(ctx context.Context, refreshToken, expectedKey string) (*Refreshed, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, ErrRefreshRevoked
	}
	ctx = oidc.ClientContext(ctx, p.client)
	token, err := p.oauth("").TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
	if err != nil {
		var refused *oauth2.RetrieveError
		if errors.As(err, &refused) {
			switch refused.ErrorCode {
			case "invalid_grant", "invalid_token", "unauthorized_client":
				return nil, ErrRefreshRevoked
			}
		}
		return nil, fmt.Errorf("could not ask the provider again: %w", err)
	}
	result := &Refreshed{RefreshToken: refreshToken}
	if token.RefreshToken != "" {
		result.RefreshToken = token.RefreshToken
	}
	raw, _ := token.Extra("id_token").(string)
	if raw == "" {
		return result, nil
	}
	idToken, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("could not verify the ID token: %w", err)
	}
	if Key(idToken.Issuer, idToken.Subject) != expectedKey {
		return nil, ErrRefreshRevoked
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("could not read the ID token: %w", err)
	}
	identity, err := p.config.Authorize(claims)
	if err != nil {
		return nil, err
	}
	identity.Issuer, identity.Subject, identity.Key = idToken.Issuer, idToken.Subject, expectedKey
	result.Identity = identity
	return result, nil
}

// OfflineAccessScope is the scope that asks a provider for a refresh token.
const OfflineAccessScope = "offline_access"

// Authorize applies the configuration to the claims of a verified token: who it is, whether the provider says the
// address is theirs, whether it is a person this installation lets in, and the role. It returns a *Denied when it
// is not.
func (c Config) Authorize(claims map[string]any) (*Identity, error) {
	email := strings.ToLower(strings.TrimSpace(stringClaim(claims, "email")))
	if email == "" {
		return nil, &Denied{DeniedNoEmail, "the ID token has no email claim; ask the provider to include the email scope"}
	}
	// A provider that does not say leaves it at that; one that says anything but yes is believed, whatever type it
	// writes it in.
	if value, said := claims["email_verified"]; said && value != nil && !affirmative(value) {
		return nil, &Denied{DeniedEmailUnverified, "the provider says the address " + email + " is not verified"}
	}
	// A required value is compared whole: an organization called "x, Acme" is not "Acme".
	for claim, want := range c.RequireClaim {
		if !slices.Contains(claimValues(claims, claim, false), strings.TrimSpace(want)) {
			return nil, &Denied{DeniedRequiredClaim, fmt.Sprintf("the claim %q does not have the required value %q", claim, want)}
		}
	}
	if c.Tenancy.On() {
		memberships, platform := c.Tenancy.memberships(claims, c.RoleMap), c.Tenancy.platform(claims)
		if len(memberships) == 0 && !platform {
			return nil, &Denied{DeniedNoOrg, fmt.Sprintf("the claim %q puts the person in no organization with a role that maps to admin or viewer", c.Tenancy.OrgsClaim)}
		}
		role := RoleViewer
		if platform {
			role = RoleAdmin
		}
		for _, member := range memberships {
			if member.Role == RoleAdmin {
				role = RoleAdmin
			}
		}
		return &Identity{Email: email, Role: role, Memberships: memberships, Platform: platform, EmailVerified: emailVerified(claims)}, nil
	}
	roles := claimValues(claims, c.RoleClaim, true)
	role := c.roleOf(roles)
	if role == "" {
		return nil, &Denied{DeniedNoRole, fmt.Sprintf("none of the values of the claim %q maps to a role (%s)", c.RoleClaim, describeValues(roles))}
	}
	return &Identity{Email: email, Role: role, EmailVerified: emailVerified(claims)}, nil
}

// emailVerified is whether the claims say the address is verified.
func emailVerified(claims map[string]any) bool {
	value, said := claims["email_verified"]
	return said && value != nil && affirmative(value)
}

// roleOf is the highest role any of the values maps to, or "" when none does.
func (c Config) roleOf(values []string) string {
	role := ""
	for _, value := range values {
		switch c.RoleMap[value] {
		case RoleAdmin:
			return RoleAdmin
		case RoleViewer:
			role = RoleViewer
		}
	}
	return role
}

func describeValues(values []string) string {
	if len(values) == 0 {
		return "the claim is missing or empty"
	}
	return "it has " + strings.Join(values, ", ")
}

// affirmative is whether a provider said yes: true, or the text "true" that some providers write instead.
func affirmative(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	}
	return false
}

func stringClaim(claims map[string]any, name string) string {
	value, _ := claims[name].(string)
	return value
}

// claimValues reads a claim, by name or by dotted path, as a list of strings: a string is one value, or several when it
// has commas and split is true (which is how a provider writes a person's several roles); a list is its string
// members. Only roles are split: a claim that names where someone belongs is not, because its text is not the
// provider's to be cut up, and a person who can name their own organization "x, Acme" must not become a member of one
// called "Acme".
func claimValues(claims map[string]any, path string, split bool) []string {
	path = strings.TrimSpace(path)
	value, ok := claims[path]
	if !ok {
		var current any = claims
		for part := range strings.SplitSeq(path, ".") {
			object, isObject := current.(map[string]any)
			if !isObject {
				return nil
			}
			if current, ok = object[part]; !ok {
				return nil
			}
		}
		value = current
	}
	var values []string
	add := func(text string) {
		if !split {
			if text != "" {
				values = append(values, text)
			}
			return
		}
		for part := range strings.SplitSeq(text, ",") {
			if part = strings.TrimSpace(part); part != "" {
				values = append(values, part)
			}
		}
	}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if text, isText := claimText(item); isText {
				add(text)
			}
		}
	default:
		if text, isText := claimText(typed); isText {
			add(text)
		}
	}
	return values
}

// claimText is a scalar claim value as the text an admin would write it as: a string as it is, a number without an
// exponent (a provider that writes an organization id as 4821 means "4821"), and true or false.
func claimText(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(typed), true
	}
	return "", false
}

// Logout is what a back-channel logout token says: the provider ended this person's sessions.
type Logout struct {
	Key string
	// ID is the token's unique identifier, so that a replayed token can be told from a new one.
	ID string
}

// VerifyLogoutToken checks a back-channel logout token as OpenID Connect Back-Channel Logout 1.0 asks: signed by the
// provider, issued for this client, not expired, carrying the logout event, an identifier and a subject, and no nonce
// (which would make it an ID token, not a logout token). It must also be recent. A token that names only a session,
// and no subject, cannot be matched to a person and is refused.
func (p *Provider) VerifyLogoutToken(ctx context.Context, raw string, now time.Time) (*Logout, error) {
	ctx = oidc.ClientContext(ctx, p.client)
	token, err := p.verifier.VerifyLogout(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("could not verify the logout token: %w", err)
	}
	if strings.TrimSpace(token.Subject) == "" {
		return nil, errors.New("the logout token names no subject")
	}
	if token.IssuedAt.IsZero() || now.Sub(token.IssuedAt) > logoutTokenMaxAge || token.IssuedAt.Sub(now) > logoutTokenMaxAge {
		return nil, errors.New("the logout token is not recent")
	}
	return &Logout{Key: Key(token.Issuer, token.Subject), ID: token.TokenID}, nil
}
