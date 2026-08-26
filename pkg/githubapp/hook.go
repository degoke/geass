package githubapp

import (
	"encoding/json"
	"fmt"
)

type HookConfig struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Active      bool   `json:"active"`
}

// GetAppHookConfig returns the GitHub App webhook configuration.
func (c *Client) GetAppHookConfig() (*HookConfig, error) {
	appJWT, err := c.appJWT()
	if err != nil {
		return nil, err
	}
	data, err := c.apiRequest(appJWT, "/app/hook/config")
	if err != nil {
		return nil, err
	}
	var hook HookConfig
	if err := json.Unmarshal(data, &hook); err != nil {
		return nil, err
	}
	if hook.URL == "" {
		return nil, fmt.Errorf("GitHub App webhook URL is not configured")
	}
	return &hook, nil
}
