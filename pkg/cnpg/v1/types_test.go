package v1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestSchemeRegistersBackupKinds(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, AddToScheme(scheme))
	for _, kind := range []string{"Cluster", "ClusterList", "Backup", "BackupList", "ScheduledBackup", "ScheduledBackupList"} {
		require.True(t, scheme.Recognizes(SchemeGroupVersion.WithKind(kind)), "kind %s is not registered", kind)
	}
}

func TestRecoveryBootstrapDoesNotSerializeInitDB(t *testing.T) {
	cluster := Cluster{Spec: ClusterSpec{Bootstrap: BootstrapConfiguration{
		Recovery: &RecoveryConfiguration{Source: "origin", Database: "app", Owner: "app"},
	}}}
	raw, err := json.Marshal(cluster)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	bootstrap := decoded["spec"].(map[string]any)["bootstrap"].(map[string]any)
	require.Contains(t, bootstrap, "recovery")
	require.NotContains(t, bootstrap, "initdb", "CNPG rejects a cluster that sets more than one bootstrap method")
}

func TestInitDBBootstrapDoesNotSerializeRecovery(t *testing.T) {
	cluster := Cluster{Spec: ClusterSpec{Bootstrap: BootstrapConfiguration{
		InitDB: &InitDBConfiguration{Database: "orders", Owner: "app"},
	}}}
	raw, err := json.Marshal(cluster)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	bootstrap := decoded["spec"].(map[string]any)["bootstrap"].(map[string]any)
	require.Contains(t, bootstrap, "initdb")
	require.NotContains(t, bootstrap, "recovery")
}

// The manifest below is the shape documented by CloudNativePG 1.25; decoding it proves our
// hand-written JSON tags match the operator's API, which a Go-only round trip cannot.
const documentedBackupCluster = `
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: orders
spec:
  instances: 1
  backup:
    retentionPolicy: 7d
    barmanObjectStore:
      destinationPath: s3://geass-backups-abc/payments
      serverName: orders-1a2b3c4d
      s3Credentials:
        accessKeyId: {name: orders-backup, key: ACCESS_KEY_ID}
        secretAccessKey: {name: orders-backup, key: ACCESS_SECRET_KEY}
        region: {name: orders-backup, key: REGION}
      wal: {compression: gzip, maxParallel: 2}
      data: {compression: gzip, jobs: 2}
  bootstrap:
    recovery:
      source: origin
      database: orders
      owner: app
      secret: {name: orders-bootstrap}
      recoveryTarget:
        backupID: "20260301T020000"
        targetImmediate: true
  externalClusters:
    - name: origin
      barmanObjectStore:
        destinationPath: s3://geass-backups-abc/payments
        serverName: orders-9f8e7d6c
        s3Credentials:
          accessKeyId: {name: orders-backup, key: ACCESS_KEY_ID}
          secretAccessKey: {name: orders-backup, key: ACCESS_SECRET_KEY}
status:
  firstRecoverabilityPoint: "2026-03-01T02:00:00Z"
  lastSuccessfulBackup: "2026-03-08T02:00:09Z"
  conditions:
    - type: ContinuousArchiving
      status: "True"
`

func TestDecodesDocumentedBackupCluster(t *testing.T) {
	var cluster Cluster
	require.NoError(t, yaml.Unmarshal([]byte(documentedBackupCluster), &cluster))

	backup := cluster.Spec.Backup
	require.NotNil(t, backup)
	require.Equal(t, "7d", backup.RetentionPolicy)
	store := backup.BarmanObjectStore
	require.NotNil(t, store)
	require.Equal(t, "s3://geass-backups-abc/payments", store.DestinationPath)
	require.Equal(t, "orders-1a2b3c4d", store.ServerName)
	require.Equal(t, "ACCESS_KEY_ID", store.S3Credentials.AccessKeyID.Key)
	require.Equal(t, "REGION", store.S3Credentials.Region.Key)
	require.Equal(t, "gzip", store.Wal.Compression)
	require.EqualValues(t, 2, store.Data.Jobs)

	recovery := cluster.Spec.Bootstrap.Recovery
	require.NotNil(t, recovery)
	require.Nil(t, cluster.Spec.Bootstrap.InitDB)
	require.Equal(t, "origin", recovery.Source)
	require.Equal(t, "orders-bootstrap", recovery.Secret.Name)
	require.Equal(t, "20260301T020000", recovery.RecoveryTarget.BackupID)
	require.True(t, *recovery.RecoveryTarget.TargetImmediate)

	require.Len(t, cluster.Spec.ExternalClusters, 1)
	require.Equal(t, "orders-9f8e7d6c", cluster.Spec.ExternalClusters[0].BarmanObjectStore.ServerName)

	require.Equal(t, "2026-03-01T02:00:00Z", cluster.Status.FirstRecoverabilityPoint)
	require.Equal(t, "2026-03-08T02:00:09Z", cluster.Status.LastSuccessfulBackup)
	require.Equal(t, "ContinuousArchiving", cluster.Status.Conditions[0].Type)
}

func TestClusterDeepCopyDoesNotShareBackupState(t *testing.T) {
	var original Cluster
	require.NoError(t, yaml.Unmarshal([]byte(documentedBackupCluster), &original))
	copied := original.DeepCopy()

	copied.Spec.Backup.RetentionPolicy = "30d"
	copied.Spec.Backup.BarmanObjectStore.DestinationPath = "s3://other"
	copied.Spec.Backup.BarmanObjectStore.S3Credentials.AccessKeyID.Key = "changed"
	copied.Spec.Backup.BarmanObjectStore.Wal.Compression = "none"
	copied.Spec.Backup.BarmanObjectStore.Data.Jobs = 9
	copied.Spec.Bootstrap.Recovery.Source = "changed"
	copied.Spec.Bootstrap.Recovery.Secret.Name = "changed"
	*copied.Spec.Bootstrap.Recovery.RecoveryTarget.TargetImmediate = false
	copied.Spec.ExternalClusters[0].BarmanObjectStore.ServerName = "changed"
	copied.Status.Conditions[0].Message = "changed"

	var pristine Cluster
	require.NoError(t, yaml.Unmarshal([]byte(documentedBackupCluster), &pristine))
	require.Equal(t, pristine.Spec, original.Spec)
	require.Equal(t, pristine.Status, original.Status)
}

func TestClusterDeepCopyDoesNotShareInitDB(t *testing.T) {
	original := &Cluster{Spec: ClusterSpec{Bootstrap: BootstrapConfiguration{
		InitDB: &InitDBConfiguration{Database: "orders", Owner: "app", Secret: corev1.LocalObjectReference{Name: "orders-bootstrap"}},
	}}}
	copied := original.DeepCopy()
	copied.Spec.Bootstrap.InitDB.Database = "changed"
	require.Equal(t, "orders", original.Spec.Bootstrap.InitDB.Database)
}

func TestBackupAndScheduledBackupDeepCopy(t *testing.T) {
	started, stopped := metav1.Now(), metav1.Now()
	backup := &Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-final"},
		Spec:       BackupSpec{Cluster: corev1.LocalObjectReference{Name: "orders"}},
		Status:     BackupStatus{Phase: BackupPhaseCompleted, BackupID: "20260301T020000", StartedAt: &started, StoppedAt: &stopped},
	}
	copiedBackup := backup.DeepCopy()
	copiedBackup.Status.StoppedAt.Time = copiedBackup.Status.StoppedAt.Add(3600)
	copiedBackup.Spec.Cluster.Name = "changed"
	require.Equal(t, "orders", backup.Spec.Cluster.Name)
	require.True(t, backup.Status.StoppedAt.Equal(&stopped))
	require.NotSame(t, backup.Status.StoppedAt, copiedBackup.Status.StoppedAt)

	immediate := true
	scheduled := &ScheduledBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "orders"},
		Spec:       ScheduledBackupSpec{Schedule: "0 0 2 * * *", Cluster: corev1.LocalObjectReference{Name: "orders"}, Immediate: &immediate},
	}
	copiedScheduled := scheduled.DeepCopy()
	*copiedScheduled.Spec.Immediate = false
	require.True(t, *scheduled.Spec.Immediate)

	list := &BackupList{Items: []Backup{*backup}}
	copiedList := list.DeepCopy()
	copiedList.Items[0].Status.BackupID = "changed"
	require.Equal(t, "20260301T020000", list.Items[0].Status.BackupID)

	scheduledList := &ScheduledBackupList{Items: []ScheduledBackup{*scheduled}}
	copiedScheduledList := scheduledList.DeepCopy()
	copiedScheduledList.Items[0].Spec.Schedule = "changed"
	require.Equal(t, "0 0 2 * * *", scheduledList.Items[0].Spec.Schedule)

	_, isObject := runtime.Object(backup).(interface{ DeepCopyObject() runtime.Object })
	require.True(t, isObject)
	require.NotNil(t, scheduled.DeepCopyObject())
}
