package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloud"
	"github.com/degoke/geass/pkg/platform"
)

// planetScaleSizesTTL is how long a list of sizes is kept. Sizes and prices change rarely, and every picker
// open would otherwise cost PlanetScale one or two requests.
const planetScaleSizesTTL = 5 * time.Minute

// planetScaleClusterSize is a size a PlanetScale database can be created with, as the picker lists it.
// The prices are whole US dollars per month, in the organization's default region, and are left out when
// PlanetScale gave none: MonthlyUSD is the highly available cluster (a primary and two replicas) and
// ReplicaMonthlyUSD one node, which is what a single-node Postgres cluster and each extra replica cost.
type planetScaleClusterSize struct {
	Name              string   `json:"name"`
	DisplayName       string   `json:"displayName"`
	CPU               string   `json:"cpu"`
	RAMBytes          int64    `json:"ramBytes"`
	StorageBytes      int64    `json:"storageBytes,omitempty"`
	Development       bool     `json:"development"`
	Production        bool     `json:"production"`
	Metal             bool     `json:"metal,omitempty"`
	Provider          string   `json:"provider,omitempty"`
	Architecture      string   `json:"architecture,omitempty"`
	MonthlyUSD        *float64 `json:"monthlyUsd,omitempty"`
	ReplicaMonthlyUSD *float64 `json:"replicaMonthlyUsd,omitempty"`
}

// planetScaleSizeCache keeps lists of sizes for a while, per project, connection and kind.
type planetScaleSizeCache struct {
	mu      sync.Mutex
	ttl     time.Duration // zero means planetScaleSizesTTL
	entries map[string]planetScaleSizeEntry
}

type planetScaleSizeEntry struct {
	items   []planetScaleClusterSize
	expires time.Time
}

func (c *planetScaleSizeCache) get(key string) ([]planetScaleClusterSize, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !time.Now().Before(entry.expires) {
		return nil, false
	}
	return entry.items, true
}

func (c *planetScaleSizeCache) put(key string, items []planetScaleClusterSize) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := c.ttl
	if ttl == 0 {
		ttl = planetScaleSizesTTL
	}
	now := time.Now()
	for k, entry := range c.entries {
		if !now.Before(entry.expires) {
			delete(c.entries, k)
		}
	}
	if c.entries == nil {
		c.entries = map[string]planetScaleSizeEntry{}
	}
	c.entries[key] = planetScaleSizeEntry{items: items, expires: now.Add(ttl)}
}

// planetScaleEngine reads the engine a size list is asked for. Only a name PlanetScale hosts is accepted, because
// anything else would quietly be answered with another engine's sizes and prices.
func planetScaleEngine(value string) (geassv1alpha1.GeassDatabaseEngine, error) {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "":
		return "", fmt.Errorf("engine is required")
	case "mysql":
		return geassv1alpha1.DatabaseEngineMySQL, nil
	case "postgres":
		return geassv1alpha1.DatabaseEnginePostgres, nil
	default:
		return "", fmt.Errorf("PlanetScale hosts MySQL and Postgres databases, not %s", value)
	}
}

// handleAPIPlanetScaleClusterSizes lists the cluster sizes PlanetScale offers an organization for an
// engine, so a database is created with a size its connection can actually use.
func (s *Server) handleAPIPlanetScaleClusterSizes(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	project := strings.TrimSpace(query.Get("project"))
	ref := strings.TrimSpace(query.Get("connection"))
	if project == "" || ref == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "project and connection are required"})
		return
	}
	engine, err := planetScaleEngine(query.Get("engine"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	kind, err := platform.PlanetScaleKind(engine)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	psClient, err := s.planetScaleClient(r.Context(), project, ref)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	items, err := s.planetScaleSizes(r.Context(), psClient, project+"/"+ref+"/"+kind, kind)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{dashboardLiteralError: "could not load PlanetScale cluster sizes"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// planetScaleSizes lists the sizes for a kind, from the cache when it has them. Prices are a courtesy: when
// PlanetScale refuses them to a token (403) or does not have them (404), the sizes are asked for again without
// prices, so such a token can still choose a size. Any other error is not retried.
func (s *Server) planetScaleSizes(ctx context.Context, psClient *cloud.PlanetScaleClient, key, kind string) ([]planetScaleClusterSize, error) {
	if items, ok := s.planetScaleSizeCache.get(key); ok {
		return items, nil
	}
	sizes, err := psClient.ListClusterSizes(ctx, kind, true)
	var apiErr *cloud.PlanetScaleAPIError
	if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusForbidden || apiErr.StatusCode == http.StatusNotFound) {
		sizes, err = psClient.ListClusterSizes(ctx, kind, false)
	}
	if err != nil {
		return nil, err
	}
	items := make([]planetScaleClusterSize, 0, len(sizes))
	for _, size := range sizes {
		item := planetScaleClusterSize{
			Name: size.Name, DisplayName: size.DisplayName, CPU: size.CPU, RAMBytes: size.RAM,
			Development: size.Development, Production: size.Production, Metal: size.Metal,
			MonthlyUSD: size.Rate, ReplicaMonthlyUSD: size.ReplicaRate,
		}
		if size.Storage != nil {
			item.StorageBytes = *size.Storage
		}
		if size.Provider != nil {
			item.Provider = *size.Provider
		}
		if size.Architecture != nil {
			item.Architecture = *size.Architecture
		}
		items = append(items, item)
	}
	s.planetScaleSizeCache.put(key, items)
	return items, nil
}

// checkPlanetScaleSize rejects a size PlanetScale does not offer for the engine, so a typo is caught when it is
// saved and not when the database is deployed. A size that cannot be checked, because the connection or
// PlanetScale cannot be reached, is accepted: PlanetScale checks it again when the database is created.
func (s *Server) checkPlanetScaleSize(ctx context.Context, project, ref string, engine geassv1alpha1.GeassDatabaseEngine, size string) error {
	kind, err := platform.PlanetScaleKind(engine)
	if err != nil {
		return err
	}
	if ref == "" {
		return nil
	}
	psClient, err := s.planetScaleClient(ctx, project, ref)
	if err != nil {
		return nil
	}
	items, err := s.planetScaleSizes(ctx, psClient, project+"/"+ref+"/"+kind, kind)
	if err != nil {
		return nil
	}
	for _, item := range items {
		if item.Name == size {
			return nil
		}
	}
	return fmt.Errorf("PlanetScale does not offer cluster size %q for %s databases", size, engine)
}

// validateClusterSize checks a cluster size chosen for a database. A size only means something to PlanetScale.
func (s *Server) validateClusterSize(ctx context.Context, db *geassv1alpha1.GeassDatabase, size string) error {
	if size == "" {
		return nil
	}
	if db.Spec.Provider != geassv1alpha1.DatabaseProviderPlanetScale {
		return errors.New("a cluster size only applies to PlanetScale databases")
	}
	ref := ""
	if db.Spec.ConnectionRef != nil {
		ref = db.Spec.ConnectionRef.Name
	}
	return s.checkPlanetScaleSize(ctx, db.Spec.Project, ref, db.Spec.Engine, size)
}

// planetScaleClient returns a client for a PlanetScale connection that belongs to the project.
func (s *Server) planetScaleClient(ctx context.Context, project, ref string) (*cloud.PlanetScaleClient, error) {
	connection, secret, err := platform.LoadCloudConnection(ctx, s.Client, project, ref)
	if err != nil {
		return nil, err
	}
	if connection.Spec.Provider != geassv1alpha1.CloudProviderPlanetScale {
		return nil, fmt.Errorf("connection %q is not a PlanetScale connection", ref)
	}
	token, org := platform.PlanetScaleAccess(*connection, secret)
	if token == "" || org == "" {
		return nil, fmt.Errorf("PlanetScale token and organization are required")
	}
	return &cloud.PlanetScaleClient{HTTP: s.HTTPClient, Token: token, Org: org}, nil
}
