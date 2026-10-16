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

func RedisServerName(database string) string { return RedisChartReleaseName(database) + "-master" }

// EngineIsSingleInstance reports whether Geass runs an engine as one instance only. Replication and failover
// come from PostgreSQL's operator; the other engines have no such thing here.
func EngineIsSingleInstance(engine geassv1alpha1.GeassDatabaseEngine) bool {
	return engine == geassv1alpha1.DatabaseEngineMySQL || engine == geassv1alpha1.DatabaseEngineRedis
}

// SingleInstanceMessage is what a MySQL or Redis database that asks for high availability, or for more than one
// instance, is told. It begins with "High availability requires" so that it is read as a wait for the user
// and does not count toward the retries that end provisioning.
func SingleInstanceMessage(engine geassv1alpha1.GeassDatabaseEngine) string {
	return fmt.Sprintf("High availability requires PostgreSQL: %s runs as a single instance. Turn it off, or choose PostgreSQL", engine)
}
