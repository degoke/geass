package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// These run against a real API server (envtest), which the fake client is not: they show that the CRDs refuse what they
// should, keep what they are given, and that the policies the limits write are ones Kubernetes accepts and stores as they
// were written, so that Geass does not write them again on every reconcile.
var _ = Describe("Outbound limits for organizations", func() {
	ctx := context.Background()

	refused := map[string]map[string]any{
		"a port of zero":                     {"blockedPorts": []any{map[string]any{"port": int64(0)}}},
		"a port above 65535":                 {"allowedPorts": []any{map[string]any{"port": int64(65536)}}},
		"a range that ends before it starts": {"blockedPorts": []any{map[string]any{"port": int64(4000), "endPort": int64(3000)}}},
		"a protocol other than TCP and UDP":  {"blockedPorts": []any{map[string]any{"port": int64(9), "protocol": "SCTP"}}},
		"an empty list of allowed ports":     {"allowedPorts": []any{}},
		"a host name for a range":            {"allow": []any{map[string]any{"cidr": "relay.example.com", "ports": []any{map[string]any{"port": int64(25)}}}}},
		"an allowance without ports":         {"allow": []any{map[string]any{"cidr": "203.0.113.25/32"}}},
		"mail that is neither":               {"mail": "Relayed"},
	}

	It("refuses limits the CRDs can tell are wrong, on the platform and on an organization", func() {
		// The same object with limits that are right is taken, so a refusal below is the limits' doing.
		accepted := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "geass.geass.dev/v1alpha1", "kind": "GeassPlatformConfig",
			"metadata": map[string]any{"name": "egress-accepted", "namespace": platform.SystemNamespace},
			"spec": map[string]any{"tenancy": map[string]any{"egress": map[string]any{
				"mail":         "Blocked",
				"blockedPorts": []any{map[string]any{"port": int64(3333), "endPort": int64(3336)}, map[string]any{"port": int64(3478), "protocol": "UDP"}},
				"allow":        []any{map[string]any{"cidr": "203.0.113.25/32", "ports": []any{map[string]any{"port": int64(587)}}}},
			}}},
		}}
		Expect(k8sClient.Create(ctx, accepted)).To(Succeed())
		Expect(k8sClient.Delete(ctx, accepted)).To(Succeed())

		i := 0
		for name, egress := range refused {
			i++
			config := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "geass.geass.dev/v1alpha1", "kind": "GeassPlatformConfig",
				"metadata": map[string]any{"name": fmt.Sprintf("egress-refused-%d", i), "namespace": platform.SystemNamespace},
				"spec":     map[string]any{"tenancy": map[string]any{"egress": egress}},
			}}
			Expect(k8sClient.Create(ctx, config)).NotTo(Succeed(), "platform: %s", name)
			org := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "geass.geass.dev/v1alpha1", "kind": "GeassOrganization",
				"metadata": map[string]any{"name": fmt.Sprintf("org-egress-refused-%d", i), "namespace": platform.SystemNamespace},
				"spec":     map[string]any{"displayName": "Refused", "idpID": fmt.Sprintf("refused-%d", i), "egress": egress},
			}}
			Expect(k8sClient.Create(ctx, org)).NotTo(Succeed(), "organization: %s", name)
		}
	})

	It("keeps an organization's limits field for field", func() {
		org := &geassv1alpha1.GeassOrganization{
			ObjectMeta: metav1.ObjectMeta{Name: "org-egress-kept", Namespace: platform.SystemNamespace},
			Spec: geassv1alpha1.GeassOrganizationSpec{DisplayName: "Kept", IdPID: "kept", Egress: &geassv1alpha1.GeassTenantEgress{
				Mail:         geassv1alpha1.EgressMailOpen,
				AllowedPorts: []geassv1alpha1.GeassEgressPortRange{{Port: 443}, {Port: 443, Protocol: corev1.ProtocolUDP}, {Port: 8000, EndPort: utilptr.To(int32(8100))}},
				BlockedPorts: []geassv1alpha1.GeassEgressPortRange{{Port: 8080}},
				Allow:        []geassv1alpha1.GeassEgressAllowance{{CIDR: "203.0.113.0/24", Ports: []geassv1alpha1.GeassEgressPortRange{{Port: 25}}, Description: "the relay"}},
			}},
		}
		Expect(k8sClient.Create(ctx, org)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, org))).To(Succeed()) })
		var stored geassv1alpha1.GeassOrganization
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(org), &stored)).To(Succeed())
		Expect(stored.Spec.Egress).To(Equal(org.Spec.Egress))
	})

	It("writes policies the API server accepts and stores as they were written", func() {
		limits := &geassv1alpha1.GeassTenantEgress{
			BlockedPorts: []geassv1alpha1.GeassEgressPortRange{{Port: 3333, EndPort: utilptr.To(int32(3336))}, {Port: 4444}, {Port: 14444}, {Port: 3478, Protocol: corev1.ProtocolUDP}},
			Allow: []geassv1alpha1.GeassEgressAllowance{
				{CIDR: "0.0.0.0/0", Ports: []geassv1alpha1.GeassEgressPortRange{{Port: 25}}},
				{CIDR: "10.0.0.0/8", Ports: []geassv1alpha1.GeassEgressPortRange{{Port: 587}, {Port: 2525, EndPort: utilptr.To(int32(2526))}}},
			},
		}
		listed := &geassv1alpha1.GeassTenantEgress{AllowedPorts: []geassv1alpha1.GeassEgressPortRange{{Port: 80}, {Port: 443}, {Port: 443, Protocol: corev1.ProtocolUDP}}}
		nodes := platform.NodeNetworkOf([]corev1.Node{egressNode}, 6443)
		blockedPlan, err := platform.PlanEgress(limits)
		Expect(err).NotTo(HaveOccurred())
		listedPlan, err := platform.PlanEgress(listed)
		Expect(err).NotTo(HaveOccurred())

		policies := []*networkingv1.NetworkPolicy{
			platform.TenantCloudMetadataPolicy("egress-envtest", "egress-envtest-production", nodes, blockedPlan.ForEnvironment(6443)),
			platform.TenantCloudMetadataPolicy("egress-envtest", "egress-envtest-staging", nil, listedPlan.ForEnvironment(6443)),
			platform.TenantOrgBuildPolicy(platform.OrgBuildNamespace("org-egress-envtest"), "org-egress-envtest", nil, blockedPlan),
		}
		for _, desired := range policies {
			Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: desired.Namespace}})).To(Succeed())
			Expect(k8sClient.Create(ctx, desired.DeepCopy())).To(Succeed(), desired.Namespace)
			var stored networkingv1.NetworkPolicy
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(desired), &stored)).To(Succeed())
			Expect(stored.Spec).To(Equal(desired.Spec), "%s is stored as it was written", desired.Namespace)

			// Applying it again is no change.
			Expect(applyNetworkPolicy(ctx, k8sClient, desired)).To(Succeed())
			var again networkingv1.NetworkPolicy
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(desired), &again)).To(Succeed())
			Expect(again.ResourceVersion).To(Equal(stored.ResourceVersion), "%s is not written again", desired.Namespace)
		}
	})
})
