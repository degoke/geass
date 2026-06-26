/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/helmchart"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

const cnpgOperatorValues = `config:
  data: {}
`

const databaseFinalizer = platform.FinalizerDatabase

// GeassDatabaseReconciler reconciles a GeassDatabase object.
type GeassDatabaseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdatabases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdatabases/finalizers,verbs=update
// +kubebuilder:rbac:groups=helm.cattle.io,resources=helmcharts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *GeassDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var db geassv1alpha1.GeassDatabase
	if err := r.Get(ctx, req.NamespacedName, &db); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if db.Spec.Engine != geassv1alpha1.DatabaseEnginePostgres {
		return r.setNotReady(ctx, &db, fmt.Sprintf("unsupported engine %q", db.Spec.Engine))
	}

	wsNS, err := platform.WorkspaceNamespace(string(db.Spec.Workspace))
	if err != nil {
		return r.setNotReady(ctx, &db, err.Error())
	}

	if !controllerutil.ContainsFinalizer(&db, databaseFinalizer) {
		controllerutil.AddFinalizer(&db, databaseFinalizer)
		return ctrl.Result{}, r.Update(ctx, &db)
	}

	if !db.DeletionTimestamp.IsZero() {
		if err := r.deleteWorkspaceResources(ctx, &db, wsNS); err != nil {
			return r.setNotReady(ctx, &db, err.Error())
		}
		controllerutil.RemoveFinalizer(&db, databaseFinalizer)
		return ctrl.Result{}, r.Update(ctx, &db)
	}

	if prevNS, moved := previousWorkspaceNamespace(db.Status.WorkspaceNamespace, wsNS); moved {
		if err := r.deleteWorkspaceResources(ctx, &db, prevNS); err != nil {
			return r.setNotReady(ctx, &db, err.Error())
		}
	}

	if err := r.ensureCNPGOperator(ctx); err != nil {
		return r.setNotReady(ctx, &db, err.Error())
	}
	operatorReady, err := r.cnpgOperatorReady(ctx)
	if err != nil {
		return r.setNotReady(ctx, &db, err.Error())
	}
	if !operatorReady {
		log.Info("Waiting for CloudNativePG operator")
		return r.setNotReady(ctx, &db, "CloudNativePG operator is not ready")
	}

	if err := r.reconcileBootstrapSecret(ctx, &db, wsNS); err != nil {
		return r.setNotReady(ctx, &db, err.Error())
	}
	if err := r.reconcileCNPGCluster(ctx, &db, wsNS); err != nil {
		return r.setNotReady(ctx, &db, err.Error())
	}

	cluster := &cnpgv1.Cluster{}
	if err := r.Get(ctx, client.ObjectKey{Name: db.Name, Namespace: wsNS}, cluster); err != nil {
		return r.setNotReady(ctx, &db, "CNPG cluster was not created")
	}
	if !cnpgClusterReady(cluster) {
		return r.setNotReady(ctx, &db, "Postgres cluster is not healthy yet")
	}

	if err := r.reconcileConnectionSecret(ctx, &db, wsNS); err != nil {
		return r.setNotReady(ctx, &db, err.Error())
	}

	host := fmt.Sprintf("%s-rw.%s.svc", db.Name, wsNS)
	latest := db.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(&db), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.WorkspaceNamespace = wsNS
	latest.Status.ConnectionSecret = db.Name + "-connection"
	latest.Status.Host = host
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "DatabaseReady", "Postgres database is ready")
	return ctrl.Result{}, r.Status().Update(ctx, latest)
}

func (r *GeassDatabaseReconciler) deleteWorkspaceResources(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) error {
	_ = client.IgnoreNotFound(r.Delete(ctx, &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-bootstrap", Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-connection", Namespace: wsNS}}))
	return nil
}

func (r *GeassDatabaseReconciler) ensureCNPGOperator(ctx context.Context) error {
	spec := helmv1.HelmChartSpec{
		Chart:           platform.CNPGReleaseChart,
		Repo:            platform.CNPGChartRepo,
		Version:         platform.CNPGChartVersion,
		TargetNamespace: platform.CNPGTargetNS,
		CreateNamespace: true,
		ValuesContent:   cnpgOperatorValues,
	}
	return helmchart.Ensure(ctx, r.Client, platform.CNPGChartName, spec)
}

func (r *GeassDatabaseReconciler) cnpgOperatorReady(ctx context.Context) (bool, error) {
	chart, err := helmchart.Get(ctx, r.Client, platform.CNPGChartName)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return helmchart.IsReady(chart), nil
}

func (r *GeassDatabaseReconciler) reconcileBootstrapSecret(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-bootstrap", Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, db, "GeassDatabase")
		if secret.StringData == nil {
			secret.StringData = map[string]string{}
		}
		if _, ok := secret.StringData["username"]; !ok {
			secret.StringData["username"] = "app"
		}
		if _, ok := secret.StringData["password"]; !ok {
			secret.StringData["password"] = db.Name + "-password"
		}
		return setSameNamespaceOwner(db, secret, r.Scheme)
	})
	return err
}

func (r *GeassDatabaseReconciler) reconcileCNPGCluster(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) error {
	instances := int32(1)
	if db.Spec.Instances != nil {
		instances = *db.Spec.Instances
	}
	version := db.Spec.Version
	if version == "" {
		version = "16"
	}
	storageSize := "10Gi"
	if db.Spec.StorageSize != nil {
		storageSize = db.Spec.StorageSize.String()
	}

	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cluster, func() error {
		applyGeassLabels(cluster, db, "GeassDatabase")
		cluster.Spec = cnpgv1.ClusterSpec{
			Instances: instances,
			ImageName: fmt.Sprintf("ghcr.io/cloudnative-pg/postgresql:%s", version),
			Storage:   cnpgv1.StorageConfiguration{Size: storageSize},
			Bootstrap: cnpgv1.BootstrapConfiguration{
				InitDB: cnpgv1.InitDBConfiguration{
					Database: db.Name,
					Owner:    "app",
					Secret:   corev1.LocalObjectReference{Name: db.Name + "-bootstrap"},
				},
			},
		}
		return setSameNamespaceOwner(db, cluster, r.Scheme)
	})
	return err
}

func (r *GeassDatabaseReconciler) reconcileConnectionSecret(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) error {
	bootstrap := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: db.Name + "-bootstrap", Namespace: wsNS}, bootstrap); err != nil {
		return err
	}
	username := string(bootstrap.Data["username"])
	password := string(bootstrap.Data["password"])
	if username == "" {
		username = bootstrap.StringData["username"]
	}
	if password == "" {
		password = bootstrap.StringData["password"]
	}
	host := fmt.Sprintf("%s-rw.%s.svc", db.Name, wsNS)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-connection", Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, db, "GeassDatabase")
		secret.StringData = map[string]string{
			"host":     host,
			"port":     "5432",
			"database": db.Name,
			"username": username,
			"password": password,
			"uri":      fmt.Sprintf("postgresql://%s:%s@%s:5432/%s", username, password, host, db.Name),
		}
		return setSameNamespaceOwner(db, secret, r.Scheme)
	})
	return err
}

func (r *GeassDatabaseReconciler) setNotReady(ctx context.Context, db *geassv1alpha1.GeassDatabase, message string) (ctrl.Result, error) {
	latest := db.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(db), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionFalse, "ReconcileError", message)
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: platform.RequeueAfterDefault}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassDatabase{}).
		Named("geassdatabase").
		Complete(r)
}
