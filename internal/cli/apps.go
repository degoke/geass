package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/degoke/geass/pkg/client"
)

func (a *App) appsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "apps", Aliases: []string{"app", "services", "service"}, Short: "Manage services"}
	cmd.AddCommand(
		a.listCmd("apps", "list", "ls"), a.getCmd("apps", "get NAME"),
		a.statusCmd(), a.deployCmd(), a.buildCmd(), a.deploymentsCmd(), a.logsCmd(), a.envCmd(), a.scaleCmd(), a.rollbackCmd(),
		a.createAppCmd(), a.deleteCmd("apps"),
	)
	return cmd
}

// ---- waiting ----

// waitFor waits for services to be ready, printing progress as it changes, and returns what each ended as.
func (a *App) waitFor(ctx context.Context, api *client.Client, names []string, timeout time.Duration) ([]client.AppStatus, error) {
	return a.waitForAfter(ctx, api, names, timeout, nil)
}

// waitForAfter is waitFor for services whose build was just run again. after is each one's build as it was before
// that was asked for, which is how a status that still shows the old run is told from the new one.
func (a *App) waitForAfter(ctx context.Context, api *client.Client, names []string, timeout time.Duration, after map[string]*client.BuildStatus) ([]client.AppStatus, error) {
	results := make([]client.AppStatus, 0, len(names))
	var failed []string
	for _, name := range names {
		var lastLine string
		status, err := api.WaitForApp(ctx, name, client.WaitOptions{
			Timeout:    timeout,
			Interval:   a.PollInterval,
			AfterBuild: after[name],
			OnStatus: func(s client.AppStatus) {
				line := progressLine(s)
				if line != lastLine {
					a.note("%s: %s", name, line)
					lastLine = line
				}
			},
		})
		results = append(results, status)
		if err != nil {
			if !errorsIsDeployFailed(err) {
				return results, err
			}
			failed = append(failed, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(failed) > 0 {
		return results, fmt.Errorf("%w: %s", client.ErrDeployFailed, strings.Join(failed, "; "))
	}
	return results, nil
}

func errorsIsDeployFailed(err error) bool {
	return strings.Contains(err.Error(), client.ErrDeployFailed.Error())
}

// progressLine says in a few words what a service is doing, for the line that shows it moving.
func progressLine(s client.AppStatus) string {
	switch {
	case s.Build != nil && s.Build.Phase != "" && s.Build.Phase != "Succeeded" && !s.Ready:
		return "build " + strings.ToLower(s.Build.Phase)
	case s.Replicas != nil && !s.Ready:
		return fmt.Sprintf("%s (%d of %d ready)", s.State, s.Replicas.Ready, s.Replicas.Desired)
	case s.Ready && s.URL != "":
		return "ready at " + s.URL
	default:
		return s.State
	}
}

func (a *App) printStatuses(statuses []client.AppStatus) error {
	if a.flags.json {
		return a.printJSON(map[string]any{"items": statuses})
	}
	rows := make([][]string, 0, len(statuses))
	for _, s := range statuses {
		replicas := "-"
		if s.Replicas != nil {
			replicas = fmt.Sprintf("%d/%d", s.Replicas.Ready, s.Replicas.Desired)
		}
		if s.Sleeping {
			replicas += " (asleep)"
		}
		rows = append(rows, []string{s.Name, s.State, replicas, dash(buildPhase(s)), dash(s.URL), dash(s.Message)})
	}
	a.printTable([]string{"NAME", "STATE", "READY", "BUILD", "URL", "MESSAGE"}, rows)
	return nil
}

func buildPhase(s client.AppStatus) string {
	if s.Build == nil {
		return ""
	}
	return s.Build.Phase
}

// ---- deploy ----

func (a *App) deployCmd() *cobra.Command {
	var wait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "deploy [APP...]",
		Short: "Deploy services, or everything pending in an environment",
		Long: "Deploy the pending changes of the named services. With no names, deploy everything that is pending in the\n" +
			"environment given by --project and --environment.\n\n" +
			"With --wait, geass waits until each service is ready and exits non-zero (5) if one fails, saying why.",
		Example: "  geass deploy web --wait\n  geass deploy -p shop -e production --wait",
		RunE: func(cmd *cobra.Command, args []string) error {
			api, resolved, err := a.api()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			names := args
			if len(args) == 0 {
				if resolved.Project == "" || resolved.Environment == "" {
					return usageError("say what to deploy: name services, or give --project and --environment to deploy everything pending")
				}
				deployed, err := api.DeployEnvironment(ctx, resolved.Project, resolved.Environment)
				if err != nil {
					return err
				}
				a.note("Deployed %d pending change(s) in %s/%s.", deployed, resolved.Project, resolved.Environment)
				if names, err = a.deployedApps(ctx, api, resolved); err != nil {
					return err
				}
			} else {
				for _, name := range args {
					result, err := api.Deploy(ctx, name)
					if err != nil {
						return fmt.Errorf("%s: %w", name, err)
					}
					a.note("%s: %s", name, firstSet(result.Title, "deploying"))
				}
			}
			if !wait {
				if a.flags.json {
					return a.printJSON(map[string]any{"deployed": names})
				}
				return nil
			}
			statuses, err := a.waitFor(ctx, api, names, timeout)
			if printErr := a.printStatuses(statuses); printErr != nil && err == nil {
				err = printErr
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "wait until the services are ready")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "how long --wait waits for each service")
	return cmd
}

// deployedApps are the services in the environment that have been deployed, which are the ones to wait on.
func (a *App) deployedApps(ctx context.Context, api *client.Client, resolved Resolved) ([]string, error) {
	items, err := api.List(ctx, "apps", client.Filter{Project: resolved.Project, Environment: resolved.Environment})
	if err != nil {
		return nil, err
	}
	var names []string
	for _, item := range items {
		if item.State != "draft" && item.State != "deleting" {
			names = append(names, item.Name)
		}
	}
	return names, nil
}

// ---- status ----

func (a *App) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [APP...]",
		Short: "Show whether services are up, and what a deploy left behind",
		Long:  "Show the status of the named services, or of every service in the project and environment given.",
		RunE: func(cmd *cobra.Command, args []string) error {
			api, resolved, err := a.api()
			if err != nil {
				return err
			}
			names := args
			if len(names) == 0 {
				items, err := api.List(cmd.Context(), "apps", client.Filter{Project: resolved.Project, Environment: resolved.Environment})
				if err != nil {
					return err
				}
				for _, item := range items {
					names = append(names, item.Name)
				}
			}
			statuses := make([]client.AppStatus, 0, len(names))
			for _, name := range names {
				status, err := api.AppStatus(cmd.Context(), name)
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				statuses = append(statuses, status)
			}
			return a.printStatuses(statuses)
		},
	}
}

// ---- logs ----

func (a *App) logsCmd() *cobra.Command {
	var follow bool
	var options client.LogOptions
	cmd := &cobra.Command{
		Use:   "logs APP",
		Short: "Show a service's logs, or follow them",
		Long: "Show a service's recent logs. With -f, keep following them as they are written, across restarts.\n\n" +
			"--tail is how many of the newest lines to show. The dashboard reads that many from the end of each copy's log\n" +
			"before it applies --level and --search, so a filter looks through the most recent lines: raise --tail to look further back.\n\n" +
			"Reading logs needs an admin token, a project token, or a read-only token \"with logs\".",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			print := func(line client.LogLine) error {
				if a.flags.json {
					return a.printJSON(line)
				}
				a.printf("%s %s %s\n", line.Time.Format("15:04:05"), shortPod(line.Pod), line.Text)
				return nil
			}
			if follow {
				err := api.FollowLogs(cmd.Context(), args[0], options, print)
				if err != nil && cmd.Context().Err() != nil {
					return nil // interrupted: that is how a person ends a follow
				}
				return err
			}
			lines, err := api.Logs(cmd.Context(), args[0], options)
			if err != nil {
				return err
			}
			for _, line := range lines {
				if err := print(line); err != nil {
					return err
				}
			}
			if len(lines) == 0 && !a.flags.json {
				a.note("No log lines matched.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep following new lines")
	cmd.Flags().IntVar(&options.Tail, "tail", 200, "how many recent lines to start with")
	cmd.Flags().StringVar(&options.Since, "since", "", "only lines newer than this long ago, such as 15m or 2h")
	cmd.Flags().StringVar(&options.Level, "level", "", "only lines of this level: error, warn, info or debug")
	cmd.Flags().StringVar(&options.Search, "search", "", "only lines containing this text")
	return cmd
}

// shortPod trims the replica set hash from a pod name, which says nothing a person needs.
func shortPod(pod string) string {
	if parts := strings.Split(pod, "-"); len(parts) > 2 {
		return strings.Join(parts[len(parts)-1:], "-")
	}
	return pod
}

// ---- variables ----

func (a *App) envCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "List, set and remove a service's variables",
		Long: "Variables are changes like any other: they apply when the service is next deployed, unless you pass --deploy.\n" +
			"The list shows names only. Secret values are never returned. Plain configuration is not secret, so `env list --json`\n" +
			"includes its values: keep anything sensitive in a secret with --secret.",
	}
	var secret, deploy, wait bool
	list := &cobra.Command{
		Use:   "list APP",
		Short: "List the names of a service's variables",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			vars, err := api.Variables(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printJSON(vars)
			}
			names := make([]string, 0)
			for _, name := range vars.Secrets {
				names = append(names, name+"\tsecret")
			}
			for key := range vars.Config {
				names = append(names, key+"\tconfig")
			}
			for _, item := range vars.Env {
				names = append(names, item.Name+"\t"+item.Kind)
			}
			slices.Sort(names)
			rows := make([][]string, 0, len(names))
			for _, line := range names {
				rows = append(rows, strings.SplitN(line, "\t", 2))
			}
			a.printTable([]string{"NAME", "KIND"}, rows)
			return nil
		},
	}
	set := &cobra.Command{
		Use:     "set APP KEY=VALUE...",
		Short:   "Set variables",
		Example: "  geass env set web LOG_LEVEL=debug\n  geass env set web --secret STRIPE_KEY=sk_live_...\n  geass env set web FLAG=on --deploy --wait",
		Args:    usageArgs(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			for _, pair := range args[1:] {
				key, value, ok := strings.Cut(pair, "=")
				if !ok || key == "" {
					return usageError("%q is not KEY=VALUE", pair)
				}
				if err := api.SetVariable(cmd.Context(), args[0], key, value, secret); err != nil {
					return fmt.Errorf("%s: %w", key, err)
				}
				a.note("set %s", key)
			}
			return a.afterChange(cmd.Context(), api, args[0], deploy, wait)
		},
	}
	unset := &cobra.Command{
		Use:   "unset APP KEY...",
		Short: "Remove variables",
		Args:  usageArgs(cobra.MinimumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			for _, key := range args[1:] {
				if err := api.UnsetVariable(cmd.Context(), args[0], key, secret); err != nil {
					return fmt.Errorf("%s: %w", key, err)
				}
				a.note("removed %s", key)
			}
			return a.afterChange(cmd.Context(), api, args[0], deploy, wait)
		},
	}
	for _, c := range []*cobra.Command{set, unset} {
		c.Flags().BoolVar(&secret, "secret", false, "it is a secret, kept apart from ordinary configuration")
		c.Flags().BoolVar(&deploy, "deploy", false, "deploy the change now")
		c.Flags().BoolVar(&wait, "wait", false, "with --deploy: wait until the service is ready")
	}
	cmd.AddCommand(list, set, unset)
	return cmd
}

// afterChange deploys what was just changed when asked to, and says what is still waiting when not.
func (a *App) afterChange(ctx context.Context, api *client.Client, app string, deploy, wait bool) error {
	if !deploy {
		a.note("The change is pending. Run `geass deploy %s` to apply it.", app)
		return nil
	}
	if _, err := api.Deploy(ctx, app); err != nil {
		return err
	}
	a.note("%s: deploying", app)
	if !wait {
		return nil
	}
	statuses, err := a.waitFor(ctx, api, []string{app}, 10*time.Minute)
	if printErr := a.printStatuses(statuses); printErr != nil && err == nil {
		err = printErr
	}
	return err
}

// ---- scale, rollback ----

func (a *App) scaleCmd() *cobra.Command {
	var sleepAfter time.Duration
	var alwaysOn bool
	cmd := &cobra.Command{
		Use:   "scale APP [REPLICAS]",
		Short: "Set how many copies of a service run, or have it sleep when idle (takes effect at once)",
		Long: "Set how many copies of a service run.\n\n" +
			"With --sleep-after the service stops when it has had no requests for that long and starts again on the next one\n" +
			"(the serverless add-on has to be on in the cluster settings, and the service needs a public host).\n" +
			"--always-on turns that off.",
		Example: "  geass scale web 3\n  geass scale web --sleep-after 10m\n  geass scale web --always-on",
		Args:    usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			sleeping := cmd.Flags().Changed("sleep-after")
			if sleeping && alwaysOn {
				return usageError("--sleep-after and --always-on cannot be used together")
			}
			if len(args) == 1 && !sleeping && !alwaysOn {
				return usageError("say how many copies, or use --sleep-after or --always-on")
			}
			if sleeping && (sleepAfter < 30*time.Second || sleepAfter > 24*time.Hour) {
				return usageError("--sleep-after must be between 30s and 24h")
			}
			replicas := -1
			if len(args) == 2 {
				n, err := strconv.Atoi(args[1])
				if err != nil || n < 0 {
					return usageError("%q is not a number of replicas", args[1])
				}
				replicas = n
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			cannotSleep := ""
			if sleeping || alwaysOn {
				if err := api.SetSleepWhenIdle(cmd.Context(), args[0], sleeping, int(sleepAfter.Seconds())); err != nil {
					return err
				}
				if sleeping {
					cannotSleep, err = a.confirmSleep(cmd.Context(), api, args[0], sleepAfter)
					if err != nil {
						return err
					}
				} else {
					a.printf("%s stays running\n", args[0])
				}
			}
			if replicas >= 0 {
				if err := api.Scale(cmd.Context(), args[0], replicas); err != nil {
					return err
				}
				a.printf("%s scaled to %d\n", args[0], replicas)
			}
			if cannotSleep != "" {
				return fmt.Errorf("saved, but %s cannot sleep when idle and stays running: %s", args[0], cannotSleep)
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&sleepAfter, "sleep-after", 0, "stop the service after this long without requests, such as 10m")
	cmd.Flags().BoolVar(&alwaysOn, "always-on", false, "keep the service running instead of sleeping when idle")
	return cmd
}

func (a *App) rollbackCmd() *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "rollback APP [DEPLOYMENT]",
		Short: "Go back to an earlier deployment (the previous one by default)",
		Long:  "Go back to an earlier deployment. `geass apps deployments APP` lists them, newest first, with the ids this takes.\nFunctions cannot be rolled back: restore the earlier code and deploy again.",
		Args:  usageArgs(cobra.RangeArgs(1, 2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			revision := ""
			if len(args) == 2 {
				revision = args[1]
			} else {
				history, err := api.Deployments(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				if len(history) < 2 {
					return &exitError{ExitNotFound, fmt.Errorf("%s has no earlier deployment to go back to", args[0])}
				}
				revision = history[1].ID
			}
			if err := api.Rollback(cmd.Context(), args[0], revision); err != nil {
				return err
			}
			a.note("%s: rolling back to %s", args[0], revision)
			if !wait {
				return nil
			}
			statuses, err := a.waitFor(cmd.Context(), api, []string{args[0]}, 10*time.Minute)
			if printErr := a.printStatuses(statuses); printErr != nil && err == nil {
				err = printErr
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "wait until the service is ready")
	return cmd
}

// confirmSleep reads back whether a service that was just told to sleep when idle can. The setting is saved at
// once, but the operator decides whether it takes effect, and a service that cannot sleep stays running: saying
// it sleeps would be false. It returns why it cannot, for the caller to end with once the rest of what was asked
// has been done, or nothing when it can.
func (a *App) confirmSleep(ctx context.Context, api *client.Client, app string, after time.Duration) (string, error) {
	state, err := api.WaitForSleep(ctx, app, client.WaitOptions{Timeout: a.SleepCheckTimeout, Interval: a.PollInterval})
	if err != nil {
		return "", err
	}
	if !state.Active {
		return state.Why(), nil
	}
	a.printf("%s sleeps after %s without requests\n", app, after)
	return "", nil
}

// ---- deployments ----

func (a *App) deploymentsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deployments APP",
		Short: "List a service's deployments, newest first",
		Long: "List a service's deployment history, newest first. The newest is the one running. The ID of an entry is what\n" +
			"`geass rollback APP DEPLOYMENT` takes. A rollback adds an entry of its own.",
		Example: "  geass apps deployments web\n  geass rollback web web-7x2kq",
		Args:    usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			history, err := api.Deployments(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.flags.json {
				if history == nil {
					history = []client.Deployment{}
				}
				return a.printJSON(map[string]any{"items": history})
			}
			rows := make([][]string, 0, len(history))
			for _, deployment := range history {
				rows = append(rows, []string{
					deployment.ID, dash(deployment.Phase), age(deployment.CreatedAt), dash(deployment.Trigger), dash(deployment.Actor), dash(deployment.ChangeTitle),
				})
			}
			a.printTable([]string{"ID", "PHASE", "AGE", "TRIGGER", "ACTOR", "CHANGE"}, rows)
			return nil
		},
	}
}

// ---- create, delete ----

func (a *App) createAppCmd() *cobra.Command {
	var image, repository, branch, runtime, functionFile, mode string
	var port int
	var deploy bool
	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a service from an image, a Git repository or a function",
		Example: "  geass apps create web -p shop -e production --image nginx:alpine --port 80 --deploy\n" +
			"  geass apps create api -p shop -e production --repo acme/api --branch main\n" +
			"  geass apps create hook -p shop -e production --function hook.ts --runtime bun",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, resolved, err := a.api()
			if err != nil {
				return err
			}
			if resolved.Project == "" || resolved.Environment == "" {
				return usageError("give --project and --environment")
			}
			fields := map[string]any{"project": resolved.Project, "environment": resolved.Environment, "name": args[0]}
			// A service is a draft or it is deployed as it is saved, and the API takes the two to be different asks.
			if deploy {
				fields["deploy"] = true
			} else {
				fields["draft"] = true
			}
			switch {
			case image != "":
				fields["source"], fields["image"] = "image", image
			case repository != "":
				fields["source"], fields["repository"], fields["branch"] = "git", repository, firstSet(branch, "main")
			case functionFile != "":
				code, err := os.ReadFile(functionFile)
				if err != nil {
					return err
				}
				fields["source"], fields["runtime"], fields["code"] = "function", firstSet(runtime, "bun"), string(code)
				if mode != "" {
					fields["mode"] = mode
				}
			default:
				return usageError("say what to run: --image, --repo or --function")
			}
			if port > 0 {
				fields["port"] = port
			}
			answer, err := api.CreateApp(cmd.Context(), fields)
			if err != nil {
				return err
			}
			if deploy {
				// A Geass older than this command line ignores deploy and saves a draft: say so, and do not say it is deploying.
				if answer["deployed"] != true {
					return fmt.Errorf("%s was created, but this Geass did not deploy it (is it older than this command line?): run `geass deploy %s`", args[0], args[0])
				}
				a.printf("Created %s and started deploying it. Follow it with `geass status %s`.\n", args[0], args[0])
			} else {
				a.printf("Created %s as a draft. Deploy it with `geass deploy %s`.\n", args[0], args[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "run this container image")
	cmd.Flags().StringVar(&repository, "repo", "", "build from this GitHub repository (owner/name)")
	cmd.Flags().StringVar(&branch, "branch", "", "the branch to build (default main)")
	cmd.Flags().StringVar(&functionFile, "function", "", "run this file as a function")
	cmd.Flags().StringVar(&runtime, "runtime", "", "the function's runtime: bun, node or python (default bun)")
	cmd.Flags().StringVar(&mode, "mode", "", "the function's mode: http (a program that serves HTTP, the default) or event (a handler)")
	cmd.Flags().IntVar(&port, "port", 0, "the port it serves on")
	cmd.Flags().BoolVar(&deploy, "deploy", false, "deploy it straight away instead of saving a draft")
	return cmd
}

func (a *App) deleteCmd(kind string) *cobra.Command {
	return &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a " + strings.TrimSuffix(kind, "s"),
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.confirm(fmt.Sprintf("Delete %s %q? This cannot be undone.", strings.TrimSuffix(kind, "s"), args[0])); err != nil {
				return err
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			if err := api.Delete(cmd.Context(), kind, args[0]); err != nil {
				return err
			}
			a.printf("Deleted %s.\n", args[0])
			return nil
		},
	}
}

// ---- metrics ----

func (a *App) metricsCmd() *cobra.Command {
	var window, kind string
	cmd := &cobra.Command{
		Use:   "metrics NAME",
		Short: "Show CPU, memory, network and request use over time",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			out, err := api.Metrics(cmd.Context(), kind, args[0], window)
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printJSON(out)
			}
			if available, _ := out["available"].(bool); !available {
				message, _ := out["message"].(string)
				return &exitError{ExitError, fmt.Errorf("no metrics yet: %s", firstSet(message, "Prometheus is not reachable"))}
			}
			series, _ := out["series"].(map[string]any)
			keys := make([]string, 0, len(series))
			for key := range series {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			rows := make([][]string, 0, len(keys))
			for _, key := range keys {
				points, _ := series[key].([]any)
				rows = append(rows, []string{key, summarizePoints(points)})
			}
			a.printTable([]string{"SERIES", "LATEST (MIN - MAX)"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&window, "window", "1h", "how far back: 15m, 1h, 6h or 24h")
	cmd.Flags().StringVar(&kind, "kind", "apps", "what NAME is: apps, databases or projects")
	return cmd
}

func summarizePoints(points []any) string {
	if len(points) == 0 {
		return "-"
	}
	lowest, highest, latest := 0.0, 0.0, 0.0
	for i, raw := range points {
		point, _ := raw.(map[string]any)
		value, _ := point["v"].(float64)
		if i == 0 || value < lowest {
			lowest = value
		}
		if i == 0 || value > highest {
			highest = value
		}
		latest = value
	}
	return fmt.Sprintf("%.3g (%.3g - %.3g)", latest, lowest, highest)
}

// ---- the escape hatch ----

func (a *App) apiCmd() *cobra.Command {
	var data string
	var query []string
	cmd := &cobra.Command{
		Use:   "api METHOD PATH",
		Short: "Call the API directly",
		Long:  "Call any v1 API endpoint and print the JSON it answers. PATH is under /api/v1, such as /apps. The body is JSON from -d, from a file with -d @file, or from standard input with -d -.",
		Example: "  geass api GET /apps?project=shop\n" +
			"  geass api POST /apps/web/scale -d '{\"replicas\": 3}'",
		Args: usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			method := strings.ToUpper(args[0])
			path, rawQuery, _ := strings.Cut(args[1], "?")
			values, err := parseQuery(rawQuery, query)
			if err != nil {
				return err
			}
			var body any
			if data != "" {
				raw, err := a.readData(data)
				if err != nil {
					return err
				}
				body = rawJSON(raw)
			}
			var out any
			if err := api.Do(cmd.Context(), method, "/"+strings.TrimPrefix(path, "/api/v1/"), values, body, &out); err != nil {
				return err
			}
			return a.printJSON(out)
		},
	}
	cmd.Flags().StringVarP(&data, "data", "d", "", "the JSON body: a string, @file, or - for standard input")
	cmd.Flags().StringArrayVarP(&query, "query", "q", nil, "a query parameter, key=value (repeatable)")
	return cmd
}

func (a *App) readData(data string) ([]byte, error) {
	switch {
	case data == "-":
		return io.ReadAll(a.In)
	case strings.HasPrefix(data, "@"):
		return os.ReadFile(strings.TrimPrefix(data, "@"))
	default:
		return []byte(data), nil
	}
}

func (a *App) openapiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "openapi",
		Short: "Print the OpenAPI description of the API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			spec, err := api.APISpec(cmd.Context())
			if err != nil {
				return err
			}
			return a.printJSON(spec)
		},
	}
}
