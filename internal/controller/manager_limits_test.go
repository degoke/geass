package controller

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"
)

// operatorContainer is the container the operator runs in, as the installed manifest has it.
func operatorContainer(t *testing.T) corev1.Container {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "manager", "manager.yaml"))
	require.NoError(t, err)
	for document := range bytes.SplitSeq(raw, []byte("\n---\n")) {
		var deployment appsv1.Deployment
		if yaml.Unmarshal(document, &deployment) != nil || deployment.Kind != "Deployment" {
			continue
		}
		for _, container := range deployment.Spec.Template.Spec.Containers {
			if container.Name == "manager" {
				return container
			}
		}
	}
	t.Fatal("the manifest has no Deployment with a manager container")
	return corev1.Container{}
}

// The operator's memory limit and the Go runtime's soft limit have to move together. A runtime limit above the
// container's is no limit at all, and one far below it makes the operator spend its CPU on garbage collection for
// room it was given and does not use. The numbers are the ones the Cluster size page measured: 20,000 services held
// 785 MiB at rest and 830 MiB at the heaviest moment of a restart.
func TestTheOperatorsRuntimeMemoryLimitSitsJustBelowItsContainerLimit(t *testing.T) {
	container := operatorContainer(t)

	limit := container.Resources.Limits[corev1.ResourceMemory]
	require.False(t, limit.IsZero(), "the operator has a memory limit")
	oneGi := resource.MustParse("1Gi")
	require.GreaterOrEqual(t, limit.Value(), oneGi.Value(),
		"512Mi did not survive a restart at 20,000 services: see the Cluster size page before lowering this")

	var goMemLimit resource.Quantity
	for _, env := range container.Env {
		if env.Name == "GOMEMLIMIT" {
			// The Go runtime writes its sizes with an IEC suffix and no "i": 800MiB.
			var err error
			goMemLimit, err = resource.ParseQuantity(env.Value[:len(env.Value)-1])
			require.NoError(t, err, env.Value)
		}
	}
	require.False(t, goMemLimit.IsZero(), "the operator sets GOMEMLIMIT")

	ratio := float64(goMemLimit.Value()) / float64(limit.Value())
	require.Greater(t, ratio, 0.65, "a runtime limit this far below the container's makes the garbage collector work for nothing")
	require.Less(t, ratio, 0.90, "the runtime's own overhead has to fit between the two limits")

	request := container.Resources.Requests[corev1.ResourceMemory]
	require.Less(t, request.Value(), limit.Value()/4, "a limit is not a reservation, and the request is what a small cluster's node is asked for")
}
