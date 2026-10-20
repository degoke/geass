package platform

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

var snapshotNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func TestACurrentCopyIsOneThatIsNotOlderThanTwoMissedOnes(t *testing.T) {
	copies := []EtcdSnapshotCopy{
		{Name: "etcd-snapshot-a", WrittenAt: snapshotNow.Add(-30 * time.Hour)},
		{Name: "etcd-snapshot-b", WrittenAt: snapshotNow.Add(-3 * time.Hour)},
		{Name: "etcd-snapshot-c", WrittenAt: snapshotNow.Add(-15 * time.Hour)},
	}
	got := AssessEtcdSnapshots(copies, snapshotNow, snapshotNow.Add(-48*time.Hour))
	require.Equal(t, metav1.ConditionTrue, got.Status)
	require.Equal(t, EtcdSnapshotsReasonCurrent, got.Reason)
	require.Equal(t, "etcd-snapshot-b", got.Newest.Name, "the newest is the one that counts, wherever it is in the listing")
	require.Equal(t, 3, got.Copies)
	require.Contains(t, got.Message, "3 hours old")
	require.Contains(t, got.Message, "etcd-snapshot-b")
}

func TestACopyThatStoppedArrivingIsStale(t *testing.T) {
	// K3s sends one every 12 hours: the newest being a day and a half old means at least two did not arrive.
	got := AssessEtcdSnapshots([]EtcdSnapshotCopy{{Name: "etcd-snapshot-a", WrittenAt: snapshotNow.Add(-36 * time.Hour)}}, snapshotNow, snapshotNow.Add(-72*time.Hour))
	require.Equal(t, metav1.ConditionFalse, got.Status)
	require.Equal(t, EtcdSnapshotsReasonStale, got.Reason)
	require.Contains(t, got.Message, "36 hours old")
	require.Contains(t, got.Message, "journalctl -u k3s")

	just := AssessEtcdSnapshots([]EtcdSnapshotCopy{{Name: "x", WrittenAt: snapshotNow.Add(-EtcdSnapshotsStaleAfter)}}, snapshotNow, snapshotNow.Add(-72*time.Hour))
	require.Equal(t, EtcdSnapshotsReasonCurrent, just.Reason, "right at the limit is not past it")
}

func TestAnEmptyBucketIsWaitedForUntilTheFirstCopyIsOverdue(t *testing.T) {
	waiting := AssessEtcdSnapshots(nil, snapshotNow, snapshotNow.Add(-2*time.Hour))
	require.Equal(t, metav1.ConditionFalse, waiting.Status)
	require.Equal(t, EtcdSnapshotsReasonWaiting, waiting.Reason)
	require.Contains(t, waiting.Message, "k3s etcd-snapshot save", "a person can take one now instead of waiting for noon")
	require.Nil(t, waiting.Newest)

	overdue := AssessEtcdSnapshots(nil, snapshotNow, snapshotNow.Add(-EtcdSnapshotsFirstCopyWithin-time.Minute))
	require.Equal(t, EtcdSnapshotsReasonNoCopy, overdue.Reason)
	require.Contains(t, overdue.Message, "etcd-snapshots enable", "the usual reason is that K3s was never told")
	require.Contains(t, overdue.Message, "13 hours")
}

func TestAgesAreSaidTheWayAPersonSaysThem(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "under a minute", time.Minute: "1 minute", 59 * time.Minute: "59 minutes", time.Hour: "1 hour",
		26 * time.Hour: "26 hours", 47 * time.Hour: "47 hours", 48 * time.Hour: "2 days", 10 * 24 * time.Hour: "10 days", -time.Hour: "under a minute",
	} {
		require.Equal(t, want, ageWords(d), d.String())
	}
}

func TestTheSecretK3sReadsHoldsTheWholeDestination(t *testing.T) {
	spec := &geassv1alpha1.GeassClusterEtcdSnapshotsSpec{
		Bucket: "acme-state", Region: "eu-north-1", Endpoint: "minio.example.com:9000", Folder: "/prod/", Insecure: true,
	}
	data := EtcdSnapshotsSecretData(spec, "AKIA", "shh")
	got := map[string]string{}
	for key, value := range data {
		got[key] = string(value)
	}
	require.Equal(t, map[string]string{
		"etcd-s3-bucket": "acme-state", "etcd-s3-region": "eu-north-1", "etcd-s3-endpoint": "minio.example.com:9000",
		"etcd-s3-folder": "prod", "etcd-s3-access-key": "AKIA", "etcd-s3-secret-key": "shh", "etcd-s3-insecure": "true",
	}, got)

	// Nothing that was not asked for: K3s fills in the AWS endpoint and us-east-1 itself.
	minimal := EtcdSnapshotsSecretData(&geassv1alpha1.GeassClusterEtcdSnapshotsSpec{Bucket: "acme-state"}, "AKIA", "shh")
	require.NotContains(t, minimal, "etcd-s3-endpoint")
	require.NotContains(t, minimal, "etcd-s3-region")
	require.NotContains(t, minimal, "etcd-s3-insecure")
	require.Equal(t, DefaultEtcdSnapshotsFolder, string(minimal["etcd-s3-folder"]))
}

func TestAWSIsToldFromAnotherServerByItsEndpoint(t *testing.T) {
	for endpoint, want := range map[string]bool{
		"": true, "s3.amazonaws.com": true, "S3.EU-NORTH-1.AMAZONAWS.COM": true, "s3.eu-north-1.amazonaws.com": true,
		"minio.example.com:9000": false, "s3.us-west-002.backblazeb2.com": false, "abc.r2.cloudflarestorage.com": false,
	} {
		require.Equal(t, want, EtcdSnapshotsIsAWS(&geassv1alpha1.GeassClusterEtcdSnapshotsSpec{Endpoint: endpoint}), endpoint)
	}
}

func TestTheCredentialsAreReadFromTheNamedSecretOrTheDefault(t *testing.T) {
	spec := &geassv1alpha1.GeassClusterEtcdSnapshotsSpec{}
	require.Equal(t, EtcdSnapshotCredentialsSecretName, EtcdSnapshotsCredentialsName(spec))
	spec.CredentialsSecretRef = &corev1.LocalObjectReference{Name: "mine"}
	require.Equal(t, "mine", EtcdSnapshotsCredentialsName(spec))

	secret := &corev1.Secret{Data: EtcdSnapshotsCredentialsData(" AKIA ", "shh\n")}
	access, key := EtcdSnapshotsCredentials(secret)
	require.Equal(t, "AKIA", access)
	require.Equal(t, "shh", key)
	access, key = EtcdSnapshotsCredentials(&corev1.Secret{StringData: map[string]string{"ACCESS_KEY_ID": "A", "ACCESS_SECRET_KEY": "B"}})
	require.Equal(t, "A", access)
	require.Equal(t, "B", key)
}

func TestNothingAMessageSaysCarriesTheKey(t *testing.T) {
	copies := []EtcdSnapshotCopy{{Name: "etcd-snapshot-a", WrittenAt: snapshotNow.Add(-1 * time.Hour)}}
	for _, got := range []EtcdSnapshotsAssessment{
		AssessEtcdSnapshots(copies, snapshotNow, snapshotNow), AssessEtcdSnapshots(nil, snapshotNow, snapshotNow),
	} {
		require.False(t, strings.Contains(got.Message, "ACCESS"))
	}
}

func TestThePolicyForTheSnapshotBucketOnlyReachesThatBucketAndCannotChangeIt(t *testing.T) {
	var policy struct {
		Statement []struct {
			Effect   string
			Action   []string
			Resource []string
		}
	}
	require.NoError(t, json.Unmarshal([]byte(EtcdSnapshotsPolicy("acme-state")), &policy))
	require.Len(t, policy.Statement, 2)
	var actions []string
	for _, statement := range policy.Statement {
		require.Equal(t, "Allow", statement.Effect)
		actions = append(actions, statement.Action...)
		for _, resource := range statement.Resource {
			require.True(t, resource == "arn:aws:s3:::acme-state" || resource == "arn:aws:s3:::acme-state/*", resource)
		}
	}
	for _, action := range actions {
		require.True(t, strings.HasPrefix(action, "s3:"), action)
		for _, forbidden := range []string{"CreateBucket", "DeleteBucket", "PutBucket", "PutBucketPolicy", "PutLifecycleConfiguration", "*"} {
			require.NotContains(t, action, forbidden, "the key that K3s holds must not be able to change the bucket")
		}
	}
	require.Contains(t, actions, "s3:PutObject")
	require.Contains(t, actions, "s3:ListBucket")
	require.Contains(t, actions, "s3:AbortMultipartUpload", "a cluster big enough to be uploaded in parts")
}
