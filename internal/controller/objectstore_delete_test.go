package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloud"
	"github.com/degoke/geass/pkg/platform"
)

func TestDeleteExternalStoreSkipsBucketsWhenCreateBucketFalse(t *testing.T) {
	var actions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if action := r.FormValue("Action"); action != "" {
			actions = append(actions, action)
			if action == "ListAccessKeys" {
				_, _ = w.Write([]byte(`<ListAccessKeysResponse><ListAccessKeysResult></ListAccessKeysResult></ListAccessKeysResponse>`))
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		actions = append(actions, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: "assets", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			CreateBucket:  false,
			Buckets:       []string{"uploads"},
			ConnectionRef: &corev1.LocalObjectReference{Name: "aws"},
		},
	}
	r := &GeassObjectStoreReconciler{HTTP: srv.Client()}
	aws := &cloud.AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL, IAMEndpoint: srv.URL}
	require.NoError(t, r.deleteExternalAWS(store, aws))
	require.Equal(t, []string{"ListAccessKeys", "DeleteUserPolicy", "DeleteUser"}, actions)
}

func TestDeleteExternalStoreRemovesBucketsWhenCreateBucketTrue(t *testing.T) {
	var actions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if action := r.FormValue("Action"); action != "" {
			actions = append(actions, action)
			if action == "ListAccessKeys" {
				_, _ = w.Write([]byte(`<ListAccessKeysResponse><ListAccessKeysResult></ListAccessKeysResult></ListAccessKeysResponse>`))
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		actions = append(actions, r.Method)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`<ListBucketResult></ListBucketResult>`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: "assets"},
		Spec:       geassv1alpha1.GeassObjectStoreSpec{CreateBucket: true, Buckets: []string{"uploads"}},
	}
	r := &GeassObjectStoreReconciler{HTTP: srv.Client()}
	aws := &cloud.AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL, IAMEndpoint: srv.URL}
	require.NoError(t, r.deleteExternalAWS(store, aws))
	require.Contains(t, actions, http.MethodDelete)
	require.Contains(t, actions, "DeleteUser")
}

func TestDeleteExternalStoreRequiresCredentials(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	connection := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "aws", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassCloudConnectionSpec{
			Provider:  geassv1alpha1.CloudProviderAWS,
			Project:   "payments",
			SecretRef: corev1.LocalObjectReference{Name: "aws-creds"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-creds", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{},
	}
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: "assets", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Project:       "payments",
			CreateBucket:  true,
			ConnectionRef: &corev1.LocalObjectReference{Name: "aws"},
		},
	}
	r := &GeassObjectStoreReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(connection, secret).Build(),
	}
	err := r.deleteExternalStore(context.Background(), store)
	require.Error(t, err)
	require.Contains(t, err.Error(), "credentials")
}

func TestDeleteExternalStoreRequiresConnectionRef(t *testing.T) {
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: "assets", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassObjectStoreSpec{CreateBucket: true},
	}
	r := &GeassObjectStoreReconciler{}
	err := r.deleteExternalStore(context.Background(), store)
	require.Error(t, err)
	require.Contains(t, err.Error(), "AWS connection is required")
}

func TestDeleteClusterObjectStorageBlockedByProjectBuckets(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	now := metav1.Now()
	cluster := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{
			Name:              platform.ClusterObjectStoreName,
			Namespace:         platform.SystemNamespace,
			Finalizers:        []string{objectStoreFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Engine:    geassv1alpha1.ObjectStoreEngineSeaweedFS,
			Placement: geassv1alpha1.ObjectStorePlacementInCluster,
		},
	}
	project := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: "uploads", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Project:   "payments",
			Engine:    geassv1alpha1.ObjectStoreEngineSeaweedFS,
			Placement: geassv1alpha1.ObjectStorePlacementInCluster,
		},
	}
	r := &GeassObjectStoreReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, project).Build(),
		Scheme: scheme,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "project buckets")
	latest := &geassv1alpha1.GeassObjectStore{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}, latest))
	require.Contains(t, latest.Finalizers, objectStoreFinalizer)
}

func TestDeleteDraftProjectBucketSkipsTheServerWhenNeverDeployed(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	now := metav1.Now()
	cluster := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Engine:    geassv1alpha1.ObjectStoreEngineSeaweedFS,
			Placement: geassv1alpha1.ObjectStorePlacementInCluster,
		},
		Status: geassv1alpha1.GeassObjectStoreStatus{},
	}
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "sleepy-coral",
			Namespace:         platform.SystemNamespace,
			Finalizers:        []string{objectStoreFinalizer},
			DeletionTimestamp: &now,
			Annotations: map[string]string{
				platform.DeployWorkflowAnnotation: "true",
			},
		},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Project:     "payments",
			Environment: geassv1alpha1.EnvironmentProduction,
			Engine:      geassv1alpha1.ObjectStoreEngineSeaweedFS,
			Placement:   geassv1alpha1.ObjectStorePlacementInCluster,
			Deploy:      geassv1alpha1.GeassDeploySpec{Enabled: false},
		},
	}
	r := &GeassObjectStoreReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, store).Build(),
		Scheme: scheme,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: store.Name, Namespace: store.Namespace}})
	require.NoError(t, err)
	err = r.Get(context.Background(), types.NamespacedName{Name: store.Name, Namespace: store.Namespace}, store)
	require.True(t, apierrors.IsNotFound(err))
}

func TestDeleteProjectBucketSucceedsWhenClusterObjectStorageMissing(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	now := metav1.Now()
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "uploads",
			Namespace:         platform.SystemNamespace,
			Finalizers:        []string{objectStoreFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Project:     "payments",
			Environment: "dev",
			Engine:      geassv1alpha1.ObjectStoreEngineSeaweedFS,
			Placement:   geassv1alpha1.ObjectStorePlacementInCluster,
		},
	}
	r := &GeassObjectStoreReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(store).Build(),
		Scheme: scheme,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: store.Name, Namespace: store.Namespace}})
	require.NoError(t, err)
	latest := &geassv1alpha1.GeassObjectStore{}
	err = r.Get(context.Background(), types.NamespacedName{Name: store.Name, Namespace: store.Namespace}, latest)
	require.True(t, err != nil && apierrors.IsNotFound(err) || err == nil && !containsFinalizer(latest, objectStoreFinalizer))
}

func containsFinalizer(store *geassv1alpha1.GeassObjectStore, name string) bool {
	if store == nil {
		return false
	}
	return slices.Contains(store.Finalizers, name)
}

// Removing the cluster's object storage server deletes its volume, so only the server itself may do it. A store with no
// project that is an AWS bucket, or external, is not the server, and deleting it must leave the real one alone.
func TestDeletingAStoreThatIsNotTheServerLeavesTheServerAlone(t *testing.T) {
	ctx := context.Background()
	for name, spec := range map[string]geassv1alpha1.GeassObjectStoreSpec{
		"an S3 store with no project":       {Engine: geassv1alpha1.ObjectStoreEngineS3, Placement: geassv1alpha1.ObjectStorePlacementExternal},
		"an external store with no project": {Engine: geassv1alpha1.ObjectStoreEngineSeaweedFS, Placement: geassv1alpha1.ObjectStorePlacementExternal},
	} {
		t.Run(name, func(t *testing.T) {
			stray := &geassv1alpha1.GeassObjectStore{
				ObjectMeta: metav1.ObjectMeta{Name: "stray", Namespace: platform.SystemNamespace, Finalizers: []string{objectStoreFinalizer}},
				Spec:       spec,
			}
			system := platform.SystemNamespace
			objects := append(runningClusterServer(geassv1alpha1.ObjectStoreEngineSeaweedFS),
				stray,
				&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerName, Namespace: system}},
				&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerName, Namespace: system}},
				&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerName, Namespace: system}},
			)
			r, c := newObjectStoreReconciler(t, nil, objects...)
			require.NoError(t, c.Delete(ctx, stray))

			_, err := r.Reconcile(ctx, reconcileRequest(stray))
			require.NoError(t, err)

			require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ObjectStoreServerName, Namespace: system}, &appsv1.Deployment{}), "the server's Deployment is still there")
			require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ObjectStoreServerName, Namespace: system}, &corev1.PersistentVolumeClaim{}), "and so is its volume")
			require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ObjectStoreServerName, Namespace: system}, &corev1.Service{}))
			require.NoError(t, c.Get(ctx, client.ObjectKey{Name: objectStoreRootSecretName(platform.ClusterObjectStoreName), Namespace: system}, &corev1.Secret{}), "and its login")
			require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(stray), &geassv1alpha1.GeassObjectStore{})), "the stray store itself is gone")
		})
	}
}
