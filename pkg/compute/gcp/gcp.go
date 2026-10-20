// Package gcp creates machines at Google Compute Engine.
//
// What a maintainer has to know:
//
//   - The credential is a service account JSON key. Geass takes client_email, private_key and project_id from it, signs
//     a JWT (RS256) with golang.org/x/oauth2/jwt, and trades it for a one hour access token that is cached. The key's
//     own token_uri is ignored on purpose: the signed assertion is only ever sent to Google's token endpoint.
//     Organisations created since 3 May 2024 refuse to create service account keys (the iam.disableServiceAccountKeyCreation
//     policy), so an administrator may have to lift that for the project before the key can exist.
//   - Region means zone here (europe-west4-a). A machine's ID is "<zone>/<name>", because Compute Engine addresses
//     instances by zone and name, and Get and Delete are given nothing else.
//   - instances.insert returns a long-running Operation, and the quota and stockout failures only show up inside that
//     Operation (QUOTA_EXCEEDED, ZONE_RESOURCE_POOL_EXHAUSTED), not in the HTTP status of the insert. Create therefore
//     watches the Operation for a few seconds, which is long enough for those checks, and returns the failure; it does
//     not wait for the machine to boot.
//   - Create asks for a deterministic requestId (a UUID made from the cluster and the name), so a repeated insert whose
//     first answer was lost is ignored by Compute Engine. Before inserting, Create also looks the name up, because a
//     requestId that was used for a failed Operation may be replayed with the same failure for a while.
//   - The boot disk is created with autoDelete set explicitly, and the external address is the ephemeral one of the
//     access config, so deleting the instance leaves nothing that keeps billing. No service account is attached to
//     the machine, so it cannot reach other Google APIs.
//   - The firewall is two global firewall rules named geass-<cluster> (SSH, the kubelet) and geass-<cluster>-overlay
//     (the overlay's UDP port, disabled unless the cluster is multi-site), both aimed at the network tag
//     geass-<cluster> that Create puts on the machine. Firewall rules have no labels, so they are recognised by their
//     description. roles/compute.instanceAdmin.v1 does NOT grant compute.firewalls.create or update: without
//     roles/compute.securityAdmin (or the skipFirewall or firewallTag setting) Create fails with an error that names
//     the permission, rather than creating a machine nothing can reach.
//   - The machine is created with the instance metadata enable-oslogin=FALSE, so that a project that turns OS Login on
//     does not stop the login Geass creates through cloud-init.
//   - Compute Engine has no prices in its API, so every Size has a zero price. Sizes are listed cheapest first by vCPU
//     and memory instead.
package gcp

import (
	"cmp"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"

	"github.com/degoke/geass/pkg/compute"
)

// ID is the provider's name in a GeassCloudConnection.
const ID = "GCP"

const (
	defaultBaseURL  = "https://compute.googleapis.com/compute/v1"
	defaultTokenURL = "https://oauth2.googleapis.com/token"
	computeScope    = "https://www.googleapis.com/auth/compute"

	defaultImageAMD64 = "projects/ubuntu-os-cloud/global/images/family/ubuntu-2404-lts-amd64"
	defaultImageARM64 = "projects/ubuntu-os-cloud/global/images/family/ubuntu-2404-lts-arm64"
	defaultDiskGB     = 30
	pageSize          = 500

	// firewallMarker starts the description of every firewall rule Geass makes, because rules carry no labels.
	firewallMarker = "Managed by Geass"
)

// Credential and setting keys.
const (
	keyServiceAccount = "serviceAccountKey"

	SettingNetwork     = "network"
	SettingSubnetwork  = "subnetwork"
	SettingImage       = "image"
	SettingDiskSizeGB  = "diskSizeGb"
	SettingDiskType    = "diskType"
	SettingFirewallTag = "firewallTag"
	SettingSkipFW      = "skipFirewall"
)

func init() {
	compute.Register(compute.Descriptor{
		ID:      ID,
		Label:   "Google Cloud",
		Summary: "A service account JSON key from a project that Geass has to itself, with roles/compute.instanceAdmin.v1 and roles/compute.securityAdmin (the first cannot make firewall rules). Organisations created since May 2024 block service account key creation by default: an administrator has to lift the iam.disableServiceAccountKeyCreation policy for the project first.",
		Fields: []compute.Field{{
			Key: keyServiceAccount, Label: "Service account key (JSON)", Secret: true, Multiline: true,
			Help: "IAM > Service accounts > Keys > Add key > JSON. Paste the whole file.",
		}},
		Settings: []compute.Setting{
			{Key: SettingNetwork, Label: "VPC network", Help: "The network to put the machine in: a name, or a full path such as projects/host/global/networks/shared. Default: the project's default network."},
			{Key: SettingSubnetwork, Label: "Subnetwork", Help: "Required when the network uses custom subnets. A name in the zone's region, or a full path."},
			{Key: SettingImage, Label: "Boot image", Help: "A source image or image family path, such as projects/ubuntu-os-cloud/global/images/family/ubuntu-2404-lts-amd64. Default: Ubuntu 24.04 LTS for the machine type's architecture."},
			{Key: SettingDiskSizeGB, Label: "Boot disk size (GB)", Help: "Default 30."},
			{Key: SettingDiskType, Label: "Boot disk type", Help: "pd-balanced, pd-ssd, hyperdisk-balanced and so on. Default: the one Compute Engine picks for the machine series."},
			{Key: SettingFirewallTag, Label: "Existing firewall tag", Help: "A network tag that your own firewall rules already target. Geass then makes no firewall rules and only puts this tag on the machine."},
			{Key: SettingSkipFW, Label: "Skip the firewall", Help: "Set to true to make no firewall rules and add no network tag. You must open tcp 22 and 10250 and udp 51820 yourself."},
		},
		RegionHelp: "A zone, not a region: europe-west4-a, us-central1-b, asia-southeast1-a. Machine types and stock differ per zone.",
		New: func(creds compute.Credentials, options compute.Options) (compute.Provider, error) {
			return New(creds.Get(keyServiceAccount), options)
		},
	})
}

// Provider is a compute.Provider for Google Compute Engine.
type Provider struct {
	api         *compute.Client
	project     string
	email       string
	defaultZone string
	tokens      oauth2.TokenSource
	// opWait is how long Create watches the insert Operation for a quota or stockout failure, polled every opPoll.
	opWait time.Duration
	opPoll time.Duration
}

type serviceAccountKey struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
}

func parseKey(raw string) (serviceAccountKey, error) {
	var key serviceAccountKey
	if err := json.Unmarshal([]byte(raw), &key); err != nil {
		return key, fmt.Errorf("the service account key is not JSON: %w", err)
	}
	if key.Type != "" && key.Type != "service_account" {
		return key, fmt.Errorf("the key is of type %q: Geass needs a service account key (type service_account)", key.Type)
	}
	for name, value := range map[string]string{"client_email": key.ClientEmail, "private_key": key.PrivateKey, "project_id": key.ProjectID} {
		if strings.TrimSpace(value) == "" {
			return key, fmt.Errorf("the service account key has no %s", name)
		}
	}
	block, _ := pem.Decode([]byte(key.PrivateKey))
	if block == nil {
		return key, errors.New("the service account key's private_key is not PEM")
	}
	if _, err := parseRSAKey(block.Bytes); err != nil {
		return key, fmt.Errorf("the service account key's private_key is not an RSA key: %w", err)
	}
	return key, nil
}

func parseRSAKey(der []byte) (*rsa.PrivateKey, error) {
	if parsed, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		if key, ok := parsed.(*rsa.PrivateKey); ok {
			return key, nil
		}
		return nil, errors.New("not an RSA key")
	}
	return x509.ParsePKCS1PrivateKey(der)
}

// New returns a provider that acts with the service account key (the JSON text of the file).
func New(serviceAccountKeyJSON string, options compute.Options) (*Provider, error) {
	key, err := parseKey(serviceAccountKeyJSON)
	if err != nil {
		return nil, err
	}
	base, tokenURL := options.BaseURL, defaultTokenURL
	if base == "" {
		base = defaultBaseURL
	} else {
		// A test (or a private endpoint) replaces every address, the token endpoint included.
		tokenURL = strings.TrimRight(base, "/") + "/token"
	}
	tokenHTTP := options.HTTP
	if tokenHTTP == nil {
		tokenHTTP = &http.Client{Timeout: 30 * time.Second}
	}
	cfg := &jwt.Config{
		Email: key.ClientEmail, PrivateKey: []byte(key.PrivateKey), PrivateKeyID: key.PrivateKeyID,
		Scopes: []string{computeScope}, TokenURL: tokenURL,
	}
	p := &Provider{
		project: key.ProjectID, email: key.ClientEmail, defaultZone: options.Region,
		// The token source caches the token and refreshes it before it expires.
		tokens: cfg.TokenSource(context.WithValue(context.Background(), oauth2.HTTPClient, tokenHTTP)),
		opWait: 8 * time.Second, opPoll: time.Second,
	}
	p.api = &compute.Client{
		HTTP:          options.HTTP,
		BaseURL:       base,
		Header:        http.Header{"User-Agent": []string{"geass"}},
		Authorize:     p.authorize,
		ClassifyError: classify,
	}
	return p, nil
}

func (p *Provider) authorize(req *http.Request, _ []byte) error {
	token, err := p.tokens.Token()
	if err != nil {
		var refused *oauth2.RetrieveError
		if errors.As(err, &refused) {
			return fmt.Errorf("%w: Google would not give the service account an access token (%s)", compute.ErrAuth, strings.TrimSpace(string(refused.Body)))
		}
		return fmt.Errorf("getting a Google access token: %w", err)
	}
	token.SetAuthHeader(req)
	return nil
}

func (p *Provider) pause(ctx context.Context, d time.Duration) error {
	if p.api.Sleep != nil {
		return p.api.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// googleError is the error body of a Google API.
type googleError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Errors  []struct {
			Message string `json:"message"`
			Reason  string `json:"reason"`
		} `json:"errors"`
	} `json:"error"`
}

type alreadyExistsError struct{ message string }

func (e *alreadyExistsError) Error() string { return "the resource exists already: " + e.message }

// normalize makes "quotaExceeded", "QUOTA_EXCEEDED" and "Quota Exceeded" the same string.
func normalize(code string) string {
	return strings.ToUpper(strings.NewReplacer("_", "", " ", "", "-", "").Replace(code))
}

// classifyCodes turns the codes and message of a Compute Engine failure (an HTTP error body or an Operation's error)
// into the errors callers act on. It returns nil when the failure is none of them.
func classifyCodes(codes []string, message string) error {
	lower := strings.ToLower(message)
	set := map[string]bool{}
	for _, c := range codes {
		set[normalize(c)] = true
	}
	switch {
	case set["RATELIMITEXCEEDED"] || set["USERRATELIMITEXCEEDED"] || set["RESOURCEOPERATIONRATEEXCEEDED"]:
		return &compute.RateLimitError{Message: message}
	case set["QUOTAEXCEEDED"] || strings.HasPrefix(message, "Quota '") || (strings.Contains(lower, "quota") && strings.Contains(lower, "exceeded")):
		return &compute.QuotaError{Message: message}
	case set["ZONERESOURCEPOOLEXHAUSTED"] || set["ZONERESOURCEPOOLEXHAUSTEDWITHDETAILS"] ||
		strings.Contains(lower, "does not have enough resources available") || strings.Contains(lower, "is currently unavailable in the"):
		return &compute.CapacityError{Message: message}
	}
	return nil
}

// classify maps Compute Engine's error responses to the errors callers act on.
func classify(_, _ string, status int, body []byte) error {
	var parsed googleError
	_ = json.Unmarshal(body, &parsed)
	message := parsed.Error.Message
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	codes := make([]string, 0, len(parsed.Error.Errors))
	for _, e := range parsed.Error.Errors {
		codes = append(codes, e.Reason)
	}
	if err := classifyCodes(codes, message); err != nil {
		return err
	}
	if status == http.StatusConflict {
		for _, c := range codes {
			if normalize(c) == "ALREADYEXISTS" {
				return &alreadyExistsError{message: message}
			}
		}
	}
	return nil
}

type operation struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  *struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	} `json:"error"`
}

// failure is the error an Operation that finished with errors stands for, or nil.
func (o operation) failure() error {
	if o.Error == nil || len(o.Error.Errors) == 0 {
		return nil
	}
	var generic []string
	for _, e := range o.Error.Errors {
		if err := classifyCodes([]string{e.Code}, e.Message); err != nil {
			return err
		}
		generic = append(generic, e.Code+": "+e.Message)
	}
	return fmt.Errorf("the Compute Engine operation failed: %s", strings.Join(generic, "; "))
}

type instance struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Status            string            `json:"status"`
	Zone              string            `json:"zone"`
	MachineType       string            `json:"machineType"`
	CreationTimestamp string            `json:"creationTimestamp"`
	Labels            map[string]string `json:"labels"`
	NetworkInterfaces []struct {
		NetworkIP     string `json:"networkIP"`
		AccessConfigs []struct {
			NatIP string `json:"natIP"`
		} `json:"accessConfigs"`
	} `json:"networkInterfaces"`
}

func (i instance) instance() compute.Instance {
	zone := path.Base(i.Zone)
	created, _ := time.Parse(time.RFC3339, i.CreationTimestamp)
	out := compute.Instance{
		ID: zone + "/" + i.Name, Name: i.Name, Region: zone, Size: path.Base(i.MachineType),
		State: stateOf(i.Status), Labels: i.Labels, CreatedAt: created,
	}
	if len(i.NetworkInterfaces) > 0 {
		out.PrivateIPv4 = i.NetworkInterfaces[0].NetworkIP
		for _, ac := range i.NetworkInterfaces[0].AccessConfigs {
			if ac.NatIP != "" {
				out.PublicIPv4 = ac.NatIP
				break
			}
		}
	}
	return out
}

func stateOf(status string) compute.State {
	switch status {
	case "RUNNING":
		return compute.StateRunning
	case "STOPPED", "SUSPENDING", "SUSPENDED", "TERMINATED":
		return compute.StateStopped
	case "STOPPING":
		// Compute Engine reports a machine that is being deleted, or taken back (Spot), as STOPPING.
		return compute.StateTerminating
	default:
		// PROVISIONING, STAGING, REPAIRING
		return compute.StatePending
	}
}

func (p *Provider) zonePath(zone string) string {
	return "/projects/" + url.PathEscape(p.project) + "/zones/" + url.PathEscape(zone)
}

func (p *Provider) globalPath() string {
	return "/projects/" + url.PathEscape(p.project) + "/global"
}

// splitID reads a machine id, which is "<zone>/<name>".
func splitID(id string) (zone, name string, err error) {
	zone, name, ok := strings.Cut(id, "/")
	if !ok || zone == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("%w: %q is not a Compute Engine machine id (zone/name)", compute.ErrNotFound, id)
	}
	return zone, name, nil
}

// Verify implements compute.Provider.
func (p *Provider) Verify(ctx context.Context) (compute.Account, error) {
	query := url.Values{"maxResults": {"1"}, "returnPartialSuccess": {"true"}}
	if _, err := p.api.Do(ctx, http.MethodGet, "/projects/"+url.PathEscape(p.project)+"/aggregated/instances", query, nil, &aggregatedList{}); err != nil {
		return compute.Account{}, err
	}
	account := compute.Account{Name: p.project + " (" + p.email + ")"}
	if _, err := p.api.Do(ctx, http.MethodGet, p.globalPath()+"/firewalls", url.Values{"maxResults": {"1"}}, nil, nil); err != nil {
		account.Warnings = append(account.Warnings, "the service account cannot read firewall rules ("+err.Error()+"): Geass makes its own firewall unless the skipFirewall or firewallTag setting is used")
	}
	account.Warnings = append(account.Warnings, "Google cannot tell, without making one, whether the service account may create firewall rules: roles/compute.instanceAdmin.v1 may not, and a machine's first create then fails until roles/compute.securityAdmin is granted or the skipFirewall setting is used")
	return account, nil
}

// Sizes implements compute.Provider. region is a zone.
func (p *Provider) Sizes(ctx context.Context, region string) ([]compute.Size, error) {
	zone := region
	if zone == "" {
		zone = p.defaultZone
	}
	if zone == "" {
		return nil, errors.New("machine types are listed per zone: give a zone such as europe-west4-a")
	}
	var sizes []compute.Size
	token := ""
	for {
		var out struct {
			Items []struct {
				Name         string `json:"name"`
				Description  string `json:"description"`
				GuestCPUs    int    `json:"guestCpus"`
				MemoryMB     int64  `json:"memoryMb"`
				Architecture string `json:"architecture"`
				Accelerators []struct {
					GuestAcceleratorCount int `json:"guestAcceleratorCount"`
				} `json:"accelerators"`
				Deprecated *struct {
					State string `json:"state"`
				} `json:"deprecated"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		query := url.Values{"maxResults": {strconv.Itoa(pageSize)}}
		if token != "" {
			query.Set("pageToken", token)
		}
		if _, err := p.api.Do(ctx, http.MethodGet, p.zonePath(zone)+"/machineTypes", query, nil, &out); err != nil {
			return nil, err
		}
		for _, t := range out.Items {
			if t.Deprecated != nil && t.Deprecated.State != "" && t.Deprecated.State != "ACTIVE" {
				continue
			}
			if len(t.Accelerators) > 0 {
				continue // Geass installs no GPU driver
			}
			sizes = append(sizes, compute.Size{
				ID: t.Name, Description: t.Description, VCPUs: t.GuestCPUs, MemoryMiB: t.MemoryMB,
				DiskGB: defaultDiskGB, Arch: archOf(t.Architecture, t.Name),
			})
		}
		if out.NextPageToken == "" {
			break
		}
		token = out.NextPageToken
	}
	// Compute Engine publishes no prices, so the smallest machine stands for the cheapest.
	slices.SortStableFunc(sizes, func(a, b compute.Size) int {
		return cmp.Or(cmp.Compare(a.VCPUs, b.VCPUs), cmp.Compare(a.MemoryMiB, b.MemoryMiB), strings.Compare(a.ID, b.ID))
	})
	return sizes, nil
}

// archOf reads a machine type's architecture, falling back to the name for the Arm series when the API says nothing.
func archOf(architecture, name string) string {
	switch architecture {
	case "ARM64":
		return "arm64"
	case "X86_64":
		return "amd64"
	}
	for _, prefix := range []string{"t2a-", "c4a-", "n4a-", "a4x-"} {
		if strings.HasPrefix(name, prefix) {
			return "arm64"
		}
	}
	return "amd64"
}

// Create implements compute.Provider.
func (p *Provider) Create(ctx context.Context, spec compute.InstanceSpec) (compute.Instance, error) {
	zone := strings.TrimSpace(spec.Region)
	if zone == "" {
		zone = p.defaultZone
	}
	if err := checkZone(zone); err != nil {
		return compute.Instance{}, err
	}
	if !validName(spec.Name) {
		return compute.Instance{}, fmt.Errorf("%q cannot be the name of a Compute Engine instance (1 to 63 lowercase letters, digits and hyphens, starting with a letter)", spec.Name)
	}
	cluster := spec.Labels[compute.LabelCluster]

	// A machine of this name that exists already is the answer to a repeated create.
	existing, err := p.get(ctx, zone, spec.Name)
	switch {
	case err == nil:
		if existing.Labels[compute.LabelCluster] != cluster {
			return compute.Instance{}, fmt.Errorf("an instance named %s exists in zone %s but is not this cluster's", spec.Name, zone)
		}
		return existing.instance(), nil
	case !errors.Is(err, compute.ErrNotFound):
		return compute.Instance{}, err
	}

	tag, err := p.ensureFirewall(ctx, spec)
	if err != nil {
		return compute.Instance{}, fmt.Errorf("firewall: %w", err)
	}

	boot := map[string]any{
		"sourceImage": p.sourceImage(ctx, zone, spec),
		"diskSizeGb":  strconv.Itoa(diskSize(spec)),
	}
	if diskType := strings.TrimSpace(spec.Settings[SettingDiskType]); diskType != "" {
		boot["diskType"] = "zones/" + zone + "/diskTypes/" + diskType
	}
	nic := map[string]any{
		"network": networkPath(spec.Settings),
		// ONE_TO_ONE_NAT without a natIP is an ephemeral external address, released with the machine.
		"accessConfigs": []map[string]any{{"type": "ONE_TO_ONE_NAT", "name": "External NAT"}},
	}
	if sub := strings.TrimSpace(spec.Settings[SettingSubnetwork]); sub != "" {
		nic["subnetwork"] = subnetworkPath(zone, sub)
	}
	body := map[string]any{
		"name":              spec.Name,
		"machineType":       "zones/" + zone + "/machineTypes/" + spec.Size,
		"disks":             []map[string]any{{"boot": true, "autoDelete": true, "type": "PERSISTENT", "initializeParams": boot}},
		"networkInterfaces": []map[string]any{nic},
		"labels":            spec.Labels,
		"metadata": map[string]any{"items": []map[string]string{
			{"key": "user-data", "value": spec.UserData},
			{"key": "enable-oslogin", "value": "FALSE"},
		}},
	}
	if tag != "" {
		body["tags"] = map[string]any{"items": []string{tag}}
	}
	if spec.Spot {
		body["scheduling"] = map[string]any{
			"provisioningModel": "SPOT", "instanceTerminationAction": "DELETE", "automaticRestart": false, "onHostMaintenance": "TERMINATE",
		}
	}

	query := url.Values{"requestId": {requestID(cluster, spec.Name)}}
	var op operation
	if _, err := p.api.Do(ctx, http.MethodPost, p.zonePath(zone)+"/instances", query, body, &op); err != nil {
		var exists *alreadyExistsError
		if errors.As(err, &exists) {
			// Another create won the race for the name.
			if found, getErr := p.get(ctx, zone, spec.Name); getErr == nil && found.Labels[compute.LabelCluster] == cluster {
				return found.instance(), nil
			}
		}
		return compute.Instance{}, err
	}
	if err := p.settle(ctx, zone, op); err != nil {
		return compute.Instance{}, err
	}
	return compute.Instance{
		ID: zone + "/" + spec.Name, Name: spec.Name, Region: zone, Size: spec.Size,
		State: compute.StatePending, Labels: spec.Labels,
	}, nil
}

// settle watches an insert Operation for a few seconds. Quota and stockout failures arrive in the Operation, shortly
// after the insert is accepted; a machine that is simply slow to boot is not waited for.
func (p *Provider) settle(ctx context.Context, zone string, op operation) error {
	attempts := 0
	if p.opPoll > 0 {
		attempts = int(p.opWait / p.opPoll)
	}
	for i := 0; ; i++ {
		if op.Status == "DONE" {
			return op.failure()
		}
		if i >= attempts || op.Name == "" {
			return nil
		}
		if err := p.pause(ctx, p.opPoll); err != nil {
			return err
		}
		var next operation
		if _, err := p.api.Do(ctx, http.MethodGet, p.zonePath(zone)+"/operations/"+url.PathEscape(op.Name), nil, nil, &next); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// The machine was accepted; being unable to watch the Operation is not a failure to create it.
			return nil
		}
		op = next
	}
}

func (p *Provider) get(ctx context.Context, zone, name string) (instance, error) {
	var out instance
	_, err := p.api.Do(ctx, http.MethodGet, p.zonePath(zone)+"/instances/"+url.PathEscape(name), nil, nil, &out)
	return out, err
}

// sourceImage picks the boot image: the machine's own Image, then the setting, then Ubuntu 24.04 for the machine
// type's architecture.
func (p *Provider) sourceImage(ctx context.Context, zone string, spec compute.InstanceSpec) string {
	if image := strings.TrimSpace(spec.Image); image != "" {
		return image
	}
	if image := strings.TrimSpace(spec.Settings[SettingImage]); image != "" {
		return image
	}
	var machine struct {
		Architecture string `json:"architecture"`
	}
	// A failure here is not worth failing the create for: the insert reports a machine type that does not exist.
	_, _ = p.api.Do(ctx, http.MethodGet, p.zonePath(zone)+"/machineTypes/"+url.PathEscape(spec.Size), nil, nil, &machine)
	if archOf(machine.Architecture, spec.Size) == "arm64" {
		return defaultImageARM64
	}
	return defaultImageAMD64
}

func diskSize(spec compute.InstanceSpec) int {
	if n, err := strconv.Atoi(strings.TrimSpace(spec.Settings[SettingDiskSizeGB])); err == nil && n >= 10 {
		return n
	}
	return defaultDiskGB
}

// checkZone refuses a region where a zone belongs: the zone has a letter at the end.
func checkZone(zone string) error {
	if zone == "" {
		return errors.New("a Compute Engine machine needs a zone, such as europe-west4-a")
	}
	i := strings.LastIndex(zone, "-")
	if i < 0 || len(zone)-i-1 != 1 || zone[i+1] < 'a' || zone[i+1] > 'z' {
		return fmt.Errorf("%q is not a zone: a zone ends in a letter, as europe-west4-a does", zone)
	}
	return nil
}

func validName(name string) bool {
	if name == "" || len(name) > 63 || name[0] < 'a' || name[0] > 'z' || name[len(name)-1] == '-' {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// requestID is the same UUID for the same cluster and name, so that Compute Engine ignores a repeated insert. It is
// laid out as a version 5 UUID, which is what the API asks for (any valid, non-zero UUID).
func requestID(cluster, name string) string {
	sum := sha256.Sum256([]byte("geass/gcp/" + cluster + "/" + name))
	b := sum[:16]
	b[6] = b[6]&0x0f | 0x50
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func networkPath(settings map[string]string) string {
	network := strings.TrimSpace(settings[SettingNetwork])
	switch {
	case network == "":
		return "global/networks/default"
	case strings.Contains(network, "/"):
		return network
	}
	return "global/networks/" + network
}

func subnetworkPath(zone, subnetwork string) string {
	if strings.Contains(subnetwork, "/") {
		return subnetwork
	}
	region := zone
	if i := strings.LastIndex(zone, "-"); i > 0 {
		region = zone[:i]
	}
	return "regions/" + region + "/subnetworks/" + subnetwork
}

// Get implements compute.Provider.
func (p *Provider) Get(ctx context.Context, id string) (compute.Instance, error) {
	zone, name, err := splitID(id)
	if err != nil {
		return compute.Instance{}, err
	}
	found, err := p.get(ctx, zone, name)
	if err != nil {
		return compute.Instance{}, err
	}
	return found.instance(), nil
}

type aggregatedList struct {
	Items map[string]struct {
		Instances []instance `json:"instances"`
	} `json:"items"`
	NextPageToken string   `json:"nextPageToken"`
	Unreachables  []string `json:"unreachables"`
}

// List implements compute.Provider. One aggregated list covers every zone of the project.
func (p *Provider) List(ctx context.Context, owner compute.Owner) ([]compute.Instance, error) {
	filter := fmt.Sprintf(`labels.%s = "true" AND labels.%s = %q`, compute.LabelManaged, compute.LabelCluster, owner.Cluster)
	owned, err := p.list(ctx, owner, filter)
	if err != nil && compute.IsStatus(err, http.StatusBadRequest) {
		// The filter was refused; the labels are checked here in any case.
		return p.list(ctx, owner, "")
	}
	if err == nil && len(owned) == 0 {
		// A filter that matched nothing is the one answer that cannot be trusted: the caller would think the
		// cluster has no machines. Looking again without it costs one more call, and only when there are none.
		return p.list(ctx, owner, "")
	}
	return owned, err
}

func (p *Provider) list(ctx context.Context, owner compute.Owner, filter string) ([]compute.Instance, error) {
	var instances []compute.Instance
	token := ""
	for {
		query := url.Values{"maxResults": {strconv.Itoa(pageSize)}, "returnPartialSuccess": {"true"}}
		if filter != "" {
			query.Set("filter", filter)
		}
		if token != "" {
			query.Set("pageToken", token)
		}
		var out aggregatedList
		if _, err := p.api.Do(ctx, http.MethodGet, "/projects/"+url.PathEscape(p.project)+"/aggregated/instances", query, nil, &out); err != nil {
			return nil, err
		}
		if len(out.Unreachables) > 0 {
			// A list with holes would make machines look as if they did not exist.
			return nil, fmt.Errorf("instances could not be listed in %s: Compute Engine reports them unreachable", strings.Join(out.Unreachables, ", "))
		}
		for _, scope := range out.Items {
			for _, i := range scope.Instances {
				if owner.Owns(i.Labels) {
					instances = append(instances, i.instance())
				}
			}
		}
		if out.NextPageToken == "" {
			return instances, nil
		}
		token = out.NextPageToken
	}
}

// Delete implements compute.Provider. The boot disk goes with the instance (autoDelete) and so does the ephemeral
// external address, so there is nothing else to remove. The delete is asked for again if it is called again.
func (p *Provider) Delete(ctx context.Context, id string) error {
	zone, name, err := splitID(id)
	if err != nil {
		return nil // an id that cannot name a machine names no machine
	}
	_, err = p.api.Do(ctx, http.MethodDelete, p.zonePath(zone)+"/instances/"+url.PathEscape(name), nil, nil, nil)
	if errors.Is(err, compute.ErrNotFound) {
		return nil
	}
	return err
}

// permissionError says which permission the service account lacks for the firewall, and what to do instead.
type permissionError struct {
	permission string
	cause      error
}

func (e *permissionError) Error() string {
	return fmt.Sprintf("the service account is not allowed to manage firewall rules (permission %s): grant it roles/compute.securityAdmin, or set the %s setting to true and open tcp 22 and 10250 and udp %d yourself, or name your own rules' network tag in the %s setting (%v)",
		e.permission, SettingSkipFW, compute.OverlayPortUDP, SettingFirewallTag, e.cause)
}

func (e *permissionError) Unwrap() error { return compute.ErrAuth }

type allowedRule struct {
	Protocol string   `json:"IPProtocol"`
	Ports    []string `json:"ports"`
}

type firewallRule struct {
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Network      string        `json:"network"`
	Direction    string        `json:"direction"`
	Allowed      []allowedRule `json:"allowed"`
	SourceRanges []string      `json:"sourceRanges"`
	TargetTags   []string      `json:"targetTags"`
	Disabled     bool          `json:"disabled"`
}

// clusterShort is the cluster's name as it appears in a rule or tag name: a DNS label of at most 40 characters.
func clusterShort(cluster string) string {
	short := compute.SafeLabelValue(cluster)
	if short != "" && len(short) <= 40 {
		return short
	}
	sum := sha256.Sum256([]byte(cluster))
	if len(short) > 31 {
		short = strings.Trim(short[:31], "-")
	}
	if short == "" {
		return hex.EncodeToString(sum[:6])
	}
	return short + "-" + hex.EncodeToString(sum[:4])
}

// desiredRules are what the machines' firewall lets in: SSH and the kubelet from the control plane (SSH from anywhere
// when it is not known), and the overlay from anywhere in a multi-site cluster. Nothing else comes in.
func desiredRules(spec compute.InstanceSpec, name, network string) ([]firewallRule, error) {
	anywhere := []string{"0.0.0.0/0"}
	var control []string
	for _, cidr := range spec.ControlPlaneCIDRs {
		if !strings.Contains(cidr, ":") { // the machines are IPv4 only
			control = append(control, cidr)
		}
	}
	if len(spec.ControlPlaneCIDRs) > 0 && len(control) == 0 {
		return nil, errors.New("the control plane has no IPv4 address, and machines on Google Cloud are reached over IPv4")
	}
	description := firewallMarker + " (cluster " + spec.Labels[compute.LabelCluster] + ")"
	ssh := control
	ports := []string{"22"}
	if len(control) == 0 {
		ssh = anywhere
	} else {
		ports = append(ports, strconv.Itoa(compute.KubeletPort))
	}
	return []firewallRule{
		{Name: name, Description: description, Network: network, Direction: "INGRESS", Allowed: []allowedRule{{Protocol: "tcp", Ports: ports}},
			SourceRanges: ssh, TargetTags: []string{name}},
		{Name: name + "-overlay", Description: description, Network: network, Direction: "INGRESS", Allowed: []allowedRule{{Protocol: "udp", Ports: []string{strconv.Itoa(compute.OverlayPortUDP)}}},
			SourceRanges: anywhere, TargetTags: []string{name}, Disabled: !spec.MultiSite},
	}, nil
}

// ensureFirewall makes sure the cluster's firewall rules exist and are current, and returns the network tag to put on
// the machine ("" when there is none). A tag the user names is used as it is, and the skipFirewall setting makes none.
func (p *Provider) ensureFirewall(ctx context.Context, spec compute.InstanceSpec) (string, error) {
	if tag := strings.TrimSpace(spec.Settings[SettingFirewallTag]); tag != "" {
		return tag, nil
	}
	if strings.EqualFold(strings.TrimSpace(spec.Settings[SettingSkipFW]), "true") {
		return "", nil
	}
	cluster := spec.Labels[compute.LabelCluster]
	if cluster == "" {
		return "", nil
	}
	name := "geass-" + clusterShort(cluster)
	rules, err := desiredRules(spec, name, networkPath(spec.Settings))
	if err != nil {
		return "", err
	}
	for _, rule := range rules {
		if err := p.ensureRule(ctx, rule); err != nil {
			return "", err
		}
	}
	return name, nil
}

func (p *Provider) ensureRule(ctx context.Context, want firewallRule) error {
	var have firewallRule
	_, err := p.api.Do(ctx, http.MethodGet, p.globalPath()+"/firewalls/"+url.PathEscape(want.Name), nil, nil, &have)
	switch {
	case errors.Is(err, compute.ErrNotFound):
		if want.Disabled {
			return nil // nothing to switch off
		}
		_, err := p.api.Do(ctx, http.MethodPost, p.globalPath()+"/firewalls", nil, want, nil)
		var exists *alreadyExistsError
		if errors.As(err, &exists) {
			return nil // a create of the same cluster ran at the same time
		}
		return firewallFailure("compute.firewalls.create", err)
	case errors.Is(err, compute.ErrAuth):
		return firewallFailure("compute.firewalls.get", err)
	case err != nil:
		return err
	}
	if !strings.HasPrefix(have.Description, firewallMarker) {
		return fmt.Errorf("a firewall rule named %s exists but is not Geass's: delete it, or name your own rules' network tag in the %s setting", want.Name, SettingFirewallTag)
	}
	if path.Base(have.Network) != path.Base(want.Network) {
		return fmt.Errorf("the firewall rule %s is on network %s, and a rule's network cannot be changed: delete the rule so that Geass makes it again", want.Name, path.Base(have.Network))
	}
	if sameRule(have, want) {
		return nil
	}
	// Sent as a map so that disabled=false is sent too.
	patch := map[string]any{"allowed": want.Allowed, "sourceRanges": want.SourceRanges, "targetTags": want.TargetTags, "disabled": want.Disabled}
	_, err = p.api.Do(ctx, http.MethodPatch, p.globalPath()+"/firewalls/"+url.PathEscape(want.Name), nil, patch, nil)
	return firewallFailure("compute.firewalls.update", err)
}

func firewallFailure(permission string, err error) error {
	if err != nil && errors.Is(err, compute.ErrAuth) {
		return &permissionError{permission: permission, cause: err}
	}
	return err
}

func sameRule(a, b firewallRule) bool {
	norm := func(r firewallRule) string {
		allowed := make([]string, 0, len(r.Allowed))
		for _, rule := range r.Allowed {
			ports := slices.Sorted(slices.Values(rule.Ports))
			allowed = append(allowed, strings.ToLower(rule.Protocol)+":"+strings.Join(ports, ","))
		}
		slices.Sort(allowed)
		sources := slices.Sorted(slices.Values(r.SourceRanges))
		tags := slices.Sorted(slices.Values(r.TargetTags))
		return strings.Join(allowed, ";") + "|" + strings.Join(sources, ",") + "|" + strings.Join(tags, ",") + "|" + strconv.FormatBool(r.Disabled)
	}
	return norm(a) == norm(b)
}

var _ compute.Provider = (*Provider)(nil)
