package controller

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

func claimBoundTo(name, namespace, volume string, labels map[string]string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: corev1.PersistentVolumeClaimSpec{
			VolumeName: volume,
			Resources:  corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")}},
		},
	}
}

func volumeScheduled(name, status string) []client.Object {
	volume := longhornVolume(name, 1)
	conditions := []any{map[string]any{"type": "Scheduled", "status": status}}
	_ = unstructured.SetNestedSlice(volume.Object, conditions, "status", "conditions")
	return []client.Object{longhornPV(name, platform.StorageClassSingle), volume}
}

func TestAPostgresDatabaseWhoseVolumeHasNoDiskSaysSo(t *testing.T) {
	objects := slices.Concat(
		[]client.Object{claimBoundTo("orders-1", "app-prod", "pv-1", map[string]string{cnpgClusterLabel: "orders"})},
		volumeScheduled("pv-1", "False"),
	)
	c := lostNodeClient(t, &deletions{}, objects...)

	message := volumeNotPlacedMessage(context.Background(), c, "app-prod", claimsOfPostgres(context.Background(), c, "app-prod", "orders")...)

	require.Equal(t, platform.VolumeNotPlacedMessage("10Gi"), message)
	require.Empty(t, volumeNotPlacedMessage(context.Background(), c, "app-prod", claimsOfPostgres(context.Background(), c, "app-prod", "another")...),
		"only the database's own volumes are looked at")
}

func TestAVolumeLonghornPlacedOrHasNotLookedAtYetIsNotUnplaced(t *testing.T) {
	// No condition at all: a volume Longhorn has not scheduled yet is not one that it could not.
	bare := longhornVolume("pv-new", 1)
	c := lostNodeClient(t, &deletions{}, claimBoundTo("orders-1", "app-prod", "pv-1", nil), claimBoundTo("orders-2", "app-prod", "pv-new", nil),
		longhornPV("pv-new", platform.StorageClassSingle), bare)
	for _, extra := range volumeScheduled("pv-1", "True") {
		require.NoError(t, c.Create(context.Background(), extra))
	}

	var all corev1.PersistentVolumeClaimList
	require.NoError(t, c.List(context.Background(), &all))
	require.Empty(t, volumeNotPlacedMessage(context.Background(), c, "app-prod", all.Items...))
}

func TestTheClaimOfAServerIsTheOneNamedForItAndItsPod(t *testing.T) {
	c := lostNodeClient(t, &deletions{},
		claimBoundTo("data-geass-mysql-orders-0", "app-prod", "", nil),
		claimBoundTo("data-main-geass-mysql-orders-0", "app-prod", "", nil), // another set whose name ends with this one's
		claimBoundTo("data-geass-mysql-orders-0", "elsewhere", "", nil),
	)
	claim := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data"}}

	found := claimsOfServer(context.Background(), c, "app-prod", serverSpec{name: "geass-mysql-orders", claim: claim})

	require.Len(t, found, 1)
	require.Equal(t, "data-geass-mysql-orders-0", found[0].Name)
	require.Equal(t, "app-prod", found[0].Namespace)
	require.Empty(t, claimsOfServer(context.Background(), c, "app-prod", serverSpec{name: "geass-mysql-orders"}), "a server that keeps nothing on disk has no volume")
	require.Empty(t, claimsOfServer(context.Background(), c, "app-prod", serverSpec{name: "geass-mysql-absent", claim: claim}), "one whose claim is not made yet is not an error")
}
