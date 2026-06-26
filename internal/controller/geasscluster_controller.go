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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/helmchart"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

const certManagerValues = `installCRDs: true
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
prometheus:
  prometheusSpec:
    serviceMonitorSelectorNilUsesHelmValues: false
prometheusOperator:
  enabled: true
`

// GeassClusterReconciler reconciles a GeassCluster object.
type GeassClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=helm.cattle.io,resources=helmcharts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch

func (r *GeassClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cluster geassv1alpha1.GeassCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	workspacesReady, err := r.reconcileWorkspaces(ctx)
	if err != nil {
		return r.updateStatus(ctx, &cluster, false, false, err)
	}

	addonsReady, err := r.reconcileAddons(ctx, &cluster)
	if err != nil {
		return r.updateStatus(ctx, &cluster, workspacesReady, false, err)
	}

	return r.updateStatus(ctx, &cluster, workspacesReady, addonsReady, nil)
}

func (r *GeassClusterReconciler) reconcileWorkspaces(ctx context.Context) (bool, error) {
	labels := map[string]string{
		"geass.dev/managed-by": "geass-cluster",
	}
	for _, ws := range platform.DefaultWorkspaces {
		ns, err := platform.WorkspaceNamespace(ws)
		if err != nil {
			return false, err
		}
		if err := ensureNamespace(ctx, r.Client, ns, labels); err != nil {
			return false, fmt.Errorf("ensure workspace namespace %s: %w", ns, err)
		}
	}
	return true, nil
}

func (r *GeassClusterReconciler) reconcileAddons(ctx context.Context, cluster *geassv1alpha1.GeassCluster) (bool, error) {
	log := logf.FromContext(ctx)
	allReady := true

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
			return false, err
		}
		ready, err := r.helmChartReady(ctx, platform.CertManagerChartName)
		if err != nil {
			return false, err
		}
		if !ready {
			allReady = false
			log.Info("Waiting for cert-manager HelmChart")
		}
	} else if err := helmchart.Delete(ctx, r.Client, platform.CertManagerChartName); err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}

	if geassv1alpha1.AddonEnabled(cluster.Spec.Addons.Monitoring.Enabled) {
		values := monitoringLiteValues
		if cluster.Spec.Addons.Monitoring.Profile == "full" {
			values = ""
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
			return false, err
		}
		ready, err := r.helmChartReady(ctx, platform.MonitoringChartName)
		if err != nil {
			return false, err
		}
		if !ready {
			allReady = false
			log.Info("Waiting for monitoring HelmChart")
		}
	} else if err := helmchart.Delete(ctx, r.Client, platform.MonitoringChartName); err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}

	return allReady, nil
}

func (r *GeassClusterReconciler) helmChartReady(ctx context.Context, name string) (bool, error) {
	chart, err := helmchart.Get(ctx, r.Client, name)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return helmchart.IsReady(chart), nil
}

func (r *GeassClusterReconciler) updateStatus(ctx context.Context, cluster *geassv1alpha1.GeassCluster, workspacesReady, addonsReady bool, reconcileErr error) (ctrl.Result, error) {
	latest := cluster.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(cluster), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	conditions := latest.Status.Conditions
	if workspacesReady {
		conditions = platform.SetCondition(conditions, platform.ConditionWorkspacesReady, metav1.ConditionTrue, "WorkspacesReady", platform.ConditionMessage(platform.ConditionWorkspacesReady, true))
	} else {
		conditions = platform.SetCondition(conditions, platform.ConditionWorkspacesReady, metav1.ConditionFalse, "WorkspacesPending", platform.ConditionMessage(platform.ConditionWorkspacesReady, false))
	}

	if reconcileErr != nil {
		conditions = platform.SetCondition(conditions, platform.ConditionAddonsReady, metav1.ConditionFalse, "ReconcileError", reconcileErr.Error())
		conditions = platform.SetCondition(conditions, platform.ConditionReady, metav1.ConditionFalse, "ReconcileError", reconcileErr.Error())
	} else if addonsReady {
		conditions = platform.SetCondition(conditions, platform.ConditionAddonsReady, metav1.ConditionTrue, "AddonsReady", platform.ConditionMessage(platform.ConditionAddonsReady, true))
	} else {
		conditions = platform.SetCondition(conditions, platform.ConditionAddonsReady, metav1.ConditionFalse, "AddonsPending", platform.ConditionMessage(platform.ConditionAddonsReady, false))
	}

	overallReady := workspacesReady && addonsReady && reconcileErr == nil
	if overallReady {
		conditions = platform.SetCondition(conditions, platform.ConditionReady, metav1.ConditionTrue, "ClusterReady", "Geass cluster is ready")
		latest.Status.Phase = geassv1alpha1.ClusterPhaseReady
	} else {
		if reconcileErr == nil {
			conditions = platform.SetCondition(conditions, platform.ConditionReady, metav1.ConditionFalse, "ClusterPending", "Geass cluster is not ready")
		}
		latest.Status.Phase = ""
	}

	latest.Status.Conditions = conditions
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}

	if reconcileErr != nil {
		return ctrl.Result{}, reconcileErr
	}
	if !overallReady {
		return ctrl.Result{RequeueAfter: platform.RequeueAfterDefault}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassCluster{}).
		Named("geasscluster").
		Complete(r)
}
