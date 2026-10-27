/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilptr "k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/helmchart"
	"github.com/degoke/geass/pkg/platform"
)

// Geass runs the MySQL and Redis servers of its databases itself: one StatefulSet of one pod each, from the
// official image, with the Services in front of it. They used to be Bitnami charts, but Bitnami removed the
// images those charts name from Docker Hub, so a server could not start on a cluster that had not pulled them
// already. Nothing about a single server needs a chart (the object storage server and SQLite are run the same
// way), and running it directly takes a security patch from a Helm release that nobody updates to one constant.
//
// The servers keep the names and labels the charts gave them, so the host an app connects to, the pods a
// database's metrics and logs are found by, and its volume are the ones they always were.

const (
	redisContainerName = "redis"
	mysqlContainerName = "mysql"
	redisPort          = int32(6379)
	mysqlPort          = int32(3306)

	// serverRunAsUser is the user both images' own servers run as (redis and mysql are uid 999 in them), so
	// they start without ever being root.
	serverRunAsUser = int64(999)
	// serverVolumeName is the volume claim a server's data is on, which makes the claim data-<server>-0, as it was.
	serverVolumeName = "data"
	mysqlDataPath    = "/var/lib/mysql"
	// mysqlDataSubPath is where in its volume MySQL keeps its files. The root of a volume holds a lost+found
	// folder on many file systems, and a MySQL that is made fresh refuses to start beside it. A folder of its own
	// avoids that, and is where the Bitnami chart kept its files too.
	mysqlDataSubPath = "data"

	redisDataPath = "/data"

	messageReplacingChart = "Replacing the %s chart with Geass's own server"
	serverStopGrace       = int64(60)
)

// serverSpec is what one engine contributes to the StatefulSet and Services of its server.
type serverSpec struct {
	// engine is the name label of the pods, as the charts set it.
	engine string
	// component is the component label of the pods: master for Redis, primary for MySQL.
	component string
	// release is the instance label of the pods, and the first part of every name.
	release string
	// name is the StatefulSet; its one pod is name-0.
	name string
	// service is the Service apps connect to, and headless the one that gives the pod a stable name.
	service  string
	headless string
	port     int32
	portName string

	container corev1.Container
	volumes   []corev1.Volume
	// claim is the volume of the server, nil for one that keeps its data in memory.
	claim       *corev1.PersistentVolumeClaim
	podSecurity *corev1.PodSecurityContext
	stopGrace   int64
}

func (s serverSpec) selector() map[string]string {
	return map[string]string{
		platform.K8sLabelAppName:   s.engine,
		platform.K8sLabelInstance:  s.release,
		platform.K8sLabelComponent: s.component,
	}
}

// claimName is the volume claim the StatefulSet makes for its pod.
func (s serverSpec) claimName() string { return serverVolumeName + "-" + s.name + "-0" }

// serverResources are the requests and limits of a server's container. A server that was not given its own gets
// the platform's, and a limit that is missing is not left open.
func serverResources(db *geassv1alpha1.GeassDatabase) corev1.ResourceRequirements {
	res := databaseInstanceResources(db)
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(resourceQuantity(res.Requests, corev1.ResourceCPU)),
			corev1.ResourceMemory: resource.MustParse(resourceQuantity(res.Requests, corev1.ResourceMemory)),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(resourceQuantity(res.Limits, corev1.ResourceCPU)),
			corev1.ResourceMemory: resource.MustParse(resourceQuantity(res.Limits, corev1.ResourceMemory)),
		},
	}
}

func execProbe(period, failures int32, command ...string) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:     corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: command}},
		PeriodSeconds:    period,
		TimeoutSeconds:   5,
		FailureThreshold: failures,
	}
}

// serverContainerSecurity keeps a server from doing more than serve: no root, no extra capabilities.
func serverContainerSecurity(readOnlyRoot bool) *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsNonRoot:             utilptr.To(true),
		RunAsUser:                utilptr.To(serverRunAsUser),
		AllowPrivilegeEscalation: utilptr.To(false),
		ReadOnlyRootFilesystem:   utilptr.To(readOnlyRoot),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

// redisServer is the Redis server of a database: one pod with a password, and nothing written to disk, as it
// has always been. The password is read from the database's bootstrap Secret, so it is in no spec.
func redisServer(db *geassv1alpha1.GeassDatabase) serverSpec {
	release := redisDatabaseChartName(db.Name)
	bootstrap := db.Name + "-bootstrap"
	// REDISCLI_AUTH is what lets redis-cli, run in the pod by the console and by the probe, log in.
	ping := execProbe(10, 3, "sh", "-c", `test "$(redis-cli ping)" = PONG`)
	return serverSpec{
		engine: "redis", component: "master", release: release,
		name: platform.RedisServerName(db.Name), service: platform.RedisServerName(db.Name), headless: release + "-headless",
		port: redisPort, portName: "redis",
		container: corev1.Container{
			Name:  redisContainerName,
			Image: platform.RedisImage,
			// The server is started directly: the image's entrypoint only exists to give up root.
			Command: []string{"sh", "-c", `exec redis-server --requirepass "$REDIS_PASSWORD" --save "" --appendonly no`},
			Env: []corev1.EnvVar{
				secretEnv("REDIS_PASSWORD", bootstrap, "password"),
				secretEnv("REDISCLI_AUTH", bootstrap, "password"),
			},
			Ports:           []corev1.ContainerPort{{Name: "redis", ContainerPort: redisPort}},
			Resources:       serverResources(db),
			VolumeMounts:    []corev1.VolumeMount{{Name: serverVolumeName, MountPath: redisDataPath}},
			ReadinessProbe:  ping,
			LivenessProbe:   &corev1.Probe{ProbeHandler: ping.ProbeHandler, InitialDelaySeconds: 20, PeriodSeconds: 10, TimeoutSeconds: 5, FailureThreshold: 6},
			SecurityContext: serverContainerSecurity(true),
		},
		volumes:     []corev1.Volume{{Name: serverVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
		podSecurity: &corev1.PodSecurityContext{SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
		stopGrace:   30,
	}
}

// mysqlServer is the MySQL server of a database: one pod on a volume. The official image makes the root login, the
// application's login and its database the first time it starts on an empty volume, from the bootstrap Secret.
func mysqlServer(db *geassv1alpha1.GeassDatabase, storageClass string) serverSpec {
	release := mysqlChartName(db.Name)
	bootstrap := db.Name + "-bootstrap"
	selector := serverSpec{engine: "mysql", component: "primary", release: release}.selector()
	// A started server answers a ping from anyone, even one that is refused, so the probe needs no login. The
	// address forces TCP: the server that sets the volume up listens on its socket only, and is not ready.
	ping := []string{"mysqladmin", "ping", "--host=127.0.0.1", "--silent"}
	startup := execProbe(5, 90, ping...)
	claim := corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: serverVolumeName, Labels: selector},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: stringPtrOrNil(storageClass),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: databaseStorageQuantity(db)},
			},
		},
	}
	return serverSpec{
		engine: "mysql", component: "primary", release: release,
		name: platform.MySQLServerName(db.Name), service: release, headless: release + "-headless",
		port: mysqlPort, portName: "mysql",
		container: corev1.Container{
			Name:  mysqlContainerName,
			Image: platform.MySQLImage,
			Env: []corev1.EnvVar{
				secretEnv("MYSQL_ROOT_PASSWORD", bootstrap, "password"),
				secretEnv("MYSQL_USER", bootstrap, "username"),
				secretEnv("MYSQL_PASSWORD", bootstrap, "password"),
				{Name: "MYSQL_DATABASE", Value: databaseName(db)},
			},
			Ports:           []corev1.ContainerPort{{Name: "mysql", ContainerPort: mysqlPort}},
			Resources:       serverResources(db),
			VolumeMounts:    []corev1.VolumeMount{{Name: serverVolumeName, MountPath: mysqlDataPath, SubPath: mysqlDataSubPath}},
			StartupProbe:    startup,
			ReadinessProbe:  execProbe(10, 3, ping...),
			LivenessProbe:   execProbe(20, 6, ping...),
			SecurityContext: serverContainerSecurity(false),
		},
		claim: &claim,
		// The user is set on the pod as well as the container: a pod that names a group and no user is refused by the
		// container runtime ("user group is specified without user").
		podSecurity: &corev1.PodSecurityContext{
			RunAsNonRoot:        utilptr.To(true),
			RunAsUser:           utilptr.To(serverRunAsUser),
			RunAsGroup:          utilptr.To(serverRunAsUser),
			FSGroup:             utilptr.To(serverRunAsUser),
			FSGroupChangePolicy: utilptr.To(corev1.FSGroupChangeOnRootMismatch),
			SeccompProfile:      &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
		stopGrace: serverStopGrace,
	}
}

// reconcileRedis runs the Redis server of a database. A database that was made on the Bitnami chart is moved to
// the server first: Redis keeps nothing on disk, so the move costs what any restart of the pod does.
func (r *GeassDatabaseReconciler) reconcileRedis(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, placement platform.Placement) (ctrl.Result, error) {
	if waiting, err := r.retireChart(ctx, wsNS, redisDatabaseChartName(db.Name), platform.RedisServerName(db.Name), "Redis"); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	} else if waiting != "" {
		return r.setNotReady(ctx, db, waiting)
	}
	return r.reconcileServer(ctx, db, wsNS, redisServer(db), placement, redisDatabaseHost(db, wsNS), platform.RedisDefaultPort, "redis")
}

// reconcileMySQL runs the MySQL server of a database. One that is still the Bitnami chart is left exactly as it
// is: its data is on a volume the chart made, and moving it is not done without being asked. One that asks for
// high availability is a MOCO cluster, and any other is a single server.
func (r *GeassDatabaseReconciler) reconcileMySQL(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS, storageClass string, placement platform.Placement) (ctrl.Result, error) {
	_, err := helmchart.Get(ctx, r.Client, mysqlChartName(db.Name))
	switch {
	case err == nil:
		return r.reconcileHelmDatabase(ctx, db, wsNS, mysqlChartName(db.Name), platform.MySQLReleaseChart, platform.MySQLChartRepo, platform.MySQLChartVersion, mysqlValues(db, storageClass, placement), mysqlHost(db, wsNS), platform.MySQLDefaultPort, "mysql", placement)
	case !apierrors.IsNotFound(err):
		return r.setNotReady(ctx, db, err.Error())
	}
	backend, message, err := r.mysqlBackend(ctx, db, wsNS)
	switch {
	case err != nil:
		return r.setNotReady(ctx, db, err.Error())
	case message != "":
		return r.setNotReady(ctx, db, message)
	}
	if _, message := oddInstances(db); message != "" {
		return r.setNotReady(ctx, db, message)
	}
	if backend == mysqlBackendHA {
		return r.reconcileMySQLHA(ctx, db, wsNS, storageClass, placement)
	}
	return r.reconcileServer(ctx, db, wsNS, mysqlServer(db, storageClass), placement, mysqlHost(db, wsNS), platform.MySQLDefaultPort, "mysql")
}

// retireChart takes a database off the chart that used to run its server. It returns a message while the chart
// is still being removed. The server is made only once the chart is gone and has taken its StatefulSet with it:
// the two use the same names, and a StatefulSet that Helm owns would be deleted with its release.
func (r *GeassDatabaseReconciler) retireChart(ctx context.Context, wsNS, chartName, serverName, what string) (string, error) {
	waiting := fmt.Sprintf(messageReplacingChart, what)
	if _, err := helmchart.Get(ctx, r.Client, chartName); err == nil {
		if err := helmchart.Delete(ctx, r.Client, chartName); err != nil {
			return "", err
		}
		return waiting, nil
	} else if !apierrors.IsNotFound(err) {
		return "", err
	}
	sts := &appsv1.StatefulSet{}
	switch err := r.Get(ctx, client.ObjectKey{Name: serverName, Namespace: wsNS}, sts); {
	case apierrors.IsNotFound(err):
		return "", nil
	case err != nil:
		return "", err
	}
	if sts.Labels[platform.K8sLabelManagedBy] == "Helm" {
		return waiting, nil
	}
	return "", nil
}

// reconcileServer makes the StatefulSet and Services of a server, waits for its pod, and then publishes the
// connection and keeps the database's backups going.
func (r *GeassDatabaseReconciler) reconcileServer(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, srv serverSpec, placement platform.Placement, host, port, scheme string) (ctrl.Result, error) {
	existing := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: srv.name, Namespace: wsNS}}
	if ok, msg := workloadFitsPlacementUnlessCreated(ctx, r.Client, r.PodUsage, existing, databaseCapacityEstimate(db), placement); !ok {
		return r.setNotReady(ctx, db, msg)
	}
	if err := r.reconcileBootstrapSecret(ctx, db, wsNS); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	if err := r.ensureServerServices(ctx, db, wsNS, srv); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	sts, err := r.ensureServerStatefulSet(ctx, db, wsNS, srv, placement)
	if err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	r.growServerVolume(ctx, db, wsNS, srv)
	if !statefulSetReady(sts, 1) {
		return r.setNotReady(ctx, db, r.serverNotReadyMessage(ctx, wsNS, srv))
	}
	if err := r.reconcileGeneratedConnectionSecret(ctx, db, wsNS, host, port, scheme); err != nil {
		return r.setNotReady(ctx, db, err.Error())
	}
	var backups dumpOutcome
	if databaseBackupKind(db) == platform.BackupKindSnapshot {
		if backups, err = r.reconcileDumpBackups(ctx, db, wsNS); err != nil {
			return r.setNotReady(ctx, db, err.Error())
		}
		if backups.waiting != "" {
			return r.setNotReady(ctx, db, backups.waiting)
		}
	}
	return r.setReadyWithBackups(ctx, db, wsNS, host, string(db.Spec.Engine)+" database is ready", backups)
}

// imagePullReasons are the states of a container that mean its image cannot be had: a pull that failed, the wait
// before the next one, and a name that is not an image at all.
var imagePullReasons = []string{"ErrImagePull", "ImagePullBackOff", "InvalidImageName"}

// serverNotReadyMessage says why a server that is not ready is not. It is "starting" unless its pod says it cannot get
// its image: that never gets better by waiting, and it was what hid the removed Bitnami images, as a database that
// said "Service is starting" for months. A pod that is only slow to pull is ContainerCreating and is starting.
func (r *GeassDatabaseReconciler) serverNotReadyMessage(ctx context.Context, wsNS string, srv serverSpec) string {
	pod := &corev1.Pod{}
	if err := r.Get(ctx, client.ObjectKey{Name: srv.name + "-0", Namespace: wsNS}, pod); err != nil {
		if !apierrors.IsNotFound(err) {
			logf.FromContext(ctx).Info("Could not read the database server pod", "pod", srv.name+"-0", "error", err.Error())
		}
		return messageServiceStarting
	}
	// A pod that is being deleted is on its way to being replaced, and what it says is about the one before.
	if !pod.DeletionTimestamp.IsZero() {
		return messageServiceStarting
	}
	for _, status := range slices.Concat(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses) {
		if waiting := status.State.Waiting; waiting != nil && slices.Contains(imagePullReasons, waiting.Reason) {
			return platform.ImagePullMessage(podContainerImage(pod, status), waiting.Reason)
		}
	}
	// A pod that waits for a volume that no disk has room for is ContainerCreating, like one that is pulling.
	if message := volumeNotPlacedMessage(ctx, r.Client, wsNS, claimsOfServer(ctx, r.Client, wsNS, srv)...); message != "" {
		return message
	}
	return messageServiceStarting
}

// podContainerImage is the image a pod's container was asked to run. The pod's spec is what failed to pull, which is
// not the StatefulSet's once its template has changed and the pod is waiting to be replaced.
func podContainerImage(pod *corev1.Pod, status corev1.ContainerStatus) string {
	for _, container := range slices.Concat(pod.Spec.InitContainers, pod.Spec.Containers) {
		if container.Name == status.Name {
			return container.Image
		}
	}
	return status.Image
}

func (r *GeassDatabaseReconciler) ensureServerServices(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, srv serverSpec) error {
	ports := []corev1.ServicePort{{Name: srv.portName, Protocol: corev1.ProtocolTCP, Port: srv.port, TargetPort: intstr.FromInt32(srv.port)}}
	headless := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: srv.headless, Namespace: wsNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, headless, func() error {
		applyGeassLabels(headless, db, "GeassDatabase")
		headless.Spec.ClusterIP = corev1.ClusterIPNone
		headless.Spec.Selector = srv.selector()
		headless.Spec.Ports = ports
		// The pod's name has to resolve before it is ready: it is what the server is asked to wait for.
		headless.Spec.PublishNotReadyAddresses = true
		return setSameNamespaceOwner(db, headless, r.Scheme)
	}); err != nil {
		return err
	}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: srv.service, Namespace: wsNS}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		applyGeassLabels(svc, db, "GeassDatabase")
		svc.Spec.Selector = srv.selector()
		svc.Spec.Ports = ports
		return setSameNamespaceOwner(db, svc, r.Scheme)
	})
	return err
}

// ensureServerStatefulSet creates or updates the server's StatefulSet and returns it as the API server last saw it.
// The selector, the Service it is governed by, how its pods are managed and its volume claim are fixed when it is
// made, and the API refuses to change them, so they are set only then.
func (r *GeassDatabaseReconciler) ensureServerStatefulSet(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, srv serverSpec, placement platform.Placement) (*appsv1.StatefulSet, error) {
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: srv.name, Namespace: wsNS}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		applyGeassLabels(sts, db, "GeassDatabase")
		maps.Copy(sts.Labels, srv.selector())
		sts.Spec.Replicas = utilptr.To(int32(1))
		if sts.ResourceVersion == "" {
			sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: srv.selector()}
			sts.Spec.ServiceName = srv.headless
			// With the default (OrderedReady) a StatefulSet does not replace a pod that never became ready, so a
			// server that cannot start, say on an image that is wrong, stays broken after the fix is applied until
			// someone deletes its pod. There is only one pod, so nothing is lost by not waiting.
			sts.Spec.PodManagementPolicy = appsv1.ParallelPodManagement
			if srv.claim != nil {
				sts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{*srv.claim.DeepCopy()}
			}
		}
		labels := srv.selector()
		labels[platform.K8sLabelManagedBy] = platform.ManagedByValue
		if db.Spec.Project != "" {
			labels[platform.LabelProject] = db.Spec.Project
			labels[platform.LabelEnvironment] = string(db.Spec.Environment)
		}
		sts.Spec.Template.Labels = labels
		sts.Spec.Template.Spec = corev1.PodSpec{
			Containers:                    []corev1.Container{*srv.container.DeepCopy()},
			Volumes:                       srv.volumes,
			SecurityContext:               srv.podSecurity.DeepCopy(),
			AutomountServiceAccountToken:  utilptr.To(false),
			EnableServiceLinks:            utilptr.To(false),
			TerminationGracePeriodSeconds: utilptr.To(srv.stopGrace),
		}
		placement.ApplyToPodSpec(&sts.Spec.Template.Spec)
		applyServerRuntimeClass(db, &sts.Spec.Template.Spec)
		return setSameNamespaceOwner(db, sts, r.Scheme)
	})
	if err != nil {
		return nil, err
	}
	return sts, nil
}

// growServerVolume makes the server's volume as big as the database asks for, when it asks for more. The
// StatefulSet's claim template cannot change, so the volume itself is resized, which only some storage classes
// allow. A refusal is logged and the database carries on at the size it has: the dashboard already stops
// a resize on a class that cannot do it, and a database that works must not be taken out of service for one.
func (r *GeassDatabaseReconciler) growServerVolume(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string, srv serverSpec) {
	if srv.claim == nil {
		return
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Get(ctx, client.ObjectKey{Name: srv.claimName(), Namespace: wsNS}, pvc); err != nil {
		return
	}
	want := databaseStorageQuantity(db)
	if current, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok && want.Cmp(current) <= 0 {
		return
	}
	base := pvc.DeepCopy()
	if pvc.Spec.Resources.Requests == nil {
		pvc.Spec.Resources.Requests = corev1.ResourceList{}
	}
	pvc.Spec.Resources.Requests[corev1.ResourceStorage] = want
	if err := r.Patch(ctx, pvc, client.MergeFrom(base)); err != nil {
		logf.FromContext(ctx).Info("Could not make the database volume bigger", "claim", pvc.Name, "size", want.String(), "error", err.Error())
	}
}

// deleteServerResources removes the StatefulSet, Services and volume of a MySQL or Redis server.
func (r *GeassDatabaseReconciler) deleteServerResources(ctx context.Context, db *geassv1alpha1.GeassDatabase, wsNS string) {
	for _, srv := range []serverSpec{redisServer(db), mysqlServer(db, "")} {
		_ = client.IgnoreNotFound(r.Delete(ctx, &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: srv.name, Namespace: wsNS}}))
		_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: srv.service, Namespace: wsNS}}))
		_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: srv.headless, Namespace: wsNS}}))
		if srv.claim != nil {
			_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: srv.claimName(), Namespace: wsNS}}))
		}
	}
}
