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

func TestTheClaimsOfAStatefulSetAreTheOnesNamedForItAndItsPods(t *testing.T) {
	c := lostNodeClient(t, &deletions{},
		claimBoundTo("data-geass-mysql-orders-0", "app-prod", "", nil),
		claimBoundTo("data-geass-mysql-orders-1", "app-prod", "", nil),
		claimBoundTo("data-geass-mysql-orders-backup-0", "app-prod", "", nil), // another set
		claimBoundTo("data-geass-mysql-orders-extra", "app-prod", "", nil),    // not a pod's number
		claimBoundTo("data-geass-mysql-orders-0", "elsewhere", "", nil),
	)

	found := claimsOfStatefulSet(context.Background(), c, "app-prod", "geass-mysql-orders")
	names := make([]string, 0, len(found))
	for _, claim := range found {
		names = append(names, claim.Name)
	}

	require.ElementsMatch(t, []string{"data-geass-mysql-orders-0", "data-geass-mysql-orders-1"}, names)
}
