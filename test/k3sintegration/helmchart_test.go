//go:build k3sintegration

package k3sintegration

import (
	"context"
	"os"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

func waitHelmChartDeployed(t *testing.T, ctx context.Context, c client.Client, chart *helmv1.HelmChart) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		latest := &helmv1.HelmChart{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(chart), latest); err != nil {
			t.Fatalf("get HelmChart: %v", err)
		}
		for _, cond := range latest.Status.Conditions {
			if cond.Type == "Deployed" && cond.Status == metav1.ConditionTrue {
				return
			}
		}
		time.Sleep(15 * time.Second)
	}
	t.Fatal("HelmChart did not reach Deployed=True before timeout")
}

func TestHelmChartReconcileOnK3s(t *testing.T) {
	if os.Getenv("K3S_INTEGRATION") != "true" {
		t.Skip("set K3S_INTEGRATION=true to run K3s HelmChart integration tests")
	}

	cfg := k3sRestConfig(t)
	_ = geassv1alpha1.AddToScheme(scheme.Scheme)
	_ = helmv1.AddToScheme(scheme.Scheme)

	c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	ctx := context.Background()
	chart := &helmv1.HelmChart{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "geass-it-cert-manager",
			Namespace: platform.HelmChartNamespace,
		},
		Spec: helmv1.HelmChartSpec{
			Chart:           platform.CertManagerReleaseChart,
			Repo:            platform.CertManagerChartRepo,
			Version:         platform.CertManagerChartVersion,
			TargetNamespace: platform.CertManagerTargetNS,
			CreateNamespace: true,
			ValuesContent:   "installCRDs: true\n",
		},
	}
	if err := c.Create(ctx, chart); err != nil {
		t.Fatalf("create HelmChart: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(ctx, chart) })
	waitHelmChartDeployed(t, ctx, c, chart)
}

func TestHelmChartUpdateOnK3s(t *testing.T) {
	if os.Getenv("K3S_INTEGRATION") != "true" {
		t.Skip("set K3S_INTEGRATION=true to run K3s HelmChart integration tests")
	}

	cfg := k3sRestConfig(t)
	_ = helmv1.AddToScheme(scheme.Scheme)
	c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	ctx := context.Background()
	name := "geass-it-redis"
	chart := &helmv1.HelmChart{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.HelmChartNamespace},
		Spec: helmv1.HelmChartSpec{
			Chart:           platform.RedisReleaseChart,
			Repo:            platform.RedisChartRepo,
			Version:         platform.RedisChartVersion,
			TargetNamespace: "geass-dev",
			CreateNamespace: true,
			ValuesContent:   "architecture: standalone\n",
		},
	}
	if err := c.Create(ctx, chart); err != nil {
		t.Fatalf("create redis HelmChart: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(ctx, chart) })
	waitHelmChartDeployed(t, ctx, c, chart)

	latest := &helmv1.HelmChart{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(chart), latest); err != nil {
		t.Fatalf("get chart: %v", err)
	}
	if latest.Status.JobName == "" && len(latest.Status.Conditions) == 0 {
		t.Fatal("expected HelmChart status to be populated after deploy")
	}
}

func TestHelmChartDeleteOnK3s(t *testing.T) {
	if os.Getenv("K3S_INTEGRATION") != "true" {
		t.Skip("set K3S_INTEGRATION=true to run K3s HelmChart integration tests")
	}

	cfg := k3sRestConfig(t)
	_ = helmv1.AddToScheme(scheme.Scheme)
	c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	ctx := context.Background()
	chart := &helmv1.HelmChart{
		ObjectMeta: metav1.ObjectMeta{Name: "geass-it-delete", Namespace: platform.HelmChartNamespace},
		Spec: helmv1.HelmChartSpec{
			Chart:           platform.RedisReleaseChart,
			Repo:            platform.RedisChartRepo,
			Version:         platform.RedisChartVersion,
			TargetNamespace: "geass-dev",
			CreateNamespace: true,
			ValuesContent:   "architecture: standalone\n",
		},
	}
	if err := c.Create(ctx, chart); err != nil {
		t.Fatalf("create chart: %v", err)
	}
	waitHelmChartDeployed(t, ctx, c, chart)

	if err := c.Delete(ctx, chart); err != nil {
		t.Fatalf("delete chart: %v", err)
	}

	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		err := c.Get(ctx, client.ObjectKeyFromObject(chart), &helmv1.HelmChart{})
		if err != nil {
			return
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatal("HelmChart was not removed after delete")
}
