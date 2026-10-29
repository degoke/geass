package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// fullClusterObjects is one 2 CPU node with all of it reserved, and an autoscaling pool that can add nodes of that size.
func fullClusterObjects(mutate func(*geassv1alpha1.GeassNodePool)) []client.Object {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	hog := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "hog", Namespace: "other"},
		Spec: corev1.PodSpec{NodeName: "worker", Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi"),
		}}}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	pool := &geassv1alpha1.GeassNodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "burst", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassNodePoolSpec{
			ConnectionRef: "aws", Size: "t3.small", MinNodes: 0, MaxNodes: 2, Storage: geassv1alpha1.NodeStorageNone,
			Autoscaling: &geassv1alpha1.GeassNodePoolAutoscaling{Enabled: true},
		},
		Status: geassv1alpha1.GeassNodePoolStatus{
			Phase: geassv1alpha1.NodePoolReady, Nodes: 0,
			NodeAllocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1750m"), corev1.ResourceMemory: resource.MustParse("3Gi")},
		},
	}
	if mutate != nil {
		mutate(pool)
	}
	return []client.Object{node, hog, pool}
}

func createServiceNow(t *testing.T, srv *Server, cpu string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{
		"name": {"api"}, "project": {testProjectName}, "environment": {"dev"}, "image": {"nginx:alpine"},
		"cpu": {cpu}, "memory": {"256Mi"}, "deploy": {"true"},
	}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/apps/create", strings.NewReader(form.Encode())).WithContext(context.Background()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	return rec
}

// A full cluster refused the services an autoscaling pool is there to make room for, so nothing asking through the
// dashboard, the command line or the API could ever make the pool buy a machine.
func TestAServiceIsLetInWhenAnAutoscalingPoolCanMakeRoomForIt(t *testing.T) {
	srv := &Server{Client: newFakeClient(fullClusterObjects(nil)...)}

	rec := createServiceNow(t, srv, "1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var app geassv1alpha1.GeassApp
	require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: "api", Namespace: platform.SystemNamespace}, &app))
	require.True(t, app.Spec.Deploy.Enabled, "it was let in and deployed, its pods wait for the node the pool adds")
}

func TestAServiceIsRefusedWhenNoPoolCanMakeRoomForIt(t *testing.T) {
	for name, mutate := range map[string]func(*geassv1alpha1.GeassNodePool){
		"the pool is at its maximum":  func(p *geassv1alpha1.GeassNodePool) { p.Status.Nodes = 2 },
		"the pool does not autoscale": func(p *geassv1alpha1.GeassNodePool) { p.Spec.Autoscaling = nil },
		"the pool is suspended":       func(p *geassv1alpha1.GeassNodePool) { p.Spec.Suspend = true },
		"nothing is known of its nodes": func(p *geassv1alpha1.GeassNodePool) {
			p.Status.NodeAllocatable = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			rec := createServiceNow(t, &Server{Client: newFakeClient(fullClusterObjects(mutate)...)}, "1")
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), "Scale up the cluster")
		})
	}
}

func TestAServiceTooBigForAMachineOfThePoolIsRefusedFromTheDashboard(t *testing.T) {
	rec := createServiceNow(t, &Server{Client: newFakeClient(fullClusterObjects(nil)...)}, "3")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAClusterWithoutPoolsStillRefusesWhatDoesNotFit(t *testing.T) {
	objects := fullClusterObjects(nil)
	srv := &Server{Client: newFakeClient(objects[:2]...)}
	rec := createServiceNow(t, srv, "1")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// A saved draft is deployed later, through the same check.
func TestADraftIsDeployedWhenAnAutoscalingPoolCanMakeRoomForIt(t *testing.T) {
	ctx := context.Background()
	srv := &Server{Client: newFakeClient(fullClusterObjects(nil)...)}
	form := url.Values{"name": {"api"}, "project": {testProjectName}, "environment": {"dev"}, "image": {"nginx:alpine"}, "cpu": {"1"}, "memory": {"256Mi"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/apps/create", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	deployRec := httptest.NewRecorder()
	srv.handleAPI(deployRec, withOrigin(httptest.NewRequest(http.MethodPost, "/api/apps/api/deploy", nil).WithContext(ctx)))
	require.Equal(t, http.StatusOK, deployRec.Code, deployRec.Body.String())
}
