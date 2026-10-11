package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/platform"
)

func TestPlanBackupsReturnsNothingWhenBackupsAreOff(t *testing.T) {
	for name, backup := range map[string]*geassv1alpha1.GeassDatabaseBackupSpec{
		"unset":    nil,
		"disabled": {Enabled: false},
	} {
		t.Run(name, func(t *testing.T) {
			env := newBackupEnv(t, func(c *backupEnvConfig) { c.backup = backup })
			plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
			require.NoError(t, err)
			require.Nil(t, plan)
			require.Empty(t, env.archives())
			require.Empty(t, env.storage.constructed, "no AWS call may be made for a database without backups")
		})
	}
}

func TestPlanBackupsIgnoresDatabasesWithNoBackupMechanism(t *testing.T) {
	for name, mutate := range map[string]func(*geassv1alpha1.GeassDatabase){
		"redis (a cache)": func(db *geassv1alpha1.GeassDatabase) { db.Spec.Engine = geassv1alpha1.DatabaseEngineRedis },
		"external":        func(db *geassv1alpha1.GeassDatabase) { db.Spec.Placement = geassv1alpha1.DatabasePlacementExternal },
		"external mysql": func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.Engine, db.Spec.Placement = geassv1alpha1.DatabaseEngineMySQL, geassv1alpha1.DatabasePlacementExternal
		},
		"external sqlite?": func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.Engine, db.Spec.Placement = geassv1alpha1.DatabaseEngineSQLite, geassv1alpha1.DatabasePlacementExternal
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := newBackupEnv(t, func(c *backupEnvConfig) { c.dbMutations = append(c.dbMutations, mutate) })
			plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
			require.NoError(t, err)
			require.Nil(t, plan, "Geass has no backup mechanism for this database")
		})
	}
}

func TestPlanBackupsWithoutProjectStorageNeedsStorage(t *testing.T) {
	env := newBackupEnv(t, func(c *backupEnvConfig) { c.storage = nil })
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.Nil(t, plan.archive, "without storage the database runs unprotected rather than failing to start")
	require.False(t, plan.health.StorageReady)
	require.Empty(t, plan.health.StorageError, "a missing choice is not an error")
	require.Empty(t, env.archives())
	require.Empty(t, env.storage.constructed)
}

func TestPlanBackupsProvisionsStorageArchiveAndCredentials(t *testing.T) {
	env := newBackupEnv(t)
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.NotNil(t, plan.archive)
	require.True(t, plan.health.StorageReady)

	// The bucket and a bucket-scoped IAM user are created with the project's own AWS credentials.
	require.Equal(t, []string{testBackupBucket}, env.storage.ensuredBuckets)
	require.Equal(t, []fakeClientCreds{{"AKIAROOT", "root-secret", testBackupRegion}}, env.storage.constructed)
	require.Len(t, env.storage.userRequests, 1)
	require.Equal(t, []string{testBackupBucket}, env.storage.userRequests[0].buckets)
	require.Equal(t, platform.BackupStorageUserName(testBackupBucket), env.storage.userRequests[0].user)

	// The scoped credentials are kept once per bucket and copied into the database's namespace.
	system := env.secret(platform.SystemNamespace, platform.BackupStorageSecretName(testBackupBucket))
	require.Equal(t, "AKIABACKUPUSER", string(system.Data[platform.BackupSecretKeyAccessKeyID]))
	require.Equal(t, testBackupRegion, string(system.Data[platform.BackupSecretKeyRegion]))
	require.NotContains(t, string(system.Data[platform.BackupSecretKeyAccessKeyID]), "AKIAROOT", "root AWS keys must never reach a database namespace")

	workspace := env.secret(env.wsNS, plan.secretName)
	require.Equal(t, testBackupSecretOrd, plan.secretName)
	require.Equal(t, "AKIABACKUPUSER", string(workspace.Data[platform.BackupSecretKeyAccessKeyID]))
	require.Equal(t, "backup-user-secret", string(workspace.Data[platform.BackupSecretKeySecretAccessKey]))
	require.Equal(t, testBackupRegion, string(workspace.Data[platform.BackupSecretKeyRegion]))

	archive := env.archive(testDBServerName)
	require.Equal(t, testDBServerName, plan.archive.Name)
	require.Equal(t, geassv1alpha1.GeassBackupArchiveSpec{
		Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev,
		Database: testDBName, DatabaseUID: string(testDBUID), ServerName: testDBServerName,
		Storage:       geassv1alpha1.GeassBackupStorage{ConnectionRef: corev1.LocalObjectReference{Name: testAWSConnection}, Bucket: testBackupBucket, Region: testBackupRegion},
		RetentionDays: platform.DefaultBackupRetentionDays,
		Source: geassv1alpha1.GeassBackupSource{
			Engine:  geassv1alpha1.DatabaseEnginePostgres,
			Version: "16", DatabaseName: testDBName,
			StorageSize: resourceQuantityPtr("20Gi"), Resources: platform.DefaultDatabaseResources(),
		},
	}, archive.Spec)
	require.Equal(t, "morning-beach", archive.Annotations[platform.DisplayNameAnnotation], "a deleted database must still be recognisable in the dashboard")
	require.Nil(t, archive.Spec.DeleteAfter)
}

func TestPlanBackupsDoesNotCallAWSAgainOnceStorageIsProvisioned(t *testing.T) {
	env := newBackupEnv(t)
	for range 3 {
		plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
		require.NoError(t, err)
		require.NotNil(t, plan.archive)
	}
	require.Len(t, env.storage.userRequests, 1, "each extra IAM access key would eventually hit AWS's two-key limit")
	require.Equal(t, []string{testBackupBucket}, env.storage.ensuredBuckets)
	require.Len(t, env.archives(), 1)
}

func TestPlanBackupsProvisionsSeparateCredentialsForEachBucket(t *testing.T) {
	env := newBackupEnv(t)
	first, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	env.retire(first.archive, time.Now().Add(24*time.Hour))

	var project geassv1alpha1.GeassProject
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &project))
	project.Spec.BackupStorage.Bucket = "geass-backups-other"
	require.NoError(t, env.client.Update(context.Background(), &project))

	second, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.Equal(t, "geass-backups-other", second.archive.Spec.Storage.Bucket, "a new archive follows the project's current choice")
	require.Equal(t, []string{testBackupBucket, "geass-backups-other"}, env.storage.ensuredBuckets)
	require.Len(t, env.storage.userRequests, 2)
	require.Equal(t, []string{"geass-backups-other"}, env.storage.userRequests[1].buckets)
	require.Empty(t, env.storage.userRequests[1].existingAccess, "each bucket gets its own scoped user; the first bucket's keys are never reused")
	require.True(t, env.exists(&corev1.Secret{}, platform.SystemNamespace, platform.BackupStorageSecretName(testBackupBucket)), "the old bucket's credentials stay for its retired archive")
	require.True(t, env.exists(&corev1.Secret{}, platform.SystemNamespace, platform.BackupStorageSecretName("geass-backups-other")))
}

func TestPlanBackupsKeepsArchivingWhenStorageCannotBeVerifiedAgain(t *testing.T) {
	env := newBackupEnv(t)
	first, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NoError(t, env.client.Delete(context.Background(), &geassv1alpha1.GeassCloudConnection{ObjectMeta: metav1.ObjectMeta{Name: testAWSConnection, Namespace: platform.SystemNamespace}}))

	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NotNil(t, plan.archive, "a database that is already being backed up keeps its archive through a credentials problem")
	require.Equal(t, first.archive.Name, plan.archive.Name)
	require.False(t, plan.health.StorageReady)
	require.NotEmpty(t, plan.health.StorageError, "but the dashboard says something is wrong")
}

func TestSummarizeStorageError(t *testing.T) {
	require.Equal(t, "AccessDenied: User is not authorized", summarizeStorageError(errString(`AWS S3 403 Forbidden: <?xml version="1.0"?><Error><Code>AccessDenied</Code><Message>User is not authorized</Message><RequestId>ABC</RequestId></Error>`)))
	require.Equal(t, "AccessDenied", summarizeStorageError(errString(`<Error><Code>AccessDenied</Code></Error>`)))
	require.Equal(t, "plain failure on two lines", summarizeStorageError(errString("plain failure\n  on two   lines")))
	require.Len(t, []rune(summarizeStorageError(errString(strings.Repeat("x", 500)))), 201, "long text is cut so a dashboard banner stays readable")
	require.Empty(t, summarizeStorageError(nil))
}

func TestPlanBackupsKeepsTheArchiveStorageWhenTheProjectMovesOn(t *testing.T) {
	env := newBackupEnv(t)
	_, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	var project geassv1alpha1.GeassProject
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &project))
	project.Spec.BackupStorage.Bucket = "geass-backups-other"
	require.NoError(t, env.client.Update(context.Background(), &project))

	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.Equal(t, testBackupBucket, plan.archive.Spec.Storage.Bucket, "a live archive keeps writing where its history lives")
	require.Len(t, env.archives(), 1)
}

func TestPlanBackupsAppliesRetentionChangesToTheLiveArchive(t *testing.T) {
	env := newBackupEnv(t)
	_, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	env.db.Spec.Backup.RetentionDays = 30
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.EqualValues(t, 30, plan.archive.Spec.RetentionDays)
	require.EqualValues(t, 30, env.archive(testDBServerName).Spec.RetentionDays)
}

func TestPlanBackupsStartsANewArchiveAfterTheOldOneIsRetired(t *testing.T) {
	env := newBackupEnv(t)
	first, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	retired := env.archive(first.archive.Name)
	retired.Spec.DeleteAfter = timePtr(metav1.NewTime(time.Now().Add(24 * time.Hour)))
	require.NoError(t, env.client.Update(context.Background(), retired))

	second, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NotEqual(t, first.archive.Name, second.archive.Name, "re-enabled backups must not write into the retired archive")
	require.NotEqual(t, first.archive.Spec.ServerName, second.archive.Spec.ServerName)
	require.True(t, strings.HasPrefix(second.archive.Spec.ServerName, testDBServerName), "the new folder stays recognisable: %s", second.archive.Spec.ServerName)
	require.NotNil(t, env.archive(first.archive.Name).Spec.DeleteAfter, "the retired archive stays retired")
	require.Nil(t, env.archive(second.archive.Name).Spec.DeleteAfter)
	require.Len(t, env.archives(), 2)

	third, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.Equal(t, second.archive.Name, third.archive.Name, "the live archive is reused, not replaced on every reconcile")
}

func TestPlanBackupsStorageFailureDoesNotBlockTheDatabase(t *testing.T) {
	env := newBackupEnv(t)
	env.storage.ensureUser = errString("AWS IAM AccessDenied: not authorized to iam:CreateUser")
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err, "a storage problem must degrade to an unprotected database, not a failed one")
	require.NotNil(t, plan)
	require.Nil(t, plan.archive)
	require.False(t, plan.health.StorageReady)
	require.Contains(t, plan.health.StorageError, "AccessDenied")
	require.Empty(t, env.archives())
}

func TestPlanBackupsRequiresAnAWSConnection(t *testing.T) {
	env := newBackupEnv(t, func(c *backupEnvConfig) { c.provider = geassv1alpha1.CloudProviderPlanetScale })
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.Nil(t, plan.archive)
	require.Contains(t, plan.health.StorageError, "AWS")
	require.Empty(t, env.storage.constructed)
}

func TestPlanBackupsReportsAMissingConnection(t *testing.T) {
	env := newBackupEnv(t, func(c *backupEnvConfig) { c.noConnection = true })
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.Nil(t, plan.archive)
	require.NotEmpty(t, plan.health.StorageError)
}

func TestDesiredClusterSpecWithBackups(t *testing.T) {
	env := newBackupEnv(t)
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	spec := desiredClusterSpec(env.db, plan, nil)
	require.NotNil(t, spec.Backup)
	require.Equal(t, "7d", spec.Backup.RetentionPolicy)
	store := spec.Backup.BarmanObjectStore
	require.Equal(t, "s3://"+testBackupBucket+"/"+testProjectName, store.DestinationPath)
	require.Equal(t, testDBServerName, store.ServerName)
	require.Equal(t, "gzip", store.Wal.Compression)
	require.Equal(t, "gzip", store.Data.Compression)
	require.Equal(t, testBackupSecretOrd, store.S3Credentials.AccessKeyID.Name)
	require.Equal(t, platform.BackupSecretKeyAccessKeyID, store.S3Credentials.AccessKeyID.Key)
	require.Equal(t, testBackupSecretOrd, store.S3Credentials.SecretAccessKey.Name)
	require.Equal(t, platform.BackupSecretKeySecretAccessKey, store.S3Credentials.SecretAccessKey.Key)
	require.Equal(t, platform.BackupSecretKeyRegion, store.S3Credentials.Region.Key)
	require.Empty(t, store.EndpointURL, "AWS S3 needs no custom endpoint")

	require.NotNil(t, spec.Bootstrap.InitDB)
	require.Nil(t, spec.Bootstrap.Recovery)
	require.Equal(t, testDBName, spec.Bootstrap.InitDB.Database)
	require.Equal(t, "orders-bootstrap", spec.Bootstrap.InitDB.Secret.Name)
}

func TestDesiredClusterSpecWithoutBackupsHasNoBackupSection(t *testing.T) {
	env := newBackupEnv(t, func(c *backupEnvConfig) { c.backup = nil })
	spec := desiredClusterSpec(env.db, nil, nil)
	require.Nil(t, spec.Backup)
	require.NotNil(t, spec.Bootstrap.InitDB)
}

func TestDesiredClusterSpecOmitsBackupsUntilStorageIsReady(t *testing.T) {
	env := newBackupEnv(t, func(c *backupEnvConfig) { c.storage = nil })
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.Nil(t, desiredClusterSpec(env.db, plan, nil).Backup)
}

// --- recovery ---------------------------------------------------------------------------------

func recoveryEnv(t *testing.T, mutate ...func(*backupEnvConfig)) (*backupEnv, *geassv1alpha1.GeassBackupArchive) {
	t.Helper()
	windowStart := metav1.NewTime(time.Now().Add(-72 * time.Hour))
	windowEnd := metav1.NewTime(time.Now().Add(-10 * time.Minute))
	storageSize := resource.MustParse("20Gi")
	source := &geassv1alpha1.GeassBackupArchive{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-old-9f8e7d6c", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassBackupArchiveSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev,
			Database: "payments-old", DatabaseUID: "9f8e7d6c-0000-4000-8000-000000000002", ServerName: "payments-old-9f8e7d6c",
			Storage:       *defaultTestBackupStorage(),
			RetentionDays: 7,
			Source:        geassv1alpha1.GeassBackupSource{Version: "16", DatabaseName: "legacy", StorageSize: &storageSize},
		},
		Status: geassv1alpha1.GeassBackupArchiveStatus{
			RecoveryWindowStart: &windowStart, RecoveryWindowEnd: &windowEnd,
			RestorePoints: []geassv1alpha1.GeassRestorePoint{{ID: "20260301T020000", Time: windowEnd}},
		},
	}
	all := append([]func(*backupEnvConfig){func(c *backupEnvConfig) {
		c.backup = nil
		c.extraObjects = append(c.extraObjects, source)
		c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) {
			db.Spec.DatabaseName = "legacy"
			db.Spec.RestoreFrom = &geassv1alpha1.GeassDatabaseRestoreSpec{Archive: source.Name}
		})
	}}, mutate...)
	return newBackupEnv(t, all...), source
}

func TestPlanRecoveryIsNilForADatabaseThatIsNotARestore(t *testing.T) {
	env := newBackupEnv(t)
	plan, err := env.r.planRecovery(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.Nil(t, plan)
}

func TestPlanRecoveryReadsTheSourceArchiveAndCopiesItsCredentials(t *testing.T) {
	env, source := recoveryEnv(t)
	plan, err := env.r.planRecovery(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NotNil(t, plan)

	require.Equal(t, "origin", plan.bootstrap.Source)
	require.Equal(t, plan.bootstrap.Source, plan.external.Name)
	require.Equal(t, "legacy", plan.bootstrap.Database, "the restored data lives in the source's database name, not the new resource name")
	require.Equal(t, appContainerName, plan.bootstrap.Owner)
	require.Equal(t, "orders-bootstrap", plan.bootstrap.Secret.Name)
	require.Nil(t, plan.bootstrap.RecoveryTarget, "no choice means the latest recoverable moment")

	store := plan.external.BarmanObjectStore
	require.Equal(t, "s3://"+testBackupBucket+"/"+testProjectName, store.DestinationPath)
	require.Equal(t, source.Spec.ServerName, store.ServerName, "recovery reads the source's folder")
	require.Equal(t, "orders-restore-credentials", store.S3Credentials.AccessKeyID.Name)

	restore := env.secret(env.wsNS, "orders-restore-credentials")
	require.Equal(t, "AKIABACKUPUSER", string(restore.Data[platform.BackupSecretKeyAccessKeyID]))
}

func TestPlanRecoveryTargets(t *testing.T) {
	stoppedAt := metav1.NewTime(time.Now().Add(-10 * time.Minute).Truncate(time.Second))
	tests := map[string]struct {
		restore func(*geassv1alpha1.GeassDatabaseRestoreSpec)
		check   func(*testing.T, *cnpgv1.RecoveryTarget)
	}{
		"restore point": {
			restore: func(r *geassv1alpha1.GeassDatabaseRestoreSpec) { r.RestorePoint = "20260301T020000" },
			check: func(t *testing.T, target *cnpgv1.RecoveryTarget) {
				require.Equal(t, "20260301T020000", target.BackupID)
				require.NotNil(t, target.TargetImmediate)
				require.True(t, *target.TargetImmediate, "a restore point means the state at the end of that backup")
				require.Empty(t, target.TargetTime)
			},
		},
		"exact time": {
			restore: func(r *geassv1alpha1.GeassDatabaseRestoreSpec) { r.Time = timePtr(stoppedAt) },
			check: func(t *testing.T, target *cnpgv1.RecoveryTarget) {
				require.Equal(t, platform.RestoreTargetTime(stoppedAt.Time), target.TargetTime)
				require.Nil(t, target.TargetImmediate)
				require.Empty(t, target.BackupID)
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			env, _ := recoveryEnv(t)
			tt.restore(env.db.Spec.RestoreFrom)
			plan, err := env.r.planRecovery(context.Background(), env.db, env.wsNS)
			require.NoError(t, err)
			tt.check(t, plan.bootstrap.RecoveryTarget)
		})
	}
}

func TestPlanRecoveryRejectsUnsafeRestores(t *testing.T) {
	beforeWindow := metav1.NewTime(time.Now().Add(-30 * 24 * time.Hour))
	afterWindow := metav1.NewTime(time.Now().Add(time.Hour))
	tests := map[string]struct {
		mutate  func(*backupEnvConfig)
		restore func(*geassv1alpha1.GeassDatabaseRestoreSpec)
		want    string
	}{
		"archive is gone": {
			restore: func(r *geassv1alpha1.GeassDatabaseRestoreSpec) { r.Archive = "purged-archive" },
			want:    "backups are no longer available",
		},
		"unknown restore point": {
			restore: func(r *geassv1alpha1.GeassDatabaseRestoreSpec) { r.RestorePoint = "20990101T000000" },
			want:    "restore point",
		},
		"time before the recovery window": {
			restore: func(r *geassv1alpha1.GeassDatabaseRestoreSpec) { r.Time = timePtr(beforeWindow) },
			want:    "earlier than the oldest",
		},
		"time after the recovery window": {
			restore: func(r *geassv1alpha1.GeassDatabaseRestoreSpec) { r.Time = timePtr(afterWindow) },
			want:    "later than the newest",
		},
		"different postgres version": {
			mutate: func(c *backupEnvConfig) {
				c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) { db.Spec.Version = "17" })
			},
			want: "Postgres version 16",
		},
		"different database name": {
			mutate: func(c *backupEnvConfig) {
				c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) { db.Spec.DatabaseName = "other" })
			},
			want: "database name legacy",
		},
		"another project's backups": {
			mutate: func(c *backupEnvConfig) {
				c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) { db.Spec.Project = "intruder" })
			},
			want: "another project",
		},
		"a disk smaller than the source": {
			mutate: func(c *backupEnvConfig) {
				c.dbMutations = append(c.dbMutations, func(db *geassv1alpha1.GeassDatabase) {
					small := resource.MustParse("5Gi")
					db.Spec.StorageSize = &small
				})
			},
			want: "at least 20Gi",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var mutate []func(*backupEnvConfig)
			if tt.mutate != nil {
				mutate = append(mutate, tt.mutate)
			}
			env, _ := recoveryEnv(t, mutate...)
			if tt.restore != nil {
				tt.restore(env.db.Spec.RestoreFrom)
			}
			plan, err := env.r.planRecovery(context.Background(), env.db, env.wsNS)
			require.Nil(t, plan)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.want)
			for _, term := range []string{"CNPG", "Cluster", "Kubernetes", "barman", "WAL"} {
				require.NotContains(t, err.Error(), term, "user-facing error leaks %q", term)
			}
		})
	}
}

func TestReconcileCNPGClusterCreatesARecoveryBootstrapForARestore(t *testing.T) {
	env, source := recoveryEnv(t)
	recovery, err := env.r.planRecovery(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	require.NoError(t, env.ensureCluster(nil, recovery))

	var cluster cnpgv1.Cluster
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testDBName, Namespace: env.wsNS}, &cluster))
	require.Nil(t, cluster.Spec.Bootstrap.InitDB, "a restore must not also ask for an empty database")
	require.NotNil(t, cluster.Spec.Bootstrap.Recovery)
	require.Equal(t, "origin", cluster.Spec.Bootstrap.Recovery.Source)
	require.Len(t, cluster.Spec.ExternalClusters, 1)
	require.Equal(t, source.Spec.ServerName, cluster.Spec.ExternalClusters[0].BarmanObjectStore.ServerName)
}

func TestReconcileCNPGClusterNeverChangesTheBootstrapOfAnExistingCluster(t *testing.T) {
	existing := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: testDevTargetNS},
		Spec: cnpgv1.ClusterSpec{
			Instances: 1,
			Bootstrap: cnpgv1.BootstrapConfiguration{InitDB: &cnpgv1.InitDBConfiguration{Database: testDBName, Owner: appContainerName}},
		},
	}
	env, _ := recoveryEnv(t, func(c *backupEnvConfig) { c.extraObjects = append(c.extraObjects, existing) })
	recovery, err := env.r.planRecovery(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	require.NoError(t, env.ensureCluster(nil, recovery))

	var cluster cnpgv1.Cluster
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testDBName, Namespace: env.wsNS}, &cluster))
	require.NotNil(t, cluster.Spec.Bootstrap.InitDB, "bootstrap is fixed at creation; changing it would be rejected by the operator")
	require.Nil(t, cluster.Spec.Bootstrap.Recovery)
	require.Empty(t, cluster.Spec.ExternalClusters)
}

func TestReconcileCNPGClusterKeepsTheRecoverySourceOfAnExistingRestore(t *testing.T) {
	// After the source archive is purged the database must keep reconciling: bootstrap and the
	// external cluster it names are preserved as they are, not rebuilt from a missing archive.
	existing := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: testDevTargetNS},
		Spec: cnpgv1.ClusterSpec{
			Instances:        1,
			Bootstrap:        cnpgv1.BootstrapConfiguration{Recovery: &cnpgv1.RecoveryConfiguration{Source: "origin", Database: "legacy", Owner: appContainerName}},
			ExternalClusters: []cnpgv1.ExternalCluster{{Name: "origin", BarmanObjectStore: &cnpgv1.BarmanObjectStoreConfiguration{DestinationPath: "s3://b/p", ServerName: "payments-old-9f8e7d6c"}}},
		},
	}
	env, _ := recoveryEnv(t, func(c *backupEnvConfig) { c.extraObjects = append(c.extraObjects, existing) })

	require.NoError(t, env.ensureCluster(nil, nil))

	var cluster cnpgv1.Cluster
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testDBName, Namespace: env.wsNS}, &cluster))
	require.NotNil(t, cluster.Spec.Bootstrap.Recovery)
	require.Nil(t, cluster.Spec.Bootstrap.InitDB)
	require.Len(t, cluster.Spec.ExternalClusters, 1)
}

func TestReconcileCNPGClusterAddsBackupsToARunningCluster(t *testing.T) {
	existing := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: testDevTargetNS},
		Spec: cnpgv1.ClusterSpec{
			Instances: 1,
			Bootstrap: cnpgv1.BootstrapConfiguration{InitDB: &cnpgv1.InitDBConfiguration{Database: testDBName, Owner: appContainerName}},
		},
	}
	env := newBackupEnv(t, func(c *backupEnvConfig) { c.extraObjects = append(c.extraObjects, existing) })
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)

	require.NoError(t, env.ensureCluster(plan, nil))

	var cluster cnpgv1.Cluster
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testDBName, Namespace: env.wsNS}, &cluster))
	require.NotNil(t, cluster.Spec.Backup, "enabling backups on a database that already runs must reach its cluster")
	require.Equal(t, testDBServerName, cluster.Spec.Backup.BarmanObjectStore.ServerName)
	require.NotNil(t, cluster.Spec.Bootstrap.InitDB)
}

func TestReconcileCNPGClusterRemovesBackupsWhenTurnedOff(t *testing.T) {
	existing := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: testDevTargetNS},
		Spec: cnpgv1.ClusterSpec{
			Instances: 1,
			Bootstrap: cnpgv1.BootstrapConfiguration{InitDB: &cnpgv1.InitDBConfiguration{Database: testDBName, Owner: appContainerName}},
			Backup:    &cnpgv1.BackupConfiguration{RetentionPolicy: "7d", BarmanObjectStore: &cnpgv1.BarmanObjectStoreConfiguration{DestinationPath: "s3://b/p"}},
		},
	}
	env := newBackupEnv(t, func(c *backupEnvConfig) { c.backup = nil; c.extraObjects = append(c.extraObjects, existing) })

	require.NoError(t, env.ensureCluster(nil, nil))

	var cluster cnpgv1.Cluster
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testDBName, Namespace: env.wsNS}, &cluster))
	require.Nil(t, cluster.Spec.Backup)
}

func errString(message string) error { return &stringError{message} }

type stringError struct{ message string }

func (e *stringError) Error() string { return e.message }

func resourceQuantityPtr(value string) *resource.Quantity {
	quantity := resource.MustParse(value)
	return &quantity
}

func TestReconcileCNPGClusterKeepsArchivingThroughATemporaryStorageProblem(t *testing.T) {
	existing := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testDBName, Namespace: testDevTargetNS},
		Spec: cnpgv1.ClusterSpec{
			Instances: 1,
			Bootstrap: cnpgv1.BootstrapConfiguration{InitDB: &cnpgv1.InitDBConfiguration{Database: testDBName, Owner: appContainerName}},
			Backup: &cnpgv1.BackupConfiguration{RetentionPolicy: "7d", BarmanObjectStore: &cnpgv1.BarmanObjectStoreConfiguration{
				DestinationPath: "s3://" + testBackupBucket + "/" + testProjectName, ServerName: testDBServerName,
			}},
		},
	}
	// The project's storage choice has vanished, so planning cannot produce an archive this time.
	env := newBackupEnv(t, func(c *backupEnvConfig) { c.storage = nil; c.extraObjects = append(c.extraObjects, existing) })
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NotNil(t, plan, "backups are still wanted")
	require.Nil(t, plan.archive)

	require.NoError(t, env.ensureCluster(plan, nil))

	var cluster cnpgv1.Cluster
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testDBName, Namespace: env.wsNS}, &cluster))
	require.NotNil(t, cluster.Spec.Backup, "stopping archiving because of a passing problem would silently open a gap in the backups")
	require.Equal(t, testDBServerName, cluster.Spec.Backup.BarmanObjectStore.ServerName)
}

func TestFreeArchiveNameTakesTheFirstNumberNoArchiveUses(t *testing.T) {
	archives := func(names ...string) []geassv1alpha1.GeassBackupArchive {
		list := make([]geassv1alpha1.GeassBackupArchive, 0, len(names))
		for _, name := range names {
			list = append(list, geassv1alpha1.GeassBackupArchive{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec:       geassv1alpha1.GeassBackupArchiveSpec{ServerName: name},
			})
		}
		return list
	}
	require.Equal(t, "orders-1a2b", freeArchiveName("orders-1a2b", nil))
	require.Equal(t, "orders-1a2b-2", freeArchiveName("orders-1a2b", archives("orders-1a2b")))
	require.Equal(t, "orders-1a2b-3", freeArchiveName("orders-1a2b", archives("orders-1a2b", "orders-1a2b-2")))
	require.Equal(t, "orders-1a2b-3", freeArchiveName("orders-1a2b", archives("orders-1a2b-2", "orders-1a2b")), "order does not matter")
	require.Equal(t, "orders-1a2b-2", freeArchiveName("orders-1a2b", archives("orders-1a2b", "orders-1a2b-3")), "the first free number, not one past the highest")
	require.Equal(t, "orders-1a2b", freeArchiveName("orders-1a2b", archives("orders-1a2b-2")), "a purged archive leaves its name, and its empty folder, free")
}

func TestPlanBackupsStillWorksAfterTheJanitorPurgedAnEarlierArchive(t *testing.T) {
	env := newBackupEnv(t)
	retire := func(name string) {
		archive := env.archive(name)
		archive.Spec.DeleteAfter = timePtr(metav1.NewTime(time.Now().Add(24 * time.Hour)))
		require.NoError(t, env.client.Update(context.Background(), archive))
	}
	first, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	retire(first.archive.Name)
	second, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	retire(second.archive.Name)

	// Seven days on, the janitor removes the first archive and its data, leaving only the second.
	require.NoError(t, env.client.Delete(context.Background(), env.archive(first.archive.Name)))

	third, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err, "counting archives would hand out the second one's name again and fail until it was purged too")
	require.NotEqual(t, second.archive.Name, third.archive.Name, "never write into a retired archive's folder")
	require.Nil(t, env.archive(third.archive.Name).Spec.DeleteAfter)
	require.Len(t, env.archives(), 2)
}

func TestPlanBackupsUpdatesTheLiveArchiveFromAFreshCopy(t *testing.T) {
	env := newBackupEnv(t)
	first, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	stale := first.archive.DeepCopy()

	// The archive's status is rewritten between reconciles, which moves its version on.
	fresh := env.archive(stale.Name)
	fresh.Status.LastBackupTime = timePtr(metav1.Now())
	require.NoError(t, env.client.Status().Update(context.Background(), fresh))

	env.db.Spec.Backup.RetentionDays = 30
	got, err := env.r.ensureBackupArchive(context.Background(), env.db, []geassv1alpha1.GeassBackupArchive{*stale}, stale, stale.Spec.Storage)
	require.NoError(t, err, "a copy read before the status was written must not cause a conflict")
	require.EqualValues(t, 30, got.Spec.RetentionDays)
	stored := env.archive(stale.Name)
	require.EqualValues(t, 30, stored.Spec.RetentionDays)
	require.NotNil(t, stored.Status.LastBackupTime, "what was written meanwhile is kept")
}

func TestADegradedPlanKeepsTheClusterArchiving(t *testing.T) {
	env := newBackupEnv(t)
	plan, err := env.r.planBackups(context.Background(), env.db, env.wsNS)
	require.NoError(t, err)
	require.NoError(t, env.ensureCluster(plan, nil))

	// Working out the plan failed this time: whatever is configured must stay as it is.
	require.NoError(t, env.ensureCluster(&backupPlan{degraded: true}, nil))

	var cluster cnpgv1.Cluster
	require.NoError(t, env.client.Get(context.Background(), client.ObjectKey{Name: testDBName, Namespace: env.wsNS}, &cluster))
	require.NotNil(t, cluster.Spec.Backup, "a passing problem must not switch archiving off")
	require.Equal(t, testDBServerName, cluster.Spec.Backup.BarmanObjectStore.ServerName)
}

func TestPlanRecoveryRefusesBackupsThatHaveNothingToRestoreFrom(t *testing.T) {
	env, source := recoveryEnv(t)
	empty := env.archive(source.Name)
	empty.Status.RestorePoints = nil
	require.NoError(t, env.client.Status().Update(context.Background(), empty))

	_, err := env.r.planRecovery(context.Background(), env.db, env.wsNS)
	require.ErrorContains(t, err, "no restore points yet", "a recovery without a base backup would never finish and never report why")
}
