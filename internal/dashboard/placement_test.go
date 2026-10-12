package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/platform"
)

func regionNode(name, region string, extra ...func(*corev1.Node)) *corev1.Node {
	node := roomyTestNode()
	node.Name = name
	node.Labels = map[string]string{}
	if region != "" {
		node.Labels[platform.LabelRegion] = region
	}
	for _, mutate := range extra {
		mutate(node)
	}
	return node
}

func tagged(tag string, reserved bool) func(*corev1.Node) {
	return func(node *corev1.Node) {
		node.Labels[platform.TagKey(tag)] = "true"
		if reserved {
			node.Spec.Taints = append(node.Spec.Taints, corev1.Taint{Key: platform.TagKey(tag), Value: platform.ReservedTaintValue, Effect: corev1.TaintEffectNoSchedule})
		}
	}
}

func placementProject(region string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"production"}, Region: region,
		},
	}
}

func placementCluster() *geassv1alpha1.GeassCluster {
	return &geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace}}
}

func getNode(t *testing.T, c client.Client, name string) corev1.Node {
	t.Helper()
	var node corev1.Node
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name}, &node))
	return node
}

func TestNodePlacementSetsRegionTagsAndReservations(t *testing.T) {
	srv := &Server{Client: newFakeClient(regionNode("worker-1", ""))}

	rec := apiPost(t, srv, "/nodes/worker-1/placement", url.Values{
		"region": {"us"}, "zone": {"aws-us-east-1"}, "tags": {"storage, compute"}, "reserved": {"storage"},
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	node := getNode(t, srv.Client, "worker-1")
	require.Equal(t, "us", node.Labels[platform.LabelRegion])
	require.Equal(t, "aws-us-east-1", node.Labels[platform.LabelZone])
	require.Equal(t, "true", node.Labels[platform.TagKey("storage")])
	require.Equal(t, "true", node.Labels[platform.TagKey("compute")])
	require.Equal(t, []corev1.Taint{{Key: platform.TagKey("storage"), Value: platform.ReservedTaintValue, Effect: corev1.TaintEffectNoSchedule}}, node.Spec.Taints)
}

func TestNodePlacementRejectsBadNamesAndReservingATagTheNodeLacks(t *testing.T) {
	srv := &Server{Client: newFakeClient(regionNode("worker-1", ""))}
	for name, form := range map[string]url.Values{
		"region with a space":       {"region": {"us east"}},
		"tag with an underscore":    {"tags": {"my_tag"}},
		"reserving an untagged tag": {"tags": {"compute"}, "reserved": {"storage"}},
		"too many tags":             {"tags": {"a,b,c,d,e,f,g,h,i,j,k,l,m,n,o,p,q"}},
	} {
		t.Run(name, func(t *testing.T) {
			rec := apiPost(t, srv, "/nodes/worker-1/placement", form)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
	require.Empty(t, getNode(t, srv.Client, "worker-1").Labels, "nothing is applied when the form is refused")
}

func TestNodePlacementLowercasesWhatIsTyped(t *testing.T) {
	srv := &Server{Client: newFakeClient(regionNode("worker-1", ""))}
	require.Equal(t, http.StatusOK, apiPost(t, srv, "/nodes/worker-1/placement", url.Values{"region": {" US "}, "tags": {"Storage"}}).Code)
	node := getNode(t, srv.Client, "worker-1")
	require.Equal(t, "us", node.Labels[platform.LabelRegion])
	require.Contains(t, node.Labels, platform.TagKey("storage"))
}

func TestNodePlacementOnAMissingNodeIsNotFound(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	require.Equal(t, http.StatusNotFound, apiPost(t, srv, "/nodes/ghost/placement", url.Values{"region": {"us"}}).Code)
}

func TestNodePlacementUpdatesTheGeassNodeSoTheOperatorDoesNotUndoIt(t *testing.T) {
	geassNode := &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassNodeSpec{Placement: &geassv1alpha1.GeassNodePlacement{Region: "eu"}},
	}
	srv := &Server{Client: newFakeClient(regionNode("worker-1", "eu"), geassNode)}

	require.Equal(t, http.StatusOK, apiPost(t, srv, "/nodes/worker-1/placement", url.Values{"region": {"us"}}).Code)

	var stored geassv1alpha1.GeassNode
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKeyFromObject(geassNode), &stored))
	require.NotNil(t, stored.Spec.Placement)
	require.Equal(t, "us", stored.Spec.Placement.Region)
}

func TestEmptyingARegionWarnsAboutTheProjectsInIt(t *testing.T) {
	srv := &Server{Client: newFakeClient(regionNode("worker-1", "us"), placementProject("us"), placementCluster())}

	rec := apiPost(t, srv, "/nodes/worker-1/placement", url.Values{"region": {"eu"}})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Warnings []string `json:"warnings"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Warnings, 1)
	require.Contains(t, body.Warnings[0], "no node left in the us region")
}

func TestDefaultRegionMustHaveANode(t *testing.T) {
	srv := &Server{Client: newFakeClient(regionNode("worker-1", "us"), placementCluster())}

	require.Equal(t, http.StatusBadRequest, apiPost(t, srv, "/platform/default-region", url.Values{"region": {"eu"}}).Code, "no node is in eu")
	require.Equal(t, http.StatusOK, apiPost(t, srv, "/platform/default-region", url.Values{"region": {"us"}}).Code)

	var cluster geassv1alpha1.GeassCluster
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: testClusterName, Namespace: platform.SystemNamespace}, &cluster))
	require.Equal(t, "us", cluster.Spec.DefaultRegion)

	require.Equal(t, http.StatusOK, apiPost(t, srv, "/platform/default-region", url.Values{"region": {""}}).Code, "clearing it is allowed")
}

func TestProjectsAreCreatedInTheChosenRegion(t *testing.T) {
	srv := &Server{Client: newFakeClient(regionNode("worker-1", "eu"), placementCluster())}

	require.Equal(t, http.StatusBadRequest, apiPost(t, srv, "/projects/create", url.Values{"region": {"us"}}).Code, "no node is in us")

	rec := apiPost(t, srv, "/projects/create", url.Values{"region": {"eu"}})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	var project geassv1alpha1.GeassProject
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: created["project"], Namespace: platform.SystemNamespace}, &project))
	require.Equal(t, "eu", project.Spec.Region)
}

func TestAProjectWithoutAChoiceIsCreatedWithNoRegion(t *testing.T) {
	srv := &Server{Client: newFakeClient(regionNode("worker-1", "eu"), placementCluster())}
	rec := apiPost(t, srv, "/projects/create", url.Values{})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	var project geassv1alpha1.GeassProject
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: created["project"], Namespace: platform.SystemNamespace}, &project))
	require.Empty(t, project.Spec.Region, "it follows the cluster default")
}

func projectSettingsForm(region string) url.Values {
	return url.Values{"environments": {"production"}, "displayName": {"shop"}, "region": {region}}
}

func TestProjectRegionCanChangeWhileItHasNoDatabases(t *testing.T) {
	srv := &Server{Client: newFakeClient(regionNode("w1", "us"), regionNode("w2", "eu"), placementProject("us"), placementCluster())}

	rec := apiPost(t, srv, "/projects/"+testProjectName+"/settings/save", projectSettingsForm("eu"))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var project geassv1alpha1.GeassProject
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &project))
	require.Equal(t, "eu", project.Spec.Region)
}

func TestProjectRegionCannotChangeOnceItHasADatabase(t *testing.T) {
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassDatabaseSpec{Project: testProjectName, Environment: "production", Engine: geassv1alpha1.DatabaseEnginePostgres},
	}
	srv := &Server{Client: newFakeClient(regionNode("w1", "us"), regionNode("w2", "eu"), placementProject("us"), placementCluster(), db)}

	rec := apiPost(t, srv, "/projects/"+testProjectName+"/settings/save", projectSettingsForm("eu"))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, errorOf(t, rec), "databases")
	var project geassv1alpha1.GeassProject
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &project))
	require.Equal(t, "us", project.Spec.Region, "the project did not move")

	// Saving the same region, as any other settings save does, is not a change.
	require.Equal(t, http.StatusOK, apiPost(t, srv, "/projects/"+testProjectName+"/settings/save", projectSettingsForm("us")).Code)
}

func placedApp() *geassv1alpha1.GeassApp {
	one := int32(1)
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: "production", Port: 8080, Replicas: &one,
			Source: imageAppSource("nginx:alpine"), Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
	}
	_ = platform.SetLastDeployedSpec(app)
	return app
}

func TestAppRunOnTagsMustExistInTheProjectsRegion(t *testing.T) {
	srv := &Server{Client: newFakeClient(
		regionNode("us-1", "us", tagged("gpu", false)), regionNode("eu-1", "eu", tagged("storage", false)),
		placementProject("us"), placementCluster(), placedApp(),
	)}

	rec := apiPost(t, srv, "/apps/api/update", url.Values{"runOnTags": {"storage"}})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, errorOf(t, rec), "No node in the us region is tagged storage")

	rec = apiPost(t, srv, "/apps/api/update", url.Values{"runOnTags": {"gpu"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	app := loadApp(t, srv)
	require.Equal(t, []string{"gpu"}, app.Spec.RunOn.Tags)
	require.Equal(t, pendingChangeSettings, app.Annotations[platform.AppPendingChangesAnnotation], "choosing nodes replaces the pods, so it waits for a deploy")
}

func TestAppRunOnTagsCanBeCleared(t *testing.T) {
	app := placedApp()
	app.Spec.RunOn.Tags = []string{"gpu"}
	require.NoError(t, platform.SetLastDeployedSpec(app))
	srv := &Server{Client: newFakeClient(regionNode("us-1", "us", tagged("gpu", false)), placementProject("us"), placementCluster(), app)}

	require.Equal(t, http.StatusOK, apiPost(t, srv, "/apps/api/update", url.Values{"runOnTags": {""}}).Code)
	require.Empty(t, loadApp(t, srv).Spec.RunOn.Tags)
}

func TestAProjectTokenCannotChooseNodes(t *testing.T) {
	srv := &Server{Client: newFakeClient(regionNode("us-1", "us", tagged("storage", true)), placementProject("us"), placementCluster(), placedApp())}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/apps/api/update", nil))
	req.Header.Set("Accept", "application/json")
	req.Form = url.Values{"runOnTags": {"storage"}}
	req = req.WithContext(context.WithValue(req.Context(), apiTokenContextKey{}, &apitokens.Record{Name: "ci"}))
	rec := httptest.NewRecorder()

	srv.handleAppUpdate(rec, req, "api")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, errorOf(t, rec), "Only a dashboard admin")
	require.Empty(t, loadApp(t, srv).Spec.RunOn.Tags, "a reserved node is the platform's to hand out")
}

func TestDeployIsRefusedWhenTheProjectsRegionHasNoRoom(t *testing.T) {
	app := placedApp()
	app.Spec.Deploy.Enabled = false
	small := regionNode("us-1", "us")
	small.Status.Allocatable = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("64Mi")}
	srv := &Server{Client: newFakeClient(small, regionNode("eu-1", "eu"), placementProject("us"), placementCluster(), app)}

	rec := apiPost(t, srv, "/apps/api/deploy", url.Values{})

	require.Equal(t, http.StatusBadRequest, rec.Code, "the eu region has room, but this project runs in us")
	require.Contains(t, errorOf(t, rec), "the us region")
}

func TestDeployIsAcceptedWhenTheProjectsRegionHasRoom(t *testing.T) {
	app := placedApp()
	app.Spec.Deploy.Enabled = false
	tight := regionNode("eu-1", "eu")
	tight.Status.Allocatable = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("64Mi")}
	srv := &Server{Client: newFakeClient(regionNode("us-1", "us"), tight, placementProject("us"), placementCluster(), app)}

	require.Equal(t, http.StatusOK, apiPost(t, srv, "/apps/api/deploy", url.Values{}).Code, "a full eu region does not matter to a us project")
}

func TestBootstrapListsRegionsTagsAndNodePlacement(t *testing.T) {
	cluster := placementCluster()
	cluster.Spec.DefaultRegion = "us"
	srv := &Server{Client: newFakeClient(
		regionNode("us-1", "us", tagged("storage", true)), regionNode("us-2", "us"), regionNode("eu-1", "eu"), cluster,
	)}

	capacity := srv.clusterCapacity(context.Background())

	require.Equal(t, []regionInfo{{Name: "eu", Nodes: 1, ReadyNodes: 1}, {Name: "us", Nodes: 2, ReadyNodes: 2}}, capacity.Regions)
	require.Equal(t, []tagInfo{{Name: "storage", Nodes: 1, Reserved: true}}, capacity.Tags)
	byName := map[string]capacityNode{}
	for _, node := range capacity.Nodes {
		byName[node.Name] = node
	}
	require.Equal(t, "us", byName["us-1"].Region)
	require.Equal(t, []geassv1alpha1.GeassNodeTag{{Name: "storage", Reserved: true}}, byName["us-1"].Tags)
	require.Equal(t, 2, capacity.SchedulableNodes, "the reserved node is not offered to general work")
}
