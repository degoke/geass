// Package linode creates machines (Linodes) at Linode, now Akamai Cloud. It follows the Hetzner driver: one REST client
// over compute.Client, Geass's labels written when the machine is made, one firewall per cluster that Geass makes once
// and keeps, and every failure turned into one of the package's errors.
//
// What a maintainer has to know about Linode:
//
//   - Linodes have tags, not key-value labels, so the labels travel as "key:value" tags (compute.LabelsToTags's form).
//     A tag may be 50 characters at most, and a node or pool name can make "geass-node:<name>" longer. Such a label
//     is split over several tags (geass-node:<first part>, geass-node.2:<second part>) and joined again on the way
//     back, so that the labels a caller reads are the ones it wrote.
//   - The label of a Linode must start with a letter, hold letters, digits, '-', '_' and '.', have no doubled
//     separator and be 3 to 64 characters. A name that does not fit is cleaned up (the machine's Name is its label).
//   - There is no idempotency token and a Linode's name is not a promise. Create therefore looks for a live Linode
//     that carries the node's tag, has the same label as spec.Name and belongs to the same cluster, and returns it
//     instead of making a second one. The label is part of the test because a replacement machine for the same node
//     has the same node tag and a different name (the attempt number).
//   - Listing is filtered by the X-Filter header, which compute.Client does not set per request. The filter travels
//     in the request's context and the client's Authorize hook turns it into the header.
//   - Creating from an image needs root_pass. Geass sends a random 32-character password that it never stores or
//     shows: the account's login is the key in the cloud-init document, and that document turns password login off.
//   - The cloud-init document goes in metadata.user_data (base64), which only some regions support. Create reads the
//     region's capabilities and fails with a plain message when it lacks "Metadata", rather than creating a machine
//     that never learns its key.
//   - A firewall can be named in the create request only for Linodes that use legacy configuration interfaces. A
//     new account may default to Linode interfaces (account setting interfaces_for_new_linodes), so the request
//     asks for legacy_config explicitly. An account whose setting is linode_only refuses that, and then the create
//     fails with a message that says what to change.
//   - A firewall label is 3 to 32 characters and unique, so "geass-<cluster>" is shortened with a hash when the
//     cluster is long. The firewall carries the cluster's tag, which is how Geass knows it made it. Its inbound
//     policy is DROP and its rules are rewritten whole (PUT) when they differ.
//   - GET /linode/types and the region lookups are public. Types do not say where they are sold: Sizes asks the
//     region's availability list and leaves out what it marks as unavailable. GPU types are left out.
//   - Deleting a Linode releases its addresses and deletes its disks. A Linode that is still being built may answer
//     "busy", which a later Delete gets past. Billing is hourly, rounded up.
//   - Creating allows 20 requests per 15 seconds; the shared client waits and retries on 429.
package linode

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
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
const ID = "Linode"

const (
	defaultBaseURL = "https://api.linode.com/v4"
	defaultImage   = "linode/ubuntu24.04"
	pageSize       = 500
	// maxPages stops a listing that never ends, which only a misbehaving endpoint produces.
	maxPages = 1000
	// maxTagLength is the longest tag Linode takes.
	maxTagLength = 50
	// maxFirewallLabel is the longest firewall label Linode takes.
	maxFirewallLabel = 32
	// maxLabel is the longest Linode label.
	maxLabel = 64
)

// Credential and setting keys.
const (
	keyToken          = "token"
	SettingFirewallID = "firewallId"
)

func init() {
	compute.Register(compute.Descriptor{
		ID:      ID,
		Label:   "Linode (Akamai Cloud)",
		Summary: "A personal access token with the scopes Linodes (read/write), Firewalls (read/write) and IPs (read/write). Use an account that Geass has to itself, so that nothing else is at risk.",
		Fields:  []compute.Field{{Key: keyToken, Label: "Personal access token", Secret: true, Help: "Profile > API Tokens > Create a Personal Access Token, with read/write for Linodes, Firewalls and IPs."}},
		Settings: []compute.Setting{
			{Key: SettingFirewallID, Label: "Firewall ID", Help: "Use this existing Cloud Firewall (its number) instead of the one Geass makes. Geass attaches each machine to it and changes nothing in it."},
		},
		RegionHelp: "A region id that supports Metadata, such as us-east (Newark), us-ord (Chicago), us-iad (Washington), us-lax (Los Angeles), eu-west (London), eu-central (Frankfurt), ap-south (Singapore), ap-northeast (Tokyo) or ap-southeast (Sydney). Required.",
		New: func(creds compute.Credentials, options compute.Options) (compute.Provider, error) {
			return New(creds.Get(keyToken), options), nil
		},
	})
}

// filterKey is the context key that carries a list filter to the Authorize hook, which sets the X-Filter header.
type filterKey struct{}

func withFilter(ctx context.Context, filter map[string]string) context.Context {
	raw, err := json.Marshal(filter)
	if err != nil {
		return ctx // a map of strings always marshals
	}
	return context.WithValue(ctx, filterKey{}, string(raw))
}

// Provider is a compute.Provider for Linode.
type Provider struct {
	api    *compute.Client
	region string

	mu       sync.Mutex
	metadata map[string]bool
}

// New returns a provider that acts with the token.
func New(token string, options compute.Options) *Provider {
	base := options.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	return &Provider{
		region:   options.Region,
		metadata: map[string]bool{},
		api: &compute.Client{
			HTTP:    options.HTTP,
			BaseURL: base,
			Header:  http.Header{"User-Agent": []string{"geass"}},
			Authorize: func(req *http.Request, _ []byte) error {
				req.Header.Set("Authorization", "Bearer "+token)
				if filter, ok := req.Context().Value(filterKey{}).(string); ok {
					req.Header.Set("X-Filter", filter)
				}
				return nil
			},
			ClassifyError: classify,
		},
	}
}

// classify maps Linode's error bodies ({"errors": [{"field": "...", "reason": "..."}]}) to the errors callers act on.
// Linode documents no error codes, so a limit and a lack of capacity are recognised by what the message says.
func classify(method, path string, status int, body []byte) error {
	var parsed struct {
		Errors []struct {
			Field  string `json:"field"`
			Reason string `json:"reason"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(body, &parsed)
	var reasons []string
	interfaceRefused := false
	for _, e := range parsed.Errors {
		if e.Field != "" {
			reasons = append(reasons, e.Field+": "+e.Reason)
		} else {
			reasons = append(reasons, e.Reason)
		}
		if e.Field == "interface_generation" {
			interfaceRefused = true
		}
	}
	message := strings.Join(reasons, "; ")
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	lower := strings.ToLower(message)
	switch {
	case status == http.StatusTooManyRequests, status == http.StatusNotFound:
		return nil // the shared client's own handling is right
	case interfaceRefused:
		return fmt.Errorf("the Linode API refused legacy configuration interfaces (%s): Geass attaches its firewall to Linodes that use them, so set the account's interfaces_for_new_linodes to a value that allows legacy_config, or name a firewall and use an account that allows it", message)
	case strings.Contains(lower, "limit") && !strings.Contains(lower, "rate limit"):
		return &compute.QuotaError{Message: message}
	case isCapacityMessage(lower):
		return &compute.CapacityError{Message: message}
	case status == http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", compute.ErrAuth, message)
	case status == http.StatusForbidden:
		return fmt.Errorf("%w: %s (does the token have read/write for Linodes and Firewalls?)", compute.ErrAuth, message)
	}
	return nil
}

func isCapacityMessage(lower string) bool {
	for _, phrase := range []string{"capacity", "out of stock", "sold out", "not available", "unavailable", "no available"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// eachPage calls fetch for page 1 and each page after it, as long as the response says there are more.
func eachPage(fetch func(page int) (pages int, err error)) error {
	for page := 1; page <= maxPages; page++ {
		pages, err := fetch(page)
		if err != nil {
			return err
		}
		if page >= pages {
			return nil
		}
	}
	return fmt.Errorf("the listing did not end after %d pages", maxPages)
}

func pageQuery(page int) url.Values {
	return url.Values{"page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(pageSize)}}
}

// tagsFromLabels writes labels as tags in the "key:value" form, splitting a label whose tag would be longer than
// Linode allows over "key:...", "key.2:...", "key.3:..." so that labelsFromTags can join it again.
func tagsFromLabels(labels map[string]string) []string {
	var tags []string
	for _, key := range sortedKeys(labels) {
		value := labels[key]
		if len(key)+1+len(value) <= maxTagLength {
			tags = append(tags, key+":"+value)
			continue
		}
		for n := 1; value != ""; n++ {
			name := key
			if n > 1 {
				name = key + "." + strconv.Itoa(n)
			}
			room := maxTagLength - len(name) - 1
			if room < 1 {
				break // a key that long cannot be written at all; Linode will say so
			}
			take := min(room, len(value))
			tags = append(tags, name+":"+value[:take])
			value = value[take:]
		}
	}
	return tags
}

// labelsFromTags reads the tags tagsFromLabels wrote, and any other "key:value" tag, back into labels.
func labelsFromTags(tags []string) map[string]string {
	parts := map[string]map[int]string{}
	labels := map[string]string{}
	for _, tag := range tags {
		key, value, ok := strings.Cut(tag, ":")
		if !ok {
			labels[tag] = ""
			continue
		}
		base, n := key, 1
		if i := strings.LastIndex(key, "."); i > 0 {
			if num, err := strconv.Atoi(key[i+1:]); err == nil && num >= 2 {
				base, n = key[:i], num
			}
		}
		if parts[base] == nil {
			parts[base] = map[int]string{}
		}
		parts[base][n] = value
	}
	for base, byIndex := range parts {
		order := make([]int, 0, len(byIndex))
		for n := range byIndex {
			order = append(order, n)
		}
		slices.Sort(order)
		var value strings.Builder
		for _, n := range order {
			value.WriteString(byIndex[n])
		}
		labels[base] = value.String()
	}
	return labels
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// lookupTag is the tag a list filter uses to find machines with a label: the first piece of it.
func lookupTag(key, value string) string {
	return tagsFromLabels(map[string]string{key: value})[0]
}

// instanceLabel turns a machine name into a label Linode accepts: a letter first, letters, digits and single
// separators after, a letter or digit last, 3 to 64 characters.
func instanceLabel(name string) string {
	return cleanLabel(name, maxLabel)
}

func cleanLabel(name string, limit int) string {
	var b []byte
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case isLetter(c) || isDigit(c):
		case c == '-' || c == '_' || c == '.':
		default:
			c = '-'
		}
		if len(b) > 0 && b[len(b)-1] == c && !isLetter(c) && !isDigit(c) {
			continue // no two hyphens, underscores or dots in a row
		}
		b = append(b, c)
	}
	label := strings.TrimLeft(string(b), "-_.")
	if label == "" || !isLetter(label[0]) {
		label = "geass-" + label
	}
	if len(label) > limit {
		label = label[:limit]
	}
	label = strings.TrimRight(label, "-_.")
	if len(label) < 3 {
		label = "geass-" + label
	}
	return strings.TrimRight(label, "-_.")
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

type linode struct {
	ID      int64    `json:"id"`
	Label   string   `json:"label"`
	Status  string   `json:"status"`
	Region  string   `json:"region"`
	Type    string   `json:"type"`
	IPv4    []string `json:"ipv4"`
	Tags    []string `json:"tags"`
	Created string   `json:"created"`
}

func (l linode) instance() compute.Instance {
	instance := compute.Instance{
		ID: strconv.FormatInt(l.ID, 10), Name: l.Label, Region: l.Region, Size: l.Type, State: stateOf(l.Status),
		Labels: labelsFromTags(l.Tags), CreatedAt: parseTime(l.Created),
	}
	for _, text := range l.IPv4 {
		addr, err := netip.ParseAddr(text)
		if err != nil {
			continue
		}
		switch {
		case addr.IsPrivate() && instance.PrivateIPv4 == "":
			instance.PrivateIPv4 = text
		case !addr.IsPrivate() && instance.PublicIPv4 == "":
			instance.PublicIPv4 = text
		}
	}
	return instance
}

// parseTime reads Linode's timestamps, which are UTC and have no zone suffix.
func parseTime(text string) time.Time {
	if t, err := time.Parse(time.RFC3339, text); err == nil {
		return t
	}
	t, _ := time.ParseInLocation("2006-01-02T15:04:05", text, time.UTC)
	return t
}

func stateOf(status string) compute.State {
	switch status {
	case "running":
		return compute.StateRunning
	case "offline", "stopped", "shutting_down", "billing_suspension":
		return compute.StateStopped
	case "deleting":
		return compute.StateTerminating
	default:
		// provisioning, booting, busy, rebooting, migrating, rebuilding, cloning, restoring
		return compute.StatePending
	}
}

// Verify implements compute.Provider.
func (p *Provider) Verify(ctx context.Context) (compute.Account, error) {
	query := url.Values{"page_size": {"25"}}
	if _, err := p.api.Do(ctx, http.MethodGet, "/linode/instances", query, nil, nil); err != nil {
		return compute.Account{}, err
	}
	return compute.Account{
		Name: "Linode account",
		Warnings: []string{
			"Linode cannot show which scopes a token has without creating something; a token that cannot write Linodes or Firewalls passes this check and fails when the first machine is made",
			"Geass asks for legacy configuration interfaces, which it needs to attach its firewall when it creates a Linode; an account whose interfaces_for_new_linodes is linode_only refuses them, and Geass cannot read that setting without the account scope",
		},
	}, nil
}

// Sizes implements compute.Provider.
func (p *Provider) Sizes(ctx context.Context, region string) ([]compute.Size, error) {
	unavailable := map[string]bool{}
	if region != "" {
		var availability []struct {
			Plan      string `json:"plan"`
			Available bool   `json:"available"`
		}
		_, err := p.api.Do(ctx, http.MethodGet, "/regions/"+url.PathEscape(region)+"/availability", nil, nil, &availability)
		switch {
		case errors.Is(err, compute.ErrNotFound):
			return nil, fmt.Errorf("the Linode API has no region %q", region)
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case err == nil:
			for _, a := range availability {
				if !a.Available {
					unavailable[a.Plan] = true
				}
			}
		}
		// Any other failure leaves every type listed: a size that is not sold there fails at Create instead.
	}
	var sizes []compute.Size
	err := eachPage(func(page int) (int, error) {
		var out struct {
			Data []struct {
				ID                 string `json:"id"`
				Label              string `json:"label"`
				VCPUs              int    `json:"vcpus"`
				Memory             int64  `json:"memory"`
				Disk               int    `json:"disk"`
				GPUs               int    `json:"gpus"`
				AcceleratedDevices int    `json:"accelerated_devices"`
				Price              struct {
					Hourly float64 `json:"hourly"`
				} `json:"price"`
				RegionPrices []struct {
					ID     string  `json:"id"`
					Hourly float64 `json:"hourly"`
				} `json:"region_prices"`
			} `json:"data"`
			Pages int `json:"pages"`
		}
		if _, err := p.api.Do(ctx, http.MethodGet, "/linode/types", pageQuery(page), nil, &out); err != nil {
			return 0, err
		}
		for _, t := range out.Data {
			if t.GPUs > 0 || t.AcceleratedDevices > 0 || t.VCPUs == 0 || unavailable[t.ID] {
				continue
			}
			price := t.Price.Hourly
			for _, rp := range t.RegionPrices {
				if rp.ID == region {
					price = rp.Hourly
				}
			}
			sizes = append(sizes, compute.Size{
				ID: t.ID, Description: t.Label, VCPUs: t.VCPUs, MemoryMiB: t.Memory, DiskGB: t.Disk / 1024,
				HourlyPrice: price, Currency: "USD", Arch: "amd64",
			})
		}
		return out.Pages, nil
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

// requireMetadata fails when the region cannot give a machine its cloud-init document.
func (p *Provider) requireMetadata(ctx context.Context, region string) error {
	p.mu.Lock()
	has, known := p.metadata[region]
	p.mu.Unlock()
	if !known {
		var out struct {
			Capabilities []string `json:"capabilities"`
		}
		_, err := p.api.Do(ctx, http.MethodGet, "/regions/"+url.PathEscape(region), nil, nil, &out)
		if errors.Is(err, compute.ErrNotFound) {
			return fmt.Errorf("the Linode API has no region %q", region)
		}
		if err != nil {
			return err
		}
		has = slices.ContainsFunc(out.Capabilities, func(c string) bool { return strings.EqualFold(c, "Metadata") })
		p.mu.Lock()
		p.metadata[region] = has
		p.mu.Unlock()
	}
	if !has {
		return fmt.Errorf("the Linode region %s does not support Metadata, which is how Geass gives a machine its login (cloud-init); choose a region that does", region)
	}
	return nil
}

// randomPassword returns a strong password that has all four kinds of character. Geass never keeps it.
func randomPassword(length int) (string, error) {
	groups := []string{"abcdefghijklmnopqrstuvwxyz", "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "0123456789", "!@#%^*-_=+"}
	all := strings.Join(groups, "")
	pick := func(set string) (byte, error) {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		if err != nil {
			return 0, err
		}
		return set[n.Int64()], nil
	}
	out := make([]byte, 0, length)
	for _, g := range groups {
		c, err := pick(g)
		if err != nil {
			return "", err
		}
		out = append(out, c)
	}
	for len(out) < length {
		c, err := pick(all)
		if err != nil {
			return "", err
		}
		out = append(out, c)
	}
	for i := len(out) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", err
		}
		out[i], out[n.Int64()] = out[n.Int64()], out[i]
	}
	return string(out), nil
}

// Create implements compute.Provider.
func (p *Provider) Create(ctx context.Context, spec compute.InstanceSpec) (compute.Instance, error) {
	region := spec.Region
	if region == "" {
		region = p.region
	}
	if region == "" {
		return compute.Instance{}, errors.New("a Linode needs a region to create a machine in, and neither the pool nor the connection names one")
	}
	if err := p.requireMetadata(ctx, region); err != nil {
		return compute.Instance{}, err
	}
	firewall, err := p.ensureFirewall(ctx, spec)
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

	image := spec.Image
	if image == "" {
		image = defaultImage
	}
	password, err := randomPassword(32)
	if err != nil {
		return compute.Instance{}, fmt.Errorf("make a root password: %w", err)
	}
	body := map[string]any{
		"type":      spec.Size,
		"region":    region,
		"image":     image,
		"label":     instanceLabel(spec.Name),
		"root_pass": password,
		"tags":      tagsFromLabels(spec.Labels),
		"metadata":  map[string]string{"user_data": base64.StdEncoding.EncodeToString([]byte(spec.UserData))},
		"booted":    true,
		// The account may turn backups on for every new Linode, and they cost extra.
		"backups_enabled": false,
		// firewall_id works for legacy configuration interfaces only, and a new account may default to the other kind.
		"interface_generation": "legacy_config",
	}
	if firewall != 0 {
		body["firewall_id"] = firewall
	}
	if spec.PrivateAddress {
		body["private_ip"] = true
	}
	var out linode
	if _, err := p.api.Do(ctx, http.MethodPost, "/linode/instances", nil, body, &out); err != nil {
		return compute.Instance{}, err
	}
	created := out.instance()
	if len(created.Labels) == 0 {
		created.Labels = spec.Labels
	}
	return created, nil
}

// findMachine returns the live Linode an earlier Create made for the spec, or nil.
func (p *Provider) findMachine(ctx context.Context, spec compute.InstanceSpec) (*linode, error) {
	var tag string
	switch {
	case spec.Labels[compute.LabelNode] != "":
		tag = lookupTag(compute.LabelNode, spec.Labels[compute.LabelNode])
	case spec.Labels[compute.LabelCluster] != "":
		tag = lookupTag(compute.LabelCluster, spec.Labels[compute.LabelCluster])
	default:
		return nil, nil
	}
	found, err := p.listLinodes(ctx, tag)
	if err != nil {
		return nil, err
	}
	label := instanceLabel(spec.Name)
	for i := range found {
		l := found[i]
		labels := labelsFromTags(l.Tags)
		if l.Label == label && labels[compute.LabelCluster] == spec.Labels[compute.LabelCluster] && l.instance().Alive() {
			return &l, nil
		}
	}
	return nil, nil
}

func (p *Provider) listLinodes(ctx context.Context, tag string) ([]linode, error) {
	if tag != "" {
		ctx = withFilter(ctx, map[string]string{"tags": tag})
	}
	var all []linode
	err := eachPage(func(page int) (int, error) {
		var out struct {
			Data  []linode `json:"data"`
			Pages int      `json:"pages"`
		}
		if _, err := p.api.Do(ctx, http.MethodGet, "/linode/instances", pageQuery(page), nil, &out); err != nil {
			return 0, err
		}
		all = append(all, out.Data...)
		return out.Pages, nil
	})
	return all, err
}

// Get implements compute.Provider.
func (p *Provider) Get(ctx context.Context, id string) (compute.Instance, error) {
	var out linode
	if _, err := p.api.Do(ctx, http.MethodGet, "/linode/instances/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return compute.Instance{}, err
	}
	return out.instance(), nil
}

// List implements compute.Provider. The API filters by one tag only, so the pool is told apart here.
func (p *Provider) List(ctx context.Context, owner compute.Owner) ([]compute.Instance, error) {
	if owner.Cluster == "" {
		return nil, nil
	}
	found, err := p.listLinodes(ctx, lookupTag(compute.LabelCluster, owner.Cluster))
	if err != nil {
		return nil, err
	}
	var instances []compute.Instance
	for _, l := range found {
		if got := l.instance(); owner.Owns(got.Labels) {
			instances = append(instances, got)
		}
	}
	return instances, nil
}

// Delete implements compute.Provider. Deleting a Linode releases its addresses and deletes its disks, and Geass makes
// nothing else for it. A Linode that is still being built answers "busy"; the caller deletes again.
func (p *Provider) Delete(ctx context.Context, id string) error {
	_, err := p.api.Do(ctx, http.MethodDelete, "/linode/instances/"+url.PathEscape(id), nil, nil, nil)
	if errors.Is(err, compute.ErrNotFound) {
		return nil
	}
	return err
}

type firewallRule struct {
	Action      string `json:"action"`
	Protocol    string `json:"protocol"`
	Ports       string `json:"ports,omitempty"`
	Addresses   addrs  `json:"addresses"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

type addrs struct {
	IPv4 []string `json:"ipv4,omitempty"`
	IPv6 []string `json:"ipv6,omitempty"`
}

type firewallRules struct {
	Inbound        []firewallRule `json:"inbound"`
	InboundPolicy  string         `json:"inbound_policy"`
	Outbound       []firewallRule `json:"outbound"`
	OutboundPolicy string         `json:"outbound_policy"`
}

type firewall struct {
	ID    int64         `json:"id"`
	Label string        `json:"label"`
	Tags  []string      `json:"tags"`
	Rules firewallRules `json:"rules"`
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

// addresses splits networks into the lists a rule takes, in IP/mask form.
func addresses(prefixes []netip.Prefix) addrs {
	var out addrs
	for _, prefix := range prefixes {
		if prefix.Addr().Is6() {
			out.IPv6 = append(out.IPv6, prefix.String())
		} else {
			out.IPv4 = append(out.IPv4, prefix.String())
		}
	}
	return out
}

// inboundRules are what a machine's firewall lets in: SSH and the kubelet from the control plane, and the overlay
// from anywhere in a multi-site cluster. Nothing else comes in; everything goes out.
func inboundRules(spec compute.InstanceSpec) ([]firewallRule, error) {
	everywhere := []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
	var control []netip.Prefix
	for _, cidr := range spec.ControlPlaneCIDRs {
		prefix, err := parsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("control plane address: %w", err)
		}
		control = append(control, prefix)
	}
	rule := func(label, protocol string, port int, from []netip.Prefix) firewallRule {
		return firewallRule{Action: "ACCEPT", Protocol: protocol, Ports: strconv.Itoa(port), Addresses: addresses(from), Label: label}
	}
	ssh := everywhere
	if len(control) > 0 {
		ssh = control
	}
	rules := []firewallRule{rule("ssh", "TCP", 22, ssh)}
	if len(control) > 0 {
		rules = append(rules, rule("kubelet", "TCP", compute.KubeletPort, control))
	}
	if spec.MultiSite {
		rules = append(rules, rule("overlay", "UDP", compute.OverlayPortUDP, everywhere))
	}
	return rules, nil
}

// firewallLabel is geass-<cluster>, kept inside the 32 characters a firewall label may have: a cluster that does
// not fit is cut short and given a hash, so that two long clusters still get two labels.
func firewallLabel(cluster string) string {
	label := cleanLabel("geass-"+cluster, 1<<10)
	if len(label) <= maxFirewallLabel {
		return label
	}
	sum := sha256.Sum256([]byte(cluster))
	suffix := "-" + hex.EncodeToString(sum[:4])
	head := strings.TrimRight(label[:maxFirewallLabel-len(suffix)], "-_.")
	return head + suffix
}

// ensureFirewall returns the id of the firewall for the machines of a cluster, making it the first time and
// bringing its rules up to date after. A firewall the user names is used as it is. Zero means no firewall.
func (p *Provider) ensureFirewall(ctx context.Context, spec compute.InstanceSpec) (int64, error) {
	if raw := strings.TrimSpace(spec.Settings[SettingFirewallID]); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("the %s setting %q is not a firewall id", SettingFirewallID, raw)
		}
		return id, nil
	}
	cluster := spec.Labels[compute.LabelCluster]
	if cluster == "" {
		return 0, nil
	}
	inbound, err := inboundRules(spec)
	if err != nil {
		return 0, err
	}
	label := firewallLabel(cluster)
	tags := tagsFromLabels(map[string]string{compute.LabelCluster: cluster})
	want := firewallRules{Inbound: inbound, InboundPolicy: "DROP", Outbound: []firewallRule{}, OutboundPolicy: "ACCEPT"}

	found, err := p.findFirewall(ctx, label)
	if err != nil {
		return 0, err
	}
	if found == nil {
		body := map[string]any{"label": label, "tags": tags, "rules": want}
		var created firewall
		if _, err := p.api.Do(ctx, http.MethodPost, "/networking/firewalls", nil, body, &created); err != nil {
			// Labels are unique, so a create that raced another one fails here: use the winner's firewall.
			if again, findErr := p.findFirewall(ctx, label); findErr == nil && again != nil {
				found = again
			} else {
				return 0, err
			}
		} else {
			return created.ID, nil
		}
	}
	for _, tag := range tags {
		if !slices.Contains(found.Tags, tag) {
			return 0, fmt.Errorf("a firewall labelled %s exists but is not tagged for this cluster: delete it or name another in the %s setting", label, SettingFirewallID)
		}
	}
	if !sameRules(found.Rules, want) {
		path := fmt.Sprintf("/networking/firewalls/%d/rules", found.ID)
		if _, err := p.api.Do(ctx, http.MethodPut, path, nil, want, nil); err != nil {
			return 0, err
		}
	}
	return found.ID, nil
}

func (p *Provider) findFirewall(ctx context.Context, label string) (*firewall, error) {
	ctx = withFilter(ctx, map[string]string{"label": label})
	var match *firewall
	err := eachPage(func(page int) (int, error) {
		var out struct {
			Data  []firewall `json:"data"`
			Pages int        `json:"pages"`
		}
		if _, err := p.api.Do(ctx, http.MethodGet, "/networking/firewalls", pageQuery(page), nil, &out); err != nil {
			return 0, err
		}
		for i := range out.Data {
			if out.Data[i].Label == label && match == nil {
				match = &out.Data[i]
			}
		}
		return out.Pages, nil
	})
	return match, err
}

// sameRules reports whether the firewall's rules are the wanted ones, whatever their order.
func sameRules(have, want firewallRules) bool {
	if have.InboundPolicy != want.InboundPolicy || have.OutboundPolicy != want.OutboundPolicy || len(have.Outbound) != len(want.Outbound) {
		return false
	}
	key := func(r firewallRule) string {
		v4, v6 := slices.Clone(r.Addresses.IPv4), slices.Clone(r.Addresses.IPv6)
		slices.Sort(v4)
		slices.Sort(v6)
		return fmt.Sprintf("%s/%s/%s/%s/%s", strings.ToUpper(r.Protocol), r.Ports, strings.ToUpper(r.Action), strings.Join(v4, ","), strings.Join(v6, ","))
	}
	keys := func(rules []firewallRule) []string {
		out := make([]string, 0, len(rules))
		for _, r := range rules {
			out = append(out, key(r))
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(keys(have.Inbound), keys(want.Inbound))
}

var _ compute.Provider = (*Provider)(nil)
