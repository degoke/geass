package controller

import (
	"context"
	"maps"
	"net/netip"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/mesh"
	"github.com/degoke/geass/pkg/platform"
)

// These run against a real API server (envtest): the CRD refuses a mesh that cannot work and fills in what is left out,
// a cluster that sets no mesh is stored as it was given, and the MeshReady condition is written to a real status.
var _ = Describe("The mesh of a cluster", func() {
	ctx := context.Background()

	cluster := func(name string, network map[string]any) *unstructured.Unstructured {
		spec := map[string]any{
			"version": "v1", "serverURL": "https://203.0.113.10:6443",
			"tokenSecretRef": map[string]any{"name": "geass-token", "namespace": platform.SystemNamespace},
		}
		if network != nil {
			spec["network"] = network
		}
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "geass.geass.dev/v1alpha1", "kind": "GeassCluster",
			"metadata": map[string]any{"name": name, "namespace": platform.SystemNamespace},
			"spec":     spec,
		}}
	}
	remove := func(name string) {
		_ = k8sClient.Delete(ctx, &geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace}})
	}
	tailscale := func(change map[string]any) map[string]any {
		settings := map[string]any{"authKeySecretRef": "geass-mesh-auth"}
		maps.Copy(settings, change)
		return map[string]any{"mesh": map[string]any{"provider": "Tailscale", "tailscale": settings}}
	}

	It("defaults the provider to WireGuard and accepts both providers", func() {
		defer remove("mesh-default")
		Expect(k8sClient.Create(ctx, cluster("mesh-default", map[string]any{"mesh": map[string]any{}}))).To(Succeed())
		stored := &geassv1alpha1.GeassCluster{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "mesh-default", Namespace: platform.SystemNamespace}, stored)).To(Succeed())
		Expect(stored.Spec.Network.Mesh).NotTo(BeNil())
		Expect(stored.Spec.Network.Mesh.Provider).To(Equal(geassv1alpha1.MeshProviderWireGuard))
		Expect(stored.Spec.Network.Mesh.Tailscale).To(BeNil())

		for i, network := range []map[string]any{
			tailscale(nil),
			tailscale(map[string]any{"controlServerURL": "https://headscale.example.com"}),
			tailscale(map[string]any{"controlServerURL": "https://headscale.example.com:8443/some/path"}),
			{"mesh": map[string]any{"provider": "WireGuard"}, "multiSite": true},
		} {
			name := "mesh-accepted-" + string(rune('a'+i))
			Expect(k8sClient.Create(ctx, cluster(name, network))).To(Succeed(), name)
			remove(name)
		}
	})

	It("refuses a mesh that cannot work", func() {
		refused := map[string]map[string]any{
			"Tailscale without its settings":          {"mesh": map[string]any{"provider": "Tailscale"}},
			"Tailscale settings on WireGuard":         {"mesh": map[string]any{"provider": "WireGuard", "tailscale": map[string]any{"authKeySecretRef": "geass-mesh-auth"}}},
			"Tailscale settings with the default":     {"mesh": map[string]any{"tailscale": map[string]any{"authKeySecretRef": "geass-mesh-auth"}}},
			"a provider that is not one":              {"mesh": map[string]any{"provider": "ZeroTier"}},
			"no Secret named":                         tailscale(map[string]any{"authKeySecretRef": ""}),
			"a Secret name that is not a name":        tailscale(map[string]any{"authKeySecretRef": "Not A Name"}),
			"a control server over plain http":        tailscale(map[string]any{"controlServerURL": "http://headscale.example.com"}),
			"a control server with a user":            tailscale(map[string]any{"controlServerURL": "https://user@headscale.example.com"}),
			"a control server with a comma":           tailscale(map[string]any{"controlServerURL": "https://headscale.example.com/a,extraArgs=--x"}),
			"a control server with a query":           tailscale(map[string]any{"controlServerURL": "https://headscale.example.com/?a=b"}),
			"a control server with a space":           tailscale(map[string]any{"controlServerURL": "https://headscale.example.com/a b"}),
			"a control server that is too long":       tailscale(map[string]any{"controlServerURL": "https://" + strings.Repeat("a", 260) + ".example.com"}),
			"a control server that is not an address": tailscale(map[string]any{"controlServerURL": "headscale.example.com"}),
		}
		i := 0
		for name, network := range refused {
			i++
			object := cluster("mesh-refused", network)
			err := k8sClient.Create(ctx, object)
			Expect(err).To(HaveOccurred(), name)
			remove("mesh-refused")
			_ = i
		}
	})

	It("stores a cluster that sets no mesh as it was given", func() {
		defer remove("mesh-none")
		defer remove("mesh-multisite")
		Expect(k8sClient.Create(ctx, cluster("mesh-none", nil))).To(Succeed())
		Expect(k8sClient.Create(ctx, cluster("mesh-multisite", map[string]any{"multiSite": true}))).To(Succeed())

		for name, multiSite := range map[string]bool{"mesh-none": false, "mesh-multisite": true} {
			stored := &geassv1alpha1.GeassCluster{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, stored)).To(Succeed())
			Expect(stored.Spec.Network.Mesh).To(BeNil(), name)
			Expect(stored.Spec.Network.MultiSite).To(Equal(multiSite), name)
			Expect(mesh.From(stored.Spec.Network).Checked).To(BeFalse(), "the new checks are not turned on by multiSite")
		}
	})

	Describe("MeshReady", func() {
		var nodes []*corev1.Node

		node := func(name string, control bool, annotations map[string]string, addresses ...corev1.NodeAddress) {
			object := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: annotations}}
			if control {
				object.Labels = map[string]string{controlPlaneLabel: "true"}
			}
			Expect(k8sClient.Create(ctx, object)).To(Succeed())
			object.Status.Addresses = addresses
			Expect(k8sClient.Status().Update(ctx, object)).To(Succeed())
			nodes = append(nodes, object)
		}
		AfterEach(func() {
			for _, object := range nodes {
				_ = k8sClient.Delete(ctx, object)
			}
			nodes = nil
		})
		reconcile := func(name string, network map[string]any) *metav1.Condition {
			Expect(k8sClient.Create(ctx, cluster(name, network))).To(Succeed())
			key := client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}
			DeferCleanup(func() { remove(name) })
			current := &geassv1alpha1.GeassCluster{}
			Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
			reconciler := &GeassClusterReconciler{Client: k8sClient, Scheme: scheme.Scheme}
			reconciler.reconcileMesh(ctx, current)
			stored := &geassv1alpha1.GeassCluster{}
			Expect(k8sClient.Get(ctx, key, stored)).To(Succeed())
			return meta.FindStatusCondition(stored.Status.Conditions, mesh.ConditionMeshReady)
		}

		It("is written to the status, naming the nodes that are not on the mesh", func() {
			node("mesh-env-cp", true, map[string]string{mesh.AnnotationFlannelBackendType: "wireguard"},
				corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: "203.0.113.10"})
			node("mesh-env-hand-joined", false, nil, corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.5.0.5"})

			condition := reconcile("mesh-env-wireguard", map[string]any{"mesh": map[string]any{}})

			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(meshReasonAttended))
			Expect(condition.Message).To(ContainSubstring("mesh-env-hand-joined"))
			Expect(condition.Message).NotTo(ContainSubstring("mesh-env-cp"))
		})

		It("says a control plane that was built without the mesh is blocking", func() {
			node("mesh-env-cp-vxlan", true, map[string]string{mesh.AnnotationFlannelBackendType: "vxlan"},
				corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.0.5"})

			condition := reconcile("mesh-env-blocked", map[string]any{"mesh": map[string]any{}})

			Expect(condition).NotTo(BeNil())
			Expect(condition.Reason).To(Equal(meshReasonBlocked))
			Expect(condition.Message).To(ContainSubstring("mesh-env-cp-vxlan"))
		})

		// The overlay check (mesh_overlay.go) with a stand-in for the connection: the nodes and the condition are real API
		// objects, and the answer from each node is made up by the test.
		It("says which nodes the operator's probe got no answer from, once it has got none twice", func() {
			ready := []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now().Add(-time.Hour))}}
			withRange := func(name, cidr string, control bool, annotations map[string]string, address string) {
				object := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: annotations}, Spec: corev1.NodeSpec{PodCIDR: cidr}}
				if control {
					object.Labels = map[string]string{controlPlaneLabel: "true"}
				}
				Expect(k8sClient.Create(ctx, object)).To(Succeed())
				object.Status.Addresses = []corev1.NodeAddress{{Type: corev1.NodeExternalIP, Address: address}}
				object.Status.Conditions = ready
				Expect(k8sClient.Status().Update(ctx, object)).To(Succeed())
				nodes = append(nodes, object)
			}
			withRange("mesh-env-probe-cp", "10.42.0.0/24", true, map[string]string{mesh.AnnotationFlannelBackendType: "wireguard"}, "203.0.113.10")
			withRange("mesh-env-probe-w1", "10.42.1.0/24", false, nil, "198.51.100.7")
			withRange("mesh-env-probe-w2", "10.42.2.0/24", false, nil, "198.51.100.8")

			Expect(k8sClient.Create(ctx, cluster("mesh-env-probe", map[string]any{"mesh": map[string]any{}}))).To(Succeed())
			DeferCleanup(func() { remove("mesh-env-probe") })
			key := client.ObjectKey{Name: "mesh-env-probe", Namespace: platform.SystemNamespace}

			prober := newFakeOverlayProber()
			prober.set("mesh-env-probe-w1", timedOut)
			now := time.Now()
			reconciler := &GeassClusterReconciler{
				Client: k8sClient, Scheme: scheme.Scheme, overlayProber: prober,
				overlayLocal: func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("10.42.0.15")} },
				overlayNow:   func() time.Time { return now },
			}
			pass := func() *metav1.Condition {
				current := &geassv1alpha1.GeassCluster{}
				Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
				reconciler.reconcileMesh(ctx, current)
				stored := &geassv1alpha1.GeassCluster{}
				Expect(k8sClient.Get(ctx, key, stored)).To(Succeed())
				Expect(stored.Status.Conditions).To(HaveLen(1), "only MeshReady is written: nothing of the cluster's Ready is touched")
				return meta.FindStatusCondition(stored.Status.Conditions, mesh.ConditionMeshReady)
			}

			first := pass()
			Expect(first.Status).To(Equal(metav1.ConditionTrue))
			Expect(first.Message).To(ContainSubstring("1 of 2 other nodes (1 not answered yet"))

			now = now.Add(meshOverlayFirstRetry)
			second := pass()
			Expect(second.Status).To(Equal(metav1.ConditionFalse))
			Expect(second.Reason).To(Equal(meshReasonOverlay))
			Expect(second.Message).To(ContainSubstring("mesh-env-probe-w1 (timed out)"))
			Expect(second.Message).NotTo(ContainSubstring("mesh-env-probe-w2"))
			Expect(second.Message).To(ContainSubstring("one probe from one point"))
		})

		It("is not written for a cluster without a mesh", func() {
			node("mesh-env-cp-plain", true, nil, corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.0.5"})

			Expect(reconcile("mesh-env-none", map[string]any{"multiSite": true})).To(BeNil())
		})
	})
})
