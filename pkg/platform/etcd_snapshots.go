package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// Copies of the cluster's own state, off the control plane's disk.
//
// K3s takes a snapshot of its database (etcd) twice a day, at 00:00 and 12:00 server time, and keeps five. They sit on
// the disk that holds the database, so a disk that is lost or corrupted takes them too. K3s can also send each one to an
// S3 bucket (--etcd-s3). Two things make that fit Geass:
//
//   - The host is told once, with a drop-in file in /etc/rancher/k3s/config.yaml.d that the installer writes
//     (etcd-s3 and the name of a Secret). That needs a restart of K3s, so it is a step of its own that a person takes.
//   - Where the copies go is in that Secret, which K3s reads again whenever it saves, lists or prunes. So the bucket and
//     its credentials are set, and changed, from the cluster (spec.etcdSnapshots), with no restart.
//
// K3s ignores the Secret when any other --etcd-s3-* flag is set, so the drop-in sets none.
const (
	// EtcdSnapshotsSecretName is the Secret in kube-system that K3s reads its S3 settings from.
	EtcdSnapshotsSecretName = "geass-etcd-snapshots"
	// EtcdSnapshotsSecretNamespace is where K3s looks for it.
	EtcdSnapshotsSecretNamespace = "kube-system"
	// EtcdSnapshotsSecretType is the type K3s documents for that Secret. A Secret's type cannot be changed once it is made.
	EtcdSnapshotsSecretType corev1.SecretType = "etcd.k3s.cattle.io/s3-config-secret"
	// EtcdSnapshotCredentialsSecretName is the Secret in geass-system that holds the bucket's access key, unless
	// spec.etcdSnapshots.credentialsSecretRef names another.
	EtcdSnapshotCredentialsSecretName = "geass-etcd-snapshot-credentials"
	// DefaultEtcdSnapshotsFolder is the prefix in the bucket when none is chosen.
	DefaultEtcdSnapshotsFolder = "geass"

	// ConditionEtcdSnapshotsCurrent says whether a recent copy of the cluster's state is in the bucket. It is not set
	// while spec.etcdSnapshots is.
	ConditionEtcdSnapshotsCurrent = "EtcdSnapshotsCurrent"

	// EtcdSnapshotsRecheck is how often the bucket is looked at. A copy arrives twice a day, so this is plenty.
	EtcdSnapshotsRecheck = 15 * time.Minute
	// EtcdSnapshotsFirstCopyWithin is how long the first copy may take: K3s takes one every 12 hours, and a little over
	// that allows for the one it was just before.
	EtcdSnapshotsFirstCopyWithin = 13 * time.Hour
	// EtcdSnapshotsStaleAfter is how old the newest copy may be before something is wrong: two missed copies, and an hour
	// for the upload.
	EtcdSnapshotsStaleAfter = 25 * time.Hour

	// Reasons of the ConditionEtcdSnapshotsCurrent condition.
	EtcdSnapshotsReasonCurrent        = "CopyCurrent"
	EtcdSnapshotsReasonWaiting        = "WaitingForFirstCopy"
	EtcdSnapshotsReasonNoCopy         = "NoCopyArrived"
	EtcdSnapshotsReasonStale          = "CopyStale"
	EtcdSnapshotsReasonUnreadable     = "BucketUnreadable"
	EtcdSnapshotsReasonNoCredentials  = "CredentialsMissing"
	EtcdSnapshotsReasonSecretNotMade  = "SettingsNotWritten"
	etcdSnapshotsSecretKeyEndpoint    = "etcd-s3-endpoint"
	etcdSnapshotsSecretKeyBucket      = "etcd-s3-bucket"
	etcdSnapshotsSecretKeyRegion      = "etcd-s3-region"
	etcdSnapshotsSecretKeyFolder      = "etcd-s3-folder"
	etcdSnapshotsSecretKeyAccessKey   = "etcd-s3-access-key"
	etcdSnapshotsSecretKeySecretKey   = "etcd-s3-secret-key"
	etcdSnapshotsSecretKeyInsecure    = "etcd-s3-insecure"
	etcdSnapshotsAWSEndpoint          = "s3.amazonaws.com"
	etcdSnapshotsAWSDomainSuffix      = ".amazonaws.com"
	etcdSnapshotsCredentialsKeyAccess = "ACCESS_KEY_ID"
	etcdSnapshotsCredentialsKeySecret = "ACCESS_SECRET_KEY"
)

// EtcdSnapshotsFolder is the prefix the copies are stored under in the bucket.
func EtcdSnapshotsFolder(spec *geassv1alpha1.GeassClusterEtcdSnapshotsSpec) string {
	if folder := strings.Trim(strings.TrimSpace(spec.Folder), "/"); folder != "" {
		return folder
	}
	return DefaultEtcdSnapshotsFolder
}

// EtcdSnapshotsCredentialsName is the Secret in geass-system that holds the bucket's access key.
func EtcdSnapshotsCredentialsName(spec *geassv1alpha1.GeassClusterEtcdSnapshotsSpec) string {
	if spec.CredentialsSecretRef != nil && strings.TrimSpace(spec.CredentialsSecretRef.Name) != "" {
		return spec.CredentialsSecretRef.Name
	}
	return EtcdSnapshotCredentialsSecretName
}

// EtcdSnapshotsCredentials reads the access key out of the Secret that holds it.
func EtcdSnapshotsCredentials(secret *corev1.Secret) (accessKey, secretKey string) {
	read := func(key string) string {
		if raw, ok := secret.Data[key]; ok {
			return strings.TrimSpace(string(raw))
		}
		return strings.TrimSpace(secret.StringData[key])
	}
	return read(etcdSnapshotsCredentialsKeyAccess), read(etcdSnapshotsCredentialsKeySecret)
}

// EtcdSnapshotsCredentialsData is what the credentials Secret holds, so that what writes it and what reads it agree.
func EtcdSnapshotsCredentialsData(accessKey, secretKey string) map[string][]byte {
	return map[string][]byte{
		etcdSnapshotsCredentialsKeyAccess: []byte(strings.TrimSpace(accessKey)),
		etcdSnapshotsCredentialsKeySecret: []byte(strings.TrimSpace(secretKey)),
	}
}

// EtcdSnapshotsIsAWS says that the destination is AWS S3 itself, which is addressed by bucket and region and not by
// an endpoint. An endpoint of a region (s3.eu-north-1.amazonaws.com) counts: AWS names them all the same way.
func EtcdSnapshotsIsAWS(spec *geassv1alpha1.GeassClusterEtcdSnapshotsSpec) bool {
	endpoint := strings.ToLower(strings.TrimSpace(spec.Endpoint))
	return endpoint == "" || endpoint == etcdSnapshotsAWSEndpoint || strings.HasSuffix(endpoint, etcdSnapshotsAWSDomainSuffix)
}

// EtcdSnapshotsSecretData is what K3s reads from its Secret: the keys are the names of its --etcd-s3-* flags. It
// holds the whole destination, because K3s reads the Secret only when no other S3 flag is set.
func EtcdSnapshotsSecretData(spec *geassv1alpha1.GeassClusterEtcdSnapshotsSpec, accessKey, secretKey string) map[string][]byte {
	data := map[string][]byte{
		etcdSnapshotsSecretKeyBucket:    []byte(spec.Bucket),
		etcdSnapshotsSecretKeyFolder:    []byte(EtcdSnapshotsFolder(spec)),
		etcdSnapshotsSecretKeyAccessKey: []byte(accessKey),
		etcdSnapshotsSecretKeySecretKey: []byte(secretKey),
	}
	if endpoint := strings.TrimSpace(spec.Endpoint); endpoint != "" {
		data[etcdSnapshotsSecretKeyEndpoint] = []byte(endpoint)
	}
	if region := strings.TrimSpace(spec.Region); region != "" {
		data[etcdSnapshotsSecretKeyRegion] = []byte(region)
	}
	if spec.Insecure {
		data[etcdSnapshotsSecretKeyInsecure] = []byte(strconv.FormatBool(true))
	}
	return data
}

// EtcdSnapshotCopy is one snapshot found in the bucket.
type EtcdSnapshotCopy struct {
	Name      string
	WrittenAt time.Time
}

// EtcdSnapshotsAssessment is what the bucket's contents mean.
type EtcdSnapshotsAssessment struct {
	Status  metav1.ConditionStatus
	Reason  string
	Message string
	// Newest is the most recent copy, nil when there is none.
	Newest *EtcdSnapshotCopy
	Copies int
}

// AssessEtcdSnapshots says whether the bucket holds a recent enough copy. waitingSince is when this was first looked at
// with nothing found, which is what the first copy's allowance counts from.
func AssessEtcdSnapshots(copies []EtcdSnapshotCopy, now, waitingSince time.Time) EtcdSnapshotsAssessment {
	assessment := EtcdSnapshotsAssessment{Copies: len(copies)}
	for i := range copies {
		if assessment.Newest == nil || copies[i].WrittenAt.After(assessment.Newest.WrittenAt) {
			assessment.Newest = &copies[i]
		}
	}
	if assessment.Newest == nil {
		if waitingSince.IsZero() || now.Sub(waitingSince) < EtcdSnapshotsFirstCopyWithin {
			assessment.Status, assessment.Reason = metav1.ConditionFalse, EtcdSnapshotsReasonWaiting
			assessment.Message = "Waiting for the first copy. K3s takes one at 00:00 and 12:00, server time. To take one now, " +
				"run `sudo k3s etcd-snapshot save` on the control plane"
			return assessment
		}
		assessment.Status, assessment.Reason = metav1.ConditionFalse, EtcdSnapshotsReasonNoCopy
		assessment.Message = fmt.Sprintf("No copy has reached the bucket in %s. K3s on the control plane may not have been told "+
			"to send copies: run the installer on it with `etcd-snapshots enable`, then with `etcd-snapshots status` "+
			"(see Disaster recovery in the documentation)", ageWords(now.Sub(waitingSince)))
		return assessment
	}
	age := now.Sub(assessment.Newest.WrittenAt)
	if age > EtcdSnapshotsStaleAfter {
		assessment.Status, assessment.Reason = metav1.ConditionFalse, EtcdSnapshotsReasonStale
		assessment.Message = fmt.Sprintf("The newest copy in the bucket is %s old (%s). K3s sends one twice a day, so sending has "+
			"stopped: look at `sudo journalctl -u k3s` on the control plane for the reason", ageWords(age), assessment.Newest.Name)
		return assessment
	}
	assessment.Status, assessment.Reason = metav1.ConditionTrue, EtcdSnapshotsReasonCurrent
	assessment.Message = fmt.Sprintf("The newest copy in the bucket is %s old (%s), and the bucket holds %d", ageWords(age), assessment.Newest.Name, len(copies))
	return assessment
}

// ageWords is a length of time as a person says it: "3 hours", "26 hours", "2 days".
func ageWords(d time.Duration) string {
	d = max(d, 0)
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return countUnit(int(d/time.Minute), "minute")
	case d < 48*time.Hour:
		return countUnit(int(d/time.Hour), "hour")
	default:
		return countUnit(int(d/(24*time.Hour)), "day")
	}
}

func countUnit(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// EtcdSnapshotsPolicy is the AWS IAM policy that an access key for the snapshot bucket needs, and no more. K3s lists the
// bucket, and reads, writes and deletes the snapshots in it; the multipart actions are for a snapshot of a cluster that is big
// enough (about 16 MiB) to be uploaded in parts. It cannot make or delete the bucket, change its settings, or reach another.
//
// It is the one definition: the documentation, the dashboard's help and the test that runs K3s against a bucket all use it.
func EtcdSnapshotsPolicy(bucket string) string {
	type statement struct {
		Effect   string   `json:"Effect"`
		Action   []string `json:"Action"`
		Resource []string `json:"Resource"`
	}
	raw, err := json.MarshalIndent(struct {
		Version   string      `json:"Version"`
		Statement []statement `json:"Statement"`
	}{
		Version: "2012-10-17",
		Statement: []statement{
			{
				Effect:   "Allow",
				Action:   []string{"s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketMultipartUploads"},
				Resource: []string{"arn:aws:s3:::" + bucket},
			},
			{
				Effect:   "Allow",
				Action:   []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"},
				Resource: []string{"arn:aws:s3:::" + bucket + "/*"},
			},
		},
	}, "", "  ")
	if err != nil {
		return ""
	}
	return string(raw)
}

var (
	etcdSnapshotsBucketShape   = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	etcdSnapshotsEndpointShape = regexp.MustCompile(`^[A-Za-z0-9.-]+(:[0-9]+)?$`)
	etcdSnapshotsFolderShape   = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)
	etcdSnapshotsRegionShape   = regexp.MustCompile(`^[a-z0-9-]*$`)
)

// ValidateEtcdSnapshots says why a destination cannot work, in words for the person who typed it. The cluster's definition
// checks the same things, for whoever applies it with kubectl; this is for the dashboard and the installer, which can say
// more than "the request is invalid".
func ValidateEtcdSnapshots(spec *geassv1alpha1.GeassClusterEtcdSnapshotsSpec) error {
	switch {
	case spec == nil:
		return errors.New("no destination was given")
	case !etcdSnapshotsBucketShape.MatchString(spec.Bucket):
		return fmt.Errorf("%q is not a bucket name: use 3 to 63 lower-case letters, digits, dots and hyphens", spec.Bucket)
	case spec.Endpoint != "" && !etcdSnapshotsEndpointShape.MatchString(spec.Endpoint):
		return fmt.Errorf("%q is not a server address: give a host, and a port if it is not the usual one, such as s3.eu-north-1.amazonaws.com "+
			"or minio.example.com:9000, with no https:// in front", spec.Endpoint)
	case !etcdSnapshotsRegionShape.MatchString(spec.Region) || len(spec.Region) > 40:
		return fmt.Errorf("%q is not a region such as eu-north-1", spec.Region)
	case !etcdSnapshotsFolderShape.MatchString(spec.Folder) || len(spec.Folder) > 200:
		return fmt.Errorf("%q is not a folder name: use letters, digits, dots, hyphens, underscores and slashes", spec.Folder)
	case spec.Insecure && EtcdSnapshotsIsAWS(spec):
		return errors.New("AWS S3 cannot be reached over plain HTTP: leave insecure off, or name the server that is not AWS")
	}
	return nil
}
