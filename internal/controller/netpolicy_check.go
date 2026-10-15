package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/netcheck"
	"github.com/degoke/geass/pkg/platform"
)

const (
	netcheckNamespace  = "geass-netcheck"
	netcheckPolicyName = "deny-everything"
	netcheckLabel      = "geass.dev/netcheck"
	// operatorDeploymentName is the Deployment the installer makes for the operator, which is where the image the
	// check runs is read from.
	operatorDeploymentName = "geass-controller-manager"
	operatorContainerName  = "manager"

	// netcheckEvery is how often a cluster is checked again. A network plugin changes when someone changes it, which
	// is rare, so this is only a safety net, and the check can be asked for at any time.
	netcheckEvery = 24 * time.Hour
	// netcheckRetryAfter is how soon a check that could not tell is tried again.
	netcheckRetryAfter = 30 * time.Minute
	// netcheckPoll is how often the checker looks for a cluster that is due.
	netcheckPoll = time.Minute
	// netcheckSettle is how long the checker waits after it starts before it checks anything, so that the cluster
	// has settled and a freshly installed one is not checked while its pods are still arriving.
	netcheckSettle = 90 * time.Second
	// netcheckTimeout bounds one check from start to finish.
	netcheckTimeout = 4 * time.Minute
)

// netcheckStep is how often the check looks at the pods it is waiting for. A variable so that tests need not wait.
var netcheckStep = 2 * time.Second

// NetworkPolicyChecker finds out whether the cluster's network plugin acts on network policies, and says so on the
// GeassCluster. Isolation between projects, the block on the cluster's machines and the protection of the metrics
// and log stores are all network policies. A plugin that does not enforce them accepts every one without complaint,
// so without this a cluster can look isolated and not be.
//
// It runs in the background and never holds anything back: a check that fails leaves the condition Unknown.
type NetworkPolicyChecker struct {
	Client client.Client
	// Reader reads what the cache does not keep. The cache leaves out pods that finished, and the probe's
	// answer is on one.
	Reader client.Reader
	// Every, Poll and Settle override how often a cluster is checked, how often the checker looks for one that
	// is due, and how long it waits after starting. Zero means the defaults.
	Every, Poll, Settle time.Duration
	// Probe runs the check against the cluster. Nil runs the real one, which needs pods.
	Probe func(ctx context.Context) (netcheck.Result, error)
	// Now is the time, for tests.
	Now func() time.Time
}

// NeedLeaderElection makes only one operator run the check.
func (c *NetworkPolicyChecker) NeedLeaderElection() bool { return true }

// Start runs until the context ends.
func (c *NetworkPolicyChecker) Start(ctx context.Context) error {
	select {
	case <-time.After(orDefault(c.Settle, netcheckSettle)):
	case <-ctx.Done():
		return nil
	}
	ticker := time.NewTicker(orDefault(c.Poll, netcheckPoll))
	defer ticker.Stop()
	for {
		c.CheckDue(ctx)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil
		}
	}
}

func orDefault(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func (c *NetworkPolicyChecker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// CheckDue checks every cluster that is due for it.
func (c *NetworkPolicyChecker) CheckDue(ctx context.Context) {
	var clusters geassv1alpha1.GeassClusterList
	if err := c.Client.List(ctx, &clusters, client.InNamespace(platform.SystemNamespace)); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list clusters for the network policy check")
		return
	}
	for i := range clusters.Items {
		cluster := &clusters.Items[i]
		if !c.due(cluster) {
			continue
		}
		c.Check(ctx, cluster)
	}
}

// due says whether a cluster should be checked now: it is ready, and has never been checked, or was asked to be
// checked again, or was last checked longer ago than it should be.
func (c *NetworkPolicyChecker) due(cluster *geassv1alpha1.GeassCluster) bool {
	if !cluster.DeletionTimestamp.IsZero() || !platform.IsConditionTrue(cluster.Status.Conditions, platform.ConditionReady) {
		return false
	}
	if _, asked := cluster.Annotations[platform.AnnotationRecheckNetworkPolicy]; asked {
		return true
	}
	condition := meta.FindStatusCondition(cluster.Status.Conditions, platform.ConditionNetworkPolicyEnforced)
	if condition == nil {
		return true
	}
	checked, err := time.Parse(time.RFC3339, cluster.Annotations[platform.AnnotationNetworkPolicyChecked])
	if err != nil {
		return true
	}
	wait := netcheckEvery
	if condition.Status == metav1.ConditionUnknown {
		wait = netcheckRetryAfter
	}
	if c.Every > 0 {
		wait = c.Every
	}
	return c.now().Sub(checked) >= wait
}

// Check runs the check once and records what it found on the cluster.
func (c *NetworkPolicyChecker) Check(ctx context.Context, cluster *geassv1alpha1.GeassCluster) {
	log := logf.FromContext(ctx)
	probe := c.Probe
	if probe == nil {
		probe = c.probe
	}
	checkCtx, cancel := context.WithTimeout(ctx, netcheckTimeout)
	defer cancel()
	result, err := probe(checkCtx)
	status, reason, message := networkPolicyCondition(result, err)
	log.Info("Checked whether network policies are enforced", "cluster", cluster.Name, "status", status, "reason", reason)

	latest := &geassv1alpha1.GeassCluster{}
	if err := c.Client.Get(ctx, client.ObjectKeyFromObject(cluster), latest); err != nil {
		return
	}
	if _, err := platform.PatchStatus(ctx, c.Client, latest, func() {
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionNetworkPolicyEnforced, status, reason, message)
	}); err != nil {
		log.Error(err, "Failed to record the network policy check", "cluster", cluster.Name)
		return
	}
	base := latest.DeepCopy()
	if latest.Annotations == nil {
		latest.Annotations = map[string]string{}
	}
	latest.Annotations[platform.AnnotationNetworkPolicyChecked] = c.now().UTC().Format(time.RFC3339)
	delete(latest.Annotations, platform.AnnotationRecheckNetworkPolicy)
	if err := c.Client.Patch(ctx, latest, client.MergeFrom(base)); err != nil {
		log.Error(err, "Failed to note when the network policy check ran", "cluster", cluster.Name)
	}
}

// networkPolicyCondition says what a result means for the cluster's condition.
func networkPolicyCondition(result netcheck.Result, err error) (metav1.ConditionStatus, string, string) {
	switch {
	case err != nil:
		return metav1.ConditionUnknown, "CheckFailed", "Geass could not check whether network policies are enforced: " + err.Error() + ". It tries again later, and on request."
	case !result.Known():
		return metav1.ConditionUnknown, "ProbeFailed", "Geass could not check whether network policies are enforced: a pod that no policy covers could not be reached from another pod, so the pod network itself may be at fault. It tries again later, and on request."
	case result.Enforced():
		return metav1.ConditionTrue, "Enforced", "Network policies are enforced: a pod that a policy closes to everyone refused a connection from another pod."
	default:
		return metav1.ConditionFalse, "NotEnforced", "Network policies are NOT enforced: a pod that a policy closes to everyone still accepted a connection. " +
			"Project isolation, the block on the cluster's machines and the protection of the metrics and log stores are written, and nothing acts on them. " +
			"K3s enforces policies unless it was started with --disable-network-policy. On another network plugin, use one that enforces them."
	}
}

// probe runs the check in the cluster: a namespace with two pods that listen and a policy that closes one of them
// to everyone, and a third pod that tries both. The pods run the operator's own image, which every cluster that
// installed Geass has, and which can be run on the control plane, where the installer put it.
func (c *NetworkPolicyChecker) probe(ctx context.Context) (netcheck.Result, error) {
	reader := c.Reader
	if reader == nil {
		reader = c.Client
	}
	image, err := operatorImage(ctx, reader)
	if err != nil {
		return netcheck.Result{}, err
	}
	if err := c.resetNetcheckNamespace(ctx, reader); err != nil {
		return netcheck.Result{}, err
	}
	defer func() {
		// The check's own context may be spent by now, which must not leave its pods behind.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = client.IgnoreNotFound(c.Client.Delete(cleanup, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: netcheckNamespace}}))
	}()

	if err := c.createWithRetry(ctx, netcheckPolicy()); err != nil {
		return netcheck.Result{}, fmt.Errorf("create the policy: %w", err)
	}
	servers := map[string]string{}
	for _, role := range []string{"open", "closed"} {
		pod := netcheckPod("server-"+role, image, role, "netcheck", "serve", "--listen", fmt.Sprintf(":%d", netcheck.Port))
		if err := c.createWithRetry(ctx, pod); err != nil {
			return netcheck.Result{}, fmt.Errorf("start the %s server: %w", role, err)
		}
		servers[role] = pod.Name
	}
	addresses := map[string]string{}
	for role, name := range servers {
		ip, err := waitForPodIP(ctx, reader, name)
		if err != nil {
			return netcheck.Result{}, fmt.Errorf("the %s server did not start: %w", role, err)
		}
		addresses[role] = fmt.Sprintf("%s:%d", ip, netcheck.Port)
	}
	probe := netcheckPod("probe", image, "probe", "netcheck", "probe", "--open", addresses["open"], "--closed", addresses["closed"])
	probe.Spec.RestartPolicy = corev1.RestartPolicyNever
	if err := c.createWithRetry(ctx, probe); err != nil {
		return netcheck.Result{}, fmt.Errorf("start the probe: %w", err)
	}
	return waitForProbe(ctx, reader, probe.Name)
}

// operatorImage is the image the operator itself runs, read from its Deployment.
func operatorImage(ctx context.Context, reader client.Reader) (string, error) {
	var deploy appsv1.Deployment
	if err := reader.Get(ctx, client.ObjectKey{Name: operatorDeploymentName, Namespace: platform.SystemNamespace}, &deploy); err != nil {
		return "", fmt.Errorf("could not find the operator's own Deployment to take its image from: %w", err)
	}
	for _, container := range deploy.Spec.Template.Spec.Containers {
		if container.Name == operatorContainerName && container.Image != "" {
			return container.Image, nil
		}
	}
	return "", errors.New("the operator's Deployment has no manager container to take an image from")
}

// resetNetcheckNamespace makes sure the check starts from nothing: a namespace left by a check that was cut short
// is removed first.
func (c *NetworkPolicyChecker) resetNetcheckNamespace(ctx context.Context, reader client.Reader) error {
	var existing corev1.Namespace
	switch err := reader.Get(ctx, client.ObjectKey{Name: netcheckNamespace}, &existing); {
	case err == nil:
		if err := client.IgnoreNotFound(c.Client.Delete(ctx, &existing)); err != nil {
			return err
		}
		if err := pollUntil(ctx, 90*time.Second, func() (bool, error) {
			err := reader.Get(ctx, client.ObjectKey{Name: netcheckNamespace}, &corev1.Namespace{})
			return apierrors.IsNotFound(err), client.IgnoreNotFound(err)
		}); err != nil {
			return fmt.Errorf("an earlier check's namespace did not go away: %w", err)
		}
	case !apierrors.IsNotFound(err):
		return err
	}
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: netcheckNamespace, Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}}}
	return c.Client.Create(ctx, namespace)
}

// createWithRetry creates an object in the new namespace, which can refuse it for a moment: a pod needs the
// namespace's default service account, and that is made a little after the namespace.
func (c *NetworkPolicyChecker) createWithRetry(ctx context.Context, obj client.Object) error {
	return pollUntil(ctx, 45*time.Second, func() (bool, error) {
		err := c.Client.Create(ctx, obj)
		if err == nil || apierrors.IsAlreadyExists(err) {
			return true, nil
		}
		if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	})
}

func pollUntil(ctx context.Context, timeout time.Duration, done func() (bool, error)) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		finished, err := done()
		if err != nil {
			return err
		}
		if finished {
			return nil
		}
		select {
		case <-time.After(netcheckStep):
		case <-deadline.C:
			return errors.New("timed out")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func waitForPodIP(ctx context.Context, reader client.Reader, name string) (string, error) {
	var ip string
	err := pollUntil(ctx, 2*time.Minute, func() (bool, error) {
		var pod corev1.Pod
		if err := reader.Get(ctx, client.ObjectKey{Name: name, Namespace: netcheckNamespace}, &pod); err != nil {
			return false, client.IgnoreNotFound(err)
		}
		if pod.Status.Phase == corev1.PodFailed {
			return false, errors.New("the pod failed: " + podFailure(&pod))
		}
		// Ready, and not only running: the answer rests on a server that refuses the probe because a policy says so, and a
		// server that has not started listening refuses it too. Kubelet's own check of the port is how it is known to
		// be listening, because it reaches a pod whatever the policies say, and the probe cannot reach the closed one.
		if pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "" && podReady(&pod) {
			ip = pod.Status.PodIP
			return true, nil
		}
		return false, nil
	})
	return ip, err
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// waitForProbe waits for the probe pod to finish and reads what it found from the pod's termination message.
func waitForProbe(ctx context.Context, reader client.Reader, name string) (netcheck.Result, error) {
	var result netcheck.Result
	err := pollUntil(ctx, 3*time.Minute, func() (bool, error) {
		var pod corev1.Pod
		if err := reader.Get(ctx, client.ObjectKey{Name: name, Namespace: netcheckNamespace}, &pod); err != nil {
			return false, client.IgnoreNotFound(err)
		}
		if pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
			return false, nil
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.State.Terminated == nil {
				continue
			}
			parsed, err := netcheck.Parse(status.State.Terminated.Message)
			if err != nil {
				return false, fmt.Errorf("the probe ended without a result (%s): %w", podFailure(&pod), err)
			}
			result = parsed
			return true, nil
		}
		return false, errors.New("the probe ended without a result: " + podFailure(&pod))
	})
	return result, err
}

func podFailure(pod *corev1.Pod) string {
	for _, status := range pod.Status.ContainerStatuses {
		if terminated := status.State.Terminated; terminated != nil {
			return fmt.Sprintf("%s, exit code %d", terminated.Reason, terminated.ExitCode)
		}
		if waiting := status.State.Waiting; waiting != nil {
			return waiting.Reason + " " + waiting.Message
		}
	}
	return string(pod.Status.Phase)
}

// netcheckPolicy closes every pod that is labelled closed to everything that tries to reach it: a policy that
// selects a pod and has no rule lets nothing in.
func netcheckPolicy() *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: netcheckPolicyName, Namespace: netcheckNamespace},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{netcheckLabel: "closed"}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		},
	}
}

// netcheckPod is one of the check's pods: the operator's image running one of its own commands, on the control
// plane where that image is, with nothing it does not need.
func netcheckPod(name, image, role string, args ...string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: netcheckNamespace,
			Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue, netcheckLabel: role},
		},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken: utilptr.To(false),
			Affinity: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{{
					Key: "node-role.kubernetes.io/control-plane", Operator: corev1.NodeSelectorOpExists,
				}}}},
			}}},
			Tolerations: []corev1.Toleration{{Key: "node-role.kubernetes.io/control-plane", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}},
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:   utilptr.To(true),
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			Containers: []corev1.Container{{
				Name:            "netcheck",
				Image:           image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         []string{"/manager"},
				Args:            args,
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: utilptr.To(false),
					ReadOnlyRootFilesystem:   utilptr.To(true),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("16Mi")},
					Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")},
				},
			}},
		},
	}
	if role != "probe" {
		// The servers say when they are listening, for waitForPodIP.
		pod.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
			ProbeHandler:  corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(netcheck.Port)}},
			PeriodSeconds: 1,
		}
	}
	return pod
}
