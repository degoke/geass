package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestValidateAppSource(t *testing.T) {
	cases := []struct {
		name    string
		app     geassv1alpha1.GeassApp
		wantErr bool
	}{
		{name: "image source", app: geassv1alpha1.GeassApp{Spec: geassv1alpha1.GeassAppSpec{Source: geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "nginx:latest"}}}}},
		{name: "both", app: geassv1alpha1.GeassApp{Spec: geassv1alpha1.GeassAppSpec{Source: geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "nginx"}, Git: &geassv1alpha1.GeassAppGitSource{ConnectionRef: corev1.LocalObjectReference{Name: "github"}, Repository: "acme/app", Branch: "main"}}}}, wantErr: true},
		{name: "none", app: geassv1alpha1.GeassApp{}, wantErr: true},
		{name: "incomplete git", app: geassv1alpha1.GeassApp{Spec: geassv1alpha1.GeassAppSpec{Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{Repository: "acme/app"}}}}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateAppSource(&tc.app); (got != nil) != tc.wantErr {
				t.Fatalf("validateAppSource error = %v, wantErr %v", got, tc.wantErr)
			}
		})
	}
}

func TestGitBuildNameChangesWithConfiguration(t *testing.T) {
	base := geassv1alpha1.GeassBuildSpec{
		App: "api", Project: "payments", Environment: geassv1alpha1.EnvironmentDev,
		Repository: "acme/api", Branch: "main", Dockerfile: "Dockerfile", Context: ".",
	}
	if got, want := gitBuildName("api", base), gitBuildName("api", base); got != want {
		t.Fatalf("gitBuildName is not stable: %q != %q", got, want)
	}
	changed := base
	changed.Dockerfile = "deploy/Dockerfile"
	if got, old := gitBuildName("api", changed), gitBuildName("api", base); got == old {
		t.Fatalf("changed build configuration reused %q", got)
	}
	if len(gitBuildName(strings.Repeat("a", 63), base)) > 63 {
		t.Fatal("git build name exceeded the Kubernetes name limit")
	}
}

func TestKanikoJobUsesRegistrySecretAndCache(t *testing.T) {
	build := &geassv1alpha1.GeassBuild{ObjectMeta: metav1.ObjectMeta{Name: "api-build", Namespace: "system"}, Spec: geassv1alpha1.GeassBuildSpec{App: "api", Project: "payments", Repository: "acme/api", Branch: "main", Registry: "registry.example/payments/api:abc", Cache: true, CredentialRef: &corev1.LocalObjectReference{Name: "registry-credentials"}}}
	job := (&GeassBuildReconciler{}).jobFor(build)
	if job.Spec.Template.Spec.Containers[0].Image != "gcr.io/kaniko-project/executor:v1.23.2" {
		t.Fatal("expected Kaniko executor")
	}
	if len(job.Spec.Template.Spec.Containers[0].Args) != 4 {
		t.Fatalf("expected cache-enabled Kaniko args, got %v", job.Spec.Template.Spec.Containers[0].Args)
	}
	if len(job.Spec.Template.Spec.Volumes) != 2 || job.Spec.Template.Spec.Volumes[1].Secret == nil {
		t.Fatal("expected registry Secret volume")
	}
	if job.Spec.Template.Spec.Volumes[1].Secret.Items[0].Path != "config.json" {
		t.Fatal("registry credentials must be mounted as config.json")
	}
}

func TestConsoleSessionExpires(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := geassv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	started := metav1.NewTime(time.Now().Add(-time.Hour))
	session := &geassv1alpha1.GeassConsoleSession{ObjectMeta: metav1.ObjectMeta{Name: "session", Namespace: "system"}, Spec: geassv1alpha1.GeassConsoleSessionSpec{TimeoutSeconds: 60}, Status: geassv1alpha1.GeassConsoleSessionStatus{Phase: geassv1alpha1.GeassConsoleActive, StartedAt: &started}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(session).WithObjects(session).Build()
	r := &GeassConsoleSessionReconciler{Client: c, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), ctrlRequest("system", "session")); err != nil {
		t.Fatal(err)
	}
	latest := &geassv1alpha1.GeassConsoleSession{}
	if err := c.Get(context.Background(), clientKey("system", "session"), latest); err != nil {
		t.Fatal(err)
	}
	if latest.Status.Phase != geassv1alpha1.GeassConsoleExpired {
		t.Fatalf("phase = %s, want Expired", latest.Status.Phase)
	}
}

func ctrlRequest(namespace, name string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}
}
func clientKey(namespace, name string) client.ObjectKey {
	return client.ObjectKey{Namespace: namespace, Name: name}
}
