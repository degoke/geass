package cloud

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanetScaleAuthorizationUsesBearer(t *testing.T) {
	require.Equal(t, "Bearer pscale_token", planetScaleAuthorization("pscale_token"))
	require.Equal(t, "Bearer pscale_token", planetScaleAuthorization("Bearer pscale_token"))
	require.Equal(t, "Bearer pscale_token", planetScaleAuthorization("bearer pscale_token"))
}

func TestPlanetScaleClientSendsBearerAuthorization(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"app"}`))
	}))
	t.Cleanup(srv.Close)

	client := &PlanetScaleClient{HTTP: srv.Client(), Token: "pscale_token", Org: "acme", Base: srv.URL}
	require.NoError(t, client.EnsureDatabase("app"))
	require.Equal(t, "Bearer pscale_token", got)
}
