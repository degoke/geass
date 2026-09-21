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

func TestEnsureBucketUserCreatesScopedIAMUser(t *testing.T) {
	var actions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		action := r.FormValue("Action")
		actions = append(actions, action)
		switch action {
		case "CreateUser":
			require.Equal(t, "geass-assets", r.FormValue("UserName"))
			w.WriteHeader(http.StatusOK)
		case "PutUserPolicy":
			require.Contains(t, r.FormValue("PolicyDocument"), "arn:aws:s3:::uploads")
			require.Contains(t, r.FormValue("PolicyDocument"), "arn:aws:s3:::uploads/*")
			w.WriteHeader(http.StatusOK)
		case "CreateAccessKey":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<CreateAccessKeyResponse><CreateAccessKeyResult><AccessKey><AccessKeyId>AKIASCOPED</AccessKeyId><SecretAccessKey>scoped-secret</SecretAccessKey></AccessKey></CreateAccessKeyResult></CreateAccessKeyResponse>`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", IAMEndpoint: srv.URL}
	access, secret, err := client.EnsureBucketUser([]string{"uploads"}, "assets", "", "")
	require.NoError(t, err)
	require.Equal(t, "AKIASCOPED", access)
	require.Equal(t, "scoped-secret", secret)
	require.Equal(t, []string{"CreateUser", "PutUserPolicy", "CreateAccessKey"}, actions)
}

func TestEnsureBucketUserReusesExistingKeys(t *testing.T) {
	var actions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		actions = append(actions, r.FormValue("Action"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", IAMEndpoint: srv.URL}
	access, secret, err := client.EnsureBucketUser([]string{"uploads"}, "assets", "EXISTING", "existing-secret")
	require.NoError(t, err)
	require.Equal(t, "EXISTING", access)
	require.Equal(t, "existing-secret", secret)
	require.Equal(t, []string{"CreateUser", "PutUserPolicy"}, actions)
}
