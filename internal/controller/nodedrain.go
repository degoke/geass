/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

// valueTrue is the value of the annotations and labels that are switches.
const valueTrue = "true"

// +kubebuilder:rbac:groups="",resources=pods/eviction,verbs=create
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// cordonNode stops new pods being scheduled on a node, and remembers that Geass did it. A node that an
// administrator had cordoned is left as it is and not marked, so that it is never uncordoned behind their back.
func cordonNode(ctx context.Context, c client.Client, name string, now time.Time) error {
	var node corev1.Node
	if err := c.Get(ctx, client.ObjectKey{Name: name}, &node); err != nil {
		return client.IgnoreNotFound(err)
	}
	if node.Spec.Unschedulable {
		return nil
	}
	base := node.DeepCopy()
	node.Spec.Unschedulable = true
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[platform.AnnotationPoolCordon] = valueTrue
	node.Annotations[platform.AnnotationPoolCordonAt] = now.UTC().Format(time.RFC3339)
	return client.IgnoreNotFound(c.Patch(ctx, &node, client.MergeFrom(base)))
}

// uncordonNode undoes cordonNode, and only when Geass is what cordoned the node.
func uncordonNode(ctx context.Context, c client.Client, name string) error {
	var node corev1.Node
	if err := c.Get(ctx, client.ObjectKey{Name: name}, &node); err != nil {
		return client.IgnoreNotFound(err)
	}
	if node.Annotations[platform.AnnotationPoolCordon] != valueTrue {
		return nil
	}
	base := node.DeepCopy()
	node.Spec.Unschedulable = false
	delete(node.Annotations, platform.AnnotationPoolCordon)
	delete(node.Annotations, platform.AnnotationPoolCordonAt)
	return client.IgnoreNotFound(c.Patch(ctx, &node, client.MergeFrom(base)))
}

// podsOnNode lists the pods that run on a node. The pods are the cache's own and are not to be changed.
func podsOnNode(ctx context.Context, c client.Reader, node string) ([]corev1.Pod, error) {
	var pods corev1.PodList
	if err := c.List(ctx, &pods, client.UnsafeDisableDeepCopy); err != nil {
		return nil, err
	}
	var out []corev1.Pod
	for i := range pods.Items {
		if pods.Items[i].Spec.NodeName == node {
			out = append(out, pods.Items[i])
		}
	}
	return out, nil
}

// drainProgress is how far emptying a node has got.
type drainProgress struct {
	// Remaining is how many pods that have to move are still on the node.
	Remaining int
	// Blocked is how many of them a disruption budget refused to let go yet.
	Blocked int
}

// Done reports whether the node holds nothing that has to move.
func (p drainProgress) Done() bool { return p.Remaining == 0 }

// evictFrom asks every movable pod on the node to leave, through the eviction API so that disruption budgets are
// honoured, and says what is left. A pod that is already terminating counts as remaining until it is gone. It is
// safe to call again and again; each call asks once more for the pods a budget refused.
func evictFrom(ctx context.Context, c client.Client, node string) (drainProgress, error) {
	pods, err := podsOnNode(ctx, c, node)
	if err != nil {
		return drainProgress{}, err
	}
	var progress drainProgress
	for i := range pods {
		pod := &pods[i]
		if !platform.Movable(pod) {
			continue
		}
		progress.Remaining++
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}
		eviction := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: pod.Namespace}}
		err := c.SubResource("eviction").Create(ctx, pod.DeepCopy(), eviction)
		switch {
		case err == nil, apierrors.IsNotFound(err):
			// Asked, or already gone.
		case apierrors.IsTooManyRequests(err):
			progress.Blocked++
		default:
			return progress, err
		}
	}
	return progress, nil
}
