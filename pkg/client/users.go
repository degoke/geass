package client

import (
	"context"
	"net/http"
	"net/url"
)

// The users who sign in to the dashboard with a password. They are the platform's, so these calls need an install-wide
// admin token. A password is never returned by a read. A password that Geass makes up is returned once, by the call that
// made it.

// DashboardUser is one person who signs in with a password.
type DashboardUser struct {
	Username string `json:"username"`
	// Role is admin or viewer.
	Role string `json:"role"`
	// Current is whether this is the user who asked. A token is no user, so it is never set for one.
	Current bool `json:"current,omitempty"`
}

// DashboardUsers is what Users answers.
type DashboardUsers struct {
	Items []DashboardUser `json:"items"`
	// IdentityProvider says an identity provider decides who signs in, so only an admin can use a password.
	IdentityProvider bool `json:"identityProvider,omitempty"`
}

// Users lists the users who sign in with a password.
func (c *Client) Users(ctx context.Context) (DashboardUsers, error) {
	var out DashboardUsers
	err := c.Do(ctx, http.MethodGet, "/users", nil, nil, &out)
	return out, err
}

// AddUser makes a user. With no password Geass makes one up, and returns it: it is not shown again.
func (c *Client) AddUser(ctx context.Context, username, role, password string) (generated string, err error) {
	return c.sendUserPassword(ctx, http.MethodPost, "/users", map[string]any{"username": username, "role": role, "password": password})
}

// SetUserRole makes a user an admin or a viewer. The last admin cannot be made a viewer.
func (c *Client) SetUserRole(ctx context.Context, username, role string) error {
	return c.Do(ctx, http.MethodPost, "/users/"+url.PathEscape(username)+"/role", nil, map[string]any{"role": role}, nil)
}

// ResetUserPassword sets a user's password and signs them out. With no password Geass makes one up, and returns it.
func (c *Client) ResetUserPassword(ctx context.Context, username, password string) (generated string, err error) {
	return c.sendUserPassword(ctx, http.MethodPost, "/users/"+url.PathEscape(username)+"/password", map[string]any{"password": password})
}

// RemoveUser removes a user and signs them out. The last admin cannot be removed.
func (c *Client) RemoveUser(ctx context.Context, username string) error {
	return c.Do(ctx, http.MethodDelete, "/users/"+url.PathEscape(username), nil, nil, nil)
}

// SignOutEveryone ends every dashboard session, of every user, for the one who asks too. Tokens keep working.
func (c *Client) SignOutEveryone(ctx context.Context) error {
	return c.Do(ctx, http.MethodPost, "/users/sign-out-all", nil, map[string]any{}, nil)
}

func (c *Client) sendUserPassword(ctx context.Context, method, path string, body map[string]any) (string, error) {
	if body["password"] == "" {
		delete(body, "password")
	}
	var out struct {
		Password string `json:"password"`
	}
	if err := c.Do(ctx, method, path, nil, body, &out); err != nil {
		return "", err
	}
	return out.Password, nil
}
