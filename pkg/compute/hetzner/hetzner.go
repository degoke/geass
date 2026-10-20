// Package hetzner creates machines at Hetzner Cloud. It is the reference for the other providers of pkg/compute: one
// REST client over compute.Client, the machine's labels set when it is created, a firewall that Geass makes once and
// keeps, and every failure turned into one of the package's errors.
//
// Hetzner's token belongs to one Project and is either read-only or read and write, so the advice to users is to give
// Geass a Project of its own: nothing outside it can be touched, and everything in it is Geass's to delete.
package hetzner

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/degoke/geass/pkg/compute"
)

// ID is the provider's name in a GeassCloudConnection.
const ID = "Hetzner"

const (
	defaultBaseURL = "https://api.hetzner.cloud/v1"
	defaultImage   = "ubuntu-24.04"
	pageSize       = 50
)

// Credential and setting keys.
const (
	keyToken          = "token"
	SettingFirewallID = "firewallId"
	SettingNetworkID  = "networkId"
)

func init() {
	compute.Register(compute.Descriptor{
		ID:      ID,
		Label:   "Hetzner Cloud",
		Summary: "An API token with Read & Write access, from a Hetzner Cloud Project that Geass has to itself. A token belongs to one Project, so nothing outside it can be touched.",
		Fields:  []compute.Field{{Key: keyToken, Label: "API token", Secret: true, Help: "Project > Security > API tokens, with Read & Write permission."}},
		Settings: []compute.Setting{
			{Key: SettingFirewallID, Label: "Firewall ID", Help: "Use this existing firewall instead of the one Geass makes."},
			{Key: SettingNetworkID, Label: "Private network ID", Help: "Attach the machine to this private network as well."},
		},
		RegionHelp: "A location: fsn1, nbg1, hel1 (Europe), ash, hil (US), sin (Singapore). Empty lets Hetzner choose.",
		New: func(creds compute.Credentials, options compute.Options) (compute.Provider, error) {
			return New(creds.Get(keyToken), options), nil
		},
	})
}

// Provider is a compute.Provider for Hetzner Cloud.
type Provider struct {
	api *compute.Client
}

// New returns a provider that acts with the token.
func New(token string, options compute.Options) *Provider {
	base := options.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	api := &compute.Client{
		HTTP:    options.HTTP,
		BaseURL: base,
		Header:  http.Header{"User-Agent": []string{"geass"}},
		Authorize: func(req *http.Request, _ []byte) error {
			req.Header.Set("Authorization", "Bearer "+token)
			return nil
		},
		ClassifyError: classify,
	}
	return &Provider{api: api}
}

// apiError is Hetzner's error body.
type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// classify maps Hetzner's error codes to the errors callers act on.
func classify(method, path string, status int, body []byte) error {
	var parsed apiError
	_ = json.Unmarshal(body, &parsed)
	code, message := parsed.Error.Code, parsed.Error.Message
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	switch code {
	case "resource_limit_exceeded", "server_limit_reached", "primary_ip_limit_exceeded":
		return &compute.QuotaError{Message: message}
	case "placement_error", "resource_unavailable", "no_space_left_in_location":
		return &compute.CapacityError{Message: message}
	case "uniqueness_error":
		return &uniquenessError{message: message}
	case "unauthorized":
		return fmt.Errorf("%w: %s", compute.ErrAuth, message)
	case "forbidden":
		return fmt.Errorf("%w: %s (is the token Read & Write?)", compute.ErrAuth, message)
	}
	return nil
}

type uniquenessError struct{ message string }

func (e *uniquenessError) Error() string { return "a server with that name exists: " + e.message }

type server struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Created  string `json:"created"`
	PublicIP struct {
		IPv4 struct {
			IP string `json:"ip"`
		} `json:"ipv4"`
	} `json:"public_net"`
	PrivateNet []struct {
		IP string `json:"ip"`
	} `json:"private_net"`
	Labels     map[string]string `json:"labels"`
	ServerType struct {
		Name string `json:"name"`
	} `json:"server_type"`
	Datacenter struct {
		Location struct {
			Name string `json:"name"`
		} `json:"location"`
	} `json:"datacenter"`
}

func (s server) instance() compute.Instance {
	created, _ := time.Parse(time.RFC3339, s.Created)
	instance := compute.Instance{
		ID: strconv.FormatInt(s.ID, 10), Name: s.Name, Size: s.ServerType.Name, Region: s.Datacenter.Location.Name,
		Labels: s.Labels, CreatedAt: created, PublicIPv4: s.PublicIP.IPv4.IP,
		State: stateOf(s.Status),
	}
	if len(s.PrivateNet) > 0 {
		instance.PrivateIPv4 = s.PrivateNet[0].IP
	}
	return instance
}

func stateOf(status string) compute.State {
	switch status {
	case "running":
		return compute.StateRunning
	case "off", "stopping":
		return compute.StateStopped
	case "deleting":
		return compute.StateTerminating
	default:
		// initializing, starting, migrating, rebuilding, unknown
		return compute.StatePending
	}
}

// Verify implements compute.Provider.
func (p *Provider) Verify(ctx context.Context) (compute.Account, error) {
	var out struct {
		Servers []server `json:"servers"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/servers", url.Values{"per_page": {"1"}}, nil, &out); err != nil {
		return compute.Account{}, err
	}
	return compute.Account{
		Name:     "Hetzner Cloud project",
		Warnings: []string{"Hetzner cannot tell a Read & Write token from a Read-only one without creating something; a read-only token passes this check and fails when the first machine is created"},
	}, nil
}

// Sizes implements compute.Provider.
func (p *Provider) Sizes(ctx context.Context, region string) ([]compute.Size, error) {
	var sizes []compute.Size
	for page := 1; ; page++ {
		var out struct {
			ServerTypes []struct {
				Name         string  `json:"name"`
				Description  string  `json:"description"`
				Cores        int     `json:"cores"`
				Memory       float64 `json:"memory"`
				Disk         int     `json:"disk"`
				Architecture string  `json:"architecture"`
				Deprecated   bool    `json:"deprecated"`
				Prices       []struct {
					Location string `json:"location"`
					Hourly   struct {
						Net string `json:"net"`
					} `json:"price_hourly"`
				} `json:"prices"`
			} `json:"server_types"`
			Meta struct {
				Pagination struct {
					NextPage *int `json:"next_page"`
				} `json:"pagination"`
			} `json:"meta"`
		}
		query := url.Values{"per_page": {strconv.Itoa(pageSize)}, "page": {strconv.Itoa(page)}}
		if _, err := p.api.Do(ctx, http.MethodGet, "/server_types", query, nil, &out); err != nil {
			return nil, err
		}
		for _, t := range out.ServerTypes {
			if t.Deprecated {
				continue
			}
			size := compute.Size{
				ID: t.Name, Description: t.Description, VCPUs: t.Cores, MemoryMiB: int64(t.Memory * 1024),
				DiskGB: t.Disk, Currency: "EUR", Arch: map[string]string{"x86": "amd64", "arm": "arm64"}[t.Architecture],
			}
			for _, price := range t.Prices {
				if region == "" || price.Location == region {
					if v, err := strconv.ParseFloat(price.Hourly.Net, 64); err == nil && (size.HourlyPrice == 0 || v < size.HourlyPrice) {
						size.HourlyPrice = v
					}
				}
			}
			if region != "" && size.HourlyPrice == 0 {
				continue // not offered there
			}
			sizes = append(sizes, size)
		}
		if out.Meta.Pagination.NextPage == nil || *out.Meta.Pagination.NextPage == 0 {
			break
		}
	}
	slices.SortStableFunc(sizes, func(a, b compute.Size) int { return cmp.Compare(a.HourlyPrice, b.HourlyPrice) })
	return sizes, nil
}

// Create implements compute.Provider.
func (p *Provider) Create(ctx context.Context, spec compute.InstanceSpec) (compute.Instance, error) {
	firewall, err := p.ensureFirewall(ctx, spec)
	if err != nil {
		return compute.Instance{}, fmt.Errorf("firewall: %w", err)
	}
	image := spec.Image
	if image == "" {
		image = defaultImage
	}
	body := map[string]any{
		"name":               spec.Name,
		"server_type":        spec.Size,
		"image":              image,
		"labels":             spec.Labels,
		"user_data":          spec.UserData,
		"start_after_create": true,
		"public_net":         map[string]any{"enable_ipv4": true, "enable_ipv6": false},
	}
	if spec.Region != "" {
		body["location"] = spec.Region
	}
	if firewall != 0 {
		body["firewalls"] = []map[string]any{{"firewall": firewall}}
	}
	if id := strings.TrimSpace(spec.Settings[SettingNetworkID]); id != "" {
		if n, err := strconv.ParseInt(id, 10, 64); err == nil {
			body["networks"] = []int64{n}
		}
	}
	var out struct {
		Server server `json:"server"`
	}
	if _, err := p.api.Do(ctx, http.MethodPost, "/servers", nil, body, &out); err != nil {
		var unique *uniquenessError
		if errors.As(err, &unique) {
			// The name is taken. If the machine is this owner's, an earlier create made it: that is the answer.
			if existing, findErr := p.findByName(ctx, spec.Name); findErr == nil && existing != nil && existing.Labels[compute.LabelCluster] == spec.Labels[compute.LabelCluster] {
				return existing.instance(), nil
			}
		}
		return compute.Instance{}, err
	}
	return out.Server.instance(), nil
}

func (p *Provider) findByName(ctx context.Context, name string) (*server, error) {
	var out struct {
		Servers []server `json:"servers"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/servers", url.Values{"name": {name}}, nil, &out); err != nil {
		return nil, err
	}
	if len(out.Servers) == 0 {
		return nil, nil
	}
	return &out.Servers[0], nil
}

// Get implements compute.Provider.
func (p *Provider) Get(ctx context.Context, id string) (compute.Instance, error) {
	var out struct {
		Server server `json:"server"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/servers/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return compute.Instance{}, err
	}
	return out.Server.instance(), nil
}

// List implements compute.Provider.
func (p *Provider) List(ctx context.Context, owner compute.Owner) ([]compute.Instance, error) {
	selector := []string{compute.LabelManaged + "=true", compute.LabelCluster + "=" + owner.Cluster}
	if owner.Pool != "" {
		selector = append(selector, compute.LabelPool+"="+owner.Pool)
	}
	var instances []compute.Instance
	for page := 1; ; page++ {
		var out struct {
			Servers []server `json:"servers"`
			Meta    struct {
				Pagination struct {
					NextPage *int `json:"next_page"`
				} `json:"pagination"`
			} `json:"meta"`
		}
		query := url.Values{"label_selector": {strings.Join(selector, ",")}, "per_page": {strconv.Itoa(pageSize)}, "page": {strconv.Itoa(page)}}
		if _, err := p.api.Do(ctx, http.MethodGet, "/servers", query, nil, &out); err != nil {
			return nil, err
		}
		for _, s := range out.Servers {
			if owner.Owns(s.Labels) {
				instances = append(instances, s.instance())
			}
		}
		if out.Meta.Pagination.NextPage == nil || *out.Meta.Pagination.NextPage == 0 {
			return instances, nil
		}
	}
}

// Delete implements compute.Provider. Deleting a server releases its primary address with it.
func (p *Provider) Delete(ctx context.Context, id string) error {
	_, err := p.api.Do(ctx, http.MethodDelete, "/servers/"+url.PathEscape(id), nil, nil, nil)
	if errors.Is(err, compute.ErrNotFound) {
		return nil
	}
	return err
}

type firewallRule struct {
	Direction   string   `json:"direction"`
	Protocol    string   `json:"protocol"`
	Port        string   `json:"port,omitempty"`
	SourceIPs   []string `json:"source_ips"`
	Description string   `json:"description,omitempty"`
}

// firewallRules are what a machine's firewall lets in: SSH and the kubelet from the control plane, and the overlay
// from anywhere in a multi-site cluster. Nothing else comes in; everything goes out.
func firewallRules(spec compute.InstanceSpec) []firewallRule {
	anywhere := []string{"0.0.0.0/0", "::/0"}
	ssh := anywhere
	if len(spec.ControlPlaneCIDRs) > 0 {
		ssh = spec.ControlPlaneCIDRs
	}
	rules := []firewallRule{{Direction: "in", Protocol: "tcp", Port: "22", SourceIPs: ssh}}
	if len(spec.ControlPlaneCIDRs) > 0 {
		rules = append(rules, firewallRule{Direction: "in", Protocol: "tcp", Port: strconv.Itoa(compute.KubeletPort), SourceIPs: spec.ControlPlaneCIDRs})
	}
	if spec.MultiSite {
		rules = append(rules, firewallRule{Direction: "in", Protocol: "udp", Port: strconv.Itoa(compute.OverlayPortUDP), SourceIPs: anywhere})
	}
	return rules
}

// ensureFirewall returns the id of the firewall for the machines of a cluster, making it the first time and
// bringing its rules up to date after. A firewall the user names is used as it is.
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
	name := "geass-" + shorten(cluster, 40)
	rules := firewallRules(spec)
	var found struct {
		Firewalls []struct {
			ID    int64          `json:"id"`
			Rules []firewallRule `json:"rules"`
		} `json:"firewalls"`
	}
	query := url.Values{"label_selector": {compute.LabelManaged + "=true," + compute.LabelCluster + "=" + cluster}}
	if _, err := p.api.Do(ctx, http.MethodGet, "/firewalls", query, nil, &found); err != nil {
		return 0, err
	}
	if len(found.Firewalls) > 0 {
		existing := found.Firewalls[0]
		if !sameRules(existing.Rules, rules) {
			if _, err := p.api.Do(ctx, http.MethodPost, fmt.Sprintf("/firewalls/%d/actions/set_rules", existing.ID), nil, map[string]any{"rules": rules}, nil); err != nil {
				return 0, err
			}
		}
		return existing.ID, nil
	}
	var created struct {
		Firewall struct {
			ID int64 `json:"id"`
		} `json:"firewall"`
	}
	body := map[string]any{"name": name, "labels": map[string]string{compute.LabelManaged: "true", compute.LabelCluster: cluster}, "rules": rules}
	if _, err := p.api.Do(ctx, http.MethodPost, "/firewalls", nil, body, &created); err != nil {
		var unique *uniquenessError
		if errors.As(err, &unique) {
			return 0, fmt.Errorf("a firewall named %s exists but is not labelled as Geass's: delete it or name it in the %s setting", name, SettingFirewallID)
		}
		return 0, err
	}
	return created.Firewall.ID, nil
}

func sameRules(a, b []firewallRule) bool {
	norm := func(rules []firewallRule) []string {
		out := make([]string, 0, len(rules))
		for _, r := range rules {
			ips := append([]string(nil), r.SourceIPs...)
			slices.Sort(ips)
			out = append(out, r.Direction+"/"+r.Protocol+"/"+r.Port+"/"+strings.Join(ips, ","))
		}
		slices.Sort(out)
		return out
	}
	x, y := norm(a), norm(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func shorten(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// accessDescription marks the rules Geass keeps in the firewall of the control plane: one rule for each protocol and
// port, whose source list holds the addresses of the machines. A rule without the mark is never touched.
const accessDescription = "geass-access"

// AllowAccess implements compute.AccessManager.
func (p *Provider) AllowAccess(ctx context.Context, target compute.AccessTarget, rules []compute.AccessRule) error {
	return p.changeAccess(ctx, target, rules, true)
}

// RevokeAccess implements compute.AccessManager.
func (p *Provider) RevokeAccess(ctx context.Context, target compute.AccessTarget, rules []compute.AccessRule) error {
	return p.changeAccess(ctx, target, rules, false)
}

func (p *Provider) changeAccess(ctx context.Context, target compute.AccessTarget, rules []compute.AccessRule, allow bool) error {
	if len(rules) == 0 {
		return nil
	}
	var current struct {
		Firewall struct {
			Rules []firewallRule `json:"rules"`
		} `json:"firewall"`
	}
	path := "/firewalls/" + url.PathEscape(target.ID)
	if _, err := p.api.Do(ctx, http.MethodGet, path, nil, nil, &current); err != nil {
		return err
	}
	updated := append([]firewallRule(nil), current.Firewall.Rules...)
	changed := false
	for _, rule := range rules {
		port := strconv.Itoa(rule.Port)
		index := -1
		for i, existing := range updated {
			if existing.Description == accessDescription && existing.Direction == "in" && existing.Protocol == rule.Protocol && existing.Port == port {
				index = i
				break
			}
		}
		switch {
		case allow && index < 0:
			updated = append(updated, firewallRule{Direction: "in", Protocol: rule.Protocol, Port: port, SourceIPs: []string{rule.CIDR}, Description: accessDescription})
			changed = true
		case allow && !containsString(updated[index].SourceIPs, rule.CIDR):
			updated[index].SourceIPs = append(updated[index].SourceIPs, rule.CIDR)
			changed = true
		case !allow && index >= 0 && containsString(updated[index].SourceIPs, rule.CIDR):
			var kept []string
			for _, ip := range updated[index].SourceIPs {
				if ip != rule.CIDR {
					kept = append(kept, ip)
				}
			}
			updated[index].SourceIPs = kept
			changed = true
		}
	}
	// A rule with no source would let nobody in, which Hetzner does not accept: it goes.
	pruned := updated[:0]
	for _, rule := range updated {
		if rule.Description == accessDescription && len(rule.SourceIPs) == 0 {
			continue
		}
		pruned = append(pruned, rule)
	}
	if !changed {
		return nil
	}
	_, err := p.api.Do(ctx, http.MethodPost, path+"/actions/set_rules", nil, map[string]any{"rules": pruned}, nil)
	return err
}

func containsString(list []string, s string) bool { return slices.Contains(list, s) }

var (
	_ compute.Provider      = (*Provider)(nil)
	_ compute.AccessManager = (*Provider)(nil)
)
