// Package digitalocean creates machines (Droplets) at DigitalOcean. It follows the Hetzner driver: one REST client
// over compute.Client, Geass's labels written when the machine is made, one firewall per cluster that Geass makes once
// and keeps, and every failure turned into one of the package's errors.
//
// What a maintainer has to know about DigitalOcean:
//
//   - Droplets have tags, not key-value labels, so the labels travel as "key:value" tags (compute.LabelsToTags).
//     A tag may hold letters, digits, ':', '-' and '_' up to 255 characters, which Geass's label values always fit.
//   - There is no idempotency token and Droplet names are not unique. Create therefore looks for a live Droplet that
//     carries the node's tag, has the same name and belongs to the same cluster, and returns it instead of making a
//     second one. The name is part of the test because a replacement machine for the same node has the same node tag
//     and a different name (the attempt number), and must not be mistaken for the one being removed.
//   - POST /droplets answers 202 with a Droplet that has no address yet (status "new", no networks). Callers poll Get.
//   - A Cloud Firewall cannot be named when a Droplet is created. It targets Droplets by tag instead: Geass makes one
//     firewall per cluster (geass-<cluster>) that targets the cluster tag, before the first Droplet, so that the
//     machine is covered as soon as it carries the tag. A firewall can only name tags that already exist, so the tag
//     is created first. A firewall the user names is attached to each Droplet by id right after it is created.
//   - A Cloud Firewall with no outbound rule blocks all outbound traffic, so the firewall carries allow-all outbound
//     rules for TCP and UDP. Inbound is only what the spec needs.
//   - The account's Droplet limit and a token that lacks a scope both arrive as 403, and the limit sometimes as 422.
//     The limit is told apart by its message, which names the Droplet limit.
//   - Deleting a Droplet deletes only the Droplet. Geass makes no snapshots, backups, volumes or reserved addresses,
//     so nothing else keeps billing.
//   - Every size is amd64. GPU sizes are left out of Sizes, because Geass would never use the GPU.
//   - The API allows 250 requests a minute and 5000 an hour; the shared client waits and retries on 429.
package digitalocean

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/degoke/geass/pkg/compute"
)

// ID is the provider's name in a GeassCloudConnection.
const ID = "DigitalOcean"

const (
	defaultBaseURL = "https://api.digitalocean.com/v2"
	defaultImage   = "ubuntu-24-04-x64"
	// pageSize is the largest page the API gives.
	pageSize = 200
	// maxPages stops a listing that never ends, which only a misbehaving endpoint produces.
	maxPages = 1000
)

// Credential and setting keys.
const (
	keyToken          = "token"
	SettingFirewallID = "firewallId"
	SettingVPCID      = "vpcUuid"
)

func init() {
	compute.Register(compute.Descriptor{
		ID:      ID,
		Label:   "DigitalOcean",
		Summary: "A personal access token with custom scopes: droplet (create, read, delete), tag (create, read), firewall (create, read, update), image:read, vpc:read, sizes:read and regions:read. Use a Team that Geass has to itself, so that nothing else is at risk.",
		Fields:  []compute.Field{{Key: keyToken, Label: "Personal access token", Secret: true, Help: "API > Tokens > Generate New Token, with the scopes listed above. A full-access token also works."}},
		Settings: []compute.Setting{
			{Key: SettingFirewallID, Label: "Firewall ID", Help: "Use this existing Cloud Firewall (its UUID) instead of the one Geass makes. Geass attaches each machine to it and changes nothing in it."},
			{Key: SettingVPCID, Label: "VPC UUID", Help: "Create the machine in this VPC instead of the region's default one."},
		},
		RegionHelp: "A region slug: nyc1, nyc3, sfo3, tor1 (North America), ams3, lon1, fra1 (Europe), sgp1, blr1, syd1 (Asia and Oceania). Empty lets DigitalOcean choose.",
		New: func(creds compute.Credentials, options compute.Options) (compute.Provider, error) {
			return New(creds.Get(keyToken), options), nil
		},
	})
}

// Provider is a compute.Provider for DigitalOcean Droplets.
type Provider struct {
	api *compute.Client
}

// New returns a provider that acts with the token.
func New(token string, options compute.Options) *Provider {
	base := options.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	return &Provider{api: &compute.Client{
		HTTP:    options.HTTP,
		BaseURL: base,
		Header:  http.Header{"User-Agent": []string{"geass"}},
		Authorize: func(req *http.Request, _ []byte) error {
			req.Header.Set("Authorization", "Bearer "+token)
			return nil
		},
		ClassifyError: classify,
	}}
}

// classify maps DigitalOcean's error bodies ({"id": "...", "message": "..."}) to the errors callers act on.
func classify(method, path string, status int, body []byte) error {
	var parsed struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &parsed)
	message := parsed.Message
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	lower := strings.ToLower(message)
	switch {
	case status == http.StatusTooManyRequests, status == http.StatusNotFound:
		return nil // the shared client's own handling is right
	case strings.Contains(lower, "limit") && !strings.Contains(lower, "rate limit"):
		return &compute.QuotaError{Message: message}
	case status == http.StatusUnprocessableEntity && (isCapacityMessage(lower) || strings.Contains(string(body), "available_regions")):
		return &compute.CapacityError{Message: message}
	case status == http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", compute.ErrAuth, message)
	case status == http.StatusForbidden:
		return fmt.Errorf("%w: %s (does the token have the scope for %s %s?)", compute.ErrAuth, message, method, path)
	}
	return nil
}

func isCapacityMessage(lower string) bool {
	for _, phrase := range []string{"capacity", "temporarily", "unavailable", "not available", "out of stock"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// pageLinks is the part of a list response that says where the next page is.
type pageLinks struct {
	Pages struct {
		Next string `json:"next"`
	} `json:"pages"`
}

// nextPage reads the page number out of the "next" link, which is an absolute URL. Empty means there is no next page.
func nextPage(link string) string {
	if link == "" {
		return ""
	}
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return u.Query().Get("page")
}

// eachPage calls fetch for page 1 and for each page it says follows, until there is none.
func eachPage(fetch func(page string) (next string, err error)) error {
	page := "1"
	for range maxPages {
		next, err := fetch(page)
		if err != nil {
			return err
		}
		if next == "" || next == page {
			return nil
		}
		page = next
	}
	return fmt.Errorf("the listing did not end after %d pages", maxPages)
}

type droplet struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	CreatedAt string   `json:"created_at"`
	SizeSlug  string   `json:"size_slug"`
	Tags      []string `json:"tags"`
	Region    struct {
		Slug string `json:"slug"`
	} `json:"region"`
	Networks struct {
		V4 []struct {
			IPAddress string `json:"ip_address"`
			Type      string `json:"type"`
		} `json:"v4"`
	} `json:"networks"`
}

func (d droplet) instance() compute.Instance {
	created, _ := time.Parse(time.RFC3339, d.CreatedAt)
	instance := compute.Instance{
		ID: strconv.FormatInt(d.ID, 10), Name: d.Name, Region: d.Region.Slug, Size: d.SizeSlug,
		State: stateOf(d.Status), Labels: compute.TagsToLabels(d.Tags), CreatedAt: created,
	}
	for _, n := range d.Networks.V4 {
		switch {
		case n.Type == "public" && instance.PublicIPv4 == "":
			instance.PublicIPv4 = n.IPAddress
		case n.Type == "private" && instance.PrivateIPv4 == "":
			instance.PrivateIPv4 = n.IPAddress
		}
	}
	return instance
}

func stateOf(status string) compute.State {
	switch status {
	case "active":
		return compute.StateRunning
	case "off":
		return compute.StateStopped
	case "archive":
		return compute.StateGone
	default:
		// "new", and anything the API adds later: not usable yet
		return compute.StatePending
	}
}

// Verify implements compute.Provider.
func (p *Provider) Verify(ctx context.Context) (compute.Account, error) {
	var droplets struct {
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/droplets", url.Values{"per_page": {"1"}}, nil, &droplets); err != nil {
		return compute.Account{}, err
	}
	account := compute.Account{
		Name:     "DigitalOcean account",
		Warnings: []string{"DigitalOcean cannot show which scopes a token has without creating something; a token that lacks droplet:create, tag:create or firewall:create passes this check and fails when the first machine is made"},
	}
	var info struct {
		Account struct {
			DropletLimit  int    `json:"droplet_limit"`
			Email         string `json:"email"`
			Name          string `json:"name"`
			Status        string `json:"status"`
			StatusMessage string `json:"status_message"`
			Team          struct {
				Name string `json:"name"`
			} `json:"team"`
		} `json:"account"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/account", nil, nil, &info); err != nil {
		if ctx.Err() != nil {
			return compute.Account{}, ctx.Err()
		}
		account.Warnings = append(account.Warnings, "the token cannot read the account (it lacks account:read), so Geass cannot show the Droplet limit or the account's status")
		return account, nil
	}
	a := info.Account
	for _, name := range []string{a.Team.Name, a.Name, a.Email} {
		if name != "" {
			account.Name = name
			break
		}
	}
	if a.Status != "" && a.Status != "active" {
		account.Warnings = append(account.Warnings, fmt.Sprintf("the account is %s: %s", a.Status, a.StatusMessage))
	}
	if a.DropletLimit > 0 && droplets.Meta.Total >= a.DropletLimit {
		account.Warnings = append(account.Warnings, fmt.Sprintf("the account has %d Droplets and its limit is %d, so no more can be created until one is deleted or the limit is raised", droplets.Meta.Total, a.DropletLimit))
	}
	return account, nil
}

// Sizes implements compute.Provider.
func (p *Provider) Sizes(ctx context.Context, region string) ([]compute.Size, error) {
	var sizes []compute.Size
	err := eachPage(func(page string) (string, error) {
		var out struct {
			Sizes []struct {
				Slug        string          `json:"slug"`
				Memory      int64           `json:"memory"`
				VCPUs       int             `json:"vcpus"`
				Disk        int             `json:"disk"`
				PriceHourly float64         `json:"price_hourly"`
				Regions     []string        `json:"regions"`
				Available   bool            `json:"available"`
				Description string          `json:"description"`
				GPUInfo     json.RawMessage `json:"gpu_info"`
			} `json:"sizes"`
			Links pageLinks `json:"links"`
		}
		query := url.Values{"per_page": {strconv.Itoa(pageSize)}, "page": {page}}
		if _, err := p.api.Do(ctx, http.MethodGet, "/sizes", query, nil, &out); err != nil {
			return "", err
		}
		for _, s := range out.Sizes {
			if !s.Available || hasGPU(s.GPUInfo) {
				continue
			}
			if region != "" && !slices.Contains(s.Regions, region) {
				continue
			}
			sizes = append(sizes, compute.Size{
				ID: s.Slug, Description: s.Description, VCPUs: s.VCPUs, MemoryMiB: s.Memory, DiskGB: s.Disk,
				HourlyPrice: s.PriceHourly, Currency: "USD", Arch: "amd64",
			})
		}
		return nextPage(out.Links.Pages.Next), nil
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

func hasGPU(info json.RawMessage) bool {
	text := strings.TrimSpace(string(info))
	return text != "" && text != "null" && text != "{}"
}

// Create implements compute.Provider.
func (p *Provider) Create(ctx context.Context, spec compute.InstanceSpec) (compute.Instance, error) {
	firewall, err := p.ensureFirewall(ctx, spec)
	if err != nil {
		return compute.Instance{}, fmt.Errorf("firewall: %w", err)
	}
	existing, err := p.findMachine(ctx, spec)
	if err != nil {
		return compute.Instance{}, err
	}
	if existing != nil {
		// An earlier create made it. Attaching a named firewall is safe to repeat, and covers a create that
		// stopped between the Droplet and its firewall.
		if err := p.attachNamed(ctx, firewall, strconv.FormatInt(existing.ID, 10)); err != nil {
			return compute.Instance{}, fmt.Errorf("firewall: %w", err)
		}
		return existing.instance(), nil
	}

	image := spec.Image
	if image == "" {
		image = defaultImage
	}
	body := map[string]any{
		"name":      spec.Name,
		"size":      spec.Size,
		"image":     image,
		"tags":      compute.LabelsToTags(spec.Labels),
		"user_data": spec.UserData,
		"ipv6":      false,
		// Geass never opens the web console, and the agent is installed at first boot by the image's own scripts.
		"with_droplet_agent": false,
	}
	if spec.Region != "" {
		body["region"] = spec.Region
	}
	if vpc := strings.TrimSpace(spec.Settings[SettingVPCID]); vpc != "" {
		body["vpc_uuid"] = vpc
	}
	var out struct {
		Droplet droplet `json:"droplet"`
	}
	if _, err := p.api.Do(ctx, http.MethodPost, "/droplets", nil, body, &out); err != nil {
		return compute.Instance{}, err
	}
	instance := out.Droplet.instance()
	if len(instance.Labels) == 0 {
		// The tags are applied as part of the request, but the answer need not show them yet.
		instance.Labels = spec.Labels
	}
	if err := p.attachNamed(ctx, firewall, instance.ID); err != nil {
		// The Droplet exists; a repeated Create finds it by its tag and tries the firewall again.
		return compute.Instance{}, fmt.Errorf("firewall: %w", err)
	}
	return instance, nil
}

// findMachine returns the live Droplet an earlier Create made for the spec, or nil.
func (p *Provider) findMachine(ctx context.Context, spec compute.InstanceSpec) (*droplet, error) {
	tag := ""
	switch {
	case spec.Labels[compute.LabelNode] != "":
		tag = compute.LabelNode + ":" + spec.Labels[compute.LabelNode]
	case spec.Labels[compute.LabelCluster] != "":
		tag = compute.LabelCluster + ":" + spec.Labels[compute.LabelCluster]
	default:
		return nil, nil
	}
	found, err := p.listDroplets(ctx, url.Values{"tag_name": {tag}})
	if err != nil {
		return nil, err
	}
	for i := range found {
		d := found[i]
		labels := compute.TagsToLabels(d.Tags)
		if d.Name == spec.Name && labels[compute.LabelCluster] == spec.Labels[compute.LabelCluster] && d.instance().Alive() {
			return &d, nil
		}
	}
	return nil, nil
}

func (p *Provider) listDroplets(ctx context.Context, filter url.Values) ([]droplet, error) {
	var all []droplet
	err := eachPage(func(page string) (string, error) {
		var out struct {
			Droplets []droplet `json:"droplets"`
			Links    pageLinks `json:"links"`
		}
		query := url.Values{"per_page": {strconv.Itoa(pageSize)}, "page": {page}}
		maps.Copy(query, filter)
		if _, err := p.api.Do(ctx, http.MethodGet, "/droplets", query, nil, &out); err != nil {
			return "", err
		}
		all = append(all, out.Droplets...)
		return nextPage(out.Links.Pages.Next), nil
	})
	return all, err
}

// Get implements compute.Provider.
func (p *Provider) Get(ctx context.Context, id string) (compute.Instance, error) {
	var out struct {
		Droplet droplet `json:"droplet"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/droplets/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return compute.Instance{}, err
	}
	return out.Droplet.instance(), nil
}

// List implements compute.Provider. The API filters by one tag only, so the pool is told apart here.
func (p *Provider) List(ctx context.Context, owner compute.Owner) ([]compute.Instance, error) {
	if owner.Cluster == "" {
		return nil, nil
	}
	found, err := p.listDroplets(ctx, url.Values{"tag_name": {compute.LabelCluster + ":" + owner.Cluster}})
	if err != nil {
		return nil, err
	}
	var instances []compute.Instance
	for _, d := range found {
		if instance := d.instance(); owner.Owns(instance.Labels) {
			instances = append(instances, instance)
		}
	}
	return instances, nil
}

// Delete implements compute.Provider. Deleting a Droplet removes the Droplet alone, and Geass makes nothing else.
func (p *Provider) Delete(ctx context.Context, id string) error {
	_, err := p.api.Do(ctx, http.MethodDelete, "/droplets/"+url.PathEscape(id), nil, nil, nil)
	if errors.Is(err, compute.ErrNotFound) {
		return nil
	}
	return err
}

type firewallTarget struct {
	Addresses        []string `json:"addresses,omitempty"`
	DropletIDs       []int64  `json:"droplet_ids,omitempty"`
	LoadBalancerUIDs []string `json:"load_balancer_uids,omitempty"`
	KubernetesIDs    []string `json:"kubernetes_ids,omitempty"`
	Tags             []string `json:"tags,omitempty"`
}

// extra reports whether the target names anything besides addresses, which Geass's rules never do.
func (t firewallTarget) extra() bool {
	return len(t.DropletIDs)+len(t.LoadBalancerUIDs)+len(t.KubernetesIDs)+len(t.Tags) > 0
}

type firewallRule struct {
	Protocol     string          `json:"protocol"`
	Ports        string          `json:"ports"`
	Action       string          `json:"action,omitempty"`
	Sources      *firewallTarget `json:"sources,omitempty"`
	Destinations *firewallTarget `json:"destinations,omitempty"`
}

type firewall struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Tags          []string       `json:"tags"`
	DropletIDs    []int64        `json:"droplet_ids"`
	InboundRules  []firewallRule `json:"inbound_rules"`
	OutboundRules []firewallRule `json:"outbound_rules"`
}

var anywhere = []string{"0.0.0.0/0", "::/0"}

// inboundRules are what a machine's firewall lets in: SSH and the kubelet from the control plane, and the overlay
// from anywhere in a multi-site cluster. Nothing else comes in.
func inboundRules(spec compute.InstanceSpec) []firewallRule {
	rule := func(protocol string, port int, addresses []string) firewallRule {
		return firewallRule{Protocol: protocol, Ports: strconv.Itoa(port), Action: "allow", Sources: &firewallTarget{Addresses: addresses}}
	}
	ssh := anywhere
	if len(spec.ControlPlaneCIDRs) > 0 {
		ssh = spec.ControlPlaneCIDRs
	}
	rules := []firewallRule{rule("tcp", 22, ssh)}
	if len(spec.ControlPlaneCIDRs) > 0 {
		rules = append(rules, rule("tcp", compute.KubeletPort, spec.ControlPlaneCIDRs))
	}
	if spec.MultiSite {
		rules = append(rules, rule("udp", compute.OverlayPortUDP, anywhere))
	}
	return rules
}

// outboundRules let everything out over TCP and UDP. Without an outbound rule DigitalOcean drops all outbound
// traffic, and a node that cannot reach the control plane, a registry or a name server is no node.
func outboundRules() []firewallRule {
	return []firewallRule{
		{Protocol: "tcp", Ports: "0", Action: "allow", Destinations: &firewallTarget{Addresses: anywhere}},
		{Protocol: "udp", Ports: "0", Action: "allow", Destinations: &firewallTarget{Addresses: anywhere}},
	}
}

// firewallRef is the firewall a new machine is covered by.
type firewallRef struct {
	// id is set for a firewall the user named: it is attached to each Droplet by id. Geass's own firewall targets
	// the cluster tag and needs nothing after the Droplet is created.
	id string
}

// ensureFirewall makes the cluster's firewall the first time and brings its rules up to date after. A firewall the
// user names is used as it is.
func (p *Provider) ensureFirewall(ctx context.Context, spec compute.InstanceSpec) (firewallRef, error) {
	if named := strings.TrimSpace(spec.Settings[SettingFirewallID]); named != "" {
		return firewallRef{id: named}, nil
	}
	cluster := spec.Labels[compute.LabelCluster]
	if cluster == "" {
		return firewallRef{}, nil
	}
	tag := compute.LabelCluster + ":" + cluster
	name := firewallName(cluster)
	inbound, outbound := inboundRules(spec), outboundRules()

	existing, err := p.listFirewalls(ctx)
	if err != nil {
		return firewallRef{}, err
	}
	for _, fw := range existing {
		if fw.Name != name {
			continue
		}
		if !slices.Contains(fw.Tags, tag) {
			return firewallRef{}, fmt.Errorf("a firewall named %s exists but does not target the cluster's tag: delete it or name another in the %s setting", name, SettingFirewallID)
		}
		if !sameRules(fw.InboundRules, inbound) || !sameRules(fw.OutboundRules, outbound) {
			// PUT replaces the whole firewall, so what is attached stays as it was.
			body := map[string]any{
				"name": fw.Name, "inbound_rules": inbound, "outbound_rules": outbound,
				"tags": fw.Tags, "droplet_ids": nonNil(fw.DropletIDs),
			}
			if _, err := p.api.Do(ctx, http.MethodPut, "/firewalls/"+url.PathEscape(fw.ID), nil, body, nil); err != nil {
				return firewallRef{}, err
			}
		}
		return firewallRef{}, nil
	}

	// A firewall can only target a tag that exists. Creating a Droplet makes its tags, but the firewall comes first.
	if _, err := p.api.Do(ctx, http.MethodPost, "/tags", nil, map[string]string{"name": tag}, nil); err != nil && !tagExists(err) {
		return firewallRef{}, fmt.Errorf("create tag %s: %w", tag, err)
	}
	body := map[string]any{"name": name, "tags": []string{tag}, "inbound_rules": inbound, "outbound_rules": outbound}
	if _, err := p.api.Do(ctx, http.MethodPost, "/firewalls", nil, body, nil); err != nil {
		return firewallRef{}, err
	}
	return firewallRef{}, nil
}

// tagExists reports whether err says the tag is already there, which is what Geass wanted.
func tagExists(err error) bool {
	var api *compute.APIError
	if !errors.As(err, &api) || (api.Status != http.StatusUnprocessableEntity && api.Status != http.StatusConflict && api.Status != http.StatusBadRequest) {
		return false
	}
	body := strings.ToLower(api.Body)
	return strings.Contains(body, "already") || strings.Contains(body, "exist")
}

func (p *Provider) listFirewalls(ctx context.Context) ([]firewall, error) {
	var all []firewall
	err := eachPage(func(page string) (string, error) {
		var out struct {
			Firewalls []firewall `json:"firewalls"`
			Links     pageLinks  `json:"links"`
		}
		query := url.Values{"per_page": {strconv.Itoa(pageSize)}, "page": {page}}
		if _, err := p.api.Do(ctx, http.MethodGet, "/firewalls", query, nil, &out); err != nil {
			return "", err
		}
		all = append(all, out.Firewalls...)
		return nextPage(out.Links.Pages.Next), nil
	})
	return all, err
}

// attachNamed puts a Droplet behind the firewall the user named. Geass's own firewall covers a Droplet by its tag.
func (p *Provider) attachNamed(ctx context.Context, firewall firewallRef, dropletID string) error {
	if firewall.id == "" {
		return nil
	}
	id, err := strconv.ParseInt(dropletID, 10, 64)
	if err != nil {
		return fmt.Errorf("the Droplet id %q is not a number", dropletID)
	}
	_, err = p.api.Do(ctx, http.MethodPost, "/firewalls/"+url.PathEscape(firewall.id)+"/droplets", nil, map[string]any{"droplet_ids": []int64{id}}, nil)
	return err
}

// firewallName is geass-<cluster>, kept inside what a firewall name may be.
func firewallName(cluster string) string {
	name := "geass-" + cluster
	if len(name) > 64 {
		name = strings.TrimRight(name[:64], "-")
	}
	return name
}

func nonNil(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

// sameRules reports whether the rules a firewall has are the rules wanted, whatever their order.
func sameRules(have, want []firewallRule) bool {
	key := func(r firewallRule) string {
		target := r.Sources
		if target == nil {
			target = r.Destinations
		}
		if target == nil {
			target = &firewallTarget{}
		}
		ports := r.Ports
		if ports == "" || ports == "all" {
			ports = "0"
		}
		addresses := slices.Clone(target.Addresses)
		slices.Sort(addresses)
		action := r.Action
		if action == "" {
			action = "allow"
		}
		return fmt.Sprintf("%s/%s/%s/%s/%t", strings.ToLower(r.Protocol), ports, action, strings.Join(addresses, ","), target.extra())
	}
	keys := func(rules []firewallRule) []string {
		out := make([]string, 0, len(rules))
		for _, r := range rules {
			out = append(out, key(r))
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(keys(have), keys(want))
}

var _ compute.Provider = (*Provider)(nil)
