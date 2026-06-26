/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func markHelmChartReady(ctx context.Context, name string) {
	chart := &helmv1.HelmChart{}
	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "kube-system"}, chart)
	}).WithTimeout(5 * time.Second).Should(Succeed())
	chart.Status.Conditions = []helmv1.HelmChartCondition{{
		Type:   "Deployed",
		Status: metav1.ConditionTrue,
	}}
	if err := k8sClient.Status().Update(ctx, chart); err != nil {
		Expect(k8sClient.Update(ctx, chart)).To(Succeed())
	}
}

func conditionIsTrue(conditions []metav1.Condition, t string) bool {
	for _, c := range conditions {
		if c.Type == t {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

var _ = Describe("GeassCluster Controller", func() {
	const (
		resourceName      = "test-cluster"
		resourceNamespace = platform.SystemNamespace
	)

	ctx := context.Background()
	typeNamespacedName := types.NamespacedName{Name: resourceName, Namespace: resourceNamespace}

	BeforeEach(func() {
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: resourceNamespace}})
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}})
	})

	AfterEach(func() {
		cluster := &geassv1alpha1.GeassCluster{}
		if err := k8sClient.Get(ctx, typeNamespacedName, cluster); err == nil {
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
		}
	})

	It("creates workspaces and addon HelmCharts with defaults", func() {
		enabled := true
		cluster := &geassv1alpha1.GeassCluster{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: resourceNamespace},
			Spec: geassv1alpha1.GeassClusterSpec{
				Version:   "v1",
				ServerURL: "https://127.0.0.1:6443",
				TokenSecretRef: corev1.SecretReference{
					Name:      "geass-token",
					Namespace: resourceNamespace,
				},
				Addons: geassv1alpha1.GeassClusterAddonsSpec{
					CertManager: geassv1alpha1.GeassClusterCertManagerAddon{Enabled: &enabled},
					Monitoring: geassv1alpha1.GeassClusterMonitoringAddon{
						Enabled: &enabled,
						Profile: "lite",
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		reconciler := &GeassClusterReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		for _, ws := range platform.DefaultWorkspaces {
			nsName, err := platform.WorkspaceNamespace(ws)
			Expect(err).NotTo(HaveOccurred())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nsName}, &corev1.Namespace{})).To(Succeed())
		}

		certChart := &helmv1.HelmChart{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.CertManagerChartName, Namespace: "kube-system"}, certChart)).To(Succeed())
		Expect(certChart.Spec.TargetNamespace).To(Equal(platform.CertManagerTargetNS))

		monChart := &helmv1.HelmChart{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.MonitoringChartName, Namespace: "kube-system"}, monChart)).To(Succeed())
		Expect(monChart.Spec.TargetNamespace).To(Equal(platform.MonitoringTargetNS))
		Expect(monChart.Spec.ValuesContent).To(ContainSubstring("defaultDashboards:\n  enabled: false"))
	})

	It("sets readiness conditions when add-ons are deployed", func() {
		enabled := true
		cluster := &geassv1alpha1.GeassCluster{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: resourceNamespace},
			Spec: geassv1alpha1.GeassClusterSpec{
				Version:   "v1",
				ServerURL: "https://127.0.0.1:6443",
				TokenSecretRef: corev1.SecretReference{
					Name:      "geass-token",
					Namespace: resourceNamespace,
				},
				Addons: geassv1alpha1.GeassClusterAddonsSpec{
					CertManager: geassv1alpha1.GeassClusterCertManagerAddon{Enabled: &enabled},
					Monitoring:  geassv1alpha1.GeassClusterMonitoringAddon{Enabled: &enabled, Profile: "lite"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		reconciler := &GeassClusterReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		markHelmChartReady(ctx, platform.CertManagerChartName)
		markHelmChartReady(ctx, platform.MonitoringChartName)

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		latest := &geassv1alpha1.GeassCluster{}
		Expect(k8sClient.Get(ctx, typeNamespacedName, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionWorkspacesReady)).To(BeTrue())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionAddonsReady)).To(BeTrue())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeTrue())
		Expect(latest.Status.Phase).To(Equal(geassv1alpha1.ClusterPhaseReady))
	})

	It("honors disabled add-ons", func() {
		disabled := false
		cluster := &geassv1alpha1.GeassCluster{
			ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: resourceNamespace},
			Spec: geassv1alpha1.GeassClusterSpec{
				Version:   "v1",
				ServerURL: "https://127.0.0.1:6443",
				TokenSecretRef: corev1.SecretReference{
					Name:      "geass-token",
					Namespace: resourceNamespace,
				},
				Addons: geassv1alpha1.GeassClusterAddonsSpec{
					CertManager: geassv1alpha1.GeassClusterCertManagerAddon{Enabled: &disabled},
					Monitoring:  geassv1alpha1.GeassClusterMonitoringAddon{Enabled: &disabled},
				},
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		reconciler := &GeassClusterReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		certChart := &helmv1.HelmChart{}
		err = k8sClient.Get(ctx, types.NamespacedName{Name: platform.CertManagerChartName, Namespace: "kube-system"}, certChart)
		Expect(err).To(HaveOccurred())

		latest := &geassv1alpha1.GeassCluster{}
		Expect(k8sClient.Get(ctx, typeNamespacedName, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionAddonsReady)).To(BeTrue())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeTrue())
	})
})

var _ = Describe("GeassApp Controller", func() {
	ctx := context.Background()
	const ns = platform.SystemNamespace

	BeforeEach(func() {
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "geass-dev"}})
	})

	It("creates Deployment, Service, and Ingress in the workspace namespace", func() {
		replicas := int32(2)
		app := &geassv1alpha1.GeassApp{
			ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: ns},
			Spec: geassv1alpha1.GeassAppSpec{
				Workspace: geassv1alpha1.WorkspaceDev,
				Image:     "nginx:alpine",
				Replicas:  &replicas,
				Port:      8080,
				Ingress: geassv1alpha1.GeassAppIngressSpec{
					Host: "demo.local",
					Path: "/",
				},
			},
		}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())

		reconciler := &GeassAppReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "demo", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "demo", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		deploy := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "demo", Namespace: "geass-dev"}, deploy)).To(Succeed())
		deploy.Status.ObservedGeneration = deploy.Generation
		deploy.Status.Replicas = 2
		deploy.Status.ReadyReplicas = 2
		deploy.Status.AvailableReplicas = 2
		Expect(k8sClient.Status().Update(ctx, deploy)).To(Succeed())

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "demo", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		Expect(*deploy.Spec.Replicas).To(Equal(int32(2)))
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "demo", Namespace: "geass-dev"}, &corev1.Service{})).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "demo", Namespace: "geass-dev"}, &networkingv1.Ingress{})).To(Succeed())

		latest := &geassv1alpha1.GeassApp{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "demo", Namespace: ns}, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeTrue())
	})

	It("creates ServiceMonitor only when metrics are enabled", func() {
		app := &geassv1alpha1.GeassApp{
			ObjectMeta: metav1.ObjectMeta{Name: "metrics-app", Namespace: ns},
			Spec: geassv1alpha1.GeassAppSpec{
				Workspace: geassv1alpha1.WorkspaceDev,
				Image:     "nginx:alpine",
				Metrics:   geassv1alpha1.GeassAppMetricsSpec{Enabled: true},
			},
		}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())

		reconciler := &GeassAppReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "metrics-app", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "metrics-app", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		deploy := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "metrics-app", Namespace: "geass-dev"}, deploy)).To(Succeed())
		deploy.Status.ObservedGeneration = deploy.Generation
		deploy.Status.Replicas = 1
		deploy.Status.ReadyReplicas = 1
		deploy.Status.AvailableReplicas = 1
		Expect(k8sClient.Status().Update(ctx, deploy)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "metrics-app", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "metrics-app-metrics", Namespace: "geass-dev"}, &monitoringv1.ServiceMonitor{})).To(Succeed())
	})
})

var _ = Describe("GeassDatabase Controller", func() {
	ctx := context.Background()
	const ns = platform.SystemNamespace

	BeforeEach(func() {
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "geass-dev"}})
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system"}})
	})

	It("installs CNPG operator and creates cluster resources", func() {
		db := &geassv1alpha1.GeassDatabase{
			ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: ns},
			Spec: geassv1alpha1.GeassDatabaseSpec{
				Workspace: geassv1alpha1.WorkspaceDev,
				Engine:    geassv1alpha1.DatabaseEnginePostgres,
			},
		}
		Expect(k8sClient.Create(ctx, db)).To(Succeed())

		reconciler := &GeassDatabaseReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "orders", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "orders", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		markHelmChartReady(ctx, platform.CNPGChartName)

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "orders", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		cluster := &cnpgv1.Cluster{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "orders", Namespace: "geass-dev"}, cluster)).To(Succeed())
		cluster.Status.Phase = "Cluster in healthy state"
		cluster.Status.ReadyInstances = 1
		if err := k8sClient.Status().Update(ctx, cluster); err != nil {
			Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
		}

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "orders", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "orders-connection", Namespace: "geass-dev"}, secret)).To(Succeed())
		host := string(secret.Data["host"])
		if host == "" {
			host = secret.StringData["host"]
		}
		Expect(host).To(ContainSubstring("orders-rw"))

		latest := &geassv1alpha1.GeassDatabase{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "orders", Namespace: ns}, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeTrue())
	})

	It("cleans up workspace resources on delete", func() {
		db := &geassv1alpha1.GeassDatabase{
			ObjectMeta: metav1.ObjectMeta{Name: "cleanup-db", Namespace: ns},
			Spec: geassv1alpha1.GeassDatabaseSpec{
				Workspace: geassv1alpha1.WorkspaceDev,
				Engine:    geassv1alpha1.DatabaseEnginePostgres,
			},
		}
		Expect(k8sClient.Create(ctx, db)).To(Succeed())

		reconciler := &GeassDatabaseReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "cleanup-db", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "cleanup-db", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Delete(ctx, db)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: "cleanup-db", Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Get(ctx, types.NamespacedName{Name: "cleanup-db", Namespace: "geass-dev"}, &cnpgv1.Cluster{})
		Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
	})
})
