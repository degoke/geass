package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// A database with no project is the cluster-wide server: one Postgres or MySQL that projects share, each
// taking logical databases of its own on it. It lives in the system namespace, as the cluster MinIO does,
// and has no project to deploy from, to back up into, or to take its region from.

func isSharedDatabase(db *geassv1alpha1.GeassDatabase) bool {
	return platform.IsSharedDatabase(db)
}

// databaseNamespace is where a database's workloads live: its project's environment namespace, or the
// system namespace for the cluster-wide server.
func databaseNamespace(db *geassv1alpha1.GeassDatabase) (string, error) {
	if isSharedDatabase(db) {
		return platform.SystemNamespace, nil
	}
	return resourceNamespace(db.Spec.Project, string(db.Spec.Environment))
}

// databaseNeedsAdminLogin reports whether the server must give out its administrative login. Creating a
// database and a login for it takes one: the application's own login only owns its database. Only
// servers that host logical databases get it, so no other database has a superuser secret beside it.
func (r *GeassDatabaseReconciler) databaseNeedsAdminLogin(ctx context.Context, db *geassv1alpha1.GeassDatabase) bool {
	if isSharedDatabase(db) {
		return true
	}
	var logical geassv1alpha1.GeassLogicalDatabaseList
	if err := r.List(ctx, &logical, client.InNamespace(platform.SystemNamespace)); err != nil {
		return false
	}
	for i := range logical.Items {
		if logical.Items[i].Spec.ServerRef == db.Name {
			return true
		}
	}
	return false
}

// databasesForLogicalDatabase maps a logical database to the server it lives on, so a server learns it
// has been given one (and so must provide an administrative login) without waiting for a resync.
func (r *GeassDatabaseReconciler) databasesForLogicalDatabase(_ context.Context, obj client.Object) []reconcile.Request {
	logical, ok := obj.(*geassv1alpha1.GeassLogicalDatabase)
	if !ok || logical.Spec.ServerRef == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: logical.Spec.ServerRef, Namespace: logical.Namespace}}}
}
