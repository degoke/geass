package dashboard

import (
	"fmt"
	"strings"

	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// forObjectStore is the class the cluster's object storage server's volume uses. A server that has been placed has it
// pinned; one that has not will get what the operator picks, by the same rule: Geass's replicated class where
// Longhorn is installed, else the cluster default.
func (i storageClassIndex) forObjectStore(server *geassv1alpha1.GeassObjectStore) *storagev1.StorageClass {
	pinned, ok := server.Annotations[platform.AnnotationStorageClass]
	switch {
	case ok && pinned != "":
		if class := i.byName[pinned]; class != nil {
			return class
		}
		// The class the volume was made with is gone, so nothing says its volume can be made bigger.
		return &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: pinned}}
	case ok:
		return i.fallback
	}
	return i.newVolume()
}

// requestedObjectStorage reads the volume size asked for the cluster's object storage server. Empty means not
// asked, and returns nil. A server that has no volume yet can be given any valid size; once it has one the size can
// only grow, and only where its class can be resized, because a volume cannot be shrunk. The floor is the server's
// current size, not what a pending edit might hold.
func requestedObjectStorage(raw string, server *geassv1alpha1.GeassObjectStore, class *storagev1.StorageClass) (*resource.Quantity, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	size, err := resource.ParseQuantity(raw)
	if err != nil {
		return nil, fmt.Errorf("storage size must be a quantity such as 10Gi")
	}
	if floor := resource.MustParse(platform.MinObjectStoreServerVolumeSize); size.Cmp(floor) < 0 {
		return nil, fmt.Errorf("storage size must be at least %s", platform.MinObjectStoreServerVolumeSize)
	}
	if server != nil && server.Spec.StorageSize != nil && !server.Spec.StorageSize.IsZero() {
		current := *server.Spec.StorageSize
		switch cmp := size.Cmp(current); {
		case cmp < 0:
			return nil, fmt.Errorf("storage can be increased but not decreased (currently %s)", current.String())
		case cmp > 0 && !classAllowsExpansion(class):
			return nil, fmt.Errorf("this cluster's storage can't be resized, so the server stays at %s", current.String())
		}
	}
	return &size, nil
}
