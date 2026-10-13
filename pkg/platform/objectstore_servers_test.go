package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestTheClustersObjectStorageCanRunOnSeaweedFSOrGarage(t *testing.T) {
	servers := ObjectStoreServers()
	engines := make([]geassv1alpha1.GeassObjectStoreEngine, 0, len(servers))
	for _, server := range servers {
		engines = append(engines, server.Engine)
		require.NotEmpty(t, server.Label)
		require.NotEmpty(t, server.Image)
		require.NotEmpty(t, server.CPU)
		require.NotEmpty(t, server.Memory)
		require.NotContains(t, server.Image, ":latest", "a server only changes version when Geass does")
	}
	require.Equal(t, []geassv1alpha1.GeassObjectStoreEngine{geassv1alpha1.ObjectStoreEngineSeaweedFS, geassv1alpha1.ObjectStoreEngineGarage}, engines)
}

func TestObjectStoreServerForKnowsOnlyTheServersGeassRuns(t *testing.T) {
	server, ok := ObjectStoreServerFor(geassv1alpha1.ObjectStoreEngineGarage)
	require.True(t, ok)
	require.Equal(t, int32(GarageS3Port), server.S3Port)
	for _, engine := range []geassv1alpha1.GeassObjectStoreEngine{geassv1alpha1.ObjectStoreEngineS3, ""} {
		_, ok := ObjectStoreServerFor(engine)
		require.False(t, ok, "%q", engine)
	}
}

func TestParseObjectStoreServerEngine(t *testing.T) {
	for input, want := range map[string]geassv1alpha1.GeassObjectStoreEngine{
		"SeaweedFS": geassv1alpha1.ObjectStoreEngineSeaweedFS,
		"seaweedfs": geassv1alpha1.ObjectStoreEngineSeaweedFS,
		" GARAGE ":  geassv1alpha1.ObjectStoreEngineGarage,
	} {
		got, err := ParseObjectStoreServerEngine(input)
		require.NoError(t, err, input)
		require.Equal(t, want, got)
	}
	for _, input := range []string{"", "ceph", "s3", "garage fs"} {
		_, err := ParseObjectStoreServerEngine(input)
		require.Error(t, err, input)
	}
}

func TestObjectStoreServerEndpoints(t *testing.T) {
	require.Equal(t, "http://geass-s3.geass-system.svc:8333", ObjectStoreServerEndpoint(geassv1alpha1.ObjectStoreEngineSeaweedFS))
	require.Equal(t, "http://geass-s3.geass-system.svc:3900", ObjectStoreServerEndpoint(geassv1alpha1.ObjectStoreEngineGarage))
	require.Equal(t, "http://geass-s3.geass-system.svc:3903", ObjectStoreServerAdminEndpoint())
	require.Empty(t, ObjectStoreServerEndpoint(geassv1alpha1.ObjectStoreEngineS3))
}

func TestGarageStartsSmallerThanSeaweedFS(t *testing.T) {
	seaweed := DefaultObjectStoreResources(geassv1alpha1.ObjectStoreEngineSeaweedFS)
	garage := DefaultObjectStoreResources(geassv1alpha1.ObjectStoreEngineGarage)
	require.Equal(t, "512Mi", seaweed.Requests.Memory().String())
	require.Equal(t, "256Mi", garage.Requests.Memory().String())
	require.Equal(t, seaweed.Requests, seaweed.Limits, "the size is reserved, as for every Geass workload")
	// Every default must be a size the dashboard offers, or its size picker could not show it.
	for _, resources := range []map[string]string{
		{"cpu": seaweed.Requests.Cpu().String(), "memory": seaweed.Requests.Memory().String()},
		{"cpu": garage.Requests.Cpu().String(), "memory": garage.Requests.Memory().String()},
	} {
		require.True(t, allowedSize(resources["cpu"], CPUSizes()), resources["cpu"])
		require.True(t, allowedSize(resources["memory"], MemorySizes()), resources["memory"])
	}
}

func TestIsClusterObjectStoreServer(t *testing.T) {
	server := func(project string, placement geassv1alpha1.GeassObjectStorePlacement, engine geassv1alpha1.GeassObjectStoreEngine) *geassv1alpha1.GeassObjectStore {
		return &geassv1alpha1.GeassObjectStore{Spec: geassv1alpha1.GeassObjectStoreSpec{Project: project, Placement: placement, Engine: engine}}
	}
	require.True(t, IsClusterObjectStoreServer(server("", geassv1alpha1.ObjectStorePlacementInCluster, geassv1alpha1.ObjectStoreEngineGarage)))
	require.False(t, IsClusterObjectStoreServer(server("payments", geassv1alpha1.ObjectStorePlacementInCluster, geassv1alpha1.ObjectStoreEngineGarage)), "a bucket has a project")
	require.False(t, IsClusterObjectStoreServer(server("", geassv1alpha1.ObjectStorePlacementExternal, geassv1alpha1.ObjectStoreEngineS3)))
	require.False(t, IsClusterObjectStoreServer(nil))
}

func TestTheServersVolumeClassFollowsItsPlacement(t *testing.T) {
	require.Equal(t, StorageClassReplicated, ObjectStoreServerStorageClass(Placement{}))
	require.Equal(t, RegionalReplicatedClass("eu"), ObjectStoreServerStorageClass(NewPlacement("eu", nil)))
	require.Equal(t, TaggedReplicatedClass("eu", []string{"hipaa"}), ObjectStoreServerStorageClass(NewPlacement("eu", []string{"hipaa"})), "a server on tagged nodes keeps its copies on them")
	require.Equal(t, TaggedReplicatedClass("", []string{"a", "b"}), ObjectStoreServerStorageClass(NewPlacement("", []string{"b", "a"})), "tag order does not matter")
	require.Equal(t, TaggedReplicatedClass("eu", []string{"hipaa", "pci"}), ObjectStoreServerStorageClass(NewPlacement("eu", []string{"pci"}).WithRequired([]string{"hipaa"})), "tags it asked for and tags a project requires both count")
}

func TestTheServersVolumeSize(t *testing.T) {
	sizeOf := func(server *geassv1alpha1.GeassObjectStore) string {
		size := ObjectStoreServerStorage(server)
		return size.String()
	}
	server := &geassv1alpha1.GeassObjectStore{}
	require.Equal(t, "10Gi", sizeOf(server))
	size := resource.MustParse("40Gi")
	server.Spec.StorageSize = &size
	require.Equal(t, "40Gi", sizeOf(server))
	zero := resource.MustParse("0")
	server.Spec.StorageSize = &zero
	require.Equal(t, "10Gi", sizeOf(server), "a zero size is no size")
	require.Equal(t, "10Gi", sizeOf(nil))
	tiny := resource.MustParse("10Mi")
	server.Spec.StorageSize = &tiny
	require.Equal(t, MinObjectStoreServerVolumeSize, sizeOf(server), "a volume too small for the server to start on is never made")
}

// The server's network policy: S3 is open to every namespace, because that is how apps reach their buckets, Garage's
// admin API is open to the platform alone, and nothing else is open.
func TestTheServersNetworkPolicy(t *testing.T) {
	seaweed := ObjectStoreServerPolicy(geassv1alpha1.ObjectStoreEngineSeaweedFS)
	require.Equal(t, SystemNamespace, seaweed.Namespace)
	require.Equal(t, map[string]string{K8sLabelAppName: ObjectStoreServerName}, seaweed.Spec.PodSelector.MatchLabels)
	require.Equal(t, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}, seaweed.Spec.PolicyTypes)
	require.Len(t, seaweed.Spec.Ingress, 1)
	require.Empty(t, seaweed.Spec.Ingress[0].From, "any namespace may reach S3")
	require.Equal(t, int32(8333), seaweed.Spec.Ingress[0].Ports[0].Port.IntVal)

	garage := ObjectStoreServerPolicy(geassv1alpha1.ObjectStoreEngineGarage)
	require.Len(t, garage.Spec.Ingress, 2)
	require.Empty(t, garage.Spec.Ingress[0].From)
	require.Equal(t, int32(3900), garage.Spec.Ingress[0].Ports[0].Port.IntVal)
	admin := garage.Spec.Ingress[1]
	require.Equal(t, int32(3903), admin.Ports[0].Port.IntVal)
	require.Len(t, admin.From, 1, "the admin port is for the platform alone")
	require.Equal(t, SystemNamespace, admin.From[0].NamespaceSelector.MatchLabels[LabelNamespaceName])

	require.Nil(t, ObjectStoreServerPolicy(geassv1alpha1.ObjectStoreEngineS3), "AWS buckets have no server")
}
