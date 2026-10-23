package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const otherOrg = "org-5e6f7a8b"

// withRegistryPolicy is the platform's configuration of the scenarios with spec.tenancy.registry set, as an operator
// writes it: the policy left to its default.
func withRegistryPolicy() *geassv1alpha1.GeassPlatformConfig {
	config := platformConfigWithRegistry()
	config.Spec.Tenancy = &geassv1alpha1.GeassPlatformTenantIsolationSpec{Registry: &geassv1alpha1.GeassTenantRegistrySpec{}}
	return config
}

// requireNoPlatformLogin looks at every Secret, Job, Pod, ConfigMap and Deployment outside the system namespace, whole,
// for the platform's login or the name of its Secret.
func requireNoPlatformLogin(t *testing.T, c client.Client) {
	t.Helper()
	ctx := context.Background()
	encoded := base64.StdEncoding.EncodeToString([]byte(platformLoginTag))
	lists := []client.ObjectList{&corev1.SecretList{}, &batchv1.JobList{}, &corev1.PodList{}, &corev1.ConfigMapList{}, &appsv1.DeploymentList{}}
	for _, list := range lists {
		if err := c.List(ctx, list); runtime.IsNotRegisteredError(err) {
			continue // a kind the fixture does not have holds nothing
		} else {
			require.NoError(t, err)
		}
		items, err := meta.ExtractList(list)
		require.NoError(t, err)
		for _, item := range items {
			object := item.(client.Object)
			if object.GetNamespace() == platform.SystemNamespace {
				continue
			}
			raw, err := json.Marshal(object)
			require.NoError(t, err)
			text := string(raw)
			require.NotContains(t, text, platformLoginTag, "%T %s/%s", object, object.GetNamespace(), object.GetName())
			require.NotContains(t, text, encoded, "%T %s/%s", object, object.GetNamespace(), object.GetName())
			require.NotContains(t, text, `"platform-registry"`, "%T %s/%s names the platform's login", object, object.GetNamespace(), object.GetName())
		}
	}
}

func registryCondition(conditions []metav1.Condition) *metav1.Condition {
	return meta.FindStatusCondition(conditions, platform.ConditionRegistryIsolated)
}

func scenariosOf(t *testing.T, snapshot string) map[string]json.RawMessage {
	t.Helper()
	var scenarios map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(snapshot), &scenarios))
	return scenarios
}

func TestWithoutTheRegistryPolicyWhatIsWrittenIsWhatTheReleaseBeforeItWrote(t *testing.T) {
	golden, err := os.ReadFile("testdata/registry-policy-off.json")
	require.NoError(t, err)
	require.Equal(t, string(golden), registrySnapshot(t, platformConfigWithRegistry), "byte for byte: the build Job, the copies and the pull secret")
}

func TestUnderTheRegistryPolicyThePlatformsProjectsAreUnchangedAndOrganizationsGetNothingOfItsLogin(t *testing.T) {
	golden, err := os.ReadFile("testdata/registry-policy-off.json")
	require.NoError(t, err)
	before, after := scenariosOf(t, string(golden)), scenariosOf(t, registrySnapshot(t, withRegistryPolicy))

	for _, platformScenario := range []string{"platform build", "platform service"} {
		require.JSONEq(t, string(before[platformScenario]), string(after[platformScenario]), platformScenario)
	}
	for _, orgScenario := range []string{"organization build", "organization service"} {
		require.NotContains(t, string(after[orgScenario]), base64.StdEncoding.EncodeToString([]byte(platformLoginTag)), orgScenario)
		require.NotContains(t, string(after[orgScenario]), "-kaniko", "%s: no Job", orgScenario)
	}
	require.Contains(t, string(after["organization build"]), `"phase": "Failed"`)
	require.Contains(t, string(after["organization service"]), platform.MessageRegistryIsolation+": the organization has no registry of its own", "no build is made, and the pull secret is not written")
}

func TestAnOrganizationsBuildWithThePlatformsLoginFailsBeforeAnythingIsMadeAndSaysWhy(t *testing.T) {
	ctx := context.Background()
	r, c := isolationFixture(t, isolatedOrg, false)
	require.NoError(t, c.Create(ctx, withRegistryPolicy()))
	recorder := events.NewFakeRecorder(10)
	r.Recorder = recorder

	reconcileBuild(t, r, "api-build-1")

	var jobs batchv1.JobList
	require.NoError(t, c.List(ctx, &jobs))
	require.Empty(t, jobs.Items, "no Job, anywhere")
	require.Empty(t, secretsIn(t, c, platform.OrgBuildNamespace(isolatedOrg)), "nothing is copied")
	requireNoPlatformLogin(t, c)
	build := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildFailed, build.Status.Phase)
	require.True(t, strings.HasPrefix(build.Status.FailureReason, platform.MessageRegistryIsolation+": "), build.Status.FailureReason)
	require.Contains(t, build.Status.FailureReason, "platform-registry is the platform's")
	condition := registryCondition(build.Status.Conditions)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, platform.RegistryReasonPlatformCredential, condition.Reason)
	require.Len(t, recorder.Events, 1)
	require.Contains(t, <-recorder.Events, "Warning "+platform.EventRegistryRefused)

	reconcileBuild(t, r, "api-build-1")
	require.Empty(t, recorder.Events, "the warning is sent once, when the build is first held back")
	require.Equal(t, geassv1alpha1.GeassBuildFailed, storedBuild(t, c, "api-build-1").Status.Phase)
}

// ownRegistryFor gives the organization of the isolation fixture the registry of its own that its login is for.
func ownRegistryFor(t *testing.T, c client.Client, org, repository string) {
	t.Helper()
	var organization geassv1alpha1.GeassOrganization
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: org, Namespace: platform.SystemNamespace}, &organization))
	organization.Spec.Registry = &geassv1alpha1.GeassPlatformRegistrySpec{Repository: repository, CredentialRef: &corev1.LocalObjectReference{Name: platform.OrgRegistrySecretName(org)}}
	require.NoError(t, c.Update(context.Background(), &organization))
}

func TestAnOrganizationsBuildWithItsOwnLoginRunsUnderThePolicy(t *testing.T) {
	ctx := context.Background()
	r, c := isolationFixture(t, isolatedOrg, true)
	require.NoError(t, c.Create(ctx, withRegistryPolicy()))
	ownRegistryFor(t, c, isolatedOrg, "ghcr.io/acme/builds")
	build := storedBuild(t, c, "api-build-1")
	build.Spec.Registry = "ghcr.io/acme/builds/shop/api"
	require.NoError(t, c.Update(ctx, &build))

	reconcileBuild(t, r, "api-build-1")

	require.Len(t, jobsIn(t, c, platform.OrgBuildNamespace(isolatedOrg)), 1)
	copies := secretsIn(t, c, platform.OrgBuildNamespace(isolatedOrg))
	require.Equal(t, orgLoginTag, string(copies["api-build-1-registry"].Data[corev1.DockerConfigJsonKey]))
	requireNoPlatformLogin(t, c)
	stored := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildRunning, stored.Status.Phase)
	require.Equal(t, metav1.ConditionTrue, registryCondition(stored.Status.Conditions).Status)
}

func TestARunningBuildWithThePlatformsLoginIsStoppedWhenThePolicyIsTurnedOn(t *testing.T) {
	ctx := context.Background()
	r, c := isolationFixture(t, isolatedOrg, false)
	require.NoError(t, c.Create(ctx, platformConfigWithRegistry()))
	reconcileBuild(t, r, "api-build-1")
	buildNamespace := platform.OrgBuildNamespace(isolatedOrg)
	require.Len(t, jobsIn(t, c, buildNamespace), 1, "without the policy it runs as it did, with the platform's login")
	require.Equal(t, platformLoginTag, string(secretsIn(t, c, buildNamespace)["api-build-1-registry"].Data[corev1.DockerConfigJsonKey]))

	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	config.Spec.Tenancy = withRegistryPolicy().Spec.Tenancy
	require.NoError(t, c.Update(ctx, &config))
	reconcileBuild(t, r, "api-build-1")

	require.Empty(t, jobsIn(t, c, buildNamespace), "the Job that holds the platform's login is stopped")
	require.Empty(t, secretsIn(t, c, buildNamespace), "and its copies go")
	requireNoPlatformLogin(t, c)
	stored := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildFailed, stored.Status.Phase)
	require.Equal(t, platform.RegistryReasonPlatformCredential, registryCondition(stored.Status.Conditions).Reason)
}

func TestCopiesOfThePlatformsLoginLeftInTheBuildNamespaceAreRemovedBeforeABuildStarts(t *testing.T) {
	ctx := context.Background()
	r, c := isolationFixture(t, isolatedOrg, true)
	require.NoError(t, c.Create(ctx, withRegistryPolicy()))
	ownRegistryFor(t, c, isolatedOrg, "ghcr.io/acme/builds")
	build := storedBuild(t, c, "api-build-1")
	build.Spec.Registry = "ghcr.io/acme/builds/shop/api"
	require.NoError(t, c.Update(ctx, &build))
	buildNamespace := platform.OrgBuildNamespace(isolatedOrg)
	require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: buildNamespace, Labels: platform.OrgBuildNamespaceLabels(isolatedOrg)}}))
	managed := map[string]string{platform.LabelManagedBy: platform.ManagedByValue, platform.LabelBuild: "api-build-0", platform.LabelBuildNamespace: platform.SystemNamespace}
	require.NoError(t, c.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "api-build-0-registry", Namespace: buildNamespace, Labels: managed}, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(platformLoginTag)}}))
	require.NoError(t, c.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "not-geass", Namespace: buildNamespace}, Data: map[string][]byte{"x": []byte("y")}}))

	reconcileBuild(t, r, "api-build-1")

	secrets := secretsIn(t, c, buildNamespace)
	require.NotContains(t, secrets, "api-build-0-registry", "a copy an earlier build left is removed")
	require.Contains(t, secrets, "not-geass", "what Geass did not write is left alone")
	require.Contains(t, secrets, "api-build-1-registry")
	requireNoPlatformLogin(t, c)
}

func TestAnOrganizationsBuildMayNotPushIntoAnotherOrganizationsRegistryOrThePlatforms(t *testing.T) {
	ctx := context.Background()
	for name, destination := range map[string]string{
		"another organization's registry": "ghcr.io/other/builds/shop/api",
		"the platform's repository":       "registry.platform.example/builds",
		"below the platform's repository": "registry.platform.example/builds/" + otherOrg + "/api",
	} {
		t.Run(name, func(t *testing.T) {
			r, c := isolationFixture(t, isolatedOrg, true)
			require.NoError(t, c.Create(ctx, withRegistryPolicy()))
			require.NoError(t, c.Create(ctx, &geassv1alpha1.GeassOrganization{
				ObjectMeta: metav1.ObjectMeta{Name: otherOrg, Namespace: platform.SystemNamespace},
				Spec:       geassv1alpha1.GeassOrganizationSpec{Registry: &geassv1alpha1.GeassPlatformRegistrySpec{Repository: "ghcr.io/other/builds"}},
			}))
			ownRegistryFor(t, c, isolatedOrg, "ghcr.io/acme/builds")
			build := storedBuild(t, c, "api-build-1")
			build.Spec.Registry = destination
			require.NoError(t, c.Update(ctx, &build))

			reconcileBuild(t, r, "api-build-1")

			var jobs batchv1.JobList
			require.NoError(t, c.List(ctx, &jobs))
			require.Empty(t, jobs.Items)
			stored := storedBuild(t, c, "api-build-1")
			require.Equal(t, geassv1alpha1.GeassBuildFailed, stored.Status.Phase)
			require.Equal(t, platform.RegistryReasonForeignRepository, registryCondition(stored.Status.Conditions).Reason)
			require.NotContains(t, stored.Status.FailureReason, "belongs to "+otherOrg, "whose it is is not said")
		})
	}
}

// orgShop makes the steady-state fixture's project an organization's, with the platform's configuration given.
func orgShop(t *testing.T, c client.Client, config *geassv1alpha1.GeassPlatformConfig, registry *geassv1alpha1.GeassPlatformRegistrySpec) {
	t.Helper()
	ctx := context.Background()
	var project geassv1alpha1.GeassProject
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "shop", Namespace: platform.SystemNamespace}, &project))
	project.Spec.Org = isolatedOrg
	require.NoError(t, c.Update(ctx, &project))
	require.NoError(t, c.Create(ctx, &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: isolatedOrg, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassOrganizationSpec{Registry: registry},
	}))
	require.NoError(t, c.Create(ctx, &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: otherOrg, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassOrganizationSpec{Registry: &geassv1alpha1.GeassPlatformRegistrySpec{Repository: "ghcr.io/other/builds"}},
	}))
	require.NoError(t, c.Create(ctx, config))
	require.NoError(t, c.Create(ctx, registrySecret("platform-registry", map[string][]byte{corev1.DockerConfigJsonKey: []byte(platformLoginTag)})))
	require.NoError(t, c.Create(ctx, registrySecret(platform.OrgRegistrySecretName(isolatedOrg), map[string][]byte{corev1.DockerConfigJsonKey: []byte(orgLoginTag)})))
}

// asGitService turns the steady-state fixture's service into one built from a repository, with no registry of its own.
func asGitService(t *testing.T, c client.Client, app *geassv1alpha1.GeassApp) {
	t.Helper()
	var stored geassv1alpha1.GeassApp
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(app), &stored))
	stored.Spec.Source = geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{
		ConnectionRef: corev1.LocalObjectReference{Name: "shop-github"}, Repository: "acme/web", Branch: "main",
	}}
	require.NoError(t, c.Update(context.Background(), &stored))
}

func serviceAsStored(t *testing.T, c client.Client, app *geassv1alpha1.GeassApp) geassv1alpha1.GeassApp {
	t.Helper()
	var stored geassv1alpha1.GeassApp
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(app), &stored))
	return stored
}

func readyMessage(app geassv1alpha1.GeassApp) string {
	if condition := meta.FindStatusCondition(app.Status.Conditions, platform.ConditionReady); condition != nil {
		return condition.Message
	}
	return ""
}

func TestUnderTheRegistryPolicyAnOrganizationWithoutARegistryOfItsOwnDoesNotBuild(t *testing.T) {
	ctx := context.Background()
	r, app, _, c := steadyStateReconciler(t)
	recorder := events.NewFakeRecorder(10)
	r.Recorder = recorder
	orgShop(t, c, withRegistryPolicy(), nil)
	asGitService(t, c, app)
	// A pull secret copied from the platform's login before the policy was set.
	require.NoError(t, c.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "web-registry-pull", Namespace: "shop-production", Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}},
		Type:       corev1.SecretTypeDockerConfigJson, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(platformLoginTag)},
	}))

	reconcileApp(t, r, app)

	var builds geassv1alpha1.GeassBuildList
	require.NoError(t, c.List(ctx, &builds))
	require.Empty(t, builds.Items, "no build is made with the platform's login")
	require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: "web-registry-pull", Namespace: "shop-production"}, &corev1.Secret{})), "the copy of the platform's login leaves the organization's namespace")
	requireNoPlatformLogin(t, c)
	stored := serviceAsStored(t, c, app)
	require.True(t, strings.HasPrefix(readyMessage(stored), platform.MessageRegistryIsolation+": the organization has no registry of its own"), readyMessage(stored))
	require.Equal(t, platform.RegistryReasonOwnRegistryRequired, registryCondition(stored.Status.Conditions).Reason)
	require.Len(t, recorder.Events, 1)
	require.False(t, platform.ReconcileFailureShouldCount(readyMessage(stored)), "a wait for the organization's registry is not a failure to give up on")
}

func TestUnderTheRegistryPolicyAnOrganizationsServiceBuildsAndPullsWithItsOwnRegistry(t *testing.T) {
	ctx := context.Background()
	r, app, _, c := steadyStateReconciler(t)
	orgShop(t, c, withRegistryPolicy(), &geassv1alpha1.GeassPlatformRegistrySpec{
		Repository: "ghcr.io/acme/builds", CredentialRef: &corev1.LocalObjectReference{Name: platform.OrgRegistrySecretName(isolatedOrg)},
	})
	asGitService(t, c, app)

	reconcileApp(t, r, app)
	var builds geassv1alpha1.GeassBuildList
	require.NoError(t, c.List(ctx, &builds))
	require.Len(t, builds.Items, 1)
	build := builds.Items[0]
	require.Equal(t, "ghcr.io/acme/builds/shop/web", build.Spec.Registry, "pushed under the organization's repository, a service at a time")
	require.Equal(t, platform.OrgRegistrySecretName(isolatedOrg), build.Spec.CredentialRef.Name)

	build.Status.Phase, build.Status.Image, build.Status.ImageDigest = geassv1alpha1.GeassBuildSucceeded, build.Spec.Registry, digestOne
	require.NoError(t, c.Update(ctx, &build))
	reconcileApp(t, r, app)

	pod := deploymentPodOf(t, c, "web", "shop-production")
	require.Equal(t, "ghcr.io/acme/builds/shop/web@"+digestOne, pod.Containers[0].Image)
	require.Equal(t, []corev1.LocalObjectReference{{Name: "web-registry-pull"}}, pod.ImagePullSecrets)
	var pull corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "web-registry-pull", Namespace: "shop-production"}, &pull))
	require.Equal(t, orgLoginTag, string(pull.Data[corev1.DockerConfigJsonKey]), "the pods pull with the organization's login")
	requireNoPlatformLogin(t, c)
	require.Equal(t, metav1.ConditionTrue, registryCondition(serviceAsStored(t, c, app).Status.Conditions).Status)
}

func TestUnderTheRegistryPolicyAnOrganizationsServiceRunsNoImageOfAnothers(t *testing.T) {
	ctx := context.Background()
	for name, image := range map[string]string{
		"another organization's registry":  "ghcr.io/other/builds/shop/web@" + digestOne,
		"the platform's repository":        "registry.platform.example/builds@" + digestOne,
		"another's path in the platform's": "registry.platform.example/builds/" + otherOrg + "/shop/web:v1",
	} {
		t.Run(name, func(t *testing.T) {
			r, app, _, c := steadyStateReconciler(t)
			orgShop(t, c, withRegistryPolicy(), nil)
			stored := serviceAsStored(t, c, app)
			stored.Spec.Source.Image.Image = image
			require.NoError(t, c.Update(ctx, &stored))

			reconcileApp(t, r, app)

			require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: "web", Namespace: "shop-production"}, &appsv1.Deployment{})), "no Deployment runs it")
			stored = serviceAsStored(t, c, app)
			require.Equal(t, platform.RegistryReasonForeignRepository, registryCondition(stored.Status.Conditions).Reason)
			require.Contains(t, readyMessage(stored), "belongs to the platform or to another organization")
			require.NotContains(t, readyMessage(stored), "ghcr.io/other/builds is", "whose it is is not said")
		})
	}

	// Its own path in the platform's repository, and an image nobody here claims, run.
	for _, image := range []string{"registry.platform.example/builds/" + isolatedOrg + "/web@" + digestOne, "nginx:alpine"} {
		r, app, _, c := steadyStateReconciler(t)
		orgShop(t, c, withRegistryPolicy(), nil)
		stored := serviceAsStored(t, c, app)
		stored.Spec.Source.Image.Image = image
		require.NoError(t, c.Update(ctx, &stored))
		reconcileApp(t, r, app)
		require.Equal(t, image, deploymentPodOf(t, c, "web", "shop-production").Containers[0].Image)
		require.Equal(t, metav1.ConditionTrue, registryCondition(serviceAsStored(t, c, app).Status.Conditions).Status)
	}
}

func TestUnderTheRegistryPolicyAPlatformServiceIsNotTouched(t *testing.T) {
	r, app, log, c := steadyStateReconciler(t)
	require.NoError(t, c.Create(context.Background(), withRegistryPolicy()))

	reconcileApp(t, r, app)
	markDeploymentAvailable(t, c)
	reconcileApp(t, r, app)
	require.Nil(t, registryCondition(serviceAsStored(t, c, app).Status.Conditions), "nothing is said of a service the policy does not apply to")

	log.reset()
	for range 3 {
		reconcileApp(t, r, app)
	}
	require.Empty(t, log.writes, "and it costs the API server nothing more than before: %v", log.writes)
}

func TestTheRegistryPolicyReachesTheServicesWhenItOrThePlatformsRegistryChanges(t *testing.T) {
	changed := registryPolicyChanged()
	off, on := platformConfigWithRegistry(), withRegistryPolicy()
	require.True(t, changed.Update(event.UpdateEvent{ObjectOld: off, ObjectNew: on}), "turned on")
	require.True(t, changed.Update(event.UpdateEvent{ObjectOld: on, ObjectNew: off}), "turned off")
	moved := withRegistryPolicy()
	moved.Spec.Registry.Repository = "registry.platform.example/elsewhere"
	require.True(t, changed.Update(event.UpdateEvent{ObjectOld: on, ObjectNew: moved}), "the platform's repository moved while it is on")
	movedOff := platformConfigWithRegistry()
	movedOff.Spec.Registry.Repository = "registry.platform.example/elsewhere"
	require.False(t, changed.Update(event.UpdateEvent{ObjectOld: off, ObjectNew: movedOff}), "while it is off, nothing new")
	domain := withRegistryPolicy()
	domain.Spec.RootDomain = "example.com"
	require.False(t, changed.Update(event.UpdateEvent{ObjectOld: on, ObjectNew: domain}), "nor for anything else")
	require.True(t, changed.Create(event.CreateEvent{Object: on}))
	require.False(t, changed.Create(event.CreateEvent{Object: off}))
}

func TestUnderTheRegistryPolicyAnOrganizationsRegistryReachesEveryService(t *testing.T) {
	ctx := context.Background()
	app := gitApp()
	other := gitApp()
	other.Name, other.Spec.Project = "elsewhere", "another"
	organization := &geassv1alpha1.GeassOrganization{ObjectMeta: metav1.ObjectMeta{Name: isolatedOrg, Namespace: platform.SystemNamespace}}
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{Org: isolatedOrg}}
	r, c := appTestReconciler(t, app, other, organization, project, platformConfigWithRegistry())
	require.Len(t, r.appsForOrganization(ctx, organization), 1, "without the policy, its own projects' services")

	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	config.Spec.Tenancy = withRegistryPolicy().Spec.Tenancy
	require.NoError(t, c.Update(ctx, &config))
	require.Len(t, r.appsForOrganization(ctx, organization), 2, "with it, every service: its registry is a part no other may use")
}
