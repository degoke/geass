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
	"net/http"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

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
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdatabases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdatabases/finalizers,verbs=update
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasshareadinesses,verbs=get;list;watch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscloudconnections,verbs=get;list;watch
// +kubebuilder:rbac:groups=helm.cattle.io,resources=helmcharts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *GeassDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var db geassv1alpha1.GeassDatabase
	if err := r.Get(ctx, req.NamespacedName, &db); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if err := platform.ValidateProjectPlacement(ctx, r.Client, db.Spec.Project, db.Spec.Environment); err != nil {
		return r.setNotReady(ctx, &db, err.Error())
	}

	wsNS, err := resourceNamespace(db.Spec.Project, string(db.Spec.Environment))
	if err != nil {
		return r.setNotReady(ctx, &db, err.Error())
	}

	if !controllerutil.ContainsFinalizer(&db, databaseFinalizer) {
		controllerutil.AddFinalizer(&db, databaseFinalizer)
		return ctrl.Result{}, r.Update(ctx, &db)
	}

	if !db.DeletionTimestamp.IsZero() {
		r.deleteTargetResources(ctx, &db, wsNS)
		controllerutil.RemoveFinalizer(&db, databaseFinalizer)
		return ctrl.Result{}, r.Update(ctx, &db)
	}

	if prevNS, moved := previousTargetNamespace(db.Status.TargetNamespace, wsNS); moved {
		r.deleteTargetResources(ctx, &db, prevNS)
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

	switch db.Spec.Engine {
	case geassv1alpha1.DatabaseEnginePostgres, "":
		return r.reconcilePostgres(ctx, &db, wsNS, log.WithValues("engine", "Postgres"))
	case geassv1alpha1.DatabaseEngineMySQL:
		return r.reconcileHelmDatabase(ctx, &db, wsNS, mysqlChartName(db.Name), platform.MySQLReleaseChart, platform.MySQLChartRepo, platform.MySQLChartVersion, mysqlValues(&db), mysqlHost(&db, wsNS), platform.MySQLDefaultPort, "mysql")
	case geassv1alpha1.DatabaseEngineRedis:
		return r.reconcileHelmDatabase(ctx, &db, wsNS, redisDatabaseChartName(db.Name), platform.RedisReleaseChart, platform.RedisChartRepo, platform.RedisChartVersion, redisDatabaseValues(&db), redisDatabaseHost(&db, wsNS), platform.RedisDefaultPort, "redis")
	case geassv1alpha1.DatabaseEngineSQLite:
		return r.reconcileSQLite(ctx, &db, wsNS)
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
	if db.Spec.HighAvailability {
		return true
	}
	return db.Spec.Instances != nil && *db.Spec.Instances >= 3
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

func (r *GeassDatabaseReconciler) reconcilePostgres(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, log interface{ Info(string, ...any) }) (ctrl.Result, error) {
	if err := r.ensureCNPGOperator(ctx); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	operatorReady, err := r.cnpgOperatorReady(ctx)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if !operatorReady {
		log.Info("Waiting for CloudNativePG operator")
		return r.setNotReady(ctx, db, "CloudNativePG operator is not ready")
	}
	if err := r.reconcileBootstrapSecret(ctx, db, wsNS); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if err := r.reconcileCNPGCluster(ctx, db, wsNS); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	cluster := &cnpgv1.Cluster{}
	if err := r.Get(ctx, client.ObjectKey{Name: db.Name, Namespace: wsNS}, cluster); err != nil {
		return r.setNotReady(ctx, db, "CNPG cluster was not created")
	}
	if !cnpgClusterReady(cluster) {
		return r.setNotReady(ctx, db, "Postgres cluster is not healthy yet")
	}
	host := fmt.Sprintf("%s-rw.%s.svc", db.Name, wsNS)
	if err := r.reconcileGeneratedConnectionSecret(ctx, db, wsNS, host, platform.PostgresDefaultPort, "postgresql"); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	return r.setReady(ctx, db, wsNS, host, "Postgres database is ready")
}

func (r *GeassDatabaseReconciler) reconcileHelmDatabase(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, chartName, chart, repo, version, values, host, port, scheme string) (ctrl.Result, error) {
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
		return r.setNotReady(ctx, db, chart+" HelmChart is not ready")
	}
	if err := r.reconcileGeneratedConnectionSecret(ctx, db, wsNS, host, port, scheme); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	return r.setReady(ctx, db, wsNS, host, string(db.Spec.Engine)+" database is ready")
}

func (r *GeassDatabaseReconciler) reconcileSQLite(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (ctrl.Result, error) {
	if db.Spec.HighAvailability {
		return r.setNotReady(ctx, db, "SQLite does not support high availability")
	}
	if err := r.reconcileBootstrapSecret(ctx, db, wsNS); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	storageSize := "1Gi"
	if db.Spec.StorageSize != nil {
		storageSize = db.Spec.StorageSize.String()
	}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-sqlite", Namespace: wsNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, pvc, func() error {
		applyGeassLabels(pvc, db, "GeassDatabase")
		pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
		pvc.Spec.Resources.Requests = corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(storageSize)}
		return setSameNamespaceOwner(db, pvc, r.Scheme)
	}); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	replicas := int32(1)
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		applyGeassLabels(deploy, db, "GeassDatabase")
		deploy.Spec.Replicas = &replicas
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": db.Name}}
		deploy.Spec.Template.ObjectMeta.Labels = map[string]string{"app.kubernetes.io/name": db.Name, platform.LabelManagedBy: platform.ManagedByValue}
		deploy.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  "sqlite",
			Image: platform.SQLiteImage,
			Args:  []string{"-http-addr", "0.0.0.0:4001", "-http-adv-addr", fmt.Sprintf("%s.%s.svc:4001", db.Name, wsNS)},
			Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 4001}},
			VolumeMounts: []corev1.VolumeMount{{
				Name:      "data",
				MountPath: "/rqlite/file",
			}},
		}}
		deploy.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name:         "data",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvc.Name}},
		}}
		return setSameNamespaceOwner(db, deploy, r.Scheme)
	}); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		applyGeassLabels(svc, db, "GeassDatabase")
		svc.Spec.Selector = map[string]string{"app.kubernetes.io/name": db.Name}
		svc.Spec.Ports = []corev1.ServicePort{{Name: "http", Port: 4001, TargetPort: intstr.FromInt(4001)}}
		return setSameNamespaceOwner(db, svc, r.Scheme)
	}); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if deploy.Status.ReadyReplicas < 1 {
		return r.setNotReady(ctx, db, "SQLite deployment is not ready")
	}
	host := fmt.Sprintf("%s.%s.svc", db.Name, wsNS)
	if err := r.reconcileGeneratedConnectionSecret(ctx, db, wsNS, host, platform.SQLiteDefaultPort, "http"); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	return r.setReady(ctx, db, wsNS, host, "SQLite database is ready")
}

func (r *GeassDatabaseReconciler) reconcileExternal(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (ctrl.Result, error) {
	host, port, username, password, err := r.resolveExternalCredentials(ctx, db)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if db.Spec.Mode == geassv1alpha1.DatabaseModeCreate && db.Spec.Provider == geassv1alpha1.DatabaseProviderPlanetScale {
		createdHost, createdUser, createdPass, err := r.createPlanetScaleDatabase(ctx, db)
		if err != nil {
			return r.setNotReady(ctx, db, err.Error())
		}
		host, username, password = createdHost, createdUser, createdPass
		port = platform.PostgresDefaultPort
	}
	if host == "" || username == "" || password == "" {
		return r.setNotReady(ctx, db, "external database host, username, and password are required")
	}
	if port == "" {
		port = defaultPortForEngine(db.Spec.Engine)
	}
	if err := r.writeConnectionSecret(ctx, db, wsNS, host, port, username, password, schemeForEngine(db.Spec.Engine)); err != nil {
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

func (r *GeassDatabaseReconciler) createPlanetScaleDatabase(ctx context.Context, db *geassv1alpha1.GeassDatabase) (host, username, password string, err error) {
	if db.Spec.ConnectionRef == nil || db.Spec.ConnectionRef.Name == "" {
		return "", "", "", fmt.Errorf("a PlanetScale connection is required")
	}
	connection, secret, err := r.cloudConnection(ctx, db.Spec.ConnectionRef.Name)
	if err != nil {
		return "", "", "", err
	}
	if connection.Spec.Provider != geassv1alpha1.CloudProviderPlanetScale {
		return "", "", "", fmt.Errorf("connection %q is not a PlanetScale connection", connection.Name)
	}
	client := &cloud.PlanetScaleClient{
		HTTP:  r.HTTP,
		Token: secretValue(secret, platform.SecretKeyToken),
		Org:   firstNonEmpty(connection.Spec.Organization, secretValue(secret, platform.SecretKeyOrganization)),
	}
	if client.Token == "" || client.Org == "" {
		return "", "", "", fmt.Errorf("PlanetScale token and organization are required")
	}
	name := databaseName(db)
	if err := client.EnsureDatabase(name); err != nil {
		return "", "", "", err
	}
	pass, err := client.CreatePassword(name, db.Name)
	if err != nil {
		return "", "", "", err
	}
	return firstNonEmpty(pass.Host, db.Spec.ExternalHost), firstNonEmpty(pass.Username, db.Spec.Username), firstNonEmpty(pass.Plain, ""), nil
}

func (r *GeassDatabaseReconciler) cloudConnection(ctx context.Context, name string) (*geassv1alpha1.GeassCloudConnection, *corev1.Secret, error) {
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := r.Get(ctx, client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, connection); err != nil {
		return nil, nil, fmt.Errorf("cloud connection %q is unavailable", name)
	}
	if connection.Spec.SecretRef.Name == "" {
		return nil, nil, fmt.Errorf("cloud connection %q has no credentials", name)
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: platform.SystemNamespace}, secret); err != nil {
		return nil, nil, fmt.Errorf("cloud connection secret %q is unavailable", connection.Spec.SecretRef.Name)
	}
	return connection, secret, nil
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
	_ = client.IgnoreNotFound(r.Delete(ctx, &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-sqlite", Namespace: wsNS}}))
	_ = helmchart.Delete(ctx, r.Client, mysqlChartName(db.Name))
	_ = helmchart.Delete(ctx, r.Client, redisDatabaseChartName(db.Name))
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-bootstrap", Namespace: wsNS}}))
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-connection", Namespace: wsNS}}))
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
	return helmchart.IsReady(ctx, r.Client, chart)
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
		if _, ok := secret.StringData["username"]; !ok && secretValue(secret, "username") == "" {
			secret.StringData["username"] = appContainerName
		}
		if _, ok := secret.StringData["password"]; !ok && secretValue(secret, "password") == "" {
			secret.StringData["password"] = db.Name + "-password"
		}
		return setSameNamespaceOwner(db, secret, r.Scheme)
	})
	return err
}

func (r *GeassDatabaseReconciler) reconcileCNPGCluster(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) error {
	instances := databaseInstances(db)
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
					Database: databaseName(db),
					Owner:    appContainerName,
					Secret:   corev1.LocalObjectReference{Name: db.Name + "-bootstrap"},
				},
			},
		}
		return setSameNamespaceOwner(db, cluster, r.Scheme)
	})
	return err
}

func (r *GeassDatabaseReconciler) reconcileGeneratedConnectionSecret(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, host, port, scheme string) error {
	bootstrap := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: db.Name + "-bootstrap", Namespace: wsNS}, bootstrap); err != nil {
		return err
	}
	username := firstNonEmpty(secretValue(bootstrap, "username"), appContainerName)
	password := firstNonEmpty(secretValue(bootstrap, "password"), db.Name+"-password")
	return r.writeConnectionSecret(ctx, db, wsNS, host, port, username, password, scheme)
}

func (r *GeassDatabaseReconciler) writeConnectionSecret(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, host, port, username, password, scheme string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: db.Name + "-connection", Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, db, "GeassDatabase")
		uri := fmt.Sprintf("%s://%s:%s@%s:%s/%s", scheme, username, password, host, port, databaseName(db))
		if scheme == "redis" {
			uri = fmt.Sprintf("redis://:%s@%s:%s", password, host, port)
		}
		if scheme == "http" {
			uri = fmt.Sprintf("http://%s:%s", host, port)
		}
		secret.StringData = map[string]string{
			platform.ConnectionKeyHost:     host,
			platform.ConnectionKeyPort:     port,
			platform.ConnectionKeyDatabase: databaseName(db),
			platform.ConnectionKeyUsername: username,
			platform.ConnectionKeyPassword: password,
			platform.ConnectionKeyURI:      uri,
		}
		return setSameNamespaceOwner(db, secret, r.Scheme)
	})
	return err
}

func (r *GeassDatabaseReconciler) setReady(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, host, message string) (ctrl.Result, error) {
	latest := db.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(db), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.TargetNamespace = wsNS
	latest.Status.ConnectionSecret = db.Name + "-connection"
	latest.Status.Host = host
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "DatabaseReady", message)
	return ctrl.Result{}, r.Status().Update(ctx, latest)
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

func mysqlChartName(name string) string {
	return "geass-mysql-" + name
}

func redisDatabaseChartName(name string) string {
	return "geass-redis-db-" + name
}

func mysqlHost(db *geassv1alpha1.GeassDatabase, wsNS string) string {
	return fmt.Sprintf("%s.%s.svc", mysqlChartName(db.Name), wsNS)
}

func redisDatabaseHost(db *geassv1alpha1.GeassDatabase, wsNS string) string {
	return fmt.Sprintf("%s-master.%s.svc", redisDatabaseChartName(db.Name), wsNS)
}

func mysqlValues(db *geassv1alpha1.GeassDatabase) string {
	architecture := "standalone"
	if databaseWantsHA(db) {
		architecture = "replication"
	}
	password := db.Name + "-password"
	return fmt.Sprintf(`architecture: %s
auth:
  rootPassword: "%s"
  username: "%s"
  password: "%s"
  database: "%s"
primary:
  persistence:
    enabled: true
    size: %s
`, architecture, password, appContainerName, password, databaseName(db), storageSize(db))
}

func redisDatabaseValues(db *geassv1alpha1.GeassDatabase) string {
	architecture := "standalone"
	if databaseWantsHA(db) {
		architecture = "replication"
	}
	return fmt.Sprintf(`architecture: %s
auth:
  enabled: true
  password: "%s"
master:
  persistence:
    enabled: false
`, architecture, db.Name+"-password")
}

func storageSize(db *geassv1alpha1.GeassDatabase) string {
	if db.Spec.StorageSize != nil {
		return db.Spec.StorageSize.String()
	}
	return "10Gi"
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
		Named("geassdatabase").
		Complete(r)
}
