package abuse

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

func annotated(annotations map[string]string) *metav1.ObjectMeta {
	return &metav1.ObjectMeta{Name: "web", Annotations: annotations}
}

func TestASuspicionIsKeptOnTheServiceAndReadBack(t *testing.T) {
	suspect := Suspect{
		Source: SourceCPU, Since: start, DetectedAt: start.Add(30 * time.Minute),
		AveragePercent: 97.4, MinPercent: 91, ThresholdPercent: 90, WindowMinutes: 30, Samples: 31, Traffic: TrafficQuiet,
		Suspended: true,
	}
	object := annotated(map[string]string{AnnotationSuspect: suspect.Encode()})
	read, ok := SuspectOf(object)
	require.True(t, ok)
	require.Equal(t, suspect, *read)
	require.True(t, Suspended(object))
	require.Equal(t, ReasonSuspended, read.Reason())
	require.Equal(t, "Abuse is suspected. Its copies used 97.4% on average (never below 91.0%) of the CPU they are allowed, at or above 90%, "+
		"for 30 minutes from 2026-10-27T09:00:00Z, while it was sent few requests. That is a signal, not proof: batch work and load tests look the same. "+
		"It is scaled to zero until a platform administrator releases or clears it; nothing was deleted.", read.Message())

	read.Suspended = false
	released := start.Add(time.Hour)
	read.ReleasedAt = &released
	require.Equal(t, ReasonSustainedCPU, read.Reason())
	require.Contains(t, read.Message(), "let it run again on 2026-10-27T10:00:00Z")
}

func TestAServiceWithoutASuspicionIsNotSuspendedAndAnUnreadableOneIs(t *testing.T) {
	_, ok := SuspectOf(annotated(nil))
	require.False(t, ok)
	require.False(t, Suspended(annotated(map[string]string{"other": "x"})))

	for _, raw := range []string{"{not json", `{"source":"guess"}`, `{}`} {
		read, ok := SuspectOf(annotated(map[string]string{AnnotationSuspect: raw}))
		require.True(t, ok, raw)
		require.True(t, read.Unreadable(), raw)
		require.True(t, read.Suspended, "%s: better stopped than let run on a record nobody can read", raw)
		require.Contains(t, read.Message(), "cannot be read")
	}
}

func TestAReportSaysWhoReportedWhat(t *testing.T) {
	read, ok := SuspectOf(annotated(map[string]string{AnnotationSuspect: Suspect{
		Source: SourceReport, Reporter: "falco", Detail: "Detect outbound connections to common miner pool ports", Since: start, DetectedAt: start,
	}.Encode()}))
	require.True(t, ok)
	require.Equal(t, ReasonReported, read.Reason())
	require.Equal(t, "Abuse is suspected. A detector outside Geass (falco) reported it: Detect outbound connections to common miner pool ports. "+
		"It is left running until a platform administrator reviews it.", read.Message())
}

func TestAReviewIsReadBackAndOneWithoutAnEndIsNone(t *testing.T) {
	review := Review{At: start, Until: start.Add(30 * 24 * time.Hour), By: "root", Note: "a nightly render", Cleared: SourceCPU}
	read := ReviewOf(annotated(map[string]string{AnnotationReview: review.Encode()}))
	require.Equal(t, &review, read)
	require.Equal(t, "A platform administrator cleared the service on 2026-10-27T09:00:00Z; the sustained-CPU signal leaves it alone until 2026-11-26T09:00:00Z.", read.Message())
	require.Nil(t, ReviewOf(annotated(map[string]string{AnnotationReview: `{"by":"root"}`})))
	require.Nil(t, ReviewOf(annotated(map[string]string{AnnotationReview: `nope`})))
	require.Nil(t, ReviewOf(annotated(nil)))
}

func TestTheStateIsWrittenOnlyWhenItChangesAndReadBack(t *testing.T) {
	ctx := context.Background()
	c := fakeClient(t)
	state, configMap, err := LoadState(ctx, c)
	require.NoError(t, err)
	require.Nil(t, configMap)
	require.Empty(t, state.Runs)

	state = State{Sampled: start, CPU: Availability{Available: true}, Runs: map[string]Run{"web": {Since: start, Last: start, Samples: 1, SumPermille: 950, MinPermille: 950, Pods: "a"}}}
	require.NoError(t, StoreState(ctx, c, state, nil))
	read, configMap, err := LoadState(ctx, c)
	require.NoError(t, err)
	require.Equal(t, state, read)
	require.Equal(t, platform.ManagedByValue, configMap.Labels[platform.LabelManagedBy])

	version := configMap.ResourceVersion
	require.NoError(t, StoreState(ctx, c, read, configMap))
	_, again, err := LoadState(ctx, c)
	require.NoError(t, err)
	require.Equal(t, version, again.ResourceVersion, "nothing changed, nothing written")

	stale := configMap.DeepCopy()
	read.Sampled = start.Add(time.Minute)
	require.NoError(t, StoreState(ctx, c, read, configMap))
	read.Sampled = start.Add(2 * time.Minute)
	require.Error(t, StoreState(ctx, c, read, stale), "a write from a version that has moved on is refused")

	require.NoError(t, DeleteState(ctx, c))
	require.NoError(t, DeleteState(ctx, c), "and again, when there is none")
	require.True(t, client.IgnoreNotFound(c.Get(ctx, client.ObjectKey{Name: StateName, Namespace: platform.SystemNamespace}, &corev1.ConfigMap{})) == nil)
}

func TestAStateThatCannotBeReadIsNone(t *testing.T) {
	c := fakeClient(t, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: StateName, Namespace: platform.SystemNamespace},
		Data:       map[string]string{stateKey: "{broken"},
	})
	state, configMap, err := LoadState(context.Background(), c)
	require.NoError(t, err)
	require.NotNil(t, configMap, "it is written over at the next sample")
	require.Empty(t, state.Runs)
}

func TestNoMoreThanMaxRunsAreKeptAndTheyAreTheOldest(t *testing.T) {
	runs := map[string]Run{}
	for i := range MaxRuns + 10 {
		runs[fmt.Sprintf("svc-%05d", i)] = Run{Since: start.Add(time.Duration(i) * time.Second)}
	}
	kept := bounded(runs)
	require.Len(t, kept, MaxRuns)
	require.Contains(t, kept, "svc-00000")
	require.Contains(t, kept, fmt.Sprintf("svc-%05d", MaxRuns-1))
	require.NotContains(t, kept, fmt.Sprintf("svc-%05d", MaxRuns))
}
