package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/degoke/geass/pkg/client"
)

// Machines: the clouds Geass can create machines at, and the pools of nodes it makes there as the load needs them.
//
// What an agent is given here stops short of the two decisions a person keeps. A connection holds the credentials of an
// account at a cloud, and a single server is a machine that is billed until someone removes it: so there is no tool that
// makes, changes, verifies or removes a connection, none that takes a credential or a key, and none that adds or removes
// a server on its own, in the way project_network can be read and not changed. An agent cannot be handed a secret it
// could leak, and it cannot choose which of a person's accounts to spend. What it can do is look at what exists, and run a
// pool that a person's connection already pays for, within a most that is set in the pool itself: create_node_pool,
// update_node_pool and resume_node_pool need a workspace admin, and delete_node_pool needs the deleting tools to be
// allowed, as every tool that deletes does.

const (
	defaultSizesShown = 30
	maxSizesShown     = 200
)

// What an agent may make or allow in one call. Every machine costs money, and an agent that is mistaken (or has been
// told something by a page or a log it read) must not be able to bill a hundred of them: the dashboard and the command
// line, which a person uses, take pools up to 200. These are the ceilings of the tools, not advice.
const (
	maxAgentPoolNodes  = 10 // max_nodes
	maxAgentMadeAtOnce = 3  // min_nodes: made as soon as the pool exists, or the value is raised
	maxAgentSpareNodes = 3  // spare_nodes: kept empty, and billed
)

// errAboveAgentCeiling says a number is more than an agent may set, and where a person can set it.
func errAboveAgentCeiling(field string, ceiling int) error {
	return fmt.Errorf("%s is at most %d when an agent sets it, because every machine costs money: ask a person to make a larger pool in the dashboard or with `geass pools`", field, ceiling)
}

type cloudSizesInput struct {
	Connection   string `json:"connection" jsonschema:"the connection to ask the cloud with, as listed by cloud_connections"`
	Region       string `json:"region,omitempty" jsonschema:"the cloud's own region or location, such as fsn1 or eu-north-1 (default: the connection's own)"`
	MinVCPUs     int    `json:"min_vcpus,omitempty" jsonschema:"only sizes with at least this many processors"`
	MinMemoryMiB int    `json:"min_memory_mib,omitempty" jsonschema:"only sizes with at least this much memory, in MiB (8192 is 8 GiB)"`
	Limit        int    `json:"limit,omitempty" jsonschema:"how many sizes to return, cheapest first (default 30, at most 200)"`
}

type createPoolInput struct {
	Name                  string            `json:"name" jsonschema:"the pool's name: lowercase letters, numbers and hyphens"`
	Connection            string            `json:"connection" jsonschema:"the cloud connection that makes the machines, as listed by cloud_connections. It must be for Compute"`
	Size                  string            `json:"size" jsonschema:"the cloud's name for a machine size, such as cx23 or t3.medium, as listed by cloud_sizes"`
	MaxNodes              int               `json:"max_nodes" jsonschema:"the most nodes the pool can ever have, 1 to 10 (a person sets more). Required. Every node is a machine that costs money, so this is the cap on what the pool can cost"`
	MinNodes              int               `json:"min_nodes,omitempty" jsonschema:"the fewest nodes the pool has, 0 to 3, default 0. These are made at once and kept, and a pool that does not autoscale has exactly this many"`
	Autoscaling           *bool             `json:"autoscaling,omitempty" jsonschema:"true to add nodes when pods cannot start for lack of room and remove idle ones, between min_nodes and max_nodes; false to keep exactly min_nodes. Required when max_nodes is above min_nodes"`
	Region                string            `json:"region,omitempty" jsonschema:"the cloud's own region or location (default: the connection's own)"`
	FallbackSizes         []string          `json:"fallback_sizes,omitempty" jsonschema:"sizes to try, in order, when the cloud has no room for size. A fallback may cost more"`
	FallbackRegions       []string          `json:"fallback_regions,omitempty" jsonschema:"regions to try, in order, when the cloud has no room in region"`
	Image                 string            `json:"image,omitempty" jsonschema:"the operating system image (default: the cloud's Ubuntu 24.04)"`
	Spot                  *bool             `json:"spot,omitempty" jsonschema:"use the cloud's cheaper spot machines, which it may take back; a node taken back is replaced"`
	Storage               string            `json:"storage,omitempty" jsonschema:"none (default: no databases or volumes on these nodes, so one can be removed freely) or allow"`
	Settings              map[string]string `json:"settings,omitempty" jsonschema:"settings of the cloud, by key, such as {\"firewallId\": \"42\"}; compute_providers lists the keys each cloud reads"`
	ScaleDownAfterMinutes int               `json:"scale_down_after_minutes,omitempty" jsonschema:"minutes a node must be a candidate for removal before it goes (default 10)"`
	SpareNodes            int               `json:"spare_nodes,omitempty" jsonschema:"empty nodes kept ready for a burst, 0 to 3; each one costs money while it waits"`
	PlacementRegion       string            `json:"placement_region,omitempty" jsonschema:"the region the nodes carry, for placing projects"`
	PlacementZone         string            `json:"placement_zone,omitempty" jsonschema:"the site the nodes carry"`
	Tags                  []string          `json:"tags,omitempty" jsonschema:"node tags the nodes carry"`
	ReservedTags          []string          `json:"reserved_tags,omitempty" jsonschema:"node tags that also keep all other work off the nodes"`
}

type updatePoolInput struct {
	Name        string `json:"name" jsonschema:"the pool to change"`
	MinNodes    *int   `json:"min_nodes,omitempty" jsonschema:"the fewest nodes the pool has, 0 to 3. Raising it makes machines at once"`
	MaxNodes    *int   `json:"max_nodes,omitempty" jsonschema:"the most nodes the pool can ever have, 1 to 10 (a person sets more). Raising it raises what the pool can cost"`
	Autoscaling *bool  `json:"autoscaling,omitempty" jsonschema:"true to follow the load between min_nodes and max_nodes, false to keep exactly min_nodes"`
	Suspend     *bool  `json:"suspend,omitempty" jsonschema:"true to stop adding and removing nodes (the ones the pool has stay), false to let it again"`
	Size        string `json:"size,omitempty" jsonschema:"the size of nodes made from now on. The nodes the pool has are not changed, and a larger size costs more"`
	SpareNodes  *int   `json:"spare_nodes,omitempty" jsonschema:"empty nodes kept ready for a burst, 0 to 3; each one costs money while it waits"`
}

type poolNameInput struct {
	Name string `json:"name" jsonschema:"the pool's name, as listed by node_pools"`
}

type deletePoolInput struct {
	Name    string `json:"name" jsonschema:"the pool to delete, as listed by node_pools"`
	Confirm string `json:"confirm" jsonschema:"the pool's name again, to confirm that it and its machines are to be deleted"`
}

// registerCompute offers the tools for machines. See the note at the top of this file for what is left out.
func (s *Server) registerCompute() {
	api := s.opts.Client

	add(s, toolSpec{name: "compute_providers", description: "List the clouds Geass can create machines at, with the credentials each takes (their names, and whether each is secret: never a value) and the settings a pool can give each. " +
		"This tool only reads. Connections to a cloud hold the credentials of a billing account, so an agent cannot make or change one: a person does it in the dashboard or with `geass connections add`.", level: readAccess, workspaceOnly: true, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (any, error) {
			providers, err := api.ComputeProviders(ctx)
			if providers == nil {
				providers = []client.ComputeProvider{}
			}
			return map[string]any{"items": providers}, err
		})

	add(s, toolSpec{name: "cloud_connections", description: "List the connections to clouds: which cloud each is for, what it is for (Compute makes machines), whether the cloud accepts its credentials, and the pools and machines that depend on it. " +
		"Credentials are never returned. This tool only reads: connections are made, changed and removed by a person.", level: readAccess, workspaceOnly: true, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (any, error) {
			connections, err := api.CloudConnections(ctx)
			if connections == nil {
				connections = []client.CloudConnection{}
			}
			return map[string]any{"items": connections}, err
		})

	add(s, toolSpec{name: "node_pools", description: "List the pools of nodes: machines that Geass makes at a cloud as the load needs them. For each, its connection and size, the fewest and most nodes it may have (max_nodes caps what it can cost), how many it has and how many are ready, whether it autoscales, how it is doing, " +
		"and its estimated cost for an hour. node_pool shows one with its nodes.", level: readAccess, workspaceOnly: true, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (any, error) {
			pools, err := api.NodePools(ctx)
			if pools == nil {
				pools = []client.NodePool{}
			}
			return map[string]any{"items": pools}, err
		})

	add(s, toolSpec{name: "node_pool", description: "Show one pool of nodes: its settings, how it is doing (phase, message, pods waiting for room), what it costs for an hour, whether it paused after repeated failures and why, " +
		"machines at the cloud that no node owns, and each of its nodes.", level: readAccess, workspaceOnly: true, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in poolNameInput) (any, error) {
			return api.NodePool(ctx, in.Name)
		})

	add(s, toolSpec{name: "cloud_sizes", description: "List the machine sizes a cloud offers in a region, cheapest first, with processors, memory and the price for an hour at the cloud's list price. The names are what create_node_pool takes as size. " +
		"It asks the cloud with a connection's credentials, so it needs an admin's token; it changes nothing.", level: adminReadAccess, workspaceOnly: true, readOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in cloudSizesInput) (any, error) {
			if strings.TrimSpace(in.Connection) == "" {
				return nil, fmt.Errorf("name a connection: cloud_connections lists them")
			}
			sizes, err := api.CloudSizes(ctx, in.Connection, in.Region)
			if err != nil {
				return nil, err
			}
			limit := in.Limit
			switch {
			case limit <= 0:
				limit = defaultSizesShown
			case limit > maxSizesShown:
				limit = maxSizesShown
			}
			chosen, matched := client.SelectSizes(sizes, in.MinVCPUs, int64(in.MinMemoryMiB), limit)
			out := map[string]any{"items": chosen, "matched": matched}
			if matched > len(chosen) {
				out["note"] = fmt.Sprintf("showing the %d cheapest of %d: narrow it with min_vcpus and min_memory_mib, or raise limit", len(chosen), matched)
			}
			return out, nil
		})

	add(s, toolSpec{name: "create_node_pool", description: "Make a pool of nodes: Geass creates machines at the cloud of a connection when pods cannot start for lack of room, and removes them when they sit idle. " +
		"THIS CREATES MACHINES THAT COST MONEY at the cloud, billed to the connection's account, until they are removed. max_nodes is required and is the most machines the pool can ever have: it is the cap on what the pool can cost, so choose it deliberately and ask the person if unsure. " +
		"min_nodes machines are made at once. If max_nodes is above min_nodes you must say whether the pool autoscales (autoscaling true or false): it is never assumed. " +
		"Check cloud_connections and cloud_sizes first. Nodes of a pool hold no databases or volumes unless storage is allow. The pool starts making machines within a moment; use node_pool to watch it. " +
		"This cannot make a pool of servers a person owns, and cannot create or change connections.", level: workspaceAdmin, workspaceOnly: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createPoolInput) (any, error) {
			if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Connection) == "" || strings.TrimSpace(in.Size) == "" {
				return nil, fmt.Errorf("name, connection and size are required")
			}
			if in.MaxNodes < 1 {
				return nil, fmt.Errorf("max_nodes is required and at least 1: it is the most nodes the pool can ever have, and the cap on what it can cost")
			}
			settings, err := in.settings()
			if err != nil {
				return nil, err
			}
			if settings.AutoscalingUnsaid() {
				return nil, fmt.Errorf("max_nodes %d is above min_nodes %d, so say whether the pool follows the load: autoscaling true adds nodes when pods cannot start and removes idle ones, false keeps exactly min_nodes", in.MaxNodes, in.MinNodes)
			}
			// The API names a pool in lowercase, so the pool is read back by the name it was given.
			name := strings.ToLower(strings.TrimSpace(in.Name))
			if err := api.CreateNodePool(ctx, name, settings); err != nil {
				return nil, err
			}
			out := map[string]any{
				"created": name, "connection": in.Connection, "maxNodes": in.MaxNodes, "minNodes": in.MinNodes,
				"note": fmt.Sprintf("machines made by this pool cost money until they are removed, and it never has more than %d", in.MaxNodes),
				"next": "call node_pool to see how it is going",
			}
			if pool, err := api.NodePool(ctx, name); err == nil {
				pool.Members = nil
				out["pool"] = pool
			}
			return out, nil
		})

	add(s, toolSpec{name: "update_node_pool", description: "Change a pool of nodes. Name only what to change; the rest stays as it is. " +
		"Raising max_nodes, min_nodes or spare_nodes, or choosing a larger size, raises what the pool can cost: min_nodes and spare_nodes make machines at once. " +
		"size applies to nodes made from now on, and the nodes it has are not changed. suspend true stops the pool adding and removing nodes without deleting any; autoscaling false keeps exactly min_nodes. " +
		"It returns the pool as it is afterwards.", level: workspaceAdmin, workspaceOnly: true, idempotent: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updatePoolInput) (any, error) {
			settings := client.PoolSettings{MinNodes: in.MinNodes, MaxNodes: in.MaxNodes, Autoscaling: in.Autoscaling, Suspend: in.Suspend, SpareNodes: in.SpareNodes}
			if in.MaxNodes != nil {
				if *in.MaxNodes < 1 {
					return nil, fmt.Errorf("max_nodes is from 1 to %d", maxAgentPoolNodes)
				}
				if *in.MaxNodes > maxAgentPoolNodes {
					return nil, errAboveAgentCeiling("max_nodes", maxAgentPoolNodes)
				}
			}
			if in.MinNodes != nil && (*in.MinNodes < 0 || *in.MinNodes > maxAgentMadeAtOnce) {
				return nil, errAboveAgentCeiling("min_nodes", maxAgentMadeAtOnce)
			}
			if in.SpareNodes != nil && (*in.SpareNodes < 0 || *in.SpareNodes > maxAgentSpareNodes) {
				return nil, errAboveAgentCeiling("spare_nodes", maxAgentSpareNodes)
			}
			if size := strings.TrimSpace(in.Size); size != "" {
				settings.Size = &size
			}
			if len(settings.Fields()) == 0 {
				return nil, fmt.Errorf("name at least one thing to change: max_nodes, min_nodes, autoscaling, suspend, size or spare_nodes")
			}
			if err := api.UpdateNodePool(ctx, in.Name, settings); err != nil {
				return nil, err
			}
			pool, err := api.NodePool(ctx, in.Name)
			if err != nil {
				return map[string]any{"updated": in.Name, "note": "saved, but the pool could not be read back (" + err.Error() + "): call node_pool"}, nil
			}
			pool.Members = nil
			return map[string]any{"updated": in.Name, "pool": pool}, nil
		})

	add(s, toolSpec{name: "resume_node_pool", description: "End the pause of a pool that stopped adding nodes after the cloud failed to make machines several times in a row. The pool forgets the failures and tries again, which makes machines and costs money, " +
		"so read node_pool first: its last failure says what went wrong, and a cause that is not fixed (an account limit, a bad setting, no capacity) fails again.", level: workspaceAdmin, workspaceOnly: true, idempotent: true},
		func(ctx context.Context, _ *mcp.CallToolRequest, in poolNameInput) (any, error) {
			if err := api.ResumeNodePool(ctx, in.Name); err != nil {
				return nil, err
			}
			return map[string]any{"resumed": in.Name, "next": "call node_pool to see whether it recovers"}, nil
		})

	if s.opts.AllowDelete {
		add(s, toolSpec{name: "delete_node_pool", description: "Permanently delete a pool of nodes, its nodes and their machines at the cloud. What runs on those nodes is moved if other nodes have room, and stops if not. " +
			"This cannot be undone: confirm must repeat the pool's name. To keep the machines but stop the pool adding and removing them, use update_node_pool with suspend true instead.", level: workspaceAdmin, workspaceOnly: true, destructive: true},
			func(ctx context.Context, _ *mcp.CallToolRequest, in deletePoolInput) (any, error) {
				if in.Confirm != in.Name {
					return nil, fmt.Errorf("not deleted: confirm must be exactly %q", in.Name)
				}
				return map[string]any{"deleted": in.Name, "note": "its machines are being deleted at the cloud"}, api.DeleteNodePool(ctx, in.Name)
			})
	}
}

// settings is what the agent asked for, as the settings a pool is made with. What was left out is not sent, so that the
// API's own defaults apply to it.
func (in createPoolInput) settings() (client.PoolSettings, error) {
	connection, size := strings.TrimSpace(in.Connection), strings.TrimSpace(in.Size)
	maxNodes, minNodes := in.MaxNodes, in.MinNodes
	if minNodes < 0 || minNodes > maxNodes {
		return client.PoolSettings{}, fmt.Errorf("min_nodes cannot be below 0 or above max_nodes")
	}
	switch {
	case maxNodes > maxAgentPoolNodes:
		return client.PoolSettings{}, errAboveAgentCeiling("max_nodes", maxAgentPoolNodes)
	case minNodes > maxAgentMadeAtOnce:
		return client.PoolSettings{}, errAboveAgentCeiling("min_nodes", maxAgentMadeAtOnce)
	case in.SpareNodes > maxAgentSpareNodes || in.SpareNodes < 0:
		return client.PoolSettings{}, errAboveAgentCeiling("spare_nodes", maxAgentSpareNodes)
	}
	settings := client.PoolSettings{Connection: &connection, Size: &size, MaxNodes: &maxNodes, MinNodes: &minNodes, Autoscaling: in.Autoscaling, Spot: in.Spot}
	text := func(target **string, value string) {
		if value = strings.TrimSpace(value); value != "" {
			*target = &value
		}
	}
	text(&settings.Region, in.Region)
	text(&settings.Image, in.Image)
	text(&settings.PlacementRegion, in.PlacementRegion)
	text(&settings.PlacementZone, in.PlacementZone)
	if storage := strings.TrimSpace(in.Storage); storage != "" {
		switch strings.ToLower(storage) {
		case "none":
			storage = "None"
		case "allow":
			storage = "Allow"
		default:
			return client.PoolSettings{}, fmt.Errorf("storage is none or allow")
		}
		settings.Storage = &storage
	}
	list := func(target **[]string, values []string) {
		if len(values) > 0 {
			*target = &values
		}
	}
	list(&settings.FallbackSizes, in.FallbackSizes)
	list(&settings.FallbackRegions, in.FallbackRegions)
	list(&settings.Tags, in.Tags)
	list(&settings.ReservedTags, in.ReservedTags)
	if len(in.Settings) > 0 {
		settings.Settings = in.Settings
	}
	if in.ScaleDownAfterMinutes != 0 {
		minutes := in.ScaleDownAfterMinutes
		settings.ScaleDownAfterMinutes = &minutes
	}
	if in.SpareNodes != 0 {
		spare := in.SpareNodes
		settings.SpareNodes = &spare
	}
	return settings, nil
}
