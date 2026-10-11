package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// The CRD default fills StorageSize for most objects, but anything that reaches
// the controller without one must still get the engine's default, not a constant.
func TestStorageSizeFallsBackToEngineDefault(t *testing.T) {
	require.Equal(t, "1Gi", storageSize(&geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineSQLite}}))
	require.Equal(t, "10Gi", storageSize(&geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{Engine: geassv1alpha1.DatabaseEngineRedis}}))

	size := resource.MustParse("25Gi")
	require.Equal(t, "25Gi", storageSize(&geassv1alpha1.GeassDatabase{Spec: geassv1alpha1.GeassDatabaseSpec{
		Engine: geassv1alpha1.DatabaseEngineSQLite, StorageSize: &size,
	}}))
}
