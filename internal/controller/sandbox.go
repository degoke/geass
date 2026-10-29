package controller

import (
	"context"
	"errors"
	"reflect"

	corev1 "k8s.io/api/core/v1"
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

// The pods of an organization's services, their functions' schedules and their builds run under the RuntimeClass of
// spec.tenancy.sandbox (see platform.ResolveSandbox). The decision is part of the pod template, so a service whose decision
// changes is rolled like any other change to its pods, by its Deployment, a copy at a time. These are the pieces the
// service and build controllers share: the condition and event that say how the pods run, and the watches that make a
// change of the setting, of the RuntimeClass or of a reservation reach the services it changes.

// sandboxWait is the RuntimeClass an error says the pods wait for, or nil when it is some other error.
func sandboxWait(err error) *platform.SandboxRuntimeMissingError {
	var missing *platform.SandboxRuntimeMissingError
	if errors.As(err, &missing) {
		return missing
	}
	return nil
}

// sandboxConditions are the conditions with the Sandboxed condition the decision gives, or without one when there is
// nothing to say. A wait for the RuntimeClass is said with its message.
func sandboxConditions(conditions []metav1.Condition, sandbox platform.Sandbox, missing *platform.SandboxRuntimeMissingError) []metav1.Condition {
	if missing != nil {
		return platform.SetCondition(conditions, platform.ConditionSandboxed, metav1.ConditionFalse, platform.SandboxReasonRuntimeClassMissing, missing.Error())
	}
	status, reason, message, ok := sandbox.Condition()
	if !ok {
		meta.RemoveStatusCondition(&conditions, platform.ConditionSandboxed)
		return conditions
	}
	return platform.SetCondition(conditions, platform.ConditionSandboxed, status, reason, message)
}

// waitingForSandbox reports whether the conditions already say that the pods wait for the RuntimeClass, so that the
// warning event is sent once when a wait begins and not on every pass.
func waitingForSandbox(conditions []metav1.Condition) bool {
	condition := meta.FindStatusCondition(conditions, platform.ConditionSandboxed)
	return condition != nil && condition.Reason == platform.SandboxReasonRuntimeClassMissing
}

// warnSandboxMissing records the warning event of a wait for the RuntimeClass on what waits. A nil recorder records nothing.
func warnSandboxMissing(recorder events.EventRecorder, regarding runtime.Object, missing *platform.SandboxRuntimeMissingError) {
	if recorder == nil {
		return
	}
	recorder.Eventf(regarding, nil, corev1.EventTypeWarning, platform.EventSandboxRuntimeClassMissing, "WaitForRuntimeClass", "%s", missing.Error())
}

// recordSandbox writes how the service's pods run onto its status. Nothing is read or written for a service that has
// nothing to say and says nothing, which is every service while the setting is off.
func (r *GeassAppReconciler) recordSandbox(ctx context.Context, app *geassv1alpha1.GeassApp, sandbox platform.Sandbox, missing *platform.SandboxRuntimeMissingError) error {
	if _, _, _, ok := sandbox.Condition(); !ok && missing == nil && meta.FindStatusCondition(app.Status.Conditions, platform.ConditionSandboxed) == nil {
		return nil
	}
	latest := &geassv1alpha1.GeassApp{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(app), latest); err != nil {
		return client.IgnoreNotFound(err)
	}
	began := missing != nil && !waitingForSandbox(latest.Status.Conditions)
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.Conditions = sandboxConditions(latest.Status.Conditions, sandbox, missing)
	}); err != nil {
		return err
	}
	app.Status.Conditions = latest.Status.Conditions
	if began {
		warnSandboxMissing(r.Recorder, latest, missing)
	}
	return nil
}

// reconcileSandboxedDeployment writes the service's Deployment, and then says on the service's status how its pods run.
func (r *GeassAppReconciler) reconcileSandboxedDeployment(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string, opts ...workloadOption) error {
	var sandbox platform.Sandbox
	if err := r.reconcileDeployment(ctx, app, wsNS, append(opts, withSandboxResult(&sandbox))...); err != nil {
		return err
	}
	return r.recordSandbox(ctx, app, sandbox, nil)
}

// notReadyFor is setNotReady for the error a step of the reconcile returned, with a wait for the sandboxed runtime said as
// the wait it is.
func (r *GeassAppReconciler) notReadyFor(ctx context.Context, app *geassv1alpha1.GeassApp, err error) (ctrl.Result, error) {
	if missing := sandboxWait(err); missing != nil {
		return r.waitForSandbox(ctx, app, missing)
	}
	return r.setNotReady(ctx, app, err.Error())
}

// waitForSandbox holds a service whose pods cannot be made without the RuntimeClass: its Deployment is left as it was,
// it says what it waits for, and it is looked at again when the class appears (and every half minute until then).
func (r *GeassAppReconciler) waitForSandbox(ctx context.Context, app *geassv1alpha1.GeassApp, missing *platform.SandboxRuntimeMissingError) (ctrl.Result, error) {
	if err := r.recordSandbox(ctx, app, platform.Sandbox{}, missing); err != nil {
		return ctrl.Result{}, err
	}
	return r.setNotReady(ctx, app, missing.Error())
}

// recordBuildSandbox writes how a build's pod runs, or why it waits, onto the build's status before the build goes on.
func (r *GeassBuildReconciler) recordBuildSandbox(ctx context.Context, build *geassv1alpha1.GeassBuild, sandbox platform.Sandbox, missing *platform.SandboxRuntimeMissingError) error {
	if _, _, _, ok := sandbox.Condition(); !ok && missing == nil && meta.FindStatusCondition(build.Status.Conditions, platform.ConditionSandboxed) == nil {
		return nil
	}
	latest := &geassv1alpha1.GeassBuild{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(build), latest); err != nil {
		return err
	}
	began := missing != nil && !waitingForSandbox(latest.Status.Conditions)
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.Conditions = sandboxConditions(latest.Status.Conditions, sandbox, missing)
	}); err != nil {
		return err
	}
	build.Status.Conditions = latest.Status.Conditions
	if began {
		warnSandboxMissing(r.Recorder, latest, missing)
	}
	return nil
}

// buildSandboxWait is what a build that waits for the RuntimeClass says, for the service that waits for the build.
func buildSandboxWait(build *geassv1alpha1.GeassBuild) string {
	if condition := meta.FindStatusCondition(build.Status.Conditions, platform.ConditionSandboxed); condition != nil && condition.Reason == platform.SandboxReasonRuntimeClassMissing {
		return condition.Message
	}
	return ""
}

// tenancyChanged lets through the platform's configuration appearing, going or changing the parts of its spec.tenancy that
// decide how organizations' services run and what they may build into and run: the sandbox and the registry policy. The
// outbound limits are written by the project and organization controllers, and reach no service.
func tenancyChanged() predicate.Predicate {
	tenancy := func(o client.Object) *geassv1alpha1.GeassPlatformTenantIsolationSpec {
		config, ok := o.(*geassv1alpha1.GeassPlatformConfig)
		if !ok || config.Name != platform.HAReadinessName || config.Namespace != platform.SystemNamespace || config.Spec.Tenancy == nil {
			return nil
		}
		if config.Spec.Tenancy.Sandbox == nil && config.Spec.Tenancy.Registry == nil {
			return nil
		}
		return &geassv1alpha1.GeassPlatformTenantIsolationSpec{Sandbox: config.Spec.Tenancy.Sandbox, Registry: config.Spec.Tenancy.Registry}
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return tenancy(e.Object) != nil },
		DeleteFunc:  func(e event.DeleteEvent) bool { return tenancy(e.Object) != nil },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return !reflect.DeepEqual(tenancy(e.ObjectOld), tenancy(e.ObjectNew))
		},
	}
}

// runtimeClassCreatedOrDeleted lets through a RuntimeClass appearing or going. What a class runs with cannot change once
// it is made, so an update changes nothing a service's pods are made from.
func runtimeClassCreatedOrDeleted() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc:  func(event.UpdateEvent) bool { return false },
	}
}

// nodeReservationChanged lets through a node appearing, going, or changing its tags or the taints that reserve them.
func nodeReservationChanged() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			before, okBefore := e.ObjectOld.(*corev1.Node)
			after, okAfter := e.ObjectNew.(*corev1.Node)
			return okBefore && okAfter && platform.NodeReservationChanged(before, after)
		},
	}
}

// appsForRuntimeClass reaches every service when the RuntimeClass of the setting appears or goes: a service that waited
// for it starts. Other classes reach none.
func (r *GeassAppReconciler) appsForRuntimeClass(ctx context.Context, class client.Object) []ctrl.Request {
	setting, err := platform.SandboxSetting(ctx, r.Client)
	if err != nil || setting == nil || setting.RuntimeClassName != class.GetName() {
		return nil
	}
	return r.allApps(ctx, class)
}

// appsForReservation reaches every service when a node's tags or reservations change while reserved nodes may run without
// the sandbox, since that can change which services certainly run on their organization's reserved nodes. Otherwise none.
func (r *GeassAppReconciler) appsForReservation(ctx context.Context, object client.Object) []ctrl.Request {
	setting, err := platform.SandboxSetting(ctx, r.Client)
	if err != nil || setting == nil || !setting.SkipOnReservedNodes {
		return nil
	}
	return r.allApps(ctx, object)
}

// appsForOrganization reaches the services of the organization's projects. It reaches every service while reserved nodes
// may run without the sandbox, since an organization that comes to require another's reserved tag makes that other's
// services no longer certainly on nodes of their own, and while the registry policy is set, since an organization's
// registry is then a part of a registry that no other organization's service may use.
func (r *GeassAppReconciler) appsForOrganization(ctx context.Context, organization client.Object) []ctrl.Request {
	var config geassv1alpha1.GeassPlatformConfig
	if err := r.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config); err == nil {
		sandbox := platform.SandboxOf(&config)
		if (sandbox != nil && sandbox.SkipOnReservedNodes) || platform.RegistryPolicyOf(&config) != "" {
			return r.allApps(ctx, organization)
		}
	}
	return forOrganizationsProjects(r.Client, r.appsForProject)(ctx, organization)
}
