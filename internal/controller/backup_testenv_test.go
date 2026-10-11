package controller

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	cnpgv1 "github.com/degoke/geass/pkg/cnpg/v1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	testBackupBucket    = "geass-backups-abc123"
	testBackupRegion    = "eu-west-1"
	testAWSConnection   = "aws"
	testAWSSecret       = "aws-credentials"
	testDBUID           = types.UID("1a2b3c4d-0000-4000-8000-000000000001")
	testDBServerName    = "orders-1a2b3c4d"
	testBackupSecretOrd = "orders-backup-credentials"
)

// fakeBackupStorage stands in for the AWS client, recording what the controller asked it to do.
type fakeBackupStorage struct {
	mu             sync.Mutex
	ensuredBuckets []string
	userRequests   []fakeUserRequest
	purged         []string // "bucket/prefix"
	constructed    []fakeClientCreds
	ensureBucket   error
	ensureUser     error
	purge          error
	// allKeys are the objects in the bucket; ListKeys returns those under the requested prefix.
	allKeys []string
	listErr error
	listed  []string // prefixes that were listed
	// deleted are the keys DeleteObject removed; they also leave allKeys.
	deleted   []string
	deleteErr error
}

type fakeUserRequest struct {
	buckets                              []string
	user, existingAccess, existingSecret string
}

type fakeClientCreds struct{ access, secret, region string }

func (f *fakeBackupStorage) factory() backupStorageFactory {
	return func(access, secret, region string) backupStorageAPI {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.constructed = append(f.constructed, fakeClientCreds{access, secret, region})
		return f
	}
}

func (f *fakeBackupStorage) EnsureBucket(bucket string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensuredBuckets = append(f.ensuredBuckets, bucket)
	return f.ensureBucket
}

func (f *fakeBackupStorage) EnsureBucketUser(buckets []string, user, existingAccess, existingSecret string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userRequests = append(f.userRequests, fakeUserRequest{buckets, user, existingAccess, existingSecret})
	if f.ensureUser != nil {
		return "", "", f.ensureUser
	}
	if existingAccess != "" && existingSecret != "" {
		return existingAccess, existingSecret, nil
	}
	return "AKIABACKUPUSER", "backup-user-secret", nil
}

func (f *fakeBackupStorage) ListKeys(bucket, prefix string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed = append(f.listed, bucket+"/"+prefix)
	if f.listErr != nil {
		return nil, f.listErr
	}
	prefix = strings.Trim(prefix, "/") + "/"
	var keys []string
	for _, key := range f.allKeys {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (f *fakeBackupStorage) DeleteObject(bucket, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, key)
	for i, existing := range f.allKeys {
		if existing == key {
			f.allKeys = append(f.allKeys[:i], f.allKeys[i+1:]...)
			break
		}
	}
	return nil
}

func (f *fakeBackupStorage) DeletePrefix(bucket, prefix string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purged = append(f.purged, bucket+"/"+prefix)
	return f.purge
}

type backupEnvConfig struct {
	storage      *geassv1alpha1.GeassBackupStorage
	provider     geassv1alpha1.GeassCloudProvider
	backup       *geassv1alpha1.GeassDatabaseBackupSpec
	noConnection bool
	extraObjects []client.Object
	dbMutations  []func(*geassv1alpha1.GeassDatabase)
}

type backupEnv struct {
	t       *testing.T
	client  client.Client
	r       *GeassDatabaseReconciler
	storage *fakeBackupStorage
	db      *geassv1alpha1.GeassDatabase
	wsNS    string
}

func defaultTestBackupStorage() *geassv1alpha1.GeassBackupStorage {
	return &geassv1alpha1.GeassBackupStorage{
		ConnectionRef: corev1.LocalObjectReference{Name: testAWSConnection},
		Bucket:        testBackupBucket,
		Region:        testBackupRegion,
	}
}

func backupTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, cnpgv1.AddToScheme(scheme))
	return scheme
}

func newBackupEnv(t *testing.T, mutate ...func(*backupEnvConfig)) *backupEnv {
	t.Helper()
	cfg := &backupEnvConfig{
		storage:  defaultTestBackupStorage(),
		provider: geassv1alpha1.CloudProviderAWS,
		backup:   &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true},
	}
	for _, m := range mutate {
		m(cfg)
	}

	storageSize := resource.MustParse("20Gi")
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{
			Name: testDBName, Namespace: platform.SystemNamespace, UID: testDBUID,
			Annotations: map[string]string{platform.DisplayNameAnnotation: "morning-beach"},
		},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev,
			Engine: geassv1alpha1.DatabaseEnginePostgres, Version: "16", StorageSize: &storageSize,
			Backup: cfg.backup,
		},
	}
	for _, m := range cfg.dbMutations {
		m(db)
	}
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{testEnvDev},
			BackupStorage: cfg.storage,
		},
		Status: geassv1alpha1.GeassProjectStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
	}
	objects := []client.Object{db, project}
	if !cfg.noConnection {
		objects = append(objects,
			&geassv1alpha1.GeassCloudConnection{
				ObjectMeta: metav1.ObjectMeta{Name: testAWSConnection, Namespace: platform.SystemNamespace},
				Spec: geassv1alpha1.GeassCloudConnectionSpec{
					Provider: cfg.provider, SecretRef: corev1.LocalObjectReference{Name: testAWSSecret},
					Project: testProjectName, Region: "us-east-1",
				},
			},
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: testAWSSecret, Namespace: platform.SystemNamespace},
				Data: map[string][]byte{
					platform.SecretKeyAccessKeyID:     []byte("AKIAROOT"),
					platform.SecretKeySecretAccessKey: []byte("root-secret"),
				},
			},
		)
	}
	objects = append(objects, cfg.extraObjects...)

	scheme := backupTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&geassv1alpha1.GeassDatabase{}, &geassv1alpha1.GeassBackupArchive{}).
		WithObjects(objects...).Build()
	storage := &fakeBackupStorage{}
	// A fixed clock keeps window ends and deadlines identical across the reconciles of one test.
	fixedNow := time.Now()
	return &backupEnv{
		t: t, client: c, storage: storage, db: db, wsNS: testDevTargetNS,
		r: &GeassDatabaseReconciler{Client: c, Scheme: scheme, newBackupStorage: storage.factory(), clock: func() time.Time { return fixedNow }},
	}
}

// ensureCluster runs reconcileCNPGCluster with no storage class, which is all the backup tests need.
func (e *backupEnv) ensureCluster(plan *backupPlan, recovery *recoveryPlan) error {
	_, err := e.r.reconcileCNPGCluster(context.Background(), e.db, e.wsNS, "", plan, recovery)
	return err
}

func (e *backupEnv) reload() *geassv1alpha1.GeassDatabase {
	e.t.Helper()
	var db geassv1alpha1.GeassDatabase
	require.NoError(e.t, e.client.Get(context.Background(), client.ObjectKeyFromObject(e.db), &db))
	return &db
}

func (e *backupEnv) archive(name string) *geassv1alpha1.GeassBackupArchive {
	e.t.Helper()
	var archive geassv1alpha1.GeassBackupArchive
	require.NoError(e.t, e.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &archive))
	return &archive
}

func (e *backupEnv) archives() []geassv1alpha1.GeassBackupArchive {
	e.t.Helper()
	var list geassv1alpha1.GeassBackupArchiveList
	require.NoError(e.t, e.client.List(context.Background(), &list, client.InNamespace(platform.SystemNamespace)))
	return list.Items
}

func (e *backupEnv) secret(namespace, name string) *corev1.Secret {
	e.t.Helper()
	var secret corev1.Secret
	require.NoError(e.t, e.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, &secret))
	return &secret
}

func (e *backupEnv) exists(obj client.Object, namespace, name string) bool {
	e.t.Helper()
	err := e.client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, obj)
	if err == nil {
		return true
	}
	require.True(e.t, isNotFound(err), "unexpected error looking up %s/%s: %v", namespace, name, err)
	return false
}

func isNotFound(err error) bool {
	return err != nil && client.IgnoreNotFound(err) == nil
}

func timePtr(t metav1.Time) *metav1.Time { return &t }
