package gcp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/degoke/geass/pkg/compute"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

// testServiceAccountKey is a service account key file as Google writes it, with a key made for the test.
func testServiceAccountKey(t *testing.T) string {
	t.Helper()
	keyOnce.Do(func() {
		var err error
		testKey, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
	})
	der, err := x509.MarshalPKCS8PrivateKey(testKey)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "proj-1", "private_key_id": "kid-1",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": "geass@proj-1.iam.gserviceaccount.com",
	})
	return string(raw)
}

// fakeGCP is the part of Compute Engine, and of Google's token endpoint, that Geass uses, in memory.
type fakeGCP struct {
	t   *testing.T
	url string

	mu         sync.Mutex
	instances  map[string]map[string]any // by zone/name
	firewalls  map[string]map[string]any
	requestIDs []string
	inserts    int
	tokenCalls int
	requests   []string
	next       int

	// pageSize is how many instances an aggregated list returns at a time.
	pageSize int
	// opOnInsert is what the Operation of an insert looks like: nil means DONE without errors.
	opOnInsert func() map[string]any
	// opAfterPolls, when set, is the Operation as a later poll shows it, while the insert answered RUNNING.
	opAfterPolls func() map[string]any
	// failInsert, when set, is the status and body of the next insert.
	failInsert func() (int, map[string]any)
	// filterMatchesNothing makes a list with a filter come back empty, as a filter that is misread would.
	filterMatchesNothing bool
	// rejectFilter makes a list with a filter fail with 400.
	rejectFilter bool
	// firewallForbidden refuses firewall creation and updates.
	firewallForbidden bool
	// archOf says which architecture a machine type has.
	archOf map[string]string
}

func newFake(t *testing.T) (*fakeGCP, *Provider) {
	f := &fakeGCP{t: t, instances: map[string]map[string]any{}, firewalls: map[string]map[string]any{}, pageSize: 500, archOf: map[string]string{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	p, err := New(testServiceAccountKey(t), compute.Options{BaseURL: srv.URL, Region: "europe-west4-a"})
	if err != nil {
		t.Fatal(err)
	}
	p.api.Sleep = func(context.Context, time.Duration) error { return nil }
	p.opPoll, p.opWait = time.Millisecond, 3*time.Millisecond
	return f, p
}

func (f *fakeGCP) reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeGCP) fail(w http.ResponseWriter, status int, reason, message string) {
	f.reply(w, status, map[string]any{"error": map[string]any{
		"code": status, "message": message, "errors": []map[string]any{{"message": message, "domain": "global", "reason": reason}},
	}})
}

func (f *fakeGCP) operation(status string, errs map[string]any) map[string]any {
	op := map[string]any{"name": "operation-1", "status": status, "kind": "compute#operation"}
	if errs != nil {
		op["error"] = errs
	}
	return op
}

func (f *fakeGCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/token" {
		f.serveToken(w, r)
		return
	}
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer access-token-") {
		f.fail(w, 401, "authError", "Invalid Credentials")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/projects/proj-1/")
	parts := strings.Split(path, "/")
	switch {
	case r.Method == "GET" && path == "aggregated/instances":
		f.serveAggregated(w, r)
	case parts[0] == "zones" && len(parts) >= 3 && parts[2] == "instances":
		f.serveInstances(w, r, parts)
	case parts[0] == "zones" && len(parts) >= 3 && parts[2] == "operations":
		op := f.operation("DONE", nil)
		if f.opAfterPolls != nil {
			op = f.opAfterPolls()
		}
		f.reply(w, 200, op)
	case parts[0] == "zones" && len(parts) >= 3 && parts[2] == "machineTypes":
		f.serveMachineTypes(w, parts)
	case parts[0] == "global" && len(parts) >= 2 && parts[1] == "firewalls":
		f.serveFirewalls(w, r, parts)
	default:
		f.fail(w, 404, "notFound", "no route "+r.Method+" "+r.URL.Path)
	}
}

// serveToken checks the signed assertion the way Google would: the signature against the key's public half, and the
// claims that Google requires.
func (f *fakeGCP) serveToken(w http.ResponseWriter, r *http.Request) {
	f.tokenCalls++
	_ = r.ParseForm()
	if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		f.reply(w, 400, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	parts := strings.Split(r.Form.Get("assertion"), ".")
	if len(parts) != 3 {
		f.reply(w, 400, map[string]string{"error": "invalid_grant", "error_description": "not a JWT"})
		return
	}
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&testKey.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		f.reply(w, 400, map[string]string{"error": "invalid_grant", "error_description": "Invalid signature"})
		return
	}
	var header, claims map[string]any
	rawHeader, _ := base64.RawURLEncoding.DecodeString(parts[0])
	rawClaims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(rawHeader, &header)
	_ = json.Unmarshal(rawClaims, &claims)
	if header["alg"] != "RS256" || header["kid"] != "kid-1" ||
		claims["iss"] != "geass@proj-1.iam.gserviceaccount.com" ||
		claims["scope"] != "https://www.googleapis.com/auth/compute" || claims["aud"] != f.url+"/token" {
		f.reply(w, 400, map[string]string{"error": "invalid_grant", "error_description": fmt.Sprintf("bad header or claims: %v %v", header, claims)})
		return
	}
	exp, iat := int64(claims["exp"].(float64)), int64(claims["iat"].(float64))
	if exp <= iat || exp-iat > 3600 {
		f.reply(w, 400, map[string]string{"error": "invalid_grant", "error_description": "bad lifetime"})
		return
	}
	f.reply(w, 200, map[string]any{"access_token": "access-token-" + strconv.Itoa(f.tokenCalls), "token_type": "Bearer", "expires_in": 3600})
}

func (f *fakeGCP) serveAggregated(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("filter")
	if filter != "" && f.rejectFilter {
		f.fail(w, 400, "invalid", "Invalid value for field 'filter'")
		return
	}
	cluster := ""
	if m := regexp.MustCompile(`labels\.geass-cluster = "([^"]*)"`).FindStringSubmatch(filter); m != nil {
		cluster = m[1]
	}
	keys := slices.Sorted(maps.Keys(f.instances))
	var matching []map[string]any
	for _, k := range keys {
		inst := f.instances[k]
		labels, _ := inst["labels"].(map[string]any)
		if filter != "" && (f.filterMatchesNothing || fmt.Sprint(labels["geass-cluster"]) != cluster) {
			continue
		}
		matching = append(matching, inst)
	}
	start := 0
	if tok := r.URL.Query().Get("pageToken"); tok != "" {
		start, _ = strconv.Atoi(tok)
	}
	end := min(start+f.pageSize, len(matching))
	items := map[string]any{}
	for _, inst := range matching[start:end] {
		scope := "zones/" + inst["_zone"].(string)
		entry, _ := items[scope].(map[string]any)
		if entry == nil {
			entry = map[string]any{"instances": []any{}}
			items[scope] = entry
		}
		entry["instances"] = append(entry["instances"].([]any), inst)
	}
	out := map[string]any{"items": items}
	if end < len(matching) {
		out["nextPageToken"] = strconv.Itoa(end)
	}
	f.reply(w, 200, out)
}

func (f *fakeGCP) serveInstances(w http.ResponseWriter, r *http.Request, parts []string) {
	zone := parts[1]
	if r.Method == "POST" && len(parts) == 3 {
		f.inserts++
		f.requestIDs = append(f.requestIDs, r.URL.Query().Get("requestId"))
		if f.failInsert != nil {
			status, body := f.failInsert()
			f.reply(w, status, body)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		name := body["name"].(string)
		if _, exists := f.instances[zone+"/"+name]; exists {
			f.fail(w, 409, "alreadyExists", "The resource 'projects/proj-1/zones/"+zone+"/instances/"+name+"' already exists")
			return
		}
		f.next++
		f.instances[zone+"/"+name] = map[string]any{
			"id": strconv.Itoa(f.next), "name": name, "status": "PROVISIONING", "_zone": zone,
			"zone":              "https://www.googleapis.com/compute/v1/projects/proj-1/zones/" + zone,
			"machineType":       "https://www.googleapis.com/compute/v1/projects/proj-1/zones/" + zone + "/" + body["machineType"].(string),
			"creationTimestamp": "2026-10-19T03:00:00.000-07:00",
			"labels":            body["labels"],
			"networkInterfaces": []any{map[string]any{"networkIP": "10.164.0.5", "accessConfigs": []any{map[string]any{"type": "ONE_TO_ONE_NAT"}}}},
			"_request":          body,
		}
		switch {
		case f.opAfterPolls != nil:
			f.reply(w, 200, f.operation("RUNNING", nil))
		case f.opOnInsert != nil:
			f.reply(w, 200, f.operation("DONE", f.opOnInsert()))
		default:
			f.reply(w, 200, f.operation("DONE", nil))
		}
		return
	}
	if len(parts) != 4 {
		f.fail(w, 404, "notFound", "no route")
		return
	}
	key := zone + "/" + parts[3]
	inst, ok := f.instances[key]
	if !ok {
		f.fail(w, 404, "notFound", "The resource 'projects/proj-1/zones/"+zone+"/instances/"+parts[3]+"' was not found")
		return
	}
	if r.Method == "DELETE" {
		delete(f.instances, key)
		f.reply(w, 200, f.operation("RUNNING", nil))
		return
	}
	f.reply(w, 200, inst)
}

func (f *fakeGCP) serveMachineTypes(w http.ResponseWriter, parts []string) {
	if len(parts) == 4 {
		arch, ok := f.archOf[parts[3]]
		if !ok {
			f.fail(w, 404, "notFound", "machine type not found")
			return
		}
		f.reply(w, 200, map[string]any{"name": parts[3], "architecture": arch})
		return
	}
	f.reply(w, 200, map[string]any{"items": []map[string]any{
		{"name": "e2-standard-4", "description": "4 vCPUs 16 GB RAM", "guestCpus": 4, "memoryMb": 16384, "architecture": "X86_64"},
		{"name": "e2-medium", "description": "2 vCPUs 4 GB RAM", "guestCpus": 2, "memoryMb": 4096, "architecture": "X86_64"},
		{"name": "t2a-standard-2", "description": "2 vCPUs 8 GB RAM", "guestCpus": 2, "memoryMb": 8192, "architecture": "ARM64"},
		{"name": "n1-standard-1", "description": "old", "guestCpus": 1, "memoryMb": 3840, "architecture": "X86_64", "deprecated": map[string]any{"state": "DEPRECATED"}},
		{"name": "a2-highgpu-1g", "description": "gpu", "guestCpus": 12, "memoryMb": 87040, "architecture": "X86_64", "accelerators": []map[string]any{{"guestAcceleratorCount": 1}}},
	}})
}

func (f *fakeGCP) serveFirewalls(w http.ResponseWriter, r *http.Request, parts []string) {
	if r.Method == "GET" && len(parts) == 2 {
		f.reply(w, 200, map[string]any{"items": []any{}})
		return
	}
	if r.Method == "POST" && len(parts) == 2 {
		if f.firewallForbidden {
			f.fail(w, 403, "forbidden", "Required 'compute.firewalls.create' permission for 'projects/proj-1/global/firewalls/geass-c1'")
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		name := body["name"].(string)
		if _, exists := f.firewalls[name]; exists {
			f.fail(w, 409, "alreadyExists", "exists")
			return
		}
		f.firewalls[name] = body
		f.reply(w, 200, f.operation("RUNNING", nil))
		return
	}
	fw, ok := f.firewalls[parts[2]]
	if !ok {
		f.fail(w, 404, "notFound", "firewall not found")
		return
	}
	if r.Method == "PATCH" {
		if f.firewallForbidden {
			f.fail(w, 403, "forbidden", "Required 'compute.firewalls.update' permission")
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		maps.Copy(fw, body)
		f.reply(w, 200, f.operation("RUNNING", nil))
		return
	}
	f.reply(w, 200, fw)
}

func (f *fakeGCP) run(zone, name, publicIP string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inst := f.instances[zone+"/"+name]
	inst["status"] = "RUNNING"
	inst["networkInterfaces"] = []any{map[string]any{"networkIP": "10.164.0.5", "accessConfigs": []any{map[string]any{"natIP": publicIP}}}}
}

func (f *fakeGCP) sent(zone, name string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.instances[zone+"/"+name]["_request"].(map[string]any)
}

func spec(name string) compute.InstanceSpec {
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	return compute.InstanceSpec{
		Name: name, Region: "europe-west4-a", Size: "e2-standard-4", UserData: "#cloud-config\nhostname: x\n",
		AuthorizedKeys: []string{"ssh-ed25519 AAAA"}, Labels: owner.Labels(name),
		ControlPlaneCIDRs: []string{"203.0.113.5/32"}, MultiSite: true,
	}
}

func TestTheServiceAccountKeyIsTradedForATokenThatIsReused(t *testing.T) {
	f, p := newFake(t)
	// The fake checks the JWT's signature against the key's public half, and its header and claims (RS256, the key id,
	// the service account as issuer, the compute scope, the token endpoint as audience, a lifetime of at most an hour).
	for range 3 {
		if _, err := p.Verify(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.tokenCalls != 1 {
		t.Errorf("%d token requests for three calls, want the token to be reused", f.tokenCalls)
	}
}

func TestAServiceAccountKeyThatCannotWorkIsRefusedWhenTheProviderIsBuilt(t *testing.T) {
	good := testServiceAccountKey(t)
	var parsed map[string]string
	_ = json.Unmarshal([]byte(good), &parsed)
	with := func(change func(map[string]string)) string {
		m := map[string]string{}
		maps.Copy(m, parsed)
		change(m)
		raw, _ := json.Marshal(m)
		return string(raw)
	}
	for name, raw := range map[string]string{
		"not json":   "AIza-an-api-key",
		"user creds": with(func(m map[string]string) { m["type"] = "authorized_user" }),
		"no email":   with(func(m map[string]string) { delete(m, "client_email") }),
		"no project": with(func(m map[string]string) { delete(m, "project_id") }),
		"not pem":    with(func(m map[string]string) { m["private_key"] = "nope" }),
		"not rsa (pem)": with(func(m map[string]string) {
			m["private_key"] = "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"
		}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(raw, compute.Options{}); err == nil {
				t.Error("a key that cannot sign was accepted")
			}
		})
	}
}

func TestAKeyGoogleRefusesIsAnAuthError(t *testing.T) {
	_, p := newFake(t)
	// Another key than the one the fake knows: the token endpoint answers invalid_grant.
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(other)
	var parsed map[string]string
	_ = json.Unmarshal([]byte(testServiceAccountKey(t)), &parsed)
	parsed["private_key"] = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	raw, _ := json.Marshal(parsed)
	bad, err := New(string(raw), compute.Options{BaseURL: strings.TrimSuffix(p.api.BaseURL, "/")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Verify(context.Background()); !errors.Is(err, compute.ErrAuth) {
		t.Errorf("Verify = %v, want ErrAuth", err)
	}
}

func TestCreateSendsTheMachineWithItsLabelsUserDataDiskAndAddress(t *testing.T) {
	f, p := newFake(t)
	f.archOf["e2-standard-4"] = "X86_64"

	instance, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if instance.ID != "europe-west4-a/burst-a" || instance.Name != "burst-a" || instance.State != compute.StatePending || instance.PublicIPv4 != "" {
		t.Errorf("instance = %+v", instance)
	}
	sent := f.sent("europe-west4-a", "burst-a")
	if sent["machineType"] != "zones/europe-west4-a/machineTypes/e2-standard-4" {
		t.Errorf("machineType = %v", sent["machineType"])
	}
	disk := sent["disks"].([]any)[0].(map[string]any)
	params := disk["initializeParams"].(map[string]any)
	if disk["boot"] != true || disk["autoDelete"] != true || params["sourceImage"] != defaultImageAMD64 || params["diskSizeGb"] != "30" {
		t.Errorf("boot disk = %v", disk)
	}
	if _, set := params["diskType"]; set {
		t.Error("a disk type was sent although none was asked for: the machine series' own default is the safe one")
	}
	nic := sent["networkInterfaces"].([]any)[0].(map[string]any)
	ac := nic["accessConfigs"].([]any)[0].(map[string]any)
	if nic["network"] != "global/networks/default" || ac["type"] != "ONE_TO_ONE_NAT" || ac["name"] != "External NAT" {
		t.Errorf("network interface = %v", nic)
	}
	if _, set := nic["subnetwork"]; set {
		t.Error("a subnetwork was sent that nobody asked for")
	}
	labels := sent["labels"].(map[string]any)
	if labels[compute.LabelManaged] != "true" || labels[compute.LabelCluster] != "c1" || labels[compute.LabelPool] != "burst" || labels[compute.LabelNode] != "burst-a" {
		t.Errorf("labels = %v", labels)
	}
	items := sent["metadata"].(map[string]any)["items"].([]any)
	meta := map[string]string{}
	for _, it := range items {
		m := it.(map[string]any)
		meta[m["key"].(string)] = m["value"].(string)
	}
	if meta["user-data"] != "#cloud-config\nhostname: x\n" {
		t.Errorf("user-data = %q, want the cloud-init document untouched", meta["user-data"])
	}
	if meta["enable-oslogin"] != "FALSE" {
		t.Errorf("metadata = %v", meta)
	}
	if tags := sent["tags"].(map[string]any)["items"].([]any); len(tags) != 1 || tags[0] != "geass-c1" {
		t.Errorf("tags = %v, want the network tag the firewall targets", tags)
	}
	if _, set := sent["scheduling"]; set {
		t.Error("an on-demand machine was given a scheduling block")
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(f.requestIDs[0]) || f.requestIDs[0] == "00000000-0000-0000-0000-000000000000" {
		t.Errorf("requestId = %q, want a UUID", f.requestIDs[0])
	}
}

func TestTheRequestIDIsTheSameForTheSameMachineAndDiffersOtherwise(t *testing.T) {
	a := requestID("c1", "burst-a")
	if a != requestID("c1", "burst-a") {
		t.Error("the request id changes between calls, so a repeated insert would not be ignored")
	}
	if a == requestID("c1", "burst-b") || a == requestID("c2", "burst-a") {
		t.Error("two machines share a request id")
	}
}

func TestASpotMachineIsDeletedWhenTakenBack(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Spot = true
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	sched := f.sent("europe-west4-a", "burst-a")["scheduling"].(map[string]any)
	if sched["provisioningModel"] != "SPOT" || sched["instanceTerminationAction"] != "DELETE" || sched["automaticRestart"] != false {
		t.Errorf("scheduling = %v", sched)
	}
}

func TestTheImageNetworkDiskAndSubnetworkCanBeChosen(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Settings = map[string]string{
		SettingNetwork: "vpc-1", SettingSubnetwork: "nodes", SettingImage: "global/images/family/mine",
		SettingDiskSizeGB: "80", SettingDiskType: "pd-ssd",
	}
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	sent := f.sent("europe-west4-a", "burst-a")
	params := sent["disks"].([]any)[0].(map[string]any)["initializeParams"].(map[string]any)
	nic := sent["networkInterfaces"].([]any)[0].(map[string]any)
	if params["sourceImage"] != "global/images/family/mine" || params["diskSizeGb"] != "80" || params["diskType"] != "zones/europe-west4-a/diskTypes/pd-ssd" {
		t.Errorf("boot disk = %v", params)
	}
	if nic["network"] != "global/networks/vpc-1" || nic["subnetwork"] != "regions/europe-west4/subnetworks/nodes" {
		t.Errorf("nic = %v", nic)
	}
	// The spec's own image beats the setting.
	s2 := spec("burst-b")
	s2.Image = "projects/debian-cloud/global/images/family/debian-12"
	s2.Settings = map[string]string{SettingImage: "global/images/family/mine", SettingNetwork: "vpc-1"}
	if _, err := p.Create(context.Background(), s2); err != nil {
		t.Fatal(err)
	}
	got := f.sent("europe-west4-a", "burst-b")["disks"].([]any)[0].(map[string]any)["initializeParams"].(map[string]any)["sourceImage"]
	if got != "projects/debian-cloud/global/images/family/debian-12" {
		t.Errorf("sourceImage = %v", got)
	}
}

func TestAnArmMachineTypeGetsTheArmImage(t *testing.T) {
	f, p := newFake(t)
	f.archOf["t2a-standard-2"] = "ARM64"
	s := spec("burst-a")
	s.Size = "t2a-standard-2"
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	got := f.sent("europe-west4-a", "burst-a")["disks"].([]any)[0].(map[string]any)["initializeParams"].(map[string]any)["sourceImage"]
	if got != defaultImageARM64 {
		t.Errorf("sourceImage = %v, want the arm64 image", got)
	}
}

func TestARepeatedCreateReturnsTheMachineInsteadOfASecond(t *testing.T) {
	f, p := newFake(t)
	first, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	f.run("europe-west4-a", "burst-a", "34.90.0.7")
	again, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || f.inserts != 1 {
		t.Errorf("the repeat made a second insert: %d inserts, %s vs %s", f.inserts, again.ID, first.ID)
	}
	if again.PublicIPv4 != "34.90.0.7" || again.State != compute.StateRunning {
		t.Errorf("the repeat did not return the machine as it is now: %+v", again)
	}
}

func TestARaceForTheNameEndsInTheOwnersMachine(t *testing.T) {
	f, p := newFake(t)
	// The machine appears between the look-up and the insert: the insert is answered with 409 and the machine is found.
	var once sync.Once
	f.failInsert = func() (int, map[string]any) {
		once.Do(func() {
			f.instances["europe-west4-a/burst-a"] = map[string]any{
				"id": "9", "name": "burst-a", "status": "PROVISIONING", "_zone": "europe-west4-a",
				"zone": ".../zones/europe-west4-a", "machineType": ".../machineTypes/e2-standard-4",
				"labels": map[string]any{"geass-cluster": "c1", "geass-managed": "true"},
			}
		})
		return 409, map[string]any{"error": map[string]any{"code": 409, "message": "exists", "errors": []map[string]any{{"reason": "alreadyExists", "message": "exists"}}}}
	}
	got, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "europe-west4-a/burst-a" {
		t.Errorf("got %+v", got)
	}
}

func TestACreateThatMeetsAnotherClustersNameIsAnErrorNotAnAdoption(t *testing.T) {
	_, p := newFake(t)
	other := spec("burst-a")
	other.Labels = compute.Owner{Cluster: "someone-else", Pool: "burst"}.Labels("burst-a")
	if _, err := p.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Create(context.Background(), spec("burst-a")); err == nil {
		t.Fatal("a machine of another cluster was taken for ours")
	}
}

func TestCreateRefusesWhatCannotBeAZoneOrAName(t *testing.T) {
	_, p := newFake(t)
	for _, region := range []string{"europe-west4", "nonsense"} {
		s := spec("burst-a")
		s.Region = region
		if _, err := p.Create(context.Background(), s); err == nil || !strings.Contains(err.Error(), "zone") {
			t.Errorf("region %q: err = %v", region, err)
		}
	}
	s := spec("Burst_A")
	if _, err := p.Create(context.Background(), s); err == nil {
		t.Error("a name Compute Engine would refuse was sent")
	}
}

func TestTheFirewallLetsInOnlyWhatTheClusterNeeds(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if len(f.firewalls) != 2 {
		t.Fatalf("%d firewall rules, want 2", len(f.firewalls))
	}
	control := f.firewalls["geass-c1"]
	if control["direction"] != "INGRESS" || fmt.Sprint(control["sourceRanges"]) != "[203.0.113.5/32]" || fmt.Sprint(control["targetTags"]) != "[geass-c1]" {
		t.Errorf("control rule = %v", control)
	}
	allowed, _ := json.Marshal(control["allowed"])
	if string(allowed) != `[{"IPProtocol":"tcp","ports":["22","10250"]}]` {
		t.Errorf("allowed = %s: SSH and the kubelet only", allowed)
	}
	overlay := f.firewalls["geass-c1-overlay"]
	allowed, _ = json.Marshal(overlay["allowed"])
	if string(allowed) != `[{"IPProtocol":"udp","ports":["51820"]}]` || fmt.Sprint(overlay["sourceRanges"]) != "[0.0.0.0/0]" || overlay["disabled"] != false {
		t.Errorf("overlay rule = %v", overlay)
	}
	if !strings.HasPrefix(control["description"].(string), firewallMarker) {
		t.Error("the rule is not marked as Geass's, so it could not be told from somebody else's later")
	}
}

func TestWithoutAControlPlaneAddressSSHIsFromAnywhereAndTheKubeletIsClosed(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.ControlPlaneCIDRs = nil
	s.MultiSite = false
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	control := f.firewalls["geass-c1"]
	allowed, _ := json.Marshal(control["allowed"])
	if string(allowed) != `[{"IPProtocol":"tcp","ports":["22"]}]` || fmt.Sprint(control["sourceRanges"]) != "[0.0.0.0/0]" {
		t.Errorf("control rule = %v", control)
	}
	// No multi-site: the overlay rule is not even made.
	if _, made := f.firewalls["geass-c1-overlay"]; made {
		t.Error("the overlay port was opened in a cluster that is not multi-site")
	}
}

func TestTheFirewallIsMadeOnceAndItsRulesAreKeptCurrent(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	next := spec("burst-b")
	next.ControlPlaneCIDRs = []string{"203.0.113.77/32"} // the control plane moved
	next.MultiSite = false                               // and the cluster is no longer multi-site
	if _, err := p.Create(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if len(f.firewalls) != 2 {
		t.Fatalf("%d rules, want the two to be reused", len(f.firewalls))
	}
	if got := fmt.Sprint(f.firewalls["geass-c1"]["sourceRanges"]); got != "[203.0.113.77/32]" {
		t.Errorf("the rule still allows the old control plane: %s", got)
	}
	if f.firewalls["geass-c1-overlay"]["disabled"] != true {
		t.Error("the overlay port stayed open after the cluster stopped needing it")
	}
	before := len(f.requests)
	if _, err := p.Create(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.requests[before:] {
		if strings.HasPrefix(r, "PATCH") || (strings.HasPrefix(r, "POST") && strings.HasSuffix(r, "firewalls")) {
			t.Errorf("a rule that was current was written again: %s", r)
		}
	}
}

func TestAnIPv6OnlyControlPlaneIsExplained(t *testing.T) {
	_, p := newFake(t)
	s := spec("burst-a")
	s.ControlPlaneCIDRs = []string{"2001:db8::/32"}
	if _, err := p.Create(context.Background(), s); err == nil || !strings.Contains(err.Error(), "IPv4") {
		t.Errorf("err = %v", err)
	}
}

func TestAFirewallTagTheUserNamesIsUsedAsItIs(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Settings = map[string]string{SettingFirewallTag: "my-nodes"}
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(f.firewalls) != 0 {
		t.Error("Geass made rules although a tag was named")
	}
	if tags := f.sent("europe-west4-a", "burst-a")["tags"].(map[string]any)["items"].([]any); len(tags) != 1 || tags[0] != "my-nodes" {
		t.Errorf("tags = %v", tags)
	}
}

func TestSkipFirewallMakesNoRulesAndAddsNoTag(t *testing.T) {
	f, p := newFake(t)
	f.firewallForbidden = true
	s := spec("burst-a")
	s.Settings = map[string]string{SettingSkipFW: "true"}
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, set := f.sent("europe-west4-a", "burst-a")["tags"]; set || len(f.firewalls) != 0 {
		t.Error("a firewall was involved although it was skipped")
	}
}

func TestAServiceAccountThatMayNotMakeFirewallRulesIsToldWhichPermissionToGrant(t *testing.T) {
	f, p := newFake(t)
	f.firewallForbidden = true
	_, err := p.Create(context.Background(), spec("burst-a"))
	if err == nil {
		t.Fatal("a machine was made that nothing could reach")
	}
	for _, want := range []string{"compute.firewalls.create", "roles/compute.securityAdmin", SettingSkipFW} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q: %v", want, err)
		}
	}
	if !errors.Is(err, compute.ErrAuth) {
		t.Error("a missing permission is not an auth error")
	}
	if len(f.instances) != 0 {
		t.Error("the machine was created before the firewall failed")
	}
}

func TestAnUnrelatedRuleWithTheNameIsNotTouched(t *testing.T) {
	f, p := newFake(t)
	f.firewalls["geass-c1"] = map[string]any{"name": "geass-c1", "description": "mine", "network": ".../global/networks/default"}
	if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !strings.Contains(err.Error(), "not Geass's") {
		t.Errorf("err = %v", err)
	}
	if f.firewalls["geass-c1"]["description"] != "mine" {
		t.Error("somebody else's rule was changed")
	}
}

func TestOperationFailuresBecomeTheErrorsCallersActOn(t *testing.T) {
	for name, tc := range map[string]struct {
		code, message string
		check         func(error) bool
	}{
		"quota": {"QUOTA_EXCEEDED", "Quota 'CPUS' exceeded.  Limit: 8.0 in region europe-west4.", compute.IsQuota},
		"stockout": {"ZONE_RESOURCE_POOL_EXHAUSTED", "The zone 'projects/proj-1/zones/europe-west4-a' does not have enough resources available to fulfill the request.",
			func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }},
		"stockout with details": {"ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS", "unavailable",
			func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }},
		"rate": {"RESOURCE_OPERATION_RATE_EXCEEDED", "slow down",
			func(err error) bool { var r *compute.RateLimitError; return errors.As(err, &r) }},
	} {
		t.Run(name, func(t *testing.T) {
			f, p := newFake(t)
			f.opOnInsert = func() map[string]any {
				return map[string]any{"errors": []map[string]any{{"code": tc.code, "message": tc.message}}}
			}
			if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !tc.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestACreateWatchesTheOperationLongEnoughToSeeAQuotaFailure(t *testing.T) {
	f, p := newFake(t)
	// The insert is accepted (RUNNING); the quota failure shows on the second look at the Operation.
	f.opAfterPolls = func() map[string]any {
		return f.operation("DONE", map[string]any{"errors": []map[string]any{{"code": "QUOTA_EXCEEDED", "message": "Quota 'CPUS' exceeded."}}})
	}
	if _, err := p.Create(context.Background(), spec("burst-a")); !compute.IsQuota(err) {
		t.Errorf("err = %v, want a quota error", err)
	}
}

func TestAMachineThatIsMerelySlowToBootIsNotAFailure(t *testing.T) {
	f, p := newFake(t)
	f.opAfterPolls = func() map[string]any { return f.operation("RUNNING", nil) }
	got, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil || got.State != compute.StatePending {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestHTTPFailuresBecomeTheErrorsCallersActOn(t *testing.T) {
	body := func(status int, reason, message string) func() (int, map[string]any) {
		return func() (int, map[string]any) {
			return status, map[string]any{"error": map[string]any{"code": status, "message": message, "errors": []map[string]any{{"reason": reason, "message": message}}}}
		}
	}
	for name, tc := range map[string]struct {
		fail  func() (int, map[string]any)
		check func(error) bool
	}{
		"quota": {body(403, "quotaExceeded", "Quota 'INSTANCES' exceeded. Limit: 24.0"), compute.IsQuota},
		"quota as 429": {body(429, "quotaExceeded", "Quota exceeded for quota metric"),
			compute.IsQuota},
		"stockout": {body(503, "unavailable", "The zone 'x' does not have enough resources available to fulfill the request."),
			func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }},
		"rate limit": {body(403, "rateLimitExceeded", "Rate Limit Exceeded"),
			func(err error) bool { var r *compute.RateLimitError; return errors.As(err, &r) }},
		"forbidden": {body(403, "forbidden", "Required 'compute.instances.create' permission"),
			func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"unauthorized": {body(401, "authError", "Invalid Credentials"),
			func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
	} {
		t.Run(name, func(t *testing.T) {
			f, p := newFake(t)
			f.failInsert = tc.fail
			if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !tc.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestListFindsOnlyTheOwnersMachinesInEveryZoneAndEveryPage(t *testing.T) {
	f, p := newFake(t)
	f.pageSize = 2
	for _, tc := range []struct{ zone, name, cluster, pool string }{
		{"europe-west4-a", "burst-a", "c1", "burst"},
		{"europe-west4-b", "burst-b", "c1", "burst"},
		{"us-central1-a", "burst-c", "c1", "burst"},
		{"europe-west4-a", "web-a", "c1", "web"},
		{"europe-west4-a", "theirs", "someone-else", "burst"},
	} {
		s := spec(tc.name)
		s.Region = tc.zone
		s.Labels = compute.Owner{Cluster: tc.cluster, Pool: tc.pool}.Labels(tc.name)
		s.Settings = map[string]string{SettingSkipFW: "true"}
		if _, err := p.Create(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	f.instances["europe-west4-a/stranger"] = map[string]any{"name": "stranger", "_zone": "europe-west4-a", "zone": ".../zones/europe-west4-a", "labels": nil}

	burst, err := p.List(context.Background(), compute.Owner{Cluster: "c1", Pool: "burst"})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(burst))
	for _, i := range burst {
		names = append(names, i.ID)
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "europe-west4-a/burst-a,europe-west4-b/burst-b,us-central1-a/burst-c" {
		t.Errorf("listed %v", names)
	}
	all, _ := p.List(context.Background(), compute.Owner{Cluster: "c1"})
	if len(all) != 4 {
		t.Errorf("the cluster's machines: %d, want 4", len(all))
	}
}

func TestAListFilterThatIsRefusedOrMatchesNothingDoesNotHideMachines(t *testing.T) {
	for name, set := range map[string]func(*fakeGCP){
		"refused":         func(f *fakeGCP) { f.rejectFilter = true },
		"matches nothing": func(f *fakeGCP) { f.filterMatchesNothing = true },
	} {
		t.Run(name, func(t *testing.T) {
			f, p := newFake(t)
			if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
				t.Fatal(err)
			}
			set(f)
			got, err := p.List(context.Background(), compute.Owner{Cluster: "c1", Pool: "burst"})
			if err != nil || len(got) != 1 {
				t.Errorf("listed %v, %v", got, err)
			}
		})
	}
}

func TestDeleteIsIdempotentAndGetReportsAGoneMachine(t *testing.T) {
	_, p := newFake(t)
	created, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Errorf("deleting again: %v", err)
	}
	if err := p.Delete(context.Background(), "garbage"); err != nil {
		t.Errorf("deleting an id that names nothing: %v", err)
	}
	if _, err := p.Get(context.Background(), created.ID); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
	if _, err := p.Get(context.Background(), "garbage"); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get of a malformed id = %v, want ErrNotFound", err)
	}
}

func TestTheBootDiskAndAddressGoWithTheMachine(t *testing.T) {
	// Compute Engine removes a boot disk only when autoDelete is set, and an ephemeral address only when it is not
	// reserved: Create must ask for the first and never for the second.
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	sent := f.sent("europe-west4-a", "burst-a")
	if sent["disks"].([]any)[0].(map[string]any)["autoDelete"] != true {
		t.Error("the boot disk would outlive the machine")
	}
	if _, reserved := sent["networkInterfaces"].([]any)[0].(map[string]any)["accessConfigs"].([]any)[0].(map[string]any)["natIP"]; reserved {
		t.Error("a reserved address was named: it would outlive the machine")
	}
}

func TestAMachinesStateIsTranslated(t *testing.T) {
	for status, want := range map[string]compute.State{
		"PROVISIONING": compute.StatePending, "STAGING": compute.StatePending, "REPAIRING": compute.StatePending,
		"RUNNING": compute.StateRunning, "STOPPED": compute.StateStopped, "TERMINATED": compute.StateStopped, "SUSPENDED": compute.StateStopped,
		"STOPPING": compute.StateTerminating,
	} {
		if got := stateOf(status); got != want {
			t.Errorf("%s -> %s, want %s", status, got, want)
		}
	}
}

func TestGetReadsTheAddressesAndTheState(t *testing.T) {
	f, p := newFake(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	f.run("europe-west4-a", "burst-a", "34.90.0.7")
	got, err := p.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Ready() || got.PublicIPv4 != "34.90.0.7" || got.PrivateIPv4 != "10.164.0.5" || got.Size != "e2-standard-4" || got.Region != "europe-west4-a" {
		t.Errorf("got %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("the creation time was not read")
	}
}

func TestSizesAreSmallestFirstWithoutRetiredOrGPUTypes(t *testing.T) {
	_, p := newFake(t)
	sizes, err := p.Sizes(context.Background(), "europe-west4-a")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(sizes))
	for _, s := range sizes {
		ids = append(ids, s.ID)
	}
	// 2 vCPU: e2-medium (4 GB) before t2a-standard-2 (8 GB); then 4 vCPU.
	if strings.Join(ids, ",") != "e2-medium,t2a-standard-2,e2-standard-4" {
		t.Fatalf("sizes = %v", ids)
	}
	if sizes[0].VCPUs != 2 || sizes[0].MemoryMiB != 4096 || sizes[0].Arch != "amd64" || sizes[0].HourlyPrice != 0 {
		t.Errorf("size = %+v", sizes[0])
	}
	if sizes[1].Arch != "arm64" {
		t.Errorf("t2a arch = %s", sizes[1].Arch)
	}
	if _, err := New(testServiceAccountKey(t), compute.Options{BaseURL: p.api.BaseURL}); err != nil {
		t.Fatal(err)
	}
	noZone, _ := New(testServiceAccountKey(t), compute.Options{BaseURL: p.api.BaseURL})
	if _, err := noZone.Sizes(context.Background(), ""); err == nil {
		t.Error("sizes were listed without a zone")
	}
}

func TestVerifyIsAHarmlessReadAndWarnsAboutFirewallRights(t *testing.T) {
	f, p := newFake(t)
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(account.Name, "proj-1") || len(account.Warnings) == 0 {
		t.Errorf("account = %+v", account)
	}
	for _, r := range f.requests {
		if !strings.HasPrefix(r, "GET") {
			t.Errorf("Verify changed something: %s", r)
		}
	}
	if len(f.instances) != 0 || len(f.firewalls) != 0 {
		t.Error("Verify created something")
	}
}

func TestTheProviderRegistersItself(t *testing.T) {
	d, ok := compute.Lookup(ID)
	if !ok || d.ID != "GCP" || len(d.Fields) != 1 || d.Fields[0].Key != "serviceAccountKey" || !d.Fields[0].Secret || !d.Fields[0].Multiline {
		t.Fatalf("descriptor = %+v", d)
	}
	if !strings.Contains(d.Summary, "disableServiceAccountKeyCreation") {
		t.Error("the summary does not warn that new organisations block service account keys")
	}
	for _, key := range []string{SettingNetwork, SettingSubnetwork, SettingImage, SettingDiskSizeGB, SettingDiskType, SettingFirewallTag, SettingSkipFW} {
		if !d.KnownSetting(key) {
			t.Errorf("setting %s is not described", key)
		}
	}
	if _, err := compute.New(ID, compute.Credentials{}, compute.Options{}); err == nil {
		t.Error("a provider was built without a key")
	}
	if _, err := compute.New(ID, compute.Credentials{keyServiceAccount: testServiceAccountKey(t)}, compute.Options{}); err != nil {
		t.Error(err)
	}
	if _, err := compute.New(ID, compute.Credentials{keyServiceAccount: "{}"}, compute.Options{}); err == nil {
		t.Error("a provider was built from an empty key file")
	}
}
