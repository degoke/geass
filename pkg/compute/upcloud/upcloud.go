// Package upcloud creates machines at UpCloud Cloud Servers through API 1.3.
//
// What a maintainer must know:
//   - A server is made in one call that returns 202 with the server in state "maintenance", with its addresses already
//     assigned; UpCloud starts it by itself. The disk is a clone of a public template (Ubuntu Server 24.04, found by
//     title) and the machine is made with a public and a utility (private) IPv4 interface, because the cloud-native
//     plans need the storage and the address stated.
//   - The cloud-init templates take no password: a login user with the cluster's key is part of the create call, and
//     the cloud-init document goes in user_data. Metadata cannot be turned off on them.
//   - The firewall is per server, not per cluster, and only a server that is not in maintenance accepts rules, which
//     then take one to two minutes to apply. So the rules are written right after the create when the server lets
//     them, and otherwise by the first Get (or List, or repeated Create) that sees it started. What the rules should
//     be travels on the server itself, in the geass-fw label, so that no later call needs the original spec. A server
//     is reported Pending until its rules are in place, so that it is never handed out without them. The rules are
//     written in one call that replaces the chain, and end with a drop of everything else coming in.
//   - UpCloud has labels (key and value) but filters a list only by key, or by key=value written as "key%3Dvalue", with
//     every filter having to match. The list gives a summary without addresses, so each owned server is read again.
//   - A server can be deleted only when it is stopped, and its storage stays and bills unless the delete says to
//     remove it. Delete is therefore two calls on two servers' states: it stops a started server and returns, and it
//     deletes a stopped server together with its storage and backups.
//   - Prices are in cents of the account's currency per hour.
//   - Credentials are either an API token (Bearer, ucat_..., valid for at most a year) or a sub-account's user name and
//     password (HTTP Basic). A token wins when both are given.
package upcloud

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/degoke/geass/pkg/compute"
)

// ID is the provider's name in a GeassCloudConnection.
const ID = "UpCloud"

const (
	defaultBaseURL = "https://api.upcloud.com/1.3"
	pageSize       = 100
	// defaultTemplateTitle is what the default image is looked for by, among the public templates.
	defaultTemplateTitle = "Ubuntu Server 24.04"
	// fallbackTemplate is Ubuntu Server 24.04 LTS (Noble Numbat), used when the template list does not show it.
	fallbackTemplate = "01000000-0000-4000-8000-000030240200"
	defaultDiskGB    = 25
	defaultTier      = "maxiops"
	// firewallLabel carries what the server's firewall should let in, as "cp=<cidr>,<cidr>;ms=<0|1>".
	firewallLabel = "geass-fw"
)

// Credential and setting keys.
const (
	keyToken    = "token"
	keyUsername = "username"
	keyPassword = "password"

	SettingDiskGB = "diskGb"
)

func init() {
	compute.Register(compute.Descriptor{
		ID:    ID,
		Label: "UpCloud",
		Summary: "Either an API token (ucat_..., made for Geass with the shortest life you can renew, at most a year) or the user name and " +
			"password of a sub-account that has API access and is allowed to create servers and storages. A sub-account cannot touch " +
			"what the main account has, so give Geass one of its own.",
		Fields: []compute.Field{
			{Key: keyToken, Label: "API token", Secret: true, Optional: true, Help: "People > Account > API tokens. It wins over the user name and password. Tokens expire within a year."},
			{Key: keyUsername, Label: "API user name", Optional: true, Help: "A sub-account with API access. Needed when there is no token."},
			{Key: keyPassword, Label: "API password", Secret: true, Optional: true},
		},
		Settings: []compute.Setting{
			{Key: SettingDiskGB, Label: "Disk (GB)", Help: "Size of the machine's disk. Default: what the plan includes, or 25 GB for a plan that includes none."},
		},
		RegionHelp: "A zone: de-fra1 (Frankfurt), fi-hel1, fi-hel2 (Helsinki), nl-ams1 (Amsterdam), uk-lon1 (London), pl-waw1 (Warsaw), es-mad1 (Madrid), se-sto1 (Stockholm), us-nyc1, us-chi1, us-sjo1, sg-sin1, au-syd1.",
		New: func(creds compute.Credentials, options compute.Options) (compute.Provider, error) {
			if creds.Get(keyToken) == "" && (creds.Get(keyUsername) == "" || creds.Get(keyPassword) == "") {
				return nil, errors.New("an API token, or an API user name and password, is needed for UpCloud")
			}
			return New(creds.Get(keyToken), creds.Get(keyUsername), creds.Get(keyPassword), options), nil
		},
	})
}

// Provider is a compute.Provider for UpCloud.
type Provider struct {
	api  *compute.Client
	zone string

	mu sync.Mutex
	// firewalled holds the servers whose rules this provider has seen to be in place.
	firewalled map[string]bool
}

// New returns a provider that acts with the token, or with the user name and password.
func New(token, username, password string, options compute.Options) *Provider {
	base := options.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	api := &compute.Client{
		HTTP:    options.HTTP,
		BaseURL: base,
		Header:  http.Header{"User-Agent": []string{"geass"}},
		Authorize: func(req *http.Request, _ []byte) error {
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			} else {
				req.SetBasicAuth(username, password)
			}
			return nil
		},
		ClassifyError: classify,
	}
	return &Provider{api: api, zone: options.Region, firewalled: map[string]bool{}}
}

// apiError is UpCloud's error body.
type apiError struct {
	Error struct {
		Code    string `json:"error_code"`
		Message string `json:"error_message"`
	} `json:"error"`
}

// classify maps UpCloud's error codes to the errors callers act on.
func classify(method, path string, status int, body []byte) error {
	var parsed apiError
	_ = json.Unmarshal(body, &parsed)
	code, message := parsed.Error.Code, parsed.Error.Message
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if code != "" {
		message = code + ": " + message
	}
	switch {
	case strings.HasSuffix(code, "_LIMIT_REACHED"), code == "INSUFFICIENT_CREDITS":
		return &compute.QuotaError{Message: message}
	case code == "SERVER_RESOURCES_UNAVAILABLE", code == "STORAGE_RESOURCES_UNAVAILABLE",
		code == "IP_ADDRESS_RESOURCES_UNAVAILABLE", code == "SERVER_RESOURCES_UNAVAILABLE_FOR_STRICT_AA":
		return &compute.CapacityError{Message: message}
	case code == "SERVER_STATE_ILLEGAL":
		return &stateError{message: message}
	case status == http.StatusForbidden:
		return fmt.Errorf("%w (forbidden): %s (may this account use the API and create servers and storages?)", compute.ErrAuth, message)
	}
	return nil
}

// stateError is UpCloud refusing a call because the server is in a state that does not allow it.
type stateError struct{ message string }

func (e *stateError) Error() string {
	return "the server is in a state that does not allow that: " + e.message
}

// --- servers ---

type label struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type labelList struct {
	Label []label `json:"label"`
}

type ipAddress struct {
	Access  string `json:"access"`
	Address string `json:"address"`
	Family  string `json:"family"`
}

type server struct {
	UUID      string    `json:"uuid"`
	Title     string    `json:"title"`
	Hostname  string    `json:"hostname"`
	State     string    `json:"state"`
	Zone      string    `json:"zone"`
	Plan      string    `json:"plan"`
	Labels    labelList `json:"labels"`
	Addresses struct {
		IPAddress []ipAddress `json:"ip_address"`
	} `json:"ip_addresses"`
}

func (s server) labels() map[string]string {
	out := map[string]string{}
	for _, l := range s.Labels.Label {
		out[l.Key] = l.Value
	}
	return out
}

func (s server) instance() compute.Instance {
	instance := compute.Instance{
		ID: s.UUID, Name: s.Title, Region: s.Zone, Size: s.Plan, State: stateOf(s.State), Labels: s.labels(),
	}
	for _, ip := range s.Addresses.IPAddress {
		if ip.Family != "IPv4" {
			continue
		}
		switch ip.Access {
		case "public":
			if instance.PublicIPv4 == "" {
				instance.PublicIPv4 = ip.Address
			}
		case "utility", "private":
			if instance.PrivateIPv4 == "" {
				instance.PrivateIPv4 = ip.Address
			}
		}
	}
	return instance
}

// stateOf translates a server's state. "maintenance" is also what a server is while it is being made or stopped.
func stateOf(state string) compute.State {
	switch state {
	case "started":
		return compute.StateRunning
	case "stopped", "error":
		return compute.StateStopped
	default:
		return compute.StatePending
	}
}

func serverPath(uuid, rest string) string { return "/server/" + url.PathEscape(uuid) + rest }

func (p *Provider) getServer(ctx context.Context, uuid string) (server, error) {
	var out struct {
		Server server `json:"server"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, serverPath(uuid, ""), nil, nil, &out); err != nil {
		return server{}, err
	}
	return out.Server, nil
}

func (p *Provider) zoneOf(region string) (string, error) {
	zone := strings.TrimSpace(region)
	if zone == "" {
		zone = p.zone
	}
	if zone == "" {
		return "", errors.New("an UpCloud machine needs a zone, such as de-fra1")
	}
	return zone, nil
}

// Verify implements compute.Provider.
func (p *Provider) Verify(ctx context.Context) (compute.Account, error) {
	var out struct {
		Account struct {
			Username       string `json:"username"`
			ResourceLimits struct {
				Cores      int   `json:"cores"`
				Memory     int64 `json:"memory"`
				PublicIPv4 int   `json:"public_ipv4"`
			} `json:"resource_limits"`
		} `json:"account"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/account", nil, nil, &out); err != nil {
		return compute.Account{}, err
	}
	account := compute.Account{Name: "UpCloud account " + out.Account.Username}
	limits := out.Account.ResourceLimits
	if (limits.Cores > 0 && limits.Cores < 8) || (limits.PublicIPv4 > 0 && limits.PublicIPv4 < 5) {
		account.Warnings = append(account.Warnings, fmt.Sprintf(
			"the account may use at most %d CPU cores, %d MiB of memory and %d public IPv4 addresses, which caps how far a pool can scale",
			limits.Cores, limits.Memory, limits.PublicIPv4))
	}
	account.Warnings = append(account.Warnings,
		"UpCloud cannot say whether a credential may create servers without creating one: a sub-account without that permission passes this check and fails when the first machine is created")
	return account, nil
}

// --- sizes ---

type plan struct {
	Name        string `json:"name"`
	CoreNumber  int    `json:"core_number"`
	Memory      int64  `json:"memory_amount"`
	StorageSize int    `json:"storage_size"`
	StorageTier string `json:"storage_tier"`
}

func (p *Provider) plans(ctx context.Context) ([]plan, error) {
	var out struct {
		Plans struct {
			Plan []plan `json:"plan"`
		} `json:"plans"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/plan", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Plans.Plan, nil
}

// Sizes implements compute.Provider.
func (p *Provider) Sizes(ctx context.Context, region string) ([]compute.Size, error) {
	zone, _ := p.zoneOf(region)
	plans, err := p.plans(ctx)
	if err != nil {
		return nil, err
	}
	// Prices are best effort: a credential that cannot read them still gets the sizes.
	prices := map[string]float64{}
	priced := false
	var priceList struct {
		Prices struct {
			Zone []map[string]json.RawMessage `json:"zone"`
		} `json:"prices"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/price", nil, nil, &priceList); err == nil {
		for _, entry := range priceList.Prices.Zone {
			var name string
			_ = json.Unmarshal(entry["name"], &name)
			if zone != "" && name != zone {
				continue
			}
			for key, raw := range entry {
				var amount struct {
					Price float64 `json:"price"`
				}
				if strings.HasPrefix(key, "server_plan_") && json.Unmarshal(raw, &amount) == nil {
					prices[strings.TrimPrefix(key, "server_plan_")] = amount.Price / 100
					priced = true
				}
			}
		}
	}
	currency := "EUR"
	var account struct {
		Account struct {
			Currency string `json:"currency"`
		} `json:"account"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/account", nil, nil, &account); err == nil && account.Account.Currency != "" {
		currency = account.Account.Currency
	}

	var sizes []compute.Size
	for _, pl := range plans {
		if pl.Name == "" || pl.CoreNumber <= 0 {
			continue
		}
		price, offered := prices[pl.Name]
		if priced && !offered {
			continue // not offered in that zone
		}
		sizes = append(sizes, compute.Size{
			ID: pl.Name, Description: pl.Name, VCPUs: pl.CoreNumber, MemoryMiB: pl.Memory, DiskGB: pl.StorageSize,
			HourlyPrice: price, Currency: currency, Arch: "amd64",
		})
	}
	slices.SortStableFunc(sizes, func(a, b compute.Size) int {
		if (a.HourlyPrice == 0) != (b.HourlyPrice == 0) {
			if a.HourlyPrice != 0 {
				return -1
			}
			return 1
		}
		if c := cmp.Compare(a.HourlyPrice, b.HourlyPrice); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return sizes, nil
}

// --- create ---

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Create implements compute.Provider.
func (p *Provider) Create(ctx context.Context, spec compute.InstanceSpec) (compute.Instance, error) {
	zone, err := p.zoneOf(spec.Region)
	if err != nil {
		return compute.Instance{}, err
	}
	labels := map[string]string{}
	maps.Copy(labels, spec.Labels)
	if labels[compute.LabelNode] == "" {
		labels[compute.LabelNode] = spec.Name
	}
	policy, err := firewallPolicy(spec)
	if err != nil {
		return compute.Instance{}, err
	}

	existing, err := p.findServer(ctx, labels)
	if err != nil {
		return compute.Instance{}, err
	}
	var srv server
	if existing != nil {
		// The list has no addresses: read the server itself.
		srv, err = p.getServer(ctx, existing.UUID)
	} else {
		srv, err = p.createServer(ctx, zone, spec, labels, policy)
	}
	if err != nil {
		return compute.Instance{}, err
	}
	// The rules go in as soon as the server lets them. A server still in maintenance may not, and is seen to by
	// the first Get that finds it started, which is why a failure here is not one of the create.
	if !p.isFirewalled(srv.UUID) {
		_ = p.ensureFirewall(ctx, srv.UUID, policy)
	}
	instance, _ := p.view(ctx, srv, false)
	return instance, nil
}

// findServer returns the server of a node, in any state, or nil.
func (p *Provider) findServer(ctx context.Context, labels map[string]string) (*server, error) {
	filters := []string{
		compute.LabelNode + "=" + labels[compute.LabelNode],
		compute.LabelCluster + "=" + labels[compute.LabelCluster],
	}
	servers, err := p.listServers(ctx, filters)
	if err != nil {
		return nil, err
	}
	for i := range servers {
		l := servers[i].labels()
		if l[compute.LabelNode] == labels[compute.LabelNode] && l[compute.LabelCluster] == labels[compute.LabelCluster] {
			return &servers[i], nil
		}
	}
	return nil, nil
}

func (p *Provider) createServer(ctx context.Context, zone string, spec compute.InstanceSpec, labels map[string]string, policy string) (server, error) {
	template, err := p.resolveTemplate(ctx, spec.Image)
	if err != nil {
		return server{}, err
	}
	size, tier, err := p.diskOf(ctx, spec)
	if err != nil {
		return server{}, err
	}
	all := map[string]string{firewallLabel: policy}
	maps.Copy(all, labels)
	keys := slices.Sorted(maps.Keys(all))
	var list labelList
	for _, k := range keys {
		list.Label = append(list.Label, label{Key: k, Value: all[k]})
	}

	body := map[string]any{
		"zone":     zone,
		"title":    spec.Name,
		"hostname": spec.Name,
		"plan":     spec.Size,
		"storage_devices": map[string]any{"storage_device": []map[string]any{{
			"action": "clone", "storage": template, "title": spec.Name + "-disk", "size": size, "tier": tier,
		}}},
		"networking": map[string]any{"interfaces": map[string]any{"interface": []map[string]any{
			{"type": "public", "ip_addresses": map[string]any{"ip_address": []map[string]string{{"family": "IPv4"}}}},
			{"type": "utility", "ip_addresses": map[string]any{"ip_address": []map[string]string{{"family": "IPv4"}}}},
		}}},
		"labels":            list,
		"metadata":          "yes",
		"firewall":          "on",
		"password_delivery": "none",
	}
	if spec.UserData != "" {
		body["user_data"] = spec.UserData
	}
	if len(spec.AuthorizedKeys) > 0 {
		body["login_user"] = map[string]any{
			"username":        compute.DefaultUser,
			"create_password": "no",
			"ssh_keys":        map[string]any{"ssh_key": spec.AuthorizedKeys},
		}
	}
	var out struct {
		Server server `json:"server"`
	}
	if _, err := p.api.Do(ctx, http.MethodPost, "/server", nil, map[string]any{"server": body}, &out); err != nil {
		return server{}, err
	}
	return out.Server, nil
}

// resolveTemplate finds the template to clone: an id as it is, or the public template whose title holds the image
// (Ubuntu Server 24.04 when none is named), preferring one made for cloud-init.
func (p *Provider) resolveTemplate(ctx context.Context, image string) (string, error) {
	image = strings.TrimSpace(image)
	if uuidPattern.MatchString(image) {
		return image, nil
	}
	want := image
	if want == "" {
		want = defaultTemplateTitle
	}
	var out struct {
		Storages struct {
			Storage []struct {
				UUID         string `json:"uuid"`
				Title        string `json:"title"`
				TemplateType string `json:"template_type"`
				State        string `json:"state"`
			} `json:"storage"`
		} `json:"storages"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, "/storage/template", nil, nil, &out); err != nil {
		if errors.Is(err, compute.ErrAuth) {
			return "", err
		}
		if image == "" {
			return fallbackTemplate, nil
		}
		return "", err
	}
	found := ""
	for _, t := range out.Storages.Storage {
		if !strings.Contains(strings.ToLower(t.Title), strings.ToLower(want)) || (t.State != "" && t.State != "online") {
			continue
		}
		if t.TemplateType == "cloud-init" {
			return t.UUID, nil
		}
		if found == "" {
			found = t.UUID
		}
	}
	switch {
	case found != "":
		return found, nil
	case image == "":
		return fallbackTemplate, nil
	}
	return "", fmt.Errorf("no template called %q at UpCloud", image)
}

// diskOf returns the size and tier of the machine's disk: what the pool says, or what the plan includes.
func (p *Provider) diskOf(ctx context.Context, spec compute.InstanceSpec) (int, string, error) {
	if raw := strings.TrimSpace(spec.Settings[SettingDiskGB]); raw != "" {
		gb, err := strconv.Atoi(raw)
		if err != nil || gb <= 0 {
			return 0, "", fmt.Errorf("the %s setting %q is not a number of gigabytes", SettingDiskGB, raw)
		}
		return gb, defaultTier, nil
	}
	size, tier := defaultDiskGB, defaultTier
	if plans, err := p.plans(ctx); err == nil {
		for _, pl := range plans {
			if pl.Name == spec.Size {
				if pl.StorageSize > 0 {
					size = pl.StorageSize
				}
				if pl.StorageTier != "" {
					tier = pl.StorageTier
				}
			}
		}
	} else if errors.Is(err, compute.ErrAuth) {
		return 0, "", err
	}
	return size, tier, nil
}

// --- get, list ---

// Get implements compute.Provider.
func (p *Provider) Get(ctx context.Context, id string) (compute.Instance, error) {
	srv, err := p.getServer(ctx, id)
	if err != nil {
		return compute.Instance{}, err
	}
	return p.view(ctx, srv, true)
}

// view turns a server into a machine. A server that Geass made, and that is started, gets its firewall checked the
// first time it is seen; until the rules are in place it is reported Pending. With strict, a failure to check them is
// returned, so that what is wrong (a credential that may not touch firewalls) is shown instead of waiting for ever.
func (p *Provider) view(ctx context.Context, srv server, strict bool) (compute.Instance, error) {
	instance := srv.instance()
	if srv.State != "started" {
		return instance, nil
	}
	policy := srv.labels()[firewallLabel]
	if policy == "" || p.isFirewalled(srv.UUID) {
		return instance, nil
	}
	err := p.ensureFirewall(ctx, srv.UUID, policy)
	switch {
	case err == nil:
		return instance, nil
	case errors.As(err, new(*stateError)):
		instance.State = compute.StatePending
		return instance, nil
	}
	instance.State = compute.StatePending
	if strict {
		return instance, fmt.Errorf("firewall: %w", err)
	}
	return instance, err
}

func (p *Provider) isFirewalled(uuid string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.firewalled[uuid]
}

// listServers returns the summaries of the servers that carry every "key" or "key=value" label filter.
func (p *Provider) listServers(ctx context.Context, filters []string) ([]server, error) {
	var servers []server
	seen := map[string]bool{}
	for offset := 0; ; {
		var out struct {
			Servers struct {
				Server []server `json:"server"`
			} `json:"servers"`
		}
		query := url.Values{"label": filters, "limit": {strconv.Itoa(pageSize)}, "offset": {strconv.Itoa(offset)}}
		if _, err := p.api.Do(ctx, http.MethodGet, "/server", query, nil, &out); err != nil {
			return nil, err
		}
		added := 0
		for _, s := range out.Servers.Server {
			if !seen[s.UUID] {
				seen[s.UUID] = true
				servers = append(servers, s)
				added++
			}
		}
		if added == 0 {
			return servers, nil
		}
		offset += len(out.Servers.Server)
	}
}

// List implements compute.Provider. UpCloud's list has no addresses, so each of the owner's servers is read again.
func (p *Provider) List(ctx context.Context, owner compute.Owner) ([]compute.Instance, error) {
	filters := []string{compute.LabelManaged + "=true", compute.LabelCluster + "=" + owner.Cluster}
	if owner.Pool != "" {
		filters = append(filters, compute.LabelPool+"="+owner.Pool)
	}
	servers, err := p.listServers(ctx, filters)
	if err != nil {
		return nil, err
	}
	var instances []compute.Instance
	for _, s := range servers {
		if !owner.Owns(s.labels()) {
			continue
		}
		full, err := p.getServer(ctx, s.UUID)
		if errors.Is(err, compute.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		instance, _ := p.view(ctx, full, false)
		instances = append(instances, instance)
	}
	return instances, nil
}

// --- delete ---

// Delete implements compute.Provider. A server can be deleted only when it is stopped, so a started one is stopped
// and the call returns; a later call deletes it with its storage and backups, which would otherwise stay and bill.
// A server that is in maintenance is being made or stopped and is left until it is not.
func (p *Provider) Delete(ctx context.Context, id string) error {
	srv, err := p.getServer(ctx, id)
	if errors.Is(err, compute.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	switch srv.State {
	case "started":
		body := map[string]any{"stop_server": map[string]string{"stop_type": "hard", "timeout": "60"}}
		_, err := p.api.Do(ctx, http.MethodPost, serverPath(id, "/stop"), nil, body, nil)
		if errors.Is(err, compute.ErrNotFound) || errors.As(err, new(*stateError)) {
			return nil
		}
		return err
	case "maintenance":
		return nil
	}
	query := url.Values{"storages": {"1"}, "backups": {"delete"}}
	_, err = p.api.Do(ctx, http.MethodDelete, serverPath(id, ""), query, nil, nil)
	if errors.Is(err, compute.ErrNotFound) {
		p.forget(id)
		return nil
	}
	if errors.As(err, new(*stateError)) {
		// It started again, or its storage is busy: the caller asks again.
		return nil
	}
	if err == nil {
		p.forget(id)
	}
	return err
}

func (p *Provider) forget(uuid string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.firewalled, uuid)
}

// --- firewall ---

type firewallRule struct {
	Direction    string `json:"direction"`
	Action       string `json:"action"`
	Family       string `json:"family,omitempty"`
	Protocol     string `json:"protocol,omitempty"`
	DestPortFrom string `json:"destination_port_start,omitempty"`
	DestPortTo   string `json:"destination_port_end,omitempty"`
	SourceFrom   string `json:"source_address_start,omitempty"`
	SourceTo     string `json:"source_address_end,omitempty"`
	Comment      string `json:"comment,omitempty"`
}

// ports and sources read back from UpCloud may be numbers or strings, so the comparison goes by their text.
func (r firewallRule) key() string {
	return strings.Join([]string{r.Direction, r.Action, r.Family, r.Protocol, r.DestPortFrom, r.DestPortTo, r.SourceFrom, r.SourceTo}, "|")
}

// firewallPolicy writes what the machine's firewall should let in as the value of the geass-fw label.
func firewallPolicy(spec compute.InstanceSpec) (string, error) {
	var cidrs []string
	for _, c := range spec.ControlPlaneCIDRs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, _, err := parseRange(c); err != nil {
			return "", err
		}
		cidrs = append(cidrs, c)
	}
	multi := "0"
	if spec.MultiSite {
		multi = "1"
	}
	policy := "cp=" + strings.Join(cidrs, ",") + ";ms=" + multi
	if len(policy) > 255 {
		return "", fmt.Errorf("a label holds at most 255 characters at UpCloud: %d control plane addresses do not fit", len(cidrs))
	}
	return policy, nil
}

func parsePolicy(policy string) (cidrs []string, multiSite bool) {
	for part := range strings.SplitSeq(policy, ";") {
		key, value, _ := strings.Cut(part, "=")
		switch key {
		case "cp":
			for c := range strings.SplitSeq(value, ",") {
				if c = strings.TrimSpace(c); c != "" {
					cidrs = append(cidrs, c)
				}
			}
		case "ms":
			multiSite = value == "1"
		}
	}
	return cidrs, multiSite
}

// parseRange turns a CIDR (or one address) into the first and last address of its range, which is how UpCloud
// writes a source.
func parseRange(cidr string) (first, last netip.Addr, err error) {
	prefix, perr := netip.ParsePrefix(cidr)
	if perr != nil {
		addr, aerr := netip.ParseAddr(cidr)
		if aerr != nil {
			return first, last, fmt.Errorf("%q is not an address or a CIDR", cidr)
		}
		return addr, addr, nil
	}
	prefix = prefix.Masked()
	first = prefix.Addr()
	raw := first.AsSlice()
	for bit := prefix.Bits(); bit < len(raw)*8; bit++ {
		raw[bit/8] |= 0x80 >> (bit % 8)
	}
	last, _ = netip.AddrFromSlice(raw)
	return first, last, nil
}

func family(addr netip.Addr) string {
	if addr.Is4() {
		return "IPv4"
	}
	return "IPv6"
}

// firewallRules are what a machine's firewall lets in: SSH and the kubelet from the control plane, and the overlay
// from anywhere in a multi-site cluster, then a drop of everything else coming in. Without a known control plane
// SSH comes from anywhere (the machine accepts only the cluster's key) and the kubelet from nowhere.
func firewallRules(cidrs []string, multiSite bool) ([]firewallRule, error) {
	var rules []firewallRule
	add := func(protocol string, port int, sources []string, comment string) error {
		if len(sources) == 0 {
			rules = append(rules, firewallRule{Direction: "in", Action: "accept", Family: "IPv4", Protocol: protocol,
				DestPortFrom: strconv.Itoa(port), DestPortTo: strconv.Itoa(port), Comment: comment})
			return nil
		}
		for _, source := range sources {
			first, last, err := parseRange(source)
			if err != nil {
				return err
			}
			rules = append(rules, firewallRule{Direction: "in", Action: "accept", Family: family(first), Protocol: protocol,
				DestPortFrom: strconv.Itoa(port), DestPortTo: strconv.Itoa(port), SourceFrom: first.String(), SourceTo: last.String(), Comment: comment})
		}
		return nil
	}
	if err := add("tcp", 22, cidrs, "Geass: SSH"); err != nil {
		return nil, err
	}
	if len(cidrs) > 0 {
		if err := add("tcp", compute.KubeletPort, cidrs, "Geass: kubelet"); err != nil {
			return nil, err
		}
	}
	if multiSite {
		if err := add("udp", compute.OverlayPortUDP, nil, "Geass: overlay"); err != nil {
			return nil, err
		}
	}
	// The last rule is the default of the chain and carries only a direction and an action.
	rules = append(rules, firewallRule{Direction: "in", Action: "drop"})
	return rules, nil
}

// ensureFirewall writes the server's rules when they are not what the policy says, in one call that replaces the
// chain, and remembers that they are.
func (p *Provider) ensureFirewall(ctx context.Context, uuid, policy string) error {
	cidrs, multi := parsePolicy(policy)
	desired, err := firewallRules(cidrs, multi)
	if err != nil {
		return err
	}
	var current struct {
		Rules struct {
			Rule []map[string]any `json:"firewall_rule"`
		} `json:"firewall_rules"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, serverPath(uuid, "/firewall_rule"), nil, nil, &current); err != nil {
		return err
	}
	var have []firewallRule
	for _, r := range current.Rules.Rule {
		text := func(k string) string {
			if v, ok := r[k]; ok && v != nil {
				return fmt.Sprint(v)
			}
			return ""
		}
		have = append(have, firewallRule{
			Direction: text("direction"), Action: text("action"), Family: text("family"), Protocol: text("protocol"),
			DestPortFrom: text("destination_port_start"), DestPortTo: text("destination_port_end"),
			SourceFrom: text("source_address_start"), SourceTo: text("source_address_end"),
		})
	}
	if !sameRules(have, desired) {
		body := map[string]any{"firewall_rules": map[string]any{"firewall_rule": desired}}
		if _, err := p.api.Do(ctx, http.MethodPut, serverPath(uuid, "/firewall_rule"), nil, body, nil); err != nil {
			return err
		}
	}
	p.mu.Lock()
	p.firewalled[uuid] = true
	p.mu.Unlock()
	return nil
}

// sameRules compares two chains in order: order is what a firewall chain means.
func sameRules(a, b []firewallRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].key() != b[i].key() {
			return false
		}
	}
	return true
}

var _ compute.Provider = (*Provider)(nil)
