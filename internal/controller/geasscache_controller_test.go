/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

var _ = Describe("GeassCache Controller", func() {
	ctx := context.Background()
	const ns = platform.SystemNamespace

	BeforeEach(func() {
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "geass-dev"}})
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}})
	})

	It("creates Redis HelmChart and connection secret", func() {
		cache := &geassv1alpha1.GeassCache{
			ObjectMeta: metav1.ObjectMeta{Name: "sessions", Namespace: ns},
			Spec: geassv1alpha1.GeassCacheSpec{
				Workspace: geassv1alpha1.WorkspaceDev,
				Engine:    geassv1alpha1.CacheEngineRedis,
			},
		}
		Expect(k8sClient.Create(ctx, cache)).To(Succeed())

		reconciler := &GeassCacheReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "sessions", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "sessions", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		markHelmChartReady(ctx, "geass-redis-sessions")

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "sessions", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "sessions-connection", Namespace: "geass-dev"}, secret)).To(Succeed())

		latest := &geassv1alpha1.GeassCache{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "sessions", Namespace: ns}, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeTrue())
	})

	It("deletes HelmChart on resource deletion", func() {
		cache := &geassv1alpha1.GeassCache{
			ObjectMeta: metav1.ObjectMeta{Name: "temp-cache", Namespace: ns},
			Spec: geassv1alpha1.GeassCacheSpec{
				Workspace: geassv1alpha1.WorkspaceDev,
				Engine:    geassv1alpha1.CacheEngineRedis,
			},
		}
		Expect(k8sClient.Create(ctx, cache)).To(Succeed())

		reconciler := &GeassCacheReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "temp-cache", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "temp-cache", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		markHelmChartReady(ctx, "geass-redis-temp-cache")

		Expect(k8sClient.Delete(ctx, cache)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "temp-cache", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		chart := &helmv1.HelmChart{}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: "geass-redis-temp-cache", Namespace: "kube-system"}, chart)
		Expect(err).To(HaveOccurred())
	})
})
