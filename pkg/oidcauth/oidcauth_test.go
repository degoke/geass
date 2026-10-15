package oidcauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	testClientID = "geass"
	testSecret   = "s3cret"
	testRedirect = "https://geass.example.com/auth/oidc/callback"
)

type signingKey struct {
	kid     string
	alg     jose.SignatureAlgorithm
	private any
	public  any
}

func newKey(t *testing.T, kid string, alg jose.SignatureAlgorithm) signingKey {
	t.Helper()
	switch alg {
	case jose.EdDSA:
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return signingKey{kid, alg, private, public}
	case jose.ES256:
		private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return signingKey{kid, alg, private, &private.PublicKey}
	case jose.RS256:
		private, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return signingKey{kid, alg, private, &private.PublicKey}
	}
	t.Fatalf("no key for %s", alg)
	return signingKey{}
}

// fakeProvider is an identity provider that answers discovery, publishes keys and trades codes for ID tokens,
// checking what a real one checks: the client's secret and that the PKCE verifier matches the challenge.
type fakeProvider struct {
	t      *testing.T
	server *httptest.Server

	mu        sync.Mutex
	published []signingKey
	signer    signingKey
	codes     map[string]issuedCode
	jwksCalls int
	// mutate changes the claims of the next ID token before it is signed.
	mutate func(claims map[string]any)
	// withoutIDToken makes the token endpoint answer without an ID token.
	withoutIDToken bool
	// discovery changes the discovery document before it is sent.
	discovery func(document map[string]any)
	// jwksFail makes the key endpoint answer 503, jwksDelay makes it slow, and jwksPad pads its answer with that many bytes.
	jwksFail  bool
	jwksDelay time.Duration
	jwksPad   int
}

type issuedCode struct{ challenge, nonce, redirect string }

func newFakeProvider(t *testing.T, key signingKey) *fakeProvider {
	t.Helper()
	t.Setenv("GEASS_ALLOW_LOOPBACK", "true")
	p := &fakeProvider{t: t, published: []signingKey{key}, signer: key, codes: map[string]issuedCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		document := map[string]any{
			"issuer": p.server.URL, "authorization_endpoint": p.server.URL + "/authorize",
			"token_endpoint": p.server.URL + "/token", "jwks_uri": p.server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"EdDSA"},
		}
		if p.discovery != nil {
			p.discovery(document)
		}
		_ = json.NewEncoder(w).Encode(document)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		p.mu.Lock()
		p.jwksCalls++
		fail, delay, pad := p.jwksFail, p.jwksDelay, p.jwksPad
		set := jose.JSONWebKeySet{}
		for _, k := range p.published {
			set.Keys = append(set.Keys, jose.JSONWebKey{Key: k.public, KeyID: k.kid, Algorithm: string(k.alg), Use: "sig"})
		}
		p.mu.Unlock()
		time.Sleep(delay)
		if fail {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		encoded, _ := json.Marshal(set)
		if pad > 0 {
			encoded = append(encoded[:len(encoded)-1], []byte(`,"padding":"`+strings.Repeat("x", pad)+`"}`)...)
		}
		_, _ = w.Write(encoded)
	})
	mux.HandleFunc("/token", p.token)
	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func (p *fakeProvider) token(w http.ResponseWriter, r *http.Request) {
	// Without this, the body is sniffed as text, and the oauth2 package reads text as a form.
	w.Header().Set("Content-Type", "application/json")
	fail := func(status int, message string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": message})
	}
	_ = r.ParseForm()
	user, password, basic := r.BasicAuth()
	if !basic {
		user, password = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if user != testClientID || password != testSecret {
		fail(http.StatusUnauthorized, "bad client credentials")
		return
	}
	p.mu.Lock()
	issued, ok := p.codes[r.PostForm.Get("code")]
	p.mu.Unlock()
	if !ok {
		fail(http.StatusBadRequest, "unknown code")
		return
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != issued.challenge {
		fail(http.StatusBadRequest, "PKCE verifier does not match")
		return
	}
	if r.PostForm.Get("redirect_uri") != issued.redirect {
		fail(http.StatusBadRequest, "redirect_uri mismatch")
		return
	}
	p.mu.Lock()
	delete(p.codes, r.PostForm.Get("code")) // a code is good once, and is used up only by an exchange that went through
	p.mu.Unlock()
	response := map[string]any{"access_token": "at", "token_type": "Bearer"}
	if !p.withoutIDToken {
		response["id_token"] = p.sign(p.claims(issued.nonce))
	}
	_ = json.NewEncoder(w).Encode(response)
}

func (p *fakeProvider) claims(nonce string) map[string]any {
	claims := map[string]any{
		"iss": p.server.URL, "aud": testClientID, "sub": "user-1", "nonce": nonce,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		"email": "Alice@Example.com", "email_verified": true, "org_role": "owner", "organization_id": "org_acme",
	}
	if p.mutate != nil {
		p.mutate(claims)
	}
	return claims
}

func (p *fakeProvider) sign(claims map[string]any) string {
	p.mu.Lock()
	signer := p.signer
	p.mu.Unlock()
	return signWith(p.t, signer, claims)
}

func signWith(t *testing.T, key signingKey, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: key.alg, Key: jose.JSONWebKey{Key: key.private, KeyID: key.kid}}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (p *fakeProvider) config() Config {
	return Config{
		Issuer: p.server.URL, ClientID: testClientID, ClientSecret: testSecret,
		RoleClaim: "org_role", RoleMap: map[string]string{"owner": RoleAdmin, "admin": RoleAdmin, "member": RoleViewer},
		RequireClaim: map[string]string{"organization_id": "org_acme"},
	}
}

func (p *fakeProvider) discover(t *testing.T, config Config) *Provider {
	t.Helper()
	provider, err := Discover(context.Background(), config)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return provider
}

// signIn does what a browser and the provider do between AuthCodeURL and Exchange, and returns what Exchange does.
func (p *fakeProvider) signIn(t *testing.T, provider *Provider, verifierOverride string) (*Identity, error) {
	t.Helper()
	verifier := NewVerifier()
	nonce := "nonce-" + verifier[:8]
	redirect := provider.AuthCodeURL(testRedirect, "state-1", nonce, verifier)
	query := mustParse(t, redirect).Query()
	code := "code-" + verifier[:8]
	p.mu.Lock()
	p.codes[code] = issuedCode{challenge: query.Get("code_challenge"), nonce: query.Get("nonce"), redirect: query.Get("redirect_uri")}
	p.mu.Unlock()
	if verifierOverride != "" {
		verifier = verifierOverride
	}
	return provider.Exchange(context.Background(), testRedirect, code, verifier, nonce)
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestAuthCodeURLCarriesPKCEStateNonceAndScopes(t *testing.T) {
	p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
	provider := p.discover(t, p.config())
	verifier := NewVerifier()
	target := mustParse(t, provider.AuthCodeURL(testRedirect, "state-1", "nonce-1", verifier))

	if got := target.Scheme + "://" + target.Host + target.Path; got != p.server.URL+"/authorize" {
		t.Errorf("authorization endpoint = %s", got)
	}
	query := target.Query()
	sum := sha256.Sum256([]byte(verifier))
	for name, want := range map[string]string{
		"response_type": "code", "client_id": testClientID, "redirect_uri": testRedirect, "state": "state-1", "nonce": "nonce-1",
		"code_challenge_method": "S256", "code_challenge": base64.RawURLEncoding.EncodeToString(sum[:]), "scope": "openid email profile",
	} {
		if got := query.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if strings.Contains(target.String(), verifier) {
		t.Error("the PKCE verifier was put in the URL; only its challenge may be")
	}
}

func TestExchangeVerifiesTokensOfEveryAsymmetricAlgorithm(t *testing.T) {
	for _, alg := range []jose.SignatureAlgorithm{jose.EdDSA, jose.ES256, jose.RS256} {
		t.Run(string(alg), func(t *testing.T) {
			p := newFakeProvider(t, newKey(t, "k1", alg))
			identity, err := p.signIn(t, p.discover(t, p.config()), "")
			if err != nil {
				t.Fatalf("sign in: %v", err)
			}
			if identity.Email != "alice@example.com" || identity.Role != RoleAdmin || identity.Subject != "user-1" || identity.Issuer != p.server.URL {
				t.Errorf("identity = %+v", identity)
			}
			if identity.Key != Key(p.server.URL, "user-1") {
				t.Errorf("key = %s", identity.Key)
			}
		})
	}
}

func TestExchangeRejectsATokenThatIsNotRight(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(claims map[string]any)
		want   string
	}{
		"another audience": {func(c map[string]any) { c["aud"] = "someone-else" }, "could not verify the ID token"},
		"another issuer":   {func(c map[string]any) { c["iss"] = "https://evil.example.com" }, "could not verify the ID token"},
		"expired":          {func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }, "could not verify the ID token"},
		"another nonce":    {func(c map[string]any) { c["nonce"] = "not-this-one" }, "does not belong to this sign-in"},
		"no nonce":         {func(c map[string]any) { delete(c, "nonce") }, "does not belong to this sign-in"},
	} {
		t.Run(name, func(t *testing.T) {
			p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
			p.mutate = tc.mutate
			_, err := p.signIn(t, p.discover(t, p.config()), "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestExchangeRejectsForgedSignatures(t *testing.T) {
	t.Run("signed by a key the provider does not publish", func(t *testing.T) {
		p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
		p.signer = newKey(t, "k1", jose.EdDSA) // same kid, other key
		if _, err := p.signIn(t, p.discover(t, p.config()), ""); err == nil {
			t.Fatal("a token signed by an unknown key was accepted")
		}
	})
	t.Run("HS256 with the public key as the secret", func(t *testing.T) {
		key := newKey(t, "k1", jose.RS256)
		p := newFakeProvider(t, key)
		public, _ := json.Marshal(jose.JSONWebKey{Key: key.public})
		p.signer = signingKey{kid: "k1", alg: jose.HS256, private: public}
		if _, err := p.signIn(t, p.discover(t, p.config()), ""); err == nil {
			t.Fatal("a symmetric signature made with the public key was accepted")
		}
	})
	t.Run("alg none", func(t *testing.T) {
		p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
		provider := p.discover(t, p.config())
		claims, _ := json.Marshal(p.claims("n"))
		unsigned := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + "."
		if _, err := provider.verifier.Verify(context.Background(), unsigned); err == nil {
			t.Fatal("an unsigned token was accepted")
		}
	})
}

func TestExchangeNeedsTheRightProofFromTheBrowser(t *testing.T) {
	p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
	provider := p.discover(t, p.config())
	if _, err := p.signIn(t, provider, "a-verifier-that-is-not-the-one-that-was-used-123456789"); err == nil {
		t.Fatal("the code was exchanged with a verifier that does not match its challenge")
	}
	if _, err := provider.Exchange(context.Background(), testRedirect, "no-such-code", NewVerifier(), "n"); err == nil {
		t.Fatal("an unknown code was exchanged")
	}
	// A code is good once.
	verifier := NewVerifier()
	query := mustParse(t, provider.AuthCodeURL(testRedirect, "s", "n1", verifier)).Query()
	p.codes["once"] = issuedCode{challenge: query.Get("code_challenge"), nonce: "n1", redirect: testRedirect}
	if _, err := provider.Exchange(context.Background(), testRedirect, "once", verifier, "n1"); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	if _, err := provider.Exchange(context.Background(), testRedirect, "once", verifier, "n1"); err == nil {
		t.Fatal("a code was exchanged twice")
	}
}

func TestExchangeNeedsAnIDToken(t *testing.T) {
	p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
	p.withoutIDToken = true
	_, err := p.signIn(t, p.discover(t, p.config()), "")
	if err == nil || !strings.Contains(err.Error(), "no ID token") {
		t.Fatalf("err = %v", err)
	}
}

func TestBadClientSecretIsRefusedByTheProvider(t *testing.T) {
	p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
	config := p.config()
	config.ClientSecret = "wrong"
	if _, err := p.signIn(t, p.discover(t, config), ""); err == nil {
		t.Fatal("signed in with the wrong client secret")
	}
}

func TestKeysAreFetchedAgainWhenTheProviderRotates(t *testing.T) {
	override(t, &keyRefetchCooldown, 0)
	old := newKey(t, "k1", jose.EdDSA)
	p := newFakeProvider(t, old)
	provider := p.discover(t, p.config())
	if _, err := p.signIn(t, provider, ""); err != nil {
		t.Fatalf("before rotation: %v", err)
	}
	callsBefore := p.jwksCalls

	fresh := newKey(t, "k2", jose.EdDSA)
	p.mu.Lock()
	p.published, p.signer = []signingKey{old, fresh}, fresh
	p.mu.Unlock()
	if _, err := p.signIn(t, provider, ""); err != nil {
		t.Fatalf("after rotation: %v", err)
	}
	if p.jwksCalls <= callsBefore {
		t.Error("the new key was not fetched")
	}
}

func TestDiscoverRefusesWhatCannotBeTrusted(t *testing.T) {
	p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))

	t.Run("an issuer that calls itself something else", func(t *testing.T) {
		config := p.config()
		config.Issuer = p.server.URL + "/other"
		if _, err := Discover(context.Background(), config); err == nil {
			t.Fatal("discovery succeeded for the wrong issuer")
		}
	})
	t.Run("a local address without the operator's opt-in", func(t *testing.T) {
		t.Setenv("GEASS_ALLOW_LOOPBACK", "false")
		_, err := Discover(context.Background(), p.config())
		if err == nil {
			t.Fatal("discovery connected to a loopback issuer")
		}
	})
	t.Run("plain http for a public name", func(t *testing.T) {
		config := p.config()
		config.Issuer = "http://auth.example.com"
		if _, err := Discover(context.Background(), config); err == nil || !strings.Contains(err.Error(), "https") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestAuthorize(t *testing.T) {
	config := Config{
		RoleClaim:    "org_role",
		RoleMap:      map[string]string{"owner": RoleAdmin, "admin": RoleAdmin, "member": RoleViewer},
		RequireClaim: map[string]string{"organization_id": "org_acme"},
	}
	base := func(extra map[string]any) map[string]any {
		claims := map[string]any{"email": "Bob@Example.com", "email_verified": true, "org_role": "member", "organization_id": "org_acme"}
		maps.Copy(claims, extra)
		return claims
	}
	for name, tc := range map[string]struct {
		config Config
		claims map[string]any
		role   string
		denied string
	}{
		"a member is a viewer":             {config, base(nil), RoleViewer, ""},
		"an owner is an admin":             {config, base(map[string]any{"org_role": "owner"}), RoleAdmin, ""},
		"the highest of several wins":      {config, base(map[string]any{"org_role": "member,owner"}), RoleAdmin, ""},
		"a list of roles":                  {config, base(map[string]any{"org_role": []any{"member", "admin"}}), RoleAdmin, ""},
		"spaces around comma-separated":    {config, base(map[string]any{"org_role": " member , nobody "}), RoleViewer, ""},
		"an unmapped role":                 {config, base(map[string]any{"org_role": "guest"}), "", DeniedNoRole},
		"no role claim":                    {config, base(map[string]any{"org_role": nil}), "", DeniedNoRole},
		"a role claim of the wrong type":   {config, base(map[string]any{"org_role": 7}), "", DeniedNoRole},
		"another organization":             {config, base(map[string]any{"organization_id": "org_other"}), "", DeniedRequiredClaim},
		"no organization":                  {config, base(map[string]any{"organization_id": nil}), "", DeniedRequiredClaim},
		"organization among several":       {config, base(map[string]any{"organization_id": []any{"org_x", "org_acme"}}), RoleViewer, ""},
		"no email":                         {config, base(map[string]any{"email": ""}), "", DeniedNoEmail},
		"an email the provider disowns":    {config, base(map[string]any{"email_verified": false}), "", DeniedEmailUnverified},
		"a provider that does not say":     {config, base(map[string]any{"email_verified": nil}), RoleViewer, ""},
		"a nested claim path":              {Config{RoleClaim: "org.role", RoleMap: map[string]string{"owner": RoleAdmin}}, map[string]any{"email": "a@b.c", "org": map[string]any{"role": "owner"}}, RoleAdmin, ""},
		"a claim whose name has a dot":     {Config{RoleClaim: "https://x.example/roles", RoleMap: map[string]string{"admin": RoleAdmin}}, map[string]any{"email": "a@b.c", "https://x.example/roles": []any{"admin"}}, RoleAdmin, ""},
		"a nested path that is not nested": {Config{RoleClaim: "org.role", RoleMap: map[string]string{"owner": RoleAdmin}}, map[string]any{"email": "a@b.c", "org": "owner"}, "", DeniedNoRole},
	} {
		t.Run(name, func(t *testing.T) {
			identity, err := tc.config.Authorize(tc.claims)
			if tc.denied != "" {
				var denied *Denied
				if !errors.As(err, &denied) || denied.Reason != tc.denied {
					t.Fatalf("err = %v, want denial %q", err, tc.denied)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if identity.Role != tc.role {
				t.Errorf("role = %q, want %q", identity.Role, tc.role)
			}
			if identity.Email != strings.ToLower(identity.Email) {
				t.Errorf("email %q is not lowercase", identity.Email)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	good := Config{Issuer: "https://auth.example.com", ClientID: "geass", RoleClaim: "role", RoleMap: map[string]string{"owner": RoleAdmin}}
	if err := good.Validate(); err != nil {
		t.Fatalf("a good configuration: %v", err)
	}
	for name, change := range map[string]func(*Config){
		"no issuer":               func(c *Config) { c.Issuer = "" },
		"an issuer with a query":  func(c *Config) { c.Issuer = "https://auth.example.com/?x=1" },
		"http":                    func(c *Config) { c.Issuer = "http://auth.example.com" },
		"localhost":               func(c *Config) { c.Issuer = "https://localhost" },
		"the metadata address":    func(c *Config) { c.Issuer = "https://169.254.169.254" },
		"no client":               func(c *Config) { c.ClientID = " " },
		"no role claim":           func(c *Config) { c.RoleClaim = "" },
		"no role map":             func(c *Config) { c.RoleMap = nil },
		"a role that is not one":  func(c *Config) { c.RoleMap = map[string]string{"owner": "superuser"} },
		"observer":                func(c *Config) { c.RoleMap = map[string]string{"owner": "observer"} },
		"an empty required claim": func(c *Config) { c.RequireClaim = map[string]string{"org": ""} },
	} {
		t.Run(name, func(t *testing.T) {
			config := good
			change(&config)
			if err := config.Validate(); err == nil {
				t.Error("an invalid configuration was accepted")
			}
		})
	}
}

func TestKeyIsStableAndSpecific(t *testing.T) {
	if Key("https://a.example", "u1") != Key("https://a.example/", "u1") {
		t.Error("a trailing slash on the issuer changes the key")
	}
	if Key("https://a.example", "u1") == Key("https://a.example", "u2") {
		t.Error("two subjects share a key")
	}
	if Key("https://a.example", "u1") == Key("https://b.example", "u1") {
		t.Error("two issuers share a key")
	}
	// The pair is hashed with a separator, so these do not collide.
	if Key("https://a.exampleu", "1") == Key("https://a.example", "u1") {
		t.Error("issuer and subject run together")
	}
	if !strings.HasPrefix(Key("i", "s"), "sso:") {
		t.Error("a key does not say it is not a local user")
	}
}

func TestFingerprintFollowsWhatMatters(t *testing.T) {
	base := Config{Issuer: "https://a.example", ClientID: "c", ClientSecret: "s", RoleClaim: "r", Scopes: []string{"a", "b"}, RoleMap: map[string]string{"x": RoleAdmin}}
	same := base
	same.Issuer = "https://a.example/"
	same.Scopes = []string{"b", "a"}
	if base.Fingerprint() != same.Fingerprint() {
		t.Error("a trailing slash or the order of scopes changed the fingerprint")
	}
	for name, change := range map[string]func(*Config){
		"secret":  func(c *Config) { c.ClientSecret = "other" },
		"client":  func(c *Config) { c.ClientID = "other" },
		"claim":   func(c *Config) { c.RoleClaim = "other" },
		"map":     func(c *Config) { c.RoleMap = map[string]string{"x": RoleViewer} },
		"require": func(c *Config) { c.RequireClaim = map[string]string{"a": "b"} },
	} {
		changed := base
		change(&changed)
		if changed.Fingerprint() == base.Fingerprint() {
			t.Errorf("changing the %s did not change the fingerprint", name)
		}
	}
}

func TestVerifyLogoutToken(t *testing.T) {
	p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
	provider := p.discover(t, p.config())
	now := time.Now()
	logoutClaims := func(extra map[string]any) map[string]any {
		claims := map[string]any{
			"iss": p.server.URL, "aud": testClientID, "sub": "user-1", "jti": "j1", "iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix(),
			"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}},
		}
		for k, v := range extra {
			if v == nil {
				delete(claims, k)
			} else {
				claims[k] = v
			}
		}
		return claims
	}

	logout, err := provider.VerifyLogoutToken(context.Background(), p.sign(logoutClaims(nil)), now)
	if err != nil {
		t.Fatalf("a good logout token: %v", err)
	}
	if logout.Key != Key(p.server.URL, "user-1") || logout.ID != "j1" {
		t.Errorf("logout = %+v", logout)
	}

	for name, extra := range map[string]map[string]any{
		"another audience": {"aud": "someone-else"},
		"another issuer":   {"iss": "https://evil.example.com"},
		"a nonce":          {"nonce": "n"},
		"no event":         {"events": nil},
		"the wrong event":  {"events": map[string]any{"http://example.com/other": map[string]any{}}},
		"no subject":       {"sub": nil},
		"no identifier":    {"jti": nil},
		"too old":          {"iat": now.Add(-time.Hour).Unix()},
		"from the future":  {"iat": now.Add(time.Hour).Unix()},
		"no issue time":    {"iat": nil},
		"no expiry":        {"exp": nil},
		"expired":          {"exp": now.Add(-time.Minute).Unix(), "iat": now.Add(-time.Hour).Unix()},
		"only a session":   {"sub": nil, "sid": "session-1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := provider.VerifyLogoutToken(context.Background(), p.sign(logoutClaims(extra)), now); err == nil {
				t.Error("a bad logout token was accepted")
			}
		})
	}

	forged := signWith(t, newKey(t, "k1", jose.EdDSA), logoutClaims(nil))
	if _, err := provider.VerifyLogoutToken(context.Background(), forged, now); err == nil {
		t.Error("a logout token signed by another key was accepted")
	}
}

func TestAnIDTokenWithoutASubjectIsRefused(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"empty":   func(c map[string]any) { c["sub"] = "" },
		"missing": func(c map[string]any) { delete(c, "sub") },
		"blank":   func(c map[string]any) { c["sub"] = "  " },
	} {
		t.Run(name, func(t *testing.T) {
			p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
			p.mutate = mutate
			_, err := p.signIn(t, p.discover(t, p.config()), "")
			if err == nil || !strings.Contains(err.Error(), "no subject") {
				t.Fatalf("err = %v, want it refused for having no subject", err)
			}
		})
	}
}

func TestDiscoveryThatSendsGeassToAnInsecureAddressIsRefused(t *testing.T) {
	t.Setenv("GEASS_ALLOW_LOOPBACK", "true")
	for name, tc := range map[string]struct {
		change func(map[string]any)
		want   string
	}{
		"a token endpoint over http":          {func(d map[string]any) { d["token_endpoint"] = "http://auth.example.com/token" }, "token endpoint"},
		"keys over http":                      {func(d map[string]any) { d["jwks_uri"] = "http://auth.example.com/jwks" }, "keys endpoint"},
		"an authorization endpoint over http": {func(d map[string]any) { d["authorization_endpoint"] = "http://auth.example.com/authorize" }, "authorization endpoint"},
		"no keys":                             {func(d map[string]any) { delete(d, "jwks_uri") }, "keys endpoint"},
		"a token endpoint that is a scheme":   {func(d map[string]any) { d["token_endpoint"] = "ftp://auth.example.com/token" }, "token endpoint"},
	} {
		t.Run(name, func(t *testing.T) {
			p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
			p.discovery = tc.change
			_, err := Discover(context.Background(), p.config())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one about the %s", err, tc.want)
			}
		})
	}
	t.Run("a provider on this machine, when the operator allows it, may use http", func(t *testing.T) {
		p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
		if _, err := Discover(context.Background(), p.config()); err != nil {
			t.Fatalf("Discover: %v", err)
		}
	})
	t.Run("but not without that", func(t *testing.T) {
		p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
		t.Setenv("GEASS_ALLOW_LOOPBACK", "false")
		if _, err := Discover(context.Background(), p.config()); err == nil {
			t.Fatal("a provider on this machine was used without the operator's opt-in")
		}
	})
}

func TestRequiredClaimsAreComparedWholeButRolesAreSplit(t *testing.T) {
	config := Config{
		RoleClaim:    "org_role",
		RoleMap:      map[string]string{"owner": RoleAdmin, "member": RoleViewer},
		RequireClaim: map[string]string{"organization_name": "Acme"},
	}
	person := func(organization any, role string) map[string]any {
		return map[string]any{"email": "x@example.com", "org_role": role, "organization_name": organization}
	}
	for name, tc := range map[string]struct {
		organization any
		allowed      bool
	}{
		"the organization":                      {"Acme", true},
		"a list that has it":                    {[]any{"Other", "Acme"}, true},
		"a name that contains it after a comma": {"Evil, Acme", false},
		"a name that contains it before one":    {"Acme, Evil", false},
		"a similar name":                        {"Acme Inc", false},
		"no organization":                       {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := config.Authorize(person(tc.organization, "owner"))
			if tc.allowed != (err == nil) {
				t.Fatalf("allowed = %v, err = %v", tc.allowed, err)
			}
		})
	}
	t.Run("a required value that has a comma in it can match", func(t *testing.T) {
		withComma := config
		withComma.RequireClaim = map[string]string{"organization_name": "Acme, Inc."}
		if _, err := withComma.Authorize(person("Acme, Inc.", "owner")); err != nil {
			t.Fatalf("err = %v", err)
		}
		if _, err := withComma.Authorize(person("Acme", "owner")); err == nil {
			t.Fatal("a part of the name was enough")
		}
	})
	t.Run("roles are still read from a comma-separated string", func(t *testing.T) {
		identity, err := config.Authorize(person("Acme", "member, owner"))
		if err != nil || identity.Role != RoleAdmin {
			t.Fatalf("identity = %+v, err = %v", identity, err)
		}
	})
}

func TestOnlyAYesIsAVerifiedEmail(t *testing.T) {
	config := Config{RoleClaim: "role", RoleMap: map[string]string{"owner": RoleAdmin}}
	for value, allowed := range map[string]bool{"true": true, "True": true, " true ": true, "false": false, "False": false, "no": false, "0": false, "1": false, "": false} {
		_, err := config.Authorize(map[string]any{"email": "x@example.com", "role": "owner", "email_verified": value})
		if allowed != (err == nil) {
			t.Errorf("email_verified %q: allowed = %v, err = %v", value, allowed, err)
		}
	}
	for name, value := range map[string]any{"the number zero": float64(0), "the number one": float64(1), "false": false, "a list": []any{true}, "an object": map[string]any{}} {
		if _, err := config.Authorize(map[string]any{"email": "x@example.com", "role": "owner", "email_verified": value}); err == nil {
			t.Errorf("email_verified as %s was believed", name)
		}
	}
	for name, value := range map[string]any{"true": true, "null": nil} {
		if _, err := config.Authorize(map[string]any{"email": "x@example.com", "role": "owner", "email_verified": value}); err != nil {
			t.Errorf("email_verified as %s: %v", name, err)
		}
	}
}

func TestForgedTokensDoNotMakeGeassFetchTheKeysOverAndOver(t *testing.T) {
	p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
	provider := p.discover(t, p.config())
	if _, err := p.signIn(t, provider, ""); err != nil {
		t.Fatalf("a first sign-in: %v", err)
	}
	fetchedBefore := p.jwksCalls

	// Tokens signed by a key nobody published, under a key id nobody has: each would be a reason to fetch again.
	for i := range 30 {
		attacker := newKey(t, fmt.Sprintf("unknown-%d", i), jose.EdDSA)
		forged := signWith(t, attacker, p.claims("n"))
		if _, err := provider.verifier.Verify(context.Background(), forged); err == nil {
			t.Fatal("a forged token was accepted")
		}
	}
	if extra := p.jwksCalls - fetchedBefore; extra > 1 {
		t.Errorf("30 forged tokens with unknown key ids caused %d fetches of the keys, want at most 1", extra)
	}

	// A forged token that names a key the provider does have costs no fetch at all.
	known := p.published[0]
	known.private = newKey(t, "k1", jose.EdDSA).private
	fetchedBefore = p.jwksCalls
	for range 30 {
		if _, err := provider.verifier.Verify(context.Background(), signWith(t, known, p.claims("n"))); err == nil {
			t.Fatal("a token signed with the wrong key under a known key id was accepted")
		}
	}
	if extra := p.jwksCalls - fetchedBefore; extra != 0 {
		t.Errorf("tokens naming a known key id caused %d fetches of the keys, want none", extra)
	}
}

func TestARealRotationIsNoticedAfterTheCooldown(t *testing.T) {
	override(t, &keyRefetchCooldown, 50*time.Millisecond)
	old := newKey(t, "k1", jose.EdDSA)
	p := newFakeProvider(t, old)
	provider := p.discover(t, p.config())
	if _, err := p.signIn(t, provider, ""); err != nil {
		t.Fatal(err)
	}
	fresh := newKey(t, "k2", jose.EdDSA)
	p.mu.Lock()
	p.published, p.signer = []signingKey{old, fresh}, fresh
	p.mu.Unlock()

	// Straight after the last fetch the new key is not looked for. Once the cooldown is over, it is.
	if _, err := p.signIn(t, provider, ""); err == nil {
		t.Log("the rotation was noticed at once (the cooldown had already passed)")
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := p.signIn(t, provider, ""); err != nil {
		t.Fatalf("after the cooldown: %v", err)
	}
}

func TestKeysThatAreOldAreFetchedAgain(t *testing.T) {
	override(t, &keyMaxAge, 20*time.Millisecond)
	override(t, &keyRefetchCooldown, 0)
	p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
	provider := p.discover(t, p.config())
	if _, err := p.signIn(t, provider, ""); err != nil {
		t.Fatal(err)
	}
	before := p.jwksCalls
	time.Sleep(40 * time.Millisecond)
	if _, err := p.signIn(t, provider, ""); err != nil {
		t.Fatal(err)
	}
	if p.jwksCalls <= before {
		t.Error("old keys were trusted without being fetched again")
	}
}

func TestNumbersAndBooleansInClaimsAreReadAsTheTextAnAdminWouldWrite(t *testing.T) {
	config := Config{RoleClaim: "role", RoleMap: map[string]string{"owner": RoleAdmin, "1": RoleViewer, "true": RoleAdmin}}
	person := func(extra map[string]any) map[string]any {
		claims := map[string]any{"email": "x@example.com", "role": "owner"}
		maps.Copy(claims, extra)
		return claims
	}
	for name, tc := range map[string]struct {
		require map[string]string
		claims  map[string]any
		allowed bool
	}{
		"an organization id that is a number":          {map[string]string{"org_id": "4821"}, person(map[string]any{"org_id": float64(4821)}), true},
		"a different number":                           {map[string]string{"org_id": "4821"}, person(map[string]any{"org_id": float64(4822)}), false},
		"a number among a list of numbers":             {map[string]string{"org_id": "7"}, person(map[string]any{"org_id": []any{float64(3), float64(7)}}), true},
		"a list of mixed numbers and text":             {map[string]string{"org_id": "acme"}, person(map[string]any{"org_id": []any{float64(3), "acme"}}), true},
		"a large id is not written with an exponent":   {map[string]string{"org_id": "100000000"}, person(map[string]any{"org_id": float64(100000000)}), true},
		"a fraction keeps its fraction":                {map[string]string{"level": "1.5"}, person(map[string]any{"level": 1.5}), true},
		"a boolean":                                    {map[string]string{"employee": "true"}, person(map[string]any{"employee": true}), true},
		"the other boolean":                            {map[string]string{"employee": "true"}, person(map[string]any{"employee": false}), false},
		"an object is not text":                        {map[string]string{"org": "x"}, person(map[string]any{"org": map[string]any{"id": "x"}}), false},
		"a null is not text":                           {map[string]string{"org": "x"}, person(map[string]any{"org": nil}), false},
		"the claim the required one must have is gone": {map[string]string{"org_id": "4821"}, person(nil), false},
	} {
		t.Run(name, func(t *testing.T) {
			c := config
			c.RequireClaim = tc.require
			_, err := c.Authorize(tc.claims)
			if tc.allowed != (err == nil) {
				t.Fatalf("allowed = %v, err = %v", tc.allowed, err)
			}
		})
	}
	t.Run("a role that is a number maps", func(t *testing.T) {
		identity, err := config.Authorize(map[string]any{"email": "x@example.com", "role": float64(1)})
		if err != nil || identity.Role != RoleViewer {
			t.Fatalf("identity = %+v, err = %v", identity, err)
		}
	})
	t.Run("a list of roles that has a boolean maps", func(t *testing.T) {
		identity, err := config.Authorize(map[string]any{"email": "x@example.com", "role": []any{false, true}})
		if err != nil || identity.Role != RoleAdmin {
			t.Fatalf("identity = %+v, err = %v", identity, err)
		}
	})
}

// override sets a package-level setting for one test and puts back what it was, whatever that was.
func override[T any](t *testing.T, target *T, value T) {
	t.Helper()
	old := *target
	*target = value
	t.Cleanup(func() { *target = old })
}

func forgedTokens(t *testing.T, p *fakeProvider, n int) []string {
	t.Helper()
	tokens := make([]string, n)
	for i := range tokens {
		tokens[i] = signWith(t, newKey(t, fmt.Sprintf("unknown-%d", i), jose.EdDSA), p.claims("n"))
	}
	return tokens
}

func TestAProviderWhoseKeysCannotBeFetchedIsNotAskedAgainAndAgain(t *testing.T) {
	p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
	p.jwksFail = true
	provider := p.discover(t, p.config())

	for _, token := range forgedTokens(t, p, 30) {
		if _, err := provider.verifier.Verify(context.Background(), token); err == nil {
			t.Fatal("a forged token was accepted")
		}
	}

	p.mu.Lock()
	calls := p.jwksCalls
	p.mu.Unlock()
	if calls > 1 {
		t.Errorf("30 forged tokens while the keys could not be fetched caused %d requests for them, want 1", calls)
	}

	// Once the provider is back and the cooldown is over, a real sign-in works.
	p.mu.Lock()
	p.jwksFail = false
	p.mu.Unlock()
	override(t, &keyRefetchCooldown, 0)
	if _, err := p.signIn(t, provider, ""); err != nil {
		t.Fatalf("after the provider came back: %v", err)
	}
}

func TestKeysThatAreOldAndCannotBeRefreshedAreNotAskedForAgainAtOnce(t *testing.T) {
	p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
	provider := p.discover(t, p.config())
	if _, err := p.signIn(t, provider, ""); err != nil {
		t.Fatal(err)
	}
	override(t, &keyMaxAge, 10*time.Millisecond)
	override(t, &keyRefetchCooldown, 0)
	time.Sleep(30 * time.Millisecond)
	p.mu.Lock()
	p.jwksFail = true
	before := p.jwksCalls
	p.mu.Unlock()
	verifyWithOldKeys := func() {
		t.Helper()
		// What the old keys signed still verifies while they cannot be refreshed.
		if _, err := provider.verifier.Verify(context.Background(), p.sign(p.claims("n"))); err != nil {
			t.Fatalf("a token the old keys signed was refused: %v", err)
		}
	}

	verifyWithOldKeys() // the refresh is tried, once, and fails
	keyRefetchCooldown = 15 * time.Second
	for range 30 {
		verifyWithOldKeys()
	}

	p.mu.Lock()
	extra := p.jwksCalls - before
	p.mu.Unlock()
	if extra != 1 {
		t.Errorf("31 verifications with old keys caused %d requests for new ones, want exactly 1 (the one that was due)", extra)
	}
}

func TestAVerificationIsNotHeldUpByAFetchThatIsUnderWay(t *testing.T) {
	override(t, &keyRefetchCooldown, 0)
	p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
	provider := p.discover(t, p.config())
	if _, err := p.signIn(t, provider, ""); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.jwksDelay = 600 * time.Millisecond
	p.mu.Unlock()

	// A token with a key id nobody has sends the key set to fetch, which now takes 600ms.
	slow := forgedTokens(t, p, 1)[0]
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = provider.verifier.Verify(context.Background(), slow)
	}()
	time.Sleep(100 * time.Millisecond)

	started := time.Now()
	if _, err := provider.verifier.Verify(context.Background(), p.sign(p.claims("n"))); err != nil {
		t.Fatalf("a token with a known key: %v", err)
	}
	if waited := time.Since(started); waited > 250*time.Millisecond {
		t.Errorf("a token whose key is known waited %v for another token's fetch", waited)
	}
	<-done
}

func TestAKeyDocumentLargerThanTheCapIsNotUsed(t *testing.T) {
	p := newFakeProvider(t, newKey(t, "k1", jose.EdDSA))
	p.jwksPad = 2 << 20
	provider := p.discover(t, p.config())

	if _, err := p.signIn(t, provider, ""); err == nil {
		t.Fatal("keys from a document larger than the cap were used")
	}

	p.mu.Lock()
	p.jwksPad = 0
	p.mu.Unlock()
	override(t, &keyRefetchCooldown, 0)
	if _, err := p.signIn(t, provider, ""); err != nil {
		t.Fatalf("a normal document after the oversized one: %v", err)
	}
}
