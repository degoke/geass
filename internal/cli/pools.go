package cli

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/degoke/geass/pkg/client"
)

// Pools of nodes: machines that Geass makes at a cloud (or joins from servers you own) as the load needs them, and
// removes when it goes. Every machine a pool makes costs money, so a pool cannot be made without a most.

func (a *App) poolsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pools",
		Aliases: []string{"pool"},
		Short:   "Pools of nodes that grow and shrink with the load",
		Long: "A pool makes machines at a cloud, or joins servers of yours, when pods cannot start for lack of room, and removes " +
			"them again when they sit idle. Each machine a pool makes costs money at the cloud until it is removed, and --max is " +
			"the most machines a pool can ever have: it is the limit on what the pool can cost.\n\n" +
			"A pool needs a connection to a cloud (see `geass connections`). Its nodes hold no databases or volumes unless you say " +
			"--storage allow, so that a node can go without taking data with it.\n\n" +
			"Making, changing and deleting a pool needs an admin's token.",
	}
	cmd.AddCommand(a.poolsListCmd(), a.poolsShowCmd(), a.poolsCreateCmd(), a.poolsUpdateCmd(), a.poolsDeleteCmd(), a.poolsResumeCmd())
	return cmd
}

func poolSource(p client.NodePool) string {
	if p.IsInventory() {
		return fmt.Sprintf("inventory (%d)", len(p.Inventory))
	}
	if p.Provider != "" {
		return p.Connection + " (" + p.Provider + ")"
	}
	return p.Connection
}

func poolAutoscaling(p client.NodePool) string {
	switch {
	case p.Suspended:
		return "suspended"
	case p.Autoscaling != nil && p.Autoscaling.Enabled:
		return "on"
	default:
		return "off"
	}
}

func (a *App) poolsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the pools with how many nodes each has and what it costs for an hour",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			pools, err := api.NodePools(cmd.Context())
			if err != nil {
				return err
			}
			if a.flags.json {
				if pools == nil {
					pools = []client.NodePool{}
				}
				return a.printItems(pools)
			}
			rows := make([][]string, 0, len(pools))
			for _, p := range pools {
				rows = append(rows, []string{
					p.Name, poolSource(p), dash(p.Size), fmt.Sprintf("%d/%d", p.ReadyNodes, p.NodeCount), fmt.Sprintf("%d-%d", p.MinNodes, p.MaxNodes),
					poolAutoscaling(p), p.Phase, dash(p.EstimatedHourlyCost), age(p.CreatedAt),
				})
			}
			a.printTable([]string{columnName, "SOURCE", "SIZE", "READY/NODES", "MIN-MAX", "AUTOSCALING", "PHASE", "PER HOUR", "AGE"}, rows)
			return nil
		},
	}
}

func (a *App) poolsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show NAME",
		Short: "Show a pool: its settings, what it is doing, what it costs and its nodes",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			pool, err := api.NodePool(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printJSON(pool)
			}
			a.printPool(pool)
			return nil
		},
	}
}

func (a *App) printPool(p client.NodePool) {
	a.kv("Name", p.Name)
	a.kv("Phase", p.Phase)
	if p.Message != "" {
		a.kv("Message", p.Message)
	}
	a.kv("Source", poolSource(p))
	if p.IsInventory() {
		a.kv("Servers", dash(strings.Join(p.Inventory, ", ")))
	} else {
		a.kv("Region", withFallbacks(p.Region, p.FallbackRegions))
		a.kv("Size", withFallbacks(p.Size, p.FallbackSizes))
		if p.ActiveSize != nil && p.ActiveSize.Name != "" && p.ActiveSize.Name != p.Size {
			a.kv("Size in use", describePoolSize(*p.ActiveSize)+" (a fallback: the first choice could not be made)")
		} else if p.ActiveSize != nil && p.ActiveSize.VCPUs > 0 {
			a.kv("Size details", describePoolSize(*p.ActiveSize))
		}
		a.kv("Image", dash(p.Image))
		a.kv("Spot machines", yesNo(p.Spot))
		a.kv("Address", dash(p.AddressType))
		if len(p.Settings) > 0 {
			keys := make([]string, 0, len(p.Settings))
			for key := range p.Settings {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for i, key := range keys {
				label := ""
				if i == 0 {
					label = "Cloud settings"
				}
				a.kv(label, key+"="+p.Settings[key])
			}
		}
	}
	a.kv("Storage", describePoolStorage(p.Storage))
	a.kv("Nodes", fmt.Sprintf("%d (%d ready, %d wanted), from %d at least to %d at most", p.NodeCount, p.ReadyNodes, p.DesiredNodes, p.MinNodes, p.MaxNodes))
	a.kv("Pods waiting for room", strconv.Itoa(int(p.PendingPods)))
	a.kv("Autoscaling", describeScaling(p))
	a.kv("Estimated cost", describeCost(p))
	if p.Placement != nil {
		a.kv("Placement", describePlacement(*p.Placement))
	}
	a.kv("Unowned machines", describeOrphans(p))
	if p.PausedUntil != nil {
		a.kv("Paused until", p.PausedUntil.UTC().Format("2006-01-02 15:04 UTC"))
	}
	if p.ConsecutiveFailures > 0 {
		a.kv("Failures in a row", strconv.Itoa(int(p.ConsecutiveFailures)))
	}
	if p.LastFailure != "" {
		a.kv("Last failure", p.LastFailure)
	}
	if p.LastScaleUp != nil {
		a.kv("Last node added", age(*p.LastScaleUp)+" ago")
	}
	if p.LastScaleDown != nil {
		a.kv("Last node removed", age(*p.LastScaleDown)+" ago")
	}
	a.kv("Created", age(p.CreatedAt)+" ago")

	if p.PausedUntil != nil || p.Phase == "Paused" {
		a.printf("\nThe pool stopped adding machines after repeated failures. Fix what failed (see the last failure), then run `geass pools resume %s`.\n", p.Name)
	}
	if len(p.Orphans) > 0 {
		a.printf("\nMachines at the cloud that belong to this pool but to no node, and may be costing money: %s\n", strings.Join(p.Orphans, ", "))
	}
	if len(p.Members) == 0 {
		a.printf("\nThe pool has no nodes.\n")
		return
	}
	a.printf("\n")
	rows := make([][]string, 0, len(p.Members))
	for _, node := range p.Members {
		phase := node.Phase
		if node.Deleting {
			phase = "Deleting"
		}
		rows = append(rows, []string{node.Name, phase, dash(node.Address), dash(node.Size), dash(node.Region), age(node.CreatedAt), dash(node.Message)})
	}
	a.printTable([]string{"NODE", "PHASE", "ADDRESS", "SIZE", "REGION", "AGE", "MESSAGE"}, rows)
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func withFallbacks(first string, fallbacks []string) string {
	text := dash(first)
	if len(fallbacks) > 0 {
		text += " (then " + strings.Join(fallbacks, ", ") + " when there is no room)"
	}
	return text
}

func describePoolSize(size client.NodePoolSize) string {
	parts := []string{size.Name}
	if size.VCPUs > 0 {
		parts = append(parts, fmt.Sprintf("%d vCPU", size.VCPUs))
	}
	if size.MemoryMiB > 0 {
		parts = append(parts, formatMemory(size.MemoryMiB))
	}
	if size.HourlyPrice != "" {
		parts = append(parts, strings.TrimSpace(size.HourlyPrice+" "+size.Currency)+" per hour")
	}
	return strings.Join(parts, ", ")
}

func describePoolStorage(storage string) string {
	if strings.EqualFold(storage, "Allow") {
		return "Allow (databases and volumes may be placed on its nodes)"
	}
	return "None (no databases or volumes on its nodes, so a node can be removed freely)"
}

func describeScaling(p client.NodePool) string {
	if p.Suspended {
		return "suspended: no nodes are added or removed"
	}
	if p.Autoscaling == nil || !p.Autoscaling.Enabled {
		return fmt.Sprintf("off: the pool has exactly %d nodes", p.MinNodes)
	}
	s := p.Autoscaling
	text := fmt.Sprintf("on: adds a node after pods wait %ds (up to %d at once); removes one used under %d%% for %d min", s.ScaleUpDelaySeconds, s.MaxScaleUp, s.ScaleDownUtilizationPercent, s.ScaleDownAfterMinutes)
	if s.SpareNodes > 0 {
		text += fmt.Sprintf("; keeps %d empty ready", s.SpareNodes)
	}
	return text
}

func describeCost(p client.NodePool) string {
	if p.EstimatedHourlyCost == "" {
		return "not known for this pool's cloud"
	}
	return p.EstimatedHourlyCost + " per hour for its machines now, at the cloud's list price"
}

func describePlacement(p client.NodePoolPlacement) string {
	var parts []string
	if p.Region != "" {
		parts = append(parts, "region "+p.Region)
	}
	if p.Zone != "" {
		parts = append(parts, "zone "+p.Zone)
	}
	if len(p.Tags) > 0 {
		parts = append(parts, "tags "+strings.Join(p.Tags, ", "))
	}
	return dash(strings.Join(parts, "; "))
}

func describeOrphans(p client.NodePool) string {
	switch {
	case len(p.Orphans) > 0:
		return fmt.Sprintf("%d found (%s)", len(p.Orphans), p.OrphanPolicy)
	case strings.EqualFold(p.OrphanPolicy, "Delete"):
		return "none; machines at the cloud that no node owns are deleted"
	default:
		return "none; machines at the cloud that no node owns are reported"
	}
}

// ---- making and changing pools ----

// switchFlag is a setting that is on or off, given as --name or --no-name, so that leaving it out means leaving it as it is.
type switchFlag struct {
	name    string
	on, off bool
}

func addSwitch(cmd *cobra.Command, name, onHelp, offHelp string) *switchFlag {
	s := &switchFlag{name: name}
	cmd.Flags().BoolVar(&s.on, name, false, onHelp)
	cmd.Flags().BoolVar(&s.off, "no-"+name, false, offHelp)
	return s
}

// value is what the person said about the switch, or nil when they said nothing.
func (s *switchFlag) value(cmd *cobra.Command) (*bool, error) {
	var result *bool
	if cmd.Flags().Changed(s.name) {
		v := s.on
		result = &v
	}
	if cmd.Flags().Changed("no-" + s.name) {
		v := !s.off
		if result != nil && *result != v {
			return nil, usageError("--%s and --no-%s say opposite things", s.name, s.name)
		}
		result = &v
	}
	return result, nil
}

// poolFlags are the settings of a pool, as flags. Both making and changing a pool take them.
type poolFlags struct {
	connection, region, size, image, addressType, storage, orphans string
	fallbackSizes, fallbackRegions, tags, reservedTags             []string
	placementRegion, placementZone                                 string
	settings                                                       []string
	inventoryFile, inventoryKeyFile                                string
	minNodes, maxNodes                                             int

	scaleUpDelay, maxScaleUp, scaleDownUtilization, scaleDownAfter, scaleDownDelayAfterAdd, minNodeLifetime, spareNodes int

	autoscale, spot, suspend *switchFlag
}

func (f *poolFlags) add(cmd *cobra.Command, creating bool) {
	fl := cmd.Flags()
	if creating {
		fl.StringVar(&f.connection, "connection", "", "the cloud connection that makes the machines (see: geass connections list)")
		fl.StringVar(&f.inventoryFile, "inventory-file", "", "instead of a cloud: a JSON file listing servers of yours, [{\"name\", \"address\", \"sshUser\", \"sshPort\"?, \"sshKeySecret\"?}]; PATH - is standard input")
	} else {
		fl.StringVar(&f.connection, "connection", "", "make machines with this other connection from now on (a pool cannot change between a cloud and servers of yours)")
		fl.StringVar(&f.inventoryFile, "inventory-file", "", "replace the pool's servers with the ones listed in this JSON file; PATH - is standard input")
	}
	fl.StringVar(&f.inventoryKeyFile, "inventory-key-file", "", "the SSH private key of the servers in --inventory-file, read from this file and stored as a Secret (PATH - is standard input)")
	fl.StringVar(&f.region, "region", "", "the cloud's own region or location (see: geass providers CLOUD)")
	fl.StringVar(&f.size, "size", "", "the cloud's name for a machine size, such as cx23 or t3.medium (see: geass sizes)")
	fl.StringSliceVar(&f.fallbackSizes, "fallback-size", nil, "a size to try, in order, when the cloud has no room for --size; repeatable; empty clears them")
	fl.StringSliceVar(&f.fallbackRegions, "fallback-region", nil, "a region to try, in order, when the cloud has no room in --region; repeatable; empty clears them")
	fl.StringVar(&f.image, "image", "", "the operating system image (default: the cloud's Ubuntu 24.04)")
	f.spot = addSwitch(cmd, "spot", "use the cloud's cheaper spot machines, which it may take back (a node taken back is replaced)", "use ordinary machines")
	fl.StringVar(&f.addressType, "address-type", "", "which address of a machine Geass reaches it on: public (the default) or private")
	fl.StringArrayVar(&f.settings, "setting", nil, "a setting of the cloud, KEY=VALUE (a subnet, a firewall; repeatable). On a change, the ones you do not name keep their values, and KEY= removes one")
	fl.StringVar(&f.storage, "storage", "", "none (the default: no databases or volumes on these nodes, so one can be removed freely) or allow")
	fl.IntVar(&f.minNodes, "min", 0, "the fewest nodes the pool has; a pool that does not autoscale has exactly this many")
	fl.IntVar(&f.maxNodes, "max", 0, "the most nodes the pool ever has: the limit on what it can cost (required when making a pool)")
	f.autoscale = addSwitch(cmd, "autoscale", "add nodes when pods cannot start for lack of room, and remove idle ones, between --min and --max", "keep exactly --min nodes")
	f.suspend = addSwitch(cmd, "suspend", "stop adding and removing nodes; the ones the pool has stay", "let the pool add and remove nodes again")
	fl.IntVar(&f.scaleUpDelay, "scale-up-delay", 0, "seconds pods must be unable to start before a node is added (default 30)")
	fl.IntVar(&f.maxScaleUp, "max-scale-up", 0, "the most nodes added in one step (default 5)")
	fl.IntVar(&f.scaleDownUtilization, "scale-down-utilization", 0, "a node used less than this percent is a candidate for removal (default 50)")
	fl.IntVar(&f.scaleDownAfter, "scale-down-after", 0, "minutes a node must be a candidate before it is removed (default 10)")
	fl.IntVar(&f.scaleDownDelayAfterAdd, "scale-down-delay-after-add", 0, "no node is removed for this many minutes after one was added (default 10)")
	fl.IntVar(&f.minNodeLifetime, "min-node-lifetime", 0, "minutes a node lives at least, for clouds that bill each started hour")
	fl.IntVar(&f.spareNodes, "spare-nodes", 0, "empty nodes kept ready for a burst; each one costs money while it waits")
	fl.StringVar(&f.placementRegion, "placement-region", "", "the region the nodes carry, for placing projects (see: geass nodes tag)")
	fl.StringVar(&f.placementZone, "placement-zone", "", "the site the nodes carry")
	fl.StringSliceVar(&f.tags, "tag", nil, "a node tag the nodes carry; repeatable")
	fl.StringSliceVar(&f.reservedTags, "reserved-tag", nil, "a node tag that also keeps all other work off the nodes; repeatable")
	fl.StringVar(&f.orphans, "orphans", "", "what to do with a machine at the cloud that no node owns: report (the default) or delete")
}

// normalizeChoice spells one of a few allowed words the way the API does.
func normalizeChoice(flag, value string, choices ...string) (string, error) {
	for _, choice := range choices {
		if strings.EqualFold(strings.TrimSpace(value), choice) {
			return choice, nil
		}
	}
	lower := make([]string, 0, len(choices))
	for _, choice := range choices {
		lower = append(lower, strings.ToLower(choice))
	}
	return "", usageError("--%s is %s, not %q", flag, strings.Join(lower, " or "), value)
}

// readFileOrStdin reads a path, or standard input for -, and is told when standard input has already been used.
func (a *App) readFileOrStdin(path string, stdinUsed *bool) ([]byte, error) {
	if path != "-" {
		return os.ReadFile(path)
	}
	if *stdinUsed {
		return nil, usageError("only one file can be read from standard input")
	}
	*stdinUsed = true
	return io.ReadAll(a.In)
}

// parseSettings reads KEY=VALUE pairs. A pair with no value is kept, as an empty value, so that a change can remove it.
func parseSettings(pairs []string) (map[string]string, error) {
	settings := map[string]string{}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if key = strings.TrimSpace(key); !ok || key == "" {
			return nil, usageError("a --setting is written KEY=VALUE, such as firewallId=42, not %q", pair)
		}
		settings[key] = strings.TrimSpace(value)
	}
	return settings, nil
}

// poolSettings turns the flags that were given into the settings to send. current is the pool as it is, for a change
// that has to send more than was given; it is nil when a pool is being made.
func (a *App) poolSettings(cmd *cobra.Command, f *poolFlags, current *client.NodePool) (client.PoolSettings, error) {
	var s client.PoolSettings
	changed := cmd.Flags().Changed
	text := func(flag string, target **string, value string) {
		if changed(flag) {
			v := strings.TrimSpace(value)
			*target = &v
		}
	}
	number := func(flag string, target **int, value int) {
		if changed(flag) {
			v := value
			*target = &v
		}
	}
	list := func(flag string, target **[]string, values []string) {
		if changed(flag) {
			v := slices.DeleteFunc(slices.Clone(values), func(item string) bool { return strings.TrimSpace(item) == "" })
			*target = &v
		}
	}
	text("connection", &s.Connection, f.connection)
	text("region", &s.Region, f.region)
	text("size", &s.Size, f.size)
	text("image", &s.Image, f.image)
	list("fallback-size", &s.FallbackSizes, f.fallbackSizes)
	list("fallback-region", &s.FallbackRegions, f.fallbackRegions)
	number("min", &s.MinNodes, f.minNodes)
	number("max", &s.MaxNodes, f.maxNodes)
	number("scale-up-delay", &s.ScaleUpDelaySeconds, f.scaleUpDelay)
	number("max-scale-up", &s.MaxScaleUp, f.maxScaleUp)
	number("scale-down-utilization", &s.ScaleDownUtilizationPercent, f.scaleDownUtilization)
	number("scale-down-after", &s.ScaleDownAfterMinutes, f.scaleDownAfter)
	number("scale-down-delay-after-add", &s.ScaleDownDelayAfterAddMinutes, f.scaleDownDelayAfterAdd)
	number("min-node-lifetime", &s.MinNodeLifetimeMinutes, f.minNodeLifetime)
	number("spare-nodes", &s.SpareNodes, f.spareNodes)

	var err error
	if s.Autoscaling, err = f.autoscale.value(cmd); err != nil {
		return s, err
	}
	if s.Spot, err = f.spot.value(cmd); err != nil {
		return s, err
	}
	if s.Suspend, err = f.suspend.value(cmd); err != nil {
		return s, err
	}
	for _, choice := range []struct {
		flag    string
		value   string
		target  **string
		allowed []string
	}{
		{"address-type", f.addressType, &s.AddressType, []string{"Public", "Private"}},
		{"storage", f.storage, &s.Storage, []string{"None", "Allow"}},
		{"orphans", f.orphans, &s.OrphanPolicy, []string{"Report", "Delete"}},
	} {
		if !changed(choice.flag) {
			continue
		}
		word, err := normalizeChoice(choice.flag, choice.value, choice.allowed...)
		if err != nil {
			return s, err
		}
		*choice.target = &word
	}

	if changed("setting") {
		given, err := parseSettings(f.settings)
		if err != nil {
			return s, err
		}
		merged := map[string]string{}
		if current != nil {
			maps.Copy(merged, current.Settings)
		}
		for key, value := range given {
			if value == "" {
				delete(merged, key)
			} else {
				merged[key] = value
			}
		}
		s.Settings = merged
	}

	if err := a.applyPlacementFlags(cmd, f, current, &s); err != nil {
		return s, err
	}

	stdinUsed := false
	if changed("inventory-file") {
		raw, err := a.readFileOrStdin(f.inventoryFile, &stdinUsed)
		if err != nil {
			return s, fmt.Errorf("could not read the inventory: %w", err)
		}
		servers, err := client.ParseInventory(raw)
		if err != nil {
			return s, usageError("%v", err)
		}
		s.Inventory = &servers
	}
	if changed("inventory-key-file") {
		if s.Inventory == nil {
			return s, usageError("--inventory-key-file goes with --inventory-file: the key is stored together with the servers it opens")
		}
		raw, err := a.readFileOrStdin(f.inventoryKeyFile, &stdinUsed)
		if err != nil {
			return s, fmt.Errorf("could not read the SSH key: %w", err)
		}
		key := strings.TrimSpace(string(raw))
		if key == "" {
			return s, usageError("the SSH key file is empty")
		}
		s.InventoryKey = &key
	}
	return s, nil
}

// applyPlacementFlags sets the placement the nodes carry. The API replaces the region, the site and the tags together
// when it is sent any of them, so a change that names only some must send the rest as they are. The tags it reads back
// do not say which of them are reserved, so a pool that has tags is never sent a change to its region or site alone: a
// reserved tag would silently become an ordinary one and let other work onto nodes that were kept clear of it.
func (a *App) applyPlacementFlags(cmd *cobra.Command, f *poolFlags, current *client.NodePool, s *client.PoolSettings) error {
	changed := cmd.Flags().Changed
	tagsGiven := changed("tag") || changed("reserved-tag")
	if !changed("placement-region") && !changed("placement-zone") && !tagsGiven {
		return nil
	}
	region, zone := strings.TrimSpace(f.placementRegion), strings.TrimSpace(f.placementZone)
	tags, reserved := slices.Clone(f.tags), slices.Clone(f.reservedTags)
	if current != nil && current.Placement != nil {
		if !changed("placement-region") {
			region = current.Placement.Region
		}
		if !changed("placement-zone") {
			zone = current.Placement.Zone
		}
		if !tagsGiven && len(current.Placement.Tags) > 0 {
			return usageError("this pool's nodes carry the tags %s, and a change to the placement replaces its region, zone and tags together. "+
				"Name every tag the nodes should carry: --tag for the ones that are shared, and --reserved-tag for the ones that keep all other work off the nodes",
				strings.Join(current.Placement.Tags, ", "))
		}
	}
	clean := func(values []string) []string {
		return slices.DeleteFunc(values, func(item string) bool { return strings.TrimSpace(item) == "" })
	}
	tags, reserved = clean(tags), clean(reserved)
	s.PlacementRegion, s.PlacementZone, s.Tags, s.ReservedTags = &region, &zone, &tags, &reserved
	return nil
}

func (a *App) poolsCreateCmd() *cobra.Command {
	flags := &poolFlags{}
	cmd := &cobra.Command{
		Use:   "create NAME --max N (--connection CLOUD --size SIZE | --inventory-file FILE)",
		Short: "Make a pool of nodes that Geass adds and removes as the load needs",
		Long: "Make a pool. Machines are created at the cloud of a connection, or servers from an inventory you list are joined, " +
			"when pods cannot start for lack of room, and removed again when they sit idle.\n\n" +
			"Every machine a pool makes costs money at the cloud until it is removed. --max is the most machines the pool can ever " +
			"have, so it is the most the pool can cost, and it is required. With room to grow, say whether the pool should follow " +
			"the load (--autoscale) or keep exactly --min nodes (--no-autoscale): Geass does not choose that for you.\n\n" +
			"Nodes of a pool hold no databases or volumes unless --storage allow is given, so a node can be removed without losing data.",
		Example: "  geass pools create burst --connection hetzner --region fsn1 --size cx23 --fallback-size cx33 --min 0 --max 5 --autoscale\n" +
			"  geass pools create fixed --connection hetzner --size cx23 --min 2 --max 2\n" +
			"  geass pools create spare --inventory-file ./servers.json --inventory-key-file ~/.ssh/geass_ed25519 --max 3 --autoscale",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("max") {
				return usageError("give --max: the most nodes the pool can ever have, which is the limit on what it can cost")
			}
			switch {
			case changed("connection") && changed("inventory-file"):
				return usageError("a pool makes machines at a cloud (--connection) or joins servers of yours (--inventory-file), not both")
			case !changed("connection") && !changed("inventory-file"):
				return usageError("say where the nodes come from: --connection (a cloud, see `geass connections list`) or --inventory-file (servers of yours)")
			case changed("connection") && !changed("size"):
				return usageError("a pool that makes machines at a cloud needs --size (see: geass sizes --connection %s)", flags.connection)
			}
			settings, err := a.poolSettings(cmd, flags, nil)
			if err != nil {
				return err
			}
			if settings.AutoscalingUnsaid() {
				return usageError("--max %d is above --min %d, so say whether the pool follows the load: --autoscale adds nodes when pods cannot start and removes idle ones, --no-autoscale keeps exactly --min nodes",
					*settings.MaxNodes, valueOr(settings.MinNodes, 0))
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			if err := api.CreateNodePool(cmd.Context(), args[0], settings); err != nil {
				return err
			}
			a.printf("Created the pool %s.\n", args[0])
			if settings.Connection != nil {
				a.printf("It makes machines at %s as the load needs them, never more than %d, and each one costs money until it is removed.\n", *settings.Connection, *settings.MaxNodes)
			} else {
				a.printf("It joins servers from its inventory as the load needs them, never more than %d.\n", *settings.MaxNodes)
			}
			a.printf("Watch it with `geass pools show %s`.\n", args[0])
			return nil
		},
	}
	flags.add(cmd, true)
	return cmd
}

func valueOr(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func (a *App) poolsUpdateCmd() *cobra.Command {
	flags := &poolFlags{}
	cmd := &cobra.Command{
		Use:   "update NAME",
		Short: "Change a pool; what you do not name stays as it is",
		Long: "Change a pool. Only the flags you give are sent. The size, region, image and settings apply to machines made from " +
			"then on: the nodes the pool has are left as they are.\n\n" +
			"Raising --max, --min or --spare-nodes, or choosing a larger --size, raises what the pool can cost. A pool changes " +
			"its region, site and tags together: if you name any of --placement-region, --placement-zone, --tag or --reserved-tag, " +
			"the rest are kept except the tags, which you must then name in full.",
		Example: "  geass pools update burst --max 8\n  geass pools update burst --suspend\n  geass pools update burst --no-autoscale --min 2 --max 2\n  geass pools update burst --setting firewallId=77 --fallback-size ''",
		Args:    usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			// What a change has to send beyond what was given is read first, but only when something needs it.
			var current *client.NodePool
			if cmd.Flags().Changed("setting") || cmd.Flags().Changed("placement-region") || cmd.Flags().Changed("placement-zone") ||
				cmd.Flags().Changed("tag") || cmd.Flags().Changed("reserved-tag") {
				pool, err := api.NodePool(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				current = &pool
			}
			settings, err := a.poolSettings(cmd, flags, current)
			if err != nil {
				return err
			}
			if len(settings.Fields()) == 0 {
				return usageError("name something to change, such as --max 8, --size cx33 or --suspend (see: geass pools update --help)")
			}
			if err := api.UpdateNodePool(cmd.Context(), args[0], settings); err != nil {
				return err
			}
			a.printf("Updated the pool %s.\n", args[0])
			if settings.Size != nil || settings.Region != nil || settings.Image != nil || settings.Settings != nil || settings.Spot != nil ||
				settings.AddressType != nil || settings.FallbackSizes != nil || settings.FallbackRegions != nil || settings.Connection != nil {
				a.printf("The size, region, image and settings apply to machines made from now on: the nodes it has are left as they are.\n")
			}
			return nil
		},
	}
	flags.add(cmd, false)
	return cmd
}

func (a *App) poolsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete NAME",
		Aliases: []string{"rm"},
		Short:   "Delete a pool, its nodes and their machines at the cloud",
		Long: "Delete a pool. Its nodes are removed from the cluster and their machines are deleted at the cloud, so what runs on them " +
			"is moved to other nodes if they have room, and stops if not.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.confirm(fmt.Sprintf("Delete the pool %q? This removes its nodes and deletes their machines at the cloud.", args[0])); err != nil {
				return err
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			if err := api.DeleteNodePool(cmd.Context(), args[0]); err != nil {
				return err
			}
			a.printf("Deleted the pool %s. Its machines are being deleted at the cloud: check `geass nodes list` and the cloud's own console.\n", args[0])
			return nil
		},
	}
}

func (a *App) poolsResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume NAME",
		Short: "End the pause of a pool that stopped after repeated failures",
		Long: "A pool that fails to make a machine several times in a row stops adding nodes and waits. Fix what failed (the pool's last " +
			"failure says what), then resume it: it forgets the failures and tries to make machines again, which costs money.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			if err := api.ResumeNodePool(cmd.Context(), args[0]); err != nil {
				return err
			}
			a.printf("Resumed the pool %s: it tries to make machines again. Watch it with `geass pools show %s`.\n", args[0], args[0])
			return nil
		},
	}
}
