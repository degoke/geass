//go:build k3sintegration

package k3sintegration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	encryptionProject = "encrypted"
	encryptionEnv     = "dev"
	encryptionNS      = encryptionProject + "-" + encryptionEnv
	encryptionApp     = "secret-reader"
	// encryptionImage is the image the burstable test uses too, so a cluster that ran that test already has it.
	encryptionImage = "python:3.12-alpine"
)

// k3sExecTimeout is how long one command inside the k3s container may take. On a loaded machine `docker exec` can stall, and
// without a limit the test would sit there until go test's own timeout ends the whole package.
const k3sExecTimeout = 90 * time.Second

// k3sExec runs a command inside the k3s container that hack/k3s-up.sh started.
func k3sExec(t *testing.T, args ...string) string {
	t.Helper()
	name := os.Getenv("K3S_NAME")
	if name == "" {
		name = "geass-k3s"
	}
	ctx, cancel := context.WithTimeout(context.Background(), k3sExecTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", append([]string{"exec", name}, args...)...).CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("docker exec %s %s did not finish within %s: is Docker overloaded?", name, strings.Join(args, " "), k3sExecTimeout)
	}
	if err != nil {
		t.Fatalf("docker exec %s %s: %v\n%s\n(set K3S_NAME if the k3s container is not called geass-k3s)", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestSecretsAreEncryptedAtRestOnK3s follows a service's secret through a k3s that has secrets encryption on, as the
// installer sets one up. The service is given its secret the way the dashboard gives one (a Secret in geass-system that
// the service names), the operator copies it into the environment's namespace, the service reads it from its
// environment, and the value is nowhere in k3s's datastore, which does hold encrypted values.
func TestSecretsAreEncryptedAtRestOnK3s(t *testing.T) {
	if os.Getenv("K3S_INTEGRATION") != "true" {
		t.Skip("set K3S_INTEGRATION=true to run the k3s secrets encryption test (hack/k3s-up.sh starts a k3s)")
	}
	ctx, c := startOperator(t, encryptionProject, encryptionEnv)
	kube, err := kubernetes.NewForConfig(k3sRestConfig(t))
	if err != nil {
		t.Fatal(err)
	}

	// A value nobody could guess and nothing else in the cluster holds: it is in the Secret and nowhere else, so
	// finding it in the datastore means a Secret was stored in the clear.
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	marker := "ZZGEASSSECRET-" + hex.EncodeToString(random)
	// A second value, in a ConfigMap, which K3s does not encrypt. The search below has to find it, or finding nothing for the
	// Secret would only mean the search cannot see values in this datastore.
	control := "ZZGEASSCONTROL-" + hex.EncodeToString(random)
	controlName := encryptionApp + "-control"
	if err := c.Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: controlName, Namespace: platform.SystemNamespace},
		Data:       map[string]string{"value": control},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Delete(context.Background(), &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: controlName, Namespace: platform.SystemNamespace}})
	})

	secretName := encryptionApp + "-secrets"
	if err := c.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: platform.SystemNamespace},
		StringData: map[string]string{"SECRET_VALUE": marker},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Delete(context.Background(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: platform.SystemNamespace}})
	})
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: encryptionApp, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: encryptionProject, Environment: encryptionEnv, Port: 8080,
			Source:    geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: encryptionImage}},
			Build:     geassv1alpha1.GeassAppBuildSpec{Command: []string{"python", "-c", "import os, time\nprint('secret=' + os.environ['SECRET_VALUE'], flush=True)\ntime.sleep(86400)\n"}},
			SecretRef: &corev1.LocalObjectReference{Name: secretName},
			Deploy:    geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
	}
	if err := c.Create(ctx, app); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), app) })

	t.Run("the service reads its secret from its environment", func(t *testing.T) {
		eventually(t, 5*time.Minute, "the service to print the secret it was given", func() (bool, string) {
			var pods corev1.PodList
			if err := c.List(ctx, &pods, client.InNamespace(encryptionNS), client.MatchingLabels{platform.K8sLabelAppName: encryptionApp}); err != nil || len(pods.Items) == 0 {
				return false, "no pod yet"
			}
			logs, err := kube.CoreV1().Pods(encryptionNS).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{}).DoRaw(ctx)
			if err != nil {
				return false, err.Error()
			}
			return strings.Contains(string(logs), "secret="+marker), "the pod has not printed it yet: " + strings.TrimSpace(string(logs))
		})
	})

	t.Run("k3s reports secrets encryption on", func(t *testing.T) {
		status := k3sExec(t, "k3s", "secrets-encrypt", "status")
		if !strings.Contains(status, "Encryption Status: Enabled") {
			t.Fatalf("k3s secrets encryption is not on, so every Secret is stored in the clear. hack/k3s-up.sh starts k3s with the installer's configuration, which turns it on, "+
				"but it reuses a geass-k3s that is already running: if this one was started before that, run `make k3s-down` and then `make k3s-up` to create it again:\n%s", status)
		}
	})

	t.Run("the secret is not in k3s's datastore, though a value that is not encrypted is", func(t *testing.T) {
		// The datastore is sqlite in the throwaway k3s and etcd in a cluster the installer built; both live here.
		const datastore = "/var/lib/rancher/k3s/server/db"
		plain := strings.TrimSpace(k3sExec(t, "sh", "-c", "grep -rla '"+marker+"' "+datastore+" 2>/dev/null; true"))
		if plain != "" {
			t.Fatalf("the secret's value is readable in k3s's datastore, in: %s", plain)
		}
		// A search that finds nothing proves nothing unless it could have found something. The ConfigMap's value is stored in
		// the same way as a Secret's, only not encrypted, so it must be found; and encrypted values must be there too.
		visible := strings.TrimSpace(k3sExec(t, "sh", "-c", "grep -rla '"+control+"' "+datastore+" 2>/dev/null; true"))
		if visible == "" {
			t.Fatalf("a ConfigMap's value, which k3s does not encrypt, is not found under %s either, so this search cannot see values stored there and finding nothing for the secret proves nothing", datastore)
		}
		encrypted := strings.TrimSpace(k3sExec(t, "sh", "-c", "grep -rla 'k8s:enc:' "+datastore+" 2>/dev/null; true"))
		if encrypted == "" {
			t.Fatalf("no file under %s holds an encrypted value, so k3s is not encrypting what it stores", datastore)
		}
	})
}
