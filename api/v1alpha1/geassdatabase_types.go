/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// GeassDatabaseEngine identifies the database engine type.
// +kubebuilder:validation:Enum=Postgres;MySQL;SQLite;Redis
type GeassDatabaseEngine string

const (
	DatabaseEnginePostgres GeassDatabaseEngine = "Postgres"
	DatabaseEngineMySQL    GeassDatabaseEngine = "MySQL"
	DatabaseEngineSQLite   GeassDatabaseEngine = "SQLite"
	DatabaseEngineRedis    GeassDatabaseEngine = "Redis"
)

// GeassDatabasePlacement selects whether the database runs in the cluster.
// +kubebuilder:validation:Enum=InCluster;External
type GeassDatabasePlacement string

const (
	DatabasePlacementInCluster GeassDatabasePlacement = "InCluster"
	DatabasePlacementExternal  GeassDatabasePlacement = "External"
)

// GeassDatabaseProvider identifies an external database provider.
// +kubebuilder:validation:Enum=PlanetScale;AWS
type GeassDatabaseProvider string

const (
	DatabaseProviderPlanetScale GeassDatabaseProvider = "PlanetScale"
	DatabaseProviderAWS         GeassDatabaseProvider = "AWS"
)

// GeassDatabaseMode selects create versus connect for external databases.
// +kubebuilder:validation:Enum=Create;Connect
type GeassDatabaseMode string

const (
	DatabaseModeCreate  GeassDatabaseMode = "Create"
	DatabaseModeConnect GeassDatabaseMode = "Connect"
)

// GeassDatabaseSpec defines the desired state of GeassDatabase.
type GeassDatabaseSpec struct {
	// Project is the owning project.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`
	// Environment is the project environment.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Required
	Environment GeassEnvironment `json:"environment"`

	// Engine is the database engine to provision.
	// +kubebuilder:validation:Enum=Postgres;MySQL;SQLite;Redis
	Engine GeassDatabaseEngine `json:"engine"`

	// Placement selects in-cluster provisioning or an external provider.
	// +kubebuilder:validation:Enum=InCluster;External
	// +kubebuilder:default=InCluster
	// +optional
	Placement GeassDatabasePlacement `json:"placement,omitempty"`

	// Provider is required when Placement is External.
	// +kubebuilder:validation:Enum=PlanetScale;AWS
	// +optional
	Provider GeassDatabaseProvider `json:"provider,omitempty"`

	// Mode selects whether Geass should create a remote database or connect to an existing one.
	// +kubebuilder:validation:Enum=Create;Connect
	// +optional
	Mode GeassDatabaseMode `json:"mode,omitempty"`

	// HighAvailability requests a multi-instance topology. It is only valid when the
	// cluster HA readiness check reports at least three healthy nodes.
	// +optional
	HighAvailability bool `json:"highAvailability,omitempty"`

	// ConnectionRef identifies a GeassCloudConnection used for PlanetScale or AWS.
	// +optional
	ConnectionRef *corev1.LocalObjectReference `json:"connectionRef,omitempty"`

	// DatabaseName overrides the default database name for external or logical-style servers.
	// +optional
	DatabaseName string `json:"databaseName,omitempty"`

	// ExternalHost is the hostname used when connecting to an existing server.
	// +optional
	ExternalHost string `json:"externalHost,omitempty"`

	// ExternalPort is the port used when connecting to an existing server.
	// +optional
	ExternalPort int32 `json:"externalPort,omitempty"`

	// Username is the login used when connecting to an existing server.
	// +optional
	Username string `json:"username,omitempty"`

	// PasswordSecretRef points at the Secret that stores the external password.
	// +optional
	PasswordSecretRef *corev1.SecretKeySelector `json:"passwordSecretRef,omitempty"`

	// Version is the engine major version for in-cluster servers.
	// +kubebuilder:default="16"
	// +optional
	Version string `json:"version,omitempty"`

	// Instances is the number of in-cluster instances. HighAvailability forces at least 3.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	Instances *int32 `json:"instances,omitempty"`

	// StorageSize is the requested PVC size per instance.
	// +kubebuilder:default="10Gi"
	// +optional
	StorageSize *resource.Quantity `json:"storageSize,omitempty"`

	// Resources is the CPU and memory assigned to each in-cluster instance.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Deploy gates database provisioning until the user deploys from the dashboard.
	// +optional
	Deploy GeassDeploySpec `json:"deploy,omitempty"`

	// Backup configures backups. It applies to in-cluster Postgres (continuous, restorable to any
	// moment), MySQL and SQLite (daily snapshots); nil or disabled leaves the database with no
	// recovery path beyond its own disks.
	// +optional
	Backup *GeassDatabaseBackupSpec `json:"backup,omitempty"`

	// RestoreFrom creates this database from another database's backups instead of an empty one.
	// It is only read while the database is first provisioned; changing it afterwards has no effect.
	// +optional
	RestoreFrom *GeassDatabaseRestoreSpec `json:"restoreFrom,omitempty"`
}

// GeassDatabaseBackupSpec configures backups for one database.
type GeassDatabaseBackupSpec struct {
	// Enabled turns continuous backups on. They are stored in the project's backup storage.
	Enabled bool `json:"enabled"`
	// RetentionDays is how many days back the database can be restored.
	// +kubebuilder:default=7
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=365
	// +optional
	RetentionDays int32 `json:"retentionDays,omitempty"`
}

// GeassDatabaseRestoreSpec selects the backups a new database starts from. Set at most one of
// RestorePoint and Time; with neither, the database is restored to the latest recoverable moment.
// +kubebuilder:validation:XValidation:rule="!(has(self.restorePoint) && has(self.time))",message="set either restorePoint or time, not both"
type GeassDatabaseRestoreSpec struct {
	// Archive is the GeassBackupArchive to restore from.
	// +kubebuilder:validation:MinLength=1
	Archive string `json:"archive"`
	// RestorePoint is the ID of a restore point listed on the archive.
	// +optional
	RestorePoint string `json:"restorePoint,omitempty"`
	// Time restores the database as it was at this moment, which must be inside the archive's
	// recovery window.
	// +optional
	Time *metav1.Time `json:"time,omitempty"`
}

// GeassDatabaseBackupState summarises how well a database is protected.
// +kubebuilder:validation:Enum=NeedsStorage;Starting;Protected;AtRisk
type GeassDatabaseBackupState string

const (
	// BackupStateNeedsStorage means backups are on but the project has nowhere to put them yet.
	BackupStateNeedsStorage GeassDatabaseBackupState = "NeedsStorage"
	// BackupStateStarting means backups are set up and the first one has not finished.
	BackupStateStarting GeassDatabaseBackupState = "Starting"
	// BackupStateProtected means backups are running and the database can be restored.
	BackupStateProtected GeassDatabaseBackupState = "Protected"
	// BackupStateAtRisk means backups are failing, so recent changes may not be recoverable.
	BackupStateAtRisk GeassDatabaseBackupState = "AtRisk"
)

// GeassDatabaseBackupStatus is the dashboard-facing summary of backup protection. It is absent
// while backups are off.
type GeassDatabaseBackupStatus struct {
	// State is how well the database is protected.
	State GeassDatabaseBackupState `json:"state"`
	// Message explains the state in plain language.
	// +optional
	Message string `json:"message,omitempty"`
	// Archive is the GeassBackupArchive holding this database's backups.
	// +optional
	Archive string `json:"archive,omitempty"`
	// LastBackupTime is when the most recent backup finished.
	// +optional
	LastBackupTime *metav1.Time `json:"lastBackupTime,omitempty"`
	// RecoveryWindowStart is the earliest moment the database can be restored to.
	// +optional
	RecoveryWindowStart *metav1.Time `json:"recoveryWindowStart,omitempty"`
}

// GeassDatabaseStatus defines the observed state of GeassDatabase.
type GeassDatabaseStatus struct {
	// TargetNamespace is the namespace workloads were created in.
	// +optional
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// ConnectionSecret is the name of the Secret containing connection credentials.
	// +optional
	ConnectionSecret string `json:"connectionSecret,omitempty"`

	// Host is the in-cluster database hostname.
	// +optional
	Host string `json:"host,omitempty"`

	// ReconcileRetry tracks provision retries before reconciliation gives up.
	// +optional
	ReconcileRetry GeassReconcileRetryStatus `json:"reconcileRetry,omitempty"`

	// Provision tracks deploy attempts and errors for this resource.
	// +optional
	Provision GeassProvisionStatus `json:"provision,omitempty"`

	// RestoredAt is when the backups named by spec.restoreFrom were loaded into this database.
	// It is only used by engines that restore by loading a snapshot; it stops the restore from
	// running a second time and overwriting newer data.
	// +optional
	RestoredAt *metav1.Time `json:"restoredAt,omitempty"`

	// Backup summarises backup protection. It is absent while backups are off.
	// +optional
	Backup *GeassDatabaseBackupStatus `json:"backup,omitempty"`

	// conditions represent the current state of the GeassDatabase resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environment`
// +kubebuilder:printcolumn:name="Engine",type=string,JSONPath=`.spec.engine`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// GeassDatabase is the Schema for the geassdatabases API.
type GeassDatabase struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GeassDatabaseSpec `json:"spec"`

	// +optional
	// +optional
	Status GeassDatabaseStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GeassDatabaseList contains a list of GeassDatabase.
type GeassDatabaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassDatabase `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GeassDatabase{}, &GeassDatabaseList{})
		return nil
	})
}
