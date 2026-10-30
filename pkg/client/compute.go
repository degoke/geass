package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Machines: the clouds Geass can create machines at, the connections that hold the credentials for them, pools that
// make and remove machines as the load goes, and single servers. A pool or a server costs money at the cloud, and a
// connection holds the keys to an account, so what these methods do is for a person with an admin token: the reads
// show what exists, and the writes spend and stop spending.

// ---- the clouds ----

// ProviderField is one credential a cloud takes. A secret one is never shown again once it is saved.
type ProviderField struct {
	// Key is the name the credential is sent under.
	Key       string `json:"key"`
	Label     string `json:"label"`
	Help      string `json:"help,omitempty"`
	Secret    bool   `json:"secret,omitempty"`
	Optional  bool   `json:"optional,omitempty"`
	Multiline bool   `json:"multiline,omitempty"`
}

// ProviderSetting is a value a pool or a server can give for one cloud, such as a subnet or a firewall.
type ProviderSetting struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Help     string `json:"help,omitempty"`
	Required bool   `json:"required,omitempty"`
}

// ComputeProvider is a cloud Geass can create machines at.
type ComputeProvider struct {
	// ID is the cloud's name, as a connection says which cloud it is for: Hetzner, AWS and so on.
	ID      string `json:"id"`
	Label   string `json:"label"`
	Summary string `json:"summary"`
	// Fields are the credentials a connection to it holds.
	Fields   []ProviderField   `json:"fields"`
	Settings []ProviderSetting `json:"settings,omitempty"`
	// FreeSettings means the cloud takes settings of any name and hands them on to something else.
	FreeSettings bool `json:"freeSettings,omitempty"`
	// RegionHelp says what a region is for this cloud, with examples.
	RegionHelp string `json:"regionHelp,omitempty"`
}

// Field returns the credential with the key.
func (p ComputeProvider) Field(key string) (ProviderField, bool) {
	for _, field := range p.Fields {
		if field.Key == key {
			return field, true
		}
	}
	return ProviderField{}, false
}

// ComputeProviders lists the clouds Geass can create machines at, and the credentials and settings each takes.
func (c *Client) ComputeProviders(ctx context.Context) ([]ComputeProvider, error) {
	var out struct {
		Items []ComputeProvider `json:"items"`
	}
	err := c.Do(ctx, http.MethodGet, "/compute/providers", nil, nil, &out)
	return out.Items, err
}

// CloudSize is a machine size a cloud offers.
type CloudSize struct {
	ID          string  `json:"id"`
	Description string  `json:"description,omitempty"`
	VCPUs       int     `json:"vcpus"`
	MemoryMiB   int64   `json:"memoryMiB"`
	DiskGB      int     `json:"diskGB,omitempty"`
	HourlyPrice float64 `json:"hourlyPrice,omitempty"`
	Currency    string  `json:"currency,omitempty"`
	Arch        string  `json:"arch,omitempty"`
}

// CloudSizes asks a cloud, through a connection, which machine sizes it offers in a region, cheapest first. It calls
// the cloud with the connection's credentials, so it needs an admin's token. An empty region is the connection's own.
func (c *Client) CloudSizes(ctx context.Context, connection, region string) ([]CloudSize, error) {
	query := url.Values{"connection": {connection}}
	if region != "" {
		query.Set("region", region)
	}
	var out struct {
		Items []CloudSize `json:"items"`
	}
	err := c.Do(ctx, http.MethodGet, "/compute/sizes", query, nil, &out)
	return out.Items, err
}

// SelectSizes keeps the sizes that have at least minVCPUs processors and minMemoryMiB of memory, in the order they
// came in (cheapest first), and at most limit of them when limit is above zero. It reports how many matched before
// the limit cut the list.
func SelectSizes(sizes []CloudSize, minVCPUs int, minMemoryMiB int64, limit int) (selected []CloudSize, matched int) {
	selected = make([]CloudSize, 0, len(sizes))
	for _, size := range sizes {
		if size.VCPUs >= minVCPUs && size.MemoryMiB >= minMemoryMiB {
			selected = append(selected, size)
		}
	}
	matched = len(selected)
	if limit > 0 && len(selected) > limit {
		selected = selected[:limit]
	}
	return selected, matched
}

// ---- connections ----

// CloudConnection is a connection to a cloud: the credentials Geass uses there, and whether the cloud accepts them.
// The credentials themselves are never returned.
type CloudConnection struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	// Purposes are what the connection is for: Compute (creating machines), and for AWS also Storage and Database.
	Purposes []string `json:"purposes"`
	// Project is set for a connection that belongs to one project. Connections that create machines never do.
	Project string `json:"project,omitempty"`
	Region  string `json:"region,omitempty"`
	// Available is whether the cloud accepted the credentials the last time it was asked.
	Available  bool       `json:"available"`
	Account    string     `json:"account,omitempty"`
	Message    string     `json:"message,omitempty"`
	Warnings   []string   `json:"warnings,omitempty"`
	VerifiedAt *time.Time `json:"verifiedAt,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	// UsedBy are the pools, and Machines the servers with a machine of their own, that depend on the connection.
	UsedBy    []string  `json:"usedBy,omitempty"`
	Machines  int       `json:"machines,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// CloudConnections lists the connections to clouds.
func (c *Client) CloudConnections(ctx context.Context) ([]CloudConnection, error) {
	var out struct {
		Items []CloudConnection `json:"items"`
	}
	err := c.Do(ctx, http.MethodGet, "/connections", nil, nil, &out)
	return out.Items, err
}

// GetCloudConnection reads one connection.
func (c *Client) GetCloudConnection(ctx context.Context, name string) (CloudConnection, error) {
	var out CloudConnection
	return out, c.Do(ctx, http.MethodGet, "/connections/"+url.PathEscape(name), nil, nil, &out)
}

// CloudConnectionRequest is a connection to make. Credentials are sent under the keys the cloud names (see
// ComputeProviders), and are stored in a Secret that nothing but the operator reads.
type CloudConnectionRequest struct {
	Name     string
	Provider string
	// Purposes default to Compute for a cloud that creates machines.
	Purposes []string
	Region   string
	// Credentials are by key. A cloud whose credentials include a key named region (AWS) takes the connection's region
	// for it: the API reads one field for both.
	Credentials map[string]string
}

// reservedConnectionKeys are the fields of a connection that a credential cannot be named.
var reservedConnectionKeys = []string{"name", "provider", "purposes"}

// credentialBody is the credentials as fields of a request, refusing one that would take the place of another field.
func credentialBody(credentials map[string]string) (map[string]any, error) {
	body := make(map[string]any, len(credentials)+4)
	for key, value := range credentials {
		if slices.Contains(reservedConnectionKeys, key) {
			return nil, fmt.Errorf("%q cannot be the name of a credential", key)
		}
		body[key] = value
	}
	return body, nil
}

// CreateCloudConnection makes a connection. It answers as soon as the connection is saved: whether the cloud accepts
// the credentials is learned a moment later, from GetCloudConnection.
func (c *Client) CreateCloudConnection(ctx context.Context, req CloudConnectionRequest) error {
	body, err := credentialBody(req.Credentials)
	if err != nil {
		return err
	}
	body["name"], body["provider"] = req.Name, req.Provider
	if len(req.Purposes) > 0 {
		body["purposes"] = req.Purposes
	}
	if req.Region != "" {
		body["region"] = req.Region
	}
	return c.Do(ctx, http.MethodPost, "/connections", nil, body, nil)
}

// CloudConnectionChange is what to change about a connection. A field that is nil or empty is left as it is.
type CloudConnectionChange struct {
	Purposes *[]string
	// Region is the connection's default region. Point at "" to clear it.
	Region *string
	// Credentials are the ones to replace, by key: the others keep their values, so a single key can be rotated.
	Credentials map[string]string
}

// IsEmpty says whether nothing is to change.
func (c CloudConnectionChange) IsEmpty() bool {
	return c.Purposes == nil && c.Region == nil && len(c.Credentials) == 0
}

// UpdateCloudConnection changes a connection.
func (c *Client) UpdateCloudConnection(ctx context.Context, name string, change CloudConnectionChange) error {
	body, err := credentialBody(change.Credentials)
	if err != nil {
		return err
	}
	if change.Purposes != nil {
		body["purposes"] = *change.Purposes
	}
	if change.Region != nil {
		body["region"] = *change.Region
	}
	return c.Do(ctx, http.MethodPatch, "/connections/"+url.PathEscape(name), nil, body, nil)
}

// DeleteCloudConnection removes a connection and its credentials, confirming it by name. The API refuses while a pool
// or a machine still depends on it, because the machines could no longer be deleted at the cloud.
func (c *Client) DeleteCloudConnection(ctx context.Context, name string) error {
	return c.Do(ctx, http.MethodDelete, "/connections/"+url.PathEscape(name), url.Values{"confirmName": {name}}, nil, nil)
}

// VerifyCloudConnection asks the cloud again whether it accepts a connection's credentials. It returns when the
// request is made, not when the cloud has answered: read the connection again for that.
func (c *Client) VerifyCloudConnection(ctx context.Context, name string) error {
	return c.Do(ctx, http.MethodPost, "/connections/"+url.PathEscape(name)+"/verify", nil, map[string]any{}, nil)
}

// ---- pools ----

// NodePoolMember is a node of a pool.
type NodePoolMember struct {
	Name       string     `json:"name"`
	Phase      string     `json:"phase"`
	Message    string     `json:"message,omitempty"`
	Ready      bool       `json:"ready"`
	Deleting   bool       `json:"deleting,omitempty"`
	InstanceID string     `json:"instanceId,omitempty"`
	Address    string     `json:"address,omitempty"`
	Size       string     `json:"size,omitempty"`
	Region     string     `json:"region,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	ReadyAt    *time.Time `json:"readyAt,omitempty"`
}

// NodePoolPlacement is the region, site and tags a pool's nodes carry.
type NodePoolPlacement struct {
	Region string   `json:"region,omitempty"`
	Zone   string   `json:"zone,omitempty"`
	Tags   []string `json:"tags,omitempty"`
}

// NodePoolScaling is how a pool follows load.
type NodePoolScaling struct {
	Enabled                       bool  `json:"enabled"`
	ScaleUpDelaySeconds           int32 `json:"scaleUpDelaySeconds"`
	MaxScaleUp                    int32 `json:"maxScaleUp"`
	ScaleDownUtilizationPercent   int32 `json:"scaleDownUtilizationPercent"`
	ScaleDownAfterMinutes         int32 `json:"scaleDownAfterMinutes"`
	ScaleDownDelayAfterAddMinutes int32 `json:"scaleDownDelayAfterAddMinutes"`
	MinNodeLifetimeMinutes        int32 `json:"minNodeLifetimeMinutes"`
	SpareNodes                    int32 `json:"spareNodes"`
}

// NodePoolSize is the machine size a pool makes nodes with now, as the cloud describes it.
type NodePoolSize struct {
	Name        string `json:"name,omitempty"`
	VCPUs       int32  `json:"vcpus,omitempty"`
	MemoryMiB   int64  `json:"memoryMiB,omitempty"`
	HourlyPrice string `json:"hourlyPrice,omitempty"`
	Currency    string `json:"currency,omitempty"`
}

// NodePool is a pool of nodes, with what it has and what it is doing. The list leaves Members out; reading one pool
// has them.
type NodePool struct {
	Name       string `json:"name"`
	Cluster    string `json:"cluster"`
	Connection string `json:"connection,omitempty"`
	Provider   string `json:"provider,omitempty"`
	// Inventory names the servers of a pool made of servers you own.
	Inventory       []string           `json:"inventory,omitempty"`
	Region          string             `json:"region,omitempty"`
	Size            string             `json:"size,omitempty"`
	FallbackSizes   []string           `json:"fallbackSizes,omitempty"`
	FallbackRegions []string           `json:"fallbackRegions,omitempty"`
	Image           string             `json:"image,omitempty"`
	Spot            bool               `json:"spot,omitempty"`
	AddressType     string             `json:"addressType,omitempty"`
	Settings        map[string]string  `json:"settings,omitempty"`
	Placement       *NodePoolPlacement `json:"placement,omitempty"`
	// Storage is None (no databases or volumes on the nodes) or Allow.
	Storage string `json:"storage"`
	// MinNodes is how many nodes the pool always has, and MaxNodes the most it ever has: the cap on what it can cost.
	MinNodes    int32            `json:"minNodes"`
	MaxNodes    int32            `json:"maxNodes"`
	Autoscaling *NodePoolScaling `json:"autoscaling,omitempty"`
	// OrphanPolicy is what is done with a machine at the cloud that no node owns: Report or Delete.
	OrphanPolicy string `json:"orphanPolicy"`
	// Suspended pools neither add nor remove nodes.
	Suspended bool `json:"suspended,omitempty"`

	// Phase is Ready, Scaling, Degraded, Paused, Suspended or Pending.
	Phase        string `json:"phase"`
	Message      string `json:"message,omitempty"`
	DesiredNodes int32  `json:"desiredNodes"`
	NodeCount    int32  `json:"nodes"`
	ReadyNodes   int32  `json:"readyNodes"`
	PendingPods  int32  `json:"pendingPods"`
	// ActiveSize is the size new nodes are made with now, which differs from Size while a fallback is in use.
	ActiveSize *NodePoolSize `json:"activeSize,omitempty"`
	// EstimatedHourlyCost is what the pool's machines cost for an hour at the cloud's list price, such as "0.0176 EUR".
	EstimatedHourlyCost string `json:"estimatedHourlyCost,omitempty"`
	// ConsecutiveFailures is how many times in a row the cloud failed to make a machine. After repeated failures the
	// pool pauses (PausedUntil) until the cause is fixed and ResumeNodePool is called, or the pause ends.
	ConsecutiveFailures int32            `json:"consecutiveFailures,omitempty"`
	LastFailure         string           `json:"lastFailure,omitempty"`
	PausedUntil         *time.Time       `json:"pausedUntil,omitempty"`
	LastScaleUp         *time.Time       `json:"lastScaleUp,omitempty"`
	LastScaleDown       *time.Time       `json:"lastScaleDown,omitempty"`
	Orphans             []string         `json:"orphans,omitempty"`
	FallbackStep        int32            `json:"fallbackStep,omitempty"`
	Members             []NodePoolMember `json:"members,omitempty"`
	CreatedAt           time.Time        `json:"createdAt"`
}

// IsInventory says whether the pool is made of servers you own and not machines made at a cloud.
func (p NodePool) IsInventory() bool { return p.Connection == "" }

// NodePools lists the pools, without their nodes.
func (c *Client) NodePools(ctx context.Context) ([]NodePool, error) {
	var out struct {
		Items []NodePool `json:"items"`
	}
	err := c.Do(ctx, http.MethodGet, "/pools", nil, nil, &out)
	return out.Items, err
}

// NodePool reads one pool, with its nodes.
func (c *Client) NodePool(ctx context.Context, name string) (NodePool, error) {
	var out NodePool
	return out, c.Do(ctx, http.MethodGet, "/pools/"+url.PathEscape(name), nil, nil, &out)
}

// InventoryServer is a server you own, listed in a pool that is made of such servers.
type InventoryServer struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	SSHUser string `json:"sshUser"`
	SSHPort int    `json:"sshPort,omitempty"`
	// SSHKeySecret names a Secret that holds the server's key, for a server whose key is not the pool's.
	SSHKeySecret string `json:"sshKeySecret,omitempty"`
}

// ParseInventory reads a pool's servers from JSON: a list of {name, address, sshUser, sshPort?, sshKeySecret?}. A
// field it does not know is an error, since a misspelt one (ssh_user) would otherwise be dropped without a word.
func ParseInventory(raw []byte) ([]InventoryServer, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var servers []InventoryServer
	if err := decoder.Decode(&servers); err != nil {
		return nil, fmt.Errorf("the inventory must be a JSON list of servers, such as [{\"name\": \"spare-1\", \"address\": \"198.51.100.1\", \"sshUser\": \"root\"}]: %w", err)
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("the inventory lists no servers")
	}
	return servers, nil
}

// PoolSettings are the settings of a pool that can be given when it is made or changed. A nil field is not sent: on a
// new pool it takes its default, and on a change it keeps its value. So a switch turned off is a pointer to false, and
// a list or a text emptied is a pointer to nothing, which is sent and clears it.
type PoolSettings struct {
	// Connection is the cloud connection that makes the machines. A pool has this or Inventory.
	Connection *string
	// Inventory is the servers of a pool made of servers you own, joined when load needs them. InventoryKey is their SSH
	// private key, stored as a Secret; give it unless every server names a SSHKeySecret.
	Inventory    *[]InventoryServer
	InventoryKey *string

	Region          *string
	Size            *string
	FallbackSizes   *[]string
	FallbackRegions *[]string
	Image           *string
	Spot            *bool
	// AddressType is Public (the default) or Private.
	AddressType *string
	// Settings are the cloud's own (a subnet, a firewall), by key. A change replaces them all, so send the whole set. An
	// empty map clears them.
	Settings map[string]string

	// PlacementRegion, PlacementZone, Tags and ReservedTags are the region, site and tags the nodes carry. The API
	// replaces all four together when any is sent: a pool whose placement is changed must be sent the whole of it.
	PlacementRegion *string
	PlacementZone   *string
	Tags            *[]string
	ReservedTags    *[]string

	// Storage is None (the default: no databases or volumes on the nodes) or Allow.
	Storage *string
	// MinNodes is how many nodes the pool always has (a pool that does not autoscale has exactly this many), and MaxNodes
	// the most it ever has. MaxNodes is required when a pool is made.
	MinNodes *int
	MaxNodes *int
	// Autoscaling makes the pool add nodes when pods cannot start and remove idle ones, between MinNodes and MaxNodes.
	Autoscaling *bool
	// Suspend stops the pool from adding or removing nodes. The ones it has stay.
	Suspend *bool

	ScaleUpDelaySeconds           *int
	MaxScaleUp                    *int
	ScaleDownUtilizationPercent   *int
	ScaleDownAfterMinutes         *int
	ScaleDownDelayAfterAddMinutes *int
	MinNodeLifetimeMinutes        *int
	SpareNodes                    *int

	// OrphanPolicy is Report (the default) or Delete: whether to delete machines at the cloud that no node owns.
	OrphanPolicy *string
}

// AutoscalingUnsaid says whether a new pool could have more nodes than it starts with but was not told whether to
// autoscale. A pool that does not autoscale has exactly MinNodes, so the maximum would do nothing, which is rarely what
// was meant: callers that make pools ask for the choice instead of guessing it.
func (p PoolSettings) AutoscalingUnsaid() bool {
	if p.Autoscaling != nil || p.MaxNodes == nil {
		return false
	}
	minimum := 0
	if p.MinNodes != nil {
		minimum = *p.MinNodes
	}
	return *p.MaxNodes > minimum
}

func putValue[T any](fields map[string]any, key string, value *T) {
	if value != nil {
		fields[key] = *value
	}
}

// putList sends a list. The API reads a list that is sent empty as nothing sent, so a list to be emptied is sent as one
// empty entry, which it takes as an empty list.
func putList(fields map[string]any, key string, value *[]string) {
	switch {
	case value == nil:
	case len(*value) == 0:
		fields[key] = []string{""}
	default:
		fields[key] = *value
	}
}

// Fields are the settings as the body of a request to make or change a pool.
func (p PoolSettings) Fields() map[string]any {
	fields := map[string]any{}
	putValue(fields, "connection", p.Connection)
	if p.Inventory != nil {
		// A list of servers is sent as its JSON text: the API takes a list in a body to be a field repeated, and
		// reads the whole inventory from one value.
		raw, _ := json.Marshal(*p.Inventory)
		fields["inventory"] = string(raw)
	}
	putValue(fields, "inventoryKey", p.InventoryKey)
	putValue(fields, "region", p.Region)
	putValue(fields, "size", p.Size)
	putList(fields, "fallbackSizes", p.FallbackSizes)
	putList(fields, "fallbackRegions", p.FallbackRegions)
	putValue(fields, "image", p.Image)
	putValue(fields, "spot", p.Spot)
	putValue(fields, "addressType", p.AddressType)
	if p.Settings != nil {
		fields["settings"] = p.Settings
	}
	putValue(fields, "placementRegion", p.PlacementRegion)
	putValue(fields, "placementZone", p.PlacementZone)
	putList(fields, "tags", p.Tags)
	putList(fields, "reservedTags", p.ReservedTags)
	putValue(fields, "storage", p.Storage)
	putValue(fields, "minNodes", p.MinNodes)
	putValue(fields, "maxNodes", p.MaxNodes)
	putValue(fields, "autoscaling", p.Autoscaling)
	putValue(fields, "suspend", p.Suspend)
	putValue(fields, "scaleUpDelaySeconds", p.ScaleUpDelaySeconds)
	putValue(fields, "maxScaleUp", p.MaxScaleUp)
	putValue(fields, "scaleDownUtilizationPercent", p.ScaleDownUtilizationPercent)
	putValue(fields, "scaleDownAfterMinutes", p.ScaleDownAfterMinutes)
	putValue(fields, "scaleDownDelayAfterAddMinutes", p.ScaleDownDelayAfterAddMinutes)
	putValue(fields, "minNodeLifetimeMinutes", p.MinNodeLifetimeMinutes)
	putValue(fields, "spareNodes", p.SpareNodes)
	putValue(fields, "orphanPolicy", p.OrphanPolicy)
	return fields
}

// CreateNodePool makes a pool. It costs money from the moment it has nodes: MaxNodes is the most it can have.
func (c *Client) CreateNodePool(ctx context.Context, name string, settings PoolSettings) error {
	if settings.MaxNodes == nil {
		return fmt.Errorf("a pool needs a maximum number of nodes: it is the limit on what the pool can cost")
	}
	fields := settings.Fields()
	fields["name"] = name
	return c.Do(ctx, http.MethodPost, "/pools", nil, fields, nil)
}

// UpdateNodePool changes a pool: what is nil in settings keeps its value. The size, region and settings apply to nodes
// made from then on, and the nodes it has are left as they are.
func (c *Client) UpdateNodePool(ctx context.Context, name string, settings PoolSettings) error {
	return c.Do(ctx, http.MethodPatch, "/pools/"+url.PathEscape(name), nil, settings.Fields(), nil)
}

// DeleteNodePool deletes a pool, its nodes and their machines at the cloud, confirming it by name.
func (c *Client) DeleteNodePool(ctx context.Context, name string) error {
	return c.Do(ctx, http.MethodDelete, "/pools/"+url.PathEscape(name), url.Values{"confirmName": {name}}, nil, nil)
}

// ResumeNodePool ends the pause of a pool that stopped adding nodes after repeated failures, and makes it forget them.
// It will try to make machines again, so fix what failed first.
func (c *Client) ResumeNodePool(ctx context.Context, name string) error {
	return c.Do(ctx, http.MethodPost, "/pools/"+url.PathEscape(name)+"/resume", nil, map[string]any{}, nil)
}

// ---- single servers ----

// ServerRequest is a server to add on its own: a machine made at a cloud (Connection and Size), or one you have (Address
// and SSHUser).
type ServerRequest struct {
	// Name is made up for a machine at a cloud when it is left out. A server you have needs one.
	Name string `json:"name,omitempty"`

	// For a machine at a cloud:
	Connection string            `json:"connection,omitempty"`
	Region     string            `json:"region,omitempty"`
	Size       string            `json:"size,omitempty"`
	Image      string            `json:"image,omitempty"`
	Settings   map[string]string `json:"settings,omitempty"`

	// For a server you have, reached over SSH. SSHPrivateKey is stored as a Secret; SSHKeySecret names one that already
	// holds the key. KnownHosts pins the server's host keys.
	Address       string `json:"address,omitempty"`
	SSHUser       string `json:"sshUser,omitempty"`
	SSHPort       int    `json:"sshPort,omitempty"`
	SSHPrivateKey string `json:"sshPrivateKey,omitempty"`
	SSHKeySecret  string `json:"sshKeySecret,omitempty"`
	KnownHosts    string `json:"knownHosts,omitempty"`

	PlacementRegion string   `json:"placementRegion,omitempty"`
	PlacementZone   string   `json:"placementZone,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	ReservedTags    []string `json:"reservedTags,omitempty"`
	// Storage is None or Allow (the default for a single server).
	Storage string `json:"storage,omitempty"`
}

// IsCloud says whether the server is a machine to be made at a cloud and not one you have.
func (s ServerRequest) IsCloud() bool { return s.Address == "" }

// AddServer adds a server and returns its name. A machine at a cloud costs money until it is removed.
func (c *Client) AddServer(ctx context.Context, server ServerRequest) (string, error) {
	body := map[string]any{}
	raw, err := json.Marshal(server)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", err
	}
	if server.IsCloud() {
		body["source"] = "cloud"
	} else {
		body["source"] = "existing"
	}
	var out struct {
		Name string `json:"name"`
	}
	if err := c.Do(ctx, http.MethodPost, "/servers", nil, body, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Name), nil
}

// RemoveServer removes a server that was added on its own, confirming it by name. Its services move elsewhere, and a
// machine made for it is deleted at the cloud.
func (c *Client) RemoveServer(ctx context.Context, name string) error {
	return c.Do(ctx, http.MethodDelete, "/servers/"+url.PathEscape(name), url.Values{"confirmName": {name}}, nil, nil)
}

// Server is a server that Geass joins to the cluster, as the operator knows it: also one that has not joined yet, or
// failed to, which Nodes does not list because the cluster has no node for it.
type Server struct {
	Name string `json:"name"`
	// Role is worker or control-plane.
	Role string `json:"role"`
	// Phase is Pending, Creating, Joining, Ready, Failed or Deleting.
	Phase string `json:"phase"`
	// Message says why a server is not ready. An observer's token reads none.
	Message string `json:"message,omitempty"`
	Address string `json:"address,omitempty"`
	// Source is cloud for a machine that Geass creates, and existing for one you have.
	Source string `json:"source"`
	Pool   string `json:"pool,omitempty"`
	// Node is the name of the cluster's node, once the server has joined.
	Node              string `json:"node,omitempty"`
	Joined            bool   `json:"joined"`
	Cordoned          bool   `json:"cordoned"`
	CordonedBy        string `json:"cordonedBy,omitempty"`
	CanRetry          bool   `json:"canRetry"`
	CanUpdateHostKeys bool   `json:"canUpdateHostKeys"`
	CanRemove         bool   `json:"canRemove"`
}

// Servers lists the servers Geass joins to the cluster, with the phase of each.
func (c *Client) Servers(ctx context.Context) ([]Server, error) {
	var out struct {
		Items []Server `json:"items"`
	}
	err := c.Do(ctx, http.MethodGet, "/servers", nil, nil, &out)
	return out.Items, err
}

// RetryServer starts a server that failed to join over.
func (c *Client) RetryServer(ctx context.Context, name string) error {
	return c.Do(ctx, http.MethodPost, "/servers/"+url.PathEscape(name)+"/retry", nil, nil, nil)
}

// UpdateServerHostKeys replaces the host keys Geass accepts from a server you have. It says whether the server is
// still failed, so that the caller can offer a retry.
func (c *Client) UpdateServerHostKeys(ctx context.Context, name, knownHosts string) (failed bool, err error) {
	var out struct {
		Failed bool `json:"failed"`
	}
	err = c.Do(ctx, http.MethodPost, "/servers/"+url.PathEscape(name)+"/host-keys", nil, map[string]any{"knownHosts": knownHosts}, &out)
	return out.Failed, err
}
