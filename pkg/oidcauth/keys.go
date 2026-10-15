package oidcauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// The provider's signing keys are kept here, and fetched again only as often as is safe. The library's own key set
// fetches them again whenever a signature fails to verify, with no limit, which would let anyone who can send a forged
// token (the back-channel logout address is open to the internet) make the operator call the provider once for each:
// a request that is cheap to send and leaves the operator's address as the one that was calling.

var (
	// keyRefetchCooldown is the least time between two fetches of the keys because of a token signed with a key that is
	// not known (which is how a rotation looks). A forged token with a key id nobody has costs at most one fetch in
	// this time, and a real rotation is noticed within it.
	keyRefetchCooldown = 15 * time.Second
	// keyMaxAge is how long the keys are trusted before the next token makes Geass fetch them again, so that a key the
	// provider withdrew does not stay good for ever.
	keyMaxAge = 6 * time.Hour
)

var signatureAlgorithms = []jose.SignatureAlgorithm{
	jose.EdDSA, jose.ES256, jose.ES384, jose.ES512, jose.RS256, jose.RS384, jose.RS512, jose.PS256, jose.PS384, jose.PS512,
}

// keySet verifies the signature of a token with the provider's keys. It satisfies the library's KeySet.
type keySet struct {
	url    string
	client *http.Client

	mu          sync.Mutex
	keys        []jose.JSONWebKey
	fetched     time.Time
	lastAttempt time.Time
}

func newKeySet(url string, client *http.Client) *keySet { return &keySet{url: url, client: client} }

// VerifySignature checks the token's signature against the key it names and returns its payload.
func (k *keySet) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	signed, err := jose.ParseSigned(raw, signatureAlgorithms)
	if err != nil {
		return nil, fmt.Errorf("could not parse the token: %w", err)
	}
	if len(signed.Signatures) != 1 {
		return nil, errors.New("a token with more than one signature is not accepted")
	}
	keyID := signed.Signatures[0].Header.KeyID
	for _, key := range k.candidates(ctx, keyID) {
		if payload, err := signed.Verify(key); err == nil {
			return payload, nil
		}
	}
	return nil, errors.New("the token is not signed by a key of the provider")
}

// candidates are the keys that could have signed a token with this key id. The provider's keys are fetched when they are
// not known yet, are older than keyMaxAge, or do not include this id, and in every one of those cases at most once in
// keyRefetchCooldown, however many tokens ask: a provider that is down, or a flood of forged tokens, costs one request
// in that time. The fetch is made without the lock held, so a slow provider does not hold up the verification of tokens
// whose keys are known.
func (k *keySet) candidates(ctx context.Context, keyID string) []*jose.JSONWebKey {
	k.mu.Lock()
	now := time.Now()
	found := k.find(keyID)
	stale := k.fetched.IsZero() || now.Sub(k.fetched) > keyMaxAge
	fetch := (stale || len(found) == 0) && now.Sub(k.lastAttempt) >= keyRefetchCooldown
	if fetch {
		// Taken now, so that the callers that arrive while the fetch is under way do not start one each.
		k.lastAttempt = now
	}
	k.mu.Unlock()
	if !fetch {
		return found
	}
	keys, ok := k.download(ctx)
	k.mu.Lock()
	defer k.mu.Unlock()
	if ok {
		k.keys, k.fetched = keys, time.Now()
	}
	return k.find(keyID)
}

func (k *keySet) find(keyID string) []*jose.JSONWebKey {
	var found []*jose.JSONWebKey
	for i := range k.keys {
		key := &k.keys[i]
		if (keyID == "" || key.KeyID == keyID) && (key.Use == "" || key.Use == "sig") {
			found = append(found, key)
		}
	}
	return found
}

// download reads the provider's current keys. The client caps the size of what it reads (see netguard.Client), so an
// oversized document is an error and not a truncated one.
func (k *keySet) download(ctx context.Context) ([]jose.JSONWebKey, bool) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url, nil)
	if err != nil {
		return nil, false
	}
	response, err := k.client.Do(request)
	if err != nil {
		return nil, false
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, false
	}
	var set jose.JSONWebKeySet
	if json.Unmarshal(body, &set) != nil || len(set.Keys) == 0 {
		return nil, false
	}
	return set.Keys, true
}
