package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	backupCredentialsSuffix  = "-backup-credentials"
	restoreCredentialsSuffix = "-restore-credentials"
	finalBackupSuffix        = "-final"
	recoverySourceName       = "origin"

	annotationBackupStorageConfig = "geass.dev/backup-storage-config"

	// A restore to "now" would ask for changes that have not reached backup storage yet, so the
	// recovery window ends this long before the present. It is also the coarseness of the end
	// time, which keeps status stable between reconciles.
	backupWindowMargin = 5 * time.Minute

	finalBackupPollInterval = 10 * time.Second
	// backupRetryInterval is how soon a database is looked at again after its backups could not be
	// brought up to date. The database itself stays up meanwhile.
	backupRetryInterval = time.Minute

	messageRestoringDatabase = "Restoring database from backup"
	defaultPostgresVersion   = "16"
)

// backupPlan is what a database's backups look like this reconcile.
type backupPlan struct {
	// archive is the live archive. It is nil while backups cannot be set up yet.
	archive *geassv1alpha1.GeassBackupArchive
	// secretName is the Secret in the database's namespace that holds the storage credentials.
	secretName string
	// health records why backups are or are not working, for the dashboard.
	health platform.BackupHealth
	// access is the scoped credential for the bucket, set once the storage is provisioned. Snapshot
	// databases use it to list their restore points.
	access *backupStorageAccess
	// degraded is true when the plan could not be worked out this time. It stands for "keep what is
	// there": nothing is set up, changed or reported until the next attempt.
	degraded bool
}

// backupStorageAccess is a credential scoped to one backup bucket.
type backupStorageAccess struct {
	accessKey, secretKey, region string
}

// recoveryPlan is how a restored database's cluster is bootstrapped from another database's backups.
type recoveryPlan struct {
	bootstrap cnpgv1.RecoveryConfiguration
	external  cnpgv1.ExternalCluster
}

func (r *GeassDatabaseReconciler) now() time.Time {
	if r.clock != nil {
		return r.clock()
	}
	return time.Now()
}

func (r *GeassDatabaseReconciler) backupStorage(accessKey, secretKey, region string) backupStorageAPI {
	if r.newBackupStorage != nil {
		return r.newBackupStorage(accessKey, secretKey, region)
	}
	return awsBackupStorageFactory(r.HTTP)(accessKey, secretKey, region)
}

// databaseBackupKind says how this database is backed up: Postgres continuously, MySQL and SQLite
// by daily snapshots, and nothing else at all (a cache, or a database that runs outside Geass).
func databaseBackupKind(db *geassv1alpha1.GeassDatabase) platform.BackupKind {
	return platform.DatabaseBackupKind(db.Spec.Engine, db.Spec.Placement)
}

func databaseSupportsBackups(db *geassv1alpha1.GeassDatabase) bool {
	return databaseBackupKind(db) != platform.BackupKindNone
}

// databaseEngine is the engine of a database, where an unset one means Postgres.
func databaseEngine(db *geassv1alpha1.GeassDatabase) geassv1alpha1.GeassDatabaseEngine {
	if db.Spec.Engine == "" {
		return geassv1alpha1.DatabaseEnginePostgres
	}
	return db.Spec.Engine
}

func databaseBackupsEnabled(db *geassv1alpha1.GeassDatabase) bool {
	return databaseSupportsBackups(db) && db.Spec.Backup != nil && db.Spec.Backup.Enabled
}

func databaseBackupRetentionDays(db *geassv1alpha1.GeassDatabase) int32 {
	if db.Spec.Backup != nil && db.Spec.Backup.RetentionDays > 0 {
		return min(db.Spec.Backup.RetentionDays, platform.MaxBackupRetentionDays)
	}
	return platform.DefaultBackupRetentionDays
}

func databaseVersion(db *geassv1alpha1.GeassDatabase) string {
	return firstNonEmpty(db.Spec.Version, defaultPostgresVersion)
}

func databaseStorageQuantity(db *geassv1alpha1.GeassDatabase) resource.Quantity {
	return resource.MustParse(storageSize(db))
}

// planBackups works out, and sets up as far as it can, the backups of a database. It returns nil
// when the database does not want backups. A problem with the storage is reported in the plan
// rather than as an error, because an unprotected database is better than one that will not start.
func (r *GeassDatabaseReconciler) planBackups(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (*backupPlan, error) {
	if !databaseBackupsEnabled(db) {
		return nil, nil
	}
	plan := &backupPlan{secretName: db.Name + backupCredentialsSuffix}

	archives, err := archivesOf(ctx, r.Client, db)
	if err != nil {
		return nil, err
	}
	live := firstLiveArchive(archives)
	plan.archive = live

	var storage *geassv1alpha1.GeassBackupStorage
	if live != nil {
		// An archive keeps writing where its history already lives, whatever the project says now.
		storage = &live.Spec.Storage
	} else if storage, err = r.projectBackupStorage(ctx, db.Spec.Project); err != nil {
		return nil, err
	}
	if storage == nil {
		return plan, nil
	}

	accessKey, secretKey, region, err := r.provisionBackupStorage(ctx, db.Spec.Project, storage)
	if err != nil {
		plan.health.StorageError = summarizeStorageError(err)
		return plan, nil
	}
	if err := r.ensureWorkspaceSecret(ctx, db, wsNS, plan.secretName, accessKey, secretKey, region); err != nil {
		return nil, err
	}
	plan.access = &backupStorageAccess{accessKey: accessKey, secretKey: secretKey, region: region}
	archive, err := r.ensureBackupArchive(ctx, db, archives, live, resolvedStorage(storage, region))
	if err != nil {
		return nil, err
	}
	plan.archive = archive
	plan.health.StorageReady = true
	return plan, nil
}

func firstLiveArchive(archives []geassv1alpha1.GeassBackupArchive) *geassv1alpha1.GeassBackupArchive {
	for i := range archives {
		if archives[i].Spec.DeleteAfter == nil {
			return &archives[i]
		}
	}
	return nil
}

func resolvedStorage(storage *geassv1alpha1.GeassBackupStorage, region string) geassv1alpha1.GeassBackupStorage {
	resolved := *storage
	resolved.Region = region
	return resolved
}

// projectBackupStorage returns where the project keeps backups, or nil when it has not chosen.
func (r *GeassDatabaseReconciler) projectBackupStorage(ctx context.Context, project string) (*geassv1alpha1.GeassBackupStorage, error) {
	var found geassv1alpha1.GeassProject
	if err := r.Get(ctx, client.ObjectKey{Name: project, Namespace: platform.SystemNamespace}, &found); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if found.Spec.BackupStorage == nil {
		return nil, nil
	}
	return found.Spec.BackupStorage.DeepCopy(), nil
}

// provisionBackupStorage makes sure the bucket exists and that a credential scoped to that bucket
// is stored once for the whole project. It talks to AWS only when something is missing or
// changed, because every access key created counts against a small per-user limit.
func (r *GeassDatabaseReconciler) provisionBackupStorage(ctx context.Context, project string, storage *geassv1alpha1.GeassBackupStorage) (accessKey, secretKey, region string, err error) {
	connection, err := resolveAWSConnection(ctx, r.Client, project, storage.ConnectionRef.Name, storage.Region)
	if err != nil {
		return "", "", "", err
	}
	region = connection.region

	secretName := platform.BackupStorageSecretName(storage.Bucket)
	stored := &corev1.Secret{}
	getErr := r.Get(ctx, client.ObjectKey{Name: secretName, Namespace: platform.SystemNamespace}, stored)
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return "", "", "", getErr
	}
	config := backupStorageConfigHash(storage.ConnectionRef.Name, storage.Bucket, region)
	existingAccess := secretValue(stored, platform.BackupSecretKeyAccessKeyID)
	existingSecret := secretValue(stored, platform.BackupSecretKeySecretAccessKey)
	if getErr == nil && existingAccess != "" && existingSecret != "" && stored.Annotations[annotationBackupStorageConfig] == config {
		return existingAccess, existingSecret, region, nil
	}

	aws := r.backupStorage(connection.accessKey, connection.secretKey, region)
	if err := aws.EnsureBucket(storage.Bucket); err != nil {
		return "", "", "", fmt.Errorf("could not create the backup bucket: %w", err)
	}
	accessKey, secretKey, err = aws.EnsureBucketUser([]string{storage.Bucket}, platform.BackupStorageUserName(storage.Bucket), existingAccess, existingSecret)
	if err != nil {
		return "", "", "", fmt.Errorf("could not create backup credentials: %w", err)
	}

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: platform.SystemNamespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels[platform.LabelManagedBy] = platform.ManagedByValue
		if secret.Annotations == nil {
			secret.Annotations = map[string]string{}
		}
		secret.Annotations[annotationBackupStorageConfig] = config
		secret.Data = backupCredentialData(accessKey, secretKey, region)
		return nil
	}); err != nil {
		return "", "", "", err
	}
	return accessKey, secretKey, region, nil
}

func backupStorageConfigHash(connection, bucket, region string) string {
	sum := sha256.Sum256([]byte(connection + "|" + bucket + "|" + region))
	return hex.EncodeToString(sum[:8])
}

func backupCredentialData(accessKey, secretKey, region string) map[string][]byte {
	return map[string][]byte{
		platform.BackupSecretKeyAccessKeyID:     []byte(accessKey),
		platform.BackupSecretKeySecretAccessKey: []byte(secretKey),
		platform.BackupSecretKeyRegion:          []byte(region),
	}
}

// ensureWorkspaceSecret copies the scoped credentials into the database's namespace, where the
// Postgres operator can read them. The root AWS keys never leave the system namespace.
func (r *GeassDatabaseReconciler) ensureWorkspaceSecret(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, name, accessKey, secretKey, region string) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, db, "GeassDatabase")
		secret.Data = backupCredentialData(accessKey, secretKey, region)
		return setSameNamespaceOwner(db, secret, r.Scheme)
	})
	return err
}

// ensureBackupArchive returns the live archive, creating it the first time and keeping its
// changeable fields current afterwards.
func (r *GeassDatabaseReconciler) ensureBackupArchive(ctx context.Context, db *geassv1alpha1.GeassDatabase, existing []geassv1alpha1.GeassBackupArchive, live *geassv1alpha1.GeassBackupArchive, storage geassv1alpha1.GeassBackupStorage) (*geassv1alpha1.GeassBackupArchive, error) {
	retention := databaseBackupRetentionDays(db)
	source := backupSourceOf(db)
	displayName := platform.ResourceDisplayName(db.ObjectMeta)

	if live != nil {
		// The archive's status is rewritten on every reconcile, so the copy from the list is often
		// stale; read it again before changing it.
		current := &geassv1alpha1.GeassBackupArchive{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(live), current); err != nil {
			return nil, err
		}
		updated := current.DeepCopy()
		updated.Spec.RetentionDays = retention
		updated.Spec.Source = source
		setArchiveMetadata(updated, db, displayName)
		if equality.Semantic.DeepEqual(current.Spec, updated.Spec) && equality.Semantic.DeepEqual(current.ObjectMeta, updated.ObjectMeta) {
			return current, nil
		}
		if err := r.Update(ctx, updated); err != nil {
			return nil, err
		}
		return updated, nil
	}

	// A database that had backups before (turned off, then on again) already used the plain name,
	// so a new archive takes the first number that is free. Counting archives would not do: the
	// janitor deletes old ones, which frees a number that a retired archive may still hold. A name
	// that is free is also an empty folder, because an archive is deleted only after its data is.
	serverName := freeArchiveName(platform.BackupServerName(db.Name, db.UID), existing)
	archive := &geassv1alpha1.GeassBackupArchive{
		ObjectMeta: metav1.ObjectMeta{Name: serverName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassBackupArchiveSpec{
			Project: db.Spec.Project, Environment: db.Spec.Environment,
			Database: db.Name, DatabaseUID: string(db.UID), ServerName: serverName,
			Storage: storage, RetentionDays: retention, Source: source,
		},
	}
	setArchiveMetadata(archive, db, displayName)
	if err := r.Create(ctx, archive); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Created a moment ago and not yet visible in the cache; the next reconcile will see it.
			return nil, fmt.Errorf("backup archive %s is still being created", serverName)
		}
		return nil, err
	}
	return archive, nil
}

// freeArchiveName returns base, or base-2, base-3 and so on: the first name no archive in existing uses.
func freeArchiveName(base string, existing []geassv1alpha1.GeassBackupArchive) string {
	taken := make(map[string]bool, len(existing))
	for i := range existing {
		taken[existing[i].Name] = true
		taken[existing[i].Spec.ServerName] = true
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s-%d", base, n)
	}
	return name
}

func setArchiveMetadata(archive *geassv1alpha1.GeassBackupArchive, db *geassv1alpha1.GeassDatabase, displayName string) {
	applyGeassLabels(archive, db, "GeassDatabase")
	if archive.Annotations == nil {
		archive.Annotations = map[string]string{}
	}
	archive.Annotations[platform.DisplayNameAnnotation] = displayName
}

// backupSourceOf records what a restore needs to know about a database. Postgres must be restored
// into the same major version and database name. MySQL's version is chosen by its chart, so only its
// database name matters. SQLite serves a single database and has neither.
func backupSourceOf(db *geassv1alpha1.GeassDatabase) geassv1alpha1.GeassBackupSource {
	size := databaseStorageQuantity(db)
	source := geassv1alpha1.GeassBackupSource{Engine: databaseEngine(db), StorageSize: &size, Resources: databaseInstanceResources(db)}
	switch source.Engine {
	case geassv1alpha1.DatabaseEnginePostgres:
		source.Version, source.DatabaseName = databaseVersion(db), databaseName(db)
	case geassv1alpha1.DatabaseEngineMySQL:
		source.DatabaseName = databaseName(db)
	}
	return source
}

func secretKeySelector(secret, key string) *corev1.SecretKeySelector {
	return &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Key: key}
}

// cnpgBackupConfiguration is the one place the operator's backup section is built, so moving to
// its plugin-based backups later is a contained change.
func cnpgBackupConfiguration(plan *backupPlan) *cnpgv1.BackupConfiguration {
	archive := plan.archive
	selector := func(key string) *corev1.SecretKeySelector { return secretKeySelector(plan.secretName, key) }
	return &cnpgv1.BackupConfiguration{
		RetentionPolicy: platform.BackupRetentionPolicy(archive.Spec.RetentionDays),
		BarmanObjectStore: &cnpgv1.BarmanObjectStoreConfiguration{
			DestinationPath: platform.BackupDestinationPath(archive.Spec.Storage.Bucket, archive.Spec.Project),
			ServerName:      archive.Spec.ServerName,
			S3Credentials: &cnpgv1.S3Credentials{
				AccessKeyID:     selector(platform.BackupSecretKeyAccessKeyID),
				SecretAccessKey: selector(platform.BackupSecretKeySecretAccessKey),
				Region:          selector(platform.BackupSecretKeyRegion),
			},
			Wal:  &cnpgv1.WalBackupConfiguration{Compression: "gzip", MaxParallel: 2},
			Data: &cnpgv1.DataBackupConfiguration{Compression: "gzip", Jobs: 2},
		},
	}
}

// planRecovery prepares a restored database's bootstrap from another database's archive. It
// returns nil for a database that is not a restore. Errors are written for end users.
func (r *GeassDatabaseReconciler) planRecovery(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (*recoveryPlan, error) {
	restore := db.Spec.RestoreFrom
	if restore == nil || databaseBackupKind(db) != platform.BackupKindContinuous {
		return nil, nil
	}
	archive := &geassv1alpha1.GeassBackupArchive{}
	if err := r.Get(ctx, client.ObjectKey{Name: restore.Archive, Namespace: platform.SystemNamespace}, archive); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, platform.UserError("These backups are no longer available")
		}
		return nil, err
	}
	if err := validateRestore(db, archive); err != nil {
		return nil, err
	}

	accessKey, secretKey, region, err := r.provisionBackupStorage(ctx, archive.Spec.Project, &archive.Spec.Storage)
	if err != nil {
		return nil, platform.Userf("The backup storage is not available: %s", summarizeStorageError(err))
	}
	secretName := db.Name + restoreCredentialsSuffix
	if err := r.ensureWorkspaceSecret(ctx, db, wsNS, secretName, accessKey, secretKey, region); err != nil {
		return nil, err
	}

	selector := func(key string) *corev1.SecretKeySelector { return secretKeySelector(secretName, key) }
	plan := &recoveryPlan{
		bootstrap: cnpgv1.RecoveryConfiguration{
			Source:         recoverySourceName,
			Database:       databaseName(db),
			Owner:          appContainerName,
			Secret:         &corev1.LocalObjectReference{Name: db.Name + "-bootstrap"},
			RecoveryTarget: recoveryTarget(restore),
		},
		external: cnpgv1.ExternalCluster{
			Name: recoverySourceName,
			BarmanObjectStore: &cnpgv1.BarmanObjectStoreConfiguration{
				DestinationPath: platform.BackupDestinationPath(archive.Spec.Storage.Bucket, archive.Spec.Project),
				ServerName:      archive.Spec.ServerName,
				S3Credentials: &cnpgv1.S3Credentials{
					AccessKeyID:     selector(platform.BackupSecretKeyAccessKeyID),
					SecretAccessKey: selector(platform.BackupSecretKeySecretAccessKey),
					Region:          selector(platform.BackupSecretKeyRegion),
				},
				Wal: &cnpgv1.WalBackupConfiguration{MaxParallel: 2},
			},
		},
	}
	return plan, nil
}

func recoveryTarget(restore *geassv1alpha1.GeassDatabaseRestoreSpec) *cnpgv1.RecoveryTarget {
	switch {
	case restore.RestorePoint != "":
		immediate := true
		return &cnpgv1.RecoveryTarget{BackupID: restore.RestorePoint, TargetImmediate: &immediate}
	case restore.Time != nil:
		return &cnpgv1.RecoveryTarget{TargetTime: platform.RestoreTargetTime(restore.Time.Time)}
	default:
		return nil
	}
}

// validateRestore rejects restores that cannot succeed, with messages written for end users.
// Catching them here matters because a failed recovery only shows up as a database that never starts.
func validateRestore(db *geassv1alpha1.GeassDatabase, archive *geassv1alpha1.GeassBackupArchive) error {
	restore := db.Spec.RestoreFrom
	if archive.Spec.Project != db.Spec.Project {
		return platform.UserError("Backups from another project cannot be restored here")
	}
	source := archive.Spec.Source
	sourceEngine := source.Engine
	if sourceEngine == "" {
		sourceEngine = geassv1alpha1.DatabaseEnginePostgres
	}
	if sourceEngine != databaseEngine(db) {
		return platform.Userf("A restored database must use the same engine as the original (%s)", sourceEngine)
	}
	if source.Version != "" && databaseVersion(db) != source.Version {
		return platform.Userf("A restored database must use Postgres version %s, the version of the backup", source.Version)
	}
	if source.DatabaseName != "" && databaseName(db) != source.DatabaseName {
		return platform.Userf("A restored database must keep the database name %s", source.DatabaseName)
	}
	if source.StorageSize != nil {
		if size := databaseStorageQuantity(db); size.Cmp(*source.StorageSize) < 0 {
			return platform.Userf("A restored database needs at least %s of storage, the size of the original", source.StorageSize.String())
		}
	}
	var at *time.Time
	if restore.Time != nil {
		at = &restore.Time.Time
	}
	return platform.ValidateRestoreTarget(archive, restore.RestorePoint, at)
}

// desiredClusterSpec is the Postgres cluster a database asks for. A cluster is bootstrapped either
// empty or from backups, never both. Backups appear only once an archive exists for them.
func desiredClusterSpec(db *geassv1alpha1.GeassDatabase, plan *backupPlan, recovery *recoveryPlan) cnpgv1.ClusterSpec {
	storage := storageSize(db)
	spec := cnpgv1.ClusterSpec{
		Instances: databaseInstances(db),
		ImageName: fmt.Sprintf("ghcr.io/cloudnative-pg/postgresql:%s", databaseVersion(db)),
		Storage:   cnpgv1.StorageConfiguration{Size: storage},
		Resources: databaseInstanceResources(db),
	}
	if recovery != nil {
		bootstrap := recovery.bootstrap
		spec.Bootstrap.Recovery = bootstrap.DeepCopy()
		external := recovery.external
		spec.ExternalClusters = []cnpgv1.ExternalCluster{{Name: external.Name, BarmanObjectStore: external.BarmanObjectStore.DeepCopy()}}
	} else {
		spec.Bootstrap.InitDB = &cnpgv1.InitDBConfiguration{
			Database: databaseName(db),
			Owner:    appContainerName,
			Secret:   corev1.LocalObjectReference{Name: db.Name + "-bootstrap"},
		}
	}
	if plan != nil && plan.archive != nil {
		spec.Backup = cnpgBackupConfiguration(plan)
	}
	return spec
}

// reconcileBackupLifecycle keeps the schedule, the archive record and the dashboard summary in step
// with the running cluster. An error here is an API failure, never a backup problem.
func (r *GeassDatabaseReconciler) reconcileBackupLifecycle(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, cluster *cnpgv1.Cluster, plan *backupPlan) error {
	if plan == nil {
		return r.windDownBackups(ctx, db, wsNS)
	}
	if plan.degraded {
		return nil
	}
	if plan.archive == nil {
		state, message := platform.DatabaseBackupState(plan.health)
		return r.setBackupStatus(ctx, db, &geassv1alpha1.GeassDatabaseBackupStatus{State: state, Message: message})
	}
	if err := r.reconcileScheduledBackup(ctx, db, wsNS); err != nil {
		return err
	}
	observed, err := r.observeBackups(ctx, db, wsNS, cluster, plan.archive)
	if err != nil {
		return err
	}
	if err := r.writeArchiveStatus(ctx, plan.archive, observed, r.liveWindowEnd(plan.archive, observed)); err != nil {
		return err
	}

	return r.setBackupStatus(ctx, db, backupStatusFor(plan.health, observed, plan.archive.Name))
}

// backupStatusFor is the dashboard summary of an archive that is collecting backups, from what its
// storage and the observed backups say.
func backupStatusFor(health platform.BackupHealth, observed observedBackups, archive string) *geassv1alpha1.GeassDatabaseBackupStatus {
	health.Archiving, health.LastSuccess, health.LastFailure = observed.archiving, observed.lastBackup(), observed.lastFailure
	state, message := platform.DatabaseBackupState(health)
	status := &geassv1alpha1.GeassDatabaseBackupStatus{State: state, Message: message, Archive: archive}
	if last := observed.lastBackup(); last != nil {
		status.LastBackupTime = timeToMeta(last)
	}
	if observed.first != nil {
		status.RecoveryWindowStart = timeToMeta(observed.first)
	}
	return status
}

// backupProblem notes that backups could not be brought up to date. Backups are an add-on, so the
// database keeps serving; the caller looks at it again soon.
func backupProblem(ctx context.Context, db *geassv1alpha1.GeassDatabase, err error) {
	logf.FromContext(ctx).Info("Could not update backups", "database", db.Name, "reason", err.Error())
}

func timeToMeta(at *time.Time) *metav1.Time {
	value := metav1.NewTime(*at)
	return &value
}

// windDownBackups handles a database that no longer wants backups: the schedule stops and the
// archive is retired, so earlier restore points stay usable for a while.
func (r *GeassDatabaseReconciler) windDownBackups(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) error {
	live, err := liveArchive(ctx, r.Client, db)
	if err != nil {
		return err
	}
	if live == nil && db.Status.Backup == nil {
		// Nothing was ever set up, or it has been wound down already: do not touch the API again on
		// every reconcile of a database that simply has no backups.
		return nil
	}
	var schedule client.Object = &cnpgv1.ScheduledBackup{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}
	if databaseBackupKind(db) == platform.BackupKindSnapshot {
		schedule = &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: dumpCronJobName(db), Namespace: wsNS}}
	}
	if err := client.IgnoreNotFound(r.Delete(ctx, schedule)); err != nil {
		return err
	}
	if live != nil {
		if err := retireArchive(ctx, r.Client, live, r.now()); err != nil {
			return err
		}
	}
	return r.setBackupStatus(ctx, db, nil)
}

func (r *GeassDatabaseReconciler) retireLiveArchive(ctx context.Context, db *geassv1alpha1.GeassDatabase) error {
	live, err := liveArchive(ctx, r.Client, db)
	if err != nil || live == nil {
		return err
	}
	return retireArchive(ctx, r.Client, live, r.now())
}

func (r *GeassDatabaseReconciler) reconcileScheduledBackup(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) error {
	immediate := true
	scheduled := &cnpgv1.ScheduledBackup{ObjectMeta: metav1.ObjectMeta{Name: db.Name, Namespace: wsNS}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, scheduled, func() error {
		applyGeassLabels(scheduled, db, "GeassDatabase")
		scheduled.Spec = cnpgv1.ScheduledBackupSpec{
			Schedule:             platform.BackupSchedule(db.UID),
			Cluster:              corev1.LocalObjectReference{Name: db.Name},
			Immediate:            &immediate,
			BackupOwnerReference: cnpgv1.BackupOwnerReferenceSelf,
			Method:               cnpgv1.BackupMethodBarmanObjectStore,
		}
		return setSameNamespaceOwner(db, scheduled, r.Scheme)
	})
	return err
}

// observedBackups is what the cluster reports about its backups.
type observedBackups struct {
	first         *time.Time
	lastSuccess   *time.Time
	lastFailure   *time.Time
	archiving     *bool
	restorePoints []geassv1alpha1.GeassRestorePoint
}

// lastBackup is when the newest base backup finished, from whichever source knows.
func (o observedBackups) lastBackup() *time.Time {
	if o.lastSuccess != nil {
		return o.lastSuccess
	}
	if len(o.restorePoints) > 0 {
		newest := o.restorePoints[0].Time.Time
		return &newest
	}
	return nil
}

func (r *GeassDatabaseReconciler) observeBackups(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, cluster *cnpgv1.Cluster, archive *geassv1alpha1.GeassBackupArchive) (observedBackups, error) {
	observed := observedBackups{
		first:       platform.ParseCNPGTime(cluster.Status.FirstRecoverabilityPoint),
		lastSuccess: platform.ParseCNPGTime(cluster.Status.LastSuccessfulBackup),
		lastFailure: platform.ParseCNPGTime(cluster.Status.LastFailedBackup),
	}
	if failed := observed.lastFailure; failed != nil && failed.Before(archive.CreationTimestamp.Time) {
		// The cluster remembers one failure across archives. One from before this archive was made
		// belongs to an earlier archive or bucket and says nothing about these backups.
		observed.lastFailure = nil
	}
	for _, condition := range cluster.Status.Conditions {
		if condition.Type == cnpgv1.ConditionContinuousArchiving {
			archiving := condition.Status == metav1.ConditionTrue
			observed.archiving = &archiving
		}
	}
	var backups cnpgv1.BackupList
	if err := r.List(ctx, &backups, client.InNamespace(wsNS)); err != nil {
		return observed, err
	}
	for i := range backups.Items {
		backup := &backups.Items[i]
		if backup.Spec.Cluster.Name != db.Name || backup.Status.Phase != cnpgv1.BackupPhaseCompleted || backup.Status.BackupID == "" || backup.Status.StoppedAt == nil {
			continue
		}
		if observed.first != nil && backup.Status.StoppedAt.Time.Before(*observed.first) {
			continue // already pruned from backup storage by retention
		}
		observed.restorePoints = append(observed.restorePoints, geassv1alpha1.GeassRestorePoint{ID: backup.Status.BackupID, Time: *backup.Status.StoppedAt})
	}
	slices.SortFunc(observed.restorePoints, func(a, b geassv1alpha1.GeassRestorePoint) int { return b.Time.Compare(a.Time.Time) })
	if len(observed.restorePoints) > platform.MaxRestorePoints {
		observed.restorePoints = observed.restorePoints[:platform.MaxRestorePoints]
	}
	if observed.first == nil && len(observed.restorePoints) > 0 {
		// The operator reports its recoverability point only about half a minute after a backup
		// finishes. Nothing can be restored from before the oldest backup ended, so use that.
		oldest := observed.restorePoints[len(observed.restorePoints)-1].Time.Time
		observed.first = &oldest
	}
	return observed, nil
}

// liveWindowEnd is the latest moment a running database can be restored to. It trails the present
// by a margin, because the newest changes may not have reached backup storage yet, and it stops
// moving while archiving is failing.
func (r *GeassDatabaseReconciler) liveWindowEnd(archive *geassv1alpha1.GeassBackupArchive, observed observedBackups) *time.Time {
	if observed.archiving != nil && !*observed.archiving {
		if archive.Status.RecoveryWindowEnd != nil {
			end := archive.Status.RecoveryWindowEnd.Time
			return &end
		}
		return observed.lastBackup()
	}
	end := r.now().Add(-backupWindowMargin).Truncate(backupWindowMargin)
	if last := observed.lastBackup(); last != nil && last.After(end) {
		end = *last
	}
	return &end
}

// writeArchiveStatus records what is known about an archive's restore points, only when it changed.
func (r *GeassDatabaseReconciler) writeArchiveStatus(ctx context.Context, archive *geassv1alpha1.GeassBackupArchive, observed observedBackups, windowEnd *time.Time) error {
	latest := &geassv1alpha1.GeassBackupArchive{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(archive), latest); err != nil {
		return client.IgnoreNotFound(err)
	}
	updated := latest.DeepCopy()
	updated.Status.RestorePoints = observed.restorePoints
	updated.Status.LastBackupTime = optionalMeta(observed.lastBackup())
	updated.Status.RecoveryWindowStart = optionalMeta(observed.first)
	updated.Status.RecoveryWindowEnd = optionalMeta(windowEnd)
	if equality.Semantic.DeepEqual(latest.Status, updated.Status) {
		return nil
	}
	return r.Status().Update(ctx, updated)
}

func optionalMeta(at *time.Time) *metav1.Time {
	if at == nil {
		return nil
	}
	return timeToMeta(at)
}

// setBackupStatus updates the dashboard summary on the database, only when it changed.
func (r *GeassDatabaseReconciler) setBackupStatus(ctx context.Context, db *geassv1alpha1.GeassDatabase, status *geassv1alpha1.GeassDatabaseBackupStatus) error {
	latest := &geassv1alpha1.GeassDatabase{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(db), latest); err != nil {
		return err
	}
	if equality.Semantic.DeepEqual(latest.Status.Backup, status) {
		return nil
	}
	latest.Status.Backup = status
	return r.Status().Update(ctx, latest)
}

// finalizeBackups runs while a database is being deleted. If the database has backups, it takes
// one last backup so nothing written since the previous one is lost, then retires the archive so
// the backups stay restorable for a while. It never holds up deletion for long: the wait is bounded
// and every failure ends in the archive being retired with whatever restore points already exist.
// It returns how long to wait before checking again, or zero when deletion can go ahead.
func (r *GeassDatabaseReconciler) finalizeBackups(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) (time.Duration, error) {
	log := logf.FromContext(ctx)
	live, err := liveArchive(ctx, r.Client, db)
	if err != nil || live == nil {
		return 0, err
	}
	if databaseBackupKind(db) == platform.BackupKindSnapshot {
		return r.finalizeDumpBackups(ctx, db, wsNS, live)
	}

	cluster := &cnpgv1.Cluster{}
	if err := r.Get(ctx, client.ObjectKey{Name: db.Name, Namespace: wsNS}, cluster); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return 0, retireArchive(ctx, r.Client, live, r.now())
		}
		return 0, err
	}
	if cluster.Status.CurrentPrimary == "" || cluster.Status.ReadyInstances < 1 {
		log.Info("Skipped final backup because the Postgres cluster is not running", "database", db.Name)
		return 0, r.retireWithStatus(ctx, db, wsNS, cluster, live, nil)
	}

	final := &cnpgv1.Backup{}
	err = r.Get(ctx, client.ObjectKey{Name: db.Name + finalBackupSuffix, Namespace: wsNS}, final)
	switch {
	case apierrors.IsNotFound(err):
		final = &cnpgv1.Backup{
			ObjectMeta: metav1.ObjectMeta{Name: db.Name + finalBackupSuffix, Namespace: wsNS},
			Spec:       cnpgv1.BackupSpec{Cluster: corev1.LocalObjectReference{Name: db.Name}, Method: cnpgv1.BackupMethodBarmanObjectStore},
		}
		applyGeassLabels(final, db, "GeassDatabase")
		if createErr := r.Create(ctx, final); createErr != nil {
			log.Info("Could not request final backup", "database", db.Name, "reason", createErr.Error())
			return 0, r.retireWithStatus(ctx, db, wsNS, cluster, live, nil)
		}
		return finalBackupPollInterval, nil
	case err != nil:
		return 0, err
	}

	switch final.Status.Phase {
	case cnpgv1.BackupPhaseCompleted:
		var finishedAt *time.Time
		if final.Status.StoppedAt != nil {
			finishedAt = &final.Status.StoppedAt.Time
		}
		return 0, r.retireWithStatus(ctx, db, wsNS, cluster, live, finishedAt)
	case cnpgv1.BackupPhaseFailed:
		log.Info("Final backup failed", "database", db.Name)
		return 0, r.retireWithStatus(ctx, db, wsNS, cluster, live, nil)
	}
	if !final.CreationTimestamp.IsZero() && r.now().Sub(final.CreationTimestamp.Time) > platform.FinalBackupWait {
		log.Info("Gave up waiting for final backup", "database", db.Name)
		return 0, r.retireWithStatus(ctx, db, wsNS, cluster, live, nil)
	}
	return finalBackupPollInterval, nil
}

// retireWithStatus records the restore points known at deletion, then retires the archive.
// finishedAt is when the final backup ended; without it the window keeps its last known end.
func (r *GeassDatabaseReconciler) retireWithStatus(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, cluster *cnpgv1.Cluster, archive *geassv1alpha1.GeassBackupArchive, finishedAt *time.Time) error {
	observed, err := r.observeBackups(ctx, db, wsNS, cluster, archive)
	if err != nil {
		return err
	}
	end := finishedAt
	if end == nil && archive.Status.RecoveryWindowEnd != nil {
		previous := archive.Status.RecoveryWindowEnd.Time
		end = &previous
	}
	if err := r.writeArchiveStatus(ctx, archive, observed, end); err != nil {
		return err
	}
	return retireArchive(ctx, r.Client, archive, r.now())
}

// databasesForProject maps a project change to the databases that want backups, because the
// project is where their backup storage is chosen.
func (r *GeassDatabaseReconciler) databasesForProject(ctx context.Context, project client.Object) []ctrl.Request {
	var databases geassv1alpha1.GeassDatabaseList
	if err := r.List(ctx, &databases, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil
	}
	var requests []ctrl.Request
	for i := range databases.Items {
		db := &databases.Items[i]
		if db.Spec.Project == project.GetName() && databaseBackupsEnabled(db) {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(db)})
		}
	}
	return requests
}
