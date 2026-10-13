package migrations

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// defaultLeaseDuration is how long controller-runtime's leader election lets a lease go unrenewed before another
// candidate may take it, when the lease does not say.
const defaultLeaseDuration = 15 * time.Second

// leasePoll is how often WaitForLeaseRelease looks at the lease again.
const leasePoll = 2 * time.Second

// WaitForLeaseRelease waits until no other operator is renewing the leader election lease: it is not there, it
// has no holder, or its holder has not renewed it for longer than the lease lasts. It is what makes "the old
// operator has stopped" something that is checked, not assumed, before a migration changes data that the old
// one would write back over.
//
// It logs to the logger in ctx.
//
// An operator that is still alive after wait is an error and not a reason to go on: the two would be
// reconciling the same objects, which is the thing the wait is there to prevent.
func WaitForLeaseRelease(ctx context.Context, c client.Reader, namespace, name string, wait time.Duration) error {
	return waitForLeaseRelease(ctx, c, namespace, name, wait, time.Now, sleepContext)
}

func waitForLeaseRelease(
	ctx context.Context, c client.Reader, namespace, name string, wait time.Duration,
	now func() time.Time, sleep func(context.Context, time.Duration) error,
) error {
	log := logr.FromContextOrDiscard(ctx)
	deadline := now().Add(wait)
	for {
		holder, renewedAt, ok, err := activeHolder(ctx, c, namespace, name, now())
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if !now().Before(deadline) {
			return fmt.Errorf("the operator %q renewed the leader election lease %s/%s at %s and is still running. "+
				"Migrating data under a running operator would be overwritten by it: stop the old operator, or wait for it to exit",
				holder, namespace, name, renewedAt.Format(time.RFC3339))
		}
		log.Info("Waiting for the previous operator to let go of leader election", "holder", holder)
		if err := sleep(ctx, leasePoll); err != nil {
			return err
		}
	}
}

// activeHolder says who is holding the lease and renewing it, if anyone is.
func activeHolder(ctx context.Context, c client.Reader, namespace, name string, now time.Time) (holder string, renewedAt time.Time, active bool, err error) {
	var lease coordinationv1.Lease
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &lease); err != nil {
		if apierrors.IsNotFound(err) {
			return "", time.Time{}, false, nil
		}
		return "", time.Time{}, false, fmt.Errorf("read the leader election lease: %w", err)
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
		return "", time.Time{}, false, nil
	}
	last := time.Time{}
	switch {
	case lease.Spec.RenewTime != nil:
		last = lease.Spec.RenewTime.Time
	case lease.Spec.AcquireTime != nil:
		last = lease.Spec.AcquireTime.Time
	}
	duration := defaultLeaseDuration
	if lease.Spec.LeaseDurationSeconds != nil && *lease.Spec.LeaseDurationSeconds > 0 {
		duration = time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
	}
	if now.After(last.Add(duration)) {
		return "", time.Time{}, false, nil
	}
	return *lease.Spec.HolderIdentity, last, true, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
