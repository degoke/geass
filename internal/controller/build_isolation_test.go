package controller

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	isolatedOrg      = "org-1a2b3c4d"
	platformLoginTag = "PLATFORM-REGISTRY-LOGIN"
	orgLoginTag      = "ORG-REGISTRY-LOGIN"
)

func isolationFixture(t *testing.T, projectOrg string, withOrgRegistry bool) (*GeassBuildReconciler, client.Client) {
	t.Helper()
	build := &geassv1alpha1.GeassBuild{
		ObjectMeta: metav1.ObjectMeta{Name: "api-build-1", Namespace: platform.SystemNamespace, Labels: platform.GitBuildLabels(&geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: "api"}})},
		Spec: geassv1alpha1.GeassBuildSpec{
			App: "api", Project: "shop", Branch: "main", Registry: "registry.example/acme/api:1",
			CredentialRef:    &corev1.LocalObjectReference{Name: "platform-registry"},
			GitCredentialRef: &corev1.LocalObjectReference{Name: "shop-github-token"},
		},
	}
	objects := []client.Object{
		build,
		&geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{Org: projectOrg}},
		&geassv1alpha1.GeassOrganization{ObjectMeta: metav1.ObjectMeta{Name: isolatedOrg, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassOrganizationSpec{DisplayName: "Acme", IdPID: "acme"}},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "platform-registry", Namespace: platform.SystemNamespace}, Type: corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(platformLoginTag), "extra": []byte("never copied")},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "shop-github-token", Namespace: platform.SystemNamespace},
			Data:       map[string][]byte{"token": []byte("ghs_shop"), "account": []byte("acme"), "private_key": []byte("never copied")},
		},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: platform.SystemNamespace}, Data: map[string][]byte{"secret": []byte("platform")}},
	}
	if withOrgRegistry {
		objects = append(objects, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platform.OrgRegistrySecretName(isolatedOrg), Namespace: platform.SystemNamespace}, Type: corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(orgLoginTag)},
		})
		build.Spec.CredentialRef = &corev1.LocalObjectReference{Name: platform.OrgRegistrySecretName(isolatedOrg)}
	}
	return isolationReconciler(t, objects...)
}

func isolationReconciler(t *testing.T, objects ...client.Object) (*GeassBuildReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, nodev1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&geassv1alpha1.GeassBuild{}, &batchv1.Job{}).Build()
	return &GeassBuildReconciler{Client: c, Scheme: scheme, HTTPClient: &http.Client{Transport: &requestCounter{}}}, c
}

func jobsIn(t *testing.T, c client.Client, namespace string) []batchv1.Job {
	t.Helper()
	var jobs batchv1.JobList
	require.NoError(t, c.List(context.Background(), &jobs, client.InNamespace(namespace)))
	return jobs.Items
}

func secretsIn(t *testing.T, c client.Client, namespace string) map[string]corev1.Secret {
	t.Helper()
	var secrets corev1.SecretList
	require.NoError(t, c.List(context.Background(), &secrets, client.InNamespace(namespace)))
	out := map[string]corev1.Secret{}
	for _, secret := range secrets.Items {
		out[secret.Name] = secret
	}
	return out
}

func TestAnOrganizationsBuildRunsInItsOwnNamespaceWithOnlyWhatItNeedsCopiedThere(t *testing.T) {
	r, c := isolationFixture(t, isolatedOrg, false)
	reconcileBuild(t, r, "api-build-1")
	buildNamespace := platform.OrgBuildNamespace(isolatedOrg)

	require.Empty(t, jobsIn(t, c, platform.SystemNamespace), "nothing runs in the platform's namespace")
	jobs := jobsIn(t, c, buildNamespace)
	require.Len(t, jobs, 1)
	job := jobs[0]
	require.Equal(t, "api-build-1-kaniko", job.Name)
	require.Empty(t, job.OwnerReferences, "a build in another namespace cannot own it")
	require.Equal(t, "api-build-1", job.Labels[platform.LabelBuild])
	require.Equal(t, platform.SystemNamespace, job.Labels[platform.LabelBuildNamespace])
	require.False(t, *job.Spec.Template.Spec.AutomountServiceAccountToken, "a build has no use for the cluster's API")

	// The namespace is the organization's, closed and bounded.
	var ns corev1.Namespace
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: buildNamespace}, &ns))
	require.Equal(t, isolatedOrg, ns.Labels[platform.LabelOrgBuild])
	var policy networkingv1.NetworkPolicy
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: platform.OrgBuildPolicyName, Namespace: buildNamespace}, &policy))
	require.Contains(t, policy.Spec.PolicyTypes, networkingv1.PolicyTypeIngress)
	require.Empty(t, policy.Spec.Ingress, "nothing connects to a build")
	var quota corev1.ResourceQuota
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: platform.OrgBuildQuotaName, Namespace: buildNamespace}, &quota))

	// What the build needs is copied, key by key, and the pod uses the copies and not the platform's Secrets.
	secrets := secretsIn(t, c, buildNamespace)
	require.Len(t, secrets, 2)
	registry, git := secrets["api-build-1-registry"], secrets["api-build-1-git"]
	require.Equal(t, platformLoginTag, string(registry.Data[corev1.DockerConfigJsonKey]))
	require.Equal(t, map[string][]byte{corev1.DockerConfigJsonKey: []byte(platformLoginTag)}, registry.Data, "only the login")
	require.Equal(t, map[string][]byte{"token": []byte("ghs_shop")}, git.Data, "only the token, not the rest of the connection")
	pod := job.Spec.Template.Spec
	var volume string
	for _, v := range pod.Volumes {
		if v.Secret != nil {
			volume = v.Secret.SecretName
		}
	}
	require.Equal(t, "api-build-1-registry", volume)
	require.Equal(t, "api-build-1-git", pod.InitContainers[0].Env[2].ValueFrom.SecretKeyRef.Name)

	build := storedBuild(t, c, "api-build-1")
	require.Equal(t, buildNamespace, build.Status.JobNamespace)
	require.Equal(t, geassv1alpha1.GeassBuildRunning, build.Status.Phase)

	// When the run ends the copies go, and the result is read from where the Job ran.
	job.Status.Succeeded = 1
	require.NoError(t, c.Status().Update(context.Background(), &job))
	reconcileBuild(t, r, "api-build-1")
	require.Empty(t, secretsIn(t, c, buildNamespace), "the credentials are not kept once the build is over")
	require.Equal(t, geassv1alpha1.GeassBuildFailed, storedBuild(t, c, "api-build-1").Status.Phase, "no pod reported a digest in this test, which is a failed run")
}

func TestABuildOfAProjectWithNoOrganizationRunsAsItAlwaysDid(t *testing.T) {
	r, c := isolationFixture(t, "", false)
	reconcileBuild(t, r, "api-build-1")

	jobs := jobsIn(t, c, platform.SystemNamespace)
	require.Len(t, jobs, 1)
	require.Len(t, jobs[0].OwnerReferences, 1, "owned by the build, as before")
	require.Equal(t, "platform-registry", jobs[0].Spec.Template.Spec.Volumes[1].Secret.SecretName)
	require.Equal(t, "ghs_shop", string(secretsIn(t, c, platform.SystemNamespace)["shop-github-token"].Data["token"]))
	var namespaces corev1.NamespaceList
	require.NoError(t, c.List(context.Background(), &namespaces))
	require.Empty(t, namespaces.Items, "no build namespace is made for it")
	require.Empty(t, storedBuild(t, c, "api-build-1").Status.JobNamespace)
}

func TestAnOrganizationsOwnLoginIsWhatItsBuildPushesWith(t *testing.T) {
	r, c := isolationFixture(t, isolatedOrg, true)
	reconcileBuild(t, r, "api-build-1")

	secrets := secretsIn(t, c, platform.OrgBuildNamespace(isolatedOrg))
	require.Equal(t, orgLoginTag, string(secrets["api-build-1-registry"].Data[corev1.DockerConfigJsonKey]))
	for _, secret := range secrets {
		for _, value := range secret.Data {
			require.NotContains(t, string(value), platformLoginTag, "the platform's login never reaches the organization's namespace")
		}
	}
}

func TestABuildNamespaceThatIsSomethingElsesIsNotUsed(t *testing.T) {
	r, c := isolationFixture(t, isolatedOrg, false)
	require.NoError(t, c.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platform.OrgBuildNamespace(isolatedOrg), Labels: map[string]string{platform.LabelProject: "someone"}}}))

	reconcileBuild(t, r, "api-build-1")

	require.Empty(t, jobsIn(t, c, platform.OrgBuildNamespace(isolatedOrg)))
	require.Empty(t, secretsIn(t, c, platform.OrgBuildNamespace(isolatedOrg)), "no credential is put in a namespace Geass did not make for it")
	build := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildFailed, build.Status.Phase)
	require.Contains(t, build.Status.FailureReason, "owned by something else")
}

func TestACancelledBuildTakesItsJobAndCopiesFromTheOrganizationsNamespace(t *testing.T) {
	r, c := isolationFixture(t, isolatedOrg, false)
	reconcileBuild(t, r, "api-build-1")
	buildNamespace := platform.OrgBuildNamespace(isolatedOrg)
	require.Len(t, jobsIn(t, c, buildNamespace), 1)

	build := storedBuild(t, c, "api-build-1")
	build.Status.CancelRequested = true
	require.NoError(t, c.Status().Update(context.Background(), &build))
	reconcileBuild(t, r, "api-build-1")

	require.Empty(t, jobsIn(t, c, buildNamespace))
	require.Empty(t, secretsIn(t, c, buildNamespace))
	require.Equal(t, geassv1alpha1.GeassBuildCancelled, storedBuild(t, c, "api-build-1").Status.Phase)
}

func TestAJobInABuildNamespaceFindsItsBuildByItsLabels(t *testing.T) {
	requests := buildForJob(context.Background(), &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name: "api-build-1-kaniko", Namespace: platform.OrgBuildNamespace(isolatedOrg),
		Labels: map[string]string{platform.LabelBuild: "api-build-1", platform.LabelBuildNamespace: platform.SystemNamespace},
	}})
	require.Len(t, requests, 1)
	require.Equal(t, client.ObjectKey{Name: "api-build-1", Namespace: platform.SystemNamespace}, requests[0].NamespacedName)
	require.Empty(t, buildForJob(context.Background(), &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "elsewhere"}}), "a Job of anything else is not a build's")
}

func TestOnlyTheOrganizationsOwnBuildNamespaceIsRemovedWithIt(t *testing.T) {
	ctx := context.Background()
	organization := &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: isolatedOrg, Namespace: platform.SystemNamespace, Finalizers: []string{FinalizerOrganization}},
		Spec:       geassv1alpha1.GeassOrganizationSpec{DisplayName: "Acme", IdPID: "acme"},
	}
	_, c := isolationReconciler(t, organization,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platform.OrgBuildNamespace(isolatedOrg), Labels: platform.OrgBuildNamespaceLabels(isolatedOrg)}},
	)
	r := &GeassOrganizationReconciler{Client: c}
	key := client.ObjectKeyFromObject(organization)

	// A live organization keeps its finalizer, and one that has none is given it.
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.OrgBuildNamespace(isolatedOrg)}, &corev1.Namespace{}))

	require.NoError(t, c.Delete(ctx, organization))
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: platform.OrgBuildNamespace(isolatedOrg)}, &corev1.Namespace{})), "its build namespace goes with it")
	require.True(t, apierrors.IsNotFound(c.Get(ctx, key, &geassv1alpha1.GeassOrganization{})), "and then it is let go")
}

func TestABuildNamespaceIsNeverAProjectsEnvironmentNamespace(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "geass-build-org-1", Labels: platform.OrgBuildNamespaceLabels("org-1")}}
	require.False(t, namespaceBelongsTo(ns, "geass"), "whatever a project and an environment spell it")
	require.False(t, namespaceBelongsTo(ns, ""))
	require.True(t, namespaceBelongsTo(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}}}, "older"), "what older versions made is unchanged")
}

func TestAnOrganizationsRegistryIsWhereItsProjectsBuildsPush(t *testing.T) {
	ctx := context.Background()
	platformConfig := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{Registry: geassv1alpha1.GeassPlatformRegistrySpec{
			Repository: "registry.platform.example/builds", CredentialRef: &corev1.LocalObjectReference{Name: "platform-registry"},
		}},
	}
	organization := func(registry *geassv1alpha1.GeassPlatformRegistrySpec) *geassv1alpha1.GeassOrganization {
		return &geassv1alpha1.GeassOrganization{
			ObjectMeta: metav1.ObjectMeta{Name: isolatedOrg, Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassOrganizationSpec{DisplayName: "Acme", IdPID: "acme", Registry: registry},
		}
	}
	project := func(org string) *geassv1alpha1.GeassProject {
		return &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{Org: org}}
	}
	app := gitApp()
	app.Spec.Build.Registry = geassv1alpha1.GeassAppRegistrySpec{}
	own := &geassv1alpha1.GeassPlatformRegistrySpec{Repository: "ghcr.io/acme/builds", CredentialRef: &corev1.LocalObjectReference{Name: platform.OrgRegistrySecretName(isolatedOrg)}}

	for name, tc := range map[string]struct {
		objects        []client.Object
		wantRepository string
		wantCredential string
	}{
		"an organization with a registry of its own":       {[]client.Object{platformConfig, project(isolatedOrg), organization(own)}, "ghcr.io/acme/builds", platform.OrgRegistrySecretName(isolatedOrg)},
		"an organization with none uses the platform's":    {[]client.Object{platformConfig, project(isolatedOrg), organization(nil)}, "registry.platform.example/builds", "platform-registry"},
		"a project of no organization uses the platform's": {[]client.Object{platformConfig, project(""), organization(own)}, "registry.platform.example/builds", "platform-registry"},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := appTestReconciler(t, tc.objects...)
			repository, credential, err := r.buildRegistry(ctx, app)
			require.NoError(t, err)
			require.Equal(t, tc.wantRepository, repository)
			require.NotNil(t, credential)
			require.Equal(t, tc.wantCredential, credential.Name)
		})
	}

	// A service that names a registry of its own keeps it, as it always did.
	named := gitApp()
	r, _ := appTestReconciler(t, platformConfig, project(isolatedOrg), organization(own))
	repository, credential, err := r.buildRegistry(ctx, named)
	require.NoError(t, err)
	require.Equal(t, "registry.example.com/acme/api", repository)
	require.Nil(t, credential)
}
