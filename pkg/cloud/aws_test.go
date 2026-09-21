package cloud

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDeleteBucketRemovesObjectsThenBucket(t *testing.T) {
	var methods []string
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<ListBucketResult><Contents><Key>logo.png</Key></Contents><IsTruncated>false</IsTruncated></ListBucketResult>`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	require.NoError(t, client.DeleteBucket("uploads"))
	require.Contains(t, methods, http.MethodGet)
	require.Contains(t, paths, "/uploads/logo.png")
	require.Equal(t, http.MethodDelete, methods[len(methods)-1])
}

func TestDeleteBucketRemovesVersionedObjects(t *testing.T) {
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Has("list-type"):
			_, _ = w.Write([]byte(`<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`))
		case r.Method == http.MethodGet && r.URL.Query().Has("versions"):
			_, _ = w.Write([]byte(`<ListVersionsResult><Version><Key>logo.png</Key><VersionId>v1</VersionId></Version><DeleteMarker><Key>logo.png</Key><VersionId>v0</VersionId></DeleteMarker><IsTruncated>false</IsTruncated></ListVersionsResult>`))
		case r.Method == http.MethodGet && r.URL.Query().Has("uploads"):
			_, _ = w.Write([]byte(`<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`))
		case r.Method == http.MethodDelete && r.URL.Query().Get("versionId") != "":
			deleted = append(deleted, r.URL.Query().Get("versionId"))
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	require.NoError(t, client.DeleteBucket("uploads"))
	require.ElementsMatch(t, []string{"v1", "v0"}, deleted)
}

func TestDeleteBucketFollowsContinuationToken(t *testing.T) {
	var tokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Has("list-type") {
			tokens = append(tokens, r.URL.Query().Get("continuation-token"))
			if r.URL.Query().Get("continuation-token") == "" {
				_, _ = w.Write([]byte(`<ListBucketResult><Contents><Key>a</Key></Contents><IsTruncated>true</IsTruncated><NextContinuationToken>page-2</NextContinuationToken></ListBucketResult>`))
				return
			}
			_, _ = w.Write([]byte(`<ListBucketResult><Contents><Key>b</Key></Contents><IsTruncated>false</IsTruncated></ListBucketResult>`))
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`<ListVersionsResult><IsTruncated>false</IsTruncated></ListVersionsResult>`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	require.NoError(t, client.DeleteBucket("uploads"))
	require.Equal(t, []string{"", "page-2"}, tokens)
}

func TestDeleteBucketFailsWhenListVersionsForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Has("list-type"):
			_, _ = w.Write([]byte(`<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`))
		case r.Method == http.MethodGet && r.URL.Query().Has("versions"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	err := client.DeleteBucket("uploads")
	require.Error(t, err)
	require.Contains(t, err.Error(), "403")
}

func TestDeleteBucketTreatsUnsupportedVersionsAsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Has("list-type"):
			_, _ = w.Write([]byte(`<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`))
		case r.Method == http.MethodGet && r.URL.Query().Has("versions"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<Error><Code>NotImplemented</Code></Error>`))
		case r.Method == http.MethodGet && r.URL.Query().Has("uploads"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<Error><Code>NotImplemented</Code></Error>`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	require.NoError(t, client.DeleteBucket("uploads"))
}

func TestDeleteBucketTreatsMissingBucketAsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchBucket</Code><Message>missing</Message></Error>`))
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	require.NoError(t, client.DeleteBucket("uploads"))
}

func TestDeleteBucketFailsWhenListingDoesNotAdvance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Has("list-type") {
			_, _ = w.Write([]byte(`<ListBucketResult><Contents><Key>a</Key></Contents><IsTruncated>true</IsTruncated><NextContinuationToken>stuck</NextContinuationToken></ListBucketResult>`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	err := client.DeleteBucket("uploads")
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing did not advance")
}

func TestDeleteBucketFailsWhenTruncatedListingHasEmptyToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Has("list-type") {
			_, _ = w.Write([]byte(`<ListBucketResult><Contents><Key>a</Key></Contents><IsTruncated>true</IsTruncated></ListBucketResult>`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	err := client.DeleteBucket("uploads")
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing did not advance")
}

func TestDeleteBucketFailsWhenTruncatedVersionsPageIsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Has("list-type"):
			_, _ = w.Write([]byte(`<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`))
		case r.Method == http.MethodGet && r.URL.Query().Has("versions"):
			_, _ = w.Write([]byte(`<ListVersionsResult><IsTruncated>true</IsTruncated></ListVersionsResult>`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	err := client.DeleteBucket("uploads")
	require.Error(t, err)
	require.Contains(t, err.Error(), "version listing did not advance")
}

func TestDeleteBucketFailsWhenTruncatedMultipartPageIsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Has("list-type"):
			_, _ = w.Write([]byte(`<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`))
		case r.Method == http.MethodGet && r.URL.Query().Has("versions"):
			_, _ = w.Write([]byte(`<ListVersionsResult><IsTruncated>false</IsTruncated></ListVersionsResult>`))
		case r.Method == http.MethodGet && r.URL.Query().Has("uploads"):
			_, _ = w.Write([]byte(`<ListMultipartUploadsResult><IsTruncated>true</IsTruncated></ListMultipartUploadsResult>`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	err := client.DeleteBucket("uploads")
	require.Error(t, err)
	require.Contains(t, err.Error(), "multipart listing did not advance")
}

func TestDeleteBucketFailsWhenListingResponseExceedsLimit(t *testing.T) {
	body := strings.Repeat("a", s3ListResponseLimit+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Has("list-type") {
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	err := client.DeleteBucket("uploads")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeded")
}

func TestDeleteBucketDoesNotTreatGeneric400AsUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Has("list-type"):
			_, _ = w.Write([]byte(`<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`))
		case r.Method == http.MethodGet && r.URL.Query().Has("versions"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<Error><Code>InvalidRequest</Code></Error>`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	err := client.DeleteBucket("uploads")
	require.Error(t, err)
	require.Contains(t, err.Error(), "400")
}

func TestDeleteBucketFailsWhenListingExceedsPageCap(t *testing.T) {
	previous := s3EmptyMaxPages
	s3EmptyMaxPages = 2
	t.Cleanup(func() { s3EmptyMaxPages = previous })

	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Has("list-type") {
			page++
			_, _ = w.Write([]byte(fmt.Sprintf(`<ListBucketResult><Contents><Key>a-%d</Key></Contents><IsTruncated>true</IsTruncated><NextContinuationToken>page-%d</NextContinuationToken></ListBucketResult>`, page, page+1)))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	err := client.DeleteBucket("uploads")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeded")
}

func TestEnsureBucketFailsWhenResponseExceedsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("a", s3ListResponseLimit+1)))
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	err := client.EnsureBucket("uploads")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeded")
}

func TestIAMCallFailsWhenResponseExceedsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("a", s3ListResponseLimit+1)))
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", IAMEndpoint: srv.URL}
	_, _, err := client.EnsureBucketUser([]string{"uploads"}, "assets", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeded")
}
