package controller

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/webhooks"
)

type recordingEmitter struct {
	mu     sync.Mutex
	events []webhooks.Event
}

func (e *recordingEmitter) Emit(_ string, event webhooks.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *recordingEmitter) types() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.events))
	for _, event := range e.events {
		out = append(out, event.Type)
	}
	return out
}

func webhookApp(annotations map[string]string) *geassv1alpha1.GeassApp {
	return &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace, Annotations: annotations},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "payments", Environment: "production",
			Source: geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "nginx"}},
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
	}
}

func webhookReconciler(t *testing.T, app *geassv1alpha1.GeassApp, emitter webhooks.Emitter) *GeassAppReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	return &GeassAppReconciler{
		Client:   fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).WithStatusSubresource(&geassv1alpha1.GeassApp{}).Build(),
		Scheme:   scheme,
		Webhooks: emitter,
	}
}

func marker(t *testing.T, r *GeassAppReconciler) string {
	t.Helper()
	var got geassv1alpha1.GeassApp
	require.NoError(t, r.Get(context.Background(), client.ObjectKey{Name: "web", Namespace: platform.SystemNamespace}, &got))
	return got.Annotations[platform.AppWebhookStateAnnotation]
}

func TestAnAnnouncedDeployReportsItsOutcomeOnce(t *testing.T) {
	emitter := &recordingEmitter{}
	app := webhookApp(map[string]string{
		platform.AppDeployRevisionAnnotation: "rev-1",
		platform.AppWebhookStateAnnotation:   "rev-1:" + webhooks.EventDeploymentStarted,
	})
	r := webhookReconciler(t, app, emitter)

	r.notifyDeployOutcome(context.Background(), app, webhooks.EventDeploymentSucceeded, "")
	require.Equal(t, []string{"deployment.succeeded"}, emitter.types())
	require.Equal(t, "rev-1:deployment.succeeded", marker(t, r))

	// Every later reconcile of the healthy service must stay quiet.
	var latest geassv1alpha1.GeassApp
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(app), &latest))
	r.notifyDeployOutcome(context.Background(), &latest, webhooks.EventDeploymentSucceeded, "")
	r.notifyDeployOutcome(context.Background(), &latest, webhooks.EventDeploymentSucceeded, "")
	require.Equal(t, []string{"deployment.succeeded"}, emitter.types(), "the outcome is sent once, not on every reconcile")
}

func TestAFailureAfterSuccessIsReportedToo(t *testing.T) {
	emitter := &recordingEmitter{}
	app := webhookApp(map[string]string{
		platform.AppDeployRevisionAnnotation: "rev-1",
		platform.AppWebhookStateAnnotation:   "rev-1:" + webhooks.EventDeploymentSucceeded,
	})
	r := webhookReconciler(t, app, emitter)

	r.notifyDeployOutcome(context.Background(), app, webhooks.EventDeploymentFailed, "boom")

	require.Equal(t, []string{"deployment.failed"}, emitter.types())
	require.Equal(t, "boom", emitter.events[0].Message)
	require.Equal(t, "rev-1:deployment.failed", marker(t, r))
}

func TestDeploysThatWereNeverAnnouncedStayQuiet(t *testing.T) {
	cases := map[string]map[string]string{
		"no annotations":        nil,
		"no webhook state":      {platform.AppDeployRevisionAnnotation: "rev-1"},
		"state from old deploy": {platform.AppDeployRevisionAnnotation: "rev-2", platform.AppWebhookStateAnnotation: "rev-1:deployment.started"},
		"state but no revision": {platform.AppWebhookStateAnnotation: "rev-1:deployment.started"},
	}
	for name, annotations := range cases {
		t.Run(name, func(t *testing.T) {
			emitter := &recordingEmitter{}
			app := webhookApp(annotations)
			r := webhookReconciler(t, app, emitter)
			r.notifyDeployOutcome(context.Background(), app, webhooks.EventDeploymentSucceeded, "")
			require.Empty(t, emitter.types(), "an operator upgrade must not announce services that were deployed long ago")
		})
	}
}

func TestNothingIsSentForUndeployedServicesOrWithoutADispatcher(t *testing.T) {
	annotations := map[string]string{
		platform.AppDeployRevisionAnnotation: "rev-1",
		platform.AppWebhookStateAnnotation:   "rev-1:" + webhooks.EventDeploymentStarted,
	}
	emitter := &recordingEmitter{}
	draft := webhookApp(annotations)
	draft.Spec.Deploy.Enabled = false
	webhookReconciler(t, draft, emitter).notifyDeployOutcome(context.Background(), draft, webhooks.EventDeploymentSucceeded, "")
	require.Empty(t, emitter.types())

	app := webhookApp(annotations)
	r := webhookReconciler(t, app, nil)
	require.NotPanics(t, func() { r.notifyDeployOutcome(context.Background(), app, webhooks.EventDeploymentSucceeded, "") })
}

func TestTwoReconcilesWithTheSameStaleCopyReportTheOutcomeOnlyOnce(t *testing.T) {
	emitter := &recordingEmitter{}
	app := webhookApp(map[string]string{
		platform.AppDeployRevisionAnnotation: "rev-1",
		platform.AppWebhookStateAnnotation:   "rev-1:" + webhooks.EventDeploymentStarted,
	})
	r := webhookReconciler(t, app, emitter)

	// Both reconciles read the same cached version before either writes.
	var first, second geassv1alpha1.GeassApp
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(app), &first))
	require.NoError(t, r.Get(context.Background(), client.ObjectKeyFromObject(app), &second))

	r.notifyDeployOutcome(context.Background(), &first, webhooks.EventDeploymentSucceeded, "")
	r.notifyDeployOutcome(context.Background(), &second, webhooks.EventDeploymentSucceeded, "")

	require.Equal(t, []string{"deployment.succeeded"}, emitter.types(),
		"the reconcile that lost the race must not send a duplicate notification")
}
