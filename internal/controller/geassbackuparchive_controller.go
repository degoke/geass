/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	// archivePurgedCondition is False while backups past their deadline could not be removed.
	archivePurgedCondition = "Purged"
	// archivePurgeRetryInterval is how long to wait before trying to remove backups again.
	archivePurgeRetryInterval = 15 * time.Minute
)

// GeassBackupArchiveReconciler removes backups once their retention window has passed. It is the
// only code that deletes backup data, and it never touches an archive whose database is alive.
type GeassBackupArchiveReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	HTTP   *http.Client

	// newBackupStorage builds the AWS client used to delete backups; tests replace it.
	newBackupStorage backupStorageFactory
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassbackuparchives,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassbackuparchives/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdatabases,verbs=get;list;watch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscloudconnections,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *GeassBackupArchiveReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var archive geassv1alpha1.GeassBackupArchive
	if err := r.Get(ctx, req.NamespacedName, &archive); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if archive.Spec.DeleteAfter == nil {
		// The archive belongs to a running database. If that database is gone without the usual
		// clean-up having run, give its backups the usual grace period instead of losing them.
		orphaned, err := r.databaseIsGone(ctx, &archive)
		if err != nil || !orphaned {
			return ctrl.Result{}, err
		}
		logf.FromContext(ctx).Info("Retired backups of a database that no longer exists", "archive", archive.Name, "database", archive.Spec.Database)
		return ctrl.Result{}, retireArchive(ctx, r.Client, &archive, time.Now())
	}

	if remaining := time.Until(archive.Spec.DeleteAfter.Time); remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, nil
	}
	return r.purge(ctx, &archive)
}

// databaseIsGone reports whether the database that owns an archive no longer exists. A database
// that is merely being deleted still owns it: its controller is taking the final backup.
func (r *GeassBackupArchiveReconciler) databaseIsGone(ctx context.Context, archive *geassv1alpha1.GeassBackupArchive) (bool, error) {
	var db geassv1alpha1.GeassDatabase
	err := r.Get(ctx, client.ObjectKey{Name: archive.Spec.Database, Namespace: platform.SystemNamespace}, &db)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return string(db.UID) != archive.Spec.DatabaseUID, nil
}

// purge deletes an archive's backups from storage and then the archive record. If anything stops
// it, the record stays so the data is never forgotten while it still exists.
func (r *GeassBackupArchiveReconciler) purge(ctx context.Context, archive *geassv1alpha1.GeassBackupArchive) (ctrl.Result, error) {
	connection, err := resolveAWSConnection(ctx, r.Client, archive.Spec.Project, archive.Spec.Storage.ConnectionRef.Name, archive.Spec.Storage.Region)
	if err != nil {
		return r.purgeFailed(ctx, archive, "ConnectionUnavailable", summarizeStorageError(err))
	}
	storage := r.backupStorage(connection.accessKey, connection.secretKey, connection.region)
	prefix := platform.BackupObjectPrefix(archive.Spec.Project, archive.Spec.ServerName)
	if err := storage.DeletePrefix(archive.Spec.Storage.Bucket, prefix); err != nil {
		return r.purgeFailed(ctx, archive, "PurgeFailed", summarizeStorageError(err))
	}
	logf.FromContext(ctx).Info("Removed backups past their retention", "archive", archive.Name, "database", archive.Spec.Database)
	return ctrl.Result{}, client.IgnoreNotFound(r.Delete(ctx, archive))
}

func (r *GeassBackupArchiveReconciler) purgeFailed(ctx context.Context, archive *geassv1alpha1.GeassBackupArchive, reason, message string) (ctrl.Result, error) {
	latest := &geassv1alpha1.GeassBackupArchive{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(archive), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, archivePurgedCondition, metav1.ConditionFalse, reason, message)
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: archivePurgeRetryInterval}, nil
}

func (r *GeassBackupArchiveReconciler) backupStorage(accessKey, secretKey, region string) backupStorageAPI {
	if r.newBackupStorage != nil {
		return r.newBackupStorage(accessKey, secretKey, region)
	}
	return awsBackupStorageFactory(r.HTTP)(accessKey, secretKey, region)
}

// archivesForDatabase maps a database event to the archives it owns, so an archive notices when
// its database disappears.
func (r *GeassBackupArchiveReconciler) archivesForDatabase(ctx context.Context, db client.Object) []ctrl.Request {
	var archives geassv1alpha1.GeassBackupArchiveList
	if err := r.List(ctx, &archives, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil
	}
	var requests []ctrl.Request
	for i := range archives.Items {
		if archives.Items[i].Spec.Database == db.GetName() {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&archives.Items[i])})
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassBackupArchiveReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassBackupArchive{}).
		// An archive only needs to know its database is going or gone, not each of the database's status writes.
		Watches(&geassv1alpha1.GeassDatabase{}, handler.EnqueueRequestsFromMapFunc(r.archivesForDatabase), builder.WithPredicates(databaseGoneOrDeleting())).
		Named("geassbackuparchive").
		Complete(r)
}
