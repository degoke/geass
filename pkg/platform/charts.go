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

	SQLiteImage = "rqlite/rqlite:8.36.16"
)
