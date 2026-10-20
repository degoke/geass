package azure

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/degoke/geass/pkg/compute"
)

const (
	testSubscription = "11111111-2222-3333-4444-555555555555"
	testGroup        = "geass-rg"
	testSubnet       = "/subscriptions/" + testSubscription + "/resourceGroups/net-rg/providers/Microsoft.Network/virtualNetworks/vnet1/subnets/nodes"
	testNSG          = "/subscriptions/" + testSubscription + "/resourceGroups/net-rg/providers/Microsoft.Network/networkSecurityGroups/mine"
	groupPrefix      = "/subscriptions/" + testSubscription + "/resourceGroups/" + testGroup
)

// fakeAzure is the part of Azure Resource Manager, Entra ID and the retail price list that Geass uses, in memory.
type fakeAzure struct {
	t        *testing.T
	tenant   string
	secret   string
	vms      map[string]map[string]any
	nics     map[string]map[string]any
	ips      map[string]map[string]any
	disks    map[string]bool
	groups   map[string]map[string]any
	requests []string
	// auth is the Authorization header of every ARM request, by path.
	auth map[string]string

	tokenCalls int
	tokenForms []url.Values
	// expiresIn is what the token endpoint says.
	expiresIn int

	// failPut is the status, code and message of the next PUT of a virtual machine.
	failPut func() (int, string, string)
	// groupPolls is how many reads of a group after its write still say Updating.
	groupPolls   int
	groupPending map[string]int
	// noSizes makes the vmSizes call refuse.
	noSizes bool
	// noPrices makes the price list fail.
	noPrices bool
	// listPage is how many machines a list returns at a time.
	listPage int
}

func newFake(t *testing.T) (*fakeAzure, *Provider) {
	f := &fakeAzure{
		t: t, tenant: "tenant-1", secret: "s3cret", vms: map[string]map[string]any{}, nics: map[string]map[string]any{},
		ips: map[string]map[string]any{}, disks: map[string]bool{}, groups: map[string]map[string]any{}, auth: map[string]string{},
		expiresIn: 3599, groupPending: map[string]int{}, listPage: 2,
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	p := New(Config{TenantID: "tenant-1", ClientID: "client-1", ClientSecret: "s3cret", SubscriptionID: testSubscription, ResourceGroup: testGroup},
		compute.Options{BaseURL: srv.URL, Region: "westeurope"})
	p.sleep = func(context.Context, time.Duration) error { return nil }
	return f, p
}

func (f *fakeAzure) reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (f *fakeAzure) fail(w http.ResponseWriter, status int, code, message string) {
	f.reply(w, status, map[string]any{"error": map[string]any{"code": code, "message": message}})
}

func (f *fakeAzure) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	body, _ := io.ReadAll(r.Body)
	path := r.URL.Path

	if strings.HasSuffix(path, "/oauth2/v2.0/token") {
		f.token(w, r, body)
		return
	}
	if path == "/api/retail/prices" {
		f.retailPrices(w, r)
		return
	}
	f.auth[path] = r.Header.Get("Authorization")
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer tok-") {
		f.fail(w, 401, "InvalidAuthenticationToken", "no token")
		return
	}
	switch {
	case r.Method == "GET" && path == groupPrefix:
		if r.URL.Query().Get("api-version") != resourcesVersion {
			f.fail(w, 400, "InvalidApiVersionParameter", "wrong version")
			return
		}
		f.reply(w, 200, map[string]any{"name": testGroup, "location": "westeurope"})
	case r.Method == "GET" && strings.HasSuffix(path, "/vmSizes"):
		if f.noSizes {
			f.fail(w, 403, "AuthorizationFailed", "no permission to read VM sizes")
			return
		}
		f.reply(w, 200, map[string]any{"value": []map[string]any{
			{"name": "Standard_B2als_v2", "numberOfCores": 2, "memoryInMB": 4096, "resourceDiskSizeInMB": 0, "osDiskSizeInMB": 1047552},
			{"name": "Standard_D2s_v5", "numberOfCores": 2, "memoryInMB": 8192, "resourceDiskSizeInMB": 0},
			{"name": "Standard_B2pls_v2", "numberOfCores": 2, "memoryInMB": 4096, "resourceDiskSizeInMB": 0},
			{"name": "Standard_D4s_v5", "numberOfCores": 4, "memoryInMB": 16384, "resourceDiskSizeInMB": 0},
			{"name": "Basic_A1", "numberOfCores": 1, "memoryInMB": 1792, "resourceDiskSizeInMB": 40960},
			{"name": "Standard_L8s_v3", "numberOfCores": 8, "memoryInMB": 65536, "resourceDiskSizeInMB": 81920},
		}})
	case path == groupPrefix+"/providers/Microsoft.Compute/virtualMachines" && r.Method == "GET":
		f.listVMs(w, r)
	case strings.HasPrefix(path, groupPrefix+"/providers/Microsoft.Compute/virtualMachines/"):
		f.vmRoute(w, r, strings.TrimPrefix(path, groupPrefix+"/providers/Microsoft.Compute/virtualMachines/"), body)
	case strings.Contains(path, "/providers/Microsoft.Network/networkInterfaces/"):
		nic, ok := f.nics[path]
		if !ok {
			f.fail(w, 404, "ResourceNotFound", "nic not found")
			return
		}
		f.reply(w, 200, nic)
	case strings.Contains(path, "/providers/Microsoft.Network/publicIPAddresses/"):
		ip, ok := f.ips[path]
		if !ok {
			f.fail(w, 404, "ResourceNotFound", "ip not found")
			return
		}
		f.reply(w, 200, ip)
	case strings.Contains(path, "/providers/Microsoft.Network/networkSecurityGroups/"):
		f.nsgRoute(w, r, path, body)
	default:
		f.fail(w, 404, "ResourceNotFound", "no route "+r.Method+" "+path)
	}
}

func (f *fakeAzure) token(w http.ResponseWriter, r *http.Request, body []byte) {
	f.tokenCalls++
	form, _ := url.ParseQuery(string(body))
	f.tokenForms = append(f.tokenForms, form)
	if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.URL.Path != "/"+f.tenant+"/oauth2/v2.0/token" {
		f.fail(w, 400, "bad", "bad token request")
		return
	}
	if form.Get("client_secret") != f.secret {
		f.reply(w, 401, map[string]any{"error": "invalid_client", "error_description": "AADSTS7000215: Invalid client secret provided.\r\nTrace ID: x"})
		return
	}
	f.reply(w, 200, map[string]any{"token_type": "Bearer", "expires_in": f.expiresIn, "access_token": "tok-" + strconv.Itoa(f.tokenCalls)})
}

func (f *fakeAzure) retailPrices(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "" {
		f.t.Errorf("the ARM token was sent to the price list")
	}
	if f.noPrices {
		f.fail(w, 500, "oops", "no prices")
		return
	}
	filter := r.URL.Query().Get("$filter")
	if !strings.Contains(filter, "serviceName eq 'Virtual Machines'") || !strings.Contains(filter, "armRegionName eq 'westeurope'") {
		f.t.Errorf("filter = %q", filter)
	}
	item := func(sku string, price float64, product, name, kind string) map[string]any {
		return map[string]any{"armSkuName": sku, "retailPrice": price, "productName": product, "skuName": name, "meterName": name, "unitOfMeasure": "1 Hour", "type": kind}
	}
	if r.URL.Query().Get("$skip") == "" {
		f.reply(w, 200, map[string]any{
			"Items": []map[string]any{
				item("Standard_B2als_v2", 0.0498, "Virtual Machines Basv2 Series", "B2als v2", "Consumption"),
				item("Standard_B2als_v2", 0.0911, "Virtual Machines Basv2 Series Windows", "B2als v2", "Consumption"),
				item("Standard_B2als_v2", 0.0123, "Virtual Machines Basv2 Series", "B2als v2 Spot", "Consumption"),
				item("Standard_D2s_v5", 0.1, "Virtual Machines Dsv5 Series", "D2s v5", "Consumption"),
			},
			"NextPageLink": "https://prices.azure.com:443/api/retail/prices?$filter=" + url.QueryEscape(filter) + "&$skip=1000",
		})
		return
	}
	f.reply(w, 200, map[string]any{"Items": []map[string]any{
		item("Standard_B2pls_v2", 0.0354, "Virtual Machines Bpsv2 Series", "B2pls v2", "Consumption"),
		item("Standard_D4s_v5", 0.192, "Virtual Machines Dsv5 Series", "D4s v5", "Consumption"),
		item("Standard_D4s_v5", 0.05, "Virtual Machines Dsv5 Series", "D4s v5 Low Priority", "Consumption"),
		item("Standard_D4s_v5", 0.001, "Virtual Machines Dsv5 Series", "D4s v5", "Reservation"),
	}, "NextPageLink": nil})
}

func (f *fakeAzure) listVMs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("api-version") != computeVersion {
		f.fail(w, 400, "InvalidApiVersionParameter", "wrong version")
		return
	}
	var names []string
	for name := range f.vms {
		names = append(names, name)
	}
	slices.Sort(names)
	skip, _ := strconv.Atoi(q.Get("$skiptoken"))
	var page []map[string]any
	for i := skip; i < len(names) && i < skip+f.listPage; i++ {
		v := f.vms[names[i]]
		// A list has no instance view.
		copied := map[string]any{}
		maps.Copy(copied, v)
		props := map[string]any{}
		for k, val := range v["properties"].(map[string]any) {
			if k != "instanceView" {
				props[k] = val
			}
		}
		copied["properties"] = props
		page = append(page, copied)
	}
	result := map[string]any{"value": page}
	if skip+f.listPage < len(names) {
		// The link points at the production host, which the provider must not follow.
		result["nextLink"] = "https://management.azure.com" + r.URL.Path + "?api-version=" + computeVersion + "&$skiptoken=" + strconv.Itoa(skip+f.listPage)
	}
	f.reply(w, 200, result)
}

func (f *fakeAzure) vmRoute(w http.ResponseWriter, r *http.Request, name string, body []byte) {
	q := r.URL.Query()
	if q.Get("api-version") != computeVersion {
		f.fail(w, 400, "InvalidApiVersionParameter", "wrong version")
		return
	}
	id := groupPrefix + "/providers/Microsoft.Compute/virtualMachines/" + name
	switch r.Method {
	case "PUT":
		if f.failPut != nil {
			status, code, message := f.failPut()
			f.fail(w, status, code, message)
			return
		}
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		props := req["properties"].(map[string]any)
		props["provisioningState"] = "Creating"
		props["timeCreated"] = "2026-10-19T10:00:00Z"
		status := 201
		if _, exists := f.vms[name]; exists {
			status = 200
		}
		req["id"], req["name"] = id, name
		req["_request"] = json.RawMessage(body)
		f.vms[name] = req
		f.disks[name] = true
		nicID := f.addNIC(name)
		props["networkProfile"] = map[string]any{"networkInterfaces": []map[string]any{{"id": nicID}}}
		f.reply(w, status, req)
	case "GET":
		v, ok := f.vms[name]
		if !ok {
			f.fail(w, 404, "ResourceNotFound", "The Resource 'Microsoft.Compute/virtualMachines/"+name+"' was not found.")
			return
		}
		out := map[string]any{}
		maps.Copy(out, v)
		if q.Get("$expand") == "instanceView" {
			props := map[string]any{}
			maps.Copy(props, v["properties"].(map[string]any))
			if view, ok := props["instanceView"]; ok {
				props["instanceView"] = view
			}
			out["properties"] = props
		} else {
			props := map[string]any{}
			for k, val := range v["properties"].(map[string]any) {
				if k != "instanceView" {
					props[k] = val
				}
			}
			out["properties"] = props
		}
		f.reply(w, 200, out)
	case "DELETE":
		v, ok := f.vms[name]
		if !ok {
			w.WriteHeader(204)
			return
		}
		props := v["properties"].(map[string]any)
		if profile, ok := props["networkProfile"].(map[string]any); ok {
			for _, n := range profile["networkInterfaces"].([]map[string]any) {
				nicID := n["id"].(string)
				// The interface, its address and the disk go with the machine when the request said so.
				if f.deleteWithMachine(v) {
					delete(f.ips, groupPrefix+"/providers/Microsoft.Network/publicIPAddresses/"+name+"-pip")
					delete(f.nics, nicID)
				}
			}
		}
		if f.deleteWithMachine(v) {
			delete(f.disks, name)
		}
		delete(f.vms, name)
		f.reply(w, 202, nil)
	}
}

// deleteWithMachine reports whether the machine was created with deleteOption Delete on its disk, interface and address.
func (f *fakeAzure) deleteWithMachine(v map[string]any) bool {
	raw, _ := v["_request"].(json.RawMessage)
	var req struct {
		Properties struct {
			StorageProfile struct {
				OSDisk struct {
					DeleteOption string `json:"deleteOption"`
				} `json:"osDisk"`
			} `json:"storageProfile"`
			NetworkProfile struct {
				Configs []struct {
					Properties struct {
						DeleteOption string `json:"deleteOption"`
						IPConfigs    []struct {
							Properties struct {
								PublicIP struct {
									Properties struct {
										DeleteOption string `json:"deleteOption"`
									} `json:"properties"`
								} `json:"publicIPAddressConfiguration"`
							} `json:"properties"`
						} `json:"ipConfigurations"`
					} `json:"properties"`
				} `json:"networkInterfaceConfigurations"`
			} `json:"networkProfile"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(raw, &req)
	cfgs := req.Properties.NetworkProfile.Configs
	return req.Properties.StorageProfile.OSDisk.DeleteOption == "Delete" && len(cfgs) == 1 && cfgs[0].Properties.DeleteOption == "Delete" &&
		len(cfgs[0].Properties.IPConfigs) == 1 && cfgs[0].Properties.IPConfigs[0].Properties.PublicIP.Properties.DeleteOption == "Delete"
}

func (f *fakeAzure) addNIC(vmName string) string {
	nicID := groupPrefix + "/providers/Microsoft.Network/networkInterfaces/" + vmName + "-nic"
	ipID := groupPrefix + "/providers/Microsoft.Network/publicIPAddresses/" + vmName + "-pip"
	f.nics[nicID] = map[string]any{"properties": map[string]any{"ipConfigurations": []map[string]any{{
		"properties": map[string]any{"primary": true, "privateIPAddress": "10.0.1.4", "publicIPAddress": map[string]any{"id": ipID}},
	}}}}
	f.ips[ipID] = map[string]any{"properties": map[string]any{"ipAddress": "203.0.113.20"}}
	return nicID
}

func (f *fakeAzure) nsgRoute(w http.ResponseWriter, r *http.Request, path string, body []byte) {
	if r.URL.Query().Get("api-version") != networkVersion {
		f.fail(w, 400, "InvalidApiVersionParameter", "wrong version")
		return
	}
	switch r.Method {
	case "GET":
		g, ok := f.groups[path]
		if !ok {
			f.fail(w, 404, "ResourceNotFound", "nsg not found")
			return
		}
		out := map[string]any{"tags": g["tags"], "properties": map[string]any{"securityRules": g["properties"].(map[string]any)["securityRules"], "provisioningState": "Succeeded"}}
		if n := f.groupPending[path]; n > 0 {
			f.groupPending[path] = n - 1
			out["properties"].(map[string]any)["provisioningState"] = "Updating"
		}
		f.reply(w, 200, out)
	case "PUT":
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		f.groups[path] = req
		f.groupPending[path] = f.groupPolls
		f.reply(w, 201, req)
	}
}

func (f *fakeAzure) count(prefix string) int {
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// run marks a machine as provisioned and running.
func (f *fakeAzure) run(name string) {
	props := f.vms[name]["properties"].(map[string]any)
	props["provisioningState"] = "Succeeded"
	props["instanceView"] = map[string]any{"statuses": []map[string]any{{"code": "ProvisioningState/succeeded"}, {"code": "PowerState/running"}}}
}

func (f *fakeAzure) setState(name, provisioning, power string) {
	props := f.vms[name]["properties"].(map[string]any)
	props["provisioningState"] = provisioning
	statuses := []map[string]any{{"code": "ProvisioningState/" + strings.ToLower(provisioning)}}
	if power != "" {
		statuses = append(statuses, map[string]any{"code": "PowerState/" + power})
	}
	props["instanceView"] = map[string]any{"statuses": statuses}
}

func spec(name string) compute.InstanceSpec {
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	return compute.InstanceSpec{
		Name: name, Region: "westeurope", Size: "Standard_B2als_v2", UserData: "#cloud-config\nhostname: " + name + "\n", Labels: owner.Labels(name),
		AuthorizedKeys:    []string{"ssh-ed25519 AAAAC3Nza geass"},
		Settings:          map[string]string{SettingSubnetID: testSubnet},
		ControlPlaneCIDRs: []string{"203.0.113.5/32"}, MultiSite: true,
	}
}

// sentBody returns the JSON of the PUT of a machine.
func sentBody(t *testing.T, f *fakeAzure, name string) map[string]any {
	t.Helper()
	raw, ok := f.vms[name]["_request"].(json.RawMessage)
	if !ok {
		t.Fatalf("no machine %s", name)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func dig(v any, path ...string) any {
	for _, p := range path {
		switch m := v.(type) {
		case map[string]any:
			v = m[p]
		case []any:
			i, _ := strconv.Atoi(p)
			if i >= len(m) {
				return nil
			}
			v = m[i]
		default:
			return nil
		}
	}
	return v
}

func TestCreateSendsTheMachineWithItsImageDiskNetworkLoginTagsAndUserData(t *testing.T) {
	f, p := newFake(t)

	instance, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if instance.ID != "burst-a" || instance.Name != "burst-a" || instance.State != compute.StatePending || instance.Region != "westeurope" || instance.Size != "Standard_B2als_v2" {
		t.Errorf("instance = %+v", instance)
	}
	sent := sentBody(t, f, "burst-a")
	if sent["location"] != "westeurope" || dig(sent, "properties", "hardwareProfile", "vmSize") != "Standard_B2als_v2" {
		t.Errorf("request = %v", sent)
	}
	if got, _ := json.Marshal(dig(sent, "properties", "storageProfile", "imageReference")); string(got) != `{"offer":"ubuntu-24_04-lts","publisher":"Canonical","sku":"server","version":"latest"}` {
		t.Errorf("image = %s", got)
	}
	disk := dig(sent, "properties", "storageProfile", "osDisk").(map[string]any)
	if disk["createOption"] != "FromImage" || disk["deleteOption"] != "Delete" || disk["diskSizeGB"] != float64(30) || dig(disk, "managedDisk", "storageAccountType") != "StandardSSD_LRS" {
		t.Errorf("osDisk = %v", disk)
	}
	os := dig(sent, "properties", "osProfile").(map[string]any)
	data, _ := base64.StdEncoding.DecodeString(os["customData"].(string))
	if string(data) != spec("burst-a").UserData || os["computerName"] != "burst-a" || os["adminUsername"] != "geass" {
		t.Errorf("osProfile = %v", os)
	}
	if dig(os, "linuxConfiguration", "disablePasswordAuthentication") != true {
		t.Error("password authentication was left on")
	}
	key := dig(os, "linuxConfiguration", "ssh", "publicKeys", "0").(map[string]any)
	if key["path"] != "/home/geass/.ssh/authorized_keys" || key["keyData"] != "ssh-ed25519 AAAAC3Nza geass" {
		t.Errorf("key = %v", key)
	}
	if dig(sent, "properties", "priority") != nil {
		t.Error("a regular machine was made a Spot one")
	}
	tags := sent["tags"].(map[string]any)
	if tags[compute.LabelManaged] != "true" || tags[compute.LabelNode] != "burst-a" || tags[compute.LabelCluster] != "c1" || tags[compute.LabelPool] != "burst" {
		t.Errorf("tags = %v", tags)
	}
	if f.auth[groupPrefix+"/providers/Microsoft.Compute/virtualMachines/burst-a"] != "Bearer tok-1" {
		t.Errorf("Authorization = %q", f.auth[groupPrefix+"/providers/Microsoft.Compute/virtualMachines/burst-a"])
	}
}

func TestNothingOfAMachineSurvivesItsDeletion(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	sent := sentBody(t, f, "burst-a")
	if dig(sent, "properties", "networkProfile", "networkApiVersion") != "2020-11-01" {
		t.Errorf("networkApiVersion = %v", dig(sent, "properties", "networkProfile", "networkApiVersion"))
	}
	nic := dig(sent, "properties", "networkProfile", "networkInterfaceConfigurations", "0").(map[string]any)
	cfg := nic["properties"].(map[string]any)
	if cfg["primary"] != true || cfg["deleteOption"] != "Delete" {
		t.Errorf("nic = %v", cfg)
	}
	ipConfig := dig(cfg, "ipConfigurations", "0", "properties").(map[string]any)
	pip := ipConfig["publicIPAddressConfiguration"].(map[string]any)
	if dig(ipConfig, "subnet", "id") != testSubnet || dig(pip, "sku", "name") != "Standard" || dig(pip, "sku", "tier") != "Regional" ||
		dig(pip, "properties", "publicIPAllocationMethod") != "Static" || dig(pip, "properties", "deleteOption") != "Delete" {
		t.Errorf("ip configuration = %v", ipConfig)
	}
	if got := dig(cfg, "networkSecurityGroup", "id"); got != groupPrefix+"/providers/Microsoft.Network/networkSecurityGroups/geass-c1-westeurope" {
		t.Errorf("nsg = %v", got)
	}

	if err := p.Delete(context.Background(), "burst-a"); err != nil {
		t.Fatal(err)
	}
	if len(f.vms) != 0 || len(f.nics) != 0 || len(f.ips) != 0 || len(f.disks) != 0 {
		t.Errorf("leftovers that bill: %d machines, %d interfaces, %d addresses, %d disks", len(f.vms), len(f.nics), len(f.ips), len(f.disks))
	}
	if err := p.Delete(context.Background(), "burst-a"); err != nil {
		t.Errorf("deleting again: %v", err)
	}
	if err := p.Delete(context.Background(), "never-was"); err != nil {
		t.Errorf("deleting an unknown machine: %v", err)
	}
	if _, err := p.Get(context.Background(), "burst-a"); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
}

func TestASpotMachineIsDeletedWhenEvictedAndNeverPaysMoreThanTheGoingPrice(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Spot = true
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	props := dig(sentBody(t, f, "burst-a"), "properties").(map[string]any)
	if props["priority"] != "Spot" || props["evictionPolicy"] != "Delete" || dig(props, "billingProfile", "maxPrice") != float64(-1) {
		t.Errorf("properties = %v", props)
	}
}

func TestTheImageFollowsTheSizeAndTheSpec(t *testing.T) {
	image := func(s compute.InstanceSpec) (string, error) {
		f, p := newFake(t)
		if _, err := p.Create(context.Background(), s); err != nil {
			return "", err
		}
		got, _ := json.Marshal(dig(sentBody(t, f, s.Name), "properties", "storageProfile", "imageReference"))
		return string(got), nil
	}
	arm := spec("burst-a")
	arm.Size = "Standard_D4ps_v5"
	if got, err := image(arm); err != nil || !strings.Contains(got, `"sku":"server-arm64"`) {
		t.Errorf("an ARM size: %s, %v", got, err)
	}
	pinned := spec("burst-b")
	pinned.Settings[SettingImageVersion] = "24.04.202510010"
	if got, err := image(pinned); err != nil || !strings.Contains(got, `"version":"24.04.202510010"`) {
		t.Errorf("a pinned version: %s, %v", got, err)
	}
	urn := spec("burst-c")
	urn.Image = "Canonical:ubuntu-26_04-lts:server:latest"
	if got, err := image(urn); err != nil || !strings.Contains(got, `"offer":"ubuntu-26_04-lts"`) {
		t.Errorf("a URN: %s, %v", got, err)
	}
	byID := spec("burst-d")
	byID.Image = "/subscriptions/" + testSubscription + "/resourceGroups/img/providers/Microsoft.Compute/images/golden"
	if got, err := image(byID); err != nil || got != `{"id":"`+byID.Image+`"}` {
		t.Errorf("an image ID: %s, %v", got, err)
	}
	bad := spec("burst-e")
	bad.Image = "ubuntu"
	if _, err := image(bad); err == nil {
		t.Error("an image that is neither a URN nor an ID was accepted")
	}
	for size, want := range map[string]bool{
		"Standard_D4ps_v5": true, "Standard_B2pts_v2": true, "Standard_E2pds_v6": true, "Standard_D2pls_v5": true,
		"Standard_B2als_v2": false, "Standard_D4s_v5": false, "Standard_NC4as_T4_v3": false, "Standard_HB120-16rs_v3": false, "Standard_L8s_v3": false,
	} {
		if isARM(size) != want {
			t.Errorf("isARM(%s) = %v", size, !want)
		}
	}
}

func TestTheSettingsAndKeysAMachineNeedsAreChecked(t *testing.T) {
	for name, mutate := range map[string]func(*compute.InstanceSpec){
		"no subnet":      func(s *compute.InstanceSpec) { delete(s.Settings, SettingSubnetID) },
		"a bad subnet":   func(s *compute.InstanceSpec) { s.Settings[SettingSubnetID] = "vnet1/nodes" },
		"no key":         func(s *compute.InstanceSpec) { s.AuthorizedKeys = nil },
		"a bad disk":     func(s *compute.InstanceSpec) { s.Settings[SettingDiskGB] = "big" },
		"a bad group id": func(s *compute.InstanceSpec) { s.Settings[SettingNSGID] = "mine" },
		"huge user data": func(s *compute.InstanceSpec) { s.UserData = strings.Repeat("x", maxCustomData+1) },
	} {
		t.Run(name, func(t *testing.T) {
			f, p := newFake(t)
			s := spec("burst-a")
			mutate(&s)
			if _, err := p.Create(context.Background(), s); err == nil {
				t.Error("the create was accepted")
			}
			if len(f.vms) != 0 {
				t.Error("a machine was made")
			}
		})
	}
	f, p := newFake(t)
	s := spec("burst-a")
	s.Settings[SettingDiskGB] = "64"
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if got := dig(sentBody(t, f, "burst-a"), "properties", "storageProfile", "osDisk", "diskSizeGB"); got != float64(64) {
		t.Errorf("diskSizeGB = %v", got)
	}
}

func TestTheGroupLetsInOnlyWhatTheClusterNeedsAndDeniesTheRest(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 1 {
		t.Fatalf("%d groups, want 1", len(f.groups))
	}
	for path, g := range f.groups {
		if !strings.HasSuffix(path, "/networkSecurityGroups/geass-c1-westeurope") || g["location"] != "westeurope" || dig(g, "tags", compute.LabelManaged) != "true" {
			t.Errorf("group %s = %v", path, g)
		}
		rules := dig(g, "properties", "securityRules").([]any)
		got := map[string]string{}
		var lastPriority float64
		for _, raw := range rules {
			r := raw.(map[string]any)
			props := r["properties"].(map[string]any)
			if props["direction"] != "Inbound" || props["sourcePortRange"] != "*" || props["destinationAddressPrefix"] != "*" {
				t.Errorf("rule = %v", r)
			}
			p := props["priority"].(float64)
			if p <= lastPriority && p != priorityDeny {
				t.Errorf("priorities are not increasing: %v", rules)
			}
			lastPriority = p
			source := fmt.Sprint(props["sourceAddressPrefix"])
			if props["sourceAddressPrefix"] == nil {
				source = fmt.Sprint(props["sourceAddressPrefixes"])
			}
			got[fmt.Sprintf("%v/%v/%v", props["access"], props["protocol"], props["destinationPortRange"])] = source
		}
		want := map[string]string{
			"Allow/Tcp/22": "203.0.113.5/32", "Allow/Tcp/10250": "203.0.113.5/32", "Allow/Udp/51820": "*", "Deny/*/*": "*",
		}
		if len(got) != len(want) {
			t.Errorf("rules = %v", got)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("rule %s from %q, want %q (rules %v)", k, got[k], v, got)
			}
		}
	}
}

func TestSeveralControlPlaneAddressesAreOneRuleAndSSHIsOpenWhenNoneIsKnown(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.ControlPlaneCIDRs = []string{"203.0.113.5/32", "198.51.100.0/24"}
	s.MultiSite = false
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	for _, g := range f.groups {
		first := dig(g, "properties", "securityRules", "0", "properties").(map[string]any)
		if _, single := first["sourceAddressPrefix"]; single || fmt.Sprint(first["sourceAddressPrefixes"]) != "[203.0.113.5/32 198.51.100.0/24]" {
			t.Errorf("rule = %v", first)
		}
	}

	f2, p2 := newFake(t)
	open := spec("burst-b")
	open.ControlPlaneCIDRs = nil
	open.MultiSite = false
	if _, err := p2.Create(context.Background(), open); err != nil {
		t.Fatal(err)
	}
	for _, g := range f2.groups {
		rules := dig(g, "properties", "securityRules").([]any)
		if len(rules) != 2 || dig(rules[0], "properties", "sourceAddressPrefix") != "*" || dig(rules[0], "properties", "destinationPortRange") != "22" {
			t.Errorf("rules = %v", rules)
		}
	}
}

func TestTheGroupIsMadeOnceAndItsRulesAreKeptCurrent(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	puts := f.count("PUT " + groupPrefix + "/providers/Microsoft.Network")
	next := spec("burst-b")
	if _, err := p.Create(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if f.count("PUT "+groupPrefix+"/providers/Microsoft.Network") != puts || len(f.groups) != 1 {
		t.Error("a group whose rules were current was written again")
	}
	moved := spec("burst-c")
	moved.ControlPlaneCIDRs = []string{"203.0.113.77/32"}
	if _, err := p.Create(context.Background(), moved); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 1 {
		t.Fatalf("%d groups, want the one to be reused", len(f.groups))
	}
	for _, g := range f.groups {
		raw, _ := json.Marshal(g)
		if !strings.Contains(string(raw), "203.0.113.77/32") || strings.Contains(string(raw), "203.0.113.5/32") {
			t.Errorf("the group still allows the old control plane: %s", raw)
		}
	}
	// Another location is another group: a group belongs to one location.
	elsewhere := spec("burst-d")
	elsewhere.Region = "northeurope"
	if _, err := p.Create(context.Background(), elsewhere); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 2 {
		t.Errorf("%d groups, want one per location", len(f.groups))
	}
}

func TestAGroupTheUserNamesIsUsedAsItIs(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Settings[SettingNSGID] = testNSG
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 0 || f.count("GET "+testNSG) != 0 {
		t.Error("Geass made or read a group although one was named")
	}
	if got := dig(sentBody(t, f, "burst-a"), "properties", "networkProfile", "networkInterfaceConfigurations", "0", "properties", "networkSecurityGroup", "id"); got != testNSG {
		t.Errorf("nsg = %v", got)
	}
}

func TestAGroupGeassDidNotMakeIsNotTakenOver(t *testing.T) {
	f, p := newFake(t)
	path := groupPrefix + "/providers/Microsoft.Network/networkSecurityGroups/geass-c1-westeurope"
	f.groups[path] = map[string]any{"location": "westeurope", "tags": map[string]any{}, "properties": map[string]any{"securityRules": []any{}}}
	if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !strings.Contains(err.Error(), "not tagged as Geass") {
		t.Errorf("err = %v", err)
	}
	if len(f.vms) != 0 {
		t.Error("a machine was made")
	}
}

func TestACreateWaitsForTheGroupItJustWroteAndGivesUpWithARetryableError(t *testing.T) {
	f, p := newFake(t)
	f.groupPolls = 3
	slept := 0
	p.sleep = func(context.Context, time.Duration) error { slept++; return nil }
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if slept != 3 {
		t.Errorf("waited %d times for a group that takes 3 more reads", slept)
	}

	f2, p2 := newFake(t)
	f2.groupPolls = 1000
	_, err := p2.Create(context.Background(), spec("burst-a"))
	var rate *compute.RateLimitError
	if !errors.As(err, &rate) || rate.RetryAfter <= 0 || !compute.IsTransient(err) {
		t.Errorf("err = %v, want a retryable error", err)
	}
	if len(f2.vms) != 0 {
		t.Error("a machine was made against a group that was not ready")
	}
}

func TestACreateThatRepeatsTheNameReturnsTheOwnersMachine(t *testing.T) {
	f, p := newFake(t)
	first, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || len(f.vms) != 1 || f.count("PUT "+groupPrefix+"/providers/Microsoft.Compute") != 1 {
		t.Errorf("a repeated create made a second machine or changed the first: %s vs %s", again.ID, first.ID)
	}
}

func TestACreateRetriesAMachineThatFailedToProvision(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	f.setState("burst-a", "Failed", "")
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if f.count("PUT "+groupPrefix+"/providers/Microsoft.Compute") != 2 || len(f.vms) != 1 {
		t.Errorf("requests = %v", f.requests)
	}
}

func TestAMachineOfAnotherClusterWithTheSameNameIsLeftAlone(t *testing.T) {
	f, p := newFake(t)
	other := spec("burst-a")
	other.Labels = compute.Owner{Cluster: "someone-else", Pool: "burst"}.Labels("burst-a")
	if _, err := p.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	before := f.count("PUT " + groupPrefix + "/providers/Microsoft.Compute")
	if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !strings.Contains(err.Error(), "not this cluster's") {
		t.Fatalf("err = %v", err)
	}
	if f.count("PUT "+groupPrefix+"/providers/Microsoft.Compute") != before {
		t.Error("the other cluster's machine was updated")
	}
	if dig(sentBody(t, f, "burst-a"), "tags", compute.LabelCluster) != "someone-else" {
		t.Error("the other cluster's machine was changed")
	}
}

func TestProviderFailuresBecomeTheErrorsCallersActOn(t *testing.T) {
	isCapacity := func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }
	for name, tc := range map[string]struct {
		status  int
		code    string
		message string
		check   func(error) bool
	}{
		"quota":       {409, "QuotaExceeded", "Quota exceeded", compute.IsQuota},
		"cores":       {400, "OperationNotAllowed", "Operation could not be completed as it results in exceeding approved Total Regional Cores quota.", compute.IsQuota},
		"addresses":   {400, "PublicIPCountLimitReached", "Cannot create more than 10 public IP addresses", compute.IsQuota},
		"subnet":      {400, "SubnetIsFull", "Subnet nodes with address prefix 10.0.1.0/24 does not have enough capacity for 1 IP addresses.", compute.IsQuota},
		"sku":         {409, "SkuNotAvailable", "The requested VM size is currently not available", isCapacity},
		"allocation":  {409, "AllocationFailed", "Allocation failed", isCapacity},
		"zonal":       {409, "ZonalAllocationFailed", "Allocation failed in the zone", isCapacity},
		"permission":  {403, "AuthorizationFailed", "The client does not have authorization to perform action", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"gone group":  {404, "ResourceGroupNotFound", "Resource group 'geass-rg' could not be found.", func(err error) bool { return err != nil && !errors.Is(err, compute.ErrNotFound) }},
		"other state": {400, "InvalidParameter", "bad", func(err error) bool { return err != nil && !compute.IsQuota(err) && !isCapacity(err) }},
	} {
		t.Run(name, func(t *testing.T) {
			f, p := newFake(t)
			f.failPut = func() (int, string, string) { return tc.status, tc.code, tc.message }
			if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !tc.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestRateLimitingIsRetriedAndThenReported(t *testing.T) {
	f, p := newFake(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token") {
			f.ServeHTTP(w, r)
			return
		}
		calls++
		w.Header().Set("Retry-After", "11")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"code":"TooManyRequests","message":"slow down"}}`))
	}))
	defer srv.Close()
	p = New(p.cfg, compute.Options{BaseURL: srv.URL, Region: "westeurope"})
	p.arm.api.Sleep = func(context.Context, time.Duration) error { return nil }
	p.arm.api.MaxAttempts = 2
	_, err := p.Get(context.Background(), "burst-a")
	var rate *compute.RateLimitError
	if !errors.As(err, &rate) || rate.RetryAfter != 11*time.Second || calls != 2 {
		t.Errorf("err = %v after %d calls", err, calls)
	}
}

func TestTheTokenIsAskedForOnceAndAgainWhenItIsAboutToExpire(t *testing.T) {
	f, p := newFake(t)
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	p.tokens.now = func() time.Time { return now }
	for range 3 {
		if _, err := p.Get(context.Background(), "x"); !errors.Is(err, compute.ErrNotFound) {
			t.Fatal(err)
		}
	}
	if f.tokenCalls != 1 {
		t.Errorf("%d token requests for 3 calls", f.tokenCalls)
	}
	form := f.tokenForms[0]
	if form.Get("grant_type") != "client_credentials" || form.Get("client_id") != "client-1" || form.Get("client_secret") != "s3cret" || form.Get("scope") != "https://management.azure.com/.default" {
		t.Errorf("form = %v", form)
	}
	now = now.Add(time.Hour) // the token lasted 3599 seconds
	if _, err := p.Get(context.Background(), "x"); !errors.Is(err, compute.ErrNotFound) {
		t.Fatal(err)
	}
	if f.tokenCalls != 2 || f.auth[groupPrefix+"/providers/Microsoft.Compute/virtualMachines/x"] != "Bearer tok-2" {
		t.Errorf("%d token requests, Authorization %q", f.tokenCalls, f.auth[groupPrefix+"/providers/Microsoft.Compute/virtualMachines/x"])
	}
}

func TestATokenAzureRefusesIsNotKept(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Get(context.Background(), "x"); !errors.Is(err, compute.ErrNotFound) {
		t.Fatal(err)
	}
	// Azure stops accepting the token (revoked): the first call fails, the next asks for a new one.
	p.tokens.mu.Lock()
	p.tokens.token = "revoked"
	p.tokens.mu.Unlock()
	if _, err := p.Get(context.Background(), "x"); !errors.Is(err, compute.ErrAuth) {
		t.Fatalf("err = %v", err)
	}
	if _, err := p.Get(context.Background(), "x"); !errors.Is(err, compute.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if f.tokenCalls != 2 {
		t.Errorf("%d token requests", f.tokenCalls)
	}
}

func TestAWrongSecretIsRefusedWithAnAuthErrorThatSaysWhy(t *testing.T) {
	f, _ := newFake(t)
	srv := httptest.NewServer(f)
	defer srv.Close()
	p := New(Config{TenantID: "tenant-1", ClientID: "client-1", ClientSecret: "wrong", SubscriptionID: testSubscription, ResourceGroup: testGroup}, compute.Options{BaseURL: srv.URL})
	_, err := p.Verify(context.Background())
	if !errors.Is(err, compute.ErrAuth) || !strings.Contains(err.Error(), "AADSTS7000215") || strings.Contains(err.Error(), "Trace ID") {
		t.Errorf("Verify = %v", err)
	}
}

func TestVerifyReadsAndCreatesNothingAndWarnsAboutWhatItCannotCheck(t *testing.T) {
	f, p := newFake(t)
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(account.Name, testSubscription) || !strings.Contains(account.Name, testGroup) {
		t.Errorf("account = %+v", account)
	}
	secret := false
	for _, w := range account.Warnings {
		secret = secret || strings.Contains(w, "client secret expires")
	}
	if !secret || len(account.Warnings) != 2 {
		t.Errorf("warnings = %v", account.Warnings)
	}
	for _, r := range f.requests {
		if !strings.HasPrefix(r, "GET ") && !strings.HasSuffix(r, "/oauth2/v2.0/token") {
			t.Errorf("Verify sent %s", r)
		}
	}

	f.noSizes = true
	account, err = p.Verify(context.Background())
	if err != nil || len(account.Warnings) != 3 || !strings.Contains(account.Warnings[0], "VM sizes") {
		t.Errorf("without the sizes read: %+v, %v", account, err)
	}
}

func TestGetShowsTheProgressOfAMachineAndItsAddresses(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	got, err := p.Get(context.Background(), "burst-a")
	if err != nil || got.State != compute.StatePending || got.Ready() {
		t.Fatalf("a machine still being made: %+v, %v", got, err)
	}
	f.run("burst-a")
	got, err = p.Get(context.Background(), "burst-a")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Ready() || got.PublicIPv4 != "203.0.113.20" || got.PrivateIPv4 != "10.0.1.4" || got.Labels[compute.LabelNode] != "burst-a" || got.CreatedAt.IsZero() {
		t.Errorf("a running machine: %+v", got)
	}
	// The addresses come from the interface and then the address it points at, on the network api version.
	if f.count("GET "+groupPrefix+"/providers/Microsoft.Network/networkInterfaces/") != 2 || f.count("GET "+groupPrefix+"/providers/Microsoft.Network/publicIPAddresses/") != 2 {
		t.Errorf("requests = %v", f.requests)
	}
}

func TestAMachinesStateIsTranslated(t *testing.T) {
	for _, tc := range []struct {
		provisioning, power string
		want                compute.State
	}{
		{"Creating", "", compute.StatePending},
		{"Creating", "starting", compute.StatePending},
		{"Succeeded", "running", compute.StateRunning},
		{"Updating", "running", compute.StateRunning},
		{"Succeeded", "stopped", compute.StateStopped},
		{"Succeeded", "deallocating", compute.StateStopped},
		{"Succeeded", "deallocated", compute.StateStopped},
		{"Failed", "", compute.StateStopped},
		{"Failed", "running", compute.StateRunning},
		{"Deleting", "running", compute.StateTerminating},
		{"Deleting", "", compute.StateTerminating},
	} {
		if got := stateOf(tc.provisioning, tc.power); got != tc.want {
			t.Errorf("%s/%s -> %s, want %s", tc.provisioning, tc.power, got, tc.want)
		}
	}
}

func TestAStoppedOrFailedMachineIsNotLookedUpForAddresses(t *testing.T) {
	f, p := newFake(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	f.setState("burst-a", "Failed", "")
	got, err := p.Get(context.Background(), "burst-a")
	if err != nil || got.State != compute.StateStopped || got.PublicIPv4 != "" {
		t.Errorf("Get = %+v, %v", got, err)
	}
	if f.count("GET "+groupPrefix+"/providers/Microsoft.Network/networkInterfaces/") != 0 {
		t.Error("the interface of a failed machine was read")
	}
}

func TestListFindsOnlyTheOwnersMachinesAcrossPages(t *testing.T) {
	f, p := newFake(t)
	for _, name := range []string{"burst-a", "burst-b", "burst-c"} {
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
	f.run("burst-a")
	f.setState("burst-b", "Succeeded", "deallocated")

	pool, err := p.List(context.Background(), compute.Owner{Cluster: "c1", Pool: "burst"})
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]compute.State{}
	for _, m := range pool {
		states[m.Name] = m.State
	}
	if len(pool) != 3 || states["burst-a"] != compute.StateRunning || states["burst-b"] != compute.StateStopped || states["burst-c"] != compute.StatePending {
		t.Errorf("listed %+v", pool)
	}
	all, _ := p.List(context.Background(), compute.Owner{Cluster: "c1"})
	if len(all) != 4 {
		t.Errorf("the whole cluster is %d machines, want 4", len(all))
	}
	for _, m := range all {
		if m.Labels[compute.LabelCluster] != "c1" {
			t.Errorf("labels = %v", m.Labels)
		}
	}
}

func TestAListNeverFollowsALinkToAnotherHost(t *testing.T) {
	f, p := newFake(t)
	for _, name := range []string{"burst-a", "burst-b", "burst-c"} {
		if _, err := p.Create(context.Background(), spec(name)); err != nil {
			t.Fatal(err)
		}
	}
	// The fake's links name management.azure.com; had the provider followed one, this test would have made a real
	// request. It finds all three machines only because it kept to the path and the query.
	got, err := p.List(context.Background(), compute.Owner{Cluster: "c1"})
	if err != nil || len(got) != 3 {
		t.Fatalf("listed %d, %v", len(got), err)
	}
	if f.count("GET "+groupPrefix+"/providers/Microsoft.Compute/virtualMachines") < 2 {
		t.Error("the list was not paged")
	}
	if path, query, ok := nextPage("https://evil.example/subscriptions/s/x?api-version=1&$skiptoken=2"); !ok || path != "/subscriptions/s/x" || query.Get("$skiptoken") != "2" {
		t.Errorf("nextPage = %q %v %v", path, query, ok)
	}
	if _, _, ok := nextPage(""); ok {
		t.Error("an empty link has a next page")
	}
}

func TestSizesAreCheapestFirstWithLinuxPayAsYouGoPrices(t *testing.T) {
	_, p := newFake(t)
	sizes, err := p.Sizes(context.Background(), "westeurope")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(sizes))
	for _, s := range sizes {
		ids = append(ids, s.ID)
	}
	if got := strings.Join(ids, ","); got != "Standard_B2pls_v2,Standard_B2als_v2,Standard_D2s_v5,Standard_D4s_v5,Standard_L8s_v3" {
		t.Fatalf("sizes = %s", got)
	}
	b := sizes[1]
	if b.VCPUs != 2 || b.MemoryMiB != 4096 || b.HourlyPrice != 0.0498 || b.Currency != "USD" || b.Arch != "amd64" || b.DiskGB != 30 {
		t.Errorf("size = %+v", b)
	}
	if sizes[0].Arch != "arm64" || sizes[3].HourlyPrice != 0.192 || sizes[4].HourlyPrice != 0 {
		t.Errorf("sizes = %+v", sizes)
	}
	if !strings.Contains(sizes[4].Description, "80 GiB temporary disk") {
		t.Errorf("description = %q", sizes[4].Description)
	}
}

func TestSizesAreStillListedWithoutPricesAndFailWithoutTheSizeList(t *testing.T) {
	f, p := newFake(t)
	f.noPrices = true
	sizes, err := p.Sizes(context.Background(), "")
	if err != nil || len(sizes) != 5 {
		t.Fatalf("sizes = %+v, %v", sizes, err)
	}
	for _, s := range sizes {
		if s.HourlyPrice != 0 {
			t.Errorf("%s has price %v", s.ID, s.HourlyPrice)
		}
	}
	f.noSizes = true
	if _, err := p.Sizes(context.Background(), "westeurope"); !errors.Is(err, compute.ErrAuth) {
		t.Errorf("Sizes = %v", err)
	}
	_, bare := newFake(t)
	bare.region = ""
	if _, err := bare.Sizes(context.Background(), ""); err == nil {
		t.Error("sizes without a location succeeded")
	}
}

func TestTheProviderRegistersItself(t *testing.T) {
	d, ok := compute.Lookup(ID)
	if !ok || len(d.Fields) != 5 {
		t.Fatalf("descriptor = %+v", d)
	}
	secrets := map[string]bool{}
	for _, f := range d.Fields {
		if f.Optional {
			t.Errorf("%s is optional", f.Key)
		}
		secrets[f.Key] = f.Secret
	}
	for _, key := range []string{"tenantId", "clientId", "clientSecret", "subscriptionId", "resourceGroup"} {
		if _, ok := secrets[key]; !ok {
			t.Errorf("no field %s", key)
		}
	}
	if !secrets["clientSecret"] || secrets["tenantId"] || secrets["clientId"] || secrets["subscriptionId"] {
		t.Errorf("secret flags = %v", secrets)
	}
	required := d.MissingSettings(map[string]string{})
	if len(required) != 1 || required[0] != SettingSubnetID {
		t.Errorf("required settings = %v", required)
	}
	for _, key := range []string{SettingSubnetID, SettingNSGID, SettingImageVersion, SettingDiskGB} {
		if !d.KnownSetting(key) {
			t.Errorf("setting %s is unknown", key)
		}
	}
	if d.RegionHelp == "" || !strings.Contains(d.Summary, "expires") {
		t.Errorf("descriptor = %+v", d)
	}
	if _, err := compute.New(ID, compute.Credentials{"tenantId": "t"}, compute.Options{}); err == nil {
		t.Error("a provider was built without credentials")
	}
	if _, err := compute.New(ID, compute.Credentials{"tenantId": "t", "clientId": "c", "clientSecret": "s", "subscriptionId": "s", "resourceGroup": "g"}, compute.Options{}); err != nil {
		t.Error(err)
	}
}
