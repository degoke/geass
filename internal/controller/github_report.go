package controller

import (
	"context"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

// Reporting to GitHub is a side effect of the work the controllers do, and never a part of it: a refusal, a
// slow answer or a missing permission is logged and put on the connection, and the build or the deploy goes on
// as if there were nobody to tell. What decides whether to send is the state recorded on the object (an
// annotation), so each state is sent once, and what protects GitHub from a retry storm is githubapp.Guard.

const (
	// buildStatusContext names the commit status of a build.
	buildStatusContext = "geass/build"
	// kanikoJobFailedReason is the failure reason of a build whose Job failed: the code did not build.
	kanikoJobFailedReason = "kaniko Job failed"
)

// reportRetries remembers which objects owe GitHub a report that is worth trying again, so that the reconcile that
// found one can ask to run again after a pause, even when the object has nothing else left to do.
type reportRetries struct {
	mu    sync.Mutex
	after map[types.NamespacedName]time.Duration
}

func (r *reportRetries) want(key types.NamespacedName, after time.Duration) {
	if after <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.after == nil {
		r.after = map[types.NamespacedName]time.Duration{}
	}
	if current, ok := r.after[key]; !ok || after < current {
		r.after[key] = after
	}
}

// take removes and returns the retry wanted for an object, if there is one.
func (r *reportRetries) take(key types.NamespacedName) (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	after, ok := r.after[key]
	delete(r.after, key)
	return after, ok
}

// apply puts the wanted retry into a reconcile's result, unless the result already asks to run sooner.
func (r *reportRetries) apply(key types.NamespacedName, result ctrl.Result) ctrl.Result {
	after, ok := r.take(key)
	if ok && (result.RequeueAfter == 0 || after < result.RequeueAfter) {
		result.RequeueAfter = after
	}
	return result
}

// reportTimeout bounds one pass of reports to GitHub that runs on its own.
const reportTimeout = 2 * time.Minute

// reportDispatcher runs the reports to GitHub of one controller. A report is several requests to GitHub, each of which
// can take its whole timeout, and a reconcile that waits for them holds a worker that every other object of the
// controller is waiting for. So in the operator (async) a report runs on its own, one pass at a time per object, and
// the object is reconciled again when the report wants to be tried again, which is what the reconcile's own requeue
// did when the report ran inside it. Without async (the tests) a report runs inside the reconcile, as it always did.
type reportDispatcher struct {
	mu      sync.Mutex
	running map[types.NamespacedName]bool
	pending map[types.NamespacedName]bool
	events  chan event.GenericEvent
}

func (d *reportDispatcher) initLocked() {
	if d.events == nil {
		d.events = make(chan event.GenericEvent, 256)
		d.running = map[types.NamespacedName]bool{}
		d.pending = map[types.NamespacedName]bool{}
	}
}

// channel is where objects that are to be reconciled again are sent, for the controller to watch.
func (d *reportDispatcher) channel() <-chan event.GenericEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.initLocked()
	return d.events
}

// dispatch runs a report for an object. With async it returns at once. While a pass for the object is still running a
// new one is not started: the object is reconciled again when it ends, and reports what is true by then.
func (d *reportDispatcher) dispatch(ctx context.Context, async bool, key types.NamespacedName, retries *reportRetries, run func(context.Context)) {
	if !async {
		run(ctx)
		return
	}
	d.mu.Lock()
	d.initLocked()
	if d.running[key] {
		d.pending[key] = true
		d.mu.Unlock()
		return
	}
	d.running[key] = true
	d.mu.Unlock()
	detached := context.WithoutCancel(ctx)
	go func() {
		runCtx, cancel := context.WithTimeout(detached, reportTimeout)
		defer cancel()
		run(runCtx)
		after, wanted := retries.take(key)
		d.mu.Lock()
		again := d.pending[key]
		delete(d.running, key)
		delete(d.pending, key)
		d.mu.Unlock()
		switch {
		case again:
			d.wake(key, 0)
		case wanted:
			d.wake(key, after)
		}
	}()
}

// wake has the controller reconcile an object again, after a pause when after is positive.
func (d *reportDispatcher) wake(key types.NamespacedName, after time.Duration) {
	send := func() {
		object := &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
		select {
		case d.events <- event.GenericEvent{Object: object}:
		default:
			// Full: the controller is far behind, and the object is reconciled when anything about it changes.
		}
	}
	if after <= 0 {
		send()
		return
	}
	time.AfterFunc(after, send)
}

// recordAnnotation writes one annotation on the newest copy of an object, which is how a reconcile remembers what it
// has told GitHub. It changes nothing when the annotation already has the value.
func recordAnnotation(ctx context.Context, c client.Client, obj client.Object, key, value string) error {
	latest, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return nil
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), latest); err != nil {
		return client.IgnoreNotFound(err)
	}
	annotations := latest.GetAnnotations()
	if annotations[key] == value {
		return nil
	}
	patch := client.MergeFrom(latest.DeepCopyObject().(client.Object))
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[key] = value
	latest.SetAnnotations(annotations)
	return c.Patch(ctx, latest, patch)
}

// ---- builds ----

func (r *GeassBuildReconciler) githubResolver() *githubapp.Resolver {
	return &githubapp.Resolver{Client: r.Client, HTTP: r.HTTPClient, Namespace: platform.SystemNamespace}
}

// buildCommitStatus is how a build looks on its commit: the state GitHub shows and the words next to it. It is
// not ok for a build with nothing to report on (no repository, or a commit that is not known yet) and for a
// phase that is not a change worth reporting (the build was only put back to the start).
func buildCommitStatus(build *geassv1alpha1.GeassBuild) (state, description string, ok bool) {
	if build.Spec.Repository == "" || build.Status.SourceRevision == "" {
		return "", "", false
	}
	switch build.Status.Phase {
	case geassv1alpha1.GeassBuildWaitingForCI:
		return githubapp.StatusPending, "Waiting for CI checks to pass before building", true
	case geassv1alpha1.GeassBuildRunning:
		return githubapp.StatusPending, "Building the image", true
	case geassv1alpha1.GeassBuildSucceeded:
		if build.Status.ImageDigest == "" {
			return "", "", false
		}
		return githubapp.StatusSuccess, "Built the image", true
	case geassv1alpha1.GeassBuildFailed:
		reason := strings.TrimSpace(build.Status.FailureReason)
		// The code is what failed when the build did, or the CI checks that had to pass first. Anything else (no
		// registry to push to, GitHub or the cluster not answering) is Geass or its setup, which GitHub calls an error.
		if build.Status.CIState == "failure" || reason == kanikoJobFailedReason {
			if reason == kanikoJobFailedReason {
				reason = "The build failed"
			}
			return githubapp.StatusFailure, firstNonEmpty(reason, "The build failed"), true
		}
		return githubapp.StatusError, firstNonEmpty(reason, "The build could not run"), true
	case geassv1alpha1.GeassBuildCancelled:
		return githubapp.StatusError, "The build was cancelled", true
	default:
		return "", "", false
	}
}

// buildStatusKey is what a build has told GitHub: the commit and the phase. Two phases that GitHub shows as the
// same state still differ, so the description (waiting for CI, then building) follows the build.
func buildStatusKey(build *geassv1alpha1.GeassBuild) string {
	return build.Status.SourceRevision + ":" + string(build.Status.Phase)
}

// reportTrackedBuild sends a build's state to GitHub when the build is one Geass has been reporting on and its
// last report did not get through. A build that was never reported on is left alone here: it starts to be
// reported on when its phase changes (reportBuild), and not because the operator started and found it.
func (r *GeassBuildReconciler) reportTrackedBuild(ctx context.Context, build *geassv1alpha1.GeassBuild) {
	if build.Annotations[platform.BuildGitHubStatusAnnotation] == "" {
		return
	}
	r.dispatchReport(ctx, build, func(ctx context.Context, build *geassv1alpha1.GeassBuild) { r.reportBuild(ctx, build, false) })
}

// dispatchReport runs a report for a build off the reconcile, on a copy of the build as it is now.
func (r *GeassBuildReconciler) dispatchReport(ctx context.Context, build *geassv1alpha1.GeassBuild, run func(context.Context, *geassv1alpha1.GeassBuild)) {
	snapshot := build.DeepCopy()
	r.reports.dispatch(ctx, r.AsyncReports, client.ObjectKeyFromObject(build), &r.retries, func(ctx context.Context) { run(ctx, snapshot) })
}

// reportBuild tells GitHub how a build is going, as a commit status on the commit it builds. changed is the
// build having just moved to this phase. The state that was last reported is on the build, so a state is sent
// once, and nothing is asked of GitHub when it has not changed.
func (r *GeassBuildReconciler) reportBuild(ctx context.Context, build *geassv1alpha1.GeassBuild, changed bool) {
	state, description, ok := buildCommitStatus(build)
	if !ok {
		return
	}
	recorded := build.Annotations[platform.BuildGitHubStatusAnnotation]
	if recorded == "" && !changed {
		return
	}
	key := buildStatusKey(build)
	if recorded == key {
		return
	}
	connection := ""
	if build.Spec.ConnectionRef != nil {
		connection = build.Spec.ConnectionRef.Name
	}
	statusContext := r.buildStatusContext(ctx, build)
	result := r.githubResolver().Do(ctx, r.guard(), githubapp.Report{
		Project:    build.Spec.Project,
		Connection: connection,
		Repository: build.Spec.Repository,
		Capability: githubapp.CapabilityStatuses,
		Key:        "build/" + build.Namespace + "/" + build.Name + "/" + key,
		Send: func(ctx context.Context, target *githubapp.Target) error {
			return target.CreateCommitStatus(ctx, build.Status.SourceRevision, githubapp.CommitStatus{
				State:       state,
				Context:     statusContext,
				Description: description,
				TargetURL:   githubapp.DashboardServiceURL(target.DashboardURL, build.Spec.Project, string(build.Spec.Environment), build.Spec.App),
			})
		},
	})
	log := logf.FromContext(ctx)
	if result.Outcome.Done() {
		if err := recordAnnotation(ctx, r.Client, build, platform.BuildGitHubStatusAnnotation, key); err != nil {
			log.Error(err, "Could not record the commit status that was reported", "build", build.Name)
		}
	}
	switch result.Outcome {
	case githubapp.Sent:
		log.V(1).Info("Reported the build to GitHub", "build", build.Name, "state", state, "commit", build.Status.SourceRevision)
	default:
		// Failed, refused, held back or given up: the report is still owed, and RetryAfter says when to try it again.
		// Nothing is recorded as reported, so it is not lost when GitHub can take it again.
		if result.RetryAfter > 0 {
			if recorded == "" {
				// The first state of a build did not get through, so there is nothing recorded to find the build by again.
				// This is what marks it as one whose reports are owed, and the retry looks for it.
				if err := recordAnnotation(ctx, r.Client, build, platform.BuildGitHubStatusAnnotation, buildStatusOwed); err != nil {
					log.Error(err, "Could not record that a build owes GitHub a status", "build", build.Name)
				}
			}
			r.retries.want(client.ObjectKeyFromObject(build), result.RetryAfter)
		}
	}
}

// buildStatusOwed is the annotation of a build that has not been able to tell GitHub anything yet. It is never
// equal to a state, so the build is always found to owe its current one.
const buildStatusOwed = "owed"

func (r *GeassBuildReconciler) guard() *githubapp.Guard {
	if r.GitHubGuard != nil {
		return r.GitHubGuard
	}
	return githubapp.DefaultGuard()
}

// buildStatusContext is the name of the commit status for a build: geass/build/<service>, as the dashboard names the
// service. It is the same for the life of the service. A name that depended on how many other services build the same
// repository would change the day a second one is added, and a team that requires the status under branch protection
// would find every pull request waiting for a status that is never sent. One name for all of a monorepo's services would
// let whichever reported last hide the others, so each has its own.
func (r *GeassBuildReconciler) buildStatusContext(ctx context.Context, build *geassv1alpha1.GeassBuild) string {
	name := build.Spec.App
	var app geassv1alpha1.GeassApp
	if err := r.Get(ctx, client.ObjectKey{Name: build.Spec.App, Namespace: build.Namespace}, &app); err == nil {
		name = platform.ResourceDisplayName(app.ObjectMeta)
	}
	return buildStatusContext + "/" + name
}
