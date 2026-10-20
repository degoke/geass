package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/oidcauth"
	"github.com/degoke/geass/pkg/platform"
)

func inOrgs(id, role string, workspaces ...string) oidcauth.Membership {
	member := oidcauth.Membership{Org: id, Role: role}
	for _, w := range workspaces {
		member.Workspaces = append(member.Workspaces, oidcauth.WorkspaceMembership{ID: w})
	}
	return member
}

func identityForDigest(memberships ...oidcauth.Membership) *oidcauth.Identity {
	return &oidcauth.Identity{Role: "viewer", Memberships: memberships}
}

// refreshFixture is the provider fixture with refreshing turned on and a person signed in with the ID token that says they
// are an owner.
type refreshFixture struct {
	ssoFixture
}

func newRefreshFixture(t *testing.T) refreshFixture {
	t.Helper()
	f := refreshFixture{newSSOFixture(t, platform.AuthModeExternal)}
	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) {
		c.Spec.Auth.OIDC.RefreshMinutes = 10
		c.Spec.Auth.OIDC.StaleGraceMinutes = 60
		c.Spec.Auth.OIDC.Scopes = nil
	})
	return f
}

func (f refreshFixture) entries(t *testing.T) map[string]refreshRecord {
	t.Helper()
	var secret corev1.Secret
	if err := f.srv.Client.Get(asSystem(context.Background()), client.ObjectKey{Name: ssoRefreshSecretName, Namespace: platform.SystemNamespace}, &secret); err != nil {
		return nil
	}
	out := map[string]refreshRecord{}
	for name, raw := range secret.Data {
		var record refreshRecord
		require.NoError(t, json.Unmarshal(raw, &record))
		out[name] = record
	}
	return out
}

// pass is one look at who is due, made when the last check of everybody is older than the interval.
func (f refreshFixture) pass(t *testing.T, since time.Duration) {
	t.Helper()
	require.NoError(t, f.srv.mutateRefreshSecret(context.Background(), func(entries map[string][]byte) bool {
		for name, raw := range entries {
			var record refreshRecord
			require.NoError(t, json.Unmarshal(raw, &record))
			record.Checked = time.Now().Add(-since).Unix()
			entries[name], _ = json.Marshal(record)
		}
		return true
	}))
	f.srv.refreshSessionsOnce(context.Background())
}

func TestASessionOfAProviderThatRefreshesIsKeptAndItsTokenIsSealed(t *testing.T) {
	f := newRefreshFixture(t)
	cookie := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))

	entries := f.entries(t)
	require.Len(t, entries, 1)
	for _, record := range entries {
		require.NotContains(t, record.Token, "rt-u-alice", "the refresh token is sealed")
		require.Equal(t, "sso:", record.Identity[:4])
		require.Greater(t, record.Expires, time.Now().Unix())
	}
	raw, _ := json.Marshal(entries)
	require.False(t, strings.Contains(string(raw), "rt-u-alice"))

	// Nothing has changed, so a pass after the interval asks, keeps the session, and keeps the next token.
	before := f.idp.refreshCalls.Load()
	f.pass(t, 11*time.Minute)
	require.Equal(t, before+1, f.idp.refreshCalls.Load(), "the provider was asked")
	require.True(t, f.authenticated(t, cookie))
	require.Len(t, f.entries(t), 1)
	f.pass(t, 11*time.Minute)
	require.True(t, f.authenticated(t, cookie), "the rotated token is the one that is used next")

	// A pass before the interval asks nothing.
	before = f.idp.refreshCalls.Load()
	f.srv.refreshSessionsOnce(context.Background())
	require.Equal(t, before, f.idp.refreshCalls.Load(), "it is not yet time")
}

func TestARoleThatChangedAtTheProviderEndsTheSessionsOfThePerson(t *testing.T) {
	f := newRefreshFixture(t)
	cookie := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	other := f.signedInAs(t, testPerson("u-bob", "bob@example.com", "member"))
	require.True(t, f.authenticated(t, cookie) && f.authenticated(t, other))

	f.idp.mu.Lock()
	f.idp.refreshed["u-alice"] = testPerson("u-alice", "alice@example.com", "member")
	f.idp.mu.Unlock()
	f.pass(t, 11*time.Minute)

	require.False(t, f.authenticated(t, cookie), "their role is not what the session was made with")
	require.True(t, f.authenticated(t, other), "and nobody else's sessions are touched")
	for _, record := range f.entries(t) {
		require.NotEqual(t, "u-alice", record.Identity, "what was kept for them is gone")
	}

	// They sign in again and are given what is true now.
	again := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "member"))
	require.Equal(t, "viewer", f.session(t, again)["role"])
}

func TestAPersonTheProviderNoLongerHonoursIsSignedOut(t *testing.T) {
	f := newRefreshFixture(t)
	cookie := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	f.idp.mu.Lock()
	for token := range f.idp.refresh {
		f.idp.revoked[token] = true
	}
	f.idp.mu.Unlock()

	f.pass(t, 11*time.Minute)

	require.False(t, f.authenticated(t, cookie), "removed at the provider, so removed here within minutes")
	require.Empty(t, f.entries(t))
}

func TestAPersonWhoseTokenNoLongerLetsThemInIsSignedOut(t *testing.T) {
	f := newRefreshFixture(t)
	cookie := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	person := testPerson("u-alice", "alice@example.com", "stranger")
	f.idp.mu.Lock()
	f.idp.refreshed["u-alice"] = person
	f.idp.mu.Unlock()

	f.pass(t, 11*time.Minute)
	require.False(t, f.authenticated(t, cookie), "a role that maps to nothing is no sign-in")
}

func TestAProviderThatCannotBeReachedKeepsTheSessionUntilTheGraceIsOver(t *testing.T) {
	f := newRefreshFixture(t)
	cookie := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	f.idp.refreshDown.Store(true)

	f.pass(t, 11*time.Minute)
	require.True(t, f.authenticated(t, cookie), "a check that could not be made says nothing of the person")
	require.Len(t, f.entries(t), 1)

	f.pass(t, 2*time.Hour)
	require.False(t, f.authenticated(t, cookie), "but one that cannot be made for the whole grace cannot be vouched for")
}

func TestAProviderThatAnswersWithoutAnIDTokenKeepsTheSession(t *testing.T) {
	f := newRefreshFixture(t)
	cookie := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	f.idp.noIDTokenOnRefresh.Store(true)
	f.idp.mu.Lock()
	f.idp.refreshed["u-alice"] = testPerson("u-alice", "alice@example.com", "member")
	f.idp.mu.Unlock()

	f.pass(t, 11*time.Minute)

	require.True(t, f.authenticated(t, cookie), "there is nothing to compare, and the provider still honours the session")
	f.pass(t, 11*time.Minute)
	require.True(t, f.authenticated(t, cookie), "and it goes on honouring the rotated token")
}

func TestAnAnswerAboutSomebodyElseEndsTheSession(t *testing.T) {
	f := newRefreshFixture(t)
	cookie := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	f.idp.mu.Lock()
	f.idp.refreshed["u-alice"] = testPerson("u-mallory", "alice@example.com", "owner")
	f.idp.mu.Unlock()

	f.pass(t, 11*time.Minute)
	require.False(t, f.authenticated(t, cookie), "a token that answers for another person is not this person's")
}

func TestNothingIsKeptWhenRefreshingIsOffOrTheProviderGivesNoRefreshToken(t *testing.T) {
	f := newSSOFixture(t, platform.AuthModeExternal)
	rf := refreshFixture{f}
	// A provider the administrator asked for refresh tokens by hand, while refreshing is off here, still gives one.
	rf.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) {
		c.Spec.Auth.OIDC.Scopes = []string{"openid", "email", "offline_access"}
	})
	rf.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	require.NotEmpty(t, rf.idp.refresh, "the provider gave a refresh token")
	require.Empty(t, rf.entries(t), "refreshing is off, so none is kept")
	require.Equal(t, int32(0), f.idp.refreshCalls.Load())
	rf.pass2(t)

	// What was kept while it was on is not used once it is turned off.
	again := newRefreshFixture(t)
	again.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	require.Len(t, again.entries(t), 1)
	again.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) { c.Spec.Auth.OIDC.RefreshMinutes = 0 })
	again.pass(t, 11*time.Minute)
	require.Equal(t, int32(0), again.idp.refreshCalls.Load(), "the provider is not asked")

	on := newRefreshFixture(t)
	on.idp.noRefreshTokens.Store(true)
	on.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	require.Empty(t, on.entries(t), "a provider that gives none is not an error")
}

func (f refreshFixture) pass2(t *testing.T) {
	t.Helper()
	f.srv.refreshSessionsOnce(context.Background())
}

func TestTheRefreshTokenIsAskedForWithOfflineAccessAndTheScopesAreKept(t *testing.T) {
	f := newRefreshFixture(t)
	settings, err := f.srv.ssoSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"openid", "email", "profile", "offline_access"}, settings.Config.Scopes, "the defaults are kept")

	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) {
		c.Spec.Auth.OIDC.Scopes = []string{"openid", "email", "groups"}
	})
	settings, err = f.srv.ssoSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"openid", "email", "groups", "offline_access"}, settings.Config.Scopes)

	f.updateConfig(t, func(c *geassv1alpha1.GeassPlatformConfig) { c.Spec.Auth.OIDC.RefreshMinutes = 0 })
	settings, err = f.srv.ssoSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"openid", "email", "groups"}, settings.Config.Scopes, "and off, it asks for nothing more")
}

func TestASessionThatHasEndedIsNotKeptForRefreshing(t *testing.T) {
	f := newRefreshFixture(t)
	f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	require.NoError(t, f.srv.mutateRefreshSecret(context.Background(), func(entries map[string][]byte) bool {
		for name, raw := range entries {
			var record refreshRecord
			require.NoError(t, json.Unmarshal(raw, &record))
			record.Expires = time.Now().Add(-time.Minute).Unix()
			entries[name], _ = json.Marshal(record)
		}
		return true
	}))
	f.srv.refreshSessionsOnce(context.Background())
	require.Empty(t, f.entries(t))
	require.Equal(t, int32(0), f.idp.refreshCalls.Load(), "and the provider is not asked about it")
}

func TestTheSealedTokenOpensOnlyWithTheSessionKeyItWasSealedWith(t *testing.T) {
	sealed, err := sealToken([]byte("key-one"), "a-refresh-token")
	require.NoError(t, err)
	opened, err := openToken([]byte("key-one"), sealed)
	require.NoError(t, err)
	require.Equal(t, "a-refresh-token", opened)
	_, err = openToken([]byte("key-two"), sealed)
	require.Error(t, err)
	again, _ := sealToken([]byte("key-one"), "a-refresh-token")
	require.NotEqual(t, sealed, again, "each is sealed with a nonce of its own")
}

func TestWhatTheProviderSaysOfAPersonIsTheSameWhateverTheOrderOfItsLists(t *testing.T) {
	a := identityForDigest(inOrgs("org_a", "admin", "w1", "w2"), inOrgs("org_b", "member"))
	b := identityForDigest(inOrgs("org_b", "member"), inOrgs("org_a", "admin", "w2", "w1"))
	require.Equal(t, identityDigest(a), identityDigest(b))
	c := identityForDigest(inOrgs("org_a", "member", "w1", "w2"), inOrgs("org_b", "member"))
	require.NotEqual(t, identityDigest(a), identityDigest(c), "a role is part of it")
	d := identityForDigest(inOrgs("org_a", "admin", "w1"), inOrgs("org_b", "member"))
	require.NotEqual(t, identityDigest(a), identityDigest(d), "and so is a workspace")
}
