package controller

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
)

const (
	messageInstallingChart = "Installing chart"
	messageStartingService = "Starting service"
	messageServiceStarting = "Service is starting"
	messageServiceFailed   = "Service failed to start"
)

func podConditionsReady(pod corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func waitForHelmChart(ctx context.Context, c client.Client, chart *helmv1.HelmChart) (installed bool, err error) {
	ready, err := helmChartReady(ctx, c, chart)
	if err != nil {
		return false, err
	}
	return ready, nil
}

func isHelmReleasePod(pod *corev1.Pod, namespace, release string) bool {
	if pod == nil || pod.Namespace != namespace || release == "" {
		return false
	}
	if pod.Labels["app.kubernetes.io/instance"] == release {
		return true
	}
	if pod.Labels["release"] == release {
		return true
	}
	// A pod labelled as part of another release is never ours, whatever its name starts with.
	if pod.Labels["app.kubernetes.io/instance"] != "" || pod.Labels["release"] != "" {
		return false
	}
	return strings.HasPrefix(pod.Name, release+"-")
}

func listHelmReleasePods(ctx context.Context, c client.Client, namespace, release string) ([]corev1.Pod, error) {
	seen := map[string]struct{}{}
	var pods []corev1.Pod
	add := func(items []corev1.Pod) {
		for i := range items {
			pod := items[i]
			if !isHelmReleasePod(&pod, namespace, release) {
				continue
			}
			if _, ok := seen[pod.Name]; ok {
				continue
			}
			seen[pod.Name] = struct{}{}
			pods = append(pods, pod)
		}
	}
	labelSets := []map[string]string{
		{"app.kubernetes.io/instance": release},
		{"release": release},
	}
	for _, labels := range labelSets {
		var list corev1.PodList
		if err := c.List(ctx, &list, client.InNamespace(namespace), client.MatchingLabels(labels)); err != nil {
			return nil, err
		}
		add(list.Items)
	}
	if len(pods) == 0 {
		var list corev1.PodList
		if err := c.List(ctx, &list, client.InNamespace(namespace)); err != nil {
			return nil, err
		}
		add(list.Items)
	}
	return pods, nil
}

func statefulSetReady(sts *appsv1.StatefulSet, desired int32) bool {
	if sts == nil {
		return false
	}
	if sts.Status.ObservedGeneration < sts.Generation {
		return false
	}
	if desired <= 0 {
		desired = 1
	}
	return sts.Status.ReadyReplicas >= desired
}

// helmReleaseWorkloadReady reports whether Helm release workloads have enough ready pods,
// or a ready Deployment/StatefulSet named like the release.
func helmReleaseWorkloadReady(ctx context.Context, c client.Client, namespace, release string, minReady int32) (bool, string, error) {
	if minReady <= 0 {
		minReady = 1
	}
	pods, err := listHelmReleasePods(ctx, c, namespace, release)
	if err != nil {
		return false, "", err
	}
	readyCount := 0
	failed := 0
	starting := 0
	for _, pod := range pods {
		switch pod.Status.Phase {
		case corev1.PodRunning:
			if podConditionsReady(pod) {
				readyCount++
			} else {
				starting++
			}
		case corev1.PodFailed:
			failed++
		default:
			starting++
		}
	}
	if readyCount >= int(minReady) {
		return true, "", nil
	}
	if len(pods) == 0 {
		deploy := &appsv1.Deployment{}
		if err := c.Get(ctx, client.ObjectKey{Name: release, Namespace: namespace}, deploy); err == nil {
			if deploymentReady(deploy, minReady) {
				return true, "", nil
			}
			return false, messageServiceStarting, nil
		}
		sts := &appsv1.StatefulSet{}
		if err := c.Get(ctx, client.ObjectKey{Name: release, Namespace: namespace}, sts); err == nil {
			if statefulSetReady(sts, minReady) {
				return true, "", nil
			}
			return false, messageServiceStarting, nil
		}
		return false, messageStartingService, nil
	}
	if failed > 0 && starting == 0 && readyCount == 0 {
		return false, messageServiceFailed, nil
	}
	return false, messageServiceStarting, nil
}

func installingChartMessage(component string) string {
	if strings.TrimSpace(component) == "" {
		return messageInstallingChart
	}
	return fmt.Sprintf("Installing %s chart", component)
}
