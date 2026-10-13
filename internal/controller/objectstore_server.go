package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilptr "k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// The cluster's object storage server is a Deployment of one pod with a volume and a Service, all in the
// system namespace and all named platform.ObjectStoreServerName. Geass runs it directly, not through a chart:
// nothing about it needs one, and a chart is one more thing to fetch and to fall out of step with.

const (
	// annotationServerSpecHash records what the server's Deployment was last made from, so that a server that
	// has not changed is not written to on every pass.
	annotationServerSpecHash = "geass.dev/object-store-spec-hash"
	// objectStoreConfigSecretName holds the files the server reads at start.
	objectStoreConfigSecretName = platform.ObjectStoreServerName + "-config"
	// objectStoreStartRetry is how soon to look again at a server that has not come up. The Deployment's own
	// status wakes the reconcile, so this only covers a watch that was missed.
	objectStoreStartRetry = time.Minute
	// objectStoreStopGrace is how long, in seconds, a server has to stop before it is killed.
	objectStoreStopGrace int64 = 60
)

func objectStoreServerKey() client.ObjectKey {
	return client.ObjectKey{Name: platform.ObjectStoreServerName, Namespace: platform.SystemNamespace}
}

func objectStoreServerLabels() map[string]string {
	return map[string]string{platform.K8sLabelAppName: platform.ObjectStoreServerName}
}

func (r *GeassObjectStoreReconciler) reconcileClusterServer(ctx context.Context, store *geassv1alpha1.GeassObjectStore, backend objectStoreBackend, log interface{ Info(string, ...any) }) (ctrl.Result, error) {
	if patched, err := r.ensureServerResourceSpec(ctx, store); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	} else if patched {
		return ctrl.Result{Requeue: true}, nil
	}
	root, err := r.ensureObjectStoreRoot(ctx, store, backend)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	placement, pinnedNow, err := r.clusterServerPlacement(ctx, store)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if pinnedNow {
		// The pod is built from the stored server, so let it see the pin first.
		return ctrl.Result{Requeue: true}, nil
	}
	est := platform.EstimateFromResources("Object storage server", objectStoreServerResources(store), 1)
	if ok, msg := workloadFitsPlacementUnlessCreated(ctx, r.Client, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerName, Namespace: platform.SystemNamespace}}, est, placement); !ok {
		return r.setNotReady(ctx, store, msg)
	}
	storageClass, wait, err := r.serverStorageClass(ctx, store, placement)
	if err != nil {
		return r.setNotReady(ctx, store, storageFailureMessage(err))
	}
	if wait {
		// Longhorn is still being installed, and a volume made now could never leave the local storage it would get.
		return r.setNotReadyAndLookAgain(ctx, store, messageStorageStarting)
	}
	if err := r.applyObjectStoreServer(ctx, store, backend, root, placement, storageClass); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, objectStoreServerKey(), deploy); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if !deploymentReady(deploy, 1) {
		log.Info("Waiting for the object storage server", "engine", store.Spec.Engine)
		return r.setNotReadyAndLookAgain(ctx, store, platform.MessageObjectStorageStarting)
	}
	endpoint := platform.ObjectStoreServerEndpoint(store.Spec.Engine)
	if err := r.reconcileConnectionSecret(ctx, store, platform.SystemNamespace, endpoint, store.Name, root.accessKey, root.secretKey); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	return r.setReady(ctx, store, platform.SystemNamespace, endpoint, "Cluster object storage is ready")
}

// objectStoreRoot reads the server's own login and secrets from its root Secret.
func (r *GeassObjectStoreReconciler) objectStoreRoot(ctx context.Context, server *geassv1alpha1.GeassObjectStore) (objectStoreRoot, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: objectStoreRootSecretName(server.Name), Namespace: platform.SystemNamespace}, secret); err != nil {
		return objectStoreRoot{}, platform.UserError(platform.MessageObjectStorageCredentials)
	}
	root := rootFromSecret(secret)
	if root.accessKey == "" || root.secretKey == "" {
		return objectStoreRoot{}, platform.UserError(platform.MessageObjectStorageCredentials)
	}
	return root, nil
}

func rootFromSecret(secret *corev1.Secret) objectStoreRoot {
	return objectStoreRoot{
		accessKey:    secretValue(secret, rootKeyAccess),
		secretKey:    secretValue(secret, rootKeySecret),
		adminToken:   secretValue(secret, rootKeyAdminToken),
		rpcSecret:    secretValue(secret, rootKeyRPCSecret),
		metricsToken: secretValue(secret, rootKeyMetricsToken),
	}
}

// ensureObjectStoreRoot makes the server's root Secret, generating what it lacks and keeping what it has,
// so the login a server was started with is the one it keeps.
func (r *GeassObjectStoreReconciler) ensureObjectStoreRoot(ctx context.Context, store *geassv1alpha1.GeassObjectStore, backend objectStoreBackend) (objectStoreRoot, error) {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: objectStoreRootSecretName(store.Name), Namespace: platform.SystemNamespace}}
	var root objectStoreRoot
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, store, "GeassObjectStore")
		root = rootFromSecret(secret)
		if err := backend.fillRoot(&root); err != nil {
			return err
		}
		values := map[string]string{rootKeyAccess: root.accessKey, rootKeySecret: root.secretKey}
		for key, value := range map[string]string{rootKeyAdminToken: root.adminToken, rootKeyRPCSecret: root.rpcSecret, rootKeyMetricsToken: root.metricsToken} {
			if value != "" {
				values[key] = value
			}
		}
		setSecretStrings(secret, values)
		return setSameNamespaceOwner(store, secret, r.Scheme)
	})
	return root, err
}

// clusterServerPlacement decides, once, which nodes the cluster's object storage server runs on: its volume
// lives there. The choice is the server's region (or the cluster default) and the tags it asks for, recorded
// on the server like a database's. pinnedNow says the decision was just recorded.
func (r *GeassObjectStoreReconciler) clusterServerPlacement(ctx context.Context, store *geassv1alpha1.GeassObjectStore) (placement platform.Placement, pinnedNow bool, err error) {
	if pinned, ok := store.Annotations[platform.AnnotationNodePlacement]; ok {
		return platform.ParsePlacement(pinned), false, nil
	}
	region := store.Spec.ServerRegion
	if region == "" {
		if region, err = platform.DefaultRegion(ctx, r.Client, ""); err != nil {
			return platform.Placement{}, false, err
		}
	}
	placement = platform.NewPlacement(region, store.Spec.RunOn.Tags)
	latest := &geassv1alpha1.GeassObjectStore{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(store), latest); err != nil {
		return platform.Placement{}, false, err
	}
	base := latest.DeepCopy()
	if latest.Annotations == nil {
		latest.Annotations = map[string]string{}
	}
	latest.Annotations[platform.AnnotationNodePlacement] = placement.Encode()
	if err := r.Patch(ctx, latest, client.MergeFrom(base)); err != nil {
		return platform.Placement{}, false, err
	}
	return placement, true, nil
}

func objectStoreServerResources(server *geassv1alpha1.GeassObjectStore) corev1.ResourceRequirements {
	if server != nil && len(server.Spec.Resources.Requests) > 0 {
		return server.Spec.Resources
	}
	engine := geassv1alpha1.GeassObjectStoreEngine("")
	if server != nil {
		engine = server.Spec.Engine
	}
	return platform.DefaultObjectStoreResources(engine)
}

// ensureServerResourceSpec writes the default size and volume size into a server that was made without them, so that
// what it runs with is always on the resource.
func (r *GeassObjectStoreReconciler) ensureServerResourceSpec(ctx context.Context, store *geassv1alpha1.GeassObjectStore) (bool, error) {
	if store == nil || (len(store.Spec.Resources.Requests) > 0 && store.Spec.StorageSize != nil) {
		return false, nil
	}
	original := store.DeepCopy()
	if len(store.Spec.Resources.Requests) == 0 {
		store.Spec.Resources = platform.DefaultObjectStoreResources(store.Spec.Engine)
	}
	if store.Spec.StorageSize == nil {
		size := serverVolumeSizeNow(ctx, r.Client, store)
		store.Spec.StorageSize = &size
	}
	if err := r.Patch(ctx, store, client.MergeFrom(original)); err != nil {
		return false, err
	}
	return true, nil
}

// serverVolumeSizeNow is the volume size to write into a server that has none: the size of the volume it already
// has, so that a server from before the size could be chosen is not asked to shrink or grow, else the default.
func serverVolumeSizeNow(ctx context.Context, c client.Reader, store *geassv1alpha1.GeassObjectStore) resource.Quantity {
	claim := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, objectStoreServerKey(), claim); err == nil {
		if size, ok := claim.Spec.Resources.Requests[corev1.ResourceStorage]; ok && !size.IsZero() {
			return size
		}
	}
	return platform.ObjectStoreServerStorage(store)
}

// serverPodTemplate is the pod the server runs in: the backend's container, with the volume, the config, the
// size, and where it may run.
func serverPodTemplate(pod serverPod, resources corev1.ResourceRequirements, placement platform.Placement, configHash string) corev1.PodTemplateSpec {
	container := pod.container
	container.Resources = resources
	container.ImagePullPolicy = corev1.PullIfNotPresent
	container.SecurityContext = serverSecurityContext()
	container.VolumeMounts = []corev1.VolumeMount{
		{Name: "data", MountPath: objectStoreDataPath},
		{Name: "config", MountPath: pod.configPath, ReadOnly: true},
	}
	spec := corev1.PodSpec{
		Containers: append([]corev1.Container{container}, pod.sidecars...),
		Volumes: []corev1.Volume{
			{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: platform.ObjectStoreServerName}}},
			{Name: "config", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: objectStoreConfigSecretName}}},
		},
		// The volume is made writable for the user the server runs as.
		SecurityContext: &corev1.PodSecurityContext{FSGroup: utilptr.To(objectStoreRunAsUser), RunAsNonRoot: utilptr.To(true)},
		// SeaweedFS takes about half a minute to stop cleanly, which is all of Kubernetes' default grace period. A server
		// that is killed instead leaves its LevelDB store to be recovered, and the pod that replaces it cannot open the
		// store until the old one lets go of it.
		TerminationGracePeriodSeconds: utilptr.To(objectStoreStopGrace),
		AutomountServiceAccountToken:  utilptr.To(false),
		EnableServiceLinks:            utilptr.To(false),
	}
	placement.ApplyToPodSpec(&spec)
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{platform.K8sLabelAppName: platform.ObjectStoreServerName, platform.LabelManagedBy: platform.ManagedByValue},
			// A change to the files the server reads at start restarts it.
			Annotations: map[string]string{"geass.dev/config-hash": configHash},
		},
		Spec: spec,
	}
}

func hashFiles(files map[string]string) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	sum := sha256.New()
	for _, name := range names {
		_, _ = fmt.Fprintf(sum, "%s\x00%s\x00", name, files[name])
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}

// applyObjectStoreServer makes the server's config, volume, Deployment and Service match what the store asks for.
func (r *GeassObjectStoreReconciler) applyObjectStoreServer(ctx context.Context, store *geassv1alpha1.GeassObjectStore, backend objectStoreBackend, root objectStoreRoot, placement platform.Placement, storageClass string) error {
	pod := backend.pod(root, objectStoreRootSecretName(store.Name))

	config := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: objectStoreConfigSecretName, Namespace: platform.SystemNamespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, config, func() error {
		applyGeassLabels(config, store, "GeassObjectStore")
		setSecretStrings(config, pod.files)
		return setSameNamespaceOwner(store, config, r.Scheme)
	}); err != nil {
		return err
	}

	// The volume is made once, with the class and size it will keep. Its class cannot change afterwards, and its size
	// can only grow, which is done below.
	size := platform.ObjectStoreServerStorage(store)
	claim := &corev1.PersistentVolumeClaim{}
	if err := r.Get(ctx, objectStoreServerKey(), claim); apierrors.IsNotFound(err) {
		claim = &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerName, Namespace: platform.SystemNamespace}}
		applyGeassLabels(claim, store, "GeassObjectStore")
		claim.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
		claim.Spec.Resources.Requests = corev1.ResourceList{corev1.ResourceStorage: size}
		claim.Spec.StorageClassName = stringPtrOrNil(storageClass)
		if err := setSameNamespaceOwner(store, claim, r.Scheme); err != nil {
			return err
		}
		if err := r.Create(ctx, claim); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
	} else if err != nil {
		return err
	} else if err := r.growServerVolume(ctx, store, claim, size); err != nil {
		return err
	}

	if policy := platform.ObjectStoreServerPolicy(store.Spec.Engine); policy != nil {
		current := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: policy.Name, Namespace: policy.Namespace}}
		if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, current, func() error {
			current.Labels = policy.Labels
			current.Spec = policy.Spec
			return setSameNamespaceOwner(store, current, r.Scheme)
		}); err != nil {
			return err
		}
	}

	template := serverPodTemplate(pod, objectStoreServerResources(store), placement, hashFiles(pod.files))
	templateJSON, err := json.Marshal(template)
	if err != nil {
		return err
	}
	specHash := hashFiles(map[string]string{"template": string(templateJSON)})
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerName, Namespace: platform.SystemNamespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		applyGeassLabels(deploy, store, "GeassObjectStore")
		if deploy.Annotations == nil {
			deploy.Annotations = map[string]string{}
		}
		if deploy.Annotations[annotationServerSpecHash] != specHash {
			deploy.Spec.Replicas = utilptr.To(int32(1))
			// The volume can only be mounted by one pod, so the old pod goes before the new one starts.
			deploy.Spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
			deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: objectStoreServerLabels()}
			deploy.Spec.Template = template
			deploy.Annotations[annotationServerSpecHash] = specHash
		}
		return setSameNamespaceOwner(store, deploy, r.Scheme)
	}); err != nil {
		return err
	}

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerName, Namespace: platform.SystemNamespace}}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		applyGeassLabels(svc, store, "GeassObjectStore")
		svc.Spec.Selector = objectStoreServerLabels()
		svc.Spec.Ports = pod.ports
		return setSameNamespaceOwner(store, svc, r.Scheme)
	})
	return err
}

// removeObjectStoreServer deletes everything the cluster's object storage server made, its data included.
func (r *GeassObjectStoreReconciler) removeObjectStoreServer(ctx context.Context, store *geassv1alpha1.GeassObjectStore) error {
	system := platform.SystemNamespace
	for _, object := range []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerName, Namespace: system}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerName, Namespace: system}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerName, Namespace: system}},
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: platform.ObjectStoreServerPolicyName, Namespace: system}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: objectStoreConfigSecretName, Namespace: system}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: objectStoreRootSecretName(store.Name), Namespace: system}},
	} {
		if err := r.Delete(ctx, object); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	return nil
}

// serverStorageClass decides, once, which storage class the server's volume is created with, and remembers it on the
// server. A volume's class cannot change after it exists, so recomputing it later, with Longhorn newly installed or
// the node count changed, would only produce a value that cannot be applied. A server that already has a volume keeps
// the class that volume has. wait is true while Longhorn is still being installed and the answer is not known yet.
func (r *GeassObjectStoreReconciler) serverStorageClass(ctx context.Context, store *geassv1alpha1.GeassObjectStore, placement platform.Placement) (class string, wait bool, err error) {
	if pinned, ok := store.Annotations[platform.AnnotationStorageClass]; ok {
		return pinned, false, nil
	}
	claim := &corev1.PersistentVolumeClaim{}
	switch err := r.Get(ctx, objectStoreServerKey(), claim); {
	case err == nil:
		existing := utilptr.Deref(claim.Spec.StorageClassName, "")
		return existing, false, r.pinServerStorageClass(ctx, store, existing)
	case !apierrors.IsNotFound(err):
		return "", false, err
	}
	class, wait, err = volumeClassFor(ctx, r.Client, platform.ObjectStoreServerStorageClass(placement))
	if err != nil || wait {
		return "", wait, err
	}
	return class, false, r.pinServerStorageClass(ctx, store, class)
}

// pinServerStorageClass records the decision on the server. It patches only the annotation, from a fresh read.
func (r *GeassObjectStoreReconciler) pinServerStorageClass(ctx context.Context, store *geassv1alpha1.GeassObjectStore, class string) error {
	latest := &geassv1alpha1.GeassObjectStore{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(store), latest); err != nil {
		return err
	}
	base := latest.DeepCopy()
	if latest.Annotations == nil {
		latest.Annotations = map[string]string{}
	}
	latest.Annotations[platform.AnnotationStorageClass] = class
	if err := r.Patch(ctx, latest, client.MergeFrom(base)); err != nil {
		return err
	}
	if store.Annotations == nil {
		store.Annotations = map[string]string{}
	}
	store.Annotations[platform.AnnotationStorageClass] = class
	return nil
}

// growServerVolume raises the volume's size to the one the server asks for. A volume is never shrunk, and it is only
// grown where its class can be resized: the API server refuses a resize otherwise, which would fail every pass. The
// dashboard does not offer a size the class cannot take, so this is only the last check. A size that cannot be given
// is put back on the server, so that what the resource says is the size the volume has, not one it never will.
func (r *GeassObjectStoreReconciler) growServerVolume(ctx context.Context, store *geassv1alpha1.GeassObjectStore, claim *corev1.PersistentVolumeClaim, want resource.Quantity) error {
	current := claim.Spec.Resources.Requests[corev1.ResourceStorage]
	if !claim.DeletionTimestamp.IsZero() {
		return nil
	}
	if want.Cmp(current) > 0 {
		allowed, err := r.classAllowsExpansion(ctx, utilptr.Deref(claim.Spec.StorageClassName, ""))
		if err != nil {
			return err
		}
		if allowed {
			base := claim.DeepCopy()
			claim.Spec.Resources.Requests[corev1.ResourceStorage] = want
			return r.Patch(ctx, claim, client.MergeFrom(base))
		}
	}
	if store.Spec.StorageSize != nil && store.Spec.StorageSize.Cmp(current) != 0 {
		logf.FromContext(ctx).Info("Kept the object storage volume at its size", "volume", current.String(), "asked", store.Spec.StorageSize.String())
		base := store.DeepCopy()
		store.Spec.StorageSize = &current
		return r.Patch(ctx, store, client.MergeFrom(base))
	}
	return nil
}

// classAllowsExpansion says whether volumes of a storage class can be resized. "" is the cluster's default class.
func (r *GeassObjectStoreReconciler) classAllowsExpansion(ctx context.Context, name string) (bool, error) {
	if name != "" {
		class := &storagev1.StorageClass{}
		if err := r.Get(ctx, client.ObjectKey{Name: name}, class); err != nil {
			return false, client.IgnoreNotFound(err)
		}
		return utilptr.Deref(class.AllowVolumeExpansion, false), nil
	}
	var classes storagev1.StorageClassList
	if err := r.List(ctx, &classes); err != nil {
		return false, err
	}
	for i := range classes.Items {
		annotations := classes.Items[i].Annotations
		if annotations["storageclass.kubernetes.io/is-default-class"] == "true" || annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true" {
			return utilptr.Deref(classes.Items[i].AllowVolumeExpansion, false), nil
		}
	}
	return false, nil
}

// setNotReadyAndLookAgain is setNotReady for a wait that nothing wakes the reconcile from: the pod's own status does,
// but Longhorn coming up does not, so it asks to be visited again.
func (r *GeassObjectStoreReconciler) setNotReadyAndLookAgain(ctx context.Context, store *geassv1alpha1.GeassObjectStore, message string) (ctrl.Result, error) {
	result, err := r.setNotReady(ctx, store, message)
	if err == nil && result.RequeueAfter == 0 {
		result.RequeueAfter = objectStoreStartRetry
	}
	return result, err
}
