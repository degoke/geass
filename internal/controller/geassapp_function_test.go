package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func functionApp(runtimeName, code string) *geassv1alpha1.GeassApp {
	return &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "hello", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "payments", Environment: "production", Port: 9000,
			Source: geassv1alpha1.GeassAppSource{Function: &geassv1alpha1.GeassAppFunctionSource{Runtime: runtimeName, Code: code}},
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
	}
}

func functionReconciler(t *testing.T) *GeassAppReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	return &GeassAppReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme}
}

func TestFunctionDeploymentRunsTheCodeOnItsRuntime(t *testing.T) {
	ctx := context.Background()
	r := functionReconciler(t)
	app := functionApp("bun", "Bun.serve({ port: 1 })")
	const ns = "payments-production"

	require.NoError(t, r.reconcileFunctionCode(ctx, app, ns))
	require.NoError(t, r.reconcileDeployment(ctx, app, ns))

	var cm corev1.ConfigMap
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello-function", Namespace: ns}, &cm))
	require.Equal(t, map[string]string{"index.ts": "Bun.serve({ port: 1 })"}, cm.Data)

	var deploy appsv1.Deployment
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: ns}, &deploy))
	pod := deploy.Spec.Template.Spec
	container := pod.Containers[0]
	require.Equal(t, "oven/bun:1-alpine", container.Image)
	require.Equal(t, []string{"bun", "run", "/function/index.ts"}, container.Command)
	require.Contains(t, container.Env, corev1.EnvVar{Name: "PORT", Value: "9000"}, "the code is told which port to serve on")
	require.Contains(t, container.VolumeMounts, corev1.VolumeMount{Name: "function", MountPath: "/function", ReadOnly: true})
	require.NotNil(t, pod.AutomountServiceAccountToken)
	require.False(t, *pod.AutomountServiceAccountToken, "user code gets no Kubernetes API token")
	require.NotEmpty(t, deploy.Spec.Template.Annotations[platform.AnnotationFunctionHash])
}

func TestEditingFunctionCodeRollsThePods(t *testing.T) {
	ctx := context.Background()
	r := functionReconciler(t)
	const ns = "payments-production"
	hash := func() string {
		var deploy appsv1.Deployment
		require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: ns}, &deploy))
		return deploy.Spec.Template.Annotations[platform.AnnotationFunctionHash]
	}

	app := functionApp("node", "console.log(1)")
	require.NoError(t, r.reconcileFunctionCode(ctx, app, ns))
	require.NoError(t, r.reconcileDeployment(ctx, app, ns))
	before := hash()

	app.Spec.Source.Function.Code = "console.log(2)"
	require.NoError(t, r.reconcileFunctionCode(ctx, app, ns))
	require.NoError(t, r.reconcileDeployment(ctx, app, ns))
	require.NotEqual(t, before, hash(), "a code edit must change the pod template or the running pods never pick it up")

	var cm corev1.ConfigMap
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello-function", Namespace: ns}, &cm))
	require.Equal(t, "console.log(2)", cm.Data["index.mjs"])
}

func TestFunctionKeepsAUserDefinedPortAndCommand(t *testing.T) {
	ctx := context.Background()
	r := functionReconciler(t)
	app := functionApp("python", "print(1)")
	app.Spec.Env = []corev1.EnvVar{{Name: "PORT", Value: "7000"}}
	app.Spec.Build.Command = []string{"python", "-m", "uvicorn"}
	const ns = "payments-production"

	require.NoError(t, r.reconcileFunctionCode(ctx, app, ns))
	require.NoError(t, r.reconcileDeployment(ctx, app, ns))

	var deploy appsv1.Deployment
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: ns}, &deploy))
	container := deploy.Spec.Template.Spec.Containers[0]
	require.Equal(t, []string{"python", "-m", "uvicorn"}, container.Command)
	count := 0
	for _, env := range container.Env {
		if env.Name == "PORT" {
			count++
			require.Equal(t, "7000", env.Value)
		}
	}
	require.Equal(t, 1, count, "PORT must not be defined twice")
}

func TestFunctionConfigMapIsRemovedWhenTheServiceStopsBeingAFunction(t *testing.T) {
	ctx := context.Background()
	r := functionReconciler(t)
	app := functionApp("bun", "x")
	const ns = "payments-production"
	require.NoError(t, r.reconcileFunctionCode(ctx, app, ns))

	app.Spec.Source = geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "nginx"}}
	require.NoError(t, r.reconcileFunctionCode(ctx, app, ns))

	var cm corev1.ConfigMap
	require.Error(t, r.Get(ctx, client.ObjectKey{Name: "hello-function", Namespace: ns}, &cm))
}

func TestValidateFunctionSources(t *testing.T) {
	function := func(runtimeName, code string) geassv1alpha1.GeassAppSource {
		return geassv1alpha1.GeassAppSource{Function: &geassv1alpha1.GeassAppFunctionSource{Runtime: runtimeName, Code: code}}
	}
	cases := []struct {
		name    string
		source  geassv1alpha1.GeassAppSource
		wantErr bool
	}{
		{name: "valid function", source: function("bun", "x")},
		{name: "unknown runtime", source: function("ruby", "x"), wantErr: true},
		{name: "empty code", source: function("bun", ""), wantErr: true},
		{name: "function and image", source: geassv1alpha1.GeassAppSource{
			Function: &geassv1alpha1.GeassAppFunctionSource{Runtime: "bun", Code: "x"},
			Image:    &geassv1alpha1.GeassAppImageSource{Image: "nginx"},
		}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := &geassv1alpha1.GeassApp{Spec: geassv1alpha1.GeassAppSpec{Source: tc.source, Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true}}}
			require.Equal(t, tc.wantErr, validateAppSource(app) != nil)
		})
	}
}

func TestServiceVariablesAndSecretsAreEnvironmentVariables(t *testing.T) {
	ctx := context.Background()
	r := functionReconciler(t)
	app := functionApp("bun", "x")
	app.Spec.ConfigData = map[string]string{"APP_ENV": "staging"}
	app.Spec.SecretRef = &corev1.LocalObjectReference{Name: "hello-secrets"}
	const ns = "payments-production"

	require.NoError(t, r.reconcileDeployment(ctx, app, ns))

	var deploy appsv1.Deployment
	require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: ns}, &deploy))
	envFrom := deploy.Spec.Template.Spec.Containers[0].EnvFrom
	require.Contains(t, envFrom, corev1.EnvFromSource{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "hello-config"}}},
		"saved variables must reach the container as environment variables, not just as files")
	require.Contains(t, envFrom, corev1.EnvFromSource{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "hello-secret"}}})
}

func TestEachDeployRestartsThePods(t *testing.T) {
	ctx := context.Background()
	r := functionReconciler(t)
	app := functionApp("bun", "x")
	app.Annotations = map[string]string{platform.AppDeployRevisionAnnotation: "revision-1"}
	const ns = "payments-production"
	revision := func() string {
		var deploy appsv1.Deployment
		require.NoError(t, r.Get(ctx, client.ObjectKey{Name: "hello", Namespace: ns}, &deploy))
		return deploy.Spec.Template.Annotations[platform.AppDeployRevisionAnnotation]
	}

	require.NoError(t, r.reconcileDeployment(ctx, app, ns))
	require.Equal(t, "revision-1", revision())

	// Only a variable changed, so the image and spec are identical, but the deploy is a new revision.
	app.Annotations[platform.AppDeployRevisionAnnotation] = "revision-2"
	require.NoError(t, r.reconcileDeployment(ctx, app, ns))
	require.Equal(t, "revision-2", revision(), "without a new pod template a deploy would not restart anything")
}
