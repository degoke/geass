package controller

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/netcheck"
	"github.com/degoke/geass/pkg/platform"
)

func netcheckClient(objects ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = geassv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = networkingv1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&geassv1alpha1.GeassCluster{}).WithObjects(objects...).Build()
}

func readyCluster(annotations map[string]string, conditions ...metav1.Condition) *geassv1alpha1.GeassCluster {
	return &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace, Annotations: annotations},
		Status: geassv1alpha1.GeassClusterStatus{Conditions: append(
			[]metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}, conditions...)},
	}
}

func netpolicyCondition(status metav1.ConditionStatus) metav1.Condition {
	return metav1.Condition{Type: platform.ConditionNetworkPolicyEnforced, Status: status}
}

func TestAClusterIsCheckedWhenItIsReadyAndHasNeverBeenChecked(t *testing.T) {
	checker := &NetworkPolicyChecker{}
	require.True(t, checker.due(readyCluster(nil)))

	notReady := readyCluster(nil)
	notReady.Status.Conditions = nil
	require.False(t, checker.due(notReady), "a cluster that is still being set up has no pods to check with yet")

	deleting := readyCluster(nil)
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	require.False(t, checker.due(deleting))
}

func TestAClusterIsCheckedAgainWhenAskedOrWhenTheAnswerIsOld(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	checker := &NetworkPolicyChecker{Now: func() time.Time { return now }}
	checkedAt := func(age time.Duration) map[string]string {
		return map[string]string{platform.AnnotationNetworkPolicyChecked: now.Add(-age).Format(time.RFC3339)}
	}

	require.False(t, checker.due(readyCluster(checkedAt(time.Hour), netpolicyCondition(metav1.ConditionTrue))), "an hour ago is recent enough")
	require.True(t, checker.due(readyCluster(checkedAt(25*time.Hour), netpolicyCondition(metav1.ConditionTrue))), "a day is long enough")
	require.True(t, checker.due(readyCluster(checkedAt(25*time.Hour), netpolicyCondition(metav1.ConditionFalse))))

	require.False(t, checker.due(readyCluster(checkedAt(10*time.Minute), netpolicyCondition(metav1.ConditionUnknown))), "a check that could not tell is not hammered")
	require.True(t, checker.due(readyCluster(checkedAt(time.Hour), netpolicyCondition(metav1.ConditionUnknown))), "but it is tried again well before a day")

	asked := checkedAt(time.Minute)
	asked[platform.AnnotationRecheckNetworkPolicy] = "now"
	require.True(t, checker.due(readyCluster(asked, netpolicyCondition(metav1.ConditionTrue))), "asking for a check is enough")

	require.True(t, checker.due(readyCluster(map[string]string{platform.AnnotationNetworkPolicyChecked: "garbage"}, netpolicyCondition(metav1.ConditionTrue))),
		"a time that cannot be read is no reason to never check")
	require.True(t, checker.due(readyCluster(nil, netpolicyCondition(metav1.ConditionTrue))), "a condition with no time is checked again to have one")
}

func TestWhatTheProbeFoundBecomesTheCondition(t *testing.T) {
	status, reason, message := networkPolicyCondition(netcheck.Result{Open: true}, nil)
	require.Equal(t, metav1.ConditionTrue, status)
	require.Equal(t, "Enforced", reason)
	require.Contains(t, message, "refused")

	status, reason, message = networkPolicyCondition(netcheck.Result{Open: true, Closed: true}, nil)
	require.Equal(t, metav1.ConditionFalse, status)
	require.Equal(t, "NotEnforced", reason)
	require.Contains(t, message, "NOT enforced")
	require.Contains(t, message, "--disable-network-policy", "it says what to look at")

	status, reason, _ = networkPolicyCondition(netcheck.Result{}, nil)
	require.Equal(t, metav1.ConditionUnknown, status, "a network that reached nothing proves nothing about a policy")
	require.Equal(t, "ProbeFailed", reason)

	status, reason, message = networkPolicyCondition(netcheck.Result{}, errors.New("no image"))
	require.Equal(t, metav1.ConditionUnknown, status)
	require.Equal(t, "CheckFailed", reason)
	require.Contains(t, message, "no image")
}

func TestACheckRecordsWhatItFoundAndWhenAndForgetsTheRequest(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	cluster := readyCluster(map[string]string{platform.AnnotationRecheckNetworkPolicy: "now"})
	c := netcheckClient(cluster)
	checker := &NetworkPolicyChecker{
		Client: c, Now: func() time.Time { return now },
		Probe: func(context.Context) (netcheck.Result, error) { return netcheck.Result{Open: true, Closed: true}, nil },
	}

	checker.CheckDue(context.Background())

	var latest geassv1alpha1.GeassCluster
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(cluster), &latest))
	condition := meta.FindStatusCondition(latest.Status.Conditions, platform.ConditionNetworkPolicyEnforced)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, now.Format(time.RFC3339), latest.Annotations[platform.AnnotationNetworkPolicyChecked])
	require.NotContains(t, latest.Annotations, platform.AnnotationRecheckNetworkPolicy, "the request is answered, so it is not asked again")
	require.True(t, platform.IsConditionTrue(latest.Status.Conditions, platform.ConditionReady), "the cluster's other conditions are left alone")

	require.False(t, checker.due(&latest), "and it is not due again straight away")
}

func TestACheckThatCannotRunLeavesTheClusterUnknownAndNotReadyIsNotAffected(t *testing.T) {
	cluster := readyCluster(nil)
	c := netcheckClient(cluster)
	checker := &NetworkPolicyChecker{
		Client: c, Probe: func(context.Context) (netcheck.Result, error) { return netcheck.Result{}, errors.New("boom") },
	}

	checker.CheckDue(context.Background())

	var latest geassv1alpha1.GeassCluster
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(cluster), &latest))
	require.Equal(t, metav1.ConditionUnknown, meta.FindStatusCondition(latest.Status.Conditions, platform.ConditionNetworkPolicyEnforced).Status)
	require.True(t, platform.IsConditionTrue(latest.Status.Conditions, platform.ConditionReady), "a check that fails must never hold the cluster back")
}

func TestAClusterThatIsNotReadyIsLeftAlone(t *testing.T) {
	cluster := readyCluster(nil)
	cluster.Status.Conditions = nil
	c := netcheckClient(cluster)
	called := false
	checker := &NetworkPolicyChecker{Client: c, Probe: func(context.Context) (netcheck.Result, error) {
		called = true
		return netcheck.Result{}, nil
	}}

	checker.CheckDue(context.Background())

	require.False(t, called)
}

func TestThePolicyClosesOnlyTheClosedPodsToEverything(t *testing.T) {
	policy := netcheckPolicy()

	require.Equal(t, map[string]string{netcheckLabel: "closed"}, policy.Spec.PodSelector.MatchLabels, "the open server must stay uncovered: it is the control")
	require.Equal(t, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}, policy.Spec.PolicyTypes)
	require.Empty(t, policy.Spec.Ingress, "a policy that selects a pod and lists no rule lets nothing in")
}

func TestTheChecksPodsRunTheOperatorsImageOnTheControlPlaneWithNothingTheyDoNotNeed(t *testing.T) {
	pod := netcheckPod("server-open", "geass-operator:v1", "open", true, "netcheck", "serve")

	require.Equal(t, netcheckNamespace, pod.Namespace)
	require.Equal(t, "open", pod.Labels[netcheckLabel])
	container := pod.Spec.Containers[0]
	require.Equal(t, "geass-operator:v1", container.Image)
	require.Equal(t, corev1.PullIfNotPresent, container.ImagePullPolicy, "the image is imported on the node and is in no registry")
	require.Equal(t, []string{"/manager"}, container.Command)
	require.Equal(t, []string{"netcheck", "serve"}, container.Args)
	require.False(t, *pod.Spec.AutomountServiceAccountToken)
	require.True(t, *container.SecurityContext.ReadOnlyRootFilesystem)
	require.False(t, *container.SecurityContext.AllowPrivilegeEscalation)
	require.Equal(t, corev1.Capability("ALL"), container.SecurityContext.Capabilities.Drop[0])
	terms := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	require.Equal(t, "node-role.kubernetes.io/control-plane", terms[0].MatchExpressions[0].Key, "where the operator's image is")
}

func operatorDeployment(image string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: operatorDeploymentName, Namespace: platform.SystemNamespace},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "sidecar", Image: "other:1"}, {Name: operatorContainerName, Image: image},
		}}}},
	}
}

func TestTheCheckTakesItsImageFromTheOperatorsOwnDeployment(t *testing.T) {
	image, err := operatorImage(context.Background(), netcheckClient(operatorDeployment("geass-operator:v0.2.0")))
	require.NoError(t, err)
	require.Equal(t, "geass-operator:v0.2.0", image)

	_, err = operatorImage(context.Background(), netcheckClient())
	require.ErrorContains(t, err, "could not find the operator's own Deployment")
}

// simulateCluster stands in for a kubelet and a network plugin: it brings the check's pods up, and when the probe pod
// appears it ends it with what a plugin that does or does not enforce policies would have made it find.
func simulateCluster(t *testing.T, c client.Client, enforced bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for ctx.Err() == nil {
			var pods corev1.PodList
			if err := c.List(ctx, &pods, client.InNamespace(netcheckNamespace)); err == nil {
				for i := range pods.Items {
					pod := &pods.Items[i]
					switch {
					case pod.Name == "probe" && pod.Status.Phase != corev1.PodSucceeded:
						result := netcheck.Result{Open: true, Closed: !enforced}
						pod.Status.Phase = corev1.PodSucceeded
						pod.Status.ContainerStatuses = []corev1.ContainerStatus{{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Message: result.String()}}}}
						_ = c.Status().Update(ctx, pod)
					case pod.Name != "probe" && pod.Status.PodIP == "":
						pod.Status.Phase = corev1.PodRunning
						pod.Status.PodIP = "10.42.0." + string(rune('1'+len(pod.Name)%8))
						pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
						_ = c.Status().Update(ctx, pod)
					}
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

func TestTheRealCheckPutsAPolicyOnOneServerAndAsksAPodToTryBoth(t *testing.T) {
	previous := netcheckStep
	netcheckStep = 10 * time.Millisecond
	t.Cleanup(func() { netcheckStep = previous })

	for name, enforced := range map[string]bool{"enforced": true, "not enforced": false} {
		t.Run(name, func(t *testing.T) {
			c := netcheckClient(operatorDeployment("geass-operator:v0.2.0"))
			simulateCluster(t, c, enforced)
			checker := &NetworkPolicyChecker{Client: c}

			result, err := checker.probe(context.Background())

			require.NoError(t, err)
			require.True(t, result.Known())
			require.Equal(t, enforced, result.Enforced())
			err = c.Get(context.Background(), client.ObjectKey{Name: netcheckNamespace}, &corev1.Namespace{})
			require.True(t, apierrors.IsNotFound(err), "the check removes what it made: %v", err)
		})
	}
}

func TestTheCheckReportsAProbeThatEndedWithoutAnAnswer(t *testing.T) {
	previous := netcheckStep
	netcheckStep = 10 * time.Millisecond
	t.Cleanup(func() { netcheckStep = previous })
	c := netcheckClient(operatorDeployment("geass-operator:v0.2.0"))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for ctx.Err() == nil {
			var pods corev1.PodList
			if err := c.List(ctx, &pods, client.InNamespace(netcheckNamespace)); err == nil {
				for i := range pods.Items {
					pod := &pods.Items[i]
					if pod.Name == "probe" {
						pod.Status.Phase = corev1.PodFailed
						pod.Status.ContainerStatuses = []corev1.ContainerStatus{{State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}}}}
						_ = c.Status().Update(ctx, pod)
					} else if pod.Status.PodIP == "" {
						pod.Status.Phase, pod.Status.PodIP = corev1.PodRunning, "10.42.0.9"
						pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
						_ = c.Status().Update(ctx, pod)
					}
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	_, err := (&NetworkPolicyChecker{Client: c}).probe(context.Background())

	require.ErrorContains(t, err, "OOMKilled")
}

func TestACheckWithNoOperatorDeploymentSaysSoInsteadOfGuessing(t *testing.T) {
	_, err := (&NetworkPolicyChecker{Client: netcheckClient()}).probe(context.Background())
	require.ErrorContains(t, err, "operator's own Deployment")
}

// A server that is running and has an address may not be listening yet, and a connection to one that is not is refused
// just as a connection a policy forbids is. Reading that refusal as the policy at work would say a cluster enforces
// policies that does not, so the probe waits for the servers to say they are listening.
func TestTheProbeWaitsForTheServersToBeListeningNotJustRunning(t *testing.T) {
	previous := netcheckStep
	netcheckStep = 10 * time.Millisecond
	t.Cleanup(func() { netcheckStep = previous })
	c := netcheckClient(operatorDeployment("geass-operator:v0.2.0"))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var probeStarted atomic.Bool
	go func() {
		for ctx.Err() == nil {
			var pods corev1.PodList
			if err := c.List(ctx, &pods, client.InNamespace(netcheckNamespace)); err == nil {
				for i := range pods.Items {
					pod := &pods.Items[i]
					if pod.Name == "probe" {
						probeStarted.Store(true)
					} else if pod.Status.PodIP == "" {
						// Running, with an address, and not ready.
						pod.Status.Phase, pod.Status.PodIP = corev1.PodRunning, "10.42.0.9"
						_ = c.Status().Update(ctx, pod)
					}
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	waiting, stop := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer stop()
	_, err := (&NetworkPolicyChecker{Client: c}).probe(waiting)

	require.ErrorContains(t, err, "did not start")
	require.False(t, probeStarted.Load(), "the probe was not started while a server was not ready")
}

func TestOnlyTheServersAreGivenAReadinessProbe(t *testing.T) {
	require.NotNil(t, netcheckPod("server-closed", "img", "closed", true).Spec.Containers[0].ReadinessProbe)
	require.NotNil(t, netcheckPod("server-open", "img", "open", true).Spec.Containers[0].ReadinessProbe)
	require.Nil(t, netcheckPod("probe", "img", "probe", true).Spec.Containers[0].ReadinessProbe, "a pod that is meant to end is not waited for to be ready")
}
