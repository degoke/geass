package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/degoke/geass/pkg/client"
)

// kindTable prints resources of any kind the same way.
func (a *App) printResources(items []client.Resource) error {
	if a.flags.json {
		if items == nil {
			items = []client.Resource{}
		}
		return a.printJSON(map[string]any{"items": items})
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, []string{
			item.Name, dash(item.Project), dash(item.Environment), item.State, dash(summary(item)), age(item.CreatedAt),
		})
	}
	a.printTable([]string{columnName, "PROJECT", "ENV", "STATE", "DETAIL", "AGE"}, rows)
	return nil
}

// summary is the one fact about a resource worth a column.
func summary(item client.Resource) string {
	if item.URL != "" {
		return item.URL
	}
	details := item.Details
	switch item.Kind {
	case "app":
		for _, key := range []string{"image", "repository", "runtime"} {
			if value, ok := details[key].(string); ok && value != "" {
				return value
			}
		}
	case "database":
		if engine, ok := details["engine"].(string); ok {
			return engine
		}
	case "logical-database":
		if database, ok := details["database"].(string); ok {
			return database
		}
	case "project":
		if environments, ok := details["environments"].([]any); ok {
			names := make([]string, 0, len(environments))
			for _, environment := range environments {
				names = append(names, fmt.Sprint(environment))
			}
			return strings.Join(names, ",")
		}
	}
	return item.Message
}

// listCmd lists resources of a kind, narrowed by --project and --environment.
func (a *App) listCmd(kind, use string, aliases ...string) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Aliases: aliases,
		Short:   "List " + strings.ReplaceAll(kind, "-", " "),
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, resolved, err := a.api()
			if err != nil {
				return err
			}
			filter := client.Filter{}
			// The saved default narrows what is listed only when the person asked for a project: a list of
			// everything is what `list` with nothing says means.
			if a.flags.project != "" || a.flags.environment != "" {
				filter = client.Filter{Project: resolved.Project, Environment: resolved.Environment}
			}
			items, err := api.List(cmd.Context(), kind, filter)
			if err != nil {
				return err
			}
			return a.printResources(items)
		},
	}
}

// getCmd shows one resource with its settings.
func (a *App) getCmd(kind, use string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: "Show one " + strings.TrimSuffix(strings.ReplaceAll(kind, "-", " "), "s") + " with its settings and status",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			detail, err := api.Get(cmd.Context(), kind, args[0])
			if err != nil {
				return err
			}
			return a.printJSON(detail)
		},
	}
}

func (a *App) projectsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "projects", Aliases: []string{"project"}, Short: "List and inspect projects"}
	cmd.AddCommand(a.listCmd("projects", "list", "ls"), a.getCmd("projects", "get NAME"), a.projectQuotaCmd(), a.projectNetworkCmd())
	return cmd
}

func (a *App) databasesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "databases", Aliases: []string{"database", "dbs", "db"}, Short: "List databases and how to connect to them"}
	cmd.AddCommand(a.listCmd("databases", "list", "ls"), a.getCmd("databases", "get NAME"))
	var reveal bool
	connect := &cobra.Command{
		Use:   "connection NAME",
		Short: "Show how to connect to a database (add --reveal for the password; admins only)",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			out, err := api.Connection(cmd.Context(), "databases", args[0], reveal)
			if err != nil {
				return err
			}
			if a.flags.json || reveal {
				return a.printJSON(out)
			}
			keys := make([]string, 0, len(out))
			for key := range out {
				if key != "keys" && key != "revealed" {
					keys = append(keys, key)
				}
			}
			slices.Sort(keys)
			for _, key := range keys {
				a.printf("%-10s %v\n", key, out[key])
			}
			a.note("The password and connection string are hidden. Add --reveal to show them.")
			return nil
		},
	}
	connect.Flags().BoolVar(&reveal, "reveal", false, "include the password and the connection string")
	cmd.AddCommand(connect)
	return cmd
}

func (a *App) storesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "stores", Aliases: []string{"buckets", "object-stores"}, Short: "List object storage buckets"}
	cmd.AddCommand(a.listCmd("object-stores", "list", "ls"), a.getCmd("object-stores", "get NAME"))
	return cmd
}

func (a *App) nodesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "nodes", Aliases: []string{"node"}, Short: "List the cluster's nodes, tag them, and cordon them"}
	cmd.AddCommand(&cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List nodes with their region, zone and tags, and the pool each belongs to",
		Long: "List the cluster's nodes. POOL is the pool a node belongs to (see `geass pools`), and EPHEMERAL says the node must " +
			"not hold data: databases and volumes stay off it, and it may be removed when the load goes. A server that has not joined yet, " +
			"or failed to, has no node: it is listed with its phase, and its reason is printed under the table.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			nodes, err := api.Nodes(cmd.Context())
			if err != nil {
				return err
			}
			// An operator that is older than this command line has no list of servers: the nodes are still listed.
			servers, err := api.Servers(cmd.Context())
			if err != nil && !client.IsNotFound(err) {
				return err
			}
			var notJoined []client.Server
			for _, server := range servers {
				if !server.Joined {
					notJoined = append(notJoined, server)
				}
			}
			if a.flags.json {
				return a.printJSON(map[string]any{"items": nodes, "servers": notJoined})
			}
			rows := make([][]string, 0, len(nodes)+len(notJoined))
			for _, node := range nodes {
				status := "Ready"
				switch {
				case !node.Ready:
					status = "NotReady"
				case node.Cordoned:
					status = "Ready, cordoned"
				}
				tags := make([]string, 0, len(node.Tags))
				for _, tag := range node.Tags {
					switch {
					case tag.Compliance:
						tags = append(tags, tag.Name+" (compliance)")
					case tag.Reserved:
						tags = append(tags, tag.Name+" (reserved)")
					default:
						tags = append(tags, tag.Name)
					}
				}
				ephemeral := "-"
				if node.Ephemeral {
					ephemeral = "yes"
				}
				rows = append(rows, []string{node.Name, status, dash(node.Region), dash(node.Zone), dash(strings.Join(tags, ", ")), dash(node.CPU), dash(node.Memory), dash(node.Pool), ephemeral})
			}
			for _, server := range notJoined {
				rows = append(rows, []string{server.Name, server.Phase, "-", "-", "-", "-", "-", dash(server.Pool), "-"})
			}
			a.printTable([]string{"NAME", "STATUS", "REGION", "ZONE", "TAGS", "CPU", "MEMORY", "POOL", "EPHEMERAL"}, rows)
			a.printServerProblems(notJoined)
			return nil
		},
	})
	cmd.AddCommand(a.nodeCordonCmd(true), a.nodeCordonCmd(false))

	var region, zone string
	var tags, reserved, compliance []string
	tag := &cobra.Command{
		Use:   "tag NODE",
		Short: "Set a node's region, zone and tags",
		Long: "Set a node's region, zone and tags. This replaces what the node had: give every tag it should carry.\n\n" +
			"A reserved tag keeps all other work off the node. A compliance tag, such as hipaa, is always reserved:\n" +
			"projects that require it run only on nodes that carry it, and nothing else runs on those nodes.",
		Example: "  geass nodes tag worker-2 --region eu --tags hipaa --compliance hipaa",
		Args:    usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			// A tag marked compliance or reserved must be one the node has.
			all := append(append([]string{}, tags...), append(append([]string{}, reserved...), compliance...)...)
			seen := map[string]bool{}
			unique := all[:0]
			for _, name := range all {
				if !seen[name] {
					seen[name] = true
					unique = append(unique, name)
				}
			}
			warnings, err := api.SetNodePlacement(cmd.Context(), args[0], region, zone, unique, reserved, compliance)
			if err != nil {
				return err
			}
			a.printf("Updated %s. Running workloads are not moved: one that restarts only returns to a node that matches.\n", args[0])
			// What the change leaves for the person to do or watch for, such as work that is already on the node and
			// does not belong there. It goes to the error stream like any note, and is never left out.
			for _, warning := range warnings {
				a.note("warning: %s", warning)
			}
			return nil
		},
	}
	tag.Flags().StringVar(&region, "region", "", "the region, such as eu")
	tag.Flags().StringVar(&zone, "zone", "", "the site inside the region")
	tag.Flags().StringSliceVar(&tags, "tags", nil, "tags the node carries")
	tag.Flags().StringSliceVar(&reserved, "reserved", nil, "tags to reserve the node for (implies the tag)")
	tag.Flags().StringSliceVar(&compliance, "compliance", nil, "compliance tags: reserved, and required by projects that need them (implies the tag)")
	cmd.AddCommand(tag)
	return cmd
}

func (a *App) complianceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compliance",
		Short: "Require node tags, such as hipaa, for everything a project runs",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show PROJECT",
		Short: "Show the tags a project requires and where it falls short",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			report, err := api.Compliance(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printJSON(report)
			}
			if len(report.RequiredTags) == 0 {
				a.printf("%s requires no node tags.\n", args[0])
				return nil
			}
			a.printf("%s requires nodes tagged: %s\n", args[0], strings.Join(report.RequiredTags, ", "))
			rows := make([][]string, 0, len(report.Databases))
			for _, db := range report.Databases {
				state := "ok"
				if !db.Compliant {
					state = "missing " + strings.Join(db.Missing, ", ")
				}
				rows = append(rows, []string{db.Name, state})
			}
			if len(rows) > 0 {
				a.printTable([]string{"DATABASE", "COMPLIANCE"}, rows)
			}
			if len(report.OpenNodes) > 0 {
				a.printf("\nThese nodes carry a required tag without being reserved for it: %s\n", strings.Join(report.OpenNodes, ", "))
			}
			return nil
		},
	})
	set := &cobra.Command{
		Use:   "set PROJECT TAG...",
		Short: "Require these node tags for everything in a project (replaces the current ones)",
		Args:  usageArgs(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			if err := api.SetRequiredTags(cmd.Context(), args[0], args[1:]); err != nil {
				return err
			}
			a.printf("%s now requires: %s\n", args[0], strings.Join(args[1:], ", "))
			return nil
		},
	}
	clearTags := &cobra.Command{
		Use:   "clear PROJECT",
		Short: "Stop requiring node tags for a project",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			if err := api.SetRequiredTags(cmd.Context(), args[0], nil); err != nil {
				return err
			}
			a.printf("%s no longer requires node tags.\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(set, clearTags)
	return cmd
}

// nodeCordonCmd cordons a node, or with cordon false uncordons it.
func (a *App) nodeCordonCmd(cordon bool) *cobra.Command {
	use, short, long, done := "uncordon NODE", "Let new services start on a node again", "Uncordon a node, so that new services can start on it. "+
		"A node that Geass cordoned itself, to upgrade it or to empty it, is uncordoned by Geass when it is done, and is refused here.", "Uncordoned"
	if cordon {
		use, short, done = "cordon NODE", "Stop new services starting on a node", "Cordoned"
		long = "Cordon a node: what runs on it stays, and no new service starts there. Use it before you work on a server. " +
			"The last node that can start new services is refused, because new services would have no place to start. " +
			"A cordoned node does not count toward high availability."
	}
	return &cobra.Command{
		Use: use, Short: short, Long: long, Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			if err := api.CordonNode(cmd.Context(), args[0], cordon); err != nil {
				return err
			}
			a.printf("%s the node %s.\n", done, args[0])
			return nil
		},
	}
}
