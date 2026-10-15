package githubapp

import (
	"context"
	"errors"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Outcome is how a report ended, which is what the caller decides what to remember from.
type Outcome string

const (
	// Sent: GitHub took it.
	Sent Outcome = "sent"
	// Skipped: there was nothing to send it with (reporting is off, no GitHub connection), or reports of this
	// kind are being held back after GitHub refused one. Nothing was asked of GitHub. When it is a hold-back,
	// RetryAfter says when it ends, since the report is still owed.
	Skipped Outcome = "skipped"
	// Refused: GitHub said the app may not do this, and will say so again until the owner changes the app. The
	// report is still owed: it is tried again after RefusedBackoff, which is how an approval is found.
	Refused Outcome = "refused"
	// Rejected: GitHub did not accept this particular report (it names a commit it does not know, say). Sending
	// it again would be rejected again.
	Rejected Outcome = "rejected"
	// Failed: GitHub could not be reached, or is struggling. The report may work if it is sent again.
	Failed Outcome = "failed"
	// GaveUp: the report failed enough times in a row that it is left for now. It is still owed, and is tried
	// again after RefusedBackoff.
	GaveUp Outcome = "gave-up"
)

// Done reports whether the report is over, so the state it was about is recorded as reported: it was sent, or
// GitHub will never take it (rejected). A report that was skipped, refused, failed or given up is still owed, and
// recording it as reported would stop it from ever being sent once GitHub can take it.
func (o Outcome) Done() bool {
	switch o {
	case Sent, Rejected:
		return true
	default:
		return false
	}
}

// Result is what Do returns.
type Result struct {
	Outcome Outcome
	Err     error
	// RetryAfter is, for a report that is still owed (failed, refused, given up, or held back), how long to wait
	// before sending it again. A caller asks to be run again after it.
	RetryAfter time.Duration
	// Target is who it was sent to, when it got that far.
	Target *Target
}

// Report is one thing to tell GitHub.
type Report struct {
	Project string
	// Connection is the GeassGitHubConnection to report with. Empty means the project's own.
	Connection string
	Repository string
	// Capability is what kind of report this is (CapabilityStatuses and the others), which decides the
	// permission it needs and what is held back when GitHub refuses it.
	Capability string
	// Key names this report for counting how often it was tried, such as the build and the state it is in.
	Key string
	// Send makes the requests.
	Send func(ctx context.Context, target *Target) error
}

// Do makes a report with everything that keeps reporting from getting in the way of the work it reports on. It
// never returns a reason to fail a build or a deploy: a missing permission is logged once, put on the
// connection as a condition, and held back for RefusedBackoff, and a failure that may pass is tried a few times
// with a gap between, and then dropped.
func (r *Resolver) Do(ctx context.Context, guard *Guard, report Report) Result {
	if guard == nil {
		guard = DefaultGuard()
	}
	log := logf.FromContext(ctx)
	if report.Connection != "" {
		if wait := guard.blockedFor(report.Connection, report.Capability); wait > 0 {
			return Result{Outcome: Skipped, RetryAfter: wait}
		}
	}
	target, err := r.Resolve(ctx, report.Project, report.Connection, report.Repository)
	switch {
	case errors.Is(err, ErrReportingOff), errors.Is(err, ErrNotConnected):
		return Result{Outcome: Skipped, Err: err}
	case err != nil:
		// Reading the cluster, or getting a token from GitHub, failed: it may pass.
		if guard.Once(report.Connection + "|unavailable|" + report.Capability) {
			log.Info("Could not get what a GitHub report needs, will try again", "connection", report.Connection, "report", report.Capability, "error", err.Error())
		}
		return retryLater(guard, report, nil, err)
	}
	if wait := guard.blockedFor(target.Connection, report.Capability); wait > 0 {
		return Result{Outcome: Skipped, RetryAfter: wait, Target: target}
	}
	if allowed, retryAfter := guard.TryAgain(report.Key); !allowed {
		if retryAfter > 0 {
			return Result{Outcome: Failed, RetryAfter: retryAfter, Target: target}
		}
		guard.Delivered(report.Key)
		return Result{Outcome: GaveUp, RetryAfter: RefusedBackoff, Target: target}
	}
	err = report.Send(ctx, target)
	switch {
	case err == nil:
		guard.Delivered(report.Key)
		guard.Unblock(target.Connection, report.Capability)
		if !guard.anyBlocked(target.Connection) {
			if err := r.SetReporting(ctx, target.Connection, true, ""); err != nil {
				log.Error(err, "Could not update the GitHub reporting condition", "connection", target.Connection)
			}
		}
		return Result{Outcome: Sent, Target: target}
	case IsNotPermitted(err):
		guard.Delivered(report.Key)
		guard.Block(target.Connection, report.Capability, RefusedBackoff)
		if guard.Once(target.Connection + "|refused|" + report.Capability) {
			log.Info("GitHub refused a report, so reports of this kind wait until the app is given the permission",
				"connection", target.Connection, "report", report.Capability, "permission", PermissionFor(report.Capability).Label, "error", err.Error())
		}
		if err := r.SetReporting(ctx, target.Connection, false, RefusalMessage(report.Capability, target.AppSlug)); err != nil {
			log.Error(err, "Could not update the GitHub reporting condition", "connection", target.Connection)
		}
		return Result{Outcome: Refused, Err: err, RetryAfter: RefusedBackoff, Target: target}
	case IsTransient(err):
		if guard.Once(target.Connection + "|failed|" + report.Capability) {
			log.Info("Could not report to GitHub, will try again", "connection", target.Connection, "report", report.Capability, "error", err.Error())
		}
		return Result{Outcome: Failed, Err: err, RetryAfter: retryDelay(err), Target: target}
	default:
		guard.Delivered(report.Key)
		if guard.Once(target.Connection + "|rejected|" + report.Capability) {
			log.Info("GitHub did not accept a report", "connection", target.Connection, "report", report.Capability, "error", err.Error())
		}
		return Result{Outcome: Rejected, Err: err, Target: target}
	}
}

// retryLater is a failure before the report was sent: it counts as a try, and the report is dropped when
// there have been enough.
func retryLater(guard *Guard, report Report, target *Target, err error) Result {
	allowed, retryAfter := guard.TryAgain(report.Key)
	if !allowed && retryAfter == 0 {
		guard.Delivered(report.Key)
		return Result{Outcome: GaveUp, Err: err, RetryAfter: RefusedBackoff, Target: target}
	}
	if retryAfter < RetrySpacing {
		retryAfter = RetrySpacing
	}
	return Result{Outcome: Failed, Err: err, RetryAfter: retryAfter, Target: target}
}

// retryDelay is how long to leave a failed report: what GitHub asked for, and never less than RetrySpacing.
func retryDelay(err error) time.Duration {
	if apiErr, ok := AsAPIError(err); ok && apiErr.RetryAfter > RetrySpacing {
		return apiErr.RetryAfter
	}
	return RetrySpacing
}
