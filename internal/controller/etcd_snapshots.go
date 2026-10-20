package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloud"
	"github.com/degoke/geass/pkg/platform"
)

// Copies of the cluster's own state in an S3 bucket (pkg/platform/etcd_snapshots.go says how the pieces fit).
//
// This is the cluster's part. K3s on the control plane is told, once, by the installer, to send every snapshot to S3 and to
// read where from the Secret kube-system/geass-etcd-snapshots. The operator keeps that Secret in step with
// spec.etcdSnapshots, which is what lets the bucket and its key be changed from the dashboard with no restart, and then
// looks in the bucket to see whether copies are arriving. It cannot see the host, so the bucket is the evidence: a copy
// that is not there is not a copy, whatever K3s believes.
//
// Like the other background work of the cluster it never returns an error and never holds the cluster back: the cluster
// reconciler has one worker, and what it returns counts toward the failures that pause it.

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

const (
	annotationEtcdSnapshotsConfig = "geass.dev/etcd-snapshots-config"
	annotationEtcdSnapshotsSince  = "geass.dev/etcd-snapshots-since"

	// etcdSnapshotsRetryFast is how soon to look again when what is wrong is likely to be put right soon: the key is not
	// in the cluster yet, or the bucket could not be read.
	etcdSnapshotsRetryFast = time.Minute
	// etcdSnapshotsRetryWaiting is how often to look while no copy has arrived yet.
	etcdSnapshotsRetryWaiting = 5 * time.Minute

	etcdSnapshotsErrorLimit = 300
	// etcdSnapshotsMinInterval is the least time between two looks in the bucket, however often the cluster is reconciled. The
	// cluster reconciler has one worker and runs for every node and volume that changes, and a look is a call over the network
	// that can take as long as its timeout, so it is not made each time.
	etcdSnapshotsMinInterval = 2 * time.Minute
	// etcdSnapshotsLookTimeout bounds one look. K3s sends two copies a day: nothing here is worth waiting half a minute for.
	etcdSnapshotsLookTimeout = 15 * time.Second
	// maxEtcdSnapshotCopiesCounted keeps the count of copies in what a status field holds.
	maxEtcdSnapshotCopiesCounted = 1 << 30
)

// etcdSnapshotLister is the part of the S3 client that this reads with. Tests substitute it.
type etcdSnapshotLister interface {
	ListObjects(bucket, prefix string) ([]cloud.S3Object, error)
}

// etcdSnapshotListerFactory builds one for a destination.
type etcdSnapshotListerFactory func(spec *geassv1alpha1.GeassClusterEtcdSnapshotsSpec, accessKey, secretKey string) etcdSnapshotLister

func awsEtcdSnapshotLister(httpClient *http.Client) etcdSnapshotListerFactory {
	return func(spec *geassv1alpha1.GeassClusterEtcdSnapshotsSpec, accessKey, secretKey string) etcdSnapshotLister {
		if httpClient == nil {
			httpClient = &http.Client{Timeout: etcdSnapshotsLookTimeout}
		}
		c := &cloud.AWSClient{HTTP: httpClient, AccessKey: accessKey, SecretKey: secretKey, Region: spec.Region}
		if !platform.EtcdSnapshotsIsAWS(spec) {
			scheme := "https"
			if spec.Insecure {
				scheme = "http"
			}
			c.Endpoint = scheme + "://" + strings.TrimSpace(spec.Endpoint)
		}
		return c
	}
}

func (r *GeassClusterReconciler) etcdSnapshotLister(spec *geassv1alpha1.GeassClusterEtcdSnapshotsSpec, accessKey, secretKey string) etcdSnapshotLister {
	if r.newEtcdSnapshotLister != nil {
		return r.newEtcdSnapshotLister(spec, accessKey, secretKey)
	}
	return awsEtcdSnapshotLister(nil)(spec, accessKey, secretKey)
}

func (r *GeassClusterReconciler) snapshotClock() time.Time {
	if r.etcdSnapshotsNow != nil {
		return r.etcdSnapshotsNow()
	}
	return time.Now()
}

// reconcileEtcdSnapshots keeps K3s's settings Secret in step with the cluster's, and says whether copies are arriving. It
// returns how soon to look again, or zero when there is nothing to look at.
func (r *GeassClusterReconciler) reconcileEtcdSnapshots(ctx context.Context, cluster *geassv1alpha1.GeassCluster) time.Duration {
	log := logf.FromContext(ctx)
	key := client.ObjectKeyFromObject(cluster)
	spec := cluster.Spec.EtcdSnapshots

	if spec == nil {
		// Not asked for. The Secret that this made is taken away, so that a K3s that still has the host's setting does not
		// keep sending copies to a bucket that the cluster no longer names; anyone else's Secret of that name is left alone.
		if err := r.removeEtcdSnapshotsSecret(ctx); err != nil {
			log.Error(err, "Could not remove the etcd snapshot settings")
			return etcdSnapshotsRetryFast
		}
		if err := r.setEtcdSnapshotsCondition(ctx, key, nil); err != nil {
			log.Error(err, "Could not clear the etcd snapshot condition")
			return etcdSnapshotsRetryFast
		}
		return 0
	}

	credentials := &corev1.Secret{}
	credentialsName := platform.EtcdSnapshotsCredentialsName(spec)
	err := r.Get(ctx, client.ObjectKey{Name: credentialsName, Namespace: platform.SystemNamespace}, credentials)
	var accessKey, secretKey string
	if err == nil {
		accessKey, secretKey = platform.EtcdSnapshotsCredentials(credentials)
	} else if !apierrors.IsNotFound(err) {
		log.Error(err, "Could not read the etcd snapshot credentials", "secret", credentialsName)
		return etcdSnapshotsRetryFast
	}
	if accessKey == "" || secretKey == "" {
		r.recordEtcdSnapshots(ctx, key, platform.EtcdSnapshotsAssessment{
			Status: metav1.ConditionFalse, Reason: platform.EtcdSnapshotsReasonNoCredentials,
			Message: fmt.Sprintf("The access key for the bucket is not in the Secret %s/%s, which needs the keys ACCESS_KEY_ID and ACCESS_SECRET_KEY",
				platform.SystemNamespace, credentialsName),
		})
		return etcdSnapshotsRetryFast
	}

	since, changed, err := r.ensureEtcdSnapshotsSecret(ctx, spec, accessKey, secretKey)
	if err != nil {
		log.Error(err, "Could not write the etcd snapshot settings")
		r.recordEtcdSnapshots(ctx, key, platform.EtcdSnapshotsAssessment{
			Status: metav1.ConditionFalse, Reason: platform.EtcdSnapshotsReasonSecretNotMade,
			Message: "The settings that tell K3s where to send copies could not be written: " + limitMessage(err.Error()),
		})
		return etcdSnapshotsRetryFast
	}

	// Looked at a moment ago, and nothing about where or how has changed since: what was seen stands, for as long as that look
	// said to wait. A look writes when it was made, which wakes the cluster's reconciler once more at once, so what this
	// returns while it waits has to be the whole of the wait and not a short one that would look again every few minutes.
	if last := cluster.Status.EtcdSnapshots; !changed && last != nil && last.CheckedAt != nil {
		if wait := etcdSnapshotsLookInterval(cluster) - r.snapshotClock().Sub(last.CheckedAt.Time); wait > 0 {
			return wait
		}
	}
	// A prefix with its slash: without it a folder called geass-eu is a part of the folder called geass.
	objects, err := r.etcdSnapshotLister(spec, accessKey, secretKey).ListObjects(spec.Bucket, platform.EtcdSnapshotsFolder(spec)+"/")
	if err != nil {
		r.recordEtcdSnapshots(ctx, key, platform.EtcdSnapshotsAssessment{
			Status: metav1.ConditionFalse, Reason: platform.EtcdSnapshotsReasonUnreadable,
			Message: "The bucket could not be read with this access key (it needs to list the bucket and read, write and delete objects in it): " + limitMessage(err.Error()),
		})
		return etcdSnapshotsRetryFast
	}
	copies := make([]platform.EtcdSnapshotCopy, 0, len(objects))
	for _, object := range objects {
		copies = append(copies, platform.EtcdSnapshotCopy{Name: lastPathElement(object.Key), WrittenAt: object.LastModified})
	}
	assessment := platform.AssessEtcdSnapshots(copies, r.snapshotClock(), since)
	r.recordEtcdSnapshots(ctx, key, assessment)
	switch assessment.Reason {
	case platform.EtcdSnapshotsReasonWaiting:
		return etcdSnapshotsRetryWaiting
	case platform.EtcdSnapshotsReasonNoCopy, platform.EtcdSnapshotsReasonStale:
		return etcdSnapshotsRetryWaiting
	}
	return platform.EtcdSnapshotsRecheck
}

// etcdSnapshotsLookInterval is how long after a look the next one is made when nothing has changed: as long as the look
// said to wait, and never less than the least time between two looks.
func etcdSnapshotsLookInterval(cluster *geassv1alpha1.GeassCluster) time.Duration {
	condition := meta.FindStatusCondition(cluster.Status.Conditions, platform.ConditionEtcdSnapshotsCurrent)
	if condition == nil {
		return etcdSnapshotsMinInterval
	}
	switch condition.Reason {
	case platform.EtcdSnapshotsReasonCurrent:
		return platform.EtcdSnapshotsRecheck
	case platform.EtcdSnapshotsReasonWaiting, platform.EtcdSnapshotsReasonNoCopy, platform.EtcdSnapshotsReasonStale:
		return etcdSnapshotsRetryWaiting
	}
	return etcdSnapshotsMinInterval
}

// ensureEtcdSnapshotsSecret writes the Secret that K3s reads, and returns when this destination was first set: what the
// first copy's allowance is counted from. The destination is the bucket, the server and the folder; a changed key is not a
// new destination.
func (r *GeassClusterReconciler) ensureEtcdSnapshotsSecret(ctx context.Context, spec *geassv1alpha1.GeassClusterEtcdSnapshotsSpec, accessKey, secretKey string) (since time.Time, changed bool, err error) {
	name := client.ObjectKey{Name: platform.EtcdSnapshotsSecretName, Namespace: platform.EtcdSnapshotsSecretNamespace}
	destination := etcdSnapshotsDestinationHash(spec)
	desired := platform.EtcdSnapshotsSecretData(spec, accessKey, secretKey)

	existing := &corev1.Secret{}
	getErr := r.Get(ctx, name, existing)
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return time.Time{}, false, getErr
	}
	found := getErr == nil
	if found && existing.Type != platform.EtcdSnapshotsSecretType {
		// A Secret's type cannot be changed, and K3s reads only this one. Whatever was here under this name was not made by
		// Geass for this, and is replaced.
		if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
			return time.Time{}, false, err
		}
		found = false
	}

	since = r.snapshotClock()
	if found && existing.Annotations[annotationEtcdSnapshotsConfig] == destination {
		if parsed, parseErr := time.Parse(time.RFC3339, existing.Annotations[annotationEtcdSnapshotsSince]); parseErr == nil {
			since = parsed
		}
		if secretDataEqual(existing.Data, desired) {
			return since, false, nil
		}
	}

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name.Name, Namespace: name.Namespace}}
	if found {
		secret = existing
	}
	secret.Type = platform.EtcdSnapshotsSecretType
	if secret.Labels == nil {
		secret.Labels = map[string]string{}
	}
	secret.Labels[platform.LabelManagedBy] = platform.ManagedByValue
	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	secret.Annotations[annotationEtcdSnapshotsConfig] = destination
	secret.Annotations[annotationEtcdSnapshotsSince] = since.UTC().Format(time.RFC3339)
	secret.Data = desired
	if found {
		return since, true, r.Update(ctx, secret)
	}
	return since, true, r.Create(ctx, secret)
}

// removeEtcdSnapshotsSecret deletes the Secret that K3s reads, when Geass made it.
func (r *GeassClusterReconciler) removeEtcdSnapshotsSecret(ctx context.Context) error {
	existing := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Name: platform.EtcdSnapshotsSecretName, Namespace: platform.EtcdSnapshotsSecretNamespace}, existing)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing.Labels[platform.LabelManagedBy] != platform.ManagedByValue {
		return nil
	}
	return client.IgnoreNotFound(r.Delete(ctx, existing))
}

// recordEtcdSnapshots writes what was found to the cluster's status. A failure to write is logged: the next look says it
// again.
func (r *GeassClusterReconciler) recordEtcdSnapshots(ctx context.Context, key client.ObjectKey, assessment platform.EtcdSnapshotsAssessment) {
	if err := r.setEtcdSnapshotsCondition(ctx, key, &assessment); err != nil {
		logf.FromContext(ctx).Error(err, "Could not record the etcd snapshot condition", "cluster", key.Name)
	}
}

// setEtcdSnapshotsCondition sets or, with a nil assessment, clears the condition and what the bucket held.
func (r *GeassClusterReconciler) setEtcdSnapshotsCondition(ctx context.Context, key client.ObjectKey, assessment *platform.EtcdSnapshotsAssessment) error {
	latest := &geassv1alpha1.GeassCluster{}
	if err := r.Get(ctx, key, latest); err != nil {
		return client.IgnoreNotFound(err)
	}
	now := metav1.NewTime(r.snapshotClock())
	_, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		if assessment == nil {
			meta.RemoveStatusCondition(&latest.Status.Conditions, platform.ConditionEtcdSnapshotsCurrent)
			latest.Status.EtcdSnapshots = nil
			return
		}
		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type: platform.ConditionEtcdSnapshotsCurrent, Status: assessment.Status, Reason: assessment.Reason, Message: assessment.Message,
		})
		status := &geassv1alpha1.GeassClusterEtcdSnapshotsStatus{CheckedAt: &now}
		// What was last seen in the bucket stays while a look fails: a bucket that cannot be read now still held the copy it
		// held.
		if previous := latest.Status.EtcdSnapshots; previous != nil && assessment.Newest == nil {
			status.LastCopyTime, status.LastCopyName, status.Copies = previous.LastCopyTime, previous.LastCopyName, previous.Copies
		}
		if assessment.Newest != nil {
			written := metav1.NewTime(assessment.Newest.WrittenAt)
			status.LastCopyTime, status.LastCopyName, status.Copies = &written, assessment.Newest.Name, int32(min(assessment.Copies, maxEtcdSnapshotCopiesCounted)) //nolint:gosec // clamped to what an int32 holds
		}
		latest.Status.EtcdSnapshots = status
	})
	return client.IgnoreNotFound(err)
}

// etcdSnapshotsDestinationHash is what makes one destination different from another, and leaves the key out.
func etcdSnapshotsDestinationHash(spec *geassv1alpha1.GeassClusterEtcdSnapshotsSpec) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		spec.Bucket, strings.TrimSpace(spec.Endpoint), spec.Region, platform.EtcdSnapshotsFolder(spec), fmt.Sprint(spec.Insecure),
	}, "|")))
	return hex.EncodeToString(sum[:8])
}

func secretDataEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || !bytes.Equal(value, other) {
			return false
		}
	}
	return true
}

func lastPathElement(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}

// limitMessage keeps an error from a server, which can be a page of XML, to what fits in a condition.
func limitMessage(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > etcdSnapshotsErrorLimit {
		return message[:etcdSnapshotsErrorLimit] + "..."
	}
	return message
}
