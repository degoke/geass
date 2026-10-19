package upcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/degoke/geass/pkg/compute"
)

const (
	cloudInitTemplate = "01000000-0000-4000-8000-000030240200"
	nativeTemplate    = "01000000-0000-4000-8000-000030240100"
)

// fakeUpCloud is the part of the UpCloud API Geass uses, in memory.
type fakeUpCloud struct {
	t        *testing.T
	user     string
	password string
	token    string
	servers  map[string]map[string]any
	rules    map[string][]map[string]any
	storages map[string]bool
	next     int
	requests []string
	// authSeen is the Authorization header of the last request.
	authSeen string

	// failCreate is the status and error code of the next POST /server.
	failCreate func() (int, string)
	// failRules is the status and error code of rule calls.
	failRules func() (int, string)
	// maintenanceRefusesRules makes rule writes fail with SERVER_STATE_ILLEGAL while a server is in maintenance.
	maintenanceRefusesRules bool
	// stopLag keeps a stopped server "started" for that many more stop calls.
	stopLag int
	// noTemplates leaves the template list empty.
	noTemplates bool
	// pageCap is the most servers a list returns, whatever limit it is asked for.
	pageCap int
}

func newFake(t *testing.T) (*fakeUpCloud, *Provider) {
	f := &fakeUpCloud{
		t: t, user: "geass-api", password: "pw", servers: map[string]map[string]any{},
		rules: map[string][]map[string]any{}, storages: map[string]bool{}, maintenanceRefusesRules: true,
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, New("", "geass-api", "pw", compute.Options{BaseURL: srv.URL + "/1.3", Region: "de-fra1"})
}

func (f *fakeUpCloud) reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (f *fakeUpCloud) fail(w http.ResponseWriter, status int, code string) {
	f.reply(w, status, map[string]any{"error": map[string]any{"error_code": code, "error_message": "refused: " + code}})
}

func (f *fakeUpCloud) authorized(r *http.Request) bool {
	f.authSeen = r.Header.Get("Authorization")
	if f.token != "" {
		return f.authSeen == "Bearer "+f.token
	}
	u, p, ok := r.BasicAuth()
	return ok && u == f.user && p == f.password
}

func (f *fakeUpCloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if !f.authorized(r) {
		f.fail(w, 401, "AUTHENTICATION_FAILED")
		return
	}
	body, _ := io.ReadAll(r.Body)
	path := strings.TrimPrefix(r.URL.Path, "/1.3")

	switch {
	case r.Method == "GET" && path == "/account":
		f.reply(w, 200, map[string]any{"account": map[string]any{
			"username": "geass-api", "credits": 100, "currency": "EUR",
			"resource_limits": map[string]any{"cores": 4, "memory": 8192, "public_ipv4": 3},
		}})
	case r.Method == "GET" && path == "/plan":
		f.reply(w, 200, map[string]any{"plans": map[string]any{"plan": []map[string]any{
			{"name": "2xCPU-4GB", "core_number": 2, "memory_amount": 4096, "storage_size": 80, "storage_tier": "maxiops"},
			{"name": "1xCPU-2GB", "core_number": 1, "memory_amount": 2048, "storage_size": 50, "storage_tier": "maxiops"},
			{"name": "CLOUDNATIVE-2xCPU-4GB", "core_number": 2, "memory_amount": 4096, "storage_size": 0, "storage_tier": nil},
			{"name": "4xCPU-8GB", "core_number": 4, "memory_amount": 8192, "storage_size": 160, "storage_tier": "maxiops"},
		}}})
	case r.Method == "GET" && path == "/price":
		f.reply(w, 200, map[string]any{"prices": map[string]any{"zone": []map[string]any{
			{"name": "de-fra1", "server_plan_2xCPU-4GB": map[string]any{"amount": 1, "price": 3.5714}, "server_plan_1xCPU-2GB": map[string]any{"amount": 1, "price": 1.7857},
				"server_plan_CLOUDNATIVE-2xCPU-4GB": map[string]any{"amount": 1, "price": 2.0}, "ipv4_address": map[string]any{"amount": 1, "price": 0.5}},
			{"name": "fi-hel1", "server_plan_2xCPU-4GB": map[string]any{"amount": 1, "price": 4.0}, "server_plan_1xCPU-2GB": map[string]any{"amount": 1, "price": 2.0},
				"server_plan_4xCPU-8GB": map[string]any{"amount": 1, "price": 8.0}},
		}}})
	case r.Method == "GET" && path == "/storage/template":
		if f.noTemplates {
			f.reply(w, 200, map[string]any{"storages": map[string]any{"storage": []any{}}})
			return
		}
		f.reply(w, 200, map[string]any{"storages": map[string]any{"storage": []map[string]any{
			{"uuid": "01000000-0000-4000-8000-000030220200", "title": "Ubuntu Server 22.04 LTS (Jammy Jellyfish)", "template_type": "cloud-init", "state": "online"},
			{"uuid": nativeTemplate, "title": "Ubuntu Server 24.04 LTS (Noble Numbat)", "template_type": "native", "state": "online"},
			{"uuid": cloudInitTemplate, "title": "Ubuntu Server 24.04 LTS (Noble Numbat) cloud-init", "template_type": "cloud-init", "state": "online"},
			{"uuid": "01000000-0000-4000-8000-000020060100", "title": "Debian GNU/Linux 12 (Bookworm)", "template_type": "native", "state": "online"},
		}}})

	case r.Method == "POST" && path == "/server":
		f.createServer(w, body)
	case r.Method == "GET" && path == "/server":
		f.listServers(w, r)
	case strings.HasPrefix(path, "/server/"):
		f.serverRoute(w, r, strings.Split(strings.TrimPrefix(path, "/server/"), "/"), body)
	default:
		f.fail(w, 404, "NOT_FOUND")
	}
}

func (f *fakeUpCloud) newID() string {
	f.next++
	return fmt.Sprintf("00%06d-0000-4000-8000-000000000000", f.next)
}

func (f *fakeUpCloud) createServer(w http.ResponseWriter, body []byte) {
	if f.failCreate != nil {
		status, code := f.failCreate()
		f.fail(w, status, code)
		return
	}
	var req struct {
		Server map[string]any `json:"server"`
	}
	_ = json.Unmarshal(body, &req)
	id := f.newID()
	f.next++
	n := f.next
	server := map[string]any{
		"uuid": id, "title": req.Server["title"], "hostname": req.Server["hostname"], "state": "maintenance",
		"zone": req.Server["zone"], "plan": req.Server["plan"], "labels": req.Server["labels"],
		"ip_addresses": map[string]any{"ip_address": []map[string]any{
			{"access": "utility", "address": fmt.Sprintf("10.3.0.%d", n), "family": "IPv4"},
			{"access": "public", "address": fmt.Sprintf("203.0.113.%d", n), "family": "IPv4"},
		}},
		"_request": req.Server,
	}
	f.servers[id] = server
	f.storages[id] = true
	f.reply(w, 202, map[string]any{"server": server})
}

func (f *fakeUpCloud) listServers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit == 0 {
		limit = 25
	}
	if f.pageCap > 0 && limit > f.pageCap {
		limit = f.pageCap
	}
	var ids []string
	for id, s := range f.servers {
		if !matchesFilters(s["labels"], q["label"]) {
			continue
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var list []map[string]any
	for i := offset; i < len(ids) && i < offset+limit; i++ {
		s := f.servers[ids[i]]
		// A list is a summary: no addresses.
		list = append(list, map[string]any{"uuid": s["uuid"], "title": s["title"], "state": s["state"], "zone": s["zone"], "plan": s["plan"], "labels": s["labels"]})
	}
	f.reply(w, 200, map[string]any{"servers": map[string]any{"server": list}})
}

func matchesFilters(labels any, filters []string) bool {
	have := map[string]string{}
	if m, ok := labels.(map[string]any); ok {
		if list, ok := m["label"].([]any); ok {
			for _, l := range list {
				e := l.(map[string]any)
				have[strings.ToLower(fmt.Sprint(e["key"]))] = fmt.Sprint(e["value"])
			}
		}
	}
	if m, ok := labels.(labelList); ok {
		for _, e := range m.Label {
			have[strings.ToLower(e.Key)] = e.Value
		}
	}
	for _, f := range filters {
		key, value, hasValue := strings.Cut(f, "=")
		v, ok := have[strings.ToLower(key)]
		if !ok || (hasValue && v != value) {
			return false
		}
	}
	return true
}

func (f *fakeUpCloud) serverRoute(w http.ResponseWriter, r *http.Request, parts []string, body []byte) {
	id := parts[0]
	server, ok := f.servers[id]
	if !ok {
		f.fail(w, 404, "SERVER_NOT_FOUND")
		return
	}
	rest := strings.Join(parts[1:], "/")
	switch {
	case rest == "" && r.Method == "GET":
		f.reply(w, 200, map[string]any{"server": server})
	case rest == "" && r.Method == "DELETE":
		if server["state"] != "stopped" {
			f.fail(w, 409, "SERVER_STATE_ILLEGAL")
			return
		}
		q := r.URL.Query()
		if q.Get("storages") == "1" && q.Get("backups") == "delete" {
			delete(f.storages, id)
		}
		delete(f.servers, id)
		delete(f.rules, id)
		w.WriteHeader(204)
	case rest == "stop" && r.Method == "POST":
		if server["state"] != "started" {
			f.fail(w, 409, "SERVER_STATE_ILLEGAL")
			return
		}
		if f.stopLag > 0 {
			f.stopLag--
		} else {
			server["state"] = "stopped"
		}
		f.reply(w, 200, map[string]any{"server": server})
	case rest == "firewall_rule":
		if f.failRules != nil {
			status, code := f.failRules()
			f.fail(w, status, code)
			return
		}
		switch r.Method {
		case "GET":
			f.reply(w, 200, map[string]any{"firewall_rules": map[string]any{"firewall_rule": f.rules[id]}})
		case "PUT":
			if f.maintenanceRefusesRules && server["state"] == "maintenance" {
				f.fail(w, 409, "SERVER_STATE_ILLEGAL")
				return
			}
			var req struct {
				Rules struct {
					Rule []map[string]any `json:"firewall_rule"`
				} `json:"firewall_rules"`
			}
			_ = json.Unmarshal(body, &req)
			for i, rule := range req.Rules.Rule {
				rule["position"] = strconv.Itoa(i + 1)
			}
			f.rules[id] = req.Rules.Rule
			w.WriteHeader(204)
		}
	default:
		f.fail(w, 404, "NOT_FOUND")
	}
}

func (f *fakeUpCloud) count(prefix string) int {
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeUpCloud) start() {
	for _, s := range f.servers {
		if s["state"] == "maintenance" {
			s["state"] = "started"
		}
	}
}

func spec(name string) compute.InstanceSpec {
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	return compute.InstanceSpec{
		Name: name, Region: "de-fra1", Size: "2xCPU-4GB", UserData: "#cloud-config\nhostname: " + name + "\n", Labels: owner.Labels(name),
		AuthorizedKeys: []string{"ssh-ed25519 AAAAC3Nza geass"}, ControlPlaneCIDRs: []string{"203.0.113.5/32"}, MultiSite: true,
	}
}

func onlyServer(t *testing.T, f *fakeUpCloud) map[string]any {
	t.Helper()
	if len(f.servers) != 1 {
		t.Fatalf("%d servers, want 1", len(f.servers))
	}
	for _, s := range f.servers {
		return s
	}
	return nil
}

func TestCreateSendsTheLabelledServerWithItsDiskNetworkLoginAndUserData(t *testing.T) {
	f, p := newFake(t)

	instance, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	server := onlyServer(t, f)
	if instance.ID != server["uuid"] || instance.Name != "burst-a" || instance.State != compute.StatePending || instance.Region != "de-fra1" || instance.Size != "2xCPU-4GB" {
		t.Errorf("instance = %+v", instance)
	}
	if instance.PublicIPv4 == "" || !strings.HasPrefix(instance.PrivateIPv4, "10.3.0.") {
		t.Errorf("the addresses of the new server are %q and %q", instance.PublicIPv4, instance.PrivateIPv4)
	}
	sent := server["_request"].(map[string]any)
	if sent["zone"] != "de-fra1" || sent["title"] != "burst-a" || sent["hostname"] != "burst-a" || sent["plan"] != "2xCPU-4GB" ||
		sent["metadata"] != "yes" || sent["firewall"] != "on" || sent["password_delivery"] != "none" || sent["user_data"] != spec("burst-a").UserData {
		t.Errorf("request = %v", sent)
	}
	disk := sent["storage_devices"].(map[string]any)["storage_device"].([]any)[0].(map[string]any)
	if disk["action"] != "clone" || disk["storage"] != cloudInitTemplate || disk["size"] != float64(80) || disk["tier"] != "maxiops" || disk["title"] == "" {
		t.Errorf("disk = %v", disk)
	}
	ifaces := sent["networking"].(map[string]any)["interfaces"].(map[string]any)["interface"].([]any)
	if len(ifaces) != 2 || ifaces[0].(map[string]any)["type"] != "public" || ifaces[1].(map[string]any)["type"] != "utility" {
		t.Errorf("interfaces = %v", ifaces)
	}
	login := sent["login_user"].(map[string]any)
	keys := login["ssh_keys"].(map[string]any)["ssh_key"].([]any)
	if login["username"] != "geass" || login["create_password"] != "no" || len(keys) != 1 || keys[0] != "ssh-ed25519 AAAAC3Nza geass" {
		t.Errorf("login = %v", login)
	}
	labels := map[string]string{}
	for _, l := range sent["labels"].(map[string]any)["label"].([]any) {
		e := l.(map[string]any)
		labels[e["key"].(string)] = e["value"].(string)
	}
	if labels[compute.LabelManaged] != "true" || labels[compute.LabelNode] != "burst-a" || labels[compute.LabelCluster] != "c1" || labels[compute.LabelPool] != "burst" {
		t.Errorf("labels = %v", labels)
	}
	if labels["geass-fw"] != "cp=203.0.113.5/32;ms=1" {
		t.Errorf("the firewall policy label is %q", labels["geass-fw"])
	}
	for k := range labels {
		if len(k) < 2 || len(k) > 32 || strings.HasPrefix(k, "_") {
			t.Errorf("label key %q breaks UpCloud's rules", k)
		}
	}
}

func TestTheTemplateIsFoundByTitleAndACloudInitOneIsPreferred(t *testing.T) {
	f, p := newFake(t)
	f.noTemplates = true
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	disk := onlyServer(t, f)["_request"].(map[string]any)["storage_devices"].(map[string]any)["storage_device"].([]any)[0].(map[string]any)
	if disk["storage"] != fallbackTemplate {
		t.Errorf("without a listing the template is %v", disk["storage"])
	}

	f2, p2 := newFake(t)
	s := spec("burst-b")
	s.Image = "debian gnu/linux 12"
	if _, err := p2.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	disk = onlyServer(t, f2)["_request"].(map[string]any)["storage_devices"].(map[string]any)["storage_device"].([]any)[0].(map[string]any)
	if disk["storage"] != "01000000-0000-4000-8000-000020060100" {
		t.Errorf("a template named by its title: %v", disk["storage"])
	}

	f3, p3 := newFake(t)
	s = spec("burst-c")
	s.Image = "01000000-0000-4000-8000-0000aaaaaaaa"
	if _, err := p3.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	disk = onlyServer(t, f3)["_request"].(map[string]any)["storage_devices"].(map[string]any)["storage_device"].([]any)[0].(map[string]any)
	if disk["storage"] != s.Image {
		t.Errorf("a template named by its id: %v", disk["storage"])
	}

	_, p4 := newFake(t)
	s = spec("burst-d")
	s.Image = "no such image"
	if _, err := p4.Create(context.Background(), s); err == nil || !strings.Contains(err.Error(), "no such image") {
		t.Errorf("err = %v", err)
	}
}

func TestTheDiskIsWhatThePlanIncludesUnlessThePoolSaysOtherwise(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Size = "CLOUDNATIVE-2xCPU-4GB" // includes no storage
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	disk := onlyServer(t, f)["_request"].(map[string]any)["storage_devices"].(map[string]any)["storage_device"].([]any)[0].(map[string]any)
	if disk["size"] != float64(defaultDiskGB) || disk["tier"] != "maxiops" {
		t.Errorf("a plan without storage: disk = %v", disk)
	}

	f2, p2 := newFake(t)
	s2 := spec("burst-b")
	s2.Settings = map[string]string{SettingDiskGB: "40"}
	if _, err := p2.Create(context.Background(), s2); err != nil {
		t.Fatal(err)
	}
	disk = onlyServer(t, f2)["_request"].(map[string]any)["storage_devices"].(map[string]any)["storage_device"].([]any)[0].(map[string]any)
	if disk["size"] != float64(40) {
		t.Errorf("disk = %v", disk)
	}

	_, p3 := newFake(t)
	s3 := spec("burst-c")
	s3.Settings = map[string]string{SettingDiskGB: "big"}
	if _, err := p3.Create(context.Background(), s3); err == nil {
		t.Error("a disk size that is not a number was accepted")
	}
}

func TestWithoutKeysTheLoginUserIsLeftToTheCloudInit(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.AuthorizedKeys = nil
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, set := onlyServer(t, f)["_request"].(map[string]any)["login_user"]; set {
		t.Error("a login user without keys was sent")
	}
}

func TestTheFirewallLetsInOnlyWhatTheClusterNeedsAndDropsTheRest(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	f.start()
	instance, err := p.Get(context.Background(), onlyServer(t, f)["uuid"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if instance.State != compute.StateRunning {
		t.Fatalf("state = %s", instance.State)
	}
	rules := f.rules[instance.ID]
	got := map[string]string{}
	for _, r := range rules[:len(rules)-1] {
		if r["direction"] != "in" || r["action"] != "accept" || r["family"] != "IPv4" {
			t.Errorf("rule = %v", r)
		}
		got[fmt.Sprintf("%v/%v", r["protocol"], r["destination_port_start"])] = fmt.Sprintf("%v-%v", r["source_address_start"], r["source_address_end"])
		if r["destination_port_start"] != r["destination_port_end"] {
			t.Errorf("rule spans ports: %v", r)
		}
	}
	if got["tcp/22"] != "203.0.113.5-203.0.113.5" || got["tcp/10250"] != "203.0.113.5-203.0.113.5" || got["udp/51820"] != "<nil>-<nil>" || len(got) != 3 {
		t.Errorf("rules = %v", got)
	}
	last := rules[len(rules)-1]
	if last["direction"] != "in" || last["action"] != "drop" || len(last) != 3 { // plus the position the fake adds
		t.Errorf("the last rule must drop everything else: %v", last)
	}
}

func TestACidrBecomesTheFirstAndLastAddressOfItsRange(t *testing.T) {
	for cidr, want := range map[string]string{
		"203.0.113.0/24":  "203.0.113.0-203.0.113.255",
		"203.0.113.77":    "203.0.113.77-203.0.113.77",
		"10.0.0.0/8":      "10.0.0.0-10.255.255.255",
		"198.51.100.5/30": "198.51.100.4-198.51.100.7",
		"2001:db8::/32":   "2001:db8::-2001:db8:ffff:ffff:ffff:ffff:ffff:ffff",
	} {
		first, last, err := parseRange(cidr)
		if err != nil || first.String()+"-"+last.String() != want {
			t.Errorf("%s -> %s-%s (%v), want %s", cidr, first, last, err, want)
		}
	}
	if _, _, err := parseRange("not an address"); err == nil {
		t.Error("garbage was accepted")
	}
	rules, err := firewallRules([]string{"2001:db8::/32"}, false)
	if err != nil || rules[0].Family != "IPv6" {
		t.Errorf("an IPv6 control plane: %+v %v", rules, err)
	}
	_, p := newFake(t)
	bad := spec("burst-a")
	bad.ControlPlaneCIDRs = []string{"nonsense"}
	if _, err := p.Create(context.Background(), bad); err == nil {
		t.Error("a control plane that is not an address was accepted")
	}
}

func TestSSHIsOpenToAllOnlyWhenTheControlPlaneIsUnknown(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.ControlPlaneCIDRs = nil
	s.MultiSite = false
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	f.start()
	if _, err := p.Get(context.Background(), onlyServer(t, f)["uuid"].(string)); err != nil {
		t.Fatal(err)
	}
	for _, rules := range f.rules {
		if len(rules) != 2 || rules[0]["destination_port_start"] != "22" || rules[0]["source_address_start"] != nil || rules[1]["action"] != "drop" {
			t.Errorf("rules = %v", rules)
		}
	}
}

func TestAServerStillInMaintenanceIsPendingAndGetsItsRulesWhenItStarts(t *testing.T) {
	f, p := newFake(t)
	instance, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatalf("a server that does not take rules yet failed the create: %v", err)
	}
	if instance.State != compute.StatePending || len(f.rules) != 0 {
		t.Fatalf("state %s, rules %v", instance.State, f.rules)
	}
	// Still in maintenance: a Get has nothing to do.
	got, err := p.Get(context.Background(), instance.ID)
	if err != nil || got.State != compute.StatePending {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	f.start()
	got, err = p.Get(context.Background(), instance.ID)
	if err != nil || got.State != compute.StateRunning || !got.Ready() {
		t.Fatalf("Get after start = %+v, %v", got, err)
	}
	if len(f.rules[instance.ID]) != 4 {
		t.Errorf("rules = %v", f.rules[instance.ID])
	}
	// Seen once: no more rule calls.
	before := f.count("GET /1.3/server/" + instance.ID + "/firewall_rule")
	if _, err := p.Get(context.Background(), instance.ID); err != nil {
		t.Fatal(err)
	}
	if f.count("GET /1.3/server/"+instance.ID+"/firewall_rule") != before {
		t.Error("the rules were read again on every Get")
	}
}

func TestAServerIsNeverRunningWithoutItsFirewall(t *testing.T) {
	f, p := newFake(t)
	instance, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	f.start()
	f.failRules = func() (int, string) { return 409, "SERVER_STATE_ILLEGAL" }
	got, err := p.Get(context.Background(), instance.ID)
	if err != nil || got.State != compute.StatePending {
		t.Errorf("a server whose rules cannot be written yet: %+v, %v", got, err)
	}
	f.failRules = func() (int, string) { return 403, "SERVER_FORBIDDEN" }
	got, err = p.Get(context.Background(), instance.ID)
	if !errors.Is(err, compute.ErrAuth) || got.State == compute.StateRunning {
		t.Errorf("a credential that may not write rules: %+v, %v", got, err)
	}
	f.failRules = nil
	got, err = p.Get(context.Background(), instance.ID)
	if err != nil || got.State != compute.StateRunning {
		t.Errorf("once the rules go in: %+v, %v", got, err)
	}
}

func TestRulesThatAreAlreadyRightAreNotRewritten(t *testing.T) {
	f, p := newFake(t)
	instance, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	f.start()
	if _, err := p.Get(context.Background(), instance.ID); err != nil {
		t.Fatal(err)
	}
	// A new provider (the operator restarted) finds the rules in place and leaves them.
	p2 := New("", "geass-api", "pw", compute.Options{BaseURL: p.api.BaseURL, Region: "de-fra1"})
	puts := f.count("PUT ")
	if _, err := p2.Get(context.Background(), instance.ID); err != nil {
		t.Fatal(err)
	}
	if f.count("PUT ") != puts {
		t.Error("rules that were right were rewritten")
	}
	// Someone opens a port: the next provider closes it.
	f.rules[instance.ID] = append([]map[string]any{{"direction": "in", "action": "accept", "family": "IPv4", "protocol": "tcp", "destination_port_start": "80", "destination_port_end": "80"}}, f.rules[instance.ID]...)
	p3 := New("", "geass-api", "pw", compute.Options{BaseURL: p.api.BaseURL, Region: "de-fra1"})
	if _, err := p3.Get(context.Background(), instance.ID); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.rules[instance.ID] {
		if r["destination_port_start"] == "80" {
			t.Errorf("port 80 was left open: %v", f.rules[instance.ID])
		}
	}
}

func TestACreateThatRepeatsTheNameReturnsTheOwnersServer(t *testing.T) {
	f, p := newFake(t)
	first, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || len(f.servers) != 1 || f.count("POST /1.3/server") != 1 {
		t.Errorf("a repeated create made a second server: %s vs %s, %d servers", again.ID, first.ID, len(f.servers))
	}
	if again.PublicIPv4 != first.PublicIPv4 || again.PublicIPv4 == "" {
		t.Errorf("the found server has address %q, want %q", again.PublicIPv4, first.PublicIPv4)
	}
}

func TestACreateResumesTheFirewallOfAServerThatLostItsRules(t *testing.T) {
	f, p := newFake(t)
	first, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	// The earlier call died while the server was still in maintenance, before any rule went in.
	f.start()
	p2 := New("", "geass-api", "pw", compute.Options{BaseURL: p.api.BaseURL, Region: "de-fra1"})
	again, err := p2.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || len(f.servers) != 1 {
		t.Fatalf("a second server was made: %s vs %s", again.ID, first.ID)
	}
	if len(f.rules[first.ID]) != 4 || again.State != compute.StateRunning {
		t.Errorf("rules = %v, state %s", f.rules[first.ID], again.State)
	}
}

func TestACreateMeetingAnotherClustersNameMakesItsOwnServer(t *testing.T) {
	f, p := newFake(t)
	other := spec("burst-a")
	other.Labels = compute.Owner{Cluster: "someone-else", Pool: "burst"}.Labels("burst-a")
	if _, err := p.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if len(f.servers) != 2 {
		t.Errorf("%d servers: the other cluster's machine was taken for ours", len(f.servers))
	}
}

func TestProviderFailuresBecomeTheErrorsCallersActOn(t *testing.T) {
	isCapacity := func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }
	for name, tc := range map[string]struct {
		status int
		code   string
		check  func(error) bool
	}{
		"creating":  {409, "SERVER_CREATING_LIMIT_REACHED", compute.IsQuota},
		"cores":     {403, "SERVER_CORE_LIMIT_REACHED", compute.IsQuota},
		"storage":   {403, "MAXIOPS_STORAGE_LIMIT_REACHED", compute.IsQuota},
		"credits":   {402, "INSUFFICIENT_CREDITS", compute.IsQuota},
		"capacity":  {409, "SERVER_RESOURCES_UNAVAILABLE", isCapacity},
		"disk":      {409, "STORAGE_RESOURCES_UNAVAILABLE", isCapacity},
		"forbidden": {403, "STORAGE_FORBIDDEN", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"login":     {401, "AUTHENTICATION_FAILED", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
	} {
		t.Run(name, func(t *testing.T) {
			f, p := newFake(t)
			f.failCreate = func() (int, string) { return tc.status, tc.code }
			if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !tc.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestRateLimitingIsRetriedAndThenReported(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"error_code":"RATE_LIMITED","error_message":"slow down"}}`))
	}))
	defer srv.Close()
	p := New("t", "", "", compute.Options{BaseURL: srv.URL})
	p.api.Sleep = func(context.Context, time.Duration) error { return nil }
	p.api.MaxAttempts = 3
	_, err := p.Verify(context.Background())
	var rate *compute.RateLimitError
	if !errors.As(err, &rate) || rate.RetryAfter != 5*time.Second || calls != 3 {
		t.Errorf("err = %v after %d calls", err, calls)
	}
}

func TestATokenIsSentAsBearerAndWinsOverAPassword(t *testing.T) {
	f, _ := newFake(t)
	f.token = "ucat_01ABC"
	srv := httptest.NewServer(f)
	defer srv.Close()
	p := New("ucat_01ABC", "geass-api", "pw", compute.Options{BaseURL: srv.URL + "/1.3"})
	if _, err := p.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.authSeen != "Bearer ucat_01ABC" {
		t.Errorf("Authorization = %q", f.authSeen)
	}
	f.token = ""
	basic := New("", "geass-api", "pw", compute.Options{BaseURL: srv.URL + "/1.3"})
	if _, err := basic.Verify(context.Background()); err != nil || !strings.HasPrefix(f.authSeen, "Basic ") {
		t.Errorf("Authorization = %q, err %v", f.authSeen, err)
	}
	wrong := New("", "geass-api", "nope", compute.Options{BaseURL: srv.URL + "/1.3"})
	if _, err := wrong.Verify(context.Background()); !errors.Is(err, compute.ErrAuth) {
		t.Errorf("a wrong password: %v", err)
	}
}

func TestVerifyReadsAndCreatesNothingAndWarnsAboutSmallLimits(t *testing.T) {
	f, p := newFake(t)
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.Name != "UpCloud account geass-api" || len(account.Warnings) < 2 || !strings.Contains(account.Warnings[0], "4 CPU cores") {
		t.Errorf("account = %+v", account)
	}
	for _, r := range f.requests {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("Verify sent %s", r)
		}
	}
}

func TestListFindsOnlyTheOwnersMachinesWithTheirAddresses(t *testing.T) {
	f, p := newFake(t)
	for _, name := range []string{"burst-a", "burst-b"} {
		if _, err := p.Create(context.Background(), spec(name)); err != nil {
			t.Fatal(err)
		}
	}
	other := spec("web-a")
	other.Labels = compute.Owner{Cluster: "someone-else", Pool: "burst"}.Labels("web-a")
	if _, err := p.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	otherPool := spec("web-b")
	otherPool.Labels = compute.Owner{Cluster: "c1", Pool: "web"}.Labels("web-b")
	if _, err := p.Create(context.Background(), otherPool); err != nil {
		t.Fatal(err)
	}
	for _, s := range f.servers {
		if s["title"] == "burst-b" {
			s["state"] = "stopped"
		}
	}

	pool, err := p.List(context.Background(), compute.Owner{Cluster: "c1", Pool: "burst"})
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]compute.State{}
	for _, m := range pool {
		states[m.Name] = m.State
		if m.PublicIPv4 == "" {
			t.Errorf("%s was listed without its address", m.Name)
		}
	}
	if len(pool) != 2 || states["burst-a"] != compute.StatePending || states["burst-b"] != compute.StateStopped {
		t.Errorf("listed %+v", pool)
	}
	all, _ := p.List(context.Background(), compute.Owner{Cluster: "c1"})
	if len(all) != 3 {
		t.Errorf("the whole cluster is %d machines, want 3", len(all))
	}
}

func TestListPaginatesEvenWhenTheProviderCapsThePage(t *testing.T) {
	f, p := newFake(t)
	f.pageCap = 40
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	for i := range 130 {
		id := f.newID()
		name := fmt.Sprintf("burst-%03d", i)
		var labels labelList
		for k, v := range owner.Labels(name) {
			labels.Label = append(labels.Label, label{Key: k, Value: v})
		}
		f.servers[id] = map[string]any{"uuid": id, "title": name, "state": "started", "zone": "de-fra1", "labels": labels}
	}
	got, err := p.List(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range got {
		seen[m.ID] = true
	}
	if len(got) != 130 || len(seen) != 130 {
		t.Errorf("listed %d machines (%d distinct), want 130", len(got), len(seen))
	}
}

func TestDeleteStopsAStartedServerThenDeletesItWithItsStorage(t *testing.T) {
	f, p := newFake(t)
	created, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	f.start()
	f.stopLag = 1 // the first stop leaves the server started a while longer

	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.servers) != 1 || f.count("DELETE ") != 0 {
		t.Fatalf("a started server was deleted: %v", f.requests)
	}
	// Still started: stopped again, not an error.
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Get(context.Background(), created.ID); got.State != compute.StateStopped {
		t.Fatalf("state = %s", got.State)
	}
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.servers) != 0 || len(f.storages) != 0 || len(f.rules) != 0 {
		t.Errorf("leftovers that bill: %d servers, %d storages", len(f.servers), len(f.storages))
	}
	// Again, and for a server that never was.
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Errorf("deleting again: %v", err)
	}
	if err := p.Delete(context.Background(), "00ffffff-0000-4000-8000-000000000000"); err != nil {
		t.Errorf("deleting an unknown server: %v", err)
	}
	if _, err := p.Get(context.Background(), created.ID); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
}

func TestDeleteLeavesAServerInMaintenanceAlone(t *testing.T) {
	f, p := newFake(t)
	created, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	f.requests = nil
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.servers) != 1 || f.count("POST ") != 0 || f.count("DELETE ") != 0 {
		t.Errorf("a server in maintenance was touched: %v", f.requests)
	}
}

func TestSizesCostWhatTheZonesPriceListSaysInTheAccountsCurrency(t *testing.T) {
	_, p := newFake(t)
	sizes, err := p.Sizes(context.Background(), "de-fra1")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(sizes))
	for _, s := range sizes {
		ids = append(ids, s.ID)
	}
	if got := strings.Join(ids, ","); got != "1xCPU-2GB,CLOUDNATIVE-2xCPU-4GB,2xCPU-4GB" {
		t.Fatalf("sizes in de-fra1 = %s (a plan the zone does not price is not offered there)", got)
	}
	s := sizes[2]
	if s.VCPUs != 2 || s.MemoryMiB != 4096 || s.DiskGB != 80 || s.Currency != "EUR" || s.Arch != "amd64" || s.HourlyPrice != 0.035714 {
		t.Errorf("size = %+v", s)
	}
	hel, _ := p.Sizes(context.Background(), "fi-hel1")
	if len(hel) != 3 || hel[0].HourlyPrice != 0.02 || hel[2].ID != "4xCPU-8GB" {
		t.Errorf("in fi-hel1: %+v", hel)
	}
}

func TestSizesWithoutAPriceListAreStillListed(t *testing.T) {
	f, _ := newFake(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/price") {
			f.fail(w, 403, "PERMISSION_DENIED")
			return
		}
		f.ServeHTTP(w, r)
	}))
	defer srv.Close()
	p := New("", "geass-api", "pw", compute.Options{BaseURL: srv.URL + "/1.3", Region: "de-fra1"})
	sizes, err := p.Sizes(context.Background(), "")
	if err != nil || len(sizes) != 4 {
		t.Fatalf("sizes = %+v, %v", sizes, err)
	}
	for _, s := range sizes {
		if s.HourlyPrice != 0 {
			t.Errorf("%s has price %v", s.ID, s.HourlyPrice)
		}
	}
}

func TestAMachinesStateIsTranslated(t *testing.T) {
	for state, want := range map[string]compute.State{
		"maintenance": compute.StatePending, "started": compute.StateRunning, "stopped": compute.StateStopped,
		"error": compute.StateStopped, "something-new": compute.StatePending,
	} {
		if got := stateOf(state); got != want {
			t.Errorf("%s -> %s, want %s", state, got, want)
		}
	}
}

func TestTheFirewallPolicyRoundTripsAndFitsALabel(t *testing.T) {
	policy, err := firewallPolicy(spec("a"))
	if err != nil {
		t.Fatal(err)
	}
	cidrs, multi := parsePolicy(policy)
	if len(cidrs) != 1 || cidrs[0] != "203.0.113.5/32" || !multi {
		t.Errorf("%q -> %v %v", policy, cidrs, multi)
	}
	many := spec("a")
	for i := range 40 {
		many.ControlPlaneCIDRs = append(many.ControlPlaneCIDRs, fmt.Sprintf("203.0.113.%d/32", i))
	}
	if _, err := firewallPolicy(many); err == nil {
		t.Error("a policy longer than a label holds was accepted")
	}
}

func TestTheProviderRegistersItself(t *testing.T) {
	d, ok := compute.Lookup(ID)
	if !ok || len(d.Fields) != 3 {
		t.Fatalf("descriptor = %+v", d)
	}
	for _, f := range d.Fields {
		if !f.Optional {
			t.Errorf("%s is required: a token or a password must each be enough alone", f.Key)
		}
		if (f.Key == "token" || f.Key == "password") != f.Secret {
			t.Errorf("%s: Secret = %v", f.Key, f.Secret)
		}
	}
	if !d.KnownSetting(SettingDiskGB) || d.RegionHelp == "" {
		t.Errorf("descriptor = %+v", d)
	}
	if _, err := compute.New(ID, compute.Credentials{}, compute.Options{}); err == nil {
		t.Error("a provider was built without credentials")
	}
	if _, err := compute.New(ID, compute.Credentials{"username": "u"}, compute.Options{}); err == nil {
		t.Error("a provider was built with a user name and no password")
	}
	if _, err := compute.New(ID, compute.Credentials{"token": "ucat_x"}, compute.Options{}); err != nil {
		t.Error(err)
	}
	if _, err := compute.New(ID, compute.Credentials{"username": "u", "password": "p"}, compute.Options{}); err != nil {
		t.Error(err)
	}
}
