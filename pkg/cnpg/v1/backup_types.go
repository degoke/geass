package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Backup phases reported by CloudNativePG in Backup.Status.Phase.
const (
	BackupPhasePending   = "pending"
	BackupPhaseStarted   = "started"
	BackupPhaseRunning   = "running"
	BackupPhaseCompleted = "completed"
	BackupPhaseFailed    = "failed"
)

// Cluster condition types that describe backup health.
const (
	ConditionContinuousArchiving = "ContinuousArchiving"
	ConditionLastBackupSucceeded = "LastBackupSucceeded"
)

// BackupMethodBarmanObjectStore takes base backups into an object store.
const BackupMethodBarmanObjectStore = "barmanObjectStore"

// BackupOwnerReferenceSelf makes the ScheduledBackup own the Backups it creates.
const BackupOwnerReferenceSelf = "self"

// BackupConfiguration configures how a cluster is backed up. Only the in-tree object store
// support is modelled; it is deprecated upstream in favour of the Barman Cloud plugin, which needs
// a newer operator than the one Geass pins. Keep construction of these types in one place in the
// controller so moving to the plugin later is a contained change.
type BackupConfiguration struct {
	BarmanObjectStore *BarmanObjectStoreConfiguration `json:"barmanObjectStore,omitempty"`
	// RetentionPolicy is how long backups are kept, for example "7d".
	RetentionPolicy string `json:"retentionPolicy,omitempty"`
}

// BarmanObjectStoreConfiguration points at an S3-compatible object store.
type BarmanObjectStoreConfiguration struct {
	DestinationPath string                   `json:"destinationPath"`
	EndpointURL     string                   `json:"endpointURL,omitempty"`
	ServerName      string                   `json:"serverName,omitempty"`
	S3Credentials   *S3Credentials           `json:"s3Credentials,omitempty"`
	Wal             *WalBackupConfiguration  `json:"wal,omitempty"`
	Data            *DataBackupConfiguration `json:"data,omitempty"`
}

// S3Credentials references the Secret keys holding S3 credentials. The Secret must live in the
// same namespace as the Cluster.
type S3Credentials struct {
	AccessKeyID     *corev1.SecretKeySelector `json:"accessKeyId,omitempty"`
	SecretAccessKey *corev1.SecretKeySelector `json:"secretAccessKey,omitempty"`
	Region          *corev1.SecretKeySelector `json:"region,omitempty"`
}

// WalBackupConfiguration tunes continuous archiving of write-ahead log files.
type WalBackupConfiguration struct {
	Compression string `json:"compression,omitempty"`
	MaxParallel int32  `json:"maxParallel,omitempty"`
}

// DataBackupConfiguration tunes base backups.
type DataBackupConfiguration struct {
	Compression string `json:"compression,omitempty"`
	Jobs        int32  `json:"jobs,omitempty"`
}

// RecoveryConfiguration bootstraps a new cluster from another cluster's backups.
type RecoveryConfiguration struct {
	// Source names an entry of ClusterSpec.ExternalClusters.
	Source         string                       `json:"source,omitempty"`
	RecoveryTarget *RecoveryTarget              `json:"recoveryTarget,omitempty"`
	Database       string                       `json:"database,omitempty"`
	Owner          string                       `json:"owner,omitempty"`
	Secret         *corev1.LocalObjectReference `json:"secret,omitempty"`
}

// RecoveryTarget selects the point a recovery stops at. Set at most one of TargetTime and
// TargetImmediate; with neither, recovery replays every archived change.
type RecoveryTarget struct {
	BackupID string `json:"backupID,omitempty"`
	// TargetTime is an RFC 3339 timestamp with an explicit zone.
	TargetTime string `json:"targetTime,omitempty"`
	// TargetImmediate stops at the first consistent state, which for an online backup is the
	// point where the backup ended.
	TargetImmediate *bool `json:"targetImmediate,omitempty"`
}

// ExternalCluster describes a source cluster that recovery reads from.
type ExternalCluster struct {
	Name              string                          `json:"name"`
	BarmanObjectStore *BarmanObjectStoreConfiguration `json:"barmanObjectStore,omitempty"`
}

// Backup is a request to back up a cluster once.
type Backup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BackupSpec   `json:"spec,omitempty"`
	Status            BackupStatus `json:"status,omitempty"`
}

// BackupList contains a list of Backup resources.
type BackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Backup `json:"items"`
}

// BackupSpec names the cluster to back up.
type BackupSpec struct {
	Cluster corev1.LocalObjectReference `json:"cluster"`
	Method  string                      `json:"method,omitempty"`
}

// BackupStatus is the observed state of a Backup.
type BackupStatus struct {
	Phase     string       `json:"phase,omitempty"`
	BackupID  string       `json:"backupId,omitempty"`
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	StoppedAt *metav1.Time `json:"stoppedAt,omitempty"`
	Error     string       `json:"error,omitempty"`
}

// ScheduledBackup takes backups of a cluster on a cron schedule.
type ScheduledBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ScheduledBackupSpec `json:"spec,omitempty"`
}

// ScheduledBackupList contains a list of ScheduledBackup resources.
type ScheduledBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ScheduledBackup `json:"items"`
}

// ScheduledBackupSpec defines when and how a cluster is backed up.
type ScheduledBackupSpec struct {
	// Schedule is a six-field cron expression whose first field is seconds.
	Schedule string                      `json:"schedule"`
	Cluster  corev1.LocalObjectReference `json:"cluster"`
	// Immediate also takes a backup as soon as the resource is created.
	Immediate            *bool  `json:"immediate,omitempty"`
	Suspend              *bool  `json:"suspend,omitempty"`
	BackupOwnerReference string `json:"backupOwnerReference,omitempty"`
	Method               string `json:"method,omitempty"`
}
