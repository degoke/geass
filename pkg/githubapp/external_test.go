package githubapp

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/stretchr/testify/require"
)

// noGitHub fails the test on any request: an installation of an external App is never minted a token by Geass.
type noGitHub struct{ t *testing.T }

func (n noGitHub) RoundTrip(req *http.Request) (*http.Response, error) {
	n.t.Errorf("Geass asked GitHub for %s %s", req.Method, req.URL)
	return nil, errors.New("no requests to GitHub")
}

func externalConnection(installations ...geassv1alpha1.GeassGitHubInstallation) *geassv1alpha1.GeassGitHubConnection {
	return &geassv1alpha1.GeassGitHubConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "rungeass", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassGitHubConnectionSpec{
			SecretRef: corev1.LocalObjectReference{Name: ExternalWebhookSecretName("rungeass")},
			External:  &geassv1alpha1.GeassGitHubExternalApp{Provider: "rungeass", AppSlug: "rungeass-app", Installations: installations},
		},
	}
}

func installationToken(id int64, token string, expires time.Time) *corev1.Secret {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: ExternalTokenSecretName("rungeass", id), Namespace: platform.SystemNamespace}}
	SetExternalToken(secret, token, expires, time.Now())
	return secret
}

// externalFixture is a project whose token Secret uses installation 42, and whatever else is given. The project's Secret
// still holds a token from long ago, which must never be used for an external installation.
func externalFixture(t *testing.T, projectData map[string][]byte, objects ...client.Object) (*TokenResolver, *corev1.Secret) {
	t.Helper()
	data := map[string][]byte{"installation_id": []byte("42"), "token": []byte("left-from-long-ago")}
	for key, value := range projectData {
		data[key] = value
	}
	project := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "payments-github-token", Namespace: platform.SystemNamespace}, Data: data}
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(append(objects, project)...).Build()
	return &TokenResolver{Client: c, HTTP: &http.Client{Transport: noGitHub{t}}}, project
}

func acme() geassv1alpha1.GeassGitHubInstallation {
	return geassv1alpha1.GeassGitHubInstallation{ID: 42, Account: "acme", AccountType: "Organization"}
}

func TestAnExternalInstallationUsesTheTokenTheProviderGaveAndKeepsItWhereTheCloneReadsIt(t *testing.T) {
	expires := time.Now().Add(50 * time.Minute).UTC().Truncate(time.Second)
	resolver, project := externalFixture(t, map[string][]byte{connectionExternalKey: []byte("rungeass")},
		externalConnection(acme()), installationToken(42, "ghs_fromtheprovider", expires))

	token, err := resolver.ConnectionToken(context.Background(), project)

	require.NoError(t, err)
	require.Equal(t, "ghs_fromtheprovider", token)
	stored := &corev1.Secret{}
	require.NoError(t, resolver.Client.Get(context.Background(), client.ObjectKeyFromObject(project), stored))
	require.Equal(t, "ghs_fromtheprovider", string(stored.Data["token"]), "the build's clone reads the project's own Secret")
	require.Equal(t, expires.Format(time.RFC3339), string(stored.Data[connectionTokenExpiresAtKey]))
}

func TestAnInstallationAnExternalConnectionListsIsItsEvenWhenTheProjectDoesNotSaySo(t *testing.T) {
	resolver, project := externalFixture(t, nil, externalConnection(acme()), installationToken(42, "ghs_listed", time.Now().Add(time.Hour)))

	token, err := resolver.ConnectionToken(context.Background(), project)

	require.NoError(t, err)
	require.Equal(t, "ghs_listed", token)
}

func TestATokenDueForRenewalIsStillUsed(t *testing.T) {
	resolver, project := externalFixture(t, nil, externalConnection(acme()), installationToken(42, "ghs_soon", time.Now().Add(5*time.Minute)))

	token, err := resolver.ConnectionToken(context.Background(), project)

	require.NoError(t, err, "a renewal that is a few minutes late costs nothing while the token lasts")
	require.Equal(t, "ghs_soon", token)
}

func TestAnExternalInstallationWithoutAUsableTokenWaitsAndSaysWhy(t *testing.T) {
	for name, tc := range map[string]struct {
		token *corev1.Secret
		want  []string
	}{
		"no token yet":                 {nil, []string{"no token for GitHub installation 42 (acme) yet", "rungeass"}},
		"an expired token":             {installationToken(42, "ghs_expired", time.Now().Add(-time.Minute)), []string{"expired at", "has not renewed it"}},
		"one with seconds left":        {installationToken(42, "ghs_nearly", time.Now().Add(30*time.Second)), []string{"expired at"}},
		"a Secret with no expiry":      {&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: ExternalTokenSecretName("rungeass", 42), Namespace: platform.SystemNamespace}, Data: map[string][]byte{"token": []byte("ghs_noexpiry")}}, []string{"no token"}},
		"a Secret with an empty token": {&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: ExternalTokenSecretName("rungeass", 42), Namespace: platform.SystemNamespace}, Data: map[string][]byte{"expires_at": []byte(time.Now().Add(time.Hour).UTC().Format(time.RFC3339))}}, []string{"no token"}},
	} {
		t.Run(name, func(t *testing.T) {
			objects := []client.Object{externalConnection(acme())}
			if tc.token != nil {
				objects = append(objects, tc.token)
			}
			resolver, project := externalFixture(t, map[string][]byte{connectionExternalKey: []byte("rungeass")}, objects...)

			token, err := resolver.ConnectionToken(context.Background(), project)

			require.ErrorIs(t, err, ErrTokenUnavailable)
			require.Empty(t, token, "the token left in the project's Secret is not used")
			for _, want := range tc.want {
				require.Contains(t, err.Error(), want)
			}
			for _, secret := range []string{"ghs_expired", "ghs_nearly", "ghs_noexpiry", "left-from-long-ago"} {
				require.NotContains(t, err.Error(), secret, "an error never carries a token")
			}
		})
	}
}

func TestAProjectWhoseExternalInstallationWasTakenAwayIsToldToConnectAgain(t *testing.T) {
	for name, objects := range map[string][]client.Object{
		"the installation is no longer listed": {externalConnection(geassv1alpha1.GeassGitHubInstallation{ID: 7, Account: "other"}), installationToken(42, "ghs_stale", time.Now().Add(time.Hour))},
		"the connection is gone":               {installationToken(42, "ghs_stale", time.Now().Add(time.Hour))},
	} {
		t.Run(name, func(t *testing.T) {
			resolver, project := externalFixture(t, map[string][]byte{connectionExternalKey: []byte("rungeass")}, objects...)

			_, err := resolver.ConnectionToken(context.Background(), project)

			require.Error(t, err)
			require.NotErrorIs(t, err, ErrTokenUnavailable, "waiting does not bring it back")
			require.Contains(t, err.Error(), "connect the project to GitHub again")
		})
	}
}

// A cluster that has its own App and also an external one: an installation the external one does not list is the
// cluster's own, and its token is minted exactly as before.
func TestAnInstallationOfTheClustersOwnAppIsStillMintedBesideAnExternalApp(t *testing.T) {
	resolver, connection, mints := tokenFixture(t, "", true)
	require.NoError(t, resolver.Client.Create(context.Background(), externalConnection(geassv1alpha1.GeassGitHubInstallation{ID: 7, Account: "someone-else"})))

	token, err := resolver.ConnectionToken(context.Background(), connection)

	require.NoError(t, err)
	require.Equal(t, "new-token", token)
	require.Equal(t, 1, *mints)
}

func TestTokenStates(t *testing.T) {
	now := time.Now()
	at := func(d time.Duration) *InstallationToken { return &InstallationToken{Token: "t", ExpiresAt: now.Add(d)} }
	require.Equal(t, TokenMissing, TokenState(nil, now))
	require.Equal(t, TokenValid, TokenState(at(time.Hour), now))
	require.Equal(t, TokenValid, TokenState(at(TokenRenewalWindow+time.Second), now))
	require.Equal(t, TokenExpiringSoon, TokenState(at(TokenRenewalWindow), now))
	require.Equal(t, TokenExpiringSoon, TokenState(at(TokenUsableMargin+time.Second), now))
	require.Equal(t, TokenExpired, TokenState(at(TokenUsableMargin), now))
	require.Equal(t, TokenExpired, TokenState(at(-time.Hour), now))
}

func TestATokenIsOnlyTakenInTheCharactersATokenHas(t *testing.T) {
	require.NoError(t, ValidateToken("ghs_16C7e42F292c6912E7710c838347Ae178B4a"))
	require.NoError(t, ValidateToken("a.b-c_d~e+f/g="))
	for name, token := range map[string]string{
		"empty":          "",
		"a space":        "ghs_abc def",
		"a newline":      "ghs_abc\nX-Injected: yes",
		"a quote":        `ghs_abc"`,
		"a shell dollar": "ghs_$(id)",
		"too long":       string(make([]byte, maxTokenLength+1)),
	} {
		err := ValidateToken(token)
		require.Error(t, err, name)
		if token != "" {
			require.NotContains(t, err.Error(), token, "the error does not repeat the token")
		}
	}
}

func TestAReportWithAnExternalInstallationIsMadeAsTheProvidersApp(t *testing.T) {
	f := newReportFixture(t, fixtureOptions{})
	ctx := context.Background()
	require.NoError(t, f.client.Create(ctx, externalConnection(acme())))
	require.NoError(t, f.client.Create(ctx, installationToken(42, "ghs_report", time.Now().Add(time.Hour))))
	secret := &corev1.Secret{}
	require.NoError(t, f.client.Get(ctx, client.ObjectKey{Name: "shop-github-token", Namespace: platform.SystemNamespace}, secret))
	SetExternalConnection(secret, "rungeass", acme())
	require.NoError(t, f.client.Update(ctx, secret))

	target, err := f.resolver.Resolve(ctx, "shop", "", "acme/web")

	require.NoError(t, err)
	require.Equal(t, "ghs_report", target.Token)
	require.Equal(t, "rungeass-app", target.AppSlug)
	require.Equal(t, "rungeass-app[bot]", target.BotLogin, "the comments Geass looks for again are the provider's App's")
}
