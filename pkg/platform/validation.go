package platform

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// ValidateProjectPlacement verifies that a workload targets an existing,
// ready project environment. API schema validation handles required fields;
// this check handles references to live Kubernetes objects.
func ValidateProjectPlacement(ctx context.Context, c client.Client, project string, environment geassv1alpha1.GeassEnvironment) error {
	if project == "" {
		return fmt.Errorf("project is required")
	}
	if _, err := ProjectNamespace(project, string(environment)); err != nil {
		return err
	}
	var p geassv1alpha1.GeassProject
	if err := c.Get(ctx, client.ObjectKey{Name: project, Namespace: SystemNamespace}, &p); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("project %q does not exist", project)
		}
		return err
	}
	if conditionStatus(p.Status.Conditions, ConditionReady) != "True" {
		return fmt.Errorf("project %q is not ready", project)
	}
	var cluster geassv1alpha1.GeassCluster
	if err := c.Get(ctx, client.ObjectKey{Name: p.Spec.ClusterRef.Name, Namespace: SystemNamespace}, &cluster); err != nil {
		return fmt.Errorf("project %q cluster %q is unavailable: %w", project, p.Spec.ClusterRef.Name, err)
	}
	if conditionStatus(cluster.Status.Conditions, ConditionReady) != "True" {
		return fmt.Errorf("project %q cluster %q is not ready", project, cluster.Name)
	}
	for _, env := range p.Spec.Environments {
		if env == string(environment) {
			return nil
		}
	}
	return fmt.Errorf("project %q has no %s environment", project, environment)
}

func conditionStatus(conditions []metav1.Condition, conditionType string) string {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return string(condition.Status)
		}
	}
	return "Unknown"
}
