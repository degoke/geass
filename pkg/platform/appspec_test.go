package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestLastDeployedSpecRoundTrip(t *testing.T) {
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "demo"},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "payments",
			Source:  geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "nginx:alpine"}},
			Port:    8080,
		},
	}
	require.NoError(t, SetLastDeployedSpec(app))
	spec, ok := LastDeployedSpec(app)
	require.True(t, ok)
	require.Equal(t, "payments", spec.Project)
	require.Equal(t, "nginx:alpine", spec.Source.Image.Image)
	require.Equal(t, int32(8080), spec.Port)
}
