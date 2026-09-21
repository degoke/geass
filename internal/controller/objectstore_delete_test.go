package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
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

func TestDeleteClusterMinIOBlockedByProjectBuckets(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	now := metav1.Now()
	cluster := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{
			Name:              platform.ClusterMinIOName,
			Namespace:         platform.SystemNamespace,
			Finalizers:        []string{objectStoreFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Engine:    geassv1alpha1.ObjectStoreEngineMinIO,
			Placement: geassv1alpha1.ObjectStorePlacementInCluster,
		},
	}
	project := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: "uploads", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassObjectStoreSpec{
			Project:   "payments",
			Engine:    geassv1alpha1.ObjectStoreEngineMinIO,
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

func TestDeleteProjectBucketSucceedsWhenClusterMinIOMissing(t *testing.T) {
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
			Engine:      geassv1alpha1.ObjectStoreEngineMinIO,
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
	for _, item := range store.Finalizers {
		if item == name {
			return true
		}
	}
	return false
}
