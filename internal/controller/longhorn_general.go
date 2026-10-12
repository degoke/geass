package controller

import (
	"context"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

// A compliance node is kept for the projects that require its tag, so another project's volume must not
// put a replica on it. Longhorn only includes nodes by tag, it cannot exclude them, so once any node is a
// compliance node every other node is tagged geass-general and the classes that serve everyone else name
// that tag. A cluster with no compliance node never reaches any of this.

// complianceMode reports whether any node is a compliance node.
func complianceMode(nodes []corev1.Node) bool {
	for i := range nodes {
		if platform.IsComplianceNode(&nodes[i]) {
			return true
		}
	}
	return false
}

// generalOnly reports whether general volumes are to be kept off compliance nodes right now. That takes
// a compliance node, somewhere else for volumes to go, and every one of those other Longhorn nodes
// already tagged: a class that names a tag no node carries yet would strand new volumes, so the classes
// wait for the tags.
func (r *GeassClusterReconciler) generalOnly(ctx context.Context) (bool, error) {
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return false, err
	}
	if !complianceMode(nodes.Items) {
		return false, nil
	}
	general := map[string]bool{}
	for i := range nodes.Items {
		if nodeSchedulable(&nodes.Items[i]) && !platform.IsComplianceNode(&nodes.Items[i]) {
			general[nodes.Items[i].Name] = true
		}
	}
	if len(general) == 0 {
		return false, nil
	}
	var list unstructured.UnstructuredList
	list.SetGroupVersionKind(longhornNodeListKind)
	if err := r.List(ctx, &list, client.InNamespace(platform.LonghornTargetNS)); err != nil {
		return false, err
	}
	tagged := 0
	for i := range list.Items {
		if !general[list.Items[i].GetName()] {
			continue
		}
		tags, _, err := unstructured.NestedStringSlice(list.Items[i].Object, "spec", "tags")
		if err != nil || !slices.Contains(tags, platform.LonghornGeneralTag) {
			return false, err
		}
		tagged++
	}
	return tagged > 0, nil
}

// withGeneralSelector adds the general tag to a class's nodeSelector, when general volumes are to stay off
// compliance nodes.
func withGeneralSelector(params map[string]string, general bool) map[string]string {
	if !general {
		return params
	}
	selector := platform.LonghornGeneralTag
	if existing := params["nodeSelector"]; existing != "" {
		selector = existing + "," + selector
	}
	params["nodeSelector"] = selector
	return params
}

// volumeSelector is the nodeSelector the volumes of a class should carry: the class's own selector, and
// the general tag when general volumes are to stay off compliance nodes. Nil leaves a volume as it is.
func volumeSelector(classSelector string, general bool) []string {
	if !general {
		return nil
	}
	selector := []string{platform.LonghornGeneralTag}
	if classSelector != "" {
		selector = append(strings.Split(classSelector, ","), selector...)
	}
	return selector
}
