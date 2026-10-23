package dashboard

import (
	"cmp"
	"net/http"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	cnpgClusterLabel  = "cnpg.io/cluster"
	cnpgInstanceLabel = "cnpg.io/instanceName"
)

// serverVolume is data that is kept on one server and on no other. It is a database when CloudNativePG put the volume
// there, and a volume otherwise.
type serverVolume struct {
	// Name is the database, or the volume's claim.
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// Database says Name is a Postgres database, so that OtherCopies means something.
	Database bool `json:"database"`
	// OtherCopies is how many other instances of the database keep their data on other servers. With none, what is on this
	// server is all there is of it.
	OtherCopies int `json:"otherCopies"`
}

// serverImpact is what removing a server does to the data that is on it.
type serverImpact struct {
	Server  string         `json:"server"`
	Volumes []serverVolume `json:"volumes"`
}

// handleAPIServerImpact serves GET /api/servers/{name}/impact: the data that is on the server alone, which is lost when
// the server is removed. It is asked for before the removal is confirmed, and lists only what is not kept anywhere else.
func (s *Server) handleAPIServerImpact(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformContext(w, r) {
		return
	}
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/servers/"), "/impact")
	ctx := r.Context()
	var node geassv1alpha1.GeassNode
	if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "server not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read the server"})
		return
	}
	nodeName := node.Name
	if node.Status.NodeName != "" {
		nodeName = node.Status.NodeName
	}
	nodes, err := s.listNodes(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read cluster nodes"})
		return
	}
	var volumes corev1.PersistentVolumeList
	if err := s.Client.List(asSystem(ctx), &volumes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read cluster volumes"})
		return
	}
	var claims corev1.PersistentVolumeClaimList
	if err := s.Client.List(asSystem(ctx), &claims); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read cluster volumes"})
		return
	}
	writeJSON(w, http.StatusOK, serverImpact{
		Server:  node.Name,
		Volumes: volumesOnlyOnServer(nodeName, nodes, volumes.Items, claims.Items),
	})
}

// volumesOnlyOnServer groups the volumes that only the named node can use by what they belong to.
func volumesOnlyOnServer(nodeName string, nodes []corev1.Node, volumes []corev1.PersistentVolume, claims []corev1.PersistentVolumeClaim) []serverVolume {
	onlyHere := map[string]bool{}
	for _, volume := range platform.VolumesOnlyOn(nodeName, nodes, volumes) {
		onlyHere[volume.Name] = true
	}
	// A Postgres database has one claim per instance: the instances that are not on this server are copies.
	type instance struct{ namespace, database, instance string }
	instances := map[instance]bool{}
	here := map[instance]bool{}
	for i := range claims {
		claim := &claims[i]
		database := claim.Labels[cnpgClusterLabel]
		if database == "" || !claim.DeletionTimestamp.IsZero() {
			continue
		}
		key := instance{claim.Namespace, database, claim.Labels[cnpgInstanceLabel]}
		instances[key] = true
		if onlyHere[claim.Spec.VolumeName] {
			here[key] = true
		}
	}

	found := map[serverVolume]bool{}
	// Never nil: the answer is JSON, where nothing is [] and not null.
	out := []serverVolume{}
	for i := range claims {
		claim := &claims[i]
		if !onlyHere[claim.Spec.VolumeName] || !claim.DeletionTimestamp.IsZero() {
			continue
		}
		item := serverVolume{Name: claim.Name, Namespace: claim.Namespace}
		if database := claim.Labels[cnpgClusterLabel]; database != "" {
			item.Name, item.Database = database, true
			for key := range instances {
				if key.namespace == claim.Namespace && key.database == database && !here[key] {
					item.OtherCopies++
				}
			}
		}
		if !found[item] {
			found[item] = true
			out = append(out, item)
		}
	}
	slices.SortFunc(out, func(a, b serverVolume) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	return out
}
