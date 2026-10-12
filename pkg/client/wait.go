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
}

// ErrDeployFailed says a service's deploy failed. The status that showed it comes with it.
var ErrDeployFailed = errors.New("the deploy failed")

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
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
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
		switch {
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
