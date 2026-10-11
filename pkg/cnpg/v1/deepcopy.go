package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (in *Cluster) DeepCopyInto(out *Cluster) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *ClusterSpec) DeepCopyInto(out *ClusterSpec) {
	*out = *in
	in.Resources.DeepCopyInto(&out.Resources)
	if in.Storage.StorageClass != nil {
		class := *in.Storage.StorageClass
		out.Storage.StorageClass = &class
	}
	in.Bootstrap.DeepCopyInto(&out.Bootstrap)
	if in.Backup != nil {
		out.Backup = in.Backup.DeepCopy()
	}
	if in.ExternalClusters != nil {
		out.ExternalClusters = make([]ExternalCluster, len(in.ExternalClusters))
		for i := range in.ExternalClusters {
			in.ExternalClusters[i].DeepCopyInto(&out.ExternalClusters[i])
		}
	}
}

func (in *ClusterStatus) DeepCopyInto(out *ClusterStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		for i := range in.Conditions {
			in.Conditions[i].DeepCopyInto(&out.Conditions[i])
		}
	}
}

func (in *BootstrapConfiguration) DeepCopyInto(out *BootstrapConfiguration) {
	*out = *in
	if in.InitDB != nil {
		initdb := *in.InitDB
		out.InitDB = &initdb
	}
	if in.Recovery != nil {
		out.Recovery = in.Recovery.DeepCopy()
	}
}

func (in *RecoveryConfiguration) DeepCopy() *RecoveryConfiguration {
	if in == nil {
		return nil
	}
	out := *in
	if in.RecoveryTarget != nil {
		out.RecoveryTarget = in.RecoveryTarget.DeepCopy()
	}
	if in.Secret != nil {
		secret := *in.Secret
		out.Secret = &secret
	}
	return &out
}

func (in *RecoveryTarget) DeepCopy() *RecoveryTarget {
	if in == nil {
		return nil
	}
	out := *in
	if in.TargetImmediate != nil {
		immediate := *in.TargetImmediate
		out.TargetImmediate = &immediate
	}
	return &out
}

func (in *BackupConfiguration) DeepCopy() *BackupConfiguration {
	if in == nil {
		return nil
	}
	out := *in
	out.BarmanObjectStore = in.BarmanObjectStore.DeepCopy()
	return &out
}

func (in *BarmanObjectStoreConfiguration) DeepCopy() *BarmanObjectStoreConfiguration {
	if in == nil {
		return nil
	}
	out := *in
	if in.S3Credentials != nil {
		out.S3Credentials = &S3Credentials{
			AccessKeyID:     copySecretKeySelector(in.S3Credentials.AccessKeyID),
			SecretAccessKey: copySecretKeySelector(in.S3Credentials.SecretAccessKey),
			Region:          copySecretKeySelector(in.S3Credentials.Region),
		}
	}
	if in.Wal != nil {
		wal := *in.Wal
		out.Wal = &wal
	}
	if in.Data != nil {
		data := *in.Data
		out.Data = &data
	}
	return &out
}

func copySecretKeySelector(in *corev1.SecretKeySelector) *corev1.SecretKeySelector {
	if in == nil {
		return nil
	}
	return in.DeepCopy()
}

func (in *ExternalCluster) DeepCopyInto(out *ExternalCluster) {
	*out = *in
	out.BarmanObjectStore = in.BarmanObjectStore.DeepCopy()
}

func (in *Cluster) DeepCopy() *Cluster {
	if in == nil {
		return nil
	}
	out := new(Cluster)
	in.DeepCopyInto(out)
	return out
}

func (in *Cluster) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *ClusterList) DeepCopyInto(out *ClusterList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]Cluster, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *ClusterList) DeepCopy() *ClusterList {
	if in == nil {
		return nil
	}
	out := new(ClusterList)
	in.DeepCopyInto(out)
	return out
}

func (in *ClusterList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *Backup) DeepCopyInto(out *Backup) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	in.Status.DeepCopyInto(&out.Status)
}

func (in *BackupStatus) DeepCopyInto(out *BackupStatus) {
	*out = *in
	if in.StartedAt != nil {
		out.StartedAt = in.StartedAt.DeepCopy()
	}
	if in.StoppedAt != nil {
		out.StoppedAt = in.StoppedAt.DeepCopy()
	}
}

func (in *Backup) DeepCopy() *Backup {
	if in == nil {
		return nil
	}
	out := new(Backup)
	in.DeepCopyInto(out)
	return out
}

func (in *Backup) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *BackupList) DeepCopyInto(out *BackupList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]Backup, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *BackupList) DeepCopy() *BackupList {
	if in == nil {
		return nil
	}
	out := new(BackupList)
	in.DeepCopyInto(out)
	return out
}

func (in *BackupList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *ScheduledBackup) DeepCopyInto(out *ScheduledBackup) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

func (in *ScheduledBackupSpec) DeepCopyInto(out *ScheduledBackupSpec) {
	*out = *in
	if in.Immediate != nil {
		immediate := *in.Immediate
		out.Immediate = &immediate
	}
	if in.Suspend != nil {
		suspend := *in.Suspend
		out.Suspend = &suspend
	}
}

func (in *ScheduledBackup) DeepCopy() *ScheduledBackup {
	if in == nil {
		return nil
	}
	out := new(ScheduledBackup)
	in.DeepCopyInto(out)
	return out
}

func (in *ScheduledBackup) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *ScheduledBackupList) DeepCopyInto(out *ScheduledBackupList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]ScheduledBackup, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *ScheduledBackupList) DeepCopy() *ScheduledBackupList {
	if in == nil {
		return nil
	}
	out := new(ScheduledBackupList)
	in.DeepCopyInto(out)
	return out
}

func (in *ScheduledBackupList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
