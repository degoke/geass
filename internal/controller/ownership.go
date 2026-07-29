package controller

import (
	"context"
	"maps"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	labelGeassKind     = "geass.dev/kind"
	labelGeassName     = "geass.dev/name"
	labelGeassSystemNS = "geass.dev/system-namespace"
)

func resourceNamespace(project, environment string) (string, error) {
	return platform.ProjectNamespace(project, environment)
}

func geassResourceLabels(owner client.Object, kind string) map[string]string {
	labels := map[string]string{
		platform.LabelManagedBy: platform.ManagedByValue,
		labelGeassKind:          kind,
		labelGeassName:          owner.GetName(),
		labelGeassSystemNS:      owner.GetNamespace(),
	}
	switch resource := owner.(type) {
	case *geassv1alpha1.GeassApp:
		labels[platform.LabelProject], labels[platform.LabelEnvironment] = resource.Spec.Project, string(resource.Spec.Environment)
	case *geassv1alpha1.GeassDatabase:
		labels[platform.LabelProject], labels[platform.LabelEnvironment] = resource.Spec.Project, string(resource.Spec.Environment)
	case *geassv1alpha1.GeassCache:
		labels[platform.LabelProject], labels[platform.LabelEnvironment] = resource.Spec.Project, string(resource.Spec.Environment)
	case *geassv1alpha1.GeassObjectStore:
		labels[platform.LabelProject], labels[platform.LabelEnvironment] = resource.Spec.Project, string(resource.Spec.Environment)
	}
	return labels
}

func applyGeassLabels(obj metav1.Object, owner client.Object, kind string) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	maps.Copy(labels, geassResourceLabels(owner, kind))
	obj.SetLabels(labels)
}

func setSameNamespaceOwner(owner, child client.Object, scheme *runtime.Scheme) error {
	if owner.GetNamespace() != child.GetNamespace() {
		return nil
	}
	return controllerutil.SetControllerReference(owner, child, scheme)
}

// previousTargetNamespace returns the namespace recorded in status when the
// placement changed and resources in the old namespace must be cleaned up.
func previousTargetNamespace(statusNS, currentNS string) (string, bool) {
	if statusNS == "" || statusNS == currentNS {
		return "", false
	}
	return statusNS, true
}

func ensureNamespace(ctx context.Context, c client.Client, name string, labels map[string]string) error {
	ns := &corev1.Namespace{}
	err := c.Get(ctx, client.ObjectKey{Name: name}, ns)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	ns = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
	return c.Create(ctx, ns)
}
