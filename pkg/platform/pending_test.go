package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPendingKindsIncludesCreatedForDraft(t *testing.T) {
	meta := metav1.ObjectMeta{Annotations: map[string]string{DeployWorkflowAnnotation: "true"}}
	kinds := PendingKinds(meta.Annotations, false)
	require.Equal(t, []string{PendingChangeCreated}, kinds)
}

func TestSetLastDeployedJSONRoundTrip(t *testing.T) {
	meta := metav1.ObjectMeta{}
	spec := map[string]string{"bucket": "uploads"}
	require.NoError(t, SetLastDeployedJSON(&meta, spec))
	out, ok := UnmarshalLastDeployed[map[string]string](meta.Annotations)
	require.True(t, ok)
	require.Equal(t, "uploads", out["bucket"])
}
