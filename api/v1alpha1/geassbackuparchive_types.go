/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Functional Source License, Version 1.1, ALv2 Future License
(FSL-1.1-ALv2, the "License"); you may not use this file except in compliance
with the License. You may obtain a copy of the License at LICENSE or
https://fsl.software/FSL-1.1-ALv2.template.md.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// GeassBackupStorage is an S3 bucket reached through an AWS connection.
type GeassBackupStorage struct {
	// ConnectionRef is the AWS GeassCloudConnection that owns the bucket.
	// +kubebuilder:validation:Required
	ConnectionRef corev1.LocalObjectReference `json:"connectionRef"`
	// Bucket is the S3 bucket that holds backups. Geass creates it when it does not exist.
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`
	Bucket string `json:"bucket"`
	// Region is the bucket's AWS region. It defaults to the connection's region.
	// +optional
	Region string `json:"region,omitempty"`
}

// GeassBackupSource records what is needed to recreate a database from its backups after the
// database itself is gone.
type GeassBackupSource struct {
	// Engine is the kind of database the backups came from. Empty means Postgres, which is what
	// archives created before other engines had backups hold.
	// +optional
	Engine GeassDatabaseEngine `json:"engine,omitempty"`
	// Version is the Postgres major version of the source database. Other engines do not record one.
	// +optional
	Version string `json:"version,omitempty"`
	// DatabaseName is the name of the database inside the Postgres server.
	// +optional
	DatabaseName string `json:"databaseName,omitempty"`
	// StorageSize is the disk size of the source database; a restored database needs at least this much.
	// +optional
	StorageSize *resource.Quantity `json:"storageSize,omitempty"`
	// Resources is the CPU and memory the source database used.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
	// HighAvailability is whether the source database ran as a replicated cluster. For MariaDB it decides how
	// the backups are made and what can be restored: a replicated database can be restored to any moment, and has
	// to be restored into a replicated database, so a restore starts with the same number of instances.
	// +optional
	HighAvailability bool `json:"highAvailability,omitempty"`
	// Instances is how many instances the source database ran.
	// +optional
	Instances *int32 `json:"instances,omitempty"`
	// CredentialsOrigin is the name of the database whose generated password the backed up data carries. A physical
	// copy holds the logins of the server it was taken from, so a database restored from one has to be given the same
	// password to be reached, and it records where it got it from for the restore of a restore.
	// +optional
	CredentialsOrigin string `json:"credentialsOrigin,omitempty"`
}

// GeassBackupArchiveSpec defines the backups kept for one database. The archive is created when
// backups are turned on and survives the database so it can still be restored for a while.
type GeassBackupArchiveSpec struct {
	// Project is the project that owned the database.
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`
	// Environment is the environment the database ran in.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	Environment GeassEnvironment `json:"environment"`
	// Database is the name of the GeassDatabase that owns the backups.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="database cannot be changed"
	Database string `json:"database"`
	// DatabaseUID tells a database apart from a later one that reuses its name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="databaseUID cannot be changed"
	DatabaseUID string `json:"databaseUID"`
	// ServerName is the unique folder the backups live in, so a new database with a reused name
	// never writes into an old database's backups.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="serverName cannot be changed"
	ServerName string `json:"serverName"`
	// Storage is where the backups are kept. It is fixed when the archive is created.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="storage cannot be changed"
	Storage GeassBackupStorage `json:"storage"`
	// RetentionDays is how many days back the database can be restored.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=365
	RetentionDays int32 `json:"retentionDays"`
	// Source describes the database so a restore can size the new one.
	// +optional
	Source GeassBackupSource `json:"source,omitempty"`
	// DeleteAfter is when the backups are removed. It is set when the database is deleted or
	// backups are turned off, and stays empty while the database is protected.
	// +optional
	DeleteAfter *metav1.Time `json:"deleteAfter,omitempty"`
}

// GeassRestorePoint is a base backup a database can be restored to.
type GeassRestorePoint struct {
	// ID identifies the backup.
	ID string `json:"id"`
	// Time is when the backup finished.
	Time metav1.Time `json:"time"`
}

// GeassBackupArchiveStatus defines the observed state of GeassBackupArchive.
type GeassBackupArchiveStatus struct {
	// LastBackupTime is when the most recent backup finished.
	// +optional
	LastBackupTime *metav1.Time `json:"lastBackupTime,omitempty"`
	// RecoveryWindowStart is the earliest moment the database can be restored to.
	// +optional
	RecoveryWindowStart *metav1.Time `json:"recoveryWindowStart,omitempty"`
	// RecoveryWindowEnd is the latest moment known to be recoverable. It stops moving once the
	// database is deleted.
	// +optional
	RecoveryWindowEnd *metav1.Time `json:"recoveryWindowEnd,omitempty"`
	// RestorePoints lists base backups, newest first.
	// +kubebuilder:validation:MaxItems=100
	// +optional
	RestorePoints []GeassRestorePoint `json:"restorePoints,omitempty"`
	// conditions represent the current state of the GeassBackupArchive resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Database",type=string,JSONPath=`.spec.database`
// +kubebuilder:printcolumn:name="Last backup",type=date,JSONPath=`.status.lastBackupTime`
// +kubebuilder:printcolumn:name="Delete after",type=date,JSONPath=`.spec.deleteAfter`

// GeassBackupArchive is the Schema for the geassbackuparchives API.
type GeassBackupArchive struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GeassBackupArchiveSpec `json:"spec"`

	// +optional
	Status GeassBackupArchiveStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GeassBackupArchiveList contains a list of GeassBackupArchive.
type GeassBackupArchiveList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassBackupArchive `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GeassBackupArchive{}, &GeassBackupArchiveList{})
		return nil
	})
}
