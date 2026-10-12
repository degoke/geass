package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// invokeTimeout covers a cold start and the function's own work.
const invokeTimeout = 2 * time.Minute

// FunctionTrigger is a schedule that calls a function.
type FunctionTrigger struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Path     string `json:"path,omitempty"`
	Payload  string `json:"payload,omitempty"`
}

// Function is how a function is run and when it is called on its own.
type Function struct {
	Runtime  string            `json:"runtime"`
	Mode     string            `json:"mode"`
	Triggers []FunctionTrigger `json:"triggers"`
	// URL is where it answers, empty until it has a public address.
	URL string `json:"url"`
}

// Function reads a function service's mode and triggers.
func (c *Client) Function(ctx context.Context, app string) (Function, error) {
	detail, err := c.Get(ctx, "apps", app)
	if err != nil {
		return Function{}, err
	}
	source, _ := detail.Spec["source"].(map[string]any)
	raw, ok := source["function"]
	if !ok {
		return Function{}, fmt.Errorf("%s is not a function", app)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return Function{}, err
	}
	fn := Function{URL: detail.Resource.URL}
	if err := json.Unmarshal(encoded, &fn); err != nil {
		return Function{}, err
	}
	if fn.Mode == "" {
		fn.Mode = "http"
	}
	return fn, nil
}

// SetFunctionTriggers replaces a function's schedules. They apply when it is next deployed.
func (c *Client) SetFunctionTriggers(ctx context.Context, app string, triggers []FunctionTrigger) error {
	// An empty list is sent as an empty string: a list with nothing in it carries no field to change.
	var value any = ""
	if len(triggers) > 0 {
		value = triggers
	}
	_, err := c.UpdateApp(ctx, app, map[string]any{"functionTriggers": value})
	return err
}

// AddFunctionTrigger adds a schedule to a function, or replaces the one with the same name.
func (c *Client) AddFunctionTrigger(ctx context.Context, app string, trigger FunctionTrigger) error {
	fn, err := c.Function(ctx, app)
	if err != nil {
		return err
	}
	triggers := slices.DeleteFunc(fn.Triggers, func(t FunctionTrigger) bool { return t.Name == trigger.Name })
	return c.SetFunctionTriggers(ctx, app, append(triggers, trigger))
}

// RemoveFunctionTrigger removes a schedule, and says whether there was one by that name.
func (c *Client) RemoveFunctionTrigger(ctx context.Context, app, name string) (bool, error) {
	fn, err := c.Function(ctx, app)
	if err != nil {
		return false, err
	}
	kept := slices.DeleteFunc(slices.Clone(fn.Triggers), func(t FunctionTrigger) bool { return t.Name == name })
	if len(kept) == len(fn.Triggers) {
		return false, nil
	}
	return true, c.SetFunctionTriggers(ctx, app, kept)
}

// InvokeResult is a function's answer.
type InvokeResult struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// Invoke calls a function at its public address, as any visitor would. A function that is asleep is woken by
// the call, so the first one can take a few seconds.
func (c *Client) Invoke(ctx context.Context, fn Function, method, path, body string) (InvokeResult, error) {
	if fn.URL == "" {
		return InvokeResult{}, fmt.Errorf("the function has no public address to call: give it a domain first")
	}
	target, err := url.Parse(fn.URL)
	if err != nil {
		return InvokeResult{}, err
	}
	// The path is joined to the function's own address, which may itself have a path.
	path, query, _ := strings.Cut(path, "?")
	target.Path = strings.TrimRight(target.Path, "/") + "/" + strings.TrimPrefix(path, "/")
	target.RawQuery = query
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return InvokeResult{}, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := (&http.Client{Timeout: invokeTimeout}).Do(req)
	if err != nil {
		return InvokeResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return InvokeResult{Status: resp.StatusCode, Body: string(raw)}, err
}
