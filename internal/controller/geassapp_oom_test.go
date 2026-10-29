package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func oomTestApp() *geassv1alpha1.GeassApp {
	return &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: "shop", Environment: "production"},
	}
}

// oomPod is a copy of the service that was stopped for memory the given time ago, or not at all.
func oomPod(name string, app *geassv1alpha1.GeassApp, killedAgo time.Duration, request, limit string) *corev1.Pod {
	status := corev1.ContainerStatus{Name: appContainerName}
	if killedAgo > 0 {
		status.LastTerminationState.Terminated = &corev1.ContainerStateTerminated{
			Reason: platform.OOMKilledReason, ExitCode: 137, FinishedAt: metav1.NewTime(time.Now().Add(-killedAgo)),
		}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "shop-production", Labels: geassResourceLabels(app, "GeassApp")},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: appContainerName, Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(request)},
			Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(limit)},
		}}}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{status}},
	}
}

func TestAServiceHearsOfACopyStoppedForMemoryFromItsOwnPodsOnly(t *testing.T) {
	app := oomTestApp()
	other := oomTestApp()
	other.Name = "worker"
	c := quotaTestClient(
		oomPod("web-1", app, 5*time.Minute, "128Mi", "1Gi"),
		oomPod("worker-1", other, time.Minute, "128Mi", "128Mi"),
	)
	r := &GeassAppReconciler{Client: c}

	kill, found := r.recentOOMKill(context.Background(), app, "shop-production")
	require.True(t, found)
	require.Equal(t, "web-1", kill.Pod, "another service's copy is not this service's")
	require.True(t, kill.Bursting())

	_, found = r.recentOOMKill(context.Background(), app, "shop-staging")
	require.False(t, found, "nor is a copy in another environment")
}

func TestAServiceWhoseCopiesWereNotStoppedForMemoryHearsNothing(t *testing.T) {
	app := oomTestApp()
	r := &GeassAppReconciler{Client: quotaTestClient(
		oomPod("web-1", app, 0, "128Mi", "128Mi"),
		oomPod("web-2", app, 2*time.Hour, "128Mi", "128Mi"),
	)}
	_, found := r.recentOOMKill(context.Background(), app, "shop-production")
	require.False(t, found, "none, or one too long ago to say anything about now")
}

func TestTheOutOfMemoryConditionIsSetWhileTheKillIsRecentAndRemovedAfter(t *testing.T) {
	app := oomTestApp()
	pod := oomPod("web-1", app, time.Minute, "256Mi", "256Mi")
	kill, found := platform.RecentOOMKill([]corev1.Pod{*pod}, time.Now(), platform.OOMKillWindow)
	require.True(t, found)

	conditions := outOfMemoryCondition(nil, kill, true)
	require.Len(t, conditions, 1)
	require.Equal(t, platform.ConditionOutOfMemory, conditions[0].Type)
	require.Equal(t, metav1.ConditionTrue, conditions[0].Status)
	require.Equal(t, platform.OOMKilledReason, conditions[0].Reason)
	require.Contains(t, conditions[0].Message, "256 MB")

	// Saying it again changes nothing, so a service that keeps the condition is not written to for it.
	again := outOfMemoryCondition(append([]metav1.Condition(nil), conditions...), kill, true)
	require.Equal(t, conditions, again)

	// Other conditions are not disturbed either way.
	ready := platform.SetCondition(nil, platform.ConditionReady, metav1.ConditionTrue, "AppReady", "Application is ready")
	require.Len(t, outOfMemoryCondition(ready, kill, true), 2)
	cleared := outOfMemoryCondition(append(ready, conditions...), platform.OOMKill{}, false)
	require.Len(t, cleared, 1)
	require.Equal(t, platform.ConditionReady, cleared[0].Type)

	// A service that never ran out of memory gets no condition at all.
	require.Empty(t, outOfMemoryCondition(nil, platform.OOMKill{}, false))
}

func TestTheServiceIsLookedAtAgainWhenTheKillIsNoLongerRecent(t *testing.T) {
	now := time.Now()
	kill := platform.OOMKill{At: now.Add(-20 * time.Minute)}
	wait := oomKillExpiry(kill, now)
	require.Greater(t, wait, 40*time.Minute)
	require.Less(t, wait, 41*time.Minute+time.Second)

	old := platform.OOMKill{At: now.Add(-3 * time.Hour)}
	require.Equal(t, time.Second, oomKillExpiry(old, now), "never negative")
}

func TestOnlyAContainerStoppedForMemoryOnceMoreWakesTheService(t *testing.T) {
	app := oomTestApp()
	predicate := podOOMKilled()
	healthy := oomPod("web-1", app, 0, "128Mi", "128Mi")
	killed := oomPod("web-1", app, time.Minute, "128Mi", "128Mi")
	killedAgain := oomPod("web-1", app, 10*time.Second, "128Mi", "128Mi")

	require.True(t, predicate.Update(event.UpdateEvent{ObjectOld: healthy, ObjectNew: killed}))
	require.True(t, predicate.Update(event.UpdateEvent{ObjectOld: killed, ObjectNew: killedAgain}))
	require.False(t, predicate.Update(event.UpdateEvent{ObjectOld: killed, ObjectNew: killed}), "the same kill, heard of again")
	require.False(t, predicate.Update(event.UpdateEvent{ObjectOld: killed, ObjectNew: healthy}))
	require.False(t, predicate.Create(event.CreateEvent{Object: killed}), "a restart of the operator would wake every service")
	require.False(t, predicate.Delete(event.DeleteEvent{Object: killed}))
}

func TestACopyThatBelongsToAServiceIsMappedToItBySharedLabels(t *testing.T) {
	app := oomTestApp()
	app.Namespace = platform.SystemNamespace
	requests := appForDeployment(context.Background(), oomPod("web-1", app, time.Minute, "128Mi", "128Mi"))
	require.Len(t, requests, 1)
	require.Equal(t, "web", requests[0].Name)
	require.Equal(t, platform.SystemNamespace, requests[0].Namespace)
}

func TestACrashLoopFromMemorySaysSoInsteadOfOnlyThatItIsCrashLooping(t *testing.T) {
	app := oomTestApp()
	r := &GeassAppReconciler{Client: quotaTestClient(oomPod("web-1", app, 2*time.Minute, "128Mi", "256Mi"))}

	message := r.startFailureMessage(context.Background(), app, "shop-production", "app is CrashLoopBackOff")

	require.True(t, strings.HasPrefix(message, platform.MessageOutOfMemory), message)
	require.Contains(t, message, "256 MB")
	require.False(t, platform.ReconcileFailureShouldCount(message), "a crash loop that has a cause to fix is not given up on")
}

func TestOtherCrashesAndOtherStartFailuresKeepTheirOwnMessage(t *testing.T) {
	app := oomTestApp()
	// A crash loop that was not about memory.
	r := &GeassAppReconciler{Client: quotaTestClient(oomPod("web-1", app, 0, "128Mi", "256Mi"))}
	require.Equal(t, "The service cannot start: app is CrashLoopBackOff", r.startFailureMessage(context.Background(), app, "shop-production", "app is CrashLoopBackOff"))

	// A copy that cannot be pulled is not helped by having once run out of memory.
	r = &GeassAppReconciler{Client: quotaTestClient(oomPod("web-1", app, time.Minute, "128Mi", "256Mi"))}
	require.Equal(t, "The service cannot start: app is ImagePullBackOff", r.startFailureMessage(context.Background(), app, "shop-production", "app is ImagePullBackOff"))
}

func TestAPodThatCannotPullItsImageWakesItsServiceOnce(t *testing.T) {
	waiting := func(reason string) *corev1.Pod {
		pod := &corev1.Pod{}
		if reason != "" {
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}}}}
		}
		return pod
	}
	update := func(before, after *corev1.Pod) bool {
		return podStartFailing().Update(event.UpdateEvent{ObjectOld: before, ObjectNew: after})
	}

	require.True(t, update(waiting("ContainerCreating"), waiting("ErrImagePull")), "it has just failed to pull")
	require.True(t, update(waiting(""), waiting("CrashLoopBackOff")))
	require.False(t, update(waiting("ErrImagePull"), waiting("ImagePullBackOff")), "it was failing already: the service heard of it, and does not need to again for every retry")
	require.False(t, update(waiting("ContainerCreating"), waiting("")), "a pod that is only starting is not the service's to hear of")
	require.False(t, podStartFailing().Create(event.CreateEvent{Object: waiting("ErrImagePull")}))
}

func TestAServiceWhoseImageCannotBePulledSaysWhichImage(t *testing.T) {
	app := oomTestApp()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "shop-production", Labels: map[string]string{"app": "web"}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx:does-not-exist"}}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "pull access denied, repository does not exist"},
		}}}},
	}
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop-production"},
		Spec: appsv1.DeploymentSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}}}
	r := &GeassAppReconciler{Client: quotaTestClient(pod)}

	problem := r.startProblem(context.Background(), "shop-production", deploy)

	require.Equal(t, "app is ImagePullBackOff (image nginx:does-not-exist)", problem)
	require.NotContains(t, problem, "repository does not exist", "the registry's own words are left to the pod's events")
	require.False(t, platform.ReconcileFailureIsPermanent(r.startFailureMessage(context.Background(), app, "shop-production", problem)))
}
