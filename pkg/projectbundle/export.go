package projectbundle

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// SourceApp is a service as it is stored, and the names (never the values) of the keys of its Secret.
type SourceApp struct {
	App        geassv1alpha1.GeassApp
	SecretKeys []string
}

// SourceConnection is a connection to a cloud that belongs to the project.
type SourceConnection struct{ Name, Provider string }

// Source is everything Build is given about a project. It holds no Secret value: the dashboard reads the names of the keys of
// a Secret and nothing else.
type Source struct {
	Project          geassv1alpha1.GeassProject
	Apps             []SourceApp
	Databases        []geassv1alpha1.GeassDatabase
	LogicalDatabases []geassv1alpha1.GeassLogicalDatabase
	Buckets          []geassv1alpha1.GeassObjectStore
	Connections      []SourceConnection
	// Tokens is how many API tokens and deploy hooks the project has.
	Tokens int
	// RootDomain is the platform's domain on the source cluster. A hostname under it was given to a service by the platform,
	// and means nothing on another cluster.
	RootDomain   string
	GeassRelease string
	Now          time.Time
}

// collector gathers what a person has to do, as the definitions are written.
type collector struct {
	reenter []Item
	not     []Item
	notes   []string
}

func (c *collector) reenterItem(kind, id, name, environment, item, reason string) {
	c.reenter = append(c.reenter, Item{Kind: kind, ID: id, Name: name, Environment: environment, Item: item, Reason: reason})
}

func (c *collector) notIncluded(kind, id, name, environment, item, reason string) {
	c.not = append(c.not, Item{Kind: kind, ID: id, Name: name, Environment: environment, Item: item, Reason: reason})
}

const (
	reasonSecretValue = "a secret value: Geass does not export secret values"
	reasonLooksSecret = "its name or its value looks like a secret, so it was left out"
)

// Build makes the bundle of a project. It never reads a secret value and never changes anything: the project, its services and
// the rest are copied, and what is cluster-specific or secret is taken out of the copy and listed.
func Build(src Source) (*Bundle, error) {
	project := &src.Project
	if project.Name == "" {
		return nil, fmt.Errorf("the project has no name")
	}
	display := platform.NormalizeProjectName(project.Spec.DisplayName)
	if display == "" {
		display = project.Name
	}
	environments := slices.Clone(project.Spec.Environments)
	var c collector

	// The Secrets a service may refer to are the connections of the databases and buckets that go with it.
	includedDatabase := map[string]bool{}
	includedBucket := map[string]bool{}
	connections := map[string]bool{}
	for i := range src.Databases {
		db := &src.Databases[i]
		if db.Spec.Placement != geassv1alpha1.DatabasePlacementExternal {
			includedDatabase[db.Name] = true
			connections[ConnectionSecretName(db.Name)] = true
		}
	}
	for i := range src.Buckets {
		store := &src.Buckets[i]
		if platform.ObjectStoreOnClusterServer(store) {
			includedBucket[store.Name] = true
			connections[ConnectionSecretName(store.Name)] = true
		}
	}
	// What a connection Secret that is not included belonged to, so that a variable that was attached to it can say so.
	owners := map[string]string{}
	for i := range src.Databases {
		if !includedDatabase[src.Databases[i].Name] {
			owners[ConnectionSecretName(src.Databases[i].Name)] = "the external database " + platform.ResourceDisplayName(src.Databases[i].ObjectMeta)
		}
	}
	for i := range src.LogicalDatabases {
		owners[ConnectionSecretName(src.LogicalDatabases[i].Name)] = "the logical database " + platform.ResourceDisplayName(src.LogicalDatabases[i].ObjectMeta)
	}
	for i := range src.Buckets {
		if !includedBucket[src.Buckets[i].Name] {
			owners[ConnectionSecretName(src.Buckets[i].Name)] = "the external bucket " + platform.ResourceDisplayName(src.Buckets[i].ObjectMeta)
		}
	}

	bundle := &Bundle{Manifest: Manifest{
		CreatedAt:    src.Now.UTC().Format(time.RFC3339),
		GeassRelease: src.GeassRelease,
		Project:      SourceProject{ID: project.Name, DisplayName: display, Environments: environments},
		Secrets:      SecretsPolicy{Mode: SecretsExcluded},
	}}

	bundle.Project = Document[ProjectSpec]{
		APIVersion: APIVersion, Kind: kindProjectDoc, Metadata: Metadata{Name: project.Name, DisplayName: display},
		Spec: projectSpec(project, display, &c),
	}

	for i := range src.Databases {
		db := &src.Databases[i]
		if !db.DeletionTimestamp.IsZero() {
			continue
		}
		displayName := platform.ResourceDisplayName(db.ObjectMeta)
		if !includedDatabase[db.Name] {
			c.notIncluded(KindDatabase, db.Name, displayName, string(db.Spec.Environment), "",
				fmt.Sprintf("an external %s database: it is not run by Geass and its data stays where it is. Create the connection on the target and connect a database to it again", db.Spec.Provider))
			continue
		}
		bundle.Databases = append(bundle.Databases, Document[geassv1alpha1.GeassDatabaseSpec]{
			APIVersion: APIVersion, Kind: kindDatabaseDoc, Metadata: docMetadata(db.Name, displayName),
			Spec: databaseSpec(db),
		})
		bundle.Manifest.Data = append(bundle.Manifest.Data, databaseData(db, displayName, project))
	}
	for i := range src.Buckets {
		store := &src.Buckets[i]
		if !store.DeletionTimestamp.IsZero() {
			continue
		}
		displayName := platform.ResourceDisplayName(store.ObjectMeta)
		if !includedBucket[store.Name] {
			c.notIncluded(KindBucket, store.Name, displayName, string(store.Spec.Environment), "",
				"a bucket in an AWS account: the objects stay where they are. Create the connection on the target and connect a bucket to it again")
			continue
		}
		bundle.Buckets = append(bundle.Buckets, Document[geassv1alpha1.GeassObjectStoreSpec]{
			APIVersion: APIVersion, Kind: kindBucketDoc, Metadata: docMetadata(store.Name, displayName),
			Spec: bucketSpec(store),
		})
		bundle.Manifest.Data = append(bundle.Manifest.Data, DataNote{
			Kind: KindBucket, ID: store.Name, Name: displayName, Environment: string(store.Spec.Environment), Method: "copy",
			Hint: "The objects are not in the bundle. Copy them with an S3 client (rclone sync, aws s3 sync) from this bucket to the one on the target.",
		})
	}
	for i := range src.LogicalDatabases {
		logical := &src.LogicalDatabases[i]
		if logical.DeletionTimestamp.IsZero() {
			c.notIncluded(KindLogical, logical.Name, platform.ResourceDisplayName(logical.ObjectMeta), string(logical.Spec.Environment), "",
				"a database on a shared database server of the source cluster, which the target does not have. Make a database on a server of the target and move its data with the engine's dump and restore tools")
		}
	}
	// In name order, so that the same project always gives the same notes.
	apps := slices.Clone(src.Apps)
	slices.SortFunc(apps, func(a, b SourceApp) int { return cmp.Compare(a.App.Name, b.App.Name) })
	for i := range apps {
		app := &apps[i]
		if !app.App.DeletionTimestamp.IsZero() {
			continue
		}
		doc := scrubService(app, connections, owners, includedBucket, src.RootDomain, &c)
		bundle.Services = append(bundle.Services, doc)
	}

	for _, connection := range src.Connections {
		c.notIncluded(KindConnection, connection.Name, connection.Name, "", connection.Provider,
			"a connection to a cloud holds credentials, which are not exported. Create it again on the target")
	}
	if project.Spec.GitHubConnectionRef != nil && project.Spec.GitHubConnectionRef.Name != "" {
		c.notIncluded(KindGitHub, "", "", "", "",
			"the project is connected to GitHub. Connect the new project again: services that build from a repository keep their repository and branch")
	}
	if project.Spec.BackupStorage != nil {
		c.notIncluded(KindBackups, "", "", "", "",
			"the project's backup storage uses a connection to AWS, which is not exported. Choose backup storage for the new project")
	}
	for _, webhook := range project.Spec.Webhooks {
		c.notIncluded(KindWebhook, webhook.ID, "", "", hostOf(webhook.URL),
			"webhooks are not exported: their signing secrets are secrets, and an address can carry a token. Add the webhook again on the target")
	}
	if src.Tokens > 0 {
		c.notIncluded(KindTokens, "", "", "", fmt.Sprintf("%d", src.Tokens),
			"API tokens and deploy hooks belong to the cluster that made them. Make new ones on the target")
	}
	if network := project.Spec.Network; network != nil {
		for _, name := range network.AllowFromProjects {
			c.notIncluded(KindProject, name, "", "", "network access from "+name,
				"the project let another project reach its services; projects have other names on another cluster. Allow it again once both are there")
		}
	}

	slices.SortFunc(bundle.Services, func(a, b Document[geassv1alpha1.GeassAppSpec]) int {
		return cmp.Compare(a.Metadata.Name, b.Metadata.Name)
	})
	slices.SortFunc(bundle.Databases, func(a, b Document[geassv1alpha1.GeassDatabaseSpec]) int {
		return cmp.Compare(a.Metadata.Name, b.Metadata.Name)
	})
	slices.SortFunc(bundle.Buckets, func(a, b Document[geassv1alpha1.GeassObjectStoreSpec]) int {
		return cmp.Compare(a.Metadata.Name, b.Metadata.Name)
	})
	slices.SortFunc(bundle.Manifest.Data, func(a, b DataNote) int { return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID)) })
	bundle.Manifest.ToReenter = sortedItems(c.reenter)
	bundle.Manifest.NotIncluded = sortedItems(c.not)
	slices.Sort(c.notes)
	bundle.Manifest.Notes = c.notes
	return bundle, nil
}

func sortedItems(items []Item) []Item {
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(a, b Item) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID), cmp.Compare(a.Item, b.Item))
	})
	return out
}

func docMetadata(name, display string) Metadata {
	m := Metadata{Name: name}
	if display != name {
		m.DisplayName = display
	}
	return m
}

func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.LastIndex(raw, "@"); i >= 0 {
		raw = raw[i+1:]
	}
	return raw
}

// projectSpec is what moves of the project itself. Not the cluster, the organization or the workspace, not the GitHub
// connection, the webhooks, the backup storage or the names of other projects.
func projectSpec(project *geassv1alpha1.GeassProject, display string, c *collector) ProjectSpec {
	spec := project.Spec
	out := ProjectSpec{
		DisplayName: display, Region: spec.Region, RequiredTags: slices.Clone(spec.RequiredTags), Environments: slices.Clone(spec.Environments),
		GitHubReporting: spec.GitHubReporting, EnvironmentQuota: spec.EnvironmentQuota.DeepCopy(), ProjectQuota: spec.ProjectQuota.DeepCopy(),
	}
	if spec.PREnvironments != nil {
		out.PREnvironments = spec.PREnvironments.DeepCopy()
	}
	if spec.Network != nil {
		out.Network = &Network{Isolation: spec.Network.Isolation, BlockNodeAddresses: spec.Network.BlockNodeAddresses}
	}
	for _, variable := range spec.SharedVariables {
		switch {
		case variable.SecretRef != nil:
			c.reenterItem(KindProject, project.Name, display, variable.Environment, variable.Name, "a secret shared variable: "+reasonSecretValue)
		case LooksSecret(variable.Name, variable.Value):
			c.reenterItem(KindProject, project.Name, display, variable.Environment, variable.Name, "a shared variable whose "+reasonLooksSecret)
		default:
			out.SharedVariables = append(out.SharedVariables, SharedVariable{Name: variable.Name, Environment: variable.Environment, Value: variable.Value})
		}
	}
	return out
}

// databaseSpec is a database's definition as it is made new: no project, no credentials, no restore, never deployed.
func databaseSpec(db *geassv1alpha1.GeassDatabase) geassv1alpha1.GeassDatabaseSpec {
	spec := *db.Spec.DeepCopy()
	spec.Project, spec.Org = "", ""
	spec.Placement = geassv1alpha1.DatabasePlacementInCluster
	spec.Provider, spec.Mode, spec.ConnectionRef, spec.PasswordSecretRef = "", "", nil, nil
	spec.ExternalHost, spec.ExternalPort, spec.Username, spec.ClusterSize = "", 0, "", ""
	spec.RestoreFrom = nil
	spec.ServerRegion = ""
	spec.Deploy = geassv1alpha1.GeassDeploySpec{}
	return spec
}

func databaseData(db *geassv1alpha1.GeassDatabase, display string, project *geassv1alpha1.GeassProject) DataNote {
	note := DataNote{Kind: KindDatabase, ID: db.Name, Name: display, Environment: string(db.Spec.Environment), Engine: string(db.Spec.Engine), Method: "none"}
	if db.Spec.Engine == geassv1alpha1.DatabaseEngineRedis {
		note.Hint = "Redis runs as a cache with no persistence: there is nothing to move."
		return note
	}
	if db.Spec.Backup != nil && db.Spec.Backup.Enabled {
		note.BackupEnabled, note.Method = true, "backup-restore"
		note.Hint = "The data is not in the bundle. Restore it on the target from this database's backups: see the guide on moving a project between clusters."
		if status := db.Status.Backup; status != nil {
			note.Archive = status.Archive
			if status.LastBackupTime != nil {
				note.LastBackup = status.LastBackupTime.UTC().Format(time.RFC3339)
			}
		}
		if storage := project.Spec.BackupStorage; storage != nil {
			note.Bucket, note.BucketRegion = storage.Bucket, storage.Region
		}
		return note
	}
	note.Hint = "The data is not in the bundle and this database has no backups to move it with. Turn backups on, wait for a backup, and restore it on the target; or dump it with the engine's own tools."
	return note
}

func bucketSpec(store *geassv1alpha1.GeassObjectStore) geassv1alpha1.GeassObjectStoreSpec {
	spec := *store.Spec.DeepCopy()
	spec.Project = ""
	spec.Placement = geassv1alpha1.ObjectStorePlacementInCluster
	// A bucket in the cluster is on whichever server the cluster has: the target says which.
	spec.ConnectionRef, spec.Region, spec.CreateBucket, spec.ServerRegion = nil, "", false, ""
	spec.StorageSize = nil
	spec.Deploy = geassv1alpha1.GeassDeploySpec{}
	return spec
}

// scrubService copies a service's definition and takes out of the copy what is secret, what refers to an object of the source
// cluster, and what the source platform filled in by itself. Everything it takes out that a person has to put back is listed.
func scrubService(app *SourceApp, connections map[string]bool, owners map[string]string, buckets map[string]bool, rootDomain string, c *collector) Document[geassv1alpha1.GeassAppSpec] {
	spec := *app.App.Spec.DeepCopy()
	id, display, environment := app.App.Name, platform.ResourceDisplayName(app.App.ObjectMeta), string(spec.Environment)
	reenter := func(item, reason string) { c.reenterItem(KindService, id, display, environment, item, reason) }
	spec.Project = ""
	spec.Deploy.Enabled = false

	for _, key := range app.SecretKeys {
		reenter(key, reasonSecretValue)
	}
	for _, key := range slices.Sorted(maps.Keys(spec.SecretData)) {
		if !slices.Contains(app.SecretKeys, key) {
			reenter(key, reasonSecretValue)
		}
	}
	spec.SecretRef, spec.SecretData = nil, nil

	if image := spec.Source.Image; image != nil && image.PullSecret != nil {
		reenter("image pull secret "+image.PullSecret.Name, "registry credentials are not exported")
		image.PullSecret = nil
	}
	if git := spec.Source.Git; git != nil {
		git.ConnectionRef = corev1.LocalObjectReference{}
	}
	if spec.Build.Registry.CredentialRef != nil {
		reenter("registry credential "+spec.Build.Registry.CredentialRef.Name, "registry credentials are not exported")
		spec.Build.Registry.CredentialRef = nil
	}
	for _, ref := range spec.Build.ArgsFrom {
		reenter("build secret "+ref.Name, "build secrets are not exported")
	}
	spec.Build.ArgsFrom = nil
	for _, ref := range spec.EnvFrom {
		switch {
		case ref.SecretRef != nil:
			reenter("variables from the Secret "+ref.SecretRef.Name, "a Secret of the source cluster, which is not exported")
		case ref.ConfigMapRef != nil:
			reenter("variables from the ConfigMap "+ref.ConfigMapRef.Name, "a ConfigMap of the source cluster, which is not exported")
		}
	}
	spec.EnvFrom = nil
	for _, ref := range spec.ConfigMapRefs {
		reenter("ConfigMap "+ref.Name+" mounted at /config/refs/"+ref.Name, "a ConfigMap of the source cluster, which is not exported")
	}
	for _, ref := range spec.SecretRefs {
		reenter("Secret "+ref.Name+" mounted at /secrets/refs/"+ref.Name, "a Secret of the source cluster, which is not exported")
	}
	spec.ConfigMapRefs, spec.SecretRefs = nil, nil

	env := spec.Env[:0:0]
	for _, variable := range spec.Env {
		switch {
		case variable.ValueFrom == nil:
			if LooksSecret(variable.Name, variable.Value) {
				reenter(variable.Name, "a variable whose "+reasonLooksSecret)
				continue
			}
		case variable.ValueFrom.SecretKeyRef != nil:
			ref := variable.ValueFrom.SecretKeyRef
			if !connections[ref.Name] || !slices.Contains(connectionKeys, ref.Key) {
				what := "a Secret of the source cluster"
				if owner := owners[ref.Name]; owner != "" {
					what = "the connection of " + owner + ", which is not moved"
				}
				reenter(variable.Name, "taken from "+what)
				continue
			}
		case variable.ValueFrom.ConfigMapKeyRef != nil:
			reenter(variable.Name, "taken from a ConfigMap of the source cluster, which is not exported")
			continue
		}
		env = append(env, variable)
	}
	spec.Env = env
	if len(spec.Env) == 0 {
		spec.Env = nil
	}

	for _, key := range slices.Sorted(maps.Keys(spec.ConfigData)) {
		if LooksSecret(key, spec.ConfigData[key]) {
			reenter(key, "a configuration value whose "+reasonLooksSecret)
			delete(spec.ConfigData, key)
		}
	}
	if len(spec.ConfigData) == 0 {
		spec.ConfigData = nil
	}

	if ref := spec.Logs.ArchiveStoreRef; ref != nil && !buckets[ref.Name] {
		c.notes = append(c.notes, fmt.Sprintf("Service %s archived its logs to a store that is not a bucket of the project: the setting was left out.", display))
		spec.Logs.ArchiveStoreRef = nil
	}

	spec.Ingress = scrubIngress(spec.Ingress, display, rootDomain, c)
	return Document[geassv1alpha1.GeassAppSpec]{
		APIVersion: APIVersion, Kind: kindServiceDoc, Metadata: docMetadata(id, display), Spec: spec,
	}
}

// scrubIngress drops a hostname the source platform gave the service, which is under its root domain, and keeps the ones the
// person chose.
func scrubIngress(ingress geassv1alpha1.GeassAppIngressSpec, display, rootDomain string, c *collector) geassv1alpha1.GeassAppIngressSpec {
	given := func(host string) bool {
		host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		return rootDomain != "" && host != "" && (host == rootDomain || strings.HasSuffix(host, "."+rootDomain))
	}
	dropped := false
	if given(ingress.Host) {
		ingress.Host, dropped = "", true
	}
	rules := ingress.Rules[:0:0]
	for _, rule := range ingress.Rules {
		if given(rule.Host) {
			dropped = true
			continue
		}
		rules = append(rules, rule)
	}
	ingress.Rules = rules
	if len(ingress.Rules) == 0 {
		ingress.Rules = nil
	}
	if dropped {
		c.notes = append(c.notes, fmt.Sprintf("Service %s had the hostname the source platform gave it; the target gives it one of its own when it is deployed.", display))
	}
	return ingress
}
