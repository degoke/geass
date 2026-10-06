package platform

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/stretchr/testify/require"
)

func TestValidateCloudConnectionForProject(t *testing.T) {
	conn := geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-aws"},
		Spec:       geassv1alpha1.GeassCloudConnectionSpec{Project: "payments"},
	}
	require.NoError(t, ValidateCloudConnectionForProject(conn, "payments"))
	require.Error(t, ValidateCloudConnectionForProject(conn, "billing"))

	conn.Spec.Project = ""
	require.Error(t, ValidateCloudConnectionForProject(conn, "billing"))
}
