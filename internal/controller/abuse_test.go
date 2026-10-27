package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	utilptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/abuse"
	"github.com/degoke/geass/pkg/platform"
)

const (
	abuseOrg       = "org-acme"
	abuseProject   = "payments"
	abuseNamespace = "payments-production"
)

var abuseStart = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

func abuseConfig(spec *geassv1alpha1.GeassTenantAbuse) *geassv1alpha1.GeassPlatformConfig {
	config := &geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}}
	if spec != nil {
		config.Spec.Tenancy = &geassv1alpha1.GeassPlatformTenantIsolationSpec{Abuse: spec}
	}
	return config
}

func abuseProjectOf(name, org string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Org: org, Environments: []string{"production"}},
	}
}

// abuseService is a deployed service of a project, with a settled Deployment of one copy limited to one core, and the pod.
func abuseService(name, project string) []client.Object {
	namespace := project + "-production"
	selector := map[string]string{"geass.dev/name": name}
	started := metav1.NewTime(abuseStart.Add(-time.Hour))
	return []client.Object{
		&geassv1alpha1.GeassApp{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassAppSpec{Project: project, Environment: "production", Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true}},
			Status:     geassv1alpha1.GeassAppStatus{TargetNamespace: namespace},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Generation: 1},
			Spec:       appsv1.DeploymentSpec{Replicas: utilptr.To(int32(1)), Selector: &metav1.LabelSelector{MatchLabels: selector}},
			Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-7d9f-abcde", Namespace: namespace, Labels: selector, UID: types.UID("uid-" + name)},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
			}}}},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning, StartTime: &started,
				Conditions:        []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
				ContainerStatuses: []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: started}}}},
			},
		},
	}
}

// fakeCPU says each pod, by name, uses the same share of its one core.
type fakeCPU struct {
	milli map[string]int64
	err   error
	calls int
}

func (f *fakeCPU) Usage(context.Context) (abuse.Usage, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	usage := abuse.Usage{}
	for name, milli := range f.milli {
		usage[types.NamespacedName{Namespace: abuseNamespace, Name: name + "-7d9f-abcde"}] = abuse.PodUse{CPUMilli: milli}
		usage[types.NamespacedName{Namespace: "internal-production", Name: name + "-7d9f-abcde"}] = abuse.PodUse{CPUMilli: milli}
		usage[types.NamespacedName{Namespace: "render-production", Name: name + "-7d9f-abcde"}] = abuse.PodUse{CPUMilli: milli}
	}
	return usage, nil
}

type fakeTraffic struct {
	rates abuse.Rates
	err   error
}

func (f *fakeTraffic) RequestRates(context.Context) (abuse.Rates, error) { return f.rates, f.err }

func abuseScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	return scheme
}

func abuseClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().WithScheme(abuseScheme(t)).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassApp{}, &geassv1alpha1.GeassProject{}).Build()
}

// abuseWorld is the organization acme with the project payments and its service miner, and the project internal of no
// organization with its service batch, under the settings.
func abuseWorld(spec *geassv1alpha1.GeassTenantAbuse, more ...client.Object) []client.Object {
	objects := []client.Object{
		abuseConfig(spec),
		&geassv1alpha1.GeassOrganization{ObjectMeta: metav1.ObjectMeta{Name: abuseOrg, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassOrganizationSpec{DisplayName: "Acme", IdPID: "acme"}},
		abuseProjectOf(abuseProject, abuseOrg), abuseProjectOf("internal", ""),
	}
	objects = append(objects, abuseService("miner", abuseProject)...)
	objects = append(objects, abuseService("batch", "internal")...)
	return append(objects, more...)
}

type detectorClock struct{ now time.Time }

func (c *detectorClock) Now() time.Time { return c.now }

func newDetector(c client.Client, cpu *fakeCPU, clock *detectorClock) *AbuseDetector {
	return &AbuseDetector{Client: c, Reader: c, CPU: cpu, Traffic: &fakeTraffic{err: errors.New("no Prometheus in a unit test")}, Now: clock.Now}
}

// sampleFor runs the detector once a minute for minutes minutes and one more, and says at which minute the service was
// first suspected, or -1.
func sampleFor(t *testing.T, d *AbuseDetector, clock *detectorClock, minutes int, service string) int {
	t.Helper()
	for minute := 0; minute <= minutes; minute++ {
		require.NoError(t, d.Sample(context.Background()))
		if _, ok := abuse.SuspectOf(storedAbuseService(t, d.Client, service)); ok {
			return minute
		}
		clock.now = clock.now.Add(time.Minute)
	}
	return -1
}

func storedAbuseService(t *testing.T, c client.Client, name string) *geassv1alpha1.GeassApp {
	t.Helper()
	app := &geassv1alpha1.GeassApp{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, app))
	return app
}

func storedState(t *testing.T, c client.Client) (abuse.State, bool) {
	t.Helper()
	state, configMap, err := abuse.LoadState(context.Background(), c)
	require.NoError(t, err)
	return state, configMap != nil
}

func TestWithoutTheSettingTheDetectorReadsNoMetricsAndWritesNothing(t *testing.T) {
	c := abuseClient(t, abuseWorld(nil)...)
	cpu := &fakeCPU{milli: map[string]int64{"miner": 1000, "batch": 1000}}
	clock := &detectorClock{now: abuseStart}
	d := newDetector(c, cpu, clock)

	require.Equal(t, -1, sampleFor(t, d, clock, 90, "miner"))
	require.Zero(t, cpu.calls, "the metrics server is not even asked")
	_, exists := storedState(t, c)
	require.False(t, exists)
	require.Empty(t, storedAbuseService(t, c, "miner").Annotations)
}

func TestAnOrganizationsServiceAtItsLimitForTheWindowIsSuspectedWithItsEvidence(t *testing.T) {
	c := abuseClient(t, abuseWorld(&geassv1alpha1.GeassTenantAbuse{})...)
	clock := &detectorClock{now: abuseStart}
	d := newDetector(c, &fakeCPU{milli: map[string]int64{"miner": 970, "batch": 1000}}, clock)

	require.Equal(t, 30, sampleFor(t, d, clock, 40, "miner"))

	suspect, _ := abuse.SuspectOf(storedAbuseService(t, c, "miner"))
	require.Equal(t, abuse.Suspect{
		Source: abuse.SourceCPU, Since: abuseStart, DetectedAt: abuseStart.Add(30 * time.Minute),
		AveragePercent: 97, MinPercent: 97, ThresholdPercent: 90, WindowMinutes: 30, Samples: 31,
	}, *suspect, "left running: the default action is None")
	_, batchSuspected := abuse.SuspectOf(storedAbuseService(t, c, "batch"))
	require.False(t, batchSuspected, "a project of no organization is the platform's own, and not looked at")

	state, _ := storedState(t, c)
	require.Empty(t, state.Runs, "a suspect's run is done with")
	require.True(t, state.CPU.Available)
	require.Nil(t, state.Traffic, "not asked for")
}

func TestBelowTheThresholdNothingIsKept(t *testing.T) {
	c := abuseClient(t, abuseWorld(&geassv1alpha1.GeassTenantAbuse{})...)
	clock := &detectorClock{now: abuseStart}
	d := newDetector(c, &fakeCPU{milli: map[string]int64{"miner": 899}}, clock)

	require.Equal(t, -1, sampleFor(t, d, clock, 60, "miner"))
	state, exists := storedState(t, c)
	require.True(t, exists, "the state says when it last looked and what answered")
	require.Empty(t, state.Runs, "a service at rest has no run")
}

func TestARestartOfTheOperatorCarriesTheRunOnAndALongAbsenceStartsItAgain(t *testing.T) {
	c := abuseClient(t, abuseWorld(&geassv1alpha1.GeassTenantAbuse{})...)
	cpu := &fakeCPU{milli: map[string]int64{"miner": 1000}}
	clock := &detectorClock{now: abuseStart}

	require.Equal(t, -1, sampleFor(t, newDetector(c, cpu, clock), clock, 19, "miner"))
	// A new operator (or a new leader) two minutes later, with nothing in memory.
	clock.now = clock.now.Add(time.Minute)
	require.Equal(t, 9, sampleFor(t, newDetector(c, cpu, clock), clock, 20, "miner"), "minutes 0 to 19, two missed, then 21 to 30")

	c = abuseClient(t, abuseWorld(&geassv1alpha1.GeassTenantAbuse{})...)
	clock = &detectorClock{now: abuseStart}
	require.Equal(t, -1, sampleFor(t, newDetector(c, cpu, clock), clock, 19, "miner"))
	clock.now = clock.now.Add(10 * time.Minute)
	require.Equal(t, 30, sampleFor(t, newDetector(c, cpu, clock), clock, 40, "miner"), "ten minutes nobody saw: the count starts again")
}

func TestExemptOrganizationsProjectsAndReviewedServicesAreLeftOut(t *testing.T) {
	render := abuseProjectOf("render", abuseOrg)
	for name, mutate := range map[string]func([]client.Object){
		"the organization is exempt": func(objects []client.Object) {
			objects[1].(*geassv1alpha1.GeassOrganization).Spec.Abuse = &geassv1alpha1.GeassOrganizationAbuse{Exempt: true}
		},
		"the project is exempt": func(objects []client.Object) {
			objects[1].(*geassv1alpha1.GeassOrganization).Spec.Abuse = &geassv1alpha1.GeassOrganizationAbuse{ExemptProjects: []string{abuseProject}}
		},
		"a platform administrator cleared it": func(objects []client.Object) {
			app := objects[4].(*geassv1alpha1.GeassApp)
			app.Annotations = map[string]string{abuse.AnnotationReview: abuse.Review{At: abuseStart, Until: abuseStart.Add(24 * time.Hour)}.Encode()}
		},
	} {
		objects := abuseWorld(&geassv1alpha1.GeassTenantAbuse{}, render)
		mutate(objects)
		c := abuseClient(t, objects...)
		clock := &detectorClock{now: abuseStart}
		require.Equal(t, -1, sampleFor(t, newDetector(c, &fakeCPU{milli: map[string]int64{"miner": 1000}}, clock), clock, 45, "miner"), name)
	}

	objects := abuseWorld(&geassv1alpha1.GeassTenantAbuse{})
	objects[4].(*geassv1alpha1.GeassApp).Annotations = map[string]string{abuse.AnnotationReview: abuse.Review{At: abuseStart.Add(-31 * 24 * time.Hour), Until: abuseStart.Add(-time.Hour)}.Encode()}
	c := abuseClient(t, objects...)
	clock := &detectorClock{now: abuseStart}
	require.Equal(t, 30, sampleFor(t, newDetector(c, &fakeCPU{milli: map[string]int64{"miner": 1000}}, clock), clock, 40, "miner"), "a review that has ended")
}

func TestTheActionIsDecidedByTheOrganizationsPolicyWhenTheSuspicionIsRecorded(t *testing.T) {
	objects := abuseWorld(&geassv1alpha1.GeassTenantAbuse{Action: geassv1alpha1.AbuseActionSuspend})
	c := abuseClient(t, objects...)
	clock := &detectorClock{now: abuseStart}
	require.Equal(t, 30, sampleFor(t, newDetector(c, &fakeCPU{milli: map[string]int64{"miner": 1000}}, clock), clock, 40, "miner"))
	require.True(t, abuse.Suspended(storedAbuseService(t, c, "miner")))

	objects = abuseWorld(&geassv1alpha1.GeassTenantAbuse{Action: geassv1alpha1.AbuseActionSuspend})
	objects[1].(*geassv1alpha1.GeassOrganization).Spec.Abuse = &geassv1alpha1.GeassOrganizationAbuse{Action: geassv1alpha1.AbuseActionNone}
	c = abuseClient(t, objects...)
	clock = &detectorClock{now: abuseStart}
	require.Equal(t, 30, sampleFor(t, newDetector(c, &fakeCPU{milli: map[string]int64{"miner": 1000}}, clock), clock, 40, "miner"))
	require.False(t, abuse.Suspended(storedAbuseService(t, c, "miner")), "the organization's own action")
}

func TestAMetricsServerThatDoesNotAnswerIsSaidAndBreaksNothingUntilTheGapIsTooLong(t *testing.T) {
	c := abuseClient(t, abuseWorld(&geassv1alpha1.GeassTenantAbuse{})...)
	cpu := &fakeCPU{milli: map[string]int64{"miner": 1000}}
	clock := &detectorClock{now: abuseStart}
	d := newDetector(c, cpu, clock)
	require.Equal(t, -1, sampleFor(t, d, clock, 10, "miner"))

	cpu.err = abuse.ErrNoMetricsServer
	require.Equal(t, -1, sampleFor(t, d, clock, 1, "miner"))
	state, _ := storedState(t, c)
	require.False(t, state.CPU.Available)
	require.Contains(t, state.CPU.Reason, "metrics server")
	require.Contains(t, state.Runs, "miner", "two minutes missed: the run is kept")

	cpu.err = nil
	require.Equal(t, 17, sampleFor(t, d, clock, 30, "miner"), "it carries on from minute 13: the run began at minute 0, and is 30 minutes long at minute 30")
}

func TestQuietTrafficLeavesABusyServiceAloneAndWithoutRequestsHoldsOutLonger(t *testing.T) {
	spec := &geassv1alpha1.GeassTenantAbuse{CPU: &geassv1alpha1.GeassAbuseCPUSignal{QuietTraffic: &geassv1alpha1.GeassAbuseQuietTraffic{MaxRequestsPerMinute: 6}}}

	c := abuseClient(t, abuseWorld(spec)...)
	clock := &detectorClock{now: abuseStart}
	d := newDetector(c, &fakeCPU{milli: map[string]int64{"miner": 1000}}, clock)
	d.Traffic = &fakeTraffic{rates: abuse.Rates{abuseNamespace + "-miner-8080@kubernetes": 2}}
	require.Equal(t, -1, sampleFor(t, d, clock, 45, "miner"), "120 requests a minute: a service at work")
	state, _ := storedState(t, c)
	require.True(t, state.Traffic.Available)

	c = abuseClient(t, abuseWorld(spec)...)
	clock = &detectorClock{now: abuseStart}
	d = newDetector(c, &fakeCPU{milli: map[string]int64{"miner": 1000}}, clock)
	d.Traffic = &fakeTraffic{rates: abuse.Rates{"other-production-web-8080@kubernetes": 2}}
	require.Equal(t, 30, sampleFor(t, d, clock, 45, "miner"), "nobody calls it")
	suspect, _ := abuse.SuspectOf(storedAbuseService(t, c, "miner"))
	require.Equal(t, abuse.TrafficQuiet, suspect.Traffic)

	c = abuseClient(t, abuseWorld(spec)...)
	clock = &detectorClock{now: abuseStart}
	d = newDetector(c, &fakeCPU{milli: map[string]int64{"miner": 1000}}, clock)
	require.Equal(t, 60, sampleFor(t, d, clock, 90, "miner"), "no Prometheus: 95%% for twice the window")
	suspect, _ = abuse.SuspectOf(storedAbuseService(t, c, "miner"))
	require.Equal(t, abuse.TrafficUnknown, suspect.Traffic)
	require.Equal(t, int32(95), suspect.ThresholdPercent)
	require.Equal(t, int32(60), suspect.WindowMinutes)
	state, _ = storedState(t, c)
	require.False(t, state.Traffic.Available)
}

func TestTurningTheSettingOffRemovesTheRuns(t *testing.T) {
	objects := abuseWorld(&geassv1alpha1.GeassTenantAbuse{})
	c := abuseClient(t, objects...)
	clock := &detectorClock{now: abuseStart}
	d := newDetector(c, &fakeCPU{milli: map[string]int64{"miner": 1000}}, clock)
	require.Equal(t, -1, sampleFor(t, d, clock, 5, "miner"))
	_, exists := storedState(t, c)
	require.True(t, exists)

	config := abuseConfig(nil)
	stored := &geassv1alpha1.GeassPlatformConfig{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(config), stored))
	stored.Spec.Tenancy = nil
	require.NoError(t, c.Update(context.Background(), stored))
	require.NoError(t, d.Sample(context.Background()))
	_, exists = storedState(t, c)
	require.False(t, exists)
	require.NoError(t, d.Sample(context.Background()), "and nothing more to do")
}

// ---- the reconciler ----

func abuseReconciler(t *testing.T, objects ...client.Object) (*AbuseReconciler, *events.FakeRecorder, *detectorClock) {
	t.Helper()
	recorder := events.NewFakeRecorder(20)
	clock := &detectorClock{now: abuseStart}
	return &AbuseReconciler{Client: abuseClient(t, objects...), Recorder: recorder, Now: clock.Now}, recorder, clock
}

func reconcileAbuse(t *testing.T, r *AbuseReconciler, project string) time.Duration {
	t.Helper()
	result, err := r.Reconcile(context.Background(), requestFor(abuseProjectOf(project, "")))
	require.NoError(t, err)
	return result.RequeueAfter
}

func abuseCondition(conditions []metav1.Condition) *metav1.Condition {
	return meta.FindStatusCondition(conditions, abuse.ConditionAbuseSuspected)
}

func storedProject(t *testing.T, c client.Client, name string) *geassv1alpha1.GeassProject {
	t.Helper()
	project := &geassv1alpha1.GeassProject{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, project))
	return project
}

func annotateService(t *testing.T, c client.Client, name string, change func(app *geassv1alpha1.GeassApp)) {
	t.Helper()
	_, err := abuse.Annotate(context.Background(), c, name, func(app *geassv1alpha1.GeassApp) bool { change(app); return true })
	require.NoError(t, err)
}

func cpuSuspect(suspended bool) abuse.Suspect {
	return abuse.Suspect{Source: abuse.SourceCPU, Since: abuseStart, DetectedAt: abuseStart.Add(30 * time.Minute),
		AveragePercent: 99, MinPercent: 95, ThresholdPercent: 90, WindowMinutes: 30, Samples: 31, Suspended: suspended}
}

func TestAServiceThatNeverWasSuspectedCarriesNoCondition(t *testing.T) {
	r, recorder, _ := abuseReconciler(t, abuseWorld(&geassv1alpha1.GeassTenantAbuse{})...)
	reconcileAbuse(t, r, abuseProject)
	require.Nil(t, abuseCondition(storedAbuseService(t, r.Client, "miner").Status.Conditions))
	require.Nil(t, abuseCondition(storedProject(t, r.Client, abuseProject).Status.Conditions))
	require.Empty(t, recorder.Events)
}

func TestASuspicionBecomesTheConditionsAndOneWarning(t *testing.T) {
	r, recorder, _ := abuseReconciler(t, abuseWorld(&geassv1alpha1.GeassTenantAbuse{Action: geassv1alpha1.AbuseActionSuspend})...)
	annotateService(t, r.Client, "miner", func(app *geassv1alpha1.GeassApp) {
		app.Annotations[abuse.AnnotationSuspect] = cpuSuspect(true).Encode()
	})

	reconcileAbuse(t, r, abuseProject)
	condition := abuseCondition(storedAbuseService(t, r.Client, "miner").Status.Conditions)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, abuse.ReasonSuspended, condition.Reason)
	require.Equal(t, cpuSuspect(true).Message(), condition.Message)
	require.Len(t, recorder.Events, 1)
	require.Equal(t, "Warning AbuseSuspected "+cpuSuspect(true).Message(), <-recorder.Events)

	projectCondition := abuseCondition(storedProject(t, r.Client, abuseProject).Status.Conditions)
	require.Equal(t, metav1.ConditionTrue, projectCondition.Status)
	require.Equal(t, abuse.ReasonServicesSuspected, projectCondition.Reason)
	require.Equal(t, "Abuse is suspected of 1 of the project's services: miner (suspended). A platform administrator reviews each one.", projectCondition.Message)

	reconcileAbuse(t, r, abuseProject)
	require.Empty(t, recorder.Events, "once for each change, not on every pass")
	require.Nil(t, abuseCondition(storedProject(t, r.Client, "internal").Status.Conditions))
}

func TestAReleaseKeepsTheSuspicionAndAClearingEndsIt(t *testing.T) {
	r, recorder, clock := abuseReconciler(t, abuseWorld(&geassv1alpha1.GeassTenantAbuse{})...)
	annotateService(t, r.Client, "miner", func(app *geassv1alpha1.GeassApp) {
		app.Annotations[abuse.AnnotationSuspect] = cpuSuspect(true).Encode()
	})
	reconcileAbuse(t, r, abuseProject)
	<-recorder.Events

	released := cpuSuspect(false)
	at := abuseStart.Add(2 * time.Hour)
	released.ReleasedAt, released.ReleasedBy = &at, "root"
	annotateService(t, r.Client, "miner", func(app *geassv1alpha1.GeassApp) {
		app.Annotations[abuse.AnnotationSuspect] = released.Encode()
	})
	reconcileAbuse(t, r, abuseProject)
	condition := abuseCondition(storedAbuseService(t, r.Client, "miner").Status.Conditions)
	require.Equal(t, metav1.ConditionTrue, condition.Status, "still suspected")
	require.Equal(t, abuse.ReasonSustainedCPU, condition.Reason)
	require.Contains(t, <-recorder.Events, "Normal AbuseReleased")

	review := abuse.Review{At: abuseStart.Add(3 * time.Hour), Until: abuseStart.Add(3*time.Hour + 7*24*time.Hour), By: "root", Cleared: abuse.SourceCPU}
	annotateService(t, r.Client, "miner", func(app *geassv1alpha1.GeassApp) {
		delete(app.Annotations, abuse.AnnotationSuspect)
		app.Annotations[abuse.AnnotationReview] = review.Encode()
	})
	clock.now = review.At
	requeue := reconcileAbuse(t, r, abuseProject)
	require.Equal(t, 7*24*time.Hour, requeue, "looked at again when the review ends")
	condition = abuseCondition(storedAbuseService(t, r.Client, "miner").Status.Conditions)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, abuse.ReasonReviewed, condition.Reason)
	require.Contains(t, <-recorder.Events, "Normal AbuseReviewed")
	require.Nil(t, abuseCondition(storedProject(t, r.Client, abuseProject).Status.Conditions), "nothing of the project is suspected now")

	clock.now = review.Until
	require.Zero(t, reconcileAbuse(t, r, abuseProject))
	service := storedAbuseService(t, r.Client, "miner")
	require.Nil(t, abuseCondition(service.Status.Conditions), "the review is over: back to a service like any other")
	require.NotContains(t, service.Annotations, abuse.AnnotationReview)
	require.Empty(t, recorder.Events)
}

func TestTurningTheSettingOffTakesAwayWhatItWrote(t *testing.T) {
	r, _, _ := abuseReconciler(t, abuseWorld(&geassv1alpha1.GeassTenantAbuse{})...)
	annotateService(t, r.Client, "miner", func(app *geassv1alpha1.GeassApp) {
		app.Annotations[abuse.AnnotationSuspect] = cpuSuspect(true).Encode()
		app.Annotations["geass.dev/display-name"] = "Miner"
	})
	reconcileAbuse(t, r, abuseProject)
	require.NotNil(t, abuseCondition(storedAbuseService(t, r.Client, "miner").Status.Conditions))

	stored := &geassv1alpha1.GeassPlatformConfig{}
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(abuseConfig(nil)), stored))
	stored.Spec.Tenancy = nil
	require.NoError(t, r.Update(context.Background(), stored))
	reconcileAbuse(t, r, abuseProject)

	service := storedAbuseService(t, r.Client, "miner")
	require.Nil(t, abuseCondition(service.Status.Conditions))
	require.NotContains(t, service.Annotations, abuse.AnnotationSuspect, "a suspended service runs again")
	require.Equal(t, "Miner", service.Annotations["geass.dev/display-name"], "and nothing else is touched")
	require.Nil(t, abuseCondition(storedProject(t, r.Client, abuseProject).Status.Conditions))
}

func TestAnUnreadableSuspicionIsASuspension(t *testing.T) {
	r, recorder, _ := abuseReconciler(t, abuseWorld(&geassv1alpha1.GeassTenantAbuse{})...)
	annotateService(t, r.Client, "miner", func(app *geassv1alpha1.GeassApp) {
		app.Annotations[abuse.AnnotationSuspect] = "suspended, says the admin"
	})
	reconcileAbuse(t, r, abuseProject)
	condition := abuseCondition(storedAbuseService(t, r.Client, "miner").Status.Conditions)
	require.Equal(t, abuse.ReasonSuspended, condition.Reason)
	require.Contains(t, condition.Message, "cannot be read")
	require.Len(t, recorder.Events, 1)
}

func eventCreate(object client.Object) event.CreateEvent { return event.CreateEvent{Object: object} }
func eventDelete(object client.Object) event.DeleteEvent { return event.DeleteEvent{Object: object} }
func eventUpdate(before, after client.Object) event.UpdateEvent {
	return event.UpdateEvent{ObjectOld: before, ObjectNew: after}
}

func TestTheReconcilerHearsOfAnnotationsAndNotOfItsOwnConditions(t *testing.T) {
	predicate := abuseAnnotationsChanged()
	plain := &geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: "web", Annotations: map[string]string{"geass.dev/display-name": "Web"}}}
	suspected := plain.DeepCopy()
	suspected.Annotations[abuse.AnnotationSuspect] = cpuSuspect(false).Encode()
	withCondition := suspected.DeepCopy()
	withCondition.Status.Conditions = []metav1.Condition{{Type: abuse.ConditionAbuseSuspected, Status: metav1.ConditionTrue}}
	going := withCondition.DeepCopy()
	going.DeletionTimestamp = &metav1.Time{Time: abuseStart}

	require.False(t, predicate.Create(eventCreate(plain)))
	require.True(t, predicate.Create(eventCreate(suspected)))
	require.True(t, predicate.Update(eventUpdate(plain, suspected)))
	require.False(t, predicate.Update(eventUpdate(suspected, withCondition)), "its own write")
	require.True(t, predicate.Update(eventUpdate(withCondition, going)), "a suspect that goes")
	require.False(t, predicate.Update(eventUpdate(plain, plain)))
	require.True(t, predicate.Delete(eventDelete(withCondition)))
	require.False(t, predicate.Delete(eventDelete(plain)))

	settings := abuseSettingChanged()
	require.False(t, settings.Update(eventUpdate(abuseConfig(nil), abuseConfig(nil))))
	require.True(t, settings.Update(eventUpdate(abuseConfig(nil), abuseConfig(&geassv1alpha1.GeassTenantAbuse{}))))
	require.True(t, settings.Update(eventUpdate(abuseConfig(&geassv1alpha1.GeassTenantAbuse{}), abuseConfig(&geassv1alpha1.GeassTenantAbuse{Action: geassv1alpha1.AbuseActionSuspend}))))
}
