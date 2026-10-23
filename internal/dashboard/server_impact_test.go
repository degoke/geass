package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func hostnameNode(name string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"kubernetes.io/hostname": name}}}
}

// volumeOnHost is a volume that can be used from one machine, and the claim for it.
func volumeOnHost(host, claim, namespace string, labels map[string]string) []client.Object {
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-" + claim},
		Spec: corev1.PersistentVolumeSpec{NodeAffinity: &corev1.VolumeNodeAffinity{Required: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
			MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "kubernetes.io/hostname", Operator: corev1.NodeSelectorOpIn, Values: []string{host}}},
		}}}}},
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: claim, Namespace: namespace, Labels: labels},
		Spec:       corev1.PersistentVolumeClaimSpec{VolumeName: pv.Name},
	}
	return []client.Object{pv, pvc}
}

func serverNamed(name string) *geassv1alpha1.GeassNode {
	return &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassNodeSpec{Role: geassv1alpha1.NodeRoleWorker},
		Status:     geassv1alpha1.GeassNodeStatus{NodeName: name},
	}
}

func getServerImpact(t *testing.T, srv *Server, name string) (*httptest.ResponseRecorder, serverImpact) {
	t.Helper()
	req := withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, "/api/servers/"+name+"/impact", nil).WithContext(context.Background()))
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	var impact serverImpact
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &impact))
	}
	return rec, impact
}

func pgClaim(database, instance string) map[string]string {
	return map[string]string{cnpgClusterLabel: database, cnpgInstanceLabel: instance}
}

func TestRemovingAServerSaysWhichDataIsOnlyThere(t *testing.T) {
	objects := slices.Concat(
		[]client.Object{serverNamed("w1"), hostnameNode("w1"), hostnameNode("w2")},
		// A replica of a database whose other instance is on another server, and the only instance of another database.
		volumeOnHost("w1", "orders-2", "shop", pgClaim("orders", "orders-2")),
		volumeOnHost("w2", "orders-1", "shop", pgClaim("orders", "orders-1")),
		volumeOnHost("w1", "ledger-1", "shop", pgClaim("ledger", "ledger-1")),
		volumeOnHost("w1", "uploads", "shop", nil),
		// Not here: a volume that only the other server can use, and one that any server can.
		volumeOnHost("w2", "elsewhere", "shop", nil),
		[]client.Object{
			&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "shop"}, Spec: corev1.PersistentVolumeClaimSpec{VolumeName: "pv-shared"}},
			&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv-shared"}},
		},
	)
	srv := &Server{Client: newFakeClient(objects...)}

	rec, impact := getServerImpact(t, srv, "w1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "w1", impact.Server)
	require.Equal(t, []serverVolume{
		{Name: "ledger", Namespace: "shop", Database: true, OtherCopies: 0},
		{Name: "orders", Namespace: "shop", Database: true, OtherCopies: 1},
		{Name: "uploads", Namespace: "shop"},
	}, impact.Volumes, "the replica has a copy on w2, the other database has none, and a volume that is not a database is named as it is")
}

func TestACopyThatIsStrandedOrNotMadeYetIsNotACopyThatSurvives(t *testing.T) {
	objects := slices.Concat(
		[]client.Object{serverNamed("w1"), hostnameNode("w1"), hostnameNode("w2")},
		volumeOnHost("w1", "orders-1", "shop", pgClaim("orders", "orders-1")),
		// The other instance's volume can only be used from a machine that is not in the cluster any more.
		volumeOnHost("w3", "orders-2", "shop", pgClaim("orders", "orders-2")),
		// And a third has a claim that no volume is bound to yet.
		[]client.Object{&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "orders-3", Namespace: "shop", Labels: pgClaim("orders", "orders-3")}}},
	)
	srv := &Server{Client: newFakeClient(objects...)}

	rec, impact := getServerImpact(t, srv, "w1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []serverVolume{{Name: "orders", Namespace: "shop", Database: true, OtherCopies: 0}}, impact.Volumes,
		"the only copy that can be used is the one on this server")
}

func TestRemovingAServerThatHoldsNothingAloneSaysSo(t *testing.T) {
	objects := slices.Concat(
		[]client.Object{serverNamed("w1"), hostnameNode("w1"), hostnameNode("w2")},
		volumeOnHost("w2", "orders-1", "shop", pgClaim("orders", "orders-1")),
	)
	srv := &Server{Client: newFakeClient(objects...)}

	rec, impact := getServerImpact(t, srv, "w1")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, impact.Volumes)
	require.Contains(t, rec.Body.String(), `"volumes":[`, "an empty list, not a missing one: the dialog reads it")
}

func TestAServerThatIsNotThereHasNoImpactToAskAbout(t *testing.T) {
	srv := &Server{Client: newFakeClient(hostnameNode("w1"))}

	rec, _ := getServerImpact(t, srv, "w1")

	require.Equal(t, http.StatusNotFound, rec.Code)
}
