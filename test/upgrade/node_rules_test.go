package upgrade

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// A machine is deleted through the connection that made it, found in the region it was made in and counted by the pool
// that made it. A node edited to say otherwise would leave its machine billing with nothing tracking it, so the API
// server refuses the edit; this runs the rules on a real one.
func TestANodesCloudConnectionRegionAndPoolCannotBeChanged(t *testing.T) {
	cfg, c := startAPIServer(t)
	install(t, cfg, currentCRDs(t))
	require.NoError(t, c.Create(background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "geass-system"}}))

	node := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "geass.geass.dev/v1alpha1", "kind": "GeassNode",
		"metadata": map[string]any{"name": "burst-a", "namespace": "geass-system"},
		"spec": map[string]any{
			"role": string(geassv1alpha1.NodeRoleWorker), "clusterRef": map[string]any{"name": "default"}, "pool": "burst",
			"cloud": map[string]any{"connectionRef": "hetzner", "region": "fsn1", "size": "cx23"},
		},
	}}
	require.NoError(t, c.Create(background(), node))

	change := func(edit func(spec map[string]any)) error {
		latest := &unstructured.Unstructured{}
		latest.SetGroupVersionKind(node.GroupVersionKind())
		require.NoError(t, c.Get(background(), client.ObjectKeyFromObject(node), latest))
		spec, _ := latest.Object["spec"].(map[string]any)
		edit(spec)
		return c.Update(background(), latest)
	}
	cloud := func(spec map[string]any) map[string]any { return spec["cloud"].(map[string]any) }

	err := change(func(spec map[string]any) { cloud(spec)["connectionRef"] = "other" })
	require.ErrorContains(t, err, "cannot be changed", "another connection would delete the machine through the wrong cloud")
	err = change(func(spec map[string]any) { cloud(spec)["region"] = "nbg1" })
	require.ErrorContains(t, err, "cannot be changed")
	err = change(func(spec map[string]any) { delete(cloud(spec), "region") })
	require.ErrorContains(t, err, "cannot be changed", "taking the region away is a change too")
	err = change(func(spec map[string]any) { spec["pool"] = "elsewhere" })
	require.ErrorContains(t, err, "cannot move to another pool")

	require.NoError(t, change(func(spec map[string]any) { cloud(spec)["size"] = "cx33" }), "what does not decide who owns the machine can still change")
}
