// Package migrations changes what an older release stored so that the operator of a newer one can read it.
//
// A CRD can only grow: a field that is removed or renamed is pruned from every stored object the next time the
// object is written, and a new required field makes the stored objects that lack it unwritable. So a release
// that has to change the shape of its data does not change the schema in place. It adds the new shape beside
// the old one and registers a migration here that fills it in from the old one. The operator runs the
// migrations it knows, once each and in order, before any controller starts, and records how far it got in a
// ConfigMap in the system namespace.
//
// The record is also the guard against going back. An operator that is told to run over data that has been
// migrated further than it knows of refuses to start (ErrSchemaTooNew) rather than reconcile data that it may
// misread and then write back damaged.
package migrations

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ConfigMapName holds the record, in the system namespace. It is a ConfigMap and not a field on the
	// GeassCluster because the operator runs migrations before any GeassCluster need exist (a first start
	// happens before the installer applies one), and because it must survive the cluster object being
	// deleted and recreated, which is a reasonable thing to do to a misbehaving one.
	ConfigMapName = "geass-schema"

	keySchemaVersion = "schemaVersion"
	keyRelease       = "release"
	keyUpdatedAt     = "updatedAt"
)

// A Migration changes stored data from one schema level to the next.
//
// Run must be idempotent, and safe to run against a cluster with nothing in it: a fresh install runs every
// migration over empty data, an older cluster runs them over what it has, and a migration that stopped part
// way (the operator was killed) is run again from its start.
type Migration struct {
	// Version is the schema level the cluster is at once this has run. Versions start at 1 and have no gaps.
	Version int
	// Name says what the migration does, for the log.
	Name string
	Run  func(ctx context.Context, c client.Client) error
}

// All is every migration, in order. Add to the end; never edit or remove one that has shipped, because a cluster
// that has run it will not run it again.
var All = []Migration{
	{
		Version: 1,
		Name:    "record the baseline schema",
		// The first release to record a schema level adds no data of its own: everything a cluster stores
		// before this is already in the shape this release reads. It exists so that every cluster has a
		// level to be compared against from here on.
		Run: func(context.Context, client.Client) error { return nil },
	},
}

// BaselineLevel is the schema level of a cluster that holds nothing but what releases from before levels were recorded
// stored. A migration that changes how data is stored comes after it, so an operator from before the record can run
// over a cluster at this level, and no further: this is how far a rollback to an operator that predates the record is
// still safe.
const BaselineLevel = 1

// Latest is the schema level of the newest migration: what a cluster is at once the operator has started.
func Latest() int { return latest(All) }

func latest(migrations []Migration) int {
	if len(migrations) == 0 {
		return 0
	}
	return migrations[len(migrations)-1].Version
}

// Validate checks that migrations are numbered from 1 with no gaps, in order. A gap would be skipped silently.
func Validate(migrations []Migration) error {
	for i, m := range migrations {
		if m.Version != i+1 {
			return fmt.Errorf("migration %q has version %d, want %d: versions start at 1 and have no gaps", m.Name, m.Version, i+1)
		}
		if m.Run == nil {
			return fmt.Errorf("migration %d (%s) has nothing to run", m.Version, m.Name)
		}
	}
	return nil
}

// ErrSchemaTooNew is returned when the cluster has been migrated further than this operator knows of.
type ErrSchemaTooNew struct {
	// Stored is the schema level the cluster is at, Supported the newest this operator knows.
	Stored, Supported int
	// Release is the release that last wrote the record, when it said.
	Release string
}

func (e *ErrSchemaTooNew) Error() string {
	from := ""
	if e.Release != "" {
		from = " by Geass " + e.Release
	}
	return fmt.Sprintf("the data in this cluster is at schema level %d%s, but this operator only knows up to level %d. "+
		"It is older than the release that last ran here, and reconciling data it may misread could damage it, so it will not start. "+
		"Install the release that wrote it or a newer one; to go back to an older release, restore the etcd snapshot taken before the update",
		e.Stored, from, e.Supported)
}

// Result is what Run did.
type Result struct {
	// From and To are the schema level before and after. They are equal when nothing needed migrating.
	From, To int
	// Applied lists the migrations that ran, by version.
	Applied []int
}

// Options changes how Run goes about it.
type Options struct {
	// Before runs once, just before the first migration that is going to run, and not at all when there is
	// nothing to migrate. An error from it stops Run with nothing changed. The operator uses it to wait until
	// the operator it replaces has let go of leader election: a migration that runs under an old operator
	// that is still reconciling is overwritten by it.
	Before func(ctx context.Context) error
}

// maxConflicts is how many times Run starts over after finding that another operator moved the record. There is
// one operator, but a rolling update briefly runs two.
const maxConflicts = 5

// Run brings the cluster's data up to the newest schema level in migrations and records the release that did it.
// It returns *ErrSchemaTooNew when the cluster is ahead of the migrations. It logs to the logger in ctx.
func Run(ctx context.Context, c client.Client, namespace, release string, migrations []Migration, opts Options) (Result, error) {
	if err := Validate(migrations); err != nil {
		return Result{}, err
	}
	var lastErr error
	for range maxConflicts {
		result, err := run(ctx, c, namespace, release, migrations, opts)
		if err == nil || !apierrors.IsConflict(err) && !apierrors.IsAlreadyExists(err) {
			return result, err
		}
		lastErr = err
		logr.FromContextOrDiscard(ctx).Info("Another operator is updating the schema record, trying again")
	}
	return Result{}, fmt.Errorf("the schema record kept changing under this operator: %w", lastErr)
}

func run(ctx context.Context, c client.Client, namespace, release string, migrations []Migration, opts Options) (Result, error) {
	log := logr.FromContextOrDiscard(ctx)
	key := client.ObjectKey{Namespace: namespace, Name: ConfigMapName}
	record := &corev1.ConfigMap{}
	exists := true
	if err := c.Get(ctx, key, record); apierrors.IsNotFound(err) {
		exists = false
		record = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: ConfigMapName}}
	} else if err != nil {
		return Result{}, fmt.Errorf("read the schema record: %w", err)
	}

	stored, err := storedVersion(record)
	if err != nil {
		return Result{}, err
	}
	target := latest(migrations)
	if stored > target {
		return Result{}, &ErrSchemaTooNew{Stored: stored, Supported: target, Release: record.Data[keyRelease]}
	}

	result := Result{From: stored, To: stored}
	before := opts.Before
	for _, m := range migrations {
		if m.Version <= stored {
			continue
		}
		if before != nil {
			if err := before(ctx); err != nil {
				return result, fmt.Errorf("not ready to migrate: %w", err)
			}
			before = nil
		}
		log.Info("Running data migration", "version", m.Version, "name", m.Name)
		if err := m.Run(ctx, c); err != nil {
			return result, fmt.Errorf("migration %d (%s) failed: %w", m.Version, m.Name, err)
		}
		// Recorded after each, so a later one that fails does not make the earlier ones run again. The write
		// carries the resourceVersion it was read at: another operator that got here first is a conflict.
		if err := save(ctx, c, record, exists, m.Version, release); err != nil {
			return result, fmt.Errorf("record migration %d: %w", m.Version, err)
		}
		exists = true
		result.To = m.Version
		result.Applied = append(result.Applied, m.Version)
	}

	// Nothing to migrate, but the release is still news: it is how the record says who last started here.
	if record.Data[keyRelease] != release || !exists {
		if err := save(ctx, c, record, exists, result.To, release); err != nil {
			return result, fmt.Errorf("record the release: %w", err)
		}
	}
	return result, nil
}

func storedVersion(record *corev1.ConfigMap) (int, error) {
	raw, ok := record.Data[keySchemaVersion]
	if !ok || raw == "" {
		if len(record.Data) > 0 {
			// The operator writes the level and the release together. A record that has the one and not the other has
			// been edited, and the level it lost is not zero.
			return 0, fmt.Errorf("the schema record %s has no %s although it has other data (%d keys); fix or delete it only if you know "+
				"which level the data is at", ConfigMapName, keySchemaVersion, len(record.Data))
		}
		return 0, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		// A record nobody can read is not level 0: running every migration again over data that may be
		// ahead of them is the one thing this record exists to prevent.
		return 0, fmt.Errorf("the schema record %s has an unreadable %s %q; fix or delete it only if you know which level the data is at",
			ConfigMapName, keySchemaVersion, raw)
	}
	return v, nil
}

func save(ctx context.Context, c client.Client, record *corev1.ConfigMap, exists bool, version int, release string) error {
	if record.Data == nil {
		record.Data = map[string]string{}
	}
	record.Data[keySchemaVersion] = strconv.Itoa(version)
	record.Data[keyRelease] = release
	record.Data[keyUpdatedAt] = time.Now().UTC().Format(time.RFC3339)
	if !exists {
		return c.Create(ctx, record)
	}
	return c.Update(ctx, record)
}

// IsTooNew reports whether err says the cluster is ahead of this operator.
func IsTooNew(err error) bool {
	var tooNew *ErrSchemaTooNew
	return errors.As(err, &tooNew)
}
