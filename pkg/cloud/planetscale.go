package cloud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// PlanetScaleClient talks to the PlanetScale HTTP API using a service token.
type PlanetScaleClient struct {
	HTTP  *http.Client
	Token string
	Org   string
	Base  string
}

type planetScaleDatabase struct {
	Name string `json:"name"`
}

type planetScalePassword struct {
	Name     string `json:"name"`
	Username string `json:"username"`
	Plain    string `json:"plain_text"`
	Host     string `json:"access_host_url"`
}

func (c *PlanetScaleClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *PlanetScaleClient) baseURL() string {
	if strings.TrimSpace(c.Base) != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return "https://api.planetscale.com/v1"
}

func planetScaleAuthorization(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return token
	}
	if len(token) >= 7 && strings.EqualFold(token[:7], "Bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	return "Bearer " + token
}

func (c *PlanetScaleClient) do(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.baseURL()+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", planetScaleAuthorization(c.Token))
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := readHTTPBody(resp.Body, s3ListResponseLimit)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("PlanetScale API %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	return json.Unmarshal(payload, out)
}

// EnsureDatabase creates the database when it does not already exist.
func (c *PlanetScaleClient) EnsureDatabase(name string) error {
	var existing planetScaleDatabase
	err := c.do(http.MethodGet, fmt.Sprintf("/organizations/%s/databases/%s", c.Org, name), nil, &existing)
	if err == nil && existing.Name != "" {
		return nil
	}
	return c.do(http.MethodPost, fmt.Sprintf("/organizations/%s/databases", c.Org), map[string]string{"name": name}, &existing)
}

// CreatePassword issues a connection password for the default production branch.
func (c *PlanetScaleClient) CreatePassword(database, name string) (planetScalePassword, error) {
	var password planetScalePassword
	err := c.do(http.MethodPost, fmt.Sprintf("/organizations/%s/databases/%s/branches/main/passwords", c.Org, database), map[string]string{"name": name}, &password)
	return password, err
}
