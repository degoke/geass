package platform

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestObjectStoreInfrastructureActive(t *testing.T) {
	draft := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{DeployWorkflowAnnotation: "true"},
		},
		Spec: geassv1alpha1.GeassObjectStoreSpec{Deploy: geassv1alpha1.GeassDeploySpec{Enabled: false}},
	}
	require.False(t, ObjectStoreInfrastructureActive(draft))

	legacyDraft := &geassv1alpha1.GeassObjectStore{
		Spec: geassv1alpha1.GeassObjectStoreSpec{Deploy: geassv1alpha1.GeassDeploySpec{Enabled: false}},
	}
	require.False(t, ObjectStoreInfrastructureActive(legacyDraft))

	deployNeverReady := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{DeployWorkflowAnnotation: "true"},
		},
		Spec: geassv1alpha1.GeassObjectStoreSpec{Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true}},
	}
	require.False(t, ObjectStoreInfrastructureActive(deployNeverReady))

	legacyLive := &geassv1alpha1.GeassObjectStore{
		Spec:   geassv1alpha1.GeassObjectStoreSpec{Deploy: geassv1alpha1.GeassDeploySpec{Enabled: false}},
		Status: geassv1alpha1.GeassObjectStoreStatus{ConnectionSecret: "floral-pebble-connection"},
	}
	require.True(t, ObjectStoreInfrastructureActive(legacyLive))
}

// A store that created something but never became ready still owns it, so deleting the store must clean up.
func TestObjectStoreInfrastructureActiveAfterProvisioningStarted(t *testing.T) {
	started := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{DeployWorkflowAnnotation: "true", AnnotationProvisioningStarted: "true"},
		},
		Spec: geassv1alpha1.GeassObjectStoreSpec{Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true}},
	}
	require.True(t, ObjectStoreInfrastructureActive(started), "no connection secret yet, but a bucket may exist")

	legacy := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationProvisioningStarted: "true"}},
	}
	require.True(t, ObjectStoreInfrastructureActive(legacy))

	draft := started.DeepCopy()
	draft.Spec.Deploy.Enabled = false
	require.False(t, ObjectStoreInfrastructureActive(draft), "a draft owns nothing")

	notStarted := started.DeepCopy()
	delete(notStarted.Annotations, AnnotationProvisioningStarted)
	require.False(t, ObjectStoreInfrastructureActive(notStarted))
}

func TestInClusterBucketNameKeepsProjectsAndEnvironmentsApart(t *testing.T) {
	payments, err := InClusterBucketName("payments", "dev", "uploads")
	require.NoError(t, err)
	require.Equal(t, "payments.dev.uploads", payments)

	billing, err := InClusterBucketName("billing", "dev", "uploads")
	require.NoError(t, err)
	require.NotEqual(t, payments, billing, "two projects with the same bucket name get different buckets")

	production, err := InClusterBucketName("payments", "production", "uploads")
	require.NoError(t, err)
	require.NotEqual(t, payments, production, "two environments of one project get different buckets")

	require.NoError(t, ValidBucketName(payments), "the stored name is itself a legal bucket name")
}

// Project and environment names may contain hyphens, so a hyphen between the parts would let different
// combinations produce one name and share a bucket. The dot cannot appear in either.
func TestInClusterBucketNameIsUnambiguous(t *testing.T) {
	first, err := InClusterBucketName("a-b", "c", "dat")
	require.NoError(t, err)
	second, err := InClusterBucketName("a", "b-c", "dat")
	require.NoError(t, err)
	require.NotEqual(t, first, second)

	third, err := InClusterBucketName("payments", "dev", "uploads-xyz")
	require.NoError(t, err)
	fourth, err := InClusterBucketName("payments-dev", "uploads", "xyz")
	require.NoError(t, err)
	require.NotEqual(t, third, fourth)

	// Parts that would read as an IP address once joined are refused.
	_, err = InClusterBucketName("1", "2", "3.4")
	require.Error(t, err)
}

func TestObjectStoreOnSharedMinIO(t *testing.T) {
	store := func(project string, placement geassv1alpha1.GeassObjectStorePlacement, engine geassv1alpha1.GeassObjectStoreEngine) *geassv1alpha1.GeassObjectStore {
		return &geassv1alpha1.GeassObjectStore{Spec: geassv1alpha1.GeassObjectStoreSpec{Project: project, Placement: placement, Engine: engine}}
	}
	require.True(t, ObjectStoreOnSharedMinIO(store("payments", geassv1alpha1.ObjectStorePlacementInCluster, geassv1alpha1.ObjectStoreEngineMinIO)))
	require.True(t, ObjectStoreOnSharedMinIO(store("payments", "", "")), "placement and engine default to in-cluster MinIO")
	require.False(t, ObjectStoreOnSharedMinIO(store("", geassv1alpha1.ObjectStorePlacementInCluster, geassv1alpha1.ObjectStoreEngineMinIO)), "the cluster server has no project")
	require.False(t, ObjectStoreOnSharedMinIO(store("payments", geassv1alpha1.ObjectStorePlacementExternal, geassv1alpha1.ObjectStoreEngineS3)))
	require.False(t, ObjectStoreOnSharedMinIO(store("payments", "", geassv1alpha1.ObjectStoreEngineS3)))
}

func TestInClusterBucketNameRefusesWhatDoesNotFit(t *testing.T) {
	_, err := InClusterBucketName("payments", "dev", "Uploads")
	require.Error(t, err, "the bucket name itself must be valid")

	_, err = InClusterBucketName("", "dev", "uploads")
	require.Error(t, err, "the project is required")

	// The prefix proj-1a2b3c4d.production. is 25 characters, which leaves 38 for the bucket name.
	fits := strings.Repeat("a", 38)
	name, err := InClusterBucketName("proj-1a2b3c4d", "production", fits)
	require.NoError(t, err)
	require.Len(t, name, 63)

	_, err = InClusterBucketName("proj-1a2b3c4d", "production", fits+"a")
	require.Error(t, err)
	require.Contains(t, err.Error(), "at most 38 characters")

	long := strings.Repeat("p", 30)
	_, err = InClusterBucketName(long, strings.Repeat("e", 30), "uploads")
	require.Error(t, err)
	require.Contains(t, err.Error(), "too long to hold a bucket")
}
