package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const apiBase = "https://api.cloudflare.com/client/v4"

type Client struct {
	AccountID string
	APIToken  string
	HTTP      *http.Client
}

type IngressRule struct {
	Hostname string
	Service  string
	Path     string
}

type Tunnel struct {
	ID   string
	Name string
}

type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Zone struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Account *AccountRef `json:"account,omitempty"`
}

// AccountRef is the owning account on a zone list response.
type AccountRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AccountIDFromZone returns the account ID Cloudflare attaches to a zone.
func AccountIDFromZone(zone Zone) string {
	if zone.Account == nil {
		return ""
	}
	return strings.TrimSpace(zone.Account.ID)
}

type apiResponse struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) request(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var parsed apiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("cloudflare API returned invalid JSON (HTTP %d)", resp.StatusCode)
	}
	if !parsed.Success {
		if len(parsed.Errors) > 0 {
			return nil, fmt.Errorf("cloudflare API: %s", parsed.Errors[0].Message)
		}
		return nil, fmt.Errorf("cloudflare API request failed")
	}
	return parsed.Result, nil
}

// NormalizeAPIToken trims whitespace and an accidental "Bearer " prefix from pasted values.
func NormalizeAPIToken(raw string) string {
	raw = strings.TrimSpace(raw)
	for strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		raw = strings.TrimSpace(raw[7:])
	}
	return raw
}

// Verify checks that the API token is valid using the user token verify endpoint.
func (c *Client) Verify(ctx context.Context) error {
	_, err := c.request(ctx, http.MethodGet, "/user/tokens/verify", nil)
	return err
}

// Validate checks that the token can reach Cloudflare APIs Geass uses. Account-owned
// tokens may fail /user/tokens/verify while zone or account calls still work.
func (c *Client) Validate(ctx context.Context) error {
	c.APIToken = NormalizeAPIToken(c.APIToken)
	if c.APIToken == "" {
		return fmt.Errorf("API token is empty")
	}
	verifyErr := c.Verify(ctx)
	if verifyErr == nil {
		return nil
	}
	probes := []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := c.request(ctx, http.MethodGet, "/zones?per_page=1", nil)
			return err
		},
		func(ctx context.Context) error {
			_, err := c.ListAccounts(ctx)
			return err
		},
	}
	for _, probe := range probes {
		if err := probe(ctx); err == nil {
			return nil
		}
	}
	return verifyErr
}

// TokenValidationMessage turns a validation error into dashboard-safe user text.
func TokenValidationMessage(err error) string {
	if err == nil {
		return "Cloudflare API token could not be verified"
	}
	msg := strings.TrimSpace(err.Error())
	switch {
	case msg == "API token is empty":
		return "Paste a Cloudflare API token (not the Global API Key or Origin CA key)"
	case strings.HasPrefix(msg, "cloudflare API:"):
		return strings.TrimPrefix(msg, "cloudflare API:")
	case strings.Contains(msg, "invalid JSON (HTTP 401)") || strings.Contains(msg, "invalid JSON (HTTP 403)"):
		return "Cloudflare rejected the API token — update it under Connectors"
	case strings.Contains(msg, "invalid JSON (HTTP 429)"):
		return "Cloudflare rate limited this request — try again in a minute"
	case strings.Contains(msg, "invalid JSON"):
		return "Could not reach Cloudflare API (non-JSON response) — check network egress to api.cloudflare.com"
	default:
		if msg != "" {
			return "Cloudflare API token could not be verified: " + msg
		}
		return "Cloudflare API token could not be verified"
	}
}

// ListAccounts returns Cloudflare accounts this token can access.
func (c *Client) ListAccounts(ctx context.Context) ([]Account, error) {
	result, err := c.request(ctx, http.MethodGet, "/accounts", nil)
	if err != nil {
		return nil, err
	}
	var accounts []Account
	if err := json.Unmarshal(result, &accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

// GetZone returns a single DNS zone by ID.
func (c *Client) GetZone(ctx context.Context, zoneID string) (Zone, error) {
	zoneID = strings.TrimSpace(zoneID)
	if zoneID == "" {
		return Zone{}, fmt.Errorf("zone ID is required")
	}
	result, err := c.request(ctx, http.MethodGet, "/zones/"+zoneID, nil)
	if err != nil {
		return Zone{}, err
	}
	var zone Zone
	if err := json.Unmarshal(result, &zone); err != nil {
		return Zone{}, err
	}
	return zone, nil
}

// ListZones returns DNS zones this token can access.
func (c *Client) ListZones(ctx context.Context) ([]Zone, error) {
	result, err := c.request(ctx, http.MethodGet, "/zones?per_page=50", nil)
	if err != nil {
		return nil, err
	}
	var zones []Zone
	if err := json.Unmarshal(result, &zones); err != nil {
		return nil, err
	}
	return zones, nil
}

// FindOrCreateTunnel returns a remotely-managed tunnel by name, creating it when missing.
func (c *Client) FindOrCreateTunnel(ctx context.Context, name string) (Tunnel, error) {
	result, err := c.request(ctx, http.MethodGet, "/accounts/"+c.AccountID+"/cfd_tunnel?name="+name, nil)
	if err != nil {
		return Tunnel{}, err
	}
	var tunnels []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(result, &tunnels); err == nil && len(tunnels) > 0 {
		return Tunnel{ID: tunnels[0].ID, Name: tunnels[0].Name}, nil
	}
	created, err := c.request(ctx, http.MethodPost, "/accounts/"+c.AccountID+"/cfd_tunnel", map[string]string{
		"name":       name,
		"config_src": "cloudflare",
	})
	if err != nil {
		return Tunnel{}, err
	}
	var tunnel struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(created, &tunnel); err != nil {
		return Tunnel{}, err
	}
	return Tunnel{ID: tunnel.ID, Name: tunnel.Name}, nil
}

// TunnelToken returns the connector token for cloudflared.
func (c *Client) TunnelToken(ctx context.Context, tunnelID string) (string, error) {
	result, err := c.request(ctx, http.MethodGet, "/accounts/"+c.AccountID+"/cfd_tunnel/"+tunnelID+"/token", nil)
	if err != nil {
		return "", err
	}
	var token string
	if err := json.Unmarshal(result, &token); err != nil {
		return "", err
	}
	return strings.TrimSpace(token), nil
}

// PutIngressConfiguration publishes public hostnames for the tunnel.
func (c *Client) PutIngressConfiguration(ctx context.Context, tunnelID string, rules []IngressRule) error {
	ingress := make([]map[string]string, 0, len(rules)+1)
	for _, rule := range rules {
		entry := map[string]string{"service": rule.Service}
		if rule.Hostname != "" {
			entry["hostname"] = rule.Hostname
		}
		if rule.Path != "" {
			entry["path"] = rule.Path
		}
		ingress = append(ingress, entry)
	}
	ingress = append(ingress, map[string]string{"service": "http_status:404"})
	body := map[string]any{
		"config": map[string]any{"ingress": ingress},
	}
	_, err := c.request(ctx, http.MethodPut, "/accounts/"+c.AccountID+"/cfd_tunnel/"+tunnelID+"/configurations", body)
	return err
}

func ensureDNSRecord(ctx context.Context, c *Client, zoneID, recordType, recordName, content string, proxied bool) error {
	recordName = strings.TrimSuffix(strings.TrimSpace(recordName), ".")
	content = strings.TrimSpace(content)
	if zoneID == "" || recordName == "" || content == "" {
		return fmt.Errorf("zone ID, record name, and content are required")
	}
	listPath := fmt.Sprintf("/zones/%s/dns_records?type=%s&name=%s", zoneID, recordType, recordName)
	result, err := c.request(ctx, http.MethodGet, listPath, nil)
	if err != nil {
		return err
	}
	var records []struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(result, &records)
	payload := map[string]any{
		"type":    recordType,
		"name":    recordName,
		"content": content,
		"proxied": proxied,
	}
	if len(records) > 0 {
		_, err = c.request(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+records[0].ID, payload)
		return err
	}
	_, err = c.request(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", payload)
	return err
}

// EnsureCNAMERecord creates or updates a proxied CNAME to the tunnel hostname.
func (c *Client) EnsureCNAMERecord(ctx context.Context, zoneID, recordName, tunnelCNAMETarget string) error {
	tunnelCNAMETarget = strings.TrimSuffix(strings.TrimSpace(tunnelCNAMETarget), ".")
	return ensureDNSRecord(ctx, c, zoneID, "CNAME", recordName, tunnelCNAMETarget, true)
}

// EnsureARecord creates or updates an A record (optionally proxied through Cloudflare).
func (c *Client) EnsureARecord(ctx context.Context, zoneID, recordName, ipv4 string, proxied bool) error {
	return ensureDNSRecord(ctx, c, zoneID, "A", recordName, ipv4, proxied)
}

func TunnelCNAMETarget(tunnelID string) string {
	tunnelID = strings.TrimSpace(tunnelID)
	if tunnelID == "" {
		return ""
	}
	return tunnelID + ".cfargotunnel.com"
}
