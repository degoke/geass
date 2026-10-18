package v1beta2

import (
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (in *MySQLCluster) DeepCopyInto(out *MySQLCluster) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *MySQLCluster) DeepCopy() *MySQLCluster {
	if in == nil {
		return nil
	}
	out := &MySQLCluster{}
	in.DeepCopyInto(out)
	return out
}

func (in *MySQLCluster) DeepCopyObject() runtime.Object {
	if out := in.DeepCopy(); out != nil {
		return out
	}
	return nil
}

func (in *MySQLClusterList) DeepCopyInto(out *MySQLClusterList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]MySQLCluster, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *MySQLClusterList) DeepCopy() *MySQLClusterList {
	if in == nil {
		return nil
	}
	out := &MySQLClusterList{}
	in.DeepCopyInto(out)
	return out
}

func (in *MySQLClusterList) DeepCopyObject() runtime.Object {
	if out := in.DeepCopy(); out != nil {
		return out
	}
	return nil
}

func (in *MySQLClusterSpec) DeepCopyInto(out *MySQLClusterSpec) {
	*out = *in
	in.PodTemplate.DeepCopyInto(&out.PodTemplate)
	if in.VolumeClaimTemplates != nil {
		out.VolumeClaimTemplates = make([]corev1.PersistentVolumeClaim, len(in.VolumeClaimTemplates))
		for i := range in.VolumeClaimTemplates {
			in.VolumeClaimTemplates[i].DeepCopyInto(&out.VolumeClaimTemplates[i])
		}
	}
	if in.MySQLConfigMapName != nil {
		name := *in.MySQLConfigMapName
		out.MySQLConfigMapName = &name
	}
}

func (in *PodTemplateSpec) DeepCopyInto(out *PodTemplateSpec) {
	*out = *in
	out.ObjectMeta = in.deepCopy()
	in.Spec.DeepCopyInto(&out.Spec)
}

func (in ObjectMeta) deepCopy() ObjectMeta {
	return ObjectMeta{Labels: maps.Clone(in.Labels), Annotations: maps.Clone(in.Annotations)}
}

func (in *MySQLClusterStatus) DeepCopyInto(out *MySQLClusterStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = slices.Clone(in.Conditions)
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
	if in.CurrentPrimaryIndex != nil {
		index := *in.CurrentPrimaryIndex
		out.CurrentPrimaryIndex = &index
	}
	out.ErrantReplicaList = slices.Clone(in.ErrantReplicaList)
}
