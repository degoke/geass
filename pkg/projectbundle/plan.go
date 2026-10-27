package projectbundle

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// placeholderID has the length of the id a new project is given, for the checks that depend on it before there is one.
const placeholderID = "proj-00000000"

// Options are what the person who imports asks for.
type Options struct {
	// Name is the new project's name. Empty keeps the name in the bundle.
	Name string
	// Region puts the project in another region than the bundle says. Empty keeps the bundle's.
	Region string
	// KeepProjectID makes the project under the id it had, which is what lets backups filed under that id be restored for it.
	KeepProjectID bool
}

// Node is what the planner needs to know about a machine of the target: where it is and what it is tagged.
type Node struct {
	Region string
	Tags   []string
}

// Target is what the cluster that imports says about itself. The dashboard fills it in from the cluster; Plan only asks it,
// which is what lets the planner be tested without one.
type Target struct {
	// CanKeepProjectID says the caller may ask for the project's old id.
	CanKeepProjectID bool
	// ProjectNameTaken says a project the caller can see already has the name (already normalized).
	ProjectNameTaken func(name string) bool
	// ProjectIDTaken says a project of any organization has the id.
	ProjectIDTaken func(id string) bool
	// NewProjectID is the id the project would get, or "" when it is not chosen yet.
	NewProjectID string
	// ProjectLimit is why the caller's organization may not have another project, or "".
	ProjectLimit string
	// Nodes are the machines that can run work.
	Nodes []Node
	// HealthyNodes is how many machines are healthy, which decides whether a highly available database can start.
	HealthyNodes int
	// ObjectStorage says the cluster has an object storage server for buckets.
	ObjectStorage bool
	// ServerlessAddon says scale to zero is available.
	ServerlessAddon bool
	// GitHubApp says the cluster can connect a project to GitHub.
	GitHubApp bool
	// Registry says builds have somewhere to push their images.
	Registry bool
	// HostProblem says why a hostname cannot be used by the new project, or "".
	HostProblem func(host string) string
}

// Finding is something the target lacks or that stands in the way.
type Finding struct {
	// Code is a word a program can switch on.
	Code        string `json:"code"`
	Kind        string `json:"kind,omitempty"`
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Environment string `json:"environment,omitempty"`
	Message     string `json:"message"`
}

// Planned is one object that an import makes.
type Planned struct {
	Kind        string `json:"kind"`
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Environment string `json:"environment,omitempty"`
	// State is draft for everything but the project: nothing runs until it is deployed.
	State string `json:"state"`
}

// PlannedProject is the project the import would make.
type PlannedProject struct {
	Name string `json:"name"`
	// ID is set when the project keeps its id. Otherwise the cluster chooses one.
	ID           string   `json:"id,omitempty"`
	Region       string   `json:"region,omitempty"`
	Environments []string `json:"environments"`
}

// Plan is what an import would do and what stands in its way.
type Plan struct {
	Project PlannedProject `json:"project"`
	Create  []Planned      `json:"create"`
	// Blockers refuse the import. Warnings do not: what they name will not work until it is put right.
	Blockers  []Finding  `json:"blockers"`
	Warnings  []Finding  `json:"warnings"`
	ToReenter []Item     `json:"toReenter"`
	NotMoved  []Item     `json:"notMoved"`
	Data      []DataNote `json:"data"`
	// Next is what the person does after the import, in order.
	Next []string `json:"next"`
}

// OK says nothing stands in the way.
func (p *Plan) OK() bool { return len(p.Blockers) == 0 }

// PlanImport works out what importing the bundle on the target would do. It changes nothing.
func PlanImport(b *Bundle, target Target, options Options) Plan {
	plan := Plan{
		Create: []Planned{}, Blockers: []Finding{}, Warnings: []Finding{},
		ToReenter: nonNilItems(b.Manifest.ToReenter), NotMoved: nonNilItems(b.Manifest.NotIncluded), Data: b.Manifest.Data, Next: []string{},
	}
	if plan.Data == nil {
		plan.Data = []DataNote{}
	}
	block := func(code, kind, id, name, environment, format string, args ...any) {
		plan.Blockers = append(plan.Blockers, Finding{Code: code, Kind: kind, ID: id, Name: name, Environment: environment, Message: fmt.Sprintf(format, args...)})
	}
	warn := func(code, kind, id, name, environment, format string, args ...any) {
		plan.Warnings = append(plan.Warnings, Finding{Code: code, Kind: kind, ID: id, Name: name, Environment: environment, Message: fmt.Sprintf(format, args...)})
	}

	name := platform.NormalizeProjectName(cmp.Or(options.Name, b.Project.Spec.DisplayName))
	region := cmp.Or(strings.ToLower(strings.TrimSpace(options.Region)), b.Project.Spec.Region)
	plan.Project = PlannedProject{Name: name, Region: region, Environments: slices.Clone(b.Project.Spec.Environments)}
	switch {
	case name == "" || len(name) > 63:
		block("invalid-name", KindProject, "", "", "", "The project's name must be 1 to 63 letters, numbers and dashes")
	case target.ProjectNameTaken != nil && target.ProjectNameTaken(name):
		block("project-name-taken", KindProject, "", name, "", "A project called %s already exists. Choose another name", name)
	}

	id := target.NewProjectID
	if options.KeepProjectID {
		switch {
		case !target.CanKeepProjectID:
			block("keep-project-id-refused", KindProject, "", "", "",
				"Only the platform's administrators can keep the project's old id, and only where organizations are not in use by others. Import without it and the project gets a new id")
		case target.ProjectIDTaken != nil && target.ProjectIDTaken(b.Manifest.Project.ID):
			block("project-id-taken", KindProject, b.Manifest.Project.ID, "", "", "A project with the id %s already exists on this cluster", b.Manifest.Project.ID)
		default:
			id = b.Manifest.Project.ID
			plan.Project.ID = id
		}
	}
	checkID := cmp.Or(id, placeholderID)
	for _, environment := range b.Project.Spec.Environments {
		if _, err := platform.ProjectNamespace(checkID, environment); err != nil {
			block("environment-name", KindProject, "", "", environment, "The environment %s cannot be made: %v", environment, err)
		}
	}
	if target.ProjectLimit != "" {
		block("project-limit", KindProject, "", "", "", "%s", target.ProjectLimit)
	}

	hasRegion := func(region string) bool {
		return slices.ContainsFunc(target.Nodes, func(n Node) bool { return n.Region == region })
	}
	hasTag := func(region, tag string) bool {
		return slices.ContainsFunc(target.Nodes, func(n Node) bool { return (region == "" || n.Region == region) && slices.Contains(n.Tags, tag) })
	}
	if region != "" && !hasRegion(region) {
		block("region-missing", KindProject, "", "", "",
			"No node of this cluster is in the %s region. Tag a node with it first, or import into another region", region)
	}
	for _, tag := range b.Project.Spec.RequiredTags {
		if !hasTag(region, tag) {
			block("tag-missing", KindProject, "", "", "",
				"The project requires nodes tagged %s and no node of this cluster has the tag. A requirement of this kind is never dropped: tag the nodes first", tag)
		}
	}

	plan.Create = append(plan.Create, Planned{Kind: KindProject, Name: name, State: "new"})
	missingTags := func(kind, id, display, environment string, tags []string) {
		for _, tag := range tags {
			if !hasTag(region, tag) {
				warn("tag-missing", kind, id, display, environment, "It asks for nodes tagged %s and no node of this cluster has the tag, so it will not start until one does", tag)
			}
		}
	}

	backups := 0
	for i := range b.Databases {
		doc := &b.Databases[i]
		display := cmp.Or(doc.Metadata.DisplayName, doc.Metadata.Name)
		environment := string(doc.Spec.Environment)
		plan.Create = append(plan.Create, Planned{Kind: KindDatabase, ID: doc.Metadata.Name, Name: display, Environment: environment, State: "draft"})
		if (doc.Spec.HighAvailability || (doc.Spec.Instances != nil && *doc.Spec.Instances >= 3)) && target.HealthyNodes < 3 {
			warn("ha-needs-nodes", KindDatabase, doc.Metadata.Name, display, environment,
				"It runs as a replicated database, which needs at least three healthy nodes, and the cluster has %d", target.HealthyNodes)
		}
		if doc.Spec.Backup != nil && doc.Spec.Backup.Enabled {
			backups++
		}
		missingTags(KindDatabase, doc.Metadata.Name, display, environment, doc.Spec.RunOn.Tags)
	}
	if backups > 0 {
		warn("backup-storage-missing", KindBackups, "", "", "",
			"%d database(s) have backups on, and the new project has nowhere to store them yet. Choose backup storage for the project (it needs a connection to AWS) before deploying them", backups)
	}

	for i := range b.Buckets {
		doc := &b.Buckets[i]
		display := cmp.Or(doc.Metadata.DisplayName, doc.Metadata.Name)
		environment := string(doc.Spec.Environment)
		plan.Create = append(plan.Create, Planned{Kind: KindBucket, ID: doc.Metadata.Name, Name: display, Environment: environment, State: "draft"})
		if !target.ObjectStorage {
			block("object-storage-missing", KindBucket, doc.Metadata.Name, display, environment,
				"This cluster has no object storage server, so it cannot make buckets. Set up object storage first")
		}
		for _, bucket := range doc.Spec.Buckets {
			if _, err := platform.InClusterBucketName(checkID, environment, bucket); err != nil {
				block("bucket-name", KindBucket, doc.Metadata.Name, display, environment, "%v", err)
			}
		}
		missingTags(KindBucket, doc.Metadata.Name, display, environment, doc.Spec.RunOn.Tags)
	}

	var gitServices []string
	for i := range b.Services {
		doc := &b.Services[i]
		display := cmp.Or(doc.Metadata.DisplayName, doc.Metadata.Name)
		environment := string(doc.Spec.Environment)
		plan.Create = append(plan.Create, Planned{Kind: KindService, ID: doc.Metadata.Name, Name: display, Environment: environment, State: "draft"})
		for _, host := range ServiceHosts(&doc.Spec) {
			if target.HostProblem != nil {
				if problem := target.HostProblem(host); problem != "" {
					block("host-unavailable", KindService, doc.Metadata.Name, display, environment, "The hostname %s: %s", host, problem)
					continue
				}
			}
			warn("domain-dns", KindService, doc.Metadata.Name, display, environment,
				"The hostname %s is a domain of yours: point its DNS at this cluster when you switch over, and its certificate is issued once DNS does", host)
		}
		if git := doc.Spec.Source.Git; git != nil {
			gitServices = append(gitServices, display)
			if !target.Registry && doc.Spec.Build.Registry.Repository == "" {
				warn("registry-missing", KindService, doc.Metadata.Name, display, environment,
					"It builds from a repository, and this cluster has no registry to push images to. Set a registry first")
			}
		}
		if sleep := doc.Spec.ScaleToZero; sleep != nil && sleep.Enabled && !target.ServerlessAddon {
			warn("serverless-missing", KindService, doc.Metadata.Name, display, environment,
				"It scales to zero, which needs the serverless add-on, and this cluster does not have it")
		}
		missingTags(KindService, doc.Metadata.Name, display, environment, doc.Spec.RunOn.Tags)
	}
	if len(gitServices) > 0 {
		warn("github-not-connected", KindGitHub, "", "", "",
			"%d service(s) build from a repository (%s). The new project is not connected to GitHub: connect it, then deploy them", len(gitServices), strings.Join(firstN(gitServices, 5), ", "))
		if !target.GitHubApp {
			warn("github-app-missing", KindGitHub, "", "", "", "This cluster has no GitHub App configured, so a project cannot be connected to GitHub yet")
		}
	}

	slices.SortStableFunc(plan.Blockers, compareFindings)
	slices.SortStableFunc(plan.Warnings, compareFindings)
	plan.Next = nextSteps(b, &plan)
	return plan
}

func compareFindings(a, b Finding) int {
	return cmp.Or(cmp.Compare(a.Code, b.Code), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID), cmp.Compare(a.Message, b.Message))
}

func firstN(values []string, n int) []string {
	values = slices.Clone(values)
	slices.Sort(values)
	if len(values) <= n {
		return values
	}
	return append(values[:n], fmt.Sprintf("and %d more", len(values)-n))
}

// nextSteps is what the person does once the project is made, in the order that works: nothing the import makes is running, so
// what the services need is put in place first.
func nextSteps(b *Bundle, plan *Plan) []string {
	var steps []string
	if slices.ContainsFunc(b.Services, func(d Document[geassv1alpha1.GeassAppSpec]) bool { return d.Spec.Source.Git != nil }) {
		steps = append(steps, "Connect the new project to GitHub.")
	}
	if len(plan.ToReenter) > 0 {
		steps = append(steps, fmt.Sprintf("Enter the %d secret value(s) listed under toReenter again, on the services and the project they belong to.", len(plan.ToReenter)))
	}
	if slices.ContainsFunc(b.Databases, func(d Document[geassv1alpha1.GeassDatabaseSpec]) bool {
		return d.Spec.Backup != nil && d.Spec.Backup.Enabled
	}) {
		steps = append(steps, "Choose backup storage for the new project.")
	}
	if len(b.Databases) > 0 {
		steps = append(steps, "Deploy the databases, and restore their data if you are moving it (see the guide).")
	}
	if len(b.Buckets) > 0 {
		steps = append(steps, "Deploy the buckets, and copy the objects into them.")
	}
	if len(b.Services) > 0 {
		steps = append(steps, "Deploy the services (POST /api/v1/projects/{project}/deploy for each environment).")
	}
	steps = append(steps, "Point DNS at the new cluster, and delete the project on the source cluster once you have checked this one.")
	return steps
}
