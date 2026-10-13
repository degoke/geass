package cloud

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// iamRecorder answers SeaweedFS's IAM API and records the policy it was given and the keys it was asked about.
type iamRecorder struct {
	srv       *httptest.Server
	policy    string
	actions   []string
	listedKey string // the access key the server says the user has, if any
}

func newIAMRecorder(t *testing.T) *iamRecorder {
	t.Helper()
	rec := &iamRecorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		action := r.FormValue("Action")
		rec.actions = append(rec.actions, action)
		switch action {
		case "PutUserPolicy":
			rec.policy = r.FormValue("PolicyDocument")
		case "ListAccessKeys":
			members := ""
			if rec.listedKey != "" {
				members = "<member><AccessKeyId>" + rec.listedKey + "</AccessKeyId></member>"
			}
			_, _ = w.Write([]byte("<ListAccessKeysResponse><ListAccessKeysResult><AccessKeyMetadata>" + members + "</AccessKeyMetadata></ListAccessKeysResult></ListAccessKeysResponse>"))
		case "CreateAccessKey":
			_, _ = w.Write([]byte(`<CreateAccessKeyResponse><CreateAccessKeyResult><AccessKey><AccessKeyId>AKIANEW</AccessKeyId><SecretAccessKey>new-secret</SecretAccessKey></AccessKey></CreateAccessKeyResult></CreateAccessKeyResponse>`))
		}
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

// SeaweedFS refuses a policy that names an AWS action it does not have, so a bucket user's policy names only the
// ones it knows.
func TestSeaweedFSBucketPolicyUsesOnlyActionsItKnows(t *testing.T) {
	rec := newIAMRecorder(t)
	client := NewSeaweedFSClient(rec.srv.Client(), rec.srv.URL, "root", "root-secret")

	_, _, err := client.EnsureBucketUser([]string{"uploads"}, "assets", "", "")

	require.NoError(t, err)
	for _, unknown := range []string{"ListBucketMultipartUploads", "ListMultipartUploadParts"} {
		require.NotContains(t, rec.policy, unknown)
	}
	for _, needed := range []string{"s3:ListBucket", "s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload", "arn:aws:s3:::uploads/*"} {
		require.Contains(t, rec.policy, needed)
	}
}

// On AWS the policy keeps the action names AWS has. Only a server that sets its own actions changes them.
func TestAWSBucketPolicyKeepsTheAWSActionNames(t *testing.T) {
	rec := newIAMRecorder(t)
	client := &AWSClient{HTTP: rec.srv.Client(), AccessKey: "a", SecretKey: "b", IAMEndpoint: rec.srv.URL}

	_, _, err := client.EnsureBucketUser([]string{"uploads"}, "assets", "", "")

	require.NoError(t, err)
	require.Contains(t, rec.policy, "s3:ListBucketMultipartUploads")
	require.Contains(t, rec.policy, "s3:ListMultipartUploadParts")
}

// A stored key is kept while the server still has it. A server that was reset has not, and gets a new one.
func TestSeaweedFSKeepsAStoredKeyOnlyWhileTheServerHasIt(t *testing.T) {
	rec := newIAMRecorder(t)
	client := NewSeaweedFSClient(rec.srv.Client(), rec.srv.URL, "root", "root-secret")

	rec.listedKey = "AKIASTORED"
	access, secret, err := client.EnsureBucketUser([]string{"uploads"}, "assets", "AKIASTORED", "stored-secret")
	require.NoError(t, err)
	require.Equal(t, "AKIASTORED", access)
	require.Equal(t, "stored-secret", secret)
	require.NotContains(t, rec.actions, "CreateAccessKey")

	rec.listedKey = ""
	access, secret, err = client.EnsureBucketUser([]string{"uploads"}, "assets", "AKIASTORED", "stored-secret")
	require.NoError(t, err)
	require.Equal(t, "AKIANEW", access)
	require.Equal(t, "new-secret", secret)
}

// AWS is not asked for its keys again: the check is only for servers that can forget them.
func TestAWSDoesNotListKeysToCheckAStoredOne(t *testing.T) {
	rec := newIAMRecorder(t)
	client := &AWSClient{HTTP: rec.srv.Client(), AccessKey: "a", SecretKey: "b", IAMEndpoint: rec.srv.URL}

	access, _, err := client.EnsureBucketUser([]string{"uploads"}, "assets", "AKIASTORED", "stored-secret")

	require.NoError(t, err)
	require.Equal(t, "AKIASTORED", access)
	require.NotContains(t, strings.Join(rec.actions, ","), "ListAccessKeys")
}

// IAM lists keys as <member> elements, as AWS and SeaweedFS both answer. A parser that expects them bare finds
// none, and a bucket user is then deleted with its keys still on it.
func TestListAccessKeysReadsTheMemberElementsIAMSends(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><ListAccessKeysResponse xmlns="https://iam.amazonaws.com/doc/2010-05-08/"><ListAccessKeysResult><AccessKeyMetadata><member><AccessKeyId>AKIA1</AccessKeyId><Status>Active</Status></member><member><AccessKeyId>AKIA2</AccessKeyId></member></AccessKeyMetadata><IsTruncated>false</IsTruncated></ListAccessKeysResult></ListAccessKeysResponse>`))
	}))
	t.Cleanup(srv.Close)
	client := &AWSClient{HTTP: srv.Client(), AccessKey: "a", SecretKey: "b", IAMEndpoint: srv.URL}

	keys, err := client.iamListAccessKeys("geass-assets")

	require.NoError(t, err)
	require.Equal(t, []string{"AKIA1", "AKIA2"}, keys)
}
