package cloud

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AWSClient performs a small set of signed S3 operations without an SDK.
type AWSClient struct {
	HTTP      *http.Client
	AccessKey string
	SecretKey string
	Region    string
	Endpoint  string
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
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256Hex(payload)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("Host", req.URL.Host)

	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n", strings.ToLower(req.URL.Host), payloadHash, amzDate)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		"",
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	region := c.region()
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
