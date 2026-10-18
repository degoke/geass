// Package v1beta2 defines the parts of MOCO's MySQLCluster API that Geass uses. MOCO is the operator that runs
// the high availability MySQL databases; like the CloudNativePG types next to it, these are written here rather
// than imported, so the operator does not take a dependency on another operator's module, and only name what
// Geass sets or reads. Fields MOCO has that are not here are left to MOCO's defaults.
// +kubebuilder:object:generate=false
package v1beta2

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	GroupName = "moco.cybozu.com"
	Version   = "v1beta2"

	// MySQLContainerName is the container MOCO requires in a cluster's pod template: it is the server.
	MySQLContainerName = "mysqld"

	// ConditionHealthy is true while every instance is up and replicating, and ConditionAvailable while the cluster
	// can serve: a cluster that lost a member is available and not healthy.
	ConditionHealthy   = "Healthy"
	ConditionAvailable = "Available"

	// RoleLabel is on each pod and says whether it is the primary or a replica.
	RoleLabel   = "moco.cybozu.com/role"
	RolePrimary = "primary"
)

var (
	SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: Version}
	SchemeBuilder      = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme        = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion, &MySQLCluster{}, &MySQLClusterList{})
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

// MySQLCluster is a MOCO cluster of MySQL servers: one primary and replicas.
type MySQLCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MySQLClusterSpec   `json:"spec,omitempty"`
	Status            MySQLClusterStatus `json:"status,omitempty"`
}

// MySQLClusterList contains a list of MySQLCluster resources.
type MySQLClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MySQLCluster `json:"items"`
}

// MySQLClusterSpec is what Geass says about a cluster.
type MySQLClusterSpec struct {
	// Replicas is the number of MySQL servers. MOCO runs an odd number, from one to five.
	Replicas int32 `json:"replicas,omitempty"`
	// PodTemplate is the pod of each server. It has to hold a container named mysqld, which is the server.
	PodTemplate PodTemplateSpec `json:"podTemplate"`
	// VolumeClaimTemplates holds the volume of each server. MOCO wants a claim named mysql-data.
	VolumeClaimTemplates []corev1.PersistentVolumeClaim `json:"volumeClaimTemplates"`
	// MySQLConfigMapName names a ConfigMap of my.cnf settings that replace MOCO's defaults.
	MySQLConfigMapName *string `json:"mysqlConfigMapName,omitempty"`
	// DisableSlowQueryLogContainer leaves out the sidecar that prints the slow query log to the pod's output.
	DisableSlowQueryLogContainer bool `json:"disableSlowQueryLogContainer,omitempty"`
}

// PodTemplateSpec is the pod of a cluster's servers.
type PodTemplateSpec struct {
	// ObjectMeta holds labels and annotations for the pods.
	ObjectMeta `json:"metadata,omitempty"`
	Spec       corev1.PodSpec `json:"spec"`
}

// ObjectMeta is the part of an object's metadata a pod template may set.
type ObjectMeta struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// MySQLClusterStatus is what MOCO reports.
type MySQLClusterStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// CurrentPrimaryIndex is the index of the pod that is the primary now.
	CurrentPrimaryIndex *int `json:"currentPrimaryIndex,omitempty"`
	// SyncedReplicas is how many replicas are in step with the primary.
	SyncedReplicas int `json:"syncedReplicas,omitempty"`
	// ErrantReplicas is how many replicas hold changes the primary does not: they need to be built again.
	ErrantReplicas    int   `json:"errantReplicas,omitempty"`
	ErrantReplicaList []int `json:"errantReplicaList,omitempty"`
}

// ConditionTrue reports whether the cluster has the named condition, and it is true.
func (c *MySQLCluster) ConditionTrue(name string) bool {
	for i := range c.Status.Conditions {
		if c.Status.Conditions[i].Type == name {
			return c.Status.Conditions[i].Status == metav1.ConditionTrue
		}
	}
	return false
}
