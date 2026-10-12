package platform

import (
	"maps"
	"slices"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// Without network policies every pod can reach every other pod in the cluster, so one project's
// service could call another project's database, read its metrics, or read its logs. The policies
// built here close that, while keeping open everything the platform itself needs.
//
// Three sets exist:
//
//   - Each project environment namespace limits who can reach its pods (ProjectIsolationPolicy).
//   - Each project environment namespace stops its pods reading the cloud provider's instance
//     credentials service (CloudMetadataPolicy).
//   - The metrics and log stores accept only the platform (MetricsStorePolicy, LogStorePolicy). They
//     have no login of their own and between them hold every project's data.

const (
	ProjectIsolationPolicyName = "geass-project-isolation"
	CloudMetadataPolicyName    = "geass-block-cloud-metadata"
	LogStorePolicyName         = "geass-restrict-log-store"
	MetricsStorePolicyName     = "geass-restrict-metrics-store"

	// LabelNamespaceName is set by Kubernetes on every namespace, so a namespace can be selected by its name.
	LabelNamespaceName = "kubernetes.io/metadata.name"

	// cloudMetadataCIDR is the link-local range that holds the instance credentials service of AWS,
	// GCP, Azure, OVH and most others (169.254.169.254).
	cloudMetadataCIDR = "169.254.0.0/16"
	anywhereCIDR      = "0.0.0.0/0"
)

// platformNamespacesReachingProjects are the namespaces whose pods may always call a project's
// services. Nobody who deploys projects can run pods in them.
//   - kube-system: the ingress controller, which forwards public traffic to services.
//   - geass-system: the operator and dashboard (and the Cloudflare tunnel, which reaches services by name).
//   - monitoring: Prometheus scraping service metrics.
//   - cnpg-system: the Postgres operator, which talks to the instance manager inside every database pod.
//   - keda: the scale-to-zero add-on's proxy, which passes requests on to services it has woken.
func platformNamespacesReachingProjects() []string {
	return []string{"kube-system", SystemNamespace, MonitoringTargetNS, CNPGTargetNS, ServerlessTargetNS}
}

// ManagedNetworkPolicyNames lists the policies the operator owns, so a watch can ignore the rest.
func ManagedNetworkPolicyNames() []string {
	return []string{ProjectIsolationPolicyName, CloudMetadataPolicyName, LogStorePolicyName, MetricsStorePolicyName}
}

// AllowedProjects returns the projects whose services may reach the given project's services: the
// project itself first, then the others it opted in, sorted and without repeats or blanks.
func AllowedProjects(project *geassv1alpha1.GeassProject) []string {
	others := make([]string, 0)
	if project.Spec.Network != nil {
		for _, name := range project.Spec.Network.AllowFromProjects {
			name = strings.TrimSpace(name)
			if name != "" && name != project.Name {
				others = append(others, name)
			}
		}
	}
	slices.Sort(others)
	return append([]string{project.Name}, slices.Compact(others)...)
}

func namespaceNamed(name string) networkingv1.NetworkPolicyPeer {
	return networkingv1.NetworkPolicyPeer{NamespaceSelector: &metav1.LabelSelector{
		MatchLabels: map[string]string{LabelNamespaceName: name},
	}}
}

func policyMeta(name, namespace string, extra map[string]string) metav1.ObjectMeta {
	labels := map[string]string{LabelManagedBy: ManagedByValue}
	maps.Copy(labels, extra)
	return metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}
}

// ProjectIsolationPolicy lets only the project's own namespaces (every environment, including pull
// request environments), the projects it opted in, and the platform namespaces reach the pods of one
// environment. Anything else, such as another project's pods, is dropped.
//
// Replies to connections the project's own pods start are never affected, because connection tracking
// lets them back in.
func ProjectIsolationPolicy(project *geassv1alpha1.GeassProject, namespace string) *networkingv1.NetworkPolicy {
	reaching := platformNamespacesReachingProjects()
	peers := make([]networkingv1.NetworkPolicyPeer, 0, 1+len(reaching))
	peers = append(peers, networkingv1.NetworkPolicyPeer{
		NamespaceSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
			Key:      LabelProject,
			Operator: metav1.LabelSelectorOpIn,
			Values:   AllowedProjects(project),
		}}},
	})
	for _, name := range reaching {
		peers = append(peers, namespaceNamed(name))
	}
	return &networkingv1.NetworkPolicy{
		ObjectMeta: policyMeta(ProjectIsolationPolicyName, namespace, map[string]string{LabelProject: project.Name}),
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress:     []networkingv1.NetworkPolicyIngressRule{{From: peers}},
		},
	}
}

// CloudMetadataPolicy lets a project's pods reach every pod and every address except the cloud
// provider's instance credentials service. Pods can otherwise read the credentials of the machine they
// run on, and with them whatever that machine's role may do. It is on whether or not a project is
// isolated.
//
// Listing every namespace's pods as well as the address range is deliberate: an egress policy that
// named only addresses would cut the pods off from cluster DNS and from other services, because
// those are pod addresses and not every network plugin counts pod addresses as part of 0.0.0.0/0.
func CloudMetadataPolicy(project, namespace string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: policyMeta(CloudMetadataPolicyName, namespace, map[string]string{LabelProject: project}),
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{{To: []networkingv1.NetworkPolicyPeer{
				{NamespaceSelector: &metav1.LabelSelector{}},
				{IPBlock: &networkingv1.IPBlock{CIDR: anywhereCIDR, Except: []string{cloudMetadataCIDR}}},
			}}},
		},
	}
}

// LogStorePolicy limits who can read the log store, which has no login and holds every project's logs.
// Only the platform (the dashboard, and the Prometheus that watches it) and the store's own namespace
// (the shipper that writes to it) can reach it.
func LogStorePolicy() *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: policyMeta(LogStorePolicyName, LoggingTargetNS, nil),
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{
				{PodSelector: &metav1.LabelSelector{}},
				namespaceNamed(SystemNamespace),
				namespaceNamed(MonitoringTargetNS),
			}}},
		},
	}
}

// MetricsStorePolicy limits who can query Prometheus, which has no login and holds every project's
// metrics. It selects only the Prometheus pods, because the namespace also holds the operator's
// admission webhook, which the cluster's API server calls from outside the pod network.
func MetricsStorePolicy() *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: policyMeta(MetricsStorePolicyName, MonitoringTargetNS, nil),
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "prometheus"}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{
				{PodSelector: &metav1.LabelSelector{}},
				namespaceNamed(SystemNamespace),
			}}},
		},
	}
}
