package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	tenantOrg     = "org-acme"
	tenantClass   = "gvisor"
	tenantTag     = "reserved-acme"
	tenantNS      = "payments-production"
	tenantProject = "payments"
)

func tenantSandboxConfig(skipOnReserved bool) *geassv1alpha1.GeassPlatformConfig {
	return &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{Tenancy: &geassv1alpha1.GeassPlatformTenantIsolationSpec{
			Sandbox: &geassv1alpha1.GeassTenantSandboxSpec{RuntimeClassName: tenantClass, SkipOnReservedNodes: skipOnReserved},
		}},
	}
}

func tenantRuntimeClass() *nodev1.RuntimeClass {
	return &nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: tenantClass}, Handler: "runsc"}
}

func tenantOrganization(name string, requiredTags ...string) *geassv1alpha1.GeassOrganization {
	return &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassOrganizationSpec{DisplayName: name, IdPID: name, RequiredTags: requiredTags},
	}
}

// readyTenantProject is the project payments, ready on a ready cluster, of the organization (none for "").
func readyTenantProject(org string) []client.Object {
	ready := []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue, Reason: platform.ConditionReasonReady, LastTransitionTime: metav1.Now()}}
	return []client.Object{
		&geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace}, Status: geassv1alpha1.GeassClusterStatus{Conditions: ready}},
		&geassv1alpha1.GeassProject{
			ObjectMeta: metav1.ObjectMeta{Name: tenantProject, Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: "default"}, Org: org, Environments: []string{"production"}},
			Status:     geassv1alpha1.GeassProjectStatus{Conditions: ready},
		},
	}
}

// reservedTenantNode is a ready node with room, tagged and reserved for the tag the way a pool's reservedTags makes it.
func reservedTenantNode(name string) *corev1.Node {
	room := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi"), corev1.ResourcePods: resource.MustParse("110")}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{platform.TagKey(tenantTag): "true"}},
		Spec:       corev1.NodeSpec{Taints: []corev1.Taint{{Key: platform.TagKey(tenantTag), Value: platform.ReservedTaintValue, Effect: corev1.TaintEffectNoSchedule}}},
		Status: corev1.NodeStatus{
			Capacity: room, Allocatable: room,
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

// tenantService is a service of the project payments that is deployed, from an image, with a finalizer already on it,
// so a reconcile goes straight to making its pods.
func tenantService() *geassv1alpha1.GeassApp {
	return &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "hello", Namespace: platform.SystemNamespace, Finalizers: []string{appFinalizer}},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: tenantProject, Environment: "production", Port: 8080,
			Source: geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "ghcr.io/acme/hello:1"}},
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
	}
}

func sandboxAppReconciler(t *testing.T, objects ...client.Object) (*GeassAppReconciler, *events.FakeRecorder) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, monitoringv1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&geassv1alpha1.GeassApp{}, &geassv1alpha1.GeassProject{}, &geassv1alpha1.GeassCluster{}).Build()
	recorder := events.NewFakeRecorder(10)
	return &GeassAppReconciler{Client: c, Scheme: scheme, Recorder: recorder}, recorder
}

func reconcileTenantService(t *testing.T, r *GeassAppReconciler) {
	t.Helper()
	_, err := r.Reconcile(context.Background(), requestFor(tenantService()))
	require.NoError(t, err)
}

func tenantDeployment(t *testing.T, r *GeassAppReconciler) (*appsv1.Deployment, bool) {
	t.Helper()
	deploy := &appsv1.Deployment{}
	if err := r.Get(context.Background(), client.ObjectKey{Name: "hello", Namespace: tenantNS}, deploy); err != nil {
		require.True(t, client.IgnoreNotFound(err) == nil, err)
		return nil, false
	}
	return deploy, true
}

func storedService(t *testing.T, r *GeassAppReconciler) *geassv1alpha1.GeassApp {
	t.Helper()
	app := &geassv1alpha1.GeassApp{}
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(tenantService()), app))
	return app
}

func runtimeClassOf(spec corev1.PodSpec) string {
	if spec.RuntimeClassName == nil {
		return ""
	}
	return *spec.RuntimeClassName
}

func drainEvents(recorder *events.FakeRecorder) []string {
	var seen []string
	for {
		select {
		case e := <-recorder.Events:
			seen = append(seen, e)
		default:
			return seen
		}
	}
}

func TestAnOrganizationsServiceAndItsSchedulesRunUnderTheRuntimeClass(t *testing.T) {
	ctx := context.Background()
	r, _ := sandboxAppReconciler(t, append(readyTenantProject(tenantOrg), tenantSandboxConfig(false), tenantRuntimeClass())...)
	app := functionApp("bun", "Bun.serve({ port: 1 })")
	app.Spec.Source.Function.Triggers = []geassv1alpha1.GeassFunctionTrigger{{Name: "nightly", Schedule: "0 3 * * *"}}

	var sandbox platform.Sandbox
	require.NoError(t, r.reconcileFunctionCode(ctx, app, tenantNS))
	require.NoError(t, r.reconcileDeployment(ctx, app, tenantNS, withSandboxResult(&sandbox)))
	require.Equal(t, tenantClass, runtimeClassOf(deploymentPod(t, r)))
	require.Equal(t, platform.Sandbox{RuntimeClass: tenantClass, Applies: true, Org: tenantOrg}, sandbox)

	require.NoError(t, r.reconcileFunctionTriggers(ctx, app, tenantNS, scaleToZeroPlan{}))
	var crons batchv1.CronJobList
	require.NoError(t, r.List(ctx, &crons, client.InNamespace(tenantNS)))
	require.Len(t, crons.Items, 1)
	require.Equal(t, tenantClass, runtimeClassOf(crons.Items[0].Spec.JobTemplate.Spec.Template.Spec), "a schedule's pod is the organization's too")
}

func TestThePlatformsOwnServiceIsNotSandboxed(t *testing.T) {
	ctx := context.Background()
	r, _ := sandboxAppReconciler(t, append(readyTenantProject(""), tenantSandboxConfig(false))...)
	var sandbox platform.Sandbox
	require.NoError(t, r.reconcileDeployment(ctx, functionApp("bun", "x"), tenantNS, withSandboxResult(&sandbox)), "the RuntimeClass is not needed, so its absence holds nothing back")
	require.Nil(t, deploymentPod(t, r).RuntimeClassName)
	require.False(t, sandbox.Applies)
}

// Without the setting a service's pod template is what it was before the setting existed: the same bytes whether there
// is no platform configuration, one with no tenancy, or one whose sandbox does not apply to the project, and writing it
// again changes nothing.
func TestWithoutTheSettingAServicesPodTemplateIsUnchanged(t *testing.T) {
	ctx := context.Background()
	render := func(t *testing.T, objects ...client.Object) []byte {
		t.Helper()
		r, _ := sandboxAppReconciler(t, objects...)
		app := functionApp("bun", "Bun.serve({ port: 1 })")
		require.NoError(t, r.reconcileFunctionCode(ctx, app, tenantNS))
		require.NoError(t, r.reconcileDeployment(ctx, app, tenantNS))
		var before appsv1.Deployment
		require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: tenantNS}, &before))
		require.NoError(t, r.reconcileDeployment(ctx, app, tenantNS))
		var after appsv1.Deployment
		require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: tenantNS}, &after))
		require.Equal(t, before.ResourceVersion, after.ResourceVersion, "a second pass writes nothing")
		require.Nil(t, after.Spec.Template.Spec.RuntimeClassName)
		encoded, err := json.Marshal(after.Spec.Template)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "runtimeClassName")
		return encoded
	}
	orgProject := readyTenantProject(tenantOrg)
	baseline := render(t, orgProject...)
	require.Equal(t, string(baseline), string(render(t, append(readyTenantProject(tenantOrg), &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
	})...)), "a platform configuration without tenancy")
	require.Equal(t, string(baseline), string(render(t, append(readyTenantProject(tenantOrg), &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassPlatformConfigSpec{Tenancy: &geassv1alpha1.GeassPlatformTenantIsolationSpec{}},
	})...)), "a tenancy section with no sandbox")
	require.Equal(t, string(baseline), string(render(t, append(readyTenantProject(""), tenantSandboxConfig(true))...)), "the sandbox set, for a project of no organization")
}

func TestAServiceWaitsForAMissingRuntimeClassThenStartsUnderIt(t *testing.T) {
	ctx := context.Background()
	r, recorder := sandboxAppReconciler(t, append(readyTenantProject(tenantOrg), tenantSandboxConfig(false), tenantService())...)

	reconcileTenantService(t, r)
	_, made := tenantDeployment(t, r)
	require.False(t, made, "no pod is started without the sandbox")
	app := storedService(t, r)
	ready := meta.FindStatusCondition(app.Status.Conditions, platform.ConditionReady)
	require.NotNil(t, ready)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.True(t, strings.HasPrefix(ready.Message, platform.MessageSandboxRuntimeMissing), ready.Message)
	require.Contains(t, ready.Message, `RuntimeClass "gvisor"`)
	sandboxed := meta.FindStatusCondition(app.Status.Conditions, platform.ConditionSandboxed)
	require.NotNil(t, sandboxed)
	require.Equal(t, metav1.ConditionFalse, sandboxed.Status)
	require.Equal(t, platform.SandboxReasonRuntimeClassMissing, sandboxed.Reason)
	seen := drainEvents(recorder)
	require.Len(t, seen, 1)
	require.Contains(t, seen[0], "Warning "+platform.EventSandboxRuntimeClassMissing)
	require.Contains(t, seen[0], `"gvisor"`)

	reconcileTenantService(t, r)
	require.Empty(t, drainEvents(recorder), "the event is sent when the wait begins, not on every pass")
	require.Zero(t, storedService(t, r).Status.ReconcileRetry.ReconcileFailures, "the wait is not a failure that would give the service up")

	// The class appearing reaches the service, which then starts under it.
	require.Equal(t, []string{"hello"}, requestNames(r.appsForRuntimeClass(ctx, tenantRuntimeClass())))
	require.Empty(t, r.appsForRuntimeClass(ctx, &nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: "kata"}}), "another class reaches nothing")
	require.NoError(t, r.Create(ctx, tenantRuntimeClass()))
	reconcileTenantService(t, r)
	deploy, made := tenantDeployment(t, r)
	require.True(t, made)
	require.Equal(t, tenantClass, runtimeClassOf(deploy.Spec.Template.Spec))
	sandboxed = meta.FindStatusCondition(storedService(t, r).Status.Conditions, platform.ConditionSandboxed)
	require.NotNil(t, sandboxed)
	require.Equal(t, metav1.ConditionTrue, sandboxed.Status)
	require.Equal(t, platform.SandboxReasonRuntimeClass, sandboxed.Reason)
}

// A service that ran before the sandbox was turned on keeps its Deployment exactly as it was while the class is missing:
// nothing of it is rewritten, and nothing new starts without the sandbox. Once the class is there it is rolled into it.
func TestTurningTheSandboxOnRollsARunningServiceOnlyOnceTheRuntimeClassIsThere(t *testing.T) {
	ctx := context.Background()
	r, _ := sandboxAppReconciler(t, append(readyTenantProject(tenantOrg), tenantService())...)
	reconcileTenantService(t, r)
	before, made := tenantDeployment(t, r)
	require.True(t, made)
	require.Nil(t, before.Spec.Template.Spec.RuntimeClassName)
	require.Nil(t, meta.FindStatusCondition(storedService(t, r).Status.Conditions, platform.ConditionSandboxed), "nothing is said while the setting is off")

	config := tenantSandboxConfig(false)
	require.NoError(t, r.Create(ctx, config))
	require.True(t, tenancyChanged().Create(event.CreateEvent{Object: config}), "the setting reaches every service")
	reconcileTenantService(t, r)
	held, _ := tenantDeployment(t, r)
	require.Equal(t, before.ResourceVersion, held.ResourceVersion, "the Deployment is not written while the class is missing")

	require.NoError(t, r.Create(ctx, tenantRuntimeClass()))
	reconcileTenantService(t, r)
	rolled, _ := tenantDeployment(t, r)
	require.Equal(t, tenantClass, runtimeClassOf(rolled.Spec.Template.Spec), "the pod template changes, so the Deployment rolls its copies")
	require.Equal(t, before.Spec.Strategy, rolled.Spec.Strategy, "in its own rolling way")

	// Turning it off again rolls the service back, and its status says nothing of the sandbox.
	latest := &geassv1alpha1.GeassPlatformConfig{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(config), latest))
	off := latest.DeepCopy()
	off.Spec.Tenancy = nil
	require.True(t, tenancyChanged().Update(event.UpdateEvent{ObjectOld: latest, ObjectNew: off}))
	require.NoError(t, r.Update(ctx, off))
	reconcileTenantService(t, r)
	back, _ := tenantDeployment(t, r)
	require.Nil(t, back.Spec.Template.Spec.RuntimeClassName)
	require.Nil(t, meta.FindStatusCondition(storedService(t, r).Status.Conditions, platform.ConditionSandboxed))
}

func TestAServiceOnlyOnItsOrganizationsReservedNodesRunsWithoutTheSandboxUntilThatIsNoLongerCertain(t *testing.T) {
	ctx := context.Background()
	r, _ := sandboxAppReconciler(t, append(readyTenantProject(tenantOrg),
		tenantSandboxConfig(true), tenantOrganization(tenantOrg, tenantTag), reservedTenantNode("reserved-1"), tenantService())...)

	reconcileTenantService(t, r)
	deploy, made := tenantDeployment(t, r)
	require.True(t, made, "the RuntimeClass is not there, and not needed")
	require.Equal(t, "true", deploy.Spec.Template.Spec.NodeSelector[platform.TagKey(tenantTag)])
	require.Nil(t, deploy.Spec.Template.Spec.RuntimeClassName)
	sandboxed := meta.FindStatusCondition(storedService(t, r).Status.Conditions, platform.ConditionSandboxed)
	require.NotNil(t, sandboxed)
	require.Equal(t, platform.SandboxReasonReservedNodes, sandboxed.Reason)

	// Another organization that comes to require the same tag reaches this service too, which is then sandboxed: here the
	// class is missing, so it waits, and its Deployment is left as it was.
	other := tenantOrganization("org-other", tenantTag)
	require.NoError(t, r.Create(ctx, other))
	require.Equal(t, []string{"hello"}, requestNames(r.appsForOrganization(ctx, other)))
	reconcileTenantService(t, r)
	held, _ := tenantDeployment(t, r)
	require.Equal(t, deploy.ResourceVersion, held.ResourceVersion)
	require.Equal(t, platform.SandboxReasonRuntimeClassMissing, meta.FindStatusCondition(storedService(t, r).Status.Conditions, platform.ConditionSandboxed).Reason)

	require.NoError(t, r.Create(ctx, tenantRuntimeClass()))
	reconcileTenantService(t, r)
	rolled, _ := tenantDeployment(t, r)
	require.Equal(t, tenantClass, runtimeClassOf(rolled.Spec.Template.Spec))
}

func TestReservationChangesReachServicesOnlyWhileReservedNodesMaySkipTheSandbox(t *testing.T) {
	ctx := context.Background()
	node := reservedTenantNode("reserved-1")
	unreserved := node.DeepCopy()
	unreserved.Spec.Taints = nil
	update := event.UpdateEvent{ObjectOld: node, ObjectNew: unreserved}
	require.True(t, nodeReservationChanged().Update(update))
	heartbeat := node.DeepCopy()
	heartbeat.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}
	require.False(t, nodeReservationChanged().Update(event.UpdateEvent{ObjectOld: node, ObjectNew: heartbeat}), "a node's status says nothing about its reservations")

	skipping, _ := sandboxAppReconciler(t, tenantSandboxConfig(true), tenantService())
	require.Equal(t, []string{"hello"}, requestNames(skipping.appsForReservation(ctx, node)))
	sandboxingAll, _ := sandboxAppReconciler(t, tenantSandboxConfig(false), tenantService())
	require.Empty(t, sandboxingAll.appsForReservation(ctx, node), "every organization's pod is sandboxed whatever the nodes, so nothing to do")
	off, _ := sandboxAppReconciler(t, tenantService())
	require.Empty(t, off.appsForReservation(ctx, node))
	require.Empty(t, off.appsForOrganization(ctx, tenantOrganization(tenantOrg)), "an organization reaches only its own projects' services, as before")
}

func TestOnlyTheTenancyOfThePlatformsConfigurationReachesServices(t *testing.T) {
	config := tenantSandboxConfig(false)
	require.False(t, tenancyChanged().Create(event.CreateEvent{Object: &geassv1alpha1.GeassPlatformConfig{ObjectMeta: config.ObjectMeta}}), "a configuration without tenancy")
	other := config.DeepCopy()
	other.Name = "not-the-platform"
	require.False(t, tenancyChanged().Create(event.CreateEvent{Object: other}), "only the platform's own configuration counts")

	renamed := config.DeepCopy()
	renamed.Spec.RootDomain = "example.com"
	require.False(t, tenancyChanged().Update(event.UpdateEvent{ObjectOld: config, ObjectNew: renamed}), "another setting changes nothing of how pods run")
	skipping := config.DeepCopy()
	skipping.Spec.Tenancy.Sandbox.SkipOnReservedNodes = true
	require.True(t, tenancyChanged().Update(event.UpdateEvent{ObjectOld: config, ObjectNew: skipping}))
}

func TestAnOrganizationsBuildRunsUnderTheRuntimeClass(t *testing.T) {
	r, c := isolationFixture(t, isolatedOrg, false)
	require.NoError(t, c.Create(context.Background(), tenantSandboxConfig(false)))
	require.NoError(t, c.Create(context.Background(), tenantRuntimeClass()))
	reconcileBuild(t, r, "api-build-1")

	jobs := jobsIn(t, c, platform.OrgBuildNamespace(isolatedOrg))
	require.Len(t, jobs, 1)
	require.Equal(t, tenantClass, runtimeClassOf(jobs[0].Spec.Template.Spec), "the build runs the organization's Dockerfile")
	build := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildRunning, build.Status.Phase)
	sandboxed := meta.FindStatusCondition(build.Status.Conditions, platform.ConditionSandboxed)
	require.NotNil(t, sandboxed)
	require.Equal(t, metav1.ConditionTrue, sandboxed.Status)
}

func TestABuildWaitsForAMissingRuntimeClassWithNothingCopiedThenRunsUnderIt(t *testing.T) {
	r, c := isolationFixture(t, isolatedOrg, false)
	recorder := events.NewFakeRecorder(10)
	r.Recorder = recorder
	require.NoError(t, c.Create(context.Background(), tenantSandboxConfig(false)))
	buildNamespace := platform.OrgBuildNamespace(isolatedOrg)

	result := reconcileBuild(t, r, "api-build-1")
	require.Positive(t, result.RequeueAfter, "it looks again for the class")
	require.Empty(t, jobsIn(t, c, buildNamespace))
	require.Empty(t, secretsIn(t, c, buildNamespace), "no credential is copied for a build that cannot run")
	build := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildPending, build.Status.Phase, "waiting, not failed")
	require.Empty(t, build.Status.FailureReason)
	sandboxed := meta.FindStatusCondition(build.Status.Conditions, platform.ConditionSandboxed)
	require.NotNil(t, sandboxed)
	require.Equal(t, platform.SandboxReasonRuntimeClassMissing, sandboxed.Reason)
	require.Contains(t, buildSandboxWait(&build), `RuntimeClass "gvisor"`, "what the service waiting for the build says")
	require.Len(t, drainEvents(recorder), 1)
	reconcileBuild(t, r, "api-build-1")
	require.Empty(t, drainEvents(recorder), "one event for one wait")

	require.NoError(t, c.Create(context.Background(), tenantRuntimeClass()))
	reconcileBuild(t, r, "api-build-1")
	jobs := jobsIn(t, c, buildNamespace)
	require.Len(t, jobs, 1)
	require.Equal(t, tenantClass, runtimeClassOf(jobs[0].Spec.Template.Spec))
	build = storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildRunning, build.Status.Phase)
	require.Empty(t, buildSandboxWait(&build))
}

func TestABuildOfAProjectWithNoOrganizationIsNotSandboxed(t *testing.T) {
	r, c := isolationFixture(t, "", false)
	require.NoError(t, c.Create(context.Background(), tenantSandboxConfig(false)))
	reconcileBuild(t, r, "api-build-1")

	jobs := jobsIn(t, c, platform.SystemNamespace)
	require.Len(t, jobs, 1, "the class is missing, and the platform's build does not need it")
	require.Nil(t, jobs[0].Spec.Template.Spec.RuntimeClassName)
	build := storedBuild(t, c, "api-build-1")
	require.Nil(t, meta.FindStatusCondition(build.Status.Conditions, platform.ConditionSandboxed))
}

func requestNames(requests []ctrl.Request) []string {
	names := make([]string, 0, len(requests))
	for _, request := range requests {
		names = append(names, request.Name)
	}
	return names
}
