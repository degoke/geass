/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilptr "k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloud"
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
	HTTP   *http.Client
	// PodUsage is what the cluster's pods ask of its nodes, for the check that a new database fits. Nil reads the pods.
	PodUsage *platform.PodUsageIndex

	// newBackupStorage builds the AWS client used for backup storage; tests replace it.
	newBackupStorage backupStorageFactory
	// clock returns the current time; tests replace it. Nil means time.Now.
	clock func() time.Time
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdatabases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdatabases/finalizers,verbs=update
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasshareadinesses,verbs=get;list;watch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscloudconnections,verbs=get;list;watch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassprojects,verbs=get;list;watch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassbackuparchives,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassbackuparchives/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=helm.cattle.io,resources=helmcharts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=postgresql.cnpg.io,resources=backups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=postgresql.cnpg.io,resources=scheduledbackups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

func (r *GeassDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var db geassv1alpha1.GeassDatabase
	if err := r.Get(ctx, req.NamespacedName, &db); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if result, paused := beginProvisionReconcileUnlessDeleting(!db.DeletionTimestamp.IsZero(), db.Status.Conditions, db.Generation, &db.Status.ReconcileRetry); paused {
		return result, nil
	}
	// A database being deleted must always be able to release its finalizer, even after its
	// environment or cloud connection was removed from the project.
	if db.DeletionTimestamp.IsZero() {
		if err := r.validateDatabaseCloudConnection(ctx, &db); err != nil {
			return r.setNotReady(ctx, &db, err.Error())
		}
		// The cluster-wide server belongs to no project, so there is no project to check.
		if !isSharedDatabase(&db) {
			if err := platform.ValidateProjectPlacement(ctx, r.Client, db.Spec.Project, db.Spec.Environment); err != nil {
				return r.setNotReady(ctx, &db, err.Error())
			}
		}
	}

	wsNS, err := databaseNamespace(&db)
	if err != nil {
		return r.setNotReady(ctx, &db, err.Error())
	}

	if !controllerutil.ContainsFinalizer(&db, databaseFinalizer) {
		controllerutil.AddFinalizer(&db, databaseFinalizer)
		return ctrl.Result{}, r.Update(ctx, &db)
	}

	if !db.DeletionTimestamp.IsZero() {
		// The last backup is taken before anything is removed, so the data is never touched
		// while it is still being saved.
		requeue, err := r.finalizeBackups(ctx, &db, wsNS)
		if err != nil {
			return ctrl.Result{}, err
		}
		if requeue > 0 {
			return ctrl.Result{RequeueAfter: requeue}, nil
		}
		r.deleteTargetResources(ctx, &db, wsNS)
		controllerutil.RemoveFinalizer(&db, databaseFinalizer)
		return ctrl.Result{}, r.Update(ctx, &db)
	}

	if prevNS, moved := previousTargetNamespace(db.Status.TargetNamespace, wsNS); moved {
		r.deleteTargetResources(ctx, &db, prevNS)
		// The old cluster is gone, so its backup folder must not be reused by the new one.
		if err := r.retireLiveArchive(ctx, &db); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := applyDatabaseDeployedSpec(&db); err != nil {
		if errors.Is(err, ErrAwaitingDeploy) {
			r.deleteTargetResources(ctx, &db, wsNS)
			if retireErr := r.retireLiveArchive(ctx, &db); retireErr != nil {
				return ctrl.Result{}, retireErr
			}
			return r.setNotReady(ctx, &db, waitingForDeployMessage)
		}
		return r.setNotReady(ctx, &db, err.Error())
	}

	if databasePlacement(&db) == geassv1alpha1.DatabasePlacementExternal {
		return r.reconcileExternal(ctx, &db, wsNS)
	}

	if databaseWantsHA(&db) {
		ready, err := r.haReady(ctx)
		if err != nil {
			return r.setNotReady(ctx, &db, err.Error())
		}
		if !ready {
			return r.setNotReady(ctx, &db, "High availability requires a cluster with at least three healthy nodes")
		}
	}

	// Placement comes first: a database in a region gets that region's storage class.
	placement, err := r.databaseNodePlacement(ctx, &db)
	if err != nil {
		return r.setNotReady(ctx, &db, err.Error())
	}

	storageClass, waitForStorage, err := r.databaseStorageClass(ctx, &db, wsNS)
	if err != nil {
		return r.setNotReady(ctx, &db, storageFailureMessage(err))
	}
	if waitForStorage {
		return r.setNotReady(ctx, &db, messageStorageStarting)
	}

	switch db.Spec.Engine {
	case geassv1alpha1.DatabaseEnginePostgres, "":
		return r.reconcilePostgres(ctx, &db, wsNS, storageClass, placement, log.WithValues("engine", "Postgres"))
	case geassv1alpha1.DatabaseEngineMySQL:
		return r.reconcileHelmDatabase(ctx, &db, wsNS, mysqlChartName(db.Name), platform.MySQLReleaseChart, platform.MySQLChartRepo, platform.MySQLChartVersion, mysqlValues(&db, storageClass, placement), mysqlHost(&db, wsNS), platform.MySQLDefaultPort, "mysql", placement)
	case geassv1alpha1.DatabaseEngineRedis:
		return r.reconcileHelmDatabase(ctx, &db, wsNS, redisDatabaseChartName(db.Name), platform.RedisReleaseChart, platform.RedisChartRepo, platform.RedisChartVersion, redisDatabaseValues(&db, placement), redisDatabaseHost(&db, wsNS), platform.RedisDefaultPort, "redis", placement)
	case geassv1alpha1.DatabaseEngineSQLite:
		return r.reconcileSQLite(ctx, &db, wsNS, storageClass, placement)
	default:
		return r.setNotReady(ctx, &db, fmt.Sprintf("unsupported engine %q", db.Spec.Engine))
	}
}

func databasePlacement(db *geassv1alpha1.GeassDatabase) geassv1alpha1.GeassDatabasePlacement {
	if db.Spec.Placement == geassv1alpha1.DatabasePlacementExternal {
		return geassv1alpha1.DatabasePlacementExternal
	}
	return geassv1alpha1.DatabasePlacementInCluster
}

func databaseWantsHA(db *geassv1alpha1.GeassDatabase) bool {
	return platform.DatabaseWantsHA(db)
}

func databaseInstances(db *geassv1alpha1.GeassDatabase) int32 {
	if db.Spec.HighAvailability {
		if db.Spec.Instances != nil && *db.Spec.Instances > 3 {
			return *db.Spec.Instances
		}
		return 3
	}
	if db.Spec.Instances != nil && *db.Spec.Instances > 0 {
		return *db.Spec.Instances
	}
	return 1
}

func databaseName(db *geassv1alpha1.GeassDatabase) string {
	if strings.TrimSpace(db.Spec.DatabaseName) != "" {
		return db.Spec.DatabaseName
	}
	return db.Name
}

func (r *GeassDatabaseReconciler) reconcilePostgres(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string, placement platform.Placement, log interface{ Info(string, ...any) }) (ctrl.Result, error) {
	if ok, msg := workloadFitsPlacementUnlessCreated(ctx, r.Client, r.PodUsage, &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}, databaseCapacityEstimate(db), placement); !ok {
		return r.setNotReady(ctx, db, msg)
	}
	if err := r.ensureCNPGOperator(ctx); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	operatorReady, operatorMsg, err := r.cnpgOperatorReady(ctx)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if !operatorReady {
		log.Info("Waiting for CloudNativePG operator", "message", operatorMsg)
		return r.setNotReady(ctx, db, operatorMsg)
	}
	if err := r.reconcileBootstrapSecret(ctx, db, wsNS); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	plan, err := r.planBackups(ctx, db, wsNS)
	backupsFailed := err != nil
	if backupsFailed {
		// Backups are an add-on: a problem working them out must not take a database out of service.
		// The degraded plan keeps whatever archiving is already configured on the cluster.
		backupProblem(ctx, db, err)
		plan = &backupPlan{degraded: true}
	}
	recovery, err := r.recoveryForNewCluster(ctx, db, wsNS)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	cluster, err := r.reconcileCNPGCluster(ctx, db, wsNS, storageClass, placement, plan, recovery)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if !cnpgClusterReady(cluster) {
		if db.Spec.RestoreFrom != nil && conditionStatus(db.Status.Conditions, platform.ConditionReady) != string(metav1.ConditionTrue) {
			return r.setNotReady(ctx, db, messageRestoringDatabase)
		}
		return r.setNotReady(ctx, db, "Postgres database is starting")
	}
	if err := r.reconcileBackupLifecycle(ctx, db, wsNS, cluster, plan); err != nil {
		backupProblem(ctx, db, err)
		backupsFailed = true
	}
	host := fmt.Sprintf("%s-rw.%s.svc", db.Name, wsNS)
	if err := r.reconcileGeneratedConnectionSecret(ctx, db, wsNS, host, platform.PostgresDefaultPort, "postgresql"); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	result, err := r.setReady(ctx, db, wsNS, host, "Postgres database is ready")
	switch {
	case err != nil:
	case backupsFailed:
		result.RequeueAfter = backupRetryInterval
	case plan != nil:
		// Backup progress is read from the cluster's status, which this controller does not watch.
		result.RequeueAfter = platform.BackupStatusRefreshInterval
	}
	return result, err
}

// recoveryForNewCluster plans a restore, but only while the cluster does not exist yet: recovery is
// how a cluster is created, and once it runs the backups it came from are no longer needed.
func (r *GeassDatabaseReconciler) recoveryForNewCluster(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (*recoveryPlan, error) {
	if db.Spec.RestoreFrom == nil {
		return nil, nil
	}
	err := r.Get(ctx, client.ObjectKey{Name: db.Name, Namespace: wsNS}, &cnpgv1.Cluster{})
	if err == nil {
		return nil, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	return r.planRecovery(ctx, db, wsNS)
}

func (r *GeassDatabaseReconciler) reconcileHelmDatabase(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, chartName, chart, repo, version, values, host, port, scheme string, placement platform.Placement) (ctrl.Result, error) {
	if ok, msg := workloadFitsPlacementUnlessCreated(ctx, r.Client, r.PodUsage, &helmv1.HelmChart{ObjectMeta: metav1.ObjectMeta{Name: chartName, Namespace: platform.HelmChartNamespace}}, databaseCapacityEstimate(db), placement); !ok {
		return r.setNotReady(ctx, db, msg)
	}
	if err := r.reconcileBootstrapSecret(ctx, db, wsNS); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	spec := helmv1.HelmChartSpec{
		Chart:           chart,
		Repo:            repo,
		Version:         version,
		TargetNamespace: wsNS,
		CreateNamespace: false,
		ValuesContent:   values,
	}
	if err := helmchart.Ensure(ctx, r.Client, chartName, spec); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	helmChart, err := helmchart.Get(ctx, r.Client, chartName)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	ready, err := helmChartReady(ctx, r.Client, helmChart)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if !ready {
		return r.setNotReady(ctx, db, installingChartMessage(string(db.Spec.Engine)+" database"))
	}
	workloadReady, waitMsg, err := helmReleaseWorkloadReady(ctx, r.Client, wsNS, chartName, databaseInstances(db))
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if !workloadReady {
		if waitMsg == "" {
			waitMsg = messageServiceStarting
		}
		return r.setNotReady(ctx, db, waitMsg)
	}
	if err := r.reconcileGeneratedConnectionSecret(ctx, db, wsNS, host, port, scheme); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	var backups dumpOutcome
	if databaseBackupKind(db) == platform.BackupKindSnapshot {
		var err error
		if backups, err = r.reconcileDumpBackups(ctx, db, wsNS); err != nil {
			return r.setNotReady(ctx, db, err.Error())
		}
		if backups.waiting != "" {
			return r.setNotReady(ctx, db, backups.waiting)
		}
	}
	return r.setReadyWithBackups(ctx, db, wsNS, host, string(db.Spec.Engine)+" database is ready", backups)
}

// setReadyWithBackups marks the database ready. While it has backups it is looked at again, because
// how they are doing is read from Jobs and from the bucket, which change without the database changing.
func (r *GeassDatabaseReconciler) setReadyWithBackups(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, host, message string, backups dumpOutcome) (ctrl.Result, error) {
	result, err := r.setReady(ctx, db, wsNS, host, message)
	switch {
	case err != nil:
	case backups.failed:
		result.RequeueAfter = backupRetryInterval
	case backups.backups:
		result.RequeueAfter = platform.BackupStatusRefreshInterval
	}
	return result, err
}

func (r *GeassDatabaseReconciler) reconcileSQLite(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string, placement platform.Placement) (ctrl.Result, error) {
	if db.Spec.HighAvailability {
		return r.setNotReady(ctx, db, "SQLite does not support high availability")
	}
	if ok, msg := workloadFitsPlacementUnlessCreated(ctx, r.Client, r.PodUsage, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}, databaseCapacityEstimate(db), placement); !ok {
		return r.setNotReady(ctx, db, msg)
	}
	if err := r.reconcileBootstrapSecret(ctx, db, wsNS); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	pvc, err := r.ensureSQLitePVC(ctx, db, wsNS, storageClass)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	replicas := int32(1)
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		applyGeassLabels(deploy, db, "GeassDatabase")
		deploy.Spec.Replicas = &replicas
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{platform.K8sLabelAppName: db.Name}}
		deploy.Spec.Template.Labels = map[string]string{platform.K8sLabelAppName: db.Name, platform.LabelManagedBy: platform.ManagedByValue}
		deploy.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:      "sqlite",
			Image:     platform.SQLiteImage,
			Args:      []string{"-http-addr", "0.0.0.0:4001", "-http-adv-addr", fmt.Sprintf("%s.%s.svc:4001", db.Name, wsNS)},
			Ports:     []corev1.ContainerPort{{Name: portNameHTTP, ContainerPort: 4001}},
			Resources: databaseInstanceResources(db),
			VolumeMounts: []corev1.VolumeMount{{
				Name:      "data",
				MountPath: "/rqlite/file",
			}},
		}}
		deploy.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name:         "data",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvc.Name}},
		}}
		placement.ApplyToPodSpec(&deploy.Spec.Template.Spec)
		return setSameNamespaceOwner(db, deploy, r.Scheme)
	}); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		applyGeassLabels(svc, db, "GeassDatabase")
		svc.Spec.Selector = map[string]string{platform.K8sLabelAppName: db.Name}
		svc.Spec.Ports = []corev1.ServicePort{{Name: "http", Port: 4001, TargetPort: intstr.FromInt(4001)}}
		return setSameNamespaceOwner(db, svc, r.Scheme)
	}); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if err := r.Get(ctx, client.ObjectKey{Name: db.Name, Namespace: wsNS}, deploy); err != nil {
		return r.setNotReady(ctx, db, "SQLite deployment was not created")
	}
	if !deploymentReady(deploy, replicas) {
		return r.setNotReady(ctx, db, messageServiceStarting)
	}
	host := fmt.Sprintf("%s.%s.svc", db.Name, wsNS)
	if err := r.reconcileGeneratedConnectionSecret(ctx, db, wsNS, host, platform.SQLiteDefaultPort, "http"); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	backups, err := r.reconcileDumpBackups(ctx, db, wsNS)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if backups.waiting != "" {
		return r.setNotReady(ctx, db, backups.waiting)
	}
	return r.setReadyWithBackups(ctx, db, wsNS, host, "SQLite database is ready", backups)
}

func (r *GeassDatabaseReconciler) reconcileExternal(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (ctrl.Result, error) {
	host, port, username, password, err := r.resolveExternalCredentials(ctx, db)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	database := databaseName(db)
	// revokeLogin undoes a PlanetScale login issued in this pass, for when it cannot be stored.
	var revokeLogin func(context.Context) error
	if db.Spec.Mode == geassv1alpha1.DatabaseModeCreate && db.Spec.Provider == geassv1alpha1.DatabaseProviderPlanetScale {
		connSecret := &corev1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Name: db.Name + "-connection", Namespace: wsNS}, connSecret)
		switch {
		case err == nil:
			// PlanetScale shows the password once, so the login is kept from the secret. The port is not
			// part of it: it comes from the spec or the engine below, so it never goes stale.
			host = secretValue(connSecret, platform.ConnectionKeyHost)
			username = secretValue(connSecret, platform.ConnectionKeyUsername)
			password = secretValue(connSecret, platform.ConnectionKeyPassword)
			// Only a Postgres login names its own database; a MySQL one connects to the database that was created.
			if db.Spec.Engine != geassv1alpha1.DatabaseEngineMySQL {
				database = firstNonEmpty(secretValue(connSecret, platform.ConnectionKeyDatabase), database)
			}
		case apierrors.IsNotFound(err):
			created, revoke, createErr := r.createPlanetScaleDatabase(ctx, db)
			if createErr != nil {
				return r.setNotReady(ctx, db, createErr.Error())
			}
			revokeLogin = revoke
			host, username, password = created.Host, created.Username, created.Password
			database = firstNonEmpty(created.Database, database)
		default:
			return r.setNotReady(ctx, db, err.Error())
		}
	}
	if host == "" || username == "" || password == "" {
		return r.setNotReady(ctx, db, "external database host, username, and password are required")
	}
	if port == "" {
		port = defaultPortForEngine(db.Spec.Engine)
	}
	if err := r.writeConnectionSecret(ctx, db, wsNS, host, port, database, username, password, schemeForEngine(db.Spec.Engine)); err != nil {
		// PlanetScale shows the password once, so a login that was not stored can never be used. It is
		// revoked rather than left behind, because the next pass issues another one.
		if revokeLogin != nil {
			if revokeErr := revokeLogin(ctx); revokeErr != nil {
				logf.FromContext(ctx).Error(revokeErr, "Could not revoke PlanetScale login that was not stored", "database", db.Name)
			}
		}
		return r.setNotReady(ctx, db, err.Error())
	}
	return r.setReady(ctx, db, wsNS, host, string(db.Spec.Provider)+" database is ready")
}

func (r *GeassDatabaseReconciler) resolveExternalCredentials(ctx context.Context, db *geassv1alpha1.GeassDatabase) (host, port, username, password string, err error) {
	host = strings.TrimSpace(db.Spec.ExternalHost)
	if db.Spec.ExternalPort > 0 {
		port = fmt.Sprintf("%d", db.Spec.ExternalPort)
	}
	username = strings.TrimSpace(db.Spec.Username)
	if db.Spec.PasswordSecretRef != nil && db.Spec.PasswordSecretRef.Name != "" {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Name: db.Spec.PasswordSecretRef.Name, Namespace: db.Namespace}, secret); err != nil {
			return "", "", "", "", fmt.Errorf("external password secret %q is unavailable", db.Spec.PasswordSecretRef.Name)
		}
		key := db.Spec.PasswordSecretRef.Key
		if key == "" {
			key = platform.ConnectionKeyPassword
		}
		password = secretValue(secret, key)
	}
	return host, port, username, password, nil
}

const (
	// messagePlanetScaleStarting must read as an in-progress message, so waiting is never counted as a failure.
	messagePlanetScaleStarting = "PlanetScale database is starting"
	// messagePlanetScaleTimedOut is what a database that never became ready reports. Unlike waiting, it counts as a
	// failure, so a database PlanetScale cannot finish ends up failed instead of "starting" forever.
	messagePlanetScaleTimedOut = "PlanetScale database did not become ready"
	// planetScaleStartTimeout is how long a new database may take. PlanetScale needs minutes.
	planetScaleStartTimeout = 30 * time.Minute
)

var errPlanetScaleStarting = errors.New(messagePlanetScaleStarting)

// planetScaleWaitExpired reports whether a database has been waiting on PlanetScale for longer than the timeout.
// The Ready condition keeps its transition time while the message stays the same, so that is how long it has been
// starting. Once it has said it gave up it keeps saying so, which lets the failures count up to a terminal one.
func planetScaleWaitExpired(conditions []metav1.Condition, now time.Time) bool {
	for _, condition := range conditions {
		if condition.Type != platform.ConditionReady || condition.Status != metav1.ConditionFalse {
			continue
		}
		if strings.HasPrefix(condition.Message, messagePlanetScaleTimedOut) {
			return true
		}
		return condition.Message == messagePlanetScaleStarting && now.Sub(condition.LastTransitionTime.Time) > planetScaleStartTimeout
	}
	return false
}

// createPlanetScaleDatabase makes sure the database exists on PlanetScale and, once it is ready, issues a login for
// it. The returned function revokes that login, for when it cannot be stored.
func (r *GeassDatabaseReconciler) createPlanetScaleDatabase(ctx context.Context, db *geassv1alpha1.GeassDatabase) (cloud.PlanetScaleCredentials, func(context.Context) error, error) {
	kind, err := platform.PlanetScaleKind(db.Spec.Engine)
	if err != nil {
		return cloud.PlanetScaleCredentials{}, nil, err
	}
	if db.Spec.ConnectionRef == nil || db.Spec.ConnectionRef.Name == "" {
		return cloud.PlanetScaleCredentials{}, nil, fmt.Errorf("a PlanetScale connection is required")
	}
	connection, secret, err := r.cloudConnection(ctx, db.Spec.Project, db.Spec.ConnectionRef.Name)
	if err != nil {
		return cloud.PlanetScaleCredentials{}, nil, err
	}
	if connection.Spec.Provider != geassv1alpha1.CloudProviderPlanetScale {
		return cloud.PlanetScaleCredentials{}, nil, fmt.Errorf("connection %q is not a PlanetScale connection", connection.Name)
	}
	token, org := platform.PlanetScaleAccess(*connection, secret)
	if token == "" || org == "" {
		return cloud.PlanetScaleCredentials{}, nil, fmt.Errorf("PlanetScale token and organization are required")
	}
	psClient := &cloud.PlanetScaleClient{HTTP: r.HTTP, Token: token, Org: org}
	name := databaseName(db)
	database, err := psClient.EnsureDatabase(ctx, name, kind, strings.TrimSpace(db.Spec.ClusterSize))
	if err != nil {
		return cloud.PlanetScaleCredentials{}, nil, err
	}
	// A new database takes a while to provision and cannot issue logins until then. Waiting costs one
	// request per pass, where a login attempt would fail and count towards giving up on the database.
	if !database.Ready {
		if planetScaleWaitExpired(db.Status.Conditions, r.now()) {
			return cloud.PlanetScaleCredentials{}, nil, fmt.Errorf("%s within %s; check the database in PlanetScale", messagePlanetScaleTimedOut, planetScaleStartTimeout)
		}
		return cloud.PlanetScaleCredentials{}, nil, errPlanetScaleStarting
	}
	creds, err := psClient.CreateCredentials(ctx, name, kind, db.Name)
	if err != nil {
		return cloud.PlanetScaleCredentials{}, nil, err
	}
	creds.Host = firstNonEmpty(creds.Host, db.Spec.ExternalHost)
	creds.Username = firstNonEmpty(creds.Username, db.Spec.Username)
	revoke := func(ctx context.Context) error { return psClient.RevokeCredentials(ctx, name, kind, creds.ID) }
	return creds, revoke, nil
}

func (r *GeassDatabaseReconciler) validateDatabaseCloudConnection(ctx context.Context, db *geassv1alpha1.GeassDatabase) error {
	if db.Spec.ConnectionRef == nil || strings.TrimSpace(db.Spec.ConnectionRef.Name) == "" {
		return nil
	}
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := r.Get(ctx, client.ObjectKey{Name: db.Spec.ConnectionRef.Name, Namespace: platform.SystemNamespace}, connection); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("cloud connection %q was not found", db.Spec.ConnectionRef.Name)
		}
		return fmt.Errorf("cloud connection %q is unavailable", db.Spec.ConnectionRef.Name)
	}
	return platform.ValidateCloudConnectionForProject(*connection, db.Spec.Project)
}

func (r *GeassDatabaseReconciler) cloudConnection(ctx context.Context, project, name string) (*geassv1alpha1.GeassCloudConnection, *corev1.Secret, error) {
	return platform.LoadCloudConnection(ctx, r.Client, project, name)
}

func (r *GeassDatabaseReconciler) haReady(ctx context.Context) (bool, error) {
	var reports geassv1alpha1.GeassHAReadinessList
	if err := r.List(ctx, &reports, client.InNamespace(platform.SystemNamespace)); err != nil {
		return false, err
	}
	for _, report := range reports.Items {
		if conditionStatus(report.Status.Conditions, platform.ConditionReady) == string(metav1.ConditionTrue) && report.Status.HealthyNodes >= 3 {
			return true, nil
		}
	}
	return false, nil
}

func (r *GeassDatabaseReconciler) deleteTargetResources(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) {
	_ = client.IgnoreNotFound(r.Delete(ctx, &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: dumpCronJobName(db), Namespace: wsNS}}))
	_ = r.DeleteAllOf(ctx, &batchv1.Job{}, client.InNamespace(wsNS), client.MatchingLabels{labelBackupFor: db.Name}, client.PropagationPolicy(metav1.DeletePropagationBackground))
	_ = client.IgnoreNotFound(r.Delete(ctx, &cnpgv1.ScheduledBackup{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &cnpgv1.Backup{ObjectMeta: metav1.ObjectMeta{Name: db.Name + finalBackupSuffix, Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-sqlite", Namespace: wsNS}}))
	_ = helmchart.Delete(ctx, r.Client, mysqlChartName(db.Name))
	_ = helmchart.Delete(ctx, r.Client, redisDatabaseChartName(db.Name))
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-bootstrap", Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-connection", Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: db.Name + backupCredentialsSuffix, Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: db.Name + restoreCredentialsSuffix, Namespace: wsNS}}))
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

func (r *GeassDatabaseReconciler) cnpgOperatorReady(ctx context.Context) (bool, string, error) {
	chart, err := helmchart.Get(ctx, r.Client, platform.CNPGChartName)
	if apierrors.IsNotFound(err) {
		return false, installingChartMessage("Postgres operator"), nil
	}
	if err != nil {
		return false, "", err
	}
	installed, err := waitForHelmChart(ctx, r.Client, chart)
	if err != nil {
		return false, "", err
	}
	if !installed {
		return false, installingChartMessage("Postgres operator"), nil
	}
	return helmReleaseWorkloadReady(ctx, r.Client, platform.CNPGTargetNS, platform.CNPGChartName, 1)
}

func (r *GeassDatabaseReconciler) reconcileBootstrapSecret(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-bootstrap", Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, db, "GeassDatabase")
		if secretValue(secret, "username") == "" {
			setSecretStrings(secret, map[string]string{"username": appContainerName})
		}
		if secretValue(secret, "password") == "" {
			setSecretStrings(secret, map[string]string{"password": db.Name + "-password"})
		}
		return setSameNamespaceOwner(db, secret, r.Scheme)
	})
	return err
}

// reconcileCNPGCluster creates or updates the CNPG Cluster and returns it as the
// API server last saw it. Callers must use the returned object rather than
// reading the Cluster back: the manager's client reads from an informer cache
// that lags a create by a few milliseconds, so an immediate Get reports NotFound.
//
// How a cluster is bootstrapped is fixed when it is created and the operator rejects
// changes to it, so an existing cluster keeps its bootstrap, and the source it
// recovered from, exactly as they are. The same goes for storageClass: it applies
// only when the Cluster is first created. An existing Cluster's volumes are already
// bound to a class, CNPG rejects changing it, and the class was pinned from them,
// so the live value is kept as it is.
func (r *GeassDatabaseReconciler) reconcileCNPGCluster(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string, placement platform.Placement, plan *backupPlan, recovery *recoveryPlan) (*cnpgv1.Cluster, error) {
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS},
	}
	adminLogin := r.databaseNeedsAdminLogin(ctx, db)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cluster, func() error {
		applyGeassLabels(cluster, db, "GeassDatabase")
		live := cluster.Spec
		desired := desiredClusterSpec(db, plan, recovery)
		desired.Storage.StorageClass = stringPtrOrNil(storageClass)
		desired.Affinity = cnpgAffinity(placement)
		// Once on, the administrative login stays: taking it away would strand logical databases that
		// are still being created or dropped.
		if adminLogin || utilptr.Deref(live.EnableSuperuserAccess, false) {
			enabled := true
			desired.EnableSuperuserAccess = &enabled
		}
		if cluster.ResourceVersion != "" { // already stored, so its volumes exist
			desired.Storage.StorageClass = live.Storage.StorageClass
			desired.Bootstrap = live.Bootstrap
			desired.ExternalClusters = live.ExternalClusters
			if plan != nil && plan.archive == nil {
				// Backups are wanted but could not be verified this time. Keep archiving as it is
				// rather than stopping it because of a passing storage problem.
				desired.Backup = live.Backup
			}
		}
		cluster.Spec = desired
		return setSameNamespaceOwner(db, cluster, r.Scheme)
	})
	if err != nil {
		return nil, err
	}
	return cluster, nil
}

// ensureSQLitePVC creates or updates the claim holding the SQLite data. The class
// is set only when the claim is created: it is immutable afterwards.
func (r *GeassDatabaseReconciler) ensureSQLitePVC(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string) (*corev1.PersistentVolumeClaim, error) {
	storageSize := platform.DefaultDatabaseStorage(db.Spec.Engine)
	if db.Spec.StorageSize != nil {
		storageSize = db.Spec.StorageSize.String()
	}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-sqlite", Namespace: wsNS}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, pvc, func() error {
		applyGeassLabels(pvc, db, "GeassDatabase")
		pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
		pvc.Spec.Resources.Requests = corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(storageSize)}
		if pvc.ResourceVersion == "" { // not stored yet: the class is immutable afterwards
			pvc.Spec.StorageClassName = stringPtrOrNil(storageClass)
		}
		return setSameNamespaceOwner(db, pvc, r.Scheme)
	})
	if err != nil {
		return nil, err
	}
	return pvc, nil
}

func (r *GeassDatabaseReconciler) reconcileGeneratedConnectionSecret(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, host, port, scheme string) error {
	bootstrap := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: db.Name + "-bootstrap", Namespace: wsNS}, bootstrap); err != nil {
		return err
	}
	username := firstNonEmpty(secretValue(bootstrap, "username"), appContainerName)
	password := firstNonEmpty(secretValue(bootstrap, "password"), db.Name+"-password")
	return r.writeConnectionSecret(ctx, db, wsNS, host, port, databaseName(db), username, password, scheme)
}

func (r *GeassDatabaseReconciler) writeConnectionSecret(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, host, port, database, username, password, scheme string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-connection", Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, db, "GeassDatabase")
		uri := fmt.Sprintf("%s://%s:%s@%s:%s/%s", scheme, username, password, host, port, database)
		if scheme == "redis" {
			uri = fmt.Sprintf("redis://:%s@%s:%s", password, host, port)
		}
		if scheme == "http" {
			uri = fmt.Sprintf("http://%s:%s", host, port)
		}
		setSecretStrings(secret, map[string]string{
			platform.ConnectionKeyHost:     host,
			platform.ConnectionKeyPort:     port,
			platform.ConnectionKeyDatabase: database,
			platform.ConnectionKeyUsername: username,
			platform.ConnectionKeyPassword: password,
			platform.ConnectionKeyURI:      uri,
		})
		return setSameNamespaceOwner(db, secret, r.Scheme)
	})
	return err
}

func (r *GeassDatabaseReconciler) setReady(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, host, message string) (ctrl.Result, error) {
	latest := &geassv1alpha1.GeassDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(db), latest); err != nil {
		return ctrl.Result{}, err
	}
	_, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.TargetNamespace = wsNS
		latest.Status.ConnectionSecret = db.Name + "-connection"
		latest.Status.Host = host
		retryState := reconcileRetryFromAPI(&latest.Status.ReconcileRetry)
		platform.ClearReconcileRetry(&retryState)
		writeReconcileRetryToAPI(&latest.Status.ReconcileRetry, retryState)
		completeProvisionSuccess(&latest.Status.Provision)
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "DatabaseReady", message)
	})
	return ctrl.Result{}, err
}

func (r *GeassDatabaseReconciler) setNotReady(ctx context.Context, db *geassv1alpha1.GeassDatabase, message string) (ctrl.Result, error) {
	latest := &geassv1alpha1.GeassDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(db), latest); err != nil {
		return ctrl.Result{}, err
	}
	var result ctrl.Result
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		var conditions []metav1.Condition
		result, conditions = finishNotReady(latest.Status.Conditions, &latest.Status.ReconcileRetry, &latest.Status.Provision, latest.Generation, message)
		latest.Status.Conditions = conditions
	}); err != nil {
		return ctrl.Result{}, err
	}
	return result, nil
}

func mysqlChartName(name string) string {
	return platform.MySQLChartReleaseName(name)
}

func redisDatabaseChartName(name string) string {
	return platform.RedisChartReleaseName(name)
}

func mysqlHost(db *geassv1alpha1.GeassDatabase, wsNS string) string {
	service := mysqlChartName(db.Name)
	if databaseWantsHA(db) {
		service += "-primary"
	}
	return fmt.Sprintf("%s.%s.svc", service, wsNS)
}

func redisDatabaseHost(db *geassv1alpha1.GeassDatabase, wsNS string) string {
	return fmt.Sprintf("%s-master.%s.svc", redisDatabaseChartName(db.Name), wsNS)
}

func mysqlValues(db *geassv1alpha1.GeassDatabase, storageClass string, placement platform.Placement) string {
	architecture := "standalone"
	if databaseWantsHA(db) {
		architecture = "replication"
	}
	password := db.Name + "-password"
	res := databaseInstanceResources(db)
	// In replication the chart runs secondaries with their own volumes, which take
	// the chart's defaults unless told otherwise. They must match the primary, or
	// they land on another class and size than the one the user sees.
	secondary := ""
	if databaseWantsHA(db) {
		secondary = "secondary:\n" + mysqlPersistence(db, storageClass) + placement.HelmValues(2)
	}
	return fmt.Sprintf(`architecture: %s
auth:
  rootPassword: "%s"
  username: "%s"
  password: "%s"
  database: "%s"
primary:
%s  resources:
    requests:
      cpu: %s
      memory: %s
    limits:
      cpu: %s
      memory: %s
%s%s`, architecture, password, appContainerName, password, databaseName(db), mysqlPersistence(db, storageClass),
		resourceQuantity(res.Requests, corev1.ResourceCPU), resourceQuantity(res.Requests, corev1.ResourceMemory),
		resourceQuantity(res.Limits, corev1.ResourceCPU), resourceQuantity(res.Limits, corev1.ResourceMemory),
		placement.HelmValues(2), secondary)
}

// mysqlPersistence is the volume block shared by the primary and its secondaries.
func mysqlPersistence(db *geassv1alpha1.GeassDatabase, storageClass string) string {
	block := fmt.Sprintf("  persistence:\n    enabled: true\n    size: %s\n", storageSize(db))
	if storageClass != "" {
		block += fmt.Sprintf("    storageClass: %q\n", storageClass)
	}
	return block
}

func redisDatabaseValues(db *geassv1alpha1.GeassDatabase, placement platform.Placement) string {
	architecture := "standalone"
	if databaseWantsHA(db) {
		architecture = "replication"
	}
	res := databaseInstanceResources(db)
	return fmt.Sprintf(`architecture: %s
auth:
  enabled: true
  password: "%s"
master:
  persistence:
    enabled: false
  resources:
    requests:
      cpu: %s
      memory: %s
    limits:
      cpu: %s
      memory: %s
%s%s`, architecture, db.Name+"-password",
		resourceQuantity(res.Requests, corev1.ResourceCPU), resourceQuantity(res.Requests, corev1.ResourceMemory),
		resourceQuantity(res.Limits, corev1.ResourceCPU), resourceQuantity(res.Limits, corev1.ResourceMemory),
		placement.HelmValues(2), redisReplicaPlacement(db, placement))
}

// redisReplicaPlacement keeps a replicated Redis's replicas in the same place as its master.
func redisReplicaPlacement(db *geassv1alpha1.GeassDatabase, placement platform.Placement) string {
	if !databaseWantsHA(db) || placement.IsZero() {
		return ""
	}
	return "replica:\n" + placement.HelmValues(2)
}

func databaseInstanceResources(db *geassv1alpha1.GeassDatabase) corev1.ResourceRequirements {
	if db != nil && len(db.Spec.Resources.Requests) > 0 {
		return db.Spec.Resources
	}
	if db != nil && db.Spec.Engine == geassv1alpha1.DatabaseEngineSQLite {
		return platform.DefaultSQLiteResources()
	}
	return platform.DefaultDatabaseResources()
}

func databaseCapacityEstimate(db *geassv1alpha1.GeassDatabase) platform.WorkloadEstimate {
	if db == nil {
		return platform.EstimateFromResources("database", platform.DefaultDatabaseResources(), 1)
	}
	label := string(db.Spec.Engine) + " database"
	if db.Spec.Engine == "" {
		label = "PostgreSQL database"
	}
	return platform.EstimateFromResources(label, databaseInstanceResources(db), databaseInstances(db))
}

func resourceQuantity(list corev1.ResourceList, name corev1.ResourceName) string {
	if value, ok := list[name]; ok && !value.IsZero() {
		return value.String()
	}
	if name == corev1.ResourceCPU {
		return "250m"
	}
	return "512Mi"
}

func storageSize(db *geassv1alpha1.GeassDatabase) string {
	if db.Spec.StorageSize != nil {
		return db.Spec.StorageSize.String()
	}
	return platform.DefaultDatabaseStorage(db.Spec.Engine)
}

func defaultPortForEngine(engine geassv1alpha1.GeassDatabaseEngine) string {
	switch engine {
	case geassv1alpha1.DatabaseEngineMySQL:
		return platform.MySQLDefaultPort
	case geassv1alpha1.DatabaseEngineRedis:
		return platform.RedisDefaultPort
	case geassv1alpha1.DatabaseEngineSQLite:
		return platform.SQLiteDefaultPort
	default:
		return platform.PostgresDefaultPort
	}
}

func schemeForEngine(engine geassv1alpha1.GeassDatabaseEngine) string {
	switch engine {
	case geassv1alpha1.DatabaseEngineMySQL:
		return "mysql"
	case geassv1alpha1.DatabaseEngineRedis:
		return "redis"
	case geassv1alpha1.DatabaseEngineSQLite:
		return "http"
	default:
		return "postgresql"
	}
}

func secretValue(secret *corev1.Secret, key string) string {
	if secret == nil {
		return ""
	}
	if secret.Data != nil {
		if value, ok := secret.Data[key]; ok {
			return string(value)
		}
	}
	if secret.StringData != nil {
		return secret.StringData[key]
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassDatabase{}).
		// A project choosing or changing its backup storage must reach the databases waiting for it.
		Watches(&geassv1alpha1.GeassProject{}, handler.EnqueueRequestsFromMapFunc(r.databasesForProject), builder.WithPredicates(readyOrSpecChanged())).
		// A database given its first logical database must start giving out its administrative login.
		Watches(&geassv1alpha1.GeassLogicalDatabase{}, handler.EnqueueRequestsFromMapFunc(r.databasesForLogicalDatabase)).
		// A restore or final backup finishing should be noticed at once, not at the next poll.
		Watches(&batchv1.Job{}, handler.EnqueueRequestsFromMapFunc(databaseForBackupJob),
			builder.WithPredicates(predicate.NewPredicateFuncs(isBackupJob))).
		Named("geassdatabase").
		Complete(r)
}
