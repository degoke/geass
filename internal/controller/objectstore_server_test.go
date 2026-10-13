package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// clusterServerReconciler is a reconciler for the cluster's object storage server, over a client that logs the
// writes made to it, and the server resource to reconcile.
func clusterServerReconciler(t *testing.T, engine geassv1alpha1.GeassObjectStoreEngine, extra ...client.Object) (*GeassObjectStoreReconciler, *geassv1alpha1.GeassObjectStore, *writeLog, client.Client) {
	t.Helper()
	scheme := objectStoreDeployScheme(t)
	server := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassObjectStoreSpec{Engine: engine, Placement: geassv1alpha1.ObjectStorePlacementInCluster},
	}
	return clusterServerReconcilerFor(t, scheme, server, extra...)
}

// clusterServerReconcilerFor is clusterServerReconciler for a server that has been given a spec.
func clusterServerReconcilerFor(t *testing.T, scheme *runtime.Scheme, server *geassv1alpha1.GeassObjectStore, extra ...client.Object) (*GeassObjectStoreReconciler, *geassv1alpha1.GeassObjectStore, *writeLog, client.Client) {
	t.Helper()
	log := &writeLog{}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(append([]client.Object{server}, extra...)...).
		WithStatusSubresource(&geassv1alpha1.GeassObjectStore{}, &appsv1.Deployment{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				log.record("create", obj)
				return c.Create(ctx, obj, opts...)
			},
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				log.record("update", obj)
				return c.Update(ctx, obj, opts...)
			},
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				log.record("patch", obj)
				return c.Patch(ctx, obj, patch, opts...)
			},
			SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				log.record("update "+sub+" of", obj)
				return c.SubResource(sub).Update(ctx, obj, opts...)
			},
			SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				log.record("patch "+sub+" of", obj)
				return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
			},
		}).Build()
	return &GeassObjectStoreReconciler{Client: c, Scheme: scheme}, server, log, c
}

func reconcileServer(t *testing.T, r *GeassObjectStoreReconciler, server *geassv1alpha1.GeassObjectStore, times int) {
	t.Helper()
	for range times {
		_, err := r.Reconcile(context.Background(), reconcileRequest(server))
		require.NoError(t, err)
	}
}

// markServerPodUp plays the part of the Deployment controller, which a fake client has none of.
func markServerPodUp(t *testing.T, c client.Client) {
	t.Helper()
	var deploy appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), objectStoreServerKey(), &deploy))
	deploy.Status.ObservedGeneration = deploy.Generation
	deploy.Status.Replicas, deploy.Status.ReadyReplicas, deploy.Status.AvailableReplicas, deploy.Status.UpdatedReplicas = 1, 1, 1, 1
	require.NoError(t, c.Status().Update(context.Background(), &deploy))
}

// A server that is up and unchanged must cost the API server nothing: no writes of any kind, status included.
// This is what keeps the operator from rewriting what it owns every time anything wakes it.
func TestAReadyObjectStorageServerIsNotWrittenToAgain(t *testing.T) {
	for _, engine := range objectStoreEngines {
		t.Run(string(engine), func(t *testing.T) {
			r, server, log, c := clusterServerReconciler(t, engine)

			// A finalizer, then the default size, then where it runs, each a pass of its own, then the workload.
			reconcileServer(t, r, server, 4)
			markServerPodUp(t, c)
			reconcileServer(t, r, server, 1)
			var stored geassv1alpha1.GeassObjectStore
			require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(server), &stored))
			require.True(t, platform.IsConditionTrue(stored.Status.Conditions, platform.ConditionReady), readyConditionMessage(stored.Status.Conditions))
			log.reset()

			reconcileServer(t, r, server, 3)

			require.Empty(t, log.writes, "three more reconciles of an unchanged, ready server wrote: %v", log.writes)
		})
	}
}

// What a server needs is made once and then kept, down to the volume.
func TestTheObjectStorageServerMakesItsWorkloadsOnce(t *testing.T) {
	r, server, log, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineSeaweedFS)

	reconcileServer(t, r, server, 4)
	reconcileServer(t, r, server, 2)

	creations := map[string]int{}
	for _, write := range log.writes {
		if name, ok := strings.CutPrefix(write, "create "); ok {
			creations[name]++
		}
	}
	// The Deployment, the Service, the volume claim and the network policy share the server's name; the config and the
	// root login each have their own Secret.
	require.Equal(t, map[string]int{
		"/" + platform.ObjectStoreServerName:         4,
		"/" + objectStoreConfigSecretName:            1,
		"/" + objectStoreRootSecretName(server.Name): 1,
	}, withoutZero(creations))
	var claim corev1.PersistentVolumeClaim
	require.NoError(t, c.Get(context.Background(), objectStoreServerKey(), &claim))
	require.Equal(t, platform.ObjectStoreServerVolumeSize, claim.Spec.Resources.Requests.Storage().String())
}

func withoutZero(counts map[string]int) map[string]int {
	out := map[string]int{}
	for name, count := range counts {
		if count > 0 {
			out[name] = count
		}
	}
	return out
}

func TestAnObjectStorageServerNotYetUpIsStarting(t *testing.T) {
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineGarage)

	reconcileServer(t, r, server, 4)

	var stored geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(server), &stored))
	require.False(t, platform.IsConditionTrue(stored.Status.Conditions, platform.ConditionReady))
	require.Equal(t, platform.MessageObjectStorageStarting, readyConditionMessage(stored.Status.Conditions))
	require.False(t, platform.ReconcileFailureShouldCount(platform.MessageObjectStorageStarting), "waiting for a pod is not a failure")
	require.False(t, platform.ReconcileFailureShouldCount(platform.MessageSetUpObjectStorage))
}

// A change to the files a server reads at start restarts it, and a change to its size does too, because both
// change the pod.
func TestAChangeToTheServersConfigOrSizeChangesItsPod(t *testing.T) {
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineSeaweedFS)
	reconcileServer(t, r, server, 4)
	var before appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), objectStoreServerKey(), &before))

	var stored geassv1alpha1.GeassObjectStore
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(server), &stored))
	stored.Spec.Resources = platform.DefaultObjectStoreResources(geassv1alpha1.ObjectStoreEngineGarage)
	require.NoError(t, c.Update(context.Background(), &stored))
	reconcileServer(t, r, server, 1)

	var after appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), objectStoreServerKey(), &after))
	require.NotEqual(t, before.Annotations[annotationServerSpecHash], after.Annotations[annotationServerSpecHash])
	require.Equal(t, "256Mi", after.Spec.Template.Spec.Containers[0].Resources.Requests.Memory().String())
}

func TestTheServersLoginIsGeneratedOnceAndKept(t *testing.T) {
	r, server, _, c := clusterServerReconciler(t, geassv1alpha1.ObjectStoreEngineGarage)
	reconcileServer(t, r, server, 4)
	key := client.ObjectKey{Name: objectStoreRootSecretName(server.Name), Namespace: platform.SystemNamespace}
	var first corev1.Secret
	require.NoError(t, c.Get(context.Background(), key, &first))

	reconcileServer(t, r, server, 2)

	var second corev1.Secret
	require.NoError(t, c.Get(context.Background(), key, &second))
	require.Equal(t, first.Data, second.Data, "a login that was handed out is never rotated by a reconcile")
	root := rootFromSecret(&second)
	require.Regexp(t, `^GK[0-9a-f]{32}$`, root.accessKey, "Garage wants a key that looks like its own")
	for name, secret := range map[string]string{"secret": root.secretKey, "rpc": root.rpcSecret, "admin": root.adminToken, "metrics": root.metricsToken} {
		require.Regexp(t, `^[0-9a-f]{64}$`, secret, name)
	}
}

// What each server is started with: where it listens, who can reach it, what it runs as, and where its
// secrets are. A server's login must never be in its arguments, where `ps` and the pod spec show it.
func TestTheServersAreStartedSafely(t *testing.T) {
	root := objectStoreRoot{accessKey: "AKEY", secretKey: "SECRETVALUE", adminToken: "ADMINTOKEN", rpcSecret: "RPCSECRET", metricsToken: "METRICS"}
	for _, engine := range objectStoreEngines {
		t.Run(string(engine), func(t *testing.T) {
			backend, ok := objectStoreBackendFor(engine)
			require.True(t, ok)
			pod := backend.pod(root, "cluster-s3-root")
			template := serverPodTemplate(pod, platform.DefaultObjectStoreResources(engine), platform.Placement{}, hashFiles(pod.files))
			container := template.Spec.Containers[0]
			for _, each := range template.Spec.Containers {
				require.True(t, *each.SecurityContext.RunAsNonRoot, each.Name)
				require.False(t, *each.SecurityContext.AllowPrivilegeEscalation, each.Name)
				require.Equal(t, []corev1.Capability{"ALL"}, each.SecurityContext.Capabilities.Drop, each.Name)
				require.Equal(t, corev1.PullIfNotPresent, each.ImagePullPolicy, each.Name)
				require.NotContains(t, each.Image, ":latest", each.Name)
			}

			command := strings.Join(append(append([]string{}, container.Command...), container.Args...), " ")
			for _, secret := range []string{root.secretKey, root.adminToken, root.rpcSecret, root.metricsToken} {
				require.NotContains(t, command, secret, "a secret is in the command line")
			}
			for _, env := range container.Env {
				for _, secret := range []string{root.secretKey, root.adminToken, root.rpcSecret, root.metricsToken} {
					require.NotEqual(t, secret, env.Value, "%s holds a secret as plain text", env.Name)
				}
			}
			require.Equal(t, objectStoreRunAsUser, *template.Spec.SecurityContext.FSGroup, "the volume is writable for the user it runs as")
			require.False(t, *template.Spec.AutomountServiceAccountToken, "the server has no use for a Kubernetes login")
			require.Equal(t, int64(60), *template.Spec.TerminationGracePeriodSeconds, "SeaweedFS needs about half a minute to stop cleanly")
			require.NotNil(t, container.ReadinessProbe)
			require.NotNil(t, container.StartupProbe)
			require.NotNil(t, container.LivenessProbe)
			var mounted []string
			for _, mount := range container.VolumeMounts {
				mounted = append(mounted, mount.MountPath)
			}
			require.ElementsMatch(t, []string{objectStoreDataPath, pod.configPath}, mounted)
		})
	}
}

// Only the S3 port, and Garage's admin port that Geass drives, are published.
func TestOnlyTheRightPortsArePublished(t *testing.T) {
	seaweed, _ := objectStoreBackendFor(geassv1alpha1.ObjectStoreEngineSeaweedFS)
	pod := seaweed.pod(objectStoreRoot{accessKey: "a", secretKey: "b"}, "root")
	require.Len(t, pod.ports, 1)
	require.Equal(t, int32(platform.SeaweedFSS3Port), pod.ports[0].Port)
	args := strings.Join(pod.container.Args, " ")
	require.Contains(t, args, "-s3.iam.readOnly=false", "Geass makes bucket logins through the IAM API")
	require.Contains(t, args, "-s3.port.iceberg=0")
	require.Contains(t, args, "-s3.port.lance=0")

	garage, _ := objectStoreBackendFor(geassv1alpha1.ObjectStoreEngineGarage)
	gpod := garage.pod(objectStoreRoot{}, "root")
	published := make([]int32, 0, len(gpod.ports))
	for _, port := range gpod.ports {
		published = append(published, port.Port)
	}
	require.ElementsMatch(t, []int32{platform.GarageS3Port, platform.GarageAdminPort}, published)
	require.Contains(t, gpod.files["garage.toml"], `rpc_bind_addr = "127.0.0.1:3901"`, "no node can join, so its RPC port is closed")
	require.Contains(t, gpod.files["garage.toml"], `s3_region = "us-east-1"`)
}

// SeaweedFS serves internal gRPC services next to S3 that need no login and accept a new admin identity from anyone
// who reaches them (found by testing a live server: a pod in another namespace minted itself an admin and listed every
// project's buckets). They cannot be switched off or bound apart from S3, so SeaweedFS listens only on localhost and a
// forwarder publishes S3 alone. This is the invariant that keeps one project out of another's data.
func TestOnlyTheForwarderIsReachableOnSeaweedFS(t *testing.T) {
	backend, _ := objectStoreBackendFor(geassv1alpha1.ObjectStoreEngineSeaweedFS)
	pod := backend.pod(objectStoreRoot{accessKey: "a", secretKey: "b"}, "root")

	args := strings.Join(pod.container.Args, " ")
	require.Contains(t, args, "-ip.bind=127.0.0.1")
	require.NotContains(t, args, "0.0.0.0", "nothing in SeaweedFS may listen on every address")
	require.NotContains(t, args, "-s3.ip.bind", "S3 follows -ip.bind onto localhost")
	require.Empty(t, pod.container.Ports, "the server itself publishes no port")
	require.Contains(t, args, "-s3.port=8334", "S3 listens on an internal port")

	require.Len(t, pod.sidecars, 1)
	forwarder := pod.sidecars[0]
	require.Equal(t, platform.S3ForwarderImage, forwarder.Image)
	require.Equal(t, []string{"TCP-LISTEN:8333,fork,reuseaddr", "TCP:127.0.0.1:8334"}, forwarder.Args)
	require.Len(t, forwarder.Ports, 1)
	require.Equal(t, int32(platform.SeaweedFSS3Port), forwarder.Ports[0].ContainerPort)
	require.True(t, *forwarder.SecurityContext.ReadOnlyRootFilesystem)

	template := serverPodTemplate(pod, platform.DefaultObjectStoreResources(geassv1alpha1.ObjectStoreEngineSeaweedFS), platform.Placement{}, "x")
	require.Len(t, template.Spec.Containers, 2)
	require.Equal(t, "seaweedfs", template.Spec.Containers[0].Name)
	require.Equal(t, "s3-forwarder", template.Spec.Containers[1].Name)
	require.Equal(t, "512Mi", template.Spec.Containers[0].Resources.Requests.Memory().String(), "the size is the server's, not the forwarder's")

	// The probes ask through the forwarder, so a pod is ready only when S3 can be reached the way an app reaches it.
	require.Equal(t, int32(platform.SeaweedFSS3Port), pod.container.ReadinessProbe.HTTPGet.Port.IntVal)
}

func TestGarageNeedsNoForwarder(t *testing.T) {
	backend, _ := objectStoreBackendFor(geassv1alpha1.ObjectStoreEngineGarage)

	pod := backend.pod(objectStoreRoot{}, "root")

	require.Empty(t, pod.sidecars)
	require.Len(t, serverPodTemplate(pod, platform.DefaultObjectStoreResources(geassv1alpha1.ObjectStoreEngineGarage), platform.Placement{}, "x").Spec.Containers, 1)
}

// SeaweedFS reads its one admin identity from a file, and Geass makes the others through the API.
func TestSeaweedFSStartsWithOnlyTheAdminIdentity(t *testing.T) {
	backend, _ := objectStoreBackendFor(geassv1alpha1.ObjectStoreEngineSeaweedFS)
	pod := backend.pod(objectStoreRoot{accessKey: "AKEY", secretKey: "SECRET"}, "root")

	var config struct {
		Identities []struct {
			Name        string
			Credentials []struct{ AccessKey, SecretKey string }
			Actions     []string
		}
	}
	require.NoError(t, json.Unmarshal([]byte(pod.files["s3.json"]), &config))
	require.Len(t, config.Identities, 1)
	require.Equal(t, "AKEY", config.Identities[0].Credentials[0].AccessKey)
	require.Contains(t, config.Identities[0].Actions, "Admin")
	require.Contains(t, pod.files["master.toml"], "copy_1 = 1", "a new bucket must not reserve seven volumes")
	require.Equal(t, "/etc/seaweedfs", pod.configPath, "SeaweedFS only finds master.toml in a few fixed directories")
	require.Contains(t, pod.container.Args, "-s3.config=/etc/seaweedfs/s3.json")
}

func TestFilesAreHashedByContent(t *testing.T) {
	a := hashFiles(map[string]string{"x": "1", "y": "2"})
	require.Equal(t, a, hashFiles(map[string]string{"y": "2", "x": "1"}), "order does not matter")
	require.NotEqual(t, a, hashFiles(map[string]string{"x": "1", "y": "3"}))
	require.NotEqual(t, hashFiles(map[string]string{"ab": "c"}), hashFiles(map[string]string{"a": "bc"}), "names and contents are told apart")
}

func TestOnlySeaweedFSAndGarageAreRunInTheCluster(t *testing.T) {
	for _, engine := range []geassv1alpha1.GeassObjectStoreEngine{geassv1alpha1.ObjectStoreEngineSeaweedFS, geassv1alpha1.ObjectStoreEngineGarage} {
		_, ok := objectStoreBackendFor(engine)
		require.True(t, ok, engine)
	}
	for _, engine := range []geassv1alpha1.GeassObjectStoreEngine{geassv1alpha1.ObjectStoreEngineS3, ""} {
		_, ok := objectStoreBackendFor(engine)
		require.False(t, ok, "%q is not run by Geass", engine)
	}
}
