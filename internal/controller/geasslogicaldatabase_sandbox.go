package controller

import (
	"context"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// The Jobs that make and drop a logical database log in to its server as its administrator and work on an organization's
// data, so with spec.tenancy.sandbox set they run under its RuntimeClass, and are not made while it is missing. The
// logical database's Sandboxed condition says how the Job that made it ran, or that it waits.

// logicalJobSandbox is how the Jobs of a logical database run: as its server's pods do when the server is an
// organization's, and otherwise as the pods of the database's project, so that a Job on an organization's data is
// sandboxed whoever owns the server (while organizations are off, the platform's server may hold an organization's).
func (r *GeassLogicalDatabaseReconciler) logicalJobSandbox(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase, server *geassv1alpha1.GeassDatabase) (platform.Sandbox, error) {
	selector := jobPlacement(pinnedNodePlacement(server)).NodeSelector()
	sandbox, err := platform.ResolveDatabaseSandbox(ctx, r.Client, server, selector)
	if err != nil || sandbox.Applies {
		return sandbox, err
	}
	return platform.ResolveSandbox(ctx, r.Client, logical.Spec.Project, selector)
}

// jobSandboxUnlessMade decides how the Job is made when it is not made yet, and records it on the logical database. A Job
// that is made is left as it runs, and nothing is decided for it.
func (r *GeassLogicalDatabaseReconciler) jobSandboxUnlessMade(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase, server *geassv1alpha1.GeassDatabase, job *batchv1.Job) (platform.Sandbox, error) {
	switch err := r.Get(ctx, client.ObjectKeyFromObject(job), &batchv1.Job{}); {
	case err == nil:
		return platform.Sandbox{}, nil
	case !apierrors.IsNotFound(err):
		return platform.Sandbox{}, err
	}
	sandbox, err := r.logicalJobSandbox(ctx, logical, server)
	if err != nil {
		return sandbox, err
	}
	return sandbox, r.recordJobSandbox(ctx, logical, sandbox, nil)
}

// waitForSandbox holds a logical database whose Job cannot be made without the RuntimeClass: nothing is made, it says what
// it waits for, without counting as a failure, and it is looked at again every half minute.
func (r *GeassLogicalDatabaseReconciler) waitForSandbox(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase, missing *platform.SandboxRuntimeMissingError) (ctrl.Result, error) {
	if err := r.recordJobSandbox(ctx, logical, platform.Sandbox{}, missing); err != nil {
		return ctrl.Result{}, err
	}
	return r.setNotReady(ctx, logical, missing.Error())
}

// recordJobSandbox writes how the logical database's Job runs, or why it waits, onto its status, with one warning event
// when a wait begins. Nothing is read or written for one that has nothing to say and says nothing.
func (r *GeassLogicalDatabaseReconciler) recordJobSandbox(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase, sandbox platform.Sandbox, missing *platform.SandboxRuntimeMissingError) error {
	if _, _, _, ok := sandbox.Condition(); !ok && missing == nil && meta.FindStatusCondition(logical.Status.Conditions, platform.ConditionSandboxed) == nil {
		return nil
	}
	latest := &geassv1alpha1.GeassLogicalDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(logical), latest); err != nil {
		return client.IgnoreNotFound(err)
	}
	began := missing != nil && !waitingForSandbox(latest.Status.Conditions)
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.Conditions = sandboxConditions(latest.Status.Conditions, sandbox, missing)
	}); err != nil {
		return err
	}
	logical.Status.Conditions = latest.Status.Conditions
	if began {
		warnSandboxMissing(r.Recorder, latest, missing)
	}
	return nil
}
