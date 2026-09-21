/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testDevTargetNS}})
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testHelmChartNS}})
	})

	AfterEach(func() {
		reconciler := &GeassObjectStoreReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		var list geassv1alpha1.GeassObjectStoreList
		_ = k8sClient.List(ctx, &list, client.InNamespace(ns))
		for i := range list.Items {
			item := &list.Items[i]
			_ = k8sClient.Delete(ctx, item)
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: item.Name, Namespace: ns}})
		}
	})

	It("creates one cluster MinIO HelmChart and connection secret", func() {
		store := &geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterMinIOName, Namespace: ns},
			Spec: geassv1alpha1.GeassObjectStoreSpec{
				Engine:    geassv1alpha1.ObjectStoreEngineMinIO,
				Placement: geassv1alpha1.ObjectStorePlacementInCluster,
			},
		}
		Expect(k8sClient.Create(ctx, store)).To(Succeed())

		reconciler := &GeassObjectStoreReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: platform.ClusterMinIOName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: platform.ClusterMinIOName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		markHelmChartReady(ctx, platform.ClusterMinIOChartName)

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: platform.ClusterMinIOName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterMinIOName + "-connection", Namespace: ns}, secret)).To(Succeed())
		endpoint := string(secret.Data[platform.ConnectionKeyEndpoint])
		if endpoint == "" {
			endpoint = secret.StringData[platform.ConnectionKeyEndpoint]
		}
		Expect(endpoint).To(ContainSubstring(platform.ClusterMinIOChartName + "." + platform.SystemNamespace + ".svc"))

		latest := &geassv1alpha1.GeassObjectStore{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterMinIOName, Namespace: ns}, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeTrue())

		root := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterMinIOName + "-root", Namespace: ns}, root)).To(Succeed())
		rootUser := string(root.Data["rootUser"])
		if rootUser == "" {
			rootUser = root.StringData["rootUser"]
		}
		Expect(rootUser).NotTo(BeEmpty())
		Expect(rootUser).NotTo(Equal(platform.ClusterMinIOName + "-access"))
		chart := &helmv1.HelmChart{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterMinIOChartName, Namespace: testHelmChartNS}, chart)).To(Succeed())
		Expect(chart.Spec.ValuesContent).To(ContainSubstring("existingSecret:"))
		Expect(chart.Spec.ValuesContent).NotTo(ContainSubstring("rootUser:"))
	})

	It("does not provision a project bucket until cluster MinIO exists", func() {
		store := &geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: testObjectStoreName, Namespace: ns},
			Spec: geassv1alpha1.GeassObjectStoreSpec{
				Project:     testProjectName,
				Environment: geassv1alpha1.EnvironmentDev,
				Engine:      geassv1alpha1.ObjectStoreEngineMinIO,
			},
		}
		Expect(k8sClient.Create(ctx, store)).To(Succeed())

		reconciler := &GeassObjectStoreReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testObjectStoreName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testObjectStoreName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		latest := &geassv1alpha1.GeassObjectStore{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testObjectStoreName, Namespace: ns}, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeFalse())
		Expect(conditionMessage(latest.Status.Conditions, platform.ConditionReady)).To(ContainSubstring("cluster settings"))

		chart := &helmv1.HelmChart{}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: "geass-minio-assets", Namespace: testHelmChartNS}, chart)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("creates a project bucket on the cluster MinIO server", func() {
		var createdPath string
		s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			createdPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		}))
		DeferCleanup(s3.Close)

		server := &geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterMinIOName, Namespace: ns},
			Spec: geassv1alpha1.GeassObjectStoreSpec{
				Engine:    geassv1alpha1.ObjectStoreEngineMinIO,
				Placement: geassv1alpha1.ObjectStorePlacementInCluster,
			},
		}
		Expect(k8sClient.Create(ctx, server)).To(Succeed())
		reconciler := &GeassObjectStoreReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), HTTP: s3.Client()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: platform.ClusterMinIOName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: platform.ClusterMinIOName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		markHelmChartReady(ctx, platform.ClusterMinIOChartName)
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: platform.ClusterMinIOName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		latestServer := &geassv1alpha1.GeassObjectStore{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterMinIOName, Namespace: ns}, latestServer)).To(Succeed())
		latestServer.Status.Endpoint = s3.URL
		Expect(k8sClient.Status().Update(ctx, latestServer)).To(Succeed())
		markHelmChartNotReady(ctx, platform.ClusterMinIOChartName)

		store := &geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: testObjectStoreName, Namespace: ns},
			Spec: geassv1alpha1.GeassObjectStoreSpec{
				Project:     testProjectName,
				Environment: geassv1alpha1.EnvironmentDev,
				Engine:      geassv1alpha1.ObjectStoreEngineMinIO,
				Buckets:     []string{"uploads"},
			},
		}
		Expect(k8sClient.Create(ctx, store)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testObjectStoreName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testObjectStoreName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		pending := &geassv1alpha1.GeassObjectStore{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testObjectStoreName, Namespace: ns}, pending)).To(Succeed())
		Expect(conditionIsTrue(pending.Status.Conditions, platform.ConditionReady)).To(BeFalse())
		Expect(conditionMessage(pending.Status.Conditions, platform.ConditionReady)).To(ContainSubstring("HelmChart"))

		markHelmChartReady(ctx, platform.ClusterMinIOChartName)
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testObjectStoreName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		Expect(createdPath).To(Equal("/uploads"))
		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "assets-connection", Namespace: testDevTargetNS}, secret)).To(Succeed())
		projectAccess := string(secret.Data[platform.ConnectionKeyAccessKey])
		if projectAccess == "" {
			projectAccess = secret.StringData[platform.ConnectionKeyAccessKey]
		}
		root := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterMinIOName + "-root", Namespace: ns}, root)).To(Succeed())
		rootUser := string(root.Data["rootUser"])
		if rootUser == "" {
			rootUser = root.StringData["rootUser"]
		}
		Expect(projectAccess).NotTo(BeEmpty())
		Expect(projectAccess).NotTo(Equal(rootUser))
		userSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "assets-minio-user", Namespace: ns}, userSecret)).To(Succeed())
		chart := &helmv1.HelmChart{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterMinIOChartName, Namespace: testHelmChartNS}, chart)).To(Succeed())
		Expect(chart.Spec.ValuesContent).To(ContainSubstring("policy: \"geass-assets\""))
		Expect(chart.Spec.ValuesContent).To(ContainSubstring("existingSecret: \"assets-minio-user\""))
		Expect(chart.Spec.ValuesContent).To(ContainSubstring("existingSecretKey: secretKey"))
		Expect(chart.Spec.ValuesContent).To(ContainSubstring("arn:aws:s3:::uploads"))
		Expect(chart.Spec.ValuesContent).NotTo(ContainSubstring("secretKey: \""))
		err = k8sClient.Get(ctx, types.NamespacedName{Name: "geass-minio-assets", Namespace: testHelmChartNS}, &helmv1.HelmChart{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		latest := &geassv1alpha1.GeassObjectStore{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testObjectStoreName, Namespace: ns}, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeTrue())
	})

	It("deletes the cluster HelmChart only when the cluster MinIO server is removed", func() {
		server := &geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterMinIOName, Namespace: ns},
			Spec: geassv1alpha1.GeassObjectStoreSpec{
				Engine: geassv1alpha1.ObjectStoreEngineMinIO,
			},
		}
		Expect(k8sClient.Create(ctx, server)).To(Succeed())
		reconciler := &GeassObjectStoreReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: platform.ClusterMinIOName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: platform.ClusterMinIOName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		markHelmChartReady(ctx, platform.ClusterMinIOChartName)

		store := &geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: testTempStoreName, Namespace: ns},
			Spec: geassv1alpha1.GeassObjectStoreSpec{
				Project:     testProjectName,
				Environment: geassv1alpha1.EnvironmentDev,
				Engine:      geassv1alpha1.ObjectStoreEngineMinIO,
			},
		}
		Expect(k8sClient.Create(ctx, store)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testTempStoreName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Delete(ctx, store)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: testTempStoreName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		chart := &helmv1.HelmChart{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterMinIOChartName, Namespace: testHelmChartNS}, chart)).To(Succeed())

		Expect(k8sClient.Delete(ctx, server)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: platform.ClusterMinIOName, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterMinIOChartName, Namespace: testHelmChartNS}, chart)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
})
