package cloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GarageClient makes the buckets and keys of a Garage server through its admin API, and empties buckets
// through its S3 API. Garage has no IAM and no policies: a key is allowed to read, write or own each bucket
// it is granted, and a bucket that a key was not granted does not exist for it.
type GarageClient struct {
	HTTP *http.Client
	// AdminEndpoint is the admin API's address, and AdminToken the bearer token it requires.
	AdminEndpoint string
	AdminToken    string
	// S3 signs with the server's own key, which Geass makes the owner of every bucket it creates, so that it
	// can empty a bucket before the admin API will delete it.
	S3 *AWSClient
}

// garageError is an error the admin API answered with.
type garageError struct {
	Status  int
	Code    string
	Message string
}

func (e *garageError) Error() string {
	return fmt.Sprintf("Garage %d %s: %s", e.Status, e.Code, e.Message)
}

func isGarageCode(err error, code string) bool {
	var ge *garageError
	return errors.As(err, &ge) && ge.Code == code
}

// garageBucket is a bucket with the keys that may use it, as the admin API reports it.
type garageBucket struct {
	ID   string `json:"id"`
	Keys []struct {
		AccessKeyID string            `json:"accessKeyId"`
		Permissions garagePermissions `json:"permissions"`
	} `json:"keys"`
}

// garageKey is a key with the buckets it may use, as the admin API reports it.
type garageKey struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	Name            string `json:"name"`
	Buckets         []struct {
		ID          string            `json:"id"`
		Permissions garagePermissions `json:"permissions"`
	} `json:"buckets"`
}

type garagePermissions struct {
	Read  bool `json:"read"`
	Write bool `json:"write"`
	Owner bool `json:"owner"`
}

// covers reports whether these permissions include every one of want.
func (p garagePermissions) covers(want garagePermissions) bool {
	return (!want.Read || p.Read) && (!want.Write || p.Write) && (!want.Owner || p.Owner)
}

func (c *GarageClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// call sends one admin API request. A body makes it a POST; out, when set, receives the JSON answer.
func (c *GarageClient) call(method, path string, query url.Values, body, out any) error {
	var reader io.Reader = http.NoBody
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	target := strings.TrimRight(c.AdminEndpoint, "/") + "/v2/" + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.AdminToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := readHTTPBody(resp.Body, s3ListResponseLimit)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		failure := &garageError{Status: resp.StatusCode}
		if json.Unmarshal(payload, failure) != nil || failure.Code == "" {
			failure.Message = strings.TrimSpace(string(payload))
		}
		return failure
	}
	if out == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	return json.Unmarshal(payload, out)
}

// bucketInfo returns the bucket with this name and who may use it, or nil when there is none.
func (c *GarageClient) bucketInfo(bucket string) (*garageBucket, error) {
	var info garageBucket
	err := c.call(http.MethodGet, "GetBucketInfo", url.Values{"globalAlias": {bucket}}, nil, &info)
	if isGarageCode(err, "NoSuchBucket") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &info, nil
}

func (c *GarageClient) changeGrant(operation, bucketID, accessKeyID string, permissions garagePermissions) error {
	return c.call(http.MethodPost, operation, nil, map[string]any{
		"bucketId": bucketID, "accessKeyId": accessKeyID, "permissions": permissions,
	}, nil)
}

// grant gives the key these permissions on the bucket, unless it already has them: a bucket that is already as it
// should be is not written to on every pass.
func (c *GarageClient) grant(bucket *garageBucket, accessKeyID string, permissions garagePermissions) error {
	for _, key := range bucket.Keys {
		if key.AccessKeyID == accessKeyID && key.Permissions.covers(permissions) {
			return nil
		}
	}
	return c.changeGrant("AllowBucketKey", bucket.ID, accessKeyID, permissions)
}

// EnsureBucket creates the bucket when it does not exist, and makes the server's own key its owner.
func (c *GarageClient) EnsureBucket(bucket string) error {
	info, err := c.bucketInfo(bucket)
	if err != nil {
		return err
	}
	if info == nil {
		var created garageBucket
		err := c.call(http.MethodPost, "CreateBucket", nil, map[string]any{"globalAlias": bucket}, &created)
		switch {
		case isGarageCode(err, "BucketAlreadyExists"):
			// Another pass made it first.
			if info, err = c.bucketInfo(bucket); err != nil || info == nil {
				return fmt.Errorf("bucket %s was not found after it was created: %w", bucket, err)
			}
		case err != nil:
			return err
		default:
			info = &created
		}
	}
	return c.grant(info, c.S3.AccessKey, garagePermissions{Read: true, Write: true, Owner: true})
}

// findKey returns the key with exactly this name. Names are not unique in Garage, so Geass keeps its own
// unique: the first match wins.
func (c *GarageClient) findKey(name string) (*garageKey, error) {
	var keys []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.call(http.MethodGet, "ListKeys", nil, nil, &keys); err != nil {
		return nil, err
	}
	for _, key := range keys {
		if key.Name == name {
			return c.keyInfo(key.ID)
		}
	}
	return nil, nil
}

// keyInfo reads a key with its secret, which Garage shows to the admin on request. It returns nil for a key
// that does not exist.
func (c *GarageClient) keyInfo(id string) (*garageKey, error) {
	var key garageKey
	err := c.call(http.MethodGet, "GetKeyInfo", url.Values{"id": {id}, "showSecretKey": {"true"}}, nil, &key)
	if isGarageCode(err, "NoSuchAccessKey") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &key, nil
}

// EnsureBucketUser makes a key for the user and lets it read and write exactly these buckets, returning the
// key's id and secret. A key that Geass stored earlier is kept while it still exists on the server, and one
// that has the user's name is adopted, secret and all, so a lost Secret never leaves a key nobody can use.
func (c *GarageClient) EnsureBucketUser(buckets []string, userName, existingAccess, existingSecret string) (string, string, error) {
	if len(buckets) == 0 || strings.TrimSpace(buckets[0]) == "" {
		return "", "", fmt.Errorf("bucket is required")
	}
	name := iamUserName(userName)
	var key *garageKey
	var err error
	if strings.TrimSpace(existingAccess) != "" && strings.TrimSpace(existingSecret) != "" {
		if key, err = c.keyInfo(existingAccess); err != nil {
			return "", "", err
		}
	}
	if key == nil {
		if key, err = c.findKey(name); err != nil {
			return "", "", err
		}
	}
	if key == nil {
		key = &garageKey{}
		if err := c.call(http.MethodPost, "CreateKey", nil, map[string]any{"name": name}, key); err != nil {
			return "", "", err
		}
	}
	if key.AccessKeyID == "" || key.SecretAccessKey == "" {
		return "", "", fmt.Errorf("the server did not return a key")
	}
	listed := make(map[string]bool, len(buckets))
	for _, bucket := range buckets {
		info, err := c.bucketInfo(bucket)
		if err != nil {
			return "", "", err
		}
		if info == nil {
			return "", "", fmt.Errorf("bucket %s does not exist", bucket)
		}
		listed[info.ID] = true
		if err := c.grant(info, key.AccessKeyID, garagePermissions{Read: true, Write: true}); err != nil {
			return "", "", err
		}
	}
	// A bucket the user no longer lists is no longer theirs. On AWS and SeaweedFS the user's policy is replaced, which
	// takes it away; a Garage key keeps every grant it was given until it is told otherwise.
	for _, held := range key.Buckets {
		if listed[held.ID] || (!held.Permissions.Read && !held.Permissions.Write) {
			continue
		}
		if err := c.changeGrant("DenyBucketKey", held.ID, key.AccessKeyID, garagePermissions{Read: true, Write: true}); err != nil {
			return "", "", err
		}
	}
	return key.AccessKeyID, key.SecretAccessKey, nil
}

// DeleteBucketUser removes the user's key. A key that is already gone counts as removed.
func (c *GarageClient) DeleteBucketUser(userName string) error {
	key, err := c.findKey(iamUserName(userName))
	if err != nil || key == nil {
		return err
	}
	err = c.call(http.MethodPost, "DeleteKey", url.Values{"id": {key.AccessKeyID}}, nil, nil)
	if isGarageCode(err, "NoSuchAccessKey") {
		return nil
	}
	return err
}

// DeleteBucket removes the bucket after deleting its objects. A bucket that is already gone counts as removed.
func (c *GarageClient) DeleteBucket(bucket string) error {
	info, err := c.bucketInfo(bucket)
	if err != nil || info == nil {
		return err
	}
	// The server's key owns the bucket from the moment Geass makes it, but a bucket from before that, or one
	// whose grant was lost, is taken back here so that it can be emptied.
	if err := c.grant(info, c.S3.AccessKey, garagePermissions{Read: true, Write: true, Owner: true}); err != nil {
		return err
	}
	if err := c.S3.emptyBucket(bucket); err != nil && !isS3NoSuchBucket(err) {
		return err
	}
	err = c.call(http.MethodPost, "DeleteBucket", url.Values{"id": {info.ID}}, nil, nil)
	if isGarageCode(err, "NoSuchBucket") {
		return nil
	}
	return err
}
