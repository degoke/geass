package helmchart

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.Delete(ctx, chart)
}

// IsReady reports whether the HelmChart status indicates a deployed release.
func IsReady(chart *helmv1.HelmChart) bool {
	if chart == nil {
		return false
	}
	for _, cond := range chart.Status.Conditions {
		if cond.Type == "Deployed" && cond.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
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
