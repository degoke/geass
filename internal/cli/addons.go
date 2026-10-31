package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/degoke/geass/pkg/client"
)

// The add-ons are what a cluster installs on the side of the platform itself: scale to zero, replicated storage, metrics and
// logs. They are named as they are under spec.addons of the cluster.

func (a *App) addonsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "addons",
		Aliases: []string{"addon"},
		Short:   "Turn the cluster's add-ons on and off, and see how each is doing",
		Long: "The add-ons are serverless (sleep when idle), longhorn (replicated storage), monitoring (metrics) and logging " +
			"(logs). Each one is Off, Installing, Ready or Failed; a Failed add-on says why. Everything here needs an admin's token.\n\n" +
			"Geass never removes serverless or longhorn. Turning serverless off leaves KEDA in place, and services that sleep keep " +
			"sleeping until you turn sleeping off for each (geass scale NAME --always-on). Longhorn cannot be turned off, because " +
			"databases keep their data on it. Turning monitoring or logging off removes it, and what it kept.",
	}
	// With nothing after it, the command lists the add-ons, which is what it is run for most.
	list := a.addonsListCmd()
	cmd.Args, cmd.RunE = usageArgs(cobra.NoArgs), list.RunE
	cmd.AddCommand(list, a.addonsEnableCmd(), a.addonsDisableCmd(), a.addonsSetCmd(), a.addonsRetryCmd())
	return cmd
}

func addonOn(addon client.Addon) string {
	if addon.Enabled {
		return "on"
	}
	return "off"
}

// printAddons shows the add-ons as a table.
func (a *App) printAddons(addons client.Addons) error {
	if a.flags.json {
		return a.printJSON(addons)
	}
	rows := make([][]string, 0, len(addons.Addons))
	for _, addon := range addons.Addons {
		rows = append(rows, []string{addon.Name, addon.Title, addonOn(addon), addon.State, dash(addonDetail(addon))})
	}
	a.printTable([]string{"ADD-ON", "NAME", "ON", "STATE", "DETAILS"}, rows)
	if addons.RetryPending != "" {
		a.note("A retry of %s is waiting for the operator.", addons.RetryPending)
	}
	return nil
}

// addonDetail is the sentence that says what an add-on is waiting for or what went wrong, and its options.
func addonDetail(addon client.Addon) string {
	parts := []string{}
	if addon.Message != "" {
		parts = append(parts, addon.Message)
	}
	if addon.Enabled && addon.Profile != "" {
		parts = append(parts, "profile "+addon.Profile+".")
	}
	if addon.Enabled && addon.RetentionDays > 0 {
		parts = append(parts, fmt.Sprintf("Logs kept %d days.", addon.RetentionDays))
	}
	if addon.CanRetry {
		parts = append(parts, "Run `geass addons retry "+addon.Name+"`.")
	}
	return strings.Join(parts, " ")
}

func (a *App) addonsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "status"},
		Short:   "List the add-ons and how each is doing",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			addons, err := api.Addons(cmd.Context())
			if err != nil {
				return err
			}
			return a.printAddons(addons)
		},
	}
}

type addonFlags struct {
	profile       string
	retentionDays int
	wait          bool
	timeout       time.Duration
}

// change is the change that the flags that were given ask for, and the enabled setting the command itself implies.
func (f *addonFlags) change(cmd *cobra.Command, enabled *bool) client.AddonChange {
	change := client.AddonChange{Enabled: enabled}
	if cmd.Flags().Changed("profile") {
		change.Profile = &f.profile
	}
	if cmd.Flags().Changed("retention-days") {
		change.RetentionDays = &f.retentionDays
	}
	return change
}

func addOptionFlags(cmd *cobra.Command, f *addonFlags) {
	cmd.Flags().StringVar(&f.profile, "profile", "", "for monitoring: lite or full")
	cmd.Flags().IntVar(&f.retentionDays, "retention-days", 0, "for logging: how many days logs are kept, 1 to 90")
}

func addWaitFlags(cmd *cobra.Command, f *addonFlags) {
	cmd.Flags().BoolVar(&f.wait, "wait", false, "wait until the add-on is ready, or has failed")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 15*time.Minute, "how long --wait waits")
}

func (a *App) addonsEnableCmd() *cobra.Command {
	var f addonFlags
	cmd := &cobra.Command{
		Use:   "enable ADD-ON",
		Short: "Turn an add-on on",
		Example: "  geass addons enable serverless --wait\n" +
			"  geass addons enable longhorn --wait\n" +
			"  geass addons enable logging --retention-days 14",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			on := true
			return a.changeAddon(cmd, args[0], f.change(cmd, &on), &f)
		},
	}
	addOptionFlags(cmd, &f)
	addWaitFlags(cmd, &f)
	return cmd
}

func (a *App) addonsDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable ADD-ON",
		Short: "Turn an add-on off",
		Long: "Turning serverless off leaves KEDA installed, and services that sleep keep sleeping. Turning monitoring or " +
			"logging off removes them and what they kept, so it asks first (or send --yes). Replicated storage (longhorn) cannot be turned off.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "monitoring" || args[0] == "logging" {
				if err := a.confirm(fmt.Sprintf("Turning %s off removes it and what it kept. Continue?", args[0])); err != nil {
					return err
				}
			}
			off := false
			return a.changeAddon(cmd, args[0], client.AddonChange{Enabled: &off}, &addonFlags{})
		},
	}
}

func (a *App) addonsSetCmd() *cobra.Command {
	var f addonFlags
	cmd := &cobra.Command{
		Use:     "set ADD-ON",
		Short:   "Change an add-on's options without turning it on or off",
		Example: "  geass addons set monitoring --profile full\n  geass addons set logging --retention-days 30",
		Args:    usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			change := f.change(cmd, nil)
			if change.Profile == nil && change.RetentionDays == nil {
				return usageError("name an option to change: --profile for monitoring, or --retention-days for logging")
			}
			return a.changeAddon(cmd, args[0], change, &f)
		},
	}
	addOptionFlags(cmd, &f)
	return cmd
}

func (a *App) addonsRetryCmd() *cobra.Command {
	var f addonFlags
	cmd := &cobra.Command{
		Use:   "retry ADD-ON",
		Short: "Start the install of a stuck add-on over",
		Long: "An add-on whose install job failed, or that K3s suspended after several changes in a row, is Failed. Retrying " +
			"deletes the stuck job, and K3s makes a new one. It does nothing for an add-on that is not stuck.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			addons, err := api.RetryAddon(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if f.wait {
				return a.waitForAddon(cmd, api, args[0], f.timeout)
			}
			return a.printAddons(addons)
		},
	}
	addWaitFlags(cmd, &f)
	return cmd
}

// changeAddon makes a change, says how the add-on stands, and with --wait waits for it to be Ready.
func (a *App) changeAddon(cmd *cobra.Command, name string, change client.AddonChange, f *addonFlags) error {
	api, _, err := a.api()
	if err != nil {
		return err
	}
	addons, err := api.SetAddon(cmd.Context(), name, change)
	if err != nil {
		return err
	}
	if f.wait && change.Enabled != nil && *change.Enabled {
		return a.waitForAddon(cmd, api, name, f.timeout)
	}
	return a.printAddons(addons)
}

// waitForAddon shows the add-on's state as it changes and ends when it is Ready or Failed.
func (a *App) waitForAddon(cmd *cobra.Command, api *client.Client, name string, timeout time.Duration) error {
	last := ""
	addon, err := api.WaitForAddon(cmd.Context(), name, client.AddonWaitOptions{Timeout: timeout, OnStatus: func(addon client.Addon) {
		line := addon.State + ": " + addon.Message
		if line != last {
			a.note("%s", strings.TrimSuffix(line, ": "))
			last = line
		}
	}})
	if err != nil {
		if errors.Is(err, client.ErrAddonFailed) && addon.CanRetry {
			return fmt.Errorf("%w. Run `geass addons retry %s` to start it again", err, name)
		}
		return err
	}
	if a.flags.json {
		return a.printJSON(addon)
	}
	a.printf("%s is ready.\n", addon.Title)
	return nil
}
