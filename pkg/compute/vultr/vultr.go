// Package vultr creates machines (Cloud Compute instances) at Vultr. It follows the Hetzner driver: one REST client
// over compute.Client, Geass's labels written when the machine is made, one firewall group per cluster that Geass
// makes once and keeps, and every failure turned into one of the package's errors.
//
// What a maintainer has to know about Vultr:
//
//   - Instances have tags, not key-value labels, so the labels travel as "key:value" tags (compute.LabelsToTags),
//     written in the create request together with the firewall group, so both are in place atomically.
//   - There is no idempotency token and neither labels nor tags are unique. Create therefore looks for a live
//     instance that carries the node's tag, has the same label as spec.Name and belongs to the same cluster, and
//     returns it instead of making a second one. The label is part of the test because a replacement machine for the
//     same node has the same node tag and a different name (the attempt number).
//   - POST /instances answers 202. The instance is "pending" and its main_ip is "0.0.0.0" until it is built; that
//     placeholder is reported as no address. A machine is Running when its status is "active" and its power_status is
//     "running". server_status is not read: its values are not documented well enough to wait on.
//   - user_data must be base64. The image is an os_id; Geass resolves the name ("Ubuntu 24.04 LTS x64" unless
//     spec.Image says otherwise, a name from GET /v2/os or a numeric id) from the public OS list and remembers it.
//   - A firewall group has no rule "update": once it has an inbound rule, everything not allowed is dropped, and rules
//     are added and removed one call at a time. Geass finds the group by its description (geass-<cluster>), adds
//     the rules that are missing and then removes the ones that are not wanted, so a machine is never without SSH
//     while the rules change. The group is not unique by description, so two racing creates can make two; they
//     carry the same rules and the older one is used from then on.
//   - Vultr's API key has no scopes. Its permissions are the account user's ACLs, which Verify reads to warn about a
//     user that cannot provision machines or manage firewalls. A key may also be limited to some source addresses,
//     and then it fails with 403 from anywhere else.
//   - Vultr's Ubuntu images may ship with ufw switched on and only SSH allowed. The provider asks for it to be
//     switched off at first boot (compute.CloudInitCustomizer): the firewall group is the filter.
//   - Plans carry a monthly price only. Billing is hourly (one hour at least) and capped at the monthly price, so an
//     hour costs the monthly price divided by 672.
//   - The API allows 30 requests a second per source address; the shared client waits and retries on 429.
package vultr

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/degoke/geass/pkg/compute"
)

// ID is the provider's name in a GeassCloudConnection.
const ID = "Vultr"

const (
	defaultBaseURL = "https://api.vultr.com/v2"
	// defaultImageName is the operating system Geass installs when the spec names none.
	defaultImageName = "Ubuntu 24.04 LTS x64"
	// defaultImageID is the id of defaultImageName, used only if the OS list cannot be read as expected.
	defaultImageID = 2284
	// planType is the family of plans Geass offers: Cloud Compute.
	planType = "vc2"
	pageSize = 500
	// hoursPerMonth is the number of hours Vultr bills at most in a month, so that the monthly price is the cap.
	hoursPerMonth = 672
	// maxPages stops a listing that never ends, which only a misbehaving endpoint produces.
	maxPages = 1000
)

// Credential and setting keys.
const (
	keyAPIKey              = "apiKey"
	SettingFirewallGroupID = "firewallGroupId"
)

func init() {
	compute.Register(compute.Descriptor{
		ID:      ID,
		Label:   "Vultr",
		Summary: "An API key from an account whose user may provision and manage firewalls. Vultr keys have no scopes, so use a user that Geass has to itself, and add the operator's address to the key's access control if it lists any.",
		Fields:  []compute.Field{{Key: keyAPIKey, Label: "API key", Secret: true, Help: "Account > API > Enable API. If the key's access control is limited to some addresses, include the one the operator runs from."}},
		Settings: []compute.Setting{
			{Key: SettingFirewallGroupID, Label: "Firewall group ID", Help: "Use this existing firewall group instead of the one Geass makes. Geass attaches each machine to it and changes nothing in it."},
		},
		RegionHelp: "A region id: fra (Frankfurt), ams, lhr, cdg, sto, waw, mad, ewr, atl, ord, dfw, lax, sjc, sea, mia, yto, sgp, nrt, icn, syd, bom. Required.",
		New: func(creds compute.Credentials, options compute.Options) (compute.Provider, error) {
			return New(creds.Get(keyAPIKey), options), nil
		},
	})
}

// Provider is a compute.Provider for Vultr.
type Provider struct {
	api    *compute.Client
	region string

	mu  sync.Mutex
	oss map[string]int
}

// New returns a provider that acts with the API key.
func New(apiKey string, options compute.Options) *Provider {
	base := options.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	return &Provider{
		region: options.Region,
		oss:    map[string]int{},
		api: &compute.Client{
			HTTP:    options.HTTP,
			BaseURL: base,
			Header:  http.Header{"User-Agent": []string{"geass"}},
			Authorize: func(req *http.Request, _ []byte) error {
				req.Header.Set("Authorization", "Bearer "+apiKey)
				return nil
			},
			ClassifyError: classify,
		},
	}
}

// classify maps Vultr's error bodies ({"error": "...", "status": 400}) to the errors callers act on. Vultr documents no
// error codes, so a limit and a lack of capacity are recognised by what the message says.
func classify(method, path string, status int, body []byte) error {
	var parsed struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)
	message := parsed.Error
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	lower := strings.ToLower(message)
	switch {
	case status == http.StatusTooManyRequests, status == http.StatusNotFound:
		return nil // the shared client's own handling is right
	case isLimitMessage(lower):
		return &compute.QuotaError{Message: message}
	case isCapacityMessage(lower):
		return &compute.CapacityError{Message: message}
	case status == http.StatusUnauthorized:
		return fmt.Errorf("%w: %s (is the API key right, and does its access control allow this operator's address?)", compute.ErrAuth, message)
	case status == http.StatusForbidden:
		return fmt.Errorf("%w: %s (does the key's access control allow this operator's address, and may its user provision machines?)", compute.ErrAuth, message)
	}
	return nil
}

func isLimitMessage(lower string) bool {
	return (strings.Contains(lower, "limit") && !strings.Contains(lower, "rate limit")) || strings.Contains(lower, "maximum number")
}

func isCapacityMessage(lower string) bool {
	for _, phrase := range []string{"capacity", "out of stock", "sold out", "not available", "unavailable", "no available"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// meta is the pagination block of a list response: the next cursor is empty on the last page.
type meta struct {
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

// eachPage calls fetch with an empty cursor and then with each cursor it returns, until there is none.
func eachPage(fetch func(cursor string) (next string, err error)) error {
	cursor := ""
	for range maxPages {
		next, err := fetch(cursor)
		if err != nil {
			return err
		}
		if next == "" || next == cursor {
			return nil
		}
		cursor = next
	}
	return fmt.Errorf("the listing did not end after %d pages", maxPages)
}

type instance struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Region      string   `json:"region"`
	Plan        string   `json:"plan"`
	MainIP      string   `json:"main_ip"`
	InternalIP  string   `json:"internal_ip"`
	Status      string   `json:"status"`
	PowerStatus string   `json:"power_status"`
	DateCreated string   `json:"date_created"`
	Tags        []string `json:"tags"`
}

func (i instance) instance() compute.Instance {
	created, _ := time.Parse(time.RFC3339, i.DateCreated)
	return compute.Instance{
		ID: i.ID, Name: i.Label, Region: i.Region, Size: i.Plan, State: stateOf(i.Status, i.PowerStatus),
		PublicIPv4: realAddress(i.MainIP), PrivateIPv4: realAddress(i.InternalIP),
		Labels: compute.TagsToLabels(i.Tags), CreatedAt: created,
	}
}

// realAddress turns Vultr's placeholder for an address that does not exist yet into no address.
func realAddress(ip string) string {
	if ip == "0.0.0.0" {
		return ""
	}
	return ip
}

func stateOf(status, power string) compute.State {
	switch status {
	case "active":
		switch power {
		case "running":
			return compute.StateRunning
		case "stopped":
			return compute.StateStopped
		}
		return compute.StatePending
	case "suspended":
		return compute.StateStopped
	default:
		// "pending", "resizing", and anything the API adds later: not usable yet
		return compute.StatePending
	}
}

// Verify implements compute.Provider. It reads the account, whose ACLs are all a Vultr key can be asked about.
func (p *Provider) Verify(ctx context.Context) (compute.Account, error) {
	var out struct {
		Account struct {
			Name  string   `json:"name"`
			Email string   `json:"email"`
			ACLs  []string `json:"acls"`
		} `json:"account"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/account", nil, nil, &out); err != nil {
		return compute.Account{}, err
	}
	account := compute.Account{Name: "Vultr account"}
	for _, name := range []string{out.Account.Name, out.Account.Email} {
		if name != "" {
			account.Name = name
			break
		}
	}
	if len(out.Account.ACLs) == 0 {
		account.Warnings = append(account.Warnings, "Vultr did not say what the key's user may do, so Geass cannot tell whether it may provision machines and manage firewalls")
	} else {
		var missing []string
		for _, acl := range []string{"provisioning", "firewall"} {
			if !slices.Contains(out.Account.ACLs, acl) {
				missing = append(missing, acl)
			}
		}
		if len(missing) > 0 {
			account.Warnings = append(account.Warnings, "the key's user lacks the "+strings.Join(missing, " and ")+" permission, which Geass needs to make machines and their firewall")
		}
	}
	account.Warnings = append(account.Warnings, "if the key's access control lists addresses, the one this operator runs from must be among them, or every later call fails with 403")
	return account, nil
}

// Sizes implements compute.Provider.
func (p *Provider) Sizes(ctx context.Context, region string) ([]compute.Size, error) {
	var sizes []compute.Size
	err := eachPage(func(cursor string) (string, error) {
		var out struct {
			Plans []struct {
				ID          string   `json:"id"`
				VCPUCount   int      `json:"vcpu_count"`
				RAM         int64    `json:"ram"`
				Disk        int      `json:"disk"`
				MonthlyCost float64  `json:"monthly_cost"`
				Locations   []string `json:"locations"`
			} `json:"plans"`
			Meta meta `json:"meta"`
		}
		query := url.Values{"type": {planType}, "per_page": {strconv.Itoa(pageSize)}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if _, err := p.api.Do(ctx, http.MethodGet, "/plans", query, nil, &out); err != nil {
			return "", err
		}
		for _, plan := range out.Plans {
			if region != "" && !slices.Contains(plan.Locations, region) {
				continue
			}
			sizes = append(sizes, compute.Size{
				ID: plan.ID, Description: plan.ID, VCPUs: plan.VCPUCount, MemoryMiB: plan.RAM, DiskGB: plan.Disk,
				HourlyPrice: plan.MonthlyCost / hoursPerMonth, Currency: "USD", Arch: "amd64",
			})
		}
		return out.Meta.Links.Next, nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(sizes, func(a, b compute.Size) int {
		return cmp.Or(
			cmp.Compare(a.HourlyPrice, b.HourlyPrice),
			cmp.Compare(a.VCPUs, b.VCPUs),
			cmp.Compare(a.MemoryMiB, b.MemoryMiB),
			strings.Compare(a.ID, b.ID),
		)
	})
	return sizes, nil
}

// osID turns the spec's image into a Vultr os_id. An empty image is Ubuntu 24.04; a number is taken as the id; any
// other text is the name of an entry of the public OS list.
func (p *Provider) osID(ctx context.Context, image string) (int, error) {
	image = strings.TrimSpace(image)
	if n, err := strconv.Atoi(image); err == nil {
		return n, nil
	}
	want := image
	if want == "" {
		want = defaultImageName
	}
	key := strings.ToLower(want)
	p.mu.Lock()
	id, cached := p.oss[key]
	p.mu.Unlock()
	if cached {
		return id, nil
	}
	found := 0
	err := eachPage(func(cursor string) (string, error) {
		var out struct {
			OS []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"os"`
			Meta meta `json:"meta"`
		}
		query := url.Values{"per_page": {strconv.Itoa(pageSize)}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if _, err := p.api.Do(ctx, http.MethodGet, "/os", query, nil, &out); err != nil {
			return "", err
		}
		for _, os := range out.OS {
			if strings.EqualFold(os.Name, want) {
				found = os.ID
				return "", nil
			}
		}
		return out.Meta.Links.Next, nil
	})
	if err != nil {
		return 0, err
	}
	if found == 0 {
		if image != "" {
			return 0, fmt.Errorf("the Vultr API has no operating system named %q (use a name from the OS list, such as %q, or its number)", image, defaultImageName)
		}
		found = defaultImageID
	}
	p.mu.Lock()
	p.oss[key] = found
	p.mu.Unlock()
	return found, nil
}

// Create implements compute.Provider.
func (p *Provider) Create(ctx context.Context, spec compute.InstanceSpec) (compute.Instance, error) {
	region := spec.Region
	if region == "" {
		region = p.region
	}
	if region == "" {
		return compute.Instance{}, errors.New("a Vultr machine needs a region to create a machine in, and neither the pool nor the connection names one")
	}
	group, err := p.ensureFirewall(ctx, spec)
	if err != nil {
		return compute.Instance{}, fmt.Errorf("firewall: %w", err)
	}
	existing, err := p.findMachine(ctx, spec)
	if err != nil {
		return compute.Instance{}, err
	}
	if existing != nil {
		return existing.instance(), nil
	}
	os, err := p.osID(ctx, spec.Image)
	if err != nil {
		return compute.Instance{}, err
	}
	body := map[string]any{
		"region":           region,
		"plan":             spec.Size,
		"os_id":            os,
		"label":            spec.Name,
		"hostname":         spec.Name,
		"tags":             compute.LabelsToTags(spec.Labels),
		"user_data":        base64.StdEncoding.EncodeToString([]byte(spec.UserData)),
		"enable_ipv6":      false,
		"backups":          "disabled",
		"activation_email": false,
	}
	if group != "" {
		body["firewall_group_id"] = group
	}
	var out struct {
		Instance instance `json:"instance"`
	}
	if _, err := p.api.Do(ctx, http.MethodPost, "/instances", nil, body, &out); err != nil {
		return compute.Instance{}, err
	}
	created := out.Instance.instance()
	if len(created.Labels) == 0 {
		created.Labels = spec.Labels
	}
	return created, nil
}

// findMachine returns the live instance an earlier Create made for the spec, or nil.
func (p *Provider) findMachine(ctx context.Context, spec compute.InstanceSpec) (*instance, error) {
	tag := ""
	switch {
	case spec.Labels[compute.LabelNode] != "":
		tag = compute.LabelNode + ":" + spec.Labels[compute.LabelNode]
	case spec.Labels[compute.LabelCluster] != "":
		tag = compute.LabelCluster + ":" + spec.Labels[compute.LabelCluster]
	default:
		return nil, nil
	}
	found, err := p.listInstances(ctx, tag)
	if err != nil {
		return nil, err
	}
	for i := range found {
		m := found[i]
		labels := compute.TagsToLabels(m.Tags)
		if m.Label == spec.Name && labels[compute.LabelCluster] == spec.Labels[compute.LabelCluster] && m.instance().Alive() {
			return &m, nil
		}
	}
	return nil, nil
}

func (p *Provider) listInstances(ctx context.Context, tag string) ([]instance, error) {
	var all []instance
	err := eachPage(func(cursor string) (string, error) {
		var out struct {
			Instances []instance `json:"instances"`
			Meta      meta       `json:"meta"`
		}
		query := url.Values{"per_page": {strconv.Itoa(pageSize)}}
		if tag != "" {
			query.Set("tag", tag)
		}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if _, err := p.api.Do(ctx, http.MethodGet, "/instances", query, nil, &out); err != nil {
			return "", err
		}
		all = append(all, out.Instances...)
		return out.Meta.Links.Next, nil
	})
	return all, err
}

// Get implements compute.Provider.
func (p *Provider) Get(ctx context.Context, id string) (compute.Instance, error) {
	var out struct {
		Instance instance `json:"instance"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/instances/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return compute.Instance{}, err
	}
	return out.Instance.instance(), nil
}

// List implements compute.Provider. The API filters by one tag only, so the pool is told apart here.
func (p *Provider) List(ctx context.Context, owner compute.Owner) ([]compute.Instance, error) {
	if owner.Cluster == "" {
		return nil, nil
	}
	found, err := p.listInstances(ctx, compute.LabelCluster+":"+owner.Cluster)
	if err != nil {
		return nil, err
	}
	var instances []compute.Instance
	for _, m := range found {
		if got := m.instance(); owner.Owns(got.Labels) {
			instances = append(instances, got)
		}
	}
	return instances, nil
}

// Delete implements compute.Provider. Deleting an instance removes its disk and its address with it, and Geass makes
// nothing else for it.
func (p *Provider) Delete(ctx context.Context, id string) error {
	_, err := p.api.Do(ctx, http.MethodDelete, "/instances/"+url.PathEscape(id), nil, nil, nil)
	if errors.Is(err, compute.ErrNotFound) {
		return nil
	}
	return err
}

// CustomizeCloudInit implements compute.CloudInitCustomizer. Vultr's Ubuntu images may start with ufw on and only
// SSH let in, which would shut out the kubelet and the overlay whatever the firewall group says.
func (p *Provider) CustomizeCloudInit(init *compute.CloudInit, _ compute.InstanceSpec) {
	init.RunCommands = append(init.RunCommands, "if command -v ufw >/dev/null 2>&1; then ufw --force disable; fi")
}

type firewallGroup struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	DateCreated string `json:"date_created"`
}

type firewallRule struct {
	ID         json.Number `json:"id"`
	IPType     string      `json:"ip_type"`
	Type       string      `json:"type"`
	Protocol   string      `json:"protocol"`
	Port       string      `json:"port"`
	Subnet     string      `json:"subnet"`
	SubnetSize int         `json:"subnet_size"`
	Source     string      `json:"source"`
}

// ruleKey is what makes a rule the same rule, however the API spells it.
type ruleKey struct {
	ipType, protocol, port, subnet string
	size                           int
}

func (r firewallRule) key() (ruleKey, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(r.Subnet))
	if err != nil {
		return ruleKey{}, false
	}
	ipType := r.IPType
	if ipType == "" {
		ipType = r.Type
	}
	return ruleKey{ipType: ipType, protocol: strings.ToLower(r.Protocol), port: strings.TrimSpace(r.Port), subnet: addr.String(), size: r.SubnetSize}, true
}

type wantedRule struct {
	ruleKey
	notes string
}

// parsePrefix reads an address or a CIDR as the network it names.
func parsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		prefix, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not an address or a CIDR", s)
		}
		return netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()).Masked(), nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not an address or a CIDR", s)
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// wantedRules are what a machine's firewall group lets in: SSH and the kubelet from the control plane, and the
// overlay from anywhere in a multi-site cluster. Nothing else comes in; everything goes out.
func wantedRules(spec compute.InstanceSpec) ([]wantedRule, error) {
	everywhere := []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
	var control []netip.Prefix
	for _, cidr := range spec.ControlPlaneCIDRs {
		prefix, err := parsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("control plane address: %w", err)
		}
		control = append(control, prefix)
	}
	var rules []wantedRule
	add := func(protocol string, port int, from []netip.Prefix, notes string) {
		for _, prefix := range from {
			ipType := "v4"
			if prefix.Addr().Is6() {
				ipType = "v6"
			}
			rules = append(rules, wantedRule{
				ruleKey: ruleKey{ipType: ipType, protocol: protocol, port: strconv.Itoa(port), subnet: prefix.Addr().String(), size: prefix.Bits()},
				notes:   notes,
			})
		}
	}
	ssh := everywhere
	if len(control) > 0 {
		ssh = control
	}
	add("tcp", 22, ssh, "geass ssh")
	if len(control) > 0 {
		add("tcp", compute.KubeletPort, control, "geass kubelet")
	}
	if spec.MultiSite {
		add("udp", compute.OverlayPortUDP, everywhere, "geass overlay")
	}
	return rules, nil
}

// ensureFirewall returns the id of the firewall group for the machines of a cluster, making it the first time and
// bringing its rules up to date after. A group the user names is used as it is.
func (p *Provider) ensureFirewall(ctx context.Context, spec compute.InstanceSpec) (string, error) {
	if named := strings.TrimSpace(spec.Settings[SettingFirewallGroupID]); named != "" {
		return named, nil
	}
	cluster := spec.Labels[compute.LabelCluster]
	if cluster == "" {
		return "", nil
	}
	wanted, err := wantedRules(spec)
	if err != nil {
		return "", err
	}
	description := "geass-" + cluster
	groups, err := p.listGroups(ctx)
	if err != nil {
		return "", err
	}
	var group *firewallGroup
	for i := range groups {
		g := groups[i]
		if g.Description == description && (group == nil || g.DateCreated < group.DateCreated) {
			group = &g
		}
	}
	if group == nil {
		var created struct {
			FirewallGroup firewallGroup `json:"firewall_group"`
		}
		if _, err := p.api.Do(ctx, http.MethodPost, "/firewalls", nil, map[string]string{"description": description}, &created); err != nil {
			return "", err
		}
		group = &created.FirewallGroup
	}
	if err := p.syncRules(ctx, group.ID, wanted); err != nil {
		return "", err
	}
	return group.ID, nil
}

func (p *Provider) listGroups(ctx context.Context) ([]firewallGroup, error) {
	var all []firewallGroup
	err := eachPage(func(cursor string) (string, error) {
		var out struct {
			Groups []firewallGroup `json:"firewall_groups"`
			Meta   meta            `json:"meta"`
		}
		query := url.Values{"per_page": {strconv.Itoa(pageSize)}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if _, err := p.api.Do(ctx, http.MethodGet, "/firewalls", query, nil, &out); err != nil {
			return "", err
		}
		all = append(all, out.Groups...)
		return out.Meta.Links.Next, nil
	})
	return all, err
}

func (p *Provider) listRules(ctx context.Context, group string) ([]firewallRule, error) {
	var all []firewallRule
	err := eachPage(func(cursor string) (string, error) {
		var out struct {
			Rules []firewallRule `json:"firewall_rules"`
			Meta  meta           `json:"meta"`
		}
		query := url.Values{"per_page": {strconv.Itoa(pageSize)}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if _, err := p.api.Do(ctx, http.MethodGet, "/firewalls/"+url.PathEscape(group)+"/rules", query, nil, &out); err != nil {
			return "", err
		}
		all = append(all, out.Rules...)
		return out.Meta.Links.Next, nil
	})
	return all, err
}

// syncRules makes the group's rules the wanted ones: the missing ones are added first and the unwanted ones removed
// after, so that SSH is never closed while the rules change.
func (p *Provider) syncRules(ctx context.Context, group string, wanted []wantedRule) error {
	have, err := p.listRules(ctx, group)
	if err != nil {
		return err
	}
	want := map[ruleKey]bool{}
	for _, w := range wanted {
		want[w.ruleKey] = true
	}
	present := map[ruleKey]bool{}
	var unwanted []firewallRule
	for _, r := range have {
		key, ok := r.key()
		if !ok || r.Source != "" || !want[key] || present[key] {
			unwanted = append(unwanted, r) // not ours, not wanted, or a duplicate
			continue
		}
		present[key] = true
	}
	for _, w := range wanted {
		if present[w.ruleKey] {
			continue
		}
		body := map[string]any{
			"ip_type": w.ipType, "protocol": w.protocol, "subnet": w.subnet, "subnet_size": w.size, "port": w.port, "notes": w.notes,
		}
		if _, err := p.api.Do(ctx, http.MethodPost, "/firewalls/"+url.PathEscape(group)+"/rules", nil, body, nil); err != nil {
			return err
		}
		present[w.ruleKey] = true
	}
	for _, r := range unwanted {
		path := "/firewalls/" + url.PathEscape(group) + "/rules/" + url.PathEscape(r.ID.String())
		if _, err := p.api.Do(ctx, http.MethodDelete, path, nil, nil, nil); err != nil && !errors.Is(err, compute.ErrNotFound) {
			return err
		}
	}
	return nil
}

var (
	_ compute.Provider            = (*Provider)(nil)
	_ compute.CloudInitCustomizer = (*Provider)(nil)
)
