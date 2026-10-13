package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func serverClaim(t *testing.T, c client.Client) corev1.PersistentVolumeClaim {
	t.Helper()
	var claim corev1.PersistentVolumeClaim
	require.NoError(t, c.Get(context.Background(), objectStoreServerKey(), &claim))
	return claim
}

// claimSize is the size the server's volume asks for.
func claimSize(t *testing.T, c client.Client) string {
	t.Helper()
	claim := serverClaim(t, c)
	size := claim.Spec.Resources.Requests[corev1.ResourceStorage]
	return size.String()
}

func serverResource(t *testing.T, c client.Client) geassv1alpha1.GeassObjectStore {
	t.Helper()
	var server geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace}, &server))
	return server
}

func serverMessage(t *testing.T, c client.Client) string {
	t.Helper()
	server := serverResource(t, c)
	return readyConditionMessage(server.Status.Conditions)
}

func expandableClass(name string, expand bool) *storagev1.StorageClass {
	return &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: name}, Provisioner: platform.LonghornProvisioner, AllowVolumeExpansion: &expand}
}

func serverWithSpec(spec geassv1alpha1.GeassObjectStoreSpec) *geassv1alpha1.GeassObjectStore {
	spec.Placement = geassv1alpha1.ObjectStorePlacementInCluster
	return &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace},
		Spec:       spec,
	}
}

func TestTheServersVolumeIsTenGigabytesUnlessAskedOtherwise(t *testing.T) {
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineSeaweedFS)

	reconcileServer(t, r, server, 4)

	require.Equal(t, "10Gi", serverResource(t, c).Spec.StorageSize.String(), "the size is written to the resource, so what runs is on it")
	require.Equal(t, "10Gi", claimSize(t, c))
}

func TestTheServerGetsTheVolumeSizeItWasAskedFor(t *testing.T) {
	size := resource.MustParse("25Gi")
	scheme := objectStoreDeployScheme(t)
	r, server, _, c := clusterServerReconcilerFor(t, scheme, serverWithSpec(geassv1alpha1.GeassObjectStoreSpec{Engine: geassv1alpha1.ObjectStoreEngineGarage, StorageSize: &size}))

	reconcileServer(t, r, server, 4)

	require.Equal(t, "25Gi", claimSize(t, c))
}

// A server made before its volume could be sized has a volume, and its size must be taken from that volume. Writing the
// default would ask a 7Gi volume to become 10Gi, or a larger one to shrink.
func TestAServerFromBeforeTheSizeCouldBeChosenKeepsTheSizeOfItsVolume(t *testing.T) {
	existing := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerName, Namespace: platform.SystemNamespace},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("7Gi")}},
		},
	}
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineSeaweedFS, existing)

	reconcileServer(t, r, server, 4)

	require.Equal(t, "7Gi", serverResource(t, c).Spec.StorageSize.String())
	require.Equal(t, "7Gi", claimSize(t, c))
}

// Without Longhorn a volume uses the cluster's default storage class, and that is remembered.
func TestWithoutLonghornTheVolumeUsesTheDefaultClass(t *testing.T) {
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineSeaweedFS)

	reconcileServer(t, r, server, 4)

	require.Nil(t, serverClaim(t, c).Spec.StorageClassName)
	stored := serverResource(t, c)
	class, pinned := stored.Annotations[platform.AnnotationStorageClass]
	require.True(t, pinned, "the choice is written down, empty meaning the default class")
	require.Empty(t, class)
}

// With Longhorn the volume is on the replicated class, because nothing else keeps the server's data on more than one node.
func TestWithLonghornTheVolumeIsReplicated(t *testing.T) {
	for _, engine := range objectStoreEngines {
		t.Run(string(engine), func(t *testing.T) {
			objects := append([]client.Object{longhornCluster(true)}, longhornClasses()...)
			r, server, _, c := clusterServerReconciler(t, engine, objects...)

			reconcileServer(t, r, server, 4)

			claim := serverClaim(t, c)
			require.NotNil(t, claim.Spec.StorageClassName)
			require.Equal(t, platform.StorageClassReplicated, *claim.Spec.StorageClassName)
		})
	}
}

// A server kept to a region, or to nodes with tags, keeps its volume's copies there too: a server on compliance
// nodes must not have its data replicated onto others.
func TestTheVolumeFollowsTheServersRegionAndTags(t *testing.T) {
	for name, tc := range map[string]struct {
		spec geassv1alpha1.GeassObjectStoreSpec
		want string
	}{
		"region": {geassv1alpha1.GeassObjectStoreSpec{Engine: geassv1alpha1.ObjectStoreEngineGarage, ServerRegion: "eu"}, platform.RegionalReplicatedClass("eu")},
		"tags": {
			geassv1alpha1.GeassObjectStoreSpec{Engine: geassv1alpha1.ObjectStoreEngineGarage, ServerRegion: "eu", RunOn: geassv1alpha1.GeassWorkloadPlacement{Tags: []string{"hipaa"}}},
			platform.TaggedReplicatedClass("eu", []string{"hipaa"}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			scheme := objectStoreDeployScheme(t)
			objects := append([]client.Object{longhornCluster(true), expandableClass(tc.want, true)}, longhornClasses()...)
			r, server, _, c := clusterServerReconcilerFor(t, scheme, serverWithSpec(tc.spec), objects...)

			reconcileServer(t, r, server, 4)

			claim := serverClaim(t, c)
			require.NotNil(t, claim.Spec.StorageClassName)
			require.Equal(t, tc.want, *claim.Spec.StorageClassName)
		})
	}
}

// The region's class is made a moment after the global one. Settling for local storage in that gap would pin the volume
// to it for good, so the server waits.
func TestTheServerWaitsForARegionsClassWhenTheGlobalOneIsThere(t *testing.T) {
	scheme := objectStoreDeployScheme(t)
	objects := append([]client.Object{longhornCluster(true)}, longhornClasses()...)
	r, server, _, c := clusterServerReconcilerFor(t, scheme, serverWithSpec(geassv1alpha1.GeassObjectStoreSpec{Engine: geassv1alpha1.ObjectStoreEngineSeaweedFS, ServerRegion: "eu"}), objects...)

	reconcileServer(t, r, server, 4)

	require.Equal(t, messageStorageStarting, serverMessage(t, c))
	var claims corev1.PersistentVolumeClaimList
	require.NoError(t, c.List(context.Background(), &claims))
	require.Empty(t, claims.Items, "no volume is made while the answer is not known")
	require.False(t, platform.ReconcileFailureShouldCount(messageStorageStarting), "waiting for Longhorn is not a failure")
}

func TestTheServerWaitsForLonghornThatIsBeingInstalled(t *testing.T) {
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineGarage, longhornClusterStuckFor(time.Minute, metav1.ConditionFalse))

	reconcileServer(t, r, server, 4)

	require.Equal(t, messageStorageStarting, serverMessage(t, c))
	var claims corev1.PersistentVolumeClaimList
	require.NoError(t, c.List(context.Background(), &claims))
	require.Empty(t, claims.Items)
}

// A Longhorn that never comes up is a failure the admin can see, not an endless "starting".
func TestTheServerSaysSoWhenLonghornNeverComesUp(t *testing.T) {
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineGarage, longhornClusterStuckFor(storageWaitTimeout+time.Minute, metav1.ConditionFalse))

	reconcileServer(t, r, server, 4)

	require.Equal(t, messageStorageUnavailable, serverMessage(t, c))
}

// A volume's class cannot change, so the class chosen for it is kept even when Longhorn arrives later.
func TestTheVolumeKeepsItsClassWhenLonghornArrivesLater(t *testing.T) {
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineSeaweedFS)
	reconcileServer(t, r, server, 4)
	require.Nil(t, serverClaim(t, c).Spec.StorageClassName)

	require.NoError(t, c.Create(context.Background(), longhornCluster(true)))
	for _, class := range longhornClasses() {
		require.NoError(t, c.Create(context.Background(), class))
	}
	reconcileServer(t, r, server, 2)

	require.Nil(t, serverClaim(t, c).Spec.StorageClassName)
	stored := serverResource(t, c)
	require.Empty(t, stored.Annotations[platform.AnnotationStorageClass])
}

// A server that already has a volume, from before classes were chosen, keeps the class that volume has.
func TestAnExistingVolumeKeepsItsClass(t *testing.T) {
	class := "local-path"
	existing := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerName, Namespace: platform.SystemNamespace},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &class,
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")}},
		},
	}
	objects := append([]client.Object{existing, longhornCluster(true)}, longhornClasses()...)
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineSeaweedFS, objects...)

	reconcileServer(t, r, server, 4)

	require.Equal(t, "local-path", *serverClaim(t, c).Spec.StorageClassName)
	stored := serverResource(t, c)
	require.Equal(t, "local-path", stored.Annotations[platform.AnnotationStorageClass])
}

func TestTheVolumeGrowsWhereItsClassCanBeResized(t *testing.T) {
	objects := append([]client.Object{longhornCluster(true), expandableClass(platform.StorageClassReplicated, true)}, longhornClasses()[1:]...)
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineSeaweedFS, objects...)
	reconcileServer(t, r, server, 4)
	require.Equal(t, "10Gi", claimSize(t, c))

	stored := serverResource(t, c)
	bigger := resource.MustParse("40Gi")
	stored.Spec.StorageSize = &bigger
	require.NoError(t, c.Update(context.Background(), &stored))
	reconcileServer(t, r, server, 1)

	require.Equal(t, "40Gi", claimSize(t, c))
}

// Asking a class that cannot be resized to grow would be refused by the API server on every pass. The volume is left alone.
func TestTheVolumeIsNotGrownWhereItsClassCannotBeResized(t *testing.T) {
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineSeaweedFS, &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{Name: "standard", Annotations: map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}},
	})
	reconcileServer(t, r, server, 4)

	stored := serverResource(t, c)
	bigger := resource.MustParse("40Gi")
	stored.Spec.StorageSize = &bigger
	require.NoError(t, c.Update(context.Background(), &stored))
	reconcileServer(t, r, server, 2)

	require.Equal(t, "10Gi", claimSize(t, c))
	require.Equal(t, platform.MessageObjectStorageStarting, serverMessage(t, c), "the server carries on, waiting only for its pod")
	require.Equal(t, "10Gi", serverResource(t, c).Spec.StorageSize.String(), "the resource says the size the volume has, not one it never will")
}

func TestTheVolumeIsNeverShrunk(t *testing.T) {
	objects := append([]client.Object{longhornCluster(true), expandableClass(platform.StorageClassReplicated, true)}, longhornClasses()[1:]...)
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineSeaweedFS, objects...)
	reconcileServer(t, r, server, 4)

	stored := serverResource(t, c)
	smaller := resource.MustParse("2Gi")
	stored.Spec.StorageSize = &smaller
	require.NoError(t, c.Update(context.Background(), &stored))
	reconcileServer(t, r, server, 2)

	require.Equal(t, "10Gi", claimSize(t, c))
	require.Equal(t, "10Gi", serverResource(t, c).Spec.StorageSize.String(), "a smaller size is put back too, so the dashboard's floor is the real size")
}

// A size below the smallest a server can start on, written without the dashboard, still gets a volume it can use.
func TestTheVolumeIsNeverSmallerThanAServerNeeds(t *testing.T) {
	tiny := resource.MustParse("10Mi")
	scheme := objectStoreDeployScheme(t)
	r, server, _, c := clusterServerReconcilerFor(t, scheme, serverWithSpec(geassv1alpha1.GeassObjectStoreSpec{Engine: geassv1alpha1.ObjectStoreEngineSeaweedFS, StorageSize: &tiny}))

	reconcileServer(t, r, server, 4)

	require.Equal(t, platform.MinObjectStoreServerVolumeSize, claimSize(t, c))
}

// Only the S3 port, and for Garage the admin port from the platform, are open to the network.
func TestTheServersNetworkPolicyOpensOnlyTheRightPorts(t *testing.T) {
	for _, engine := range objectStoreEngines {
		t.Run(string(engine), func(t *testing.T) {
			r, server, _, c := clusterServerReconciler(t, engine)

			reconcileServer(t, r, server, 4)

			var policy networkingv1.NetworkPolicy
			require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: platform.ObjectStoreServerPolicyName, Namespace: platform.SystemNamespace}, &policy))
			require.Equal(t, platform.ObjectStoreServerPolicy(engine).Spec, policy.Spec)
			require.NotEmpty(t, policy.OwnerReferences, "the policy goes with the server")
		})
	}
}

// A policy that is deleted, or changed by hand, is put back.
func TestTheServersNetworkPolicyIsPutBackWhenItIsRemoved(t *testing.T) {
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineGarage)
	reconcileServer(t, r, server, 4)
	policy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerPolicyName, Namespace: platform.SystemNamespace}}
	require.NoError(t, c.Delete(context.Background(), policy))

	reconcileServer(t, r, server, 1)

	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(policy), policy))
	policy.Spec.Ingress = nil
	require.NoError(t, c.Update(context.Background(), policy))
	reconcileServer(t, r, server, 1)
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(policy), policy))
	require.Len(t, policy.Spec.Ingress, 2)
}
