package controller

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilptr "k8s.io/utils/ptr"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloud"
	"github.com/degoke/geass/pkg/platform"
)

// The two servers Geass can run as the cluster's object storage differ in how they are started and in how a
// bucket and a login for it are made, and in nothing else. Each is an objectStoreBackend: a description of its
// pod, and the client that makes buckets and users on it once it runs. Everything else (the Deployment, the
// volume, the Service, the root Secret, readiness, removal) is the same for both and lives in
// objectstore_server.go.

const (
	// objectStoreDataPath is where the server's volume is mounted.
	objectStoreDataPath = "/data"
	// seaweedFSConfigPath is where SeaweedFS looks for the files it reads at start. It searches a fixed few
	// directories for master.toml, and this is the only one of them that a pod can mount over.
	seaweedFSConfigPath = "/etc/seaweedfs"
	garageConfigPath    = "/etc/garage"
	// objectStoreRunAsUser is the user the servers run as. Their volume is made writable for it.
	objectStoreRunAsUser int64 = 65532

	portNameS3    = "s3"
	portNameAdmin = "admin"

	// seaweedFSInternalS3Port is where SeaweedFS's S3 API listens, on localhost. The forwarder publishes it as
	// platform.SeaweedFSS3Port.
	seaweedFSInternalS3Port = 8334

	// Root Secret keys. accessKey and secretKey are the server's own S3 login; Garage has three more secrets.
	rootKeyAccess       = platform.ConnectionKeyAccessKey
	rootKeySecret       = platform.ConnectionKeySecretKey
	rootKeyAdminToken   = "adminToken"
	rootKeyRPCSecret    = "rpcSecret"
	rootKeyMetricsToken = "metricsToken"
)

// objectStoreRoot is the server's own login and secrets. Geass generates them once, keeps them in the root
// Secret, and never gives them to an app.
type objectStoreRoot struct {
	accessKey, secretKey string
	// adminToken, rpcSecret and metricsToken are Garage's; SeaweedFS has none of them.
	adminToken, rpcSecret, metricsToken string
}

// bucketAdmin makes and removes the buckets and bucket users of a running server.
type bucketAdmin interface {
	EnsureBucket(bucket string) error
	// EnsureBucketUser makes a login for the user that can reach exactly these buckets, and returns its keys.
	// Keys that were stored earlier are kept while the server still knows them.
	EnsureBucketUser(buckets []string, userName, existingAccess, existingSecret string) (string, string, error)
	DeleteBucket(bucket string) error
	DeleteBucketUser(userName string) error
}

// serverPod is what a backend contributes to the server's Deployment and Service.
type serverPod struct {
	container corev1.Container
	// files are the contents of the config Secret, mounted at configPath.
	files      map[string]string
	configPath string
	// sidecars are further containers of the pod.
	sidecars []corev1.Container
	// ports are the ports the Service publishes.
	ports []corev1.ServicePort
}

type objectStoreBackend interface {
	// fillRoot generates whatever the server's login and secrets still lack.
	fillRoot(root *objectStoreRoot) error
	// pod describes the server's pod. rootSecret names the Secret that holds the root login, for the
	// backends that read it from the environment rather than a file.
	pod(root objectStoreRoot, rootSecret string) serverPod
	// admin returns the client that makes buckets and users on the running server.
	admin(httpClient *http.Client, endpoint string, root objectStoreRoot) bucketAdmin
}

// objectStoreBackendFor returns the backend for an engine, and false for an engine that is not run in the
// cluster.
func objectStoreBackendFor(engine geassv1alpha1.GeassObjectStoreEngine) (objectStoreBackend, bool) {
	switch engine {
	case geassv1alpha1.ObjectStoreEngineSeaweedFS:
		return seaweedFSBackend{}, true
	case geassv1alpha1.ObjectStoreEngineGarage:
		return garageBackend{}, true
	default:
		return nil, false
	}
}

// randomHex returns that many random bytes as hex digits.
func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func fillIfEmpty(target *string, generate func() (string, error)) error {
	if *target != "" {
		return nil
	}
	value, err := generate()
	if err != nil {
		return err
	}
	*target = value
	return nil
}

// httpProbe asks a server's health endpoint. The timeout is generous because a server is slow to stop and costly to
// restart: a node that is briefly starved of CPU must not have its storage restarted for being slow to answer.
func httpProbe(path string, port, period, failures int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:     corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: path, Port: intstr.FromInt32(port), Scheme: corev1.URISchemeHTTP}},
		PeriodSeconds:    period,
		TimeoutSeconds:   10,
		SuccessThreshold: 1,
		FailureThreshold: failures,
	}
}

// serverSecurityContext keeps a server from doing more than serve: no root, no extra capabilities.
func serverSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsNonRoot:             utilptr.To(true),
		RunAsUser:                utilptr.To(objectStoreRunAsUser),
		AllowPrivilegeEscalation: utilptr.To(false),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

func rootSecretEnv(name, secret, key string) corev1.EnvVar {
	return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: secretKeySelector(secret, key)}}
}

// seaweedFSBackend runs SeaweedFS as one process: master, volume server, filer and S3 gateway together, which
// is how it is meant to run on a single node.
type seaweedFSBackend struct{}

// seaweedFSMasterConfig keeps SeaweedFS from reserving seven volumes for every new bucket. A bucket is a
// "collection", and by default a collection grows seven volumes at once, so a server with room for forty
// volumes would run out after five buckets.
const seaweedFSMasterConfig = `[master.volume_growth]
copy_1 = 1
copy_2 = 1
copy_3 = 1
copy_other = 1
`

func (seaweedFSBackend) fillRoot(root *objectStoreRoot) error {
	if err := fillIfEmpty(&root.accessKey, func() (string, error) { return randomHex(16) }); err != nil {
		return err
	}
	return fillIfEmpty(&root.secretKey, func() (string, error) { return randomHex(32) })
}

func (seaweedFSBackend) pod(root objectStoreRoot, _ string) serverPod {
	// The admin identity is the only one in the file. Bucket users are made later through the IAM API, which
	// keeps them on the server's own volume, so a restart does not forget them.
	identities, _ := json.Marshal(map[string]any{"identities": []any{map[string]any{
		"name":        "geass-root",
		"credentials": []any{map[string]string{"accessKey": root.accessKey, "secretKey": root.secretKey}},
		"actions":     []string{"Admin", "Read", "Write", "List", "Tagging"},
	}}})
	port := int32(platform.SeaweedFSS3Port)
	return serverPod{
		container: corev1.Container{
			Name:  "seaweedfs",
			Image: platform.SeaweedFSImage,
			Args: []string{
				"server", "-dir=" + objectStoreDataPath, "-filer", "-s3",
				"-s3.config=" + seaweedFSConfigPath + "/s3.json",
				fmt.Sprintf("-s3.port=%d", seaweedFSInternalS3Port),
				// Everything listens on localhost, S3 included. SeaweedFS serves internal gRPC services beside S3 that
				// have no login and accept a new admin identity from anyone who reaches them, and the filer would
				// give its whole disk to anyone too. Only the forwarder below is reachable from other pods.
				"-ip=127.0.0.1", "-ip.bind=127.0.0.1",
				"-s3.iam.readOnly=false",
				"-s3.port.iceberg=0", "-s3.port.lance=0",
				"-master.volumeSizeLimitMB=256", "-volume.max=1000", "-volume.minFreeSpacePercent=5",
			},
			// The probes ask the forwarder, so that a pod is ready only when S3 can be reached the way an app reaches it.
			StartupProbe:   httpProbe("/healthz", port, 5, 60),
			ReadinessProbe: httpProbe("/healthz", port, 10, 3),
			LivenessProbe:  httpProbe("/healthz", port, 30, 10),
		},
		sidecars: []corev1.Container{{
			Name:  "s3-forwarder",
			Image: platform.S3ForwarderImage,
			Args: []string{
				fmt.Sprintf("TCP-LISTEN:%d,fork,reuseaddr", port),
				fmt.Sprintf("TCP:127.0.0.1:%d", seaweedFSInternalS3Port),
			},
			Ports:           []corev1.ContainerPort{{Name: portNameS3, ContainerPort: port, Protocol: corev1.ProtocolTCP}},
			Resources:       forwarderResources(),
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: forwarderSecurityContext(),
		}},
		files:      map[string]string{"s3.json": string(identities), "master.toml": seaweedFSMasterConfig},
		configPath: seaweedFSConfigPath,
		ports:      []corev1.ServicePort{{Name: portNameS3, Protocol: corev1.ProtocolTCP, Port: port, TargetPort: intstr.FromInt32(port)}},
	}
}

// forwarderResources is all a TCP forwarder needs.
func forwarderResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("16Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
	}
}

// forwarderSecurityContext is serverSecurityContext, and the forwarder writes nothing either.
func forwarderSecurityContext() *corev1.SecurityContext {
	context := serverSecurityContext()
	context.ReadOnlyRootFilesystem = utilptr.To(true)
	return context
}

func (seaweedFSBackend) admin(httpClient *http.Client, endpoint string, root objectStoreRoot) bucketAdmin {
	return cloud.NewSeaweedFSClient(httpClient, endpoint, root.accessKey, root.secretKey)
}

// garageBackend runs Garage in its single-node mode, which lays the cluster out by itself.
type garageBackend struct{}

func (garageBackend) fillRoot(root *objectStoreRoot) error {
	// Garage wants its keys to look like its own: "GK" and 32 hex digits, and a 64 digit secret.
	if err := fillIfEmpty(&root.accessKey, func() (string, error) {
		id, err := randomHex(16)
		return "GK" + id, err
	}); err != nil {
		return err
	}
	for _, target := range []*string{&root.secretKey, &root.rpcSecret, &root.adminToken, &root.metricsToken} {
		if err := fillIfEmpty(target, func() (string, error) { return randomHex(32) }); err != nil {
			return err
		}
	}
	return nil
}

func (garageBackend) pod(_ objectStoreRoot, rootSecret string) serverPod {
	s3, admin := int32(platform.GarageS3Port), int32(platform.GarageAdminPort)
	// The secrets are not in this file: Garage reads them from the environment. The region is us-east-1 so that
	// an S3 client with no region set signs correctly, as it does against AWS. The RPC port is only for nodes
	// that join, and this server has none, so it listens on localhost.
	config := fmt.Sprintf(`metadata_dir = "%[1]s/meta"
data_dir = "%[1]s/data"
db_engine = "sqlite"
replication_factor = 1

rpc_bind_addr = "127.0.0.1:3901"
rpc_public_addr = "127.0.0.1:3901"

[s3_api]
s3_region = "us-east-1"
api_bind_addr = "0.0.0.0:%[2]d"

[admin]
api_bind_addr = "0.0.0.0:%[3]d"
`, objectStoreDataPath, s3, admin)
	return serverPod{
		container: corev1.Container{
			Name:    "garage",
			Image:   platform.GarageImage,
			Command: []string{"/garage"},
			Args:    []string{"server", "--single-node", "--default-access-key"},
			Env: []corev1.EnvVar{
				{Name: "GARAGE_CONFIG_FILE", Value: garageConfigPath + "/garage.toml"},
				rootSecretEnv("GARAGE_RPC_SECRET", rootSecret, rootKeyRPCSecret),
				rootSecretEnv("GARAGE_ADMIN_TOKEN", rootSecret, rootKeyAdminToken),
				rootSecretEnv("GARAGE_METRICS_TOKEN", rootSecret, rootKeyMetricsToken),
				rootSecretEnv("GARAGE_DEFAULT_ACCESS_KEY", rootSecret, rootKeyAccess),
				rootSecretEnv("GARAGE_DEFAULT_SECRET_KEY", rootSecret, rootKeySecret),
			},
			Ports: []corev1.ContainerPort{
				{Name: portNameS3, ContainerPort: s3, Protocol: corev1.ProtocolTCP},
				{Name: portNameAdmin, ContainerPort: admin, Protocol: corev1.ProtocolTCP},
			},
			StartupProbe:   httpProbe("/health", admin, 5, 60),
			ReadinessProbe: httpProbe("/health", admin, 10, 3),
			LivenessProbe:  httpProbe("/health", admin, 30, 10),
		},
		files:      map[string]string{"garage.toml": config},
		configPath: garageConfigPath,
		ports: []corev1.ServicePort{
			{Name: portNameS3, Protocol: corev1.ProtocolTCP, Port: s3, TargetPort: intstr.FromInt32(s3)},
			{Name: portNameAdmin, Protocol: corev1.ProtocolTCP, Port: admin, TargetPort: intstr.FromInt32(admin)},
		},
	}
}

func (garageBackend) admin(httpClient *http.Client, endpoint string, root objectStoreRoot) bucketAdmin {
	return &cloud.GarageClient{
		HTTP:          httpClient,
		AdminEndpoint: platform.ObjectStoreServerAdminEndpoint(),
		AdminToken:    root.adminToken,
		S3:            &cloud.AWSClient{HTTP: httpClient, AccessKey: root.accessKey, SecretKey: root.secretKey, Endpoint: endpoint},
	}
}
