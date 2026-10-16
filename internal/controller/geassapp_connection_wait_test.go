package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	connectionSecret = "stellar-river-connection"
	secretNotFound   = `secret "stellar-river-connection" not found`
	keyNotFound      = `couldn't find key uri in Secret payments-production/stellar-river-connection`
)

// readsFrom is a service that takes its settings from the connection Secret of a database or bucket.
func readsFrom(connection string) *geassv1alpha1.GeassApp {
	app := sleepyApp()
	app.Spec.ScaleToZero = nil
	app.Spec.Env = []corev1.EnvVar{{Name: "DATABASE_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: connection}, Key: "uri"}}}}
	return app
}

// stuckCopy is the copy of a service that Kubernetes cannot create, and says why.
func stuckCopy(labels map[string]string, why string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "hello-abc", Namespace: sleepyNS, Labels: labels},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: "CreateContainerConfigError", Message: why}}}}},
	}
}

func database(ready bool) *geassv1alpha1.GeassDatabase {
	db := &geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{
		Name: "stellar-river", Namespace: platform.SystemNamespace, Annotations: map[string]string{platform.DisplayNameAnnotation: "postgres"}}}
	if ready {
		db.Status.Conditions = platform.SetCondition(nil, platform.ConditionReady, metav1.ConditionTrue, "Ready", "ready")
	}
	return db
}

func readyConditionOf(t *testing.T, r *GeassAppReconciler, app *geassv1alpha1.GeassApp) *metav1.Condition {
	t.Helper()
	var latest geassv1alpha1.GeassApp
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(app), &latest))
	return meta.FindStatusCondition(latest.Status.Conditions, platform.ConditionReady)
}

// gateRuns reconciles a service whose copy is stuck as many times as it takes to give up on one that is failing. A
// reconcile comes with every change to the Deployment and its copies, so that many come in seconds.
func gateRuns(t *testing.T, objects []client.Object, why string) *metav1.Condition {
	t.Helper()
	ctx := context.Background()
	r := sleepyReconciler(t, true)
	app := readsFrom(connectionSecret)
	require.NoError(t, r.Create(ctx, app))
	for _, object := range objects {
		require.NoError(t, r.Create(ctx, object))
	}
	plan, deploy := deployAsleep(t, r, app)
	require.NoError(t, r.Create(ctx, stuckCopy(deploy.Spec.Selector.MatchLabels, why)))
	for range platform.ReconcileFailureThreshold * 3 {
		settled, _, err := r.startGate(ctx, app, sleepyNS, deploy, plan, 1)
		require.NoError(t, err)
		require.True(t, settled)
	}
	return readyConditionOf(t, r, app)
}

// A service deployed with its database cannot read the database's connection Secret until the database is ready,
// which takes minutes on a cluster that has never had one (the Postgres operator is installed first). Its copy waits
// in CreateContainerConfigError for that, which is what is meant to happen, and the service has to say it is waiting,
// not be counted as failing: five counted failures end its provisioning for good, and with it the deployment webhook
// says the deploy failed for a service that goes on to run.
func TestAServiceWaitsForTheDatabaseItReadsFromInsteadOfFailing(t *testing.T) {
	ready := gateRuns(t, []client.Object{database(false)}, secretNotFound)
	require.Equal(t, "Waiting for database postgres", ready.Message)
	require.NotEqual(t, platform.ConditionReasonProvisionFailed, ready.Reason, "a wait is not the end of the service")
}

// The Secret is made when the database is ready, and the copy tries again some seconds after that, so for a while the
// database is ready and the copy still says the Secret is not there. That is the end of the wait and not a failure
// either, and counting it ended the service just as it was about to start.
func TestTheWaitDoesNotEndTheMomentTheDatabaseIsReady(t *testing.T) {
	ready := gateRuns(t, []client.Object{database(true)}, secretNotFound)
	require.Equal(t, "Waiting for database postgres", ready.Message)
	require.NotEqual(t, platform.ConditionReasonProvisionFailed, ready.Reason)
}

func TestAServiceWaitsForTheBucketItReadsFrom(t *testing.T) {
	store := &geassv1alpha1.GeassObjectStore{ObjectMeta: metav1.ObjectMeta{
		Name: "stellar-river", Namespace: platform.SystemNamespace, Annotations: map[string]string{platform.DisplayNameAnnotation: "uploads"}}}
	ready := gateRuns(t, []client.Object{store}, secretNotFound)
	require.Equal(t, "Waiting for bucket uploads", ready.Message)
	require.NotEqual(t, platform.ConditionReasonProvisionFailed, ready.Reason)
}

// What waiting does not mend is still a failure: a reference to a Secret that no database or bucket makes, a key the
// Secret does not have, and a start that fails for any other reason.
func TestAServiceThatWaitsForNothingThatIsComingFailsAsBefore(t *testing.T) {
	for name, tc := range map[string]struct {
		objects []client.Object
		why     string
	}{
		"a Secret nothing makes":                 {nil, secretNotFound},
		"a key the Secret does not have":         {[]client.Object{database(true)}, keyNotFound},
		"a Secret that another database makes":   {[]client.Object{database(false)}, `secret "other-connection" not found`},
		"a start that fails for another reason":  {[]client.Object{database(false)}, "failed to create containerd task"},
		"an empty reason from the copy":          {[]client.Object{database(false)}, ""},
		"a Secret of that name but not a Secret": {[]client.Object{database(false)}, `configmap "stellar-river-connection" not found`},
	} {
		t.Run(name, func(t *testing.T) {
			ready := gateRuns(t, tc.objects, tc.why)
			require.Equal(t, platform.ConditionReasonProvisionFailed, ready.Reason, "after as many tries as a service that is failing gets")
		})
	}
}

func TestAWaitForAConnectionIsNotCountedAsAFailure(t *testing.T) {
	for _, message := range []string{"Waiting for database postgres", "Waiting for bucket uploads"} {
		require.False(t, platform.ReconcileFailureShouldCount(message), message)
	}
	require.True(t, platform.ReconcileFailureShouldCount("The service cannot start: app is CreateContainerConfigError"))
}
