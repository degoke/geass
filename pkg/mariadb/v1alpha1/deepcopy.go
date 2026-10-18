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
	out.PointInTimeRecoveryRef = ptrCopy(in.PointInTimeRecoveryRef)
	out.BootstrapFrom = in.BootstrapFrom.deepCopy()
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

func (in *BootstrapFrom) deepCopy() *BootstrapFrom {
	if in == nil {
		return nil
	}
	out := &BootstrapFrom{PointInTimeRecoveryRef: ptrCopy(in.PointInTimeRecoveryRef)}
	if in.TargetRecoveryTime != nil {
		out.TargetRecoveryTime = in.TargetRecoveryTime.DeepCopy()
	}
	out.StagingStorage = in.StagingStorage.deepCopy()
	return out
}

func (in *StagingStorage) deepCopy() *StagingStorage {
	if in == nil {
		return nil
	}
	return &StagingStorage{PersistentVolumeClaim: in.PersistentVolumeClaim.deepCopy()}
}

func (in *PersistentVolumeClaimStorage) deepCopy() *PersistentVolumeClaimStorage {
	if in == nil {
		return nil
	}
	copied := &PersistentVolumeClaimStorage{AccessModes: slices.Clone(in.AccessModes), StorageClassName: ptrCopy(in.StorageClassName)}
	in.Resources.DeepCopyInto(&copied.Resources)
	return copied
}

func (in *S3) deepCopy() *S3 {
	if in == nil {
		return nil
	}
	out := *in
	out.TLS = ptrCopy(in.TLS)
	return &out
}

func (in *PhysicalBackupStorage) deepCopy() PhysicalBackupStorage {
	return PhysicalBackupStorage{PersistentVolumeClaim: in.PersistentVolumeClaim.deepCopy(), S3: in.S3.deepCopy()}
}

func (in *PhysicalBackup) DeepCopyInto(out *PhysicalBackup) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		for i := range in.Status.Conditions {
			in.Status.Conditions[i].DeepCopyInto(&out.Status.Conditions[i])
		}
	}
	if in.Status.LastScheduleTime != nil {
		out.Status.LastScheduleTime = in.Status.LastScheduleTime.DeepCopy()
	}
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
	if in.Schedule != nil {
		schedule := *in.Schedule
		schedule.Immediate = ptrCopy(in.Schedule.Immediate)
		out.Schedule = &schedule
	}
	out.PodAffinity = ptrCopy(in.PodAffinity)
	out.Tolerations = copyTolerations(in.Tolerations)
	out.Storage = in.Storage.deepCopy()
	if in.MaxRetention != nil {
		out.MaxRetention = in.MaxRetention.DeepCopy()
	}
	out.StagingStorage = in.StagingStorage.deepCopy()
}

func (in *PointInTimeRecovery) DeepCopyInto(out *PointInTimeRecovery) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec.Storage = in.Spec.Storage.deepCopy()
	out.Spec.StrictMode = ptrCopy(in.Spec.StrictMode)
	if in.Status.LastRecoverableTime != nil {
		out.Status.LastRecoverableTime = in.Status.LastRecoverableTime.DeepCopy()
	}
}

func (in *PointInTimeRecovery) DeepCopy() *PointInTimeRecovery {
	if in == nil {
		return nil
	}
	out := &PointInTimeRecovery{}
	in.DeepCopyInto(out)
	return out
}

func (in *PointInTimeRecovery) DeepCopyObject() runtime.Object {
	if out := in.DeepCopy(); out != nil {
		return out
	}
	return nil
}

func (in *PointInTimeRecoveryList) DeepCopyInto(out *PointInTimeRecoveryList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]PointInTimeRecovery, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *PointInTimeRecoveryList) DeepCopy() *PointInTimeRecoveryList {
	if in == nil {
		return nil
	}
	out := &PointInTimeRecoveryList{}
	in.DeepCopyInto(out)
	return out
}

func (in *PointInTimeRecoveryList) DeepCopyObject() runtime.Object {
	if out := in.DeepCopy(); out != nil {
		return out
	}
	return nil
}
