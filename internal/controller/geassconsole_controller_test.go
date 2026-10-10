package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func consoleSessionReconciler(t *testing.T, session *geassv1alpha1.GeassConsoleSession) *GeassConsoleSessionReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	return &GeassConsoleSessionReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(session).WithStatusSubresource(&geassv1alpha1.GeassConsoleSession{}).Build(),
		Scheme: scheme,
	}
}

func TestConsoleSessionBecomesActiveWithStartTimeAndDefaultTimeout(t *testing.T) {
	session := &geassv1alpha1.GeassConsoleSession{
		ObjectMeta: metav1.ObjectMeta{Name: "web-console-1", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassConsoleSessionSpec{App: "web", Pod: "web-abc", Container: "app"},
	}
	r := consoleSessionReconciler(t, session)

	result, err := r.Reconcile(context.Background(), reconcileRequest(session))
	require.NoError(t, err)
	require.Positive(t, result.RequeueAfter, "the session must be revisited when it expires")

	var updated geassv1alpha1.GeassConsoleSession
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(session), &updated))
	require.Equal(t, geassv1alpha1.GeassConsoleActive, updated.Status.Phase)
	require.NotNil(t, updated.Status.StartedAt)
	require.Equal(t, int32(300), updated.Spec.TimeoutSeconds)
}

func TestConsoleSessionExpiresAfterItsTimeout(t *testing.T) {
	started := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	session := &geassv1alpha1.GeassConsoleSession{
		ObjectMeta: metav1.ObjectMeta{Name: "web-console-2", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassConsoleSessionSpec{App: "web", Pod: "web-abc", Container: "app", TimeoutSeconds: 60},
		Status:     geassv1alpha1.GeassConsoleSessionStatus{Phase: geassv1alpha1.GeassConsoleActive, StartedAt: &started},
	}
	r := consoleSessionReconciler(t, session)

	_, err := r.Reconcile(context.Background(), reconcileRequest(session))
	require.NoError(t, err)

	var updated geassv1alpha1.GeassConsoleSession
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(session), &updated))
	require.Equal(t, geassv1alpha1.GeassConsoleExpired, updated.Status.Phase)
	require.NotNil(t, updated.Status.EndedAt)
}
