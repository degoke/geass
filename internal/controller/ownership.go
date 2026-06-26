package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	labelManagedBy     = "geass.dev/managed-by"
	labelGeassKind     = "geass.dev/kind"
	labelGeassName     = "geass.dev/name"
	labelGeassSystemNS = "geass.dev/system-namespace"
	managedByValue     = "geass"
)

func geassResourceLabels(owner client.Object, kind string) map[string]string {
	return map[string]string{
		labelManagedBy:     managedByValue,
		labelGeassKind:     kind,
		labelGeassName:     owner.GetName(),
		labelGeassSystemNS: owner.GetNamespace(),
	}
}

func applyGeassLabels(obj metav1.Object, owner client.Object, kind string) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	for k, v := range geassResourceLabels(owner, kind) {
		labels[k] = v
	}
	obj.SetLabels(labels)
}

func setSameNamespaceOwner(owner, child client.Object, scheme *runtime.Scheme) error {
	if owner.GetNamespace() != child.GetNamespace() {
		return nil
	}
	return controllerutil.SetControllerReference(owner, child, scheme)
}

// previousWorkspaceNamespace returns the namespace recorded in status when the
// workspace changed and resources in the old namespace must be cleaned up.
func previousWorkspaceNamespace(statusNS, currentNS string) (string, bool) {
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
