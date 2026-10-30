package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// WaitOptions control WaitForApp.
type WaitOptions struct {
	// Timeout ends the wait; zero means ten minutes.
	Timeout time.Duration
	// Interval is how often the status is read; zero means three seconds.
	Interval time.Duration
	// OnStatus is called with each status read, so a caller can show progress.
	OnStatus func(AppStatus)
	// AfterBuild is the service's build as it was just before a caller asked for the build to run again (the
	// Build of a status read first). A status that still shows that finished run is what was there before the
	// request took effect, not the answer, so it is not taken for one until StaleFor has passed. Leave it nil
	// when waiting for a deploy, which has no run to tell apart.
	AfterBuild *BuildStatus
	// StaleFor is how long a status that shows AfterBuild unchanged is taken to be the old one; zero means
	// fifteen seconds. A build restarted by the dashboard shows as not finished within moments, so a run that
	// really did end exactly as the last one (a build that fails the same way at once) is only accepted after
	// this long.
	StaleFor time.Duration
}

// ErrDeployFailed says a service's deploy failed. The status that showed it comes with it.
var ErrDeployFailed = errors.New("the deploy failed")

// defaultStaleFor is how long WaitForApp distrusts a status that still shows the build as it was before.
const defaultStaleFor = 15 * time.Second

// WaitForApp reads a service's status until it is ready, fails, or the wait runs out. A service that is ready
// and has no pending changes is done; one that failed returns ErrDeployFailed with the status that said so.
func (c *Client) WaitForApp(ctx context.Context, name string, options WaitOptions) (AppStatus, error) {
	timeout, interval := options.Timeout, options.Interval
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	if interval <= 0 {
		interval = 3 * time.Second
	}
	staleFor := options.StaleFor
	if staleFor <= 0 {
		staleFor = defaultStaleFor
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	var last AppStatus
	for {
		status, err := c.AppStatus(ctx, name)
		if err != nil {
			if ctx.Err() != nil {
				return last, fmt.Errorf("gave up waiting for %s after %s: %w", name, timeout, ctx.Err())
			}
			return last, err
		}
		last = status
		if options.OnStatus != nil {
			options.OnStatus(status)
		}
		stale := options.AfterBuild != nil && time.Since(started) < staleFor && sameFinishedBuild(status.Build, options.AfterBuild)
		switch {
		case stale:
			// Not read as an answer: it is the build that ran before this one was asked for.
		case status.State == "failed" || (status.Build != nil && status.Build.Phase == "Failed"):
			return status, fmt.Errorf("%w: %s", ErrDeployFailed, failureReason(status))
		case status.Ready && status.PendingChanges == 0 && (status.Build == nil || status.Build.Phase == "Succeeded" || status.Build.Phase == ""):
			return status, nil
		}
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("gave up waiting for %s after %s (it is %s): %w", name, timeout, status.State, ctx.Err())
		case <-time.After(interval):
		}
	}
}

// sameFinishedBuild reports whether a build status is a run that finished and is the very run before describes:
// the same build, ending the same way, at the same time, from the same commit. A build that is going (pending,
// waiting for CI, running) is never the same as one that finished, which is what a restart changes first.
func sameFinishedBuild(now, before *BuildStatus) bool {
	if now == nil || before == nil {
		return false
	}
	switch now.Phase {
	case "Succeeded", "Failed", "Cancelled":
	default:
		return false
	}
	return now.Name == before.Name && now.Phase == before.Phase && now.FailureReason == before.FailureReason &&
		now.SourceRevision == before.SourceRevision && sameTime(now.StartedAt, before.StartedAt) && sameTime(now.CompletedAt, before.CompletedAt)
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func failureReason(status AppStatus) string {
	switch {
	case status.Build != nil && status.Build.FailureReason != "":
		return status.Build.FailureReason
	case status.Message != "":
		return status.Message
	case status.LastAttempt != nil && len(status.LastAttempt.Errors) > 0:
		return status.LastAttempt.Errors[len(status.LastAttempt.Errors)-1].Message
	default:
		return "no reason was given"
	}
}

// AddonWaitOptions control WaitForAddon.
type AddonWaitOptions struct {
	// Timeout ends the wait; zero means fifteen minutes. An add-on that pulls images can take a while on a small cluster.
	Timeout time.Duration
	// Interval is how often the add-ons are read; zero means three seconds.
	Interval time.Duration
	// OnStatus is called with the add-on each time it is read, so a caller can show progress.
	OnStatus func(Addon)
}

// ErrAddonFailed says an add-on is Failed. The add-on, with its reason, comes with it.
var ErrAddonFailed = errors.New("the add-on failed")

// WaitForAddon reads an add-on until it is Ready or Failed, or the wait runs out. An add-on that is Failed has been failing for
// several tries, but K3s goes on trying: the failure is reported, and it can still come up later.
func (c *Client) WaitForAddon(ctx context.Context, name string, options AddonWaitOptions) (Addon, error) {
	timeout, interval := options.Timeout, options.Interval
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	if interval <= 0 {
		interval = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last Addon
	for {
		addons, err := c.Addons(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return last, fmt.Errorf("gave up waiting for %s after %s: %w", name, timeout, ctx.Err())
			}
			return last, err
		}
		addon, found := addons.Find(name)
		if !found {
			return last, fmt.Errorf("the cluster has no add-on %q", name)
		}
		last = addon
		if options.OnStatus != nil {
			options.OnStatus(addon)
		}
		switch addon.State {
		case "Ready":
			return addon, nil
		case "Failed":
			return addon, fmt.Errorf("%w: %s", ErrAddonFailed, addon.Message)
		}
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("gave up waiting for %s after %s (it is %s): %w", name, timeout, addon.State, ctx.Err())
		case <-time.After(interval):
		}
	}
}
