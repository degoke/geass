package oidcauth

import (
	"testing"

	jose "github.com/go-jose/go-jose/v4"
)

func TestOnlyASigningKeyWithTheRightIDIsACandidate(t *testing.T) {
	keys := &keySet{keys: []jose.JSONWebKey{
		{KeyID: "a", Use: "enc"},
		{KeyID: "a", Use: "sig"},
		{KeyID: "b"},
		{KeyID: "c", Use: "sig"},
	}}
	if got := keys.find("a"); len(got) != 1 || got[0].Use != "sig" {
		t.Errorf("find(a) = %v, want only the signing key", got)
	}
	if got := keys.find("b"); len(got) != 1 {
		t.Errorf("a key with no stated use is a signing key: %v", got)
	}
	if got := keys.find("nobody"); len(got) != 0 {
		t.Errorf("find(nobody) = %v", got)
	}
	if got := keys.find(""); len(got) != 3 {
		t.Errorf("with no key id every signing key is tried: %d", len(got))
	}
}
