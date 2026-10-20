package dashboard

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/degoke/geass/pkg/oidcauth"
	"github.com/degoke/geass/pkg/platform"
)

// What a person's membership can change while their session is open. A session of the provider carries, in its cookie, the role
// and the organizations the person had when they signed in, and is honoured until it ends. With refresh on, Geass keeps the
// refresh token the provider gave and asks the provider again every few minutes: if the provider no longer honours it (they
// signed out there, or were removed), or what it says of them is not what the session was made with (a role, an organization,
// a platform claim), their sessions are ended and they sign in again, which gives them what is true now.

const (
	// ssoRefreshSecretName is the Secret that holds what refreshing needs, one entry for each person who signed in: the
	// refresh token, encrypted, and a digest of what the provider said of them.
	ssoRefreshSecretName = "geass-sso-refresh"
	// ssoRefreshCap is how many people it holds at once. The oldest entries go first, which only matters to an installation with
	// more people signed in than that, who then fall back to signing in again when their session ends.
	ssoRefreshCap = 2000
	// ssoRefreshStep is how often the dashboard looks for entries that are due.
	defaultSSORefreshStep = time.Minute
	defaultStaleGrace     = 2 * time.Hour
	// maxRefreshPerPass bounds the provider calls one pass makes, so that a pass never runs long.
	maxRefreshPerPass = 200
)

// ssoRefreshStep is a variable so that a test need not wait.
var ssoRefreshStep = defaultSSORefreshStep

// refreshRecord is what is kept for one person.
type refreshRecord struct {
	// Token is the refresh token, sealed with a key made from the session key, so that reading the Secret is not enough.
	Token string `json:"t"`
	// Digest is a hash of what the provider said of the person when it was last asked: role, platform claim, organizations.
	Digest string `json:"d"`
	// Expires is when the session this was made for ends, in Unix seconds: the entry is not needed after.
	Expires int64 `json:"e"`
	// Checked is when the provider was last asked and answered, and Started when the person signed in, in Unix seconds.
	Checked int64 `json:"c"`
	Started int64 `json:"s"`
	// Identity is who it is for, as sessions name them: what ending their sessions is done by. Key is who the provider knows
	// them as, which is what its answer has to be about. Org is the organization whose own provider it is, or empty.
	Identity string `json:"i"`
	Key      string `json:"k,omitempty"`
	Org      string `json:"o,omitempty"`
}

// identityDigest is a hash of what the provider says of a person that a session depends on. The organizations Geass knows do
// not take part, so that setting one up does not end anybody's session.
func identityDigest(identity *oidcauth.Identity) string {
	type workspace struct{ ID, Role string }
	type membership struct {
		Org, Role  string
		Workspaces []workspace
	}
	type view struct {
		Role        string
		Platform    bool
		Memberships []membership
	}
	out := view{Role: identity.Role, Platform: identity.Platform}
	for _, member := range identity.Memberships {
		entry := membership{Org: member.Org, Role: member.Role}
		for _, w := range member.Workspaces {
			entry.Workspaces = append(entry.Workspaces, workspace{ID: w.ID, Role: w.Role})
		}
		slices.SortFunc(entry.Workspaces, func(a, b workspace) int { return strings.Compare(a.ID, b.ID) })
		out.Memberships = append(out.Memberships, entry)
	}
	slices.SortFunc(out.Memberships, func(a, b membership) int { return strings.Compare(a.Org, b.Org) })
	raw, _ := json.Marshal(out)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func refreshKey(sessionKey []byte) []byte {
	mac := hmac.New(sha256.New, sessionKey)
	_, _ = mac.Write([]byte("geass-sso-refresh-v1"))
	return mac.Sum(nil)
}

func sealToken(sessionKey []byte, plain string) (string, error) {
	block, err := aes.NewCipher(refreshKey(sessionKey))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func openToken(sessionKey []byte, sealed string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(refreshKey(sessionKey))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("the sealed token is too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plain), err
}

// refreshEntryName is the name of a person's entry: the identity key, which names the provider and the person.
func refreshEntryName(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:16])
}

// mutateRefreshSecret changes what is kept for refreshing, trying again when it was written meanwhile. The function says
// whether anything changed.
func (s *Server) mutateRefreshSecret(ctx context.Context, change func(entries map[string][]byte) bool) error {
	ctx = asSystem(ctx)
	key := client.ObjectKey{Name: ssoRefreshSecretName, Namespace: platform.SystemNamespace}
	retryable := func(err error) bool { return apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err) }
	return retry.OnError(writeRetry, retryable, func() error {
		secret := &corev1.Secret{}
		err := s.Client.Get(ctx, key, secret)
		if apierrors.IsNotFound(err) {
			entries := map[string][]byte{}
			if !change(entries) {
				return nil
			}
			return s.Client.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}}, Type: corev1.SecretTypeOpaque, Data: entries})
		}
		if err != nil {
			return err
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		if !change(secret.Data) {
			return nil
		}
		return s.Client.Update(ctx, secret)
	})
}

// rememberRefresh keeps what is needed to ask the provider again about a person who has just signed in. It does nothing when
// refreshing is off, or the provider gave no refresh token (it is then not an error: it is a provider that does not do it).
func (s *Server) rememberRefresh(ctx context.Context, settings ssoSettings, sessionKey []byte, identity *oidcauth.Identity, expires time.Time, sessionIdentity, org string) {
	if settings.RefreshMinutes <= 0 || identity.RefreshToken == "" {
		return
	}
	logger := ctrllog.FromContext(ctx)
	sealed, err := sealToken(sessionKey, identity.RefreshToken)
	if err != nil {
		logger.Error(err, "Could not keep what is needed to refresh a session")
		return
	}
	now := time.Now().Unix()
	record := refreshRecord{Token: sealed, Digest: identityDigest(identity), Expires: expires.Unix(), Checked: now, Started: now, Identity: sessionIdentity, Key: identity.Key, Org: org}
	encoded, _ := json.Marshal(record)
	name := refreshEntryName(sessionIdentity)
	if err := s.mutateRefreshSecret(ctx, func(entries map[string][]byte) bool {
		entries[name] = encoded
		dropOldestRefresh(entries)
		return true
	}); err != nil {
		logger.Error(err, "Could not keep what is needed to refresh a session")
	}
}

// dropOldestRefresh keeps the entries within the cap, the oldest signed-in going first.
func dropOldestRefresh(entries map[string][]byte) {
	if len(entries) <= ssoRefreshCap {
		return
	}
	type aged struct {
		name    string
		started int64
	}
	all := make([]aged, 0, len(entries))
	for name, raw := range entries {
		var record refreshRecord
		_ = json.Unmarshal(raw, &record)
		all = append(all, aged{name, record.Started})
	}
	slices.SortFunc(all, func(a, b aged) int { return int(a.started - b.started) })
	for _, old := range all[:len(all)-ssoRefreshCap] {
		delete(entries, old.name)
	}
}

// forgetRefresh drops what is kept for a person, which is done when their sessions are ended.
func (s *Server) forgetRefresh(ctx context.Context, identity string) {
	name := refreshEntryName(identity)
	_ = s.mutateRefreshSecret(ctx, func(entries map[string][]byte) bool {
		if _, there := entries[name]; !there {
			return false
		}
		delete(entries, name)
		return true
	})
}

// forgetAllRefresh drops everything that is kept, when every session of the provider is ended.
func (s *Server) forgetAllRefresh(ctx context.Context) {
	_ = s.mutateRefreshSecret(ctx, func(entries map[string][]byte) bool {
		if len(entries) == 0 {
			return false
		}
		clear(entries)
		return true
	})
}

// runSSORefresh asks the provider again about the people who are due, for as long as the dashboard runs.
func (s *Server) runSSORefresh(ctx context.Context) {
	ticker := time.NewTicker(ssoRefreshStep)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshSessionsOnce(ctx)
		}
	}
}

// refreshSessionsOnce is one pass: each person whose last check is older than the interval is asked about again.
func (s *Server) refreshSessionsOnce(ctx context.Context) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	logger := ctrllog.FromContext(ctx)
	settings, err := s.ssoSettings(ctx)
	if err != nil || !settings.External() || settings.RefreshMinutes <= 0 {
		return
	}
	var secret corev1.Secret
	if err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: ssoRefreshSecretName, Namespace: platform.SystemNamespace}, &secret); err != nil || len(secret.Data) == 0 {
		return
	}
	auth, err := s.dashboardAuth(mustRequest(ctx))
	if err != nil {
		return
	}
	provider, err := s.ssoProvider(ctx, settings)
	if err != nil {
		logger.Info("Could not reach the identity provider to refresh sessions", "reason", err.Error())
		// The organizations' own providers are asked about all the same.
		provider = nil
	}
	interval := time.Duration(settings.RefreshMinutes) * time.Minute
	now := time.Now()
	asked := 0
	for name, raw := range secret.Data {
		var record refreshRecord
		if json.Unmarshal(raw, &record) != nil {
			s.dropRefreshEntry(ctx, name)
			continue
		}
		if now.Unix() > record.Expires {
			s.dropRefreshEntry(ctx, name)
			continue
		}
		if now.Sub(time.Unix(record.Checked, 0)) < interval {
			continue
		}
		if asked >= maxRefreshPerPass {
			return
		}
		asked++
		recordProvider, recordSettings := provider, settings
		if record.Org != "" {
			var gone error
			if recordSettings, recordProvider, gone = s.orgRefreshProvider(ctx, record.Org, settings); gone != nil {
				s.endSessions(ctx, record.Identity, name, "the organization's identity provider is no longer in use")
				continue
			}
		}
		if recordProvider == nil {
			continue
		}
		s.refreshOne(ctx, recordSettings, recordProvider, auth.sessionKey, name, record, now)
	}
}

// mustRequest is a request for the pieces of the dashboard that read the session key through one.
func mustRequest(ctx context.Context) *http.Request {
	request, _ := http.NewRequestWithContext(asSystem(ctx), http.MethodGet, "/", nil)
	return request
}

func (s *Server) dropRefreshEntry(ctx context.Context, name string) {
	_ = s.mutateRefreshSecret(ctx, func(entries map[string][]byte) bool {
		if _, there := entries[name]; !there {
			return false
		}
		delete(entries, name)
		return true
	})
}

// refreshOne asks the provider again about one person and acts on the answer.
func (s *Server) refreshOne(ctx context.Context, settings ssoSettings, provider *oidcauth.Provider, sessionKey []byte, name string, record refreshRecord, now time.Time) {
	logger := ctrllog.FromContext(ctx)
	token, err := openToken(sessionKey, record.Token)
	if err != nil {
		s.dropRefreshEntry(ctx, name)
		return
	}
	identityKey, providerKey := record.Identity, record.Key
	if identityKey == "" {
		s.dropRefreshEntry(ctx, name)
		return
	}
	if providerKey == "" {
		providerKey = identityKey
	}
	result, err := provider.Refresh(ctx, token, providerKey)
	var denied *oidcauth.Denied
	switch {
	case errors.Is(err, oidcauth.ErrRefreshRevoked):
		s.endSessions(ctx, identityKey, name, "the provider no longer honours the session")
		return
	case errors.As(err, &denied):
		s.endSessions(ctx, identityKey, name, "the provider no longer lets the person in ("+denied.Reason+")")
		return
	case err != nil:
		// A check that could not be made says nothing of the person. The session is kept until the provider has been out of
		// reach for the grace, and then ended: it cannot be vouched for.
		if now.Sub(time.Unix(record.Checked, 0)) > settings.StaleGrace {
			s.endSessions(ctx, identityKey, name, "the provider could not be reached for too long")
			return
		}
		logger.Info("Could not refresh a session yet", "reason", err.Error())
		return
	}
	if result.Identity != nil && identityDigest(result.Identity) != record.Digest {
		s.endSessions(ctx, identityKey, name, "what the provider says of the person changed")
		return
	}
	sealed, err := sealToken(sessionKey, result.RefreshToken)
	if err != nil {
		return
	}
	record.Token, record.Checked = sealed, now.Unix()
	encoded, _ := json.Marshal(record)
	_ = s.mutateRefreshSecret(ctx, func(entries map[string][]byte) bool {
		if _, there := entries[name]; !there {
			return false
		}
		entries[name] = encoded
		return true
	})
}

// endSessions ends the sessions of a person, as a sign-out at the provider would, and forgets what was kept for them.
func (s *Server) endSessions(ctx context.Context, identityKey, entryName, why string) {
	logger := ctrllog.FromContext(ctx)
	if err := s.revokeSSOIdentity(ctx, identityKey); err != nil {
		logger.Error(err, "Could not end the sessions of a person the provider no longer vouches for")
		return
	}
	s.dropRefreshEntry(ctx, entryName)
	logger.Info("Ended the sessions of a person", "reason", why)
}

// orgRefreshProvider is the settings and the provider to ask about a person who signed in through an organization's own provider.
// The platform's refresh interval and grace apply. It is an error when the organization's provider is no longer in use: the
// person's sessions have ended already, and the entry is dropped.
func (s *Server) orgRefreshProvider(ctx context.Context, org string, platformSettings ssoSettings) (ssoSettings, *oidcauth.Provider, error) {
	settings, _, err := s.orgSigninSettings(ctx, org, purposeLogin)
	if err != nil {
		return ssoSettings{}, nil, err
	}
	settings.RefreshMinutes, settings.StaleGrace = platformSettings.RefreshMinutes, platformSettings.StaleGrace
	provider, err := s.orgProvider(ctx, settings)
	if err != nil {
		// Out of reach says nothing of the person: the entry is kept, and asked about again.
		return settings, nil, nil
	}
	return settings, provider, nil
}
