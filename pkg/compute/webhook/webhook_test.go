package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/degoke/geass/pkg/compute"
)

// service is a user's machine service, as small as the contract allows.
type service struct {
	machines map[string]machine
	seq      int
	token    string
	created  []createRequest
	noVerify bool
}

func (s *service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.token != "" && r.Header.Get("Authorization") != "Bearer "+s.token {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	reply := func(status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/verify":
		if s.noVerify {
			http.NotFound(w, r)
			return
		}
		reply(200, map[string]any{"account": "my racks", "warnings": []string{"power is metered"}, "expiresAt": "2030-01-01T00:00:00Z"})
	case r.Method == "GET" && r.URL.Path == "/sizes":
		reply(200, map[string]any{"sizes": []map[string]any{{"id": "rack-unit", "vcpus": 8, "memoryMiB": 16384, "hourlyPrice": 0.02, "currency": "USD", "arch": "amd64"}}})
	case r.Method == "POST" && r.URL.Path == "/machines":
		var req createRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		for _, m := range s.machines {
			if m.Name == req.Name {
				reply(200, m)
				return
			}
		}
		s.created = append(s.created, req)
		s.seq++
		m := machine{ID: "m" + string(rune('0'+s.seq)), Name: req.Name, State: "pending", Labels: req.Labels, Size: req.Size}
		s.machines[m.ID] = m
		reply(202, m)
	case r.Method == "GET" && r.URL.Path == "/machines":
		var list []machine
		for _, m := range s.machines {
			// A careless service that ignores the filter.
			list = append(list, m)
		}
		reply(200, map[string]any{"machines": list})
	case r.Method == "GET":
		m, ok := s.machines[r.URL.Path[len("/machines/"):]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		m.State, m.PublicIPv4 = "running", "198.51.100.8"
		reply(200, m)
	case r.Method == "DELETE":
		delete(s.machines, r.URL.Path[len("/machines/"):])
		w.WriteHeader(204)
	default:
		http.NotFound(w, r)
	}
}

func newService(t *testing.T, token string) (*service, *Provider) {
	s := &service{machines: map[string]machine{}, token: token}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	p, err := New(srv.URL, token, compute.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func TestCreateSendsEverythingTheServiceNeedsAndIsIdempotentOnTheName(t *testing.T) {
	s, p := newService(t, "tok")
	owner := compute.Owner{Cluster: "c1", Pool: "racks"}
	spec := compute.InstanceSpec{
		Name: "racks-a", Region: "dc1", Size: "rack-unit", UserData: "#cloud-config\n", AuthorizedKeys: []string{"ssh-ed25519 AAAA"},
		Labels: owner.Labels("racks-a"), Settings: map[string]string{"vlan": "12"}, MultiSite: true, ControlPlaneCIDRs: []string{"203.0.113.5/32"},
	}
	first, err := p.Create(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Create(context.Background(), spec)
	if err != nil || again.ID != first.ID || len(s.created) != 1 {
		t.Fatalf("a repeated create made %d machines (%v)", len(s.created), err)
	}
	sent := s.created[0]
	if sent.UserData != "#cloud-config\n" || sent.Settings["vlan"] != "12" || !sent.MultiSite || sent.AuthorizedKeys[0] != "ssh-ed25519 AAAA" || sent.Labels[compute.LabelNode] != "racks-a" {
		t.Errorf("request = %+v", sent)
	}
}

func TestGetListAndDeleteFollowTheContract(t *testing.T) {
	_, p := newService(t, "")
	owner := compute.Owner{Cluster: "c1", Pool: "racks"}
	made, _ := p.Create(context.Background(), compute.InstanceSpec{Name: "racks-a", Size: "x", Labels: owner.Labels("racks-a")})
	if _, err := p.Create(context.Background(), compute.InstanceSpec{Name: "theirs", Size: "x", Labels: compute.Owner{Cluster: "other", Pool: "racks"}.Labels("theirs")}); err != nil {
		t.Fatal(err)
	}

	got, err := p.Get(context.Background(), made.ID)
	if err != nil || !got.Ready() || got.PublicIPv4 != "198.51.100.8" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	listed, err := p.List(context.Background(), owner)
	if err != nil || len(listed) != 1 || listed[0].Name != "racks-a" {
		t.Errorf("a service that ignores the filter must not make Geass see another cluster's machines: %+v, %v", listed, err)
	}
	if err := p.Delete(context.Background(), made.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(context.Background(), made.ID); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
	if _, err := p.Get(context.Background(), made.ID); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
}

func TestTheServiceMayLeaveOutVerifyAndSizes(t *testing.T) {
	s, p := newService(t, "")
	s.noVerify = true
	account, err := p.Verify(context.Background())
	if err != nil || len(account.Warnings) != 1 {
		t.Errorf("Verify = %+v, %v", account, err)
	}
	sizes, err := p.Sizes(context.Background(), "")
	if err != nil || len(sizes) != 1 || sizes[0].ID != "rack-unit" || sizes[0].HourlyPrice != 0.02 {
		t.Errorf("Sizes = %+v, %v", sizes, err)
	}
}

func TestVerifyReadsTheAccountAndExpiry(t *testing.T) {
	_, p := newService(t, "")
	account, err := p.Verify(context.Background())
	if err != nil || account.Name != "my racks" || account.ExpiresAt.Year() != 2030 || account.Warnings[0] != "power is metered" {
		t.Errorf("Verify = %+v, %v", account, err)
	}
}

func TestABadTokenIsRefused(t *testing.T) {
	s := &service{machines: map[string]machine{}, token: "right"}
	srv := httptest.NewServer(s)
	defer srv.Close()
	p, _ := New(srv.URL, "wrong", compute.Options{})
	if _, err := p.Verify(context.Background()); !errors.Is(err, compute.ErrAuth) {
		t.Errorf("Verify = %v", err)
	}
}

func TestAddressesTheOperatorShouldNotCallAreRefused(t *testing.T) {
	for _, bad := range []string{"", "ftp://example.com", "http://169.254.169.254/latest", "not a url", "https://0.0.0.0/x"} {
		if _, err := New(bad, "", compute.Options{}); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	for _, good := range []string{"https://machines.example.com/geass", "http://10.0.0.5:8080"} {
		if _, err := New(good, "", compute.Options{}); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
}

func TestStatesAreTranslated(t *testing.T) {
	for in, want := range map[string]compute.State{
		"running": compute.StateRunning, "off": compute.StateStopped, "deleting": compute.StateTerminating, "deleted": compute.StateGone, "booting": compute.StatePending,
	} {
		if got := stateOf(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestTheProviderRegistersItself(t *testing.T) {
	d, ok := compute.Lookup(ID)
	if !ok || len(d.Fields) != 2 || !d.Fields[1].Optional {
		t.Fatalf("descriptor = %+v", d)
	}
	if _, err := compute.New(ID, compute.Credentials{}, compute.Options{}); err == nil {
		t.Error("a provider was built without an address")
	}
}

// A service address is checked as it is written, which a name that resolves to a metadata address, or a redirect to
// one, gets past. The client checks where it is about to connect, and where it is sent.
func TestAServiceCannotSendTheOperatorToAMetadataAddress(t *testing.T) {
	for _, address := range []string{"169.254.169.254:80", "[fe80::1]:80", "[fd00:ec2::254]:80", "0.0.0.0:80", "[::]:80"} {
		if err := refuseMetadataAddress("tcp", address, nil); err == nil {
			t.Errorf("a connection to %s was allowed", address)
		}
	}
	for _, address := range []string{"127.0.0.1:8080", "10.0.0.5:443", "203.0.113.7:443", "[2001:db8::1]:443"} {
		if err := refuseMetadataAddress("tcp", address, nil); err != nil {
			t.Errorf("a connection to %s was refused: %v", address, err)
		}
	}

	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/verify" {
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
			return
		}
		reached = true
	}))
	defer srv.Close()
	p, err := New(srv.URL, "", compute.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Verify(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not one a service can be reached at") {
		t.Fatalf("a redirect to the metadata service was followed or not explained: %v", err)
	}
	if reached {
		t.Fatal("the service's own handler ran for the redirect target")
	}
}
