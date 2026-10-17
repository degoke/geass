package platform

import (
	"fmt"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const (
	HelmChartNamespace = "kube-system"

	CertManagerChartName    = "geass-cert-manager"
	CertManagerChartRepo    = "https://charts.jetstack.io"
	CertManagerChartVersion = "v1.17.2"
	CertManagerTargetNS     = "cert-manager"
	CertManagerReleaseChart = "cert-manager"

	MonitoringChartName    = "geass-kube-prometheus-stack"
	MonitoringChartRepo    = "https://prometheus-community.github.io/helm-charts"
	MonitoringChartVersion = "69.8.2"
	MonitoringTargetNS     = "monitoring"
	MonitoringReleaseChart = "kube-prometheus-stack"
	// MonitoringFullName is the chart's fullnameOverride. The chart names everything after the Helm
	// release and cuts the name to 26 characters, so the release "geass-kube-prometheus-stack" has always
	// produced names beginning "geass-kube-prometheus-stac" (sic). That is the name running clusters
	// already use; pinning it changes nothing for them and gives the dashboard a Service name that it
	// controls, instead of one that depends on how the chart shortens a longer name.
	MonitoringFullName = "geass-kube-prometheus-stac"
	// MonitoringPrometheusService is the Service that fronts Prometheus in MonitoringTargetNS.
	MonitoringPrometheusService = MonitoringFullName + "-prometheus"
	// MonitoringAlertmanagerService is the Service that fronts Alertmanager in MonitoringTargetNS, on port 9093.
	MonitoringAlertmanagerService = MonitoringFullName + "-alertmanager"

	// 1.12.x is the newest line that still supports Kubernetes 1.25+; 1.13 needs 1.34.
	LonghornChartName    = "geass-longhorn"
	LonghornChartRepo    = "https://charts.longhorn.io"
	LonghornChartVersion = "1.12.1"
	LonghornTargetNS     = "longhorn-system"
	LonghornReleaseChart = "longhorn"

	// KEDA and its HTTP add-on make scale to zero. They install into one namespace, and the add-on's chart
	// names its proxy Service after the chart (not the release), which is what ServerlessInterceptorHost
	// relies on. 0.16 is the add-on line current when this was written; its HTTPScaledObject is v1alpha1.
	ServerlessKEDAChartName    = "geass-keda"
	ServerlessKEDAChartRepo    = "https://kedacore.github.io/charts"
	ServerlessKEDAChartVersion = "2.21.0"
	ServerlessKEDAReleaseChart = "keda"
	ServerlessHTTPChartName    = "geass-keda-http"
	ServerlessHTTPChartVersion = "0.16.0"
	ServerlessHTTPReleaseChart = "keda-add-ons-http"
	ServerlessTargetNS         = "keda"
	// ServerlessInterceptorHost is where requests for a service that may be asleep are sent: the add-on's
	// proxy, which wakes the service and passes the request on.
	ServerlessInterceptorHost = "keda-add-ons-http-interceptor-proxy." + ServerlessTargetNS + ".svc.cluster.local"
	ServerlessInterceptorPort = 8080

	CNPGChartName    = "geass-cloudnative-pg"
	CNPGChartRepo    = "https://cloudnative-pg.github.io/charts"
	CNPGChartVersion = "0.23.2"
	CNPGTargetNS     = "cnpg-system"
	CNPGReleaseChart = "cloudnative-pg"

	// MySQL databases made before Geass ran its own servers are still a Bitnami chart. The chart's image
	// is gone from Docker Hub, so nothing new is made from it; it stays only so that a database that is
	// already running on it is left alone until it is moved (see reconcileMySQL).
	MySQLChartRepo    = "https://charts.bitnami.com/bitnami"
	MySQLChartVersion = "12.3.3"
	MySQLReleaseChart = "mysql"

	// Logging: a Fluent Bit shipper on every node and a VictoriaLogs server that keeps what it ships.
	LoggingTargetNS = "geass-logging"
	// LogStoreChartName and LogShipperChartName are the HelmChart objects; the Helm releases are named the same.
	LogStoreChartName    = "geass-victoria-logs"
	LogStoreChartRepo    = "https://victoriametrics.github.io/helm-charts/"
	LogStoreChartVersion = "0.13.10"
	LogStoreReleaseChart = "victoria-logs-single"
	// LogStoreServiceName is pinned (server.fullnameOverride) so the shipper and the dashboard can find it.
	LogStoreServiceName = "geass-victorialogs"
	LogStorePort        = "9428"

	LogShipperChartName    = "geass-fluent-bit"
	LogShipperChartRepo    = "https://fluent.github.io/helm-charts"
	LogShipperChartVersion = "0.58.3"
	LogShipperReleaseChart = "fluent-bit"

	SQLiteImage = "rqlite/rqlite:8.36.16"

	// RedisImage and MySQLImage are the official images Geass runs its Redis and MySQL servers from. They
	// are pinned to a release, so a server changes only when Geass does. The tag is the one thing to change
	// to take a patch (or, for Redis, to move to another build such as Valkey).
	RedisImage = "redis:7.4.11-alpine"
	MySQLImage = "mysql:8.4.11"

	// MySQL high availability is run by MOCO, an operator for MySQL that replicates with loss-less semi-synchronous
	// replication and promotes a replica when the primary is lost. It is installed the first time a database asks
	// for it, like the Postgres operator, and needs cert-manager for its webhooks. Its pods run its own build of the
	// official MySQL, tagged with the MySQL version, which is why the image is not MySQLImage.
	MOCOChartName    = "geass-moco"
	MOCOChartRepo    = "https://cybozu-go.github.io/moco/"
	MOCOChartVersion = "0.27.0"
	MOCOTargetNS     = "moco-system"
	MOCOReleaseChart = "moco"
	MOCOMySQLImage   = "ghcr.io/cybozu-go/moco/mysql:8.4.10"

	// BackupTransferImage moves MySQL and SQLite snapshots to and from the project's bucket. curl signs
	// S3 requests itself (--aws-sigv4), so the image needs nothing else.
	BackupTransferImage = "curlimages/curl:8.10.1"
)

// MySQLChartReleaseName and RedisChartReleaseName are the names of a database's MySQL and Redis servers: the
// prefix of their StatefulSet and Services, and the value of the pods' app.kubernetes.io/instance label, which is how
// pods are traced back to the database. They are the names the Bitnami charts gave, kept so that the hosts
// apps already connect to, and everything that finds a server's pods, volumes and logs, did not change when
// Geass began to run the servers itself.
func MySQLChartReleaseName(database string) string { return "geass-mysql-" + database }

func RedisChartReleaseName(database string) string { return "geass-redis-db-" + database }

// MySQLServerName and RedisServerName are the StatefulSets of the servers, and the pods are the name with -0. They are
// what the Bitnami charts named them in the layout Geass used (a single server), which is a MySQL set named for the
// release and a Redis one with the suffix -master.
func MySQLServerName(database string) string { return MySQLChartReleaseName(database) }

// MOCOServerName is the StatefulSet MOCO makes for a MySQL database that runs for high availability; its pods are the
// name with an index, and its claims are mysql-data- and then the pod.
func MOCOServerName(database string) string { return "moco-" + database }

func RedisServerName(database string) string { return RedisChartReleaseName(database) + "-master" }

// EngineIsSingleInstance reports whether Geass runs an engine as one instance only. Replication and failover
// come from an operator: CloudNativePG for PostgreSQL and MOCO for MySQL. Redis has no such thing here.
func EngineIsSingleInstance(engine geassv1alpha1.GeassDatabaseEngine) bool {
	return engine == geassv1alpha1.DatabaseEngineRedis
}

// SingleInstanceMessage is what a Redis database that asks for high availability, or for more than one
// instance, is told. It begins with "High availability requires" so that it is read as a wait for the user
// and does not count toward the retries that end provisioning.
func SingleInstanceMessage(engine geassv1alpha1.GeassDatabaseEngine) string {
	return fmt.Sprintf("High availability requires PostgreSQL or MySQL: %s runs as a single instance. Turn it off, or choose PostgreSQL or MySQL", engine)
}

// MessageCreatingDatabaseLogin is what a MySQL cluster says while the job that makes the application's login and
// database runs. It is a wait: the cluster is up, and the job finishes in seconds. Counting it as a failure would end
// provisioning after five quick passes, with the database fine and the job done.
const MessageCreatingDatabaseLogin = "Creating the database login"

// MySQLHAMessage is what a MySQL database is told when its instances cannot be run. MOCO runs an odd number of
// MySQL servers, so a database is one instance or three or five, and it cannot change from one to the other
// because they are not the same servers. It begins like SingleInstanceMessage and for the same reason.
func MySQLHAMessage(instances int32) string {
	return fmt.Sprintf("High availability requires 3 or 5 MySQL instances, not %d. Set the instances to 3 or 5, or turn high availability off", instances)
}

// MySQLChangeMessage is what a MySQL database that changed between one instance and high availability is told.
// highlyAvailable is what the database runs as now.
func MySQLChangeMessage(highlyAvailable bool) string {
	if highlyAvailable {
		return "High availability requires a new database: this MySQL database runs three or more instances and cannot become one. Turn it back on, or create a new database"
	}
	return "High availability requires a new database: this MySQL database runs one instance and cannot become highly available. Turn it back off, or create a new database"
}
