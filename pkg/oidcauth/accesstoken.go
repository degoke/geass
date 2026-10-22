package oidcauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// An access token is how a program, or a page that is not Geass's own, acts for a person who signed in at the provider: the
// provider issued it to that person, for an API, and the API takes it as `Authorization: Bearer`. What Geass accepts is the
// profile of RFC 9068 (JWT access tokens), and no more:
//
//   - a JWT signed by one of the provider's keys, with a public-key algorithm (see signingAlgorithms; "none" and the HMAC
//     ones are refused);
//   - the header "typ" is "at+jwt". That is what tells an access token from an ID token, which is signed by the same keys and
//     is addressed to the same client: an ID token has no such type, and is never accepted here;
//   - "iss" is the configured issuer, "aud" holds the audience the caller asks for (the cluster's own, so that a token for
//     another cluster of the same provider is not good here), "sub" is there, and "exp" and "iat" are, with at most
//     accessTokenSkew of difference between the clocks.
//
// An opaque access token (one that is not a JWT) is not accepted: nothing here asks the provider whether it is good.

// AccessTokenType is the "typ" an access token's header has (RFC 9068, section 2.1).
const AccessTokenType = "at+jwt"

const (
	// accessTokenSkew is how far the clocks of the provider and of Geass may disagree about exp, nbf and iat.
	accessTokenSkew = 30 * time.Second
	// accessTokenMaxLength bounds what is parsed: a bearer credential comes from anyone who can reach the API.
	accessTokenMaxLength = 16 * 1024
)

// ErrNotAccessToken is what VerifyAccessToken says of a credential that is not a valid access token of this provider for
// this audience. The reason is in the error's text, for a log: it is never a reason to say more to the caller.
var ErrNotAccessToken = errors.New("not a valid access token")

// AccessToken is a verified access token.
type AccessToken struct {
	Issuer  string
	Subject string
	// Key is Key(Issuer, Subject): the same person's key as signing in gives, when the provider uses one subject for both.
	Key string
	// IssuedAt and Expires bound when the token is good. Expires is the "exp" claim itself, without the skew, so that
	// something that outlives a request (a stream) can end when it says.
	IssuedAt time.Time
	Expires  time.Time
	// Claims are all of its claims, to be read with Config.AuthorizeAccessToken.
	Claims map[string]any
}

// VerifyAccessToken checks a bearer credential as an access token of the provider that is good for audience at now. It
// returns ErrNotAccessToken, wrapped with the reason, when it is not.
func (p *Provider) VerifyAccessToken(ctx context.Context, raw, audience string, now time.Time) (*AccessToken, error) {
	reject := func(format string, args ...any) (*AccessToken, error) {
		return nil, fmt.Errorf("%w: %s", ErrNotAccessToken, fmt.Sprintf(format, args...))
	}
	if strings.TrimSpace(audience) == "" {
		return reject("no audience is set to check it against")
	}
	if raw == "" || len(raw) > accessTokenMaxLength {
		return reject("it is empty or too long")
	}
	// Parsing with the allowed algorithms is what refuses "none" and every HMAC algorithm: a token that names another is
	// not parsed at all.
	signed, err := jose.ParseSigned(raw, signatureAlgorithms)
	if err != nil {
		return reject("it is not a signed token")
	}
	if len(signed.Signatures) != 1 {
		return reject("it has more than one signature")
	}
	if !isAccessTokenType(signed.Signatures[0].Protected.ExtraHeaders[jose.HeaderType]) {
		return reject("its type is not %s", AccessTokenType)
	}
	payload, err := p.keys.VerifySignature(ctx, raw)
	if err != nil {
		return reject("%v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil || claims == nil {
		return reject("its claims cannot be read")
	}
	if issuer, _ := claims["iss"].(string); issuer != p.config.Issuer {
		return reject("it was not issued by %s", p.config.Issuer)
	}
	if !audienceHas(claims["aud"], audience) {
		return reject("it is not for %s", audience)
	}
	subject, _ := claims["sub"].(string)
	if strings.TrimSpace(subject) == "" {
		return reject("it names no subject")
	}
	// An ID token carries these, and a token of the kind that is not an access token says so: either is refused, whatever
	// its header says.
	for _, name := range []string{"nonce", "at_hash", "c_hash"} {
		if _, found := claims[name]; found {
			return reject("it is an ID token (it has %s)", name)
		}
	}
	if use, found := claims["token_use"]; found && use != "access" {
		return reject("its token_use is not access")
	}
	expires, ok := numericDate(claims["exp"])
	if !ok {
		return reject("it has no expiry")
	}
	issued, ok := numericDate(claims["iat"])
	if !ok {
		return reject("it does not say when it was issued")
	}
	if !now.Before(expires.Add(accessTokenSkew)) {
		return reject("it has expired")
	}
	if issued.After(now.Add(accessTokenSkew)) {
		return reject("it was issued in the future")
	}
	if value, found := claims["nbf"]; found {
		notBefore, ok := numericDate(value)
		if !ok || notBefore.After(now.Add(accessTokenSkew)) {
			return reject("it is not valid yet")
		}
	}
	return &AccessToken{
		Issuer: p.config.Issuer, Subject: subject, Key: Key(p.config.Issuer, subject), IssuedAt: issued, Expires: expires, Claims: claims,
	}, nil
}

// isAccessTokenType is whether a header's "typ" is the one of access tokens: the media type name is compared without
// case, and the "application/" its short form leaves out may be there (RFC 7515, section 4.1.9).
func isAccessTokenType(value any) bool {
	text, _ := value.(string)
	text = strings.ToLower(strings.TrimSpace(text))
	return strings.TrimPrefix(text, "application/") == AccessTokenType
}

// audienceHas is whether an "aud" claim, a string or a list of them, holds the audience.
func audienceHas(claim any, audience string) bool {
	switch typed := claim.(type) {
	case string:
		return typed == audience
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && text == audience {
				return true
			}
		}
	}
	return false
}

// numericDate reads a JWT date: the seconds since the epoch, a number.
func numericDate(value any) (time.Time, bool) {
	seconds, ok := value.(float64)
	if !ok || seconds <= 0 || seconds > 1<<40 {
		return time.Time{}, false
	}
	return time.Unix(int64(seconds), 0), true
}
