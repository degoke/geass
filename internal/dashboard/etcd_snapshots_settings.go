package dashboard

import (
	"context"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const etcdSnapshotsSettingsPath = "/cluster"

// A copy of the cluster's own state in an S3 bucket of the person's own (pkg/platform/etcd_snapshots.go). K3s on the
// control plane is told once, by the installer, to send its snapshots there; this says where, and it takes effect with no
// restart. The access key is stored in a Secret and never comes back to the browser.

// Where the copies stand, in the words the page uses.
const (
	etcdSnapshotsOff     = "off"
	etcdSnapshotsWaiting = "waiting"
	etcdSnapshotsCurrent = "current"
	etcdSnapshotsFailing = "failing"
)

type etcdSnapshotSettings struct {
	// Configured says that a destination is set. State is off, waiting, current or failing.
	Configured bool   `json:"configured"`
	State      string `json:"state"`
	Message    string `json:"message,omitempty"`

	// What follows is for people who may change it: where the copies go, whether a key is stored, and the policy that the
	// key should have.
	Bucket   string `json:"bucket,omitempty"`
	Region   string `json:"region,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Folder   string `json:"folder,omitempty"`
	Insecure bool   `json:"insecure,omitempty"`
	HasKey   bool   `json:"hasKey"`
	Policy   string `json:"policy,omitempty"`

	LastCopyTime string `json:"lastCopyTime,omitempty"`
	LastCopyName string `json:"lastCopyName,omitempty"`
	Copies       int32  `json:"copies,omitempty"`
}

// etcdSnapshotSettingsOf is what the cluster says about its copies.
func etcdSnapshotSettingsOf(cluster *geassv1alpha1.GeassCluster, hasKey, mayChange bool) etcdSnapshotSettings {
	out := etcdSnapshotSettings{State: etcdSnapshotsOff}
	spec := cluster.Spec.EtcdSnapshots
	if spec == nil {
		out.Policy = platform.EtcdSnapshotsPolicy("YOUR-BUCKET")
		return out
	}
	out.Configured = true
	if condition := meta.FindStatusCondition(cluster.Status.Conditions, platform.ConditionEtcdSnapshotsCurrent); condition != nil {
		out.Message = condition.Message
		switch {
		case condition.Status == metav1.ConditionTrue:
			out.State = etcdSnapshotsCurrent
		case condition.Reason == platform.EtcdSnapshotsReasonWaiting:
			out.State = etcdSnapshotsWaiting
		default:
			out.State = etcdSnapshotsFailing
		}
	} else {
		// Saved a moment ago: the operator has not looked yet.
		out.State = etcdSnapshotsWaiting
		out.Message = "Looking for the first copy."
	}
	if status := cluster.Status.EtcdSnapshots; status != nil {
		out.LastCopyName, out.Copies = status.LastCopyName, status.Copies
		if status.LastCopyTime != nil {
			out.LastCopyTime = status.LastCopyTime.UTC().Format(time.RFC3339)
		}
	}
	if mayChange {
		out.Bucket, out.Region, out.Endpoint, out.Folder, out.Insecure = spec.Bucket, spec.Region, spec.Endpoint, spec.Folder, spec.Insecure
		out.HasKey = hasKey
		out.Policy = platform.EtcdSnapshotsPolicy(spec.Bucket)
	}
	return out
}

// handleAPIEtcdSnapshotSettings says where the cluster's copies go and whether they are arriving. Anyone signed in can see
// whether they are; where they go, and the policy for the key, are for the people who can change it. It never returns the key.
func (s *Server) handleAPIEtcdSnapshotSettings(w http.ResponseWriter, r *http.Request) {
	cluster, err := s.etcdSnapshotCluster(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load the cluster"})
		return
	}
	hasKey := false
	if spec := cluster.Spec.EtcdSnapshots; spec != nil {
		secret := &corev1.Secret{}
		if err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.EtcdSnapshotsCredentialsName(spec), Namespace: systemNamespace}, secret); err == nil {
			access, key := platform.EtcdSnapshotsCredentials(secret)
			hasKey = access != "" && key != ""
		}
	}
	writeJSON(w, http.StatusOK, etcdSnapshotSettingsOf(cluster, hasKey, s.sessionCanMutate(r)))
}

// etcdSnapshotCluster is the cluster that the copies belong to.
func (s *Server) etcdSnapshotCluster(ctx context.Context) (*geassv1alpha1.GeassCluster, error) {
	name, err := s.defaultCluster(ctx)
	if err != nil {
		return nil, err
	}
	cluster := &geassv1alpha1.GeassCluster{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}

// handleEtcdSnapshotsSave sets where the cluster's copies go. The key is optional only when one is stored already and the
// form says to keep it: a destination with no key cannot work.
func (s *Server) handleEtcdSnapshotsSave(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, etcdSnapshotsSettingsPath) || !parseFormOrRedirect(w, r, etcdSnapshotsSettingsPath) {
		return
	}
	fail := func(message string) { redirectProbe(w, r, etcdSnapshotsSettingsPath, dashboardLiteralError, message) }
	spec := &geassv1alpha1.GeassClusterEtcdSnapshotsSpec{
		Bucket:   strings.TrimSpace(r.FormValue("bucket")),
		Region:   strings.TrimSpace(r.FormValue("region")),
		Endpoint: strings.TrimSpace(r.FormValue("endpoint")),
		Folder:   strings.Trim(strings.TrimSpace(r.FormValue("folder")), "/"),
		Insecure: formChecked(r.FormValue("insecure")),
	}
	if err := platform.ValidateEtcdSnapshots(spec); err != nil {
		fail(err.Error())
		return
	}
	accessKey, secretKey := strings.TrimSpace(r.FormValue("accessKey")), strings.TrimSpace(r.FormValue("secretKey"))
	keepKey := formChecked(r.FormValue("keepKey"))
	if (accessKey == "") != (secretKey == "") {
		fail("enter both the access key ID and the secret access key, or neither")
		return
	}

	cluster, err := s.etcdSnapshotCluster(r.Context())
	if err != nil {
		fail("could not load the cluster")
		return
	}
	if accessKey == "" && (!keepKey || cluster.Spec.EtcdSnapshots == nil) {
		fail("enter an access key for the bucket: K3s cannot send copies without one")
		return
	}

	// A key that was just written is put back as it was if the cluster cannot be saved, so that what the cluster names and what
	// is stored do not disagree.
	rollback := func() {}
	if accessKey != "" {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platform.EtcdSnapshotCredentialsSecretName, Namespace: systemNamespace, Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}},
			Data:       platform.EtcdSnapshotsCredentialsData(accessKey, secretKey),
		}
		if rollback, err = s.persistSecretWithRollback(r.Context(), secret); err != nil {
			fail("could not save the access key")
			return
		}
	} else if existing := cluster.Spec.EtcdSnapshots; existing != nil {
		// The key that is stored stays, and so does the Secret it is in, even one that somebody named on the cluster with kubectl.
		spec.CredentialsSecretRef = existing.CredentialsSecretRef
	}
	cluster.Spec.EtcdSnapshots = spec
	if err := s.Client.Update(r.Context(), cluster); err != nil {
		rollback()
		fail("could not save where the copies go")
		return
	}
	redirect(w, r, etcdSnapshotsSettingsPath)
}

// handleEtcdSnapshotsClear stops the cluster naming a bucket. K3s on the control plane has to be told as well, with the
// installer's etcd-snapshots disable, and the page says so: until then it goes on trying and failing.
func (s *Server) handleEtcdSnapshotsClear(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, etcdSnapshotsSettingsPath) || !parseFormOrRedirect(w, r, etcdSnapshotsSettingsPath) {
		return
	}
	cluster, err := s.etcdSnapshotCluster(r.Context())
	if err != nil {
		redirectProbe(w, r, etcdSnapshotsSettingsPath, dashboardLiteralError, "could not load the cluster")
		return
	}
	var stale string
	if spec := cluster.Spec.EtcdSnapshots; spec != nil {
		// Only a key that the dashboard made is deleted: one that somebody created with kubectl and named on the cluster is theirs.
		if name := platform.EtcdSnapshotsCredentialsName(spec); name == platform.EtcdSnapshotCredentialsSecretName {
			stale = name
		}
		cluster.Spec.EtcdSnapshots = nil
		if err := s.Client.Update(r.Context(), cluster); err != nil {
			redirectProbe(w, r, etcdSnapshotsSettingsPath, dashboardLiteralError, "could not clear where the copies go")
			return
		}
	}
	if err := s.deleteSystemSecret(r.Context(), stale); err != nil && !apierrors.IsNotFound(err) {
		redirectProbe(w, r, etcdSnapshotsSettingsPath, dashboardLiteralError, "the bucket was cleared, but its access key could not be removed")
		return
	}
	redirect(w, r, etcdSnapshotsSettingsPath)
}

func formChecked(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", boolTrue, "1", "yes":
		return true
	}
	return false
}
