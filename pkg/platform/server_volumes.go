package platform

import (
	corev1 "k8s.io/api/core/v1"
)

// VolumesOnlyOn are the volumes that can be used from the named node and from no other node of the cluster: a local volume,
// or a Longhorn volume of the geass-single class, which keeps its one copy on the disk of the machine it runs on. Taking
// the node out of the cluster takes their data with it. A volume that is not tied to a node, or that more than one node
// can use, is not here.
func VolumesOnlyOn(name string, nodes []corev1.Node, volumes []corev1.PersistentVolume) []corev1.PersistentVolume {
	var here *corev1.Node
	for i := range nodes {
		if nodes[i].Name == name {
			here = &nodes[i]
		}
	}
	if here == nil {
		return nil
	}
	var only []corev1.PersistentVolume
	for i := range volumes {
		selector, bound := VolumeNodeSelector(&volumes[i])
		if !bound || !NodeSelectorMatches(selector, here) {
			continue
		}
		elsewhere := false
		for j := range nodes {
			if nodes[j].Name != name && NodeSelectorMatches(selector, &nodes[j]) {
				elsewhere = true
				break
			}
		}
		if !elsewhere {
			only = append(only, volumes[i])
		}
	}
	return only
}
