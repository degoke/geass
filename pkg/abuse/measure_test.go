package abuse

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

const testNamespace = "shop-production"

// deployment is a settled Deployment of the service web that asks for the copies.
func deployment(copies int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: testNamespace, Generation: 3},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(copies)},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 3, Replicas: copies, UpdatedReplicas: copies, ReadyReplicas: copies, AvailableReplicas: copies},
	}
}

// copyOf is a ready pod of web that started at the time, with one container limited to the CPU.
func copyOf(name string, started time.Time, cpuLimit string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace, UID: types.UID("uid-" + name)},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
			Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpuLimit)},
		}}}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			StartTime:  &metav1.Time{Time: started},
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "web", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.Time{Time: started}}},
			}},
		},
	}
}

func usageOf(pairs map[string]int64) Usage {
	usage := Usage{}
	for name, milli := range pairs {
		usage[types.NamespacedName{Namespace: testNamespace, Name: name}] = PodUse{CPUMilli: milli}
	}
	return usage
}

func TestASettledServiceIsMeasuredAgainstItsLimits(t *testing.T) {
	now := start.Add(time.Hour)
	pods := []corev1.Pod{copyOf("web-1", start, "500m"), copyOf("web-2", start, "500m")}

	sample := Measure(now, deployment(2), pods, usageOf(map[string]int64{"web-1": 480, "web-2": 470}))

	require.True(t, sample.Measured, sample.Why)
	require.Equal(t, int64(950), sample.UsedMilli)
	require.Equal(t, int64(1000), sample.AllowedMilli, "the limits, not the requests")
	require.Equal(t, int64(950), sample.Permille())
	require.NotEmpty(t, sample.Pods)

	again := Measure(now.Add(time.Minute), deployment(2), []corev1.Pod{pods[1], pods[0]}, usageOf(map[string]int64{"web-1": 1, "web-2": 1}))
	require.Equal(t, sample.Pods, again.Pods, "the same copies, in any order, are the same run")
}

func TestARequestIsTheShareWhenThereIsNoLimitAndNeitherIsNoShare(t *testing.T) {
	now := start.Add(time.Hour)
	pod := copyOf("web-1", start, "1")
	pod.Spec.Containers[0].Resources.Limits = nil
	sample := Measure(now, deployment(1), []corev1.Pod{pod}, usageOf(map[string]int64{"web-1": 95}))
	require.True(t, sample.Measured)
	require.Equal(t, int64(100), sample.AllowedMilli)
	require.Equal(t, int64(950), sample.Permille())

	pod.Spec.Containers[0].Resources.Requests = nil
	sample = Measure(now, deployment(1), []corev1.Pod{pod}, usageOf(map[string]int64{"web-1": 95}))
	require.False(t, sample.Measured)
	require.Equal(t, whyUnbounded, sample.Why)
}

func TestASidecarCountsInWhatThePodIsAllowed(t *testing.T) {
	pod := copyOf("web-1", start, "1")
	pod.Spec.InitContainers = []corev1.Container{
		{Name: "setup", Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")}}},
		{Name: "proxy", RestartPolicy: ptr.To(corev1.ContainerRestartPolicyAlways), Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m")}}},
	}
	sample := Measure(start.Add(time.Hour), deployment(1), []corev1.Pod{pod}, usageOf(map[string]int64{"web-1": 1000}))
	require.True(t, sample.Measured)
	require.Equal(t, int64(1250), sample.AllowedMilli, "the sidecar runs beside the service; the init container that finished does not")
}

func TestAServiceThatIsNotSettledIsNotMeasured(t *testing.T) {
	now := start.Add(time.Hour)
	use := usageOf(map[string]int64{"web-1": 1000, "web-2": 1000, "web-3": 1000})
	settledPods := func() []corev1.Pod {
		return []corev1.Pod{copyOf("web-1", start, "1"), copyOf("web-2", start, "1")}
	}
	cases := map[string]struct {
		deploy *appsv1.Deployment
		pods   func() []corev1.Pod
		why    string
	}{
		"no Deployment": {nil, settledPods, whyNoDeployment},
		"scaled to zero, by a person, by scale to zero or by a suspension": {
			func() *appsv1.Deployment { d := deployment(0); return d }(), func() []corev1.Pod { return nil }, whyNoCopies,
		},
		"a rollout the Deployment controller has not seen yet": {
			func() *appsv1.Deployment { d := deployment(2); d.Generation = 4; return d }(), settledPods, whyChanging,
		},
		"a rollout half done": {
			func() *appsv1.Deployment { d := deployment(2); d.Status.UpdatedReplicas = 1; return d }(), settledPods, whyChanging,
		},
		"scaling up": {
			func() *appsv1.Deployment { d := deployment(3); d.Status.ReadyReplicas = 2; return d }(), settledPods, whyChanging,
		},
		"a copy still going after scaling down": {
			deployment(1), settledPods, whyChanging,
		},
		"a copy that is not ready": {
			deployment(2), func() []corev1.Pod {
				pods := settledPods()
				pods[1].Status.Conditions[0].Status = corev1.ConditionFalse
				return pods
			}, whySettling,
		},
		"a copy that started two minutes ago": {
			deployment(2), func() []corev1.Pod {
				return []corev1.Pod{copyOf("web-1", start, "1"), copyOf("web-3", now.Add(-2*time.Minute), "1")}
			}, whySettling,
		},
		"a container restarted a minute ago": {
			deployment(2), func() []corev1.Pod {
				pods := settledPods()
				pods[0].Status.ContainerStatuses[0].RestartCount = 1
				pods[0].Status.ContainerStatuses[0].State.Running.StartedAt = metav1.Time{Time: now.Add(-time.Minute)}
				return pods
			}, whySettling,
		},
		"a container that is waiting": {
			deployment(2), func() []corev1.Pod {
				pods := settledPods()
				pods[0].Status.ContainerStatuses[0].State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}
				return pods
			}, whySettling,
		},
		"a use the metrics server has not reported": {
			deployment(2), func() []corev1.Pod { return []corev1.Pod{copyOf("web-1", start, "1"), copyOf("web-9", start, "1")} }, whyNoUse,
		},
	}
	for name, tc := range cases {
		sample := Measure(now, tc.deploy, tc.pods(), use)
		require.False(t, sample.Measured, name)
		require.Equal(t, tc.why, sample.Why, name)
	}
}

func TestARestartChangesTheCopiesOnceItHasSettled(t *testing.T) {
	now := start.Add(time.Hour)
	pods := []corev1.Pod{copyOf("web-1", start, "1")}
	before := Measure(now, deployment(1), pods, usageOf(map[string]int64{"web-1": 990}))
	pods[0].Status.ContainerStatuses[0].RestartCount = 1
	pods[0].Status.ContainerStatuses[0].State.Running.StartedAt = metav1.Time{Time: now.Add(-SettleTime)}
	after := Measure(now, deployment(1), pods, usageOf(map[string]int64{"web-1": 990}))
	require.True(t, before.Measured)
	require.True(t, after.Measured, "settled again after three minutes")
	require.NotEqual(t, before.Pods, after.Pods, "a restarted copy is a new run")
}

// timeline runs a service through minutes of samples, a minute apart, with the copies and use each minute gives, and says
// at which minute it was first suspected, or -1.
func timeline(minutes int, at func(minute int, now time.Time) (*appsv1.Deployment, []corev1.Pod, Usage)) int {
	var run *Run
	for minute := 0; minute <= minutes; minute++ {
		now := start.Add(time.Duration(minute) * time.Minute)
		deploy, pods, usage := at(minute, now)
		var suspected bool
		run, suspected = Observe(run, Measure(now, deploy, pods, usage), defaults())
		if suspected {
			return minute
		}
	}
	return -1
}

func TestARolloutOfHotCopiesIsNeverCountedAndTheCountStartsWhenTheyHaveSettled(t *testing.T) {
	old := copyOf("web-old", start.Add(-time.Hour), "1")
	first := timeline(120, func(minute int, now time.Time) (*appsv1.Deployment, []corev1.Pod, Usage) {
		switch {
		case minute < 20:
			// Busy, but less than the window: 20 minutes.
			return deployment(1), []corev1.Pod{old}, usageOf(map[string]int64{"web-old": 950})
		case minute < 25:
			// The rollout: the new copy starts, busy (a JIT warming up), beside the old one.
			d := deployment(1)
			d.Status.Replicas, d.Status.UpdatedReplicas = 2, 1
			fresh := copyOf("web-new", start.Add(20*time.Minute), "1")
			return d, []corev1.Pod{old, fresh}, usageOf(map[string]int64{"web-old": 950, "web-new": 1000})
		default:
			fresh := copyOf("web-new", start.Add(20*time.Minute), "1")
			return deployment(1), []corev1.Pod{fresh}, usageOf(map[string]int64{"web-new": 1000})
		}
	})
	require.Equal(t, 25+30, first, "the new copy is counted from minute 25, when it has settled and the old one is gone, and suspected 30 minutes later")
}

func TestScaleToZeroAndChangesInCopiesNeverAddUp(t *testing.T) {
	first := timeline(180, func(minute int, now time.Time) (*appsv1.Deployment, []corev1.Pod, Usage) {
		// Woken by a request every 25 minutes, busy for 20, asleep for 5: busy two thirds of the time, never for 30.
		cycle := minute % 25
		if cycle >= 20 {
			return deployment(0), nil, Usage{}
		}
		woke := start.Add(time.Duration(minute-cycle) * time.Minute)
		pod := copyOf(fmt.Sprintf("web-%d", minute/25), woke, "1")
		return deployment(1), []corev1.Pod{pod}, usageOf(map[string]int64{pod.Name: 1000})
	})
	require.Equal(t, -1, first)

	first = timeline(180, func(minute int, now time.Time) (*appsv1.Deployment, []corev1.Pod, Usage) {
		// An autoscaled service that adds a copy every 20 minutes, all of them busy.
		copies := 1 + minute/20
		pods := make([]corev1.Pod, 0, copies)
		use := map[string]int64{}
		for i := range copies {
			pod := copyOf(fmt.Sprintf("web-%d", i), start.Add(time.Duration(i*20)*time.Minute), "1")
			pods = append(pods, pod)
			use[pod.Name] = 1000
		}
		return deployment(int32(copies)), pods, usageOf(use)
	})
	require.Equal(t, -1, first, "each new copy starts the count again")
}
