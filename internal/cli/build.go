package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/degoke/geass/pkg/client"
)

// sourceRepository is what the API calls the source of a service that is built from a repository.
const sourceRepository = "git"

func (a *App) buildCmd() *cobra.Command {
	var wait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "build [APP...]",
		Short: "Run the build of services built from a repository again",
		Long: "Run the build of the named services again, from the head of their branch (or the commit they pin). With no names,\n" +
			"build every service built from a repository in the environment given by --project and --environment. It is what\n" +
			"a push to the branch does, for a pipeline that has pushed nothing: a service that is already running keeps\n" +
			"serving from its current image until the new one is built, and is then rolled out onto it. Changes saved to a\n" +
			"service and not yet deployed are not applied by this: run `geass deploy` for those.\n\n" +
			"With --wait, geass follows each build through to the rollout and exits non-zero (5) if the build or the deploy\n" +
			"fails, saying why. Without it, it returns as soon as the build has been started. With --json it prints\n" +
			"{\"building\": [...]} when it does not wait, and {\"items\": [...]} (the services' statuses) when it does.",
		Example: "  geass build web --wait\n  geass build -p shop -e production --wait --timeout 20m",
		RunE: func(cmd *cobra.Command, args []string) error {
			api, resolved, err := a.api()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			names := args
			if len(args) == 0 {
				if resolved.Project == "" || resolved.Environment == "" {
					return usageError("say what to build: name services, or give --project and --environment to build every service built from a repository")
				}
				if names, err = a.builtApps(ctx, api, resolved); err != nil {
					return err
				}
				if len(names) == 0 {
					a.note("No services built from a repository to build in %s/%s.", resolved.Project, resolved.Environment)
					if a.flags.json {
						return a.printJSON(map[string]any{"building": []string{}})
					}
					return nil
				}
			}
			// Each service's build as it is now, so that waiting is for the build that is about to start and not for the
			// one that finished before it.
			after := map[string]*client.BuildStatus{}
			if wait {
				for _, name := range names {
					status, err := api.AppStatus(ctx, name)
					if err != nil {
						return fmt.Errorf("%s: %w", name, err)
					}
					after[name] = status.Build
				}
			}
			for _, name := range names {
				if err := api.RestartBuild(ctx, name); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				a.note("%s: build started", name)
			}
			if !wait {
				if a.flags.json {
					return a.printJSON(map[string]any{"building": names})
				}
				return nil
			}
			statuses, err := a.waitForAfter(ctx, api, names, timeout, after)
			if printErr := a.printStatuses(statuses); printErr != nil && err == nil {
				err = printErr
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "wait until the builds have finished and the services are ready")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "how long --wait waits for each service")
	return cmd
}

// builtApps are the services of the environment that are built from a repository and have been deployed: the ones that
// have a build to run again. A draft has none yet.
func (a *App) builtApps(ctx context.Context, api *client.Client, resolved Resolved) ([]string, error) {
	items, err := api.List(ctx, "apps", client.Filter{Project: resolved.Project, Environment: resolved.Environment})
	if err != nil {
		return nil, err
	}
	var names []string
	for _, item := range items {
		if source, _ := item.Details["source"].(string); source == sourceRepository && item.State != "draft" && item.State != "deleting" {
			names = append(names, item.Name)
		}
	}
	return names, nil
}
