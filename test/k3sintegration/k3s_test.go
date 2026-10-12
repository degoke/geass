//go:build k3sintegration

package k3sintegration

import (
	"testing"

	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

// k3sRestConfig is the cluster the tests run against: the one the current kubeconfig (or KUBECONFIG) names.
// hack/k3s-up.sh starts a throwaway k3s in Docker and prints the settings for it.
func k3sRestConfig(t *testing.T) *rest.Config {
	t.Helper()
	cfg, err := ctrl.GetConfig()
	if err != nil {
		t.Fatalf("no kubeconfig for the k3s cluster: %v", err)
	}
	return cfg
}
