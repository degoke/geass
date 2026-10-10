package controller

import (
	"context"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/webhooks"
)

// notifyDeployOutcome tells the project's webhooks how a deploy ended, once per outcome.
//
// The dashboard marks a deploy as announced when it sends deployment.started. Only announced deploys
// report an outcome, so reconciling an old, healthy service never produces a notification.
func (r *GeassAppReconciler) notifyDeployOutcome(ctx context.Context, app *geassv1alpha1.GeassApp, eventType, message string) {
	if r.Webhooks == nil || !app.Spec.Deploy.Enabled {
		return
	}
	revision := app.Annotations[platform.AppDeployRevisionAnnotation]
	state := app.Annotations[platform.AppWebhookStateAnnotation]
	if revision == "" || !strings.HasPrefix(state, revision+":") {
		return
	}
	next := revision + ":" + eventType
	if state == next {
		return
	}
	// Claim the outcome before sending so a restart or a second reconcile cannot send it twice. The
	// optimistic lock matters: two reconciles can hold the same cached copy, and only the first
	// patch against that version may win.
	patch := client.MergeFromWithOptions(app.DeepCopy(), client.MergeFromWithOptimisticLock{})
	app.Annotations[platform.AppWebhookStateAnnotation] = next
	if err := r.Patch(ctx, app, patch); err != nil {
		if !apierrors.IsConflict(err) {
			logf.FromContext(ctx).Info("Could not record webhook state", "app", app.Name, "error", err.Error())
		}
		return
	}
	r.Webhooks.Emit(app.Spec.Project, webhooks.ServiceEvent(eventType, app, message))
}
