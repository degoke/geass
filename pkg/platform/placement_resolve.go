package platform

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// ResolvePlacement is where a project's workload may run: the project's region, or the cluster's
// default when the project has none, plus the node tags the workload asks for. A project that is
// gone resolves to the tags alone, so cleanup of its leftovers is never blocked on the region.
func ResolvePlacement(ctx context.Context, c client.Reader, project string, tags []string) (Placement, error) {
	region, err := ProjectRegion(ctx, c, project)
	if err != nil {
		return Placement{}, err
	}
	return NewPlacement(region, tags), nil
}

// ProjectRegion is the region a project runs in, falling back to the cluster default. It is empty
// when neither is set, which means "any node".
func ProjectRegion(ctx context.Context, c client.Reader, project string) (string, error) {
	if project == "" {
		return DefaultRegion(ctx, c, "")
	}
	var p geassv1alpha1.GeassProject
	if err := c.Get(ctx, client.ObjectKey{Name: project, Namespace: SystemNamespace}, &p); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	if p.Spec.Region != "" {
		return p.Spec.Region, nil
	}
	return DefaultRegion(ctx, c, p.Spec.ClusterRef.Name)
}

// DefaultRegion is where projects with no region run. With no cluster named it reads the first cluster,
// which is the only one in practice.
func DefaultRegion(ctx context.Context, c client.Reader, cluster string) (string, error) {
	if cluster != "" {
		var found geassv1alpha1.GeassCluster
		if err := c.Get(ctx, client.ObjectKey{Name: cluster, Namespace: SystemNamespace}, &found); err != nil {
			return "", client.IgnoreNotFound(err)
		}
		return found.Spec.DefaultRegion, nil
	}
	var clusters geassv1alpha1.GeassClusterList
	if err := c.List(ctx, &clusters, client.InNamespace(SystemNamespace)); err != nil {
		return "", err
	}
	for i := range clusters.Items {
		if clusters.Items[i].Spec.DefaultRegion != "" {
			return clusters.Items[i].Spec.DefaultRegion, nil
		}
	}
	return "", nil
}
