package scaleway

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
	ubuntuLocalID = "11111111-1111-4111-8111-111111111111"
	ubuntuSBSID   = "22222222-2222-4222-8222-222222222222"
)

// fakeScaleway is the part of the Scaleway API Geass uses, in memory. It keeps what a real one keeps: a server's
// local disk goes with terminate, its block volume and its address stay.
type fakeScaleway struct {
	t        *testing.T
	token    string
	servers  map[string]map[string]any
	groups   map[string]map[string]any
	rules    map[string][]map[string]any
	volumes  map[string]string // block volume id -> status
	busy     map[string]int    // block volume id -> deletes refused while it is still attached
	ips      map[string]bool
	userData map[string]string
	next     int
	requests []string

	// failCreate, failPoweron and failUserData are the status and body of the next such call.
	failCreate   func() (int, string)
	failPoweron  func() (int, string)
	failUserData func() (int, string)
	// listCalls counts the calls to list servers, by state.
	listCalls map[string]int
	// detachDelay is how many deletes of a block volume are refused after terminate.
	detachDelay int
}

func newFake(t *testing.T) (*fakeScaleway, *Provider) {
	f := &fakeScaleway{
		t: t, token: "secret", servers: map[string]map[string]any{}, groups: map[string]map[string]any{},
		rules: map[string][]map[string]any{}, volumes: map[string]string{}, busy: map[string]int{}, ips: map[string]bool{},
		userData: map[string]string{}, listCalls: map[string]int{}, detachDelay: 2,
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	p := New("secret", "proj-1", compute.Options{BaseURL: srv.URL, Region: "fr-par-1"})
	p.sleep = func(context.Context, time.Duration) error { return nil }
	return f, p
}

func (f *fakeScaleway) id(prefix string) string {
	f.next++
	return fmt.Sprintf("%s%07d-0000-4000-8000-000000000000", prefix, f.next)
}

func (f *fakeScaleway) reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (f *fakeScaleway) fail(w http.ResponseWriter, status int, typ, message string) {
	f.reply(w, status, map[string]any{"type": typ, "message": message})
}

func (f *fakeScaleway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if r.Header.Get("X-Auth-Token") != f.token {
		f.fail(w, 401, "denied_authentication", "invalid auth")
		return
	}
	body, _ := io.ReadAll(r.Body)
	path := r.URL.Path
	q := r.URL.Query()

	switch {
	case r.Method == "GET" && path == "/marketplace/v2/local-images":
		if q.Get("image_label") != "ubuntu_noble" {
			f.reply(w, 200, map[string]any{"local_images": []any{}, "total_count": 0})
			return
		}
		f.reply(w, 200, map[string]any{"total_count": 2, "local_images": []map[string]any{
			{"id": ubuntuSBSID, "type": "instance_sbs", "arch": "x86_64", "zone": q.Get("zone"), "label": "ubuntu_noble", "compatible_commercial_types": []string{"PLAY2-NANO", "DEV1-S", "PRO2-S"}},
			{"id": ubuntuLocalID, "type": "instance_local", "arch": "x86_64", "zone": q.Get("zone"), "label": "ubuntu_noble", "compatible_commercial_types": []string{"DEV1-S", "DEV1-M"}},
		}})
		return
	case strings.HasPrefix(path, "/block/v1/zones/"):
		f.block(w, r)
		return
	case !strings.HasPrefix(path, "/instance/v1/zones/"):
		f.fail(w, 404, "not_found", "no route "+path)
		return
	}
	rest := strings.TrimPrefix(path, "/instance/v1/zones/")
	zone, rest, _ := strings.Cut(rest, "/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")

	switch {
	case r.Method == "GET" && rest == "products/servers":
		f.reply(w, 200, map[string]any{"servers": map[string]any{
			"PLAY2-NANO": map[string]any{"hourly_price": 0.0101, "ncpus": 2, "ram": 4 * 1024 * 1024 * 1024, "arch": "x86_64", "volumes_constraint": map[string]any{"min_size": 0, "max_size": 0}},
			"DEV1-S":     map[string]any{"hourly_price": 0.0099, "ncpus": 2, "ram": 2 * 1024 * 1024 * 1024, "arch": "x86_64", "volumes_constraint": map[string]any{"min_size": 20000000000, "max_size": 20000000000}},
			"COPARM1-2C": map[string]any{"hourly_price": 0.0085, "ncpus": 2, "ram": 8 * 1024 * 1024 * 1024, "arch": "arm64", "volumes_constraint": map[string]any{"max_size": 50000000000}},
			"OLD1":       map[string]any{"hourly_price": 0.001, "ncpus": 1, "ram": 1 << 30, "arch": "x86_64", "end_of_service": true},
			"SOLDOUT":    map[string]any{"hourly_price": 0.002, "ncpus": 1, "ram": 1 << 30, "arch": "x86_64"},
			"FREE":       map[string]any{"ncpus": 1, "ram": 1 << 30, "arch": "x86_64"},
			"GBA":        map[string]any{"hourly_price": 0.0005, "ncpus": 1, "ram": 1 << 30, "arch": "arm"},
		}})
	case r.Method == "GET" && rest == "products/servers/availability":
		f.reply(w, 200, map[string]any{"servers": map[string]any{"SOLDOUT": map[string]any{"availability": "shortage"}, "DEV1-S": map[string]any{"availability": "available"}}})

	case r.Method == "GET" && rest == "servers":
		f.listServers(w, zone, q)
	case r.Method == "POST" && rest == "servers":
		f.createServer(w, zone, body)
	case len(parts) >= 2 && parts[0] == "servers":
		f.serverRoute(w, r, zone, parts[1:], body)

	case r.Method == "GET" && rest == "security_groups":
		var list []map[string]any
		for _, g := range f.groups {
			if tags := q.Get("tags"); tags != "" && !hasAll(g["tags"], strings.Split(tags, ",")) {
				continue
			}
			list = append(list, g)
		}
		f.reply(w, 200, map[string]any{"security_groups": list})
	case r.Method == "POST" && rest == "security_groups":
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		id := f.id("5")
		req["id"] = id
		f.groups[id] = req
		f.reply(w, 201, map[string]any{"security_group": req})
	case len(parts) == 3 && parts[0] == "security_groups" && parts[2] == "rules":
		id := parts[1]
		if _, ok := f.groups[id]; !ok {
			f.fail(w, 404, "not_found", "security group not found")
			return
		}
		switch r.Method {
		case "GET":
			f.reply(w, 200, map[string]any{"rules": append([]map[string]any{{"id": "default-smtp", "protocol": "TCP", "direction": "outbound", "action": "drop", "ip_range": "0.0.0.0/0", "editable": false}}, f.rules[id]...)})
		case "PUT":
			var req struct {
				Rules []map[string]any `json:"rules"`
			}
			_ = json.Unmarshal(body, &req)
			for i := range req.Rules {
				req.Rules[i]["id"] = f.id("6")
				req.Rules[i]["editable"] = true
			}
			f.rules[id] = req.Rules
			f.reply(w, 200, map[string]any{"rules": req.Rules})
		}
	case r.Method == "DELETE" && len(parts) == 2 && parts[0] == "ips":
		if !f.ips[parts[1]] {
			f.fail(w, 404, "not_found", "ip not found")
			return
		}
		delete(f.ips, parts[1])
		w.WriteHeader(204)
	default:
		f.fail(w, 404, "not_found", "no route "+r.Method+" "+path)
	}
}

func hasAll(tags any, want []string) bool {
	have := map[string]bool{}
	list, _ := tags.([]any)
	for _, t := range list {
		have[fmt.Sprint(t)] = true
	}
	if ss, ok := tags.([]string); ok {
		for _, t := range ss {
			have[t] = true
		}
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}

func (f *fakeScaleway) listServers(w http.ResponseWriter, zone string, q map[string][]string) {
	state := ""
	if v := q["state"]; len(v) > 0 {
		state = v[0]
	}
	f.listCalls[state]++
	perPage, _ := strconv.Atoi(first(q["per_page"]))
	page, _ := strconv.Atoi(first(q["page"]))
	if perPage == 0 {
		perPage = 50
	}
	if page == 0 {
		page = 1
	}
	var ids []string
	for id, s := range f.servers {
		if s["zone"] != zone {
			continue
		}
		if state != "" && s["state"] != state {
			continue
		}
		if tags := first(q["tags"]); tags != "" && !hasAll(s["tags"], strings.Split(tags, ",")) {
			continue
		}
		if project := first(q["project"]); project != "" && s["project"] != project {
			continue
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var list []map[string]any
	for i := (page - 1) * perPage; i < len(ids) && i < page*perPage; i++ {
		list = append(list, f.servers[ids[i]])
	}
	f.reply(w, 200, map[string]any{"servers": list})
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func (f *fakeScaleway) createServer(w http.ResponseWriter, zone string, body []byte) {
	if f.failCreate != nil {
		status, typ := f.failCreate()
		f.fail(w, status, typ, "refused: "+typ)
		return
	}
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	id := f.id("7")
	volumes := map[string]any{}
	switch req["image"] {
	case ubuntuSBSID, "ubuntu_noble":
		vol := f.id("8")
		f.volumes[vol] = "in_use"
		volumes["0"] = map[string]any{"id": vol, "volume_type": "sbs_volume"}
	default:
		volumes["0"] = map[string]any{"id": f.id("9"), "volume_type": "l_ssd"}
	}
	ip := f.id("a")
	f.ips[ip] = true
	server := map[string]any{
		"id": id, "name": req["name"], "state": "stopped", "tags": req["tags"], "commercial_type": req["commercial_type"],
		"zone": zone, "project": req["project"], "creation_date": "2026-10-19T10:00:00.000000+00:00",
		"public_ips": []any{map[string]any{"id": ip, "address": "", "family": "inet", "dynamic": true}},
		"volumes":    volumes, "_request": req,
	}
	f.servers[id] = server
	f.reply(w, 201, map[string]any{"server": server})
}

func (f *fakeScaleway) serverRoute(w http.ResponseWriter, r *http.Request, zone string, parts []string, body []byte) {
	// parts[0] is the id
	id := parts[0]
	server, ok := f.servers[id]
	if !ok {
		f.fail(w, 404, "not_found", "server not found")
		return
	}
	switch {
	case len(parts) == 1 && r.Method == "GET":
		f.reply(w, 200, map[string]any{"server": server})
	case len(parts) == 3 && parts[1] == "user_data" && (r.Method == "PATCH" || r.Method == "PUT"):
		if f.failUserData != nil {
			status, typ := f.failUserData()
			f.fail(w, status, typ, "refused")
			return
		}
		if r.Header.Get("Content-Type") != "text/plain" {
			f.fail(w, 400, "invalid_request_error", "user data is plain text")
			return
		}
		f.userData[id+"/"+parts[2]] = string(body)
		w.WriteHeader(204)
	case len(parts) == 2 && parts[1] == "action" && r.Method == "POST":
		var req map[string]string
		_ = json.Unmarshal(body, &req)
		switch req["action"] {
		case "poweron":
			if f.failPoweron != nil {
				status, typ := f.failPoweron()
				f.fail(w, status, typ, "refused: "+typ)
				return
			}
			server["state"] = "starting"
			for _, ip := range server["public_ips"].([]any) {
				ip.(map[string]any)["address"] = "203.0.113.50"
			}
			f.reply(w, 200, map[string]any{"task": map[string]any{"id": "t"}})
		case "terminate":
			if server["state"] == "starting" {
				f.fail(w, 409, "transient_state", "the server is starting")
				return
			}
			delete(f.servers, id)
			for _, v := range server["volumes"].(map[string]any) {
				vol := v.(map[string]any)
				if vol["volume_type"] == "sbs_volume" {
					f.busy[vol["id"].(string)] = f.detachDelay
				}
			}
			f.reply(w, 200, map[string]any{"task": map[string]any{"id": "t"}})
		default:
			f.fail(w, 400, "invalid_request_error", "unknown action")
		}
	default:
		f.fail(w, 404, "not_found", "no route")
	}
}

func (f *fakeScaleway) block(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	status, ok := f.volumes[id]
	if !ok || r.Method != "DELETE" {
		f.fail(w, 404, "not_found", "volume not found")
		return
	}
	if n := f.busy[id]; n > 0 {
		f.busy[id] = n - 1
		f.fail(w, 412, "precondition_failed", "volume is "+status+" and cannot be deleted")
		return
	}
	delete(f.volumes, id)
	w.WriteHeader(204)
}

func (f *fakeScaleway) count(prefix string) int {
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func spec(name string) compute.InstanceSpec {
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	return compute.InstanceSpec{
		Name: name, Region: "fr-par-1", Size: "PLAY2-NANO", UserData: "#cloud-config\nhostname: " + name + "\n", Labels: owner.Labels(name),
		ControlPlaneCIDRs: []string{"203.0.113.5/32"}, MultiSite: true,
	}
}

// running marks every server as started, the way a few minutes after power on they are.
func running(f *fakeScaleway) {
	for _, s := range f.servers {
		s["state"] = "running"
	}
}

func onlyServer(t *testing.T, f *fakeScaleway) map[string]any {
	t.Helper()
	if len(f.servers) != 1 {
		t.Fatalf("%d servers, want 1", len(f.servers))
	}
	for _, s := range f.servers {
		return s
	}
	return nil
}

func TestCreateSendsTheLabelledServerWithItsGroupImageAndUserData(t *testing.T) {
	f, p := newFake(t)

	instance, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	server := onlyServer(t, f)
	if instance.Name != "burst-a" || instance.State != compute.StatePending || instance.Region != "fr-par-1" || instance.Size != "PLAY2-NANO" {
		t.Errorf("instance = %+v", instance)
	}
	if want := "fr-par-1/" + server["id"].(string); instance.ID != want {
		t.Errorf("id = %q, want %q", instance.ID, want)
	}
	if instance.PublicIPv4 != "203.0.113.50" {
		t.Errorf("the address of the started server is %q", instance.PublicIPv4)
	}
	sent := server["_request"].(map[string]any)
	if sent["commercial_type"] != "PLAY2-NANO" || sent["project"] != "proj-1" || sent["dynamic_ip_required"] != true || sent["enable_ipv6"] != false {
		t.Errorf("request = %v", sent)
	}
	if sent["image"] != ubuntuSBSID {
		t.Errorf("image = %v, want the block image the size boots", sent["image"])
	}
	if _, set := sent["volumes"]; set {
		t.Errorf("a root volume was configured without being asked: %v", sent["volumes"])
	}
	tags := strings.Join(toStrings(sent["tags"]), ",")
	for _, want := range []string{"geass-managed:true", "geass-node:burst-a", "geass-cluster:c1", "geass-pool:burst"} {
		if !strings.Contains(tags, want) {
			t.Errorf("tags %q lack %s", tags, want)
		}
	}
	if sent["security_group"] == nil || sent["security_group"] == "" {
		t.Error("the server is created without its security group")
	}
	if got := f.userData[server["id"].(string)+"/cloud-init"]; got != spec("burst-a").UserData {
		t.Errorf("cloud-init = %q", got)
	}
	// The order matters: user data must be in place before the machine boots.
	poweron, userdata := -1, -1
	for i, r := range f.requests {
		if strings.HasSuffix(r, "/user_data/cloud-init") {
			userdata = i
		}
		if strings.HasSuffix(r, "/action") {
			poweron = i
		}
	}
	if userdata < 0 || poweron < 0 || userdata > poweron {
		t.Errorf("user data at %d, power on at %d", userdata, poweron)
	}
}

func toStrings(v any) []string {
	list := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func TestTheImageIsTheOneTheSizeCanBoot(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Size = "DEV1-M"
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if got := onlyServer(t, f)["_request"].(map[string]any)["image"]; got != ubuntuLocalID {
		t.Errorf("a size with a local disk got image %v", got)
	}

	f2, p2 := newFake(t)
	both := spec("burst-b")
	both.Size = "DEV1-S" // boots from either: the local disk is part of its price
	if _, err := p2.Create(context.Background(), both); err != nil {
		t.Fatal(err)
	}
	if got := onlyServer(t, f2)["_request"].(map[string]any)["image"]; got != ubuntuLocalID {
		t.Errorf("a size that boots from either got image %v, want the local one", got)
	}

	_, p3 := newFake(t)
	none := spec("burst-c")
	none.Size = "GP1-XL"
	if _, err := p3.Create(context.Background(), none); err == nil || !strings.Contains(err.Error(), "GP1-XL") {
		t.Errorf("a size no image boots: err = %v", err)
	}
}

func TestAnImageIdAndAnUnknownLabelAreLeftToScaleway(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Image = "33333333-3333-4333-8333-333333333333"
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if got := onlyServer(t, f)["_request"].(map[string]any)["image"]; got != s.Image {
		t.Errorf("image = %v", got)
	}
	f2, p2 := newFake(t)
	s2 := spec("burst-b")
	s2.Image = "debian_bookworm"
	if _, err := p2.Create(context.Background(), s2); err != nil {
		t.Fatal(err)
	}
	if got := onlyServer(t, f2)["_request"].(map[string]any)["image"]; got != "debian_bookworm" {
		t.Errorf("image = %v", got)
	}
}

func TestARootVolumeSizeIsSentForBlockImagesOnly(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Settings = map[string]string{SettingRootVolumeGB: "30"}
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	volumes := onlyServer(t, f)["_request"].(map[string]any)["volumes"].(map[string]any)["0"].(map[string]any)
	if volumes["volume_type"] != "sbs_volume" || volumes["size"] != float64(30_000_000_000) || volumes["boot"] != true {
		t.Errorf("volumes = %v", volumes)
	}

	f2, p2 := newFake(t)
	local := spec("burst-b")
	local.Size = "DEV1-M"
	local.Settings = map[string]string{SettingRootVolumeGB: "30"}
	if _, err := p2.Create(context.Background(), local); err != nil {
		t.Fatal(err)
	}
	if _, set := onlyServer(t, f2)["_request"].(map[string]any)["volumes"]; set {
		t.Error("a local disk was given a size")
	}

	_, p3 := newFake(t)
	bad := spec("burst-c")
	bad.Settings = map[string]string{SettingRootVolumeGB: "lots"}
	if _, err := p3.Create(context.Background(), bad); err == nil {
		t.Error("a root volume size that is not a number was accepted")
	}
}

func TestTheSecurityGroupLetsInOnlyWhatTheClusterNeeds(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 1 {
		t.Fatalf("%d groups, want 1", len(f.groups))
	}
	var id string
	for k, g := range f.groups {
		id = k
		if g["name"] != "geass-c1" || g["inbound_default_policy"] != "drop" || g["outbound_default_policy"] != "accept" || g["stateful"] != true || g["project"] != "proj-1" {
			t.Errorf("group = %v", g)
		}
	}
	got := map[string]string{}
	for _, r := range f.rules[id] {
		if r["direction"] != "inbound" || r["action"] != "accept" {
			t.Errorf("rule = %v", r)
		}
		got[fmt.Sprintf("%v/%v", r["protocol"], r["dest_port_from"])] = fmt.Sprint(r["ip_range"])
		if r["dest_port_from"] != r["dest_port_to"] {
			t.Errorf("rule spans ports: %v", r)
		}
	}
	if got["TCP/22"] != "203.0.113.5/32" || got["TCP/10250"] != "203.0.113.5/32" || got["UDP/51820"] != "0.0.0.0/0" || len(got) != 3 {
		t.Errorf("rules = %v", got)
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
	for _, rules := range f.rules {
		if len(rules) != 1 || rules[0]["ip_range"] != "0.0.0.0/0" || rules[0]["dest_port_from"] != float64(22) {
			t.Errorf("rules = %v", rules)
		}
	}
}

func TestTheGroupIsMadeOnceAndItsRulesAreKeptCurrent(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	next := spec("burst-b")
	next.ControlPlaneCIDRs = []string{"203.0.113.77/32"}
	if _, err := p.Create(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 1 {
		t.Fatalf("%d groups, want the one to be reused", len(f.groups))
	}
	for _, rules := range f.rules {
		raw, _ := json.Marshal(rules)
		if !strings.Contains(string(raw), "203.0.113.77/32") || strings.Contains(string(raw), "203.0.113.5/32") {
			t.Errorf("the group still allows the old control plane: %s", raw)
		}
	}
	// With nothing changed the rules are left alone.
	puts := 0
	before := f.count("PUT ")
	if _, err := p.Create(context.Background(), spec("burst-c")); err != nil {
		t.Fatal(err)
	}
	puts = f.count("PUT ") - before
	if puts != 1 { // burst-c names the first control plane again, so one update
		t.Errorf("%d rule updates", puts)
	}
	before = f.count("PUT ")
	if _, err := p.Create(context.Background(), spec("burst-d")); err != nil {
		t.Fatal(err)
	}
	if f.count("PUT ") != before {
		t.Error("the rules were rewritten although they were current")
	}
}

func TestAGroupTheUserNamesIsUsedAsItIs(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Settings = map[string]string{SettingSecurityGroupID: "44444444-4444-4444-8444-444444444444"}
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 0 || f.count("PUT ") != 0 {
		t.Error("Geass made or changed a group although one was named")
	}
	if got := onlyServer(t, f)["_request"].(map[string]any)["security_group"]; got != s.Settings[SettingSecurityGroupID] {
		t.Errorf("security_group = %v", got)
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
	if again.ID != first.ID || len(f.servers) != 1 {
		t.Errorf("a repeated create made a second server: %s vs %s, %d servers", again.ID, first.ID, len(f.servers))
	}
	if f.count("POST /instance/v1/zones/fr-par-1/servers/") != 1 {
		t.Errorf("the server was powered on again: %v", f.requests)
	}
}

func TestACreateFindsAServerInAnyStateByItsTags(t *testing.T) {
	f, p := newFake(t)
	first, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	// It has been running, and now is stopped: still the owner's machine for that name.
	for _, s := range f.servers {
		s["state"] = "stopped in place"
	}
	again, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || len(f.servers) != 1 {
		t.Errorf("a second server was made: %s vs %s", again.ID, first.ID)
	}
}

func TestACreateResumesAServerThatWasNotStarted(t *testing.T) {
	f, p := newFake(t)
	// The earlier call died after creating the server and before setting its user data: a stopped server that
	// carries the tags is all it left.
	s := spec("burst-a")
	seed := f.id("7")
	f.servers[seed] = map[string]any{
		"id": seed, "name": "burst-a", "state": "stopped", "zone": "fr-par-1", "project": "proj-1", "commercial_type": "PLAY2-NANO",
		"tags":       compute.LabelsToTags(s.Labels),
		"public_ips": []any{}, "volumes": map[string]any{},
	}

	instance, err := p.Create(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.servers) != 1 || instance.ID != "fr-par-1/"+seed {
		t.Fatalf("the create made another server: %d servers, instance %s", len(f.servers), instance.ID)
	}
	if f.count("POST /instance/v1/zones/fr-par-1/servers") != f.count("POST /instance/v1/zones/fr-par-1/servers/") {
		t.Errorf("a server was created: %v", f.requests)
	}
	if f.userData[seed+"/cloud-init"] != s.UserData || f.servers[seed]["state"] != "starting" || instance.State != compute.StatePending {
		t.Errorf("the missing steps were not done: user data %q, state %v / %s", f.userData[seed+"/cloud-init"], f.servers[seed]["state"], instance.State)
	}
}

func TestACreateLeavesARunningServerAlone(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	for _, s := range f.servers {
		s["state"] = "running"
	}
	f.requests = nil
	instance, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if instance.State != compute.StateRunning || instance.PublicIPv4 != "203.0.113.50" {
		t.Errorf("instance = %+v", instance)
	}
	for _, r := range f.requests {
		if strings.HasPrefix(r, "PATCH") || strings.HasPrefix(r, "PUT") || strings.Contains(r, "/action") {
			t.Errorf("a running server was touched: %s", r)
		}
	}
}

func TestAServerThatCannotBeStartedIsNotLeftBehindWhenThisCallMadeIt(t *testing.T) {
	f, p := newFake(t)
	f.failPoweron = func() (int, string) { return 400, "out_of_stock" }
	_, err := p.Create(context.Background(), spec("burst-a"))
	var capacity *compute.CapacityError
	if !errors.As(err, &capacity) {
		t.Fatalf("err = %v, want a capacity error", err)
	}
	if len(f.servers) != 0 || len(f.volumes) != 0 || len(f.ips) != 0 {
		t.Errorf("a stopped server that bills was left: %d servers, %d volumes, %d addresses", len(f.servers), len(f.volumes), len(f.ips))
	}
}

func TestAServerAnEarlierCallMadeIsKeptWhenItCannotBeStartedYet(t *testing.T) {
	f, p := newFake(t)
	first, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range f.servers {
		s["state"] = "stopped"
	}
	f.failPoweron = func() (int, string) { return 400, "out_of_stock" }
	got, err := p.Create(context.Background(), spec("burst-a"))
	var capacity *compute.CapacityError
	if !errors.As(err, &capacity) {
		t.Fatalf("err = %v", err)
	}
	if len(f.servers) != 1 || got.ID != first.ID {
		t.Errorf("the server of the earlier call was deleted or replaced: %d servers, %s", len(f.servers), got.ID)
	}
}

func TestAFailedUserDataStepRemovesTheNewServer(t *testing.T) {
	f, p := newFake(t)
	f.failUserData = func() (int, string) { return 500, "internal" }
	if _, err := p.Create(context.Background(), spec("burst-a")); err == nil {
		t.Fatal("a create without its user data succeeded")
	}
	if len(f.servers) != 0 || len(f.ips) != 0 || len(f.volumes) != 0 {
		t.Errorf("leftovers: %d servers, %d addresses, %d volumes", len(f.servers), len(f.ips), len(f.volumes))
	}
	if f.count("POST /instance/v1/zones/fr-par-1/servers/") != 1 { // only the terminate
		t.Errorf("requests = %v", f.requests)
	}
}

func TestProviderFailuresBecomeTheErrorsCallersActOn(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		typ    string
		check  func(error) bool
	}{
		"quota":      {403, "quotas_exceeded", compute.IsQuota},
		"capacity":   {400, "out_of_stock", func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }},
		"permission": {403, "permissions_denied", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"key":        {401, "denied_authentication", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
	} {
		t.Run(name, func(t *testing.T) {
			f, p := newFake(t)
			f.failCreate = func() (int, string) { return tc.status, tc.typ }
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
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"message":"slow down","type":"too_many_requests"}`))
	}))
	defer srv.Close()
	p := New("secret", "proj-1", compute.Options{BaseURL: srv.URL, Region: "fr-par-1"})
	p.api.Sleep = func(context.Context, time.Duration) error { return nil }
	p.api.MaxAttempts = 2
	_, err := p.Verify(context.Background())
	var rate *compute.RateLimitError
	if !errors.As(err, &rate) || rate.RetryAfter != 7*time.Second || calls != 2 {
		t.Errorf("err = %v after %d calls", err, calls)
	}
}

func TestABadKeyIsRefused(t *testing.T) {
	f, _ := newFake(t)
	srv := httptest.NewServer(f)
	defer srv.Close()
	p := New("wrong", "proj-1", compute.Options{BaseURL: srv.URL, Region: "fr-par-1"})
	if _, err := p.Verify(context.Background()); !errors.Is(err, compute.ErrAuth) {
		t.Errorf("Verify = %v", err)
	}
}

func TestVerifyReadsAndCreatesNothing(t *testing.T) {
	f, p := newFake(t)
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.Name != "Scaleway project proj-1" || len(account.Warnings) == 0 {
		t.Errorf("account = %+v", account)
	}
	for _, r := range f.requests {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("Verify sent %s", r)
		}
	}
}

func TestListFindsOnlyTheOwnersMachinesInEveryState(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Create(context.Background(), spec("burst-b")); err != nil {
		t.Fatal(err)
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
	var stopped string
	for id, s := range f.servers {
		if s["name"] == "burst-b" {
			s["state"] = "stopped in place"
			stopped = id
		}
	}

	pool, err := p.List(context.Background(), compute.Owner{Cluster: "c1", Pool: "burst"})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]compute.State{}
	for _, m := range pool {
		names[m.Name] = m.State
	}
	if len(pool) != 2 || names["burst-a"] != compute.StatePending || names["burst-b"] != compute.StateStopped {
		t.Errorf("listed %+v (stopped server %s)", pool, stopped)
	}
	all, _ := p.List(context.Background(), compute.Owner{Cluster: "c1"})
	if len(all) != 3 {
		t.Errorf("the whole cluster is %d machines, want 3", len(all))
	}
	for _, m := range all {
		if m.Labels[compute.LabelCluster] != "c1" || m.Labels[compute.LabelManaged] != "true" {
			t.Errorf("labels = %v", m.Labels)
		}
	}
	// A server in any state is found: the list is asked for each one.
	for _, state := range serverStates {
		if f.listCalls[state] == 0 {
			t.Errorf("no list call for state %q", state)
		}
	}
}

func TestListPaginatesAndLooksInEveryZoneItIsGiven(t *testing.T) {
	f, p := newFake(t)
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	for i := range 130 {
		id := f.id("7")
		f.servers[id] = map[string]any{
			"id": id, "name": fmt.Sprintf("burst-%03d", i), "state": "running", "zone": "fr-par-1", "project": "proj-1",
			"tags": compute.LabelsToTags(owner.Labels(fmt.Sprintf("burst-%03d", i))),
		}
	}
	got, err := p.List(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 130 {
		t.Fatalf("listed %d machines, want 130", len(got))
	}
	seen := map[string]bool{}
	for _, m := range got {
		seen[m.ID] = true
	}
	if len(seen) != 130 {
		t.Error("a machine was listed twice")
	}

	before := f.count("GET /instance/v1/zones/nl-ams-1/servers")
	owner.Regions = []string{"nl-ams-1", "fr-par-1", "nl-ams-1"}
	if _, err := p.List(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	if n := f.count("GET /instance/v1/zones/nl-ams-1/servers") - before; n != len(serverStates) {
		t.Errorf("the zone was listed %d times, want once per state", n)
	}
}

func TestListWithoutAnyZoneIsAnError(t *testing.T) {
	p := New("secret", "proj-1", compute.Options{BaseURL: "http://127.0.0.1:1"})
	if _, err := p.List(context.Background(), compute.Owner{Cluster: "c1"}); err == nil {
		t.Error("a list without a zone succeeded")
	}
}

func TestDeleteTerminatesAndRemovesWhatTerminateLeaves(t *testing.T) {
	f, p := newFake(t)
	created, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.volumes) != 1 || len(f.ips) != 1 {
		t.Fatalf("setup: %d volumes, %d addresses", len(f.volumes), len(f.ips))
	}
	running(f)
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.servers) != 0 || len(f.volumes) != 0 || len(f.ips) != 0 {
		t.Errorf("leftovers that bill: %d servers, %d block volumes, %d addresses", len(f.servers), len(f.volumes), len(f.ips))
	}
	if f.count("DELETE /block/v1/zones/fr-par-1/volumes/") < 3 {
		t.Errorf("the volume was not retried while it was still attached: %v", f.requests)
	}
	// Again, and for something that never was.
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Errorf("deleting again: %v", err)
	}
	if err := p.Delete(context.Background(), "fr-par-1/55555555-5555-4555-8555-555555555555"); err != nil {
		t.Errorf("deleting an unknown server: %v", err)
	}
	if _, err := p.Get(context.Background(), created.ID); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
}

func TestDeleteGivesUpOnAVolumeThatNeverLetsGo(t *testing.T) {
	f, p := newFake(t)
	f.detachDelay = 1 << 30
	created, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	running(f)
	if err := p.Delete(context.Background(), created.ID); err == nil || !strings.Contains(err.Error(), "still there") {
		t.Errorf("Delete = %v, want an error that says what is left", err)
	}
}

func TestDeleteLeavesAStartingServerForALaterCall(t *testing.T) {
	f, p := newFake(t)
	created, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	f.requests = nil
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.servers) != 1 {
		t.Errorf("a starting server was terminated: %v", f.requests)
	}
	running(f)
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.servers) != 0 || len(f.volumes) != 0 || len(f.ips) != 0 {
		t.Errorf("the second call did not finish it: %d servers, %d volumes, %d addresses", len(f.servers), len(f.volumes), len(f.ips))
	}
}

func TestDeleteOfALocalDiskServerNeedsNoVolumeCall(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Size = "DEV1-M"
	created, err := p.Create(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	running(f)
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if f.count("DELETE /block/") != 0 || len(f.ips) != 0 {
		t.Errorf("requests = %v, addresses left %d", f.requests, len(f.ips))
	}
}

func TestAnIdWithoutAZoneBelongsToTheDefaultOne(t *testing.T) {
	f, p := newFake(t)
	created, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	bare := strings.TrimPrefix(created.ID, "fr-par-1/")
	got, err := p.Get(context.Background(), bare)
	if err != nil || got.ID != created.ID {
		t.Errorf("Get(%q) = %+v, %v", bare, got, err)
	}
	_ = f
}

func TestSizesAreCheapestFirstWithoutRetiredSoldOutOrUnknownOnes(t *testing.T) {
	_, p := newFake(t)
	sizes, err := p.Sizes(context.Background(), "fr-par-1")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(sizes))
	for _, s := range sizes {
		ids = append(ids, s.ID)
	}
	if got := strings.Join(ids, ","); got != "COPARM1-2C,DEV1-S,PLAY2-NANO,FREE" {
		t.Fatalf("sizes = %s", got)
	}
	nano := sizes[2]
	if nano.VCPUs != 2 || nano.MemoryMiB != 4096 || nano.HourlyPrice != 0.0101 || nano.Currency != "EUR" || nano.Arch != "amd64" || nano.DiskGB != 0 {
		t.Errorf("size = %+v", nano)
	}
	if sizes[0].Arch != "arm64" || sizes[0].DiskGB != 50 || sizes[1].DiskGB != 20 {
		t.Errorf("sizes = %+v", sizes[:2])
	}
}

func TestAMachinesStateIsTranslated(t *testing.T) {
	for state, want := range map[string]compute.State{
		"starting": compute.StatePending, "running": compute.StateRunning, "stopping": compute.StateStopped,
		"stopped": compute.StateStopped, "stopped in place": compute.StateStopped, "locked": compute.StateStopped,
		"something-new": compute.StatePending,
	} {
		if got := stateOf(state); got != want {
			t.Errorf("%s -> %s, want %s", state, got, want)
		}
	}
}

func TestAnInstanceCarriesItsAddressesLabelsAndAge(t *testing.T) {
	private := "10.64.0.2"
	s := server{
		ID: "abc", Name: "n", State: "running", CommercialType: "DEV1-S", Zone: "nl-ams-1", CreationDate: "2026-10-19T10:00:00.123456+00:00",
		Tags: []string{"geass-node:n", "geass-managed:true"}, PrivateIP: &private,
		PublicIPs: []serverIP{{Address: "2001:db8::1", Family: "inet6"}, {Address: "203.0.113.9", Family: "inet"}},
	}
	got := s.instance("fr-par-1")
	if got.ID != "nl-ams-1/abc" || got.Region != "nl-ams-1" || got.PublicIPv4 != "203.0.113.9" || got.PrivateIPv4 != private ||
		got.Labels[compute.LabelNode] != "n" || got.CreatedAt.IsZero() || !got.Ready() {
		t.Errorf("instance = %+v", got)
	}
}

func TestTheProviderRegistersItself(t *testing.T) {
	d, ok := compute.Lookup(ID)
	if !ok || len(d.Fields) != 2 || d.Fields[0].Key != "secretKey" || !d.Fields[0].Secret || d.Fields[1].Key != "projectId" || d.Fields[1].Secret {
		t.Fatalf("descriptor = %+v", d)
	}
	if !d.KnownSetting(SettingSecurityGroupID) || !d.KnownSetting(SettingRootVolumeGB) || d.RegionHelp == "" {
		t.Errorf("descriptor = %+v", d)
	}
	if _, err := compute.New(ID, compute.Credentials{"secretKey": "k"}, compute.Options{}); err == nil {
		t.Error("a provider was built without a project")
	}
	if _, err := compute.New(ID, compute.Credentials{"secretKey": "k", "projectId": "p"}, compute.Options{}); err != nil {
		t.Error(err)
	}
}
