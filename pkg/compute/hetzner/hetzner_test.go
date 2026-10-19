package hetzner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/degoke/geass/pkg/compute"
)

// fakeHetzner is the part of the Hetzner Cloud API Geass uses, in memory.
type fakeHetzner struct {
	t         *testing.T
	token     string
	servers   map[int64]map[string]any
	firewalls map[int64]map[string]any
	next      int64
	// failCreate, when set, is the status and error body of the next POST /servers.
	failCreate func() (int, string)
	requests   []string
}

func newFakeHetzner(t *testing.T) (*fakeHetzner, *Provider) {
	f := &fakeHetzner{t: t, token: "tok", servers: map[int64]map[string]any{}, firewalls: map[int64]map[string]any{}, next: 100}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, New("tok", compute.Options{BaseURL: srv.URL})
}

func (f *fakeHetzner) reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeHetzner) fail(w http.ResponseWriter, status int, code, message string) {
	f.reply(w, status, map[string]any{"error": map[string]any{"code": code, "message": message}})
}

func (f *fakeHetzner) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		f.fail(w, 401, "unauthorized", "bad token")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	switch {
	case r.Method == "GET" && path == "server_types":
		f.reply(w, 200, map[string]any{"server_types": []map[string]any{
			{"name": "cx33", "description": "CX33", "cores": 4, "memory": 8.0, "disk": 80, "architecture": "x86",
				"prices": []map[string]any{{"location": "fsn1", "price_hourly": map[string]any{"net": "0.0150"}}}},
			{"name": "cx23", "description": "CX23", "cores": 2, "memory": 4.0, "disk": 40, "architecture": "x86",
				"prices": []map[string]any{{"location": "fsn1", "price_hourly": map[string]any{"net": "0.0088"}}, {"location": "ash", "price_hourly": map[string]any{"net": "0.0120"}}}},
			{"name": "old", "description": "old", "cores": 1, "memory": 1.0, "disk": 20, "architecture": "x86", "deprecated": true,
				"prices": []map[string]any{{"location": "fsn1", "price_hourly": map[string]any{"net": "0.001"}}}},
		}, "meta": map[string]any{"pagination": map[string]any{"next_page": nil}}})
	case r.Method == "GET" && path == "firewalls":
		var list []map[string]any
		for _, fw := range f.firewalls {
			list = append(list, fw)
		}
		f.reply(w, 200, map[string]any{"firewalls": list})
	case r.Method == "GET" && strings.HasPrefix(path, "firewalls/"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(path, "firewalls/"), 10, 64)
		fw, ok := f.firewalls[id]
		if !ok {
			f.fail(w, 404, "not_found", "firewall not found")
			return
		}
		f.reply(w, 200, map[string]any{"firewall": fw})
	case r.Method == "POST" && path == "firewalls":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.next++
		body["id"] = f.next
		f.firewalls[f.next] = body
		f.reply(w, 201, map[string]any{"firewall": body})
	case r.Method == "POST" && strings.HasSuffix(path, "/actions/set_rules"):
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		id, _ := strconv.ParseInt(strings.Split(path, "/")[1], 10, 64)
		f.firewalls[id]["rules"] = body["rules"]
		f.reply(w, 201, map[string]any{"actions": []any{}})
	case r.Method == "POST" && path == "servers":
		if f.failCreate != nil {
			status, code := f.failCreate()
			f.fail(w, status, code, "refused")
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, s := range f.servers {
			if s["name"] == body["name"] {
				f.fail(w, 409, "uniqueness_error", "server name is already used")
				return
			}
		}
		f.next++
		server := map[string]any{
			"id": f.next, "name": body["name"], "status": "initializing", "created": "2026-10-19T10:00:00+00:00",
			"labels": body["labels"], "server_type": map[string]any{"name": body["server_type"]},
			"datacenter": map[string]any{"location": map[string]any{"name": body["location"]}},
			"public_net": map[string]any{"ipv4": map[string]any{"ip": "203.0.113.50"}},
			"_request":   body,
		}
		f.servers[f.next] = server
		f.reply(w, 201, map[string]any{"server": server})
	case r.Method == "GET" && path == "servers":
		var list []map[string]any
		sel := r.URL.Query().Get("label_selector")
		name := r.URL.Query().Get("name")
		for _, s := range f.servers {
			if name != "" && s["name"] != name {
				continue
			}
			if sel != "" && !matchesSelector(s["labels"], sel) {
				continue
			}
			list = append(list, s)
		}
		f.reply(w, 200, map[string]any{"servers": list, "meta": map[string]any{"pagination": map[string]any{"next_page": nil}}})
	case strings.HasPrefix(path, "servers/"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(path, "servers/"), 10, 64)
		server, ok := f.servers[id]
		if !ok {
			f.fail(w, 404, "not_found", "server not found")
			return
		}
		if r.Method == "DELETE" {
			delete(f.servers, id)
			f.reply(w, 200, map[string]any{"action": map[string]any{}})
			return
		}
		f.reply(w, 200, map[string]any{"server": server})
	default:
		f.fail(w, 404, "not_found", "no route "+r.Method+" "+path)
	}
}

func matchesSelector(labels any, selector string) bool {
	m, _ := labels.(map[string]any)
	for term := range strings.SplitSeq(selector, ",") {
		k, v, _ := strings.Cut(term, "=")
		if fmt.Sprint(m[k]) != v {
			return false
		}
	}
	return true
}

func spec(name string) compute.InstanceSpec {
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	return compute.InstanceSpec{
		Name: name, Region: "fsn1", Size: "cx23", UserData: "#cloud-config\n", Labels: owner.Labels(name),
		ControlPlaneCIDRs: []string{"203.0.113.5/32"}, MultiSite: true,
	}
}

func TestCreateSendsTheMachineLabelledWithItsUserDataAndAFirewall(t *testing.T) {
	f, p := newFakeHetzner(t)

	instance, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if instance.Name != "burst-a" || instance.PublicIPv4 != "203.0.113.50" || instance.State != compute.StatePending {
		t.Errorf("instance = %+v", instance)
	}
	var sent map[string]any
	for _, s := range f.servers {
		sent = s["_request"].(map[string]any)
	}
	if sent["server_type"] != "cx23" || sent["location"] != "fsn1" || sent["image"] != "ubuntu-24.04" || sent["user_data"] != "#cloud-config\n" {
		t.Errorf("request = %v", sent)
	}
	labels := sent["labels"].(map[string]any)
	if labels[compute.LabelManaged] != "true" || labels[compute.LabelNode] != "burst-a" || labels[compute.LabelCluster] != "c1" {
		t.Errorf("labels = %v", labels)
	}
	if len(sent["firewalls"].([]any)) != 1 {
		t.Errorf("the machine is created without its firewall: %v", sent["firewalls"])
	}
}

func TestTheFirewallLetsInOnlyWhatTheClusterNeeds(t *testing.T) {
	f, p := newFakeHetzner(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if len(f.firewalls) != 1 {
		t.Fatalf("%d firewalls, want 1", len(f.firewalls))
	}
	var rules []firewallRule
	for _, fw := range f.firewalls {
		raw, _ := json.Marshal(fw["rules"])
		_ = json.Unmarshal(raw, &rules)
	}
	got := map[string]string{}
	for _, r := range rules {
		got[r.Protocol+"/"+r.Port] = strings.Join(r.SourceIPs, ",")
	}
	if got["tcp/22"] != "203.0.113.5/32" || got["tcp/10250"] != "203.0.113.5/32" {
		t.Errorf("SSH and the kubelet must come from the control plane only: %v", got)
	}
	if got["udp/51820"] != "0.0.0.0/0,::/0" {
		t.Errorf("the overlay port must be open in a multi-site cluster: %v", got)
	}
	if len(got) != 3 {
		t.Errorf("rules = %v, want exactly three", got)
	}
}

func TestTheFirewallIsMadeOnceAndItsRulesAreKeptCurrent(t *testing.T) {
	f, p := newFakeHetzner(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	next := spec("burst-b")
	next.ControlPlaneCIDRs = []string{"203.0.113.77/32"} // the control plane moved
	if _, err := p.Create(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if len(f.firewalls) != 1 {
		t.Fatalf("%d firewalls, want the one to be reused", len(f.firewalls))
	}
	for _, fw := range f.firewalls {
		raw, _ := json.Marshal(fw["rules"])
		if !strings.Contains(string(raw), "203.0.113.77/32") || strings.Contains(string(raw), "203.0.113.5/32") {
			t.Errorf("the firewall still allows the old control plane: %s", raw)
		}
	}
}

func TestAFirewallTheUserNamesIsUsedAsItIs(t *testing.T) {
	f, p := newFakeHetzner(t)
	s := spec("burst-a")
	s.Settings = map[string]string{SettingFirewallID: "4242"}
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(f.firewalls) != 0 {
		t.Error("Geass made a firewall although one was named")
	}
	for _, srv := range f.servers {
		fw := srv["_request"].(map[string]any)["firewalls"].([]any)[0].(map[string]any)["firewall"]
		if fw != float64(4242) {
			t.Errorf("firewall = %v", fw)
		}
	}
}

func TestACreateThatRepeatsTheNameReturnsTheOwnersMachine(t *testing.T) {
	f, p := newFakeHetzner(t)
	first, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || len(f.servers) != 1 {
		t.Errorf("a repeated create made a second machine: %s vs %s, %d servers", again.ID, first.ID, len(f.servers))
	}
}

func TestACreateThatMeetsAnotherClustersNameIsAnErrorNotAnAdoption(t *testing.T) {
	_, p := newFakeHetzner(t)
	other := spec("burst-a")
	other.Labels = compute.Owner{Cluster: "someone-else", Pool: "burst"}.Labels("burst-a")
	if _, err := p.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Create(context.Background(), spec("burst-a")); err == nil {
		t.Fatal("a machine of another cluster was taken for ours")
	}
}

func TestProviderFailuresBecomeTheErrorsCallersActOn(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		code   string
		check  func(error) bool
	}{
		"limit":    {403, "resource_limit_exceeded", compute.IsQuota},
		"capacity": {422, "placement_error", func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }},
		"stock":    {412, "resource_unavailable", func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }},
		"token":    {403, "forbidden", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
	} {
		t.Run(name, func(t *testing.T) {
			f, p := newFakeHetzner(t)
			f.failCreate = func() (int, string) { return tc.status, tc.code }
			if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !tc.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestABadTokenIsRefused(t *testing.T) {
	f, _ := newFakeHetzner(t)
	f.token = "other"
	srv := httptest.NewServer(f)
	defer srv.Close()
	p := New("tok", compute.Options{BaseURL: srv.URL})
	if _, err := p.Verify(context.Background()); !errors.Is(err, compute.ErrAuth) {
		t.Errorf("Verify = %v", err)
	}
}

func TestListFindsOnlyTheOwnersMachines(t *testing.T) {
	_, p := newFakeHetzner(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	other := spec("web-a")
	other.Labels = compute.Owner{Cluster: "someone-else", Pool: "burst"}.Labels("web-a")
	if _, err := p.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	pool, err := p.List(context.Background(), compute.Owner{Cluster: "c1", Pool: "burst"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pool) != 1 || pool[0].Name != "burst-a" {
		t.Errorf("listed %+v", pool)
	}
	elsewhere, _ := p.List(context.Background(), compute.Owner{Cluster: "c1", Pool: "other-pool"})
	if len(elsewhere) != 0 {
		t.Errorf("listed another pool's machines: %+v", elsewhere)
	}
}

func TestDeleteIsIdempotentAndGetReportsAGoneMachine(t *testing.T) {
	_, p := newFakeHetzner(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Errorf("deleting again: %v", err)
	}
	if _, err := p.Get(context.Background(), created.ID); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
}

func TestSizesAreCheapestFirstWithoutRetiredOnesAndOnlyWhereOffered(t *testing.T) {
	_, p := newFakeHetzner(t)
	sizes, err := p.Sizes(context.Background(), "fsn1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 2 || sizes[0].ID != "cx23" || sizes[1].ID != "cx33" {
		t.Fatalf("sizes = %+v", sizes)
	}
	if sizes[0].VCPUs != 2 || sizes[0].MemoryMiB != 4096 || sizes[0].HourlyPrice != 0.0088 || sizes[0].Currency != "EUR" || sizes[0].Arch != "amd64" {
		t.Errorf("size = %+v", sizes[0])
	}
	ash, _ := p.Sizes(context.Background(), "ash")
	if len(ash) != 1 || ash[0].HourlyPrice != 0.012 {
		t.Errorf("in ash: %+v", ash)
	}
}

func TestAMachinesStateIsTranslated(t *testing.T) {
	for status, want := range map[string]compute.State{
		"initializing": compute.StatePending, "starting": compute.StatePending, "running": compute.StateRunning,
		"off": compute.StateStopped, "deleting": compute.StateTerminating,
	} {
		if got := stateOf(status); got != want {
			t.Errorf("%s -> %s, want %s", status, got, want)
		}
	}
}

func TestTheProviderRegistersItself(t *testing.T) {
	d, ok := compute.Lookup(ID)
	if !ok || len(d.Fields) != 1 || d.Fields[0].Key != "token" || !d.Fields[0].Secret {
		t.Fatalf("descriptor = %+v", d)
	}
	if _, err := compute.New(ID, compute.Credentials{}, compute.Options{}); err == nil {
		t.Error("a provider was built without a token")
	}
	if _, err := compute.New(ID, compute.Credentials{"token": "t"}, compute.Options{}); err != nil {
		t.Error(err)
	}
}

func controlPlaneFirewall(f *fakeHetzner) int64 {
	f.next++
	f.firewalls[f.next] = map[string]any{"id": f.next, "rules": []map[string]any{
		{"direction": "in", "protocol": "tcp", "port": "22", "source_ips": []string{"198.51.100.1/32"}, "description": "mine"},
	}}
	return f.next
}

func rulesOf(t *testing.T, f *fakeHetzner, id int64) []firewallRule {
	t.Helper()
	raw, _ := json.Marshal(f.firewalls[id]["rules"])
	var rules []firewallRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		t.Fatal(err)
	}
	return rules
}

func TestMachinesAreLetThroughTheControlPlanesFirewallInOneRulePerPort(t *testing.T) {
	f, p := newFakeHetzner(t)
	id := controlPlaneFirewall(f)
	target := compute.AccessTarget{ID: strconv.FormatInt(id, 10)}

	for _, cidr := range []string{"203.0.113.9/32", "203.0.113.10/32"} {
		err := p.AllowAccess(context.Background(), target, []compute.AccessRule{{Protocol: "tcp", Port: 6443, CIDR: cidr}, {Protocol: "udp", Port: 51820, CIDR: cidr}})
		if err != nil {
			t.Fatal(err)
		}
	}
	rules := rulesOf(t, f, id)
	if len(rules) != 3 {
		t.Fatalf("%d rules, want the user's and one per port (a rule per machine would run into the limit): %+v", len(rules), rules)
	}
	if rules[0].Description != "mine" || rules[0].SourceIPs[0] != "198.51.100.1/32" {
		t.Errorf("the user's own rule was changed: %+v", rules[0])
	}
	managed := map[string][]string{}
	for _, r := range rules[1:] {
		if r.Description != accessDescription {
			t.Errorf("rule %+v is not marked as Geass's", r)
		}
		managed[r.Protocol+"/"+r.Port] = r.SourceIPs
	}
	if len(managed["tcp/6443"]) != 2 || len(managed["udp/51820"]) != 2 {
		t.Errorf("managed rules = %v", managed)
	}

	// Allowing the same address again changes nothing and sends nothing.
	before := len(f.requests)
	if err := p.AllowAccess(context.Background(), target, []compute.AccessRule{{Protocol: "tcp", Port: 6443, CIDR: "203.0.113.9/32"}}); err != nil {
		t.Fatal(err)
	}
	for _, request := range f.requests[before:] {
		if strings.Contains(request, "set_rules") {
			t.Error("a rule that was there already was written again")
		}
	}
}

func TestTakingAMachineOutRemovesItsAddressAndTheRuleWhenItIsTheLast(t *testing.T) {
	f, p := newFakeHetzner(t)
	id := controlPlaneFirewall(f)
	target := compute.AccessTarget{ID: strconv.FormatInt(id, 10)}
	rules := func(cidr string) []compute.AccessRule {
		return []compute.AccessRule{{Protocol: "tcp", Port: 6443, CIDR: cidr}}
	}
	_ = p.AllowAccess(context.Background(), target, rules("203.0.113.9/32"))
	_ = p.AllowAccess(context.Background(), target, rules("203.0.113.10/32"))

	if err := p.RevokeAccess(context.Background(), target, rules("203.0.113.9/32")); err != nil {
		t.Fatal(err)
	}
	managed := rulesOf(t, f, id)[1]
	if len(managed.SourceIPs) != 1 || managed.SourceIPs[0] != "203.0.113.10/32" {
		t.Errorf("sources = %v", managed.SourceIPs)
	}
	if err := p.RevokeAccess(context.Background(), target, rules("203.0.113.10/32")); err != nil {
		t.Fatal(err)
	}
	if got := rulesOf(t, f, id); len(got) != 1 || got[0].Description != "mine" {
		t.Errorf("after the last machine went: %+v", got)
	}
	if err := p.RevokeAccess(context.Background(), target, rules("203.0.113.99/32")); err != nil {
		t.Errorf("revoking an address that was never allowed: %v", err)
	}
}
