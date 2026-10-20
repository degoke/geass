//go:build k3sintegration

package k3sintegration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/internal/controller"
	"github.com/degoke/geass/pkg/cloud"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

// TestEtcdSnapshotsOnK3s is the cluster's side of copies of its own state, run by hack/k3s-restore-test.sh, which makes
// the bucket and K3s (told by the installer's drop-in to send snapshots there), and wipes the control plane between the
// two phases.
//
//	before: the operator keeps the Secret that K3s reads in step with spec.etcdSnapshots, and says that copies are
//	        arriving once the ones K3s takes on its schedule are in the bucket.
//	after:  the control plane was lost and rebuilt from a copy in the bucket. The cluster object, and with it the
//	        destination, came back inside that copy, and the operator finds the copies again.
func TestEtcdSnapshotsOnK3s(t *testing.T) {
	if os.Getenv("K3S_INTEGRATION") != "true" {
		t.Skip("set K3S_INTEGRATION=true to run the k3s integration tests")
	}
	phase := os.Getenv("DR_PHASE")
	endpoint, bucket := os.Getenv("DR_ENDPOINT"), os.Getenv("DR_BUCKET")
	access, secretKey := os.Getenv("DR_ACCESS_KEY"), os.Getenv("DR_SECRET_KEY")
	if phase == "" || endpoint == "" || bucket == "" || access == "" || secretKey == "" {
		t.Skip("run hack/k3s-restore-test.sh: it sets DR_PHASE, DR_ENDPOINT, DR_BUCKET, DR_ACCESS_KEY and DR_SECRET_KEY")
	}
	const folder = "geass"
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(false)))

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, geassv1alpha1.AddToScheme, helmv1.AddToScheme, monitoringv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c, err := client.New(k3sRestConfig(t), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := client.ObjectKey{Name: "default", Namespace: platform.SystemNamespace}

	// The cluster as the operator sees it with nothing else installed: no add-ons, so that what is looked at here is the
	// copies and not cert-manager or Prometheus arriving.
	off := false
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
		Spec: geassv1alpha1.GeassClusterSpec{
			Version: "v1", ServerURL: "https://127.0.0.1:6443",
			TokenSecretRef: corev1.SecretReference{Name: "geass-token", Namespace: platform.SystemNamespace},
			Addons: geassv1alpha1.GeassClusterAddonsSpec{
				CertManager: geassv1alpha1.GeassClusterCertManagerAddon{Enabled: &off},
				Monitoring:  geassv1alpha1.GeassClusterMonitoringAddon{Enabled: &off},
			},
			EtcdSnapshots: &geassv1alpha1.GeassClusterEtcdSnapshotsSpec{Bucket: bucket, Endpoint: endpoint, Insecure: true, Folder: folder},
		},
	}
	reconciler := &controller.GeassClusterReconciler{Client: c, Scheme: scheme}
	reconcile := func() {
		t.Helper()
		if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}
	current := func() (*geassv1alpha1.GeassCluster, *metav1.Condition) {
		latest := &geassv1alpha1.GeassCluster{}
		if err := c.Get(ctx, key, latest); err != nil {
			t.Fatalf("get the cluster: %v", err)
		}
		return latest, meta.FindStatusCondition(latest.Status.Conditions, platform.ConditionEtcdSnapshotsCurrent)
	}
	k3sSecret := func() *corev1.Secret {
		secret := &corev1.Secret{}
		err := c.Get(ctx, client.ObjectKey{Name: platform.EtcdSnapshotsSecretName, Namespace: platform.EtcdSnapshotsSecretNamespace}, secret)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			t.Fatalf("get the settings Secret: %v", err)
		}
		return secret
	}
	waitCurrent := func(within time.Duration) *geassv1alpha1.GeassCluster {
		t.Helper()
		deadline := time.Now().Add(within)
		for {
			reconcile()
			latest, condition := current()
			if condition != nil && condition.Status == metav1.ConditionTrue {
				return latest
			}
			if time.Now().After(deadline) {
				message := "no condition"
				if condition != nil {
					message = condition.Reason + ": " + condition.Message
				}
				t.Fatalf("the cluster did not say that copies are arriving within %s (%s)", within, message)
			}
			time.Sleep(10 * time.Second)
		}
	}

	switch phase {
	case "before":
		if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platform.SystemNamespace}}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
		if err := c.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platform.EtcdSnapshotCredentialsSecretName, Namespace: platform.SystemNamespace},
			Data:       platform.EtcdSnapshotsCredentialsData(access, secretKey),
		}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}
		if err := c.Create(ctx, cluster); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatal(err)
		}

		// The operator makes the Secret that K3s reads, in the type K3s documents, with the whole destination in it.
		reconcile()
		secret := k3sSecret()
		if secret == nil {
			t.Fatal("the operator did not make the Secret that K3s reads")
		}
		if secret.Type != platform.EtcdSnapshotsSecretType {
			t.Fatalf("the Secret is of type %q", secret.Type)
		}
		if string(secret.Data["etcd-s3-bucket"]) != bucket || string(secret.Data["etcd-s3-access-key"]) != access {
			t.Fatalf("the Secret does not hold the destination: %v", keys(secret.Data))
		}
		_, first := current()
		if first == nil || first.Status != metav1.ConditionFalse {
			t.Fatalf("before any copy is in the bucket the cluster should be waiting for one, not %+v", first)
		}
		t.Logf("waiting for a copy: %s", first.Message)

		// K3s, told by the drop-in, is taking one every minute for the test: the first that reaches the bucket makes the
		// cluster current.
		latest := waitCurrent(5 * time.Minute)
		status := latest.Status.EtcdSnapshots
		if status == nil || status.Copies < 1 || !strings.HasPrefix(status.LastCopyName, "etcd-snapshot-") || status.LastCopyTime == nil {
			t.Fatalf("status does not say which copy is the newest: %+v", status)
		}
		t.Logf("copies are arriving: %d in the bucket, the newest is %s", status.Copies, status.LastCopyName)

	case "after":
		// The control plane was wiped and rebuilt from a copy. What the cluster held then is here, including this.
		restored, _ := current()
		if restored.Spec.EtcdSnapshots == nil || restored.Spec.EtcdSnapshots.Bucket != bucket {
			t.Fatalf("the restored cluster does not name the bucket: %+v", restored.Spec.EtcdSnapshots)
		}
		if k3sSecret() == nil {
			t.Fatal("the Secret that K3s reads did not come back with the cluster's state")
		}
		// The proof that the rebuilt control plane sends copies is one that carries its name: K3s names a snapshot for the
		// machine that took it. The copies that were already in the bucket are the old machine's.
		newHost := os.Getenv("DR_NEW_HOST")
		deadline := time.Now().Add(5 * time.Minute)
		var latest *geassv1alpha1.GeassCluster
		for {
			latest = waitCurrent(5 * time.Minute)
			if newHost == "" || strings.Contains(latest.Status.EtcdSnapshots.LastCopyName, newHost) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the newest copy is %s, and none from %s has arrived", latest.Status.EtcdSnapshots.LastCopyName, newHost)
			}
			time.Sleep(10 * time.Second)
		}
		t.Logf("copies are arriving again, from the new machine: %s", latest.Status.EtcdSnapshots.LastCopyName)

		// The copy that the cluster was rebuilt from is still in the bucket, which is what a second restore would need.
		want := os.Getenv("DR_EXPECT_COPY")
		if want == "" {
			t.Fatal("DR_EXPECT_COPY names the copy that the cluster was restored from")
		}
		objects, err := (&cloud.AWSClient{AccessKey: access, SecretKey: secretKey, Endpoint: "http://" + endpoint}).ListObjects(bucket, folder)
		if err != nil {
			t.Fatalf("list the bucket: %v", err)
		}
		found := false
		for _, object := range objects {
			found = found || object.Key == folder+"/"+want
		}
		if !found {
			t.Fatalf("%s is not in the bucket any more", want)
		}

	default:
		t.Fatalf("DR_PHASE is %q: it is before or after", phase)
	}
}

func keys(data map[string][]byte) []string {
	out := make([]string, 0, len(data))
	for key := range data {
		out = append(out, key)
	}
	return out
}
