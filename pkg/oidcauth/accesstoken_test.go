package oidcauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const testAudience = "https://cluster-a.example.com/api"

// signTyped signs claims with a header of the given type, which is what tells an access token from an ID token.
func signTyped(t *testing.T, key signingKey, typ string, claims map[string]any) string {
	t.Helper()
	options := &jose.SignerOptions{}
	if typ != "" {
		options = options.WithType(jose.ContentType(typ))
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: key.alg, Key: jose.JSONWebKey{Key: key.private, KeyID: key.kid}}, options)
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// accessClaims are those of a token Better Auth issues for a resource: no nonce, the resource as the audience.
func accessClaims(p *fakeProvider, now time.Time, change map[string]any) map[string]any {
	claims := map[string]any{
		"iss": p.server.URL, "aud": testAudience, "sub": "user-1", "client_id": testClientID, "azp": testClientID, "scope": "openid email",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "jti": "j1",
		"email": "Alice@Example.com", "email_verified": true, "org_role": "owner", "organization_id": "org_acme",
	}
	for name, value := range change {
		if value == nil {
			delete(claims, name)
		} else {
			claims[name] = value
		}
	}
	return claims
}

func TestVerifyAccessTokenAcceptsAJWTAccessToken(t *testing.T) {
	for _, alg := range []jose.SignatureAlgorithm{jose.EdDSA, jose.ES256, jose.RS256} {
		t.Run(string(alg), func(t *testing.T) {
			key := newKey(t, "k1", alg)
			p := newFakeProvider(t, key)
			provider := p.discover(t, p.config())
			now := time.Now()
			token, err := provider.VerifyAccessToken(context.Background(), signTyped(t, key, "at+jwt", accessClaims(p, now, nil)), testAudience, now)
			if err != nil {
				t.Fatalf("a good access token: %v", err)
			}
			if token.Subject != "user-1" || token.Key != Key(p.server.URL, "user-1") || token.Issuer != p.server.URL {
				t.Errorf("token = %+v", token)
			}
			if !token.Expires.Equal(time.Unix(now.Add(time.Hour).Unix(), 0)) || token.Claims["org_role"] != "owner" {
				t.Errorf("token = %+v", token)
			}
		})
	}
}

func TestVerifyAccessTokenReadsTheTypeAndTheAudienceInEveryFormTheyHave(t *testing.T) {
	key := newKey(t, "k1", jose.EdDSA)
	p := newFakeProvider(t, key)
	provider := p.discover(t, p.config())
	now := time.Now()
	for name, tc := range map[string]struct {
		typ    string
		claims map[string]any
	}{
		"the type with its media type prefix": {"application/at+jwt", nil},
		"the type in capitals":                {"AT+JWT", nil},
		// Better Auth adds the address of its userinfo endpoint when the openid scope was granted.
		"an audience that is a list": {"at+jwt", map[string]any{"aud": []string{testAudience, "https://auth.example.com/api/auth/oauth2/userinfo"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := provider.VerifyAccessToken(context.Background(), signTyped(t, key, tc.typ, accessClaims(p, now, tc.claims)), testAudience, now); err != nil {
				t.Errorf("refused: %v", err)
			}
		})
	}
}

func TestVerifyAccessTokenRefusesWhatIsNotOne(t *testing.T) {
	key := newKey(t, "k1", jose.EdDSA)
	p := newFakeProvider(t, key)
	provider := p.discover(t, p.config())
	now := time.Now()
	good := func(change map[string]any) string { return signTyped(t, key, "at+jwt", accessClaims(p, now, change)) }
	idToken := func(change map[string]any) string {
		claims := accessClaims(p, now, map[string]any{"aud": testClientID, "nonce": "n-1", "at_hash": "abc"})
		maps.Copy(claims, change)
		return signTyped(t, key, "JWT", claims)
	}

	for name, raw := range map[string]string{
		"another issuer":                         good(map[string]any{"iss": "https://evil.example.com"}),
		"another audience":                       good(map[string]any{"aud": "https://cluster-b.example.com/api"}),
		"an audience list without it":            good(map[string]any{"aud": []string{"https://cluster-b.example.com/api", "https://auth.example.com/api/auth/oauth2/userinfo"}}),
		"no audience":                            good(map[string]any{"aud": nil}),
		"expired":                                good(map[string]any{"exp": now.Add(-time.Minute).Unix(), "iat": now.Add(-time.Hour).Unix()}),
		"not valid yet":                          good(map[string]any{"nbf": now.Add(time.Hour).Unix()}),
		"issued in the future":                   good(map[string]any{"iat": now.Add(time.Hour).Unix()}),
		"no expiry":                              good(map[string]any{"exp": nil}),
		"no issue time":                          good(map[string]any{"iat": nil}),
		"no subject":                             good(map[string]any{"sub": nil}),
		"an empty subject":                       good(map[string]any{"sub": " "}),
		"a string for the expiry":                good(map[string]any{"exp": "tomorrow"}),
		"signed by another key with the same id": signTyped(t, newKey(t, "k1", jose.EdDSA), "at+jwt", accessClaims(p, now, nil)),
		"a type that is not at+jwt":              signTyped(t, key, "JWT", accessClaims(p, now, nil)),
		"no type":                                signTyped(t, key, "", accessClaims(p, now, nil)),
		// An ID token is signed by the same keys and addressed to the same client, so what says it is not an access token is its
		// type, and the claims only an ID token has.
		"an ID token": idToken(nil),
		"an ID token whose audience is the cluster":  idToken(map[string]any{"aud": testAudience}),
		"an access token's type on an ID token":      signTyped(t, key, "at+jwt", accessClaims(p, now, map[string]any{"nonce": "n-1"})),
		"an at_hash under an access token's type":    signTyped(t, key, "at+jwt", accessClaims(p, now, map[string]any{"at_hash": "abc"})),
		"a token_use that is id":                     good(map[string]any{"token_use": "id"}),
		"not a token":                                "hello",
		"a token with a missing part":                "e30.e30",
		"an empty credential":                        "",
		"an access token that is nothing but dashes": "---",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := provider.VerifyAccessToken(context.Background(), raw, testAudience, now)
			if !errors.Is(err, ErrNotAccessToken) {
				t.Errorf("err = %v, want ErrNotAccessToken", err)
			}
		})
	}

	t.Run("no audience to check against", func(t *testing.T) {
		if _, err := provider.VerifyAccessToken(context.Background(), good(nil), " ", now); !errors.Is(err, ErrNotAccessToken) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a token for the same audience after it", func(t *testing.T) {
		if _, err := provider.VerifyAccessToken(context.Background(), good(nil), testAudience, now); err != nil {
			t.Errorf("a good one: %v", err)
		}
	})
}

func TestVerifyAccessTokenAllowsAFewSecondsOfClockDifference(t *testing.T) {
	key := newKey(t, "k1", jose.EdDSA)
	p := newFakeProvider(t, key)
	provider := p.discover(t, p.config())
	now := time.Now()
	for name, change := range map[string]map[string]any{
		"just expired":    {"exp": now.Add(-10 * time.Second).Unix(), "iat": now.Add(-time.Hour).Unix()},
		"just issued":     {"iat": now.Add(10 * time.Second).Unix()},
		"just not before": {"nbf": now.Add(10 * time.Second).Unix()},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := provider.VerifyAccessToken(context.Background(), signTyped(t, key, "at+jwt", accessClaims(p, now, change)), testAudience, now); err != nil {
				t.Errorf("refused: %v", err)
			}
		})
	}
	expired := signTyped(t, key, "at+jwt", accessClaims(p, now, map[string]any{"exp": now.Add(-time.Minute).Unix(), "iat": now.Add(-time.Hour).Unix()}))
	if _, err := provider.VerifyAccessToken(context.Background(), expired, testAudience, now); err == nil {
		t.Error("a token that expired a minute ago was accepted")
	}
}

func TestVerifyAccessTokenRefusesAlgorithmsThatProveNothing(t *testing.T) {
	key := newKey(t, "k1", jose.EdDSA)
	p := newFakeProvider(t, key)
	provider := p.discover(t, p.config())
	now := time.Now()
	encode := func(value any) string {
		raw, _ := json.Marshal(value)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	claims := accessClaims(p, now, nil)

	unsigned := encode(map[string]any{"alg": "none", "typ": "at+jwt", "kid": "k1"}) + "." + encode(claims) + "."
	if _, err := provider.VerifyAccessToken(context.Background(), unsigned, testAudience, now); !errors.Is(err, ErrNotAccessToken) {
		t.Errorf("alg none: err = %v", err)
	}
	// With a signature part that is not empty, in case a verifier only looks at whether there is one.
	forged := encode(map[string]any{"alg": "none", "typ": "at+jwt", "kid": "k1"}) + "." + encode(claims) + "." + encode("signature")
	if _, err := provider.VerifyAccessToken(context.Background(), forged, testAudience, now); !errors.Is(err, ErrNotAccessToken) {
		t.Errorf("alg none with a signature: err = %v", err)
	}

	// An HMAC token made with the provider's public key as the secret, the classic confusion of key types.
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte("0123456789abcdef0123456789abcdef")}, (&jose.SignerOptions{}).WithType("at+jwt"))
	if err != nil {
		t.Fatal(err)
	}
	hmacToken, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.VerifyAccessToken(context.Background(), hmacToken, testAudience, now); !errors.Is(err, ErrNotAccessToken) {
		t.Errorf("HS256: err = %v", err)
	}
}

func TestVerifyAccessTokenFollowsAKeyRotation(t *testing.T) {
	first := newKey(t, "k1", jose.EdDSA)
	p := newFakeProvider(t, first)
	provider := p.discover(t, p.config())
	now := time.Now()
	if _, err := provider.VerifyAccessToken(context.Background(), signTyped(t, first, "at+jwt", accessClaims(p, now, nil)), testAudience, now); err != nil {
		t.Fatal(err)
	}
	second := newKey(t, "k2", jose.EdDSA)
	p.mu.Lock()
	p.published = []signingKey{first, second}
	p.mu.Unlock()
	override(t, &keyRefetchCooldown, time.Duration(0))
	if _, err := provider.VerifyAccessToken(context.Background(), signTyped(t, second, "at+jwt", accessClaims(p, now, nil)), testAudience, now); err != nil {
		t.Errorf("a token under a new key: %v", err)
	}
}

func TestAuthorizeAccessTokenMapsClaimsAsASignInDoes(t *testing.T) {
	p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
	config := p.config()

	claims := func(change map[string]any) map[string]any { return accessClaims(p, time.Now(), change) }
	identity, err := config.AuthorizeAccessToken(claims(nil))
	if err != nil || identity.Role != RoleAdmin || identity.Email != "alice@example.com" {
		t.Fatalf("identity = %+v, err = %v", identity, err)
	}
	if identity, err = config.AuthorizeAccessToken(claims(map[string]any{"org_role": "member"})); err != nil || identity.Role != RoleViewer {
		t.Errorf("a member: identity = %+v, err = %v", identity, err)
	}

	// The same people are refused as when they sign in.
	for name, change := range map[string]map[string]any{
		"a role that maps to nothing": {"org_role": "guest"},
		"no role":                     {"org_role": nil},
		"another organization":        {"organization_id": "org_other"},
		"no organization":             {"organization_id": nil},
		"an unverified address":       {"email_verified": false},
	} {
		t.Run(name, func(t *testing.T) {
			var denied *Denied
			if _, err := config.AuthorizeAccessToken(claims(change)); !errors.As(err, &denied) {
				t.Errorf("err = %v, want a denial", err)
			}
		})
	}

	// What an ID token must have and an access token need not: the address. The person is then named by the subject.
	identity, err = config.AuthorizeAccessToken(claims(map[string]any{"email": nil, "email_verified": nil}))
	if err != nil || identity.Email != "" || identity.Role != RoleAdmin {
		t.Errorf("without an address: identity = %+v, err = %v", identity, err)
	}
	if _, err = config.Authorize(claims(map[string]any{"email": nil, "email_verified": nil})); err == nil {
		t.Error("an ID token without an address was accepted")
	}
}

func TestAuthorizeAccessTokenReadsOrganizations(t *testing.T) {
	config := Config{
		Issuer: "https://auth.example.com", ClientID: testClientID, RoleMap: map[string]string{"owner": RoleAdmin, "member": RoleViewer},
		Tenancy: Tenancy{OrgsClaim: "orgs", PlatformClaim: "geass_platform", PlatformValues: []string{"true"}},
	}
	base := map[string]any{"sub": "u1", "orgs": []any{
		map[string]any{"id": "org_a", "role": "member", "workspaces": []any{"w1"}},
		map[string]any{"id": "org_b", "role": "owner"},
	}}
	identity, err := config.AuthorizeAccessToken(base)
	if err != nil || identity.Role != RoleAdmin || len(identity.Memberships) != 2 || identity.Platform {
		t.Fatalf("identity = %+v, err = %v", identity, err)
	}
	if identity.Memberships[0].Org != "org_a" || identity.Memberships[0].Role != RoleViewer || len(identity.Memberships[0].Workspaces) != 1 {
		t.Errorf("memberships = %+v", identity.Memberships)
	}

	withPlatform := map[string]any{"sub": "u2", "geass_platform": "true"}
	if identity, err = config.AuthorizeAccessToken(withPlatform); err != nil || !identity.Platform || identity.Role != RoleAdmin {
		t.Errorf("a platform administrator: identity = %+v, err = %v", identity, err)
	}

	// A person in no organization with a role that maps is refused, and so is one the claim does not name at all.
	for name, claims := range map[string]map[string]any{
		"an organization with an unmapped role": {"sub": "u3", "orgs": []any{map[string]any{"id": "org_a", "role": "guest"}}},
		"no organizations claim":                {"sub": "u4"},
		"another claim's platform value":        {"sub": "u5", "geass_platform": "false"},
	} {
		t.Run(name, func(t *testing.T) {
			var denied *Denied
			if _, err := config.AuthorizeAccessToken(claims); !errors.As(err, &denied) || denied.Reason != DeniedNoOrg {
				t.Errorf("err = %v, want %s", err, DeniedNoOrg)
			}
		})
	}
}
