package dashboard

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/pkg/platform"
)

func TestTheDefaultPrometheusAddressIsTheServiceTheMonitoringChartCreates(t *testing.T) {
	t.Setenv("GEASS_PROMETHEUS_URL", "")

	require.Equal(t, "http://"+platform.MonitoringPrometheusService+"."+platform.MonitoringTargetNS+".svc:9090", defaultPrometheusURL(),
		"the chart's values pin this Service name; a different name here and every metric silently fails")
}

func TestThePrometheusAddressCanStillBeOverridden(t *testing.T) {
	t.Setenv("GEASS_PROMETHEUS_URL", "http://127.0.0.1:19090")
	require.Equal(t, "http://127.0.0.1:19090", defaultPrometheusURL())
}
