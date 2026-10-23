package controller

import (
	"context"
	"errors"
	"reflect"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Under spec.tenancy.registry an organization's service builds only into the organization's own registry, never with the
// platform's login, and runs no image that is the platform's or another organization's (see platform.ResolveRegistryIsolation).
// The service controller holds a service back before anything new is written for it, and takes the platform's login out of
// its namespace; the build controller holds a build back before its Job is made or anything is copied for it, and stops one
// that is running with the platform's login. These are the pieces they share: the condition and event that say so, and the
// watches that bring a change of the setting, or of any organization's registry, to the services it changes.

// registryRefusal is the refusal an error is, or nil when it is some other error.
func registryRefusal(err error) *platform.RegistryRefusedError {
	var refusal *platform.RegistryRefusedError
	if errors.As(err, &refusal) {
		return refusal
	}
	return nil
}

// registryConditions are the conditions with the RegistryIsolated condition that the policy gives, or without one when the
// policy does not apply.
func registryConditions(conditions []metav1.Condition, applies bool, refusal *platform.RegistryRefusedError) []metav1.Condition {
	switch {
	case refusal != nil:
		return platform.SetCondition(conditions, platform.ConditionRegistryIsolated, metav1.ConditionFalse, refusal.Reason, refusal.Error())
	case applies:
		return platform.SetCondition(conditions, platform.ConditionRegistryIsolated, metav1.ConditionTrue, platform.RegistryReasonIsolated,
			"Uses no registry login of the platform's or of another organization's, and no image of theirs")
	default:
		meta.RemoveStatusCondition(&conditions, platform.ConditionRegistryIsolated)
		return conditions
	}
}

// heldForRegistry reports whether the conditions already say the policy holds the object back for this reason, so that the
// warning event is sent once when it begins and not on every pass.
func heldForRegistry(conditions []metav1.Condition, refusal *platform.RegistryRefusedError) bool {
	condition := meta.FindStatusCondition(conditions, platform.ConditionRegistryIsolated)
	return condition != nil && condition.Status == metav1.ConditionFalse && condition.Reason == refusal.Reason
}

// warnRegistryRefused records the warning event of a refusal on what was refused. A nil recorder records nothing.
func warnRegistryRefused(recorder events.EventRecorder, regarding runtime.Object, refusal *platform.RegistryRefusedError) {
	if recorder == nil {
		return
	}
	recorder.Eventf(regarding, nil, corev1.EventTypeWarning, platform.EventRegistryRefused, "HoldBack", "%s", refusal.Error())
}

// recordAppRegistry writes onto the service's status what the policy says of it. Nothing is read or written for a service
// the policy does not apply to and that says nothing, which is every service while the setting is off.
func (r *GeassAppReconciler) recordAppRegistry(ctx context.Context, app *geassv1alpha1.GeassApp, applies bool, refusal *platform.RegistryRefusedError) error {
	if !applies && refusal == nil && meta.FindStatusCondition(app.Status.Conditions, platform.ConditionRegistryIsolated) == nil {
		return nil
	}
	latest := &geassv1alpha1.GeassApp{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(app), latest); err != nil {
		return client.IgnoreNotFound(err)
	}
	began := refusal != nil && !heldForRegistry(latest.Status.Conditions, refusal)
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.Conditions = registryConditions(latest.Status.Conditions, applies, refusal)
	}); err != nil {
		return err
	}
	app.Status.Conditions = latest.Status.Conditions
	if began {
		warnRegistryRefused(r.Recorder, latest, refusal)
	}
	return nil
}

// holdForRegistry applies the registry policy to a service that is to be deployed. stop is true when the policy holds it
// back: its Deployment is left as it was and nothing new is written for it, and it says why. Whatever it decides, a copy of
// the platform's login in the service's namespace is removed.
func (r *GeassAppReconciler) holdForRegistry(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) (result ctrl.Result, stop bool, err error) {
	isolation, err := platform.ResolveRegistryIsolation(ctx, r.Client, app.Spec.Project)
	if err != nil {
		result, err = r.setNotReady(ctx, app, err.Error())
		return result, true, err
	}
	if isolation == nil {
		return ctrl.Result{}, false, r.recordAppRegistry(ctx, app, false, nil)
	}
	if err := r.removePlatformPullSecret(ctx, app, wsNS, isolation); err != nil {
		result, err = r.setNotReady(ctx, app, err.Error())
		return result, true, err
	}
	checked := r.checkAppRegistry(ctx, app, isolation)
	refusal := registryRefusal(checked)
	if checked != nil && refusal == nil {
		result, err = r.setNotReady(ctx, app, checked.Error())
		return result, true, err
	}
	if err := r.recordAppRegistry(ctx, app, true, refusal); err != nil {
		return ctrl.Result{}, true, err
	}
	if refusal != nil {
		result, err = r.setNotReady(ctx, app, refusal.Error())
		return result, true, err
	}
	return ctrl.Result{}, false, nil
}

// checkAppRegistry is what the policy refuses of a service: an image that is not the organization's to run, or a build that
// would push with a login or to a repository that is not the organization's. A function runs an image of Geass's own.
func (r *GeassAppReconciler) checkAppRegistry(ctx context.Context, app *geassv1alpha1.GeassApp, isolation *platform.RegistryIsolation) error {
	switch {
	case app.Spec.Source.Image != nil:
		return isolation.CheckImage(app.Spec.Source.Image.Image)
	case app.Spec.Source.Git != nil:
		_, _, err := isolation.BuildRegistry(ctx, r.Client, app)
		return err
	default:
		return nil
	}
}

// removePlatformPullSecret deletes the service's pull secret when it holds the platform's login, as one copied before the
// policy was set does. The service's pods then pull with the organization's own login once Geass writes it there, or with
// none.
func (r *GeassAppReconciler) removePlatformPullSecret(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string, isolation *platform.RegistryIsolation) error {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: registryPullSecretName(app), Namespace: wsNS}, secret); err != nil {
		return client.IgnoreNotFound(err)
	}
	holds, err := isolation.HoldsPlatformLogin(ctx, r.Client, secret.Data[corev1.DockerConfigJsonKey])
	if err != nil || !holds {
		return err
	}
	return client.IgnoreNotFound(r.Delete(ctx, secret))
}

// buildRegistryWait is what a build that the policy holds back says, for the service that waits for the build.
func buildRegistryWait(build *geassv1alpha1.GeassBuild) string {
	if condition := meta.FindStatusCondition(build.Status.Conditions, platform.ConditionRegistryIsolated); condition != nil && condition.Status == metav1.ConditionFalse {
		return condition.Message
	}
	return ""
}

// recordBuildRegistry writes onto the build's status what the policy says of it, before the build goes on.
func (r *GeassBuildReconciler) recordBuildRegistry(ctx context.Context, build *geassv1alpha1.GeassBuild, applies bool, refusal *platform.RegistryRefusedError) error {
	if !applies && refusal == nil && meta.FindStatusCondition(build.Status.Conditions, platform.ConditionRegistryIsolated) == nil {
		return nil
	}
	latest := &geassv1alpha1.GeassBuild{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(build), latest); err != nil {
		return err
	}
	began := refusal != nil && !heldForRegistry(latest.Status.Conditions, refusal)
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.Conditions = registryConditions(latest.Status.Conditions, applies, refusal)
	}); err != nil {
		return err
	}
	build.Status.Conditions = latest.Status.Conditions
	if began {
		warnRegistryRefused(r.Recorder, latest, refusal)
	}
	return nil
}

// holdBuildForRegistry applies the registry policy to a build that has not finished. held is true when the policy refused
// it: a Job it had is deleted with what was copied for it, and the build has failed and says why. Before an organization's
// build starts, any copy of the platform's login left in the organization's build namespace is removed.
func (r *GeassBuildReconciler) holdBuildForRegistry(ctx context.Context, build *geassv1alpha1.GeassBuild, started bool) (held bool, err error) {
	isolation, err := platform.ResolveRegistryIsolation(ctx, r.Client, build.Spec.Project)
	if err != nil {
		return false, err
	}
	if isolation == nil {
		return false, r.recordBuildRegistry(ctx, build, false, nil)
	}
	if !started {
		if err := r.removePlatformLogins(ctx, isolation); err != nil {
			return false, err
		}
	}
	checked := isolation.CheckCredential(ctx, r.Client, build.Namespace, build.Spec.CredentialRef)
	if checked == nil {
		checked = isolation.CheckDestination(build.Spec.Registry)
	}
	refusal := registryRefusal(checked)
	if checked != nil && refusal == nil {
		return false, checked
	}
	if refusal == nil {
		return false, r.recordBuildRegistry(ctx, build, true, nil)
	}
	job := &batchv1.Job{}
	switch err := r.Get(ctx, client.ObjectKey{Name: jobNameOf(build), Namespace: jobNamespaceOf(build)}, job); {
	case err == nil:
		if err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
			return true, err
		}
	case !apierrors.IsNotFound(err):
		return true, err
	}
	r.removeBuildSecrets(ctx, build)
	if err := r.removePlatformLogins(ctx, isolation); err != nil {
		return true, err
	}
	if err := r.recordBuildRegistry(ctx, build, true, refusal); err != nil {
		return true, err
	}
	return true, r.updateStatus(ctx, build, geassv1alpha1.GeassBuildFailed, nil, refusal)
}

// removePlatformLogins deletes from an organization's build namespace every Secret Geass copied there that holds the
// platform's login, as the copies of builds that ran before the policy was set do.
func (r *GeassBuildReconciler) removePlatformLogins(ctx context.Context, isolation *platform.RegistryIsolation) error {
	if isolation.Org == "" || platform.ValidOrgBuildNamespace(isolation.Org) != nil {
		return nil
	}
	var secrets corev1.SecretList
	if err := r.List(ctx, &secrets, client.InNamespace(platform.OrgBuildNamespace(isolation.Org)), client.MatchingLabels{platform.LabelManagedBy: platform.ManagedByValue}); err != nil {
		return err
	}
	for i := range secrets.Items {
		holds, err := isolation.HoldsPlatformLogin(ctx, r.Client, secrets.Items[i].Data[corev1.DockerConfigJsonKey])
		if err != nil {
			return err
		}
		if holds {
			if err := r.Delete(ctx, &secrets.Items[i]); client.IgnoreNotFound(err) != nil {
				return err
			}
		}
	}
	return nil
}

// registrySetting is what of the platform's configuration the registry policy depends on: the policy, and while it is set,
// the platform's registry, whose repository is the platform's and whose login is never handed over.
func registrySetting(o client.Object) (string, geassv1alpha1.GeassPlatformRegistrySpec) {
	config, ok := o.(*geassv1alpha1.GeassPlatformConfig)
	if !ok || config.Name != platform.HAReadinessName || config.Namespace != platform.SystemNamespace {
		return "", geassv1alpha1.GeassPlatformRegistrySpec{}
	}
	policy := platform.RegistryPolicyOf(config)
	if policy == "" {
		return "", geassv1alpha1.GeassPlatformRegistrySpec{}
	}
	return policy, config.Spec.Registry
}

// registryPolicyChanged lets through the platform's configuration appearing, going or changing what the registry policy
// depends on.
func registryPolicyChanged() predicate.Predicate {
	set := func(o client.Object) bool { policy, _ := registrySetting(o); return policy != "" }
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return set(e.Object) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return set(e.Object) },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldPolicy, oldRegistry := registrySetting(e.ObjectOld)
			newPolicy, newRegistry := registrySetting(e.ObjectNew)
			return oldPolicy != newPolicy || !reflect.DeepEqual(oldRegistry, newRegistry)
		},
	}
}

// appsForOrganization reaches the services of the organization's projects. While the registry policy is set it reaches
// every service: an organization's registry is a part of a registry that no other organization's service may use.
func (r *GeassAppReconciler) appsForOrganization(ctx context.Context, organization client.Object) []ctrl.Request {
	var config geassv1alpha1.GeassPlatformConfig
	if err := r.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config); err == nil && platform.RegistryPolicyOf(&config) != "" {
		return r.allApps(ctx, organization)
	}
	return forOrganizationsProjects(r.Client, r.appsForProject)(ctx, organization)
}
