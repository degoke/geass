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

var _ = Describe("GeassObjectStore Controller", func() {
	ctx := context.Background()
	const ns = platform.SystemNamespace

	BeforeEach(func() {
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testDevWorkspaceNS}})
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testHelmChartNS}})
	})

	It("creates MinIO HelmChart and connection secret", func() {
		store := &geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: testObjectStoreName, Namespace: ns},
			Spec: geassv1alpha1.GeassObjectStoreSpec{
				Workspace: geassv1alpha1.WorkspaceDev,
				Engine:    geassv1alpha1.ObjectStoreEngineMinIO,
			},
		}
		Expect(k8sClient.Create(ctx, store)).To(Succeed())

		reconciler := &GeassObjectStoreReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testObjectStoreName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testObjectStoreName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		markHelmChartReady(ctx, "geass-minio-assets")

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testObjectStoreName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "assets-connection", Namespace: testDevWorkspaceNS}, secret)).To(Succeed())
		endpoint := string(secret.Data["endpoint"])
		if endpoint == "" {
			endpoint = secret.StringData["endpoint"]
		}
		Expect(endpoint).To(ContainSubstring("geass-minio-assets.geass-dev.svc"))

		latest := &geassv1alpha1.GeassObjectStore{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testObjectStoreName, Namespace: ns}, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeTrue())
	})

	It("deletes HelmChart on resource deletion", func() {
		store := &geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: testTempStoreName, Namespace: ns},
			Spec: geassv1alpha1.GeassObjectStoreSpec{
				Workspace: geassv1alpha1.WorkspaceDev,
				Engine:    geassv1alpha1.ObjectStoreEngineMinIO,
			},
		}
		Expect(k8sClient.Create(ctx, store)).To(Succeed())

		reconciler := &GeassObjectStoreReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testTempStoreName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testTempStoreName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		markHelmChartReady(ctx, "geass-minio-temp-store")

		Expect(k8sClient.Delete(ctx, store)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testTempStoreName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		chart := &helmv1.HelmChart{}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: "geass-minio-temp-store", Namespace: testHelmChartNS}, chart)
		Expect(err).To(HaveOccurred())
	})
})
