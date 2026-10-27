package projectbundle

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// goodTarget is a cluster that has everything the fixture project needs.
func goodTarget() Target {
	return Target{
		CanKeepProjectID: true, NewProjectID: "proj-aaaa1111",
		ProjectNameTaken: func(string) bool { return false },
		ProjectIDTaken:   func(string) bool { return false },
		Nodes: []Node{
			{Region: "eu", Tags: []string{"hipaa", "gpu"}}, {Region: "eu"}, {Region: "us"},
		},
		HealthyNodes: 3, ObjectStorage: true, ServerlessAddon: true, GitHubApp: true, Registry: true,
		HostProblem: func(string) string { return "" },
	}
}

func codes(findings []Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Code)
	}
	return out
}

func TestAProjectThatTheTargetCanTakeHasOnlyWarningsForWhatItStillNeeds(t *testing.T) {
	plan := PlanImport(buildFixture(t), goodTarget(), Options{})

	require.True(t, plan.OK(), "%v", plan.Blockers)
	require.Equal(t, "morning-beach", plan.Project.Name)
	require.Equal(t, "eu", plan.Project.Region)
	require.Empty(t, plan.Project.ID, "the cluster chooses the id unless the old one is asked for")
	require.ElementsMatch(t, []string{"backup-storage-missing", "domain-dns", "domain-dns", "github-not-connected"}, codes(plan.Warnings),
		"the databases' backups need storage, the two domains need DNS, and the repository service needs GitHub")

	var created []string
	for _, planned := range plan.Create {
		created = append(created, planned.Kind+"/"+planned.Name+"/"+planned.State)
	}
	require.ElementsMatch(t, []string{
		"project/morning-beach/new", "database/orders/draft", "database/cache/draft", "bucket/uploads/draft",
		"service/Storefront/draft", "service/nightly/draft", "service/api/draft",
	}, created, "everything but the project is a draft: nothing runs until it is deployed")

	require.NotEmpty(t, plan.ToReenter)
	require.NotEmpty(t, plan.NotMoved)
	require.Len(t, plan.Data, 3)
	require.Equal(t, []string{
		"Connect the new project to GitHub.",
		"Enter the 14 secret value(s) listed under toReenter again, on the services and the project they belong to.",
		"Choose backup storage for the new project.",
		"Deploy the databases, and restore their data if you are moving it (see the guide).",
		"Deploy the buckets, and copy the objects into them.",
		"Deploy the services (POST /api/v1/projects/{project}/deploy for each environment).",
		"Point DNS at the new cluster, and delete the project on the source cluster once you have checked this one.",
	}, plan.Next)
}

func TestAnImportThatTheTargetCannotTakeSaysEveryReason(t *testing.T) {
	for name, tc := range map[string]struct {
		target func(*Target)
		option Options
		change func(*Bundle)
		want   []string
	}{
		"a name that is taken": {target: func(t *Target) { t.ProjectNameTaken = func(n string) bool { return n == "morning-beach" } }, want: []string{"project-name-taken"}},
		"another name that is free": {option: Options{Name: "Evening Shore"}, target: func(t *Target) {
			t.ProjectNameTaken = func(n string) bool { return n == "morning-beach" }
		}},
		"a name that is not a name":                      {option: Options{Name: "!!!"}, want: []string{"invalid-name"}},
		"a region the target has not":                    {option: Options{Region: "ap"}, want: []string{"region-missing", "tag-missing"}},
		"another region that it has":                     {option: Options{Region: "us"}, want: []string{"tag-missing"}},
		"a tag the project requires and nothing carries": {target: func(t *Target) { t.Nodes = []Node{{Region: "eu"}} }, want: []string{"tag-missing"}},
		"the old id, which is free":                      {option: Options{KeepProjectID: true}},
		"the old id, which is taken":                     {option: Options{KeepProjectID: true}, target: func(t *Target) { t.ProjectIDTaken = func(id string) bool { return id == "proj-1a2b3c4d" } }, want: []string{"project-id-taken"}},
		"the old id, which this caller may not keep":     {option: Options{KeepProjectID: true}, target: func(t *Target) { t.CanKeepProjectID = false }, want: []string{"keep-project-id-refused"}},
		"an organization at its limit":                   {target: func(t *Target) { t.ProjectLimit = "Your organization has reached its limit of 3 projects." }, want: []string{"project-limit"}},
		"no object storage":                              {target: func(t *Target) { t.ObjectStorage = false }, want: []string{"object-storage-missing"}},
		"a hostname that is taken": {target: func(t *Target) {
			t.HostProblem = func(host string) string {
				if host == "shop.acme.example" {
					return "that hostname is already used by another service"
				}
				return ""
			}
		}, want: []string{"host-unavailable"}},
		"an environment too long for the id": {change: func(b *Bundle) {
			b.Project.Spec.Environments = append(b.Project.Spec.Environments, strings.Repeat("e", 50))
		}, want: []string{"environment-name"}},
		"a bucket too long for the project": {change: func(b *Bundle) { b.Buckets[0].Spec.Buckets = []string{strings.Repeat("b", 60)} }, want: []string{"bucket-name"}},
	} {
		t.Run(name, func(t *testing.T) {
			target := goodTarget()
			if tc.target != nil {
				tc.target(&target)
			}
			bundle := buildFixture(t)
			if tc.change != nil {
				tc.change(bundle)
			}
			plan := PlanImport(bundle, target, tc.option)

			require.ElementsMatch(t, tc.want, codes(plan.Blockers), "%v", plan.Blockers)
			require.Equal(t, len(tc.want) == 0, plan.OK())
		})
	}
}

func TestTheOldIdIsKeptWhenAskedForAndAllowed(t *testing.T) {
	plan := PlanImport(buildFixture(t), goodTarget(), Options{KeepProjectID: true})

	require.True(t, plan.OK(), "%v", plan.Blockers)
	require.Equal(t, "proj-1a2b3c4d", plan.Project.ID)
}

func TestWhatTheTargetLacksIsAWarningWhenTheProjectCanStillBeMade(t *testing.T) {
	target := goodTarget()
	target.HealthyNodes = 1
	target.ServerlessAddon = false
	target.GitHubApp = false
	target.Registry = false
	plan := PlanImport(buildFixture(t), target, Options{})

	require.True(t, plan.OK(), "%v", plan.Blockers)
	require.Subset(t, codes(plan.Warnings), []string{"ha-needs-nodes", "serverless-missing", "github-app-missing", "registry-missing", "github-not-connected", "backup-storage-missing"})
	for _, warning := range plan.Warnings {
		if warning.Code == "ha-needs-nodes" {
			require.Equal(t, "orders-db", warning.ID)
			require.Contains(t, warning.Message, "1")
		}
	}
}

func TestAServiceThatAsksForATagNoNodeHasIsWarnedAboutNotRefused(t *testing.T) {
	bundle := buildFixture(t)
	bundle.Services[indexOfService(bundle, "web")].Spec.RunOn.Tags = []string{"fpga"}
	bundle.Databases[indexOfDatabase(bundle, "cache")].Spec.RunOn.Tags = []string{"fpga"}

	plan := PlanImport(bundle, goodTarget(), Options{})

	require.True(t, plan.OK(), "%v", plan.Blockers)
	var warned []string
	for _, warning := range plan.Warnings {
		if warning.Code == "tag-missing" {
			warned = append(warned, warning.Kind+"/"+warning.ID)
		}
	}
	require.ElementsMatch(t, []string{"service/web", "database/cache"}, warned)
}

func TestAServiceThatNamesItsOwnRegistryNeedsNoneOfTheTargets(t *testing.T) {
	bundle := buildFixture(t)
	api := &bundle.Services[indexOfService(bundle, "api")].Spec
	target := goodTarget()
	target.Registry = false

	require.Contains(t, codes(PlanImport(bundle, target, Options{}).Warnings), "registry-missing")

	api.Build.Registry.Repository = "ghcr.io/acme/api"
	require.NotContains(t, codes(PlanImport(bundle, target, Options{}).Warnings), "registry-missing")
}

func TestAProjectWithNothingToMoveNeedsNoSteps(t *testing.T) {
	bundle := &Bundle{
		Manifest: Manifest{Project: SourceProject{ID: "proj-1a2b3c4d", DisplayName: "empty", Environments: []string{"production"}}},
		Project:  Document[ProjectSpec]{Spec: ProjectSpec{DisplayName: "empty", Environments: []string{"production"}}},
	}

	plan := PlanImport(bundle, goodTarget(), Options{})

	require.True(t, plan.OK())
	require.Equal(t, []string{"Point DNS at the new cluster, and delete the project on the source cluster once you have checked this one."}, plan.Next)
	require.Empty(t, plan.Warnings)
	require.NotNil(t, plan.ToReenter)
	require.NotNil(t, plan.Data)
}

func TestThePlannerNeverChangesTheBundle(t *testing.T) {
	bundle := buildFixture(t)
	before, err := bundle.Encode()
	require.NoError(t, err)

	PlanImport(bundle, goodTarget(), Options{Name: "other", Region: "us", KeepProjectID: true})

	after, err := bundle.Encode()
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestAnHostGivenInUpperCaseOrWithADotIsComparedAsItIs(t *testing.T) {
	spec := &geassv1alpha1.GeassAppSpec{Ingress: geassv1alpha1.GeassAppIngressSpec{Host: " Shop.Acme.Example. ", Rules: []geassv1alpha1.GeassAppIngressRule{{Host: "WWW.acme.example"}}}}

	require.Equal(t, []string{"shop.acme.example", "www.acme.example"}, ServiceHosts(spec))
}
