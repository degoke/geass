package v1alpha1

import (
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (in *MariaDB) DeepCopyInto(out *MariaDB) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *MariaDB) DeepCopy() *MariaDB {
	if in == nil {
		return nil
	}
	out := &MariaDB{}
	in.DeepCopyInto(out)
	return out
}

func (in *MariaDB) DeepCopyObject() runtime.Object {
	if out := in.DeepCopy(); out != nil {
		return out
	}
	return nil
}

func (in *MariaDBList) DeepCopyInto(out *MariaDBList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]MariaDB, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *MariaDBList) DeepCopy() *MariaDBList {
	if in == nil {
		return nil
	}
	out := &MariaDBList{}
	in.DeepCopyInto(out)
	return out
}

func (in *MariaDBList) DeepCopyObject() runtime.Object {
	if out := in.DeepCopy(); out != nil {
		return out
	}
	return nil
}

func ptrCopy[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func (in *MariaDBSpec) DeepCopyInto(out *MariaDBSpec) {
	*out = *in
	out.Database = ptrCopy(in.Database)
	out.Username = ptrCopy(in.Username)
	out.PasswordSecretKeyRef = ptrCopy(in.PasswordSecretKeyRef)
	out.MyCnf = ptrCopy(in.MyCnf)
	out.StartupProbe = ptrCopy(in.StartupProbe)
	if in.Storage.Size != nil {
		size := in.Storage.Size.DeepCopy()
		out.Storage.Size = &size
	}
	out.Storage.StorageClassName = ptrCopy(in.Storage.StorageClassName)
	if in.Resources != nil {
		out.Resources = in.Resources.DeepCopy()
	}
	out.NodeSelector = maps.Clone(in.NodeSelector)
	out.Tolerations = copyTolerations(in.Tolerations)
	if in.Affinity != nil {
		affinity := &Affinity{}
		if in.Affinity.NodeAffinity != nil {
			affinity.NodeAffinity = in.Affinity.NodeAffinity.DeepCopy()
		}
		if in.Affinity.PodAntiAffinity != nil {
			affinity.PodAntiAffinity = in.Affinity.PodAntiAffinity.DeepCopy()
		}
		out.Affinity = affinity
	}
	if in.Replication != nil {
		replication := &Replication{Enabled: in.Replication.Enabled}
		if p := in.Replication.Primary; p != nil {
			replication.Primary = &PrimaryReplication{PodIndex: ptrCopy(p.PodIndex), AutoFailover: ptrCopy(p.AutoFailover)}
		}
		if r := in.Replication.Replica; r != nil {
			replica := &ReplicaReplication{}
			if r.BootstrapFrom != nil {
				replica.BootstrapFrom = ptrCopy(r.BootstrapFrom)
			}
			replica.Recovery = ptrCopy(r.Recovery)
			replication.Replica = replica
		}
		out.Replication = replication
	}
}

func copyTolerations(in []corev1.Toleration) []corev1.Toleration {
	if in == nil {
		return nil
	}
	out := make([]corev1.Toleration, len(in))
	for i := range in {
		in[i].DeepCopyInto(&out[i])
	}
	return out
}

func (in *MariaDBStatus) DeepCopyInto(out *MariaDBStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
	out.CurrentPrimary = ptrCopy(in.CurrentPrimary)
	if in.CurrentPrimaryFailingSince != nil {
		out.CurrentPrimaryFailingSince = in.CurrentPrimaryFailingSince.DeepCopy()
	}
	if in.Replication != nil {
		replication := &ReplicationStatus{Roles: maps.Clone(in.Replication.Roles)}
		if in.Replication.Replicas != nil {
			replication.Replicas = make(map[string]ReplicaStatus, len(in.Replication.Replicas))
			for name, status := range in.Replication.Replicas {
				status.SlaveIORunning = ptrCopy(status.SlaveIORunning)
				status.SlaveSQLRunning = ptrCopy(status.SlaveSQLRunning)
				replication.Replicas[name] = status
			}
		}
		out.Replication = replication
	}
}

func (in *PhysicalBackup) DeepCopyInto(out *PhysicalBackup) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

func (in *PhysicalBackup) DeepCopy() *PhysicalBackup {
	if in == nil {
		return nil
	}
	out := &PhysicalBackup{}
	in.DeepCopyInto(out)
	return out
}

func (in *PhysicalBackup) DeepCopyObject() runtime.Object {
	if out := in.DeepCopy(); out != nil {
		return out
	}
	return nil
}

func (in *PhysicalBackupList) DeepCopyInto(out *PhysicalBackupList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]PhysicalBackup, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *PhysicalBackupList) DeepCopy() *PhysicalBackupList {
	if in == nil {
		return nil
	}
	out := &PhysicalBackupList{}
	in.DeepCopyInto(out)
	return out
}

func (in *PhysicalBackupList) DeepCopyObject() runtime.Object {
	if out := in.DeepCopy(); out != nil {
		return out
	}
	return nil
}

func (in *PhysicalBackupSpec) DeepCopyInto(out *PhysicalBackupSpec) {
	*out = *in
	out.MariaDBRef.WaitForIt = ptrCopy(in.MariaDBRef.WaitForIt)
	out.Schedule = ptrCopy(in.Schedule)
	out.PodAffinity = ptrCopy(in.PodAffinity)
	out.Tolerations = copyTolerations(in.Tolerations)
	if claim := in.Storage.PersistentVolumeClaim; claim != nil {
		copied := &PersistentVolumeClaimStorage{
			AccessModes:      slices.Clone(claim.AccessModes),
			StorageClassName: ptrCopy(claim.StorageClassName),
		}
		claim.Resources.DeepCopyInto(&copied.Resources)
		out.Storage.PersistentVolumeClaim = copied
	}
}
