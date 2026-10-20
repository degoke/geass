/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Functional Source License, Version 1.1, ALv2 Future License
(FSL-1.1-ALv2, the "License"); you may not use this file except in compliance
with the License. You may obtain a copy of the License at LICENSE or
https://fsl.software/FSL-1.1-ALv2.template.md.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloud"
	"github.com/degoke/geass/pkg/platform"
)

const objectStoreFinalizer = platform.FinalizerObjectStore

// errClusterObjectStoreMissing is what a project bucket meets when the cluster has no object storage server.
var errClusterObjectStoreMissing = platform.UserError(platform.MessageSetUpObjectStorage)

// GeassObjectStoreReconciler reconciles a GeassObjectStore object. A store with a project is a bucket, in AWS or
// on the cluster's object storage server; a store with none is that server.
type GeassObjectStoreReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	HTTP   *http.Client
	// PodUsage is what the cluster's pods ask of its nodes, for the check that the server fits. Nil reads the pods.
	PodUsage *platform.PodUsageIndex
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassobjectstores,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassobjectstores/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassobjectstores/finalizers,verbs=update
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscloudconnections,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters,verbs=get;list;watch

func (r *GeassObjectStoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var store geassv1alpha1.GeassObjectStore
	if err := r.Get(ctx, req.NamespacedName, &store); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if result, paused := beginProvisionReconcileUnlessDeleting(!store.DeletionTimestamp.IsZero(), store.Status.Conditions, store.Generation, &store.Status.ReconcileRetry); paused {
		return result, nil
	}

	clusterServer := isClusterObjectStore(&store)
	wsNS := platform.SystemNamespace
	if !clusterServer {
		if ns, err := resourceNamespace(store.Spec.Project, string(store.Spec.Environment)); err == nil {
			wsNS = ns
		}
	}

	if !controllerutil.ContainsFinalizer(&store, objectStoreFinalizer) {
		controllerutil.AddFinalizer(&store, objectStoreFinalizer)
		return ctrl.Result{}, r.Update(ctx, &store)
	}

	if !store.DeletionTimestamp.IsZero() {
		// A store that never became ready was never usable, so failed cleanup must not hold its deletion: the
		// credentials that could not create anything (no S3 or IAM permission, say) cannot clean up either.
		// Once a store has been ready, cleanup has to finish before the finalizer goes.
		tolerate := func(err error) error {
			if err == nil || strings.TrimSpace(store.Status.ConnectionSecret) != "" {
				return err
			}
			log.Info("Could not clean up a bucket that never became ready", "name", store.Name, "error", err.Error())
			return nil
		}
		if clusterServer {
			attached, attachErr := r.attachedProjectBucketNames(ctx)
			if attachErr != nil {
				return ctrl.Result{}, attachErr
			}
			if len(attached) > 0 {
				return ctrl.Result{}, fmt.Errorf("delete project buckets before removing the object storage server: %s", strings.Join(attached, ", "))
			}
			if err := r.removeObjectStoreServer(ctx, &store); err != nil {
				return ctrl.Result{}, err
			}
		} else if objectStorePlacement(&store) == geassv1alpha1.ObjectStorePlacementExternal {
			if platform.ObjectStoreInfrastructureActive(&store) {
				if err := tolerate(r.deleteExternalStore(ctx, deployedObjectStore(&store))); err != nil {
					return ctrl.Result{}, err
				}
			}
		} else {
			if err := tolerate(r.deleteInClusterBuckets(ctx, deployedObjectStore(&store))); err != nil {
				return ctrl.Result{}, err
			}
		}
		_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: store.Name + "-connection", Namespace: wsNS}}))
		controllerutil.RemoveFinalizer(&store, objectStoreFinalizer)
		return ctrl.Result{}, r.Update(ctx, &store)
	}

	if !clusterServer {
		if err := r.validateObjectStoreCloudConnection(ctx, &store); err != nil {
			return r.setNotReady(ctx, &store, err.Error())
		}
		if err := platform.ValidateProjectPlacement(ctx, r.Client, store.Spec.Project, store.Spec.Environment); err != nil {
			return r.setNotReady(ctx, &store, err.Error())
		}
		ns, err := resourceNamespace(store.Spec.Project, string(store.Spec.Environment))
		if err != nil {
			return r.setNotReady(ctx, &store, err.Error())
		}
		wsNS = ns
	}

	if !clusterServer {
		if err := applyObjectStoreDeployedSpec(&store); err != nil {
			if errors.Is(err, ErrAwaitingDeploy) {
				return r.setNotReady(ctx, &store, waitingForDeployMessage)
			}
			return r.setNotReady(ctx, &store, err.Error())
		}
	}
	if objectStorePlacement(&store) == geassv1alpha1.ObjectStorePlacementExternal || store.Spec.Engine == geassv1alpha1.ObjectStoreEngineS3 {
		return r.reconcileExternal(ctx, &store, wsNS)
	}
	if clusterServer {
		backend, ok := objectStoreBackendFor(store.Spec.Engine)
		if !ok {
			return r.setNotReady(ctx, &store, fmt.Sprintf("unsupported engine %q", store.Spec.Engine))
		}
		return r.reconcileClusterServer(ctx, &store, backend, log)
	}
	return r.reconcileProjectBucket(ctx, &store, wsNS)
}

// isClusterObjectStore says the store is the cluster's object storage server. It must agree with the dashboard and the
// platform about that: a store with no project that is not one (an S3 or External one) must never be treated as the
// server, because removing the server deletes its volume.
func isClusterObjectStore(store *geassv1alpha1.GeassObjectStore) bool {
	return platform.IsClusterObjectStoreServer(store)
}

func objectStorePlacement(store *geassv1alpha1.GeassObjectStore) geassv1alpha1.GeassObjectStorePlacement {
	if store.Spec.Placement == geassv1alpha1.ObjectStorePlacementExternal || store.Spec.Engine == geassv1alpha1.ObjectStoreEngineS3 {
		return geassv1alpha1.ObjectStorePlacementExternal
	}
	return geassv1alpha1.ObjectStorePlacementInCluster
}

func objectStoreRootSecretName(storeName string) string {
	return storeName + "-root"
}

func pinnedStorePlacement(store *geassv1alpha1.GeassObjectStore) platform.Placement {
	if store == nil {
		return platform.Placement{}
	}
	return platform.ParsePlacement(store.Annotations[platform.AnnotationNodePlacement])
}

// objectStoreBucketNames returns buckets explicitly configured on the spec (draft stores have none).
func objectStoreBucketNames(store *geassv1alpha1.GeassObjectStore) ([]string, error) {
	buckets := store.Spec.Buckets
	for _, bucket := range buckets {
		if err := platform.ValidBucketName(bucket); err != nil {
			return nil, err
		}
	}
	return buckets, nil
}

// inClusterBucketNames are the names the store's buckets have on the cluster's object storage server: the names in the spec,
// each behind the project environment, so that projects never share a bucket.
func inClusterBucketNames(store *geassv1alpha1.GeassObjectStore) ([]string, error) {
	buckets, err := objectStoreBucketNames(store)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(buckets))
	for i, bucket := range buckets {
		if names[i], err = platform.InClusterBucketName(store.Spec.Project, string(store.Spec.Environment), bucket); err != nil {
			return nil, err
		}
	}
	return names, nil
}

// deployedObjectStore is the store carrying the spec that was last deployed. A saved edit stays pending until it
// is deployed, so what exists on the server or in AWS follows the deployed spec: that is what to grant access to and
// what to remove. A store with nothing deployed keeps its own spec. With no edit pending it is the store itself,
// so the result must be treated as read-only.
func deployedObjectStore(store *geassv1alpha1.GeassObjectStore) *geassv1alpha1.GeassObjectStore {
	if platform.PendingUpdates(store.Annotations) == 0 {
		return store
	}
	deployed := store.DeepCopy()
	// An error leaves the spec as it was, which is the best there is when no snapshot was recorded.
	_ = applyObjectStoreDeployedSpec(deployed)
	return deployed
}

// markProvisioningStarted records, before the first bucket or user is created, that the store may own resources
// outside the cluster. Deleting a store that never became ready would otherwise leave them behind, because a
// store is only known to own anything once its connection secret is written, which happens last. The annotation
// is patched onto a fresh copy so that the deployed spec the reconcile is running is never written back.
func (r *GeassObjectStoreReconciler) markProvisioningStarted(ctx context.Context, store *geassv1alpha1.GeassObjectStore) error {
	if store.Annotations[platform.AnnotationProvisioningStarted] == "true" {
		return nil
	}
	latest := &geassv1alpha1.GeassObjectStore{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(store), latest); err != nil {
		return err
	}
	if latest.Annotations[platform.AnnotationProvisioningStarted] != "true" {
		base := latest.DeepCopy()
		if latest.Annotations == nil {
			latest.Annotations = map[string]string{}
		}
		latest.Annotations[platform.AnnotationProvisioningStarted] = "true"
		if err := r.Patch(ctx, latest, client.MergeFrom(base)); err != nil {
			return err
		}
	}
	if store.Annotations == nil {
		store.Annotations = map[string]string{}
	}
	store.Annotations[platform.AnnotationProvisioningStarted] = "true"
	return nil
}

func (r *GeassObjectStoreReconciler) reconcileProjectBucket(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS string) (ctrl.Result, error) {
	serverBuckets, err := inClusterBucketNames(store)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if len(serverBuckets) == 0 {
		return r.setNotReady(ctx, store, "Configure the bucket name in settings, then deploy")
	}
	server, err := r.clusterObjectStore(ctx)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if _, ok := objectStoreBackendFor(server.Spec.Engine); !ok {
		return r.setNotReady(ctx, store, fmt.Sprintf("unsupported engine %q", server.Spec.Engine))
	}
	// A server that is being removed takes no new buckets.
	if !server.DeletionTimestamp.IsZero() {
		return r.setNotReady(ctx, store, platform.MessageSetUpObjectStorage)
	}
	if conditionStatus(server.Status.Conditions, platform.ConditionReady) != string(metav1.ConditionTrue) || server.Status.Endpoint == "" {
		return r.setNotReady(ctx, store, platform.MessageObjectStorageStarting)
	}
	// A project that requires node tags must not keep its objects on a server that runs elsewhere.
	// If the project cannot be read the bucket waits: it must not be made on a server that may not be allowed.
	required, err := platform.ProjectRequiredTags(ctx, r.Client, store.Spec.Project)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if message := platform.ComplianceMismatchMessage("object storage server", pinnedStorePlacement(server), required); message != "" {
		return r.setNotReady(ctx, store, message)
	}
	admin, err := r.bucketAdminFor(ctx, server)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if err := r.markProvisioningStarted(ctx, store); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	for _, bucket := range serverBuckets {
		if err := admin.EnsureBucket(bucket); err != nil {
			return r.setNotReady(ctx, store, err.Error())
		}
	}
	existing := &corev1.Secret{}
	existingAccess, existingSecret := "", ""
	if err := r.Get(ctx, client.ObjectKey{Name: store.Name + "-connection", Namespace: wsNS}, existing); err == nil {
		existingAccess = secretValue(existing, platform.ConnectionKeyAccessKey)
		existingSecret = secretValue(existing, platform.ConnectionKeySecretKey)
	} else if !apierrors.IsNotFound(err) {
		return r.setNotReady(ctx, store, err.Error())
	}
	userAccess, userSecret, err := admin.EnsureBucketUser(serverBuckets, store.Name, existingAccess, existingSecret)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if err := r.reconcileConnectionSecret(ctx, store, wsNS, server.Status.Endpoint, serverBuckets[0], userAccess, userSecret); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	return r.setReady(ctx, store, wsNS, server.Status.Endpoint, "Bucket is ready on the cluster's object storage")
}

// bucketAdminFor returns the client that makes buckets and users on the cluster's object storage server,
// logged in with the server's own credentials, which never leave the system namespace.
func (r *GeassObjectStoreReconciler) bucketAdminFor(ctx context.Context, server *geassv1alpha1.GeassObjectStore) (bucketAdmin, error) {
	if server.Status.Endpoint == "" {
		return nil, platform.UserError(platform.MessageObjectStorageStarting)
	}
	root, err := r.objectStoreRoot(ctx, server)
	if err != nil {
		return nil, err
	}
	backend, ok := objectStoreBackendFor(server.Spec.Engine)
	if !ok {
		return nil, fmt.Errorf("unsupported engine %q", server.Spec.Engine)
	}
	return backend.admin(r.HTTP, server.Status.Endpoint, root), nil
}

func (r *GeassObjectStoreReconciler) validateObjectStoreCloudConnection(ctx context.Context, store *geassv1alpha1.GeassObjectStore) error {
	if store.Spec.ConnectionRef == nil || strings.TrimSpace(store.Spec.ConnectionRef.Name) == "" {
		return nil
	}
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := r.Get(ctx, client.ObjectKey{Name: store.Spec.ConnectionRef.Name, Namespace: platform.SystemNamespace}, connection); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("cloud connection %q was not found", store.Spec.ConnectionRef.Name)
		}
		return fmt.Errorf("cloud connection %q is unavailable", store.Spec.ConnectionRef.Name)
	}
	return platform.ValidateCloudConnectionForProject(*connection, store.Spec.Project)
}

func (r *GeassObjectStoreReconciler) deleteExternalStore(ctx context.Context, store *geassv1alpha1.GeassObjectStore) error {
	if store.Spec.ConnectionRef == nil || store.Spec.ConnectionRef.Name == "" {
		return fmt.Errorf("AWS connection is required to delete this store")
	}
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := r.Get(ctx, client.ObjectKey{Name: store.Spec.ConnectionRef.Name, Namespace: platform.SystemNamespace}, connection); err != nil {
		return fmt.Errorf("AWS connection is unavailable: %w", err)
	}
	if err := platform.ValidateCloudConnectionForProject(*connection, store.Spec.Project); err != nil {
		return err
	}
	if connection.Spec.Provider != geassv1alpha1.CloudProviderAWS || connection.Spec.SecretRef.Name == "" {
		return fmt.Errorf("AWS connection is unavailable")
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: platform.SystemNamespace}, secret); err != nil {
		return fmt.Errorf("AWS credentials are unavailable: %w", err)
	}
	accessKey := secretValue(secret, platform.SecretKeyAccessKeyID)
	secretKey := secretValue(secret, platform.SecretKeySecretAccessKey)
	if accessKey == "" || secretKey == "" {
		return fmt.Errorf("AWS credentials are unavailable")
	}
	aws := &cloud.AWSClient{
		HTTP:      r.HTTP,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Region:    firstNonEmpty(store.Spec.Region, connection.Spec.Region, secretValue(secret, platform.SecretKeyRegion), "us-east-1"),
	}
	return r.deleteExternalAWS(store, aws)
}

func (r *GeassObjectStoreReconciler) deleteExternalAWS(store *geassv1alpha1.GeassObjectStore, aws *cloud.AWSClient) error {
	if store.Spec.CreateBucket {
		// Names that are missing or not valid mean no bucket was created, so none is removed: never one that
		// merely shares the resource's name.
		buckets, _ := objectStoreBucketNames(store)
		for _, bucket := range buckets {
			if err := aws.DeleteBucket(bucket); err != nil {
				return err
			}
		}
	}
	return aws.DeleteBucketUser(store.Name)
}

func (r *GeassObjectStoreReconciler) deleteInClusterBuckets(ctx context.Context, store *geassv1alpha1.GeassObjectStore) error {
	if !platform.ObjectStoreInfrastructureActive(store) {
		return nil
	}
	buckets, err := inClusterBucketNames(store)
	if err != nil || len(buckets) == 0 {
		// Names that cannot be stored, or none, mean no bucket was created, so none is removed: never one that
		// merely shares the resource's name.
		return nil
	}
	server, err := r.clusterObjectStore(ctx)
	if err != nil {
		if errors.Is(err, errClusterObjectStoreMissing) {
			return nil
		}
		return err
	}
	admin, err := r.bucketAdminFor(ctx, server)
	if err != nil {
		return err
	}
	for _, bucket := range buckets {
		if err := admin.DeleteBucket(bucket); err != nil {
			return err
		}
	}
	return admin.DeleteBucketUser(store.Name)
}

// clusterObjectStore returns the cluster's object storage server, or errClusterObjectStoreMissing. A server that is
// being removed is still the server until its finalizer is gone: a bucket deleted at that moment must still clean up
// on it, or it would leave its bucket and login behind.
func (r *GeassObjectStoreReconciler) clusterObjectStore(ctx context.Context) (*geassv1alpha1.GeassObjectStore, error) {
	var list geassv1alpha1.GeassObjectStoreList
	if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil, err
	}
	for i := range list.Items {
		if platform.IsClusterObjectStoreServer(&list.Items[i]) {
			return &list.Items[i], nil
		}
	}
	return nil, errClusterObjectStoreMissing
}

// attachedProjectBucketNames are the project buckets that live on the cluster's object storage server, which cannot
// be removed while any of them exists.
func (r *GeassObjectStoreReconciler) attachedProjectBucketNames(ctx context.Context) ([]string, error) {
	var list geassv1alpha1.GeassObjectStoreList
	if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil, err
	}
	var names []string
	for i := range list.Items {
		item := &list.Items[i]
		if isClusterObjectStore(item) || !item.DeletionTimestamp.IsZero() {
			continue
		}
		if objectStorePlacement(item) == geassv1alpha1.ObjectStorePlacementExternal {
			continue
		}
		names = append(names, item.Name)
	}
	return names, nil
}

func (r *GeassObjectStoreReconciler) reconcileExternal(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS string) (ctrl.Result, error) {
	buckets, err := objectStoreBucketNames(store)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if len(buckets) == 0 {
		return r.setNotReady(ctx, store, "Configure the bucket name in settings, then deploy")
	}
	if store.Spec.ConnectionRef == nil || store.Spec.ConnectionRef.Name == "" {
		return r.setNotReady(ctx, store, "an AWS connection is required for external buckets")
	}
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := r.Get(ctx, client.ObjectKey{Name: store.Spec.ConnectionRef.Name, Namespace: platform.SystemNamespace}, connection); err != nil {
		return r.setNotReady(ctx, store, "AWS connection is unavailable")
	}
	if err := platform.ValidateCloudConnectionForProject(*connection, store.Spec.Project); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if connection.Spec.Provider != geassv1alpha1.CloudProviderAWS {
		return r.setNotReady(ctx, store, "external buckets require an AWS connection")
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: platform.SystemNamespace}, secret); err != nil {
		return r.setNotReady(ctx, store, "AWS credentials are unavailable")
	}
	accessKey := secretValue(secret, platform.SecretKeyAccessKeyID)
	secretKey := secretValue(secret, platform.SecretKeySecretAccessKey)
	region := firstNonEmpty(store.Spec.Region, connection.Spec.Region, secretValue(secret, platform.SecretKeyRegion), "us-east-1")
	if accessKey == "" || secretKey == "" {
		return r.setNotReady(ctx, store, "AWS access key and secret key are required")
	}
	aws := &cloud.AWSClient{HTTP: r.HTTP, AccessKey: accessKey, SecretKey: secretKey, Region: region}
	if err := r.markProvisioningStarted(ctx, store); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if store.Spec.CreateBucket {
		for _, bucket := range buckets {
			if err := aws.EnsureBucket(bucket); err != nil {
				return r.setNotReady(ctx, store, err.Error())
			}
		}
	}
	existing := &corev1.Secret{}
	existingAccess, existingSecret := "", ""
	if err := r.Get(ctx, client.ObjectKey{Name: store.Name + "-connection", Namespace: wsNS}, existing); err == nil {
		existingAccess = secretValue(existing, platform.ConnectionKeyAccessKey)
		existingSecret = secretValue(existing, platform.ConnectionKeySecretKey)
	} else if !apierrors.IsNotFound(err) {
		return r.setNotReady(ctx, store, err.Error())
	}
	userAccess, userSecret, err := aws.EnsureBucketUser(buckets, store.Name, existingAccess, existingSecret)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	endpoint := fmt.Sprintf("https://s3.%s.amazonaws.com", region)
	if err := r.reconcileConnectionSecret(ctx, store, wsNS, endpoint, buckets[0], userAccess, userSecret); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	return r.setReady(ctx, store, wsNS, endpoint, "AWS S3 bucket is ready")
}

func (r *GeassObjectStoreReconciler) reconcileConnectionSecret(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS, endpoint, bucket, accessKey, secretKey string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: store.Name + "-connection", Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, store, "GeassObjectStore")
		setSecretStrings(secret, map[string]string{
			platform.ConnectionKeyEndpoint:  endpoint,
			platform.ConnectionKeyAccessKey: accessKey,
			platform.ConnectionKeySecretKey: secretKey,
			platform.ConnectionKeyBucket:    bucket,
		})
		return setSameNamespaceOwner(store, secret, r.Scheme)
	})
	return err
}

func (r *GeassObjectStoreReconciler) setReady(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS, endpoint, message string) (ctrl.Result, error) {
	latest := &geassv1alpha1.GeassObjectStore{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(store), latest); err != nil {
		return ctrl.Result{}, err
	}
	_, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.TargetNamespace = wsNS
		latest.Status.ConnectionSecret = store.Name + "-connection"
		latest.Status.Endpoint = endpoint
		retryState := reconcileRetryFromAPI(&latest.Status.ReconcileRetry)
		platform.ClearReconcileRetry(&retryState)
		writeReconcileRetryToAPI(&latest.Status.ReconcileRetry, retryState)
		completeProvisionSuccess(&latest.Status.Provision)
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "ObjectStoreReady", message)
	})
	return ctrl.Result{}, err
}

func (r *GeassObjectStoreReconciler) setNotReady(ctx context.Context, store *geassv1alpha1.GeassObjectStore, message string) (ctrl.Result, error) {
	latest := &geassv1alpha1.GeassObjectStore{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(store), latest); err != nil {
		return ctrl.Result{}, err
	}
	var result ctrl.Result
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		var conditions []metav1.Condition
		result, conditions = finishNotReady(latest.Status.Conditions, &latest.Status.ReconcileRetry, &latest.Status.Provision, latest.Generation, message)
		latest.Status.Conditions = conditions
	}); err != nil {
		return ctrl.Result{}, err
	}
	return result, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassObjectStoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	enqueueStores := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []ctrl.Request {
		var list geassv1alpha1.GeassObjectStoreList
		if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
			return nil
		}
		requests := make([]ctrl.Request, 0, len(list.Items))
		for i := range list.Items {
			store := &list.Items[i]
			requests = append(requests, ctrl.Request{NamespacedName: types.NamespacedName{Name: store.Name, Namespace: store.Namespace}})
		}
		return requests
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassObjectStore{}).
		// The server's network policy is put back if it is changed or deleted by hand.
		Owns(&networkingv1.NetworkPolicy{}).
		// The server coming up, or going down, changes whether the buckets that wait for it can be made.
		Watches(&appsv1.Deployment{}, enqueueStores, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetNamespace() == platform.SystemNamespace && obj.GetName() == platform.ObjectStoreServerName
		}))).
		// A project changing its region or required tags changes which server it may use.
		Watches(&geassv1alpha1.GeassProject{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, project client.Object) []ctrl.Request {
			var list geassv1alpha1.GeassObjectStoreList
			if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
				return nil
			}
			var requests []ctrl.Request
			for i := range list.Items {
				if list.Items[i].Spec.Project == project.GetName() {
					requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
				}
			}
			return requests
		}), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("geassobjectstore").
		Complete(r)
}
