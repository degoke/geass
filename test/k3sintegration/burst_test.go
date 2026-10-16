//go:build k3sintegration

package k3sintegration

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
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

const (
	burstProject = "burst"
	burstEnv     = "dev"
	burstNS      = burstProject + "-" + burstEnv
	// burstImage has to be small and to have a way to hold memory. A bytes object of x is written to, so the memory is
	// really used and not only reserved.
	burstImage = "python:3.12-alpine"
)

// memoryEater is a service that waits, holds an amount of memory, and then keeps running.
func memoryEater(name string, waitSeconds, megabytes int, res corev1.ResourceRequirements) *geassv1alpha1.GeassApp {
	script := "import time\ntime.sleep(" + strconv.Itoa(waitSeconds) + ")\nheld = b'x' * (" + strconv.Itoa(megabytes) + " * 1024 * 1024)\ntime.sleep(86400)\n"
	return &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: burstProject, Environment: burstEnv, Port: 8080,
			Source:    geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: burstImage}},
			Build:     geassv1alpha1.GeassAppBuildSpec{Command: []string{"python", "-c", script}},
			Resources: res,
			Deploy:    geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
	}
}

// TestBurstableServicesOnK3s follows a service that reserves less than it may use: that it starts with only the
// reservation to find on a node, that it may use what it was allowed, that a copy stopped for going over is said to
// have been, in the service's own status, and that raising the ceiling puts it right.
func TestBurstableServicesOnK3s(t *testing.T) {
	if os.Getenv("K3S_INTEGRATION") != "true" {
		t.Skip("set K3S_INTEGRATION=true to run the k3s burstable service test (hack/k3s-up.sh starts a k3s)")
	}
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
		ObjectMeta: metav1.ObjectMeta{Name: burstProject, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: "default"}, Environments: []string{burstEnv}},
	}
	if err := c.Create(ctx, project); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Delete(context.Background(), project)
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			if apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKey{Name: burstNS}, &corev1.Namespace{})) {
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

	create := func(app *geassv1alpha1.GeassApp) {
		t.Helper()
		if err := c.Create(ctx, app); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Delete(context.Background(), app) })
	}
	statusOf := func(name string) (ready metav1.ConditionStatus, message string, oom *metav1.Condition) {
		var app geassv1alpha1.GeassApp
		if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &app); err != nil {
			return "", err.Error(), nil
		}
		if cond := apimeta.FindStatusCondition(app.Status.Conditions, platform.ConditionReady); cond != nil {
			ready, message = cond.Status, cond.Message
		}
		return ready, message, apimeta.FindStatusCondition(app.Status.Conditions, platform.ConditionOutOfMemory)
	}
	podsOf := func(name string) []corev1.Pod {
		var pods corev1.PodList
		if err := c.List(ctx, &pods, client.InNamespace(burstNS), client.MatchingLabels{platform.K8sLabelAppName: name}); err != nil {
			return nil
		}
		return pods.Items
	}

	// ---- one that reserves 128 MB and may use 1 GB, and uses 300 MB ----
	bursting, err := platform.ResourcesFromSizes("100m", "128Mi", "500m", "1Gi")
	if err != nil {
		t.Fatal(err)
	}
	create(memoryEater("bursts", 0, 300, bursting))

	// ---- one that reserves 128 MB, may use 256 MB, and goes on to use 600 MB, after being ready for a while ----
	overruns, err := platform.ResourcesFromSizes("100m", "128Mi", "500m", "256Mi")
	if err != nil {
		t.Fatal(err)
	}
	create(memoryEater("overruns", 12, 600, overruns))

	// ---- and one that reserves what it may use, and goes over it, which is what every service did before ----
	fixed, err := platform.ResourcesFromSize("100m", "256Mi")
	if err != nil {
		t.Fatal(err)
	}
	create(memoryEater("fixed", 12, 600, fixed))

	t.Run("a service that is allowed to burst starts, and uses more than it reserved", func(t *testing.T) {
		eventually(t, 4*time.Minute, "the service that bursts to be ready", func() (bool, string) {
			ready, message, _ := statusOf("bursts")
			return ready == metav1.ConditionTrue, string(ready) + ": " + message
		})
		pods := podsOf("bursts")
		if len(pods) != 1 {
			t.Fatalf("want one copy, got %d", len(pods))
		}
		if pods[0].Status.QOSClass != corev1.PodQOSBurstable {
			t.Fatalf("a service that reserves less than it may use should be Burstable, got %s", pods[0].Status.QOSClass)
		}
		requested := pods[0].Spec.Containers[0].Resources.Requests[corev1.ResourceMemory]
		limit := pods[0].Spec.Containers[0].Resources.Limits[corev1.ResourceMemory]
		if requested.String() != "128Mi" || limit.String() != "1Gi" {
			t.Fatalf("pod asks for %s and may use %s", requested.String(), limit.String())
		}
		// It has had the time to hold its memory, and is still the same copy, ready, and has not been said to run out.
		time.Sleep(20 * time.Second)
		pods = podsOf("bursts")
		if len(pods) != 1 || pods[0].Status.ContainerStatuses[0].RestartCount != 0 {
			t.Fatalf("the copy that bursts should not have been restarted: %+v", pods)
		}
		if ready, message, oom := statusOf("bursts"); ready != metav1.ConditionTrue || oom != nil {
			t.Fatalf("service that bursts is %s (%s), out of memory condition %v", ready, message, oom)
		}
	})

	t.Run("a service reserving all of what it may use is Guaranteed, as before", func(t *testing.T) {
		eventually(t, 4*time.Minute, "the fixed service to have a copy", func() (bool, string) {
			pods := podsOf("fixed")
			return len(pods) > 0 && pods[0].Status.QOSClass != "", "no copy yet"
		})
		if class := podsOf("fixed")[0].Status.QOSClass; class != corev1.PodQOSGuaranteed {
			t.Fatalf("want Guaranteed, got %s", class)
		}
	})

	t.Run("a copy stopped for going over its ceiling is said to have been, and not just starting", func(t *testing.T) {
		eventually(t, 5*time.Minute, "the service that overruns to say it ran out of memory", func() (bool, string) {
			ready, message, _ := statusOf("overruns")
			return ready != metav1.ConditionTrue && strings.Contains(message, "Ran out of memory"), string(ready) + ": " + message
		})
		_, message, _ := statusOf("overruns")
		for _, want := range []string{"reserves 128 MB", "up to 256 MB", "using more than they reserve"} {
			if !strings.Contains(message, want) {
				t.Fatalf("message %q lacks %q", message, want)
			}
		}
		// While a copy is ready between two kills the service has the condition, so that the status says it too.
		eventually(t, 5*time.Minute, "the out of memory condition on the service that overruns", func() (bool, string) {
			_, message, oom := statusOf("overruns")
			return oom != nil && oom.Status == metav1.ConditionTrue && strings.Contains(oom.Message, "up to 256 MB"), message
		})
	})

	t.Run("a service that reserves all it may use says it went over its own size", func(t *testing.T) {
		eventually(t, 5*time.Minute, "the fixed service to say it ran out of memory", func() (bool, string) {
			ready, message, _ := statusOf("fixed")
			return ready != metav1.ConditionTrue && strings.Contains(message, "going over its 256 MB"), string(ready) + ": " + message
		})
	})

	t.Run("letting it use more puts it right", func(t *testing.T) {
		more, err := platform.ResourcesFromSizes("100m", "128Mi", "500m", "1Gi")
		if err != nil {
			t.Fatal(err)
		}
		var app geassv1alpha1.GeassApp
		eventually(t, time.Minute, "the service to be changed", func() (bool, string) {
			if err := c.Get(ctx, client.ObjectKey{Name: "overruns", Namespace: platform.SystemNamespace}, &app); err != nil {
				return false, err.Error()
			}
			app.Spec.Resources = more
			if err := c.Update(ctx, &app); err != nil {
				return false, err.Error()
			}
			return true, ""
		})
		eventually(t, 5*time.Minute, "the service to be ready and to stop saying it ran out of memory", func() (bool, string) {
			ready, message, oom := statusOf("overruns")
			return ready == metav1.ConditionTrue && oom == nil, string(ready) + ": " + message
		})
	})
}
