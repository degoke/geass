package dashboard

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloud"
	"github.com/degoke/geass/pkg/platform"
)

// Recovering databases from a bucket.
//
// A cluster that is lost takes with it the records of what its backups are of. The backups are in the bucket, and beside each
// database's are the record that its archive wrote (pkg/platform/archive_manifest.go). A new cluster that is given the bucket
// and a key to it reads them, makes the projects again under the names they had (a backup's folder is named for its project),
// and makes a record of each database's backups, which the dashboard then offers to restore like any other.
//
// What it makes does not delete anything: the backups may belong to a cluster that is alive, so the records are marked as
// recovered, and when they expire only they go.

const (
	recoverBackupsPath = "/cluster"
	// maxManifestBytes bounds a manifest that is read from a bucket Geass did not necessarily write.
	maxManifestBytes = 64 << 10
	// maxBaseCopiesRead bounds how many records of base copies are read for one database.
	maxBaseCopiesRead = platform.MaxRestorePoints
	// recoveredConnectionPrefix names the connection that a recovered project's backups are read with.
	recoveredConnectionPrefix = "recovered-"
)

// recoveryStorage is what recovery reads a bucket with. Tests substitute it.
type recoveryStorage interface {
	ListFolders(bucket, prefix string) ([]string, error)
	ListKeys(bucket, prefix string) ([]string, error)
	ListObjects(bucket, prefix string) ([]cloud.S3Object, error)
	GetObject(bucket, key string, limit int64) ([]byte, error)
}

func (s *Server) recoveryStorage(accessKey, secretKey, region string) recoveryStorage {
	if s.newRecoveryStorage != nil {
		return s.newRecoveryStorage(accessKey, secretKey, region)
	}
	return &cloud.AWSClient{AccessKey: accessKey, SecretKey: secretKey, Region: region}
}

// recoveredDatabase is one database whose backups were found.
type recoveredDatabase struct {
	Project     string `json:"project"`
	ProjectName string `json:"projectName,omitempty"`
	Environment string `json:"environment"`
	Database    string `json:"database"`
	Engine      string `json:"engine"`
	Archive     string `json:"archive"`
	// Status is found, or already-here when this cluster had a record of them.
	Status        string     `json:"status"`
	RestorePoints int        `json:"restorePoints"`
	Newest        *time.Time `json:"newest,omitempty"`
	OfferedUntil  *time.Time `json:"offeredUntil,omitempty"`
}

// skippedBackups is a folder in the bucket that could not be recovered, and why.
type skippedBackups struct {
	Folder string `json:"folder"`
	Reason string `json:"reason"`
}

type recoveryReport struct {
	Databases []recoveredDatabase `json:"databases"`
	Skipped   []skippedBackups    `json:"skipped"`
	// Projects are the projects that were made again, because the backups name them.
	Projects []string `json:"projects"`
}

type recoveryInput struct {
	Bucket, Region, AccessKey, SecretKey string
}

var projectFolderName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// handleBackupsRecover looks in a bucket for backups that Geass made and makes the records of them again. It answers with what
// it found, and why it left out what it did.
func (s *Server) handleBackupsRecover(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, recoverBackupsPath) || !parseFormOrRedirect(w, r, recoverBackupsPath) {
		return
	}
	fail := func(message string) { redirectProbe(w, r, recoverBackupsPath, dashboardLiteralError, message) }
	input := recoveryInput{
		Bucket: strings.TrimSpace(r.FormValue("bucket")), Region: strings.TrimSpace(r.FormValue("region")),
		AccessKey: strings.TrimSpace(r.FormValue("accessKey")), SecretKey: strings.TrimSpace(r.FormValue("secretKey")),
	}
	if err := platform.ValidBucketName(input.Bucket); err != nil {
		fail(err.Error())
		return
	}
	if input.AccessKey == "" || input.SecretKey == "" {
		fail("enter the AWS access key ID and secret access key that Geass used for backups: it makes a restricted user for the bucket from it, as it does for every project")
		return
	}
	report, err := s.recoverBackups(r.Context(), input)
	if err != nil {
		// What a person can act on (the bucket, the key) is said as it is; anything else is not for the page.
		if platform.IsUserError(err) {
			redirectFormUserError(w, r, recoverBackupsPath, err)
			return
		}
		redirectFormInternalError(w, r, recoverBackupsPath)
		return
	}
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, report)
		return
	}
	redirect(w, r, recoverBackupsPath)
}

func (s *Server) recoverBackups(ctx context.Context, input recoveryInput) (*recoveryReport, error) {
	storage := s.recoveryStorage(input.AccessKey, input.SecretKey, input.Region)
	projects, err := storage.ListFolders(input.Bucket, "")
	if err != nil {
		return nil, platform.UserError("The bucket could not be read with that key: " + summarizeRecoveryError(err))
	}

	type candidate struct {
		manifest platform.ArchiveManifest
		points   []geassv1alpha1.GeassRestorePoint
		newest   *time.Time
	}
	report := &recoveryReport{Databases: []recoveredDatabase{}, Skipped: []skippedBackups{}, Projects: []string{}}
	byProject := map[string][]candidate{}
	for _, folder := range projects {
		project := strings.TrimSuffix(folder, "/")
		if !projectFolderName.MatchString(project) {
			continue
		}
		servers, err := storage.ListFolders(input.Bucket, project)
		if err != nil {
			return nil, platform.UserError("The bucket could not be read: " + summarizeRecoveryError(err))
		}
		for _, serverFolder := range servers {
			serverName := strings.TrimSuffix(strings.TrimPrefix(serverFolder, project+"/"), "/")
			if serverName == "" {
				continue
			}
			manifest, points, newest, skip := readBackups(storage, input.Bucket, project, serverName)
			if skip != "" {
				report.Skipped = append(report.Skipped, skippedBackups{Folder: project + "/" + serverName, Reason: skip})
				continue
			}
			byProject[project] = append(byProject[project], candidate{manifest, points, newest})
		}
	}

	names := make([]string, 0, len(byProject))
	for name := range byProject {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, project := range names {
		candidates := byProject[project]
		displayName, environments := "", []string{}
		for _, c := range candidates {
			if displayName == "" {
				displayName = c.manifest.ProjectDisplayName
			}
			if !slices.Contains(environments, string(c.manifest.Environment)) {
				environments = append(environments, string(c.manifest.Environment))
			}
		}
		// What cannot be made again is said, and what can is: one folder's record that is no good must not keep the others from
		// being found, after some of them were made.
		skipAll := func(what string, err error) {
			for _, c := range candidates {
				report.Skipped = append(report.Skipped, skippedBackups{Folder: project + "/" + c.manifest.ServerName, Reason: what + ": " + summarizeRecoveryError(err)})
			}
		}
		created, err := s.ensureRecoveredProject(ctx, project, displayName, environments)
		if err != nil {
			skipAll("The project could not be made again", err)
			continue
		}
		if created {
			report.Projects = append(report.Projects, project)
		}
		connection, err := s.ensureRecoveredConnection(ctx, project, input)
		if err != nil {
			skipAll("The connection to the bucket could not be made", err)
			continue
		}
		// Only a project made here is pointed at the bucket: one that was here has its own say about where its backups go.
		if created {
			if err := s.useRecoveredBucketForNewBackups(ctx, project, connection, input); err != nil {
				skipAll("The project could not be pointed at the bucket", err)
				continue
			}
		}
		for _, c := range candidates {
			entry, err := s.ensureRecoveredArchive(ctx, c.manifest, c.points, c.newest, connection, input)
			if err != nil {
				report.Skipped = append(report.Skipped, skippedBackups{Folder: project + "/" + c.manifest.ServerName, Reason: "Its record could not be made again: " + summarizeRecoveryError(err)})
				continue
			}
			entry.ProjectName = displayName
			report.Databases = append(report.Databases, entry)
		}
	}
	return report, nil
}

// readBackups reads what one database's folder holds: its record, and the restore points that its engine's layout gives. A
// folder it cannot make a record of is skipped, with why.
func readBackups(storage recoveryStorage, bucket, project, serverName string) (platform.ArchiveManifest, []geassv1alpha1.GeassRestorePoint, *time.Time, string) {
	raw, err := storage.GetObject(bucket, platform.ArchiveManifestKey(project, serverName), maxManifestBytes)
	if errors.Is(err, cloud.ErrObjectNotFound) {
		return platform.ArchiveManifest{}, nil, nil, "It has no record of what it is a backup of (geass-archive.json). Backups made by a release before copies could be found do not have one, until the cluster that made them has run a newer release for a few minutes"
	}
	if err != nil {
		return platform.ArchiveManifest{}, nil, nil, "Its record could not be read: " + summarizeRecoveryError(err)
	}
	manifest, err := platform.ParseArchiveManifest(raw, project, serverName)
	if err != nil {
		return platform.ArchiveManifest{}, nil, nil, "Its record is not one that can be used: " + err.Error()
	}

	var points []geassv1alpha1.GeassRestorePoint
	var newestLog *time.Time
	switch platform.LayoutOf(manifest.Source) {
	case platform.LayoutDumps:
		keys, err := storage.ListKeys(bucket, platform.DumpObjectPrefix(project, serverName))
		if err != nil {
			return manifest, nil, nil, "Its backups could not be listed: " + summarizeRecoveryError(err)
		}
		points = platform.DumpRestorePoints(keys)
	case platform.LayoutPhysical:
		keys, err := storage.ListKeys(bucket, platform.PhysicalObjectPrefix(project, serverName))
		if err != nil {
			return manifest, nil, nil, "Its backups could not be listed: " + summarizeRecoveryError(err)
		}
		points = platform.PhysicalRestorePoints(keys)
		newestLog = newestModified(storage, bucket, platform.BinlogObjectPrefix(project, serverName))
	case platform.LayoutBarman:
		keys, err := storage.ListKeys(bucket, platform.BarmanBasePrefix(project, serverName))
		if err != nil {
			return manifest, nil, nil, "Its backups could not be listed: " + summarizeRecoveryError(err)
		}
		infos := platform.BarmanBackupInfoKeys(keys)
		finished := map[string]time.Time{}
		for _, id := range platform.NewestBarmanBackupIDs(infos, maxBaseCopiesRead) {
			info, err := storage.GetObject(bucket, infos[id], maxManifestBytes)
			if err != nil {
				continue
			}
			if at, ok := platform.ParseBarmanBackupInfo(info); ok {
				finished[id] = at
			}
		}
		points = platform.BarmanRestorePoints(finished)
		newestLog = newestModified(storage, bucket, platform.BarmanLogPrefix(project, serverName))
	}
	if len(points) == 0 {
		return manifest, nil, nil, "It holds no backup that finished, so there is nothing to restore"
	}
	return manifest, points, newestLog, ""
}

// newestModified is when the newest object under a prefix was written, or nil when there is none or it cannot be listed.
func newestModified(storage recoveryStorage, bucket, prefix string) *time.Time {
	objects, err := storage.ListObjects(bucket, prefix)
	if err != nil || len(objects) == 0 {
		return nil
	}
	newest := objects[0].LastModified
	for _, object := range objects[1:] {
		if object.LastModified.After(newest) {
			newest = object.LastModified
		}
	}
	return &newest
}

// ensureRecoveredProject makes a project that the backups name, under the name they have, when this cluster has none of it. A project
// that is here is left as it is.
func (s *Server) ensureRecoveredProject(ctx context.Context, name, displayName string, environments []string) (bool, error) {
	err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &geassv1alpha1.GeassProject{})
	if err == nil {
		return false, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, err
	}
	cluster, err := s.defaultCluster(ctx)
	if err != nil {
		return false, err
	}
	for _, environment := range environments {
		if _, err := platform.ProjectNamespace(name, environment); err != nil {
			return false, err
		}
	}
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			DisplayName: firstNonEmptyString(displayName, name), ClusterRef: corev1.LocalObjectReference{Name: cluster},
			Environments: environments, Network: geassv1alpha1.NewProjectNetwork(),
		},
	}
	if err := s.Client.Create(ctx, project); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, err
	}
	return true, nil
}

// ensureRecoveredConnection makes the AWS connection that a recovered project's backups are read with, from the key it was given,
// and puts the key in the connection's Secret again if it was made before.
func (s *Server) ensureRecoveredConnection(ctx context.Context, project string, input recoveryInput) (string, error) {
	name := recoveredConnectionPrefix + project
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name + "-credentials", Namespace: systemNamespace}}
	data := map[string][]byte{
		platform.SecretKeyAccessKeyID:     []byte(input.AccessKey),
		platform.SecretKeySecretAccessKey: []byte(input.SecretKey),
		platform.SecretKeyRegion:          []byte(input.Region),
	}
	existing := &corev1.Secret{}
	switch err := s.Client.Get(ctx, client.ObjectKeyFromObject(secret), existing); {
	case apierrors.IsNotFound(err):
		secret.Data = data
		if err := s.Client.Create(ctx, secret); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	default:
		existing.Data = data
		if err := s.Client.Update(ctx, existing); err != nil {
			return "", err
		}
	}
	connection := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassCloudConnectionSpec{
			Provider: geassv1alpha1.CloudProviderAWS, SecretRef: corev1.LocalObjectReference{Name: secret.Name},
			Project: project, Region: input.Region,
		},
	}
	if err := s.Client.Create(ctx, connection); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", err
	}
	return name, nil
}

// useRecoveredBucketForNewBackups sets where a recovered project's own backups go, when it has not chosen: the bucket they were
// found in, so that the databases restored from them go on being protected where they were. A project that has chosen is left.
func (s *Server) useRecoveredBucketForNewBackups(ctx context.Context, project, connection string, input recoveryInput) error {
	return s.mutateProject(ctx, project, func(p *geassv1alpha1.GeassProject) error {
		if p.Spec.BackupStorage != nil {
			return nil
		}
		p.Spec.BackupStorage = &geassv1alpha1.GeassBackupStorage{
			ConnectionRef: corev1.LocalObjectReference{Name: connection}, Bucket: input.Bucket, Region: input.Region,
		}
		return nil
	})
}

// ensureRecoveredArchive makes the record of one database's backups from its manifest and what was found, unless this cluster has
// one of them already.
func (s *Server) ensureRecoveredArchive(ctx context.Context, manifest platform.ArchiveManifest, points []geassv1alpha1.GeassRestorePoint, newestLog *time.Time, connection string, input recoveryInput) (recoveredDatabase, error) {
	entry := recoveredDatabase{
		Project: manifest.Project, Environment: string(manifest.Environment), Database: manifest.Database,
		Engine: string(manifest.Source.Engine), RestorePoints: len(points),
	}
	if newest := points[0].Time.Time; !newest.IsZero() {
		entry.Newest = &newest
	}

	var archives geassv1alpha1.GeassBackupArchiveList
	if err := s.Client.List(ctx, &archives, client.InNamespace(systemNamespace)); err != nil {
		return entry, err
	}
	for i := range archives.Items {
		if archives.Items[i].Spec.ServerName == manifest.ServerName && archives.Items[i].Spec.Project == manifest.Project {
			entry.Status, entry.Archive = "already-here", archives.Items[i].Name
			if until := archives.Items[i].Spec.DeleteAfter; until != nil {
				at := until.Time
				entry.OfferedUntil = &at
			}
			return entry, nil
		}
	}

	offeredUntil := metav1.NewTime(time.Now().Add(platform.RecoveredArchiveGrace))
	archive := &geassv1alpha1.GeassBackupArchive{
		ObjectMeta: metav1.ObjectMeta{
			Name: recoveredArchiveName(manifest.ServerName), Namespace: systemNamespace,
			Labels:      map[string]string{platform.LabelManagedBy: platform.ManagedByValue, platform.LabelRecovered: platform.RecoveredValue},
			Annotations: map[string]string{platform.DisplayNameAnnotation: firstNonEmptyString(manifest.DatabaseDisplayName, manifest.Database)},
		},
		Spec: geassv1alpha1.GeassBackupArchiveSpec{
			Project: manifest.Project, Environment: manifest.Environment, Database: manifest.Database,
			DatabaseUID: "recovered-" + manifest.ServerName, ServerName: manifest.ServerName,
			Storage:       geassv1alpha1.GeassBackupStorage{ConnectionRef: corev1.LocalObjectReference{Name: connection}, Bucket: input.Bucket, Region: input.Region},
			RetentionDays: manifest.RetentionDays, Source: manifest.Source, DeleteAfter: &offeredUntil,
		},
	}
	if err := s.Client.Create(ctx, archive); err != nil {
		return entry, err
	}
	start, end, last := platform.RecoveredWindow(points, newestLog)
	archive.Status.RestorePoints, archive.Status.RecoveryWindowStart, archive.Status.RecoveryWindowEnd, archive.Status.LastBackupTime = points, start, end, last
	if err := s.Client.Status().Update(ctx, archive); err != nil {
		return entry, err
	}
	entry.Status, entry.Archive = "found", archive.Name
	at := offeredUntil.Time
	entry.OfferedUntil = &at
	return entry, nil
}

// recoveredArchiveName names a recovered archive for its folder, so that finding the same backups again finds the same record.
func recoveredArchiveName(serverName string) string {
	return "recovered-" + serverName
}

// summarizeRecoveryError keeps what a server said to what fits in a message, and leaves out what is not for a person.
func summarizeRecoveryError(err error) string {
	message := strings.Join(strings.Fields(err.Error()), " ")
	if len(message) > 300 {
		message = message[:300] + "..."
	}
	return message
}
