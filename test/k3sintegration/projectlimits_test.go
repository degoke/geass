//go:build k3sintegration

package k3sintegration

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	limitsProject = "limits"
	limitsEnv     = "dev"
	limitsNS      = limitsProject + "-" + limitsEnv
)

type limitsEnvironment struct {
	ctx  context.Context
	c    client.Client
	kube kubernetes.Interface
}

// probe runs curl from a pod on one node, once per url, and returns the HTTP status of each, or 000 where the
// connection was refused or timed out. kube-router lets a new pod through a policy a moment after it starts, so the
// pod waits first.
func (e *limitsEnvironment) probe(t *testing.T, node string, urls ...string) []string {
	t.Helper()
	requests := make([][]string, 0, len(urls))
	for _, url := range urls {
		requests = append(requests, []string{"-sk", "-m", "4", "-o", "/dev/null", "-w", "%{http_code} ", url})
	}
	name := fmt.Sprintf("probe-%d", time.Now().UnixNano()%1_000_000_000)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: limitsNS},
		Spec: corev1.PodSpec{
			NodeName:      node,
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name: "curl", Image: curlImage,
				Command: []string{"sh", "-c", `sleep 5; exec curl "$@"`, "curl"},
				Args:    together(requests...),
			}},
		},
	}
	if err := e.c.Create(e.ctx, pod); err != nil {
		t.Fatalf("could not start a probe on %s: %v", node, err)
	}
	defer func() { _ = e.c.Delete(context.Background(), pod) }()
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		var current corev1.Pod
		if err := e.c.Get(e.ctx, client.ObjectKeyFromObject(pod), &current); err == nil &&
			(current.Status.Phase == corev1.PodSucceeded || current.Status.Phase == corev1.PodFailed) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	logs, err := e.kube.CoreV1().Pods(limitsNS).GetLogs(name, &corev1.PodLogOptions{}).DoRaw(e.ctx)
	if err != nil {
		t.Fatalf("no output from the probe on %s: %v", node, err)
	}
	got := codes(string(logs))
	if len(got) != len(urls) {
		t.Fatalf("probe on %s answered %q for %d addresses", node, string(logs), len(urls))
	}
	return got
}

func (e *limitsEnvironment) egressExceptions(t *testing.T) []string {
	t.Helper()
	var policy networkingv1.NetworkPolicy
	if err := e.c.Get(e.ctx, client.ObjectKey{Name: platform.CloudMetadataPolicyName, Namespace: limitsNS}, &policy); err != nil {
		t.Fatalf("no egress policy: %v", err)
	}
	return policy.Spec.Egress[0].To[1].IPBlock.Except
}

func (e *limitsEnvironment) setProject(t *testing.T, change func(*geassv1alpha1.GeassProject)) {
	t.Helper()
	for range 5 {
		var project geassv1alpha1.GeassProject
		if err := e.c.Get(e.ctx, client.ObjectKey{Name: limitsProject, Namespace: platform.SystemNamespace}, &project); err != nil {
			t.Fatal(err)
		}
		change(&project)
		err := e.c.Update(e.ctx, &project)
		if err == nil {
			return
		}
		if !apierrors.IsConflict(err) {
			t.Fatal(err)
		}
	}
	t.Fatal("the project kept changing under the test")
}

// TestProjectLimitsOnK3s runs the project controller against a real k3s, ideally with two nodes (the control plane and
// an agent), and follows what a project can opt into: keeping its services away from the machines the cluster runs on,
// and a cap on what each environment can use.
//
// k3s matters because it enforces network policies, which a kind cluster does not, and because only a real API server
// applies a quota to the pods a Deployment tries to make.
func TestProjectLimitsOnK3s(t *testing.T) {
	if os.Getenv("K3S_INTEGRATION") != "true" {
		t.Skip("set K3S_INTEGRATION=true to run the k3s project limits integration test (hack/k3s-up.sh starts a k3s)")
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
	e := &limitsEnvironment{ctx: ctx, c: c, kube: kubernetes.NewForConfigOrDie(cfg)}

	// ---- the cluster and one project, with nothing opted into ----
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
		ObjectMeta: metav1.ObjectMeta{Name: limitsProject, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: "default"}, Environments: []string{limitsEnv}},
	}
	if err := c.Create(ctx, project); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Delete(context.Background(), project)
		// A run that starts while the last one's namespace is still going finds its pods deleted under it.
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			if apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKey{Name: limitsNS}, &corev1.Namespace{})) {
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
		for _, cond := range latest.Status.Conditions {
			if cond.Type == platform.ConditionReady && cond.Status == metav1.ConditionTrue {
				return true, ""
			}
		}
		return false, "the project is not ready"
	})

	var nodeList corev1.NodeList
	if err := c.List(ctx, &nodeList); err != nil {
		t.Fatal(err)
	}
	var controlPlane string
	var hostIPs, podGateways, nodeNames []string
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		nodeNames = append(nodeNames, node.Name)
		for _, address := range node.Status.Addresses {
			if address.Type == corev1.NodeInternalIP {
				hostIPs = append(hostIPs, address.Address)
				if platform.IsControlPlaneNode(node) {
					controlPlane = address.Address
				}
			}
		}
		for _, cidr := range node.Spec.PodCIDRs {
			base := strings.TrimSuffix(cidr, ".0/24")
			podGateways = append(podGateways, base+".1")
		}
	}
	if controlPlane == "" {
		t.Fatal("no control plane node with an internal address")
	}
	t.Logf("nodes %v, addresses %v, pod gateways %v, control plane %s", nodeNames, hostIPs, podGateways, controlPlane)

	// What each node's kubelet and the pod gateway answer, and the API on the control plane, as a pod sees them.
	kubelets := make([]string, 0, len(hostIPs)+len(podGateways))
	for _, ip := range slices.Concat(hostIPs, podGateways) {
		kubelets = append(kubelets, "https://"+ip+":10250/healthz")
	}
	api := "https://" + controlPlane + ":6443/version"
	service := "https://kubernetes.default.svc.cluster.local/version"
	internet := "https://1.1.1.1/"
	targets := slices.Concat(kubelets, []string{api, service, internet})
	reachable := func(code string) bool { return code != "000" }

	// ---- 1. by default nothing about the machines is blocked, which is what it was before the option existed ----
	t.Run("the machines are reachable until a project opts in", func(t *testing.T) {
		if exceptions := e.egressExceptions(t); !slices.Equal(exceptions, []string{"169.254.0.0/16"}) {
			t.Fatalf("a project that did not opt in must keep the plain metadata block, got %v", exceptions)
		}
		for _, node := range nodeNames {
			for i, code := range e.probe(t, node, targets...) {
				if !reachable(code) && targets[i] != internet {
					t.Errorf("from %s: %s answered %s before the block, so the test could not tell a block from a closed port", node, targets[i], code)
				}
			}
		}
	})
	if t.Failed() {
		return
	}
	baselineInternet := reachable(e.probe(t, nodeNames[0], internet)[0])

	// ---- 2. opting in cuts every pod off from every machine, and leaves the rest alone ----
	e.setProject(t, func(p *geassv1alpha1.GeassProject) {
		p.Spec.Network = &geassv1alpha1.GeassProjectNetwork{BlockNodeAddresses: true}
	})
	eventually(t, time.Minute, "the policy to name the nodes", func() (bool, string) {
		exceptions := e.egressExceptions(t)
		for _, ip := range slices.Concat(hostIPs, podGateways) {
			if !slices.Contains(exceptions, ip+"/32") {
				return false, ip + " is not in " + strings.Join(exceptions, ",")
			}
		}
		return true, ""
	})
	t.Run("a pod cannot reach a machine, on any node", func(t *testing.T) {
		for _, node := range nodeNames {
			got := e.probe(t, node, targets...)
			for i, target := range targets {
				switch {
				case i < len(kubelets):
					if reachable(got[i]) {
						t.Errorf("from %s: %s answered %s, so the machine is not blocked", node, target, got[i])
					}
				case target == api, target == service:
					if !reachable(got[i]) {
						t.Errorf("from %s: %s did not answer, so the Kubernetes API is cut off (a Postgres instance would lose it)", node, target)
					}
				case target == internet:
					if reachable(got[i]) != baselineInternet {
						t.Errorf("from %s: the internet answered %s, and it did not change anything before the block", node, got[i])
					}
				}
			}
		}
	})

	// ---- 3. the policy follows nodes as they join and leave ----
	t.Run("the policy follows a node that joins and leaves", func(t *testing.T) {
		fake := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "limits-fake-node"},
			Spec:       corev1.NodeSpec{PodCIDRs: []string{"10.99.7.0/24"}},
		}
		if err := c.Create(ctx, fake); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Delete(context.Background(), fake) })
		// The cluster's own node controller touches a new Node at once, so the address goes on a fresh copy.
		eventually(t, 30*time.Second, "the fake node to take an address", func() (bool, string) {
			var latest corev1.Node
			if err := c.Get(ctx, client.ObjectKeyFromObject(fake), &latest); err != nil {
				return false, err.Error()
			}
			latest.Status.Addresses = []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.99.0.1"}}
			if err := c.Status().Update(ctx, &latest); err != nil {
				return false, err.Error()
			}
			return true, ""
		})
		eventually(t, time.Minute, "the policy to name the new node", func() (bool, string) {
			exceptions := e.egressExceptions(t)
			return slices.Contains(exceptions, "10.99.0.1/32") && slices.Contains(exceptions, "10.99.7.1/32"), strings.Join(exceptions, ",")
		})
		if err := c.Delete(ctx, fake); err != nil {
			t.Fatal(err)
		}
		eventually(t, time.Minute, "the policy to forget the node that left", func() (bool, string) {
			exceptions := e.egressExceptions(t)
			return !slices.Contains(exceptions, "10.99.0.1/32"), strings.Join(exceptions, ",")
		})
	})

	// ---- 4. opting out puts it all back ----
	e.setProject(t, func(p *geassv1alpha1.GeassProject) { p.Spec.Network = &geassv1alpha1.GeassProjectNetwork{} })
	eventually(t, time.Minute, "the machines to be unblocked", func() (bool, string) {
		exceptions := e.egressExceptions(t)
		return slices.Equal(exceptions, []string{"169.254.0.0/16"}), strings.Join(exceptions, ",")
	})

	// ---- 5. quota ----
	t.Run("a quota caps an environment and a raised one lets the service finish starting", func(t *testing.T) {
		e.setProject(t, func(p *geassv1alpha1.GeassProject) {
			cpu, memory := resource.MustParse("500m"), resource.MustParse("512Mi")
			pods := int32(3)
			p.Spec.EnvironmentQuota = &geassv1alpha1.GeassEnvironmentQuota{CPU: &cpu, Memory: &memory, Pods: &pods}
		})
		eventually(t, time.Minute, "the quota objects", func() (bool, string) {
			if err := c.Get(ctx, client.ObjectKey{Name: platform.ProjectQuotaName, Namespace: limitsNS}, &corev1.ResourceQuota{}); err != nil {
				return false, err.Error()
			}
			return true, ""
		})
		if err := c.Get(ctx, client.ObjectKey{Name: platform.ProjectDefaultsName, Namespace: limitsNS}, &corev1.LimitRange{}); err != nil {
			t.Fatalf("a quota on CPU and memory comes with defaults for containers that ask for neither: %v", err)
		}

		// A container that asks for nothing is not refused, and is counted.
		bare := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "bare", Namespace: limitsNS},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "c", Image: curlImage, Command: []string{"sleep", "3600"},
			}}},
		}
		if err := c.Create(ctx, bare); err != nil {
			t.Fatalf("a pod that asks for nothing was refused under a quota: %v", err)
		}
		t.Cleanup(func() { _ = c.Delete(context.Background(), bare) })
		cpuRequest := bare.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]
		if cpuRequest.Cmp(resource.MustParse("50m")) != 0 {
			t.Errorf("the default request was %s, want 50m", cpuRequest.String())
		}

		// Two copies of 300m do not fit under 500m with the pod above.
		replicas := int32(2)
		labels := map[string]string{"app": "web"}
		web := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: limitsNS},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: labels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels},
					Spec: corev1.PodSpec{Containers: []corev1.Container{{
						Name: "c", Image: curlImage, Command: []string{"sleep", "3600"},
						Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("300m"), corev1.ResourceMemory: resource.MustParse("100Mi"),
						}},
					}}},
				},
			},
		}
		if err := c.Create(ctx, web); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Delete(context.Background(), web) })
		eventually(t, 2*time.Minute, "the Deployment to report the quota", func() (bool, string) {
			var latest appsv1.Deployment
			if err := c.Get(ctx, client.ObjectKeyFromObject(web), &latest); err != nil {
				return false, err.Error()
			}
			for _, cond := range latest.Status.Conditions {
				if cond.Type == appsv1.DeploymentReplicaFailure && cond.Status == corev1.ConditionTrue && strings.Contains(cond.Message, "exceeded quota") {
					return true, cond.Message
				}
			}
			return false, fmt.Sprintf("%d of 2 copies available, no ReplicaFailure yet", latest.Status.AvailableReplicas)
		})

		// Raising the cap is all it takes: nothing about the service changes.
		e.setProject(t, func(p *geassv1alpha1.GeassProject) {
			cpu := resource.MustParse("2")
			p.Spec.EnvironmentQuota.CPU = &cpu
		})
		eventually(t, 4*time.Minute, "both copies to start under the raised quota", func() (bool, string) {
			var latest appsv1.Deployment
			if err := c.Get(ctx, client.ObjectKeyFromObject(web), &latest); err != nil {
				return false, err.Error()
			}
			return latest.Status.AvailableReplicas == 2, fmt.Sprintf("%d of 2 copies available", latest.Status.AvailableReplicas)
		})
	})

	// ---- 6. a quota edited by hand is put back, and taking the quota away removes what made it ----
	t.Run("an edited quota is restored and a removed one goes away", func(t *testing.T) {
		var quota corev1.ResourceQuota
		if err := c.Get(ctx, client.ObjectKey{Name: platform.ProjectQuotaName, Namespace: limitsNS}, &quota); err != nil {
			t.Fatal(err)
		}
		quota.Spec.Hard[corev1.ResourceRequestsCPU] = resource.MustParse("400")
		if err := c.Update(ctx, &quota); err != nil {
			t.Fatal(err)
		}
		eventually(t, time.Minute, "the hand edit to be reverted", func() (bool, string) {
			if err := c.Get(ctx, client.ObjectKey{Name: platform.ProjectQuotaName, Namespace: limitsNS}, &quota); err != nil {
				return false, err.Error()
			}
			cpu := quota.Spec.Hard[corev1.ResourceRequestsCPU]
			return cpu.Cmp(resource.MustParse("2")) == 0, cpu.String()
		})

		e.setProject(t, func(p *geassv1alpha1.GeassProject) { p.Spec.EnvironmentQuota = nil })
		eventually(t, time.Minute, "the quota objects to be removed", func() (bool, string) {
			quotaErr := c.Get(ctx, client.ObjectKey{Name: platform.ProjectQuotaName, Namespace: limitsNS}, &corev1.ResourceQuota{})
			defaultsErr := c.Get(ctx, client.ObjectKey{Name: platform.ProjectDefaultsName, Namespace: limitsNS}, &corev1.LimitRange{})
			return apierrors.IsNotFound(quotaErr) && apierrors.IsNotFound(defaultsErr), fmt.Sprint(quotaErr, defaultsErr)
		})
	})

	// ---- 7. a cap on the whole project is shared by its environments ----
	t.Run("a project-wide cap is shared by the environments", func(t *testing.T) {
		const wide = "wide"
		cpuTotal := resource.MustParse("1")
		wideProject := &geassv1alpha1.GeassProject{
			ObjectMeta: metav1.ObjectMeta{Name: wide, Namespace: platform.SystemNamespace},
			Spec: geassv1alpha1.GeassProjectSpec{
				ClusterRef:   corev1.LocalObjectReference{Name: "default"},
				Environments: []string{"dev", "staging"},
				ProjectQuota: &geassv1alpha1.GeassProjectQuota{CPU: &cpuTotal},
			},
		}
		if err := c.Create(ctx, wideProject); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = c.Delete(context.Background(), wideProject)
			deadline := time.Now().Add(2 * time.Minute)
			for time.Now().Before(deadline) {
				devGone := apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKey{Name: "wide-dev"}, &corev1.Namespace{}))
				stagingGone := apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKey{Name: "wide-staging"}, &corev1.Namespace{}))
				if devGone && stagingGone {
					return
				}
				time.Sleep(2 * time.Second)
			}
		})
		hardCPU := func(namespace string) string {
			var quota corev1.ResourceQuota
			if err := c.Get(ctx, client.ObjectKey{Name: platform.ProjectQuotaName, Namespace: namespace}, &quota); err != nil {
				return err.Error()
			}
			cpu := quota.Spec.Hard[corev1.ResourceRequestsCPU]
			return cpu.String()
		}
		eventually(t, 2*time.Minute, "both environments to be capped at the whole total", func() (bool, string) {
			state := hardCPU("wide-dev") + " " + hardCPU("wide-staging")
			if hardCPU("wide-dev") != "1" || hardCPU("wide-staging") != "1" {
				var latest geassv1alpha1.GeassProject
				if err := c.Get(ctx, client.ObjectKeyFromObject(wideProject), &latest); err == nil {
					state += fmt.Sprintf(" | conditions %+v | environments %+v", latest.Status.Conditions, latest.Status.Environments)
				}
			}
			return hardCPU("wide-dev") == "1" && hardCPU("wide-staging") == "1", state
		})

		deployment := func(namespace, name string) *appsv1.Deployment {
			replicas := int32(1)
			labels := map[string]string{"app": name}
			return &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: labels},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: labels},
						Spec: corev1.PodSpec{Containers: []corev1.Container{{
							Name: "c", Image: curlImage, Command: []string{"sleep", "3600"},
							Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("600m")}},
						}}},
					},
				},
			}
		}
		available := func(namespace, name string) func() (bool, string) {
			return func() (bool, string) {
				var latest appsv1.Deployment
				if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, &latest); err != nil {
					return false, err.Error()
				}
				return latest.Status.AvailableReplicas == 1, fmt.Sprintf("%d available", latest.Status.AvailableReplicas)
			}
		}

		// One environment uses 600m of the 1 CPU, so the other may ask for 400m.
		first := deployment("wide-dev", "first")
		if err := c.Create(ctx, first); err != nil {
			t.Fatal(err)
		}
		eventually(t, 2*time.Minute, "the first environment's service to start", available("wide-dev", "first"))
		eventually(t, time.Minute, "the other environment's cap to shrink to what is left", func() (bool, string) {
			return hardCPU("wide-staging") == "400m", hardCPU("wide-staging")
		})

		second := deployment("wide-staging", "second")
		if err := c.Create(ctx, second); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Delete(context.Background(), second) })
		eventually(t, 2*time.Minute, "the second environment's service to be refused for the project total", func() (bool, string) {
			var latest appsv1.Deployment
			if err := c.Get(ctx, client.ObjectKeyFromObject(second), &latest); err != nil {
				return false, err.Error()
			}
			for _, cond := range latest.Status.Conditions {
				if cond.Type == appsv1.DeploymentReplicaFailure && cond.Status == corev1.ConditionTrue && strings.Contains(cond.Message, "exceeded quota") {
					return true, cond.Message
				}
			}
			return false, "no ReplicaFailure yet"
		})
		eventually(t, time.Minute, "the project to report what it uses", func() (bool, string) {
			var latest geassv1alpha1.GeassProject
			if err := c.Get(ctx, client.ObjectKeyFromObject(wideProject), &latest); err != nil {
				return false, err.Error()
			}
			if latest.Status.QuotaUsage == nil || latest.Status.QuotaUsage.CPU == nil {
				return false, "no usage reported yet"
			}
			return latest.Status.QuotaUsage.CPU.Cmp(resource.MustParse("600m")) == 0, latest.Status.QuotaUsage.CPU.String()
		})

		// The first gives its room back, and the second, which was refused, starts without anything being changed.
		if err := c.Delete(ctx, first); err != nil {
			t.Fatal(err)
		}
		eventually(t, time.Minute, "the room to go back to the other environment", func() (bool, string) {
			return hardCPU("wide-staging") == "1", hardCPU("wide-staging")
		})
		eventually(t, 4*time.Minute, "the second environment's service to start in the room that was freed", available("wide-staging", "second"))
	})
}
