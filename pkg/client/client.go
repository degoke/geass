// Package client is a Go client for the Geass v1 API. The command line and the MCP server are both built on it,
// so what a person can do from a terminal and what an agent can do through a tool are the same operations.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a Geass dashboard.
type Client struct {
	// BaseURL is the dashboard's address, such as https://geass.example.com.
	BaseURL string
	// Token is an API token. Leave it empty for the calls that need none (signing in).
	Token string
	// HTTP is the client used for requests; nil uses one with a timeout that suits calls, not streams.
	HTTP *http.Client
	// UserAgent is sent with every request.
	UserAgent string
}

// New returns a client for the dashboard at baseURL.
func New(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, UserAgent: "geass-client"}
}

// Error is an error the API answered with.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("the API answered %d", e.Status)
	}
	return e.Message
}

// IsNotFound reports whether the API said the thing does not exist (or is not the caller's to see).
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsUnauthorized reports whether the token was missing, wrong or expired.
func IsUnauthorized(err error) bool { return hasStatus(err, http.StatusUnauthorized) }

// IsForbidden reports whether the token may not do this.
func IsForbidden(err error) bool { return hasStatus(err, http.StatusForbidden) }

func hasStatus(err error, status int) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.Status == status
}

// ErrorCode is the machine-readable code of an API error, or "" for any other error.
func ErrorCode(err error) string {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (c *Client) newRequest(ctx context.Context, method, path string, query url.Values, body any) (*http.Request, error) {
	target := c.BaseURL + "/api/v1" + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	return req, nil
}

// Do sends a request and decodes the JSON answer into out (nil to discard it). A non-success answer is
// returned as an *Error.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	req, err := c.newRequest(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return decodeError(resp.StatusCode, raw)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("the API answered something that is not JSON: %w", err)
	}
	return nil
}

func decodeError(status int, raw []byte) error {
	var payload struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Error == "" {
		payload.Error = strings.TrimSpace(string(raw))
	}
	return &Error{Status: status, Code: payload.Code, Message: payload.Error}
}

// Stream follows a server-sent event stream, calling handle with the data of each event until the stream ends,
// handle returns an error, or the context is cancelled. Comments and keep-alives are skipped.
func (c *Client) Stream(ctx context.Context, path string, query url.Values, handle func(data []byte) error) error {
	req, err := c.newRequest(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	// A stream has no timeout of its own: the context ends it.
	streaming := &http.Client{}
	if c.HTTP != nil {
		copied := *c.HTTP
		copied.Timeout = 0
		streaming = &copied
	}
	resp, err := streaming.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return decodeError(resp.StatusCode, raw)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		if err := handle([]byte(strings.TrimPrefix(data, " "))); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return ctx.Err()
}
