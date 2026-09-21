package cloud

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnsureBucketPathStyle(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloned := r.Clone(r.Context())
		got = cloned
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	require.NoError(t, client.EnsureBucket("uploads"))
	require.NotNil(t, got)
	require.Equal(t, http.MethodPut, got.Method)
	require.Equal(t, "/uploads", got.URL.Path)
	require.Contains(t, got.Header.Get("Authorization"), "AWS4-HMAC-SHA256")
}

func TestEnsureBucketConflictIsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	require.NoError(t, client.EnsureBucket("uploads"))
}
