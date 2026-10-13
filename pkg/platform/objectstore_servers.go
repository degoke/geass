package platform

import (
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// The cluster's object storage is one server, run by Geass in the system namespace: a Deployment with a
// single replica, a volume, and a Service, all named ObjectStoreServerName. Which server it is, SeaweedFS
// or Garage, is chosen once when it is set up, and every project's in-cluster buckets live on it.
const (
	// ClusterObjectStoreName is the resource name the cluster's object storage server gets by default.
	ClusterObjectStoreName = "cluster-s3"
	// ObjectStoreServerName names the server's Deployment, Service, volume claim and config, and is the
	// name label its pods carry.
	ObjectStoreServerName = "geass-s3"
	// ObjectStoreServerVolumeSize is the size of the server's volume unless one was chosen.
	ObjectStoreServerVolumeSize = "10Gi"
	// MinObjectStoreServerVolumeSize is the smallest volume a server can be given.
	MinObjectStoreServerVolumeSize = "1Gi"

	// SeaweedFSImage and GarageImage are the images the two servers run. They are pinned, so a server only
	// changes version when Geass does.
	SeaweedFSImage = "chrislusf/seaweedfs:4.48"
	GarageImage    = "dxflrs/garage:v2.4.1"
	// S3ForwarderImage publishes SeaweedFS's S3 port and nothing else. SeaweedFS serves internal gRPC services on
	// the same address as S3, with no login, that let a caller add an admin identity; so SeaweedFS listens only on
	// localhost and this forwards the one port that is safe to publish.
	S3ForwarderImage = "alpine/socat:1.8.1.1"

	// SeaweedFSS3Port, GarageS3Port and GarageAdminPort are where the servers listen. Only the S3 port (and
	// Garage's admin port, which Geass drives) is published by the Service.
	SeaweedFSS3Port = 8333
	GarageS3Port    = 3900
	GarageAdminPort = 3903
)

// ObjectStoreServer describes one of the servers the cluster's object storage can run on.
type ObjectStoreServer struct {
	Engine geassv1alpha1.GeassObjectStoreEngine
	// Label is how the server is named to people.
	Label string
	Image string
	// S3Port is the port the S3 API is served on.
	S3Port int32
	// CPU and Memory are the default size of the server.
	CPU, Memory string
}

var objectStoreServers = []ObjectStoreServer{
	{Engine: geassv1alpha1.ObjectStoreEngineSeaweedFS, Label: "SeaweedFS", Image: SeaweedFSImage, S3Port: SeaweedFSS3Port, CPU: "250m", Memory: "512Mi"},
	{Engine: geassv1alpha1.ObjectStoreEngineGarage, Label: "Garage", Image: GarageImage, S3Port: GarageS3Port, CPU: "100m", Memory: "256Mi"},
}

// ObjectStoreServers lists the servers a cluster's object storage can be set up with.
func ObjectStoreServers() []ObjectStoreServer {
	return append([]ObjectStoreServer(nil), objectStoreServers...)
}

// ObjectStoreServerFor returns the server for an engine, and false for an engine that Geass does not run
// in the cluster (AWS S3).
func ObjectStoreServerFor(engine geassv1alpha1.GeassObjectStoreEngine) (ObjectStoreServer, bool) {
	for _, server := range objectStoreServers {
		if server.Engine == engine {
			return server, true
		}
	}
	return ObjectStoreServer{}, false
}

// ParseObjectStoreServerEngine reads an engine chosen for the cluster's object storage, whatever its case.
func ParseObjectStoreServerEngine(value string) (geassv1alpha1.GeassObjectStoreEngine, error) {
	value = strings.TrimSpace(value)
	for _, server := range objectStoreServers {
		if strings.EqualFold(string(server.Engine), value) {
			return server.Engine, nil
		}
	}
	return "", fmt.Errorf("choose SeaweedFS or Garage for cluster object storage")
}

// ObjectStoreServerEndpoint is the address of a server's S3 API inside the cluster.
func ObjectStoreServerEndpoint(engine geassv1alpha1.GeassObjectStoreEngine) string {
	server, ok := ObjectStoreServerFor(engine)
	if !ok {
		return ""
	}
	return fmt.Sprintf("http://%s.%s.svc:%d", ObjectStoreServerName, SystemNamespace, server.S3Port)
}

// ObjectStoreServerAdminEndpoint is the address of Garage's admin API, which Geass uses to make buckets and
// keys. SeaweedFS has no separate one: its management API is served on the S3 port.
func ObjectStoreServerAdminEndpoint() string {
	return fmt.Sprintf("http://%s.%s.svc:%d", ObjectStoreServerName, SystemNamespace, GarageAdminPort)
}

// DefaultObjectStoreResources is the size an object storage server gets when nobody chose one.
func DefaultObjectStoreResources(engine geassv1alpha1.GeassObjectStoreEngine) corev1.ResourceRequirements {
	cpu, memory := "250m", "512Mi"
	if server, ok := ObjectStoreServerFor(engine); ok {
		cpu, memory = server.CPU, server.Memory
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory)},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory)},
	}
}

// IsClusterObjectStoreServer reports whether a store is the cluster's object storage server rather than a
// bucket: it belongs to no project and lives in the cluster.
func IsClusterObjectStoreServer(store *geassv1alpha1.GeassObjectStore) bool {
	if store == nil || store.Spec.Project != "" {
		return false
	}
	return store.Spec.Placement != geassv1alpha1.ObjectStorePlacementExternal && store.Spec.Engine != geassv1alpha1.ObjectStoreEngineS3
}

// ObjectStoreServerStorageClass is the storage class a new object storage server's volume asks for when Longhorn is
// installed: the replicated one, because nothing else keeps the server's data on more than one node. It is kept to
// the region and to the nodes carrying every tag the server was placed with, so that the copies of its volume stay
// where its pod may run: a server on compliance nodes must not have its data replicated onto others.
func ObjectStoreServerStorageClass(placement Placement) string {
	if tags := ObjectStoreServerStorageTags(placement); len(tags) > 0 {
		return TaggedReplicatedClass(placement.Region, tags)
	}
	if placement.Region != "" {
		return RegionalReplicatedClass(placement.Region)
	}
	return StorageClassReplicated
}

// ObjectStoreServerStorageTags are the node tags a server's volume must stay on: all it was placed with.
func ObjectStoreServerStorageTags(placement Placement) []string {
	return NewPlacement("", append(slices.Clone(placement.Tags), placement.Required...)).Tags
}

// ObjectStoreServerStorage is the size of a server's volume: the one it asks for, else the default, and never less
// than MinObjectStoreServerVolumeSize. The dashboard refuses a smaller size, but the resource can be written without
// it, and a volume too small for the server to start on would never come up.
func ObjectStoreServerStorage(server *geassv1alpha1.GeassObjectStore) resource.Quantity {
	size := resource.MustParse(ObjectStoreServerVolumeSize)
	if server != nil && server.Spec.StorageSize != nil && !server.Spec.StorageSize.IsZero() {
		size = *server.Spec.StorageSize
	}
	if floor := resource.MustParse(MinObjectStoreServerVolumeSize); size.Cmp(floor) < 0 {
		return floor
	}
	return size
}
