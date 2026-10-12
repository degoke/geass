package controller

import (
	"context"
	"errors"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/helmchart"
	"github.com/degoke/geass/pkg/platform"
)

const (
	messageStorageStarting = "Storage is starting"
	// messageStorageUnavailable is what a database reports once storage has failed to
	// come up for longer than storageWaitTimeout. Unlike "starting" it counts as a
	// failure, so the database reaches a terminal state the user can act on.
	messageStorageUnavailable = "Storage is unavailable"

	// storageWaitTimeout is how long a new database waits for Longhorn before
	// giving up. Image pulls on a fresh node can be slow, so it is generous.
	storageWaitTimeout = 15 * time.Minute
)

// errStorageUnavailable is the error behind messageStorageUnavailable. Error strings
// are lower case by convention while the message is shown to users, so the two are
// kept apart and mapped by storageFailureMessage, as ErrAwaitingDeploy is.
var errStorageUnavailable = errors.New("storage is unavailable")

// storageFailureMessage is what a database reports for an error from
// databaseStorageClass: the user-facing text for a known failure, else the error's own.
func storageFailureMessage(err error) string {
	if errors.Is(err, errStorageUnavailable) {
		return messageStorageUnavailable
	}
	return err.Error()
}

// databaseStorageClass decides which storage class a database's volume is created
// with. "" means the cluster default. wait is true when the answer is not known yet
// because Longhorn is still being installed; the caller should retry later.
//
// The decision is made once and recorded in an annotation. A volume's class cannot
// change after it exists (claims are immutable, CNPG rejects the change), so
// recomputing it later, with Longhorn newly installed or the node count changed,
// would only produce a value that cannot be applied. Databases that already have a
// volume from before this existed are pinned to whatever that volume uses.
func (r *GeassDatabaseReconciler) databaseStorageClass(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (class string, wait bool, err error) {
	if !platform.DatabaseHasVolume(db.Spec.Engine) {
		return "", false, nil
	}
	if pinned, ok := db.Annotations[platform.AnnotationStorageClass]; ok {
		return pinned, false, nil
	}

	existing, found, err := r.existingVolumeClass(ctx, db, wsNS)
	if err != nil {
		return "", false, err
	}
	if found {
		return existing, false, r.pinStorageClass(ctx, db, existing)
	}

	class, wait, err = r.newVolumeStorageClass(ctx, db)
	if err != nil || wait {
		return "", wait, err
	}
	return class, false, r.pinStorageClass(ctx, db, class)
}

// newVolumeStorageClass is the policy for a database with no volume yet.
func (r *GeassDatabaseReconciler) newVolumeStorageClass(ctx context.Context, db *geassv1alpha1.GeassDatabase) (string, bool, error) {
	region := pinnedNodePlacement(db).Region
	want := platform.DatabaseStorageClassIn(db.Spec.Engine, databaseWantsHA(db), region)
	available, err := r.storageClassExists(ctx, want)
	if err != nil {
		return "", false, err
	}
	if available {
		return want, false, nil
	}
	// A region's class is made by the cluster controller once the region's Longhorn nodes are ready. If
	// Longhorn is already serving the global class, the regional one is only a moment behind, and
	// settling for local storage now would pin the database to it for good. Wait for it.
	if _, regional := platform.RegionFromRegionalClass(want); regional {
		if global, err := r.storageClassExists(ctx, platform.StorageClassReplicated); err != nil {
			return "", false, err
		} else if global {
			return "", true, nil
		}
	}
	// The class is not there. If Longhorn is on its way, wait for it instead of
	// settling for local storage that this database could never leave. Waiting is
	// bounded: a Longhorn that never comes up must show as a failure, not as an
	// endless "starting".
	pending, stuckFor, err := r.longhornPending(ctx)
	if err != nil {
		return "", false, err
	}
	if !pending {
		return "", false, nil
	}
	if stuckFor > storageWaitTimeout {
		return "", false, errStorageUnavailable
	}
	return "", true, nil
}

func (r *GeassDatabaseReconciler) storageClassExists(ctx context.Context, name string) (bool, error) {
	err := r.Get(ctx, client.ObjectKey{Name: name}, &storagev1.StorageClass{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// longhornPending reports whether a cluster has Longhorn enabled, and for how long
// its add-ons have been not ready. That is measured from the AddonsReady
// condition, so it is independent of any one database. Once add-ons are ready the
// classes are about to appear, which is a gap between reconciles, not a stall.
func (r *GeassDatabaseReconciler) longhornPending(ctx context.Context) (bool, time.Duration, error) {
	var clusters geassv1alpha1.GeassClusterList
	if err := r.List(ctx, &clusters); err != nil {
		return false, 0, err
	}
	for i := range clusters.Items {
		cluster := &clusters.Items[i]
		if !geassv1alpha1.LonghornEnabled(cluster.Spec.Addons) {
			continue
		}
		condition := meta.FindStatusCondition(cluster.Status.Conditions, platform.ConditionAddonsReady)
		if condition != nil && condition.Status == metav1.ConditionTrue {
			return true, 0, nil
		}
		since := cluster.CreationTimestamp.Time
		if condition != nil && !condition.LastTransitionTime.IsZero() {
			since = condition.LastTransitionTime.Time
		}
		if since.IsZero() {
			return true, 0, nil
		}
		return true, time.Since(since), nil
	}
	return false, 0, nil
}

// existingVolumeClass reports the class of volumes this database already has.
func (r *GeassDatabaseReconciler) existingVolumeClass(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (class string, found bool, err error) {
	switch db.Spec.Engine {
	case geassv1alpha1.DatabaseEngineSQLite:
		pvc := &corev1.PersistentVolumeClaim{}
		err := r.Get(ctx, client.ObjectKey{Name: db.Name + "-sqlite", Namespace: wsNS}, pvc)
		if apierrors.IsNotFound(err) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return utilptr.Deref(pvc.Spec.StorageClassName, ""), true, nil
	case geassv1alpha1.DatabaseEngineMySQL:
		// The chart owns its claims and we cannot read their class back from it, so
		// an existing chart is treated as being on the default class.
		_, err := helmchart.Get(ctx, r.Client, mysqlChartName(db.Name))
		if apierrors.IsNotFound(err) {
			return "", false, nil
		}
		return "", err == nil, err
	default:
		cluster := &cnpgv1.Cluster{}
		err := r.Get(ctx, client.ObjectKey{Name: db.Name, Namespace: wsNS}, cluster)
		// The CNPG CRD only exists once the operator chart is installed, which
		// reconcilePostgres does later. Until then there is no Cluster to find, and the
		// lookup failing with "no such kind" must not stop that install from happening.
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return utilptr.Deref(cluster.Spec.Storage.StorageClass, ""), true, nil
	}
}

// pinStorageClass records the decision on the database. It patches only the
// annotation, from a fresh read: the reconcile's copy may have had its spec
// replaced by the last-deployed snapshot, which must not be written back.
func (r *GeassDatabaseReconciler) pinStorageClass(ctx context.Context, db *geassv1alpha1.GeassDatabase, class string) error {
	latest := &geassv1alpha1.GeassDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(db), latest); err != nil {
		return err
	}
	base := latest.DeepCopy()
	if latest.Annotations == nil {
		latest.Annotations = map[string]string{}
	}
	latest.Annotations[platform.AnnotationStorageClass] = class
	if err := r.Patch(ctx, latest, client.MergeFrom(base)); err != nil {
		return err
	}
	if db.Annotations == nil {
		db.Annotations = map[string]string{}
	}
	db.Annotations[platform.AnnotationStorageClass] = class
	return nil
}

// stringPtrOrNil is nil for "", which for a storage class means "the cluster default".
func stringPtrOrNil(value string) *string {
	if value == "" {
		return nil
	}
	return stringPtr(value)
}
