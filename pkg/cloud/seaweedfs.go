package cloud

import "net/http"

// NewSeaweedFSClient returns a client for a SeaweedFS server's S3 API and the IAM API it serves on the same
// port, signed with the server's admin login. SeaweedFS speaks AWS's IAM protocol but not every AWS action
// name: it has no "ListBucketMultipartUploads" or "ListMultipartUploadParts", and allows multipart uploads
// to whoever may PutObject, so a bucket user's policy names the actions it does know.
func NewSeaweedFSClient(httpClient *http.Client, endpoint, accessKey, secretKey string) *AWSClient {
	return &AWSClient{
		HTTP:            httpClient,
		AccessKey:       accessKey,
		SecretKey:       secretKey,
		Endpoint:        endpoint,
		IAMEndpoint:     endpoint,
		RenewMissingKey: true,
		BucketActions:   []string{"s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketVersions", "s3:ListMultipartUploads"},
		ObjectActions:   []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:DeleteObjectVersion", "s3:AbortMultipartUpload"},
	}
}
