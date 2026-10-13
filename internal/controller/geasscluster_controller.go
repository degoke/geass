/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/helmchart"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

const certManagerValues = `installCRDs: true
`

// monitoringPrometheusValues apply to both monitoring profiles.
//   - The chart's names are pinned, so the Prometheus Service is one the dashboard can find.
//   - The monitor selectors are opened up because Geass creates ServiceMonitors without the chart's
//     release label, which the chart's default selector requires; without this, app metrics are
//     never scraped.
//   - Metrics sit on a volume so history survives the pod being rescheduled (the chart default is an
//     emptyDir), and are capped by age and size so they cannot fill it.
const monitoringPrometheusValues = `fullnameOverride: ` + platform.MonitoringFullName + `
prometheus:
  prometheusSpec:
    serviceMonitorSelectorNilUsesHelmValues: false
    podMonitorSelectorNilUsesHelmValues: false
    retention: 15d
    retentionSize: 4GB
    storageSpec:
      volumeClaimTemplate:
        spec:
          accessModes: ["ReadWriteOnce"]
          resources:
            requests:
              storage: 5Gi
`

const monitoringLiteValues = `grafana:
  enabled: false
  defaultDashboardsEnabled: false
alertmanager:
  enabled: false
defaultRules:
  create: false
defaultDashboards:
  enabled: false
kubeStateMetrics:
  enabled: true
nodeExporter:
  enabled: true
prometheusOperator:
  enabled: true
` + monitoringPrometheusValues

// GeassClusterReconciler reconciles a GeassCluster object.
type GeassClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// watchClusterIssuers starts the watch on cert-manager's ClusterIssuers, which cannot be set up with the
	// controller (see startClusterIssuerWatch). SetupWithManager sets it; without a manager it is nil.
	watchClusterIssuers func() error
	issuerWatchMu       sync.Mutex
	issuerWatchStarted  bool
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=helm.cattle.io,resources=helmcharts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch

func (r *GeassClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cluster geassv1alpha1.GeassCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if cluster.DeletionTimestamp.IsZero() {
		if err := r.recordRelease(ctx, &cluster); err != nil {
			logf.FromContext(ctx).Error(err, "Could not record the operator's release on the cluster")
		}
	}
	if result, paused := beginProvisionReconcileUnlessDeleting(!cluster.DeletionTimestamp.IsZero(), cluster.Status.Conditions, cluster.Generation, &cluster.Status.ReconcileRetry); paused {
		return result, nil
	}

	addonsReady, certManagerReady, err := r.reconcileAddons(ctx, &cluster)
	if err != nil {
		return r.updateStatus(ctx, &cluster, false, err)
	}

	result, err := r.updateStatus(ctx, &cluster, addonsReady, nil)
	if err != nil {
		return result, err
	}

	// The issuer that every ingress's certificate is requested from comes first of the background work:
	// HTTPS waits on it, it takes a moment, and the steps below can each spend their whole timeout on
	// something it does not depend on.
	result.RequeueAfter = shortestRequeue(result.RequeueAfter, r.reconcileClusterIssuer(ctx, &cluster, certManagerReady))

	// Growing existing volumes is background work, so it runs after the status is
	// written and only ever shortens the requeue. It cannot affect readiness, and a
	// problem in it must not feed the failure count that pauses this reconciler.
	result.RequeueAfter = shortestRequeue(result.RequeueAfter, r.reconcileLonghornVolumeReplicas(ctx, &cluster))
	// The same goes for keeping Longhorn's regions in step with the nodes' labels.
	result.RequeueAfter = shortestRequeue(result.RequeueAfter, r.reconcileLonghornRegions(ctx, &cluster))
	// And closing the metrics and log stores to everything but the platform.
	result.RequeueAfter = shortestRequeue(result.RequeueAfter, r.reconcileTelemetryIsolation(ctx, &cluster))
	// And measuring the ingress controller, which is where every service's request rate comes from.
	result.RequeueAfter = shortestRequeue(result.RequeueAfter, r.reconcileIngressMetrics(ctx, &cluster))
	// And the operator's own monitor and alert rules, so its health is measured and noticed too.
	result.RequeueAfter = shortestRequeue(result.RequeueAfter, r.reconcileOperatorMonitoring(ctx, &cluster))
	// And scale to zero, which is not needed to run workloads and comes up on the side, asking to be visited
	// again until it has.
	result.RequeueAfter = shortestRequeue(result.RequeueAfter, r.reconcileServerless(ctx, &cluster))
	return result, nil
}

func (r *GeassClusterReconciler) reconcileAddons(ctx context.Context, cluster *geassv1alpha1.GeassCluster) (allReady, certManagerReady bool, err error) {
	log := logf.FromContext(ctx)
	allReady = true

	if geassv1alpha1.AddonEnabled(cluster.Spec.Addons.CertManager.Enabled) {
		spec := helmv1.HelmChartSpec{
			Chart:           platform.CertManagerReleaseChart,
			Repo:            platform.CertManagerChartRepo,
			Version:         platform.CertManagerChartVersion,
			TargetNamespace: platform.CertManagerTargetNS,
			CreateNamespace: true,
			ValuesContent:   certManagerValues,
		}
		if err := helmchart.Ensure(ctx, r.Client, platform.CertManagerChartName, spec); err != nil {
			return false, false, err
		}
		if ready, err := r.addonReady(ctx, platform.CertManagerChartName, platform.CertManagerTargetNS); err != nil {
			return false, false, err
		} else if !ready {
			allReady = false
			log.Info("Waiting for cert-manager add-on")
		} else {
			certManagerReady = true
		}
	} else if err := helmchart.Delete(ctx, r.Client, platform.CertManagerChartName); err != nil && !apierrors.IsNotFound(err) {
		return false, false, err
	}

	if geassv1alpha1.AddonEnabled(cluster.Spec.Addons.Monitoring.Enabled) {
		values, err := monitoringChartValues(cluster)
		if err != nil {
			return false, false, err
		}
		spec := helmv1.HelmChartSpec{
			Chart:           platform.MonitoringReleaseChart,
			Repo:            platform.MonitoringChartRepo,
			Version:         platform.MonitoringChartVersion,
			TargetNamespace: platform.MonitoringTargetNS,
			CreateNamespace: true,
			ValuesContent:   values,
		}
		if err := helmchart.Ensure(ctx, r.Client, platform.MonitoringChartName, spec); err != nil {
			return false, false, err
		}
		if ready, err := r.addonReady(ctx, platform.MonitoringChartName, platform.MonitoringTargetNS); err != nil {
			return false, false, err
		} else if !ready {
			allReady = false
			log.Info("Waiting for monitoring add-on")
		}
	} else if err := helmchart.Delete(ctx, r.Client, platform.MonitoringChartName); err != nil && !apierrors.IsNotFound(err) {
		return false, false, err
	}

	// Unlike the add-ons above there is deliberately no "else: uninstall" branch.
	// Longhorn holds user data, so switching the flag off must never be what
	// removes it; taking it out is an explicit, manual operation.
	if geassv1alpha1.LonghornEnabled(cluster.Spec.Addons) {
		if ready, err := r.reconcileLonghorn(ctx); err != nil {
			return false, false, err
		} else if !ready {
			allReady = false
			log.Info("Waiting for Longhorn add-on")
		}
	}

	// Logging is not needed to run workloads, so it is installed on the side and never holds the cluster back.
	r.reconcileLogging(ctx, cluster)

	return allReady, certManagerReady, nil
}

func (r *GeassClusterReconciler) addonReady(ctx context.Context, chartName, targetNamespace string) (bool, error) {
	chart, err := helmchart.Get(ctx, r.Client, chartName)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	installed, err := waitForHelmChart(ctx, r.Client, chart)
	if err != nil {
		return false, err
	}
	if !installed {
		return false, nil
	}
	ready, _, err := helmReleaseWorkloadReady(ctx, r.Client, targetNamespace, chartName, 1)
	return ready, err
}

func (r *GeassClusterReconciler) updateStatus(ctx context.Context, cluster *geassv1alpha1.GeassCluster, addonsReady bool, reconcileErr error) (ctrl.Result, error) {
	latest := &geassv1alpha1.GeassCluster{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(cluster), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var failureOutcome platform.NotReadyOutcome
	overallReady := false
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		conditions := latest.Status.Conditions
		if reconcileErr != nil {
			conditions = platform.SetCondition(conditions, platform.ConditionAddonsReady, metav1.ConditionFalse, platform.ConditionReasonReconcileError, reconcileErr.Error())
			failureOutcome = platform.ApplyNotReady(conditions, reconcileRetryFromAPI(&latest.Status.ReconcileRetry), latest.Generation, reconcileErr.Error())
			writeReconcileRetryToAPI(&latest.Status.ReconcileRetry, failureOutcome.Retry)
			conditions = failureOutcome.Conditions
		} else if addonsReady {
			conditions = platform.SetCondition(conditions, platform.ConditionAddonsReady, metav1.ConditionTrue, "AddonsReady", platform.ConditionMessage(platform.ConditionAddonsReady, true))
		} else {
			conditions = platform.SetCondition(conditions, platform.ConditionAddonsReady, metav1.ConditionFalse, "AddonsPending", platform.ConditionMessage(platform.ConditionAddonsReady, false))
		}

		overallReady = addonsReady && reconcileErr == nil
		if overallReady {
			retryState := reconcileRetryFromAPI(&latest.Status.ReconcileRetry)
			platform.ClearReconcileRetry(&retryState)
			writeReconcileRetryToAPI(&latest.Status.ReconcileRetry, retryState)
			conditions = platform.SetCondition(conditions, platform.ConditionReady, metav1.ConditionTrue, "ClusterReady", "Geass cluster is ready")
			latest.Status.Phase = geassv1alpha1.ClusterPhaseReady
		} else {
			if reconcileErr == nil {
				conditions = platform.SetCondition(conditions, platform.ConditionReady, metav1.ConditionFalse, "ClusterPending", "Geass cluster is not ready")
			}
			latest.Status.Phase = ""
		}

		latest.Status.Conditions = conditions
	}); err != nil {
		return ctrl.Result{}, err
	}

	if reconcileErr != nil {
		if failureOutcome.RequeueAfter == 0 {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{RequeueAfter: failureOutcome.RequeueAfter}, nil
	}
	if !overallReady {
		return ctrl.Result{RequeueAfter: platform.RequeueAfterDefault}, nil
	}
	return ctrl.Result{}, nil
}

func (r *GeassClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	c, err := ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassCluster{}).
		Watches(&corev1.Node{}, handler.EnqueueRequestsFromMapFunc(r.clustersForNodeChange), builder.WithPredicates(nodeSchedulingChanged())).
		// A replicated volume provisioned after the last pass would otherwise stay at
		// its old replica count until the next node event. The mapper enqueues every
		// cluster whatever object it is given.
		Watches(&corev1.PersistentVolume{}, handler.EnqueueRequestsFromMapFunc(r.clustersForNodeChange), builder.WithPredicates(replicatedVolumeBound())).
		// A project starting to require node tags needs a storage class for them, so its databases
		// are not left waiting for the next node event.
		Watches(&geassv1alpha1.GeassProject{}, handler.EnqueueRequestsFromMapFunc(r.clustersForNodeChange), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("geasscluster").
		Build(r)
	if err != nil {
		return err
	}
	// The issuer is watched too, so one that is deleted or edited is put back at once. Its kind belongs to
	// cert-manager, which this controller installs, so the watch is added later: startClusterIssuerWatch.
	r.watchClusterIssuers = func() error {
		issuer := &unstructured.Unstructured{}
		issuer.SetGroupVersionKind(platform.ClusterIssuerGVK)
		return c.Watch(source.Kind(mgr.GetCache(), client.Object(issuer),
			handler.EnqueueRequestsFromMapFunc(r.clustersForNodeChange), predicate.NewPredicateFuncs(isGeassClusterIssuer)))
	}
	return nil
}

// isGeassClusterIssuer picks out the one issuer that the ingresses ask for: whoever made it, a change to it
// matters, and no other issuer does.
func isGeassClusterIssuer(obj client.Object) bool {
	return obj.GetName() == platform.ClusterIssuerName
}
