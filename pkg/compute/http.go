package compute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxResponseBytes bounds what is read from a provider, so that a misbehaving endpoint cannot fill the operator's
// memory.
const maxResponseBytes = 8 << 20

// Client is the small JSON-over-HTTP client the providers share: it signs or authorises each request, waits and
// retries when the provider says to slow down, and turns failures into the errors of this package.
type Client struct {
	HTTP    *http.Client
	BaseURL string
	// Authorize adds credentials to a request after its body is set, so it can sign the body.
	Authorize func(req *http.Request, body []byte) error
	// Header is added to every request.
	Header http.Header
	// ClassifyError turns an error response into an error. Returning nil leaves it to the default.
	ClassifyError func(method, path string, status int, body []byte) error
	// Sleep waits between attempts. Nil means time.Sleep honouring the context; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
	// MaxAttempts is how many times a request that was rate limited is sent. Zero means 4.
	MaxAttempts int
}

// APIError is an error response of a provider that is none of the more specific errors.
type APIError struct {
	Method string
	Path   string
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, strings.TrimSpace(e.Body))
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Do sends one request. body, when it is not nil, is sent as JSON; a successful response is decoded into out when
// out is not nil. It returns the status code of the response.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, out any) (int, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("encode request: %w", err)
		}
	}
	return c.DoRaw(ctx, method, path, query, payload, "application/json", out)
}

// DoRaw is Do for a body that is already encoded, such as a form. When out is a *[]byte the response body is
// stored in it undecoded.
func (c *Client) DoRaw(ctx context.Context, method, path string, query url.Values, payload []byte, contentType string, out any) (int, error) {
	attempts := c.MaxAttempts
	if attempts <= 0 {
		attempts = 4
	}
	target := strings.TrimRight(c.BaseURL, "/") + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var lastStatus int
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
		if err != nil {
			return 0, err
		}
		for key, values := range c.Header {
			for _, v := range values {
				req.Header.Add(key, v)
			}
		}
		if req.Header.Get("Accept") == "" {
			req.Header.Set("Accept", "application/json")
		}
		if payload != nil && contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if c.Authorize != nil {
			if err := c.Authorize(req, payload); err != nil {
				return 0, err
			}
		}
		resp, err := c.httpClient().Do(req)
		if err != nil {
			// A connection that never got as far as sending the request is safe to try again, whatever the request
			// is, because the provider cannot have acted on it. Cloud APIs drop a connection now and then.
			if attempt < attempts && ctx.Err() == nil && notSent(err) {
				if sleepErr := c.sleep(ctx, retryAfter(nil, attempt)); sleepErr != nil {
					return 0, sleepErr
				}
				continue
			}
			return 0, err
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
		if readErr != nil {
			return resp.StatusCode, readErr
		}
		lastStatus = resp.StatusCode

		if resp.StatusCode == http.StatusTooManyRequests && attempt < attempts && !c.isQuota(method, path, resp.StatusCode, raw) {
			wait := retryAfter(resp.Header, attempt)
			if err := c.sleep(ctx, wait); err != nil {
				return lastStatus, err
			}
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if bytesOut, ok := out.(*[]byte); ok {
				// A provider that answers in XML or plain text gets the body as it came.
				*bytesOut = raw
				return resp.StatusCode, nil
			}
			if out != nil && len(bytes.TrimSpace(raw)) > 0 {
				if err := json.Unmarshal(raw, out); err != nil {
					return resp.StatusCode, fmt.Errorf("%s %s: unreadable response: %w", method, path, err)
				}
			}
			return resp.StatusCode, nil
		}
		if c.ClassifyError != nil {
			if classified := c.ClassifyError(method, path, resp.StatusCode, raw); classified != nil {
				return resp.StatusCode, classified
			}
		}
		return resp.StatusCode, DefaultError(method, path, resp.StatusCode, resp.Header, raw)
	}
}

// notSent reports whether a transport error happened before any of the request reached the provider: the connection
// could not be made, or the TLS handshake timed out. An error after the request was written says nothing about
// whether the provider acted on it, so it is never retried here.
func notSent(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	return strings.Contains(err.Error(), "TLS handshake timeout")
}

// isQuota reports whether a 429 is really a limit that waiting will not lift: some providers (Google) answer an
// exhausted quota with the status that means "slow down". Retrying it only delays the news.
func (c *Client) isQuota(method, path string, status int, body []byte) bool {
	if c.ClassifyError == nil {
		return false
	}
	classified := c.ClassifyError(method, path, status, body)
	var rate *RateLimitError
	return classified != nil && !errors.As(classified, &rate)
}

// DefaultError maps the statuses every provider uses the same way.
func DefaultError(method, path string, status int, header http.Header, body []byte) error {
	msg := summarize(body)
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", ErrAuth, msg)
	case http.StatusForbidden:
		return fmt.Errorf("%w (forbidden): %s", ErrAuth, msg)
	case http.StatusNotFound:
		if method == http.MethodGet || method == http.MethodDelete {
			return fmt.Errorf("%w: %s %s", ErrNotFound, method, path)
		}
	case http.StatusTooManyRequests:
		return &RateLimitError{RetryAfter: retryAfter(header, 1), Message: msg}
	}
	return &APIError{Method: method, Path: path, Status: status, Body: msg}
}

// retryAfter reads the Retry-After header (seconds), and otherwise backs off exponentially. It never waits more
// than a minute: a request that needs longer is returned to the caller, which retries on its own schedule.
func retryAfter(header http.Header, attempt int) time.Duration {
	if v := strings.TrimSpace(header.Get("Retry-After")); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil && seconds >= 0 {
			return min(time.Duration(seconds)*time.Second, time.Minute)
		}
	}
	return min(time.Duration(1<<(attempt-1))*time.Second, 30*time.Second)
}

// summarize shortens a response body for an error message.
func summarize(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 400 {
		text = text[:400] + "..."
	}
	return text
}

// IsStatus reports whether err is an API error with the status.
func IsStatus(err error, status int) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == status
}
