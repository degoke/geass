package controller

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloud"
	"github.com/degoke/geass/pkg/platform"
)

// backupStorageAPI is the slice of the AWS client that backups use. Tests substitute it.
type backupStorageAPI interface {
	EnsureBucket(bucket string) error
	EnsureBucketUser(buckets []string, userName, existingAccess, existingSecret string) (string, string, error)
	DeletePrefix(bucket, prefix string) error
	ListKeys(bucket, prefix string) ([]string, error)
	DeleteObject(bucket, key string) error
}

// backupStorageFactory builds a storage client from AWS credentials.
type backupStorageFactory func(accessKey, secretKey, region string) backupStorageAPI

func awsBackupStorageFactory(httpClient *http.Client) backupStorageFactory {
	return func(accessKey, secretKey, region string) backupStorageAPI {
		return &cloud.AWSClient{HTTP: httpClient, AccessKey: accessKey, SecretKey: secretKey, Region: region}
	}
}

// awsConnection is a project's AWS credentials, resolved from one of its cloud connections.
type awsConnection struct {
	accessKey, secretKey, region string
}

// resolveAWSConnection reads an AWS cloud connection that belongs to the project. regionOverride
// wins over the connection's own region, which wins over the default.
func resolveAWSConnection(ctx context.Context, c client.Reader, project, name, regionOverride string) (*awsConnection, error) {
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, connection); err != nil {
		return nil, fmt.Errorf("cloud connection %q is unavailable", name)
	}
	if err := platform.ValidateCloudConnectionForProject(*connection, project); err != nil {
		return nil, err
	}
	if connection.Spec.Provider != geassv1alpha1.CloudProviderAWS {
		return nil, platform.UserError("Backups need an AWS connection")
	}
	secret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: platform.SystemNamespace}, secret); err != nil {
		return nil, fmt.Errorf("credentials for cloud connection %q are unavailable", name)
	}
	resolved := &awsConnection{
		accessKey: secretValue(secret, platform.SecretKeyAccessKeyID),
		secretKey: secretValue(secret, platform.SecretKeySecretAccessKey),
		region:    firstNonEmpty(regionOverride, connection.Spec.Region, secretValue(secret, platform.SecretKeyRegion), "us-east-1"),
	}
	if resolved.accessKey == "" || resolved.secretKey == "" {
		return nil, fmt.Errorf("AWS access key and secret key are required on cloud connection %q", name)
	}
	return resolved, nil
}

// liveArchive returns the archive that is currently collecting a database's backups: the one
// for this exact database incarnation that has not been retired. It is nil when there is none.
func liveArchive(ctx context.Context, c client.Reader, db *geassv1alpha1.GeassDatabase) (*geassv1alpha1.GeassBackupArchive, error) {
	archives, err := archivesOf(ctx, c, db)
	if err != nil {
		return nil, err
	}
	for i := range archives {
		if archives[i].Spec.DeleteAfter == nil {
			return &archives[i], nil
		}
	}
	return nil, nil
}

// archivesOf lists every archive, live or retired, that belongs to this database incarnation.
func archivesOf(ctx context.Context, c client.Reader, db *geassv1alpha1.GeassDatabase) ([]geassv1alpha1.GeassBackupArchive, error) {
	var list geassv1alpha1.GeassBackupArchiveList
	if err := c.List(ctx, &list, client.InNamespace(platform.SystemNamespace), client.MatchingLabels{labelGeassName: db.Name}); err != nil {
		return nil, err
	}
	var owned []geassv1alpha1.GeassBackupArchive
	for i := range list.Items {
		if list.Items[i].Spec.Database == db.Name && list.Items[i].Spec.DatabaseUID == string(db.UID) {
			owned = append(owned, list.Items[i])
		}
	}
	return owned, nil
}

// retireArchive schedules an archive's backups for removal. The data stays restorable until
// DeleteAfter; only the archive janitor ever deletes it.
func retireArchive(ctx context.Context, c client.Client, archive *geassv1alpha1.GeassBackupArchive, now time.Time) error {
	latest := &geassv1alpha1.GeassBackupArchive{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(archive), latest); err != nil {
		return client.IgnoreNotFound(err)
	}
	if latest.Spec.DeleteAfter != nil {
		return nil
	}
	deleteAfter := metav1.NewTime(platform.BackupDeleteAfter(now))
	latest.Spec.DeleteAfter = &deleteAfter
	return c.Update(ctx, latest)
}

var (
	storageErrorCode    = regexp.MustCompile(`<Code>([^<]+)</Code>`)
	storageErrorMessage = regexp.MustCompile(`<Message>([^<]+)</Message>`)
)

// summarizeStorageError shortens an AWS error into one line fit for the dashboard. AWS returns XML
// bodies that are unreadable to users, so the code and message are pulled out of them.
func summarizeStorageError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	if code := storageErrorCode.FindStringSubmatch(text); code != nil {
		summary := code[1]
		if message := storageErrorMessage.FindStringSubmatch(text); message != nil {
			summary += ": " + message[1]
		}
		return truncate(summary, 200)
	}
	return truncate(strings.Join(strings.Fields(text), " "), 200)
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return strings.TrimSpace(text[:limit]) + "…"
}
