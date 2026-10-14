//go:build k3sintegration

package k3sintegration

import (
	"context"
	"os"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/controller"
	"github.com/degoke/geass/pkg/platform"
)

// TestNetworkPolicyCheck runs the check that the cluster enforces network policies, against the cluster the kubeconfig
// names, and says whether it found what the cluster should do. On k3s that is that they are enforced; on a kind
// cluster, whose network plugin does not enforce them, that they are not: set GEASS_NETCHECK_EXPECT=NotEnforced.
//
// It needs the operator's image on the cluster's control plane node, because that is the image the check runs. Build
// one and import it (docker save, then k3s ctr images import, or kind load docker-image) and name it in
// GEASS_NETCHECK_IMAGE. The test does not run the operator: it gives the check a Deployment that names the image, and
// runs the check once.
func TestNetworkPolicyCheck(t *testing.T) {
	if os.Getenv("K3S_INTEGRATION") != "true" {
		t.Skip("set K3S_INTEGRATION=true to run the k3s integration tests")
	}
	image := os.Getenv("GEASS_NETCHECK_IMAGE")
	if image == "" {
		t.Skip("set GEASS_NETCHECK_IMAGE to an operator image that is on the cluster's control plane node")
	}
	expect := os.Getenv("GEASS_NETCHECK_EXPECT")
	if expect == "" {
		expect = "Enforced"
	}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, geassv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c, err := client.New(k3sRestConfig(t), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platform.SystemNamespace}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	replicas := int32(0)
	labels := map[string]string{"app": "operator-stand-in"}
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "geass-controller-manager", Namespace: platform.SystemNamespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "manager", Image: image}}},
			},
		},
	}
	// The check reads the operator's image from the operator's own Deployment. A cluster that has one is left as it is,
	// and one that does not is given a stand-in that runs nothing, which is removed afterwards.
	if err := c.Get(ctx, client.ObjectKeyFromObject(deploy), &appsv1.Deployment{}); apierrors.IsNotFound(err) {
		if err := c.Create(ctx, deploy); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Delete(context.Background(), deploy) })
	} else if err != nil {
		t.Fatal(err)
	} else {
		t.Log("the cluster has an operator Deployment already, so the check uses the image it names, not GEASS_NETCHECK_IMAGE")
	}

	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "netcheck", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{
			Version: "v1", ServerURL: "https://127.0.0.1:6443",
			TokenSecretRef: corev1.SecretReference{Name: "geass-token", Namespace: platform.SystemNamespace},
		},
	}
	if err := c.Create(ctx, cluster); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), cluster) })
	cluster.Status.Conditions = platform.SetCondition(nil, platform.ConditionReady, metav1.ConditionTrue, "Ready", "ready")
	if err := c.Status().Update(ctx, cluster); err != nil {
		t.Fatal(err)
	}

	checker := &controller.NetworkPolicyChecker{Client: c, Reader: c}
	started := time.Now()
	checker.Check(ctx, cluster) // this test's own cluster only: any other GeassCluster in the cluster is not touched
	t.Logf("the check took %s", time.Since(started).Round(time.Second))

	var latest geassv1alpha1.GeassCluster
	if err := c.Get(ctx, client.ObjectKeyFromObject(cluster), &latest); err != nil {
		t.Fatal(err)
	}
	condition := meta.FindStatusCondition(latest.Status.Conditions, platform.ConditionNetworkPolicyEnforced)
	if condition == nil {
		t.Fatal("the check recorded nothing on the cluster")
	}
	t.Logf("%s=%s (%s): %s", condition.Type, condition.Status, condition.Reason, condition.Message)
	if condition.Reason != expect {
		t.Errorf("the check found %s, want %s", condition.Reason, expect)
	}
	if latest.Annotations[platform.AnnotationNetworkPolicyChecked] == "" {
		t.Error("the check did not note when it ran")
	}

	err = c.Get(ctx, client.ObjectKey{Name: "geass-netcheck"}, &corev1.Namespace{})
	if err == nil {
		// A namespace being deleted is still there for a moment; it only must not stay.
		time.Sleep(20 * time.Second)
		err = c.Get(ctx, client.ObjectKey{Name: "geass-netcheck"}, &corev1.Namespace{})
	}
	if !apierrors.IsNotFound(err) {
		t.Errorf("the check left its namespace behind: %v", err)
	}
}
