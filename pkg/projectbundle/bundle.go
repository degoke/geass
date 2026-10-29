// Package projectbundle is the file a project is moved between clusters in: what is exported from one Geass and imported by
// another, and the rules for reading it.
//
// A bundle is a gzip-compressed tar of YAML files with a JSON manifest first. It holds a project's definition (its
// services, databases and buckets, their configuration, domains and schedules) and never a secret value, a credential,
// the identity of the organization it came from, or anything a cluster made for itself (status, certificates, builds).
// What it leaves out is listed in the manifest, so that the person who imports it knows what to do next.
//
// A bundle is untrusted input. Nothing authenticates who made one: the checksums in the manifest catch damage, not
// forgery. So Decode checks every field that could name something on the cluster that reads it (a Secret, a ConfigMap, a
// connection) and refuses a bundle that carries one that Geass never writes.
package projectbundle

import (
	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const (
	// Format names what the archive is, and Version which layout of it this is. Version only grows: a reader refuses a
	// version it does not know, and a writer never changes what an old version meant.
	Format  = "geass.dev/project-bundle"
	Version = 1

	// ManifestName is the first file of the archive.
	ManifestName = "geass-bundle.json"
	// ProjectFile holds the project's definition.
	ProjectFile = "project.yaml"

	dirServices  = "services"
	dirDatabases = "databases"
	dirBuckets   = "object-stores"

	// APIVersion is the API group and version the definitions in a bundle are written for.
	APIVersion = "geass.geass.dev/v1alpha1"

	kindProjectDoc  = "GeassProject"
	kindServiceDoc  = "GeassApp"
	kindDatabaseDoc = "GeassDatabase"
	kindBucketDoc   = "GeassObjectStore"

	// SecretsExcluded says no Secret value is in the bundle, which is the only mode version 1 has.
	SecretsExcluded = "excluded"
)

// The kinds of thing a manifest names. They are the words the API answers with.
const (
	KindProject    = "project"
	KindService    = "service"
	KindDatabase   = "database"
	KindBucket     = "bucket"
	KindLogical    = "logical-database"
	KindWebhook    = "webhook"
	KindGitHub     = "github"
	KindConnection = "connection"
	KindTokens     = "tokens"
	KindBackups    = "backup-storage"
)

// Limits bound what is read from an archive. They are what keeps a small upload from becoming a large allocation.
type Limits struct {
	// MaxArchiveBytes is the compressed size.
	MaxArchiveBytes int64
	// MaxExpandedBytes is what all the files add up to.
	MaxExpandedBytes int64
	// MaxFileBytes is the most one file may hold. A function's code, the largest thing a service has, is at most 256 KiB.
	MaxFileBytes int64
	// MaxFiles is how many files there may be, the manifest included.
	MaxFiles int
}

// DefaultLimits are about ten times what a large real project needs.
var DefaultLimits = Limits{MaxArchiveBytes: 16 << 20, MaxExpandedBytes: 64 << 20, MaxFileBytes: 1 << 20, MaxFiles: 500}

// Manifest describes a bundle: what made it, what it holds, and what it could not hold.
type Manifest struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	// CreatedAt is when the bundle was made, RFC 3339.
	CreatedAt string `json:"createdAt"`
	// GeassRelease is the release that made it. It is for people: the Version decides whether it can be read.
	GeassRelease string `json:"geassRelease"`
	// Project is the project that was exported. Its id is what database backups are filed under.
	Project SourceProject `json:"project"`
	// Secrets says what became of secret values.
	Secrets SecretsPolicy `json:"secrets"`
	// Entries lists every file but the manifest, with its checksum.
	Entries []Entry `json:"entries"`
	// ToReenter are the values a person has to enter again on the target: each is named, none is a value.
	ToReenter []Item `json:"toReenter"`
	// NotIncluded are the parts of the project that a bundle does not carry, and why.
	NotIncluded []Item `json:"notIncluded"`
	// Data says, for every database and bucket, that its data is not in the bundle and how it can be moved.
	Data []DataNote `json:"data"`
	// Notes are plain sentences about what was changed on the way out, such as a default hostname that was dropped.
	Notes []string `json:"notes,omitempty"`
}

// SourceProject names the project a bundle was made from.
type SourceProject struct {
	ID           string   `json:"id"`
	DisplayName  string   `json:"displayName"`
	Environments []string `json:"environments"`
}

// SecretsPolicy is what the bundle does with secrets.
type SecretsPolicy struct {
	// Mode is "excluded": no Secret was read for its value.
	Mode string `json:"mode"`
}

// Entry is one file of the archive.
type Entry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Item is one thing a person has to do something about. It names the thing; it never carries a value.
type Item struct {
	Kind string `json:"kind"`
	// ID is the object's name on the source cluster, which is how a plan finds the object it made for it.
	ID string `json:"id,omitempty"`
	// Name is what people call it.
	Name        string `json:"name,omitempty"`
	Environment string `json:"environment,omitempty"`
	// Item is what exactly: a variable, a secret key, a hostname.
	Item   string `json:"item,omitempty"`
	Reason string `json:"reason"`
}

// DataNote says where a resource's data is and how to move it.
type DataNote struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Environment string `json:"environment"`
	Engine      string `json:"engine,omitempty"`
	// Included is always false in version 1.
	Included bool `json:"included"`
	// BackupEnabled says the database's backups are on, so its data can be restored on the target from them.
	BackupEnabled bool `json:"backupEnabled,omitempty"`
	// LastBackup is when its newest backup finished, RFC 3339.
	LastBackup string `json:"lastBackup,omitempty"`
	// Archive is the backup archive on the source cluster, and Bucket and BucketRegion are where its backups are.
	Archive      string `json:"archive,omitempty"`
	Bucket       string `json:"bucket,omitempty"`
	BucketRegion string `json:"bucketRegion,omitempty"`
	// Method is how the data can be moved: "backup-restore" for a database that has backups, "copy" for objects in a
	// bucket, "none" when there is nothing to move or no way Geass offers.
	Method string `json:"method"`
	Hint   string `json:"hint,omitempty"`
}

// Metadata is what a definition names itself by.
type Metadata struct {
	// Name is the object's name on the source cluster. It is only used to tie references together; the target makes its
	// own names.
	Name string `json:"name"`
	// DisplayName is what the object is called in the dashboard.
	DisplayName string `json:"displayName,omitempty"`
}

// Document is one definition in the archive.
type Document[S any] struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Metadata   Metadata `json:"metadata"`
	Spec       S        `json:"spec"`
}

// ProjectSpec is the part of a project that moves.
type ProjectSpec struct {
	DisplayName      string                               `json:"displayName"`
	Region           string                               `json:"region,omitempty"`
	RequiredTags     []string                             `json:"requiredTags,omitempty"`
	Environments     []string                             `json:"environments"`
	SharedVariables  []SharedVariable                     `json:"sharedVariables,omitempty"`
	PREnvironments   *geassv1alpha1.GeassPREnvironments   `json:"prEnvironments,omitempty"`
	GitHubReporting  *bool                                `json:"githubReporting,omitempty"`
	Network          *Network                             `json:"network,omitempty"`
	EnvironmentQuota *geassv1alpha1.GeassEnvironmentQuota `json:"environmentQuota,omitempty"`
	ProjectQuota     *geassv1alpha1.GeassProjectQuota     `json:"projectQuota,omitempty"`
}

// SharedVariable is a project variable with a plain value. A secret one is not here: it is in the manifest's toReenter.
type SharedVariable struct {
	Name        string `json:"name"`
	Environment string `json:"environment"`
	Value       string `json:"value"`
}

// Network is a project's network settings without the names of other projects, which mean nothing on another cluster.
type Network struct {
	Isolation          geassv1alpha1.GeassNetworkIsolation `json:"isolation,omitempty"`
	BlockNodeAddresses bool                                `json:"blockNodeAddresses,omitempty"`
}

// Bundle is a bundle in memory.
type Bundle struct {
	Manifest  Manifest
	Project   Document[ProjectSpec]
	Services  []Document[geassv1alpha1.GeassAppSpec]
	Databases []Document[geassv1alpha1.GeassDatabaseSpec]
	Buckets   []Document[geassv1alpha1.GeassObjectStoreSpec]
}

// ConnectionSecretName is the name of the Secret that holds how to connect to a database or a bucket: the name of the object
// and "-connection". It is the only Secret a service in a bundle may refer to.
func ConnectionSecretName(id string) string { return id + "-connection" }
