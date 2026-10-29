package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/abuse"
	"github.com/degoke/geass/pkg/platform"
)

// AbuseReconciler says on each service, and on its project, what the abuse detection decided about it: the services'
// annotations (pkg/abuse) are what was decided, by the sustained-CPU signal, by a report, or by a platform administrator,
// and this turns them into the AbuseSuspected conditions and the events, once for each change. The suspension itself is
// the service controller's, which keeps a suspended service at zero copies. It works a project at a time, so that a
// project's condition is always worked out from all its services.
//
// With spec.tenancy.abuse unset it removes what the detection wrote: the annotations, so that a suspended service runs
// again, and the conditions.
type AbuseReconciler struct {
	client.Client
	Recorder events.EventRecorder
	// Now is the time, for tests.
	Now func() time.Time
}

func (r *AbuseReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Reconcile brings the services of one project, and the project, in line with what was decided about them.
func (r *AbuseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	project := &geassv1alpha1.GeassProject{}
	if err := r.Get(ctx, req.NamespacedName, project); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	settings, err := abuse.Load(ctx, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	var apps geassv1alpha1.GeassAppList
	if err := r.List(ctx, &apps, client.InNamespace(platform.SystemNamespace)); err != nil {
		return ctrl.Result{}, err
	}
	now := r.now()
	var requeue time.Duration
	var suspected []string
	for i := range apps.Items {
		app := &apps.Items[i]
		if app.Spec.Project != project.Name || !app.DeletionTimestamp.IsZero() {
			continue
		}
		until, err := r.reconcileService(ctx, app, settings != nil, now)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !until.IsZero() && (requeue == 0 || until.Sub(now) < requeue) {
			requeue = max(until.Sub(now), time.Second)
		}
		if suspect, ok := abuse.SuspectOf(app); ok && settings != nil {
			name := platform.ResourceDisplayName(app.ObjectMeta)
			if suspect.Suspended {
				name += " (suspended)"
			}
			suspected = append(suspected, name)
		}
	}
	if err := r.reconcileProject(ctx, project, suspected); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// reconcileService writes one service's condition, and the event of a change. It returns when its review ends, if it has one.
func (r *AbuseReconciler) reconcileService(ctx context.Context, app *geassv1alpha1.GeassApp, on bool, now time.Time) (time.Time, error) {
	_, hasSuspect := abuse.SuspectOf(app)
	_, hasReview := app.Annotations[abuse.AnnotationReview]
	review := abuse.ReviewOf(app)
	ended := review != nil && !now.Before(review.Until)
	if (!on && (hasSuspect || hasReview)) || ended || (hasReview && review == nil) {
		// Off, a review that has ended, or one that cannot be read: what is no longer to be said goes.
		latest, err := abuse.Annotate(ctx, r.Client, app.Name, func(latest *geassv1alpha1.GeassApp) bool {
			changed := false
			for _, key := range []string{abuse.AnnotationSuspect, abuse.AnnotationReview} {
				if _, ok := latest.Annotations[key]; ok && (!on || key == abuse.AnnotationReview) {
					delete(latest.Annotations, key)
					changed = true
				}
			}
			return changed
		})
		if err != nil || latest == nil {
			return time.Time{}, err
		}
		*app = *latest
		hasSuspect, review = false, nil
		if on {
			_, hasSuspect = abuse.SuspectOf(app)
		}
	}

	previous := meta.FindStatusCondition(app.Status.Conditions, abuse.ConditionAbuseSuspected)
	var before *metav1.Condition
	if previous != nil {
		before = previous.DeepCopy()
	}
	var status metav1.ConditionStatus
	var reason, message string
	suspect, _ := abuse.SuspectOf(app)
	switch {
	case hasSuspect:
		status, reason, message = metav1.ConditionTrue, suspect.Reason(), suspect.Message()
	case review != nil:
		status, reason, message = metav1.ConditionFalse, abuse.ReasonReviewed, review.Message()
	}
	if before == nil && status == "" {
		return time.Time{}, nil
	}
	if _, err := platform.PatchStatus(ctx, r.Client, app, func() {
		if status == "" {
			meta.RemoveStatusCondition(&app.Status.Conditions, abuse.ConditionAbuseSuspected)
			return
		}
		app.Status.Conditions = platform.SetCondition(app.Status.Conditions, abuse.ConditionAbuseSuspected, status, reason, message)
	}); err != nil {
		return time.Time{}, err
	}
	r.announce(app, before, status, reason, message)
	if review != nil {
		return review.Until, nil
	}
	return time.Time{}, nil
}

// announce records the event of a change of a service's condition: a warning when it becomes suspected, and a note when
// a suspension is released or a suspicion cleared. Nothing is recorded for a pass that changes nothing.
func (r *AbuseReconciler) announce(app *geassv1alpha1.GeassApp, before *metav1.Condition, status metav1.ConditionStatus, reason, message string) {
	if r.Recorder == nil {
		return
	}
	wasSuspected := before != nil && before.Status == metav1.ConditionTrue
	switch {
	case status == metav1.ConditionTrue && !wasSuspected:
		action := "Detect"
		if reason == abuse.ReasonSuspended {
			action = "Suspend"
		}
		r.Recorder.Eventf(app, nil, corev1.EventTypeWarning, abuse.EventSuspected, action, "%s", message)
	case status == metav1.ConditionTrue && before.Reason == abuse.ReasonSuspended && reason != abuse.ReasonSuspended:
		r.Recorder.Eventf(app, nil, corev1.EventTypeNormal, abuse.EventReleased, "Release", "%s", message)
	case wasSuspected && reason == abuse.ReasonReviewed:
		r.Recorder.Eventf(app, nil, corev1.EventTypeNormal, abuse.EventReviewed, "Review", "%s", message)
	}
}

// reconcileProject writes the project's condition: True while any of its services is suspected, and none otherwise.
func (r *AbuseReconciler) reconcileProject(ctx context.Context, project *geassv1alpha1.GeassProject, suspected []string) error {
	if len(suspected) == 0 && meta.FindStatusCondition(project.Status.Conditions, abuse.ConditionAbuseSuspected) == nil {
		return nil
	}
	slices.Sort(suspected)
	message := fmt.Sprintf("Abuse is suspected of %d of the project's services: %s. A platform administrator reviews each one.", len(suspected), strings.Join(suspected, ", "))
	_, err := platform.PatchStatus(ctx, r.Client, project, func() {
		if len(suspected) == 0 {
			meta.RemoveStatusCondition(&project.Status.Conditions, abuse.ConditionAbuseSuspected)
			return
		}
		project.Status.Conditions = platform.SetCondition(project.Status.Conditions, abuse.ConditionAbuseSuspected, metav1.ConditionTrue, abuse.ReasonServicesSuspected, message)
	})
	return err
}

// SetupWithManager reconciles a project when it appears (and so each one once when the operator starts), when the abuse
// annotations of one of its services change or a service that had them goes, and every project when the setting is turned
// on, off or changed.
func (r *AbuseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	appProject := func(_ context.Context, object client.Object) []ctrl.Request {
		app, ok := object.(*geassv1alpha1.GeassApp)
		if !ok || app.Spec.Project == "" {
			return nil
		}
		return []ctrl.Request{{NamespacedName: client.ObjectKey{Name: app.Spec.Project, Namespace: platform.SystemNamespace}}}
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named("abuse").
		For(&geassv1alpha1.GeassProject{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc:  func(event.CreateEvent) bool { return true },
			UpdateFunc:  func(event.UpdateEvent) bool { return false },
			DeleteFunc:  func(event.DeleteEvent) bool { return false },
			GenericFunc: func(event.GenericEvent) bool { return false },
		})).
		Watches(&geassv1alpha1.GeassApp{}, handler.EnqueueRequestsFromMapFunc(appProject), builder.WithPredicates(abuseAnnotationsChanged())).
		Watches(&geassv1alpha1.GeassPlatformConfig{}, handler.EnqueueRequestsFromMapFunc(r.allProjects), builder.WithPredicates(abuseSettingChanged())).
		Complete(r)
}

func (r *AbuseReconciler) allProjects(ctx context.Context, _ client.Object) []ctrl.Request {
	var projects geassv1alpha1.GeassProjectList
	if err := r.List(ctx, &projects, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(projects.Items))
	for i := range projects.Items {
		requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&projects.Items[i])})
	}
	return requests
}

// abuseMarks are what of a service the reconciler works from: its two annotations, and whether it carries the condition.
func abuseMarks(object client.Object) map[string]string {
	marks := map[string]string{}
	for _, key := range []string{abuse.AnnotationSuspect, abuse.AnnotationReview} {
		if value, ok := object.GetAnnotations()[key]; ok {
			marks[key] = value
		}
	}
	if app, ok := object.(*geassv1alpha1.GeassApp); ok && meta.FindStatusCondition(app.Status.Conditions, abuse.ConditionAbuseSuspected) != nil {
		marks[abuse.ConditionAbuseSuspected] = ""
	}
	return marks
}

// abuseAnnotationsChanged lets through a service whose abuse annotations changed, one that appears or goes with any, and
// one moved to another project with any. The condition the reconciler writes is not a change: only its going is, which
// happens when a service is deleted.
func abuseAnnotationsChanged() predicate.Predicate {
	annotations := func(object client.Object) map[string]string {
		marks := abuseMarks(object)
		delete(marks, abuse.ConditionAbuseSuspected)
		return marks
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return len(abuseMarks(e.Object)) > 0 },
		DeleteFunc:  func(e event.DeleteEvent) bool { return len(abuseMarks(e.Object)) > 0 },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			before, after := annotations(e.ObjectOld), annotations(e.ObjectNew)
			if !maps.Equal(before, after) {
				return true
			}
			// A service deleted with a finalizer is an update first: the project's condition must stop naming it.
			beginsGoing := e.ObjectOld.GetDeletionTimestamp().IsZero() && !e.ObjectNew.GetDeletionTimestamp().IsZero()
			oldApp, okOld := e.ObjectOld.(*geassv1alpha1.GeassApp)
			newApp, okNew := e.ObjectNew.(*geassv1alpha1.GeassApp)
			moved := okOld && okNew && oldApp.Spec.Project != newApp.Spec.Project
			return len(abuseMarks(e.ObjectNew)) > 0 && (beginsGoing || moved)
		},
	}
}

// abuseSettingChanged lets through the platform configuration's spec.tenancy.abuse appearing, going or changing.
func abuseSettingChanged() predicate.Predicate {
	setting := func(object client.Object) *geassv1alpha1.GeassTenantAbuse {
		config, ok := object.(*geassv1alpha1.GeassPlatformConfig)
		if !ok || config.Name != platform.HAReadinessName || config.Namespace != platform.SystemNamespace {
			return nil
		}
		return abuse.SpecOf(config)
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return setting(e.Object) != nil },
		DeleteFunc:  func(e event.DeleteEvent) bool { return setting(e.Object) != nil },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return !equality.Semantic.DeepEqual(setting(e.ObjectOld), setting(e.ObjectNew))
		},
	}
}
