package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestTunnelCNAMETarget(t *testing.T) {
	if TunnelCNAMETarget("abc-123") != "abc-123.cfargotunnel.com" {
		t.Fatal("unexpected tunnel cname")
	}
}

func TestNormalizeAPIToken(t *testing.T) {
	if NormalizeAPIToken("  Bearer  tok  ") != "tok" {
		t.Fatal("expected bearer prefix and whitespace to be stripped")
	}
}

func TestValidateAcceptsZonesWhenVerifyFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/client/v4/user/tokens/verify":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"verify not allowed"}]}`))
		case "/client/v4/zones":
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{
		APIToken: "zone-token",
		HTTP:     &http.Client{Transport: &rewriteTransport{base: base}},
	}

	if err := client.Validate(context.Background()); err != nil {
		t.Fatalf("expected validate to succeed via zones probe: %v", err)
	}
}

type rewriteTransport struct {
	base *url.URL
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = t.base.Scheme
	cloned.URL.Host = t.base.Host
	return http.DefaultTransport.RoundTrip(cloned)
}

func TestZoneUnmarshalIncludesAccount(t *testing.T) {
	raw := `{"id":"zone1","name":"example.com","account":{"id":"acct1","name":"Acme"}}`
	var zone Zone
	if err := json.Unmarshal([]byte(raw), &zone); err != nil {
		t.Fatal(err)
	}
	if AccountIDFromZone(zone) != "acct1" {
		t.Fatalf("expected account id acct1, got %q", AccountIDFromZone(zone))
	}
}
