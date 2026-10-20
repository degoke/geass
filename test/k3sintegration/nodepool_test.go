//go:build k3sintegration

package k3sintegration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/controller"
	"github.com/degoke/geass/pkg/compute"
	computefake "github.com/degoke/geass/pkg/compute/fake"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/ssh"
)

const (
	poolNS        = "autoscale"
	poolAgentTag  = "geass-test-autoscale"
	k3sServerName = "geass-k3s"
)

// offsetClock is the pool controller's clock, which the test can push forward so that the ten minutes a node must be
// unneeded before it is removed do not have to pass.
type offsetClock struct{ offset atomic.Int64 }

func (c *offsetClock) Now() time.Time { return time.Now().Add(time.Duration(c.offset.Load())) }

func (c *offsetClock) Advance(d time.Duration) { c.offset.Add(int64(d)) }

// Reset puts the clock back to the real time. The times the cluster itself writes (when a node became ready) are real,
// so a pool whose clock runs ahead of them cannot tell a node that has only just joined from one that has been up a
// while.
func (c *offsetClock) Reset() { c.offset.Store(0) }

func docker(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("docker", args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, errBuf.String())
	}
	return strings.TrimSpace(out.String())
}

// containerAgents is the stand-in for SSH: where Geass would log in to a machine and install the K3s agent, the test
// starts the agent as a container, with exactly the configuration Geass gave it (its name, labels, taints and the
// server to join), and removes the container where Geass would uninstall the agent. Everything else, from the pool
// deciding to add a node to the scheduler placing the pods on it, is the real thing.
type containerAgents struct {
	t     *testing.T
	image string
	mu    sync.Mutex
	// byHost is the container that stands for the machine at each address.
	byHost map[string]string
}

func (a *containerAgents) Provision(_ context.Context, in ssh.ProvisionInput) error {
	args := []string{
		"run", "-d", "--name", in.NodeName, "--label", poolAgentTag + "=1",
		"--privileged", "--tmpfs", "/run", "--tmpfs", "/var/run",
		"-e", "K3S_URL=" + in.ServerURL, "-e", "K3S_TOKEN=" + in.Token,
		a.image, "agent", "--node-name", in.NodeName,
		// The host's disk may be nearly full, which makes a kubelet evict pods; that is not what is under test.
		"--kubelet-arg=eviction-hard=imagefs.available<1%,nodefs.available<1%",
	}
	for _, label := range in.Config.NodeLabels {
		args = append(args, "--node-label", label)
	}
	for _, taint := range in.Config.NodeTaints {
		args = append(args, "--node-taint", taint)
	}
	cmd := exec.Command("docker", args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker run: %v: %s", err, errBuf.String())
	}
	a.mu.Lock()
	a.byHost[in.Host] = in.NodeName
	a.mu.Unlock()
	return nil
}

func (a *containerAgents) Upgrade(context.Context, ssh.ProvisionInput) error {
	return fmt.Errorf("the test does not upgrade nodes")
}

func (a *containerAgents) Drain(_ context.Context, in ssh.ProvisionInput) error {
	a.mu.Lock()
	name := a.byHost[in.Host]
	a.mu.Unlock()
	if name == "" {
		return nil
	}
	return exec.Command("docker", "rm", "-fv", name).Run()
}

func removeAgents() {
	out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label="+poolAgentTag).Output()
	for id := range strings.FieldsSeq(string(out)) {
		_ = exec.Command("docker", "rm", "-fv", id).Run()
	}
}

// containerCloud is a fake cloud whose machines are the containers containerAgents starts: deleting a machine
// removes its container, which is what deleting a virtual machine does to the K3s agent running on it. Without it
// the kubelet in the container would register its node again as soon as the node object was deleted.
type containerCloud struct {
	*computefake.Provider
	t *testing.T
}

func (c containerCloud) Delete(ctx context.Context, id string) error {
	if instance, err := c.Provider.Get(ctx, id); err == nil {
		_ = exec.Command("docker", "rm", "-fv", instance.Name).Run()
	}
	return c.Provider.Delete(ctx, id)
}

// poolFactory gives the pool and node controllers the one fake cloud.
type poolFactory struct{ provider compute.Provider }

func (f poolFactory) Provider(context.Context, string) (compute.Provider, *geassv1alpha1.GeassCloudConnection, error) {
	return f.provider, &geassv1alpha1.GeassCloudConnection{}, nil
}

// TestNodePoolAutoscalingOnK3s runs the node and pool controllers against a real k3s and follows an autoscaling pool
// through its life: pods that cannot be placed make the pool add a node, the node joins with the labels and the taint
// Geass gave it, the scheduler places the pods there and keeps everything that did not ask for it away, and when the
// pods are gone the pool empties the node and removes it, and its machine with it.
//
// The cloud is a fake and the machines are containers (see containerAgents), because what is under test is Geass's
// side: the decisions, the join configuration, and what a real scheduler and API server do with them.
func TestNodePoolAutoscalingOnK3s(t *testing.T) {
	if os.Getenv("K3S_INTEGRATION") != "true" {
		t.Skip("set K3S_INTEGRATION=true to run the node pool integration test (hack/k3s-up.sh starts a k3s)")
	}
	t.Cleanup(removeAgents)
	removeAgents()

	cfg := k3sRestConfig(t)
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, geassv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(false)))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}

	// The server's own node: how big it is decides how big the pods must be to not fit on it twice.
	var base corev1.Node
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes); err != nil || len(nodes.Items) == 0 {
		t.Fatalf("no nodes: %v", err)
	}
	base = nodes.Items[0]
	cpu := base.Status.Allocatable.Cpu().MilliValue()
	memory := base.Status.Allocatable.Memory().Value()
	t.Logf("the server node offers %dm CPU and %d MiB", cpu, memory>>20)

	cloud := computefake.New()
	cloud.SizeList = []compute.Size{{ID: "test", VCPUs: int((cpu + platform.NodeSystemCPUMillis + 999) / 1000), MemoryMiB: (memory + platform.NodeSystemMemoryBytes) >> 20, HourlyPrice: 0.01, Currency: "USD"}}
	factory := poolFactory{provider: containerCloud{Provider: cloud, t: t}}
	agents := &containerAgents{t: t, image: "rancher/k3s:v1.31.5-k3s1", byHost: map[string]string{}}
	clock := &offsetClock{}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, Cache: controller.CacheOptions(), Metrics: metricsserver.Options{BindAddress: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, setup := range []func(ctrl.Manager) error{
		(&controller.GeassNodeReconciler{Client: mgr.GetClient(), Scheme: scheme, SSH: agents, Compute: factory}).SetupWithManager,
		(&controller.GeassNodePoolReconciler{Client: mgr.GetClient(), Scheme: scheme, Compute: factory, Now: clock.Now, Reader: mgr.GetAPIReader()}).SetupWithManager,
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

	// ---- the cluster the nodes join, and the namespace the pods run in ----
	token := docker(t, "exec", k3sServerName, "cat", "/var/lib/rancher/k3s/server/node-token")
	server := docker(t, "inspect", "-f", "{{.NetworkSettings.IPAddress}}", k3sServerName)
	for _, object := range []client.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platform.SystemNamespace}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: poolNS}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "geass-token", Namespace: platform.SystemNamespace}, StringData: map[string]string{"token": token}},
	} {
		if err := c.Create(ctx, object); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
	}
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{
			Version: "v1", ServerURL: "https://" + server + ":6443",
			TokenSecretRef: corev1.SecretReference{Name: "geass-token", Namespace: platform.SystemNamespace},
		},
	}
	if err := c.Create(ctx, cluster); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster); err != nil {
		t.Fatal(err)
	}
	cluster.Status.Phase = geassv1alpha1.ClusterPhaseReady
	if err := c.Status().Update(ctx, cluster); err != nil {
		t.Fatal(err)
	}

	pool := &geassv1alpha1.GeassNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "burst", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassNodePoolSpec{
			Cluster: "default", ConnectionRef: "test", Size: "test", Region: "r1",
			MinNodes: 0, MaxNodes: 2, Storage: geassv1alpha1.NodeStorageNone, OrphanPolicy: geassv1alpha1.OrphanReport,
			Placement:   &geassv1alpha1.GeassNodePlacement{Region: "lab", Zone: "docker"},
			Autoscaling: &geassv1alpha1.GeassNodePoolAutoscaling{Enabled: true},
		},
	}
	if err := c.Create(ctx, pool); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Delete(context.Background(), &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "hog", Namespace: poolNS}})
		_ = c.Delete(context.Background(), pool)
	})

	// ---- load that does not fit: two pods of 60% of the server node's CPU ----
	request := cpu * 6 / 10
	hog := podSpec(request)
	platform.TolerateEphemeralNodes(&hog)
	replicas := int32(2)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "hog", Namespace: poolNS},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "hog"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "hog"}}, Spec: hog},
		},
	}
	if err := c.Create(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	// A pod that did not ask for nodes that must not hold data, and wants one of the pool's.
	database := podSpec(100)
	database.NodeSelector = map[string]string{platform.LabelPool: "burst"}
	databasePod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "database", Namespace: poolNS}, Spec: database}
	if err := c.Create(ctx, databasePod); err != nil {
		t.Fatal(err)
	}

	var poolNode string
	eventually(t, 8*time.Minute, "the pool to add a node that becomes ready", func() (bool, string) {
		var list corev1.NodeList
		if err := c.List(ctx, &list, client.MatchingLabels{platform.LabelPool: "burst"}); err != nil {
			return false, err.Error()
		}
		for _, n := range list.Items {
			for _, cond := range n.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					poolNode = n.Name
					return true, ""
				}
			}
		}
		var latest geassv1alpha1.GeassNodePool
		_ = c.Get(ctx, client.ObjectKeyFromObject(pool), &latest)
		return false, fmt.Sprintf("%d nodes with the label; pool phase %q: %s", len(list.Items), latest.Status.Phase, latest.Status.Message)
	})
	t.Logf("the pool added %s", poolNode)

	// The node joined as Geass configured it.
	var added corev1.Node
	if err := c.Get(ctx, client.ObjectKey{Name: poolNode}, &added); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		platform.LabelPool: "burst", platform.LabelAutoscaled: "true", platform.LabelNodeStorage: "none",
		platform.LabelRegion: "lab", platform.LabelZone: "docker",
	} {
		if added.Labels[key] != want {
			t.Errorf("label %s = %q, want %q (labels: %v)", key, added.Labels[key], want, added.Labels)
		}
	}
	taint := false
	for _, tn := range added.Spec.Taints {
		taint = taint || tn.Key == platform.TaintEphemeral
	}
	if !taint {
		t.Errorf("the node has no %s taint: %v", platform.TaintEphemeral, added.Spec.Taints)
	}

	eventually(t, 3*time.Minute, "both pods to run, one on each node", func() (bool, string) {
		var pods corev1.PodList
		if err := c.List(ctx, &pods, client.InNamespace(poolNS), client.MatchingLabels{"app": "hog"}); err != nil {
			return false, err.Error()
		}
		placed := map[string]int{}
		for _, p := range pods.Items {
			if p.Status.Phase == corev1.PodRunning {
				placed[p.Spec.NodeName]++
			}
		}
		return len(placed) == 2 && placed[poolNode] == 1, fmt.Sprintf("running pods by node: %v", placed)
	})

	// The taint holds: the pod that did not tolerate it never leaves Pending, though its node selector names the pool.
	var held corev1.Pod
	if err := c.Get(ctx, client.ObjectKeyFromObject(databasePod), &held); err != nil {
		t.Fatal(err)
	}
	if held.Spec.NodeName != "" {
		t.Errorf("the pod that does not tolerate the taint was placed on %s", held.Spec.NodeName)
	}

	var current geassv1alpha1.GeassNodePool
	if err := c.Get(ctx, client.ObjectKeyFromObject(pool), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.ReadyNodes != 1 || current.Status.Nodes != 1 {
		t.Errorf("the pool reports %d nodes, %d ready; want 1 and 1", current.Status.Nodes, current.Status.ReadyNodes)
	}

	// ---- the load goes: the pool empties the node and removes it, and its machine ----
	if err := c.Delete(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	// No node is removed for ten minutes after the pool last grew; those minutes are skipped, and then the pool starts
	// to count how long the node has been unneeded.
	clock.Advance(platform.DefaultScaleDownDelayAfterAdd + time.Minute)
	eventually(t, 2*time.Minute, "the pool to notice the node is not needed", func() (bool, string) {
		var latest geassv1alpha1.GeassNodePool
		if err := c.Get(ctx, client.ObjectKeyFromObject(pool), &latest); err != nil {
			return false, err.Error()
		}
		_, ok := latest.Status.UnneededSince[poolNode]
		return ok, fmt.Sprintf("unneededSince = %v", latest.Status.UnneededSince)
	})
	clock.Advance(platform.DefaultScaleDownAfter + time.Minute)

	eventually(t, 5*time.Minute, "the node, its machine and its container to be gone", func() (bool, string) {
		var node corev1.Node
		nodeErr := c.Get(ctx, client.ObjectKey{Name: poolNode}, &node)
		var geassNodes geassv1alpha1.GeassNodeList
		if err := c.List(ctx, &geassNodes, client.InNamespace(platform.SystemNamespace)); err != nil {
			return false, err.Error()
		}
		running := docker(t, "ps", "-q", "--filter", "label="+poolAgentTag)
		gone := apierrors.IsNotFound(nodeErr) && len(geassNodes.Items) == 0 && len(cloud.Instances()) == 0 && running == ""
		return gone, fmt.Sprintf("node error %v, %d GeassNodes, %d machines at the provider, containers %q", nodeErr, len(geassNodes.Items), len(cloud.Instances()), running)
	})

	// ---- a node that still has a pod on it: the pod is moved, through the eviction API, before the node goes ----
	clock.Reset()
	// "mover" would rather run on the pool's nodes, so it lands on one when there is one, and has room elsewhere.
	moverSpec := podSpec(100)
	platform.TolerateEphemeralNodes(&moverSpec)
	moverSpec.Affinity = &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{
		Weight: 100, Preference: corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: platform.LabelPool, Operator: corev1.NodeSelectorOpIn, Values: []string{"burst"}}}},
	}}}}
	one := int32(1)
	mover := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "mover", Namespace: poolNS},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "mover"}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "mover"}}, Spec: moverSpec},
		},
	}
	if err := c.Create(ctx, mover); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Delete(context.Background(), mover)
		_ = c.Delete(context.Background(), &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "hog2", Namespace: poolNS}})
	})
	hog2 := deployment.DeepCopy()
	hog2.ObjectMeta = metav1.ObjectMeta{Name: "hog2", Namespace: poolNS}
	hog2.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "hog2"}}
	hog2.Spec.Template.ObjectMeta.Labels = map[string]string{"app": "hog2"}
	if err := c.Create(ctx, hog2); err != nil {
		t.Fatal(err)
	}
	eventually(t, 8*time.Minute, "the pool to add a node again, and the second hog to run on it", func() (bool, string) {
		var pods corev1.PodList
		if err := c.List(ctx, &pods, client.InNamespace(poolNS), client.MatchingLabels{"app": "hog2"}); err != nil {
			return false, err.Error()
		}
		on := map[string]int{}
		for _, p := range pods.Items {
			if p.Status.Phase == corev1.PodRunning {
				on[p.Spec.NodeName]++
			}
		}
		for node, n := range on {
			if node != base.Name && n == 1 {
				poolNode = node
				return len(on) == 2, fmt.Sprintf("running by node: %v", on)
			}
		}
		return false, fmt.Sprintf("running by node: %v", on)
	})
	// Put "mover" on the pool's node: it is started again, and prefers it.
	var moverPods corev1.PodList
	if err := c.List(ctx, &moverPods, client.InNamespace(poolNS), client.MatchingLabels{"app": "mover"}); err != nil {
		t.Fatal(err)
	}
	for i := range moverPods.Items {
		_ = c.Delete(ctx, &moverPods.Items[i])
	}
	eventually(t, 2*time.Minute, "mover to run on the pool's node", func() (bool, string) {
		var pods corev1.PodList
		if err := c.List(ctx, &pods, client.InNamespace(poolNS), client.MatchingLabels{"app": "mover"}); err != nil {
			return false, err.Error()
		}
		for _, p := range pods.Items {
			if p.DeletionTimestamp.IsZero() && p.Status.Phase == corev1.PodRunning && p.Spec.NodeName == poolNode {
				return true, ""
			}
		}
		return false, fmt.Sprintf("%d mover pods", len(pods.Items))
	})

	if err := c.Delete(ctx, hog2); err != nil {
		t.Fatal(err)
	}
	clock.Advance(platform.DefaultScaleDownDelayAfterAdd + time.Minute)
	eventually(t, 2*time.Minute, "the pool to notice the node is not needed though a pod is on it", func() (bool, string) {
		var latest geassv1alpha1.GeassNodePool
		if err := c.Get(ctx, client.ObjectKeyFromObject(pool), &latest); err != nil {
			return false, err.Error()
		}
		_, ok := latest.Status.UnneededSince[poolNode]
		return ok, fmt.Sprintf("unneededSince = %v, message %q", latest.Status.UnneededSince, latest.Status.Message)
	})
	clock.Advance(platform.DefaultScaleDownAfter + time.Minute)

	eventually(t, 5*time.Minute, "the node to be emptied, removed with its machine, and the pod to be running elsewhere", func() (bool, string) {
		var node corev1.Node
		nodeErr := c.Get(ctx, client.ObjectKey{Name: poolNode}, &node)
		var pods corev1.PodList
		if err := c.List(ctx, &pods, client.InNamespace(poolNS), client.MatchingLabels{"app": "mover"}); err != nil {
			return false, err.Error()
		}
		moved := false
		for _, p := range pods.Items {
			moved = moved || (p.DeletionTimestamp.IsZero() && p.Status.Phase == corev1.PodRunning && p.Spec.NodeName == base.Name)
		}
		running := docker(t, "ps", "-q", "--filter", "label="+poolAgentTag)
		done := apierrors.IsNotFound(nodeErr) && moved && len(cloud.Instances()) == 0 && running == ""
		return done, fmt.Sprintf("node error %v, mover moved %v, %d machines, containers %q", nodeErr, moved, len(cloud.Instances()), running)
	})
}

func podSpec(cpuMillis int64) corev1.PodSpec {
	return corev1.PodSpec{
		TerminationGracePeriodSeconds: new(int64),
		Containers: []corev1.Container{{
			Name: "sleep", Image: "busybox:1.36", Command: []string{"sleep", "3600"},
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(cpuMillis, resource.DecimalSI),
			}},
		}},
	}
}
