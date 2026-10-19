package linode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/degoke/geass/pkg/compute"
)

// validLabel is Linode's own rule for a Linode or firewall label: a letter first, then letters, digits and single
// hyphens, underscores or dots. (The API's pattern uses a look-ahead, which Go's regexp does not have.)
func validLabel(s string) bool {
	if len(s) < 2 || (s[0] < 'A' || s[0] > 'z' || (s[0] > 'Z' && s[0] < 'a')) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.':
			if s[i-1] == c {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// fakeLinode is the part of the Linode API Geass uses, in memory.
type fakeLinode struct {
	t         *testing.T
	srv       *httptest.Server
	token     string
	linodes   []map[string]any
	firewalls []map[string]any
	regions   map[string][]string
	next      int
	// pageLimit, when set, caps the page size so that listings have to be followed.
	pageLimit int
	// failCreate, when set, is the status and error body of the next POST /linode/instances.
	failCreate func() (int, map[string]any)
	// limitedOnce makes the next request answer 429 once.
	limitedOnce bool
	// availability is the answer to a region's availability list; nil makes the request fail.
	availability []map[string]any
	// filters records the X-Filter header of each list request.
	filters  []string
	requests []string
}

func newFakeLinode(t *testing.T) (*fakeLinode, *Provider) {
	f := &fakeLinode{t: t, token: "tok", next: 1000, regions: map[string][]string{
		"eu-central": {"Linodes", "Metadata", "Block Storage"},
		"us-east":    {"Linodes", "Metadata"},
		"us-old":     {"Linodes"},
	}}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f, New("tok", compute.Options{BaseURL: f.srv.URL, Region: "eu-central"})
}

func (f *fakeLinode) reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (f *fakeLinode) fail(w http.ResponseWriter, status int, field, reason string) {
	e := map[string]any{"reason": reason}
	if field != "" {
		e["field"] = field
	}
	f.reply(w, status, map[string]any{"errors": []any{e}})
}

// page cuts one page out of items and wraps it in Linode's envelope.
func (f *fakeLinode) page(r *http.Request, items []map[string]any) map[string]any {
	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if size <= 0 {
		size = 100
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
	pages := (len(items) + size - 1) / size
	if pages == 0 {
		pages = 1
	}
	if items == nil {
		items = []map[string]any{}
	}
	return map[string]any{"data": items[start:end], "page": page, "pages": pages, "results": len(items)}
}

func tagsOf(m map[string]any) []string {
	out := make([]string, 0, len(m["tags"].([]any)))
	for _, t := range m["tags"].([]any) {
		out = append(out, t.(string))
	}
	return out
}

func (f *fakeLinode) filter(r *http.Request) map[string]string {
	raw := r.Header.Get("X-Filter")
	f.filters = append(f.filters, raw)
	out := map[string]string{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (f *fakeLinode) findLinode(id string) (int, map[string]any) {
	for n, l := range f.linodes {
		if fmt.Sprint(l["id"]) == id {
			return n, l
		}
	}
	return -1, nil
}

func (f *fakeLinode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	public := r.Method == "GET" && (path == "linode/types" || strings.HasPrefix(path, "regions/"))
	if !public && r.Header.Get("Authorization") != "Bearer "+f.token {
		f.fail(w, 401, "", "Invalid Token")
		return
	}
	if f.limitedOnce {
		f.limitedOnce = false
		w.Header().Set("Retry-After", "0")
		f.fail(w, 429, "", "Too many requests")
		return
	}
	switch {
	case r.Method == "GET" && len(parts) == 2 && parts[0] == "regions":
		caps, ok := f.regions[parts[1]]
		if !ok {
			f.fail(w, 404, "", "Not found")
			return
		}
		f.reply(w, 200, map[string]any{"id": parts[1], "capabilities": caps, "status": "ok"})
	case r.Method == "GET" && len(parts) == 3 && parts[0] == "regions" && parts[2] == "availability":
		if _, ok := f.regions[parts[1]]; !ok {
			f.fail(w, 404, "", "Not found")
			return
		}
		if f.availability == nil {
			f.fail(w, 500, "", "availability is down")
			return
		}
		f.reply(w, 200, f.availability)
	case r.Method == "GET" && path == "linode/types":
		types := []map[string]any{
			{"id": "g6-standard-4", "label": "Linode 8GB", "vcpus": 4, "memory": 8192, "disk": 163840, "price": map[string]any{"hourly": 0.06, "monthly": 40.0}, "region_prices": []any{}},
			{"id": "g6-standard-2", "label": "Linode 4GB", "vcpus": 2, "memory": 4096, "disk": 81920, "price": map[string]any{"hourly": 0.03, "monthly": 20.0},
				"region_prices": []any{map[string]any{"id": "eu-central", "hourly": 0.036, "monthly": 24.0}}},
			{"id": "g6-nanode-1", "label": "Nanode 1GB", "vcpus": 1, "memory": 1024, "disk": 25600, "price": map[string]any{"hourly": 0.0075, "monthly": 5.0}, "region_prices": []any{}},
			{"id": "g1-gpu-rtx6000-1", "label": "RTX6000 GPU", "vcpus": 8, "memory": 32768, "disk": 655360, "gpus": 1, "price": map[string]any{"hourly": 1.5, "monthly": 1000.0}},
			{"id": "g6-standard-6", "label": "Linode 16GB", "vcpus": 6, "memory": 16384, "disk": 327680, "price": map[string]any{"hourly": 0.12, "monthly": 80.0}},
		}
		f.reply(w, 200, f.page(r, types))
	case r.Method == "GET" && path == "networking/firewalls":
		filter := f.filter(r)
		var list []map[string]any
		for _, fw := range f.firewalls {
			if label, ok := filter["label"]; ok && fw["label"] != label {
				continue
			}
			list = append(list, fw)
		}
		f.reply(w, 200, f.page(r, list))
	case r.Method == "POST" && path == "networking/firewalls":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		label := body["label"].(string)
		if len(label) < 3 || len(label) > 32 || !validLabel(label) || strings.HasSuffix(label, "-") {
			f.fail(w, 400, "label", "Label is invalid: "+label)
			return
		}
		for _, fw := range f.firewalls {
			if fw["label"] == label {
				f.fail(w, 400, "label", "Label must be unique among your firewalls")
				return
			}
		}
		for _, tag := range body["tags"].([]any) {
			if len(tag.(string)) > 50 {
				f.fail(w, 400, "tags", "Tag is too long")
				return
			}
		}
		f.next++
		body["id"] = f.next
		body["status"] = "enabled"
		f.firewalls = append(f.firewalls, body)
		f.reply(w, 200, body)
	case r.Method == "PUT" && len(parts) == 4 && parts[1] == "firewalls" && parts[3] == "rules":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, fw := range f.firewalls {
			if fmt.Sprint(fw["id"]) == parts[2] {
				fw["rules"] = body
				f.reply(w, 200, body)
				return
			}
		}
		f.fail(w, 404, "", "Not found")
	case r.Method == "POST" && path == "linode/instances":
		if f.failCreate != nil {
			status, body := f.failCreate()
			f.reply(w, status, body)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		label := body["label"].(string)
		if len(label) < 3 || len(label) > 64 || !validLabel(label) {
			f.fail(w, 400, "label", "Label is invalid: "+label)
			return
		}
		pass, _ := body["root_pass"].(string)
		if len(pass) < 7 {
			f.fail(w, 400, "root_pass", "root_pass is required when deploying from an image")
			return
		}
		for _, tag := range body["tags"].([]any) {
			if n := len(tag.(string)); n < 3 || n > 50 {
				f.fail(w, 400, "tags", "Tag must be 3 to 50 characters: "+tag.(string))
				return
			}
		}
		if _, ok := f.regions[body["region"].(string)]; !ok {
			f.fail(w, 400, "region", "region is not valid")
			return
		}
		f.next++
		linode := map[string]any{
			"id": f.next, "label": label, "status": "provisioning", "region": body["region"], "type": body["type"],
			"ipv4": []any{"203.0.113.50"}, "tags": body["tags"], "created": "2026-10-19T10:00:00",
			"_request": body,
		}
		f.linodes = append(f.linodes, linode)
		f.reply(w, 200, linode)
	case r.Method == "GET" && path == "linode/instances":
		filter := f.filter(r)
		var list []map[string]any
		for _, l := range f.linodes {
			if tag, ok := filter["tags"]; ok && !slices.Contains(tagsOf(l), tag) {
				continue
			}
			list = append(list, l)
		}
		f.reply(w, 200, f.page(r, list))
	case len(parts) == 3 && parts[0] == "linode" && parts[1] == "instances":
		n, linode := f.findLinode(parts[2])
		if linode == nil {
			f.fail(w, 404, "", "Not found")
			return
		}
		if r.Method == "DELETE" {
			f.linodes = append(f.linodes[:n], f.linodes[n+1:]...)
			f.reply(w, 200, map[string]any{})
			return
		}
		f.reply(w, 200, linode)
	default:
		f.fail(w, 404, "", "no route "+r.Method+" "+path)
	}
}

func (f *fakeLinode) run(id string) {
	_, l := f.findLinode(id)
	l["status"] = "running"
}

func spec(name string) compute.InstanceSpec {
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	return compute.InstanceSpec{
		Name: name, Region: "eu-central", Size: "g6-standard-2", UserData: "#cloud-config\nhostname: x\n", Labels: owner.Labels(name),
		ControlPlaneCIDRs: []string{"203.0.113.5/32"}, MultiSite: true,
	}
}

func request(f *fakeLinode, label string) map[string]any {
	for _, l := range f.linodes {
		if l["label"] == label {
			return l["_request"].(map[string]any)
		}
	}
	f.t.Fatalf("no linode labelled %s", label)
	return nil
}

func TestCreateSendsTheMachineTaggedWithBase64UserDataAndItsFirewall(t *testing.T) {
	f, p := newFakeLinode(t)

	got, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "burst-a" || got.State != compute.StatePending || got.Ready() || got.PublicIPv4 != "203.0.113.50" {
		t.Errorf("instance = %+v", got)
	}
	req := request(f, "burst-a")
	if req["type"] != "g6-standard-2" || req["region"] != "eu-central" || req["image"] != "linode/ubuntu24.04" || req["booted"] != true {
		t.Errorf("request = %v", req)
	}
	user := req["metadata"].(map[string]any)["user_data"].(string)
	decoded, err := base64.StdEncoding.DecodeString(user)
	if err != nil || string(decoded) != "#cloud-config\nhostname: x\n" {
		t.Errorf("user data must be base64 of the document as given: %q (%v)", user, err)
	}
	if req["backups_enabled"] != false || req["interface_generation"] != "legacy_config" {
		t.Errorf("request = %v", req)
	}
	if _, set := req["private_ip"]; set {
		t.Errorf("a private address was asked for: %v", req)
	}
	tags := make([]string, 0, len(req["tags"].([]any)))
	for _, tag := range req["tags"].([]any) {
		tags = append(tags, tag.(string))
	}
	want := []string{"geass-cluster:c1", "geass-managed:true", "geass-node:burst-a", "geass-pool:burst"}
	if !slices.Equal(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
	if len(f.firewalls) != 1 || req["firewall_id"] != float64(f.firewalls[0]["id"].(int)) {
		t.Errorf("the machine must be created behind the cluster's firewall: %v vs %v", req["firewall_id"], f.firewalls)
	}
	if got.Labels[compute.LabelNode] != "burst-a" || got.Labels[compute.LabelCluster] != "c1" {
		t.Errorf("labels = %v", got.Labels)
	}
	if strings.Contains(fmt.Sprintf("%+v", got), req["root_pass"].(string)) {
		t.Error("the root password leaked into the instance")
	}
}

func TestEachMachineGetsItsOwnStrongRootPasswordThatIsNotKept(t *testing.T) {
	f, p := newFakeLinode(t)
	for _, name := range []string{"burst-a", "burst-b"} {
		if _, err := p.Create(context.Background(), spec(name)); err != nil {
			t.Fatal(err)
		}
	}
	a, b := request(f, "burst-a")["root_pass"].(string), request(f, "burst-b")["root_pass"].(string)
	if a == b {
		t.Error("two machines got the same password")
	}
	for _, pass := range []string{a, b} {
		if len(pass) != 32 || !regexp.MustCompile(`[a-z]`).MatchString(pass) || !regexp.MustCompile(`[A-Z]`).MatchString(pass) ||
			!regexp.MustCompile(`[0-9]`).MatchString(pass) || !regexp.MustCompile(`[^a-zA-Z0-9]`).MatchString(pass) {
			t.Errorf("password %q is not 32 characters with all four kinds", pass)
		}
	}
}

func TestAMachineIsReadyOnceItIsRunning(t *testing.T) {
	f, p := newFakeLinode(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	f.run(created.ID)
	_, l := f.findLinode(created.ID)
	l["ipv4"] = []any{"192.168.140.9", "203.0.113.50"} // a private address may come first
	got, err := p.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Ready() || got.PublicIPv4 != "203.0.113.50" || got.PrivateIPv4 != "192.168.140.9" || got.Region != "eu-central" || got.Size != "g6-standard-2" || got.CreatedAt.IsZero() {
		t.Errorf("instance = %+v", got)
	}
}

func TestAPrivateAddressIsAskedForOnlyWhenTheSpecWantsOne(t *testing.T) {
	f, p := newFakeLinode(t)
	s := spec("burst-a")
	s.PrivateAddress = true
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if request(f, "burst-a")["private_ip"] != true {
		t.Errorf("request = %v", request(f, "burst-a"))
	}
}

func TestACreateInARegionWithoutMetadataFailsBeforeAnythingIsMade(t *testing.T) {
	f, p := newFakeLinode(t)
	s := spec("burst-a")
	s.Region = "us-old"
	_, err := p.Create(context.Background(), s)
	if err == nil || !strings.Contains(err.Error(), "Metadata") || !strings.Contains(err.Error(), "us-old") {
		t.Fatalf("err = %v", err)
	}
	if len(f.linodes) != 0 || len(f.firewalls) != 0 {
		t.Error("something was made in a region that cannot give the machine its login")
	}
	s.Region = "nowhere"
	if _, err := p.Create(context.Background(), s); err == nil || !strings.Contains(err.Error(), "no region") || errors.Is(err, compute.ErrNotFound) {
		t.Errorf("err = %v: an unknown region must not look like a missing machine", err)
	}
}

func TestTheRegionsCapabilitiesAreReadOnce(t *testing.T) {
	f, p := newFakeLinode(t)
	for _, name := range []string{"burst-a", "burst-b"} {
		if _, err := p.Create(context.Background(), spec(name)); err != nil {
			t.Fatal(err)
		}
	}
	lookups := 0
	for _, r := range f.requests {
		if r == "GET /regions/eu-central" {
			lookups++
		}
	}
	if lookups != 1 {
		t.Errorf("the region was read %d times, want once", lookups)
	}
}

func TestARegionIsRequiredButTheConnectionsDefaultWillDo(t *testing.T) {
	f, p := newFakeLinode(t)
	s := spec("burst-a")
	s.Region = ""
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if request(f, "burst-a")["region"] != "eu-central" {
		t.Errorf("the default region was not used: %v", request(f, "burst-a"))
	}
	bare := New("tok", compute.Options{BaseURL: f.srv.URL})
	if _, err := bare.Create(context.Background(), s); err == nil || !strings.Contains(err.Error(), "region") {
		t.Errorf("err = %v", err)
	}
}

func TestALongNodeNameStillFitsInTagsAndComesBackWhole(t *testing.T) {
	f, p := newFakeLinode(t)
	name := "autoscale-burst-pool-with-a-rather-long-name-7"
	s := spec(name)
	s.Labels = compute.Owner{Cluster: "0b1f7c5e-9a42-4d1e-8c3a-5f6a7b8c9d0e", Pool: "autoscale-burst-pool-with-a-rather-long-name"}.Labels(name)
	created, err := p.Create(context.Background(), s)
	if err != nil {
		t.Fatalf("the API refuses tags over 50 characters, and the fake does too: %v", err)
	}
	for k, v := range s.Labels {
		if created.Labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, created.Labels[k], v)
		}
	}
	got, err := p.Get(context.Background(), created.ID)
	if err != nil || got.Labels[compute.LabelNode] != name || got.Labels[compute.LabelPool] != s.Labels[compute.LabelPool] {
		t.Errorf("labels read back = %v (%v)", got.Labels, err)
	}
	again, err := p.Create(context.Background(), s)
	if err != nil || again.ID != created.ID || len(f.linodes) != 1 {
		t.Errorf("a repeated create of a long name made a second machine: %v, %d linodes", err, len(f.linodes))
	}
	owner := compute.Owner{Cluster: s.Labels[compute.LabelCluster], Pool: s.Labels[compute.LabelPool]}
	listed, err := p.List(context.Background(), owner)
	if err != nil || len(listed) != 1 {
		t.Errorf("List = %+v, %v", listed, err)
	}
}

func TestTagsAndLabelsConvertBothWays(t *testing.T) {
	short := map[string]string{"geass-managed": "true", "geass-cluster": "c1"}
	if got := tagsFromLabels(short); !slices.Equal(got, []string{"geass-cluster:c1", "geass-managed:true"}) {
		t.Errorf("tags = %v", got)
	}
	long := map[string]string{"geass-node": strings.Repeat("a", 30) + strings.Repeat("b", 30) + "c", "geass-pool": "burst"}
	tags := tagsFromLabels(long)
	for _, tag := range tags {
		if len(tag) > maxTagLength {
			t.Errorf("tag %q is %d characters", tag, len(tag))
		}
	}
	if len(tags) != 3 {
		t.Errorf("tags = %v, want the node split in two", tags)
	}
	// Order and extra tags do not matter on the way back.
	shuffled := append([]string{"owner:someone", "plain"}, tags...)
	slices.Reverse(shuffled)
	back := labelsFromTags(shuffled)
	if back["geass-node"] != long["geass-node"] || back["geass-pool"] != "burst" || back["owner"] != "someone" {
		t.Errorf("labels = %v", back)
	}
	if _, has := back["plain"]; !has {
		t.Errorf("a tag without a colon must be a label without a value: %v", back)
	}
}

func TestLabelsAreMadeToFitLinodesRules(t *testing.T) {
	for in, want := range map[string]string{
		"burst-a":                     "burst-a",
		"Burst_A.1":                   "Burst_A.1",
		"3node":                       "geass-3node",
		"a--b":                        "a-b",
		"node.":                       "node",
		"ab":                          "geass-ab",
		"":                            "geass",
		"has space":                   "has-space",
		strings.Repeat("x", 70):       strings.Repeat("x", 64),
		strings.Repeat("x", 63) + "-": strings.Repeat("x", 63),
	} {
		got := instanceLabel(in)
		if got != want || !validLabel(got) || strings.HasSuffix(got, "-") {
			t.Errorf("instanceLabel(%q) = %q, want %q", in, got, want)
		}
	}
	for _, cluster := range []string{"c1", "0b1f7c5e-9a42-4d1e-8c3a-5f6a7b8c9d0e", "0b1f7c5e-9a42-4d1e-8c3a-5f6a7b8c9d0f", "a--b"} {
		got := firewallLabel(cluster)
		if len(got) > 32 || !validLabel(got) || strings.HasSuffix(got, "-") {
			t.Errorf("firewallLabel(%q) = %q", cluster, got)
		}
	}
	if firewallLabel("0b1f7c5e-9a42-4d1e-8c3a-5f6a7b8c9d0e") == firewallLabel("0b1f7c5e-9a42-4d1e-8c3a-5f6a7b8c9d0f") {
		t.Error("two long clusters got the same firewall label")
	}
	if firewallLabel("c1") != "geass-c1" {
		t.Errorf("a short cluster must give geass-<cluster>: %q", firewallLabel("c1"))
	}
}

func TestTheFirewallLetsInOnlyWhatTheClusterNeeds(t *testing.T) {
	f, p := newFakeLinode(t)
	s := spec("burst-a")
	s.ControlPlaneCIDRs = []string{"203.0.113.5", "2001:db8::/64"}
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(f.firewalls) != 1 {
		t.Fatalf("%d firewalls, want 1", len(f.firewalls))
	}
	fw := f.firewalls[0]
	if fw["label"] != "geass-c1" || fmt.Sprint(fw["tags"]) != "[geass-cluster:c1]" {
		t.Errorf("firewall = %v: it must be labelled for the cluster and tagged with it", fw)
	}
	raw, _ := json.Marshal(fw["rules"])
	var rules firewallRules
	if err := json.Unmarshal(raw, &rules); err != nil {
		t.Fatal(err)
	}
	if rules.InboundPolicy != "DROP" || rules.OutboundPolicy != "ACCEPT" || len(rules.Outbound) != 0 {
		t.Errorf("rules = %s", raw)
	}
	got := map[string]string{}
	for _, r := range rules.Inbound {
		got[r.Protocol+"/"+r.Ports] = r.Action + " " + strings.Join(r.Addresses.IPv4, ",") + " | " + strings.Join(r.Addresses.IPv6, ",")
		if len(r.Label) < 3 {
			t.Errorf("rule label %q is shorter than Linode allows", r.Label)
		}
	}
	if got["TCP/22"] != "ACCEPT 203.0.113.5/32 | 2001:db8::/64" || got["TCP/10250"] != "ACCEPT 203.0.113.5/32 | 2001:db8::/64" {
		t.Errorf("SSH and the kubelet must come from the control plane only: %v", got)
	}
	if got["UDP/51820"] != "ACCEPT 0.0.0.0/0 | ::/0" {
		t.Errorf("the overlay port must be open in a multi-site cluster: %v", got)
	}
	if len(got) != 3 {
		t.Errorf("inbound = %v, want exactly three rules", got)
	}
}

func TestWithoutControlPlaneAddressesSSHIsOpenAndTheKubeletIsNot(t *testing.T) {
	f, p := newFakeLinode(t)
	s := spec("burst-a")
	s.ControlPlaneCIDRs = nil
	s.MultiSite = false
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(f.firewalls[0]["rules"])
	var rules firewallRules
	_ = json.Unmarshal(raw, &rules)
	if len(rules.Inbound) != 1 || rules.Inbound[0].Ports != "22" || fmt.Sprint(rules.Inbound[0].Addresses.IPv4, rules.Inbound[0].Addresses.IPv6) != "[0.0.0.0/0] [::/0]" {
		t.Errorf("rules = %s", raw)
	}
}

func TestTheFirewallIsMadeOnceAndItsRulesAreKeptCurrent(t *testing.T) {
	f, p := newFakeLinode(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Create(context.Background(), spec("burst-b")); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.requests {
		if strings.HasPrefix(r, "PUT ") {
			t.Fatalf("a firewall with the right rules was rewritten: %v", f.requests)
		}
	}
	moved := spec("burst-c")
	moved.ControlPlaneCIDRs = []string{"203.0.113.77/32"} // the control plane moved
	if _, err := p.Create(context.Background(), moved); err != nil {
		t.Fatal(err)
	}
	if len(f.firewalls) != 1 {
		t.Fatalf("%d firewalls, want the one to be reused", len(f.firewalls))
	}
	raw, _ := json.Marshal(f.firewalls[0]["rules"])
	if !strings.Contains(string(raw), "203.0.113.77/32") || strings.Contains(string(raw), "203.0.113.5/32") {
		t.Errorf("the firewall still allows the old control plane: %s", raw)
	}
	if request(f, "burst-c")["firewall_id"] != float64(f.firewalls[0]["id"].(int)) {
		t.Error("the machine is not behind the firewall")
	}
}

func TestAFirewallTheUserNamesIsUsedAsItIs(t *testing.T) {
	f, p := newFakeLinode(t)
	s := spec("burst-a")
	s.Settings = map[string]string{SettingFirewallID: "4242"}
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(f.firewalls) != 0 {
		t.Error("Geass made a firewall although one was named")
	}
	if request(f, "burst-a")["firewall_id"] != float64(4242) {
		t.Errorf("request = %v", request(f, "burst-a"))
	}
	s.Settings = map[string]string{SettingFirewallID: "not-a-number"}
	if _, err := p.Create(context.Background(), spec("burst-b")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Create(context.Background(), s); err == nil || !strings.Contains(err.Error(), SettingFirewallID) {
		t.Errorf("err = %v", err)
	}
}

func TestAFirewallWithOurLabelThatIsNotTaggedForTheClusterIsAnErrorNotAnAdoption(t *testing.T) {
	f, p := newFakeLinode(t)
	f.firewalls = []map[string]any{{"id": 7, "label": "geass-c1", "tags": []any{"web"}, "rules": map[string]any{}}}
	if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !strings.Contains(err.Error(), SettingFirewallID) {
		t.Fatalf("err = %v", err)
	}
	if len(f.linodes) != 0 {
		t.Error("a machine was made behind a firewall that is not ours")
	}
}

func TestACreateThatRacesAnotherForTheFirewallUsesTheWinners(t *testing.T) {
	f, _ := newFakeLinode(t)
	// The first look finds nothing; by the time Geass creates, another create has made it.
	racing := &raceFirewall{fakeLinode: f}
	srv := httptest.NewServer(racing)
	defer srv.Close()
	p := New("tok", compute.Options{BaseURL: srv.URL, Region: "eu-central"})
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if len(f.firewalls) != 1 || request(f, "burst-a")["firewall_id"] != float64(f.firewalls[0]["id"].(int)) {
		t.Errorf("firewalls = %v", f.firewalls)
	}
}

// raceFirewall hides the cluster's firewall from the first list, as if it had been made a moment later.
type raceFirewall struct {
	*fakeLinode
	hidden bool
}

func (r *raceFirewall) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == "GET" && req.URL.Path == "/networking/firewalls" && !r.hidden {
		r.hidden = true
		r.firewalls = append(r.firewalls, map[string]any{
			"id": 55, "label": "geass-c1", "tags": []any{"geass-cluster:c1"}, "status": "enabled",
			"rules": map[string]any{"inbound_policy": "DROP", "outbound_policy": "ACCEPT", "outbound": []any{}, "inbound": []any{}},
		})
		r.reply(w, 200, r.page(req, nil))
		return
	}
	r.fakeLinode.ServeHTTP(w, req)
}

func TestACreateThatRepeatsTheNameReturnsTheFirstMachine(t *testing.T) {
	f, p := newFakeLinode(t)
	first, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || len(f.linodes) != 1 {
		t.Errorf("a repeated create made a second machine: %s vs %s, %d linodes", again.ID, first.ID, len(f.linodes))
	}
	found := false
	for _, filter := range f.filters {
		if strings.Contains(filter, "geass-node:burst-a") {
			found = true
		}
	}
	if !found {
		t.Errorf("the lookup did not filter by the node's tag: %v", f.filters)
	}
}

func TestAReplacementForTheSameNodeAndAMachineOfAnotherClusterAreNotTakenForTheFirst(t *testing.T) {
	f, p := newFakeLinode(t)
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
	if third.ID == first.ID || len(f.linodes) != 3 {
		t.Errorf("a machine of another cluster was taken for ours: %d linodes", len(f.linodes))
	}
}

func TestAGoneMachineWithTheSameNameDoesNotBlockANewOne(t *testing.T) {
	f, p := newFakeLinode(t)
	first, _ := p.Create(context.Background(), spec("burst-a"))
	_, l := f.findLinode(first.ID)
	l["status"] = "deleting"
	second, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Error("a machine that is being deleted was returned as if it were alive")
	}
}

func TestProviderFailuresBecomeTheErrorsCallersActOn(t *testing.T) {
	body := func(field, reason string) map[string]any {
		return map[string]any{"errors": []any{map[string]any{"field": field, "reason": reason}}}
	}
	for name, tc := range map[string]struct {
		status int
		body   map[string]any
		check  func(error) bool
	}{
		"limit":     {400, body("", "Account Limit reached. Please open a support ticket."), compute.IsQuota},
		"capacity":  {400, body("region", "This region is currently unavailable for that type"), func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }},
		"token":     {401, body("", "Invalid Token"), func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"scope":     {403, body("", "Your OAuth token is not authorized to use this endpoint."), func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"interface": {400, body("interface_generation", "interface_generation must be linode"), func(err error) bool { return err != nil && strings.Contains(err.Error(), "interfaces_for_new_linodes") }},
		"other": {400, body("type", "Invalid type"), func(err error) bool {
			return compute.IsStatus(err, 400) && !compute.IsQuota(err) && !compute.IsTransient(err)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			f, p := newFakeLinode(t)
			f.failCreate = func() (int, map[string]any) { return tc.status, tc.body }
			if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !tc.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestARateLimitedRequestIsRetried(t *testing.T) {
	f, p := newFakeLinode(t)
	f.limitedOnce = true
	if _, err := p.Verify(context.Background()); err != nil {
		t.Fatalf("a single 429 must be waited out: %v", err)
	}
}

func TestListFindsOnlyTheOwnersMachinesAcrossPagesInAnyState(t *testing.T) {
	f, p := newFakeLinode(t)
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
	for _, l := range f.linodes {
		if l["label"] == "burst-c" {
			l["status"] = "offline" // off, but it still costs money
		}
	}
	f.filters = nil

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
	if len(f.filters) == 0 || f.filters[0] != `{"tags":"geass-cluster:c1"}` {
		t.Errorf("List must filter by the cluster tag: %v", f.filters)
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
	f, p := newFakeLinode(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	before := len(f.requests)
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	// A Linode's disks and addresses go with it, and Geass makes no volume for it.
	if calls := f.requests[before:]; len(calls) != 1 || calls[0] != "DELETE "+"/linode/instances/"+created.ID {
		t.Errorf("Delete made the calls %v, want the one that removes the machine", calls)
	}
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Errorf("deleting again: %v", err)
	}
	if _, err := p.Get(context.Background(), created.ID); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
}

func TestSizesAreCheapestFirstWithRegionPricesAndWithoutGPUOrUnavailableTypes(t *testing.T) {
	f, p := newFakeLinode(t)
	f.availability = []map[string]any{
		{"region": "eu-central", "plan": "g6-standard-6", "available": false},
		{"region": "eu-central", "plan": "g6-standard-4", "available": true},
	}
	sizes, err := p.Sizes(context.Background(), "eu-central")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(sizes))
	for _, s := range sizes {
		ids = append(ids, s.ID)
	}
	if want := []string{"g6-nanode-1", "g6-standard-2", "g6-standard-4"}; !slices.Equal(ids, want) {
		t.Fatalf("sizes = %v, want %v", ids, want)
	}
	s := sizes[1]
	if s.VCPUs != 2 || s.MemoryMiB != 4096 || s.DiskGB != 80 || s.Currency != "USD" || s.Arch != "amd64" || s.Description != "Linode 4GB" {
		t.Errorf("size = %+v", s)
	}
	if s.HourlyPrice != 0.036 {
		t.Errorf("the price in eu-central is %v, want the region's own 0.036", s.HourlyPrice)
	}
	us, err := p.Sizes(context.Background(), "us-east")
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range us {
		if size.ID == "g6-standard-2" && size.HourlyPrice != 0.03 {
			t.Errorf("the default price was not used in us-east: %+v", size)
		}
	}
}

func TestSizesStillListWhenTheAvailabilityCannotBeRead(t *testing.T) {
	_, p := newFakeLinode(t) // availability answers 500
	sizes, err := p.Sizes(context.Background(), "eu-central")
	if err != nil || len(sizes) != 4 {
		t.Errorf("sizes = %d, %v", len(sizes), err)
	}
	if _, err := p.Sizes(context.Background(), "nowhere"); err == nil || !strings.Contains(err.Error(), "no region") {
		t.Errorf("err = %v", err)
	}
	all, err := p.Sizes(context.Background(), "")
	if err != nil || len(all) != 4 {
		t.Errorf("sizes without a region = %d, %v", len(all), err)
	}
}

func TestSizesFollowThePages(t *testing.T) {
	f, p := newFakeLinode(t)
	f.pageLimit = 1
	sizes, err := p.Sizes(context.Background(), "")
	if err != nil || len(sizes) != 4 {
		t.Errorf("sizes = %d, %v", len(sizes), err)
	}
}

func TestAMachinesStateIsTranslated(t *testing.T) {
	for status, want := range map[string]compute.State{
		"provisioning": compute.StatePending, "booting": compute.StatePending, "busy": compute.StatePending,
		"rebooting": compute.StatePending, "migrating": compute.StatePending,
		"running": compute.StateRunning, "offline": compute.StateStopped, "stopped": compute.StateStopped,
		"shutting_down": compute.StateStopped, "billing_suspension": compute.StateStopped,
		"deleting": compute.StateTerminating,
	} {
		if got := stateOf(status); got != want {
			t.Errorf("%s -> %s, want %s", status, got, want)
		}
	}
}

func TestVerifyIsAReadAndSaysWhatItCannotCheck(t *testing.T) {
	f, p := newFakeLinode(t)
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(account.Warnings, "\n")
	if account.Name == "" || !strings.Contains(joined, "scopes") || !strings.Contains(joined, "interfaces_for_new_linodes") {
		t.Errorf("account = %+v", account)
	}
	for _, r := range f.requests {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("Verify changed something: %v", f.requests)
		}
	}
}

func TestABadTokenIsRefused(t *testing.T) {
	f, _ := newFakeLinode(t)
	p := New("wrong", compute.Options{BaseURL: f.srv.URL})
	if _, err := p.Verify(context.Background()); !errors.Is(err, compute.ErrAuth) {
		t.Errorf("Verify = %v", err)
	}
}

func TestTheProviderRegistersItself(t *testing.T) {
	d, ok := compute.Lookup(ID)
	if !ok || d.ID != "Linode" || len(d.Fields) != 1 || d.Fields[0].Key != "token" || !d.Fields[0].Secret {
		t.Fatalf("descriptor = %+v", d)
	}
	if !d.KnownSetting(SettingFirewallID) || d.RegionHelp == "" || d.Summary == "" {
		t.Errorf("descriptor = %+v", d)
	}
	if _, err := compute.New(ID, compute.Credentials{}, compute.Options{}); err == nil {
		t.Error("a provider was built without a token")
	}
	if _, err := compute.New(ID, compute.Credentials{"token": "t"}, compute.Options{}); err != nil {
		t.Error(err)
	}
}
