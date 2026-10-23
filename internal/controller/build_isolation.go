package controller

import (
	"context"
	"errors"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// A project that belongs to an organization builds in the organization's own build namespace (see platform.OrgBuildPolicy).
// What the build needs from geass-system is copied there for it: the login it pushes with and the git token of its project,
// and nothing else. The copies are removed when the build is done.

// jobNamespaceOf is the namespace a build's Job is in, which is the build's own unless the Job was made in an organization's
// build namespace.
func jobNamespaceOf(build *geassv1alpha1.GeassBuild) string {
	if build.Status.JobNamespace != "" {
		return build.Status.JobNamespace
	}
	return build.Namespace
}

func jobNameOf(build *geassv1alpha1.GeassBuild) string { return build.Name + "-kaniko" }

// errBuildNamespaceNotOwned is what a build says when the namespace it would run in exists and is not an organization's build
// namespace: Geass will not put an organization's credentials into something it did not make.
var errBuildNamespaceNotOwned = errors.New("the build namespace is owned by something else")

// buildNamespaceFor is where a build that is about to start runs: the build's own namespace for a project that belongs to
// no organization (as every build did before organizations), and otherwise the organization's build namespace. The
// organization is returned with it, for the login it may have of its own.
func (r *GeassBuildReconciler) buildNamespaceFor(ctx context.Context, build *geassv1alpha1.GeassBuild) (string, *geassv1alpha1.GeassOrganization, error) {
	var project geassv1alpha1.GeassProject
	if err := r.Get(ctx, client.ObjectKey{Name: build.Spec.Project, Namespace: platform.SystemNamespace}, &project); err != nil {
		if apierrors.IsNotFound(err) {
			return build.Namespace, nil, nil
		}
		return "", nil, err
	}
	org, err := platform.OrganizationOf(ctx, r.Client, &project)
	if err != nil || org == nil {
		return build.Namespace, nil, err
	}
	if err := platform.ValidOrgBuildNamespace(org.Name); err != nil {
		return "", nil, err
	}
	return platform.OrgBuildNamespace(org.Name), org, nil
}

// ensureOrgBuildNamespace makes an organization's build namespace, with the policy and quota that close and bound it, or
// accepts the one that is there when Geass made it for the organization. The organization's limits are written there too,
// and returned for the build's own containers.
func (r *GeassBuildReconciler) ensureOrgBuildNamespace(ctx context.Context, org *geassv1alpha1.GeassOrganization) (*geassv1alpha1.GeassTenantLimits, error) {
	name := platform.OrgBuildNamespace(org.Name)
	ns := &corev1.Namespace{}
	switch err := r.Get(ctx, client.ObjectKey{Name: name}, ns); {
	case err == nil:
		if ns.Labels[platform.LabelOrgBuild] != org.Name {
			return nil, fmt.Errorf("%w: namespace %q", errBuildNamespaceNotOwned, name)
		}
	case apierrors.IsNotFound(err):
		if err := r.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: platform.OrgBuildNamespaceLabels(org.Name)}}); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
	default:
		return nil, err
	}
	if err := applyNetworkPolicy(ctx, r.Client, platform.OrgBuildPolicy(name, org.Name, nil)); err != nil {
		return nil, err
	}
	quota := platform.OrgBuildQuota(name, org.Name)
	current := &corev1.ResourceQuota{ObjectMeta: quota.ObjectMeta}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, current, func() error {
		current.Labels = quota.Labels
		current.Spec.Hard = quota.Spec.Hard
		return nil
	}); err != nil {
		return nil, err
	}
	return reconcileOrgBuildLimits(ctx, r.Client, org)
}

// isolateJob moves a build's Job into the organization's build namespace, with copies of what it needs. The Job cannot be
// owned by the build, which is in another namespace, so it carries labels that say whose it is, and the controller watches
// Jobs by them.
func (r *GeassBuildReconciler) isolateJob(ctx context.Context, build *geassv1alpha1.GeassBuild, job *batchv1.Job, org *geassv1alpha1.GeassOrganization, namespace string) error {
	limits, err := r.ensureOrgBuildNamespace(ctx, org)
	if err != nil {
		return err
	}
	job.Namespace = namespace
	if job.Labels == nil {
		job.Labels = map[string]string{}
	}
	job.Labels[platform.LabelBuild], job.Labels[platform.LabelBuildNamespace] = build.Name, build.Namespace
	pod := &job.Spec.Template.Spec
	// A build asks for nothing of its own. Where the organization's limits give a container a most, the build is given it,
	// and not the default limit a service is: a build needs far more for the few minutes it runs.
	if resources := platform.BuildResources(limits); resources != nil {
		for i := range pod.InitContainers {
			pod.InitContainers[i].Resources = *resources.DeepCopy()
		}
		for i := range pod.Containers {
			pod.Containers[i].Resources = *resources.DeepCopy()
		}
	}

	if build.Spec.CredentialRef != nil {
		copied, err := r.copyBuildSecret(ctx, build, namespace, build.Spec.CredentialRef.Name, build.Name+"-registry", map[string]string{corev1.DockerConfigJsonKey: corev1.DockerConfigJsonKey}, corev1.SecretTypeDockerConfigJson)
		if err != nil {
			return err
		}
		for i := range pod.Volumes {
			if pod.Volumes[i].Secret != nil && pod.Volumes[i].Name == "registry-auth" {
				pod.Volumes[i].Secret.SecretName = copied
			}
		}
	}
	if build.Spec.GitCredentialRef != nil {
		copied, err := r.copyBuildSecret(ctx, build, namespace, build.Spec.GitCredentialRef.Name, build.Name+"-git", map[string]string{"token": "token"}, corev1.SecretTypeOpaque)
		if err != nil {
			return err
		}
		for i := range pod.InitContainers {
			for j := range pod.InitContainers[i].Env {
				if ref := pod.InitContainers[i].Env[j].ValueFrom; ref != nil && ref.SecretKeyRef != nil {
					ref.SecretKeyRef.Name = copied
				}
			}
		}
	}
	return nil
}

// copyBuildSecret copies the named keys of a Secret of the build's namespace into the build namespace, under a name of the
// build's, and says what it is called there. The copy is labelled for the build so that it is removed with it.
func (r *GeassBuildReconciler) copyBuildSecret(ctx context.Context, build *geassv1alpha1.GeassBuild, namespace, from, to string, keys map[string]string, kind corev1.SecretType) (string, error) {
	var source corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Name: from, Namespace: build.Namespace}, &source); err != nil {
		return "", fmt.Errorf("the secret %q the build needs is unavailable", from)
	}
	data := map[string][]byte{}
	for sourceKey, targetKey := range keys {
		value, ok := source.Data[sourceKey]
		if !ok {
			return "", fmt.Errorf("the secret %q has no %q", from, sourceKey)
		}
		data[targetKey] = value
	}
	copied := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: to, Namespace: namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, copied, func() error {
		copied.Labels = map[string]string{platform.LabelManagedBy: platform.ManagedByValue, platform.LabelBuild: build.Name, platform.LabelBuildNamespace: build.Namespace}
		copied.Type = kind
		copied.Data = data
		return nil
	}); err != nil {
		return "", err
	}
	return to, nil
}

// removeBuildSecrets takes the copies made for a build out of the build namespace. It is best effort: one that is left is
// the organization's own credential in the organization's own namespace, and the next run of the build writes it again.
func (r *GeassBuildReconciler) removeBuildSecrets(ctx context.Context, build *geassv1alpha1.GeassBuild) {
	namespace := jobNamespaceOf(build)
	if namespace == build.Namespace {
		return
	}
	for _, name := range []string{build.Name + "-registry", build.Name + "-git"} {
		_ = r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}})
	}
}

// buildForJob maps a Job in an organization's build namespace to the build it is for.
func buildForJob(_ context.Context, job client.Object) []reconcile.Request {
	name, namespace := job.GetLabels()[platform.LabelBuild], job.GetLabels()[platform.LabelBuildNamespace]
	if name == "" || namespace == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: name, Namespace: namespace}}}
}
