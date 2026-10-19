// Package aws creates machines on Amazon EC2 through the Query API, signed with the SigV4 signer the S3 and IAM client
// already uses. There is no AWS SDK.
//
// What a maintainer must know:
//   - EC2 answers in XML, and eventually consistent: a machine that was just launched may be missing from
//     DescribeInstances for a moment (the node controller waits before it believes that), and a terminated machine is
//     listed for about an hour (it is reported as gone).
//   - A key that is allowed to launch only machines tagged geass-managed=true (the documented IAM policy) cannot
//     list only those: Describe actions take no resource-level conditions, so listing filters by tag in the request.
//   - The machine's security group is Geass's own, made once per cluster in the machine's VPC; rules are kept equal to
//     what the cluster needs (SSH and the kubelet from the control plane, the overlay from anywhere in a multi-site
//     cluster). A group the user names is used as it is.
//   - Burstable (T) instances are launched with standard credits: the default, unlimited, can bill beyond the hourly
//     price.
package aws

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/degoke/geass/pkg/cloud"
	"github.com/degoke/geass/pkg/compute"
)

// ID is the provider's name in a GeassCloudConnection.
const ID = "AWS"

// paramAction is the parameter that names the operation of a Query API request.
const paramAction = "Action"

const (
	ec2Version     = "2016-11-15"
	defaultRegion  = "us-east-1"
	canonicalOwner = "099720109477"
	defaultDiskGB  = 30
	tagName        = "Name"
)

// Credential and setting keys. The credential keys are the ones an AWS connection for storage already uses, so one
// kind of Secret serves both.
const (
	keyAccessKeyID     = "accessKeyId"
	keySecretAccessKey = "secretAccessKey"
	keyRegion          = "region"

	SettingSubnetID        = "subnetId"
	SettingSecurityGroupID = "securityGroupId"
	SettingDiskGB          = "diskGb"
)

func init() {
	compute.Register(compute.Descriptor{
		ID:      ID,
		Label:   "Amazon EC2",
		Summary: "An IAM user's access key that may launch only instances tagged geass-managed=true and terminate only the instances that carry the tag (the policy is in the AWS connector documentation).",
		Fields: []compute.Field{
			{Key: keyAccessKeyID, Label: "Access key ID"},
			{Key: keySecretAccessKey, Label: "Secret access key", Secret: true},
			{Key: keyRegion, Label: "Default region", Optional: true, Help: "Used when a pool names no region, for example eu-north-1."},
		},
		Settings: []compute.Setting{
			{Key: SettingSubnetID, Label: "Subnet ID", Help: "Launch into this subnet (it must give instances a public address, or the pool's address type must be Private). Default: a default-VPC subnet."},
			{Key: SettingSecurityGroupID, Label: "Security group ID", Help: "Use this existing group instead of the one Geass makes."},
			{Key: SettingDiskGB, Label: "Root disk (GB)", Help: "Default 30."},
		},
		RegionHelp: "An AWS region such as eu-north-1 or us-east-1.",
		New: func(creds compute.Credentials, options compute.Options) (compute.Provider, error) {
			region := creds.Get(keyRegion)
			if region == "" {
				region = options.Region
			}
			return New(creds.Get(keyAccessKeyID), creds.Get(keySecretAccessKey), region, options), nil
		},
	})
}

// Provider is a compute.Provider for EC2.
type Provider struct {
	accessKey, secretKey string
	region               string
	options              compute.Options

	// mu guards clients: one Provider serves the node, pool and connection controllers and the dashboard at once.
	mu      sync.Mutex
	clients map[string]*compute.Client
}

// New returns a provider that acts with an access key. region is the default for a spec that names none.
func New(accessKey, secretKey, region string, options compute.Options) *Provider {
	if region == "" {
		region = defaultRegion
	}
	return &Provider{accessKey: accessKey, secretKey: secretKey, region: region, options: options, clients: map[string]*compute.Client{}}
}

// api returns the client for a service in a region.
func (p *Provider) api(service, region string) *compute.Client {
	key := service + "/" + region
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[key]; ok {
		return c
	}
	base := p.options.BaseURL
	if base == "" {
		base = fmt.Sprintf("https://%s.%s.amazonaws.com", service, region)
	}
	c := &compute.Client{
		HTTP: p.options.HTTP, BaseURL: base,
		// EC2 answers in XML whatever is asked, but STS answers in JSON when it is told JSON is wanted, and this
		// driver reads XML from both.
		Header: http.Header{"User-Agent": []string{"geass"}, "Accept": []string{"text/xml"}},
		Authorize: func(req *http.Request, body []byte) error {
			return cloud.SignAWSRequest(req, body, p.accessKey, p.secretKey, service, region)
		},
		ClassifyError: classify,
	}
	p.clients[key] = c
	return c
}

func (p *Provider) regionOf(spec string) string {
	if spec != "" {
		return spec
	}
	return p.region
}

type xmlError struct {
	Errors []struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	} `xml:"Errors>Error"`
	// STS and IAM wrap the error differently.
	Error struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	} `xml:"Error"`
}

type ec2Error struct {
	Code, Message string
}

func (e *ec2Error) Error() string { return e.Code + ": " + e.Message }

func parseError(body []byte) (code, message string) {
	var parsed xmlError
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return "", strings.TrimSpace(string(body))
	}
	if len(parsed.Errors) > 0 {
		return parsed.Errors[0].Code, trimEncodedFailure(parsed.Errors[0].Message)
	}
	return parsed.Error.Code, trimEncodedFailure(parsed.Error.Message)
}

// trimEncodedFailure drops the "Encoded authorization failure message" that EC2 appends to a refusal. It is about
// fifteen hundred characters that only "aws sts decode-authorization-message" can read, and the sentence before it
// already says what was refused and by which policy.
func trimEncodedFailure(message string) string {
	if reason, _, found := strings.Cut(message, "Encoded authorization failure message:"); found {
		return strings.TrimSpace(reason)
	}
	return message
}

// classify maps EC2's error codes to the errors callers act on.
func classify(_, _ string, _ int, body []byte) error {
	code, message := parseError(body)
	switch code {
	case "InstanceLimitExceeded", "VcpuLimitExceeded", "MaxSpotInstanceCountExceeded", "AddressLimitExceeded", "SecurityGroupLimitExceeded":
		return &compute.QuotaError{Message: message}
	case "InsufficientInstanceCapacity", "InsufficientHostCapacity", "InsufficientCapacity", "Unsupported", "SpotMaxPriceTooLow", "InsufficientFreeAddressesInSubnet":
		return &compute.CapacityError{Message: message}
	case "RequestLimitExceeded", "Throttling", "ThrottlingException":
		return &compute.RateLimitError{Message: message}
	case "AuthFailure", "UnauthorizedOperation", "InvalidClientTokenId", "SignatureDoesNotMatch", "OptInRequired", "UnrecognizedClientException":
		return fmt.Errorf("%w: %s %s", compute.ErrAuth, code, message)
	case "InvalidInstanceID.NotFound", "InvalidInstanceID.Malformed":
		return fmt.Errorf("%w: %s", compute.ErrNotFound, message)
	case "":
		return nil
	}
	return &ec2Error{Code: code, Message: message}
}

// call sends one Query API request and returns the XML answer.
func (p *Provider) call(ctx context.Context, service, region, version string, values url.Values) ([]byte, error) {
	values.Set("Version", version)
	var out []byte
	_, err := p.api(service, region).DoRaw(ctx, http.MethodPost, "/", nil, []byte(values.Encode()), "application/x-www-form-urlencoded", &out)
	return out, err
}

func (p *Provider) ec2(ctx context.Context, region string, values url.Values) ([]byte, error) {
	return p.call(ctx, "ec2", region, ec2Version, values)
}

type ec2Instance struct {
	InstanceID string `xml:"instanceId"`
	State      struct {
		Name string `xml:"name"`
	} `xml:"instanceState"`
	PrivateIP    string `xml:"privateIpAddress"`
	PublicIP     string `xml:"ipAddress"`
	InstanceType string `xml:"instanceType"`
	LaunchTime   string `xml:"launchTime"`
	Placement    struct {
		AZ string `xml:"availabilityZone"`
	} `xml:"placement"`
	Tags []struct {
		Key   string `xml:"key"`
		Value string `xml:"value"`
	} `xml:"tagSet>item"`
}

// withRegion returns an instance whose id says where it is. EC2 answers per region and an instance id does not carry
// its region, so the id Geass keeps is "<region>/<instance id>".
func withRegion(region string, i ec2Instance) compute.Instance {
	instance := i.instance()
	instance.ID = region + "/" + i.InstanceID
	if instance.Region == "" {
		instance.Region = region
	}
	return instance
}

// splitID reads an id made by withRegion. An id with no region is taken to be in the default one.
func (p *Provider) splitID(id string) (region, instanceID string) {
	if region, instanceID, ok := strings.Cut(id, "/"); ok {
		return region, instanceID
	}
	return p.region, id
}

func (i ec2Instance) instance() compute.Instance {
	labels := map[string]string{}
	name := ""
	for _, t := range i.Tags {
		if t.Key == tagName {
			name = t.Value
			continue
		}
		labels[t.Key] = t.Value
	}
	launched, _ := time.Parse(time.RFC3339, i.LaunchTime)
	region := i.Placement.AZ
	if len(region) > 1 {
		region = region[:len(region)-1]
	}
	return compute.Instance{
		ID: i.InstanceID, Name: name, Region: region, Size: i.InstanceType, State: stateOf(i.State.Name),
		PublicIPv4: i.PublicIP, PrivateIPv4: i.PrivateIP, Labels: labels, CreatedAt: launched,
	}
}

func stateOf(name string) compute.State {
	switch name {
	case "pending":
		return compute.StatePending
	case "running":
		return compute.StateRunning
	case "stopping", "stopped":
		return compute.StateStopped
	case "shutting-down":
		return compute.StateTerminating
	case "terminated":
		return compute.StateGone
	}
	return compute.StatePending
}

// tagParams writes tags as the numbered Tag.N.Key/Value parameters of a TagSpecification.
func tagParams(values url.Values, prefix string, tags map[string]string) {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for i, k := range keys {
		values.Set(fmt.Sprintf("%s.Tag.%d.Key", prefix, i+1), k)
		values.Set(fmt.Sprintf("%s.Tag.%d.Value", prefix, i+1), tags[k])
	}
}

// Verify implements compute.Provider. It asks who the key is, reads instances, and asks EC2 whether a launch with
// Geass's tags would be allowed, without launching one.
func (p *Provider) Verify(ctx context.Context) (compute.Account, error) {
	var account compute.Account
	out, err := p.call(ctx, "sts", p.region, "2011-06-15", url.Values{paramAction: {"GetCallerIdentity"}})
	if err != nil {
		return account, err
	}
	var identity struct {
		Account string `xml:"GetCallerIdentityResult>Account"`
		Arn     string `xml:"GetCallerIdentityResult>Arn"`
	}
	_ = xml.Unmarshal(out, &identity)
	account.Name = "AWS account " + identity.Account
	if _, err := p.ec2(ctx, p.region, url.Values{paramAction: {"DescribeInstances"}, "MaxResults": {"5"}}); err != nil {
		return account, err
	}
	probe := url.Values{
		paramAction: {"RunInstances"}, "DryRun": {"true"}, "ImageId": {"ami-00000000000000000"}, "InstanceType": {"t3.micro"},
		"MinCount": {"1"}, "MaxCount": {"1"},
	}
	tagParams(probe, "TagSpecification.1", map[string]string{compute.LabelManaged: "true"})
	probe.Set("TagSpecification.1.ResourceType", "instance")
	_, err = p.ec2(ctx, p.region, probe)
	var ec2Err *ec2Error
	switch {
	case err == nil:
	case errors.As(err, &ec2Err) && (ec2Err.Code == "DryRunOperation" || strings.HasPrefix(ec2Err.Code, "InvalidAMIID")):
		// The request was allowed, and only the placeholder image was wrong.
	case errors.Is(err, compute.ErrAuth):
		account.Warnings = append(account.Warnings, "the key may not launch instances tagged "+compute.LabelManaged+"=true: "+err.Error())
	default:
		account.Warnings = append(account.Warnings, "could not check that the key may launch instances: "+err.Error())
	}
	return account, nil
}

// Sizes implements compute.Provider.
func (p *Provider) Sizes(ctx context.Context, region string) ([]compute.Size, error) {
	region = p.regionOf(region)
	type item struct {
		Type string   `xml:"instanceType"`
		VCPU int      `xml:"vCpuInfo>defaultVCpus"`
		Mem  int64    `xml:"memoryInfo>sizeInMiB"`
		Arch []string `xml:"processorInfo>supportedArchitectures>item"`
		Free bool     `xml:"freeTierEligible"`
	}
	var sizes []compute.Size
	token := ""
	for range 20 {
		values := url.Values{paramAction: {"DescribeInstanceTypes"}, "MaxResults": {"100"},
			"Filter.1.Name": {"current-generation"}, "Filter.1.Value.1": {"true"},
			"Filter.2.Name": {"bare-metal"}, "Filter.2.Value.1": {"false"}}
		if token != "" {
			values.Set("NextToken", token)
		}
		body, err := p.ec2(ctx, region, values)
		if err != nil {
			return nil, err
		}
		var parsed struct {
			Items     []item `xml:"instanceTypeSet>item"`
			NextToken string `xml:"nextToken"`
		}
		if err := xml.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("unreadable DescribeInstanceTypes answer: %w", err)
		}
		for _, it := range parsed.Items {
			if it.VCPU < 1 || it.VCPU > 32 {
				continue
			}
			arch := "amd64"
			if len(it.Arch) > 0 && it.Arch[0] == "arm64" {
				arch = "arm64"
			}
			sizes = append(sizes, compute.Size{ID: it.Type, VCPUs: it.VCPU, MemoryMiB: it.Mem, Arch: arch})
		}
		if parsed.NextToken == "" {
			break
		}
		token = parsed.NextToken
	}
	// EC2's API has no prices, so the order is by size.
	slices.SortStableFunc(sizes, func(a, b compute.Size) int {
		return cmp.Or(cmp.Compare(a.VCPUs, b.VCPUs), cmp.Compare(a.MemoryMiB, b.MemoryMiB))
	})
	return sizes, nil
}

// imageFor finds the newest Canonical Ubuntu 24.04 image for an architecture, or uses the image the spec names.
func (p *Provider) imageFor(ctx context.Context, region, image, arch string) (string, error) {
	if image != "" {
		return image, nil
	}
	values := url.Values{paramAction: {"DescribeImages"}, "Owner.1": {canonicalOwner},
		"Filter.1.Name": {"name"}, "Filter.1.Value.1": {"ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-" + arch + "-server-*"},
		"Filter.2.Name": {"state"}, "Filter.2.Value.1": {"available"}}
	body, err := p.ec2(ctx, region, values)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Images []struct {
			ID      string `xml:"imageId"`
			Created string `xml:"creationDate"`
		} `xml:"imagesSet>item"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("unreadable DescribeImages answer: %w", err)
	}
	if len(parsed.Images) == 0 {
		return "", fmt.Errorf("no Ubuntu 24.04 %s image found in %s: name an image in the pool", arch, region)
	}
	slices.SortFunc(parsed.Images, func(a, b struct {
		ID      string `xml:"imageId"`
		Created string `xml:"creationDate"`
	}) int {
		return strings.Compare(b.Created, a.Created)
	})
	return parsed.Images[0].ID, nil
}

// archOf reads a type's architecture from EC2 so that the right image is chosen.
func (p *Provider) archOf(ctx context.Context, region, instanceType string) string {
	body, err := p.ec2(ctx, region, url.Values{paramAction: {"DescribeInstanceTypes"}, "InstanceType.1": {instanceType}})
	if err != nil {
		return "amd64"
	}
	var parsed struct {
		Arch []string `xml:"instanceTypeSet>item>processorInfo>supportedArchitectures>item"`
	}
	if xml.Unmarshal(body, &parsed) == nil && len(parsed.Arch) > 0 && parsed.Arch[0] == "arm64" {
		return "arm64"
	}
	return "amd64"
}

// clientToken makes the idempotency token of a launch: the same machine name in the same cluster, for the same node,
// is the same launch. EC2 keeps a token for a while after the instance is terminated and answers a repeat with that
// instance, so a node that reuses a removed node's name must not reuse its token.
func clientToken(spec compute.InstanceSpec) string {
	sum := sha256.Sum256([]byte(spec.Labels[compute.LabelCluster] + "/" + spec.Name + "/" + spec.Incarnation))
	return hex.EncodeToString(sum[:16])
}

// Create implements compute.Provider.
func (p *Provider) Create(ctx context.Context, spec compute.InstanceSpec) (compute.Instance, error) {
	region := p.regionOf(spec.Region)
	subnet := strings.TrimSpace(spec.Settings[SettingSubnetID])
	group, err := p.ensureSecurityGroup(ctx, region, subnet, spec)
	if err != nil {
		return compute.Instance{}, fmt.Errorf("security group: %w", err)
	}
	image, err := p.imageFor(ctx, region, spec.Image, p.archOf(ctx, region, spec.Size))
	if err != nil {
		return compute.Instance{}, err
	}
	disk := defaultDiskGB
	if raw := strings.TrimSpace(spec.Settings[SettingDiskGB]); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 8 {
			disk = n
		}
	}

	tags := map[string]string{tagName: spec.Name}
	maps.Copy(tags, spec.Labels)
	values := url.Values{
		paramAction: {"RunInstances"}, "ImageId": {image}, "InstanceType": {spec.Size}, "MinCount": {"1"}, "MaxCount": {"1"},
		"ClientToken": {clientToken(spec)}, "UserData": {base64.StdEncoding.EncodeToString([]byte(spec.UserData))},
		"BlockDeviceMapping.1.DeviceName": {"/dev/sda1"}, "BlockDeviceMapping.1.Ebs.VolumeSize": {strconv.Itoa(disk)},
		"BlockDeviceMapping.1.Ebs.VolumeType": {"gp3"}, "BlockDeviceMapping.1.Ebs.DeleteOnTermination": {"true"},
		// Pods must not reach the instance's credentials: IMDSv2 only, and one network hop.
		"MetadataOptions.HttpTokens": {"required"}, "MetadataOptions.HttpPutResponseHopLimit": {"1"},
	}
	if subnet != "" {
		values.Set("NetworkInterface.1.DeviceIndex", "0")
		values.Set("NetworkInterface.1.SubnetId", subnet)
		values.Set("NetworkInterface.1.AssociatePublicIpAddress", strconv.FormatBool(!spec.PrivateAddress))
		values.Set("NetworkInterface.1.SecurityGroupId.1", group)
	} else {
		values.Set("SecurityGroupId.1", group)
	}
	if strings.HasPrefix(strings.ToLower(spec.Size), "t") {
		values.Set("CreditSpecification.CpuCredits", "standard")
	}
	if spec.Spot {
		values.Set("InstanceMarketOptions.MarketType", "spot")
		values.Set("InstanceMarketOptions.SpotOptions.SpotInstanceType", "one-time")
		values.Set("InstanceMarketOptions.SpotOptions.InstanceInterruptionBehavior", "terminate")
	}
	for i, resource := range []string{"instance", "volume"} {
		prefix := fmt.Sprintf("TagSpecification.%d", i+1)
		values.Set(prefix+".ResourceType", resource)
		tagParams(values, prefix, tags)
	}

	body, err := p.ec2(ctx, region, values)
	if err != nil {
		var ec2Err *ec2Error
		if errors.As(err, &ec2Err) && ec2Err.Code == "IdempotentParameterMismatch" {
			// The same launch with other parameters (the pool changed size between two tries): the machine that
			// the first try made is the answer.
			if existing := p.findByName(ctx, region, spec); existing != nil {
				return *existing, nil
			}
		}
		return compute.Instance{}, err
	}
	var parsed struct {
		Instances []ec2Instance `xml:"instancesSet>item"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil || len(parsed.Instances) == 0 {
		return compute.Instance{}, fmt.Errorf("unreadable RunInstances answer: %v", err)
	}
	instance := withRegion(region, parsed.Instances[0])
	if instance.Name == "" {
		instance.Name = spec.Name
	}
	if instance.Labels == nil || instance.Labels[compute.LabelCluster] == "" {
		instance.Labels = spec.Labels
	}
	return instance, nil
}

func (p *Provider) findByName(ctx context.Context, region string, spec compute.InstanceSpec) *compute.Instance {
	owner := compute.Owner{Cluster: spec.Labels[compute.LabelCluster]}
	instances, err := p.list(ctx, region, owner)
	if err != nil {
		return nil
	}
	for i := range instances {
		if instances[i].Name == spec.Name && instances[i].Alive() {
			return &instances[i]
		}
	}
	return nil
}

// Get implements compute.Provider.
func (p *Provider) Get(ctx context.Context, id string) (compute.Instance, error) {
	region, instanceID := p.splitID(id)
	body, err := p.ec2(ctx, region, url.Values{paramAction: {"DescribeInstances"}, "InstanceId.1": {instanceID}})
	if err != nil {
		return compute.Instance{}, err
	}
	var parsed struct {
		Reservations []struct {
			Instances []ec2Instance `xml:"instancesSet>item"`
		} `xml:"reservationSet>item"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return compute.Instance{}, fmt.Errorf("unreadable DescribeInstances answer: %w", err)
	}
	for _, r := range parsed.Reservations {
		for _, i := range r.Instances {
			if i.InstanceID == instanceID {
				return withRegion(region, i), nil
			}
		}
	}
	return compute.Instance{}, compute.ErrNotFound
}

// List implements compute.Provider. It searches the regions the owner names, and the default region when it names
// none, because EC2 answers for one region at a time.
func (p *Provider) List(ctx context.Context, owner compute.Owner) ([]compute.Instance, error) {
	regions := owner.Regions
	if len(regions) == 0 {
		regions = []string{p.region}
	}
	seen := map[string]bool{}
	var all []compute.Instance
	for _, region := range regions {
		if region == "" || seen[region] {
			continue
		}
		seen[region] = true
		found, err := p.list(ctx, region, owner)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", region, err)
		}
		all = append(all, found...)
	}
	return all, nil
}

func (p *Provider) list(ctx context.Context, region string, owner compute.Owner) ([]compute.Instance, error) {
	var instances []compute.Instance
	token := ""
	for range 100 {
		values := url.Values{paramAction: {"DescribeInstances"}, "MaxResults": {"500"},
			"Filter.1.Name": {"tag:" + compute.LabelManaged}, "Filter.1.Value.1": {"true"},
			"Filter.2.Name": {"tag:" + compute.LabelCluster}, "Filter.2.Value.1": {owner.Cluster},
			"Filter.3.Name": {"instance-state-name"},
		}
		for i, state := range []string{"pending", "running", "stopping", "stopped", "shutting-down"} {
			values.Set(fmt.Sprintf("Filter.3.Value.%d", i+1), state)
		}
		if owner.Pool != "" {
			values.Set("Filter.4.Name", "tag:"+compute.LabelPool)
			values.Set("Filter.4.Value.1", owner.Pool)
		}
		if token != "" {
			values.Set("NextToken", token)
		}
		body, err := p.ec2(ctx, region, values)
		if err != nil {
			return nil, err
		}
		var parsed struct {
			Reservations []struct {
				Instances []ec2Instance `xml:"instancesSet>item"`
			} `xml:"reservationSet>item"`
			NextToken string `xml:"nextToken"`
		}
		if err := xml.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("unreadable DescribeInstances answer: %w", err)
		}
		for _, r := range parsed.Reservations {
			for _, i := range r.Instances {
				if instance := withRegion(region, i); owner.Owns(instance.Labels) {
					instances = append(instances, instance)
				}
			}
		}
		if parsed.NextToken == "" {
			break
		}
		token = parsed.NextToken
	}
	return instances, nil
}

// Delete implements compute.Provider. The root volume is deleted with the instance (DeleteOnTermination), and the
// address EC2 assigned is released with it.
func (p *Provider) Delete(ctx context.Context, id string) error {
	region, instanceID := p.splitID(id)
	_, err := p.ec2(ctx, region, url.Values{paramAction: {"TerminateInstances"}, "InstanceId.1": {instanceID}})
	if errors.Is(err, compute.ErrNotFound) {
		return nil
	}
	return err
}

type permission struct {
	Protocol string
	Port     int
	CIDR     string
}

// wantedPermissions are the inbound rules of the cluster's group: SSH and the kubelet from the control plane, and
// the overlay from anywhere in a multi-site cluster.
func wantedPermissions(spec compute.InstanceSpec) []permission {
	var out []permission
	ssh := spec.ControlPlaneCIDRs
	if len(ssh) == 0 {
		ssh = []string{"0.0.0.0/0"}
	}
	for _, cidr := range ssh {
		if !strings.Contains(cidr, ":") {
			out = append(out, permission{"tcp", 22, cidr})
		}
	}
	for _, cidr := range spec.ControlPlaneCIDRs {
		if !strings.Contains(cidr, ":") {
			out = append(out, permission{"tcp", compute.KubeletPort, cidr})
		}
	}
	if spec.MultiSite {
		out = append(out, permission{"udp", compute.OverlayPortUDP, "0.0.0.0/0"})
	}
	return out
}

// ensureSecurityGroup returns the id of the group of the cluster's machines, making it the first time and bringing
// its rules up to date after. The group the user names is used as it is.
func (p *Provider) ensureSecurityGroup(ctx context.Context, region, subnet string, spec compute.InstanceSpec) (string, error) {
	if id := strings.TrimSpace(spec.Settings[SettingSecurityGroupID]); id != "" {
		return id, nil
	}
	cluster := spec.Labels[compute.LabelCluster]
	if cluster == "" {
		return "", fmt.Errorf("a machine needs the cluster label")
	}
	vpc := ""
	if subnet != "" {
		body, err := p.ec2(ctx, region, url.Values{paramAction: {"DescribeSubnets"}, "SubnetId.1": {subnet}})
		if err != nil {
			return "", err
		}
		var parsed struct {
			VPC string `xml:"subnetSet>item>vpcId"`
		}
		_ = xml.Unmarshal(body, &parsed)
		if parsed.VPC == "" {
			return "", fmt.Errorf("subnet %s was not found", subnet)
		}
		vpc = parsed.VPC
	}
	name := "geass-" + cluster
	if len(name) > 60 {
		name = name[:60]
	}
	describe := url.Values{paramAction: {"DescribeSecurityGroups"},
		"Filter.1.Name": {"group-name"}, "Filter.1.Value.1": {name},
		"Filter.2.Name": {"tag:" + compute.LabelManaged}, "Filter.2.Value.1": {"true"}}
	if vpc != "" {
		describe.Set("Filter.3.Name", "vpc-id")
		describe.Set("Filter.3.Value.1", vpc)
	}
	body, err := p.ec2(ctx, region, describe)
	if err != nil {
		return "", err
	}
	type ipRange struct {
		CIDR string `xml:"cidrIp"`
	}
	var found struct {
		Groups []struct {
			ID    string `xml:"groupId"`
			Rules []struct {
				Protocol string    `xml:"ipProtocol"`
				From     int       `xml:"fromPort"`
				To       int       `xml:"toPort"`
				Ranges   []ipRange `xml:"ipRanges>item"`
			} `xml:"ipPermissions>item"`
		} `xml:"securityGroupInfo>item"`
	}
	if err := xml.Unmarshal(body, &found); err != nil {
		return "", fmt.Errorf("unreadable DescribeSecurityGroups answer: %w", err)
	}
	wanted := wantedPermissions(spec)
	if len(found.Groups) == 0 {
		create := url.Values{paramAction: {"CreateSecurityGroup"}, "GroupName": {name}, "GroupDescription": {"Machines of the Geass cluster " + cluster}}
		if vpc != "" {
			create.Set("VpcId", vpc)
		}
		create.Set("TagSpecification.1.ResourceType", "security-group")
		tagParams(create, "TagSpecification.1", map[string]string{compute.LabelManaged: "true", compute.LabelCluster: cluster, tagName: name})
		out, err := p.ec2(ctx, region, create)
		if err != nil {
			return "", err
		}
		var made struct {
			ID string `xml:"groupId"`
		}
		_ = xml.Unmarshal(out, &made)
		if made.ID == "" {
			return "", fmt.Errorf("CreateSecurityGroup returned no group id")
		}
		return made.ID, p.setPermissions(ctx, region, made.ID, wanted, nil)
	}
	group := found.Groups[0]
	var existing []permission
	for _, rule := range group.Rules {
		for _, r := range rule.Ranges {
			existing = append(existing, permission{rule.Protocol, rule.From, r.CIDR})
		}
	}
	return group.ID, p.setPermissions(ctx, region, group.ID, wanted, existing)
}

// setPermissions makes a group's inbound rules, among the ones Geass manages (SSH, the kubelet, the overlay), equal to
// wanted. Rules on other ports are the user's and are left alone.
func (p *Provider) setPermissions(ctx context.Context, region, group string, wanted, existing []permission) error {
	managed := func(r permission) bool {
		return (r.Protocol == "tcp" && (r.Port == 22 || r.Port == compute.KubeletPort)) || (r.Protocol == "udp" && r.Port == compute.OverlayPortUDP)
	}
	has := func(list []permission, want permission) bool { return slices.Contains(list, want) }
	var add, remove []permission
	for _, w := range wanted {
		if !has(existing, w) {
			add = append(add, w)
		}
	}
	for _, e := range existing {
		if managed(e) && !has(wanted, e) {
			remove = append(remove, e)
		}
	}
	apply := func(action string, rules []permission) error {
		if len(rules) == 0 {
			return nil
		}
		values := url.Values{paramAction: {action}, "GroupId": {group}}
		for i, r := range rules {
			prefix := fmt.Sprintf("IpPermissions.%d", i+1)
			values.Set(prefix+".IpProtocol", r.Protocol)
			values.Set(prefix+".FromPort", strconv.Itoa(r.Port))
			values.Set(prefix+".ToPort", strconv.Itoa(r.Port))
			values.Set(prefix+".IpRanges.1.CidrIp", r.CIDR)
		}
		_, err := p.ec2(ctx, region, values)
		return err
	}
	if err := apply("AuthorizeSecurityGroupIngress", add); err != nil {
		var ec2Err *ec2Error
		if !errors.As(err, &ec2Err) || ec2Err.Code != "InvalidPermission.Duplicate" {
			return err
		}
	}
	return apply("RevokeSecurityGroupIngress", remove)
}

// accessDescription marks the rules Geass adds to the security group of the control plane, so that they are known
// for what they are in the console.
const accessDescription = "geass"

// AllowAccess implements compute.AccessManager: it adds one ingress rule per address and port to a security group
// that guards machines Geass did not create. EC2 refuses a whole request when one rule in it exists already, so a
// refusal for that reason is retried one rule at a time.
func (p *Provider) AllowAccess(ctx context.Context, target compute.AccessTarget, rules []compute.AccessRule) error {
	return p.changeAccess(ctx, "AuthorizeSecurityGroupIngress", "InvalidPermission.Duplicate", target, rules)
}

// RevokeAccess implements compute.AccessManager.
func (p *Provider) RevokeAccess(ctx context.Context, target compute.AccessTarget, rules []compute.AccessRule) error {
	return p.changeAccess(ctx, "RevokeSecurityGroupIngress", "InvalidPermission.NotFound", target, rules)
}

func (p *Provider) changeAccess(ctx context.Context, action, ignore string, target compute.AccessTarget, rules []compute.AccessRule) error {
	if len(rules) == 0 {
		return nil
	}
	region := p.regionOf(target.Region)
	send := func(batch []compute.AccessRule) error {
		values := url.Values{paramAction: {action}, "GroupId": {target.ID}}
		for i, r := range batch {
			prefix := fmt.Sprintf("IpPermissions.%d", i+1)
			values.Set(prefix+".IpProtocol", r.Protocol)
			values.Set(prefix+".FromPort", strconv.Itoa(r.Port))
			values.Set(prefix+".ToPort", strconv.Itoa(r.Port))
			values.Set(prefix+".IpRanges.1.CidrIp", r.CIDR)
			if action == "AuthorizeSecurityGroupIngress" {
				values.Set(prefix+".IpRanges.1.Description", accessDescription)
			}
		}
		_, err := p.ec2(ctx, region, values)
		return err
	}
	ignored := func(err error) bool {
		var ec2Err *ec2Error
		return errors.As(err, &ec2Err) && ec2Err.Code == ignore
	}
	err := send(rules)
	if err == nil || !ignored(err) {
		return err
	}
	for _, r := range rules {
		if err := send([]compute.AccessRule{r}); err != nil && !ignored(err) {
			return err
		}
	}
	return nil
}

var (
	_ compute.Provider      = (*Provider)(nil)
	_ compute.AccessManager = (*Provider)(nil)
)
