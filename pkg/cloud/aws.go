package cloud

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AWSClient performs a small set of signed S3 and IAM operations without an SDK.
type AWSClient struct {
	HTTP        *http.Client
	AccessKey   string
	SecretKey   string
	Region      string
	Endpoint    string
	IAMEndpoint string
}

func (c *AWSClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *AWSClient) region() string {
	if strings.TrimSpace(c.Region) != "" {
		return c.Region
	}
	return "us-east-1"
}

// EnsureBucket creates the bucket when it does not already exist.
func (c *AWSClient) EnsureBucket(bucket string) error {
	if strings.TrimSpace(c.Endpoint) != "" {
		return c.ensurePathStyleBucket(bucket)
	}
	region := c.region()
	host := fmt.Sprintf("%s.s3.%s.amazonaws.com", bucket, region)
	if region == "us-east-1" {
		host = fmt.Sprintf("%s.s3.amazonaws.com", bucket)
	}
	body := []byte{}
	if region != "us-east-1" {
		body = fmt.Appendf(nil, `<CreateBucketConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><LocationConstraint>%s</LocationConstraint></CreateBucketConfiguration>`, region)
	}
	req, err := http.NewRequest(http.MethodPut, "https://"+host, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/xml")
	}
	if err := c.sign(req, body, "s3"); err != nil {
		return err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 300 || resp.StatusCode == http.StatusConflict {
		return nil
	}
	return fmt.Errorf("AWS S3 %s: %s", resp.Status, strings.TrimSpace(string(payload)))
}

// EnsureBucketUser creates an IAM user scoped to the buckets and returns access keys.
// existingAccess and existingSecret are reused when already stored so CreateAccessKey
// is not called again. Cluster root keys are never returned as a fallback.
func (c *AWSClient) EnsureBucketUser(buckets []string, userName, existingAccess, existingSecret string) (string, string, error) {
	if len(buckets) == 0 || strings.TrimSpace(buckets[0]) == "" {
		return "", "", fmt.Errorf("bucket is required")
	}
	userName = iamUserName(userName)
	if err := c.iamCreateUser(userName); err != nil {
		return "", "", err
	}
	if err := c.iamPutUserPolicy(userName, buckets); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(existingAccess) != "" && strings.TrimSpace(existingSecret) != "" {
		return existingAccess, existingSecret, nil
	}
	return c.iamCreateAccessKey(userName)
}

// DeleteBucketUser removes the scoped IAM user, its access keys, and policy.
// Missing users are treated as already deleted.
func (c *AWSClient) DeleteBucketUser(userName string) error {
	userName = iamUserName(userName)
	keys, err := c.iamListAccessKeys(userName)
	if err != nil && !isIAMNoSuchEntity(err) {
		return err
	}
	for _, key := range keys {
		if err := c.iamDeleteAccessKey(userName, key); err != nil && !isIAMNoSuchEntity(err) {
			return err
		}
	}
	if err := c.iamDeleteUserPolicy(userName); err != nil && !isIAMNoSuchEntity(err) {
		return err
	}
	if err := c.iamDeleteUser(userName); err != nil && !isIAMNoSuchEntity(err) {
		return err
	}
	return nil
}

// DeleteBucket removes the bucket after deleting its objects. Missing buckets
// are treated as already deleted.
func (c *AWSClient) DeleteBucket(bucket string) error {
	if err := c.emptyBucket(bucket); err != nil && !isS3NoSuchBucket(err) {
		return err
	}
	if err := c.deleteBucketRequest(bucket); err != nil && !isS3NoSuchBucket(err) {
		return err
	}
	return nil
}

func (c *AWSClient) emptyBucket(bucket string) error {
	token := ""
	for {
		keys, next, err := c.listBucketKeys(bucket, token)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if err := c.deleteObject(bucket, key, ""); err != nil && !isS3NoSuchBucket(err) {
				return err
			}
		}
		if next == "" {
			break
		}
		if next == token {
			return fmt.Errorf("bucket %s listing did not advance", bucket)
		}
		token = next
	}
	if err := c.emptyBucketVersions(bucket); err != nil && !isS3NoSuchBucket(err) {
		return err
	}
	if err := c.abortMultipartUploads(bucket); err != nil && !isS3NoSuchBucket(err) {
		return err
	}
	return nil
}

func (c *AWSClient) listBucketKeys(bucket, continuation string) ([]string, string, error) {
	query := url.Values{"list-type": {"2"}}
	if continuation != "" {
		query.Set("continuation-token", continuation)
	}
	req, err := http.NewRequest(http.MethodGet, c.bucketURL(bucket)+"?"+query.Encode(), http.NoBody)
	if err != nil {
		return nil, "", err
	}
	if err := c.sign(req, nil, "s3"); err != nil {
		return nil, "", err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound || isS3ErrorCode(payload, "NoSuchBucket") {
		return nil, "", fmt.Errorf("AWS S3 NoSuchBucket: %s", strings.TrimSpace(string(payload)))
	}
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("AWS S3 %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	var parsed struct {
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
		IsTruncated           bool   `xml:"IsTruncated"`
		NextContinuationToken string `xml:"NextContinuationToken"`
	}
	if err := xml.Unmarshal(payload, &parsed); err != nil {
		return nil, "", fmt.Errorf("S3 ListObjects: %w", err)
	}
	keys := make([]string, 0, len(parsed.Contents))
	for _, item := range parsed.Contents {
		if item.Key != "" {
			keys = append(keys, item.Key)
		}
	}
	if parsed.IsTruncated {
		if parsed.NextContinuationToken == "" {
			return nil, "", fmt.Errorf("bucket %s listing did not advance", bucket)
		}
		return keys, parsed.NextContinuationToken, nil
	}
	return keys, "", nil
}

func (c *AWSClient) emptyBucketVersions(bucket string) error {
	keyMarker, versionMarker := "", ""
	for {
		versions, nextKey, nextVersion, truncated, err := c.listBucketVersions(bucket, keyMarker, versionMarker)
		if err != nil {
			if isS3NoSuchBucket(err) {
				return err
			}
			if isS3Unsupported(err) {
				return nil
			}
			return err
		}
		for _, version := range versions {
			if err := c.deleteObject(bucket, version.key, version.versionID); err != nil && !isS3NoSuchBucket(err) {
				return err
			}
		}
		if !truncated {
			return nil
		}
		if len(versions) == 0 || (nextKey == "" && nextVersion == "") || (nextKey == keyMarker && nextVersion == versionMarker) {
			return fmt.Errorf("bucket %s version listing did not advance", bucket)
		}
		keyMarker, versionMarker = nextKey, nextVersion
	}
}

type objectVersion struct {
	key       string
	versionID string
}

func (c *AWSClient) listBucketVersions(bucket, keyMarker, versionMarker string) ([]objectVersion, string, string, bool, error) {
	query := url.Values{"versions": {""}}
	if keyMarker != "" {
		query.Set("key-marker", keyMarker)
	}
	if versionMarker != "" {
		query.Set("version-id-marker", versionMarker)
	}
	req, err := http.NewRequest(http.MethodGet, c.bucketURL(bucket)+"?"+query.Encode(), http.NoBody)
	if err != nil {
		return nil, "", "", false, err
	}
	if err := c.sign(req, nil, "s3"); err != nil {
		return nil, "", "", false, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, "", "", false, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound || isS3ErrorCode(payload, "NoSuchBucket") {
		return nil, "", "", false, fmt.Errorf("AWS S3 NoSuchBucket: %s", strings.TrimSpace(string(payload)))
	}
	if resp.StatusCode >= 300 {
		return nil, "", "", false, fmt.Errorf("AWS S3 %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	var parsed struct {
		Versions []struct {
			Key       string `xml:"Key"`
			VersionID string `xml:"VersionId"`
		} `xml:"Version"`
		DeleteMarkers []struct {
			Key       string `xml:"Key"`
			VersionID string `xml:"VersionId"`
		} `xml:"DeleteMarker"`
		IsTruncated         bool   `xml:"IsTruncated"`
		NextKeyMarker       string `xml:"NextKeyMarker"`
		NextVersionIdMarker string `xml:"NextVersionIdMarker"`
	}
	if err := xml.Unmarshal(payload, &parsed); err != nil {
		return nil, "", "", false, fmt.Errorf("S3 ListObjectVersions: %w", err)
	}
	versions := make([]objectVersion, 0, len(parsed.Versions)+len(parsed.DeleteMarkers))
	for _, item := range parsed.Versions {
		if item.Key != "" {
			versions = append(versions, objectVersion{key: item.Key, versionID: item.VersionID})
		}
	}
	for _, item := range parsed.DeleteMarkers {
		if item.Key != "" {
			versions = append(versions, objectVersion{key: item.Key, versionID: item.VersionID})
		}
	}
	return versions, parsed.NextKeyMarker, parsed.NextVersionIdMarker, parsed.IsTruncated, nil
}

func (c *AWSClient) abortMultipartUploads(bucket string) error {
	keyMarker, uploadMarker := "", ""
	for {
		uploads, nextKey, nextUpload, truncated, err := c.listMultipartUploads(bucket, keyMarker, uploadMarker)
		if err != nil {
			if isS3NoSuchBucket(err) {
				return err
			}
			if isS3Unsupported(err) {
				return nil
			}
			return err
		}
		for _, upload := range uploads {
			if err := c.abortMultipartUpload(bucket, upload.key, upload.uploadID); err != nil && !isS3NoSuchBucket(err) {
				return err
			}
		}
		if !truncated {
			return nil
		}
		if len(uploads) == 0 || (nextKey == "" && nextUpload == "") || (nextKey == keyMarker && nextUpload == uploadMarker) {
			return fmt.Errorf("bucket %s multipart listing did not advance", bucket)
		}
		keyMarker, uploadMarker = nextKey, nextUpload
	}
}

type multipartUpload struct {
	key      string
	uploadID string
}

func (c *AWSClient) listMultipartUploads(bucket, keyMarker, uploadMarker string) ([]multipartUpload, string, string, bool, error) {
	query := url.Values{"uploads": {""}}
	if keyMarker != "" {
		query.Set("key-marker", keyMarker)
	}
	if uploadMarker != "" {
		query.Set("upload-id-marker", uploadMarker)
	}
	req, err := http.NewRequest(http.MethodGet, c.bucketURL(bucket)+"?"+query.Encode(), http.NoBody)
	if err != nil {
		return nil, "", "", false, err
	}
	if err := c.sign(req, nil, "s3"); err != nil {
		return nil, "", "", false, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, "", "", false, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound || isS3ErrorCode(payload, "NoSuchBucket") {
		return nil, "", "", false, fmt.Errorf("AWS S3 NoSuchBucket: %s", strings.TrimSpace(string(payload)))
	}
	if resp.StatusCode >= 300 {
		return nil, "", "", false, fmt.Errorf("AWS S3 %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	var parsed struct {
		Uploads []struct {
			Key      string `xml:"Key"`
			UploadID string `xml:"UploadId"`
		} `xml:"Upload"`
		IsTruncated        bool   `xml:"IsTruncated"`
		NextKeyMarker      string `xml:"NextKeyMarker"`
		NextUploadIdMarker string `xml:"NextUploadIdMarker"`
	}
	if err := xml.Unmarshal(payload, &parsed); err != nil {
		return nil, "", "", false, fmt.Errorf("S3 ListMultipartUploads: %w", err)
	}
	uploads := make([]multipartUpload, 0, len(parsed.Uploads))
	for _, item := range parsed.Uploads {
		if item.Key != "" && item.UploadID != "" {
			uploads = append(uploads, multipartUpload{key: item.Key, uploadID: item.UploadID})
		}
	}
	return uploads, parsed.NextKeyMarker, parsed.NextUploadIdMarker, parsed.IsTruncated, nil
}

func (c *AWSClient) abortMultipartUpload(bucket, key, uploadID string) error {
	req, err := http.NewRequest(http.MethodDelete, c.objectURL(bucket, key)+"?uploadId="+url.QueryEscape(uploadID), http.NoBody)
	if err != nil {
		return err
	}
	if err := c.sign(req, nil, "s3"); err != nil {
		return err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 300 || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("AWS S3 %s: %s", resp.Status, strings.TrimSpace(string(payload)))
}

func (c *AWSClient) deleteObject(bucket, key, versionID string) error {
	target := c.objectURL(bucket, key)
	if versionID != "" {
		target += "?versionId=" + url.QueryEscape(versionID)
	}
	req, err := http.NewRequest(http.MethodDelete, target, http.NoBody)
	if err != nil {
		return err
	}
	if err := c.sign(req, nil, "s3"); err != nil {
		return err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 300 || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("AWS S3 %s: %s", resp.Status, strings.TrimSpace(string(payload)))
}

func (c *AWSClient) deleteBucketRequest(bucket string) error {
	req, err := http.NewRequest(http.MethodDelete, c.bucketURL(bucket), http.NoBody)
	if err != nil {
		return err
	}
	if err := c.sign(req, nil, "s3"); err != nil {
		return err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound || isS3ErrorCode(payload, "NoSuchBucket") {
		return fmt.Errorf("AWS S3 NoSuchBucket: %s", strings.TrimSpace(string(payload)))
	}
	return fmt.Errorf("AWS S3 %s: %s", resp.Status, strings.TrimSpace(string(payload)))
}

func (c *AWSClient) bucketURL(bucket string) string {
	if endpoint := strings.TrimRight(c.Endpoint, "/"); endpoint != "" {
		return endpoint + "/" + bucket
	}
	region := c.region()
	host := fmt.Sprintf("%s.s3.%s.amazonaws.com", bucket, region)
	if region == "us-east-1" {
		host = fmt.Sprintf("%s.s3.amazonaws.com", bucket)
	}
	return "https://" + host
}

func (c *AWSClient) objectURL(bucket, key string) string {
	escaped := strings.ReplaceAll(url.PathEscape(key), "%2F", "/")
	return c.bucketURL(bucket) + "/" + escaped
}

func isS3NoSuchBucket(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NoSuchBucket")
}

func isS3Unsupported(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "AWS S3 405") || strings.Contains(msg, "NotImplemented") || strings.Contains(msg, "MethodNotAllowed")
}

func isS3ErrorCode(payload []byte, code string) bool {
	var parsed struct {
		Code string `xml:"Code"`
	}
	if err := xml.Unmarshal(payload, &parsed); err != nil {
		return false
	}
	return parsed.Code == code
}

func (c *AWSClient) iamListAccessKeys(userName string) ([]string, error) {
	payload, err := c.iamCall(url.Values{"Action": {"ListAccessKeys"}, "UserName": {userName}, "Version": {"2010-05-08"}})
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Result struct {
			Keys []struct {
				AccessKeyID string `xml:"AccessKeyId"`
			} `xml:"AccessKeyMetadata"`
		} `xml:"ListAccessKeysResult"`
	}
	if err := xml.Unmarshal(payload, &parsed); err != nil {
		return nil, fmt.Errorf("IAM ListAccessKeys: %w", err)
	}
	ids := make([]string, 0, len(parsed.Result.Keys))
	for _, key := range parsed.Result.Keys {
		if key.AccessKeyID != "" {
			ids = append(ids, key.AccessKeyID)
		}
	}
	return ids, nil
}

func (c *AWSClient) iamDeleteAccessKey(userName, accessKeyID string) error {
	_, err := c.iamCall(url.Values{"Action": {"DeleteAccessKey"}, "UserName": {userName}, "AccessKeyId": {accessKeyID}, "Version": {"2010-05-08"}})
	return err
}

func (c *AWSClient) iamDeleteUserPolicy(userName string) error {
	_, err := c.iamCall(url.Values{"Action": {"DeleteUserPolicy"}, "UserName": {userName}, "PolicyName": {"geass-bucket"}, "Version": {"2010-05-08"}})
	return err
}

func (c *AWSClient) iamDeleteUser(userName string) error {
	_, err := c.iamCall(url.Values{"Action": {"DeleteUser"}, "UserName": {userName}, "Version": {"2010-05-08"}})
	return err
}

func (c *AWSClient) iamCreateUser(userName string) error {
	_, err := c.iamCall(url.Values{"Action": {"CreateUser"}, "UserName": {userName}, "Version": {"2010-05-08"}})
	if err == nil || isIAMAlreadyExists(err) {
		return nil
	}
	return err
}

func (c *AWSClient) iamPutUserPolicy(userName string, buckets []string) error {
	type statement struct {
		Effect   string   `json:"Effect"`
		Action   []string `json:"Action"`
		Resource []string `json:"Resource"`
	}
	type document struct {
		Version   string      `json:"Version"`
		Statement []statement `json:"Statement"`
	}
	policy := document{Version: "2012-10-17"}
	for _, bucket := range buckets {
		policy.Statement = append(policy.Statement,
			statement{
				Effect:   "Allow",
				Action:   []string{"s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketMultipartUploads"},
				Resource: []string{"arn:aws:s3:::" + bucket},
			},
			statement{
				Effect:   "Allow",
				Action:   []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload", "s3:ListMultipartUploadParts"},
				Resource: []string{"arn:aws:s3:::" + bucket + "/*"},
			},
		)
	}
	payload, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	_, err = c.iamCall(url.Values{
		"Action":         {"PutUserPolicy"},
		"UserName":       {userName},
		"PolicyName":     {"geass-bucket"},
		"PolicyDocument": {string(payload)},
		"Version":        {"2010-05-08"},
	})
	return err
}

func (c *AWSClient) iamCreateAccessKey(userName string) (string, string, error) {
	payload, err := c.iamCall(url.Values{"Action": {"CreateAccessKey"}, "UserName": {userName}, "Version": {"2010-05-08"}})
	if err != nil {
		return "", "", err
	}
	var parsed struct {
		Result struct {
			AccessKey struct {
				AccessKeyID     string `xml:"AccessKeyId"`
				SecretAccessKey string `xml:"SecretAccessKey"`
			} `xml:"AccessKey"`
		} `xml:"CreateAccessKeyResult"`
	}
	if err := xml.Unmarshal(payload, &parsed); err != nil {
		return "", "", fmt.Errorf("IAM CreateAccessKey: %w", err)
	}
	if parsed.Result.AccessKey.AccessKeyID == "" || parsed.Result.AccessKey.SecretAccessKey == "" {
		return "", "", fmt.Errorf("IAM CreateAccessKey did not return credentials")
	}
	return parsed.Result.AccessKey.AccessKeyID, parsed.Result.AccessKey.SecretAccessKey, nil
}

func (c *AWSClient) iamCall(values url.Values) ([]byte, error) {
	body := []byte(values.Encode())
	endpoint := strings.TrimRight(c.IAMEndpoint, "/")
	if endpoint == "" {
		endpoint = "https://iam.amazonaws.com"
	}
	req, err := http.NewRequest(http.MethodPost, endpoint+"/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	region := "us-east-1"
	if err := c.signWithRegion(req, body, "iam", region); err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 300 {
		return payload, nil
	}
	code, message := iamError(payload)
	if code != "" {
		return payload, fmt.Errorf("AWS IAM %s: %s", code, message)
	}
	return payload, fmt.Errorf("AWS IAM %s: %s", resp.Status, strings.TrimSpace(string(payload)))
}

func iamError(payload []byte) (code, message string) {
	var parsed struct {
		Error struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		} `xml:"Error"`
	}
	if err := xml.Unmarshal(payload, &parsed); err != nil {
		return "", ""
	}
	return parsed.Error.Code, parsed.Error.Message
}

func isIAMAlreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "EntityAlreadyExists")
}

func isIAMNoSuchEntity(err error) bool {
	return err != nil && strings.Contains(err.Error(), "NoSuchEntity")
}

func iamUserName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	b.WriteString("geass-")
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func (c *AWSClient) ensurePathStyleBucket(bucket string) error {
	endpoint := strings.TrimRight(c.Endpoint, "/")
	req, err := http.NewRequest(http.MethodPut, endpoint+"/"+bucket, http.NoBody)
	if err != nil {
		return err
	}
	if err := c.sign(req, nil, "s3"); err != nil {
		return err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 300 || resp.StatusCode == http.StatusConflict {
		return nil
	}
	return fmt.Errorf("object store %s: %s", resp.Status, strings.TrimSpace(string(payload)))
}

func (c *AWSClient) sign(req *http.Request, payload []byte, service string) error {
	return c.signWithRegion(req, payload, service, c.region())
}

func (c *AWSClient) signWithRegion(req *http.Request, payload []byte, service, region string) error {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256Hex(payload)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("Host", req.URL.Host)

	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n", strings.ToLower(req.URL.Host), payloadHash, amzDate)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := strings.Join([]string{
		req.Method,
		path,
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signingKey := awsSigningKey(c.SecretKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		c.AccessKey, credentialScope, signedHeaders, signature,
	))
	return nil
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func awsSigningKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}
