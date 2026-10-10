package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestObjectStoreInfrastructureActive(t *testing.T) {
	draft := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{DeployWorkflowAnnotation: "true"},
		},
		Spec: geassv1alpha1.GeassObjectStoreSpec{Deploy: geassv1alpha1.GeassDeploySpec{Enabled: false}},
	}
	require.False(t, ObjectStoreInfrastructureActive(draft))

	legacyDraft := &geassv1alpha1.GeassObjectStore{
		Spec: geassv1alpha1.GeassObjectStoreSpec{Deploy: geassv1alpha1.GeassDeploySpec{Enabled: false}},
	}
	require.False(t, ObjectStoreInfrastructureActive(legacyDraft))

	deployNeverReady := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{DeployWorkflowAnnotation: "true"},
		},
		Spec: geassv1alpha1.GeassObjectStoreSpec{Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true}},
	}
	require.False(t, ObjectStoreInfrastructureActive(deployNeverReady))

	legacyLive := &geassv1alpha1.GeassObjectStore{
		Spec:   geassv1alpha1.GeassObjectStoreSpec{Deploy: geassv1alpha1.GeassDeploySpec{Enabled: false}},
		Status: geassv1alpha1.GeassObjectStoreStatus{ConnectionSecret: "floral-pebble-connection"},
	}
	require.True(t, ObjectStoreInfrastructureActive(legacyLive))
}
