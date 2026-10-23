package controller

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

// longhornConditionScheduled is the volume condition that says whether Longhorn found disks for the copies of the volume.
const longhornConditionScheduled = "Scheduled"

// volumeNotPlacedMessage says why a database that is not ready is not, when it is that Longhorn has no disk to put its
// volume on. A pod that waits for such a volume is ContainerCreating, and says nothing a user reads: the database said
// "Service is starting" for as long as the disks were full. The answer is "" when no volume of the claims is unplaced.
// Claims are looked for by name, and the claims of a database that has none yet are not an error.
func volumeNotPlacedMessage(ctx context.Context, c client.Reader, namespace string, claims ...corev1.PersistentVolumeClaim) string {
	for i := range claims {
		claim := &claims[i]
		if claim.Spec.VolumeName == "" {
			continue
		}
		var pv corev1.PersistentVolume
		if err := c.Get(ctx, client.ObjectKey{Name: claim.Spec.VolumeName}, &pv); err != nil ||
			pv.Spec.CSI == nil || pv.Spec.CSI.Driver != platform.LonghornProvisioner {
			continue
		}
		volume := &unstructured.Unstructured{}
		volume.SetGroupVersionKind(longhornVolumeKind)
		if err := c.Get(ctx, client.ObjectKey{Namespace: platform.LonghornTargetNS, Name: pv.Spec.CSI.VolumeHandle}, volume); err != nil {
			continue
		}
		if !longhornVolumeUnplaced(volume) {
			continue
		}
		size := ""
		if requested, ok := claim.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			size = requested.String()
		}
		return platform.VolumeNotPlacedMessage(size)
	}
	return ""
}

// longhornVolumeUnplaced says that Longhorn could not find disks for the copies of a volume: its Scheduled condition is
// false. A volume whose copies are all on disks is Scheduled, and so is one that has no condition yet.
func longhornVolumeUnplaced(volume *unstructured.Unstructured) bool {
	conditions, _, _ := unstructured.NestedSlice(volume.Object, "status", "conditions")
	for _, entry := range conditions {
		condition, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := condition["type"].(string)
		status, _ := condition["status"].(string)
		if strings.EqualFold(kind, longhornConditionScheduled) && strings.EqualFold(status, "False") {
			return true
		}
	}
	return false
}

// claimsOfStatefulSet are the volumes a StatefulSet made for its pods, which are named after the set and the pod's number
// (data-mysql-0), and carry no label of their own to find them by.
func claimsOfStatefulSet(ctx context.Context, c client.Reader, namespace, set string) []corev1.PersistentVolumeClaim {
	var all corev1.PersistentVolumeClaimList
	if err := c.List(ctx, &all, client.InNamespace(namespace)); err != nil {
		return nil
	}
	var claims []corev1.PersistentVolumeClaim
	for i := range all.Items {
		name := all.Items[i].Name
		at := strings.LastIndex(name, "-"+set+"-")
		if at < 0 {
			continue
		}
		if ordinal := name[at+len(set)+2:]; ordinal != "" && strings.Trim(ordinal, "0123456789") == "" {
			claims = append(claims, all.Items[i])
		}
	}
	return claims
}

// claimsOfPostgres are the volumes of a Postgres database's instances, which CloudNativePG labels with the database.
func claimsOfPostgres(ctx context.Context, c client.Reader, namespace, database string) []corev1.PersistentVolumeClaim {
	var claims corev1.PersistentVolumeClaimList
	if err := c.List(ctx, &claims, client.InNamespace(namespace), client.MatchingLabels{cnpgClusterLabel: database}); err != nil {
		return nil
	}
	return claims.Items
}
