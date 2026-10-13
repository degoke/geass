package migrations

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const leaseName = "1dcf5cc9.geass.dev"

// clock is a time that only moves when the test sleeps.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }
func (c *clock) sleep(_ context.Context, d time.Duration) error {
	c.t = c.t.Add(d)
	return nil
}

func lease(holder string, renewed time.Time, seconds int32) *coordinationv1.Lease {
	renew := metav1.NewMicroTime(renewed)
	l := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: leaseName},
		Spec:       coordinationv1.LeaseSpec{RenewTime: &renew, LeaseDurationSeconds: &seconds},
	}
	if holder != "" {
		l.Spec.HolderIdentity = &holder
	}
	return l
}

func waitWith(t *testing.T, c client.Reader, clk *clock, wait time.Duration) error {
	t.Helper()
	return waitForLeaseRelease(context.Background(), c, ns, leaseName, wait, clk.now, clk.sleep)
}

func TestNoLeaseMeansNoOperatorToWaitFor(t *testing.T) {
	clk := &clock{t: time.Now()}
	require.NoError(t, waitWith(t, fake.NewClientBuilder().Build(), clk, time.Minute))
}

func TestALeaseWithNoHolderIsFree(t *testing.T) {
	clk := &clock{t: time.Now()}
	c := fake.NewClientBuilder().WithObjects(lease("", clk.t, 15)).Build()
	require.NoError(t, waitWith(t, c, clk, time.Minute))
}

func TestALeaseThatHasStoppedBeingRenewedIsFree(t *testing.T) {
	clk := &clock{t: time.Now()}
	c := fake.NewClientBuilder().WithObjects(lease("old-pod_abc", clk.t.Add(-40*time.Second), 15)).Build()
	require.NoError(t, waitWith(t, c, clk, time.Minute))
	require.Equal(t, clk.t, clk.now(), "it did not need to wait")
}

func TestItWaitsForTheLeaseOfAnOperatorThatWasJustStopped(t *testing.T) {
	clk := &clock{t: time.Now()}
	// The old operator renewed 5 seconds ago and was told to stop: its lease runs out in 10 more.
	c := fake.NewClientBuilder().WithObjects(lease("old-pod_abc", clk.t.Add(-5*time.Second), 15)).Build()
	start := clk.t
	require.NoError(t, waitWith(t, c, clk, time.Minute))
	waited := clk.t.Sub(start)
	require.GreaterOrEqual(t, waited, 10*time.Second, "not before the lease is over")
	require.Less(t, waited, 15*time.Second)
}

func TestAnOperatorThatKeepsRenewingIsAnErrorNotAReasonToGoOn(t *testing.T) {
	clk := &clock{t: time.Now()}
	l := lease("live-pod_abc", clk.t, 15)
	c := fake.NewClientBuilder().WithObjects(l).Build()

	// Renews every time the clock moves: the holder is alive.
	renewing := func(ctx context.Context, d time.Duration) error {
		clk.t = clk.t.Add(d)
		renew := metav1.NewMicroTime(clk.t)
		l.Spec.RenewTime = &renew
		return c.Update(ctx, l)
	}
	err := waitForLeaseRelease(context.Background(), c, ns, leaseName, 20*time.Second, clk.now, renewing)
	require.Error(t, err)
	require.Contains(t, err.Error(), "live-pod_abc")
	require.Contains(t, err.Error(), "still running")
}
