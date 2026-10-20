package githubapp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	userTokenURL = "https://github.com/login/oauth/access_token"
	// maxUserInstallationPages bounds how many pages of a person's installations are read: 1000 installations is more
	// than anyone who is not an administrator of GitHub itself has access to.
	maxUserInstallationPages = 10
	maxUserTokenBody         = 1 << 20
)

// UserAccessToken exchanges the code GitHub sends back when someone installs the App, for that person's own token. GitHub
// only sends a code when the App asks for the person's authorization during installation, and the token says who came
// back, which the installation id in the address does not: anyone can write that into an address.
func (c *Client) UserAccessToken(code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", fmt.Errorf("GitHub did not send an authorization code")
	}
	form := url.Values{"client_id": {c.Config.ClientID}, "client_secret": {c.Config.ClientSecret}, "code": {code}}
	request, err := http.NewRequest(http.MethodPost, userTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient().Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxUserTokenBody))
	if err != nil {
		return "", err
	}
	if response.StatusCode >= 300 {
		return "", fmt.Errorf("GitHub returned %s", response.Status)
	}
	// GitHub answers 200 with an error in the body for a code that is wrong, used or expired.
	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	if result.AccessToken == "" {
		if result.Error != "" {
			return "", fmt.Errorf("GitHub did not accept the authorization code (%s)", result.Error)
		}
		return "", fmt.Errorf("GitHub did not return a user token")
	}
	return result.AccessToken, nil
}

// UserHasInstallation is whether the person the token is for may access the installation of the App: they installed it,
// or administer or are a member with access to the account it is installed on. That is what GitHub lists for the token.
func (c *Client) UserHasInstallation(userToken string, installationID int64) (bool, error) {
	for page := 1; page <= maxUserInstallationPages; page++ {
		data, err := c.apiRequest(userToken, fmt.Sprintf("/user/installations?per_page=100&page=%d", page))
		if err != nil {
			return false, err
		}
		var result struct {
			Installations []struct {
				ID int64 `json:"id"`
			} `json:"installations"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return false, err
		}
		for _, installation := range result.Installations {
			if installation.ID == installationID {
				return true, nil
			}
		}
		if len(result.Installations) < 100 {
			break
		}
	}
	return false, nil
}
