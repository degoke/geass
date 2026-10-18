/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilptr "k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/helmchart"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	mocov1 "github.com/degoke/geass/pkg/moco/v1beta2"
	"github.com/degoke/geass/pkg/platform"
)

// A MySQL database that asks for high availability is run by MOCO: three or five MySQL servers that replicate
// to each other, one of them the primary, with a replica promoted when the primary is lost. A database that
// does not ask is the single server of geassdatabase_servers.go, which needs no operator. The two are not the
// same servers, so a database is one or the other for its life.
//
// MOCO makes the servers, their volumes and the Services, and Geass makes what MOCO does not: the application's
// login and database, which MOCO leaves to its user, and the connection Secret that publishes them.

const (
	// mocoVolumeName is the claim of each server's data. MOCO names the claims it makes after it and the pod.
	mocoVolumeName = "mysql-data"
	// mocoWritableUser is the login MOCO makes for writing, and the one Geass uses to make the application's
	// login and to copy the database. Its password is in the cluster's Secret, under mocoWritableKey.
	mocoWritableUser = "moco-writable"
	mocoWritableKey  = "WRITABLE_PASSWORD"
	// mocoUserID is the user the servers run as. The volume is made writable for the same group.
	mocoUserID = int64(10000)
	// mocoMemoryFloor is the least a MOCO server is given. MySQL 8.4 as MOCO sets it up uses about half a
	// gigabyte without a single row in it, so a server with less than this is killed for its memory at once.
	mocoMemoryFloor = "1Gi"
	// mocoAppName is the name label MOCO puts on the pods of every cluster.
	mocoAppName = "mysql"
	// maxClusterInstances is how many servers a highly available MySQL or MariaDB database runs at most: both operators
	// run an odd number, one of three or five, and the volume claims of that many are cleaned up with the database.
	maxClusterInstances = 5

	labelLoginFor = "geass.dev/login-for"

	messageNeedsCertManager = "High availability requires cert-manager: turn the cert-manager add-on on, or choose PostgreSQL"
	messageCreatingLogin    = platform.MessageCreatingDatabaseLogin
)

// mocoOperatorValues keeps two controllers, so that losing the node one is on does not stop failover: MOCO is
// the thing that promotes a replica, and a cluster whose operator is down waits for it.
const mocoOperatorValues = `replicaCount: 2
`

// mysqlBackendHA says a MySQL database is run by MOCO. Its other backend is the single server.
const (
	mysqlBackendSingle = "single"
	mysqlBackendHA     = "ha"
)

// mocoServerName is the StatefulSet MOCO makes for a cluster, and the pods are the name with an index.
func mocoServerName(db *geassv1alpha1.GeassDatabase) string { return platform.MOCOServerName(db.Name) }

// mocoPrimaryService is the Service that always reaches the primary, which is the address of the database.
func mocoPrimaryService(db *geassv1alpha1.GeassDatabase) string {
	return mocoServerName(db) + "-primary"
}

// mocoSecretName holds the logins MOCO makes for itself and for writing.
func mocoSecretName(db *geassv1alpha1.GeassDatabase) string { return mocoServerName(db) }

func mysqlHAHost(db *geassv1alpha1.GeassDatabase, wsNS string) string {
	return fmt.Sprintf("%s.%s.svc", mocoPrimaryService(db), wsNS)
}

// mocoClaimName is the volume claim of one server.
func mocoClaimName(db *geassv1alpha1.GeassDatabase, index int) string {
	return fmt.Sprintf("%s-%s-%d", mocoVolumeName, mocoServerName(db), index)
}

// oddInstances is how many servers a MySQL or MariaDB database runs, or the message that says why it cannot. A
// database that does not ask for high availability is one server, and anything else is refused: both operators
// run an odd number of servers, so two or four would not be a cluster that can lose one and carry on.
func oddInstances(db *geassv1alpha1.GeassDatabase) (int32, string) {
	instances := databaseInstances(db)
	if !databaseWantsHA(db) {
		if instances > 1 {
			return 0, platform.HAInstancesMessage(db.Spec.Engine, instances)
		}
		return 1, ""
	}
	if instances != 3 && instances != maxClusterInstances {
		return 0, platform.HAInstancesMessage(db.Spec.Engine, instances)
	}
	return instances, ""
}

// mysqlHAResources is what each MOCO server asks for. It is the database's own size, but never with less memory
// than MOCO's servers need to start, which is raised without being asked, as the size the database then costs is
// the one the capacity check counts.
func mysqlHAResources(db *geassv1alpha1.GeassDatabase) corev1.ResourceRequirements {
	res := serverResources(db)
	floor := resource.MustParse(mocoMemoryFloor)
	for _, list := range []corev1.ResourceList{res.Requests, res.Limits} {
		if memory := list[corev1.ResourceMemory]; memory.Cmp(floor) < 0 {
			list[corev1.ResourceMemory] = floor
		}
	}
	return res
}

// mocoClusterSpec is the cluster MOCO is asked to run. The pods keep to the placement the database was given,
// which is also what the compliance policy of a project that requires tags checks them for, and spread over the
// nodes where there are enough of them, which is a preference and not a rule: a region with fewer than three nodes
// still runs the database, on the nodes it has.
func mocoClusterSpec(db *geassv1alpha1.GeassDatabase, storageClass string, placement platform.Placement, instances int32) mocov1.MySQLClusterSpec {
	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{{
			Name:      mocov1.MySQLContainerName,
			Image:     platform.MOCOMySQLImage,
			Resources: mysqlHAResources(db),
		}},
		// The volume is made writable for the user the server runs as. Without this a class that gives the volume
		// to root, as Longhorn's does, leaves the server unable to write to it.
		SecurityContext: &corev1.PodSecurityContext{
			FSGroup:             utilptr.To(mocoUserID),
			FSGroupChangePolicy: utilptr.To(corev1.FSGroupChangeOnRootMismatch),
		},
		Affinity: &corev1.Affinity{PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight: 100,
				PodAffinityTerm: corev1.PodAffinityTerm{
					TopologyKey: "kubernetes.io/hostname",
					LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
						platform.K8sLabelAppName:  mocoAppName,
						platform.K8sLabelInstance: db.Name,
					}},
				},
			}},
		}},
	}
	placement.ApplyToPodSpec(&podSpec)

	labels := map[string]string{platform.K8sLabelManagedBy: platform.ManagedByValue}
	if db.Spec.Project != "" {
		labels[platform.LabelProject] = db.Spec.Project
		labels[platform.LabelEnvironment] = string(db.Spec.Environment)
	}
	return mocov1.MySQLClusterSpec{
		Replicas:    instances,
		PodTemplate: mocov1.PodTemplateSpec{ObjectMeta: mocov1.ObjectMeta{Labels: labels}, Spec: podSpec},
		VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
			ObjectMeta: metav1.ObjectMeta{Name: mocoVolumeName},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				StorageClassName: stringPtrOrNil(storageClass),
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: databaseStorageQuantity(db)},
				},
			},
		}},
		// The slow query log is a sidecar that only prints a file; nothing here reads it.
		DisableSlowQueryLogContainer: true,
	}
}

// mysqlBackend decides which kind of MySQL a database is. A database that already has servers keeps what it has:
// the message says so when it now asks for the other. One that has none is what it asks to be.
func (r *GeassDatabaseReconciler) mysqlBackend(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (backend, message string, err error) {
	existing, err := r.existingMySQLBackend(ctx, db, wsNS)
	if err != nil {
		return "", "", err
	}
	wanted := mysqlBackendSingle
	if databaseWantsHA(db) {
		wanted = mysqlBackendHA
	}
	if existing != "" && existing != wanted {
		return existing, platform.MySQLChangeMessage(existing == mysqlBackendHA), nil
	}
	return wanted, "", nil
}

// existingMySQLBackend is the kind of servers a database has now, or "" when it has none yet.
func (r *GeassDatabaseReconciler) existingMySQLBackend(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (string, error) {
	err := r.Get(ctx, client.ObjectKey{Name: db.Name, Namespace: wsNS}, &mocov1.MySQLCluster{})
	switch {
	case err == nil:
		return mysqlBackendHA, nil
	// The kind does not exist until MOCO is installed, which is no cluster.
	case !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err):
		return "", err
	}
	err = r.Get(ctx, client.ObjectKey{Name: platform.MySQLServerName(db.Name), Namespace: wsNS}, &appsv1.StatefulSet{})
	switch {
	case err == nil:
		return mysqlBackendSingle, nil
	case apierrors.IsNotFound(err):
		return "", nil
	}
	return "", err
}

// reconcileMySQLHA runs the MySQL cluster of a database that asks for high availability.
func (r *GeassDatabaseReconciler) reconcileMySQLHA(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string, placement platform.Placement) (ctrl.Result, error) {
	instances, message := oddInstances(db)
	if message != "" {
		return r.setNotReady(ctx, db, message)
	}
	existing := &mocov1.MySQLCluster{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}
	if ok, msg := workloadFitsPlacementUnlessCreated(ctx, r.Client, r.PodUsage, existing, databaseCapacityEstimate(db), placement); !ok {
		return r.setNotReady(ctx, db, msg)
	}
	if ready, msg, err := r.certManagerReady(ctx); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	} else if !ready {
		return r.setNotReady(ctx, db, msg)
	}
	if err := r.ensureMOCOOperator(ctx); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if ready, msg, err := r.mocoOperatorReady(ctx); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	} else if !ready {
		logf.FromContext(ctx).Info("Waiting for MOCO operator", "message", msg)
		return r.setNotReady(ctx, db, msg)
	}
	if err := r.reconcileBootstrapSecret(ctx, db, wsNS); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	cluster, err := r.ensureMOCOCluster(ctx, db, wsNS, storageClass, placement, instances)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if !cluster.ConditionTrue(mocov1.ConditionAvailable) {
		return r.setNotReady(ctx, db, r.serverNotReadyMessage(ctx, wsNS, serverSpec{name: mocoServerName(db)}))
	}
	done, err := r.ensureMySQLLogin(ctx, db, wsNS, cluster, placement)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if !done {
		return r.setNotReady(ctx, db, messageCreatingLogin)
	}
	host := mysqlHAHost(db, wsNS)
	if err := r.reconcileGeneratedConnectionSecret(ctx, db, wsNS, host, platform.MySQLDefaultPort, "mysql"); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	var backups dumpOutcome
	if databaseBackupKind(db) == platform.BackupKindSnapshot {
		if backups, err = r.reconcileDumpBackups(ctx, db, wsNS); err != nil {
			return r.setNotReady(ctx, db, err.Error())
		}
		if backups.waiting != "" {
			return r.setNotReady(ctx, db, backups.waiting)
		}
	}
	return r.setReadyWithBackups(ctx, db, wsNS, host, string(db.Spec.Engine)+" database is ready", backups)
}

// certManagerReady reports whether cert-manager is installed and running, which MOCO's webhooks need.
func (r *GeassDatabaseReconciler) certManagerReady(ctx context.Context) (bool, string, error) {
	chart, err := helmchart.Get(ctx, r.Client, platform.CertManagerChartName)
	if apierrors.IsNotFound(err) {
		return false, messageNeedsCertManager, nil
	}
	if err != nil {
		return false, "", err
	}
	installed, err := waitForHelmChart(ctx, r.Client, chart)
	if err != nil {
		return false, "", err
	}
	if !installed {
		return false, installingChartMessage("cert-manager"), nil
	}
	return helmReleaseWorkloadReady(ctx, r.Client, platform.CertManagerTargetNS, platform.CertManagerChartName, 1)
}

func (r *GeassDatabaseReconciler) ensureMOCOOperator(ctx context.Context) error {
	spec := helmv1.HelmChartSpec{
		Chart:           platform.MOCOReleaseChart,
		Repo:            platform.MOCOChartRepo,
		Version:         platform.MOCOChartVersion,
		TargetNamespace: platform.MOCOTargetNS,
		CreateNamespace: true,
		ValuesContent:   mocoOperatorValues,
	}
	return helmchart.Ensure(ctx, r.Client, platform.MOCOChartName, spec)
}

// mocoOperatorReady waits for the chart to be installed and its controller to be running. The controller's
// Deployment is named by the chart and not by the release, so it is read by that name.
func (r *GeassDatabaseReconciler) mocoOperatorReady(ctx context.Context) (bool, string, error) {
	chart, err := helmchart.Get(ctx, r.Client, platform.MOCOChartName)
	if apierrors.IsNotFound(err) {
		return false, installingChartMessage("MySQL operator"), nil
	}
	if err != nil {
		return false, "", err
	}
	installed, err := waitForHelmChart(ctx, r.Client, chart)
	if err != nil {
		return false, "", err
	}
	if !installed {
		return false, installingChartMessage("MySQL operator"), nil
	}
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Name: "moco-controller", Namespace: platform.MOCOTargetNS}, deploy); err != nil {
		if apierrors.IsNotFound(err) {
			return false, installingChartMessage("MySQL operator"), nil
		}
		return false, "", err
	}
	if !deploymentReady(deploy, 1) {
		return false, installingChartMessage("MySQL operator"), nil
	}
	return true, "", nil
}

// ensureMOCOCluster creates or updates the cluster and returns it as the API server last saw it. What decides
// where the data lives, the volume claims, cannot change once the servers exist, so an existing cluster keeps
// its own; the number of servers and what they ask for can. It is patched, not replaced: the type models only some of
// the cluster's fields, and an update would send the others back empty.
func (r *GeassDatabaseReconciler) ensureMOCOCluster(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string, placement platform.Placement, instances int32) (*mocov1.MySQLCluster, error) {
	cluster := &mocov1.MySQLCluster{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}
	_, err := controllerutil.CreateOrPatch(ctx, r.Client, cluster, func() error {
		applyGeassLabels(cluster, db, "GeassDatabase")
		desired := mocoClusterSpec(db, storageClass, placement, instances)
		if cluster.ResourceVersion != "" {
			desired.VolumeClaimTemplates = cluster.Spec.VolumeClaimTemplates
		}
		cluster.Spec = desired
		return setSameNamespaceOwner(db, cluster, r.Scheme)
	})
	if err != nil {
		return nil, err
	}
	return cluster, nil
}

// ensureMySQLLogin makes the application's login and database on the primary, once for each cluster. MOCO makes
// neither: its own logins are for itself, and it leaves the application's to whoever runs the cluster. The job
// is named after the cluster's identity, so a cluster made again after a delete gets its login again, and the
// script can be run twice, so one that ran and was not seen to finish is harmless.
func (r *GeassDatabaseReconciler) ensureMySQLLogin(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, cluster *mocov1.MySQLCluster, placement platform.Placement) (bool, error) {
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mysqlLoginJobName(db, cluster), Namespace: wsNS}}
	err := r.Get(ctx, client.ObjectKeyFromObject(job), job)
	switch {
	case apierrors.IsNotFound(err):
		job = mysqlLoginJob(db, wsNS, cluster, placement)
		if err := setSameNamespaceOwner(db, job, r.Scheme); err != nil {
			return false, err
		}
		if err := r.Create(ctx, job); err != nil && !apierrors.IsAlreadyExists(err) {
			return false, err
		}
		return false, nil
	case err != nil:
		return false, err
	}
	complete, failed := jobFinished(job)
	if failed {
		return false, fmt.Errorf("the job that creates the database login failed: read its pod's log in %s", wsNS)
	}
	return complete, nil
}

func mysqlLoginJobName(db *geassv1alpha1.GeassDatabase, cluster *mocov1.MySQLCluster) string {
	uid := string(cluster.UID)
	if len(uid) > 8 {
		uid = uid[:8]
	}
	return db.Name + "-login-" + uid
}

// mysqlLoginJob runs the same script that makes a logical database on a server, as the cluster's writing login.
func mysqlLoginJob(db *geassv1alpha1.GeassDatabase, wsNS string, cluster *mocov1.MySQLCluster, placement platform.Placement) *batchv1.Job {
	bootstrap := db.Name + "-bootstrap"
	env := []corev1.EnvVar{
		{Name: "DB_HOST", Value: mysqlHAHost(db, wsNS)},
		{Name: "DB_PORT", Value: platform.MySQLDefaultPort},
		{Name: "DB_USER", Value: mocoWritableUser},
		secretEnv("DB_PASSWORD", mocoSecretName(db), mocoWritableKey),
		{Name: "APP_DB", Value: databaseName(db)},
		secretEnv("APP_USER", bootstrap, "username"),
		secretEnv("APP_PASSWORD", bootstrap, "password"),
		{Name: "CREATE_LOGIN", Value: "1"},
		// No limit on the logins: the application is the only one that uses it.
		{Name: "CONNECTION_LIMIT", Value: "0"},
	}
	pod := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyOnFailure,
		SecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot:   utilptr.To(true),
			RunAsUser:      utilptr.To(mocoUserID),
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		Containers: []corev1.Container{{
			Name:    "create-login",
			Image:   platform.MOCOMySQLImage,
			Command: []string{"/bin/sh", "-ceu", mysqlCreateScript},
			Env:     env,
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			}},
			SecurityContext: &corev1.SecurityContext{
				RunAsNonRoot:             utilptr.To(true),
				RunAsUser:                utilptr.To(mocoUserID),
				AllowPrivilegeEscalation: utilptr.To(false),
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			},
		}},
	}
	jobPlacement(placement).ApplyToPodSpec(&pod)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: mysqlLoginJobName(db, cluster), Namespace: wsNS},
		Spec: batchv1.JobSpec{
			BackoffLimit: utilptr.To(int32(6)),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{labelLoginFor: db.Name}},
				Spec:       pod,
			},
		},
	}
	applyGeassLabels(job, db, "GeassDatabase")
	job.Labels[labelLoginFor] = db.Name
	return job
}

// deleteMySQLHAResources removes a cluster, the job that made its login, and its volumes. MOCO's own objects are
// owned by the cluster and go with it; the volume claims of a StatefulSet are not, so they are removed by name.
func (r *GeassDatabaseReconciler) deleteMySQLHAResources(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) {
	log := logf.FromContext(ctx)
	if err := r.Delete(ctx, &mocov1.MySQLCluster{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}); err != nil && !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
		log.Info("Could not delete MySQLCluster", "name", db.Name, "error", err.Error())
	}
	_ = r.DeleteAllOf(ctx, &batchv1.Job{}, client.InNamespace(wsNS), client.MatchingLabels{labelLoginFor: db.Name}, client.PropagationPolicy(metav1.DeletePropagationBackground))
	for i := range maxClusterInstances {
		_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: mocoClaimName(db, i), Namespace: wsNS}}))
	}
}
