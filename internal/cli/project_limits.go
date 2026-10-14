package cli

import (
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/degoke/geass/pkg/client"
)

// quotaFlag ties a flag to the limit the API calls it.
type quotaFlag struct {
	flag, field, help string
}

// quotaFlags are the limits, in the order they are listed. The first four are what one environment may ask for, and
// the others are what the whole project may ask for, added up over its environments.
var quotaFlags = []quotaFlag{
	{"cpu", "cpu", "the most CPU one environment's pods may ask for, such as 4 or 500m"},
	{"memory", "memory", "the most memory one environment's pods may ask for, such as 8Gi"},
	{"storage", "storage", "the most disk one environment's volumes may ask for, such as 50Gi"},
	{"pods", "pods", "the most pods one environment may have at once"},
	{"project-cpu", "projectCpu", "the most CPU the whole project's pods may ask for, over all its environments"},
	{"project-memory", "projectMemory", "the most memory the whole project's pods may ask for"},
	{"project-storage", "projectStorage", "the most disk the whole project's volumes may ask for"},
	{"project-pods", "projectPods", "the most pods the whole project may have at once"},
}

func (a *App) projectQuotaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quota",
		Short: "Cap what a project's environments, and the project as a whole, can use",
		Long: "A project with no limits can ask for everything the cluster has. A limit caps what the pods of one " +
			"environment ask for (the --cpu, --memory, --storage and --pods flags), or what all the project's environments " +
			"ask for together (the --project-* flags). Changing limits needs an admin's token.",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show PROJECT",
		Short: "Show a project's limits and what it uses of them",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			quota, err := api.ProjectQuota(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.printQuota(args[0], quota)
		},
	})

	set := &cobra.Command{
		Use:   "set PROJECT",
		Short: "Set limits for a project; a limit you do not name keeps its value, and one given empty is removed",
		Example: "  geass projects quota set shop --project-cpu 8 --project-memory 16Gi --cpu 4\n" +
			"  geass projects quota set shop --memory ''     # remove the per-environment memory limit",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			fields := map[string]string{}
			for _, q := range quotaFlags {
				if cmd.Flags().Changed(q.flag) {
					value, _ := cmd.Flags().GetString(q.flag)
					fields[q.field] = strings.TrimSpace(value)
				}
			}
			if len(fields) == 0 {
				return usageError("name a limit to change, such as --cpu 4 or --project-memory 16Gi")
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			quota, err := api.SetProjectQuota(cmd.Context(), args[0], fields)
			if err != nil {
				return err
			}
			return a.printQuota(args[0], quota)
		},
	}
	for _, q := range quotaFlags {
		set.Flags().String(q.flag, "", q.help+"; empty removes it")
	}
	cmd.AddCommand(set)

	cmd.AddCommand(&cobra.Command{
		Use:   "clear PROJECT",
		Short: "Remove every limit from a project",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			fields := make(map[string]string, len(client.QuotaFields))
			for _, name := range client.QuotaFields {
				fields[name] = ""
			}
			quota, err := api.SetProjectQuota(cmd.Context(), args[0], fields)
			if err != nil {
				return err
			}
			return a.printQuota(args[0], quota)
		},
	})
	return cmd
}

// printQuota shows the limits as a table: what each environment may ask for, what the project may ask for as a
// whole, and what the whole-project limit is being used up to.
func (a *App) printQuota(project string, quota client.ProjectQuota) error {
	if a.flags.json {
		return a.printJSON(quota)
	}
	if quota.Environment.IsZero() && quota.Project.IsZero() {
		a.printf("%s has no limits: it can ask for everything the cluster has.\n", project)
		return nil
	}
	pods := func(n int) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	rows := [][]string{
		{"cpu", dash(quota.Environment.CPU), dash(quota.Project.CPU), dash(quota.Used.CPU)},
		{"memory", dash(quota.Environment.Memory), dash(quota.Project.Memory), dash(quota.Used.Memory)},
		{"storage", dash(quota.Environment.Storage), dash(quota.Project.Storage), dash(quota.Used.Storage)},
		{"pods", dash(pods(quota.Environment.Pods)), dash(pods(quota.Project.Pods)), dash(pods(quota.Used.Pods))},
	}
	a.printf("Limits for %s\n", project)
	a.printTable([]string{"", "PER ENVIRONMENT", "WHOLE PROJECT", "IN USE"}, rows)
	if quota.Project.IsZero() {
		a.note("In use is shown for the whole-project limits only.")
	}
	return nil
}

func (a *App) projectNetworkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Choose who can reach a project's services, and whether they are kept away from the cluster's machines",
		Long: "By default the services of a project can be reached by the project itself, the platform and public " +
			"domains. A project can be opened to every service in the cluster, or to named projects, and it can be " +
			"kept away from the addresses of the machines the cluster runs on. Changing it needs an admin's token.",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show PROJECT",
		Short: "Show who can reach a project's services",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			network, err := api.ProjectNetwork(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.printNetwork(args[0], network)
		},
	})

	var isolation string
	var allowFrom []string
	var blockNodes bool
	set := &cobra.Command{
		Use:   "set PROJECT",
		Short: "Change who can reach a project's services; what you do not name stays as it is",
		Example: "  geass projects network set shop --block-nodes            # keep its services away from the cluster's machines\n" +
			"  geass projects network set shop --block-nodes=false\n" +
			"  geass projects network set shop --allow-from billing,search\n" +
			"  geass projects network set shop --isolation none        # open it to every service in the cluster",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var change client.NetworkChange
			if cmd.Flags().Changed("isolation") {
				switch strings.ToLower(strings.TrimSpace(isolation)) {
				case "project", "private":
					value := "Project"
					change.Isolation = &value
				case "none", "open":
					value := "None"
					change.Isolation = &value
				default:
					return usageError("--isolation is project (private) or none (open), not %q", isolation)
				}
			}
			if cmd.Flags().Changed("allow-from") {
				change.AllowFromProjects = &allowFrom
			}
			if cmd.Flags().Changed("block-nodes") {
				change.BlockNodeAddresses = &blockNodes
			}
			if change.Isolation == nil && change.AllowFromProjects == nil && change.BlockNodeAddresses == nil {
				return usageError("name something to change: --isolation, --allow-from or --block-nodes")
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			network, err := api.SetProjectNetwork(cmd.Context(), args[0], change)
			if err != nil {
				return err
			}
			return a.printNetwork(args[0], network)
		},
	}
	set.Flags().StringVar(&isolation, "isolation", "", "project (only the project itself, the platform and public domains) or none (every service in the cluster)")
	set.Flags().StringSliceVar(&allowFrom, "allow-from", nil, "other projects that may reach this one, separated by commas; empty allows none")
	set.Flags().BoolVar(&blockNodes, "block-nodes", false, "keep the project's services away from the machines the cluster runs on (use --block-nodes=false to allow it)")
	cmd.AddCommand(set)
	return cmd
}

func (a *App) printNetwork(project string, network client.ProjectNetwork) error {
	if a.flags.json {
		return a.printJSON(network)
	}
	who := "only the project itself, the platform and public domains"
	if network.Isolation == "None" {
		who = "every service in the cluster (isolation is off)"
	}
	a.printf("Who can reach %s: %s\n", project, who)
	if network.Isolation != "None" && len(network.AllowFromProjects) > 0 {
		a.printf("Also allowed in: %s\n", strings.Join(network.AllowFromProjects, ", "))
	}
	blocked := "no"
	if network.BlockNodeAddresses {
		blocked = "yes"
	}
	a.printf("Kept away from the cluster's machines: %s\n", blocked)
	return nil
}
