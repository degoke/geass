package digitalocean

import (
	"context"
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

// fakeDO is the part of the DigitalOcean API Geass uses, in memory.
type fakeDO struct {
	t         *testing.T
	srv       *httptest.Server
	token     string
	droplets  map[int64]map[string]any
	firewalls map[string]map[string]any
	tags      map[string]bool
	next      int64
	// pageLimit, when set, caps the page size so that listings have to be followed.
	pageLimit int
	// failCreate, when set, is the status and error body of the next POST /droplets.
	failCreate func() (int, string, string)
	// tagCreate, when set, answers POST /tags in place of 201.
	tagCreate func(w http.ResponseWriter)
	// limitedOnce makes the next request answer 429 once.
	limitedOnce bool
	requests    []string
	account     map[string]any
}

func newFakeDO(t *testing.T) (*fakeDO, *Provider) {
	f := &fakeDO{t: t, token: "tok", droplets: map[int64]map[string]any{}, firewalls: map[string]map[string]any{}, tags: map[string]bool{}, next: 100}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f, New("tok", compute.Options{BaseURL: f.srv.URL})
}

func (f *fakeDO) reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (f *fakeDO) fail(w http.ResponseWriter, status int, id, message string) {
	f.reply(w, status, map[string]any{"id": id, "message": message})
}

// page returns the items of one page and the links object that points at the next.
func (f *fakeDO) page(r *http.Request, items []map[string]any) ([]map[string]any, map[string]any) {
	size, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if size <= 0 {
		size = 20
	}
	if f.pageLimit > 0 && f.pageLimit < size {
		size = f.pageLimit
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	start := min((page-1)*size, len(items))
	end := min(start+size, len(items))
	links := map[string]any{}
	if end < len(items) {
		links["pages"] = map[string]any{"next": fmt.Sprintf("%s%s?page=%d&per_page=%d", f.srv.URL, r.URL.Path, page+1, size)}
	} else {
		links["pages"] = map[string]any{}
	}
	return items[start:end], links
}

func sortedByID[V any](m map[int64]V) []V {
	ids := make([]int64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]V, 0, len(ids))
	for _, id := range ids {
		out = append(out, m[id])
	}
	return out
}

func (f *fakeDO) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		f.fail(w, 401, "unauthorized", "Unable to authenticate you.")
		return
	}
	if f.limitedOnce {
		f.limitedOnce = false
		w.Header().Set("Retry-After", "0")
		f.fail(w, 429, "too_many_requests", "API rate limit exceeded.")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	switch {
	case r.Method == "GET" && path == "account":
		if f.account == nil {
			f.fail(w, 403, "forbidden", "You do not have access for the attempted action.")
			return
		}
		f.reply(w, 200, map[string]any{"account": f.account})
	case r.Method == "GET" && path == "sizes":
		sizes := []map[string]any{
			{"slug": "s-4vcpu-8gb", "memory": 8192, "vcpus": 4, "disk": 160, "price_hourly": 0.05952, "regions": []string{"fra1", "nyc3"}, "available": true, "description": "Basic"},
			{"slug": "s-2vcpu-4gb", "memory": 4096, "vcpus": 2, "disk": 80, "price_hourly": 0.02679, "regions": []string{"fra1", "nyc3"}, "available": true, "description": "Basic"},
			{"slug": "s-1vcpu-1gb", "memory": 1024, "vcpus": 1, "disk": 25, "price_hourly": 0.00744, "regions": []string{"nyc3"}, "available": true, "description": "Basic"},
			{"slug": "s-2vcpu-4gb-old", "memory": 4096, "vcpus": 2, "disk": 80, "price_hourly": 0.01, "regions": []string{"fra1"}, "available": false},
			{"slug": "gpu-h100x1-80gb", "memory": 245760, "vcpus": 20, "disk": 720, "price_hourly": 3.39, "regions": []string{"fra1"}, "available": true, "gpu_info": map[string]any{"count": 1}},
		}
		items, links := f.page(r, sizes)
		f.reply(w, 200, map[string]any{"sizes": items, "links": links, "meta": map[string]any{"total": len(sizes)}})
	case r.Method == "POST" && path == "tags":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if f.tagCreate != nil {
			f.tagCreate(w)
			return
		}
		f.tags[body["name"].(string)] = true
		f.reply(w, 201, map[string]any{"tag": body})
	case r.Method == "GET" && path == "firewalls":
		var list []map[string]any
		ids := make([]string, 0, len(f.firewalls))
		for id := range f.firewalls {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			list = append(list, f.firewalls[id])
		}
		items, links := f.page(r, list)
		f.reply(w, 200, map[string]any{"firewalls": items, "links": links})
	case r.Method == "POST" && path == "firewalls":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, tag := range body["tags"].([]any) {
			if !f.tags[tag.(string)] {
				f.fail(w, 422, "unprocessable_entity", "tag "+tag.(string)+" does not exist")
				return
			}
		}
		f.next++
		id := fmt.Sprintf("fw-%d", f.next)
		body["id"] = id
		body["droplet_ids"] = []any{}
		f.firewalls[id] = body
		f.reply(w, 202, map[string]any{"firewall": body})
	case r.Method == "PUT" && strings.HasPrefix(path, "firewalls/"):
		id := strings.TrimPrefix(path, "firewalls/")
		if _, ok := f.firewalls[id]; !ok {
			f.fail(w, 404, "not_found", "firewall not found")
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["id"] = id
		f.firewalls[id] = body
		f.reply(w, 200, map[string]any{"firewall": body})
	case r.Method == "POST" && strings.HasPrefix(path, "firewalls/") && strings.HasSuffix(path, "/droplets"):
		id := strings.Split(path, "/")[1]
		fw, ok := f.firewalls[id]
		if !ok {
			f.fail(w, 404, "not_found", "firewall not found")
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		fw["droplet_ids"] = append(fw["droplet_ids"].([]any), body["droplet_ids"].([]any)...)
		w.WriteHeader(204)
	case r.Method == "POST" && path == "droplets":
		if f.failCreate != nil {
			status, id, message := f.failCreate()
			f.fail(w, status, id, message)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.next++
		droplet := map[string]any{
			"id": f.next, "name": body["name"], "status": "new", "created_at": "2026-10-19T10:00:00Z",
			"size_slug": body["size"], "tags": body["tags"], "region": map[string]any{"slug": body["region"]},
			"networks": map[string]any{"v4": []any{}, "v6": []any{}},
			"_request": body,
		}
		f.droplets[f.next] = droplet
		f.reply(w, 202, map[string]any{"droplet": droplet})
	case r.Method == "GET" && path == "droplets":
		var list []map[string]any
		tag := r.URL.Query().Get("tag_name")
		for _, d := range sortedByID(f.droplets) {
			if tag != "" && !hasTag(d, tag) {
				continue
			}
			list = append(list, d)
		}
		items, links := f.page(r, list)
		f.reply(w, 200, map[string]any{"droplets": items, "links": links, "meta": map[string]any{"total": len(list)}})
	case strings.HasPrefix(path, "droplets/"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(path, "droplets/"), 10, 64)
		droplet, ok := f.droplets[id]
		if !ok {
			f.fail(w, 404, "not_found", "The resource you requested could not be found.")
			return
		}
		if r.Method == "DELETE" {
			delete(f.droplets, id)
			w.WriteHeader(204)
			return
		}
		f.reply(w, 200, map[string]any{"droplet": droplet})
	default:
		f.fail(w, 404, "not_found", "no route "+r.Method+" "+path)
	}
}

func hasTag(d map[string]any, tag string) bool {
	tags, _ := d["tags"].([]any)
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

// activate gives the droplet an address and makes it active, the way DigitalOcean does a little after the 202.
func (f *fakeDO) activate(id string) {
	n, _ := strconv.ParseInt(id, 10, 64)
	d := f.droplets[n]
	d["status"] = "active"
	d["networks"] = map[string]any{"v4": []any{
		map[string]any{"ip_address": "10.116.0.2", "type": "private"},
		map[string]any{"ip_address": "203.0.113.50", "type": "public"},
	}}
}

func spec(name string) compute.InstanceSpec {
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	return compute.InstanceSpec{
		Name: name, Region: "fra1", Size: "s-2vcpu-4gb", UserData: "#cloud-config\nhostname: x\n", Labels: owner.Labels(name),
		ControlPlaneCIDRs: []string{"203.0.113.5/32"}, MultiSite: true,
	}
}

func sent(f *fakeDO, name string) map[string]any {
	for _, d := range f.droplets {
		if d["name"] == name {
			return d["_request"].(map[string]any)
		}
	}
	f.t.Fatalf("no droplet named %s", name)
	return nil
}

func TestCreateSendsTheMachineTaggedWithItsUserDataBeforeAnyAddressIsKnown(t *testing.T) {
	f, p := newFakeDO(t)

	instance, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if instance.Name != "burst-a" || instance.PublicIPv4 != "" || instance.State != compute.StatePending || instance.Ready() {
		t.Errorf("a droplet that has no address yet must be pending with an empty address: %+v", instance)
	}
	req := sent(f, "burst-a")
	if req["size"] != "s-2vcpu-4gb" || req["region"] != "fra1" || req["image"] != "ubuntu-24-04-x64" || req["ipv6"] != false {
		t.Errorf("request = %v", req)
	}
	if req["user_data"] != "#cloud-config\nhostname: x\n" {
		t.Errorf("user data must reach the machine as given, in plain text: %q", req["user_data"])
	}
	tags := make([]string, 0, len(req["tags"].([]any)))
	for _, tag := range req["tags"].([]any) {
		tags = append(tags, tag.(string))
	}
	want := []string{"geass-cluster:c1", "geass-managed:true", "geass-node:burst-a", "geass-pool:burst"}
	if !slices.Equal(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
	if instance.Labels[compute.LabelNode] != "burst-a" {
		t.Errorf("labels = %v", instance.Labels)
	}
}

func TestAnAddressAndAnActiveStatusMakeTheMachineReady(t *testing.T) {
	f, p := newFakeDO(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	f.activate(created.ID)
	got, err := p.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Ready() || got.PublicIPv4 != "203.0.113.50" || got.PrivateIPv4 != "10.116.0.2" || got.Region != "fra1" || got.Size != "s-2vcpu-4gb" {
		t.Errorf("instance = %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("the creation time was not read")
	}
}

func TestCreateOptionsAreSentWhenGiven(t *testing.T) {
	f, p := newFakeDO(t)
	s := spec("burst-a")
	s.Image = "debian-12-x64"
	s.Settings = map[string]string{SettingVPCID: "vpc-uuid-1"}
	s.Region = ""
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	req := sent(f, "burst-a")
	if req["image"] != "debian-12-x64" || req["vpc_uuid"] != "vpc-uuid-1" {
		t.Errorf("request = %v", req)
	}
	if _, set := req["region"]; set {
		t.Errorf("an empty region must be left out so that DigitalOcean chooses: %v", req)
	}
}

func TestTheFirewallExistsBeforeTheMachineAndLetsInOnlyWhatTheClusterNeeds(t *testing.T) {
	f, p := newFakeDO(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	firewallAt, dropletAt := -1, -1
	for i, r := range f.requests {
		switch r {
		case "POST /firewalls":
			firewallAt = i
		case "POST /droplets":
			dropletAt = i
		}
	}
	if firewallAt < 0 || dropletAt < 0 || firewallAt > dropletAt {
		t.Errorf("the firewall must be made before the droplet: %v", f.requests)
	}
	if len(f.firewalls) != 1 {
		t.Fatalf("%d firewalls, want 1", len(f.firewalls))
	}
	var fw map[string]any
	for _, one := range f.firewalls {
		fw = one
	}
	if fw["name"] != "geass-c1" || fmt.Sprint(fw["tags"]) != "[geass-cluster:c1]" {
		t.Errorf("firewall = %v: it must be named for the cluster and target the cluster's tag", fw)
	}
	in := map[string]string{}
	for _, r := range fw["inbound_rules"].([]any) {
		rule := r.(map[string]any)
		sources := rule["sources"].(map[string]any)
		addresses := []string{}
		for _, a := range sources["addresses"].([]any) {
			addresses = append(addresses, a.(string))
		}
		in[rule["protocol"].(string)+"/"+rule["ports"].(string)] = strings.Join(addresses, ",")
	}
	if in["tcp/22"] != "203.0.113.5/32" || in["tcp/10250"] != "203.0.113.5/32" {
		t.Errorf("SSH and the kubelet must come from the control plane only: %v", in)
	}
	if in["udp/51820"] != "0.0.0.0/0,::/0" {
		t.Errorf("the overlay port must be open in a multi-site cluster: %v", in)
	}
	if len(in) != 3 {
		t.Errorf("inbound = %v, want exactly three rules", in)
	}
	out := fw["outbound_rules"].([]any)
	if len(out) != 2 {
		t.Errorf("without outbound rules DigitalOcean drops all outbound traffic: %v", out)
	}
}

func TestWithoutControlPlaneAddressesSSHIsOpenAndTheKubeletIsNot(t *testing.T) {
	f, p := newFakeDO(t)
	s := spec("burst-a")
	s.ControlPlaneCIDRs = nil
	s.MultiSite = false
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	for _, fw := range f.firewalls {
		rules := fw["inbound_rules"].([]any)
		if len(rules) != 1 {
			t.Fatalf("rules = %v", rules)
		}
		rule := rules[0].(map[string]any)
		if rule["ports"] != "22" || fmt.Sprint(rule["sources"].(map[string]any)["addresses"]) != "[0.0.0.0/0 ::/0]" {
			t.Errorf("rule = %v", rule)
		}
	}
}

func TestTheFirewallIsMadeOnceAndItsRulesAreKeptCurrentWithoutLosingWhatIsAttached(t *testing.T) {
	f, p := newFakeDO(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	for _, fw := range f.firewalls {
		fw["droplet_ids"] = []any{float64(7)} // something attached by hand
	}
	// The same rules again change nothing.
	if _, err := p.Create(context.Background(), spec("burst-b")); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.requests {
		if strings.HasPrefix(r, "PUT ") {
			t.Fatalf("a firewall with the right rules was rewritten: %v", f.requests)
		}
	}
	next := spec("burst-c")
	next.ControlPlaneCIDRs = []string{"203.0.113.77/32"} // the control plane moved
	if _, err := p.Create(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if len(f.firewalls) != 1 {
		t.Fatalf("%d firewalls, want the one to be reused", len(f.firewalls))
	}
	for _, fw := range f.firewalls {
		raw, _ := json.Marshal(fw["inbound_rules"])
		if !strings.Contains(string(raw), "203.0.113.77/32") || strings.Contains(string(raw), "203.0.113.5/32") {
			t.Errorf("the firewall still allows the old control plane: %s", raw)
		}
		if fmt.Sprint(fw["droplet_ids"]) != "[7]" || fmt.Sprint(fw["tags"]) != "[geass-cluster:c1]" {
			t.Errorf("updating the rules dropped what was attached: %v", fw)
		}
	}
}

func TestAFirewallTheUserNamesIsUsedAsItIsAndEachMachineIsAttachedToIt(t *testing.T) {
	f, p := newFakeDO(t)
	f.firewalls["fw-mine"] = map[string]any{"id": "fw-mine", "name": "mine", "droplet_ids": []any{}, "inbound_rules": []any{}}
	s := spec("burst-a")
	s.Settings = map[string]string{SettingFirewallID: "fw-mine"}
	instance, err := p.Create(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.firewalls) != 1 || len(f.firewalls["fw-mine"]["inbound_rules"].([]any)) != 0 {
		t.Errorf("Geass made or changed a firewall although one was named: %v", f.firewalls)
	}
	if fmt.Sprint(f.firewalls["fw-mine"]["droplet_ids"]) != "["+instance.ID+"]" {
		t.Errorf("the machine was not attached to the named firewall: %v", f.firewalls["fw-mine"]["droplet_ids"])
	}
	if f.tags["geass-cluster:c1"] {
		t.Error("a tag was made for a firewall that is not Geass's")
	}
}

func TestAFirewallWithOurNameThatIsNotGeassIsAnErrorNotAnAdoption(t *testing.T) {
	f, p := newFakeDO(t)
	f.firewalls["fw-other"] = map[string]any{"id": "fw-other", "name": "geass-c1", "tags": []any{"web"}, "inbound_rules": []any{}}
	if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !strings.Contains(err.Error(), SettingFirewallID) {
		t.Fatalf("err = %v", err)
	}
	if len(f.droplets) != 0 {
		t.Error("a machine was made behind a firewall that is not ours")
	}
}

func TestATagThatAlreadyExistsIsNotAFailure(t *testing.T) {
	f, p := newFakeDO(t)
	f.tags["geass-cluster:c1"] = true
	f.tagCreate = func(w http.ResponseWriter) {
		f.fail(w, 422, "unprocessable_entity", "tag name already exists")
	}
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if len(f.firewalls) != 1 {
		t.Errorf("%d firewalls", len(f.firewalls))
	}
}

func TestACreateThatRepeatsTheNameReturnsTheFirstMachine(t *testing.T) {
	f, p := newFakeDO(t)
	first, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || len(f.droplets) != 1 {
		t.Errorf("a repeated create made a second machine: %s vs %s, %d droplets", again.ID, first.ID, len(f.droplets))
	}
}

func TestAReplacementMachineForTheSameNodeIsNotTakenForTheOneBeingRemoved(t *testing.T) {
	f, p := newFakeDO(t)
	first, _ := p.Create(context.Background(), spec("burst-a"))
	// The node controller names the next attempt burst-a-1 but labels it with the same node.
	retry := spec("burst-a-1")
	retry.Labels[compute.LabelNode] = "burst-a"
	second, err := p.Create(context.Background(), retry)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || len(f.droplets) != 2 {
		t.Errorf("the replacement was not made: %s vs %s, %d droplets", second.ID, first.ID, len(f.droplets))
	}
}

func TestAMachineOfAnotherClusterWithTheSameNameIsNotAdopted(t *testing.T) {
	f, p := newFakeDO(t)
	other := spec("burst-a")
	other.Labels = compute.Owner{Cluster: "someone-else", Pool: "burst"}.Labels("burst-a")
	first, err := p.Create(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	ours, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if ours.ID == first.ID || len(f.droplets) != 2 {
		t.Errorf("a machine of another cluster was taken for ours: %d droplets", len(f.droplets))
	}
}

func TestAGoneMachineWithTheSameNameDoesNotBlockANewOne(t *testing.T) {
	f, p := newFakeDO(t)
	first, _ := p.Create(context.Background(), spec("burst-a"))
	n, _ := strconv.ParseInt(first.ID, 10, 64)
	f.droplets[n]["status"] = "archive"
	second, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Error("an archived droplet was returned as if it were alive")
	}
}

func TestProviderFailuresBecomeTheErrorsCallersActOn(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		id      string
		message string
		check   func(error) bool
	}{
		"limit":     {403, "forbidden", "You have reached your droplet limit.", compute.IsQuota},
		"limit 422": {422, "unprocessable_entity", "creating these droplets will exceed your droplet limit", compute.IsQuota},
		"capacity":  {422, "unprocessable_entity", "Droplet creation failed: size is temporarily unavailable in this region", func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }},
		"token":     {403, "forbidden", "You do not have access for the attempted action.", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"bad token": {401, "unauthorized", "Unable to authenticate you.", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"validation": {422, "unprocessable_entity", "image does not exist", func(err error) bool {
			return compute.IsStatus(err, 422) && !compute.IsQuota(err) && !compute.IsTransient(err)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			f, p := newFakeDO(t)
			f.failCreate = func() (int, string, string) { return tc.status, tc.id, tc.message }
			if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !tc.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestARateLimitedRequestIsRetried(t *testing.T) {
	f, p := newFakeDO(t)
	f.limitedOnce = true
	if _, err := p.Sizes(context.Background(), "fra1"); err != nil {
		t.Fatalf("a single 429 must be waited out: %v", err)
	}
}

func TestListFindsOnlyTheOwnersMachinesAcrossPages(t *testing.T) {
	f, p := newFakeDO(t)
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
	pool, err := p.List(context.Background(), compute.Owner{Cluster: "c1", Pool: "burst"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pool) != 5 {
		t.Errorf("listed %d machines of the pool, want 5 (all pages): %+v", len(pool), pool)
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

func TestListShowsAMachineInWhateverStateItIsIn(t *testing.T) {
	f, p := newFakeDO(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	n, _ := strconv.ParseInt(created.ID, 10, 64)
	f.droplets[n]["status"] = "off"
	got, err := p.List(context.Background(), compute.Owner{Cluster: "c1", Pool: "burst"})
	if err != nil || len(got) != 1 || got[0].State != compute.StateStopped {
		t.Errorf("List = %+v, %v", got, err)
	}
}

func TestDeleteIsIdempotentRemovesTheMachineInOneCallAndGetReportsAGoneMachine(t *testing.T) {
	f, p := newFakeDO(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	before := len(f.requests)
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	// A Droplet's disk goes with it, and Geass makes no snapshot, backup, volume or reserved address.
	if calls := f.requests[before:]; len(calls) != 1 || calls[0] != "DELETE "+"/droplets/"+created.ID {
		t.Errorf("Delete made the calls %v, want the one that removes the machine", calls)
	}
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Errorf("deleting again: %v", err)
	}
	if _, err := p.Get(context.Background(), created.ID); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
}

func TestSizesAreCheapestFirstWithoutUnavailableOrGPUOnesAndOnlyWhereOffered(t *testing.T) {
	_, p := newFakeDO(t)
	sizes, err := p.Sizes(context.Background(), "fra1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 2 || sizes[0].ID != "s-2vcpu-4gb" || sizes[1].ID != "s-4vcpu-8gb" {
		t.Fatalf("sizes in fra1 = %+v", sizes)
	}
	s := sizes[0]
	if s.VCPUs != 2 || s.MemoryMiB != 4096 || s.DiskGB != 80 || s.HourlyPrice != 0.02679 || s.Currency != "USD" || s.Arch != "amd64" {
		t.Errorf("size = %+v", s)
	}
	nyc, _ := p.Sizes(context.Background(), "nyc3")
	if len(nyc) != 3 || nyc[0].ID != "s-1vcpu-1gb" {
		t.Errorf("sizes in nyc3 = %+v", nyc)
	}
	all, _ := p.Sizes(context.Background(), "")
	if len(all) != 3 {
		t.Errorf("sizes without a region = %+v", all)
	}
}

func TestSizesFollowThePagesOfTheListing(t *testing.T) {
	f, p := newFakeDO(t)
	f.pageLimit = 1
	sizes, err := p.Sizes(context.Background(), "nyc3")
	if err != nil || len(sizes) != 3 {
		t.Errorf("sizes = %+v, %v", sizes, err)
	}
}

func TestAMachinesStateIsTranslated(t *testing.T) {
	for status, want := range map[string]compute.State{
		"new": compute.StatePending, "active": compute.StateRunning, "off": compute.StateStopped, "archive": compute.StateGone,
		"something-new": compute.StatePending,
	} {
		if got := stateOf(status); got != want {
			t.Errorf("%s -> %s, want %s", status, got, want)
		}
	}
}

func TestVerifyIsAReadAndSaysWhatItCannotCheck(t *testing.T) {
	f, p := newFakeDO(t)
	f.account = map[string]any{"droplet_limit": 3, "email": "me@example.com", "status": "active", "team": map[string]any{"name": "Geass team"}}
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.Name != "Geass team" || len(account.Warnings) != 1 || !strings.Contains(account.Warnings[0], "scopes") {
		t.Errorf("account = %+v", account)
	}
	for _, r := range f.requests {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("Verify changed something: %v", f.requests)
		}
	}
}

func TestVerifyWarnsWhenTheDropletLimitIsReachedOrTheAccountIsNotActive(t *testing.T) {
	f, p := newFakeDO(t)
	f.account = map[string]any{"droplet_limit": 1, "status": "locked", "status_message": "payment failed"}
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(account.Warnings, "\n")
	if !strings.Contains(joined, "limit is 1") || !strings.Contains(joined, "locked: payment failed") {
		t.Errorf("warnings = %v", account.Warnings)
	}
}

func TestVerifyStillWorksWhenTheTokenCannotReadTheAccount(t *testing.T) {
	_, p := newFakeDO(t)
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.Name != "DigitalOcean account" || !strings.Contains(strings.Join(account.Warnings, " "), "account:read") {
		t.Errorf("account = %+v", account)
	}
}

func TestABadTokenIsRefused(t *testing.T) {
	f, _ := newFakeDO(t)
	p := New("wrong", compute.Options{BaseURL: f.srv.URL})
	if _, err := p.Verify(context.Background()); !errors.Is(err, compute.ErrAuth) {
		t.Errorf("Verify = %v", err)
	}
}

func TestTheProviderRegistersItself(t *testing.T) {
	d, ok := compute.Lookup(ID)
	if !ok || d.ID != "DigitalOcean" || len(d.Fields) != 1 || d.Fields[0].Key != "token" || !d.Fields[0].Secret {
		t.Fatalf("descriptor = %+v", d)
	}
	if !d.KnownSetting(SettingFirewallID) || !d.KnownSetting(SettingVPCID) || d.RegionHelp == "" || d.Summary == "" {
		t.Errorf("descriptor = %+v", d)
	}
	if _, err := compute.New(ID, compute.Credentials{}, compute.Options{}); err == nil {
		t.Error("a provider was built without a token")
	}
	if _, err := compute.New(ID, compute.Credentials{"token": "t"}, compute.Options{}); err != nil {
		t.Error(err)
	}
}
