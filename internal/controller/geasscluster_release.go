package controller

import (
	"context"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/migrations"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/release"
)

// recordRelease writes which release this operator is, and the data schema level it has migrated to, on the
// cluster object. The installer reads them: before an update, to refuse a downgrade, and after it, to know that
// the new operator has taken over. It is separate from the status that decides readiness, and runs first, so
// that a cluster whose reconciliation has been paused by repeated failures still says which operator it has.
//
// A failure to record it is not a failure to reconcile, so it is the caller's to log.
func (r *GeassClusterReconciler) recordRelease(ctx context.Context, cluster *geassv1alpha1.GeassCluster) error {
	_, err := platform.PatchStatus(ctx, r.Client, cluster, func() {
		cluster.Status.Release = release.Version
		cluster.Status.SchemaVersion = int32(migrations.Latest())
	})
	return err
}
