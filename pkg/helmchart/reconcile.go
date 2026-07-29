package helmchart

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

// Ensure creates or updates a HelmChart in kube-system with the provided spec.
func Ensure(ctx context.Context, c client.Client, name string, spec helmv1.HelmChartSpec) error {
	chart := &helmv1.HelmChart{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: platform.HelmChartNamespace,
		},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, c, chart, func() error {
		chart.Spec = spec
		return nil
	})
	if err != nil {
		return fmt.Errorf("ensure HelmChart %s: %w", name, err)
	}
	_ = op
	return nil
}

// Delete removes a HelmChart if it exists.
func Delete(ctx context.Context, c client.Client, name string) error {
	chart := &helmv1.HelmChart{}
	err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: platform.HelmChartNamespace}, chart)
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := c.Delete(ctx, chart); apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return nil
	} else if err != nil {
		return err
	}
	return nil
}

// IsReady reports whether the HelmChart install job completed successfully.
// K3s records the install job name on status.jobName; job completion is the
// authoritative signal that the chart was deployed.
func IsReady(ctx context.Context, c client.Client, chart *helmv1.HelmChart) (bool, error) {
	if chart == nil || chart.Status.JobName == "" {
		return false, nil
	}
	if c == nil {
		return false, fmt.Errorf("client is required to verify HelmChart install job")
	}

	job := &batchv1.Job{}
	err := c.Get(ctx, types.NamespacedName{
		Name:      chart.Status.JobName,
		Namespace: chart.Namespace,
	}, job)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return job.Status.Succeeded > 0, nil
}

// Get fetches a HelmChart by name from kube-system.
func Get(ctx context.Context, c client.Client, name string) (*helmv1.HelmChart, error) {
	chart := &helmv1.HelmChart{}
	err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: platform.HelmChartNamespace}, chart)
	if err != nil {
		return nil, err
	}
	return chart, nil
}
