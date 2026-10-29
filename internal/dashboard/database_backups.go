package dashboard

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

var (
	errBackupsUnsupported = platform.UserError("Backups are not available for this kind of database")
	errBackupRetention    = platform.UserError("Keep backups for between 1 and 365 days")
	errBackupToggle       = platform.UserError("Choose whether backups are on or off")
	errChooseConnection   = platform.UserError("Choose an AWS connection")
	errNeedsAWS           = platform.UserError("Backups need an AWS connection")
	errChooseBackups      = platform.UserError("Choose which backups to restore")
	errBackupsGone        = platform.UserError("These backups are no longer available")
	errBadRestoreTime     = platform.UserError("Enter a valid date and time")
)

// databaseSupportsBackups reports whether this kind of database has a backup mechanism: Postgres,
// MySQL and SQLite that run in the cluster do; caches and databases that run elsewhere do not.
func databaseSupportsBackups(db *geassv1alpha1.GeassDatabase) bool {
	return platform.DatabaseBackupKind(db.Spec.Engine, db.Spec.Placement, platform.DatabaseWantsHA(db)) != platform.BackupKindNone
}

// defaultBackups is what a new database starts with: backups on for the kinds that support them.
// They protect nothing until the project has somewhere to put them, which the dashboard says.
func defaultBackups(db *geassv1alpha1.GeassDatabase) *geassv1alpha1.GeassDatabaseBackupSpec {
	if !databaseSupportsBackups(db) {
		return nil
	}
	return &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: platform.DefaultBackupRetentionDays}
}

// applyBackupForm applies the backup fields of a settings form. Fields that are absent leave the
// current choice alone, and turning backups off keeps the retention for when they are turned on again.
func applyBackupForm(r *http.Request, db *geassv1alpha1.GeassDatabase) error {
	mode := strings.ToLower(strings.TrimSpace(r.FormValue("backups")))
	days := strings.TrimSpace(r.FormValue("backupRetentionDays"))
	if mode == "" && days == "" {
		return nil
	}
	if !databaseSupportsBackups(db) {
		return errBackupsUnsupported
	}
	spec := &geassv1alpha1.GeassDatabaseBackupSpec{}
	if db.Spec.Backup != nil {
		*spec = *db.Spec.Backup
	}
	if days != "" {
		parsed, err := strconv.Atoi(days)
		if err != nil || parsed < 1 || parsed > int(platform.MaxBackupRetentionDays) {
			return errBackupRetention
		}
		spec.RetentionDays = int32(parsed)
	}
	switch mode {
	case "on":
		spec.Enabled = true
	case "off":
		spec.Enabled = false
	case "":
	default:
		return errBackupToggle
	}
	if spec.Enabled && spec.RetentionDays == 0 {
		spec.RetentionDays = platform.DefaultBackupRetentionDays
	}
	db.Spec.Backup = spec
	return nil
}

// handleProjectBackupStorage chooses, or clears, where a project's databases keep their backups.
// Choosing storage is explicit because it creates a bucket and a scoped user in the user's AWS
// account.
func (s *Server) handleProjectBackupStorage(w http.ResponseWriter, r *http.Request, project string) {
	fallback := workspacePanelURL(project, "", "", "")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	ctx := r.Context()

	if r.FormValue("remove") == "on" {
		err := s.mutateProject(ctx, project, func(p *geassv1alpha1.GeassProject) error {
			p.Spec.BackupStorage = nil
			return nil
		})
		s.replyWebhookMutation(w, r, fallback, err, nil, map[string]any{"ok": true})
		return
	}

	if err := s.Client.Get(ctx, client.ObjectKey{Name: project, Namespace: systemNamespace}, &geassv1alpha1.GeassProject{}); err != nil {
		if apierrors.IsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		redirectFormInternalError(w, r, fallback)
		return
	}
	connectionName := strings.TrimSpace(r.FormValue("connection"))
	if connectionName == "" {
		redirectFormUserError(w, r, fallback, errChooseConnection)
		return
	}
	if err := s.validateCloudConnectionForProject(ctx, project, connectionName); err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}
	var connection geassv1alpha1.GeassCloudConnection
	if err := s.Client.Get(ctx, client.ObjectKey{Name: connectionName, Namespace: systemNamespace}, &connection); err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	if connection.Spec.Provider != geassv1alpha1.CloudProviderAWS {
		redirectFormUserError(w, r, fallback, errNeedsAWS)
		return
	}
	bucket := strings.TrimSpace(r.FormValue("bucket"))
	if bucket != "" {
		if err := platform.ValidBucketName(bucket); err != nil {
			redirectFormUserError(w, r, fallback, err)
			return
		}
	}
	region := strings.TrimSpace(r.FormValue("region"))

	err := s.mutateProject(ctx, project, func(p *geassv1alpha1.GeassProject) error {
		chosen := bucket
		if chosen == "" {
			// Saving again keeps the bucket, or the backups already in it would be orphaned. A
			// different connection is a different AWS account, where that bucket does not exist.
			if existing := p.Spec.BackupStorage; existing != nil && existing.ConnectionRef.Name == connectionName && existing.Bucket != "" {
				chosen = existing.Bucket
			} else {
				generated, err := platform.NewBackupBucketName()
				if err != nil {
					return err
				}
				chosen = generated
			}
		}
		p.Spec.BackupStorage = &geassv1alpha1.GeassBackupStorage{
			ConnectionRef: corev1.LocalObjectReference{Name: connectionName},
			Bucket:        chosen,
			Region:        region,
		}
		return nil
	})
	s.replyWebhookMutation(w, r, fallback, err, nil, map[string]any{"ok": true})
}

// handleDatabaseRestore creates a new database from a database's backups. The original is never
// touched: the new database appears next to it, and the user moves their apps over once they have
// checked the data.
func (s *Server) handleDatabaseRestore(w http.ResponseWriter, r *http.Request) {
	fallback := "/databases"
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	ctx := r.Context()

	archiveName := strings.TrimSpace(r.FormValue("archive"))
	if archiveName == "" {
		redirectFormUserError(w, r, fallback, errChooseBackups)
		return
	}
	var archive geassv1alpha1.GeassBackupArchive
	if err := s.Client.Get(ctx, client.ObjectKey{Name: archiveName, Namespace: systemNamespace}, &archive); err != nil {
		if apierrors.IsNotFound(err) {
			redirectFormUserError(w, r, fallback, errBackupsGone)
			return
		}
		redirectFormInternalError(w, r, fallback)
		return
	}
	fallback = workspaceURL(archive.Spec.Project, string(archive.Spec.Environment), nil)

	restorePoint := strings.TrimSpace(r.FormValue("restorePoint"))
	var restoreTime *metav1.Time
	if value := strings.TrimSpace(r.FormValue("time")); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			redirectFormUserError(w, r, fallback, errBadRestoreTime)
			return
		}
		moment := metav1.NewTime(parsed)
		restoreTime = &moment
	}
	var at *time.Time
	if restoreTime != nil {
		at = &restoreTime.Time
	}
	if err := platform.ValidateRestoreTarget(&archive, restorePoint, at); err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}

	environment := geassv1alpha1.GeassEnvironment(firstNonEmptyString(r.FormValue("environment"), string(archive.Spec.Environment)))
	if err := platform.ValidateProjectPlacement(ctx, s.system(), archive.Spec.Project, environment); err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}

	db := restoredDatabase(&archive, environment, restorePoint, restoreTime)
	est := platform.EstimateWorkload(databaseCreateWorkloadKind(db.Spec.Engine), platform.DatabaseWantsHA(db), 0)
	est = platform.EstimateFromResources(est.Label, databaseResources(db), est.Replicas)
	if s.rejectIfNoCapacityForPlacement(w, r, fallback, est, s.databasePlacement(ctx, db)) {
		return
	}
	if s.refuseUnsandboxableDatabase(w, r, fallback, db) {
		return
	}
	// A restore is a deliberate action with nothing to review, so it deploys straight away, as the
	// database copies made for pull requests do.
	db.Spec.Deploy.Enabled = true
	if err := platform.SetLastDeployedJSON(&db.ObjectMeta, db.Spec); err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	if err := s.createNamed(ctx, db, platform.ResourceDisplayName(archive.ObjectMeta)+"-restored"); err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	_ = s.startProvisionAttempt(ctx, client.ObjectKeyFromObject(db), db)
	redirectAfterResourceCreate(w, r, archive.Spec.Project, string(environment), "databases", db.Name)
}

// restoredDatabase describes the database a restore creates. It must match the original in every
// way the backup depends on: the same engine, the same Postgres version and database name where the
// engine has them, and at least the same amount of disk. Archives from before other engines had
// backups record no engine and are Postgres.
func restoredDatabase(archive *geassv1alpha1.GeassBackupArchive, environment geassv1alpha1.GeassEnvironment, restorePoint string, restoreTime *metav1.Time) *geassv1alpha1.GeassDatabase {
	source := archive.Spec.Source
	resources := source.Resources
	if len(resources.Requests) == 0 {
		resources = platform.DefaultDatabaseResources()
	}
	retention := archive.Spec.RetentionDays
	if retention == 0 {
		retention = platform.DefaultBackupRetentionDays
	}
	engine := source.Engine
	if engine == "" {
		engine = geassv1alpha1.DatabaseEnginePostgres
	}
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: archive.Spec.Project, Environment: environment,
			Engine: engine, Placement: geassv1alpha1.DatabasePlacementInCluster,
			Version: source.Version, DatabaseName: source.DatabaseName, Resources: resources,
			Backup:      &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: retention},
			RestoreFrom: &geassv1alpha1.GeassDatabaseRestoreSpec{Archive: archive.Name, RestorePoint: restorePoint, Time: restoreTime},
		},
	}
	if source.StorageSize != nil {
		size := source.StorageSize.DeepCopy()
		db.Spec.StorageSize = &size
	}
	// Backups of a replicated MariaDB database are restored into a replicated one: the operator replays the archived
	// logs on a database that replicates, and on nothing else.
	if engine == geassv1alpha1.DatabaseEngineMariaDB && source.HighAvailability {
		instances := int32(3)
		if source.Instances != nil {
			instances = *source.Instances
		}
		db.Spec.HighAvailability, db.Spec.Instances = true, &instances
	}
	return db
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
