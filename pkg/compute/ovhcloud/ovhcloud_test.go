package ovhcloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/degoke/geass/pkg/compute"
)

const (
	testProject = "0123456789abcdef0123456789abcdef"
	testSecret  = "s3cret"
)

// fakeOVH is the part of the OVHcloud API, and of its OAuth2 token endpoint, that Geass uses, in memory.
type fakeOVH struct {
	t *testing.T

	mu        sync.Mutex
	instances map[string]map[string]any
	order     []string
	next      int
	creates   int
	tokens    int
	requests  []string
	queries   map[string]url.Values // by "METHOD path"

	// tokenLifetime is the expires_in the token endpoint answers with.
	tokenLifetime int
	flavors       []map[string]any
	images        []map[string]any
	networks      []map[string]any
	regions       []string
	quotas        []map[string]any
	project       map[string]any
	lastCreate    map[string]any
	// failCreate, when set, is the status and body of the next create.
	failCreate func() (int, map[string]any)
	// deleteWhileDeleting is the status a second delete of an instance that is being deleted gets.
	deleteWhileDeleting int
	// refuseDelete, when set, is the status every delete is refused with.
	refuseDelete int
}

func newFake(t *testing.T) (*fakeOVH, *Provider) {
	f := &fakeOVH{
		t: t, instances: map[string]map[string]any{}, queries: map[string]url.Values{}, tokenLifetime: 3599,
		flavors: []map[string]any{
			{"id": "f-gra-b3-8", "name": "b3-8", "type": "ovh.cpu", "osType": "linux", "region": "GRA11", "vcpus": 2, "ram": 8000, "disk": 50, "available": true, "quota": 5},
			{"id": "f-gra-d2-4", "name": "d2-4", "type": "ovh.ssd.eg", "osType": "linux", "region": "GRA11", "vcpus": 2, "ram": 4000, "disk": 50, "available": true, "quota": 5},
			{"id": "f-gra-c3-16", "name": "c3-16", "type": "ovh.cpu", "osType": "linux", "region": "GRA11", "vcpus": 4, "ram": 16000, "disk": 100, "available": false, "quota": 5},
			{"id": "f-gra-r3-32", "name": "r3-32", "type": "ovh.ram", "osType": "linux", "region": "GRA11", "vcpus": 8, "ram": 32, "disk": 200, "available": true, "quota": 0},
			{"id": "f-gra-win", "name": "win-b3-8", "type": "ovh.cpu", "osType": "windows", "region": "GRA11", "vcpus": 2, "ram": 8000, "disk": 50, "available": true, "quota": 5},
			{"id": "f-gra-t1", "name": "t1-45", "type": "ovh.ssd.gpu", "osType": "linux", "region": "GRA11", "vcpus": 8, "ram": 45000, "disk": 400, "available": true, "quota": 5},
			{"id": "f-bhs-b3-8", "name": "b3-8", "type": "ovh.cpu", "osType": "linux", "region": "BHS5", "vcpus": 2, "ram": 8000, "disk": 50, "available": true, "quota": 5},
		},
		images: []map[string]any{
			{"id": "img-gra-2204", "name": "Ubuntu 22.04", "region": "GRA11", "status": "active", "visibility": "public"},
			{"id": "img-gra-2404-docker", "name": "Ubuntu 24.04 - Docker", "region": "GRA11", "status": "active", "visibility": "public"},
			{"id": "img-gra-2404", "name": "Ubuntu 24.04", "region": "GRA11", "status": "active", "visibility": "public"},
			{"id": "img-gra-2404-private", "name": "Ubuntu 24.04", "region": "GRA11", "status": "active", "visibility": "private"},
			{"id": "img-gra-2404-uefi", "name": "Ubuntu 24.04 UEFI", "region": "GRA11", "status": "active", "visibility": "public", "flavorType": "ovh.uefi"},
			{"id": "11111111-2222-3333-4444-555555555555", "name": "Debian 12", "region": "GRA11", "status": "active", "visibility": "public"},
			{"id": "img-bhs-2404", "name": "Ubuntu 24.04", "region": "BHS5", "status": "active", "visibility": "public"},
		},
		networks: []map[string]any{{"id": "net-ext", "name": "Ext-Net", "type": "public", "regions": []map[string]any{
			{"region": "GRA11", "status": "ACTIVE"}, {"region": "BHS5", "status": "ACTIVE"},
		}}},
		regions: []string{"GRA11", "BHS5"},
		quotas: []map[string]any{
			{"region": "GRA11", "instance": map[string]any{"maxInstances": 10, "usedInstances": 1}},
			{"region": "BHS5", "instance": map[string]any{"maxInstances": 10, "usedInstances": 1}},
		},
		project: map[string]any{"project_id": testProject, "description": "geass-project", "status": "ok", "expiration": nil},
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	p, err := New(compute.Credentials{keyClientID: "client-1", keyClientSecret: testSecret, keyServiceName: testProject}, compute.Options{BaseURL: srv.URL, Region: "GRA11"})
	if err != nil {
		t.Fatal(err)
	}
	return f, p
}

func (f *fakeOVH) reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeOVH) fail(w http.ResponseWriter, status int, class, message string) {
	f.reply(w, status, map[string]string{"class": class, "message": message})
}

func (f *fakeOVH) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/auth/oauth2/token" {
		f.serveToken(w, r)
		return
	}
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	f.queries[r.Method+" "+r.URL.Path] = r.URL.Query()
	if r.Header.Get("Authorization") != "Bearer token-"+strconv.Itoa(f.tokens) {
		f.fail(w, 401, "Client::Unauthorized", "invalid or expired credentials")
		return
	}
	base := "/cloud/project/" + testProject
	if !strings.HasPrefix(r.URL.Path, base) {
		f.fail(w, 404, "Client::NotFound", "no route "+r.URL.Path)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, base)
	region := r.URL.Query().Get("region")
	switch {
	case r.Method == "GET" && path == "":
		f.reply(w, 200, f.project)
	case r.Method == "GET" && path == "/region":
		f.reply(w, 200, f.regions)
	case r.Method == "GET" && path == "/quota":
		f.reply(w, 200, f.quotas)
	case r.Method == "GET" && path == "/flavor":
		f.reply(w, 200, filterRegion(f.flavors, region))
	case r.Method == "GET" && path == "/image":
		f.reply(w, 200, filterRegion(f.images, region))
	case r.Method == "GET" && path == "/network/public":
		f.reply(w, 200, f.networks)
	case r.Method == "POST" && path == "/instance":
		f.create(w, r)
	case r.Method == "GET" && path == "/instance":
		var out []map[string]any
		for _, id := range f.order {
			if inst, ok := f.instances[id]; ok && (region == "" || inst["region"] == region) {
				out = append(out, inst)
			}
		}
		if out == nil {
			out = []map[string]any{}
		}
		f.reply(w, 200, out)
	case strings.HasPrefix(path, "/instance/"):
		f.instance(w, r, strings.TrimPrefix(path, "/instance/"))
	default:
		f.fail(w, 404, "Client::NotFound", "no route "+r.Method+" "+r.URL.Path)
	}
}

func filterRegion(items []map[string]any, region string) []map[string]any {
	out := []map[string]any{}
	for _, i := range items {
		if region == "" || i["region"] == region {
			out = append(out, i)
		}
	}
	return out
}

func (f *fakeOVH) serveToken(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if r.Method != "POST" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") ||
		r.PostForm.Get("grant_type") != "client_credentials" || r.PostForm.Get("client_id") != "client-1" || r.PostForm.Get("scope") != "all" {
		f.fail(w, 400, "invalid_request", "bad token request")
		return
	}
	if r.PostForm.Get("client_secret") != testSecret {
		f.reply(w, 400, map[string]string{"error": "invalid_client", "error_description": "bad secret"})
		return
	}
	f.tokens++
	f.reply(w, 200, map[string]any{"access_token": "token-" + strconv.Itoa(f.tokens), "token_type": "Bearer", "expires_in": f.tokenLifetime, "scope": "all"})
}

func (f *fakeOVH) create(w http.ResponseWriter, r *http.Request) {
	f.creates++
	if f.failCreate != nil {
		status, body := f.failCreate()
		f.reply(w, status, body)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.lastCreate = body
	f.next++
	id := fmt.Sprintf("00000000-0000-0000-0000-%012d", f.next)
	inst := map[string]any{
		"id": id, "name": body["name"], "status": "BUILD", "region": body["region"], "flavorId": body["flavorId"],
		"imageId": body["imageId"], "created": "2026-10-19T10:00:00Z", "ipAddresses": []any{},
	}
	f.instances[id] = inst
	f.order = append(f.order, id)
	f.reply(w, 200, inst)
}

func (f *fakeOVH) instance(w http.ResponseWriter, r *http.Request, id string) {
	inst, ok := f.instances[id]
	if !ok {
		f.fail(w, 404, "Client::NotFound", "This instance does not exist")
		return
	}
	if r.Method == "DELETE" {
		if f.refuseDelete != 0 {
			f.fail(w, f.refuseDelete, "Client::BadRequest", "the instance is locked")
			return
		}
		if inst["status"] == "DELETING" {
			f.fail(w, f.deleteWhileDeleting, "Client::BadRequest", "Instance is being deleted")
			return
		}
		inst["status"] = "DELETING"
		w.WriteHeader(200)
		return
	}
	f.reply(w, 200, inst)
}

func (f *fakeOVH) activate(id, publicIP string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	inst := f.instances[id]
	inst["status"] = "ACTIVE"
	inst["ipAddresses"] = []any{
		map[string]any{"ip": "2001:db8::7", "type": "public", "version": 6},
		map[string]any{"ip": publicIP, "type": "public", "version": 4},
		map[string]any{"ip": "10.1.0.7", "type": "private", "version": 4},
	}
}

func (f *fakeOVH) status(id, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instances[id]["status"] = status
}

func spec(name string) compute.InstanceSpec {
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	return compute.InstanceSpec{
		Name: name, Region: "GRA11", Size: "b3-8", UserData: "#cloud-config\nhostname: x\n", Labels: owner.Labels(name),
	}
}

func TestTheServiceAccountIsTradedForATokenThatIsReusedAndRenewed(t *testing.T) {
	f, p := newFake(t)
	clock := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return clock }
	// The fake checks the form (client_credentials, the client id and secret, scope=all) and the Bearer on each call.
	for range 3 {
		if _, err := p.Verify(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.tokens != 1 {
		t.Fatalf("%d tokens for three calls, want one reused", f.tokens)
	}
	clock = clock.Add(50 * time.Minute)
	if _, err := p.Verify(context.Background()); err != nil || f.tokens != 1 {
		t.Fatalf("50 minutes in: %v, %d tokens", err, f.tokens)
	}
	clock = clock.Add(8 * time.Minute) // 58 minutes: inside the margin before the 59 minutes the token lasts
	if _, err := p.Verify(context.Background()); err != nil || f.tokens != 2 {
		t.Fatalf("58 minutes in: %v, %d tokens, want a renewed one", err, f.tokens)
	}
}

func TestAShortLivedTokenIsRenewedBeforeItEnds(t *testing.T) {
	f, p := newFake(t)
	f.tokenLifetime = 60
	clock := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return clock }
	if _, err := p.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(31 * time.Second) // half the lifetime is the margin
	if _, err := p.Verify(context.Background()); err != nil || f.tokens != 2 {
		t.Errorf("%v, %d tokens", err, f.tokens)
	}
}

func TestARefusedServiceAccountIsAnAuthError(t *testing.T) {
	_, p := newFake(t)
	p.clientSecret = "wrong"
	if _, err := p.Verify(context.Background()); !errors.Is(err, compute.ErrAuth) || !strings.Contains(err.Error(), "bad secret") {
		t.Errorf("Verify = %v", err)
	}
}

func TestTheEndpointPicksTheAPIAndTheTokenAddress(t *testing.T) {
	for endpoint, want := range map[string][2]string{
		"":       {"https://eu.api.ovh.com/v1", "https://www.ovh.com/auth/oauth2/token"},
		"eu":     {"https://eu.api.ovh.com/v1", "https://www.ovh.com/auth/oauth2/token"},
		"ovh-eu": {"https://eu.api.ovh.com/v1", "https://www.ovh.com/auth/oauth2/token"},
		"CA":     {"https://ca.api.ovh.com/v1", "https://ca.ovh.com/auth/oauth2/token"},
		"us":     {"https://api.us.ovhcloud.com/v1", "https://us.ovhcloud.com/auth/oauth2/token"},
	} {
		p, err := New(compute.Credentials{keyClientID: "c", keyClientSecret: "s", keyServiceName: testProject, keyEndpoint: endpoint}, compute.Options{})
		if err != nil {
			t.Fatalf("endpoint %q: %v", endpoint, err)
		}
		tokenURL := p.tokens.BaseURL + p.tokenPath
		if p.api.BaseURL != want[0] || tokenURL != want[1] {
			t.Errorf("endpoint %q: api %s token %s, want %v", endpoint, p.api.BaseURL, tokenURL, want)
		}
	}
	for _, bad := range []string{"asia", "ovh-xx"} {
		if _, err := New(compute.Credentials{keyClientID: "c", keyClientSecret: "s", keyServiceName: testProject, keyEndpoint: bad}, compute.Options{}); err == nil {
			t.Errorf("endpoint %q was accepted", bad)
		}
	}
	if _, err := New(compute.Credentials{keyClientID: "c", keyClientSecret: "s", keyServiceName: "a/b"}, compute.Options{}); err == nil {
		t.Error("a project id that is a path was accepted")
	}
}

func TestAMachinesNameCarriesItsOwnerAndComesBackOutOfIt(t *testing.T) {
	for _, tc := range []struct {
		node, name string
		attempt    int
	}{
		{"burst-x7k2m", "burst-x7k2m", 0},
		{"burst-x7k2m", "burst-x7k2m-2", 2},
		// A node whose own name ends in a number is not mistaken for a retry.
		{"worker-1", "worker-1", 0},
		{"worker-1", "worker-1-3", 3},
	} {
		s := spec(tc.name)
		s.Labels = compute.Owner{Cluster: "cluster-uid-1", Pool: "burst"}.Labels(tc.node)
		m, err := nameFor(s)
		if err != nil {
			t.Fatal(err)
		}
		parsed, ok := parseName(m.String())
		if !ok || parsed.node != tc.node || parsed.attempt != tc.attempt || parsed.name() != tc.name {
			t.Errorf("%s (node %s): %s -> %+v", tc.name, tc.node, m, parsed)
		}
		if parsed.cluster != clusterHash("cluster-uid-1") || parsed.pool != poolHash("burst") {
			t.Errorf("hashes in %s", m)
		}
	}
	// A name that is neither the node nor the node and a number is carried whole, as the node.
	odd := spec("custom-name")
	odd.Labels = compute.Owner{Cluster: "c1", Pool: "burst"}.Labels("other-node")
	m, err := nameFor(odd)
	if err != nil || m.node != "custom-name" || m.attempt != 0 {
		t.Errorf("odd name: %+v, %v", m, err)
	}
}

func TestNamesThatCannotBeCarriedAreRefused(t *testing.T) {
	for _, name := range []string{"", "Upper", "under_score", "dot.ted", "sp ace", strings.Repeat("a", 232)} {
		s := spec(name)
		if _, err := nameFor(s); err == nil {
			t.Errorf("name %q was accepted", name)
		}
	}
	s := spec("burst-a")
	s.Labels = nil
	if _, err := nameFor(s); err == nil {
		t.Error("a spec without a cluster was accepted: its machine could never be found again")
	}
	if got := len(machineName{cluster: clusterHash("c"), pool: poolHash("p"), node: ""}.String()); got != namePrefixLength {
		t.Errorf("the prefix is %d characters, the constant says %d", got, namePrefixLength)
	}
}

func TestOnlyNamesOfTheShapeAreGeass(t *testing.T) {
	for _, name := range []string{"geass", "geass-test", "web-1", "geass-ZZZZZZZZ-000000-0-x", "geass-0123456-000000-0-x", "geass-01234567-000000-0-"} {
		if _, ok := parseName(name); ok {
			t.Errorf("%q was taken for a Geass machine", name)
		}
	}
}

func TestCreateSendsTheMachineWithTheOwnersNameAndAPublicAddress(t *testing.T) {
	f, p := newFake(t)
	instance, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	sent := f.lastCreate
	wantName := "geass-" + clusterHash("c1") + "-" + poolHash("burst") + "-0-burst-a"
	if sent["name"] != wantName || sent["region"] != "GRA11" || sent["flavorId"] != "f-gra-b3-8" || sent["imageId"] != "img-gra-2404" {
		t.Errorf("request = %v", sent)
	}
	if sent["monthlyBilling"] != false {
		t.Error("the machine would be billed monthly")
	}
	if nets := sent["networks"].([]any); len(nets) != 1 || nets[0].(map[string]any)["networkId"] != "net-ext" {
		t.Errorf("networks = %v", sent["networks"])
	}
	if sent["userData"] != "#cloud-config\nhostname: x\n" {
		t.Errorf("userData = %q, want the cloud-init document untouched", sent["userData"])
	}
	if _, set := sent["sshKeyId"]; set {
		t.Error("an SSH key was named: the cloud-init user is the way in")
	}
	if instance.Name != "burst-a" || instance.State != compute.StatePending || instance.PublicIPv4 != "" || instance.Region != "GRA11" || instance.Size != "b3-8" {
		t.Errorf("instance = %+v", instance)
	}
	if instance.Labels[compute.LabelNode] != "burst-a" || instance.Labels[compute.LabelCluster] != "c1" || instance.Labels[compute.LabelPool] != "burst" || instance.Labels[compute.LabelManaged] != "true" {
		t.Errorf("labels = %v", instance.Labels)
	}
}

func TestUserDataCanBeSentBase64Encoded(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Settings = map[string]string{SettingUserDataBase64: "true"}
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(f.lastCreate["userData"].(string))
	if err != nil || string(raw) != "#cloud-config\nhostname: x\n" {
		t.Errorf("userData = %v: %q, %v", f.lastCreate["userData"], raw, err)
	}
}

func TestTheImageAndNetworkAreChosenFromTheRegionsOwnLists(t *testing.T) {
	f, p := newFake(t)
	// The plain Ubuntu 24.04 wins over the Docker, UEFI-only and private ones; Bhs has its own ids.
	s := spec("burst-a")
	s.Region = "BHS5"
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if f.lastCreate["imageId"] != "img-bhs-2404" || f.lastCreate["flavorId"] != "f-bhs-b3-8" {
		t.Errorf("request = %v", f.lastCreate)
	}
	// The setting beats the lookup; so does a machine's image given as an id, or as a name.
	s2 := spec("burst-b")
	s2.Settings = map[string]string{SettingImageID: "img-mine", SettingNetworkID: "net-other"}
	if _, err := p.Create(context.Background(), s2); err != nil {
		t.Fatal(err)
	}
	if f.lastCreate["imageId"] != "img-mine" || f.lastCreate["networks"].([]any)[0].(map[string]any)["networkId"] != "net-other" {
		t.Errorf("request = %v", f.lastCreate)
	}
	s3 := spec("burst-c")
	s3.Image = "11111111-2222-3333-4444-555555555555"
	if _, err := p.Create(context.Background(), s3); err != nil || f.lastCreate["imageId"] != s3.Image {
		t.Errorf("an image id: %v, %v", err, f.lastCreate["imageId"])
	}
	s4 := spec("burst-d")
	s4.Image = "Debian 12"
	if _, err := p.Create(context.Background(), s4); err != nil || f.lastCreate["imageId"] != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("an image name: %v, %v", err, f.lastCreate["imageId"])
	}
}

func TestCreateSaysWhatIsMissingInsteadOfGuessing(t *testing.T) {
	_, p := newFake(t)
	for name, tc := range map[string]struct {
		mutate func(*compute.InstanceSpec)
		want   string
	}{
		"no such flavor":   {func(s *compute.InstanceSpec) { s.Size = "z9-1" }, "no flavor"},
		"windows flavor":   {func(s *compute.InstanceSpec) { s.Size = "win-b3-8" }, "no flavor"},
		"gpu flavor":       {func(s *compute.InstanceSpec) { s.Size = "t1-45" }, "no flavor"},
		"region unknown":   {func(s *compute.InstanceSpec) { s.Region = "MARS1" }, "no flavors"},
		"no image by name": {func(s *compute.InstanceSpec) { s.Image = "Gentoo" }, "image called Gentoo"},
		"no region":        {func(s *compute.InstanceSpec) { s.Region = "" }, ""},
	} {
		t.Run(name, func(t *testing.T) {
			s := spec("burst-a")
			tc.mutate(&s)
			p.defaultRegion = ""
			if _, err := p.Create(context.Background(), s); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestAFlavorOutOfStockOrOverQuotaIsRefusedBeforeTheCall(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Size = "c3-16"
	_, err := p.Create(context.Background(), s)
	var capacity *compute.CapacityError
	if !errors.As(err, &capacity) {
		t.Errorf("out of stock: %v", err)
	}
	s.Size = "r3-32"
	if _, err := p.Create(context.Background(), s); !compute.IsQuota(err) {
		t.Errorf("no quota left: %v", err)
	}
	if f.creates != 0 {
		t.Error("a create was sent for a flavor that cannot be had")
	}
}

func TestARepeatedCreateReturnsTheMachineInsteadOfASecond(t *testing.T) {
	f, p := newFake(t)
	first, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	f.activate(first.ID, "51.210.0.7")
	again, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || f.creates != 1 {
		t.Errorf("the repeat made a second machine: %s vs %s, %d creates", again.ID, first.ID, f.creates)
	}
	if again.PublicIPv4 != "51.210.0.7" || !again.Ready() {
		t.Errorf("the repeat did not return the machine as it is: %+v", again)
	}
	// The name is unique in the project: a create that names another region still finds the machine.
	elsewhere := spec("burst-a")
	elsewhere.Region = "BHS5"
	if found, err := p.Create(context.Background(), elsewhere); err != nil || found.ID != first.ID || f.creates != 1 {
		t.Errorf("a repeat in another region: %+v, %v, %d creates", found, err, f.creates)
	}
	// A machine that is gone does not stand in the way of a new one with the same name.
	f.status(first.ID, "DELETED")
	third, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil || third.ID == first.ID || f.creates != 2 {
		t.Errorf("after the first was deleted: %+v, %v, %d creates", third, err, f.creates)
	}
}

func TestProviderFailuresBecomeTheErrorsCallersActOn(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		message string
		check   func(error) bool
	}{
		"quota":    {403, "Quota exceeded for cores: requested 4, but already used 8 of 8", compute.IsQuota},
		"payment":  {400, "You must add a payment method to create an instance", compute.IsQuota},
		"capacity": {500, "No valid host was found. There are not enough hosts available.", func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }},
		"stock":    {409, "Flavor b3-8 is out of stock in GRA11", func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }},
		"iam":      {403, "This call has not been granted", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"token":    {401, "invalid credentials", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"rate":     {429, "Too many requests", func(err error) bool { var r *compute.RateLimitError; return errors.As(err, &r) }},
	} {
		t.Run(name, func(t *testing.T) {
			f, p := newFake(t)
			p.api.Sleep = func(context.Context, time.Duration) error { return nil }
			f.failCreate = func() (int, map[string]any) {
				return tc.status, map[string]any{"class": "Client::Whatever", "message": tc.message}
			}
			if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !tc.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestListFindsOnlyTheOwnersMachinesInEveryRegionAndLabelsThem(t *testing.T) {
	f, p := newFake(t)
	for _, tc := range []struct{ region, name, cluster, pool string }{
		{"GRA11", "burst-a", "c1", "burst"},
		{"BHS5", "burst-b", "c1", "burst"},
		{"GRA11", "web-a", "c1", "web"},
		{"GRA11", "theirs", "someone-else", "burst"},
	} {
		s := spec(tc.name)
		s.Region = tc.region
		s.Labels = compute.Owner{Cluster: tc.cluster, Pool: tc.pool}.Labels(tc.name)
		if _, err := p.Create(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	// Machines that are not Geass's, including one that is named like it.
	for i, name := range []string{"my-server", "geass-test"} {
		f.instances["stranger"+strconv.Itoa(i)] = map[string]any{"id": "stranger" + strconv.Itoa(i), "name": name, "status": "ACTIVE", "region": "GRA11"}
		f.order = append(f.order, "stranger"+strconv.Itoa(i))
	}

	burst, err := p.List(context.Background(), compute.Owner{Cluster: "c1", Pool: "burst"})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(burst))
	for _, i := range burst {
		names = append(names, i.Region+"/"+i.Name)
		if i.Labels[compute.LabelCluster] != "c1" || i.Labels[compute.LabelPool] != "burst" || i.Labels[compute.LabelManaged] != "true" || i.Labels[compute.LabelNode] != i.Name {
			t.Errorf("labels of %s = %v", i.Name, i.Labels)
		}
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "BHS5/burst-b,GRA11/burst-a" {
		t.Errorf("listed %v", names)
	}
	if q := f.queries["GET /cloud/project/"+testProject+"/instance"]; q.Get("region") != "" {
		t.Errorf("List asked for one region (%q): it has to see all of them", q.Get("region"))
	}
	all, _ := p.List(context.Background(), compute.Owner{Cluster: "c1"})
	if len(all) != 3 {
		t.Errorf("the cluster's machines: %d, want 3", len(all))
	}
	for _, i := range all {
		if i.Labels[compute.LabelCluster] != "c1" || i.Labels[compute.LabelNode] == "" {
			t.Errorf("labels of %s = %v", i.Name, i.Labels)
		}
	}
	if none, _ := p.List(context.Background(), compute.Owner{Cluster: "nobody"}); len(none) != 0 {
		t.Errorf("another owner's machines: %+v", none)
	}
	if none, _ := p.List(context.Background(), compute.Owner{}); len(none) != 0 {
		t.Errorf("an owner with no cluster was given machines: %+v", none)
	}
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	for _, i := range burst {
		if !owner.Owns(i.Labels) {
			t.Errorf("%s is listed but its labels do not say it is the owner's", i.Name)
		}
	}
}

func TestDeleteIsIdempotentAndGetReportsAGoneMachine(t *testing.T) {
	f, p := newFake(t)
	f.deleteWhileDeleting = 400
	created, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	got, err := p.Get(context.Background(), created.ID)
	if err != nil || got.State != compute.StateTerminating || got.Alive() {
		t.Errorf("a machine being deleted: %+v, %v", got, err)
	}
	// Deleting again while it is on its way out is not an error, whether the API says 400 or 409.
	for _, status := range []int{400, 409} {
		f.deleteWhileDeleting = status
		if err := p.Delete(context.Background(), created.ID); err != nil {
			t.Errorf("deleting again (%d): %v", status, err)
		}
	}
	delete(f.instances, created.ID)
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Errorf("deleting a machine that is gone: %v", err)
	}
	if _, err := p.Get(context.Background(), created.ID); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
	if err := p.Delete(context.Background(), "a/b"); err != nil {
		t.Errorf("deleting an id that names nothing: %v", err)
	}
	if _, err := p.Get(context.Background(), "a/b"); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get of a malformed id = %v", err)
	}
}

func TestADeleteThatFailsForAnotherReasonIsAnError(t *testing.T) {
	f, p := newFake(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	f.activate(created.ID, "51.210.0.7")
	// A refusal for an instance that is still running is real: only a refusal for one on its way out counts as done.
	for _, status := range []int{400, 409, 500} {
		f.refuseDelete = status
		if err := p.Delete(context.Background(), created.ID); err == nil {
			t.Errorf("a delete refused with %d for a running instance was reported as done", status)
		}
	}
}

func TestAMachinesStateAndAddressesAreRead(t *testing.T) {
	f, p := newFake(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	pending, err := p.Get(context.Background(), created.ID)
	if err != nil || pending.State != compute.StatePending || pending.Ready() {
		t.Fatalf("building: %+v, %v", pending, err)
	}
	f.activate(created.ID, "51.210.0.7")
	got, err := p.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Ready() || got.PublicIPv4 != "51.210.0.7" || got.PrivateIPv4 != "10.1.0.7" || got.CreatedAt.IsZero() || got.Region != "GRA11" {
		t.Errorf("got %+v", got)
	}
	if got.Name != "burst-a" || got.Labels[compute.LabelNode] != "burst-a" || got.Labels[compute.LabelManaged] != "true" {
		t.Errorf("name and labels = %q %v", got.Name, got.Labels)
	}
}

func TestStatesAreTranslated(t *testing.T) {
	for status, want := range map[string]compute.State{
		"BUILD": compute.StatePending, "BUILDING": compute.StatePending, "REBUILD": compute.StatePending, "REBOOT": compute.StatePending, "UNKNOWN": compute.StatePending,
		"ACTIVE":  compute.StateRunning,
		"SHUTOFF": compute.StateStopped, "STOPPED": compute.StateStopped, "SUSPENDED": compute.StateStopped, "ERROR": compute.StateStopped,
		"DELETING": compute.StateTerminating,
		"DELETED":  compute.StateGone, "SOFT_DELETED": compute.StateGone,
	} {
		if got := stateOf(status); got != want {
			t.Errorf("%s -> %s, want %s", status, got, want)
		}
	}
}

func TestSizesAreSmallestFirstWithoutWindowsOrGPUFlavors(t *testing.T) {
	_, p := newFake(t)
	sizes, err := p.Sizes(context.Background(), "GRA11")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(sizes))
	for _, s := range sizes {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "d2-4,b3-8,c3-16,r3-32" {
		t.Fatalf("sizes = %v", ids)
	}
	if s := sizes[1]; s.VCPUs != 2 || s.MemoryMiB != 8000 || s.DiskGB != 50 || s.Arch != "amd64" || s.HourlyPrice != 0 {
		t.Errorf("b3-8 = %+v", s)
	}
	if !strings.Contains(sizes[2].Description, "out of stock") {
		t.Errorf("c3-16 = %+v: a flavor out of stock should say so, not vanish", sizes[2])
	}
	if s := sizes[3]; s.MemoryMiB != 32*1024 {
		t.Errorf("r3-32 reports %d MiB: memory given in gibibytes must be converted", s.MemoryMiB)
	}
	// No region given: the connection's, and with none at all every region, each name once.
	p.defaultRegion = ""
	all, _ := p.Sizes(context.Background(), "")
	count := 0
	for _, s := range all {
		if s.ID == "b3-8" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("b3-8 is listed %d times", count)
	}
}

func TestVerifyIsAHarmlessReadThatWarnsAboutTheMissingFirewall(t *testing.T) {
	f, p := newFake(t)
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(account.Name, "geass-project") {
		t.Errorf("account = %+v", account)
	}
	joined := strings.Join(account.Warnings, "\n")
	if !strings.Contains(joined, "no firewall API") {
		t.Errorf("warnings = %v", account.Warnings)
	}
	for _, r := range f.requests {
		if !strings.HasPrefix(r, "GET") {
			t.Errorf("Verify changed something: %s", r)
		}
	}
	if len(f.instances) != 0 {
		t.Error("Verify created something")
	}
}

func TestVerifyWarnsAboutADisabledRegionAnExhaustedQuotaAndAProjectThatIsNotOK(t *testing.T) {
	f, p := newFake(t)
	f.regions = []string{"BHS5"}
	f.quotas = []map[string]any{
		{"region": "GRA11", "instance": map[string]any{"maxInstances": 0, "usedInstances": 0}},
		{"region": "BHS5", "instance": map[string]any{"maxInstances": 0, "usedInstances": 0}},
	}
	f.project["status"] = "suspended"
	f.project["expiration"] = "2027-01-01T00:00:00Z"
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(account.Warnings, "\n")
	for _, want := range []string{"not enabled", "quota in GRA11 is used up", "no region of the project has room", "suspended"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no warning about %q in:\n%s", want, joined)
		}
	}
	if account.ExpiresAt.IsZero() {
		t.Error("the project's expiry was not read")
	}
}

func TestTheProviderRegistersItself(t *testing.T) {
	d, ok := compute.Lookup(ID)
	if !ok || d.ID != "OVHcloud" {
		t.Fatalf("descriptor = %+v", d)
	}
	fields := map[string]compute.Field{}
	for _, f := range d.Fields {
		fields[f.Key] = f
	}
	for _, key := range []string{"clientId", "clientSecret", "serviceName", "endpoint"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("field %s is missing", key)
		}
	}
	if !fields["clientSecret"].Secret || !fields["endpoint"].Optional || fields["clientId"].Optional {
		t.Errorf("fields = %+v", fields)
	}
	for _, key := range []string{SettingImageID, SettingNetworkID, SettingUserDataBase64} {
		if !d.KnownSetting(key) {
			t.Errorf("setting %s is not described", key)
		}
	}
	if _, err := compute.New(ID, compute.Credentials{}, compute.Options{}); err == nil {
		t.Error("a provider was built without credentials")
	}
	if _, err := compute.New(ID, compute.Credentials{"clientId": "c", "clientSecret": "s", "serviceName": testProject}, compute.Options{}); err != nil {
		t.Error(err)
	}
	if missing := d.MissingFields(compute.Credentials{"clientId": "c", "clientSecret": "s", "serviceName": "p"}); len(missing) != 0 {
		t.Errorf("the endpoint is optional but %v is reported missing", missing)
	}
}
