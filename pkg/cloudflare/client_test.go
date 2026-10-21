package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
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

// dnsFake is a Cloudflare zone with the records it is given. It keeps the requests that wrote.
type dnsFake struct {
	records []map[string]string
	writes  []string
	bodies  []map[string]any
}

func (f *dnsFake) client(t *testing.T) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			raw, _ := json.Marshal(f.records)
			_, _ = w.Write([]byte(`{"success":true,"result":` + string(raw) + `}`))
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.writes = append(f.writes, r.Method+" "+r.URL.Path)
		f.bodies = append(f.bodies, body)
		_, _ = w.Write([]byte(`{"success":true,"result":{}}`))
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{APIToken: "token", HTTP: &http.Client{Transport: &rewriteTransport{base: base}}}
}

func TestACNAMENobodyHadIsCreatedAndMarkedAsGeasss(t *testing.T) {
	fake := &dnsFake{}
	err := fake.client(t).EnsureCNAMERecord(context.Background(), "zone1", "shop.example.com", "tunnel-1.cfargotunnel.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.writes) != 1 || fake.writes[0] != "POST /client/v4/zones/zone1/dns_records" {
		t.Fatalf("expected one create, got %v", fake.writes)
	}
	if fake.bodies[0]["comment"] != recordComment {
		t.Fatalf("a record Geass makes says so: %v", fake.bodies[0])
	}
}

func TestACNAMEGeassMadeIsRepointed(t *testing.T) {
	for name, record := range map[string]map[string]string{
		"marked":                       {"id": "r1", "content": "old.example.net", "comment": recordComment},
		"already right, from before":   {"id": "r1", "content": "tunnel-1.cfargotunnel.com"},
		"an older tunnel, from before": {"id": "r1", "content": "tunnel-0.cfargotunnel.com"},
	} {
		fake := &dnsFake{records: []map[string]string{record}}
		err := fake.client(t).EnsureCNAMERecord(context.Background(), "zone1", "shop.example.com", "tunnel-1.cfargotunnel.com")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(fake.writes) != 1 || fake.writes[0] != "PUT /client/v4/zones/zone1/dns_records/r1" {
			t.Fatalf("%s: expected an update, got %v", name, fake.writes)
		}
	}
}

func TestACNAMESomeoneElseMadeIsNotWrittenOver(t *testing.T) {
	fake := &dnsFake{records: []map[string]string{{"id": "r1", "content": "cname.vercel-dns.com"}}}
	err := fake.client(t).EnsureCNAMERecord(context.Background(), "zone1", "shop.example.com", "tunnel-1.cfargotunnel.com")
	if !errors.Is(err, ErrRecordNotOurs) {
		t.Fatalf("expected ErrRecordNotOurs, got %v", err)
	}
	if len(fake.writes) != 0 {
		t.Fatalf("nothing may be written over it: %v", fake.writes)
	}
}

func TestTheDashboardsARecordIsStillManagedWhateverWasThere(t *testing.T) {
	fake := &dnsFake{records: []map[string]string{{"id": "r1", "content": "203.0.113.9"}}}
	if err := fake.client(t).EnsureARecord(context.Background(), "zone1", "geass.example.com", "198.51.100.7", true); err != nil {
		t.Fatal(err)
	}
	if len(fake.writes) != 1 || fake.writes[0] != "PUT /client/v4/zones/zone1/dns_records/r1" {
		t.Fatalf("the platform administrator chose this name, so it is updated: %v", fake.writes)
	}
}
