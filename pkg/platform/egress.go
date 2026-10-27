package platform

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// On a cluster that organizations share, a stranger's pod can send spam straight to the internet's mail servers, or mine
// for a pool, from the platform's addresses. The outbound limits close ports to addresses outside the cluster for the pods
// of organizations' projects.
//
// They are not a policy of their own. Network policies only ever allow, and a pod may open any connection that one of the
// policies selecting it allows, so a second policy could not close a port that geass-block-cloud-metadata (or, in a build
// namespace, geass-build-isolation) opens. The limits are written into those policies instead, by splitting their rule for
// the outside world:
//
//   - every pod of the cluster stays reachable on every port, so the cluster's DNS and other services are never cut off;
//   - addresses outside the cluster, less those the policy already closes, are reachable on the ports left open, written as
//     ranges (port and endPort), for TCP and UDP;
//   - each allowance opens its ports to its range of addresses, less those the policy already closes.
//
// A rule that lists ports leaves out every protocol it does not list, so ICMP (ping) and SCTP no longer leave the cluster
// once ports are closed.
//
// What a network policy cannot do is not attempted. It cannot detect mining (closing the ports pools listen on, which
// miningPools does for a curated list, is a heuristic; pkg/abuse is what looks for it), cannot count or rate-limit mail, and
// cannot name a destination by its host name.

const (
	// ConditionEgressLimitsApplied, on a project of an organization whose outbound traffic is limited, says whether the
	// limits are written into every environment's network policy and whether the cluster enforces them.
	ConditionEgressLimitsApplied = "EgressLimitsApplied"

	maxPort = 65535
)

// MailPorts are the TCP ports mail is sent and submitted on, which the limits close unless mail is Open: 25 (mail servers
// passing mail to each other, which is how spam goes straight to a mailbox's server), 465 and 587 (a mail program
// submitting mail, with TLS from the start or after STARTTLS), 2525 (the alternative most mail services offer where 587 is
// closed), and 2465 and 2587 (Amazon SES's alternatives to 465 and 587).
func MailPorts() []int32 { return []int32{25, 465, 587, 2465, 2525, 2587} }

// MiningPoolPorts are the TCP ports the limits close when miningPools is Blocked: the stratum ports that many public mining
// pools, and the example configurations of mining programs, use (3333 to 3336, 4444, 5555 and 7777), and further ports that
// published lists of pools name (14433, 14444 and 45700). Closing them is a heuristic, kept short on purpose: it makes a
// miner run with a pool's defaults fail, and does nothing against one that uses port 443, another port or a proxy. Ports
// with more chance of other uses (6666 is also IRC's, 9999 other software's) are left to blockedPorts.
func MiningPoolPorts() []geassv1alpha1.GeassEgressPortRange {
	single := func(port int32) geassv1alpha1.GeassEgressPortRange {
		return geassv1alpha1.GeassEgressPortRange{Port: port, Protocol: corev1.ProtocolTCP}
	}
	last := int32(3336)
	return []geassv1alpha1.GeassEgressPortRange{
		{Port: 3333, EndPort: &last, Protocol: corev1.ProtocolTCP}, single(4444), single(5555), single(7777),
		single(14433), single(14444), single(45700),
	}
}

// egressProtocols are the protocols the limits write ports for, in the order they are written.
var egressProtocols = []corev1.Protocol{corev1.ProtocolTCP, corev1.ProtocolUDP}

// neverAllowed are ranges an allowance may not name, because nothing in them is a destination to open: the link-local
// range, which holds the cloud's instance credentials service, and loopback.
var neverAllowed = []netip.Prefix{netip.MustParsePrefix(cloudMetadataCIDR), netip.MustParsePrefix("127.0.0.0/8")}

// EgressPlan is what an organization's outbound limits leave open, worked out once (see PlanEgress) for every namespace
// its pods run in. A nil plan limits nothing.
type EgressPlan struct {
	// open are the ports open to addresses outside the cluster, by protocol, as the fewest ranges in order.
	open map[corev1.Protocol][]portSpan
	// allowances are the ranges of addresses opened on ports of their own.
	allowances []egressAllowance
	// listed is set when only listed ports are open (allowedPorts), and summary says what is closed or open, for the
	// project's condition.
	listed  bool
	summary string
}

type portSpan struct{ first, last int32 }

type egressAllowance struct {
	prefix netip.Prefix
	ports  map[corev1.Protocol][]portSpan
}

// portSet holds, for one protocol, which of the ports 1 to 65535 are in it. Index 0 is never used.
type portSet [maxPort + 1]bool

func (s *portSet) fill(span portSpan, in bool) {
	for port := span.first; port <= span.last; port++ {
		s[port] = in
	}
}

// spans are the set's ports as the fewest ranges, in order.
func (s *portSet) spans() []portSpan {
	var out []portSpan
	for port := int32(1); port <= maxPort; port++ {
		if !s[port] {
			continue
		}
		first := port
		for port < maxPort && s[port+1] {
			port++
		}
		out = append(out, portSpan{first: first, last: port})
	}
	return out
}

func setOf(spans []portSpan) *portSet {
	set := &portSet{}
	for _, span := range spans {
		set.fill(span, true)
	}
	return set
}

func spanOf(r geassv1alpha1.GeassEgressPortRange) portSpan {
	last := r.Port
	if r.EndPort != nil {
		last = *r.EndPort
	}
	return portSpan{first: r.Port, last: last}
}

func protocolOf(r geassv1alpha1.GeassEgressPortRange) corev1.Protocol {
	if r.Protocol == "" {
		return corev1.ProtocolTCP
	}
	return r.Protocol
}

// checkPortRange says what Kubernetes would refuse in a range: a port outside 1 to 65535, an end below its start, a
// protocol other than TCP and UDP.
func checkPortRange(r geassv1alpha1.GeassEgressPortRange) error {
	switch {
	case r.Port < 1 || r.Port > maxPort:
		return fmt.Errorf("port %d is not between 1 and %d", r.Port, maxPort)
	case r.EndPort != nil && (*r.EndPort < r.Port || *r.EndPort > maxPort):
		return fmt.Errorf("endPort %d is not between port %d and %d", *r.EndPort, r.Port, maxPort)
	case r.Protocol != "" && r.Protocol != corev1.ProtocolTCP && r.Protocol != corev1.ProtocolUDP:
		return fmt.Errorf("protocol %q is not TCP or UDP", r.Protocol)
	}
	return nil
}

// allowancePrefix parses an allowance's range of addresses: IPv4, written as its first address, and not in a range that is
// never opened.
func allowancePrefix(cidr string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil || !prefix.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("%q is not a range of IPv4 addresses such as 203.0.113.25/32", cidr)
	}
	if prefix != prefix.Masked() {
		return netip.Prefix{}, fmt.Errorf("%q does not start at the first address of its range, which is %s", cidr, prefix.Masked())
	}
	for _, closed := range neverAllowed {
		if closed.Bits() <= prefix.Bits() && closed.Contains(prefix.Addr()) {
			return netip.Prefix{}, fmt.Errorf("%q is inside %s, which is never opened", cidr, closed)
		}
	}
	return prefix, nil
}

// spansByProtocol puts ranges together by protocol, as the fewest ranges in order.
func spansByProtocol(ranges []geassv1alpha1.GeassEgressPortRange) map[corev1.Protocol][]portSpan {
	sets := map[corev1.Protocol]*portSet{}
	for _, r := range ranges {
		protocol := protocolOf(r)
		if sets[protocol] == nil {
			sets[protocol] = &portSet{}
		}
		sets[protocol].fill(spanOf(r), true)
	}
	out := make(map[corev1.Protocol][]portSpan, len(sets))
	for protocol, set := range sets {
		out[protocol] = set.spans()
	}
	return out
}

// PlanEgress checks an organization's outbound limits and works out what they leave open. It returns nil for no limits,
// and an error, with every problem it found, for limits that cannot be applied as they are written: the CRD checks each
// field, but the platform's and an organization's are only put together here, and an IPv4 range is only parsed here.
func PlanEgress(egress *geassv1alpha1.GeassTenantEgress) (*EgressPlan, error) {
	if egress == nil {
		return nil, nil
	}
	var problems []string
	switch egress.Mail {
	case "", geassv1alpha1.EgressMailBlocked, geassv1alpha1.EgressMailOpen:
	default:
		problems = append(problems, fmt.Sprintf("mail is %q, not Blocked or Open", egress.Mail))
	}
	switch egress.MiningPools {
	case "", geassv1alpha1.EgressMiningPoolsBlocked, geassv1alpha1.EgressMiningPoolsOpen:
	default:
		problems = append(problems, fmt.Sprintf("miningPools is %q, not Blocked or Open", egress.MiningPools))
	}
	if egress.AllowedPorts != nil && len(egress.AllowedPorts) == 0 {
		problems = append(problems, "allowedPorts is an empty list, which would close every port: leave it out to open every port but the closed ones")
	}
	check := func(field string, ranges []geassv1alpha1.GeassEgressPortRange) {
		for i, r := range ranges {
			if err := checkPortRange(r); err != nil {
				problems = append(problems, fmt.Sprintf("%s[%d]: %v", field, i, err))
			}
		}
	}
	check("allowedPorts", egress.AllowedPorts)
	check("blockedPorts", egress.BlockedPorts)
	for i, allowance := range egress.Allow {
		field := fmt.Sprintf("allow[%d]", i)
		if _, err := allowancePrefix(allowance.CIDR); err != nil {
			problems = append(problems, field+".cidr: "+err.Error())
		}
		if len(allowance.Ports) == 0 {
			problems = append(problems, field+".ports is empty")
		}
		check(field+".ports", allowance.Ports)
	}
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}

	open := make(map[corev1.Protocol]*portSet, len(egressProtocols))
	for _, protocol := range egressProtocols {
		open[protocol] = &portSet{}
		if egress.AllowedPorts == nil {
			open[protocol].fill(portSpan{first: 1, last: maxPort}, true)
		}
	}
	for _, r := range egress.AllowedPorts {
		open[protocolOf(r)].fill(spanOf(r), true)
	}
	// What is closed wins over what is allowed.
	if egress.MailBlocked() {
		for _, port := range MailPorts() {
			open[corev1.ProtocolTCP][port] = false
		}
	}
	if egress.MiningPoolsBlocked() {
		for _, r := range MiningPoolPorts() {
			open[protocolOf(r)].fill(spanOf(r), false)
		}
	}
	for _, r := range egress.BlockedPorts {
		open[protocolOf(r)].fill(spanOf(r), false)
	}

	plan := &EgressPlan{open: make(map[corev1.Protocol][]portSpan, len(open)), listed: egress.AllowedPorts != nil}
	for protocol, set := range open {
		if spans := set.spans(); len(spans) > 0 {
			plan.open[protocol] = spans
		}
	}
	if len(plan.open) == 0 {
		return nil, errors.New("the limits leave no port open to addresses outside the cluster, which is not what they are for: open at least one, in allowedPorts or by closing fewer")
	}
	for _, allowance := range egress.Allow {
		prefix, _ := allowancePrefix(allowance.CIDR)
		plan.allowances = append(plan.allowances, egressAllowance{prefix: prefix, ports: spansByProtocol(allowance.Ports)})
	}
	plan.summary = plan.describe()
	return plan, nil
}

// ForEnvironment is the plan for a project environment, where the cluster's API port stays open to addresses outside the
// cluster whatever the limits say: the Postgres databases in a project's namespace use the API, which pods reach at the
// address and port the cluster's "kubernetes" Service sends them to, and that address can be anywhere (a hosted control
// plane is on none of the nodes). apiPort zero means the default.
func (p *EgressPlan) ForEnvironment(apiPort int32) *EgressPlan {
	if p == nil {
		return nil
	}
	if apiPort <= 0 || apiPort > maxPort {
		apiPort = DefaultAPIServerPort
	}
	out := *p
	out.open = maps.Clone(p.open)
	set := setOf(p.open[corev1.ProtocolTCP])
	set[apiPort] = true
	out.open[corev1.ProtocolTCP] = set.spans()
	out.summary = out.describe()
	return &out
}

// Summary says what the plan closes and opens, for a person.
func (p *EgressPlan) Summary() string {
	if p == nil {
		return ""
	}
	return p.summary
}

// closesNothing reports whether every TCP and UDP port is open, so that the plan changes nothing a policy says.
func (p *EgressPlan) closesNothing() bool {
	for _, protocol := range egressProtocols {
		spans := p.open[protocol]
		if len(spans) != 1 || spans[0] != (portSpan{first: 1, last: maxPort}) {
			return false
		}
	}
	return true
}

// policyPorts writes ports for a policy, TCP then UDP, each protocol's ranges in order. A protocol with every port open is
// one entry without a port, which needs no support for ranges.
func policyPorts(spans map[corev1.Protocol][]portSpan) []networkingv1.NetworkPolicyPort {
	var ports []networkingv1.NetworkPolicyPort
	for _, protocol := range egressProtocols {
		for _, span := range spans[protocol] {
			// Each entry has its own copy, so that no two share what they point to.
			entryProtocol := protocol
			if span == (portSpan{first: 1, last: maxPort}) {
				ports = append(ports, networkingv1.NetworkPolicyPort{Protocol: &entryProtocol})
				continue
			}
			port := intstr.FromInt32(span.first)
			entry := networkingv1.NetworkPolicyPort{Protocol: &entryProtocol, Port: &port}
			if span.last > span.first {
				end := span.last
				entry.EndPort = &end
			}
			ports = append(ports, entry)
		}
	}
	return ports
}

// rules are the egress rules for the outside world under the plan, for a policy that closes the addresses in except: the
// ports left open to everything else, and each allowance, less what except closes. A rule without ports would open every
// port, so none is written without them.
func (p *EgressPlan) rules(except []string) []networkingv1.NetworkPolicyEgressRule {
	var rules []networkingv1.NetworkPolicyEgressRule
	if ports := policyPorts(p.open); len(ports) > 0 {
		rules = append(rules, networkingv1.NetworkPolicyEgressRule{
			To:    []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: anywhereCIDR, Except: except}}},
			Ports: ports,
		})
	}
	for _, allowance := range p.allowances {
		block, ok := clippedBlock(allowance.prefix, except)
		ports := policyPorts(allowance.ports)
		if !ok || len(ports) == 0 {
			continue
		}
		rules = append(rules, networkingv1.NetworkPolicyEgressRule{To: []networkingv1.NetworkPolicyPeer{{IPBlock: block}}, Ports: ports})
	}
	return rules
}

// clippedBlock is an allowance's range less the ranges a policy closes. An allowance that lies wholly inside a closed range
// opens nothing, and is left out. Ranges either nest or do not meet, so a closed range is either inside the allowance,
// and becomes one of its exceptions, or around it, or apart from it.
func clippedBlock(prefix netip.Prefix, except []string) (*networkingv1.IPBlock, bool) {
	var inside []string
	for _, value := range except {
		closed, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, false
		}
		closed = closed.Masked()
		switch {
		case closed.Bits() <= prefix.Bits() && closed.Contains(prefix.Addr()):
			return nil, false
		case prefix.Bits() < closed.Bits() && prefix.Contains(closed.Addr()):
			inside = append(inside, value)
		}
	}
	return &networkingv1.IPBlock{CIDR: prefix.String(), Except: inside}, true
}

// describe says what the plan closes, or what it leaves open when only listed ports are, and what it opens to whom.
func (p *EgressPlan) describe() string {
	var out string
	if p.listed {
		out = "only " + spanList(p.open) + " open to addresses outside the cluster"
	} else {
		closed := map[corev1.Protocol][]portSpan{}
		for _, protocol := range egressProtocols {
			set := setOf(p.open[protocol])
			for port := 1; port <= maxPort; port++ {
				set[port] = !set[port]
			}
			if spans := set.spans(); len(spans) > 0 {
				closed[protocol] = spans
			}
		}
		if len(closed) == 0 {
			out = "no port closed to addresses outside the cluster"
		} else {
			out = spanList(closed) + " closed to addresses outside the cluster"
		}
	}
	switch n := len(p.allowances); n {
	case 0:
	case 1:
		out += ", and opened on some ports to " + p.allowances[0].prefix.String()
	default:
		out += fmt.Sprintf(", and opened on some ports to %d ranges of addresses", n)
	}
	return out
}

// spanList writes ranges as "TCP 25, 465, 3333-3334 and UDP 443", with no more than a few of them.
func spanList(spans map[corev1.Protocol][]portSpan) string {
	const most = 10
	var groups []string
	shown, total := 0, 0
	for _, protocol := range egressProtocols {
		total += len(spans[protocol])
		var items []string
		for _, span := range spans[protocol] {
			if shown == most {
				break
			}
			shown++
			if span.first == span.last {
				items = append(items, strconv.Itoa(int(span.first)))
			} else {
				items = append(items, fmt.Sprintf("%d-%d", span.first, span.last))
			}
		}
		if len(items) > 0 {
			groups = append(groups, string(protocol)+" "+strings.Join(items, ", "))
		}
	}
	out := strings.Join(groups, " and ")
	if total > shown {
		out += fmt.Sprintf(" and %d more", total-shown)
	}
	return out
}

// PlatformTenantEgress is the platform's outbound limits for every organization, or nil when it sets none.
func PlatformTenantEgress(ctx context.Context, c client.Reader) (*geassv1alpha1.GeassTenantEgress, error) {
	var config geassv1alpha1.GeassPlatformConfig
	if err := c.Get(ctx, client.ObjectKey{Name: HAReadinessName, Namespace: SystemNamespace}, &config); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if config.Spec.Tenancy == nil {
		return nil, nil
	}
	return config.Spec.Tenancy.Egress, nil
}

// EffectiveTenantEgress is what an organization is held to: the platform's limits, with each field the organization sets
// in their place. It returns nil when neither sets any.
func EffectiveTenantEgress(defaults, own *geassv1alpha1.GeassTenantEgress) *geassv1alpha1.GeassTenantEgress {
	if defaults == nil && own == nil {
		return nil
	}
	out := &geassv1alpha1.GeassTenantEgress{}
	if defaults != nil {
		out = defaults.DeepCopy()
	}
	if own != nil {
		own = own.DeepCopy()
		if own.Mail != "" {
			out.Mail = own.Mail
		}
		if own.AllowedPorts != nil {
			out.AllowedPorts = own.AllowedPorts
		}
		if own.BlockedPorts != nil {
			out.BlockedPorts = own.BlockedPorts
		}
		if own.Allow != nil {
			out.Allow = own.Allow
		}
		if own.MiningPools != "" {
			out.MiningPools = own.MiningPools
		}
	}
	return out
}

// TenantEgressOf is the outbound limits of an organization, or nil for no organization or one that is not limited.
func TenantEgressOf(ctx context.Context, c client.Reader, org *geassv1alpha1.GeassOrganization) (*geassv1alpha1.GeassTenantEgress, error) {
	if org == nil {
		return nil, nil
	}
	defaults, err := PlatformTenantEgress(ctx, c)
	if err != nil {
		return nil, err
	}
	return EffectiveTenantEgress(defaults, org.Spec.Egress), nil
}
