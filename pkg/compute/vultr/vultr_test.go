package vultr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/degoke/geass/pkg/compute"
)

// fakeVultr is the part of the Vultr API Geass uses, in memory.
type fakeVultr struct {
	t         *testing.T
	srv       *httptest.Server
	key       string
	instances []map[string]any
	groups    []map[string]any
	rules     map[string][]map[string]any
	next      int
	// pageLimit, when set, caps the page size so that listings have to be followed by cursor.
	pageLimit int
	// failCreate, when set, is the status and error message of the next POST /instances.
	failCreate func() (int, string)
	// limitedOnce makes the next request answer 429 once.
	limitedOnce bool
	account     map[string]any
	// osList is what GET /os answers.
	osList   []map[string]any
	requests []string
}

func newFakeVultr(t *testing.T) (*fakeVultr, *Provider) {
	f := &fakeVultr{t: t, key: "key", rules: map[string][]map[string]any{}, next: 100, account: map[string]any{"name": "Geass", "acls": []any{"provisioning", "firewall", "dns"}}}
	f.osList = []map[string]any{
		{"id": 1743, "name": "Ubuntu 22.04 LTS x64", "arch": "x64", "family": "ubuntu"},
		{"id": 2284, "name": "Ubuntu 24.04 LTS x64", "arch": "x64", "family": "ubuntu"},
		{"id": 2136, "name": "Debian 12 x64 (bookworm)", "arch": "x64", "family": "debian"},
	}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f, New("key", compute.Options{BaseURL: f.srv.URL, Region: "fra"})
}

func (f *fakeVultr) reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (f *fakeVultr) fail(w http.ResponseWriter, status int, message string) {
	f.reply(w, status, map[string]any{"error": message, "status": status})
}

// page cuts one page out of items. The cursor is the index of the first item, as text.
func (f *fakeVultr) page(r *http.Request, items []map[string]any) ([]map[string]any, map[string]any) {
	size, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if size <= 0 {
		size = 100
	}
	if f.pageLimit > 0 && f.pageLimit < size {
		size = f.pageLimit
	}
	start, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
	start = min(start, len(items))
	end := min(start+size, len(items))
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return items[start:end], map[string]any{"total": len(items), "links": map[string]any{"next": next, "prev": ""}}
}

func hasTag(i map[string]any, tag string) bool {
	tags, _ := i["tags"].([]any)
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

func (f *fakeVultr) find(id string) (int, map[string]any) {
	for n, i := range f.instances {
		if i["id"] == id {
			return n, i
		}
	}
	return -1, nil
}

func (f *fakeVultr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer "+f.key {
		f.fail(w, 401, "Invalid API token.")
		return
	}
	if f.limitedOnce {
		f.limitedOnce = false
		w.Header().Set("Retry-After", "0")
		f.fail(w, 429, "Rate limit exceeded")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	switch {
	case r.Method == "GET" && path == "account":
		f.reply(w, 200, map[string]any{"account": f.account})
	case r.Method == "GET" && path == "os":
		items, meta := f.page(r, f.osList)
		f.reply(w, 200, map[string]any{"os": items, "meta": meta})
	case r.Method == "GET" && path == "plans":
		if r.URL.Query().Get("type") != "vc2" {
			f.fail(w, 400, "unexpected plan type")
			return
		}
		plans := []map[string]any{
			{"id": "vc2-4c-8gb", "vcpu_count": 4, "ram": 8192, "disk": 160, "monthly_cost": 40.0, "type": "vc2", "locations": []string{"fra", "ewr"}},
			{"id": "vc2-2c-4gb", "vcpu_count": 2, "ram": 4096, "disk": 80, "monthly_cost": 20.0, "type": "vc2", "locations": []string{"fra", "ewr"}},
			{"id": "vc2-1c-1gb", "vcpu_count": 1, "ram": 1024, "disk": 25, "monthly_cost": 5.0, "type": "vc2", "locations": []string{"ewr"}},
		}
		items, meta := f.page(r, plans)
		f.reply(w, 200, map[string]any{"plans": items, "meta": meta})
	case r.Method == "GET" && path == "firewalls":
		items, meta := f.page(r, f.groups)
		f.reply(w, 200, map[string]any{"firewall_groups": items, "meta": meta})
	case r.Method == "POST" && path == "firewalls":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.next++
		group := map[string]any{"id": fmt.Sprintf("fwg-%d", f.next), "description": body["description"], "date_created": fmt.Sprintf("2026-10-19T10:00:%02dZ", f.next%60)}
		f.groups = append(f.groups, group)
		f.reply(w, 201, map[string]any{"firewall_group": group})
	case len(parts) == 3 && parts[0] == "firewalls" && parts[2] == "rules" && r.Method == "GET":
		items, meta := f.page(r, f.rules[parts[1]])
		f.reply(w, 200, map[string]any{"firewall_rules": items, "meta": meta})
	case len(parts) == 3 && parts[0] == "firewalls" && parts[2] == "rules" && r.Method == "POST":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.next++
		body["id"] = f.next
		body["action"] = "accept"
		body["source"] = ""
		f.rules[parts[1]] = append(f.rules[parts[1]], body)
		f.reply(w, 201, map[string]any{"firewall_rule": body})
	case len(parts) == 4 && parts[0] == "firewalls" && parts[2] == "rules" && r.Method == "DELETE":
		id, _ := strconv.Atoi(parts[3])
		kept := f.rules[parts[1]][:0]
		found := false
		for _, rule := range f.rules[parts[1]] {
			if rule["id"] == id {
				found = true
				continue
			}
			kept = append(kept, rule)
		}
		f.rules[parts[1]] = kept
		if !found {
			f.fail(w, 404, "rule not found")
			return
		}
		w.WriteHeader(204)
	case r.Method == "POST" && path == "instances":
		if f.failCreate != nil {
			status, message := f.failCreate()
			f.fail(w, status, message)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.next++
		instance := map[string]any{
			"id": fmt.Sprintf("inst-%d", f.next), "label": body["label"], "hostname": body["hostname"], "region": body["region"], "plan": body["plan"],
			"main_ip": "0.0.0.0", "internal_ip": "", "status": "pending", "power_status": "stopped", "server_status": "none",
			"date_created": "2026-10-19T10:00:00+00:00", "tags": body["tags"], "default_password": "never-shown",
			"firewall_group_id": body["firewall_group_id"], "_request": body,
		}
		f.instances = append(f.instances, instance)
		f.reply(w, 202, map[string]any{"instance": instance})
	case r.Method == "GET" && path == "instances":
		var list []map[string]any
		tag := r.URL.Query().Get("tag")
		for _, i := range f.instances {
			if tag != "" && !hasTag(i, tag) {
				continue
			}
			list = append(list, i)
		}
		items, meta := f.page(r, list)
		f.reply(w, 200, map[string]any{"instances": items, "meta": meta})
	case len(parts) == 2 && parts[0] == "instances":
		n, instance := f.find(parts[1])
		if instance == nil {
			f.fail(w, 404, "Invalid instance ID.")
			return
		}
		if r.Method == "DELETE" {
			f.instances = append(f.instances[:n], f.instances[n+1:]...)
			w.WriteHeader(204)
			return
		}
		f.reply(w, 200, map[string]any{"instance": instance})
	default:
		f.fail(w, 404, "no route "+r.Method+" "+path)
	}
}

func (f *fakeVultr) activate(id string) {
	_, i := f.find(id)
	i["status"] = "active"
	i["power_status"] = "running"
	i["main_ip"] = "203.0.113.50"
	i["internal_ip"] = "10.2.96.3"
}

func spec(name string) compute.InstanceSpec {
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	return compute.InstanceSpec{
		Name: name, Region: "fra", Size: "vc2-2c-4gb", UserData: "#cloud-config\nhostname: x\n", Labels: owner.Labels(name),
		ControlPlaneCIDRs: []string{"203.0.113.5/32"}, MultiSite: true,
	}
}

func request(f *fakeVultr, name string) map[string]any {
	for _, i := range f.instances {
		if i["label"] == name {
			return i["_request"].(map[string]any)
		}
	}
	f.t.Fatalf("no instance labelled %s", name)
	return nil
}

func TestCreateSendsTheMachineTaggedWithBase64UserDataAndItsFirewallGroup(t *testing.T) {
	f, p := newFakeVultr(t)

	got, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "burst-a" || got.PublicIPv4 != "" || got.State != compute.StatePending || got.Ready() {
		t.Errorf("an instance that is still building must be pending with no address, not 0.0.0.0: %+v", got)
	}
	req := request(f, "burst-a")
	if req["region"] != "fra" || req["plan"] != "vc2-2c-4gb" || req["hostname"] != "burst-a" || req["os_id"] != float64(2284) {
		t.Errorf("request = %v", req)
	}
	decoded, err := base64.StdEncoding.DecodeString(req["user_data"].(string))
	if err != nil || string(decoded) != "#cloud-config\nhostname: x\n" {
		t.Errorf("user data must be base64 of the document as given: %q (%v)", req["user_data"], err)
	}
	if req["enable_ipv6"] != false || req["backups"] != "disabled" {
		t.Errorf("request = %v", req)
	}
	tags := make([]string, 0, len(req["tags"].([]any)))
	for _, tag := range req["tags"].([]any) {
		tags = append(tags, tag.(string))
	}
	want := []string{"geass-cluster:c1", "geass-managed:true", "geass-node:burst-a", "geass-pool:burst"}
	if !slices.Equal(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
	if len(f.groups) != 1 || req["firewall_group_id"] != f.groups[0]["id"] {
		t.Errorf("the machine must be created in the cluster's firewall group: %v vs %v", req["firewall_group_id"], f.groups)
	}
	if got.Labels[compute.LabelNode] != "burst-a" || got.Labels[compute.LabelCluster] != "c1" {
		t.Errorf("labels = %v", got.Labels)
	}
}

func TestAMachineIsReadyOnlyWhenActiveRunningAndAddressed(t *testing.T) {
	f, p := newFakeVultr(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	_, i := f.find(created.ID)
	i["status"], i["power_status"] = "active", "running" // up, but the address is still the placeholder
	got, _ := p.Get(context.Background(), created.ID)
	if got.Ready() || got.State != compute.StateRunning || got.PublicIPv4 != "" {
		t.Errorf("running without an address is not ready: %+v", got)
	}
	f.activate(created.ID)
	got, err := p.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Ready() || got.PublicIPv4 != "203.0.113.50" || got.PrivateIPv4 != "10.2.96.3" || got.Region != "fra" || got.Size != "vc2-2c-4gb" || got.CreatedAt.IsZero() {
		t.Errorf("instance = %+v", got)
	}
}

func TestTheImageIsResolvedByNameOnceAndAnUnknownNameIsAnError(t *testing.T) {
	f, p := newFakeVultr(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Create(context.Background(), spec("burst-b")); err != nil {
		t.Fatal(err)
	}
	lookups := 0
	for _, r := range f.requests {
		if r == "GET /os" {
			lookups++
		}
	}
	if lookups != 1 {
		t.Errorf("the OS list was read %d times, want once", lookups)
	}

	named := spec("burst-c")
	named.Image = "debian 12 x64 (bookworm)"
	if _, err := p.Create(context.Background(), named); err != nil {
		t.Fatal(err)
	}
	if request(f, "burst-c")["os_id"] != float64(2136) {
		t.Errorf("a named image was not resolved: %v", request(f, "burst-c"))
	}
	numeric := spec("burst-d")
	numeric.Image = "1743"
	if _, err := p.Create(context.Background(), numeric); err != nil {
		t.Fatal(err)
	}
	if request(f, "burst-d")["os_id"] != float64(1743) {
		t.Errorf("a numeric image was not used as the id: %v", request(f, "burst-d"))
	}
	unknown := spec("burst-e")
	unknown.Image = "Plan 9"
	if _, err := p.Create(context.Background(), unknown); err == nil || !strings.Contains(err.Error(), "Plan 9") {
		t.Errorf("err = %v", err)
	}
}

func TestTheImageNameIsFoundOnALaterPageAndTheDocumentedIDIsTheFallback(t *testing.T) {
	f, p := newFakeVultr(t)
	f.pageLimit = 1
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if request(f, "burst-a")["os_id"] != float64(2284) {
		t.Errorf("os_id = %v", request(f, "burst-a")["os_id"])
	}

	f2, p2 := newFakeVultr(t)
	f2.osList = []map[string]any{{"id": 1, "name": "Something else"}}
	if _, err := p2.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if request(f2, "burst-a")["os_id"] != float64(2284) {
		t.Errorf("the fallback id was not used: %v", request(f2, "burst-a")["os_id"])
	}
}

func TestARegionIsRequiredButTheConnectionsDefaultWillDo(t *testing.T) {
	f, p := newFakeVultr(t)
	s := spec("burst-a")
	s.Region = ""
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if request(f, "burst-a")["region"] != "fra" {
		t.Errorf("the default region was not used: %v", request(f, "burst-a"))
	}
	bare := New("key", compute.Options{BaseURL: f.srv.URL})
	if _, err := bare.Create(context.Background(), s); err == nil || !strings.Contains(err.Error(), "region") {
		t.Errorf("err = %v", err)
	}
}

func TestTheFirewallGroupLetsInOnlyWhatTheClusterNeeds(t *testing.T) {
	f, p := newFakeVultr(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 1 || f.groups[0]["description"] != "geass-c1" {
		t.Fatalf("groups = %v", f.groups)
	}
	got := map[string]bool{}
	for _, r := range f.rules[f.groups[0]["id"].(string)] {
		got[fmt.Sprintf("%s %s/%s %s/%v", r["ip_type"], r["protocol"], r["port"], r["subnet"], r["subnet_size"])] = true
	}
	want := []string{
		"v4 tcp/22 203.0.113.5/32", "v4 tcp/10250 203.0.113.5/32",
		"v4 udp/51820 0.0.0.0/0", "v6 udp/51820 ::/0",
	}
	if len(got) != len(want) {
		t.Errorf("rules = %v, want exactly %v", got, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing rule %q in %v", w, got)
		}
	}
}

func TestWithoutControlPlaneAddressesSSHIsOpenAndTheKubeletIsNot(t *testing.T) {
	f, p := newFakeVultr(t)
	s := spec("burst-a")
	s.ControlPlaneCIDRs = nil
	s.MultiSite = false
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range f.rules[f.groups[0]["id"].(string)] {
		got[fmt.Sprintf("%s %s/%s %s/%v", r["ip_type"], r["protocol"], r["port"], r["subnet"], r["subnet_size"])] = true
	}
	if len(got) != 2 || !got["v4 tcp/22 0.0.0.0/0"] || !got["v6 tcp/22 ::/0"] {
		t.Errorf("rules = %v", got)
	}
}

func TestAddressesAreReadAsNetworksWhetherBareOrWithMaskOrIPv6(t *testing.T) {
	f, p := newFakeVultr(t)
	s := spec("burst-a")
	s.MultiSite = false
	s.ControlPlaneCIDRs = []string{"198.51.100.7", "192.0.2.77/24", "2001:db8::1/64"}
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range f.rules[f.groups[0]["id"].(string)] {
		if r["port"] == "22" {
			got[fmt.Sprintf("%s %s/%v", r["ip_type"], r["subnet"], r["subnet_size"])] = true
		}
	}
	for _, w := range []string{"v4 198.51.100.7/32", "v4 192.0.2.0/24", "v6 2001:db8::/64"} {
		if !got[w] {
			t.Errorf("missing %q in %v", w, got)
		}
	}
	bad := spec("burst-b")
	bad.ControlPlaneCIDRs = []string{"not-an-address"}
	if _, err := p.Create(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "not-an-address") {
		t.Errorf("err = %v", err)
	}
}

func TestTheGroupIsMadeOnceAndItsRulesAreKeptCurrent(t *testing.T) {
	f, p := newFakeVultr(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	id := f.groups[0]["id"].(string)
	// Someone added a rule by hand: it must go.
	f.rules[id] = append(f.rules[id], map[string]any{"id": 9999, "ip_type": "v4", "protocol": "tcp", "port": "3306", "subnet": "0.0.0.0", "subnet_size": 0, "source": "", "action": "accept"})
	before := len(f.requests)
	if _, err := p.Create(context.Background(), spec("burst-b")); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.requests[before:] {
		if strings.HasPrefix(r, "POST /firewalls") && !strings.Contains(r, "rules") {
			t.Errorf("a second group was made: %v", f.requests[before:])
		}
	}
	for _, rule := range f.rules[id] {
		if rule["port"] == "3306" {
			t.Error("a rule Geass did not make was left in the group")
		}
	}

	moved := spec("burst-c")
	moved.ControlPlaneCIDRs = []string{"203.0.113.77/32"} // the control plane moved
	if _, err := p.Create(context.Background(), moved); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 1 {
		t.Fatalf("%d groups, want the one to be reused", len(f.groups))
	}
	raw, _ := json.Marshal(f.rules[id])
	if !strings.Contains(string(raw), "203.0.113.77") || strings.Contains(string(raw), "203.0.113.5\"") {
		t.Errorf("the group still allows the old control plane: %s", raw)
	}
	if len(f.rules[id]) != 4 {
		t.Errorf("%d rules, want 4: %s", len(f.rules[id]), raw)
	}
}

func TestNewRulesAreAddedBeforeOldOnesAreRemoved(t *testing.T) {
	f, p := newFakeVultr(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	before := len(f.requests)
	moved := spec("burst-b")
	moved.ControlPlaneCIDRs = []string{"203.0.113.77/32"}
	if _, err := p.Create(context.Background(), moved); err != nil {
		t.Fatal(err)
	}
	lastAdd, firstRemove := -1, 1<<30
	for i, r := range f.requests[before:] {
		if strings.HasSuffix(r, "/rules") && strings.HasPrefix(r, "POST") {
			lastAdd = i
		}
		if strings.HasPrefix(r, "DELETE") && firstRemove == 1<<30 {
			firstRemove = i
		}
	}
	if lastAdd < 0 || firstRemove == 1<<30 || lastAdd > firstRemove {
		t.Errorf("SSH must stay open while the rules change: %v", f.requests[before:])
	}
}

func TestAGroupTheUserNamesIsUsedAsItIs(t *testing.T) {
	f, p := newFakeVultr(t)
	s := spec("burst-a")
	s.Settings = map[string]string{SettingFirewallGroupID: "fwg-mine"}
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 0 || len(f.rules) != 0 {
		t.Errorf("Geass made or changed a group although one was named: %v %v", f.groups, f.rules)
	}
	if request(f, "burst-a")["firewall_group_id"] != "fwg-mine" {
		t.Errorf("request = %v", request(f, "burst-a"))
	}
}

func TestTheOlderOfTwoGroupsWithTheSameDescriptionIsUsed(t *testing.T) {
	f, p := newFakeVultr(t)
	f.groups = []map[string]any{
		{"id": "new", "description": "geass-c1", "date_created": "2026-10-19T10:05:00Z"},
		{"id": "old", "description": "geass-c1", "date_created": "2026-10-19T10:00:00Z"},
		{"id": "other", "description": "geass-other", "date_created": "2026-10-19T09:00:00Z"},
	}
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if request(f, "burst-a")["firewall_group_id"] != "old" {
		t.Errorf("request = %v", request(f, "burst-a"))
	}
}

func TestACreateThatRepeatsTheNameReturnsTheFirstMachine(t *testing.T) {
	f, p := newFakeVultr(t)
	first, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || len(f.instances) != 1 {
		t.Errorf("a repeated create made a second machine: %s vs %s, %d instances", again.ID, first.ID, len(f.instances))
	}
}

func TestAReplacementForTheSameNodeAndAMachineOfAnotherClusterAreNotTakenForTheFirst(t *testing.T) {
	f, p := newFakeVultr(t)
	first, _ := p.Create(context.Background(), spec("burst-a"))

	retry := spec("burst-a-1")
	retry.Labels[compute.LabelNode] = "burst-a" // the node controller's next attempt
	second, err := p.Create(context.Background(), retry)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Error("the replacement was taken for the machine being removed")
	}

	other := spec("burst-a")
	other.Labels = compute.Owner{Cluster: "someone-else", Pool: "burst"}.Labels("burst-a")
	third, err := p.Create(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == first.ID || len(f.instances) != 3 {
		t.Errorf("a machine of another cluster was taken for ours: %d instances", len(f.instances))
	}
}

func TestProviderFailuresBecomeTheErrorsCallersActOn(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		message string
		check   func(error) bool
	}{
		"limit":    {400, "Server add failed: you have reached the limit of servers for your account.", compute.IsQuota},
		"capacity": {400, "Unable to create instance: this plan is currently out of stock in the selected location", func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }},
		"key":      {401, "Invalid API token.", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"address":  {403, "Unauthorized IP address: 198.51.100.1", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"other": {400, "Invalid plan.", func(err error) bool {
			return compute.IsStatus(err, 400) && !compute.IsQuota(err) && !compute.IsTransient(err)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			f, p := newFakeVultr(t)
			f.failCreate = func() (int, string) { return tc.status, tc.message }
			if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !tc.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestARateLimitedRequestIsRetried(t *testing.T) {
	f, p := newFakeVultr(t)
	f.limitedOnce = true
	if _, err := p.Sizes(context.Background(), "fra"); err != nil {
		t.Fatalf("a single 429 must be waited out: %v", err)
	}
}

func TestListFindsOnlyTheOwnersMachinesAcrossPagesInAnyState(t *testing.T) {
	f, p := newFakeVultr(t)
	f.pageLimit = 2
	for _, name := range []string{"burst-a", "burst-b", "burst-c", "burst-d", "burst-e"} {
		if _, err := p.Create(context.Background(), spec(name)); err != nil {
			t.Fatal(err)
		}
	}
	other := spec("web-a")
	other.Labels = compute.Owner{Cluster: "someone-else", Pool: "burst"}.Labels("web-a")
	if _, err := p.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	otherPool := spec("db-a")
	otherPool.Labels = compute.Owner{Cluster: "c1", Pool: "db"}.Labels("db-a")
	if _, err := p.Create(context.Background(), otherPool); err != nil {
		t.Fatal(err)
	}
	// An instance that is off still costs money, so it is listed.
	for _, inst := range f.instances {
		if inst["label"] == "burst-c" {
			inst["status"], inst["power_status"] = "active", "stopped"
		}
	}

	pool, err := p.List(context.Background(), compute.Owner{Cluster: "c1", Pool: "burst"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pool) != 5 {
		t.Errorf("listed %d machines of the pool, want 5 (all pages): %+v", len(pool), pool)
	}
	stopped := 0
	for _, m := range pool {
		if m.State == compute.StateStopped {
			stopped++
		}
	}
	if stopped != 1 {
		t.Errorf("a stopped machine must still be listed, as stopped: %+v", pool)
	}
	cluster, _ := p.List(context.Background(), compute.Owner{Cluster: "c1"})
	if len(cluster) != 6 {
		t.Errorf("listed %d machines of the cluster, want 6", len(cluster))
	}
	nobody, _ := p.List(context.Background(), compute.Owner{})
	if len(nobody) != 0 {
		t.Errorf("an owner with no cluster was given machines: %+v", nobody)
	}
}

func TestDeleteIsIdempotentRemovesTheMachineInOneCallAndGetReportsAGoneMachine(t *testing.T) {
	f, p := newFakeVultr(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	before := len(f.requests)
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	// An instance's disk and address go with it, and Geass makes nothing else for it.
	if calls := f.requests[before:]; len(calls) != 1 || calls[0] != "DELETE "+"/instances/"+created.ID {
		t.Errorf("Delete made the calls %v, want the one that removes the machine", calls)
	}
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Errorf("deleting again: %v", err)
	}
	if _, err := p.Get(context.Background(), created.ID); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
}

func TestSizesAreCheapestFirstAndOnlyWhereOffered(t *testing.T) {
	_, p := newFakeVultr(t)
	sizes, err := p.Sizes(context.Background(), "fra")
	if err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 2 || sizes[0].ID != "vc2-2c-4gb" || sizes[1].ID != "vc2-4c-8gb" {
		t.Fatalf("sizes in fra = %+v", sizes)
	}
	s := sizes[0]
	if s.VCPUs != 2 || s.MemoryMiB != 4096 || s.DiskGB != 80 || s.Currency != "USD" || s.Arch != "amd64" {
		t.Errorf("size = %+v", s)
	}
	if want := 20.0 / 672; s.HourlyPrice != want {
		t.Errorf("hourly price = %v, want the monthly price over 672 hours: %v", s.HourlyPrice, want)
	}
	ewr, _ := p.Sizes(context.Background(), "ewr")
	if len(ewr) != 3 || ewr[0].ID != "vc2-1c-1gb" {
		t.Errorf("sizes in ewr = %+v", ewr)
	}
}

func TestSizesFollowTheCursor(t *testing.T) {
	f, p := newFakeVultr(t)
	f.pageLimit = 1
	sizes, err := p.Sizes(context.Background(), "ewr")
	if err != nil || len(sizes) != 3 {
		t.Errorf("sizes = %+v, %v", sizes, err)
	}
}

func TestAMachinesStateIsTranslated(t *testing.T) {
	for _, tc := range []struct {
		status, power string
		want          compute.State
	}{
		{"pending", "stopped", compute.StatePending},
		{"pending", "running", compute.StatePending},
		{"active", "running", compute.StateRunning},
		{"active", "stopped", compute.StateStopped},
		{"suspended", "stopped", compute.StateStopped},
		{"resizing", "running", compute.StatePending},
		{"active", "", compute.StatePending},
	} {
		if got := stateOf(tc.status, tc.power); got != tc.want {
			t.Errorf("%s/%s -> %s, want %s", tc.status, tc.power, got, tc.want)
		}
	}
}

func TestVerifyReadsTheAccountAndWarnsAboutMissingPermissions(t *testing.T) {
	f, p := newFakeVultr(t)
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.Name != "Geass" {
		t.Errorf("account = %+v", account)
	}
	if len(account.Warnings) != 1 || !strings.Contains(account.Warnings[0], "access control") {
		t.Errorf("warnings = %v: only the note about the key's address limits is expected", account.Warnings)
	}

	f.account = map[string]any{"email": "me@example.com", "acls": []any{"dns"}}
	account, _ = p.Verify(context.Background())
	joined := strings.Join(account.Warnings, "\n")
	if account.Name != "me@example.com" || !strings.Contains(joined, "provisioning and firewall") {
		t.Errorf("account = %+v", account)
	}

	f.account = map[string]any{"name": "x"}
	account, _ = p.Verify(context.Background())
	if !strings.Contains(strings.Join(account.Warnings, "\n"), "cannot tell") {
		t.Errorf("warnings = %v", account.Warnings)
	}
	for _, r := range f.requests {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("Verify changed something: %v", f.requests)
		}
	}
}

func TestABadKeyIsRefused(t *testing.T) {
	f, _ := newFakeVultr(t)
	p := New("wrong", compute.Options{BaseURL: f.srv.URL})
	if _, err := p.Verify(context.Background()); !errors.Is(err, compute.ErrAuth) {
		t.Errorf("Verify = %v", err)
	}
}

func TestTheImagesFirewallIsSwitchedOffAtFirstBoot(t *testing.T) {
	_, p := newFakeVultr(t)
	var customizer compute.CloudInitCustomizer = p
	init := compute.CloudInit{AuthorizedKeys: []string{"ssh-ed25519 AAAA"}, RunCommands: []string{"echo before"}}
	customizer.CustomizeCloudInit(&init, spec("burst-a"))
	if len(init.RunCommands) != 2 || init.RunCommands[0] != "echo before" || !strings.Contains(init.RunCommands[1], "ufw --force disable") {
		t.Errorf("run commands = %v", init.RunCommands)
	}
	if !strings.Contains(init.Render(), "ufw --force disable") {
		t.Errorf("the command is not in the document:\n%s", init.Render())
	}
}

func TestTheProviderRegistersItself(t *testing.T) {
	d, ok := compute.Lookup(ID)
	if !ok || d.ID != "Vultr" || len(d.Fields) != 1 || d.Fields[0].Key != "apiKey" || !d.Fields[0].Secret {
		t.Fatalf("descriptor = %+v", d)
	}
	if !d.KnownSetting(SettingFirewallGroupID) || d.RegionHelp == "" || d.Summary == "" {
		t.Errorf("descriptor = %+v", d)
	}
	if _, err := compute.New(ID, compute.Credentials{}, compute.Options{}); err == nil {
		t.Error("a provider was built without a key")
	}
	built, err := compute.New(ID, compute.Credentials{"apiKey": "k"}, compute.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := built.(compute.CloudInitCustomizer); !ok {
		t.Error("the provider does not customise cloud-init")
	}
}

func TestRulesAreComparedWhateverTheCaseOrSpellingOfTheirAddresses(t *testing.T) {
	a := firewallRule{IPType: "v6", Protocol: "TCP", Port: "22", Subnet: "2001:0DB8:0:0:0:0:0:0", SubnetSize: 64}
	b := firewallRule{Type: "v6", Protocol: "tcp", Port: "22", Subnet: "2001:db8::", SubnetSize: 64}
	ka, _ := a.key()
	kb, _ := b.key()
	if ka != kb {
		t.Errorf("%+v != %+v", ka, kb)
	}
}
