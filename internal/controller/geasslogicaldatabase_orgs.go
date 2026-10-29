package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	// messageMixingRefused ends what a database that would mix organizations says: it is not made.
	messageMixingRefused = ". It is not made: create it on a server of the project's own organization (for a project of none, one of the platform's), " +
		"which a platform administrator can set up, or have them give the server or the project to one organization"
	// messageMixingKept ends what a database that was made before says: it is left as it is.
	messageMixingKept = ". It was made before this was checked, so it is kept as it is and still served, and listed for the platform's administrators to move"
)

// keepToOneOrganization holds a shared server to one organization while organizations are on (platform.SharedServerAllows).
// A database that would put a project's data on a server of another organization, an organization's on the platform's
// server or the platform's on an organization's, is not made: nothing runs on the server and no login is created. One that
// was made before (before the rule, or from objects written past the dashboard) holds data the project's services may be
// using, so it is left as it is and goes on being served, and is reported instead. Either way its condition
// OrganizationIsolated and a Warning event name both organizations, and the platform's administrators list it
// (GET /api/shared-databases/mixed). Nothing is deleted or moved here.
//
// stop says the reconcile ends here, with result.
func (r *GeassLogicalDatabaseReconciler) keepToOneOrganization(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase, server *geassv1alpha1.GeassDatabase) (result ctrl.Result, stop bool, err error) {
	var project geassv1alpha1.GeassProject
	if err := r.Get(ctx, client.ObjectKey{Name: logical.Spec.Project, Namespace: platform.SystemNamespace}, &project); err != nil {
		return ctrl.Result{}, true, err
	}
	mixing := platform.SharedServerMixing(server.Name, server.Spec.Org, project.Name, project.Spec.Org)
	if mixing == "" {
		return ctrl.Result{}, false, r.clearOrganizationMixing(ctx, logical)
	}
	// What was made is what has a connection secret: it is written once the database and its login are on the server.
	made := logical.Status.ConnectionSecret != ""
	message, action := mixing+messageMixingRefused, "Refused"
	if made {
		message, action = mixing+messageMixingKept, "Kept"
	}
	latest := &geassv1alpha1.GeassLogicalDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(logical), latest); err != nil {
		return ctrl.Result{}, true, client.IgnoreNotFound(err)
	}
	wrote, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type: platform.ConditionOrganizationIsolated, Status: metav1.ConditionFalse, Reason: platform.ReasonMixedOrganizations,
			Message: message, ObservedGeneration: latest.Generation,
		})
	})
	if err != nil {
		return ctrl.Result{}, true, err
	}
	if wrote {
		// Said once, when it is first seen or changes, and not on every pass.
		logf.FromContext(ctx).Info("Found logical database on a shared server of another organization", "logicalDatabase", logical.Name,
			"server", server.Name, "serverOrganization", server.Spec.Org, "project", project.Name, "projectOrganization", project.Spec.Org, "made", made)
		if r.Recorder != nil {
			r.Recorder.Eventf(latest, server, corev1.EventTypeWarning, platform.ReasonMixedOrganizations, action, "%s", message)
		}
	}
	if made {
		return ctrl.Result{}, false, nil
	}
	result, err = r.setNotReady(ctx, logical, message)
	return result, true, err
}

// clearOrganizationMixing takes the condition away from a database that no longer mixes organizations, or whose
// organizations are off.
func (r *GeassLogicalDatabaseReconciler) clearOrganizationMixing(ctx context.Context, logical *geassv1alpha1.GeassLogicalDatabase) error {
	if meta.FindStatusCondition(logical.Status.Conditions, platform.ConditionOrganizationIsolated) == nil {
		return nil
	}
	latest := &geassv1alpha1.GeassLogicalDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(logical), latest); err != nil {
		return client.IgnoreNotFound(err)
	}
	wrote, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		meta.RemoveStatusCondition(&latest.Status.Conditions, platform.ConditionOrganizationIsolated)
	})
	if wrote {
		logf.FromContext(ctx).Info("Logical database no longer shares a server with another organization", "logicalDatabase", logical.Name)
	}
	return err
}

// logicalDatabasesOnServer maps a shared server to the databases on it: whose the server is decides whose they may be.
func (r *GeassLogicalDatabaseReconciler) logicalDatabasesOnServer(ctx context.Context, server client.Object) []reconcile.Request {
	var list geassv1alpha1.GeassLogicalDatabaseList
	if err := r.List(ctx, &list, client.InNamespace(server.GetNamespace())); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for i := range list.Items {
		if list.Items[i].Spec.ServerRef == server.GetName() {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
	}
	return requests
}

// isSharedServer is whether an object is a shared database server, one with no project.
func isSharedServer(obj client.Object) bool {
	db, ok := obj.(*geassv1alpha1.GeassDatabase)
	return ok && isSharedDatabase(db)
}

// everyLogicalDatabase maps a change of the platform's settings to every logical database.
func (r *GeassLogicalDatabaseReconciler) everyLogicalDatabase(ctx context.Context, _ client.Object) []reconcile.Request {
	var list geassv1alpha1.GeassLogicalDatabaseList
	if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return requests
}

// organizationsToggled lets through a change of the platform's settings that turns organizations on or off, which is all a
// logical database reads of them.
func organizationsToggled() predicate.Predicate {
	tenancy := func(obj client.Object) bool {
		config, ok := obj.(*geassv1alpha1.GeassPlatformConfig)
		return ok && platform.TenancyEnabled(*config)
	}
	return predicate.Funcs{
		UpdateFunc:  func(e event.UpdateEvent) bool { return tenancy(e.ObjectOld) != tenancy(e.ObjectNew) },
		CreateFunc:  func(e event.CreateEvent) bool { return tenancy(e.Object) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return tenancy(e.Object) },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}
