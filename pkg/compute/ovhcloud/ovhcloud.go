// Package ovhcloud creates machines in an OVHcloud Public Cloud project. It is the Public Cloud product, an OpenStack
// based one that can be created and deleted by the hour through the API; the VPS product cannot be autoscaled and is
// not what this package talks to.
//
// What a maintainer has to know:
//
//   - The credential is an OAuth2 service account (client id and secret), traded for a Bearer token at the endpoint's
//     token URL (client_credentials, scope=all). The token lives about an hour and is cached and refreshed before it
//     ends. The endpoint is eu, ca or us, which picks both the API address and the token address. The service account
//     has no rights until an IAM policy grants it some on the project.
//
//   - A machine's ID is the instance id the API gives it. The API is per project, and a call without a region covers
//     every region the project has enabled.
//
//   - OVHcloud instances have NO tags or metadata. Ownership is therefore written into the instance NAME, and read back
//     from it, as
//
//     geass-<c>-<p>-<a>-<node>
//
//     where c is the first 8 hex digits of the SHA-256 of the cluster, p the first 6 of the SHA-256 of the pool, a one
//     base-36 character for the attempt number the core appends to a name ("-2" after a first machine was given up on),
//     and node the node's own name. The name is reversible: the node and the attempt come out of it, so Instance.Name
//     and the geass-node label are what Create was given. The cluster and the pool are hashes, so List can tell whose a
//     machine is but Get cannot name the cluster: a machine read by Get has geass-managed and geass-node labels only,
//     and a machine read by List has geass-cluster (the owner's) and, when the owner names a pool, geass-pool. A name
//     that does not have this shape is not Geass's and is never listed.
//
//   - The name takes lowercase letters, digits and hyphens only, and is at most 255 characters (the limit of the
//     OpenStack name behind it). The 24 characters of prefix leave 39 for the node's name when the whole name has to stay
//     a 63 character hostname, which OVHcloud's documentation does not promise it must: a longer name is sent as it is
//     and the API's answer, if it refuses, is the error.
//
//   - There is no firewall in the Public Cloud API. The instance gets the project's OpenStack default security group,
//     which lets everything in, and OVHcloud says not to edit it. The machine accepts only the cluster's key on SSH and
//     the kubelet and overlay authenticate their peers; Verify says so in a warning. Restrict the project's network
//     (a private network or an IP restriction at the OVHcloud side) if that is not enough.
//
//   - A new project is limited and cannot create instances until a payment method is added. Both show up as quota
//     errors. A region has to be enabled for the project (POST /cloud/project/{serviceName}/region) before it can be
//     used; Geass never enables one, because that is the user's decision.
//
//   - The machine's root disk is part of the instance and is removed with it. Billing is hourly, every commenced hour.
//
//   - The API has no prices in the calls Geass makes, so every Size has a zero price. A Size is the flavor's name (d2-4,
//     b3-8, c3-16); each region has its own flavor ids, which Create looks up from the name.
//
//   - Create uses the project-level instance call (POST /cloud/project/{serviceName}/instance), which answers with the
//     instance at once. The region-level call that three-availability-zone regions are said to need answers with an
//     operation instead and is not used.
package ovhcloud

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/degoke/geass/pkg/compute"
)

// ID is the provider's name in a GeassCloudConnection.
const ID = "OVHcloud"

// Credential and setting keys.
const (
	keyClientID     = "clientId"
	keyClientSecret = "clientSecret"
	keyServiceName  = "serviceName"
	keyEndpoint     = "endpoint"

	SettingImageID        = "imageId"
	SettingNetworkID      = "networkId"
	SettingUserDataBase64 = "userDataBase64"
)

// endpoints are the OVHcloud API regions: the API address, and the address that trades a service account for a token.
var endpoints = map[string]struct{ api, token string }{
	"eu": {"https://eu.api.ovh.com/v1", "https://www.ovh.com/auth/oauth2/token"},
	"ca": {"https://ca.api.ovh.com/v1", "https://ca.ovh.com/auth/oauth2/token"},
	"us": {"https://api.us.ovhcloud.com/v1", "https://us.ovhcloud.com/auth/oauth2/token"},
}

func init() {
	compute.Register(compute.Descriptor{
		ID:      ID,
		Label:   "OVHcloud Public Cloud",
		Summary: "An OAuth2 service account (client id and secret) with an IAM policy on a Public Cloud project that Geass has to itself, which may list flavors, images and networks and create, read and delete instances. New projects are limited and need a payment method. OVHcloud has no firewall API: the instances sit behind the project's default security group, which lets everything in.",
		Fields: []compute.Field{
			{Key: keyClientID, Label: "Service account client ID", Help: "Created with POST /me/api/oauth2/client (flow CLIENT_CREDENTIALS)."},
			{Key: keyClientSecret, Label: "Service account client secret", Secret: true, Help: "Shown once, when the service account is created."},
			{Key: keyServiceName, Label: "Public Cloud project ID", Help: "The project's id (serviceName), a 32 character hex string."},
			{Key: keyEndpoint, Label: "API endpoint", Optional: true, Help: "eu (default), ca or us: where the OVHcloud account lives."},
		},
		Settings: []compute.Setting{
			{Key: SettingImageID, Label: "Image ID", Help: "Boot from this image. Image ids differ per region. Default: the Ubuntu 24.04 image of the region."},
			{Key: SettingNetworkID, Label: "Public network ID", Help: "The network that gives the machine its public address. Default: the project's public network (Ext-Net)."},
			{Key: SettingUserDataBase64, Label: "Send cloud-init base64 encoded", Help: "Set to true if the machines do not run their cloud-init: OVHcloud does not say whether the API wants the user data encoded."},
		},
		RegionHelp: "A Public Cloud region the project has enabled: GRA11, SBG5, RBX-A (Europe), BHS5 (Canada), WAW1, DE1, UK1, SGP1, SYD1. Enable a region in the project before using it.",
		New: func(creds compute.Credentials, options compute.Options) (compute.Provider, error) {
			return New(creds, options)
		},
	})
}

// Provider is a compute.Provider for OVHcloud Public Cloud.
type Provider struct {
	api           *compute.Client
	tokens        *compute.Client
	tokenPath     string
	clientID      string
	clientSecret  string
	service       string
	defaultRegion string
	now           func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// New returns a provider that acts with the service account.
func New(creds compute.Credentials, options compute.Options) (*Provider, error) {
	name := strings.TrimPrefix(strings.ToLower(creds.Get(keyEndpoint)), "ovh-")
	if name == "" {
		name = "eu"
	}
	endpoint, ok := endpoints[name]
	if !ok {
		return nil, fmt.Errorf("the endpoint %q is not one of eu, ca, us", creds.Get(keyEndpoint))
	}
	apiBase, tokenURL := endpoint.api, endpoint.token
	if options.BaseURL != "" {
		// A test replaces every address, the token address included.
		apiBase = strings.TrimRight(options.BaseURL, "/")
		tokenURL = apiBase + "/auth/oauth2/token"
	}
	parsed, err := url.Parse(tokenURL)
	if err != nil {
		return nil, err
	}
	service := creds.Get(keyServiceName)
	if service == "" || strings.ContainsAny(service, "/?# ") {
		return nil, fmt.Errorf("%q is not a Public Cloud project id", service)
	}
	p := &Provider{
		clientID: creds.Get(keyClientID), clientSecret: creds.Get(keyClientSecret), service: service,
		defaultRegion: strings.TrimSpace(options.Region), now: time.Now,
		tokenPath: parsed.Path,
	}
	p.tokens = &compute.Client{
		HTTP: options.HTTP, BaseURL: parsed.Scheme + "://" + parsed.Host,
		Header:        http.Header{"User-Agent": []string{"geass"}},
		ClassifyError: classifyToken,
	}
	p.api = &compute.Client{
		HTTP: options.HTTP, BaseURL: apiBase,
		Header:        http.Header{"User-Agent": []string{"geass"}},
		Authorize:     p.authorize,
		ClassifyError: classify,
	}
	return p, nil
}

// classifyToken turns a refused service account into an auth error that says what to look at.
func classifyToken(_, _ string, status int, body []byte) error {
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: OVHcloud refused the service account (check the client id and secret, and the endpoint): %s", compute.ErrAuth, strings.TrimSpace(string(body)))
	}
	return nil
}

// bearer returns a valid access token, asking for a new one when the last is about to end.
func (p *Provider) bearer(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.token != "" && p.now().Before(p.expires) {
		return p.token, nil
	}
	form := url.Values{
		"grant_type": {"client_credentials"}, "client_id": {p.clientID}, "client_secret": {p.clientSecret}, "scope": {"all"},
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if _, err := p.tokens.DoRaw(ctx, http.MethodPost, p.tokenPath, nil, []byte(form.Encode()), "application/x-www-form-urlencoded", &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("%w: OVHcloud answered without an access token", compute.ErrAuth)
	}
	lifetime := time.Duration(out.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = 30 * time.Minute
	}
	// Renew a couple of minutes early, so that a request is never sent with a token that dies on the way.
	margin := min(2*time.Minute, lifetime/2)
	p.token, p.expires = out.AccessToken, p.now().Add(lifetime-margin)
	return p.token, nil
}

func (p *Provider) authorize(req *http.Request, _ []byte) error {
	token, err := p.bearer(req.Context())
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// ovhError is the error body of the OVHcloud API.
type ovhError struct {
	Class   string `json:"class"`
	Message string `json:"message"`
}

// classify maps OVHcloud's failures to the errors callers act on. The API has no error codes for a project at its
// limit or a region out of stock, so they are told from the words of the message.
func classify(_, _ string, status int, body []byte) error {
	var parsed ovhError
	_ = json.Unmarshal(body, &parsed)
	message := parsed.Message
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	lower := strings.ToLower(message)
	switch {
	case containsAny(lower, "no valid host", "out of stock", "insufficient capacity", "not enough capacity", "no capacity"):
		return &compute.CapacityError{Message: message}
	case containsAny(lower, "quota", "payment method", "not paid", "unpaid"):
		return &compute.QuotaError{Message: message}
	case status == http.StatusForbidden || status == http.StatusUnauthorized:
		return fmt.Errorf("%w: %s (does the service account's IAM policy allow this call on the project?)", compute.ErrAuth, message)
	}
	return nil
}

func containsAny(s string, words ...string) bool {
	return slices.ContainsFunc(words, func(w string) bool { return strings.Contains(s, w) })
}

func (p *Provider) projectPath(rest string) string {
	return "/cloud/project/" + url.PathEscape(p.service) + rest
}

// The name of a machine carries who owns it, since an instance has nowhere else to say.
var namePattern = regexp.MustCompile(`^geass-([0-9a-f]{8})-([0-9a-f]{6})-([0-9a-z])-(.+)$`)

const (
	attemptDigits = "0123456789abcdefghijklmnopqrstuvwxyz"
	// namePrefixLength is "geass-", the two hashes, the attempt and the three hyphens between them.
	namePrefixLength = 6 + 8 + 1 + 6 + 1 + 1 + 1
	maxNameLength    = 255
)

func hashOf(kind, value string, digits int) string {
	sum := sha256.Sum256([]byte(kind + ":" + value))
	return hex.EncodeToString(sum[:])[:digits]
}

func clusterHash(cluster string) string { return hashOf("cluster", cluster, 8) }
func poolHash(pool string) string       { return hashOf("pool", pool, 6) }

// machineName is what a machine's name is: geass-<cluster hash>-<pool hash>-<attempt>-<node>.
type machineName struct {
	cluster, pool string // hashes
	attempt       int
	node          string
}

// name is the Instance.Name this machine was made with: the node, and the attempt after it.
func (m machineName) name() string {
	if m.attempt == 0 {
		return m.node
	}
	return m.node + "-" + strconv.Itoa(m.attempt)
}

func (m machineName) String() string {
	return "geass-" + m.cluster + "-" + m.pool + "-" + string(attemptDigits[m.attempt]) + "-" + m.node
}

func parseName(name string) (machineName, bool) {
	match := namePattern.FindStringSubmatch(name)
	if match == nil {
		return machineName{}, false
	}
	return machineName{cluster: match[1], pool: match[2], attempt: strings.Index(attemptDigits, match[3]), node: match[4]}, true
}

// nameFor works out the name a machine of the spec gets. spec.Name is the node's name, or the node's name and "-N" when
// the node's earlier machine was given up on; a name that is neither is carried whole as the node.
func nameFor(spec compute.InstanceSpec) (machineName, error) {
	cluster := spec.Labels[compute.LabelCluster]
	if cluster == "" {
		return machineName{}, errors.New("a machine carries its owner in its name, and the spec has no geass-cluster label to make it from")
	}
	m := machineName{cluster: clusterHash(cluster), pool: poolHash(spec.Labels[compute.LabelPool]), node: spec.Name}
	if node := spec.Labels[compute.LabelNode]; node != "" && node != spec.Name {
		if tail, ok := strings.CutPrefix(spec.Name, node+"-"); ok {
			if n, err := strconv.Atoi(tail); err == nil && n >= 1 && n < len(attemptDigits) && strconv.Itoa(n) == tail {
				m.node, m.attempt = node, n
			}
		}
	}
	for _, r := range m.node {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return machineName{}, fmt.Errorf("%q cannot be a machine name: it takes lowercase letters, digits and hyphens", m.node)
		}
	}
	if m.node == "" || len(m.String()) > maxNameLength {
		return machineName{}, fmt.Errorf("%q cannot be a machine name (1 to %d characters)", m.node, maxNameLength-namePrefixLength)
	}
	return m, nil
}

type instance struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Region   string `json:"region"`
	FlavorID string `json:"flavorId"`
	Created  string `json:"created"`
	Flavor   *struct {
		Name string `json:"name"`
	} `json:"flavor"`
	IPAddresses []struct {
		IP      string `json:"ip"`
		Type    string `json:"type"`
		Version int    `json:"version"`
	} `json:"ipAddresses"`
}

// instance turns the API's instance into Geass's. known, when it is not nil, is the owner the caller asked about and
// fills in the labels the name only holds as hashes.
func (i instance) instance(known *compute.Owner) compute.Instance {
	created, _ := time.Parse(time.RFC3339, i.Created)
	out := compute.Instance{ID: i.ID, Name: i.Name, Region: i.Region, Size: i.FlavorID, State: stateOf(i.Status), CreatedAt: created}
	if i.Flavor != nil && i.Flavor.Name != "" {
		out.Size = i.Flavor.Name
	}
	for _, a := range i.IPAddresses {
		switch {
		case a.Version == 4 && a.Type == "public" && out.PublicIPv4 == "":
			out.PublicIPv4 = a.IP
		case a.Version == 4 && a.Type == "private" && out.PrivateIPv4 == "":
			out.PrivateIPv4 = a.IP
		}
	}
	if m, ok := parseName(i.Name); ok {
		out.Name = m.name()
		out.Labels = map[string]string{compute.LabelManaged: "true", compute.LabelNode: m.node}
		if known != nil && clusterHash(known.Cluster) == m.cluster {
			out.Labels[compute.LabelCluster] = known.Cluster
			if known.Pool != "" && poolHash(known.Pool) == m.pool {
				out.Labels[compute.LabelPool] = known.Pool
			}
		}
	}
	return out
}

func stateOf(status string) compute.State {
	switch status {
	case "ACTIVE":
		return compute.StateRunning
	case "DELETING":
		return compute.StateTerminating
	case "DELETED", "SOFT_DELETED":
		return compute.StateGone
	case "SHUTOFF", "STOPPED", "SUSPENDED", "PAUSED", "SHELVED", "SHELVED_OFFLOADED", "SHELVING", "ERROR":
		// An instance in ERROR exists, costs money and will not come up: it is reported as off so that a caller that
		// waits for it to run gives up and deletes it.
		return compute.StateStopped
	default:
		// BUILD, BUILDING, REBUILD, REBOOT, HARD_REBOOT, MIGRATING, RESUMING, UNKNOWN, ...
		return compute.StatePending
	}
}

// Verify implements compute.Provider.
func (p *Provider) Verify(ctx context.Context) (compute.Account, error) {
	var project struct {
		ProjectID   string `json:"project_id"`
		ProjectName string `json:"projectName"`
		Description string `json:"description"`
		Status      string `json:"status"`
		Expiration  string `json:"expiration"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, p.projectPath(""), nil, nil, &project); err != nil {
		return compute.Account{}, err
	}
	name := firstNonEmpty(project.Description, project.ProjectName, project.ProjectID, p.service)
	account := compute.Account{Name: "project " + name}
	if t, err := time.Parse(time.RFC3339, project.Expiration); err == nil {
		account.ExpiresAt = t
	}
	if project.Status != "" && project.Status != "ok" {
		account.Warnings = append(account.Warnings, "the project is "+project.Status+", and a project that is not ok cannot create instances")
	}
	account.Warnings = append(account.Warnings, "OVHcloud has no firewall API: the machines sit behind the project's default security group, which lets everything in, and OVHcloud says not to edit it. They accept SSH only with the cluster's key")

	var regions []string
	if _, err := p.api.Do(ctx, http.MethodGet, p.projectPath("/region"), nil, nil, &regions); err == nil && p.defaultRegion != "" && !slices.Contains(regions, p.defaultRegion) {
		account.Warnings = append(account.Warnings, "the region "+p.defaultRegion+" is not enabled for the project: enable it (POST /cloud/project/"+p.service+"/region) before creating machines there")
	}
	var quotas []struct {
		Region   string `json:"region"`
		Instance *struct {
			MaxInstances  int64 `json:"maxInstances"`
			UsedInstances int64 `json:"usedInstances"`
		} `json:"instance"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, p.projectPath("/quota"), nil, nil, &quotas); err == nil {
		anyRoom, known := false, 0
		for _, q := range quotas {
			if q.Instance == nil {
				continue
			}
			known++
			room := q.Instance.MaxInstances - q.Instance.UsedInstances
			anyRoom = anyRoom || room > 0
			if q.Region == p.defaultRegion && room <= 0 {
				account.Warnings = append(account.Warnings, "the project's instance quota in "+q.Region+" is used up: ask OVHcloud to raise it, or add a payment method to the project")
			}
		}
		if known > 0 && !anyRoom {
			account.Warnings = append(account.Warnings, "no region of the project has room for another instance: new projects are limited until a payment method is added")
		}
	}
	return account, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

type flavor struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	OSType    string `json:"osType"`
	Region    string `json:"region"`
	VCPUs     int    `json:"vcpus"`
	RAM       int64  `json:"ram"`
	Disk      int    `json:"disk"`
	Available *bool  `json:"available"`
	Quota     *int64 `json:"quota"`
}

// usable says whether a flavor is one a Linux worker can be made from: not a Windows or GPU one.
func (f flavor) usable() bool {
	return (f.OSType == "" || f.OSType == "linux") && !strings.Contains(strings.ToLower(f.Type), "gpu") && !strings.HasPrefix(f.Name, "win-")
}

// memoryMiB reads a flavor's memory. The API's documentation says gibibytes, but it answers in megabytes (b2-7 has
// 7000); a figure too small to be megabytes is taken as gibibytes.
func (f flavor) memoryMiB() int64 {
	if f.RAM > 0 && f.RAM < 512 {
		return f.RAM * 1024
	}
	return f.RAM
}

func (p *Provider) flavors(ctx context.Context, region string) ([]flavor, error) {
	var out []flavor
	query := url.Values{}
	if region != "" {
		query.Set("region", region)
	}
	if _, err := p.api.Do(ctx, http.MethodGet, p.projectPath("/flavor"), query, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Sizes implements compute.Provider.
func (p *Provider) Sizes(ctx context.Context, region string) ([]compute.Size, error) {
	if region == "" {
		region = p.defaultRegion
	}
	flavors, err := p.flavors(ctx, region)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var sizes []compute.Size
	for _, f := range flavors {
		if !f.usable() || seen[f.Name] {
			continue
		}
		seen[f.Name] = true
		description := f.Name
		if f.Type != "" {
			description += " (" + f.Type + ")"
		}
		if f.Available != nil && !*f.Available {
			description += ", out of stock"
		}
		sizes = append(sizes, compute.Size{
			ID: f.Name, Description: description, VCPUs: f.VCPUs, MemoryMiB: f.memoryMiB(), DiskGB: f.Disk, Arch: "amd64",
		})
	}
	// The API gives no price here, so the smallest machine stands for the cheapest.
	slices.SortStableFunc(sizes, func(a, b compute.Size) int {
		return cmp.Or(cmp.Compare(a.VCPUs, b.VCPUs), cmp.Compare(a.MemoryMiB, b.MemoryMiB), strings.Compare(a.ID, b.ID))
	})
	return sizes, nil
}

// Create implements compute.Provider.
func (p *Provider) Create(ctx context.Context, spec compute.InstanceSpec) (compute.Instance, error) {
	region := strings.TrimSpace(spec.Region)
	if region == "" {
		region = p.defaultRegion
	}
	if region == "" {
		return compute.Instance{}, errors.New("a region is needed, such as GRA11")
	}
	name, err := nameFor(spec)
	if err != nil {
		return compute.Instance{}, err
	}

	// A machine that has this name already is the answer to a repeated create, in whatever region it was made. The
	// name holds the cluster, so it is this owner's.
	existing, err := p.instances(ctx, "")
	if err != nil {
		return compute.Instance{}, err
	}
	for _, i := range existing {
		if i.Name == name.String() && stateOf(i.Status) != compute.StateGone {
			out := i.instance(nil)
			out.Labels = spec.Labels
			return out, nil
		}
	}

	machine, err := p.flavorByName(ctx, region, spec.Size)
	if err != nil {
		return compute.Instance{}, err
	}
	imageID, err := p.imageFor(ctx, region, machine, spec)
	if err != nil {
		return compute.Instance{}, err
	}
	networkID, err := p.publicNetwork(ctx, region, spec.Settings)
	if err != nil {
		return compute.Instance{}, err
	}
	userData := spec.UserData
	if strings.EqualFold(strings.TrimSpace(spec.Settings[SettingUserDataBase64]), "true") {
		userData = base64.StdEncoding.EncodeToString([]byte(userData))
	}
	body := map[string]any{
		"name": name.String(), "region": region, "flavorId": machine.ID, "imageId": imageID,
		"networks":       []map[string]string{{"networkId": networkID}},
		"monthlyBilling": false,
		"userData":       userData,
	}
	var created instance
	if _, err := p.api.Do(ctx, http.MethodPost, p.projectPath("/instance"), nil, body, &created); err != nil {
		return compute.Instance{}, err
	}
	out := created.instance(nil)
	out.Labels = spec.Labels
	if out.Region == "" {
		out.Region = region
	}
	if out.Size == "" || out.Size == machine.ID {
		out.Size = machine.Name
	}
	return out, nil
}

func (p *Provider) instances(ctx context.Context, region string) ([]instance, error) {
	query := url.Values{}
	if region != "" {
		query.Set("region", region)
	}
	var out []instance
	if _, err := p.api.Do(ctx, http.MethodGet, p.projectPath("/instance"), query, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// flavorByName finds the region's flavor of that name, and refuses early a flavor that is out of stock or over quota.
func (p *Provider) flavorByName(ctx context.Context, region, size string) (flavor, error) {
	flavors, err := p.flavors(ctx, region)
	if err != nil {
		return flavor{}, err
	}
	if len(flavors) == 0 {
		return flavor{}, fmt.Errorf("no flavors are listed in %s for this project: the region may not be enabled (POST /cloud/project/%s/region) or its name may be wrong", region, p.service)
	}
	for _, f := range flavors {
		if f.Name != size || !f.usable() {
			continue
		}
		if f.Available != nil && !*f.Available {
			return flavor{}, &compute.CapacityError{Message: "the flavor " + size + " is out of stock in " + region}
		}
		if f.Quota != nil && *f.Quota <= 0 {
			return flavor{}, &compute.QuotaError{Message: "the project's quota allows no more " + size + " instances in " + region}
		}
		return f, nil
	}
	var names []string
	for _, f := range flavors {
		if f.usable() && len(names) < 12 && !slices.Contains(names, f.Name) {
			names = append(names, f.Name)
		}
	}
	return flavor{}, fmt.Errorf("no flavor %q in %s (some that exist there: %s)", size, region, strings.Join(names, ", "))
}

var ubuntuPattern = regexp.MustCompile(`(?i)^ubuntu 24\.04`)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// imageFor picks the boot image: the imageId setting, then the machine's own Image (an id or a name), then the region's
// Ubuntu 24.04. Image ids are per region, so the region's list is read for a name.
func (p *Provider) imageFor(ctx context.Context, region string, machine flavor, spec compute.InstanceSpec) (string, error) {
	if id := strings.TrimSpace(spec.Settings[SettingImageID]); id != "" {
		return id, nil
	}
	wanted := strings.TrimSpace(spec.Image)
	if uuidPattern.MatchString(wanted) {
		return wanted, nil
	}
	var images []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Visibility string `json:"visibility"`
		FlavorType string `json:"flavorType"`
	}
	query := url.Values{"region": {region}, "osType": {"linux"}}
	if _, err := p.api.Do(ctx, http.MethodGet, p.projectPath("/image"), query, nil, &images); err != nil {
		return "", err
	}
	best, bestName := "", ""
	for _, i := range images {
		if (i.Status != "" && i.Status != "active") || (i.Visibility != "" && i.Visibility != "public") {
			continue
		}
		if i.FlavorType != "" && i.FlavorType != machine.Type {
			continue // made for another kind of flavor
		}
		matches := ubuntuPattern.MatchString(i.Name)
		if wanted != "" {
			matches = strings.EqualFold(i.Name, wanted) || strings.HasPrefix(strings.ToLower(i.Name), strings.ToLower(wanted))
		}
		// The plainest name wins: "Ubuntu 24.04" over "Ubuntu 24.04 - Docker".
		if matches && (best == "" || len(i.Name) < len(bestName)) {
			best, bestName = i.ID, i.Name
		}
	}
	if best == "" {
		what := "Ubuntu 24.04 image"
		if wanted != "" {
			what = "image called " + wanted
		}
		return "", fmt.Errorf("no %s fits flavor %s in %s: name one in the %s setting", what, machine.Name, region, SettingImageID)
	}
	return best, nil
}

// publicNetwork finds the project's public network, which is what gives a machine its public address.
func (p *Provider) publicNetwork(ctx context.Context, region string, settings map[string]string) (string, error) {
	if id := strings.TrimSpace(settings[SettingNetworkID]); id != "" {
		return id, nil
	}
	var networks []struct {
		ID      string `json:"id"`
		Regions []struct {
			Region string `json:"region"`
			Status string `json:"status"`
		} `json:"regions"`
	}
	if _, err := p.api.Do(ctx, http.MethodGet, p.projectPath("/network/public"), nil, nil, &networks); err != nil {
		return "", err
	}
	for _, n := range networks {
		for _, r := range n.Regions {
			if r.Region == region && (r.Status == "" || r.Status == "ACTIVE") {
				return n.ID, nil
			}
		}
	}
	if len(networks) == 1 && len(networks[0].Regions) == 0 {
		return networks[0].ID, nil
	}
	return "", fmt.Errorf("no public network is listed in %s: name one in the %s setting", region, SettingNetworkID)
}

// Get implements compute.Provider.
func (p *Provider) Get(ctx context.Context, id string) (compute.Instance, error) {
	if id == "" || strings.ContainsAny(id, "/?# ") {
		return compute.Instance{}, fmt.Errorf("%w: %q is not an instance id", compute.ErrNotFound, id)
	}
	var found instance
	if _, err := p.api.Do(ctx, http.MethodGet, p.projectPath("/instance/"+url.PathEscape(id)), nil, nil, &found); err != nil {
		return compute.Instance{}, err
	}
	return found.instance(nil), nil
}

// List implements compute.Provider. One call without a region covers every region of the project, so Owner.Regions is
// not needed.
func (p *Provider) List(ctx context.Context, owner compute.Owner) ([]compute.Instance, error) {
	all, err := p.instances(ctx, "")
	if err != nil {
		return nil, err
	}
	if owner.Cluster == "" {
		return nil, nil
	}
	var out []compute.Instance
	for _, i := range all {
		m, ok := parseName(i.Name)
		if !ok || m.cluster != clusterHash(owner.Cluster) || (owner.Pool != "" && m.pool != poolHash(owner.Pool)) {
			continue
		}
		machine := i.instance(&owner)
		if !owner.Owns(machine.Labels) {
			continue
		}
		out = append(out, machine)
	}
	return out, nil
}

// Delete implements compute.Provider. The instance's root disk and its addresses go with it. Deleting an instance that
// is already being deleted succeeds.
func (p *Provider) Delete(ctx context.Context, id string) error {
	if id == "" || strings.ContainsAny(id, "/?# ") {
		return nil // an id that cannot name a machine names no machine
	}
	_, err := p.api.Do(ctx, http.MethodDelete, p.projectPath("/instance/"+url.PathEscape(id)), nil, nil, nil)
	switch {
	case err == nil, errors.Is(err, compute.ErrNotFound):
		return nil
	case compute.IsStatus(err, http.StatusBadRequest), compute.IsStatus(err, http.StatusConflict):
		// An instance on its way out can answer a second delete with a refusal: that is a success.
		found, getErr := p.Get(ctx, id)
		if errors.Is(getErr, compute.ErrNotFound) || (getErr == nil && !found.Alive()) {
			return nil
		}
	}
	return err
}

var _ compute.Provider = (*Provider)(nil)
