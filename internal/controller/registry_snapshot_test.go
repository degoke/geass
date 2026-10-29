package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// What Geass writes for a build and for a built service's pull secret, as JSON, for the scenarios the registry policy
// changes when it is set. Without the policy it must be what Geass wrote before the policy existed, byte for byte, so
// this file uses nothing the policy added: testdata/registry-policy-off.json is its output on the release before it.

// snapshotObject is an object as it was written, without what the fake API server adds to it.
func snapshotObject(object client.Object) map[string]any {
	object.SetResourceVersion("")
	object.SetCreationTimestamp(metav1.Time{})
	object.SetManagedFields(nil)
	object.SetUID("")
	raw, _ := json.Marshal(object)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	delete(out, "status")
	return out
}

// writtenFor is every Job and Secret outside the system namespace, and the builds in it, sorted.
func writtenFor(t *testing.T, c client.Client) []map[string]any {
	t.Helper()
	ctx := context.Background()
	var out []map[string]any
	var jobs batchv1.JobList
	require.NoError(t, c.List(ctx, &jobs))
	for i := range jobs.Items {
		out = append(out, snapshotObject(&jobs.Items[i]))
	}
	var secrets corev1.SecretList
	require.NoError(t, c.List(ctx, &secrets))
	for i := range secrets.Items {
		if secrets.Items[i].Namespace != platform.SystemNamespace {
			out = append(out, snapshotObject(&secrets.Items[i]))
		}
	}
	var builds geassv1alpha1.GeassBuildList
	require.NoError(t, c.List(ctx, &builds))
	for i := range builds.Items {
		phase := builds.Items[i].Status.Phase
		snapshot := snapshotObject(&builds.Items[i])
		snapshot["phase"], snapshot["jobNamespace"] = phase, builds.Items[i].Status.JobNamespace
		out = append(out, snapshot)
	}
	slices.SortFunc(out, func(a, b map[string]any) int {
		return strings.Compare(fmt.Sprint(a["kind"], a["metadata"]), fmt.Sprint(b["kind"], b["metadata"]))
	})
	return out
}

// registrySnapshot runs the scenarios on the fixtures given, which carry the platform's configuration.
func registrySnapshot(t *testing.T, config func() *geassv1alpha1.GeassPlatformConfig) string {
	t.Helper()
	ctx := context.Background()
	scenarios := map[string][]map[string]any{}

	// An organization's build, and a platform project's, as the service controller made them: with the platform's login.
	for name, org := range map[string]string{"organization build": isolatedOrg, "platform build": ""} {
		r, c := isolationFixture(t, org, false)
		require.NoError(t, c.Create(ctx, config()))
		reconcileBuild(t, r, "api-build-1")
		scenarios[name] = writtenFor(t, c)
	}

	// An organization's service with no registry of its own, and a platform project's: the build the service controller
	// makes for it, and the pull secret its pods get.
	for name, org := range map[string]string{"organization service": isolatedOrg, "platform service": ""} {
		app := gitApp()
		app.Spec.Build.Registry = geassv1alpha1.GeassAppRegistrySpec{}
		r, c := appTestReconciler(t, app, config(),
			&geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassProjectSpec{Org: org}},
			&geassv1alpha1.GeassOrganization{ObjectMeta: metav1.ObjectMeta{Name: isolatedOrg, Namespace: platform.SystemNamespace}},
			registrySecret("platform-registry", map[string][]byte{corev1.DockerConfigJsonKey: []byte(platformLoginTag)}),
		)
		// A step that is refused says so in the snapshot; without the policy none is.
		var refusals []map[string]any
		if _, _, err := r.ensureGitBuild(ctx, app); err != nil {
			refusals = append(refusals, map[string]any{"build": err.Error()})
		}
		if _, err := r.reconcileRegistryPullSecret(ctx, app, "payments-production"); err != nil {
			refusals = append(refusals, map[string]any{"pullSecret": err.Error()})
		}
		scenarios[name] = append(writtenFor(t, c), refusals...)
	}
	raw, err := json.MarshalIndent(scenarios, "", "  ")
	require.NoError(t, err)
	return string(raw) + "\n"
}

// platformConfigWithRegistry is the platform's configuration of the scenarios: a registry with the platform's login.
func platformConfigWithRegistry() *geassv1alpha1.GeassPlatformConfig {
	return &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{Registry: geassv1alpha1.GeassPlatformRegistrySpec{
			Repository: "registry.platform.example/builds", CredentialRef: &corev1.LocalObjectReference{Name: "platform-registry"},
		}},
	}
}
