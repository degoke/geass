package mesh

import (
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// maxNamed is how many node names a message lists before it says "and N more".
const maxNamed = 5

// Report is what is wrong with a running mesh.
type Report struct {
	// Blocking is why a worker must not be joined now: it would register and be unable to reach the other nodes. It is
	// a property of the control plane, which workers cannot change.
	Blocking []string
	// Warnings are what the person should look at and Geass cannot fix, and that do not stop a join.
	Warnings []string
}

// OK is whether there is nothing to say.
func (r Report) OK() bool { return len(r.Blocking) == 0 && len(r.Warnings) == 0 }

// Message is the report as one line of a condition: what blocks first, then what to look at.
func (r Report) Message() string {
	return strings.Join(append(slices.Clone(r.Blocking), r.Warnings...), "; ")
}

// Check says what is wrong with the mesh of a cluster, from what the nodes themselves report: the addresses they
// advertise and the overlay flannel runs on the control plane. It reads only what Kubernetes already knows, and so
// cannot tell whether traffic flows between two sites: a node that advertises the right address behind a closed UDP port
// is still reported as fine. The verification steps in the documentation are for that.
//
// controlPlane is the nodes that run the control plane, which may be none when it is hosted outside the cluster.
func Check(settings Settings, serverURL string, controlPlane, nodes []corev1.Node) Report {
	var report Report
	if !settings.Enabled {
		return report
	}

	if problem := CheckServerURL(settings, serverURL); problem != "" {
		report.Warnings = append(report.Warnings, problem)
	}

	report.Blocking = append(report.Blocking, controlPlaneProblems(settings, controlPlane)...)

	var unadvertised, privateWireGuard, unusable []string
	for i := range nodes {
		node := &nodes[i]
		addresses := externalAddresses(node)
		if len(addresses) == 0 {
			unadvertised = append(unadvertised, node.Name)
			continue
		}
		for _, address := range addresses {
			kind, ok := Kind(address)
			switch {
			case !ok, kind == AddressUnusable:
				unusable = append(unusable, node.Name)
			case kind == AddressPrivate && settings.Provider == geassv1alpha1.MeshProviderWireGuard:
				privateWireGuard = append(privateWireGuard, node.Name)
			}
		}
	}
	if len(unadvertised) > 0 {
		report.Warnings = append(report.Warnings, "nodes that advertise no external address are not on the mesh, and other sites cannot reach them "+
			"(a worker joined by hand without node-external-ip, or one from before the mesh was turned on): "+named(unadvertised))
	}
	if len(unusable) > 0 {
		report.Warnings = append(report.Warnings, "nodes that advertise an external address no other machine can reach: "+named(unusable))
	}
	if len(privateWireGuard) > 0 {
		report.Warnings = append(report.Warnings, "nodes that advertise a private address (a WireGuard node with a private address is reached from another "+
			"site only after it has opened the tunnel itself, as a node behind a NAT does, so two such nodes cannot reach each other): "+named(privateWireGuard))
	}
	return report
}

// controlPlaneProblems are the reasons the control plane is not on the mesh, which is what a worker is joined to.
func controlPlaneProblems(settings Settings, controlPlane []corev1.Node) []string {
	if len(controlPlane) == 0 {
		return nil
	}
	var bare, vxlan []string
	for i := range controlPlane {
		node := &controlPlane[i]
		if len(externalAddresses(node)) == 0 {
			bare = append(bare, node.Name)
		}
		if settings.Provider == geassv1alpha1.MeshProviderWireGuard {
			if backend, ok := node.Annotations[AnnotationFlannelBackendType]; ok && backend != flannelBackendWireGuard {
				vxlan = append(vxlan, node.Name)
			}
		}
	}
	var problems []string
	if len(vxlan) > 0 {
		problems = append(problems, fmt.Sprintf("the control plane (%s) runs the %q overlay, not wireguard: it was installed without the mesh, and a mesh "+
			"cannot be switched on afterwards. Workers joined now could not reach the other nodes. Reinstall the control plane with GEASS_MESH set",
			strings.Join(vxlan, ", "), "vxlan"))
	}
	if len(bare) > 0 && len(bare) == len(controlPlane) {
		problems = append(problems, fmt.Sprintf("the control plane (%s) advertises no external address, so it was installed without the mesh. "+
			"Workers joined now could not reach it from other sites. Reinstall the control plane with GEASS_MESH set", strings.Join(bare, ", ")))
	}
	return problems
}

func externalAddresses(node *corev1.Node) []string {
	var addresses []string
	for _, address := range node.Status.Addresses {
		if address.Type == corev1.NodeExternalIP && strings.TrimSpace(address.Address) != "" {
			addresses = append(addresses, strings.TrimSpace(address.Address))
		}
	}
	return addresses
}

// named lists node names for a message, in order, without repeats, and cuts the list.
func named(names []string) string {
	names = slices.Compact(slices.Sorted(slices.Values(names)))
	if len(names) <= maxNamed {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:maxNamed], ", "), len(names)-maxNamed)
}
