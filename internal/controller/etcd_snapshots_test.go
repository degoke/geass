package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloud"
	"github.com/degoke/geass/pkg/platform"
)

var snapshotTestNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// fakeBucket is what the operator looks into, and what it was asked for.
type fakeBucket struct {
	objects []cloud.S3Object
	err     error

	asked   []string
	spec    geassv1alpha1.GeassClusterEtcdSnapshotsSpec
	access  string
	secret  string
	created int
}

func (b *fakeBucket) ListObjects(bucket, prefix string) ([]cloud.S3Object, error) {
	b.asked = append(b.asked, bucket+"/"+prefix)
	return b.objects, b.err
}

type snapshotFixture struct {
	r       *GeassClusterReconciler
	c       client.Client
	bucket  *fakeBucket
	cluster *geassv1alpha1.GeassCluster
	now     time.Time
}

func newSnapshotFixture(t *testing.T, spec *geassv1alpha1.GeassClusterEtcdSnapshotsSpec, objects ...client.Object) *snapshotFixture {
	t.Helper()
	scheme := longhornTestScheme(t)
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassClusterSpec{EtcdSnapshots: spec},
	}
	all := append([]client.Object{cluster}, objects...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(all...).WithStatusSubresource(&geassv1alpha1.GeassCluster{}).Build()
	f := &snapshotFixture{c: c, bucket: &fakeBucket{}, cluster: cluster, now: snapshotTestNow}
	f.r = &GeassClusterReconciler{
		Client: c, Scheme: scheme,
		etcdSnapshotsNow: func() time.Time { return f.now },
		newEtcdSnapshotLister: func(spec *geassv1alpha1.GeassClusterEtcdSnapshotsSpec, access, secret string) etcdSnapshotLister {
			f.bucket.spec, f.bucket.access, f.bucket.secret = *spec, access, secret
			f.bucket.created++
			return f.bucket
		},
	}
	return f
}

func (f *snapshotFixture) reconcile() time.Duration {
	f.cluster.Spec.EtcdSnapshots = f.latest().Spec.EtcdSnapshots
	return f.r.reconcileEtcdSnapshots(context.Background(), f.cluster)
}

func (f *snapshotFixture) latest() *geassv1alpha1.GeassCluster {
	latest := &geassv1alpha1.GeassCluster{}
	if err := f.c.Get(context.Background(), client.ObjectKeyFromObject(f.cluster), latest); err != nil {
		panic(err)
	}
	return latest
}

func (f *snapshotFixture) condition() *metav1.Condition {
	return meta.FindStatusCondition(f.latest().Status.Conditions, platform.ConditionEtcdSnapshotsCurrent)
}

func (f *snapshotFixture) k3sSecret() *corev1.Secret {
	secret := &corev1.Secret{}
	err := f.c.Get(context.Background(), client.ObjectKey{Name: platform.EtcdSnapshotsSecretName, Namespace: platform.EtcdSnapshotsSecretNamespace}, secret)
	if err != nil {
		return nil
	}
	return secret
}

func snapshotCredentials(access, secret string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.EtcdSnapshotCredentialsSecretName, Namespace: platform.SystemNamespace},
		Data:       platform.EtcdSnapshotsCredentialsData(access, secret),
	}
}

func snapshotSpec() *geassv1alpha1.GeassClusterEtcdSnapshotsSpec {
	return &geassv1alpha1.GeassClusterEtcdSnapshotsSpec{Bucket: "acme-state", Region: "eu-north-1"}
}

func TestACluserThatAskedForNoCopiesIsLeftAlone(t *testing.T) {
	f := newSnapshotFixture(t, nil)
	require.Zero(t, f.reconcile())
	require.Nil(t, f.condition())
	require.Nil(t, f.k3sSecret())
	require.Zero(t, f.bucket.created, "the bucket is not looked at when none was named")
}

func TestTheSettingsK3sReadsAreWrittenFromTheSpecAndTheKey(t *testing.T) {
	f := newSnapshotFixture(t, snapshotSpec(), snapshotCredentials("AKIA", "shh"))
	f.reconcile()

	secret := f.k3sSecret()
	require.NotNil(t, secret)
	require.Equal(t, platform.EtcdSnapshotsSecretType, secret.Type)
	require.Equal(t, "acme-state", string(secret.Data["etcd-s3-bucket"]))
	require.Equal(t, "AKIA", string(secret.Data["etcd-s3-access-key"]))
	require.Equal(t, "shh", string(secret.Data["etcd-s3-secret-key"]))
	require.Equal(t, platform.ManagedByValue, secret.Labels[platform.LabelManagedBy])
	require.Equal(t, "AKIA", f.bucket.access, "the bucket is read with the same key that K3s writes with")
	require.Equal(t, []string{"acme-state/geass"}, f.bucket.asked)
}

func TestWithNoKeyInTheClusterTheStatusSaysSoAndNothingIsSentToK3s(t *testing.T) {
	f := newSnapshotFixture(t, snapshotSpec())
	require.Equal(t, etcdSnapshotsRetryFast, f.reconcile())

	condition := f.condition()
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, platform.EtcdSnapshotsReasonNoCredentials, condition.Reason)
	require.Contains(t, condition.Message, "geass-system/"+platform.EtcdSnapshotCredentialsSecretName)
	require.Nil(t, f.k3sSecret(), "K3s is not given settings that cannot work")
	require.Zero(t, f.bucket.created)

	// An access key with half of it missing is no key.
	half := newSnapshotFixture(t, snapshotSpec(), snapshotCredentials("AKIA", ""))
	half.reconcile()
	require.Equal(t, platform.EtcdSnapshotsReasonNoCredentials, half.condition().Reason)
}

func TestACredentialsSecretCanBeNamed(t *testing.T) {
	spec := snapshotSpec()
	spec.CredentialsSecretRef = &corev1.LocalObjectReference{Name: "state-bucket-key"}
	named := snapshotCredentials("NAMED", "key")
	named.Name = "state-bucket-key"
	f := newSnapshotFixture(t, spec, named, snapshotCredentials("DEFAULT", "key"))
	f.reconcile()
	require.Equal(t, "NAMED", f.bucket.access)
}

func TestACopyThatIsInTheBucketMakesTheClusterCurrentAndSaysWhichOne(t *testing.T) {
	f := newSnapshotFixture(t, snapshotSpec(), snapshotCredentials("AKIA", "shh"))
	f.bucket.objects = []cloud.S3Object{
		{Key: "geass/etcd-snapshot-cp1-100", LastModified: snapshotTestNow.Add(-14 * time.Hour)},
		{Key: "geass/etcd-snapshot-cp1-200", LastModified: snapshotTestNow.Add(-2 * time.Hour)},
	}
	require.Equal(t, platform.EtcdSnapshotsRecheck, f.reconcile())

	condition := f.condition()
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, platform.EtcdSnapshotsReasonCurrent, condition.Reason)
	status := f.latest().Status.EtcdSnapshots
	require.NotNil(t, status)
	require.Equal(t, "etcd-snapshot-cp1-200", status.LastCopyName, "the name that k3s server --cluster-reset-restore-path is given, without the folder")
	require.EqualValues(t, 2, status.Copies)
	require.True(t, status.LastCopyTime.Time.Equal(snapshotTestNow.Add(-2*time.Hour)))
	require.NotNil(t, status.CheckedAt)
}

func TestTheFirstCopyIsWaitedForAndThenItsAbsenceIsAProblem(t *testing.T) {
	f := newSnapshotFixture(t, snapshotSpec(), snapshotCredentials("AKIA", "shh"))
	require.Equal(t, etcdSnapshotsRetryWaiting, f.reconcile())
	require.Equal(t, platform.EtcdSnapshotsReasonWaiting, f.condition().Reason)

	f.now = f.now.Add(platform.EtcdSnapshotsFirstCopyWithin - time.Minute)
	f.reconcile()
	require.Equal(t, platform.EtcdSnapshotsReasonWaiting, f.condition().Reason, "still inside the allowance")

	f.now = f.now.Add(2 * time.Minute)
	f.reconcile()
	require.Equal(t, platform.EtcdSnapshotsReasonNoCopy, f.condition().Reason)
}

// The allowance for the first copy counts from when the destination was set. Setting a different bucket starts it again,
// and a new key for the same bucket does not.
func TestTheFirstCopysAllowanceStartsAgainOnlyForANewDestination(t *testing.T) {
	f := newSnapshotFixture(t, snapshotSpec(), snapshotCredentials("AKIA", "shh"))
	f.reconcile()
	since := f.k3sSecret().Annotations[annotationEtcdSnapshotsSince]

	f.now = f.now.Add(10 * time.Hour)
	live := &corev1.Secret{}
	require.NoError(t, f.c.Get(context.Background(), client.ObjectKey{Name: platform.EtcdSnapshotCredentialsSecretName, Namespace: platform.SystemNamespace}, live))
	live.Data = platform.EtcdSnapshotsCredentialsData("ROTATED", "newshh")
	require.NoError(t, f.c.Update(context.Background(), live))
	f.reconcile()
	require.Equal(t, since, f.k3sSecret().Annotations[annotationEtcdSnapshotsSince], "a rotated key is not a new destination")
	require.Equal(t, "ROTATED", string(f.k3sSecret().Data["etcd-s3-access-key"]), "but K3s does get the new key, with no restart")

	latest := f.latest()
	latest.Spec.EtcdSnapshots.Bucket = "another-bucket"
	require.NoError(t, f.c.Update(context.Background(), latest))
	f.reconcile()
	require.NotEqual(t, since, f.k3sSecret().Annotations[annotationEtcdSnapshotsSince])
	require.Equal(t, "another-bucket", string(f.k3sSecret().Data["etcd-s3-bucket"]))
}

func TestACopyThatStoppedArrivingIsReported(t *testing.T) {
	f := newSnapshotFixture(t, snapshotSpec(), snapshotCredentials("AKIA", "shh"))
	f.bucket.objects = []cloud.S3Object{{Key: "geass/etcd-snapshot-cp1-100", LastModified: snapshotTestNow.Add(-40 * time.Hour)}}
	f.reconcile()
	require.Equal(t, platform.EtcdSnapshotsReasonStale, f.condition().Reason)
	require.Equal(t, "etcd-snapshot-cp1-100", f.latest().Status.EtcdSnapshots.LastCopyName, "what is known of the last one stays known")
}

func TestABucketThatCannotBeReadIsReportedWithoutTheKeyAndKeepsWhatWasKnown(t *testing.T) {
	f := newSnapshotFixture(t, snapshotSpec(), snapshotCredentials("AKIA", "shh"))
	f.bucket.objects = []cloud.S3Object{{Key: "geass/etcd-snapshot-cp1-100", LastModified: snapshotTestNow.Add(-1 * time.Hour)}}
	f.reconcile()
	require.Equal(t, platform.EtcdSnapshotsReasonCurrent, f.condition().Reason)

	f.bucket.objects, f.bucket.err = nil, errors.New("AWS S3 403 Forbidden: <Error><Code>AccessDenied</Code></Error>")
	require.Equal(t, etcdSnapshotsRetryFast, f.reconcile())
	condition := f.condition()
	require.Equal(t, platform.EtcdSnapshotsReasonUnreadable, condition.Reason)
	require.Contains(t, condition.Message, "AccessDenied")
	require.NotContains(t, condition.Message, "shh")
	require.Equal(t, "etcd-snapshot-cp1-100", f.latest().Status.EtcdSnapshots.LastCopyName, "a bucket that cannot be read now still held that copy")
}

func TestAnErrorFromAServerIsKeptToWhatFitsACondition(t *testing.T) {
	f := newSnapshotFixture(t, snapshotSpec(), snapshotCredentials("AKIA", "shh"))
	f.bucket.err = errors.New("AWS S3 500: " + string(make([]byte, 5000)))
	f.reconcile()
	require.Less(t, len(f.condition().Message), 700)
}

func TestTheSettingsAreMadeAgainWhenSomeoneEditsThemAndLeftAloneWhenNothingChanged(t *testing.T) {
	f := newSnapshotFixture(t, snapshotSpec(), snapshotCredentials("AKIA", "shh"))
	f.reconcile()
	first := f.k3sSecret()
	f.reconcile()
	require.Equal(t, first.ResourceVersion, f.k3sSecret().ResourceVersion, "nothing is written when nothing changed")

	tampered := f.k3sSecret()
	tampered.Data["etcd-s3-bucket"] = []byte("someone-elses")
	require.NoError(t, f.c.Update(context.Background(), tampered))
	f.reconcile()
	require.Equal(t, "acme-state", string(f.k3sSecret().Data["etcd-s3-bucket"]))
}

func TestASecretOfTheWrongTypeIsReplacedBecauseTheTypeCannotBeChanged(t *testing.T) {
	wrong := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.EtcdSnapshotsSecretName, Namespace: platform.EtcdSnapshotsSecretNamespace},
		Data:       map[string][]byte{"x": []byte("y")},
	}
	f := newSnapshotFixture(t, snapshotSpec(), snapshotCredentials("AKIA", "shh"), wrong)
	f.reconcile()
	require.Equal(t, platform.EtcdSnapshotsSecretType, f.k3sSecret().Type)
	require.Equal(t, "acme-state", string(f.k3sSecret().Data["etcd-s3-bucket"]))
}

func TestWhenCopiesAreNoLongerWantedTheSecretGeassMadeGoesAndAnotherOnesDoesNot(t *testing.T) {
	f := newSnapshotFixture(t, snapshotSpec(), snapshotCredentials("AKIA", "shh"))
	f.bucket.objects = []cloud.S3Object{{Key: "geass/etcd-snapshot-cp1-100", LastModified: snapshotTestNow.Add(-1 * time.Hour)}}
	f.reconcile()
	require.NotNil(t, f.k3sSecret())
	require.NotNil(t, f.condition())

	latest := f.latest()
	latest.Spec.EtcdSnapshots = nil
	require.NoError(t, f.c.Update(context.Background(), latest))
	f.reconcile()
	require.Nil(t, f.k3sSecret())
	require.Nil(t, f.condition())
	require.Nil(t, f.latest().Status.EtcdSnapshots)

	theirs := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.EtcdSnapshotsSecretName, Namespace: platform.EtcdSnapshotsSecretNamespace},
		Type:       platform.EtcdSnapshotsSecretType,
	}
	other := newSnapshotFixture(t, nil, theirs)
	other.reconcile()
	require.NotNil(t, other.k3sSecret(), "a Secret that Geass did not make is not Geass's to delete")
}

func TestOnlyAnAWSDestinationIsAddressedByRegion(t *testing.T) {
	aws := awsEtcdSnapshotLister(nil)(&geassv1alpha1.GeassClusterEtcdSnapshotsSpec{Bucket: "b", Region: "eu-north-1"}, "a", "s")
	require.Empty(t, aws.(*cloud.AWSClient).Endpoint)
	require.Equal(t, "eu-north-1", aws.(*cloud.AWSClient).Region)

	minio := awsEtcdSnapshotLister(nil)(&geassv1alpha1.GeassClusterEtcdSnapshotsSpec{Bucket: "b", Endpoint: "minio.example.com:9000"}, "a", "s")
	require.Equal(t, "https://minio.example.com:9000", minio.(*cloud.AWSClient).Endpoint)
	plain := awsEtcdSnapshotLister(nil)(&geassv1alpha1.GeassClusterEtcdSnapshotsSpec{Bucket: "b", Endpoint: "minio.local:9000", Insecure: true}, "a", "s")
	require.Equal(t, "http://minio.local:9000", plain.(*cloud.AWSClient).Endpoint)
}

// The cluster is reconciled for every node and volume that changes, by one worker, and a look in the bucket is a call over the
// network. It is not made again within a couple of minutes unless where the copies go has changed.
func TestTheBucketIsNotLookedInAgainWithinTwoMinutesUnlessWhereItIsChanged(t *testing.T) {
	f := newSnapshotFixture(t, snapshotSpec(), snapshotCredentials("AKIA", "shh"))
	f.bucket.objects = []cloud.S3Object{{Key: "geass/etcd-snapshot-cp1-100", LastModified: snapshotTestNow.Add(-1 * time.Hour)}}
	f.reconcile()
	require.Len(t, f.bucket.asked, 1)

	// The status as the operator wrote it, which is what the next pass reads.
	f.cluster = f.latest()
	f.now = f.now.Add(30 * time.Second)
	wait := f.r.reconcileEtcdSnapshots(context.Background(), f.cluster)
	require.Len(t, f.bucket.asked, 1, "looked at 30 seconds ago, and nothing has changed")
	require.InDelta(t, (etcdSnapshotsMinInterval - 30*time.Second).Seconds(), wait.Seconds(), 1, "it asks to be visited when the time is up")

	f.now = f.now.Add(2 * time.Minute)
	f.r.reconcileEtcdSnapshots(context.Background(), f.cluster)
	require.Len(t, f.bucket.asked, 2, "and again once it has been long enough")

	// A different bucket is looked in at once, whatever the time.
	f.cluster = f.latest()
	f.cluster.Spec.EtcdSnapshots.Bucket = "another-bucket"
	f.now = f.now.Add(time.Second)
	f.r.reconcileEtcdSnapshots(context.Background(), f.cluster)
	require.Len(t, f.bucket.asked, 3)
	require.Equal(t, "another-bucket/geass", f.bucket.asked[2])
}
