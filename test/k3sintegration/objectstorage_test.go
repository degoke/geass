//go:build k3sintegration

package k3sintegration

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
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
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
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
	curlImage  = "curlimages/curl:8.10.1"
	oneProject = "payments"
	twoProject = "billing"
	osEnv      = "dev"
)

// clusterForward keeps a port-forward to the object storage server's pod alive for the test process, which is not
// in the cluster and so cannot resolve the server's service name. Requests keep the address the operator uses and are
// only dialled through the forward, so what is signed is what the server sees.
type clusterForward struct {
	cfg  *rest.Config
	kube kubernetes.Interface

	mu    sync.Mutex
	ports []int // remote ports to forward
	pod   string
	local map[int]int // remote port -> local port
	alive bool
	stop  chan struct{}
}

func newClusterForward(cfg *rest.Config, kube kubernetes.Interface) *clusterForward {
	return &clusterForward{cfg: cfg, kube: kube, local: map[int]int{}}
}

// forward sets which of the server's ports are forwarded, and drops the current forward so it is remade.
func (f *clusterForward) forward(ports ...int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ports = ports
	f.closeLocked()
}

func (f *clusterForward) closeLocked() {
	if f.stop != nil {
		close(f.stop)
		f.stop = nil
	}
	f.pod, f.alive = "", false
	f.local = map[int]int{}
}

// readyPod is the server's pod once every container in it is ready.
func (f *clusterForward) readyPod(ctx context.Context) string {
	pods, err := f.kube.CoreV1().Pods(platform.SystemNamespace).List(ctx, metav1.ListOptions{LabelSelector: platform.K8sLabelAppName + "=" + platform.ObjectStoreServerName})
	if err != nil {
		return ""
	}
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning || len(pod.Status.ContainerStatuses) == 0 {
			continue
		}
		ready := true
		for _, status := range pod.Status.ContainerStatuses {
			ready = ready && status.Ready
		}
		if ready {
			return pod.Name
		}
	}
	return ""
}

// run follows the server's pod, which is replaced when it restarts, for as long as ctx lives.
func (f *clusterForward) run(ctx context.Context) {
	for ctx.Err() == nil {
		f.mu.Lock()
		ports, current, alive := f.ports, f.pod, f.alive
		f.mu.Unlock()
		if len(ports) > 0 {
			pod := f.readyPod(ctx)
			if pod != "" && (pod != current || !alive) {
				f.start(ctx, pod, ports)
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
	f.mu.Lock()
	f.closeLocked()
	f.mu.Unlock()
}

func (f *clusterForward) start(ctx context.Context, pod string, ports []int) {
	transport, upgrader, err := spdy.RoundTripperFor(f.cfg)
	if err != nil {
		return
	}
	target := f.kube.CoreV1().RESTClient().Post().Resource("pods").Namespace(platform.SystemNamespace).Name(pod).SubResource("portforward").URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, target)
	specs := make([]string, len(ports))
	for i, port := range ports {
		specs[i] = fmt.Sprintf("0:%d", port)
	}
	stop, ready := make(chan struct{}), make(chan struct{})
	forwarder, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, specs, stop, ready, io.Discard, io.Discard)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.closeLocked()
	f.stop = stop
	f.mu.Unlock()
	go func() {
		_ = forwarder.ForwardPorts()
		f.mu.Lock()
		if f.stop == stop {
			f.alive = false
		}
		f.mu.Unlock()
	}()
	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		return
	case <-ctx.Done():
		return
	}
	forwarded, err := forwarder.GetPorts()
	if err != nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stop != stop {
		return
	}
	for _, p := range forwarded {
		f.local[int(p.Remote)] = int(p.Local)
	}
	f.pod, f.alive = pod, true
}

func (f *clusterForward) dial(ctx context.Context, _, address string) (net.Conn, error) {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var remote int
	if _, err := fmt.Sscanf(port, "%d", &remote); err != nil {
		return nil, err
	}
	f.mu.Lock()
	local, ok := f.local[remote]
	f.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("the server's port %d is not forwarded yet", remote)
	}
	return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", local))
}

// objectStorageEnv is what each engine's test needs.
type objectStorageEnv struct {
	ctx  context.Context
	c    client.Client
	kube kubernetes.Interface
	fwd  *clusterForward
}

// runCurl runs curl in a pod in a namespace, the way an app (or another pod) would reach the server, and returns what
// it printed. The first run in a cluster waits while the image is pulled.
func (e *objectStorageEnv) runCurl(t *testing.T, namespace string, args ...string) string {
	t.Helper()
	name := fmt.Sprintf("curl-%d", time.Now().UnixNano()%1_000_000_000)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			// k3s enforces network policies with kube-router, which lets a new pod through a policy's "from" rule a
			// moment after the pod starts; curl run at once is refused, even from a namespace the policy allows.
			// Waiting first makes an allowed pod allowed and leaves a blocked one blocked.
			Containers: []corev1.Container{{
				Name:    "curl",
				Image:   curlImage,
				Command: []string{"sh", "-c", `sleep 5; exec curl "$@"`, "curl"},
				Args:    args,
			}},
		},
	}
	if _, err := e.kube.CoreV1().Pods(namespace).Create(e.ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("could not start a curl pod in %s: %v", namespace, err)
	}
	defer func() {
		_ = e.kube.CoreV1().Pods(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
	}()
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		current, err := e.kube.CoreV1().Pods(namespace).Get(e.ctx, name, metav1.GetOptions{})
		if err == nil && (current.Status.Phase == corev1.PodSucceeded || current.Status.Phase == corev1.PodFailed) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	logs, err := e.kube.CoreV1().Pods(namespace).GetLogs(name, &corev1.PodLogOptions{}).DoRaw(e.ctx)
	if err != nil {
		t.Fatalf("no output from the curl pod in %s: %v", namespace, err)
	}
	return strings.TrimSpace(string(logs))
}

// signed is one signed S3 request for runCurl, printing only the status code.
func signed(creds, service, method, target string, extra ...string) []string {
	args := []string{"-s", "-m", "10", "-o", "/dev/null", "-w", "%{http_code} ", "-X", method,
		"--aws-sigv4", "aws:amz:us-east-1:" + service, "--user", creds, "-H", "x-amz-content-sha256: UNSIGNED-PAYLOAD"}
	return append(append(args, extra...), target)
}

// together joins requests into one curl invocation, so a pod answers many of them.
func together(requests ...[]string) []string {
	var out []string
	for i, request := range requests {
		if i > 0 {
			out = append(out, "--next")
		}
		out = append(out, request...)
	}
	return out
}

func codes(output string) []string { return strings.Fields(output) }

func (e *objectStorageEnv) eventually(t *testing.T, timeout time.Duration, what string, check func() (bool, string)) {
	t.Helper()
	eventually(t, timeout, what, check)
}

func (e *objectStorageEnv) storeReady(name string) func() (bool, string) {
	return func() (bool, string) {
		var store geassv1alpha1.GeassObjectStore
		if err := e.c.Get(e.ctx, client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &store); err != nil {
			return false, err.Error()
		}
		for _, cond := range store.Status.Conditions {
			if cond.Type == platform.ConditionReady {
				return cond.Status == metav1.ConditionTrue, cond.Message
			}
		}
		return false, "no Ready condition yet"
	}
}

func (e *objectStorageEnv) gone(obj client.Object) func() (bool, string) {
	return func() (bool, string) {
		err := e.c.Get(e.ctx, client.ObjectKeyFromObject(obj), obj)
		return apierrors.IsNotFound(err), fmt.Sprint(err)
	}
}

func (e *objectStorageEnv) secretValue(t *testing.T, namespace, name, key string) string {
	t.Helper()
	var secret corev1.Secret
	if err := e.c.Get(e.ctx, client.ObjectKey{Name: name, Namespace: namespace}, &secret); err != nil {
		t.Fatalf("secret %s/%s: %v", namespace, name, err)
	}
	return string(secret.Data[key])
}

func bucketStore(name, project string, engine geassv1alpha1.GeassObjectStoreEngine, bucket string) *geassv1alpha1.GeassObjectStore {
	return &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Project: project, Environment: osEnv, Engine: engine,
			Placement: geassv1alpha1.ObjectStorePlacementInCluster, Buckets: []string{bucket},
		},
	}
}

// TestObjectStorageOnK3s runs the operator against a real k3s, for each server the cluster's object storage can run
// on, and follows it through its life: the server coming up hardened, a network that enforces its policy, a bucket per
// project that each app can use and no other, the data surviving a restart, a volume that is not resized where its
// class cannot be, a bucket being removed with its login, a server that refuses to go while buckets live on it, and
// everything it made being removed with it.
//
// k3s matters here because it enforces network policies, which a kind cluster does not.
func TestObjectStorageOnK3s(t *testing.T) {
	if os.Getenv("K3S_INTEGRATION") != "true" {
		t.Skip("set K3S_INTEGRATION=true to run the k3s object storage integration test (hack/k3s-up.sh starts a k3s)")
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

	kube := kubernetes.NewForConfigOrDie(cfg)
	fwd := newClusterForward(cfg, kube)
	go fwd.run(ctx)

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, Cache: controller.CacheOptions(), Metrics: metricsserver.Options{BindAddress: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DialContext: fwd.dial}}
	for _, setup := range []func(ctrl.Manager) error{
		(&controller.GeassClusterReconciler{Client: mgr.GetClient(), Scheme: scheme}).SetupWithManager,
		(&controller.GeassProjectReconciler{Client: mgr.GetClient(), Scheme: scheme}).SetupWithManager,
		(&controller.GeassObjectStoreReconciler{Client: mgr.GetClient(), Scheme: scheme, HTTP: httpClient}).SetupWithManager,
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
	e := &objectStorageEnv{ctx: ctx, c: c, kube: kube, fwd: fwd}

	// ---- the cluster and two projects, with no add-ons ----
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
	for _, name := range []string{oneProject, twoProject} {
		project := &geassv1alpha1.GeassProject{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: "default"}, Environments: []string{osEnv}},
		}
		if err := c.Create(ctx, project); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Delete(context.Background(), project) })
	}
	for _, name := range []string{oneProject, twoProject} {
		eventually(t, 2*time.Minute, name+" to be ready", func() (bool, string) {
			var latest geassv1alpha1.GeassProject
			if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &latest); err != nil {
				return false, err.Error()
			}
			for _, cond := range latest.Status.Conditions {
				if cond.Type == platform.ConditionReady && cond.Status == metav1.ConditionTrue {
					return true, ""
				}
			}
			return false, "the project is not ready"
		})
	}

	for _, engine := range []geassv1alpha1.GeassObjectStoreEngine{geassv1alpha1.ObjectStoreEngineSeaweedFS, geassv1alpha1.ObjectStoreEngineGarage} {
		t.Run(string(engine), func(t *testing.T) { e.engine(t, engine) })
		if t.Failed() {
			break
		}
	}
}

func (e *objectStorageEnv) engine(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine) {
	server := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassObjectStoreSpec{Engine: engine, Placement: geassv1alpha1.ObjectStorePlacementInCluster},
	}
	info, _ := platform.ObjectStoreServerFor(engine)
	if engine == geassv1alpha1.ObjectStoreEngineGarage {
		e.fwd.forward(int(info.S3Port), platform.GarageAdminPort)
	} else {
		e.fwd.forward(int(info.S3Port))
	}
	paymentsNS, _ := platform.ProjectNamespace(oneProject, osEnv)
	billingNS, _ := platform.ProjectNamespace(twoProject, osEnv)
	one, two := bucketStore("assets", oneProject, engine, "uploads"), bucketStore("files", twoProject, engine, "uploads")
	// Whatever happens, the next engine starts with nothing of this one's.
	t.Cleanup(func() {
		for _, obj := range []client.Object{one, two, server} {
			_ = e.c.Delete(context.Background(), obj)
			deadline := time.Now().Add(3 * time.Minute)
			for time.Now().Before(deadline) && !apierrors.IsNotFound(e.c.Get(context.Background(), client.ObjectKeyFromObject(obj), obj)) {
				time.Sleep(2 * time.Second)
			}
		}
	})

	// ---- the server comes up ----
	if err := e.c.Create(e.ctx, server); err != nil {
		t.Fatal(err)
	}
	e.eventually(t, 15*time.Minute, "the server to be ready", e.storeReady(server.Name))
	serverKey := client.ObjectKey{Name: platform.ObjectStoreServerName, Namespace: platform.SystemNamespace}

	t.Run("it comes up with a volume and a network policy", func(t *testing.T) {
		var deploy appsv1.Deployment
		if err := e.c.Get(e.ctx, serverKey, &deploy); err != nil {
			t.Fatal(err)
		}
		wantContainers := 1
		if engine == geassv1alpha1.ObjectStoreEngineSeaweedFS {
			wantContainers = 2 // the server, and the forwarder that publishes S3 alone
		}
		if got := len(deploy.Spec.Template.Spec.Containers); got != wantContainers {
			t.Errorf("the pod has %d containers, want %d", got, wantContainers)
		}
		var claim corev1.PersistentVolumeClaim
		if err := e.c.Get(e.ctx, serverKey, &claim); err != nil {
			t.Fatal(err)
		}
		if size := claim.Spec.Resources.Requests[corev1.ResourceStorage]; size.Cmp(resource.MustParse("10Gi")) != 0 {
			t.Errorf("the volume asks for %s, want 10Gi", size.String())
		}
		var stored geassv1alpha1.GeassObjectStore
		if err := e.c.Get(e.ctx, client.ObjectKeyFromObject(server), &stored); err != nil {
			t.Fatal(err)
		}
		if stored.Spec.StorageSize == nil || stored.Spec.StorageSize.Cmp(resource.MustParse("10Gi")) != 0 {
			t.Errorf("the size is not on the resource: %v", stored.Spec.StorageSize)
		}
		var policy networkingv1.NetworkPolicy
		if err := e.c.Get(e.ctx, client.ObjectKey{Name: platform.ObjectStoreServerPolicyName, Namespace: platform.SystemNamespace}, &policy); err != nil {
			t.Fatalf("no network policy: %v", err)
		}
		want := platform.ObjectStoreServerPolicy(engine)
		if len(policy.Spec.Ingress) != len(want.Spec.Ingress) {
			t.Errorf("the policy has %d ingress rules, want %d", len(policy.Spec.Ingress), len(want.Spec.Ingress))
		}
	})

	t.Run("only the S3 port answers a pod in a project, and the admin port answers only the platform", func(t *testing.T) {
		pods, err := e.kube.CoreV1().Pods(platform.SystemNamespace).List(e.ctx, metav1.ListOptions{LabelSelector: platform.K8sLabelAppName + "=" + platform.ObjectStoreServerName})
		if err != nil || len(pods.Items) == 0 {
			t.Fatalf("no server pod: %v", err)
		}
		ip := pods.Items[0].Status.PodIP
		scan := []int{8333, 8334, 18333, 18334, 8888, 9333, 8080, 19333, 18080, 18888, 3900, 3901, 3902, 3903}
		var requests [][]string
		for _, port := range scan {
			requests = append(requests, []string{"-s", "-m", "4", "-o", "/dev/null", "-w", "%{http_code} ", fmt.Sprintf("http://%s:%d/", ip, port)})
		}
		got := codes(e.runCurl(t, paymentsNS, together(requests...)...))
		if len(got) != len(scan) {
			t.Fatalf("the scan answered %v for ports %v", got, scan)
		}
		var open []int
		for i, port := range scan {
			if got[i] != "000" {
				open = append(open, port)
			}
		}
		if len(open) != 1 || open[0] != int(info.S3Port) {
			t.Errorf("a pod in a project reaches ports %v of the server, want only %d (answers: %v for %v)", open, info.S3Port, got, scan)
		}
		if engine == geassv1alpha1.ObjectStoreEngineGarage {
			// The admin port is open on the pod's own address, so what closed it to the project is the network policy.
			health := fmt.Sprintf("http://geass-s3.%s.svc:%d/health", platform.SystemNamespace, platform.GarageAdminPort)
			from := func(namespace string) string {
				return strings.TrimSpace(e.runCurl(t, namespace, "-s", "-m", "6", "-o", "/dev/null", "-w", "%{http_code}", health))
			}
			if code := from(platform.SystemNamespace); code != "200" {
				t.Errorf("the platform's own namespace gets %s from the admin API, want 200", code)
			}
			if code := from(paymentsNS); code != "000" {
				t.Errorf("a project's namespace gets %s from the admin API, want it blocked", code)
			}
		}
	})

	// ---- a bucket for each project ----
	for _, store := range []*geassv1alpha1.GeassObjectStore{one, two} {
		if err := e.c.Create(e.ctx, store); err != nil {
			t.Fatal(err)
		}
	}
	for _, store := range []*geassv1alpha1.GeassObjectStore{one, two} {
		e.eventually(t, 3*time.Minute, store.Name+" to be ready", e.storeReady(store.Name))
	}
	endpoint := e.secretValue(t, paymentsNS, "assets-connection", "endpoint")
	paymentsCreds := e.secretValue(t, paymentsNS, "assets-connection", "accessKey") + ":" + e.secretValue(t, paymentsNS, "assets-connection", "secretKey")
	billingCreds := e.secretValue(t, billingNS, "files-connection", "accessKey") + ":" + e.secretValue(t, billingNS, "files-connection", "secretKey")
	rootCreds := e.secretValue(t, platform.SystemNamespace, platform.ClusterObjectStoreName+"-root", "accessKey") + ":" + e.secretValue(t, platform.SystemNamespace, platform.ClusterObjectStoreName+"-root", "secretKey")
	own, other := endpoint+"/"+oneProject+".dev.uploads", endpoint+"/"+twoProject+".dev.uploads"

	t.Run("each app can use its own bucket and no other", func(t *testing.T) {
		requests := [][]string{
			signed(paymentsCreds, "s3", "PUT", own+"/hello.txt", "-d", "hello"),
			signed(paymentsCreds, "s3", "GET", own+"/hello.txt"),
			signed(paymentsCreds, "s3", "GET", own+"?list-type=2"),
			signed(paymentsCreds, "s3", "PUT", other+"/evil.txt", "-d", "evil"),
			signed(paymentsCreds, "s3", "GET", other+"/anything"),
			signed(paymentsCreds, "s3", "GET", other+"?list-type=2"),
			signed(paymentsCreds, "s3", "PUT", endpoint+"/"+oneProject+".dev.sneaky"),
			signed(paymentsCreds, "s3", "DELETE", own),
			signed(billingCreds, "s3", "GET", own+"/hello.txt"),
			signed(billingCreds, "s3", "PUT", other+"/mine.txt", "-d", "mine"),
		}
		want := []string{"200", "200", "200", "403", "403", "403", "403", "403", "403", "200"}
		got := codes(e.runCurl(t, paymentsNS, together(requests...)...))
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("the apps got %v, want %v", got, want)
		}
		if engine == geassv1alpha1.ObjectStoreEngineSeaweedFS {
			// A login for an app has no say in who else exists.
			iam := signed(paymentsCreds, "iam", "POST", endpoint+"/", "-d", "Action=CreateUser&UserName=intruder&Version=2010-05-08")
			if got := codes(e.runCurl(t, paymentsNS, iam...)); len(got) != 1 || got[0] != "403" {
				t.Errorf("an app's key making an IAM user got %v, want 403", got)
			}
		}
	})

	t.Run("the data and the logins survive the server's pod being replaced", func(t *testing.T) {
		pods, _ := e.kube.CoreV1().Pods(platform.SystemNamespace).List(e.ctx, metav1.ListOptions{LabelSelector: platform.K8sLabelAppName + "=" + platform.ObjectStoreServerName})
		old := ""
		if len(pods.Items) > 0 {
			old = pods.Items[0].Name
			if err := e.kube.CoreV1().Pods(platform.SystemNamespace).Delete(e.ctx, old, metav1.DeleteOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		e.eventually(t, 5*time.Minute, "a new server pod", func() (bool, string) {
			name := e.fwd.readyPod(e.ctx)
			return name != "" && name != old, "the pod is " + name
		})
		got := e.runCurl(t, paymentsNS, "-s", "-m", "10", "--aws-sigv4", "aws:amz:us-east-1:s3", "--user", paymentsCreds,
			"-H", "x-amz-content-sha256: UNSIGNED-PAYLOAD", own+"/hello.txt")
		if got != "hello" {
			t.Errorf("after the restart the object reads %q, want hello", got)
		}
		e.eventually(t, 2*time.Minute, "the server to be ready again", e.storeReady(server.Name))
	})

	t.Run("a bigger volume is left alone where the class cannot be resized", func(t *testing.T) {
		var stored geassv1alpha1.GeassObjectStore
		if err := e.c.Get(e.ctx, client.ObjectKeyFromObject(server), &stored); err != nil {
			t.Fatal(err)
		}
		bigger := resource.MustParse("20Gi")
		stored.Spec.StorageSize = &bigger
		if err := e.c.Update(e.ctx, &stored); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Second)
		var claim corev1.PersistentVolumeClaim
		if err := e.c.Get(e.ctx, serverKey, &claim); err != nil {
			t.Fatal(err)
		}
		if size := claim.Spec.Resources.Requests[corev1.ResourceStorage]; size.Cmp(resource.MustParse("10Gi")) != 0 {
			t.Errorf("the volume was asked for %s, but k3s's local storage cannot be resized so it must stay 10Gi", size.String())
		}
		e.eventually(t, 2*time.Minute, "the server to stay ready", e.storeReady(server.Name))
	})

	t.Run("a removed bucket takes its login with it", func(t *testing.T) {
		if err := e.c.Delete(e.ctx, two); err != nil {
			t.Fatal(err)
		}
		e.eventually(t, 3*time.Minute, "the bucket to be removed", e.gone(two))
		head := signed(rootCreds, "s3", "GET", other+"?list-type=2")
		if got := codes(e.runCurl(t, platform.SystemNamespace, head...)); len(got) != 1 || got[0] != "404" {
			t.Errorf("the removed bucket answers %v to the server's own login, want 404", got)
		}
		switch engine {
		case geassv1alpha1.ObjectStoreEngineGarage:
			token := e.secretValue(t, platform.SystemNamespace, platform.ClusterObjectStoreName+"-root", "adminToken")
			keys := e.runCurl(t, platform.SystemNamespace, "-s", "-m", "10", "-H", "Authorization: Bearer "+token,
				fmt.Sprintf("http://geass-s3.%s.svc:%d/v2/ListKeys", platform.SystemNamespace, platform.GarageAdminPort))
			if strings.Contains(keys, "geass-files") || !strings.Contains(keys, "geass-assets") {
				t.Errorf("the keys on the server are %s: the removed bucket's login must be gone, and the other's kept", keys)
			}
		default:
			users := e.runCurl(t, platform.SystemNamespace, "-s", "-m", "10", "--aws-sigv4", "aws:amz:us-east-1:iam", "--user", rootCreds,
				"-H", "x-amz-content-sha256: UNSIGNED-PAYLOAD", "-X", "POST", "-d", "Action=ListUsers&Version=2010-05-08",
				fmt.Sprintf("http://geass-s3.%s.svc:%d/", platform.SystemNamespace, platform.SeaweedFSS3Port))
			if strings.Contains(users, "geass-files") || !strings.Contains(users, "geass-assets") {
				t.Errorf("the users on the server are %s: the removed bucket's login must be gone, and the other's kept", users)
			}
		}
	})

	t.Run("the server will not go while a bucket is on it, and takes everything with it when it does", func(t *testing.T) {
		if err := e.c.Delete(e.ctx, server); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Second)
		var deploy appsv1.Deployment
		if err := e.c.Get(e.ctx, serverKey, &deploy); err != nil {
			t.Fatalf("the server was removed while a bucket lives on it: %v", err)
		}
		// Removing the bucket now, with the server itself going, must still clean up on the server.
		if err := e.c.Delete(e.ctx, one); err != nil {
			t.Fatal(err)
		}
		e.eventually(t, 3*time.Minute, "the bucket to be removed", e.gone(one))
		e.eventually(t, 5*time.Minute, "the server to be removed", e.gone(&geassv1alpha1.GeassObjectStore{ObjectMeta: metav1.ObjectMeta{Name: server.Name, Namespace: server.Namespace}}))
		for _, obj := range []client.Object{
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: serverKey.Name, Namespace: serverKey.Namespace}},
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: serverKey.Name, Namespace: serverKey.Namespace}},
			&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: serverKey.Name, Namespace: serverKey.Namespace}},
			&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerPolicyName, Namespace: platform.SystemNamespace}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "geass-s3-config", Namespace: platform.SystemNamespace}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterObjectStoreName + "-root", Namespace: platform.SystemNamespace}},
		} {
			e.eventually(t, 3*time.Minute, fmt.Sprintf("%T %s to be removed", obj, obj.GetName()), e.gone(obj))
		}
	})
}
