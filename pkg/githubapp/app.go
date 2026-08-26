package githubapp

import (
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Config holds GitHub App credentials for the installation OAuth flow.
type Config struct {
	AppID         int64
	ClientID      string
	ClientSecret  string
	Slug          string
	PrivateKeyPEM string
	WebhookSecret string
	PublicBaseURL string
}

func (c Config) Configured() bool {
	return c.Validate() == nil
}

// Validate checks the complete credential set needed for GitHub App
// installation, API authentication, and webhook verification.
func (c Config) Validate() error {
	if c.AppID <= 0 || strings.TrimSpace(c.ClientID) == "" || strings.TrimSpace(c.ClientSecret) == "" || strings.TrimSpace(c.Slug) == "" {
		return fmt.Errorf("GitHub App credentials are incomplete")
	}
	if strings.TrimSpace(c.WebhookSecret) == "" {
		return fmt.Errorf("GitHub webhook secret is not configured")
	}
	base, err := url.Parse(strings.TrimSpace(c.PublicBaseURL))
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return fmt.Errorf("GitHub App public URL must use HTTPS")
	}
	if _, err := parsePrivateKey(c.PrivateKeyPEM); err != nil {
		return err
	}
	return nil
}

func (c Config) InstallURL(state string) string {
	return fmt.Sprintf("https://github.com/apps/%s/installations/new?state=%s", url.PathEscape(c.Slug), url.QueryEscape(state))
}

func (c Config) CallbackURL() string {
	return strings.TrimRight(c.PublicBaseURL, "/") + "/github/callback"
}

func (c Config) InstallationSettingsURL(installationID string) string {
	if installationID == "" {
		return "https://github.com/settings/installations"
	}
	return "https://github.com/settings/installations/" + url.PathEscape(installationID)
}

type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
}

type Repository struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	HTMLURL       string `json:"html_url"`
}

type Client struct {
	Config Config
	HTTP   *http.Client
}

func NewClient(cfg Config) *Client {
	client := &http.Client{Timeout: 15 * time.Second}
	return &Client{Config: cfg, HTTP: client}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *Client) SignState(project, environment string) (string, error) {
	if c.Config.ClientSecret == "" {
		return "", fmt.Errorf("GitHub App client secret is not configured")
	}
	payload, err := json.Marshal(struct {
		Project     string `json:"project"`
		Environment string `json:"environment"`
		ExpiresAt   int64  `json:"expiresAt"`
	}{
		Project:     project,
		Environment: environment,
		ExpiresAt:   time.Now().Add(15 * time.Minute).Unix(),
	})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(c.Config.ClientSecret))
	_, _ = mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + signature, nil
}

func (c *Client) VerifyState(state string) (project, environment string, err error) {
	parts := strings.Split(state, ".")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid OAuth state")
	}
	mac := hmac.New(sha256.New, []byte(c.Config.ClientSecret))
	_, _ = mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return "", "", fmt.Errorf("OAuth state signature mismatch")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", err
	}
	var payload struct {
		Project     string `json:"project"`
		Environment string `json:"environment"`
		ExpiresAt   int64  `json:"expiresAt"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", "", err
	}
	if payload.Project == "" || time.Now().Unix() > payload.ExpiresAt {
		return "", "", fmt.Errorf("OAuth state expired or invalid")
	}
	return payload.Project, payload.Environment, nil
}

func (c *Client) appJWT() (string, error) {
	key, err := parsePrivateKey(c.Config.PrivateKeyPEM)
	if err != nil {
		return "", err
	}
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(c.Config.AppID, 10),
	})
	return token.SignedString(key)
}

func parsePrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("GitHub App private key is not valid PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("GitHub App private key is not RSA")
	}
	return rsaKey, nil
}

func (c *Client) InstallationToken(installationID int64) (string, error) {
	appJWT, err := c.appJWT()
	if err != nil {
		return "", err
	}
	request, err := http.NewRequest(http.MethodPost, fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", installationID), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+appJWT)
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if response.StatusCode >= 300 {
		return "", fmt.Errorf("GitHub API returned %s", response.Status)
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	if result.Token == "" {
		return "", fmt.Errorf("GitHub did not return an installation token")
	}
	return result.Token, nil
}

func (c *Client) GetInstallation(installationID int64) (*Installation, error) {
	appJWT, err := c.appJWT()
	if err != nil {
		return nil, err
	}
	data, err := c.apiRequest(appJWT, fmt.Sprintf("/app/installations/%d", installationID))
	if err != nil {
		return nil, err
	}
	var installation Installation
	if err := json.Unmarshal(data, &installation); err != nil {
		return nil, err
	}
	if installation.ID == 0 {
		return nil, fmt.Errorf("GitHub installation was not found")
	}
	return &installation, nil
}

func (c *Client) ListInstallationRepositories(token string) ([]Repository, error) {
	var repositories []Repository
	page := 1
	for page <= 5 {
		data, err := c.apiRequest(token, fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page))
		if err != nil {
			return nil, err
		}
		var result struct {
			Repositories []Repository `json:"repositories"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		if len(result.Repositories) == 0 {
			break
		}
		repositories = append(repositories, result.Repositories...)
		if len(result.Repositories) < 100 {
			break
		}
		page++
	}
	return repositories, nil
}

func (c *Client) apiRequest(token, path string) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, "https://api.github.com"+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub API returned %s", response.Status)
	}
	return data, nil
}

func LoadConfigFromEnv() Config {
	appID, _ := strconv.ParseInt(strings.TrimSpace(os.Getenv("GEASS_GITHUB_APP_ID")), 10, 64)
	return Config{
		AppID:         appID,
		ClientID:      strings.TrimSpace(os.Getenv("GEASS_GITHUB_APP_CLIENT_ID")),
		ClientSecret:  strings.TrimSpace(os.Getenv("GEASS_GITHUB_APP_CLIENT_SECRET")),
		Slug:          strings.TrimSpace(os.Getenv("GEASS_GITHUB_APP_SLUG")),
		PrivateKeyPEM: strings.TrimSpace(os.Getenv("GEASS_GITHUB_APP_PRIVATE_KEY")),
		WebhookSecret: strings.TrimSpace(os.Getenv("GEASS_GITHUB_WEBHOOK_SECRET")),
		PublicBaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("GEASS_PUBLIC_URL")), "/"),
	}
}
