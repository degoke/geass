package cloud

import (
	"encoding/json"
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
			require.True(t, json.Valid([]byte(r.FormValue("PolicyDocument"))))
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

func TestEnsureBucketUserJSONEscapesBucketNames(t *testing.T) {
	var policy string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		if r.FormValue("Action") == "PutUserPolicy" {
			policy = r.FormValue("PolicyDocument")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", IAMEndpoint: srv.URL}
	_, _, err := client.EnsureBucketUser([]string{`uploads"evil`}, "assets", "EXISTING", "existing-secret")
	require.NoError(t, err)
	require.True(t, json.Valid([]byte(policy)))
	var parsed struct {
		Statement []struct {
			Resource []string `json:"Resource"`
		} `json:"Statement"`
	}
	require.NoError(t, json.Unmarshal([]byte(policy), &parsed))
	require.Equal(t, "arn:aws:s3:::uploads\"evil", parsed.Statement[0].Resource[0])
}

func TestDeleteBucketUserRemovesKeysPolicyAndUser(t *testing.T) {
	var actions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		action := r.FormValue("Action")
		actions = append(actions, action)
		switch action {
		case "ListAccessKeys":
			require.Equal(t, "geass-assets", r.FormValue("UserName"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<ListAccessKeysResponse><ListAccessKeysResult><AccessKeyMetadata><AccessKeyId>AKIASCOPED</AccessKeyId></AccessKeyMetadata></ListAccessKeysResult></ListAccessKeysResponse>`))
		case "DeleteAccessKey":
			require.Equal(t, "AKIASCOPED", r.FormValue("AccessKeyId"))
			w.WriteHeader(http.StatusOK)
		case "DeleteUserPolicy":
			require.Equal(t, "geass-bucket", r.FormValue("PolicyName"))
			w.WriteHeader(http.StatusOK)
		case "DeleteUser":
			require.Equal(t, "geass-assets", r.FormValue("UserName"))
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", IAMEndpoint: srv.URL}
	require.NoError(t, client.DeleteBucketUser("assets"))
	require.Equal(t, []string{"ListAccessKeys", "DeleteAccessKey", "DeleteUserPolicy", "DeleteUser"}, actions)
}

func TestDeleteBucketUserTreatsMissingUserAsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<ErrorResponse><Error><Code>NoSuchEntity</Code><Message>user missing</Message></Error></ErrorResponse>`))
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", IAMEndpoint: srv.URL}
	require.NoError(t, client.DeleteBucketUser("assets"))
}
