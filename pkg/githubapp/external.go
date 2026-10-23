package githubapp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// An external GitHub App is one that a provider outside the cluster runs (rungeass runs one for every cluster it
// manages). The provider alone holds the App's private key: it mints each installation's token and gives it to Geass,
// which keeps it in a Secret of the installation's own and uses it where it would otherwise have minted one. Nothing
// here asks GitHub for a token.

const (
	// LabelGitHubConnection and LabelGitHubInstallation are on the Secret that holds an installation's token, so the
	// connection is reconciled when the provider gives a new one.
	LabelGitHubConnection   = "geass.dev/github-connection"
	LabelGitHubInstallation = "geass.dev/github-installation"

	// The keys of an installation's token Secret. expires_at and updated_at are RFC 3339.
	externalTokenKey     = "token"
	externalExpiresAtKey = "expires_at"
	externalUpdatedAtKey = "updated_at"

	// connectionExternalKey, in a project's token Secret, names the external connection whose installation the project
	// uses. A project connected that way never falls back to minting a token, or to a token it was given long ago.
	connectionExternalKey = "external_connection"
	// connectionTokenExpiresAtKey, in a project's token Secret, is when the token copied into it expires.
	connectionTokenExpiresAtKey = "token_expires_at"

	// TokenRenewalWindow is how long before it expires a token is said to be due for renewal. GitHub's last an hour.
	TokenRenewalWindow = 10 * time.Minute
	// TokenUsableMargin is the least a token must have left to be handed out: one about to lapse would fail a clone
	// half way through, which says less than waiting for the next one.
	TokenUsableMargin = time.Minute

	// ConditionTokensReady is the condition of an external connection that says whether Geass holds a token for each of
	// its installations that is good for a while yet.
	ConditionTokensReady = "TokensReady"
)

// The state of the token Geass holds for an installation, as status.installations[].tokenState says it.
const (
	TokenValid        = "Valid"
	TokenExpiringSoon = "ExpiringSoon"
	TokenExpired      = "Expired"
	TokenMissing      = "Missing"
)

// ErrTokenUnavailable is what using an installation of an external App fails with while Geass holds no token for it
// that is still good: the provider has not given one yet, or has not renewed the last. It passes when the provider gives
// one, so what waits on it waits and says why instead of failing.
var ErrTokenUnavailable = errors.New("no usable GitHub installation token")

type tokenUnavailableError struct{ message string }

func (e tokenUnavailableError) Error() string        { return e.message }
func (e tokenUnavailableError) Is(target error) bool { return target == ErrTokenUnavailable }

// ExternalWebhookSecretName is the Secret that holds the secret an external connection's provider signs the events it
// forwards with, under SecretKeyWebhookSecret.
func ExternalWebhookSecretName(connection string) string {
	return "github-app-" + connection + "-webhook"
}

// ExternalTokenSecretName is the Secret that holds the token of one installation of an external connection.
func ExternalTokenSecretName(connection string, installationID int64) string {
	return "github-app-" + connection + "-installation-" + strconv.FormatInt(installationID, 10)
}

// InstallationToken is the token a provider gave for an installation, and when it expires.
type InstallationToken struct {
	Token     string
	ExpiresAt time.Time
	UpdatedAt time.Time
}

// TokenFromExternalSecret reads an installation's token Secret. ok is false when it holds no token, or no expiry that
// can be read: a token whose end is not known is not used.
func TokenFromExternalSecret(secret *corev1.Secret) (InstallationToken, bool) {
	if secret == nil || len(secret.Data[externalTokenKey]) == 0 {
		return InstallationToken{}, false
	}
	expires, err := time.Parse(time.RFC3339, strings.TrimSpace(string(secret.Data[externalExpiresAtKey])))
	if err != nil {
		return InstallationToken{}, false
	}
	updated, _ := time.Parse(time.RFC3339, strings.TrimSpace(string(secret.Data[externalUpdatedAtKey])))
	return InstallationToken{Token: string(secret.Data[externalTokenKey]), ExpiresAt: expires, UpdatedAt: updated}, true
}

// SetExternalToken writes a token and its expiry into an installation's token Secret.
func SetExternalToken(secret *corev1.Secret, token string, expiresAt, now time.Time) {
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[externalTokenKey] = []byte(token)
	secret.Data[externalExpiresAtKey] = []byte(expiresAt.UTC().Format(time.RFC3339))
	secret.Data[externalUpdatedAtKey] = []byte(now.UTC().Format(time.RFC3339))
}

// TokenState is the state of a token at a moment: nil is a token Geass does not have.
func TokenState(token *InstallationToken, now time.Time) string {
	switch {
	case token == nil:
		return TokenMissing
	case !now.Before(token.ExpiresAt.Add(-TokenUsableMargin)):
		return TokenExpired
	case !now.Before(token.ExpiresAt.Add(-TokenRenewalWindow)):
		return TokenExpiringSoon
	default:
		return TokenValid
	}
}

// tokenPattern is the characters a token may have (token68, RFC 7235): it is sent in an Authorization header, and
// put in one by a clone's shell, so nothing that could end a header or a word is allowed.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]+$`)

// maxTokenLength bounds a token. GitHub's installation tokens are 40 characters today.
const maxTokenLength = 1024

// ValidateToken says what is wrong with a token a provider sent, without repeating it.
func ValidateToken(token string) error {
	if token == "" {
		return errors.New("token is required")
	}
	if len(token) > maxTokenLength {
		return fmt.Errorf("token is longer than %d characters", maxTokenLength)
	}
	if !tokenPattern.MatchString(token) {
		return errors.New("token has characters a GitHub token does not have")
	}
	return nil
}

// FindInstallation is the installation an external connection lists with that id.
func FindInstallation(connection *geassv1alpha1.GeassGitHubConnection, installationID int64) (geassv1alpha1.GeassGitHubInstallation, bool) {
	if connection == nil || connection.Spec.External == nil {
		return geassv1alpha1.GeassGitHubInstallation{}, false
	}
	index := slices.IndexFunc(connection.Spec.External.Installations, func(i geassv1alpha1.GeassGitHubInstallation) bool { return i.ID == installationID })
	if index < 0 {
		return geassv1alpha1.GeassGitHubInstallation{}, false
	}
	return connection.Spec.External.Installations[index], true
}

// ExternalConnections lists the connections of external Apps.
func ExternalConnections(ctx context.Context, c client.Client, namespace string) ([]geassv1alpha1.GeassGitHubConnection, error) {
	var list geassv1alpha1.GeassGitHubConnectionList
	if err := c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	var out []geassv1alpha1.GeassGitHubConnection
	for _, connection := range list.Items {
		if connection.Spec.External != nil {
			out = append(out, connection)
		}
	}
	slices.SortFunc(out, func(a, b geassv1alpha1.GeassGitHubConnection) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// ExternalInstallation is the external connection that lists an installation, and the installation. Both are nil when
// none does, which is an installation of the cluster's own App, or of none.
func ExternalInstallation(ctx context.Context, c client.Client, namespace string, installationID int64) (*geassv1alpha1.GeassGitHubConnection, *geassv1alpha1.GeassGitHubInstallation, error) {
	if installationID <= 0 {
		return nil, nil, nil
	}
	connections, err := ExternalConnections(ctx, c, namespace)
	if err != nil {
		return nil, nil, err
	}
	for i := range connections {
		if installation, ok := FindInstallation(&connections[i], installationID); ok {
			return &connections[i], &installation, nil
		}
	}
	return nil, nil, nil
}

// ExternalConnectionOf is the external connection a project's token Secret says it uses, or "".
func ExternalConnectionOf(secret *corev1.Secret) string {
	if secret == nil {
		return ""
	}
	return strings.TrimSpace(string(secret.Data[connectionExternalKey]))
}

// SetExternalConnection records in a project's token Secret the external connection and installation it uses.
func SetExternalConnection(secret *corev1.Secret, connection string, installation geassv1alpha1.GeassGitHubInstallation) {
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[connectionInstallationKey] = []byte(strconv.FormatInt(installation.ID, 10))
	secret.Data[connectionExternalKey] = []byte(connection)
	secret.Data["account"] = []byte(installation.Account)
	secret.Data["account_type"] = []byte(installation.AccountType)
	// A token from an App this project used before is not this installation's.
	delete(secret.Data, connectionTokenKey)
	delete(secret.Data, connectionTokenAtKey)
	delete(secret.Data, connectionTokenExpiresAtKey)
}

// externalToken is the token to use for an installation of an external App. handled is false when no external
// connection is concerned with it: the project's Secret names none, and none lists the installation.
func (r *TokenResolver) externalToken(ctx context.Context, connectionSecret *corev1.Secret, installationID int64) (token string, handled bool, err error) {
	named := ExternalConnectionOf(connectionSecret)
	var connection *geassv1alpha1.GeassGitHubConnection
	var installation *geassv1alpha1.GeassGitHubInstallation
	if named != "" {
		found := &geassv1alpha1.GeassGitHubConnection{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: named, Namespace: r.namespace()}, found); err != nil && !apierrors.IsNotFound(err) {
			return "", true, err
		} else if err == nil {
			if listed, ok := FindInstallation(found, installationID); ok {
				connection, installation = found, &listed
			}
		}
		if connection == nil {
			return "", true, fmt.Errorf("GitHub installation %d is no longer connected to this cluster through %s: connect the project to GitHub again", installationID, named)
		}
	} else {
		connection, installation, err = ExternalInstallation(ctx, r.Client, r.namespace(), installationID)
		if err != nil {
			return "", true, err
		}
		if connection == nil {
			return "", false, nil
		}
	}

	provider := connection.Spec.External.Provider
	who := fmt.Sprintf("GitHub installation %d (%s)", installation.ID, installation.Account)
	secret := &corev1.Secret{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: ExternalTokenSecretName(connection.Name, installationID), Namespace: r.namespace()}, secret); err != nil {
		if !apierrors.IsNotFound(err) {
			return "", true, err
		}
		secret = nil
	}
	held, ok := TokenFromExternalSecret(secret)
	now := time.Now()
	switch {
	case !ok:
		return "", true, tokenUnavailableError{fmt.Sprintf("Geass has no token for %s yet: %s, which runs the GitHub App, has not given one. What needs it waits until it does", who, provider)}
	case TokenState(&held, now) == TokenExpired:
		return "", true, tokenUnavailableError{fmt.Sprintf("the token for %s expired at %s and %s, which runs the GitHub App, has not renewed it. What needs it waits until it does", who, held.ExpiresAt.UTC().Format(time.RFC3339), provider)}
	}
	// The clone of a build reads the token from the project's own Secret, so the one in use is kept there too.
	if string(connectionSecret.Data[connectionTokenKey]) != held.Token {
		if err := r.persistExternalToken(ctx, connectionSecret, held); err != nil {
			return "", true, err
		}
	}
	return held.Token, true, nil
}

// ExternalAppSlug is the slug of the external App a project's token Secret uses an installation of, or "" when it uses
// none (or its connection names no slug).
func (r *TokenResolver) ExternalAppSlug(ctx context.Context, connectionSecret *corev1.Secret) (slug string, external bool) {
	installationID := InstallationIDFromSecret(connectionSecret)
	if installationID <= 0 {
		return "", false
	}
	if named := ExternalConnectionOf(connectionSecret); named != "" {
		found := &geassv1alpha1.GeassGitHubConnection{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: named, Namespace: r.namespace()}, found); err != nil || found.Spec.External == nil {
			return "", true
		}
		return found.Spec.External.AppSlug, true
	}
	connection, _, err := ExternalInstallation(ctx, r.Client, r.namespace(), installationID)
	if err != nil || connection == nil {
		return "", false
	}
	return connection.Spec.External.AppSlug, true
}

func (r *TokenResolver) persistExternalToken(ctx context.Context, secret *corev1.Secret, held InstallationToken) error {
	latest := &corev1.Secret{}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(secret), latest); err != nil {
		return err
	}
	if latest.Data == nil {
		latest.Data = map[string][]byte{}
	}
	latest.Data[connectionTokenKey] = []byte(held.Token)
	latest.Data[connectionTokenExpiresAtKey] = []byte(held.ExpiresAt.UTC().Format(time.RFC3339))
	delete(latest.Data, connectionTokenAtKey)
	return r.Client.Update(ctx, latest)
}
