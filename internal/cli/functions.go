package cli

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/degoke/geass/pkg/client"
)

func (a *App) functionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "functions",
		Aliases: []string{"function", "fn"},
		Short:   "Schedule and call functions",
		Long: "Functions are services that run code you give them. A function in event mode is a handler that is given an event for\n" +
			"each request and returns the response; one in http mode is a program that serves HTTP itself.\n" +
			"A trigger calls the function on a schedule, and wakes it first when it is asleep.",
	}
	cmd.AddCommand(a.triggersCmd(), a.invokeCmd())
	return cmd
}

func (a *App) triggersCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "triggers", Aliases: []string{"trigger"}, Short: "List, add and remove a function's schedules"}
	cmd.AddCommand(a.triggersListCmd(), a.triggersAddCmd(), a.triggersRemoveCmd())
	return cmd
}

func (a *App) triggersListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list APP",
		Short: "Show a function's schedules",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			fn, err := api.Function(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printJSON(fn)
			}
			rows := make([][]string, 0, len(fn.Triggers))
			for _, trigger := range fn.Triggers {
				rows = append(rows, []string{trigger.Name, trigger.Schedule, firstSet(trigger.Path, "/")})
			}
			a.printTable([]string{columnName, "SCHEDULE (UTC)", "PATH"}, rows)
			return nil
		},
	}
}

func (a *App) triggersAddCmd() *cobra.Command {
	var path, payload string
	cmd := &cobra.Command{
		Use:   "add APP NAME SCHEDULE",
		Short: "Call a function on a schedule (replaces the schedule with that name)",
		Long: "Call a function on a schedule. The schedule is a cron expression in UTC with five fields, or @hourly, @daily,\n" +
			"@weekly, @monthly or @yearly. Each run is a POST to the function. Schedules apply when the function is next deployed.",
		Example: "  geass functions triggers add report nightly \"0 3 * * *\"\n" +
			"  geass functions triggers add report sync \"*/15 * * * *\" --path /sync --payload '{\"full\":true}'",
		Args: usageArgs(cobra.ExactArgs(3)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			trigger := client.FunctionTrigger{Name: args[1], Schedule: args[2], Path: path, Payload: payload}
			if err := api.AddFunctionTrigger(cmd.Context(), args[0], trigger); err != nil {
				return err
			}
			a.printf("%s will call %s on schedule %q once it is deployed. Deploy it with `geass deploy %s`.\n", args[0], args[1], args[2], args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "the path to call (default /)")
	cmd.Flags().StringVar(&payload, "payload", "", "the JSON body to send")
	return cmd
}

func (a *App) triggersRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove APP NAME",
		Aliases: []string{"rm"},
		Short:   "Stop calling a function on a schedule",
		Args:    usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			removed, err := api.RemoveFunctionTrigger(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			if !removed {
				return usageError("%s has no schedule named %q", args[0], args[1])
			}
			a.printf("Removed %s. It stops once %s is deployed.\n", args[1], args[0])
			return nil
		},
	}
}

func (a *App) invokeCmd() *cobra.Command {
	var method, path, data string
	cmd := &cobra.Command{
		Use:     "invoke APP",
		Short:   "Call a function at its public address and print the answer",
		Example: "  geass functions invoke hello\n  geass functions invoke report --path /sync --data '{\"full\":true}'",
		Args:    usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			fn, err := api.Function(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			method = strings.ToUpper(method)
			if method == "" {
				method = http.MethodPost
				if data == "" {
					method = http.MethodGet
				}
			}
			result, err := api.Invoke(cmd.Context(), fn, method, firstSet(path, "/"), data)
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printJSON(result)
			}
			a.note("HTTP %d", result.Status)
			a.printf("%s\n", strings.TrimRight(result.Body, "\n"))
			if result.Status >= http.StatusBadRequest {
				return &exitError{ExitError, fmt.Errorf("the function answered HTTP %d", result.Status)}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&method, "method", "", "the HTTP method (GET, or POST when there is --data)")
	cmd.Flags().StringVar(&path, "path", "", "the path to call, with a query if you like (default /)")
	cmd.Flags().StringVar(&data, "data", "", "the JSON body to send")
	return cmd
}
