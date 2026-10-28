// Package compute is how Geass asks a cloud for machines: create one, look at it, list the ones Geass owns, delete
// one. Every provider hides its own API, signing, network and billing quirks behind the same small contract, so
// that node pools and the autoscaler never know which cloud a node lives in.
//
// A machine made through this package is only ever a bare Ubuntu server with a public IPv4 address and one login
// (the key in its cloud-init). Everything that turns it into a cluster node is the SSH provisioner's job, as it is
// for a server the user brought along.
package compute

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Provider creates and destroys machines at one cloud, with one set of credentials.
type Provider interface {
	// Verify checks that the credentials work and that they allow what Geass needs. It changes nothing.
	Verify(ctx context.Context) (Account, error)
	// Sizes lists the machine sizes that can be created in a region, cheapest first. A provider that has no price
	// for a size leaves it zero.
	Sizes(ctx context.Context, region string) ([]Size, error)
	// Create starts a machine. It returns as soon as the cloud has accepted the request: the machine is usually
	// still pending and has no address yet, so callers poll Get. Creating a machine whose spec.Name already exists
	// and is owned the same way returns that machine instead of a second one, on every provider that can tell.
	Create(ctx context.Context, spec InstanceSpec) (Instance, error)
	// Get returns one machine. A machine that does not exist, or that the cloud has already removed, is
	// ErrNotFound.
	Get(ctx context.Context, id string) (Instance, error)
	// List returns the machines that carry Geass's labels for the owner, whatever state they are in. Machines that
	// are not Geass's are never listed, so a caller can treat everything it sees as its own to delete.
	List(ctx context.Context, owner Owner) ([]Instance, error)
	// Delete removes a machine and what only it used (its disk and its address). Deleting a machine that is
	// already gone succeeds.
	Delete(ctx context.Context, id string) error
}

// Account is what Verify learned about the credentials.
type Account struct {
	// Name identifies the account, project or tenant the credentials act on, for display.
	Name string
	// Warnings are things that work but are likely to fail later: a credential that expires soon, a missing
	// permission for something optional.
	Warnings []string
	// ExpiresAt is when the credentials stop working, when the provider says.
	ExpiresAt time.Time
	// Temporary says the credentials are short-lived by design, and whatever issues them renews them before they lapse
	// (a session token from sts:AssumeRole). ExpiresAt is still shown, but not counted down in a warning.
	Temporary bool
}

// Size is a machine size a provider offers.
type Size struct {
	// ID is what InstanceSpec.Size takes.
	ID          string
	Description string
	VCPUs       int
	MemoryMiB   int64
	DiskGB      int
	// HourlyPrice is the price of one machine for an hour, in Currency, without tax. Zero when unknown.
	HourlyPrice float64
	Currency    string
	// Arch is "amd64" or "arm64".
	Arch string
}

// InstanceSpec says what machine to create.
type InstanceSpec struct {
	// Name is unique among the owner's machines and is how a repeated Create finds the first one. It is a valid
	// DNS label, at most 63 characters.
	Name string
	// Region is the provider's own place name: a location, zone or region.
	Region string
	// Size is a Size.ID.
	Size string
	// Image names the operating system image. Empty means the provider's Ubuntu 24.04.
	Image string
	// UserData is the cloud-init document the machine starts with.
	UserData string
	// AuthorizedKeys are the public keys the machine's login accepts. They are already in UserData; a provider
	// whose API insists on a key at create time (Azure) reads them here.
	AuthorizedKeys []string
	// Labels are written on the machine when it is created, and are what List finds it by.
	Labels map[string]string
	// Spot asks for a cheaper machine that the provider may take back, where the provider has one.
	Spot bool
	// Settings are the provider-specific values of a pool (a subnet, a firewall, a zone), keyed as the provider's
	// descriptor documents.
	Settings map[string]string
	// ControlPlaneCIDRs are the addresses of the cluster's control plane, as CIDRs. A provider that makes a
	// firewall for the machine lets SSH and the kubelet port in from these and no others. Empty means SSH from
	// anywhere (the machine accepts only the cluster's key) and no kubelet access.
	ControlPlaneCIDRs []string
	// MultiSite says the machine joins a cluster whose nodes reach each other over the internet through
	// WireGuard, so the firewall also lets the overlay's UDP port in from anywhere. The overlay authenticates
	// every packet. It is false for a cluster whose mesh is a tailnet, which opens no port.
	MultiSite bool
	// PrivateAddress asks for a machine that Geass reaches on a private address. The provider still creates a
	// public one unless its settings say otherwise.
	PrivateAddress bool
	// Incarnation tells apart two nodes that were given the same name one after the other (a server removed and added
	// again). A provider that remembers a request token after the machine is gone includes it in the token, so that
	// the second node is not answered with the first one's dead machine.
	Incarnation string
}

// AccessTarget names a firewall that guards machines Geass did not create (the control plane's), at the provider.
type AccessTarget struct {
	// ID is the provider's id for the firewall or security group.
	ID string
	// Region is where it is, for a provider whose API answers per region.
	Region string
}

// AccessRule lets one address reach one port.
type AccessRule struct {
	Protocol string // "tcp" or "udp"
	Port     int
	CIDR     string
}

// AccessManager is implemented by a provider that can change the firewall in front of the control plane, so that a
// machine Geass creates can reach it without anyone opening the control plane to the internet. The rules a provider
// writes are marked as Geass's, and it never changes any other.
type AccessManager interface {
	// AllowAccess adds the rules to the firewall. A rule that is already there is left as it is.
	AllowAccess(ctx context.Context, target AccessTarget, rules []AccessRule) error
	// RevokeAccess removes the rules. A rule that is not there is not an error.
	RevokeAccess(ctx context.Context, target AccessTarget, rules []AccessRule) error
}

// CloudInitCustomizer is implemented by a provider whose images need something at first boot that others do not,
// such as an operating system firewall that is on by default. The node controller calls it before it renders the
// cloud-init document.
type CloudInitCustomizer interface {
	CustomizeCloudInit(init *CloudInit, spec InstanceSpec)
}

// OverlayPortUDP is the port k3s's WireGuard overlay listens on in a multi-site cluster.
const OverlayPortUDP = 51820

// KubeletPort is the TCP port of the kubelet, which the control plane reads logs and metrics from.
const KubeletPort = 10250

// State is where a machine is in its life.
type State string

const (
	// StatePending: the cloud is still creating the machine, or has not given it an address.
	StatePending State = "Pending"
	// StateRunning: the machine is up.
	StateRunning State = "Running"
	// StateStopped: the machine exists but is off. It still costs money on most clouds.
	StateStopped State = "Stopped"
	// StateTerminating: the cloud is removing it.
	StateTerminating State = "Terminating"
	// StateGone: the cloud no longer has it (some clouds keep listing a deleted machine for a while).
	StateGone State = "Gone"
)

// Instance is a machine as the cloud reports it.
type Instance struct {
	ID          string
	Name        string
	Region      string
	Size        string
	State       State
	PublicIPv4  string
	PrivateIPv4 string
	Labels      map[string]string
	CreatedAt   time.Time
}

// Ready reports whether the machine is running and can be reached at an address.
func (i Instance) Ready() bool {
	return i.State == StateRunning && i.PublicIPv4 != ""
}

// Alive reports whether the machine still costs money or may come back: it is not gone and not being removed.
func (i Instance) Alive() bool {
	return i.State != StateGone && i.State != StateTerminating
}

// The labels Geass writes on every machine. Their values hold only lowercase letters, digits and hyphens, which
// every provider accepts in a label or a tag.
const (
	LabelManaged = "geass-managed"
	LabelCluster = "geass-cluster"
	LabelPool    = "geass-pool"
	LabelNode    = "geass-node"
)

// Owner says whose machines to list.
type Owner struct {
	// Cluster is the cluster's identity, stable for the life of the cluster. It keeps one Geass installation from
	// touching the machines of another in the same cloud account.
	Cluster string
	// Pool narrows the list to one pool. Empty means every pool of the cluster.
	Pool string
	// Regions are the places to look in, for a provider whose API answers for one region at a time. Empty means
	// the connection's default region. Providers whose API sees the whole account ignore it.
	Regions []string
}

// Labels returns the labels a machine of the owner carries, with the node's name.
func (o Owner) Labels(node string) map[string]string {
	labels := map[string]string{LabelManaged: "true", LabelCluster: o.Cluster}
	if o.Pool != "" {
		labels[LabelPool] = o.Pool
	}
	if node != "" {
		labels[LabelNode] = node
	}
	return labels
}

// Owns reports whether labels say a machine belongs to the owner.
func (o Owner) Owns(labels map[string]string) bool {
	if labels[LabelManaged] != "true" || labels[LabelCluster] != o.Cluster || o.Cluster == "" {
		return false
	}
	return o.Pool == "" || labels[LabelPool] == o.Pool
}

// SafeLabelValue turns s into a value every provider's label rules accept: lowercase letters, digits and hyphens,
// at most 63 characters, starting and ending with a letter or digit.
func SafeLabelValue(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	return out
}

// Errors a provider returns. Callers match them with errors.Is or errors.As.
var (
	// ErrNotFound: the machine does not exist.
	ErrNotFound = errors.New("the machine does not exist")
	// ErrAuth: the credentials were refused. Retrying with the same credentials cannot work.
	ErrAuth = errors.New("the provider refused the credentials")
)

// QuotaError means the account is at a limit (instances, vCPUs, addresses) and cannot create another machine until
// something is deleted or the limit is raised.
type QuotaError struct{ Message string }

func (e *QuotaError) Error() string { return "the account is at a limit: " + e.Message }

// CapacityError means the provider has no room for this size in this place right now. A different size or place,
// or a later try, may work.
type CapacityError struct{ Message string }

func (e *CapacityError) Error() string { return "the provider has no capacity: " + e.Message }

// RateLimitError means the provider asked Geass to slow down.
type RateLimitError struct {
	RetryAfter time.Duration
	Message    string
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("the provider is rate limiting requests (retry in %s): %s", e.RetryAfter.Round(time.Second), e.Message)
	}
	return "the provider is rate limiting requests: " + e.Message
}

// IsTransient reports whether trying the same call again later can work: rate limiting and capacity problems,
// as opposed to refused credentials or a request the provider rejects.
func IsTransient(err error) bool {
	var rate *RateLimitError
	var capacity *CapacityError
	return errors.As(err, &rate) || errors.As(err, &capacity)
}

// IsQuota reports whether err is a QuotaError.
func IsQuota(err error) bool {
	var quota *QuotaError
	return errors.As(err, &quota)
}

// LabelsToTags writes labels as the "key:value" tags that providers without key-value labels use, sorted so that the
// result is the same each time.
func LabelsToTags(labels map[string]string) []string {
	tags := make([]string, 0, len(labels))
	for k, v := range labels {
		tags = append(tags, k+":"+v)
	}
	slices.Sort(tags)
	return tags
}

// TagsToLabels reads "key:value" tags back into labels. A tag without a colon is a label with no value.
func TagsToLabels(tags []string) map[string]string {
	labels := map[string]string{}
	for _, tag := range tags {
		k, v, _ := strings.Cut(tag, ":")
		labels[k] = v
	}
	return labels
}
