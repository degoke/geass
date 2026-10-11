package dashboard

import (
	"context"
	"strconv"

	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Both annotations mark a default class; the beta one is still honoured by Kubernetes.
var defaultStorageClassAnnotations = []string{
	"storageclass.kubernetes.io/is-default-class",
	"storageclass.beta.kubernetes.io/is-default-class",
}

// storageClassIndex is a snapshot of the cluster's storage classes, read once so
// that judging many databases does not mean listing the classes many times.
// Every lookup answers nil when it cannot tell, and callers treat nil as
// "unknown", which never blocks anything: the cluster has the last word.
type storageClassIndex struct {
	byName   map[string]*storagev1.StorageClass
	fallback *storagev1.StorageClass
	// longhornEnabled is true when the cluster is set up to have Geass's own classes,
	// whether or not they exist yet.
	longhornEnabled bool
}

func (s *Server) storageClasses(ctx context.Context) storageClassIndex {
	index := storageClassIndex{byName: map[string]*storagev1.StorageClass{}}
	var clusters geassv1alpha1.GeassClusterList
	if err := s.Client.List(ctx, &clusters); err == nil {
		for i := range clusters.Items {
			if geassv1alpha1.LonghornEnabled(clusters.Items[i].Spec.Addons) {
				index.longhornEnabled = true
			}
		}
	}
	var classes storagev1.StorageClassList
	if err := s.Client.List(ctx, &classes); err != nil {
		return index
	}
	for i := range classes.Items {
		class := &classes.Items[i]
		index.byName[class.Name] = class
		if isDefaultStorageClass(class) && (index.fallback == nil || newerDefault(class, index.fallback)) {
			index.fallback = class
		}
	}
	return index
}

func isDefaultStorageClass(class *storagev1.StorageClass) bool {
	for _, annotation := range defaultStorageClassAnnotations {
		if isDefault, _ := strconv.ParseBool(class.Annotations[annotation]); isDefault {
			return true
		}
	}
	return false
}

// newerDefault orders two default classes the way Kubernetes resolves several of
// them: the most recently created wins. The name breaks ties. The cache lists in
// no particular order, so without this the answer would change between requests.
func newerDefault(candidate, current *storagev1.StorageClass) bool {
	if !candidate.CreationTimestamp.Equal(&current.CreationTimestamp) {
		return current.CreationTimestamp.Before(&candidate.CreationTimestamp)
	}
	return candidate.Name < current.Name
}

// expected is the Geass class with this name. When Longhorn is enabled but has not
// created it yet, it answers with what the class will be, so the dashboard does not
// describe a database as stuck on local storage that it will never use.
func (i storageClassIndex) expected(name string) *storagev1.StorageClass {
	if class, ok := i.byName[name]; ok {
		return class
	}
	if i.longhornEnabled {
		expand := platform.LonghornAllowsExpansion
		return &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: name}, AllowVolumeExpansion: &expand}
	}
	return i.fallback
}

// newVolume is the class a brand new, non-HA database will get: Geass's own class
// when Longhorn is installed, else the cluster default.
func (i storageClassIndex) newVolume() *storagev1.StorageClass {
	return i.expected(platform.StorageClassReplicated)
}

// newVolumeExpandable reports whether volumes of newly created databases can be
// resized. It answers true when that cannot be determined.
func (i storageClassIndex) newVolumeExpandable() bool {
	return classAllowsExpansion(i.newVolume())
}

// forDatabase is the class this database's volumes use. A database that has been
// provisioned has it pinned; one that has not yet will get what the operator picks,
// by the same rule the operator uses.
func (i storageClassIndex) forDatabase(db *geassv1alpha1.GeassDatabase) *storagev1.StorageClass {
	pinned, ok := db.Annotations[platform.AnnotationStorageClass]
	switch {
	case ok && pinned != "":
		return i.byName[pinned]
	case ok:
		return i.fallback
	}
	return i.expected(platform.DatabaseStorageClass(db.Spec.Engine, platform.DatabaseWantsHA(db)))
}
