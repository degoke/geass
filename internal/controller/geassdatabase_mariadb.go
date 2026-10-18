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
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilptr "k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/helmchart"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	mariadbv1 "github.com/degoke/geass/pkg/mariadb/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// A MariaDB database is run by mariadb-operator: one server, or with high availability three or five that
// replicate to each other with a primary that is replaced when it is lost. The operator makes the servers, their
// volumes, the Services, and the application's login and database, which it makes from the Secret Geass hands it,
// so there is no job as there is for MySQL on MOCO.
//
// What the operator cannot do alone is replace a primary whose machine went silent: it promotes only a replica
// that is ready and still replicating, and a replica that lost its primary is neither. Geass notices the lost
// machine and names the replica to promote, which the operator then does as a switchover (mariadbFailover below).

const (
	// mariadbRecoverySuffix names the copy of the database the operator takes from a ready replica to build another
	// replica that cannot catch up. The copy is never taken on a schedule.
	mariadbRecoverySuffix = "-recovery"

	// mariadbNodeLossGrace is how long the machine of the primary has to be unresponsive before a replica is
	// promoted. Kubernetes needs 40 seconds to call a machine that stopped reporting not ready, and a machine that
	// is only slow, or restarting, must not lose its primary.
	mariadbNodeLossGrace = time.Minute
	// mariadbFailoverCheckInterval is how often a highly available database is looked at when nothing else wakes it:
	// the controller does not watch nodes.
	mariadbFailoverCheckInterval = 15 * time.Second

	// mariadbStartupProbeFailures is how many times the startup probe may fail, ten seconds apart, before the server is
	// restarted. The operator's own allows 50 seconds in all, which is not enough for the first start of a server with a
	// quarter of a CPU on a busy node: the image sets up its data directory, is killed in the middle of it, and starts
	// the next time on a directory that looks done and has no root password, which no later start repairs. Found on K3s,
	// where two of three servers started together made it.
	mariadbStartupProbeFailures = 30

	messageMariaDBFailingOver = platform.MessagePromotingReplica

	// mariadbRestoreDeadline is how long a database that is restored from backups may take to start before the restore
	// is reported as failed.
	mariadbRestoreDeadline = time.Duration(restoreJobDeadline) * time.Second

	// mariadbSwitchoverDeadline is how long a promotion that Geass asked for may take before it is given up: the
	// database is returned to the primary that is serving, and another promotion waits mariadbPromotionRetryAfter. A
	// switchover needs the old primary to answer, so one that cannot reach it never finishes by itself.
	mariadbSwitchoverDeadline  = 10 * time.Minute
	mariadbPromotionRetryAfter = 30 * time.Minute

	// annotationPromotionStarted and annotationPromotionAbandoned are on the MariaDB and say when Geass asked for a
	// promotion, and when it gave one up.
	annotationPromotionStarted   = "geass.dev/promotion-started"
	annotationPromotionAbandoned = "geass.dev/promotion-abandoned"

	// annotationMariaDBInitialized is put on a database the first time the operator says it is ready. The operator
	// makes the application's login and database only once every server is up, so until then a server that runs is not
	// a database that can be used, and afterwards it is.
	annotationMariaDBInitialized = "geass.dev/mariadb-initialized"
)

// mariadbServerName is the StatefulSet of a database and, with an index, its pods.
func mariadbServerName(db *geassv1alpha1.GeassDatabase) string { return db.Name }

// mariadbService is the Service a database is reached by: the primary's, for one that is replicated, and the only
// one otherwise.
func mariadbService(db *geassv1alpha1.GeassDatabase, highlyAvailable bool) string {
	if highlyAvailable {
		return db.Name + "-primary"
	}
	return db.Name
}

func mariadbHost(db *geassv1alpha1.GeassDatabase, wsNS string) string {
	return fmt.Sprintf("%s.%s.svc", mariadbService(db, databaseWantsHA(db)), wsNS)
}

// mariadbClaimName is the volume claim of one server.
func mariadbClaimName(db *geassv1alpha1.GeassDatabase, index int) string {
	return fmt.Sprintf("storage-%s-%d", db.Name, index)
}

func mariadbRecoveryName(db *geassv1alpha1.GeassDatabase) string {
	return db.Name + mariadbRecoverySuffix
}

// mariadbOperatorValues keeps two copies of each of the operator's three parts, spread over nodes where there are
// enough of them. The operator is what promotes a replica, and its webhook is what every change to a database goes
// through, so a database whose operator was on the machine it lost waits for one to start somewhere else, which
// takes minutes. The instance is the release's name, which is the name of the HelmChart.
const mariadbOperatorValues = `ha:
  enabled: true
  replicas: 2
affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          topologyKey: kubernetes.io/hostname
          labelSelector:
            matchLabels:
              app.kubernetes.io/name: mariadb-operator
              app.kubernetes.io/instance: ` + platform.MariaDBOperatorChartName + `
webhook:
  ha:
    enabled: true
    replicas: 2
  affinity:
    podAntiAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
        - weight: 100
          podAffinityTerm:
            topologyKey: kubernetes.io/hostname
            labelSelector:
              matchLabels:
                app.kubernetes.io/name: mariadb-operator-webhook
                app.kubernetes.io/instance: ` + platform.MariaDBOperatorChartName + `
certController:
  ha:
    enabled: true
    replicas: 2
  affinity:
    podAntiAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
        - weight: 100
          podAffinityTerm:
            topologyKey: kubernetes.io/hostname
            labelSelector:
              matchLabels:
                app.kubernetes.io/name: mariadb-operator-cert-controller
                app.kubernetes.io/instance: ` + platform.MariaDBOperatorChartName + `
`

// mariadbSpec is the database the operator is asked to run. The servers keep to the placement the database was
// given, which is also what the compliance policy of a project that requires tags checks them for, and spread over
// the nodes where there are enough of them, as a preference and not a rule.
func mariadbSpec(db *geassv1alpha1.GeassDatabase, storageClass string, placement platform.Placement, instances int32) mariadbv1.MariaDBSpec {
	bootstrap := db.Name + "-bootstrap"
	size := databaseStorageQuantity(db)
	resources := serverResources(db)
	spec := mariadbv1.MariaDBSpec{
		Image:                    platform.MariaDBImage,
		Replicas:                 instances,
		RootPasswordSecretKeyRef: mariadbv1.SecretKeyRef{Name: bootstrap, Key: "password"},
		Database:                 utilptr.To(databaseName(db)),
		Username:                 utilptr.To(appContainerName),
		PasswordSecretKeyRef:     &mariadbv1.SecretKeyRef{Name: bootstrap, Key: "password"},
		Storage:                  mariadbv1.Storage{Size: &size, StorageClassName: stringPtrOrNil(storageClass)},
		Resources:                &resources,
		NodeSelector:             placement.NodeSelector(),
		Tolerations:              placement.Tolerations(),
		StartupProbe:             &mariadbv1.Probe{PeriodSeconds: 10, FailureThreshold: mariadbStartupProbeFailures},
	}
	if instances > 1 {
		spec.Affinity = &mariadbv1.Affinity{PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight: 100,
				PodAffinityTerm: corev1.PodAffinityTerm{
					TopologyKey: "kubernetes.io/hostname",
					LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
						platform.K8sLabelAppName:  "mariadb",
						platform.K8sLabelInstance: db.Name,
					}},
				},
			}},
		}}
		spec.Replication = &mariadbv1.Replication{
			Enabled: true,
			// The operator's own failover stays on: it is right for the cases it can handle, a pod that dies or a
			// process that stops answering, and Geass adds the one it cannot.
			Primary: &mariadbv1.PrimaryReplication{AutoFailover: utilptr.To(true), PodIndex: utilptr.To(0)},
			Replica: &mariadbv1.ReplicaReplication{
				BootstrapFrom: &mariadbv1.ReplicaBootstrapFrom{PhysicalBackupTemplateRef: corev1.LocalObjectReference{Name: mariadbRecoveryName(db)}},
				// A replica that was cut off from a primary that changed under it cannot start again as it is. The
				// operator builds it again from a copy of a ready replica; left off, it restarts for ever.
				Recovery: &mariadbv1.ReplicaRecovery{Enabled: true},
			},
		}
	}
	return spec
}

// mariadbRecoveryTemplate is the copy the operator takes from a ready replica to build another. It is kept on a
// volume of its own, as large as the database, and is used only when a replica has to be built again.
func mariadbRecoveryTemplate(db *geassv1alpha1.GeassDatabase, wsNS, storageClass string, placement platform.Placement) *mariadbv1.PhysicalBackup {
	size := databaseStorageQuantity(db)
	return &mariadbv1.PhysicalBackup{
		ObjectMeta: metav1.ObjectMeta{Name: mariadbRecoveryName(db), Namespace: wsNS},
		Spec: mariadbv1.PhysicalBackupSpec{
			MariaDBRef: mariadbv1.MariaDBRef{Name: db.Name, WaitForIt: utilptr.To(false)},
			// A schedule that is suspended makes the backup a template that nothing runs by itself.
			Schedule: &mariadbv1.PhysicalBackupSchedule{Cron: "0 0 1 1 *", Suspend: true},
			Target:   "PreferReplica",
			Storage: mariadbv1.PhysicalBackupStorage{PersistentVolumeClaim: &mariadbv1.PersistentVolumeClaimStorage{
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: size}},
				StorageClassName: stringPtrOrNil(storageClass),
			}},
			PodAffinity: utilptr.To(true),
			Tolerations: jobPlacement(placement).Tolerations(),
		},
	}
}

// reconcileMariaDB runs the MariaDB database of a database resource.
func (r *GeassDatabaseReconciler) reconcileMariaDB(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string, placement platform.Placement) (ctrl.Result, error) {
	instances, message := oddInstances(db)
	if message != "" {
		return r.setNotReady(ctx, db, message)
	}
	existing, err := r.existingMariaDB(ctx, db, wsNS)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if existing != nil && (existing.Spec.Replication != nil && existing.Spec.Replication.Enabled) != databaseWantsHA(db) {
		return r.setNotReady(ctx, db, platform.HAChangeMessage(db.Spec.Engine, existing.Spec.Replication != nil && existing.Spec.Replication.Enabled))
	}
	object := &mariadbv1.MariaDB{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}
	if ok, msg := workloadFitsPlacementUnlessCreated(ctx, r.Client, r.PodUsage, object, databaseCapacityEstimate(db), placement); !ok {
		return r.setNotReady(ctx, db, msg)
	}
	if err := r.ensureMariaDBOperator(ctx); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if ready, msg, err := r.mariadbOperatorReady(ctx); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	} else if !ready {
		logf.FromContext(ctx).Info("Waiting for the MariaDB operator", "message", msg)
		return r.setNotReady(ctx, db, msg)
	}
	if err := r.inheritMariaDBCredentials(ctx, db); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if err := r.correctRestoredPassword(ctx, db, wsNS); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if err := r.reconcileBootstrapSecret(ctx, db, wsNS); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if instances > 1 {
		if err := r.ensureMariaDBRecoveryTemplate(ctx, db, wsNS, storageClass, placement); err != nil {
			return r.setNotReady(ctx, db, err.Error())
		}
	}
	bootstrap, err := r.mariadbRestoreBootstrap(ctx, db, wsNS, storageClass)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	mdb, err := r.ensureMariaDB(ctx, db, wsNS, storageClass, placement, instances, bootstrap)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}

	if err := r.markMariaDBInitialized(ctx, db, mdb); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	var result ctrl.Result
	if instances > 1 {
		result.RequeueAfter = mariadbFailoverCheckInterval
		promoting, err := r.mariadbFailover(ctx, db, wsNS, mdb)
		if err != nil {
			return r.setNotReady(ctx, db, err.Error())
		}
		if promoting {
			res, err := r.setNotReady(ctx, db, messageMariaDBFailingOver)
			res.RequeueAfter = shortestRequeue(res.RequeueAfter, mariadbFailoverCheckInterval)
			return res, err
		}
	}
	if !r.mariadbServing(ctx, db, wsNS, mdb) {
		message := r.serverNotReadyMessage(ctx, wsNS, serverSpec{name: mariadbServerName(db)})
		if mdb.Spec.BootstrapFrom != nil && db.Status.RestoredAt == nil {
			message = r.mariadbRestoreMessage(mdb)
		}
		res, err := r.setNotReady(ctx, db, message)
		res.RequeueAfter = shortestRequeue(res.RequeueAfter, result.RequeueAfter)
		return res, err
	}
	host := mariadbHost(db, wsNS)
	if err := r.reconcileGeneratedConnectionSecret(ctx, db, wsNS, host, platform.MySQLDefaultPort, "mysql"); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	backups, err := r.reconcileMariaDBBackups(ctx, db, wsNS, storageClass, placement, mdb)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if backups.waiting != "" {
		return r.setNotReady(ctx, db, backups.waiting)
	}
	res, err := r.setReadyWithBackups(ctx, db, wsNS, host, string(db.Spec.Engine)+" database is ready", backups)
	if err == nil && result.RequeueAfter > 0 {
		res.RequeueAfter = shortestRequeue(res.RequeueAfter, result.RequeueAfter)
	}
	return res, err
}

// mariadbRestoreMessage is what a database says while it starts from backups. A server that cannot (the original's
// password was changed, a log is missing, the bucket cannot be read) stays initializing and says nothing more, so a
// restore is given the time a restore of a dump is, and then reported as failed instead of waited for for ever.
func (r *GeassDatabaseReconciler) mariadbRestoreMessage(mdb *mariadbv1.MariaDB) string {
	if !mdb.CreationTimestamp.IsZero() && r.now().Sub(mdb.CreationTimestamp.Time) > mariadbRestoreDeadline {
		return messageRestoreFailed
	}
	return messageRestoringDatabase
}

// existingMariaDB is the database the operator already runs for this resource, or nil when it has none yet or the
// operator is not installed.
func (r *GeassDatabaseReconciler) existingMariaDB(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (*mariadbv1.MariaDB, error) {
	mdb := &mariadbv1.MariaDB{}
	err := r.Get(ctx, client.ObjectKey{Name: db.Name, Namespace: wsNS}, mdb)
	switch {
	case err == nil:
		return mdb, nil
	// The kind does not exist until the operator's chart of resource definitions is installed.
	case apierrors.IsNotFound(err) || meta.IsNoMatchError(err):
		return nil, nil
	}
	return nil, err
}

// ensureMariaDBOperator installs the two charts: the operator's resource definitions first, and then the operator.
func (r *GeassDatabaseReconciler) ensureMariaDBOperator(ctx context.Context) error {
	crds := helmv1.HelmChartSpec{
		Chart:           platform.MariaDBCRDsReleaseChart,
		Repo:            platform.MariaDBOperatorRepo,
		Version:         platform.MariaDBOperatorVersion,
		TargetNamespace: platform.MariaDBTargetNS,
		CreateNamespace: true,
	}
	if err := helmchart.Ensure(ctx, r.Client, platform.MariaDBCRDsChartName, crds); err != nil {
		return err
	}
	operator := helmv1.HelmChartSpec{
		Chart:           platform.MariaDBOperatorRelease,
		Repo:            platform.MariaDBOperatorRepo,
		Version:         platform.MariaDBOperatorVersion,
		TargetNamespace: platform.MariaDBTargetNS,
		CreateNamespace: true,
		ValuesContent:   mariadbOperatorValues,
	}
	return helmchart.Ensure(ctx, r.Client, platform.MariaDBOperatorChartName, operator)
}

// mariadbOperatorParts are the three Deployments of the operator, which all have to run: the controller makes the
// servers, the webhook admits every database, and the certificate controller keeps the webhook's certificate.
var mariadbOperatorParts = []string{"mariadb-operator", "mariadb-operator-webhook", "mariadb-operator-cert-controller"}

// mariadbOperatorReady waits for both charts to be installed and for every part of the operator to be running.
func (r *GeassDatabaseReconciler) mariadbOperatorReady(ctx context.Context) (bool, string, error) {
	waiting := installingChartMessage("MariaDB operator")
	for _, name := range []string{platform.MariaDBCRDsChartName, platform.MariaDBOperatorChartName} {
		chart, err := helmchart.Get(ctx, r.Client, name)
		if apierrors.IsNotFound(err) {
			return false, waiting, nil
		}
		if err != nil {
			return false, "", err
		}
		installed, err := waitForHelmChart(ctx, r.Client, chart)
		if err != nil {
			return false, "", err
		}
		if !installed {
			return false, waiting, nil
		}
	}
	var deployments appsv1.DeploymentList
	if err := r.List(ctx, &deployments, client.InNamespace(platform.MariaDBTargetNS), client.MatchingLabels{platform.K8sLabelInstance: platform.MariaDBOperatorChartName}); err != nil {
		return false, "", err
	}
	running := map[string]bool{}
	for i := range deployments.Items {
		if deploymentReady(&deployments.Items[i], 1) {
			running[deployments.Items[i].Labels[platform.K8sLabelAppName]] = true
		}
	}
	for _, part := range mariadbOperatorParts {
		if !running[part] {
			return false, waiting, nil
		}
	}
	return true, "", nil
}

// ensureMariaDBRecoveryTemplate makes the copy the operator builds a replica from. Its volume is the size of the
// database and is created when it is used, so a database that never needs a replica built again does not hold it.
func (r *GeassDatabaseReconciler) ensureMariaDBRecoveryTemplate(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string, placement platform.Placement) error {
	desired := mariadbRecoveryTemplate(db, wsNS, storageClass, placement)
	template := &mariadbv1.PhysicalBackup{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: wsNS}}
	_, err := controllerutil.CreateOrPatch(ctx, r.Client, template, func() error {
		applyGeassLabels(template, db, "GeassDatabase")
		spec := desired.Spec
		if template.ResourceVersion != "" && template.Spec.Storage.PersistentVolumeClaim != nil && spec.Storage.PersistentVolumeClaim != nil {
			// Where a copy is kept is fixed once the template exists, as the claim of the database is.
			spec.Storage.PersistentVolumeClaim.StorageClassName = template.Spec.Storage.PersistentVolumeClaim.StorageClassName
		}
		// What the operator fills in and Geass does not say is left as it is, so a pass with nothing to change sends nothing.
		spec.Compression, spec.MaxRetention = template.Spec.Compression, template.Spec.MaxRetention
		template.Spec = spec
		return setSameNamespaceOwner(db, template, r.Scheme)
	})
	return err
}

// ensureMariaDB creates or updates the database and returns it as the API server last saw it. What the operator
// refuses to change once the servers exist is kept as it is: the logins, the volume's class, and which server is
// the primary, which is the operator's to move on a failover and Geass's on a lost machine.
func (r *GeassDatabaseReconciler) ensureMariaDB(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string, placement platform.Placement, instances int32, bootstrap *mariadbv1.BootstrapFrom) (*mariadbv1.MariaDB, error) {
	mdb := &mariadbv1.MariaDB{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}
	archivesLogs := r.mariadbArchivesLogs(ctx, db, wsNS)
	_, err := controllerutil.CreateOrPatch(ctx, r.Client, mdb, func() error {
		applyGeassLabels(mdb, db, "GeassDatabase")
		live := mdb.Spec
		desired := mariadbSpec(db, storageClass, placement, instances)
		// The database archives its binary logs only once the object that says where exists, and stops before it is
		// removed. What it was started from is fixed with the database.
		if archivesLogs {
			desired.PointInTimeRecoveryRef = &mariadbv1.LocalRef{Name: mariadbPITRName(db)}
		}
		desired.BootstrapFrom = bootstrap
		if mdb.ResourceVersion != "" {
			desired.BootstrapFrom = live.BootstrapFrom
			desired.RootPasswordSecretKeyRef = live.RootPasswordSecretKeyRef
			desired.Database, desired.Username, desired.PasswordSecretKeyRef = live.Database, live.Username, live.PasswordSecretKeyRef
			desired.Storage.StorageClassName = live.Storage.StorageClassName
			if live.Replication != nil && live.Replication.Primary != nil && desired.Replication != nil && desired.Replication.Primary != nil {
				desired.Replication.Primary.PodIndex = live.Replication.Primary.PodIndex
			}
		}
		mdb.Spec = desired
		return setSameNamespaceOwner(db, mdb, r.Scheme)
	})
	if err != nil {
		return nil, err
	}
	return mdb, nil
}

// markMariaDBInitialized records that the operator has finished setting the database up. It is written once.
func (r *GeassDatabaseReconciler) markMariaDBInitialized(ctx context.Context, db *geassv1alpha1.GeassDatabase, mdb *mariadbv1.MariaDB) error {
	if mariadbInitialized(db) || !mdb.ConditionTrue(mariadbv1.ConditionReady) {
		return nil
	}
	latest := &geassv1alpha1.GeassDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(db), latest); err != nil {
		return err
	}
	base := latest.DeepCopy()
	if latest.Annotations == nil {
		latest.Annotations = map[string]string{}
	}
	latest.Annotations[annotationMariaDBInitialized] = strconv.FormatBool(true)
	if err := r.Patch(ctx, latest, client.MergeFrom(base)); err != nil {
		return err
	}
	if db.Annotations == nil {
		db.Annotations = map[string]string{}
	}
	db.Annotations[annotationMariaDBInitialized] = strconv.FormatBool(true)
	return nil
}

func mariadbInitialized(db *geassv1alpha1.GeassDatabase) bool {
	return db.Annotations[annotationMariaDBInitialized] != ""
}

// mariadbServing reports whether the database can be used: the operator says it is ready, or it has been and its
// primary is running and ready, which it is while a replica is being built again. Before the operator has said it is
// ready once a running primary is not enough: the application's login and database are made last, and one that is
// still being set up has neither.
func (r *GeassDatabaseReconciler) mariadbServing(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, mdb *mariadbv1.MariaDB) bool {
	if mdb.ConditionTrue(mariadbv1.ConditionReady) {
		return true
	}
	if !mariadbInitialized(db) || mdb.Status.CurrentPrimary == nil {
		return false
	}
	pod := &corev1.Pod{}
	if err := r.Get(ctx, client.ObjectKey{Name: *mdb.Status.CurrentPrimary, Namespace: wsNS}, pod); err != nil {
		return false
	}
	return pod.DeletionTimestamp.IsZero() && podConditionsReady(*pod)
}

// mariadbFailover promotes a replica when the machine the primary runs on has stopped answering, and reports whether
// it did or the promotion is under way. mariadb-operator does this itself for a pod that is lost or a server that
// stops answering, because a replica that is ready and replicating is then still there to promote. A machine that goes
// silent takes that away: the replicas lose their primary without being told, their replication threads fall into
// reconnecting, they stop being ready, and the operator is left with nothing it will promote. Tested on three machines,
// the database stayed without a primary for as long as the machine was down.
func (r *GeassDatabaseReconciler) mariadbFailover(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, mdb *mariadbv1.MariaDB) (bool, error) {
	if mdb.Spec.Replication == nil || mdb.Spec.Replication.Primary == nil || mdb.Status.CurrentPrimary == nil {
		return false, nil
	}
	primaryIndex, ok := mariadbPodIndex(db, *mdb.Status.CurrentPrimary)
	if !ok {
		return false, nil
	}
	now := r.now()
	started, asked := annotationTime(mdb, annotationPromotionStarted)
	// A switchover is under way when the primary the spec names is not the one the operator reports.
	if want := utilptr.Deref(mdb.Spec.Replication.Primary.PodIndex, 0); want != primaryIndex {
		if !asked || now.Sub(started) < mariadbSwitchoverDeadline {
			return true, nil
		}
		// One that Geass asked for and the operator cannot finish: the old primary is still the primary, so say so.
		logf.FromContext(ctx).Info("Gave up the promotion of a MariaDB replica", "database", db.Name, "primary", *mdb.Status.CurrentPrimary)
		base := mdb.DeepCopy()
		mdb.Spec.Replication.Primary.PodIndex = utilptr.To(primaryIndex)
		delete(mdb.Annotations, annotationPromotionStarted)
		setAnnotation(mdb, annotationPromotionAbandoned, now.UTC().Format(time.RFC3339))
		return false, r.Patch(ctx, mdb, client.MergeFrom(base))
	}
	if asked {
		// The switchover finished.
		base := mdb.DeepCopy()
		delete(mdb.Annotations, annotationPromotionStarted)
		if err := r.Patch(ctx, mdb, client.MergeFrom(base)); err != nil {
			return false, err
		}
	}
	if abandoned, ok := annotationTime(mdb, annotationPromotionAbandoned); ok && now.Sub(abandoned) < mariadbPromotionRetryAfter {
		return false, nil
	}
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(wsNS), client.MatchingLabels{platform.K8sLabelInstance: db.Name, platform.K8sLabelAppName: "mariadb"}); err != nil {
		return false, err
	}
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes, client.UnsafeDisableDeepCopy); err != nil {
		return false, err
	}
	target, found := mariadbFailoverTarget(db, mdb, pods.Items, nodes.Items, now)
	if !found {
		return false, nil
	}
	logf.FromContext(ctx).Info("Promoting a MariaDB replica because the primary's machine is not responding",
		"database", db.Name, "primary", *mdb.Status.CurrentPrimary, "newPrimary", target)
	base := mdb.DeepCopy()
	mdb.Spec.Replication.Primary.PodIndex = utilptr.To(target)
	setAnnotation(mdb, annotationPromotionStarted, now.UTC().Format(time.RFC3339))
	delete(mdb.Annotations, annotationPromotionAbandoned)
	if err := r.Patch(ctx, mdb, client.MergeFrom(base)); err != nil {
		return false, err
	}
	return true, nil
}

// annotationTime is the time an annotation of the MariaDB holds, and whether it holds one.
func annotationTime(mdb *mariadbv1.MariaDB, key string) (time.Time, bool) {
	at, err := time.Parse(time.RFC3339, mdb.Annotations[key])
	return at, err == nil
}

func setAnnotation(mdb *mariadbv1.MariaDB, key, value string) {
	if mdb.Annotations == nil {
		mdb.Annotations = map[string]string{}
	}
	mdb.Annotations[key] = value
}

// mariadbPodIndex is the index in a pod's name, for a pod of this database.
func mariadbPodIndex(db *geassv1alpha1.GeassDatabase, pod string) (int, bool) {
	prefix := mariadbServerName(db) + "-"
	if !strings.HasPrefix(pod, prefix) {
		return 0, false
	}
	index, err := strconv.Atoi(strings.TrimPrefix(pod, prefix))
	return index, err == nil && index >= 0
}

// mariadbFailoverTarget decides whether the primary has been lost and which replica to promote. It is a pure function
// of what is on the cluster. The primary is lost when its machine is gone, or has not been ready for the grace
// period. The replica promoted is the one that has seen the most of what the primary wrote, by the position the
// operator last read from it, and that runs on a machine that is ready: promoting one that is behind throws away what
// only another replica has.
func mariadbFailoverTarget(db *geassv1alpha1.GeassDatabase, mdb *mariadbv1.MariaDB, pods []corev1.Pod, nodes []corev1.Node, now time.Time) (int, bool) {
	if mdb.Status.CurrentPrimary == nil || mdb.Status.Replication == nil {
		return 0, false
	}
	primary := *mdb.Status.CurrentPrimary
	byName := map[string]*corev1.Pod{}
	for i := range pods {
		byName[pods[i].Name] = &pods[i]
	}
	nodeByName := map[string]*corev1.Node{}
	for i := range nodes {
		nodeByName[nodes[i].Name] = &nodes[i]
	}
	primaryPod, ok := byName[primary]
	if !ok || primaryPod.Spec.NodeName == "" {
		return 0, false
	}
	if !nodeUnresponsive(nodeByName[primaryPod.Spec.NodeName], now) {
		return 0, false
	}
	best, bestSequence, found := 0, int64(-1), false
	for name, status := range mdb.Status.Replication.Replicas {
		if name == primary {
			continue
		}
		index, ok := mariadbPodIndex(db, name)
		pod := byName[name]
		if !ok || pod == nil || pod.Status.Phase != corev1.PodRunning || !pod.DeletionTimestamp.IsZero() {
			continue
		}
		if node := nodeByName[pod.Spec.NodeName]; node == nil || nodeUnresponsive(node, now) {
			continue
		}
		sequence, ok := mariadbGtidSequence(status.GtidCurrentPos)
		if !ok {
			continue
		}
		if sequence > bestSequence || (sequence == bestSequence && index < best) {
			best, bestSequence, found = index, sequence, true
		}
	}
	return best, found
}

// nodeUnresponsive is whether a machine has been not ready for the grace period, or is gone from the cluster. nil is
// a machine that is not there.
func nodeUnresponsive(node *corev1.Node, now time.Time) bool {
	if node == nil {
		return true
	}
	for _, condition := range node.Status.Conditions {
		if condition.Type != corev1.NodeReady {
			continue
		}
		if condition.Status == corev1.ConditionTrue {
			return false
		}
		return now.Sub(condition.LastTransitionTime.Time) >= mariadbNodeLossGrace
	}
	// A machine that has never reported is not one that was serving.
	return false
}

// mariadbGtidSequence is the sequence number of a MariaDB position, which is domain-server-sequence: 0-10-155 is the
// 155th transaction of server 10 in domain 0. A position that lists several, comma separated, is the largest.
func mariadbGtidSequence(position string) (int64, bool) {
	best, found := int64(0), false
	for gtid := range strings.SplitSeq(position, ",") {
		parts := strings.Split(strings.TrimSpace(gtid), "-")
		if len(parts) != 3 {
			continue
		}
		sequence, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			continue
		}
		if !found || sequence > best {
			best, found = sequence, true
		}
	}
	return best, found
}

// deleteMariaDBResources removes a database, the copy used to build replicas, and the volumes. The operator's own
// objects are owned by the database and go with it, but the volume claims of a StatefulSet do not.
func (r *GeassDatabaseReconciler) deleteMariaDBResources(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) {
	log := logf.FromContext(ctx)
	if err := r.deleteMariaDBBackupObjects(ctx, db, wsNS, true); err != nil {
		log.Info("Could not delete the MariaDB backups", "name", db.Name, "error", err.Error())
	}
	// Deleting a kind that is not installed is not an error worth saying: nothing was ever made.
	ignore := func(err error) bool {
		return err == nil || apierrors.IsNotFound(err) || meta.IsNoMatchError(err)
	}
	if err := r.Delete(ctx, &mariadbv1.PhysicalBackup{ObjectMeta: metav1.ObjectMeta{Name: mariadbRecoveryName(db), Namespace: wsNS}}); !ignore(err) {
		log.Info("Could not delete the MariaDB recovery template", "name", mariadbRecoveryName(db), "error", err.Error())
	}
	if err := r.Delete(ctx, &mariadbv1.MariaDB{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}); !ignore(err) {
		log.Info("Could not delete MariaDB", "name", db.Name, "error", err.Error())
	}
	for i := range maxClusterInstances {
		_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: mariadbClaimName(db, i), Namespace: wsNS}}))
	}
}
