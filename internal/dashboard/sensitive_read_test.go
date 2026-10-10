package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObjectStoreLogsAreRestrictedToRolesThatCanMutate(t *testing.T) {
	for _, path := range []string{"/api/object-stores/cluster-minio/logs", "/api/object-stores/cluster-minio/runtime"} {
		require.True(t, dashboardSensitiveRead(path), path)
	}

	f := newTokenFixture(t)
	viewer := withDashboardSession(t, f.srv, httptest.NewRequest(http.MethodGet, "/api/object-stores/cluster-minio/logs", nil), "viewer", dashboardRoleViewer)
	require.Equal(t, http.StatusForbidden, f.do(viewer).Code, "a viewer must not read MinIO pod logs")
}
