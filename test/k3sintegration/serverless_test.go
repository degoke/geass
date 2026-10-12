//go:build k3sintegration

package k3sintegration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
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
	testProject = "serverless-it"
	testEnv     = "production"
	testHost    = "fn.it.local"
	testNS      = testProject + "-" + testEnv
	testApp     = "hello"
)

var httpScaledObject = schema(unstructured.Unstructured{}, "http.keda.sh", "v1alpha1", "HTTPScaledObject")

func schema(obj unstructured.Unstructured, group, version, kind string) unstructured.Unstructured {
	obj.SetAPIVersion(group + "/" + version)
	obj.SetKind(kind)
	return obj
}

// eventually polls until check returns true, failing the test with what it last said when time runs out.
func eventually(t *testing.T, timeout time.Duration, what string, check func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		ok, detail := check()
		if ok {
			return
		}
		last = detail
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("timed out waiting for %s: %s", what, last)
}

// visit sends a request through the cluster's ingress, the way a visitor's would arrive, and returns the answer.
// The first request to a sleeping service waits while it wakes, so the timeout is generous.
func visit(t *testing.T, headers map[string]string, path string) (int, string) {
	t.Helper()
	addr := os.Getenv("K3S_HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:18080"
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = testHost
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := (&http.Client{Timeout: 3 * time.Minute}).Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

const handlerCode = `export async function handler(event) {
  console.log(JSON.stringify({ trigger: event.trigger, name: event.triggerName, path: event.path }));
  return { trigger: event.trigger, triggerName: event.triggerName, path: event.path };
}
`

// TestServerlessFunctionsOnK3s runs the operator against a real k3s and follows a function through everything
// scale to zero and schedules do: the add-on installing itself, the function sleeping and waking on a request,
// a schedule waking it and proving itself with its token, a visitor not being able to pretend to be one, and
// the function being put back to always running.
func TestServerlessFunctionsOnK3s(t *testing.T) {
	if os.Getenv("K3S_INTEGRATION") != "true" {
		t.Skip("set K3S_INTEGRATION=true to run the k3s serverless integration test (hack/k3s-up.sh starts a k3s)")
	}
	cfg := k3sRestConfig(t)
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, geassv1alpha1.AddToScheme, helmv1.AddToScheme, monitoringv1.AddToScheme, cnpgv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	// The operator's own log, which says why a reconcile failed.
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
	kube := kubernetes.NewForConfigOrDie(cfg)

	// ---- the cluster, with scale to zero turned on ----
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platform.SystemNamespace}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	if err := c.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "geass-token", Namespace: platform.SystemNamespace}, StringData: map[string]string{"token": "x"}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	enabled, disabled := true, false
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{
			Version: "v1", ServerURL: "https://127.0.0.1:6443",
			TokenSecretRef: corev1.SecretReference{Name: "geass-token", Namespace: platform.SystemNamespace},
			Addons: geassv1alpha1.GeassClusterAddonsSpec{
				CertManager: geassv1alpha1.GeassClusterCertManagerAddon{Enabled: &disabled},
				Monitoring:  geassv1alpha1.GeassClusterMonitoringAddon{Enabled: &disabled},
				Serverless:  geassv1alpha1.GeassClusterServerlessAddon{Enabled: &enabled},
			},
		},
	}
	if err := c.Create(ctx, cluster); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProject, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: "default"}, Environments: []string{testEnv}},
	}
	if err := c.Create(ctx, project); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}

	// An app made before its project is ready fails its retries in the first seconds, so the project goes first,
	// as it does when it is made in the dashboard.
	eventually(t, 2*time.Minute, "the project to be ready", func() (bool, string) {
		var latest geassv1alpha1.GeassProject
		if err := c.Get(ctx, client.ObjectKeyFromObject(project), &latest); err != nil {
			return false, err.Error()
		}
		for _, cond := range latest.Status.Conditions {
			if cond.Type == platform.ConditionReady && cond.Status == metav1.ConditionTrue {
				return true, ""
			}
		}
		return false, "the project is not ready"
	})

	// The add-on installs itself: KEDA, then the HTTP add-on, and Traefik is told it may route to a name.
	eventually(t, 15*time.Minute, "the scale-to-zero add-on to come up", func() (bool, string) {
		var crd unstructured.Unstructured
		crd.SetAPIVersion("apiextensions.k8s.io/v1")
		crd.SetKind("CustomResourceDefinition")
		if err := c.Get(ctx, client.ObjectKey{Name: "httpscaledobjects.http.keda.sh"}, &crd); err != nil {
			return false, "the HTTPScaledObject CRD is not there yet"
		}
		var deployments appsv1.DeploymentList
		if err := c.List(ctx, &deployments, client.InNamespace(platform.ServerlessTargetNS)); err != nil {
			return false, err.Error()
		}
		for i := range deployments.Items {
			if deployments.Items[i].Status.AvailableReplicas == 0 {
				return false, deployments.Items[i].Name + " is not available yet"
			}
		}
		return len(deployments.Items) >= 5, fmt.Sprintf("%d deployments in %s", len(deployments.Items), platform.ServerlessTargetNS)
	})

	// ---- the function: a handler with a schedule, that sleeps after half a minute ----
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: testApp, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProject, Environment: testEnv, Port: 8080,
			Source: geassv1alpha1.GeassAppSource{Function: &geassv1alpha1.GeassAppFunctionSource{
				Runtime: "node", Mode: platform.FunctionModeEvent, Code: handlerCode,
				Triggers: []geassv1alpha1.GeassFunctionTrigger{{Name: "every-minute", Schedule: "* * * * *", Path: "/cron"}},
			}},
			Ingress:     geassv1alpha1.GeassAppIngressSpec{Host: testHost, Path: "/"},
			ScaleToZero: &geassv1alpha1.GeassAppScaleToZeroSpec{Enabled: true, IdleSeconds: 30, MaxReplicas: 2},
			Deploy:      geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
	}
	if err := c.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	// The operator is still running when this does, so the finalizers are processed and nothing is left behind.
	t.Cleanup(func() {
		for _, obj := range []client.Object{app, project} {
			_ = c.Delete(context.Background(), obj)
			deadline := time.Now().Add(2 * time.Minute)
			for time.Now().Before(deadline) && !apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKeyFromObject(obj), obj)) {
				time.Sleep(2 * time.Second)
			}
		}
	})

	condition := func(kind string) (metav1.ConditionStatus, string) {
		var latest geassv1alpha1.GeassApp
		if err := c.Get(ctx, client.ObjectKeyFromObject(app), &latest); err != nil {
			return metav1.ConditionUnknown, err.Error()
		}
		for _, cond := range latest.Status.Conditions {
			if cond.Type == kind {
				return cond.Status, cond.Reason + ": " + cond.Message
			}
		}
		return metav1.ConditionUnknown, "no " + kind + " condition"
	}
	replicas := func() int32 {
		var deploy appsv1.Deployment
		if err := c.Get(ctx, client.ObjectKey{Name: testApp, Namespace: testNS}, &deploy); err != nil || deploy.Spec.Replicas == nil {
			return -1
		}
		return *deploy.Spec.Replicas
	}

	t.Run("it sleeps and the service reports ready and sleeping", func(t *testing.T) {
		eventually(t, 5*time.Minute, "the function to be ready and sleeping", func() (bool, string) {
			status, _ := condition("ScaleToZero")
			ready, why := condition(platform.ConditionReady)
			return status == metav1.ConditionTrue && ready == metav1.ConditionTrue && replicas() == 0, fmt.Sprintf("ScaleToZero %s; Ready %s (%s); replicas %d", status, ready, why, replicas())
		})
	})

	t.Run("a request wakes it, and a visitor is not a schedule", func(t *testing.T) {
		code, body := visit(t, nil, "/wake")
		if code != http.StatusOK {
			t.Fatalf("the request that wakes it answered %d: %s", code, body)
		}
		var answer map[string]any
		if err := json.Unmarshal([]byte(body), &answer); err != nil || answer["trigger"] != "http" {
			t.Fatalf("a visitor should be an http trigger, got %s", body)
		}
		for name, headers := range map[string]map[string]string{
			"the old header":  {"x-geass-trigger": "cron"},
			"a guessed token": {"x-geass-trigger-token": "guess", "x-geass-trigger-name": "every-minute"},
		} {
			_, body := visit(t, headers, "/spoof")
			if !strings.Contains(body, `"trigger":"http"`) {
				t.Fatalf("%s should not make a request a schedule, got %s", name, body)
			}
		}
	})

	t.Run("it goes back to sleep when idle", func(t *testing.T) {
		eventually(t, 3*time.Minute, "the function to scale to zero", func() (bool, string) {
			return replicas() == 0, fmt.Sprintf("replicas %d", replicas())
		})
	})

	t.Run("a schedule wakes it and proves itself with the token", func(t *testing.T) {
		eventually(t, 4*time.Minute, "a schedule to have run and the function to have logged it", func() (bool, string) {
			var pods corev1.PodList
			if err := c.List(ctx, &pods, client.InNamespace(testNS), client.MatchingLabels{"app.kubernetes.io/name": testApp}); err != nil {
				return false, err.Error()
			}
			for _, pod := range pods.Items {
				logs, err := kube.CoreV1().Pods(testNS).GetLogs(pod.Name, &corev1.PodLogOptions{}).DoRaw(ctx)
				if err == nil && strings.Contains(string(logs), `"trigger":"cron","name":"every-minute"`) {
					return true, ""
				}
			}
			var jobs batchv1.JobList
			_ = c.List(ctx, &jobs, client.InNamespace(testNS))
			return false, fmt.Sprintf("%d jobs, %d function pods", len(jobs.Items), len(pods.Items))
		})
	})

	t.Run("a schedule that is removed stops and its CronJob goes", func(t *testing.T) {
		var latest geassv1alpha1.GeassApp
		if err := c.Get(ctx, client.ObjectKeyFromObject(app), &latest); err != nil {
			t.Fatal(err)
		}
		latest.Spec.Source.Function.Triggers = nil
		if err := c.Update(ctx, &latest); err != nil {
			t.Fatal(err)
		}
		eventually(t, 2*time.Minute, "the CronJob and the token to be removed", func() (bool, string) {
			var crons batchv1.CronJobList
			cronErr := c.List(ctx, &crons, client.InNamespace(testNS), client.MatchingLabels{"geass.dev/function-trigger": "every-minute"})
			if cronErr == nil && len(crons.Items) > 0 {
				cronErr = fmt.Errorf("%d CronJobs remain", len(crons.Items))
			} else if cronErr == nil {
				cronErr = apierrors.NewNotFound(batchv1.Resource("cronjobs"), "every-minute")
			}
			secretErr := c.Get(ctx, client.ObjectKey{Name: testApp + "-trigger", Namespace: testNS}, &corev1.Secret{})
			// An event function keeps its token even without schedules, since its shim reads it.
			return apierrors.IsNotFound(cronErr) && secretErr == nil, fmt.Sprintf("cron: %v, token: %v", cronErr, secretErr)
		})
	})

	t.Run("turning it off puts the service back to always running", func(t *testing.T) {
		var latest geassv1alpha1.GeassApp
		if err := c.Get(ctx, client.ObjectKeyFromObject(app), &latest); err != nil {
			t.Fatal(err)
		}
		latest.Spec.ScaleToZero.Enabled = false
		if err := c.Update(ctx, &latest); err != nil {
			t.Fatal(err)
		}
		eventually(t, 3*time.Minute, "the scaling objects to go and the pod to run", func() (bool, string) {
			scaled := httpScaledObject.DeepCopy()
			err := c.Get(ctx, client.ObjectKey{Name: testApp, Namespace: testNS}, scaled)
			wakeErr := c.Get(ctx, client.ObjectKey{Name: testApp + "-wake", Namespace: testNS}, &corev1.Service{})
			return apierrors.IsNotFound(err) && apierrors.IsNotFound(wakeErr) && replicas() >= 1, fmt.Sprintf("HTTPScaledObject: %v, wake service: %v, replicas %d", err, wakeErr, replicas())
		})
		eventually(t, 3*time.Minute, "the function to answer straight through the ingress", func() (bool, string) {
			code, body := visit(t, nil, "/direct")
			return code == http.StatusOK, fmt.Sprintf("%d %s", code, body)
		})
	})

	t.Run("a version that cannot start fails the deploy instead of waiting for a visitor", func(t *testing.T) {
		broken := &geassv1alpha1.GeassApp{
			ObjectMeta: metav1.ObjectMeta{Name: "broken", Namespace: platform.SystemNamespace},
			Spec: geassv1alpha1.GeassAppSpec{
				Project: testProject, Environment: testEnv, Port: 8080,
				Source:      geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "ghcr.io/geass-it/this-image-does-not-exist:1"}},
				Ingress:     geassv1alpha1.GeassAppIngressSpec{Host: "broken.it.local", Path: "/"},
				ScaleToZero: &geassv1alpha1.GeassAppScaleToZeroSpec{Enabled: true, IdleSeconds: 30},
				Deploy:      geassv1alpha1.GeassAppDeploySpec{Enabled: true},
			},
		}
		if err := c.Create(ctx, broken); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = c.Delete(context.Background(), broken)
			deadline := time.Now().Add(2 * time.Minute)
			for time.Now().Before(deadline) && !apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKeyFromObject(broken), broken)) {
				time.Sleep(2 * time.Second)
			}
		})
		eventually(t, 6*time.Minute, "the deploy to be reported as failed", func() (bool, string) {
			var latest geassv1alpha1.GeassApp
			if err := c.Get(ctx, client.ObjectKeyFromObject(broken), &latest); err != nil {
				return false, err.Error()
			}
			reason := platform.ReadyConditionReason(latest.Status.Conditions)
			return reason == platform.ConditionReasonProvisionFailed, "Ready reason: " + reason
		})
		// It was held at one copy the whole time, so the add-on never put it to sleep before it was found out.
		var deploy appsv1.Deployment
		if err := c.Get(ctx, client.ObjectKey{Name: "broken", Namespace: testNS}, &deploy); err != nil {
			t.Fatal(err)
		}
		if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas < 1 {
			t.Fatalf("a version that has not started should be held at one copy, got %v", deploy.Spec.Replicas)
		}
	})

}
