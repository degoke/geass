package controller

import (
	appsv1 "k8s.io/api/apps/v1"

	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/helmchart"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
)

func deploymentReady(deploy *appsv1.Deployment, desired int32) bool {
	if deploy == nil {
		return false
	}
	if deploy.Status.ObservedGeneration < deploy.Generation {
		return false
	}
	if desired == 0 {
		return true
	}
	return deploy.Status.AvailableReplicas >= desired
}

func cnpgClusterReady(cluster *cnpgv1.Cluster) bool {
	if cluster == nil {
		return false
	}
	if cluster.Status.Phase == "Cluster in healthy state" {
		return true
	}
	instances := cluster.Spec.Instances
	if instances == 0 {
		instances = 1
	}
	return cluster.Status.ReadyInstances >= instances
}

func helmChartReady(chart *helmv1.HelmChart) bool {
	return helmchart.IsReady(chart)
}
