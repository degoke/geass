//go:build k3sintegration

package k3sintegration

import (
	"context"
	"os"
	"testing"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/controller"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

// k3sRestConfig is the cluster the tests run against: the one the current kubeconfig (or KUBECONFIG) names.
// hack/k3s-up.sh starts a throwaway k3s in Docker and prints the settings for it.
func k3sRestConfig(t *testing.T) *rest.Config {
	t.Helper()
	cfg, err := ctrl.GetConfig()
	if err != nil {
		t.Fatalf("no kubeconfig for the k3s cluster: %v", err)
	}
	return cfg
}

// startOperator runs the cluster, project and app controllers in the test process against the k3s cluster, as the
// operator would, then creates a project with one environment and waits for it to be ready. It returns the context the
// controllers run in and a client for the cluster. What it created is removed when the test ends, and the controllers stop
// last, because removing the project needs them.
func startOperator(t *testing.T, projectName, environment string) (context.Context, client.Client) {
	t.Helper()
	namespace := projectName + "-" + environment
	cfg := k3sRestConfig(t)
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, geassv1alpha1.AddToScheme, helmv1.AddToScheme, monitoringv1.AddToScheme, cnpgv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(false)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, Cache: controller.CacheOptions(), Metrics: metricsserver.Options{BindAddress: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, setup := range []func(ctrl.Manager) error{
		(&controller.GeassClusterReconciler{Client: mgr.GetClient(), Scheme: scheme}).SetupWithManager,
		(&controller.GeassProjectReconciler{Client: mgr.GetClient(), Scheme: scheme}).SetupWithManager,
		(&controller.GeassAppReconciler{Client: mgr.GetClient(), Scheme: scheme}).SetupWithManager,
	} {
		if err := setup(mgr); err != nil {
			t.Fatal(err)
		}
	}
	go func() {
		if err := mgr.Start(ctx); err != nil {
			t.Errorf("manager: %v", err)
		}
	}()
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platform.SystemNamespace}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	if err := c.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "geass-token", Namespace: platform.SystemNamespace}, StringData: map[string]string{"token": "x"}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	disabled := false
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{
			Version: "v1", ServerURL: "https://127.0.0.1:6443",
			TokenSecretRef: corev1.SecretReference{Name: "geass-token", Namespace: platform.SystemNamespace},
			Addons: geassv1alpha1.GeassClusterAddonsSpec{
				CertManager: geassv1alpha1.GeassClusterCertManagerAddon{Enabled: &disabled},
				Monitoring:  geassv1alpha1.GeassClusterMonitoringAddon{Enabled: &disabled},
			},
		},
	}
	if err := c.Create(ctx, cluster); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: projectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: "default"}, Environments: []string{environment}},
	}
	if err := c.Create(ctx, project); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Delete(context.Background(), project)
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			if apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKey{Name: namespace}, &corev1.Namespace{})) {
				return
			}
			time.Sleep(2 * time.Second)
		}
	})
	eventually(t, 2*time.Minute, "the project to be ready", func() (bool, string) {
		var latest geassv1alpha1.GeassProject
		if err := c.Get(ctx, client.ObjectKeyFromObject(project), &latest); err != nil {
			return false, err.Error()
		}
		return apimeta.IsStatusConditionTrue(latest.Status.Conditions, platform.ConditionReady), "not ready yet"
	})
	return ctx, c
}
