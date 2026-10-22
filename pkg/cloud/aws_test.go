package cloud

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
			require.Contains(t, r.FormValue("PolicyDocument"), "s3:ListBucketVersions", "pruning in a versioned bucket must be able to list versions")
			require.Contains(t, r.FormValue("PolicyDocument"), "s3:DeleteObjectVersion")
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
			_, _ = w.Write([]byte(`<ListAccessKeysResponse><ListAccessKeysResult><AccessKeyMetadata><member><AccessKeyId>AKIASCOPED</AccessKeyId></member></AccessKeyMetadata></ListAccessKeysResult></ListAccessKeysResponse>`))
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
		switch r.Method {
		case http.MethodGet:
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
			_, _ = fmt.Fprintf(w, `<ListBucketResult><Contents><Key>a-%d</Key></Contents><IsTruncated>true</IsTruncated><NextContinuationToken>page-%d</NextContinuationToken></ListBucketResult>`, page, page+1)
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

func TestDeletePrefixOnlyRemovesObjectsUnderThePrefix(t *testing.T) {
	var listedPrefixes, deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Has("list-type"):
			listedPrefixes = append(listedPrefixes, r.URL.Query().Get("prefix"))
			_, _ = w.Write([]byte(`<ListBucketResult><Contents><Key>payments/orders-1a2b/wals/0001</Key></Contents><Contents><Key>payments/orders-1a2b/base/2026/data.tar.gz</Key></Contents><IsTruncated>false</IsTruncated></ListBucketResult>`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<Result><IsTruncated>false</IsTruncated></Result>`))
		case r.Method == http.MethodDelete:
			deleted = append(deleted, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	require.NoError(t, client.DeletePrefix("backups", "payments/orders-1a2b"))

	require.Equal(t, []string{"payments/orders-1a2b/"}, listedPrefixes, "a trailing slash stops orders-1a2b from also matching orders-1a2bc")
	require.ElementsMatch(t, []string{"/backups/payments/orders-1a2b/wals/0001", "/backups/payments/orders-1a2b/base/2026/data.tar.gz"}, deleted)
	require.NotContains(t, deleted, "/backups", "the bucket itself must survive a prefix purge")
}

func TestDeletePrefixRefusesAnEmptyPrefix(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	for _, prefix := range []string{"", "  ", "/"} {
		require.Error(t, client.DeletePrefix("backups", prefix), "prefix %q would purge the whole bucket", prefix)
	}
	require.Error(t, client.DeletePrefix("", "payments/orders"))
	require.False(t, called, "no request may be sent for an unsafe prefix")
}

func TestDeletePrefixRemovesVersionsAndMultipartUploadsUnderThePrefix(t *testing.T) {
	var versionPrefix, uploadPrefix string
	var deletedVersions, abortedUploads []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Has("list-type"):
			_, _ = w.Write([]byte(`<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`))
		case r.Method == http.MethodGet && r.URL.Query().Has("versions"):
			versionPrefix = r.URL.Query().Get("prefix")
			_, _ = w.Write([]byte(`<ListVersionsResult><Version><Key>p/x/wals/1</Key><VersionId>v1</VersionId></Version><DeleteMarker><Key>p/x/wals/1</Key><VersionId>v0</VersionId></DeleteMarker><IsTruncated>false</IsTruncated></ListVersionsResult>`))
		case r.Method == http.MethodGet && r.URL.Query().Has("uploads"):
			uploadPrefix = r.URL.Query().Get("prefix")
			_, _ = w.Write([]byte(`<ListMultipartUploadsResult><Upload><Key>p/x/base/big</Key><UploadId>u1</UploadId></Upload><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`))
		case r.Method == http.MethodDelete && r.URL.Query().Get("versionId") != "":
			deletedVersions = append(deletedVersions, r.URL.Query().Get("versionId"))
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Query().Get("uploadId") != "":
			abortedUploads = append(abortedUploads, r.URL.Query().Get("uploadId"))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	require.NoError(t, client.DeletePrefix("backups", "p/x/"))
	require.Equal(t, "p/x/", versionPrefix)
	require.Equal(t, "p/x/", uploadPrefix)
	require.ElementsMatch(t, []string{"v1", "v0"}, deletedVersions)
	require.Equal(t, []string{"u1"}, abortedUploads)
}

func TestDeletePrefixTreatsMissingBucketAsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchBucket</Code></Error>`))
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	require.NoError(t, client.DeletePrefix("backups", "p/x/"))
}

func TestDeletePrefixFailsWhenListingIsForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	require.Error(t, client.DeletePrefix("backups", "p/x/"), "a purge that could not list must not report success and drop the record of the data")
}

func TestListKeysReturnsEveryKeyUnderThePrefixAcrossPages(t *testing.T) {
	var requested []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "p/s/dumps/", r.URL.Query().Get("prefix"))
		token := r.URL.Query().Get("continuation-token")
		requested = append(requested, token)
		switch token {
		case "":
			_, _ = w.Write([]byte(`<ListBucketResult><Contents><Key>p/s/dumps/a.sql.gz</Key></Contents><Contents><Key>p/s/dumps/b.sql.gz</Key></Contents><IsTruncated>true</IsTruncated><NextContinuationToken>page2</NextContinuationToken></ListBucketResult>`))
		case "page2":
			_, _ = w.Write([]byte(`<ListBucketResult><Contents><Key>p/s/dumps/c.sql.gz</Key></Contents><IsTruncated>false</IsTruncated></ListBucketResult>`))
		default:
			t.Errorf("unexpected token %q", token)
		}
	}))
	t.Cleanup(srv.Close)

	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	keys, err := client.ListKeys("backups", "p/s/dumps")
	require.NoError(t, err)
	require.Equal(t, []string{"p/s/dumps/a.sql.gz", "p/s/dumps/b.sql.gz", "p/s/dumps/c.sql.gz"}, keys)
	require.Equal(t, []string{"", "page2"}, requested)
}

func TestListKeysRefusesAnUnsafePrefixAndReportsFailures(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
	}))
	t.Cleanup(srv.Close)
	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}

	_, err := client.ListKeys("backups", "")
	require.Error(t, err, "listing a whole bucket is never what a database wants")
	_, err = client.ListKeys("", "p/s/")
	require.Error(t, err)
	require.False(t, called, "no request may be sent for an unsafe prefix")

	_, err = client.ListKeys("backups", "p/s/")
	require.ErrorContains(t, err, "AccessDenied", "a failed listing must not look like an empty folder")
}

func TestDeleteObjectRemovesOneKeyAndTreatsAMissingOneAsDone(t *testing.T) {
	var deleted []string
	status := http.StatusNoContent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Query().Has("versions") {
			_, _ = w.Write([]byte(`<ListVersionsResult><IsTruncated>false</IsTruncated></ListVersionsResult>`))
			return
		}
		require.Equal(t, http.MethodDelete, r.Method)
		deleted = append(deleted, r.URL.Path)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}

	require.NoError(t, client.DeleteObject("backups", "p/s/dumps/20261011T020000Z.sql.gz"))
	require.Equal(t, []string{"/backups/p/s/dumps/20261011T020000Z.sql.gz"}, deleted)

	status = http.StatusNotFound
	require.NoError(t, client.DeleteObject("backups", "p/s/dumps/gone.sql.gz"), "already deleted is the outcome that was wanted")

	status = http.StatusForbidden
	require.Error(t, client.DeleteObject("backups", "p/s/dumps/x.sql.gz"))
	require.Error(t, client.DeleteObject("backups", ""), "an empty key would address the bucket itself")
	require.Error(t, client.DeleteObject("", "k"))
}

func TestDeleteObjectRemovesEveryVersionOfThatKeyOnly(t *testing.T) {
	var versionPrefix string
	var deletedVersions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Query().Has("versions"):
			versionPrefix = r.URL.Query().Get("prefix")
			_, _ = w.Write([]byte(`<ListVersionsResult>` +
				`<Version><Key>p/s/dumps/a.sql.gz</Key><VersionId>v1</VersionId></Version>` +
				`<DeleteMarker><Key>p/s/dumps/a.sql.gz</Key><VersionId>v2</VersionId></DeleteMarker>` +
				`<Version><Key>p/s/dumps/a.sql.gz.keep</Key><VersionId>other</VersionId></Version>` +
				`<IsTruncated>false</IsTruncated></ListVersionsResult>`))
		case r.Method == http.MethodDelete && r.URL.Query().Get("versionId") != "":
			deletedVersions = append(deletedVersions, r.URL.Query().Get("versionId"))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}

	require.NoError(t, client.DeleteObject("backups", "p/s/dumps/a.sql.gz"))

	require.Equal(t, "p/s/dumps/a.sql.gz", versionPrefix)
	require.ElementsMatch(t, []string{"v1", "v2"}, deletedVersions,
		"a versioned bucket keeps old versions behind a delete marker, and a key that merely starts the same must be left alone")
}

func TestAnObjectIsWrittenAndReadBackThroughSignedRequests(t *testing.T) {
	stored := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Contains(t, r.Header.Get("Authorization"), "AWS4-HMAC-SHA256")
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(body)), r.Header.Get("X-Amz-Content-Sha256"), "what is written is signed whole")
			stored[r.URL.Path] = body
		case http.MethodGet:
			body, ok := stored[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code></Error>`))
				return
			}
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(srv.Close)
	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}

	require.NoError(t, client.PutObject("state", "shop/orders-1a2b3c4d/geass-archive.json", []byte(`{"schema":1}`)))
	got, err := client.GetObject("state", "shop/orders-1a2b3c4d/geass-archive.json", 1024)
	require.NoError(t, err)
	require.JSONEq(t, `{"schema":1}`, string(got))
	require.Contains(t, stored, "/state/shop/orders-1a2b3c4d/geass-archive.json", "path style, with the key as it is written, slashes and all")

	_, err = client.GetObject("state", "shop/none/geass-archive.json", 1024)
	require.ErrorIs(t, err, ErrObjectNotFound, "an object that is not there is told from a bucket that cannot be read")
}

func TestAnObjectOverTheLimitIsAnErrorAndNotAShortRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, 5000)) }))
	t.Cleanup(srv.Close)
	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}
	_, err := client.GetObject("state", "big", 100)
	require.ErrorContains(t, err, "exceeded")
}

func TestFoldersAreTheNamesDirectlyUnderAPrefixAcrossPages(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		require.Equal(t, "/", r.URL.Query().Get("delimiter"))
		if r.URL.Query().Get("continuation-token") == "" {
			_, _ = w.Write([]byte(`<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>page2</NextContinuationToken>
				<CommonPrefixes><Prefix>shop/orders-1a2b3c4d/</Prefix></CommonPrefixes><Contents><Key>shop/stray.txt</Key></Contents></ListBucketResult>`))
			return
		}
		_, _ = w.Write([]byte(`<ListBucketResult><IsTruncated>false</IsTruncated><CommonPrefixes><Prefix>shop/cache-9f8e7d6c/</Prefix></CommonPrefixes></ListBucketResult>`))
	}))
	t.Cleanup(srv.Close)
	client := &AWSClient{HTTP: srv.Client(), AccessKey: "AKIA", SecretKey: "secret", Endpoint: srv.URL}

	folders, err := client.ListFolders("state", "shop")
	require.NoError(t, err)
	require.Equal(t, []string{"shop/orders-1a2b3c4d/", "shop/cache-9f8e7d6c/"}, folders, "the slash is added, and a stray file is not a folder")
	require.Contains(t, queries[0], "prefix=shop%2F")

	queries = nil
	_, err = client.ListFolders("state", "")
	require.NoError(t, err)
	require.NotContains(t, queries[0], "prefix=", "the top of the bucket has no prefix to name")
}

// signedEC2Query is the request the signing tests sign: an EC2 Query API call, at a fixed moment.
func signedEC2Query(t *testing.T, client *AWSClient) *http.Request {
	t.Helper()
	body := []byte("Action=DescribeInstances&Version=2016-11-15")
	req, err := http.NewRequest(http.MethodPost, "https://ec2.eu-north-1.amazonaws.com/", strings.NewReader(string(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	require.NoError(t, client.signAt(req, body, "ec2", "eu-north-1", time.Date(2026, time.October, 22, 12, 30, 45, 0, time.UTC)))
	return req
}

// The expected signatures in the signing tests were worked out by a separate implementation of SigV4 (a short Python
// script that shares no code with this package), so that they check the signer and do not repeat it.
func TestAnIAMUsersKeySignsTheSameThreeHeadersAsEver(t *testing.T) {
	req := signedEC2Query(t, &AWSClient{AccessKey: "AKIDEXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"})

	require.Equal(t, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20261022/eu-north-1/ec2/aws4_request, "+
		"SignedHeaders=host;x-amz-content-sha256;x-amz-date, "+
		"Signature=d548bd8c12a50f1517de6854f30507963779e5ba40d3c1b35636b4ffff4c2fa9", req.Header.Get("Authorization"))
	require.Equal(t, "20261022T123045Z", req.Header.Get("X-Amz-Date"))
	require.Equal(t, "6171eb09865e32b0602af0f7957e26573a51f53caaedff02ff88883cb0275885", req.Header.Get("X-Amz-Content-Sha256"))
	require.Empty(t, req.Header.Values("X-Amz-Security-Token"), "a key with no session token sends none")
}
