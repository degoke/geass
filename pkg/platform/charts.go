package platform

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

	RedisChartRepo    = "https://charts.bitnami.com/bitnami"
	RedisChartVersion = "20.7.1"
	RedisReleaseChart = "redis"

	MySQLChartRepo    = "https://charts.bitnami.com/bitnami"
	MySQLChartVersion = "12.3.3"
	MySQLReleaseChart = "mysql"

	MinIOChartRepo    = "https://charts.min.io/"
	MinIOChartVersion = "5.4.0"
	MinIOReleaseChart = "minio"
	// Quay often returns 401 for anonymous pulls; Docker Hub mirrors work on Kind and typical dev clusters.
	MinIOImageRepository   = "minio/minio"
	MinIOImageTag          = "RELEASE.2025-02-18T16-25-55Z"
	MinIOMCImageRepository = "minio/mc"
	MinIOMCImageTag        = "RELEASE.2025-02-21T16-00-46Z"
	ClusterMinIOName       = "cluster-minio"
	ClusterMinIOChartName  = "geass-minio"

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

	// BackupTransferImage moves MySQL and SQLite snapshots to and from the project's bucket. curl signs
	// S3 requests itself (--aws-sigv4), so the image needs nothing else. The MinIO client that the MinIO
	// chart uses was not chosen because the minio/* images are no longer reliably published.
	BackupTransferImage = "curlimages/curl:8.10.1"
)

// MySQLChartReleaseName and RedisChartReleaseName are the names the Helm releases of a database's MySQL
// and Redis servers have. Pods of those releases carry the name as their instance label, which is how
// they are traced back to the database.
func MySQLChartReleaseName(database string) string { return "geass-mysql-" + database }

func RedisChartReleaseName(database string) string { return "geass-redis-db-" + database }
