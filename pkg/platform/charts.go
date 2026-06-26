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

	MinIOChartRepo    = "https://charts.min.io/"
	MinIOChartVersion = "5.4.0"
	MinIOReleaseChart = "minio"
)
