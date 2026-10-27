package platform

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// A project that belongs to an organization builds in a namespace of the organization's own. A build runs what the
// repository says in its Dockerfile, as root, with the credential it pushes its image with mounted: in the platform's
// namespace that is the platform's credential, beside everything else the operator keeps there. In the organization's it
// is the organization's credential, a copy of the git token of the one project, and nothing else, behind a policy that
// lets the build reach the internet and nothing inside the cluster.

const (
	// LabelOrgBuild marks a namespace that is an organization's build namespace, and names the organization. Nothing
	// else is a project's: no project may take it for an environment.
	LabelOrgBuild = "geass.dev/org-build"
	// LabelBuild, on a build's Job and the Secrets copied for it, names the GeassBuild they are for; LabelBuildNamespace
	// names the namespace the build is in, which is not the Job's.
	LabelBuild          = "geass.dev/build"
	LabelBuildNamespace = "geass.dev/build-namespace"

	// OrgBuildPolicyName is the policy that closes the build namespace, and OrgBuildQuotaName the quota that bounds it.
	OrgBuildPolicyName = "geass-build-isolation"
	OrgBuildQuotaName  = "geass-build-quota"
	// MaxOrgBuildPods is how many build pods an organization's build namespace may hold at once, retries and finished pods
	// that have not been cleaned up included.
	MaxOrgBuildPods = 20
)

// privateRanges are the addresses a build may not reach: the cluster's own networks, the link-local range that holds the
// cloud's instance credentials, and loopback. What is left is the internet.
var privateRanges = []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16"}

// OrgBuildNamespace is the namespace an organization's builds run in.
func OrgBuildNamespace(org string) string { return "geass-build-" + org }

// OrgRegistrySecretName is the Secret in the system namespace that holds the login an organization saved for its registry.
func OrgRegistrySecretName(org string) string { return "org-registry-" + org }

// OrgBuildNamespaceLabels are what the namespace carries: that Geass made it, and whose it is.
func OrgBuildNamespaceLabels(org string) map[string]string {
	return map[string]string{LabelManagedBy: ManagedByValue, LabelOrgBuild: org}
}

// OrgBuildPolicy closes an organization's build namespace. Nothing may connect to a build pod. A build pod may look up
// names in the cluster's DNS and may reach any address that is not a private one, so that it can fetch its source and push
// its image, and cannot reach another project's pods, the platform's services, the cluster's API or the cloud's metadata.
// The addresses the nodes are on are left out too when they are known.
func OrgBuildPolicy(namespace, org string, nodes *NodeNetwork) *networkingv1.NetworkPolicy {
	return TenantOrgBuildPolicy(namespace, org, nodes, nil)
}

// TenantOrgBuildPolicy is OrgBuildPolicy with the organization's outbound limits (see PlanEgress) in its rule for the
// internet: a build runs the organization's Dockerfile, which can send mail or mine as well as a service can. Without
// limits, or with limits that close nothing, it is OrgBuildPolicy exactly. The cluster's DNS stays open, and the private
// ranges closed, whatever the limits say.
func TenantOrgBuildPolicy(namespace, org string, nodes *NodeNetwork, limits *EgressPlan) *networkingv1.NetworkPolicy {
	except := append([]string{}, privateRanges...)
	if nodes != nil {
		except = append(except, nodes.Blocked...)
	}
	dns := networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"}},
		}},
		Ports: []networkingv1.NetworkPolicyPort{dnsPort(corev1.ProtocolUDP), dnsPort(corev1.ProtocolTCP)},
	}
	egress := []networkingv1.NetworkPolicyEgressRule{dns}
	if limits == nil || limits.closesNothing() {
		egress = append(egress, networkingv1.NetworkPolicyEgressRule{To: []networkingv1.NetworkPolicyPeer{
			{IPBlock: &networkingv1.IPBlock{CIDR: anywhereCIDR, Except: except}},
		}})
	} else {
		egress = append(egress, limits.rules(except)...)
	}
	return &networkingv1.NetworkPolicy{
		ObjectMeta: policyMeta(OrgBuildPolicyName, namespace, map[string]string{LabelOrgBuild: org}),
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Egress:      egress,
		},
	}
}

func dnsPort(protocol corev1.Protocol) networkingv1.NetworkPolicyPort {
	port := intstr.FromInt32(53)
	return networkingv1.NetworkPolicyPort{Protocol: &protocol, Port: &port}
}

// OrgBuildQuota bounds the build namespace, so that an organization's builds cannot fill the cluster with pods.
func OrgBuildQuota(namespace, org string) *corev1.ResourceQuota {
	return &corev1.ResourceQuota{
		ObjectMeta: policyMeta(OrgBuildQuotaName, namespace, map[string]string{LabelOrgBuild: org}),
		Spec: corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{
			corev1.ResourcePods: *resource.NewQuantity(MaxOrgBuildPods, resource.DecimalSI),
		}},
	}
}

// ValidOrgBuildNamespace is whether a namespace name is one an organization's builds could be given: it has to fit in the
// 63 characters a namespace name may have.
func ValidOrgBuildNamespace(org string) error {
	if name := OrgBuildNamespace(org); len(name) > 63 {
		return fmt.Errorf("the build namespace %q is too long", name)
	}
	return nil
}
