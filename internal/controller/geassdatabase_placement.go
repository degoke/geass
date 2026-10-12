package controller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/platform"
)

// databaseNodePlacement decides which nodes a database runs on, once. Its volumes are created on
// those nodes, so moving it later, because the project's region or the cluster default changed, would
// strand it; the decision is recorded on the database, as its storage class is. A database that was
// provisioned before placement existed stays wherever it is.
func (r *GeassDatabaseReconciler) databaseNodePlacement(ctx context.Context, db *geassv1alpha1.GeassDatabase) (platform.Placement, error) {
	if pinned, ok := db.Annotations[platform.AnnotationNodePlacement]; ok {
		return platform.ParsePlacement(pinned), nil
	}
	placement := platform.Placement{}
	if db.Status.TargetNamespace == "" {
		resolved, err := r.resolveDatabasePlacement(ctx, db)
		if err != nil {
			return platform.Placement{}, err
		}
		placement = resolved
	}
	return placement, r.pinNodePlacement(ctx, db, placement)
}

// resolveDatabasePlacement is where a database that has not been placed yet will run: its project's region,
// or for the cluster-wide server its own region (the cluster default when none is chosen), plus its tags.
func (r *GeassDatabaseReconciler) resolveDatabasePlacement(ctx context.Context, db *geassv1alpha1.GeassDatabase) (platform.Placement, error) {
	if !isSharedDatabase(db) {
		return platform.ResolvePlacement(ctx, r.Client, db.Spec.Project, db.Spec.RunOn.Tags)
	}
	region := db.Spec.ServerRegion
	if region == "" {
		var err error
		if region, err = platform.DefaultRegion(ctx, r.Client, ""); err != nil {
			return platform.Placement{}, err
		}
	}
	return platform.NewPlacement(region, db.Spec.RunOn.Tags), nil
}

// pinNodePlacement records the decision. Like pinStorageClass it patches only the annotation, from a
// fresh read, because the reconcile's copy may carry the last-deployed snapshot's spec.
func (r *GeassDatabaseReconciler) pinNodePlacement(ctx context.Context, db *geassv1alpha1.GeassDatabase, placement platform.Placement) error {
	latest := &geassv1alpha1.GeassDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(db), latest); err != nil {
		return err
	}
	base := latest.DeepCopy()
	if latest.Annotations == nil {
		latest.Annotations = map[string]string{}
	}
	latest.Annotations[platform.AnnotationNodePlacement] = placement.Encode()
	if err := r.Patch(ctx, latest, client.MergeFrom(base)); err != nil {
		return err
	}
	if db.Annotations == nil {
		db.Annotations = map[string]string{}
	}
	db.Annotations[platform.AnnotationNodePlacement] = placement.Encode()
	return nil
}

// pinnedNodePlacement reads where a database was placed, for helpers that never decide it.
func pinnedNodePlacement(db *geassv1alpha1.GeassDatabase) platform.Placement {
	return platform.ParsePlacement(db.Annotations[platform.AnnotationNodePlacement])
}

// jobPlacement is where the short-lived jobs of a database (backups, restores, creating logical
// databases) run: in its region, near it, but never pinned to the tags it asked for, so they do not
// take room on nodes reserved for the database itself. They do keep the tags its project requires:
// a dump of the data is as sensitive as the data, so it stays on the nodes the data is allowed on.
func jobPlacement(p platform.Placement) platform.Placement {
	return p.Jobs()
}

// cnpgAffinity restricts a Postgres cluster's instances to the placement. Nil leaves CNPG's defaults.
func cnpgAffinity(p platform.Placement) *cnpgv1.AffinityConfiguration {
	if p.IsZero() {
		return nil
	}
	return &cnpgv1.AffinityConfiguration{NodeSelector: p.NodeSelector(), Tolerations: p.Tolerations()}
}
