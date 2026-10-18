// Package v1alpha1 defines the parts of mariadb-operator's API that Geass uses. mariadb-operator runs the MariaDB
// databases; like the CloudNativePG and MOCO types next to it, these are written here rather than imported, so
// the operator takes no dependency on another operator's module, and only name what Geass sets or reads. Fields
// the operator has that are not here are left to its defaults.
// +kubebuilder:object:generate=false
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	GroupName = "k8s.mariadb.com"
	Version   = "v1alpha1"

	// ConditionReady is true while the database serves and every instance is up.
	ConditionReady = "Ready"

	// RoleLabel is on each pod and says whether it is the primary or a replica.
	RoleLabel   = "k8s.mariadb.com/role"
	RolePrimary = "primary"

	// ContainerName is the container of the server in each pod. The pod has a second one, the operator's agent.
	ContainerName = "mariadb"
)

var (
	SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: Version}
	SchemeBuilder      = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme        = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion, &MariaDB{}, &MariaDBList{}, &PhysicalBackup{}, &PhysicalBackupList{},
		&PointInTimeRecovery{}, &PointInTimeRecoveryList{})
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

// MariaDB is a MariaDB server, or a primary with replicas.
type MariaDB struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MariaDBSpec   `json:"spec,omitempty"`
	Status            MariaDBStatus `json:"status,omitempty"`
}

// MariaDBList contains a list of MariaDB resources.
type MariaDBList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MariaDB `json:"items"`
}

// MariaDBSpec is what Geass says about a database.
type MariaDBSpec struct {
	// Image is the official MariaDB image. The operator works only with official images.
	Image string `json:"image,omitempty"`
	// Replicas is the number of servers: one is a standalone server, and with replication three or more.
	Replicas int32 `json:"replicas,omitempty"`
	// RootPasswordSecretKeyRef is where the root login's password is read from.
	RootPasswordSecretKeyRef SecretKeyRef `json:"rootPasswordSecretKeyRef"`
	// Database, Username and PasswordSecretKeyRef make the application's database and login when the server starts.
	Database             *string       `json:"database,omitempty"`
	Username             *string       `json:"username,omitempty"`
	PasswordSecretKeyRef *SecretKeyRef `json:"passwordSecretKeyRef,omitempty"`
	// Storage is the volume of each server.
	Storage Storage `json:"storage"`
	// Resources is the container of the server.
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
	// NodeSelector, Tolerations and Affinity decide which nodes the servers may run on.
	NodeSelector map[string]string   `json:"nodeSelector,omitempty"`
	Tolerations  []corev1.Toleration `json:"tolerations,omitempty"`
	Affinity     *Affinity           `json:"affinity,omitempty"`
	// Replication makes the servers a primary and replicas. Nil is a single server.
	Replication *Replication `json:"replication,omitempty"`
	// MyCnf is the server's configuration, appended to the operator's.
	MyCnf *string `json:"myCnf,omitempty"`
	// StartupProbe is laid over the operator's own, whose handler it keeps: what is said here replaces the same
	// field of the default and nothing else changes.
	StartupProbe *Probe `json:"startupProbe,omitempty"`
	// PointInTimeRecoveryRef has the primary archive its binary logs to the storage of the PointInTimeRecovery it
	// names, so the database can be restored to any moment after a physical backup. It needs replication.
	PointInTimeRecoveryRef *LocalRef `json:"pointInTimeRecoveryRef,omitempty"`
	// BootstrapFrom makes a new database from a backup, and from binary logs up to a moment, instead of an empty one.
	// It is fixed once the database exists.
	BootstrapFrom *BootstrapFrom `json:"bootstrapFrom,omitempty"`
}

// LocalRef names an object of the same namespace.
type LocalRef struct {
	Name string `json:"name"`
}

// BootstrapFrom is where a new database's data comes from.
type BootstrapFrom struct {
	// PointInTimeRecoveryRef is the PointInTimeRecovery whose physical backup and binary logs are restored.
	PointInTimeRecoveryRef *LocalRef `json:"pointInTimeRecoveryRef,omitempty"`
	// TargetRecoveryTime is the moment to restore to. Empty restores everything that was archived.
	TargetRecoveryTime *metav1.Time `json:"targetRecoveryTime,omitempty"`
	// StagingStorage holds the backup and the binary logs while they are restored.
	StagingStorage *StagingStorage `json:"stagingStorage,omitempty"`
}

// StagingStorage is the temporary volume that a backup passes through on its way to or from the bucket.
type StagingStorage struct {
	PersistentVolumeClaim *PersistentVolumeClaimStorage `json:"persistentVolumeClaim,omitempty"`
}

// S3 is a bucket, reached with a key that Geass keeps in a Secret of the database's namespace.
type S3 struct {
	Bucket                      string       `json:"bucket"`
	Endpoint                    string       `json:"endpoint"`
	Prefix                      string       `json:"prefix,omitempty"`
	Region                      string       `json:"region,omitempty"`
	AccessKeyIDSecretKeyRef     SecretKeyRef `json:"accessKeyIdSecretKeyRef"`
	SecretAccessKeySecretKeyRef SecretKeyRef `json:"secretAccessKeySecretKeyRef"`
	TLS                         *S3TLS       `json:"tls,omitempty"`
}

// S3TLS says whether the endpoint is spoken to over TLS.
type S3TLS struct {
	Enabled bool `json:"enabled"`
}

// Probe is the part of a container probe that Geass changes. The operator fills in the rest, including what is asked.
type Probe struct {
	InitialDelaySeconds int32 `json:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int32 `json:"periodSeconds,omitempty"`
	FailureThreshold    int32 `json:"failureThreshold,omitempty"`
	TimeoutSeconds      int32 `json:"timeoutSeconds,omitempty"`
}

// SecretKeyRef names a key of a Secret. Geass always makes the Secret itself and never has the operator generate one.
type SecretKeyRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// Storage is the volume of each server.
type Storage struct {
	Size             *resource.Quantity `json:"size,omitempty"`
	StorageClassName *string            `json:"storageClassName,omitempty"`
}

// Affinity is the part of a pod's placement that Geass sets.
type Affinity struct {
	NodeAffinity    *corev1.NodeAffinity    `json:"nodeAffinity,omitempty"`
	PodAntiAffinity *corev1.PodAntiAffinity `json:"podAntiAffinity,omitempty"`
}

// Replication is the replicated topology: one primary and replicas that follow it.
type Replication struct {
	Enabled bool                `json:"enabled"`
	Primary *PrimaryReplication `json:"primary,omitempty"`
	Replica *ReplicaReplication `json:"replica,omitempty"`
}

// PrimaryReplication says which server is the primary and whether the operator replaces it when it is lost.
type PrimaryReplication struct {
	// PodIndex is the server that is the primary. Setting it to another is a switchover.
	PodIndex *int `json:"podIndex,omitempty"`
	// AutoFailover has the operator choose another when the primary is lost. It only works while a replica is
	// still ready and replicating, which a machine that has gone silent leaves none.
	AutoFailover *bool `json:"autoFailover,omitempty"`
}

// ReplicaReplication says how a replica that cannot catch up is built again.
type ReplicaReplication struct {
	BootstrapFrom *ReplicaBootstrapFrom `json:"bootstrapFrom,omitempty"`
	Recovery      *ReplicaRecovery      `json:"recovery,omitempty"`
}

// ReplicaBootstrapFrom is the backup a replica is built from: the operator takes one from a ready replica.
type ReplicaBootstrapFrom struct {
	PhysicalBackupTemplateRef corev1.LocalObjectReference `json:"physicalBackupTemplateRef"`
}

// ReplicaRecovery has the operator build a replica again when its replication broke beyond repair.
type ReplicaRecovery struct {
	Enabled bool `json:"enabled"`
}

// MariaDBStatus is what the operator reports.
type MariaDBStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// CurrentPrimary is the pod that is the primary now.
	CurrentPrimary *string `json:"currentPrimary,omitempty"`
	// CurrentPrimaryFailingSince is when the primary began to fail, while it is failing.
	CurrentPrimaryFailingSince *metav1.Time       `json:"currentPrimaryFailingSince,omitempty"`
	Replication                *ReplicationStatus `json:"replication,omitempty"`
}

// ReplicationStatus is how each replica follows the primary.
type ReplicationStatus struct {
	Replicas map[string]ReplicaStatus `json:"replicas,omitempty"`
	// Roles is Primary or Replica for each pod.
	Roles map[string]string `json:"roles,omitempty"`
}

// ReplicaStatus is the last thing the operator read from one replica.
type ReplicaStatus struct {
	// GtidCurrentPos and GtidIOPos are positions like 0-10-155: domain, server and then the sequence number, which
	// grows with every transaction the replica has.
	GtidCurrentPos  string `json:"gtidCurrentPos,omitempty"`
	GtidIOPos       string `json:"gtidIOPos,omitempty"`
	SlaveIORunning  *bool  `json:"slaveIORunning,omitempty"`
	SlaveSQLRunning *bool  `json:"slaveSQLRunning,omitempty"`
}

// ConditionTrue reports whether the database has the named condition, and it is true.
func (m *MariaDB) ConditionTrue(name string) bool {
	for i := range m.Status.Conditions {
		if m.Status.Conditions[i].Type == name {
			return m.Status.Conditions[i].Status == metav1.ConditionTrue
		}
	}
	return false
}

// PhysicalBackup is a copy of a database's files. Geass makes one as a template, never run on a schedule: when a
// replica has to be built again the operator takes a copy from a ready replica by it.
type PhysicalBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PhysicalBackupSpec   `json:"spec,omitempty"`
	Status            PhysicalBackupStatus `json:"status,omitempty"`
}

// PhysicalBackupStatus is what the operator reports about a backup.
type PhysicalBackupStatus struct {
	Conditions       []metav1.Condition `json:"conditions,omitempty"`
	LastScheduleTime *metav1.Time       `json:"lastScheduleTime,omitempty"`
}

// ConditionComplete is true while the last backup finished and false while it runs or failed.
const ConditionComplete = "Complete"

// PhysicalBackupList contains a list of PhysicalBackup resources.
type PhysicalBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PhysicalBackup `json:"items"`
}

// PhysicalBackupSpec is how and where a copy is taken.
type PhysicalBackupSpec struct {
	MariaDBRef MariaDBRef `json:"mariaDbRef"`
	// Schedule with Suspend set makes the backup a template that nothing runs by itself.
	Schedule *PhysicalBackupSchedule `json:"schedule,omitempty"`
	// Target is which server the copy is taken from: PreferReplica leaves the primary alone while a replica is ready.
	Target string `json:"target,omitempty"`
	// Storage is where the copy is kept.
	Storage PhysicalBackupStorage `json:"storage"`
	// PodAffinity runs the job on the node of the server it copies, so it can reach the volume.
	PodAffinity *bool `json:"podAffinity,omitempty"`
	// Tolerations let the job onto the nodes a project keeps its data on.
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// Compression is how the copy is compressed before it is stored.
	Compression string `json:"compression,omitempty"`
	// MaxRetention is how long the operator keeps copies in storage. Older ones are removed after each backup.
	MaxRetention *metav1.Duration `json:"maxRetention,omitempty"`
	// StagingStorage holds a copy while it is made and before it is uploaded, when the storage is a bucket.
	StagingStorage *StagingStorage `json:"stagingStorage,omitempty"`
}

// MariaDBRef names the database a backup is of.
type MariaDBRef struct {
	Name string `json:"name"`
	// WaitForIt is whether the backup waits for the database to be ready. A template for rebuilding replicas must not.
	WaitForIt *bool `json:"waitForIt,omitempty"`
}

// PhysicalBackupSchedule is when a backup runs.
type PhysicalBackupSchedule struct {
	Cron string `json:"cron,omitempty"`
	// Immediate takes the first backup as soon as the object exists, without waiting for the schedule.
	Immediate *bool `json:"immediate,omitempty"`
	// OnDemand takes another backup when the value changes.
	OnDemand string `json:"onDemand,omitempty"`
	Suspend  bool   `json:"suspend,omitempty"`
}

// PhysicalBackupStorage is where a copy is kept. Geass uses a volume claim for the copies it makes to build replicas,
// and the project's bucket for the copies that are backups.
type PhysicalBackupStorage struct {
	PersistentVolumeClaim *PersistentVolumeClaimStorage `json:"persistentVolumeClaim,omitempty"`
	S3                    *S3                           `json:"s3,omitempty"`
}

// PersistentVolumeClaimStorage is the claim a copy is kept on.
type PersistentVolumeClaimStorage struct {
	AccessModes      []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`
	Resources        corev1.VolumeResourceRequirements   `json:"resources,omitempty"`
	StorageClassName *string                             `json:"storageClassName,omitempty"`
}

// PointInTimeRecovery archives the binary logs of a replicated database to a bucket. Together with a physical backup
// it lets a new database be made as the old one was at a chosen moment.
type PointInTimeRecovery struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PointInTimeRecoverySpec   `json:"spec,omitempty"`
	Status            PointInTimeRecoveryStatus `json:"status,omitempty"`
}

// PointInTimeRecoveryList contains a list of PointInTimeRecovery resources.
type PointInTimeRecoveryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PointInTimeRecovery `json:"items"`
}

// PointInTimeRecoverySpec is where binary logs go and which backup they continue from.
type PointInTimeRecoverySpec struct {
	// PhysicalBackupRef is the backup that serves as the base: the logs are replayed on top of it.
	PhysicalBackupRef LocalRef `json:"physicalBackupRef"`
	// Storage is the bucket the logs are archived to.
	Storage PhysicalBackupStorage `json:"storage"`
	// Compression is how each log is compressed before it is stored.
	Compression string `json:"compression,omitempty"`
	// StrictMode makes a restore to a moment the logs do not reach fail, instead of stopping at the last one.
	StrictMode *bool `json:"strictMode,omitempty"`
}

// PointInTimeRecoveryStatus is what the operator knows about the archived logs.
type PointInTimeRecoveryStatus struct {
	// LastRecoverableTime is the latest moment that can be restored: the last event in the newest archived log that
	// continues from a completed physical backup.
	LastRecoverableTime *metav1.Time `json:"lastRecoverableTime,omitempty"`
}
