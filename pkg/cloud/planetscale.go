package cloud

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
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

// PlanetScaleAPIError is an error response from the PlanetScale API.
type PlanetScaleAPIError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *PlanetScaleAPIError) Error() string {
	return fmt.Sprintf("PlanetScale API %s: %s", e.Status, e.Body)
}

// PlanetScale database kinds, as the create-database API names them.
const (
	PlanetScaleKindMySQL    = "mysql"
	PlanetScaleKindPostgres = "postgresql"
)

// planetScaleBranch is the default production branch, which holds the data every login connects to.
const planetScaleBranch = "main"

// PlanetScaleDatabase is a database as PlanetScale reports it.
type PlanetScaleDatabase struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Ready is false while PlanetScale is still provisioning the database, which takes a while after
	// it is created; branches and logins cannot be created until it is true.
	Ready bool `json:"ready"`
}

// PlanetScaleClusterSize is a size PlanetScale offers for a database. Name is what the create call takes.
type PlanetScaleClusterSize struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	CPU         string `json:"cpu"`
	RAM         int64  `json:"ram"`     // bytes
	Storage     *int64 `json:"storage"` // bytes, absent for sizes with elastic storage
	Enabled     bool   `json:"enabled"` // whether the organization may use the size
	Development bool   `json:"development"`
	Production  bool   `json:"production"`
	SortOrder   int    `json:"sort_order"`
	Metal       bool   `json:"metal"`
	// Provider and Architecture tell apart sizes that share a name: Postgres is cheaper on arm64 than on x86_64.
	Provider     *string `json:"provider"`
	Architecture *string `json:"architecture"`
	// Rate and ReplicaRate are set only when rates were asked for. PlanetScale's API reference states no
	// unit; they are whole US dollars per month, as the pscale CLI prints them ("$%d") and as PlanetScale's
	// published pricing lists them. Rate is for the highly available cluster (a primary and two replicas),
	// ReplicaRate for one node: each additional replica, and a single-node Postgres cluster. Both are for the
	// organization's default region, which is where databases are created.
	Rate        *float64 `json:"rate"`
	ReplicaRate *float64 `json:"replica_rate"`
}

// PlanetScaleCredentials is a login for a PlanetScale database. PlanetScale shows the password once.
type PlanetScaleCredentials struct {
	// ID identifies the login to PlanetScale, so a login that could not be stored can be revoked.
	ID       string
	Host     string
	Username string
	Password string
	// Database is the database to connect to when PlanetScale names one; empty means the database that was created.
	Database string
}

// planetScalePassword is a Vitess (MySQL) branch password.
type planetScalePassword struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Plain    string `json:"plain_text"`
	Host     string `json:"access_host_url"`
}

// planetScaleRole is a Postgres role: Postgres databases have no branch passwords.
type planetScaleRole struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Password string `json:"password"`
	Host     string `json:"access_host_url"`
	Database string `json:"database_name"`
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

func (c *PlanetScaleClient) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL()+path, reader)
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
	defer func() { _ = resp.Body.Close() }()
	payload, err := readHTTPBody(resp.Body, s3ListResponseLimit)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return &PlanetScaleAPIError{StatusCode: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(payload))}
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	return json.Unmarshal(payload, out)
}

// path builds an API path under the organization. Every segment is escaped, so a name cannot reach another endpoint.
func (c *PlanetScaleClient) path(segments ...string) string {
	parts := make([]string, 0, 2+len(segments))
	parts = append(parts, "organizations", url.PathEscape(c.Org))
	for _, segment := range segments {
		parts = append(parts, url.PathEscape(segment))
	}
	return "/" + strings.Join(parts, "/")
}

// checkNames rejects names that would not stay one path segment: escaping leaves "." and ".." alone.
func (c *PlanetScaleClient) checkNames(names ...string) error {
	for _, name := range append([]string{c.Org}, names...) {
		if name == "" || name == "." || name == ".." {
			return fmt.Errorf("PlanetScale name %q is not valid", name)
		}
	}
	return nil
}

// EnsureDatabase creates a database of the given kind when none with that name exists, and reports it.
// A database that already exists must be of the same kind, so a MySQL database is never handed out as
// a Postgres one. clusterSize is used only when the database is created; empty leaves it to PlanetScale.
func (c *PlanetScaleClient) EnsureDatabase(ctx context.Context, name, kind, clusterSize string) (PlanetScaleDatabase, error) {
	if err := c.checkNames(name); err != nil {
		return PlanetScaleDatabase{}, err
	}
	var database PlanetScaleDatabase
	err := c.do(ctx, http.MethodGet, c.path("databases", name), nil, &database)
	if err == nil && database.Name != "" {
		if database.Kind != "" && database.Kind != kind {
			return PlanetScaleDatabase{}, fmt.Errorf("PlanetScale database %q already exists as %s, not %s", name, database.Kind, kind)
		}
		return database, nil
	}
	body := map[string]string{"name": name, "kind": kind}
	if clusterSize != "" {
		body["cluster_size"] = clusterSize
	}
	database = PlanetScaleDatabase{}
	if err := c.do(ctx, http.MethodPost, c.path("databases"), body, &database); err != nil {
		return PlanetScaleDatabase{}, err
	}
	return database, nil
}

// ListClusterSizes returns the sizes the organization can create a database of the given kind with,
// smallest first. withRates also asks for what each size costs.
func (c *PlanetScaleClient) ListClusterSizes(ctx context.Context, kind string, withRates bool) ([]PlanetScaleClusterSize, error) {
	if err := c.checkNames(); err != nil {
		return nil, err
	}
	query := url.Values{"engine": {kind}}
	if withRates {
		query.Set("rates", "true")
	}
	var all []PlanetScaleClusterSize
	if err := c.do(ctx, http.MethodGet, c.path("cluster-size-skus")+"?"+query.Encode(), nil, &all); err != nil {
		return nil, err
	}
	sizes := make([]PlanetScaleClusterSize, 0, len(all))
	for _, size := range all {
		if size.Enabled && size.Name != "" {
			sizes = append(sizes, size)
		}
	}
	slices.SortStableFunc(sizes, func(a, b PlanetScaleClusterSize) int { return cmp.Compare(a.SortOrder, b.SortOrder) })
	return sizes, nil
}

// loginResource is the endpoint that issues logins: MySQL has branch passwords and Postgres has roles.
func loginResource(kind string) (string, error) {
	switch kind {
	case PlanetScaleKindMySQL:
		return "passwords", nil
	case PlanetScaleKindPostgres:
		return "roles", nil
	default:
		return "", fmt.Errorf("PlanetScale database kind %q is not supported", kind)
	}
}

// hostOnly returns the host in what PlanetScale reports as access_host_url. It is a host for branch passwords
// but described as a connection string for roles, and the URI built around it must hold only the host.
func hostOnly(raw string) string {
	raw = strings.TrimSpace(raw)
	candidate := raw
	if !strings.Contains(candidate, "://") {
		candidate = "//" + candidate
	}
	if parsed, err := url.Parse(candidate); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return raw
}

// CreateCredentials issues a login for the default production branch. MySQL databases get a branch
// password and Postgres databases get a role, which PlanetScale serves from different endpoints.
func (c *PlanetScaleClient) CreateCredentials(ctx context.Context, database, kind, name string) (PlanetScaleCredentials, error) {
	resource, err := loginResource(kind)
	if err != nil {
		return PlanetScaleCredentials{}, err
	}
	if err := c.checkNames(database); err != nil {
		return PlanetScaleCredentials{}, err
	}
	path := c.path("databases", database, "branches", planetScaleBranch, resource)
	if kind == PlanetScaleKindMySQL {
		var password planetScalePassword
		if err := c.do(ctx, http.MethodPost, path, map[string]string{"name": name}, &password); err != nil {
			return PlanetScaleCredentials{}, err
		}
		return PlanetScaleCredentials{ID: password.ID, Host: hostOnly(password.Host), Username: password.Username, Password: password.Plain}, nil
	}
	var role planetScaleRole
	// The application owns its database, as a MySQL branch password does, so it can run migrations.
	body := map[string]any{"name": name, "inherited_roles": []string{"postgres"}}
	if err := c.do(ctx, http.MethodPost, path, body, &role); err != nil {
		return PlanetScaleCredentials{}, err
	}
	return PlanetScaleCredentials{ID: role.ID, Host: hostOnly(role.Host), Username: role.Username, Password: role.Password, Database: role.Database}, nil
}

// RevokeCredentials deletes a login this client issued, such as one that could not be stored and so can
// never be used: PlanetScale shows the password once. A login that is already gone is not an error.
func (c *PlanetScaleClient) RevokeCredentials(ctx context.Context, database, kind, id string) error {
	resource, err := loginResource(kind)
	if err != nil {
		return err
	}
	if err := c.checkNames(database, id); err != nil {
		return err
	}
	err = c.do(ctx, http.MethodDelete, c.path("databases", database, "branches", planetScaleBranch, resource, id), nil, nil)
	var apiErr *PlanetScaleAPIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}
