// Command loadtest measures what the operator costs as a cluster grows.
//
// For each size it starts a real Kubernetes API server and etcd (envtest), runs the operator's own binary against
// it, fills the cluster with projects and services, and reads the operator's memory and CPU from its own metrics and
// from /proc. Nothing is stubbed in the operator: it creates the Deployment, Service, Secret and the rest of each
// service itself. What is stubbed is the rest of Kubernetes. envtest has no kubelet and no controller-manager, so
// this program plays both: it makes the pods a Deployment asks for and marks the Deployment available, which is what
// takes a service to Ready.
//
//	go run ./hack/loadtest -apps 250,500,1000
//
// What it cannot tell you: the real cost of a real workload (builds, backups, databases, a dashboard with people on
// it), and what the machine around the operator is doing. It measures the operator holding a cluster of ready
// services and being restarted, which is where its memory goes: the informer caches.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

var (
	appsFlag = flag.String("apps", "250,500,1000",
		"comma-separated numbers of services to measure, one cluster each")
	perProject    = flag.Int("per-project", 25, "services in each project")
	podsPerApp    = flag.Int("replicas", 1, "replicas of each service")
	settleFor     = flag.Duration("settle", 60*time.Second, "how long the operator must be idle before it is measured")
	sampleFor     = flag.Duration("sample", 45*time.Second, "how long it is sampled for")
	readyTimeout  = flag.Duration("timeout", 25*time.Minute, "how long to wait for every service to be ready")
	managerBinary = flag.String("manager", "", "the operator binary; built from ./cmd when empty")
	memoryLimit   = flag.String("gomemlimit", "",
		"GOMEMLIMIT for the operator, as in its Deployment (800MiB). Empty measures it with none, which shows "+
			"what it needs rather than what it is squeezed into")
	outputJSON     = flag.String("json", "", "also write the results as JSON to this file")
	keepLogs       = flag.String("logs", "", "write each operator's log to files in this directory")
	concurrency    = flag.Int("concurrency", 24, "parallel requests while filling the cluster")
	skipRestart    = flag.Bool("skip-restart", false, "do not measure a restart of the operator")
	skipMonitoring = flag.Bool("skip-etcd", false, "do not read the API server's storage metrics")
)

func main() {
	flag.Parse()
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	manager := *managerBinary
	if manager == "" {
		manager = filepath.Join(os.TempDir(), "geass-loadtest-manager")
		fmt.Fprintln(os.Stderr, "building the operator ...")
		build := exec.Command("go", "build", "-o", manager, "./cmd/main.go")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		build.Stdout, build.Stderr = os.Stderr, os.Stderr
		if err := build.Run(); err != nil {
			fatal(fmt.Errorf("could not build the operator: %w", err))
		}
	}
	sizes := strings.Split(*appsFlag, ",")
	results := make([]result, 0, len(sizes))
	for _, field := range sizes {
		apps, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || apps < 1 {
			fatal(fmt.Errorf("bad -apps value %q", field))
		}
		fmt.Fprintf(os.Stderr, "\n=== %d services ===\n", apps)
		res, err := measure(root, manager, apps)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAILED at %d services: %v\n", apps, err)
			res.Error = err.Error()
		}
		results = append(results, res)
		printTable(results)
	}
	if *outputJSON != "" {
		raw, _ := json.MarshalIndent(results, "", "  ")
		if err := os.WriteFile(*outputJSON, raw, 0o600); err != nil {
			fatal(err)
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// result is what is measured at one size.
type result struct {
	Apps     int `json:"apps"`
	Projects int `json:"projects"`
	// Objects is what the API server holds, by kind, once everything is ready.
	Objects      map[string]int `json:"objects"`
	TotalObjects int            `json:"totalObjects"`
	StorageBytes float64        `json:"storageBytes"`

	SettleSeconds float64 `json:"settleSeconds"`
	// Steady state, after the operator has been idle for a while.
	RSSMiB       float64 `json:"rssMiB"`
	HeapLiveMiB  float64 `json:"heapLiveMiB"`
	HeapAllocMax float64 `json:"heapAllocMaxMiB"`
	Goroutines   float64 `json:"goroutines"`
	CPUSettle    float64 `json:"cpuSecondsToSettle"`
	Reconciles   float64 `json:"reconciles"`
	// After the operator is stopped and started again over the same cluster.
	RestartPeakRSSMiB float64 `json:"restartPeakRSSMiB"`
	RestartSettleSecs float64 `json:"restartSettleSeconds"`
	RestartCPUSecs    float64 `json:"restartCPUSeconds"`
	RestartHeapMiB    float64 `json:"restartHeapLiveMiB"`

	Error string `json:"error,omitempty"`
}

func printTable(results []result) {
	fmt.Println()
	fmt.Println("| Services | Projects | Objects in the API server | Operator RSS | Live heap | Settled in | " +
		"Restart: peak RSS | Restart: settled in |")
	fmt.Println("| --- | --- | --- | --- | --- | --- | --- | --- |")
	for _, r := range results {
		if r.Error != "" {
			fmt.Printf("| %d | %d | failed: %s |  |  |  |  |  |\n", r.Apps, r.Projects, r.Error)
			continue
		}
		fmt.Printf("| %d | %d | %d | %.0f MiB | %.0f MiB | %.0f s | %.0f MiB | %.0f s |\n",
			r.Apps, r.Projects, r.TotalObjects, r.RSSMiB, r.HeapLiveMiB, r.SettleSeconds,
			r.RestartPeakRSSMiB, r.RestartSettleSecs)
	}
	last := results[len(results)-1]
	if last.Error != "" || len(last.Objects) == 0 {
		return
	}
	fmt.Printf("\nWhat the API server holds at %d services (%.1f MiB of storage):\n\n", last.Apps, last.StorageBytes/mib)
	fmt.Println("| Kind | Objects | Per service |")
	fmt.Println("| --- | --- | --- |")
	kinds := make([]string, 0, len(last.Objects))
	for kind := range last.Objects {
		kinds = append(kinds, kind)
	}
	slices.SortFunc(kinds, func(a, b string) int { return last.Objects[b] - last.Objects[a] })
	for _, kind := range kinds {
		fmt.Printf("| %s | %d | %.2f |\n", kind, last.Objects[kind], float64(last.Objects[kind])/float64(last.Apps))
	}
}

func measure(root, manager string, apps int) (result, error) {
	res := result{Apps: apps, Projects: (apps + *perProject - 1) / *perProject}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(root, "config", "crd", "bases")},
		CRDs:                  monitoringStubs(),
		ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: assetsDir(root),
	}
	// envtest's default Service range is a /24, which holds 253 services and then refuses the next one.
	env.ControlPlane.GetAPIServer().Configure().Set("service-cluster-ip-range", "10.96.0.0/12")
	cfg, err := env.Start()
	if err != nil {
		return res, fmt.Errorf("start envtest: %w", err)
	}
	defer func() { _ = env.Stop() }()
	cfg.QPS, cfg.Burst = 400, 800

	user, err := env.AddUser(envtest.User{Name: "loadtest", Groups: []string{"system:masters"}}, nil)
	if err != nil {
		return res, err
	}
	kubeconfig, err := user.KubeConfig()
	if err != nil {
		return res, err
	}
	kubeconfigPath := filepath.Join(os.TempDir(), fmt.Sprintf("geass-loadtest-%d.kubeconfig", os.Getpid()))
	if err := os.WriteFile(kubeconfigPath, kubeconfig, 0o600); err != nil {
		return res, err
	}
	defer func() { _ = os.Remove(kubeconfigPath) }()

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = geassv1alpha1.AddToScheme(scheme)
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return res, err
	}
	kube := kubernetes.NewForConfigOrDie(cfg)

	if err := populateBase(ctx, c); err != nil {
		return res, err
	}
	op, err := startOperator(manager, kubeconfigPath, "first")
	if err != nil {
		return res, err
	}
	defer op.stop()
	if err := op.waitHealthy(2 * time.Minute); err != nil {
		return res, err
	}

	// The stand-in for kubelet and controller-manager runs for as long as the cluster does.
	sim := &simulator{c: c, replicas: int32(*podsPerApp), seen: map[string]int64{}}
	resync := 30 * time.Second
	informers, err := ctrlcache.New(cfg, ctrlcache.Options{Scheme: scheme, SyncPeriod: &resync})
	if err != nil {
		return res, err
	}
	go sim.run(ctx, informers)

	started := time.Now()
	cpuBefore := op.cpuSeconds()
	if err := populateApps(ctx, c, apps); err != nil {
		return res, err
	}
	if err := waitReady(ctx, c, apps, *readyTimeout); err != nil {
		return res, err
	}
	fmt.Fprintf(os.Stderr, "all %d services ready after %s\n", apps, time.Since(started).Round(time.Second))
	if err := op.waitIdle(*settleFor, 20*time.Minute); err != nil {
		return res, err
	}
	res.SettleSeconds = time.Since(started).Seconds()
	res.CPUSettle = op.cpuSeconds() - cpuBefore

	objects, storage := storageObjects(ctx, c, kube, *skipMonitoring)
	res.Objects, res.StorageBytes = objects, storage
	for _, n := range objects {
		res.TotalObjects += n
	}

	samples := op.sample(*sampleFor, 3*time.Second)
	res.RSSMiB = samples.rssMax / mib
	res.HeapLiveMiB = samples.heapMin / mib
	res.HeapAllocMax = samples.heapMax / mib
	res.Goroutines = samples.goroutines
	res.Reconciles = samples.reconciles
	fmt.Fprintf(os.Stderr, "steady: RSS %.0f MiB, live heap %.0f MiB (heap allocated up to %.0f MiB), %d objects\n",
		res.RSSMiB, res.HeapLiveMiB, res.HeapAllocMax, res.TotalObjects)

	if *skipRestart {
		return res, nil
	}
	op.stop()
	again, err := startOperator(manager, kubeconfigPath, "restart")
	if err != nil {
		return res, err
	}
	defer again.stop()
	restarted := time.Now()
	peak := again.watchPeak(&res)
	if err := again.waitIdle(*settleFor, 15*time.Minute); err != nil {
		return res, err
	}
	res.RestartSettleSecs = time.Since(restarted).Seconds() - settleFor.Seconds()
	res.RestartPeakRSSMiB = math.Max(peak(), again.peakRSS()) / mib
	res.RestartCPUSecs = again.cpuSeconds()
	after := again.sample(15*time.Second, 3*time.Second)
	res.RestartHeapMiB = after.heapMin / mib
	fmt.Fprintf(os.Stderr, "restart: peak RSS %.0f MiB, idle after %.0f s, then live heap %.0f MiB\n",
		res.RestartPeakRSSMiB, res.RestartSettleSecs, res.RestartHeapMiB)
	return res, nil
}

const mib = 1024 * 1024

func assetsDir(root string) string {
	if dir := os.Getenv("KUBEBUILDER_ASSETS"); dir != "" {
		return dir
	}
	matches, _ := filepath.Glob(filepath.Join(root, "bin", "k8s", "*"))
	slices.Sort(matches)
	if len(matches) > 0 {
		return matches[len(matches)-1]
	}
	return ""
}

// monitoringStubs are the three Prometheus Operator kinds the operator writes. They accept anything: only the cost
// of holding them matters here.
func monitoringStubs() []*apiextensionsv1.CustomResourceDefinition {
	kinds := [][2]string{
		{"servicemonitors", "ServiceMonitor"}, {"podmonitors", "PodMonitor"}, {"prometheusrules", "PrometheusRule"},
	}
	crds := make([]*apiextensionsv1.CustomResourceDefinition, 0, len(kinds))
	for _, kind := range kinds {
		crds = append(crds, &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: kind[0] + ".monitoring.coreos.com"},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "monitoring.coreos.com",
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural: kind[0], Singular: strings.ToLower(kind[1]), Kind: kind[1], ListKind: kind[1] + "List",
				},
				Scope: apiextensionsv1.NamespaceScoped,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
					Name: "v1", Served: true, Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type: "object", XPreserveUnknownFields: ptr.To(true),
					}},
				}},
			},
		})
	}
	return crds
}

// populateBase makes what a cluster that Geass installed has before anyone deploys anything: three ready nodes, the
// operator's namespace and the cluster object.
func populateBase(ctx context.Context, c client.Client) error {
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platform.SystemNamespace}}); err != nil {
		return err
	}
	token := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "geass-token", Namespace: platform.SystemNamespace},
		StringData: map[string]string{"token": "x"},
	}
	if err := c.Create(ctx, token); err != nil {
		return err
	}
	for i := range 3 {
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("node-%d", i)}}
		if i == 0 {
			node.Labels = map[string]string{"node-role.kubernetes.io/control-plane": "true"}
		}
		if err := c.Create(ctx, node); err != nil {
			return err
		}
		big := corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("96"), corev1.ResourceMemory: resource.MustParse("512Gi"),
			corev1.ResourcePods: resource.MustParse("100000"),
		}
		node.Status = corev1.NodeStatus{
			Capacity:    big,
			Allocatable: big,
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			Addresses:   []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: fmt.Sprintf("10.0.0.%d", 10+i)}},
		}
		if err := c.Status().Update(ctx, node); err != nil {
			return err
		}
	}
	disabled := false
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{
			Version: "v1", ServerURL: "https://10.0.0.10:6443",
			TokenSecretRef: corev1.SecretReference{Name: "geass-token", Namespace: platform.SystemNamespace},
			Addons: geassv1alpha1.GeassClusterAddonsSpec{
				CertManager: geassv1alpha1.GeassClusterCertManagerAddon{Enabled: &disabled},
				Monitoring:  geassv1alpha1.GeassClusterMonitoringAddon{Enabled: &disabled},
			},
		},
	}
	return c.Create(ctx, cluster)
}

func appName(i int) string     { return fmt.Sprintf("svc-%06d", i) }
func projectName(p int) string { return fmt.Sprintf("p%05d", p) }

// populateApps creates the projects and the services in them, the way people do: through the Geass objects, and
// nothing else. Everything the operator makes of them is its own doing.
func populateApps(ctx context.Context, c client.Client, apps int) error {
	projects := (apps + *perProject - 1) / *perProject
	work := make(chan func() error, 1024)
	var wg sync.WaitGroup
	var failed atomic.Value
	for range *concurrency {
		wg.Go(func() {
			for job := range work {
				if err := job(); err != nil && !apierrors.IsAlreadyExists(err) {
					failed.CompareAndSwap(nil, err)
				}
			}
		})
	}
	for p := range projects {
		work <- func() error {
			return c.Create(ctx, &geassv1alpha1.GeassProject{
				ObjectMeta: metav1.ObjectMeta{Name: projectName(p), Namespace: platform.SystemNamespace},
				Spec: geassv1alpha1.GeassProjectSpec{
					ClusterRef: corev1.LocalObjectReference{Name: "default"}, Environments: []string{"production"},
					Network: geassv1alpha1.NewProjectNetwork(),
				},
			})
		}
	}
	// Projects first, so that no service is made for a project that does not exist yet.
	close(work)
	wg.Wait()
	if err := failed.Load(); err != nil {
		return err.(error)
	}
	fmt.Fprintf(os.Stderr, "%d projects created\n", projects)
	if err := waitProjectsReady(ctx, c, projects, 5*time.Minute); err != nil {
		return err
	}

	work = make(chan func() error, 1024)
	for range *concurrency {
		wg.Go(func() {
			for job := range work {
				if err := job(); err != nil && !apierrors.IsAlreadyExists(err) {
					failed.CompareAndSwap(nil, err)
				}
			}
		})
	}
	for i := range apps {
		work <- func() error {
			return c.Create(ctx, &geassv1alpha1.GeassApp{
				ObjectMeta: metav1.ObjectMeta{Name: appName(i), Namespace: platform.SystemNamespace},
				Spec: geassv1alpha1.GeassAppSpec{
					Project: projectName(i / *perProject), Environment: "production",
					Source: geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "nginx:1.27-alpine"}},
					Port:   8080,
					Env: []corev1.EnvVar{
						{Name: "LOG_LEVEL", Value: "info"}, {Name: "REGION", Value: "eu-north-1"},
						{Name: "FEATURE_FLAGS", Value: "a,b,c"},
					},
					Replicas: ptr.To(int32(*podsPerApp)),
					Deploy:   geassv1alpha1.GeassAppDeploySpec{Enabled: true},
				},
			})
		}
	}
	close(work)
	wg.Wait()
	if err := failed.Load(); err != nil {
		return err.(error)
	}
	fmt.Fprintf(os.Stderr, "%d services created\n", apps)
	return nil
}

func waitProjectsReady(ctx context.Context, c client.Client, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var projects geassv1alpha1.GeassProjectList
		if err := c.List(ctx, &projects, client.InNamespace(platform.SystemNamespace)); err != nil {
			return err
		}
		ready := 0
		for i := range projects.Items {
			if platform.IsConditionTrue(projects.Items[i].Status.Conditions, platform.ConditionReady) {
				ready++
			}
		}
		if ready >= want {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("not every project became ready within %s", timeout)
}

func waitReady(ctx context.Context, c client.Client, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	lastProgress, lastReady := time.Now(), 0
	for time.Now().Before(deadline) {
		var list geassv1alpha1.GeassAppList
		if err := c.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
			return err
		}
		ready := 0
		var sample string
		for i := range list.Items {
			if platform.IsConditionTrue(list.Items[i].Status.Conditions, platform.ConditionReady) {
				ready++
			} else if sample == "" {
				for _, cond := range list.Items[i].Status.Conditions {
					if cond.Type == platform.ConditionReady {
						sample = cond.Message
					}
				}
			}
		}
		fmt.Fprintf(os.Stderr, "  %d of %d services ready %s\n", ready, want, sample)
		if ready >= want {
			return nil
		}
		if ready > lastReady {
			lastReady, lastProgress = ready, time.Now()
		}
		if time.Since(lastProgress) > 4*time.Minute {
			return fmt.Errorf("stuck at %d of %d services ready: %s", ready, want, sample)
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("only %d of %d services ready within %s", lastReady, want, timeout)
}

// ---- the stand-in for kubelet and controller-manager ----

type simulator struct {
	c        client.Client
	replicas int32
	// seen is the generation of each Deployment that pods were made and status was set for.
	seen map[string]int64
	mu   sync.Mutex
}

// run watches Deployments and settles each one that is new or has changed. It reacts to what the API server reports
// instead of listing every Deployment on a timer, which at thousands of services would be most of the work the API
// server does, and would measure the program that is measuring.
func (s *simulator) run(ctx context.Context, informers ctrlcache.Cache) {
	work := make(chan *appsv1.Deployment, 8192)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for deploy := range work {
				s.settle(ctx, deploy)
			}
		})
	}
	defer func() { close(work); wg.Wait() }()

	enqueue := func(obj any) {
		deploy, ok := obj.(*appsv1.Deployment)
		if !ok {
			return
		}
		s.mu.Lock()
		done := s.seen[deploy.Namespace+"/"+deploy.Name] == deploy.Generation &&
			deploy.Status.ObservedGeneration == deploy.Generation
		s.mu.Unlock()
		if done {
			return
		}
		select {
		case work <- deploy:
		default: // full: the next resync brings it round again
		}
	}
	informer, err := informers.GetInformer(ctx, &appsv1.Deployment{})
	if err != nil {
		fatal(err)
	}
	if _, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: enqueue, UpdateFunc: func(_, obj any) { enqueue(obj) },
	}); err != nil {
		fatal(err)
	}
	if err := informers.Start(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "the stand-in's cache stopped:", err)
	}
}

// settle does what a controller-manager and a kubelet do for a Deployment: makes its pods, running, and says that it
// is available.
func (s *simulator) settle(ctx context.Context, deploy *appsv1.Deployment) {
	desired := s.replicas
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}
	for i := range desired {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-7c9d8b6f5-%05d", deploy.Name, i), Namespace: deploy.Namespace,
				Labels: deploy.Spec.Template.Labels, Annotations: deploy.Spec.Template.Annotations,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1", Kind: "ReplicaSet", Name: deploy.Name + "-7c9d8b6f5", UID: deploy.UID,
				}},
			},
			Spec: *deploy.Spec.Template.Spec.DeepCopy(),
		}
		pod.Spec.NodeName = fmt.Sprintf("node-%d", int(i)%3)
		if err := s.c.Create(ctx, pod); err != nil && !apierrors.IsAlreadyExists(err) {
			return
		}
		pod.Status = runningStatus(pod)
		_ = s.c.Status().Update(ctx, pod)
	}
	latest := &appsv1.Deployment{}
	if err := s.c.Get(ctx, client.ObjectKeyFromObject(deploy), latest); err != nil {
		return
	}
	latest.Status = appsv1.DeploymentStatus{
		ObservedGeneration: latest.Generation, Replicas: desired, UpdatedReplicas: desired,
		ReadyReplicas: desired, AvailableReplicas: desired,
		Conditions: []appsv1.DeploymentCondition{
			{
				Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue,
				Reason: "MinimumReplicasAvailable", Message: "Deployment has minimum availability.",
			},
			{
				Type: appsv1.DeploymentProgressing, Status: corev1.ConditionTrue,
				Reason: "NewReplicaSetAvailable", Message: "ReplicaSet has successfully progressed.",
			},
		},
	}
	if err := s.c.Status().Update(ctx, latest); err == nil {
		s.mu.Lock()
		s.seen[deploy.Namespace+"/"+deploy.Name] = latest.Generation
		s.mu.Unlock()
	}
}

// runningStatus is the status of a pod that has been running for a while, as large as a real one.
func runningStatus(pod *corev1.Pod) corev1.PodStatus {
	now := metav1.Now()
	statuses := make([]corev1.ContainerStatus, 0, len(pod.Spec.Containers))
	for _, container := range pod.Spec.Containers {
		statuses = append(statuses, corev1.ContainerStatus{
			Name: container.Name, Ready: true, Started: ptr.To(true), RestartCount: 0,
			Image: container.Image,
			ImageID: "docker.io/library/" + container.Image +
				"@sha256:0f1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8",
			ContainerID: "containerd://3f9c0f1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6",
			State:       corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: now}},
		})
	}
	return corev1.PodStatus{
		Phase: corev1.PodRunning, HostIP: "10.0.0.10", PodIP: "10.42.0.7", PodIPs: []corev1.PodIP{{IP: "10.42.0.7"}},
		StartTime: &now, QOSClass: corev1.PodQOSBurstable,
		Conditions: []corev1.PodCondition{
			{Type: corev1.PodScheduled, Status: corev1.ConditionTrue, LastTransitionTime: now},
			{Type: corev1.PodInitialized, Status: corev1.ConditionTrue, LastTransitionTime: now},
			{Type: corev1.ContainersReady, Status: corev1.ConditionTrue, LastTransitionTime: now},
			{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: now},
		},
		ContainerStatuses: statuses,
	}
}

// ---- the operator under test ----

type operator struct {
	cmd         *exec.Cmd
	metricsPort int
	healthPort  int
	stopped     bool
	// exited is closed when the process has ended, for any reason. The metrics of an operator that is gone read as
	// empty, which looks like an operator with nothing to do, so every wait has to look here too.
	exited chan struct{}
}

// died reports whether the operator ended on its own, which is a crash or a kill by the kernel, and not a stop().
func (o *operator) died() bool {
	select {
	case <-o.exited:
		return !o.stopped
	default:
		return false
	}
}

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func startOperator(binary, kubeconfig, label string) (*operator, error) {
	op := &operator{metricsPort: freePort(), healthPort: freePort()}
	cmd := exec.Command(binary,
		"--leader-elect=false",
		fmt.Sprintf("--metrics-bind-address=127.0.0.1:%d", op.metricsPort), "--metrics-secure=false",
		fmt.Sprintf("--health-probe-bind-address=127.0.0.1:%d", op.healthPort),
		fmt.Sprintf("--dashboard-bind-address=127.0.0.1:%d", freePort()),
		"--zap-devel=false", "--zap-log-level=error",
	)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig, "GEASS_DASHBOARD_PASSWORD=loadtest")
	if *memoryLimit != "" {
		cmd.Env = append(cmd.Env, "GOMEMLIMIT="+*memoryLimit)
	}
	var sink io.Writer = io.Discard //nolint:staticcheck // the file below is another writer, so it is named
	if *keepLogs != "" {
		if err := os.MkdirAll(*keepLogs, 0o755); err != nil {
			return nil, err
		}
		f, err := os.Create(filepath.Join(*keepLogs, fmt.Sprintf("operator-%d-%s.log", time.Now().UnixNano(), label)))
		if err != nil {
			return nil, err
		}
		sink = f
	}
	cmd.Stdout, cmd.Stderr = sink, sink
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	op.cmd = cmd
	op.exited = make(chan struct{})
	go func() { _ = cmd.Wait(); close(op.exited) }()
	return op, nil
}

func (o *operator) stop() {
	if o.stopped || o.cmd == nil || o.cmd.Process == nil {
		return
	}
	o.stopped = true
	_ = syscall.Kill(-o.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-o.exited:
	case <-time.After(20 * time.Second):
		_ = syscall.Kill(-o.cmd.Process.Pid, syscall.SIGKILL)
		<-o.exited
	}
}

func (o *operator) waitHealthy(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", o.healthPort))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("the operator was not ready within %s", timeout)
}

// metrics reads the operator's own metrics, summing the series of each name.
func (o *operator) metrics() map[string]float64 {
	out := map[string]float64{}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", o.metricsPort))
	if err != nil {
		return out
	}
	defer func() { _ = resp.Body.Close() }()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		name, remainder, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		if i := strings.IndexByte(name, '{'); i >= 0 {
			name = name[:i]
		}
		value, err := strconv.ParseFloat(strings.Fields(remainder)[0], 64)
		if err != nil {
			continue
		}
		out[name] += value
	}
	return out
}

func (o *operator) cpuSeconds() float64 { return o.metrics()["process_cpu_seconds_total"] }

// rss is the operator's resident set size right now, read from /proc, which is what a container's memory limit counts.
func (o *operator) rss() (rss, peak float64) {
	if o.cmd == nil || o.cmd.Process == nil {
		return 0, 0
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", o.cmd.Process.Pid))
	if err != nil {
		return 0, 0
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, _ := strconv.ParseFloat(fields[1], 64)
		switch fields[0] {
		case "VmRSS:":
			rss = kb * 1024
		case "VmHWM:":
			peak = kb * 1024
		}
	}
	return rss, peak
}

func (o *operator) peakRSS() float64 {
	_, peak := o.rss()
	return peak
}

// waitIdle waits until the operator has had nothing queued for the whole of quiet.
func (o *operator) waitIdle(quiet, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	idleSince := time.Time{}
	for time.Now().Before(deadline) {
		if o.died() {
			return fmt.Errorf("the operator ended on its own while it was being waited for " +
				"(a crash, or the kernel killed it for going over its memory limit): see its log")
		}
		m := o.metrics()
		// An operator that cannot be asked has nothing in its metrics, which must not read as nothing to do.
		if len(m) > 0 && m["workqueue_depth"] == 0 && m["controller_runtime_active_workers"] == 0 {
			if idleSince.IsZero() {
				idleSince = time.Now()
			}
			if time.Since(idleSince) >= quiet {
				return nil
			}
		} else {
			idleSince = time.Time{}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("the operator was still busy after %s", timeout)
}

type sampled struct {
	rssMax, heapMin, heapMax, goroutines, reconciles float64
}

// sample watches the operator for a while. The live heap is the lowest the allocated heap goes: it climbs with garbage
// until a collection and drops to what is really held, so the floor is what the operator keeps.
func (o *operator) sample(duration, every time.Duration) sampled {
	s := sampled{heapMin: math.MaxFloat64}
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		m := o.metrics()
		if heap := m["go_memstats_heap_alloc_bytes"]; heap > 0 {
			s.heapMin, s.heapMax = math.Min(s.heapMin, heap), math.Max(s.heapMax, heap)
		}
		rss, _ := o.rss()
		s.rssMax = math.Max(s.rssMax, rss)
		s.goroutines = m["go_goroutines"]
		s.reconciles = m["controller_runtime_reconcile_total"]
		time.Sleep(every)
	}
	if s.heapMin == math.MaxFloat64 {
		s.heapMin = 0
	}
	return s
}

// watchPeak follows the resident size in the background until the returned function is called, because the highest
// point of a start is a moment, and VmHWM is only kept for the life of one process.
func (o *operator) watchPeak(_ *result) func() float64 {
	stop := make(chan struct{})
	done := make(chan struct{})
	var peak float64
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(250 * time.Millisecond):
				rss, hwm := o.rss()
				peak = math.Max(peak, math.Max(rss, hwm))
			}
		}
	}()
	return func() float64 {
		select {
		case <-stop:
		default:
			close(stop)
		}
		<-done
		return peak
	}
}

// countedKinds are what the operator and Kubernetes keep for a service, and what a cluster holds besides.
var countedKinds = []schema.GroupVersionKind{
	{Version: "v1", Kind: "Namespace"}, {Version: "v1", Kind: "Pod"}, {Version: "v1", Kind: "Service"},
	{Version: "v1", Kind: "ConfigMap"}, {Version: "v1", Kind: "Secret"}, {Version: "v1", Kind: "ResourceQuota"},
	{Group: "apps", Version: "v1", Kind: "Deployment"}, {Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy"},
	{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
	{Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler"},
	{Group: "monitoring.coreos.com", Version: "v1", Kind: "ServiceMonitor"},
	{Group: "geass.geass.dev", Version: "v1alpha1", Kind: "GeassProject"},
	{Group: "geass.geass.dev", Version: "v1alpha1", Kind: "GeassApp"},
	{Group: "geass.geass.dev", Version: "v1alpha1", Kind: "GeassDeployment"},
}

// storageObjects counts what the API server holds, by kind, and reads how big its storage is. A list of one item
// says how many more there are, which is exact and needs no waiting for a metric to catch up.
func storageObjects(
	ctx context.Context, c client.Client, kube kubernetes.Interface, skipSize bool,
) (map[string]int, float64) {
	objects := map[string]int{}
	for _, gvk := range countedKinds {
		list := &metav1.PartialObjectMetadataList{}
		list.SetGroupVersionKind(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
		if err := c.List(ctx, list, client.Limit(1)); err != nil {
			continue
		}
		count := len(list.Items)
		if list.RemainingItemCount != nil {
			count += int(*list.RemainingItemCount)
		}
		if count > 0 {
			objects[strings.ToLower(gvk.Kind)] = count
		}
	}
	var size float64
	if skipSize {
		return objects, size
	}
	raw, err := kube.CoreV1().RESTClient().(*rest.RESTClient).Get().AbsPath("/metrics").DoRaw(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not read the API server's metrics:", err)
		return objects, size
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.HasPrefix(line, "apiserver_storage_size_bytes{") ||
			strings.HasPrefix(line, "etcd_db_total_size_in_bytes") {
			fields := strings.Fields(line)
			value, _ := strconv.ParseFloat(fields[len(fields)-1], 64)
			size = math.Max(size, value)
		}
	}
	return objects, size
}
