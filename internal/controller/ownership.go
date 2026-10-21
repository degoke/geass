package controller

import (
	"context"
	"errors"
	"fmt"
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

// errNamespaceNotOwned is returned when a project's environment namespace already exists and belongs to someone else.
var errNamespaceNotOwned = errors.New("namespace is owned by something else")

// namespaceBelongsTo reports whether a project may use, and later delete, the namespace. A namespace that names its
// project belongs to that project alone. One that does not name a project, which an older version created, belongs to
// whoever Geass created it for, and one Geass did not create (kube-system, a namespace made by hand) belongs to nobody
// here, however a project and an environment happen to spell its name.
func namespaceBelongsTo(ns *corev1.Namespace, project string) bool {
	// An organization's build namespace is nobody's project's, however a project and an environment spell its name.
	if ns.Labels[platform.LabelOrgBuild] != "" {
		return false
	}
	if owner := ns.Labels[platform.LabelProject]; owner != "" {
		return owner == project
	}
	return ns.Labels[platform.LabelManagedBy] == platform.ManagedByValue
}

// deleteOwnedNamespace deletes a project environment's namespace, unless it is someone else's. The labels on an object
// passed to Delete are not looked at by the API server, so the owner has to be checked on what is stored.
func deleteOwnedNamespace(ctx context.Context, c client.Client, name, project string) error {
	ns := &corev1.Namespace{}
	if err := c.Get(ctx, client.ObjectKey{Name: name}, ns); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !namespaceBelongsTo(ns, project) {
		return nil
	}
	return client.IgnoreNotFound(c.Delete(ctx, ns))
}

// ensureNamespace creates a project environment's namespace, or accepts the one that is there when it is the project's.
// It refuses a namespace that belongs to another project or that Geass did not create: using it would hand one project
// the other's workloads, and deleting the project would delete them.
func ensureNamespace(ctx context.Context, c client.Client, name string, labels map[string]string) error {
	ns := &corev1.Namespace{}
	err := c.Get(ctx, client.ObjectKey{Name: name}, ns)
	if err == nil {
		if project := labels[platform.LabelProject]; project != "" && !namespaceBelongsTo(ns, project) {
			return fmt.Errorf("%w: namespace %q already exists and is not project %q's", errNamespaceNotOwned, name, project)
		}
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

// setSecretStrings sets string values on a secret's Data, leaving its other keys as they are, which is how
// StringData is merged by the API server. It is written to Data and not StringData because the API server
// never returns StringData: a mutate function that sets it always looks like a change to
// CreateOrUpdate, and the secret would be written on every reconcile.
func setSecretStrings(secret *corev1.Secret, values map[string]string) {
	if secret.Data == nil {
		secret.Data = make(map[string][]byte, len(values))
	}
	for key, value := range values {
		secret.Data[key] = []byte(value)
	}
}
