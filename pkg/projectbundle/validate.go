package projectbundle

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

var (
	variableName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	// connectionKeys are the keys of a connection Secret that a service may take a variable from.
	connectionKeys = []string{
		platform.ConnectionKeyURI, platform.ConnectionKeyHost, platform.ConnectionKeyPort, platform.ConnectionKeyPassword,
		platform.ConnectionKeyDatabase, platform.ConnectionKeyUsername, platform.ConnectionKeyAccessKey, platform.ConnectionKeySecretKey,
		platform.ConnectionKeyEndpoint, platform.ConnectionKeyBucket,
	}
)

// problems collects what is wrong, so that a bundle is refused with all of it and not one thing at a time.
type problems struct{ list []string }

func (p *problems) add(format string, args ...any) {
	p.list = append(p.list, fmt.Sprintf(format, args...))
}

func (p *problems) err() error {
	if len(p.list) == 0 {
		return nil
	}
	if len(p.list) > 20 {
		more := len(p.list) - 20
		p.list = append(p.list[:20], fmt.Sprintf("and %d more", more))
	}
	return &InvalidError{Problems: p.list}
}

// Validate checks everything that does not depend on the cluster that reads the bundle. What it refuses is what makes a
// bundle dangerous or meaningless to import, and what Geass never writes: a reference to a Secret or a ConfigMap, a
// credential, a connection to something outside, an environment the project does not have.
func (b *Bundle) Validate() error {
	var p problems
	source := b.Manifest.Project
	if source.ID == "" || len(validation.IsDNS1123Label(source.ID)) > 0 {
		p.add("the project's id %q is not a name", source.ID)
	}
	environments := b.Project.Spec.Environments
	if len(environments) == 0 || len(environments) > 100 {
		p.add("a project has between 1 and 100 environments, this one has %d", len(environments))
	}
	if !slices.Equal(environments, source.Environments) {
		p.add("the manifest and project.yaml list different environments")
	}
	seenEnvironment := map[string]bool{}
	for _, environment := range environments {
		if len(validation.IsDNS1123Label(environment)) > 0 {
			p.add("environment %q is not a name", environment)
		} else if _, err := platform.ProjectNamespace(source.ID, environment); err != nil {
			p.add("environment %q: %v", environment, err)
		}
		if seenEnvironment[environment] {
			p.add("environment %q is listed twice", environment)
		}
		seenEnvironment[environment] = true
	}

	b.validateProject(&p, seenEnvironment)

	buckets := map[string]bool{}
	connections := map[string]bool{}
	for _, doc := range b.Databases {
		connections[ConnectionSecretName(doc.Metadata.Name)] = true
	}
	for _, doc := range b.Buckets {
		buckets[doc.Metadata.Name] = true
		connections[ConnectionSecretName(doc.Metadata.Name)] = true
	}

	seen := map[string]bool{}
	unique := func(kind, name string) {
		key := kind + "/" + name
		if seen[key] {
			p.add("%s %q is in the bundle twice", kind, name)
		}
		seen[key] = true
	}
	for i := range b.Databases {
		doc := &b.Databases[i]
		unique(KindDatabase, doc.Metadata.Name)
		validateDatabase(&p, doc, seenEnvironment)
	}
	for i := range b.Buckets {
		doc := &b.Buckets[i]
		unique(KindBucket, doc.Metadata.Name)
		validateBucket(&p, doc, seenEnvironment)
	}
	for i := range b.Services {
		doc := &b.Services[i]
		unique(KindService, doc.Metadata.Name)
		validateService(&p, doc, seenEnvironment, connections, buckets)
	}
	return p.err()
}

func (b *Bundle) validateProject(p *problems, environments map[string]bool) {
	doc := b.Project
	if doc.APIVersion != APIVersion || doc.Kind != kindProjectDoc {
		p.add("project.yaml is not a %s of %s", kindProjectDoc, APIVersion)
	}
	if doc.Metadata.Name != b.Manifest.Project.ID {
		p.add("project.yaml names a different project than the manifest")
	}
	spec := doc.Spec
	if name := platform.NormalizeProjectName(spec.DisplayName); name == "" || len(name) > 63 {
		p.add("the project's name must be 1 to 63 letters, numbers and dashes")
	}
	if spec.Region != "" {
		if err := platform.ValidatePlacementName("region", spec.Region); err != nil {
			p.add("%v", err)
		}
	}
	if len(spec.RequiredTags) > 8 {
		p.add("a project requires at most 8 node tags")
	}
	for _, tag := range spec.RequiredTags {
		if err := platform.ValidatePlacementName("tag", tag); err != nil {
			p.add("%v", err)
		}
	}
	for _, variable := range spec.SharedVariables {
		if !variableName.MatchString(variable.Name) {
			p.add("shared variable %q is not an uppercase variable name", variable.Name)
		}
		if !environments[variable.Environment] {
			p.add("shared variable %s is for the environment %q, which the project does not have", variable.Name, variable.Environment)
		}
	}
	if len(spec.SharedVariables) > 500 {
		p.add("a project has at most 500 shared variables")
	}
	if spec.Network != nil && spec.Network.Isolation != "" &&
		spec.Network.Isolation != geassv1alpha1.NetworkIsolationProject && spec.Network.Isolation != geassv1alpha1.NetworkIsolationNone {
		p.add("network isolation must be Project or None")
	}
	if pr := spec.PREnvironments; pr != nil && pr.BaseEnvironment != "" && !environments[pr.BaseEnvironment] {
		p.add("pull request environments copy %q, which the project does not have", pr.BaseEnvironment)
	}
}

func validateDocument[S any](p *problems, kind, want string, doc *Document[S]) bool {
	if doc.APIVersion != APIVersion || doc.Kind != want {
		p.add("%s %q is not a %s of %s", kind, doc.Metadata.Name, want, APIVersion)
		return false
	}
	if len(validation.IsDNS1123Label(doc.Metadata.Name)) > 0 {
		p.add("%s %q is not a name", kind, doc.Metadata.Name)
		return false
	}
	if _, err := platform.NormalizeResourceDisplayName(doc.Metadata.DisplayName); err != nil {
		p.add("%s %q: %v", kind, doc.Metadata.Name, err)
	}
	return true
}

func validateDatabase(p *problems, doc *Document[geassv1alpha1.GeassDatabaseSpec], environments map[string]bool) {
	if !validateDocument(p, KindDatabase, kindDatabaseDoc, doc) {
		return
	}
	name, spec := doc.Metadata.Name, doc.Spec
	if spec.Project != "" || spec.Org != "" {
		p.add("database %q names a project or an organization: a bundle holds neither", name)
	}
	if !environments[string(spec.Environment)] {
		p.add("database %q is in the environment %q, which the project does not have", name, spec.Environment)
	}
	switch spec.Engine {
	case geassv1alpha1.DatabaseEnginePostgres, geassv1alpha1.DatabaseEngineMySQL, geassv1alpha1.DatabaseEngineMariaDB,
		geassv1alpha1.DatabaseEngineSQLite, geassv1alpha1.DatabaseEngineRedis:
	default:
		p.add("database %q has the engine %q, which Geass does not run", name, spec.Engine)
	}
	if (spec.Placement != "" && spec.Placement != geassv1alpha1.DatabasePlacementInCluster) || spec.Provider != "" || spec.Mode != "" ||
		spec.ConnectionRef != nil || spec.PasswordSecretRef != nil || spec.ExternalHost != "" || spec.ExternalPort != 0 || spec.Username != "" ||
		spec.ClusterSize != "" {
		p.add("database %q is not an in-cluster database: external databases are not moved, and a bundle never holds a connection or a password", name)
	}
	if spec.RestoreFrom != nil {
		p.add("database %q restores from backups: that is done on the target, not carried in a bundle", name)
	}
	if spec.Instances != nil && (*spec.Instances < 1 || *spec.Instances > 9) {
		p.add("database %q asks for %d instances", name, *spec.Instances)
	}
	validateTags(p, "database "+name, spec.RunOn.Tags)
}

func validateBucket(p *problems, doc *Document[geassv1alpha1.GeassObjectStoreSpec], environments map[string]bool) {
	if !validateDocument(p, KindBucket, kindBucketDoc, doc) {
		return
	}
	name, spec := doc.Metadata.Name, doc.Spec
	if spec.Project != "" {
		p.add("bucket %q names a project: a bundle holds none", name)
	}
	if !environments[string(spec.Environment)] {
		p.add("bucket %q is in the environment %q, which the project does not have", name, spec.Environment)
	}
	if (spec.Placement != "" && spec.Placement != geassv1alpha1.ObjectStorePlacementInCluster) || spec.Engine == geassv1alpha1.ObjectStoreEngineS3 ||
		spec.ConnectionRef != nil || spec.Region != "" || spec.CreateBucket {
		p.add("bucket %q is not a bucket on the cluster's object storage: buckets in an AWS account are not moved", name)
	}
	if len(spec.Buckets) > 32 {
		p.add("bucket %q holds more than 32 buckets", name)
	}
	for _, bucket := range spec.Buckets {
		if err := platform.ValidBucketName(bucket); err != nil {
			p.add("bucket %q: %v", name, err)
		}
	}
	validateTags(p, "bucket "+name, spec.RunOn.Tags)
}

func validateTags(p *problems, what string, tags []string) {
	if len(tags) > 8 {
		p.add("%s asks for more than 8 node tags", what)
	}
	for _, tag := range tags {
		if err := platform.ValidatePlacementName("tag", tag); err != nil {
			p.add("%s: %v", what, err)
		}
	}
}

// validateService holds a service to what Geass writes. The references it must not carry are the ones that would let a
// bundle make a service read an object that belongs to someone else on the cluster that imports it.
func validateService(p *problems, doc *Document[geassv1alpha1.GeassAppSpec], environments, connections, buckets map[string]bool) {
	if !validateDocument(p, KindService, kindServiceDoc, doc) {
		return
	}
	name, spec := doc.Metadata.Name, doc.Spec
	refuse := func(what string) {
		p.add("service %q carries %s, which a bundle never does: it would name an object of the cluster that imports it", name, what)
	}
	if spec.Project != "" {
		p.add("service %q names a project: a bundle holds none", name)
	}
	if !environments[string(spec.Environment)] {
		p.add("service %q is in the environment %q, which the project does not have", name, spec.Environment)
	}

	sources := 0
	if image := spec.Source.Image; image != nil {
		sources++
		if strings.TrimSpace(image.Image) == "" || strings.ContainsAny(image.Image, " \t\r\n") {
			p.add("service %q has an image that is not a reference", name)
		}
		if image.PullSecret != nil {
			refuse("an image pull secret")
		}
	}
	if git := spec.Source.Git; git != nil {
		sources++
		if git.ConnectionRef.Name != "" {
			refuse("a GitHub connection")
		}
	}
	if function := spec.Source.Function; function != nil {
		sources++
		if err := platform.ValidateFunctionSource(function); err != nil {
			p.add("service %q: %v", name, err)
		}
	}
	if sources != 1 {
		p.add("service %q must have exactly one source (an image, a repository or a function)", name)
	}

	if spec.SecretRef != nil || len(spec.SecretData) > 0 {
		refuse("secrets")
	}
	if len(spec.EnvFrom) > 0 || len(spec.ConfigMapRefs) > 0 || len(spec.SecretRefs) > 0 {
		refuse("references to ConfigMaps or Secrets")
	}
	if spec.Build.Registry.CredentialRef != nil || len(spec.Build.ArgsFrom) > 0 {
		refuse("a registry credential or build secrets")
	}
	if spec.Logs.ArchiveStoreRef != nil && !buckets[spec.Logs.ArchiveStoreRef.Name] {
		refuse("a log archive that is not a bucket of the project")
	}
	for _, env := range spec.Env {
		if !variableName.MatchString(env.Name) && len(validation.IsEnvVarName(env.Name)) > 0 {
			p.add("service %q has a variable called %q", name, env.Name)
		}
		switch {
		case env.ValueFrom == nil:
		case env.ValueFrom.ConfigMapKeyRef != nil:
			refuse("a variable taken from a ConfigMap")
		case env.ValueFrom.SecretKeyRef != nil:
			ref := env.ValueFrom.SecretKeyRef
			if !connections[ref.Name] || !slices.Contains(connectionKeys, ref.Key) {
				refuse(fmt.Sprintf("the variable %s taken from a Secret that is not the connection of a database or bucket of the project", env.Name))
			}
		}
	}
	for _, host := range serviceHosts(&spec) {
		if _, err := url.Parse("https://" + host); err != nil || (len(validation.IsDNS1123Subdomain(host)) > 0 && len(validation.IsWildcardDNS1123Subdomain(host)) > 0) {
			p.add("service %q has the hostname %q, which is not one", name, host)
		}
	}
	if spec.Port < 0 || spec.Port > 65535 {
		p.add("service %q has the port %d", name, spec.Port)
	}
	if spec.Replicas != nil && *spec.Replicas < 0 {
		p.add("service %q has %d replicas", name, *spec.Replicas)
	}
	validateTags(p, "service "+name, spec.RunOn.Tags)
}

// serviceHosts are the hostnames a service asks to be served on, lowercase and without a trailing dot.
func serviceHosts(spec *geassv1alpha1.GeassAppSpec) []string {
	var hosts []string
	add := func(host string) {
		if host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), ".")); host != "" {
			hosts = append(hosts, host)
		}
	}
	add(spec.Ingress.Host)
	for _, rule := range spec.Ingress.Rules {
		add(rule.Host)
	}
	return hosts
}

// ServiceHosts is serviceHosts for the planner and the importer.
func ServiceHosts(spec *geassv1alpha1.GeassAppSpec) []string { return serviceHosts(spec) }
