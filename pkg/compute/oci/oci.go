// Package oci creates machines at Oracle Cloud Infrastructure (Compute).
//
// What a maintainer has to know:
//
//   - Every request is signed with the user's RSA API key (HTTP Signature, rsa-sha256), in sign.go. The key must not be
//     passphrase protected, and the machine's clock must be within five minutes of Oracle's (a skewed clock is a 401).
//   - The API is per region, and so is the address of every call (iaas.<region>.oraclecloud.com, and the Identity
//     service at identity.<region>.oci.oraclecloud.com). Only the commercial realm is supported: government and
//     sovereign realms use other domains. A machine's ID is "<region>/<ocid>", so that Get and Delete find the right
//     region; a bare OCID works too, the region being read from it (Oracle writes phx and iad for Phoenix and Ashburn).
//     List looks in Owner.Regions, or in the connection's region when that is empty.
//   - A Size is "SHAPE" for a fixed shape (VM.Standard2.2) and "SHAPE:OCPUS:MEMORY_GB" for a Flex shape
//     (VM.Standard.E4.Flex:2:8). An OCPU is two vCPUs on x86 (one core with hyper-threading) and one vCPU on Arm
//     (Ampere), and Sizes reports vCPUs accordingly.
//   - The launch response holds no address: the public and private IP are on the VNIC, found through the instance's VNIC
//     attachment. Get and List do that lookup; Create does not, so it returns a pending machine without an address.
//   - ListInstances cannot filter by tag. List reads the compartment and keeps the instances whose freeform tags are
//     the owner's. Oracle keeps a terminated instance in the list for about twelve hours: List shows it as Gone, Get
//     reports it as not found.
//   - Launching is idempotent through opc-retry-token, made from the cluster, the name and the availability domain. Oracle
//     only honours a token for 24 hours, and not once the instance it made is gone.
//   - "Out of host capacity" (HTTP 500, InternalError) is a stockout in one availability domain. When the pool names no
//     availability domain, Create tries the tenancy's others before it gives up.
//   - Geass does not touch the network. The subnet's security list, or the network security group named in the nsgId
//     setting, has to allow tcp 22 and tcp 10250 from the control plane and udp 51820 in a multi-site cluster; the
//     subnet has to allow a public address on the VNIC.
//   - Oracle's Ubuntu images carry an operating system firewall that lets in SSH and nothing else, and ends its INPUT
//     chain with a REJECT. CustomizeCloudInit therefore opens udp 51820 and tcp 10250 with iptables, ahead of the
//     REJECT, and saves the rules with netfilter-persistent. It does not use ufw, which Canonical warns can stop an
//     Oracle instance from booting.
//   - Billing is per second with a one minute minimum. Oracle's API has no prices, so every Size has a zero price.
package oci

import (
	"cmp"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
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
const ID = "OCI"

const pageLimit = 100

// Credential and setting keys.
const (
	keyTenancy     = "tenancyOcid"
	keyUser        = "userOcid"
	keyFingerprint = "fingerprint"
	keyPrivateKey  = "privateKey"
	keyCompartment = "compartmentOcid"

	SettingAvailabilityDomain = "availabilityDomain"
	SettingSubnetID           = "subnetId"
	SettingImageID            = "imageId"
	SettingNSGID              = "nsgId"
	SettingDiskSizeGB         = "diskSizeGb"
)

func init() {
	compute.Register(compute.Descriptor{
		ID:      ID,
		Label:   "Oracle Cloud",
		Summary: "An API signing key (a user's RSA key, without a passphrase) of a user who may manage instance-family and use virtual-network-family in a compartment that Geass has to itself (and list the tenancy's availability domains, unless the availabilityDomain setting is given). Geass changes no network settings: the subnet's security list, or the network security group in the nsgId setting, must let in tcp 22 and 10250 from the control plane and udp 51820. A Flex shape's size is SHAPE:OCPUS:MEMORY_GB, such as VM.Standard.E4.Flex:2:8.",
		Fields: []compute.Field{
			{Key: keyTenancy, Label: "Tenancy OCID", Help: "Profile > Tenancy. Starts with ocid1.tenancy."},
			{Key: keyUser, Label: "User OCID", Help: "Profile > My profile. Starts with ocid1.user."},
			{Key: keyFingerprint, Label: "API key fingerprint", Help: "My profile > API keys: the fingerprint of the key, as aa:bb:cc:..."},
			{Key: keyPrivateKey, Label: "API private key (PEM)", Secret: true, Multiline: true, Help: "The private key file of the API key, without a passphrase."},
			{Key: keyCompartment, Label: "Compartment OCID", Help: "The compartment the machines are made in. Starts with ocid1.compartment. (or ocid1.tenancy. for the root)."},
		},
		Settings: []compute.Setting{
			{Key: SettingSubnetID, Label: "Subnet OCID", Help: "The subnet to attach the machine to. It must allow a public address.", Required: true},
			{Key: SettingAvailabilityDomain, Label: "Availability domain", Help: "A name such as Uocm:EU-FRANKFURT-1-AD-1. Default: the tenancy's first, then its others when one has no capacity."},
			{Key: SettingImageID, Label: "Image OCID", Help: "Boot from this image. Default: the newest Canonical Ubuntu 24.04 image that fits the shape."},
			{Key: SettingNSGID, Label: "Network security group OCID", Help: "Put the machine's VNIC in this group (several, separated by commas). Geass does not edit security lists."},
			{Key: SettingDiskSizeGB, Label: "Boot volume size (GB)", Help: "At least 50. Default: the image's own size."},
		},
		RegionHelp: "A region identifier: eu-frankfurt-1, us-ashburn-1, uk-london-1, ap-sydney-1. Shapes and stock differ per region and availability domain.",
		New: func(creds compute.Credentials, options compute.Options) (compute.Provider, error) {
			return New(creds, options)
		},
	})
}

// Provider is a compute.Provider for Oracle Cloud Infrastructure.
type Provider struct {
	tenancy, user, fingerprint, compartment string
	key                                     *rsa.PrivateKey
	region                                  string // the connection's default region
	baseURL                                 string // replaces every address, for tests
	httpClient                              *http.Client
	now                                     func() time.Time
	// sleep, when set, replaces waiting between retries of a rate limited request.
	sleep func(context.Context, time.Duration) error

	mu      sync.Mutex
	domains map[string][]string // availability domain names by region
}

var (
	fingerprintPattern = regexp.MustCompile(`^([0-9a-fA-F]{2}:){15}[0-9a-fA-F]{2}$`)
	regionPattern      = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)
	armShapePattern    = regexp.MustCompile(`\.A\d\.`)
)

// New returns a provider that acts with the credentials.
func New(creds compute.Credentials, options compute.Options) (*Provider, error) {
	p := &Provider{
		tenancy: creds.Get(keyTenancy), user: creds.Get(keyUser), fingerprint: creds.Get(keyFingerprint),
		compartment: creds.Get(keyCompartment), region: expandRegion(strings.TrimSpace(options.Region)),
		baseURL: strings.TrimRight(options.BaseURL, "/"), now: time.Now, domains: map[string][]string{},
	}
	for key, prefix := range map[string]string{keyTenancy: "ocid1.tenancy.", keyUser: "ocid1.user."} {
		if !strings.HasPrefix(creds.Get(key), prefix) {
			return nil, fmt.Errorf("%s must start with %s", key, prefix)
		}
	}
	if !strings.HasPrefix(p.compartment, "ocid1.compartment.") && !strings.HasPrefix(p.compartment, "ocid1.tenancy.") {
		return nil, fmt.Errorf("%s must start with ocid1.compartment. (or ocid1.tenancy. for the root compartment)", keyCompartment)
	}
	if !fingerprintPattern.MatchString(p.fingerprint) {
		return nil, fmt.Errorf("%s must look like aa:bb:cc:..., sixteen pairs of hex digits (the fingerprint Oracle shows next to the API key)", keyFingerprint)
	}
	key, err := parsePrivateKey(creds.Get(keyPrivateKey))
	if err != nil {
		return nil, err
	}
	p.key = key
	p.httpClient = withPageCapture(options.HTTP)
	return p, nil
}

func parsePrivateKey(text string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, errors.New("the API private key is not PEM")
	}
	if block.Type == "ENCRYPTED PRIVATE KEY" || block.Headers["Proc-Type"] != "" {
		return nil, errors.New("the API private key has a passphrase, which Geass cannot use: remove it with openssl rsa -in key.pem -out key-nopass.pem")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the API private key is not an RSA key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("the API private key is not an RSA key")
	}
	return key, nil
}

// expandRegion turns the short codes Oracle writes in some places into the region identifiers its addresses use.
func expandRegion(region string) string {
	switch region {
	case "phx":
		return "us-phoenix-1"
	case "iad":
		return "us-ashburn-1"
	}
	return region
}

// pageCapture lets a call find out what Oracle put in the opc-next-page header: the shared client hands back the
// status and the body only, and Oracle pages its lists through that header.
type pageCapture struct{ next string }

type pageKey struct{}

type captureTransport struct{ base http.RoundTripper }

func (t captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err == nil {
		if capture, ok := req.Context().Value(pageKey{}).(*pageCapture); ok {
			capture.next = resp.Header.Get("opc-next-page")
		}
	}
	return resp, err
}

func withPageCapture(base *http.Client) *http.Client {
	client := &http.Client{Timeout: 30 * time.Second}
	transport := http.DefaultTransport
	if base != nil {
		copied := *base
		client = &copied
		if base.Transport != nil {
			transport = base.Transport
		}
	}
	client.Transport = captureTransport{base: transport}
	return client
}

// api returns the client for a service in a region. service is "iaas" (Compute and Networking) or "identity".
func (p *Provider) api(service, region string) *compute.Client {
	base := p.baseURL
	if base == "" {
		if service == "identity" {
			base = "https://identity." + region + ".oci.oraclecloud.com"
		} else {
			base = "https://" + service + "." + region + ".oraclecloud.com"
		}
	}
	return &compute.Client{
		HTTP: p.httpClient, BaseURL: base, Sleep: p.sleep,
		Header:        http.Header{"User-Agent": []string{"geass"}},
		Authorize:     p.sign,
		ClassifyError: classify,
	}
}

func (p *Provider) regionOf(requested string) (string, error) {
	region := expandRegion(strings.TrimSpace(requested))
	if region == "" {
		region = p.region
	}
	if region == "" {
		return "", errors.New("a region is needed, such as eu-frankfurt-1")
	}
	if !regionPattern.MatchString(region) {
		return "", fmt.Errorf("%q is not an Oracle Cloud region identifier (eu-frankfurt-1, us-ashburn-1)", region)
	}
	return region, nil
}

// query writes a query string the way Oracle signs it: spaces as %20, never +.
func query(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			continue
		}
		parts = append(parts, url.QueryEscape(pairs[i])+"="+strings.ReplaceAll(url.QueryEscape(pairs[i+1]), "+", "%20"))
	}
	if len(parts) == 0 {
		return ""
	}
	return "?" + strings.Join(parts, "&")
}

// ociError is the error body of an Oracle Cloud API.
type ociError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// classify maps Oracle's error codes to the errors callers act on.
func classify(_, _ string, status int, body []byte) error {
	var parsed ociError
	_ = json.Unmarshal(body, &parsed)
	message := parsed.Message
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	lower := strings.ToLower(message)
	switch {
	case parsed.Code == "LimitExceeded" || parsed.Code == "QuotaExceeded":
		return &compute.QuotaError{Message: message}
	case parsed.Code == "OutOfHostCapacity" || strings.Contains(lower, "out of host capacity") || strings.Contains(lower, "out of capacity"):
		return &compute.CapacityError{Message: message}
	case status == http.StatusUnauthorized:
		return fmt.Errorf("%w: %s (check the user and tenancy OCIDs, the key's fingerprint, and that the clock of the machine Geass runs on is within five minutes of Oracle's)", compute.ErrAuth, message)
	}
	return nil
}

// Verify implements compute.Provider.
func (p *Provider) Verify(ctx context.Context) (compute.Account, error) {
	region, err := p.regionOf("")
	if err != nil {
		return compute.Account{}, err
	}
	// The compartment's name is only for display, and reading it takes a permission (inspect compartments) that a user
	// who may merely launch instances can lack, so a failure here is not a failure to verify. Listing instances below
	// is the real check, and it fails on a bad key as well.
	var compartment struct {
		Name string `json:"name"`
	}
	_, _ = p.api("identity", region).Do(ctx, http.MethodGet, "/20160918/compartments/"+url.PathEscape(p.compartment), nil, nil, &compartment)
	var instances []instance
	path := "/20160918/instances" + query("compartmentId", p.compartment, "limit", "1")
	if _, err := p.api("iaas", region).Do(ctx, http.MethodGet, path, nil, nil, &instances); err != nil {
		if errors.Is(err, compute.ErrNotFound) {
			return compute.Account{}, fmt.Errorf("%w: the compartment %s does not exist, or the user may not see it", compute.ErrAuth, p.compartment)
		}
		return compute.Account{}, err
	}
	name := compartment.Name
	if name == "" {
		name = p.compartment
	}
	return compute.Account{
		Name: "compartment " + name + " in " + region,
		Warnings: []string{
			"Geass does not edit security lists: the machines' subnet (or the network security group in the nsgId setting) has to allow tcp 22 and 10250 from the control plane and udp 51820 in a multi-site cluster, or the machines are created and never join",
		},
	}, nil
}

type shape struct {
	Shape                string   `json:"shape"`
	ProcessorDescription string   `json:"processorDescription"`
	Ocpus                float64  `json:"ocpus"`
	MemoryInGBs          float64  `json:"memoryInGBs"`
	Gpus                 int      `json:"gpus"`
	LocalDisks           int      `json:"localDisks"`
	IsFlexible           bool     `json:"isFlexible"`
	OcpuOptions          *minmax  `json:"ocpuOptions"`
	MemoryOptions        *memopts `json:"memoryOptions"`
}

type minmax struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

type memopts struct {
	MinInGBs            float64 `json:"minInGBs"`
	MaxInGBs            float64 `json:"maxInGBs"`
	MinPerOcpuInGBs     float64 `json:"minPerOcpuInGBs"`
	MaxPerOcpuInGBs     float64 `json:"maxPerOcpuInGBs"`
	DefaultPerOcpuInGBs float64 `json:"defaultPerOcpuInGBs"`
}

func isArm(s shape) bool {
	return strings.Contains(strings.ToLower(s.ProcessorDescription), "ampere") || armShapePattern.MatchString(s.Shape)
}

// Sizes implements compute.Provider.
func (p *Provider) Sizes(ctx context.Context, region string) ([]compute.Size, error) {
	region, err := p.regionOf(region)
	if err != nil {
		return nil, err
	}
	var shapes []shape
	if err := p.list(ctx, "iaas", region, "/20160918/shapes", []string{"compartmentId", p.compartment}, &shapes); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var sizes []compute.Size
	for _, s := range shapes {
		// Bare metal, GPU and local-disk shapes are not what an autoscaled worker is for.
		if seen[s.Shape] || !strings.HasPrefix(s.Shape, "VM.") || s.Gpus > 0 || s.LocalDisks > 0 {
			continue
		}
		seen[s.Shape] = true
		arch, perOcpu := "amd64", 2
		if isArm(s) {
			arch, perOcpu = "arm64", 1
		}
		if !s.IsFlexible {
			sizes = append(sizes, compute.Size{
				ID: s.Shape, Description: s.Shape, VCPUs: int(s.Ocpus) * perOcpu, MemoryMiB: int64(s.MemoryInGBs * 1024), Arch: arch,
			})
			continue
		}
		sizes = append(sizes, flexSizes(s, arch, perOcpu)...)
	}
	slices.SortStableFunc(sizes, func(a, b compute.Size) int {
		return cmp.Or(cmp.Compare(a.VCPUs, b.VCPUs), cmp.Compare(a.MemoryMiB, b.MemoryMiB), strings.Compare(a.ID, b.ID))
	})
	return sizes, nil
}

// flexSizes offers a Flex shape in a few useful sizes: 1 to 16 OCPUs, with 4 or 8 GB of memory per OCPU, inside the
// limits the shape states.
func flexSizes(s shape, arch string, perOcpu int) []compute.Size {
	var sizes []compute.Size
	for _, ocpus := range []float64{1, 2, 4, 8, 16} {
		if s.OcpuOptions != nil && (ocpus < s.OcpuOptions.Min || (s.OcpuOptions.Max > 0 && ocpus > s.OcpuOptions.Max)) {
			continue
		}
		last := -1.0
		for _, ratio := range []float64{4, 8} {
			memory := ocpus * ratio
			if m := s.MemoryOptions; m != nil {
				if m.MinPerOcpuInGBs > 0 && ratio < m.MinPerOcpuInGBs || m.MaxPerOcpuInGBs > 0 && ratio > m.MaxPerOcpuInGBs {
					continue
				}
				memory = max(memory, m.MinInGBs)
				if m.MaxInGBs > 0 {
					memory = min(memory, m.MaxInGBs)
				}
			}
			if memory == last {
				continue
			}
			last = memory
			sizes = append(sizes, compute.Size{
				ID:          s.Shape + ":" + formatNumber(ocpus) + ":" + formatNumber(memory),
				Description: fmt.Sprintf("%s, %s OCPU, %s GB", s.Shape, formatNumber(ocpus), formatNumber(memory)),
				VCPUs:       int(ocpus) * perOcpu, MemoryMiB: int64(memory * 1024), Arch: arch,
			})
		}
	}
	return sizes
}

func formatNumber(n float64) string { return strconv.FormatFloat(n, 'f', -1, 64) }

// parseSize reads a Size: "SHAPE", or "SHAPE:OCPUS:MEMORY_GB" for a Flex shape.
func parseSize(size string) (shape string, ocpus, memory float64, err error) {
	parts := strings.Split(strings.TrimSpace(size), ":")
	shape = parts[0]
	switch len(parts) {
	case 1:
		if strings.HasSuffix(shape, ".Flex") {
			return "", 0, 0, fmt.Errorf("%s is a Flex shape: give its size as %s:OCPUS:MEMORY_GB, such as %s:2:8", shape, shape, shape)
		}
		return shape, 0, 0, nil
	case 3:
		var errOcpus, errMemory error
		ocpus, errOcpus = strconv.ParseFloat(parts[1], 64)
		memory, errMemory = strconv.ParseFloat(parts[2], 64)
		if errOcpus != nil || errMemory != nil || ocpus <= 0 || memory <= 0 {
			return "", 0, 0, fmt.Errorf("%q is not a size: use SHAPE:OCPUS:MEMORY_GB with numbers, such as VM.Standard.E4.Flex:2:8", size)
		}
		return shape, ocpus, memory, nil
	}
	return "", 0, 0, fmt.Errorf("%q is not a size: use SHAPE, or SHAPE:OCPUS:MEMORY_GB for a Flex shape", size)
}

type instance struct {
	ID             string `json:"id"`
	DisplayName    string `json:"displayName"`
	LifecycleState string `json:"lifecycleState"`
	Shape          string `json:"shape"`
	ShapeConfig    *struct {
		Ocpus       float64 `json:"ocpus"`
		MemoryInGBs float64 `json:"memoryInGBs"`
	} `json:"shapeConfig"`
	TimeCreated  string            `json:"timeCreated"`
	FreeformTags map[string]string `json:"freeformTags"`
}

func (i instance) instance(region string) compute.Instance {
	created, _ := time.Parse(time.RFC3339, i.TimeCreated)
	size := i.Shape
	if i.ShapeConfig != nil && strings.HasSuffix(i.Shape, ".Flex") {
		size += ":" + formatNumber(i.ShapeConfig.Ocpus) + ":" + formatNumber(i.ShapeConfig.MemoryInGBs)
	}
	return compute.Instance{
		ID: region + "/" + i.ID, Name: i.DisplayName, Region: region, Size: size, State: stateOf(i.LifecycleState),
		Labels: i.FreeformTags, CreatedAt: created,
	}
}

func stateOf(lifecycle string) compute.State {
	switch lifecycle {
	case "RUNNING", "CREATING_IMAGE":
		return compute.StateRunning
	case "STOPPING", "STOPPED":
		return compute.StateStopped
	case "TERMINATING":
		return compute.StateTerminating
	case "TERMINATED":
		return compute.StateGone
	default:
		// PROVISIONING, STARTING, MOVING
		return compute.StatePending
	}
}

// retryToken is the same for the same cluster, name and availability domain: Oracle then returns the machine the first
// launch made instead of launching a second.
func retryToken(cluster, name, domain string) string {
	sum := sha256.Sum256([]byte("geass/oci/" + cluster + "/" + name + "/" + domain))
	return "geass-" + hex.EncodeToString(sum[:20])
}

// Create implements compute.Provider.
func (p *Provider) Create(ctx context.Context, spec compute.InstanceSpec) (compute.Instance, error) {
	region, err := p.regionOf(spec.Region)
	if err != nil {
		return compute.Instance{}, err
	}
	subnet := strings.TrimSpace(spec.Settings[SettingSubnetID])
	if subnet == "" {
		return compute.Instance{}, fmt.Errorf("the %s setting is needed: the subnet to put the machine in", SettingSubnetID)
	}
	shapeName, ocpus, memory, err := parseSize(spec.Size)
	if err != nil {
		return compute.Instance{}, err
	}
	imageID, err := p.imageFor(ctx, region, shapeName, spec)
	if err != nil {
		return compute.Instance{}, err
	}
	domains, err := p.availabilityDomains(ctx, region, spec.Settings[SettingAvailabilityDomain])
	if err != nil {
		return compute.Instance{}, err
	}

	vnic := map[string]any{"subnetId": subnet, "assignPublicIp": true}
	if nsgs := splitList(spec.Settings[SettingNSGID]); len(nsgs) > 0 {
		vnic["nsgIds"] = nsgs
	}
	source := map[string]any{"sourceType": "image", "imageId": imageID}
	if n, err := strconv.Atoi(strings.TrimSpace(spec.Settings[SettingDiskSizeGB])); err == nil && n >= 50 {
		source["bootVolumeSizeInGBs"] = n
	}
	body := map[string]any{
		"compartmentId":     p.compartment,
		"shape":             shapeName,
		"displayName":       spec.Name,
		"sourceDetails":     source,
		"createVnicDetails": vnic,
		"metadata":          map[string]string{"user_data": base64.StdEncoding.EncodeToString([]byte(spec.UserData))},
		"freeformTags":      spec.Labels,
	}
	if ocpus > 0 {
		body["shapeConfig"] = map[string]any{"ocpus": ocpus, "memoryInGBs": memory}
	}
	if spec.Spot {
		body["preemptibleInstanceConfig"] = map[string]any{"preemptionAction": map[string]any{"type": "TERMINATE", "preserveBootVolume": false}}
	}

	cluster := spec.Labels[compute.LabelCluster]
	var capacityErr error
	for _, domain := range domains {
		body["availabilityDomain"] = domain
		client := p.api("iaas", region)
		client.Header.Set("opc-retry-token", retryToken(cluster, spec.Name, domain))
		var launched instance
		if _, err := client.Do(ctx, http.MethodPost, "/20160918/instances", nil, body, &launched); err != nil {
			var capacity *compute.CapacityError
			if errors.As(err, &capacity) && len(domains) > 1 {
				capacityErr = err
				continue // another availability domain may have room
			}
			return compute.Instance{}, err
		}
		out := launched.instance(region)
		if out.Labels == nil {
			out.Labels = spec.Labels
		}
		return out, nil
	}
	return compute.Instance{}, capacityErr
}

func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// imageFor picks the boot image: the imageId setting, then the machine's own Image (an OCID, or a display name), then
// the newest Canonical Ubuntu 24.04 image that fits the shape.
func (p *Provider) imageFor(ctx context.Context, region, shapeName string, spec compute.InstanceSpec) (string, error) {
	if id := strings.TrimSpace(spec.Settings[SettingImageID]); id != "" {
		return id, nil
	}
	image := strings.TrimSpace(spec.Image)
	if strings.HasPrefix(image, "ocid1.image.") {
		return image, nil
	}
	pairs := []string{"compartmentId", p.compartment, "shape", shapeName, "lifecycleState", "AVAILABLE", "sortBy", "TIMECREATED", "sortOrder", "DESC", "limit", "10"}
	if image != "" {
		pairs = append(pairs, "displayName", image)
	} else {
		pairs = append(pairs, "operatingSystem", "Canonical Ubuntu", "operatingSystemVersion", "24.04")
	}
	var images []struct {
		ID             string `json:"id"`
		LifecycleState string `json:"lifecycleState"`
	}
	if _, err := p.api("iaas", region).Do(ctx, http.MethodGet, "/20160918/images"+query(pairs...), nil, nil, &images); err != nil {
		return "", err
	}
	for _, i := range images {
		if i.LifecycleState == "" || i.LifecycleState == "AVAILABLE" {
			return i.ID, nil
		}
	}
	what := "Canonical Ubuntu 24.04 image"
	if image != "" {
		what = "image called " + image
	}
	return "", fmt.Errorf("no %s fits shape %s in %s: name one in the %s setting", what, shapeName, region, SettingImageID)
}

// availabilityDomains returns the domain the pool names, or the tenancy's, in the order Oracle lists them.
func (p *Provider) availabilityDomains(ctx context.Context, region, named string) ([]string, error) {
	if named = strings.TrimSpace(named); named != "" {
		return []string{named}, nil
	}
	p.mu.Lock()
	cached := p.domains[region]
	p.mu.Unlock()
	if len(cached) > 0 {
		return cached, nil
	}
	var domains []struct {
		Name string `json:"name"`
	}
	path := "/20160918/availabilityDomains" + query("compartmentId", p.tenancy)
	if _, err := p.api("identity", region).Do(ctx, http.MethodGet, path, nil, nil, &domains); err != nil {
		if errors.Is(err, compute.ErrAuth) || errors.Is(err, compute.ErrNotFound) {
			// Listing the tenancy's availability domains is a permission a user who only launches instances can lack.
			return nil, fmt.Errorf("listing the availability domains: %w (name one in the %s setting to skip this call)", err, SettingAvailabilityDomain)
		}
		return nil, err
	}
	var names []string
	for _, d := range domains {
		names = append(names, d.Name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no availability domain is listed in %s: name one in the %s setting", region, SettingAvailabilityDomain)
	}
	p.mu.Lock()
	p.domains[region] = names
	p.mu.Unlock()
	return names, nil
}

// list reads every page of a list call into out, which must be a pointer to a slice.
func (p *Provider) list(ctx context.Context, service, region, path string, pairs []string, out any) error {
	var all []json.RawMessage
	page := ""
	for {
		capture := &pageCapture{}
		var items []json.RawMessage
		full := path + query(append(append([]string(nil), pairs...), "limit", strconv.Itoa(pageLimit), "page", page)...)
		if _, err := p.api(service, region).Do(context.WithValue(ctx, pageKey{}, capture), http.MethodGet, full, nil, nil, &items); err != nil {
			return err
		}
		all = append(all, items...)
		if capture.next == "" {
			break
		}
		page = capture.next
	}
	raw, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// splitID reads a machine id: "<region>/<ocid>", or a bare OCID whose fourth field is its region.
func (p *Provider) splitID(id string) (region, ocid string, err error) {
	if r, o, ok := strings.Cut(id, "/"); ok {
		if r = expandRegion(r); regionPattern.MatchString(r) && strings.HasPrefix(o, "ocid1.instance.") {
			return r, o, nil
		}
		return "", "", fmt.Errorf("%w: %q is not an Oracle Cloud machine id", compute.ErrNotFound, id)
	}
	if !strings.HasPrefix(id, "ocid1.instance.") {
		return "", "", fmt.Errorf("%w: %q is not an Oracle Cloud machine id", compute.ErrNotFound, id)
	}
	if fields := strings.Split(id, "."); len(fields) >= 5 {
		if r := expandRegion(fields[3]); regionPattern.MatchString(r) {
			return r, id, nil
		}
	}
	if p.region == "" {
		return "", "", fmt.Errorf("%w: %q has no region in it and the connection has none", compute.ErrNotFound, id)
	}
	return p.region, id, nil
}

type vnicAttachment struct {
	InstanceID     string `json:"instanceId"`
	LifecycleState string `json:"lifecycleState"`
	NicIndex       *int   `json:"nicIndex"`
	VnicID         string `json:"vnicId"`
}

// primaryVnic picks the VNIC the machine was launched with: the attached one with the lowest index.
func primaryVnic(attachments []vnicAttachment) string {
	best, bestIndex := "", int(^uint(0)>>1)
	for _, a := range attachments {
		if a.LifecycleState != "ATTACHED" || a.VnicID == "" {
			continue
		}
		index := 0
		if a.NicIndex != nil {
			index = *a.NicIndex
		}
		if index < bestIndex {
			best, bestIndex = a.VnicID, index
		}
	}
	return best
}

// addresses reads the public and private IP of a VNIC. A VNIC that cannot be read yet leaves them empty.
func (p *Provider) addresses(ctx context.Context, region, vnicID string) (public, private string, err error) {
	if vnicID == "" {
		return "", "", nil
	}
	var vnic struct {
		PublicIP  string `json:"publicIp"`
		PrivateIP string `json:"privateIp"`
	}
	if _, err := p.api("iaas", region).Do(ctx, http.MethodGet, "/20160918/vnics/"+url.PathEscape(vnicID), nil, nil, &vnic); err != nil {
		if errors.Is(err, compute.ErrNotFound) {
			return "", "", nil
		}
		return "", "", err
	}
	return vnic.PublicIP, vnic.PrivateIP, nil
}

// Get implements compute.Provider.
func (p *Provider) Get(ctx context.Context, id string) (compute.Instance, error) {
	region, ocid, err := p.splitID(id)
	if err != nil {
		return compute.Instance{}, err
	}
	var found instance
	if _, err := p.api("iaas", region).Do(ctx, http.MethodGet, "/20160918/instances/"+url.PathEscape(ocid), nil, nil, &found); err != nil {
		return compute.Instance{}, err
	}
	out := found.instance(region)
	switch out.State {
	case compute.StateGone:
		return compute.Instance{}, fmt.Errorf("%w: %s is terminated", compute.ErrNotFound, ocid)
	case compute.StateTerminating:
		return out, nil
	}
	var attachments []vnicAttachment
	path := "/20160918/vnicAttachments" + query("compartmentId", p.compartment, "instanceId", ocid)
	if _, err := p.api("iaas", region).Do(ctx, http.MethodGet, path, nil, nil, &attachments); err != nil {
		return compute.Instance{}, err
	}
	out.PublicIPv4, out.PrivateIPv4, err = p.addresses(ctx, region, primaryVnic(attachments))
	return out, err
}

// List implements compute.Provider.
func (p *Provider) List(ctx context.Context, owner compute.Owner) ([]compute.Instance, error) {
	regions := owner.Regions
	if len(regions) == 0 {
		regions = []string{""}
	}
	seen := map[string]bool{}
	var out []compute.Instance
	for _, requested := range regions {
		region, err := p.regionOf(requested)
		if err != nil {
			return nil, err
		}
		if seen[region] {
			continue
		}
		seen[region] = true
		found, err := p.listRegion(ctx, region, owner)
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	return out, nil
}

func (p *Provider) listRegion(ctx context.Context, region string, owner compute.Owner) ([]compute.Instance, error) {
	var all []instance
	if err := p.list(ctx, "iaas", region, "/20160918/instances", []string{"compartmentId", p.compartment}, &all); err != nil {
		return nil, err
	}
	var owned []instance
	for _, i := range all {
		if owner.Owns(i.FreeformTags) {
			owned = append(owned, i)
		}
	}
	if len(owned) == 0 {
		return nil, nil
	}
	// One list of the compartment's VNIC attachments serves every machine, instead of one call each.
	var attachments []vnicAttachment
	if err := p.list(ctx, "iaas", region, "/20160918/vnicAttachments", []string{"compartmentId", p.compartment}, &attachments); err != nil {
		return nil, err
	}
	byInstance := map[string][]vnicAttachment{}
	for _, a := range attachments {
		byInstance[a.InstanceID] = append(byInstance[a.InstanceID], a)
	}
	var out []compute.Instance
	for _, i := range owned {
		machine := i.instance(region)
		if machine.State != compute.StateGone && machine.State != compute.StateTerminating {
			var err error
			machine.PublicIPv4, machine.PrivateIPv4, err = p.addresses(ctx, region, primaryVnic(byInstance[i.ID]))
			if err != nil {
				return nil, err
			}
		}
		out = append(out, machine)
	}
	return out, nil
}

// Delete implements compute.Provider. The boot volume is deleted with the instance (preserveBootVolume=false) and the
// VNIC and its ephemeral public address go with it.
func (p *Provider) Delete(ctx context.Context, id string) error {
	region, ocid, err := p.splitID(id)
	if err != nil {
		return nil // an id that cannot name a machine names no machine
	}
	path := "/20160918/instances/" + url.PathEscape(ocid) + query("preserveBootVolume", "false")
	_, err = p.api("iaas", region).Do(ctx, http.MethodDelete, path, nil, nil, nil)
	switch {
	case err == nil, errors.Is(err, compute.ErrNotFound):
		return nil
	case compute.IsStatus(err, http.StatusConflict):
		// A machine that is already being removed answers a second delete with a conflict: that is a success.
		var found instance
		if _, getErr := p.api("iaas", region).Do(ctx, http.MethodGet, "/20160918/instances/"+url.PathEscape(ocid), nil, nil, &found); getErr == nil {
			if state := stateOf(found.LifecycleState); state == compute.StateTerminating || state == compute.StateGone {
				return nil
			}
		} else if errors.Is(getErr, compute.ErrNotFound) {
			return nil
		}
	}
	return err
}

// CustomizeCloudInit implements compute.CloudInitCustomizer. Oracle's Ubuntu images only let SSH through the operating
// system firewall, so the overlay and kubelet ports are opened here, ahead of the REJECT that ends the INPUT chain, and
// saved so that they survive a reboot.
func (p *Provider) CustomizeCloudInit(init *compute.CloudInit, _ compute.InstanceSpec) {
	init.RunCommands = append(init.RunCommands,
		openPort("udp", compute.OverlayPortUDP),
		openPort("tcp", compute.KubeletPort),
		"command -v netfilter-persistent >/dev/null 2>&1 && netfilter-persistent save || true",
	)
}

// openPort is the shell command that lets a port in, once, in front of the REJECT rule (or first when there is none).
func openPort(protocol string, port int) string {
	return fmt.Sprintf(`pos=$(iptables -L INPUT --line-numbers -n | awk '/REJECT/ {print $1; exit}'); `+
		`iptables -C INPUT -p %[1]s --dport %[2]d -j ACCEPT 2>/dev/null || iptables -I INPUT ${pos:-1} -p %[1]s --dport %[2]d -j ACCEPT`, protocol, port)
}

var (
	_ compute.Provider            = (*Provider)(nil)
	_ compute.CloudInitCustomizer = (*Provider)(nil)
)
