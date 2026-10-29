package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// draftWithASecret is a service that has not been deployed and has a secret set on it. The dashboard writes a secret's values to <app>-secrets in the
// system namespace and points spec.secretRef at it. The service has nothing running to give them to, and the operator must leave them where they are until
// the deploy: it used to delete them with what it clears for a service that waits to be deployed, so the first deploy of a service that carried a secret
// failed with: Secret "<app>-secrets" not found (found on two real K3s clusters, 2026-10-28).
func draftWithASecret(t *testing.T, mutate func(*geassv1alpha1.GeassApp)) (*GeassAppReconciler, client.Client, *geassv1alpha1.GeassApp) {
	t.Helper()
	project, cluster := readyProjectCluster("payments", "production")
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: platform.SystemNamespace, Finalizers: []string{appFinalizer}},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "payments", Environment: "production", Port: 8080,
			Source:    geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "nginx:1.27-alpine"}},
			SecretRef: &corev1.LocalObjectReference{Name: "worker-secrets"},
		},
	}
	if mutate != nil {
		mutate(app)
	}
	values := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-secrets", Namespace: platform.SystemNamespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"WORKER_SIGNING_KEY": []byte("s3cret")},
	}
	r, c := appTestReconciler(t, app, project, cluster, values)
	return r, c, app
}

func secretValuesStillThere(t *testing.T, c client.Client) bool {
	t.Helper()
	var got corev1.Secret
	err := c.Get(context.Background(), client.ObjectKey{Name: "worker-secrets", Namespace: platform.SystemNamespace}, &got)
	if err != nil {
		require.True(t, client.IgnoreNotFound(err) == nil, "%v", err)
		return false
	}
	require.Equal(t, "s3cret", string(got.Data["WORKER_SIGNING_KEY"]))
	return true
}

func TestADraftKeepsTheSecretValuesSetOnIt(t *testing.T) {
	r, c, app := draftWithASecret(t, nil)

	_, err := r.Reconcile(context.Background(), reconcileRequest(app))

	require.NoError(t, err)
	require.True(t, secretValuesStillThere(t, c), "the draft's secret was deleted by the pass that waits for the deploy, so the deploy could not find it")
}

// A Git service is held at a different place in the pass (it waits for its build), which clears what it made in the same way.
func TestAGitDraftKeepsTheSecretValuesSetOnIt(t *testing.T) {
	r, c, app := draftWithASecret(t, func(app *geassv1alpha1.GeassApp) {
		app.Spec.Source = gitApp().Spec.Source
		app.Spec.Build = gitApp().Spec.Build
	})

	_, err := r.Reconcile(context.Background(), reconcileRequest(app))

	require.NoError(t, err)
	require.True(t, secretValuesStillThere(t, c), "the Git draft's secret was deleted by the pass that waits for the deploy")
}

// The whole order the REST API gives: a secret set on a draft, the draft looked at by the operator, then the deploy. The deploy must find the secret and
// hand it to the service's namespace.
func TestADraftsSecretReachesTheServiceWhenItIsDeployed(t *testing.T) {
	ctx := context.Background()
	r, c, app := draftWithASecret(t, nil)

	_, err := r.Reconcile(ctx, reconcileRequest(app))
	require.NoError(t, err)

	var live geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(app), &live))
	live.Spec.Deploy.Enabled = true
	require.NoError(t, platform.SetLastDeployedSpec(&live))
	platform.ClearPendingChanges(&live.ObjectMeta)
	require.NoError(t, c.Update(ctx, &live))

	_, err = r.Reconcile(ctx, reconcileRequest(app))
	require.NoError(t, err)

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(&live), &live))
	require.NotContains(t, readyConditionMessage(live.Status.Conditions), "not found", "the deploy did not find the secret the draft was given")
	var delivered corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "worker-secret", Namespace: "payments-production"}, &delivered),
		"the draft's secret did not reach the service's namespace")
	require.Equal(t, "s3cret", string(delivered.Data["WORKER_SIGNING_KEY"]))
}

// A service that is moved to another environment is cleared from the namespace it ran in, and goes on running in the new one with the same secret.
func TestMovingAServiceToAnotherEnvironmentKeepsItsSecretValues(t *testing.T) {
	r, c, app := draftWithASecret(t, func(app *geassv1alpha1.GeassApp) {
		app.Spec.Deploy.Enabled = true
		app.Status.TargetNamespace = "payments-staging"
	})

	_, err := r.Reconcile(context.Background(), reconcileRequest(app))

	require.NoError(t, err)
	require.True(t, secretValuesStillThere(t, c), "clearing what the service made in the namespace it left deleted the values it still needs")
}

// Deleting the service still removes its secret values, whichever name spec.secretRef gives them.
func TestDeletingAServiceRemovesTheSecretItsSecretRefNames(t *testing.T) {
	r, c, app := draftWithASecret(t, func(app *geassv1alpha1.GeassApp) {
		app.Spec.SecretRef = &corev1.LocalObjectReference{Name: "worker-secrets"}
		now := metav1.Now()
		app.DeletionTimestamp = &now
	})

	_, err := r.Reconcile(context.Background(), reconcileRequest(app))

	require.NoError(t, err)
	require.False(t, secretValuesStillThere(t, c), "the secret values must not outlive the service")
}
