// Package azure creates machines as Microsoft Azure virtual machines through the Azure Resource Manager REST API,
// signed in with a Microsoft Entra service principal (client credentials). There is no Azure SDK.
//
// What a maintainer must know:
//   - Geass works inside one resource group that the connection names, and a custom role can be scoped to it (a tag
//     cannot scope a role). Everything a machine needs is made inside that group by one PUT of the virtual machine:
//     the network interface and the public address are described inline in networkProfile and made by Azure, with
//     deleteOption "Delete" on the interface, the address and the OS disk, so that deleting the virtual machine
//     deletes all of them and nothing keeps billing. The subnet is not Geass's: the pool names an existing one in a
//     virtual network Geass does not manage.
//   - The PUT is a long-running operation. Create returns when Azure has accepted it (201); Get reports the progress:
//     provisioningState and the power state of the instance view, then the addresses read from the network interface
//     and the public address it points at (three calls). A machine whose provisioning failed is reported Stopped, and
//     Delete removes it like any other. Allocation failures that Azure reports only afterwards therefore show up as
//     a machine that never runs.
//   - A machine is named by the PUT, so a repeated Create finds it by name. A machine of the same name that belongs
//     to another cluster (its geass-cluster tag differs) is an error and is never touched, because the PUT would
//     update it. A machine that exists with provisioningState Failed gets the same PUT again, which makes Azure try
//     once more.
//   - Public addresses are Standard SKU and static: Basic public addresses are retired. A Standard address accepts
//     nothing until a network security group allows it, so every machine has one: the pool's (nsgId) or Geass's own
//     per cluster and location, named geass-<cluster>-<location>. Its rules are the common ones (SSH and the kubelet
//     from the control plane, the overlay from anywhere in a multi-site cluster) followed by a deny of everything else
//     coming in, which also closes the virtual network's default allow. A group that was just written is waited for
//     until it is provisioned, because a network interface cannot refer to one that is still being made.
//   - Machines are tagged with Geass's labels, and List reads them back, so the tags are the identity of a machine.
//   - One host serves each of three APIs: login.microsoftonline.com (the token, cached until shortly before it
//     expires), management.azure.com (everything else) and prices.azure.com (unauthenticated, and never sent the
//     token). Options.BaseURL replaces all three. A next-page link is followed by its path and query only.
//   - The client secret of an app registration expires, after at most 24 months, and Azure does not tell a client
//     secret how long it has left. Every call then fails with AADSTS7000222.
//   - Sizes come from the subscription's vmSizes for the location, which is a subscription-scope read. Azure gives no
//     architecture there, so ARM sizes are recognised by the p in the name's suffix (Standard_D4ps_v5), and they get the
//     arm64 image.
package azure

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
const ID = "Azure"

const (
	defaultLoginURL  = "https://login.microsoftonline.com"
	defaultARMURL    = "https://management.azure.com"
	defaultPricesURL = "https://prices.azure.com"

	// computeVersion is a stable api-version of Microsoft.Compute that documents everything used here.
	computeVersion = "2024-07-01"
	// networkVersion is the api-version of Microsoft.Network for network interfaces, addresses and groups.
	networkVersion = "2024-05-01"
	// resourcesVersion is the api-version of resource groups.
	resourcesVersion = "2021-04-01"

	defaultImageVersion = "latest"
	defaultDiskGB       = 30
	adminUser           = compute.DefaultUser
	// the priorities of the rules of a group: Azure allows 100 to 4096, the lower first.
	priorityFirst = 100
	priorityDeny  = 4000
	// nsgWaitAttempts and nsgWait bound how long Create waits for a group it has just written.
	nsgWaitAttempts = 15
	nsgWait         = 2 * time.Second
	maxPricePages   = 40
	// maxCustomData is the most bytes Azure keeps of a machine's custom data.
	maxCustomData = 65535
)

// Credential and setting keys.
const (
	keyTenantID       = "tenantId"
	keyClientID       = "clientId"
	keyClientSecret   = "clientSecret"
	keySubscriptionID = "subscriptionId"
	keyResourceGroup  = "resourceGroup"

	SettingSubnetID     = "subnetId"
	SettingNSGID        = "nsgId"
	SettingImageVersion = "imageVersion"
	SettingDiskGB       = "diskGb"
)

func init() {
	compute.Register(compute.Descriptor{
		ID:    ID,
		Label: "Microsoft Azure",
		Summary: "A Microsoft Entra app registration (service principal) with a client secret, holding a custom role scoped to one resource group " +
			"that Geass has to itself: virtual machines, disks, network interfaces, public IP addresses and network security groups in it, " +
			"plus the subnet join action on the subnet and read of VM sizes in the subscription. The client secret expires, after at most 24 " +
			"months, and Azure cannot say when: note the date and renew it before then.",
		Fields: []compute.Field{
			{Key: keyTenantID, Label: "Tenant ID", Help: "Microsoft Entra ID > Overview > Tenant ID."},
			{Key: keyClientID, Label: "Client (application) ID"},
			{Key: keyClientSecret, Label: "Client secret", Secret: true, Help: "The secret's value, not its ID. It expires within 24 months."},
			{Key: keySubscriptionID, Label: "Subscription ID"},
			{Key: keyResourceGroup, Label: "Resource group", Help: "The group Geass creates machines in. Give it one of its own and scope the role to it."},
		},
		Settings: []compute.Setting{
			{Key: SettingSubnetID, Label: "Subnet ID", Required: true, Help: "The full resource ID of an existing subnet, in a virtual network Geass does not manage and that is in the pool's region."},
			{Key: SettingNSGID, Label: "Network security group ID", Help: "Use this existing group instead of the one Geass makes. It must let in what the cluster needs."},
			{Key: SettingImageVersion, Label: "Image version", Help: "Version of the Ubuntu 24.04 image, such as 24.04.202510010. Default: latest."},
			{Key: SettingDiskGB, Label: "OS disk (GB)", Help: "Default 30."},
		},
		RegionHelp: "An Azure location name such as westeurope, northeurope, uksouth, eastus2 or swedencentral (az account list-locations).",
		New: func(creds compute.Credentials, options compute.Options) (compute.Provider, error) {
			return New(Config{
				TenantID: creds.Get(keyTenantID), ClientID: creds.Get(keyClientID), ClientSecret: creds.Get(keyClientSecret),
				SubscriptionID: creds.Get(keySubscriptionID), ResourceGroup: creds.Get(keyResourceGroup),
			}, options), nil
		},
	})
}

// Config is the identity and the place Geass acts in.
type Config struct {
	TenantID       string
	ClientID       string
	ClientSecret   string
	SubscriptionID string
	ResourceGroup  string
}

// Provider is a compute.Provider for Azure virtual machines.
type Provider struct {
	cfg    Config
	arm    *armClient
	prices *compute.Client
	tokens *tokenSource
	region string
	// sleep waits between the checks of a group being made. Tests replace it.
	sleep func(ctx context.Context, d time.Duration) error
}

// New returns a provider that acts as the service principal in the resource group.
func New(cfg Config, options compute.Options) *Provider {
	login, arm, prices := defaultLoginURL, defaultARMURL, defaultPricesURL
	if options.BaseURL != "" {
		login, arm, prices = options.BaseURL, options.BaseURL, options.BaseURL
	}
	header := http.Header{"User-Agent": []string{"geass"}}
	p := &Provider{cfg: cfg, region: options.Region}
	p.tokens = &tokenSource{
		client: &compute.Client{HTTP: options.HTTP, BaseURL: login, Header: header, ClassifyError: classifyToken},
		cfg:    cfg, now: time.Now,
	}
	api := &compute.Client{
		HTTP: options.HTTP, BaseURL: arm, Header: header,
		Authorize: func(req *http.Request, _ []byte) error {
			token, err := p.tokens.get(req.Context())
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+token)
			return nil
		},
		ClassifyError: func(method, path string, status int, body []byte) error {
			if status == http.StatusUnauthorized {
				// A token that Azure refuses is not kept.
				p.tokens.invalidate()
			}
			return classify(method, path, status, body)
		},
	}
	p.arm = &armClient{api: api, subscription: cfg.SubscriptionID, group: cfg.ResourceGroup}
	// The price list is public, so it gets no credentials: the token is for management.azure.com only.
	p.prices = &compute.Client{HTTP: options.HTTP, BaseURL: prices, Header: header}
	p.sleep = func(ctx context.Context, d time.Duration) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
	return p
}

// --- sign-in ---

// tokenSource gets an access token for Azure Resource Manager with the client credentials grant and keeps it until
// shortly before it expires.
type tokenSource struct {
	client *compute.Client
	cfg    Config
	now    func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

func (t *tokenSource) get(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && t.now().Add(5*time.Minute).Before(t.expires) {
		return t.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {t.cfg.ClientID},
		"client_secret": {t.cfg.ClientSecret},
		"scope":         {"https://management.azure.com/.default"},
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   any    `json:"expires_in"`
	}
	path := "/" + url.PathEscape(t.cfg.TenantID) + "/oauth2/v2.0/token"
	if _, err := t.client.DoRaw(ctx, http.MethodPost, path, nil, []byte(form.Encode()), "application/x-www-form-urlencoded", &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("%w: the token endpoint returned no token", compute.ErrAuth)
	}
	lifetime := time.Hour
	if seconds, err := strconv.ParseFloat(fmt.Sprint(out.ExpiresIn), 64); err == nil && seconds > 0 {
		lifetime = time.Duration(seconds * float64(time.Second))
	}
	t.token, t.expires = out.AccessToken, t.now().Add(lifetime)
	return t.token, nil
}

func (t *tokenSource) invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.token, t.expires = "", time.Time{}
}

// classifyToken turns a refusal by the token endpoint into ErrAuth: a wrong secret, an expired one, an unknown tenant
// or application are all the credentials being wrong, and retrying with them cannot work.
func classifyToken(method, path string, status int, body []byte) error {
	if status < 400 || status >= 500 || status == http.StatusTooManyRequests {
		return nil
	}
	var parsed struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &parsed)
	message := parsed.Description
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if first, _, found := strings.Cut(message, "\r\n"); found {
		message = first
	}
	if parsed.Error != "" {
		message = parsed.Error + ": " + message
	}
	return fmt.Errorf("%w: %s", compute.ErrAuth, message)
}

// --- Resource Manager ---

// armClient makes the calls to Azure Resource Manager of one subscription and resource group. Every call carries an
// api-version, and a resource is named by its path.
type armClient struct {
	api          *compute.Client
	subscription string
	group        string
}

func (a *armClient) groupPath(rest string) string {
	return "/subscriptions/" + url.PathEscape(a.subscription) + "/resourceGroups/" + url.PathEscape(a.group) + rest
}

func (a *armClient) subscriptionPath(rest string) string {
	return "/subscriptions/" + url.PathEscape(a.subscription) + rest
}

func (a *armClient) do(ctx context.Context, method, path, version string, extra url.Values, body, out any) (int, error) {
	query := url.Values{"api-version": {version}}
	maps.Copy(query, extra)
	return a.api.Do(ctx, method, path, query, body, out)
}

// nextPage reads the path and query of a next-page link, and nothing of its host: a link is only ever followed on the
// host the provider was built with.
func nextPage(link string) (string, url.Values, bool) {
	if link == "" {
		return "", nil, false
	}
	u, err := url.Parse(link)
	if err != nil || u.Path == "" {
		return "", nil, false
	}
	return u.Path, u.Query(), true
}

// armError is the body of a Resource Manager error.
type armError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"details"`
	} `json:"error"`
}

// classify maps Azure's error codes to the errors callers act on.
func classify(method, path string, status int, body []byte) error {
	var parsed armError
	_ = json.Unmarshal(body, &parsed)
	code, message := parsed.Error.Code, parsed.Error.Message
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	for _, d := range parsed.Error.Details {
		if message != "" && d.Message != "" && !strings.Contains(message, d.Message) {
			message += " (" + d.Code + ": " + d.Message + ")"
		}
	}
	if code != "" {
		message = code + ": " + message
	}
	lower := strings.ToLower(message)
	switch {
	case code == "ResourceGroupNotFound":
		return fmt.Errorf("the resource group does not exist or is not visible to the service principal: %s", message)
	case strings.Contains(code, "QuotaExceeded"), strings.Contains(code, "LimitReached"), strings.Contains(code, "CountLimit"),
		code == "SubnetIsFull", code == "OperationNotAllowed" && strings.Contains(lower, "quota"),
		strings.Contains(lower, "exceeding approved") && strings.Contains(lower, "quota"):
		return &compute.QuotaError{Message: message}
	case code == "SkuNotAvailable", code == "AllocationFailed", code == "ZonalAllocationFailed",
		code == "OverconstrainedAllocationRequest", code == "OverconstrainedZonalAllocationRequest",
		code == "AllocationFailedOverconstrained":
		return &compute.CapacityError{Message: message}
	case status == http.StatusForbidden:
		return fmt.Errorf("%w (forbidden): %s", compute.ErrAuth, message)
	}
	return nil
}

// --- virtual machines ---

type vm struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags"`
	Properties struct {
		ProvisioningState string `json:"provisioningState"`
		TimeCreated       string `json:"timeCreated"`
		HardwareProfile   struct {
			VMSize string `json:"vmSize"`
		} `json:"hardwareProfile"`
		NetworkProfile struct {
			NetworkInterfaces []struct {
				ID string `json:"id"`
			} `json:"networkInterfaces"`
		} `json:"networkProfile"`
		InstanceView *struct {
			Statuses []struct {
				Code string `json:"code"`
			} `json:"statuses"`
		} `json:"instanceView"`
	} `json:"properties"`
}

func (v vm) powerState() string {
	if v.Properties.InstanceView == nil {
		return ""
	}
	for _, s := range v.Properties.InstanceView.Statuses {
		if rest, ok := strings.CutPrefix(s.Code, "PowerState/"); ok {
			return rest
		}
	}
	return ""
}

// stateOf translates a virtual machine's provisioning and power state. A machine whose provisioning failed but that
// runs anyway (an extension failed, say) is running; one that did not run is Stopped, so that it is deleted.
func stateOf(provisioning, power string) compute.State {
	switch {
	case strings.EqualFold(provisioning, "Deleting"):
		return compute.StateTerminating
	case power == "running":
		return compute.StateRunning
	case power == "stopping", power == "stopped", power == "deallocating", power == "deallocated":
		return compute.StateStopped
	case strings.EqualFold(provisioning, "Failed"):
		return compute.StateStopped
	}
	return compute.StatePending
}

func (v vm) instance() compute.Instance {
	created, _ := time.Parse(time.RFC3339, v.Properties.TimeCreated)
	return compute.Instance{
		ID: v.Name, Name: v.Name, Region: v.Location, Size: v.Properties.HardwareProfile.VMSize, Labels: v.Tags, CreatedAt: created,
		State: stateOf(v.Properties.ProvisioningState, v.powerState()),
	}
}

func (p *Provider) vmPath(name string) string {
	return p.arm.groupPath("/providers/Microsoft.Compute/virtualMachines/" + url.PathEscape(name))
}

func (p *Provider) getVM(ctx context.Context, name string, instanceView bool) (vm, error) {
	var extra url.Values
	if instanceView {
		extra = url.Values{"$expand": {"instanceView"}}
	}
	var out vm
	if _, err := p.arm.do(ctx, http.MethodGet, p.vmPath(name), computeVersion, extra, nil, &out); err != nil {
		return vm{}, err
	}
	return out, nil
}

// Verify implements compute.Provider.
func (p *Provider) Verify(ctx context.Context) (compute.Account, error) {
	var group struct {
		Name     string `json:"name"`
		Location string `json:"location"`
	}
	if _, err := p.arm.do(ctx, http.MethodGet, p.arm.groupPath(""), resourcesVersion, nil, nil, &group); err != nil {
		return compute.Account{}, err
	}
	account := compute.Account{Name: fmt.Sprintf("Azure subscription %s, resource group %s (%s)", p.cfg.SubscriptionID, p.cfg.ResourceGroup, group.Location)}
	// Reading the virtual machines shows that the role covers Compute in the group.
	if _, err := p.arm.do(ctx, http.MethodGet, p.arm.groupPath("/providers/Microsoft.Compute/virtualMachines"), computeVersion, nil, nil, &struct{}{}); err != nil {
		return compute.Account{}, fmt.Errorf("listing the virtual machines of the resource group: %w", err)
	}
	// Sizes are a subscription-scope read that a role scoped to the group does not include.
	if _, err := p.arm.do(ctx, http.MethodGet, p.arm.subscriptionPath("/providers/Microsoft.Compute/locations/"+url.PathEscape(locationOr(group.Location, p.region))+"/vmSizes"),
		computeVersion, nil, nil, &struct{}{}); err != nil {
		account.Warnings = append(account.Warnings, "the service principal cannot list VM sizes (Microsoft.Compute/locations/vmSizes/read at the subscription), so sizes cannot be offered: "+err.Error())
	}
	account.Warnings = append(account.Warnings,
		"the client secret expires, after at most 24 months, and Azure cannot say when: note its date and renew it before then",
		"Azure cannot say whether the principal may create machines, join the subnet or write security groups without doing it: a role that lacks them passes this check and fails when the first machine is created")
	return account, nil
}

func locationOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// --- sizes ---

var armSuffix = regexp.MustCompile(`^Standard_[A-Za-z]+[0-9]+(-[0-9]+)?[a-z]*p[a-z]*_v[0-9]+$`)

func isARM(size string) bool { return armSuffix.MatchString(size) }

// Sizes implements compute.Provider.
func (p *Provider) Sizes(ctx context.Context, region string) ([]compute.Size, error) {
	location := locationOr(strings.TrimSpace(region), p.region)
	if location == "" {
		return nil, errors.New("listing Azure sizes needs a location, such as westeurope")
	}
	var out struct {
		Value []struct {
			Name           string `json:"name"`
			NumberOfCores  int    `json:"numberOfCores"`
			MemoryInMB     int64  `json:"memoryInMB"`
			ResourceDiskMB int64  `json:"resourceDiskSizeInMB"`
		} `json:"value"`
	}
	path := p.arm.subscriptionPath("/providers/Microsoft.Compute/locations/" + url.PathEscape(location) + "/vmSizes")
	if _, err := p.arm.do(ctx, http.MethodGet, path, computeVersion, nil, nil, &out); err != nil {
		return nil, err
	}
	prices := p.hourlyPrices(ctx, location)

	var sizes []compute.Size
	for _, s := range out.Value {
		if s.Name == "" || s.NumberOfCores <= 0 || strings.HasPrefix(s.Name, "Basic_") {
			continue
		}
		arch := "amd64"
		if isARM(s.Name) {
			arch = "arm64"
		}
		sizes = append(sizes, compute.Size{
			ID: s.Name, Description: fmt.Sprintf("%s (%d GiB temporary disk)", s.Name, s.ResourceDiskMB/1024), VCPUs: s.NumberOfCores,
			MemoryMiB: s.MemoryInMB, DiskGB: defaultDiskGB, HourlyPrice: prices[s.Name], Currency: "USD", Arch: arch,
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

// hourlyPrices reads the pay-as-you-go Linux price of every size in the location from the public retail price list.
// It is best effort: a size without a price, or a list that cannot be read, leaves the price zero.
func (p *Provider) hourlyPrices(ctx context.Context, location string) map[string]float64 {
	prices := map[string]float64{}
	filter := fmt.Sprintf("serviceName eq 'Virtual Machines' and armRegionName eq '%s' and priceType eq 'Consumption'", strings.ReplaceAll(location, "'", ""))
	path, query := "/api/retail/prices", url.Values{"$filter": {filter}}
	for range maxPricePages {
		var out struct {
			Items []struct {
				RetailPrice   float64 `json:"retailPrice"`
				ArmSkuName    string  `json:"armSkuName"`
				ProductName   string  `json:"productName"`
				SkuName       string  `json:"skuName"`
				MeterName     string  `json:"meterName"`
				UnitOfMeasure string  `json:"unitOfMeasure"`
				Type          string  `json:"type"`
			} `json:"Items"`
			NextPageLink string `json:"NextPageLink"`
		}
		if _, err := p.prices.Do(ctx, http.MethodGet, path, query, nil, &out); err != nil {
			break
		}
		for _, item := range out.Items {
			name := strings.ToLower(item.SkuName + " " + item.MeterName)
			if item.ArmSkuName == "" || item.RetailPrice <= 0 || item.UnitOfMeasure != "1 Hour" || (item.Type != "" && item.Type != "Consumption") ||
				strings.Contains(item.ProductName, "Windows") || strings.Contains(name, "spot") || strings.Contains(name, "low priority") {
				continue
			}
			if old, ok := prices[item.ArmSkuName]; !ok || item.RetailPrice < old {
				prices[item.ArmSkuName] = item.RetailPrice
			}
		}
		next, nextQuery, ok := nextPage(out.NextPageLink)
		if !ok {
			break
		}
		path, query = next, nextQuery
	}
	return prices
}

// --- create ---

var (
	subnetPattern = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Network/virtualNetworks/[^/]+/subnets/[^/]+$`)
	nsgPattern    = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Network/networkSecurityGroups/[^/]+$`)
)

// Create implements compute.Provider.
func (p *Provider) Create(ctx context.Context, spec compute.InstanceSpec) (compute.Instance, error) {
	location := locationOr(strings.TrimSpace(spec.Region), p.region)
	if location == "" {
		return compute.Instance{}, errors.New("an Azure machine needs a location, such as westeurope")
	}
	subnet := strings.TrimSpace(spec.Settings[SettingSubnetID])
	if !subnetPattern.MatchString(subnet) {
		return compute.Instance{}, fmt.Errorf("the %s setting must be the full resource ID of a subnet (/subscriptions/.../virtualNetworks/.../subnets/...)", SettingSubnetID)
	}
	if len(spec.AuthorizedKeys) == 0 {
		return compute.Instance{}, errors.New("a machine at Azure has no password, so it needs an SSH public key")
	}
	if len(spec.UserData) > maxCustomData {
		return compute.Instance{}, fmt.Errorf("the cloud-init document is %d bytes and Azure holds at most %d of custom data", len(spec.UserData), maxCustomData)
	}

	// A machine of this name is the answer to a repeated Create, unless it is another cluster's: the PUT would
	// change that one.
	existing, err := p.getVM(ctx, spec.Name, false)
	switch {
	case err == nil:
		if existing.Tags[compute.LabelCluster] != spec.Labels[compute.LabelCluster] {
			return compute.Instance{}, fmt.Errorf("a virtual machine named %s exists in the resource group and is not this cluster's: it was left alone", spec.Name)
		}
		if !strings.EqualFold(existing.Properties.ProvisioningState, "Failed") {
			return p.Get(ctx, spec.Name)
		}
		// A machine that failed to provision is tried again by the same PUT.
	case !errors.Is(err, compute.ErrNotFound):
		return compute.Instance{}, err
	}

	nsg, err := p.ensureSecurityGroup(ctx, spec, location)
	if err != nil {
		return compute.Instance{}, fmt.Errorf("network security group: %w", err)
	}
	body, err := p.machineBody(spec, location, subnet, nsg)
	if err != nil {
		return compute.Instance{}, err
	}
	var created vm
	if _, err := p.arm.do(ctx, http.MethodPut, p.vmPath(spec.Name), computeVersion, nil, body, &created); err != nil {
		return compute.Instance{}, err
	}
	instance := created.instance()
	if instance.ID == "" {
		instance.ID, instance.Name = spec.Name, spec.Name
	}
	return instance, nil
}

func (p *Provider) machineBody(spec compute.InstanceSpec, location, subnet, nsg string) (map[string]any, error) {
	image, err := imageReference(spec)
	if err != nil {
		return nil, err
	}
	disk := defaultDiskGB
	if raw := strings.TrimSpace(spec.Settings[SettingDiskGB]); raw != "" {
		gb, err := strconv.Atoi(raw)
		if err != nil || gb <= 0 {
			return nil, fmt.Errorf("the %s setting %q is not a number of gigabytes", SettingDiskGB, raw)
		}
		disk = gb
	}
	var keys []map[string]string
	for _, key := range spec.AuthorizedKeys {
		keys = append(keys, map[string]string{"path": "/home/" + adminUser + "/.ssh/authorized_keys", "keyData": key})
	}
	networkInterface := map[string]any{
		"name": "nic-" + spec.Name,
		"properties": map[string]any{
			"primary":              true,
			"deleteOption":         "Delete",
			"networkSecurityGroup": map[string]string{"id": nsg},
			"ipConfigurations": []map[string]any{{
				"name": "ipconfig1",
				"properties": map[string]any{
					"primary": true,
					"subnet":  map[string]string{"id": subnet},
					"publicIPAddressConfiguration": map[string]any{
						"name": "pip-" + spec.Name,
						"sku":  map[string]string{"name": "Standard", "tier": "Regional"},
						"properties": map[string]any{
							"publicIPAllocationMethod": "Static",
							"deleteOption":             "Delete",
						},
					},
				},
			}},
		},
	}
	properties := map[string]any{
		"hardwareProfile": map[string]string{"vmSize": spec.Size},
		"storageProfile": map[string]any{
			"imageReference": image,
			"osDisk": map[string]any{
				"createOption": "FromImage",
				"deleteOption": "Delete",
				"diskSizeGB":   disk,
				"managedDisk":  map[string]string{"storageAccountType": "StandardSSD_LRS"},
			},
		},
		"osProfile": map[string]any{
			"computerName":  spec.Name,
			"adminUsername": adminUser,
			"customData":    base64.StdEncoding.EncodeToString([]byte(spec.UserData)),
			"linuxConfiguration": map[string]any{
				"disablePasswordAuthentication": true,
				"ssh":                           map[string]any{"publicKeys": keys},
			},
		},
		"networkProfile": map[string]any{
			"networkApiVersion":              "2020-11-01",
			"networkInterfaceConfigurations": []map[string]any{networkInterface},
		},
	}
	if spec.Spot {
		properties["priority"] = "Spot"
		properties["evictionPolicy"] = "Delete"
		properties["billingProfile"] = map[string]any{"maxPrice": -1}
	}
	return map[string]any{"location": location, "tags": spec.Labels, "properties": properties}, nil
}

// imageReference says which image to boot: Ubuntu 24.04 from Canonical by default (its arm64 build for an ARM size),
// or the image the spec names, as "publisher:offer:sku:version" or as the resource ID of a gallery or custom image.
func imageReference(spec compute.InstanceSpec) (map[string]string, error) {
	image := strings.TrimSpace(spec.Image)
	switch {
	case image == "":
		version := strings.TrimSpace(spec.Settings[SettingImageVersion])
		if version == "" {
			version = defaultImageVersion
		}
		sku := "server"
		if isARM(spec.Size) {
			sku = "server-arm64"
		}
		return map[string]string{"publisher": "Canonical", "offer": "ubuntu-24_04-lts", "sku": sku, "version": version}, nil
	case strings.HasPrefix(image, "/subscriptions/"):
		return map[string]string{"id": image}, nil
	}
	parts := strings.Split(image, ":")
	if len(parts) != 4 {
		return nil, fmt.Errorf("the image %q is not publisher:offer:sku:version or a resource ID", image)
	}
	return map[string]string{"publisher": parts[0], "offer": parts[1], "sku": parts[2], "version": parts[3]}, nil
}

// --- get, list, delete ---

// Get implements compute.Provider.
func (p *Provider) Get(ctx context.Context, id string) (compute.Instance, error) {
	machine, err := p.getVM(ctx, id, true)
	if err != nil {
		return compute.Instance{}, err
	}
	instance := machine.instance()
	if instance.State == compute.StateTerminating || instance.State == compute.StateStopped || len(machine.Properties.NetworkProfile.NetworkInterfaces) == 0 {
		return instance, nil
	}
	instance.PublicIPv4, instance.PrivateIPv4, err = p.addresses(ctx, machine.Properties.NetworkProfile.NetworkInterfaces[0].ID)
	if err != nil {
		return compute.Instance{}, err
	}
	return instance, nil
}

// addresses reads the private address of a network interface and the public address it points at. An interface or an
// address that is not there yet is no address, not an error: Azure makes them while the machine is provisioning.
func (p *Provider) addresses(ctx context.Context, nicID string) (public, private string, err error) {
	if !strings.HasPrefix(nicID, "/subscriptions/") {
		return "", "", nil
	}
	var nic struct {
		Properties struct {
			IPConfigurations []struct {
				Properties struct {
					PrivateIPAddress string `json:"privateIPAddress"`
					Primary          bool   `json:"primary"`
					PublicIPAddress  *struct {
						ID string `json:"id"`
					} `json:"publicIPAddress"`
				} `json:"properties"`
			} `json:"ipConfigurations"`
		} `json:"properties"`
	}
	if _, err := p.arm.do(ctx, http.MethodGet, nicID, networkVersion, nil, nil, &nic); err != nil {
		if errors.Is(err, compute.ErrNotFound) {
			return "", "", nil
		}
		return "", "", err
	}
	var ipID string
	for _, c := range nic.Properties.IPConfigurations {
		if private == "" || c.Properties.Primary {
			private = c.Properties.PrivateIPAddress
		}
		if c.Properties.PublicIPAddress != nil && (ipID == "" || c.Properties.Primary) {
			ipID = c.Properties.PublicIPAddress.ID
		}
	}
	if !strings.HasPrefix(ipID, "/subscriptions/") {
		return "", private, nil
	}
	var pip struct {
		Properties struct {
			IPAddress string `json:"ipAddress"`
		} `json:"properties"`
	}
	if _, err := p.arm.do(ctx, http.MethodGet, ipID, networkVersion, nil, nil, &pip); err != nil {
		if errors.Is(err, compute.ErrNotFound) {
			return "", private, nil
		}
		return "", "", err
	}
	return pip.Properties.IPAddress, private, nil
}

// List implements compute.Provider. The addresses of a machine are not read: that takes two more calls each, and Get
// has them. The power state costs one call for a machine that has finished provisioning.
func (p *Provider) List(ctx context.Context, owner compute.Owner) ([]compute.Instance, error) {
	var instances []compute.Instance
	path, query := p.arm.groupPath("/providers/Microsoft.Compute/virtualMachines"), url.Values{"api-version": {computeVersion}}
	for {
		var out struct {
			Value    []vm   `json:"value"`
			NextLink string `json:"nextLink"`
		}
		if _, err := p.arm.api.Do(ctx, http.MethodGet, path, query, nil, &out); err != nil {
			return nil, err
		}
		for _, machine := range out.Value {
			if !owner.Owns(machine.Tags) {
				continue
			}
			if strings.EqualFold(machine.Properties.ProvisioningState, "Succeeded") && machine.Properties.InstanceView == nil {
				full, err := p.getVM(ctx, machine.Name, true)
				if errors.Is(err, compute.ErrNotFound) {
					continue
				}
				if err != nil {
					return nil, err
				}
				machine = full
			}
			instances = append(instances, machine.instance())
		}
		next, nextQuery, ok := nextPage(out.NextLink)
		if !ok {
			return instances, nil
		}
		path, query = next, nextQuery
	}
}

// Delete implements compute.Provider. The disk, the network interface and the public address of a machine made here
// are set to be deleted with it, so one call removes them all. Azure does the deleting after it has accepted the
// call, and the machine is Terminating until it is gone; asking again is harmless.
func (p *Provider) Delete(ctx context.Context, id string) error {
	_, err := p.arm.do(ctx, http.MethodDelete, p.vmPath(id), computeVersion, nil, nil, nil)
	if errors.Is(err, compute.ErrNotFound) {
		return nil
	}
	return err
}

// --- network security group ---

type nsgRule struct {
	Name     string
	Priority int
	Protocol string
	Port     string
	Access   string
	Sources  []string
}

// securityRules are what a machine's group lets in: SSH and the kubelet from the control plane, and the overlay from
// anywhere in a multi-site cluster, then a deny of everything else coming in. Without a known control plane SSH comes
// from anywhere (the machine accepts only the cluster's key) and the kubelet from nowhere.
func securityRules(spec compute.InstanceSpec) []nsgRule {
	anywhere := []string{"*"}
	ssh := anywhere
	if len(spec.ControlPlaneCIDRs) > 0 {
		ssh = spec.ControlPlaneCIDRs
	}
	rules := []nsgRule{{Name: "geass-ssh", Protocol: "Tcp", Port: "22", Access: "Allow", Sources: ssh}}
	if len(spec.ControlPlaneCIDRs) > 0 {
		rules = append(rules, nsgRule{Name: "geass-kubelet", Protocol: "Tcp", Port: strconv.Itoa(compute.KubeletPort), Access: "Allow", Sources: spec.ControlPlaneCIDRs})
	}
	if spec.MultiSite {
		rules = append(rules, nsgRule{Name: "geass-overlay", Protocol: "Udp", Port: strconv.Itoa(compute.OverlayPortUDP), Access: "Allow", Sources: anywhere})
	}
	for i := range rules {
		rules[i].Priority = priorityFirst + i
	}
	// The default AllowVnetInBound would otherwise let in every machine of the virtual network.
	return append(rules, nsgRule{Name: "geass-deny-inbound", Priority: priorityDeny, Protocol: "*", Port: "*", Access: "Deny", Sources: anywhere})
}

func (r nsgRule) body() map[string]any {
	properties := map[string]any{
		"priority": r.Priority, "direction": "Inbound", "access": r.Access, "protocol": r.Protocol,
		"sourcePortRange": "*", "destinationPortRange": r.Port, "destinationAddressPrefix": "*",
	}
	if len(r.Sources) == 1 {
		properties["sourceAddressPrefix"] = r.Sources[0]
	} else {
		properties["sourceAddressPrefixes"] = r.Sources
	}
	return map[string]any{"name": r.Name, "properties": properties}
}

func (r nsgRule) key() string {
	sources := append([]string(nil), r.Sources...)
	slices.Sort(sources)
	return fmt.Sprintf("%s|%d|%s|%s|%s|%s", r.Name, r.Priority, strings.ToLower(r.Access), strings.ToLower(r.Protocol), r.Port, strings.Join(sources, ","))
}

type nsg struct {
	Tags       map[string]string `json:"tags"`
	Properties struct {
		ProvisioningState string `json:"provisioningState"`
		SecurityRules     []struct {
			Name       string `json:"name"`
			Properties struct {
				Priority                 int      `json:"priority"`
				Direction                string   `json:"direction"`
				Access                   string   `json:"access"`
				Protocol                 string   `json:"protocol"`
				DestinationPortRange     string   `json:"destinationPortRange"`
				SourceAddressPrefix      string   `json:"sourceAddressPrefix"`
				SourceAddressPrefixes    []string `json:"sourceAddressPrefixes"`
				DestinationPortRanges    []string `json:"destinationPortRanges"`
				DestinationAddressPrefix string   `json:"destinationAddressPrefix"`
			} `json:"properties"`
		} `json:"securityRules"`
	} `json:"properties"`
}

func (g nsg) rules() []nsgRule {
	var rules []nsgRule
	for _, r := range g.Properties.SecurityRules {
		if !strings.EqualFold(r.Properties.Direction, "Inbound") {
			continue
		}
		sources := r.Properties.SourceAddressPrefixes
		if r.Properties.SourceAddressPrefix != "" {
			sources = append([]string{r.Properties.SourceAddressPrefix}, sources...)
		}
		port := r.Properties.DestinationPortRange
		if port == "" && len(r.Properties.DestinationPortRanges) == 1 {
			port = r.Properties.DestinationPortRanges[0]
		}
		rules = append(rules, nsgRule{Name: r.Name, Priority: r.Properties.Priority, Protocol: r.Properties.Protocol, Port: port, Access: r.Properties.Access, Sources: sources})
	}
	return rules
}

func sameRules(a, b []nsgRule) bool {
	norm := func(rules []nsgRule) []string {
		out := make([]string, 0, len(rules))
		for _, r := range rules {
			out = append(out, r.key())
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

// ensureSecurityGroup returns the ID of the network security group of a cluster's machines in a location, making it
// the first time and bringing its rules up to date after, and waiting until Azure has provisioned it. A group the
// user names is used as it is.
func (p *Provider) ensureSecurityGroup(ctx context.Context, spec compute.InstanceSpec, location string) (string, error) {
	if id := strings.TrimSpace(spec.Settings[SettingNSGID]); id != "" {
		if !nsgPattern.MatchString(id) {
			return "", fmt.Errorf("the %s setting must be the full resource ID of a network security group", SettingNSGID)
		}
		return id, nil
	}
	cluster := spec.Labels[compute.LabelCluster]
	if cluster == "" {
		cluster = "default"
	}
	name := "geass-" + shorten(compute.SafeLabelValue(cluster), 40) + "-" + location
	path := p.arm.groupPath("/providers/Microsoft.Network/networkSecurityGroups/" + url.PathEscape(name))
	id := path
	desired := securityRules(spec)

	var current nsg
	_, err := p.arm.do(ctx, http.MethodGet, path, networkVersion, nil, nil, &current)
	exists := err == nil
	if err != nil && !errors.Is(err, compute.ErrNotFound) {
		return "", err
	}
	if exists && current.Tags[compute.LabelManaged] != "true" {
		return "", fmt.Errorf("a network security group named %s exists but is not tagged as Geass's: delete it or name it in the %s setting", name, SettingNSGID)
	}
	if exists && sameRules(current.rules(), desired) && strings.EqualFold(current.Properties.ProvisioningState, "Succeeded") {
		return id, nil
	}
	if !exists || !sameRules(current.rules(), desired) {
		rules := make([]map[string]any, 0, len(desired))
		for _, r := range desired {
			rules = append(rules, r.body())
		}
		body := map[string]any{
			"location":   location,
			"tags":       map[string]string{compute.LabelManaged: "true", compute.LabelCluster: spec.Labels[compute.LabelCluster]},
			"properties": map[string]any{"securityRules": rules},
		}
		if _, err := p.arm.do(ctx, http.MethodPut, path, networkVersion, nil, body, nil); err != nil {
			return "", err
		}
	}
	// A network interface cannot refer to a group that is still being written, so wait for Azure to finish it.
	for attempt := range nsgWaitAttempts {
		if attempt > 0 {
			if err := p.sleep(ctx, nsgWait); err != nil {
				return "", err
			}
		}
		var got nsg
		if _, err := p.arm.do(ctx, http.MethodGet, path, networkVersion, nil, nil, &got); err != nil {
			return "", err
		}
		switch strings.ToLower(got.Properties.ProvisioningState) {
		case "succeeded":
			return id, nil
		case "failed":
			return "", fmt.Errorf("the network security group %s could not be provisioned", name)
		}
	}
	return "", &compute.RateLimitError{RetryAfter: 10 * time.Second, Message: "the network security group " + name + " is still being provisioned"}
}

func shorten(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

var _ compute.Provider = (*Provider)(nil)
