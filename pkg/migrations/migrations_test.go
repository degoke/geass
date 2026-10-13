package migrations

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const ns = "geass-system"

// recorder makes migrations that note when they ran.
type recorder struct{ ran []int }

func (r *recorder) migration(version int) Migration {
	return Migration{Version: version, Name: "test", Run: func(context.Context, client.Client) error {
		r.ran = append(r.ran, version)
		return nil
	}}
}

func recordOf(t *testing.T, c client.Client) *corev1.ConfigMap {
	t.Helper()
	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: ConfigMapName}, cm))
	return cm
}

func seedRecord(t *testing.T, c client.Client, data map[string]string) {
	t.Helper()
	require.NoError(t, c.Create(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: ConfigMapName}, Data: data,
	}))
}

func TestTheShippedMigrationsAreNumberedWithoutGaps(t *testing.T) {
	require.NoError(t, Validate(All))
	require.GreaterOrEqual(t, Latest(), 1)
}

func TestValidateCatchesGapsAndEmptyMigrations(t *testing.T) {
	var r recorder
	require.NoError(t, Validate([]Migration{r.migration(1), r.migration(2)}))
	require.Error(t, Validate([]Migration{r.migration(1), r.migration(3)}), "a gap would be skipped without a word")
	require.Error(t, Validate([]Migration{r.migration(2)}), "numbering starts at 1")
	require.Error(t, Validate([]Migration{{Version: 1, Name: "nothing"}}))
}

func TestAFreshClusterRunsEveryMigrationAndRecordsTheLevel(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	var r recorder
	result, err := Run(context.Background(), c, ns, "v0.2.0", []Migration{r.migration(1), r.migration(2)}, Options{})
	require.NoError(t, err)
	require.Equal(t, []int{1, 2}, r.ran)
	require.Equal(t, Result{From: 0, To: 2, Applied: []int{1, 2}}, result)

	record := recordOf(t, c)
	require.Equal(t, "2", record.Data["schemaVersion"])
	require.Equal(t, "v0.2.0", record.Data["release"])
}

func TestAClusterThatIsUpToDateRunsNothingButNotesTheRelease(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	seedRecord(t, c, map[string]string{"schemaVersion": "2", "release": "v0.2.0"})
	var r recorder
	result, err := Run(context.Background(), c, ns, "v0.2.1", []Migration{r.migration(1), r.migration(2)}, Options{})
	require.NoError(t, err)
	require.Empty(t, r.ran, "a migration that has run is never run again")
	require.Equal(t, Result{From: 2, To: 2}, result)
	require.Equal(t, "v0.2.1", recordOf(t, c).Data["release"], "the record says which release started here last")
}

func TestOnlyTheMigrationsAfterTheStoredLevelRun(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	seedRecord(t, c, map[string]string{"schemaVersion": "1", "release": "v0.1.0"})
	var r recorder
	result, err := Run(context.Background(), c, ns, "v0.3.0", []Migration{r.migration(1), r.migration(2), r.migration(3)}, Options{})
	require.NoError(t, err)
	require.Equal(t, []int{2, 3}, r.ran)
	require.Equal(t, []int{2, 3}, result.Applied)
	require.Equal(t, "3", recordOf(t, c).Data["schemaVersion"])
}

func TestAFailedMigrationLeavesTheRecordAtTheLastGoodLevel(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	var r recorder
	boom := Migration{Version: 2, Name: "broken", Run: func(context.Context, client.Client) error { return errors.New("boom") }}

	_, err := Run(context.Background(), c, ns, "v0.2.0", []Migration{r.migration(1), boom, r.migration(3)}, Options{})
	require.ErrorContains(t, err, "migration 2 (broken) failed: boom")
	require.Equal(t, []int{1}, r.ran, "nothing after the failure runs")
	require.Equal(t, "1", recordOf(t, c).Data["schemaVersion"], "so a retry resumes at the migration that failed, not at the start")

	r.ran = nil
	fixed := r.migration(2)
	result, err := Run(context.Background(), c, ns, "v0.2.0", []Migration{r.migration(1), fixed, r.migration(3)}, Options{})
	require.NoError(t, err)
	require.Equal(t, []int{2, 3}, r.ran)
	require.Equal(t, 3, result.To)
}

func TestAnOperatorOlderThanTheDataRefusesToRun(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	seedRecord(t, c, map[string]string{"schemaVersion": "5", "release": "v0.9.0"})
	var r recorder

	_, err := Run(context.Background(), c, ns, "v0.2.0", []Migration{r.migration(1), r.migration(2)}, Options{})
	require.Error(t, err)
	require.True(t, IsTooNew(err))
	var tooNew *ErrSchemaTooNew
	require.ErrorAs(t, err, &tooNew)
	require.Equal(t, 5, tooNew.Stored)
	require.Equal(t, 2, tooNew.Supported)
	require.Contains(t, err.Error(), "v0.9.0", "the error names the release that wrote the data")
	require.Contains(t, err.Error(), "snapshot", "and says how to go back")

	require.Empty(t, r.ran)
	record := recordOf(t, c)
	require.Equal(t, "5", record.Data["schemaVersion"], "refusing must not lower the level")
	require.Equal(t, "v0.9.0", record.Data["release"], "or claim the data for the older release")
}

func TestAnUnreadableRecordIsNotTreatedAsLevelZero(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	seedRecord(t, c, map[string]string{"schemaVersion": "two"})
	var r recorder

	_, err := Run(context.Background(), c, ns, "v0.2.0", []Migration{r.migration(1)}, Options{})
	require.ErrorContains(t, err, "unreadable")
	require.Empty(t, r.ran, "running every migration again over data that may be ahead of them is what the record prevents")
}

func TestAnOperatorThatLosesARaceReadsTheRecordAgainAndCarriesOn(t *testing.T) {
	conflicts := 1
	c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if conflicts > 0 {
				conflicts--
				// The other operator created the record first, and got through migration 1.
				other := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: ConfigMapName},
					Data:       map[string]string{"schemaVersion": "1", "release": "v0.2.0"},
				}
				if err := c.Create(ctx, other); err != nil {
					return err
				}
				return apierrors.NewAlreadyExists(schema.GroupResource{Resource: "configmaps"}, ConfigMapName)
			}
			return c.Create(ctx, obj, opts...)
		},
	}).Build()
	var r recorder

	result, err := Run(context.Background(), c, ns, "v0.2.0", []Migration{r.migration(1), r.migration(2)}, Options{})
	require.NoError(t, err)
	require.Equal(t, 2, result.To)
	require.Equal(t, "2", recordOf(t, c).Data["schemaVersion"])
	require.Equal(t, []int{1, 2}, r.ran, "the loser may repeat a migration (they are idempotent) but must not skip one")
}

func TestTheGuardRunsOnceBeforeTheFirstMigrationAndNotWhenThereIsNothingToDo(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	var r recorder
	guarded := 0
	opts := Options{Before: func(context.Context) error {
		guarded++
		require.Empty(t, r.ran, "the guard comes before any migration")
		return nil
	}}

	_, err := Run(context.Background(), c, ns, "v0.2.0", []Migration{r.migration(1), r.migration(2)}, opts)
	require.NoError(t, err)
	require.Equal(t, 1, guarded, "once for the run, not once per migration")

	_, err = Run(context.Background(), c, ns, "v0.2.0", []Migration{r.migration(1), r.migration(2)}, opts)
	require.NoError(t, err)
	require.Equal(t, 1, guarded, "an up to date cluster has nothing to wait for")
}

func TestAGuardThatRefusesStopsTheRunBeforeAnythingChanges(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	var r recorder
	opts := Options{Before: func(context.Context) error { return errors.New("the old operator is still running") }}

	_, err := Run(context.Background(), c, ns, "v0.2.0", []Migration{r.migration(1)}, opts)
	require.ErrorContains(t, err, "the old operator is still running")
	require.Empty(t, r.ran)
	require.True(t, apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: ConfigMapName}, &corev1.ConfigMap{})),
		"nothing was recorded")
}

func TestARecordThatLostItsLevelButKeptOtherDataIsNotTreatedAsLevelZero(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	seedRecord(t, c, map[string]string{"release": "v0.9.0"})
	var r recorder

	_, err := Run(context.Background(), c, ns, "v0.2.0", []Migration{r.migration(1), r.migration(2)}, Options{})
	require.ErrorContains(t, err, "has no schemaVersion")
	require.Empty(t, r.ran, "running every migration again over data that may be ahead of them is what the record prevents")
}

func TestAnEmptyRecordIsAFreshCluster(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	seedRecord(t, c, map[string]string{})
	var r recorder

	result, err := Run(context.Background(), c, ns, "v0.2.0", []Migration{r.migration(1)}, Options{})
	require.NoError(t, err)
	require.Equal(t, 1, result.To)
}
