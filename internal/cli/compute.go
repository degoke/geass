package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/degoke/geass/pkg/client"
)

// Machines at clouds: the clouds Geass knows, the connections that hold the keys to an account at one, and the sizes a
// cloud offers. Pools and single servers, which spend the money, are in pools.go and servers.go.

// columnName is the heading of the column that names what a table lists.
const columnName = "NAME"

// printItems prints a list for a program: the items under "items", as every list of the API is.
func (a *App) printItems(items any) error { return a.printJSON(map[string]any{"items": items}) }

// kv prints one line of a detail view, with the labels lined up.
func (a *App) kv(label, value string) {
	if label != "" {
		label += ":"
	}
	a.printf("%-22s %s\n", label, value)
}

// ---- the clouds ----

func (a *App) providersCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "providers [NAME]",
		Aliases: []string{"clouds"},
		Short:   "List the clouds Geass can create machines at, and the credentials and settings each takes",
		Long: "List the clouds Geass can create machines at. Each one takes its own credentials (an API token, or a pair of keys), " +
			"which a connection holds: see `geass connections add`. A pool or a server can also give settings of the cloud, such as a " +
			"subnet or a firewall.\n\nGive a name to see what each credential and setting is, and what a region means there.",
		Example: "  geass providers\n  geass providers Hetzner",
		Args:    usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			providers, err := api.ComputeProviders(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 1 {
				provider, ok := findProvider(providers, args[0])
				if !ok {
					return &exitError{ExitNotFound, fmt.Errorf("there is no cloud called %q: Geass can create machines at %s", args[0], providerNames(providers))}
				}
				return a.printProvider(provider)
			}
			if a.flags.json {
				if providers == nil {
					providers = []client.ComputeProvider{}
				}
				return a.printItems(providers)
			}
			rows := make([][]string, 0, len(providers))
			for _, provider := range providers {
				rows = append(rows, []string{provider.ID, provider.Label, dash(describeFields(provider.Fields)), dash(describeSettings(provider))})
			}
			a.printTable([]string{"ID", columnName, "CREDENTIALS", "SETTINGS"}, rows)
			a.note("Run `geass providers ID` to see what each credential and setting is.")
			return nil
		},
	}
}

// findProvider finds a cloud by its ID or its name, ignoring case, so that hetzner finds Hetzner.
func findProvider(providers []client.ComputeProvider, name string) (client.ComputeProvider, bool) {
	name = strings.TrimSpace(name)
	for _, provider := range providers {
		if strings.EqualFold(provider.ID, name) || strings.EqualFold(provider.Label, name) {
			return provider, true
		}
	}
	return client.ComputeProvider{}, false
}

func providerNames(providers []client.ComputeProvider) string {
	ids := make([]string, 0, len(providers))
	for _, provider := range providers {
		ids = append(ids, provider.ID)
	}
	return strings.Join(ids, ", ")
}

// describeFields names the credentials of a cloud and marks the ones that are secret or can be left out.
func describeFields(fields []client.ProviderField) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		var marks []string
		if field.Secret {
			marks = append(marks, "secret")
		}
		if field.Optional {
			marks = append(marks, "optional")
		}
		if len(marks) > 0 {
			parts = append(parts, field.Key+" ("+strings.Join(marks, ", ")+")")
		} else {
			parts = append(parts, field.Key)
		}
	}
	return strings.Join(parts, ", ")
}

func describeSettings(provider client.ComputeProvider) string {
	if provider.FreeSettings {
		return "any"
	}
	parts := make([]string, 0, len(provider.Settings))
	for _, setting := range provider.Settings {
		if setting.Required {
			parts = append(parts, setting.Key+" (required)")
		} else {
			parts = append(parts, setting.Key)
		}
	}
	return strings.Join(parts, ", ")
}

func (a *App) printProvider(provider client.ComputeProvider) error {
	if a.flags.json {
		return a.printJSON(provider)
	}
	a.printf("%s (%s)\n%s\n", provider.Label, provider.ID, provider.Summary)
	if provider.RegionHelp != "" {
		a.printf("\nRegion: %s\n", provider.RegionHelp)
	}
	a.printf("\nCredentials, given to `geass connections add`:\n")
	for _, field := range provider.Fields {
		var marks []string
		if field.Secret {
			marks = append(marks, "secret")
		}
		if field.Optional {
			marks = append(marks, "optional")
		}
		if field.Multiline {
			marks = append(marks, "several lines: give it with --credential-file")
		}
		line := fmt.Sprintf("  %s: %s", field.Key, field.Label)
		if len(marks) > 0 {
			line += " (" + strings.Join(marks, ", ") + ")"
		}
		a.printf("%s\n", line)
		if field.Help != "" {
			a.printf("      %s\n", field.Help)
		}
	}
	switch {
	case provider.FreeSettings:
		a.printf("\nSettings, given to `geass pools create --setting KEY=VALUE`: any name; they are passed on to the service that makes the machines.\n")
	case len(provider.Settings) > 0:
		a.printf("\nSettings, given to `geass pools create --setting KEY=VALUE`:\n")
		for _, setting := range provider.Settings {
			line := fmt.Sprintf("  %s: %s", setting.Key, setting.Label)
			if setting.Required {
				line += " (required)"
			}
			a.printf("%s\n", line)
			if setting.Help != "" {
				a.printf("      %s\n", setting.Help)
			}
		}
	}
	return nil
}

// ---- sizes ----

func (a *App) sizesCmd() *cobra.Command {
	var connection, region string
	var minVCPUs, limit int
	var minMemory float64
	cmd := &cobra.Command{
		Use:   "sizes",
		Short: "List the machine sizes a cloud offers in a region, cheapest first (admins only)",
		Long: "Ask a cloud, with the credentials of a connection, which machine sizes it offers in a region, cheapest first, " +
			"and what each costs for an hour at the cloud's list price. The size names are what a pool's --size takes. " +
			"This calls the cloud with the connection's credentials, so it needs an admin's token.",
		Example: "  geass sizes --connection hetzner --region fsn1\n  geass sizes --connection aws --region eu-north-1 --min-vcpus 4 --min-memory 8 --limit 10",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if connection == "" {
				return usageError("say which connection to ask with --connection (see: geass connections list)")
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			sizes, err := api.CloudSizes(cmd.Context(), connection, region)
			if err != nil {
				return err
			}
			chosen, matched := client.SelectSizes(sizes, minVCPUs, int64(minMemory*1024), limit)
			if a.flags.json {
				return a.printItems(chosen)
			}
			rows := make([][]string, 0, len(chosen))
			for _, size := range chosen {
				rows = append(rows, []string{size.ID, strconv.Itoa(size.VCPUs), formatMemory(size.MemoryMiB), diskGB(size.DiskGB), dash(size.Arch), formatPrice(size.HourlyPrice, size.Currency)})
			}
			a.printTable([]string{"SIZE", "VCPUS", "MEMORY", "DISK", "ARCH", "PER HOUR"}, rows)
			if matched > len(chosen) {
				a.note("Showing %d of %d sizes: raise --limit to see more.", len(chosen), matched)
			}
			if len(chosen) == 0 {
				a.note("No size matched.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&connection, "connection", "", "the connection to ask the cloud with")
	cmd.Flags().StringVar(&region, "region", "", "the cloud's region or location (default: the connection's own)")
	cmd.Flags().IntVar(&minVCPUs, "min-vcpus", 0, "only sizes with at least this many processors")
	cmd.Flags().Float64Var(&minMemory, "min-memory", 0, "only sizes with at least this much memory, in GiB")
	cmd.Flags().IntVar(&limit, "limit", 0, "show at most this many sizes (default: all)")
	return cmd
}

func formatMemory(mib int64) string {
	gib := float64(mib) / 1024
	if gib == float64(int64(gib)) {
		return fmt.Sprintf("%d GiB", int64(gib))
	}
	return fmt.Sprintf("%.1f GiB", gib)
}

func diskGB(gb int) string {
	if gb == 0 {
		return "-"
	}
	return fmt.Sprintf("%d GB", gb)
}

func formatPrice(price float64, currency string) string {
	if price == 0 {
		return "-"
	}
	return strings.TrimSpace(fmt.Sprintf("%.4f %s", price, currency))
}

// ---- connections ----

func (a *App) connectionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "connections",
		Aliases: []string{"connection", "conn"},
		Short:   "Connect clouds, so that Geass can create machines there",
		Long: "A connection holds the credentials Geass uses at a cloud, stored in a Secret that only the operator reads and never " +
			"shown again. Pools and servers made at a cloud use a connection, and the cloud bills the account it belongs to: " +
			"use a project or an account of its own for Geass, and credentials that can do no more than create and delete machines.\n\n" +
			"Making, changing and removing a connection needs an admin's token.",
	}
	cmd.AddCommand(a.connectionsListCmd(), a.connectionsShowCmd(), a.connectionsAddCmd(), a.connectionsUpdateCmd(), a.connectionsRemoveCmd(), a.connectionsVerifyCmd())
	return cmd
}

func connectionStatus(c client.CloudConnection) string {
	switch {
	case c.Available:
		return "accepted"
	case c.Message != "":
		return "not accepted"
	default:
		return "not checked yet"
	}
}

func describeUse(c client.CloudConnection) string {
	var parts []string
	if n := len(c.UsedBy); n > 0 {
		parts = append(parts, plural(n, "pool"))
	}
	if c.Machines > 0 {
		parts = append(parts, plural(c.Machines, "machine"))
	}
	return strings.Join(parts, ", ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func (a *App) connectionsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the connections to clouds, and whether each cloud accepts them",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			connections, err := api.CloudConnections(cmd.Context())
			if err != nil {
				return err
			}
			if a.flags.json {
				if connections == nil {
					connections = []client.CloudConnection{}
				}
				return a.printItems(connections)
			}
			rows := make([][]string, 0, len(connections))
			for _, c := range connections {
				rows = append(rows, []string{c.Name, c.Provider, dash(strings.Join(c.Purposes, ",")), dash(c.Region), connectionStatus(c), dash(describeUse(c)), age(c.CreatedAt)})
			}
			a.printTable([]string{columnName, "PROVIDER", "PURPOSES", "REGION", "STATUS", "USED BY", "AGE"}, rows)
			return nil
		},
	}
}

func (a *App) connectionsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show NAME",
		Short: "Show a connection: what the cloud said about it, and what depends on it",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			c, err := api.GetCloudConnection(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printJSON(c)
			}
			a.kv("Name", c.Name)
			a.kv("Provider", c.Provider)
			a.kv("Purposes", dash(strings.Join(c.Purposes, ", ")))
			a.kv("Region", dash(c.Region))
			a.kv("Status", connectionStatus(c))
			if c.Message != "" {
				a.kv("Message", c.Message)
			}
			if c.Account != "" {
				a.kv("Account", c.Account)
			}
			if c.VerifiedAt != nil {
				a.kv("Last accepted", c.VerifiedAt.UTC().Format("2006-01-02 15:04 UTC")+" ("+age(*c.VerifiedAt)+" ago)")
			}
			if c.ExpiresAt != nil {
				a.kv("Credentials expire", describeExpiry(c.ExpiresAt, time.Now()))
			}
			for _, warning := range c.Warnings {
				a.kv("Warning", warning)
			}
			a.kv("Used by pools", dash(strings.Join(c.UsedBy, ", ")))
			a.kv("Machines", strconv.Itoa(c.Machines))
			a.kv("Created", age(c.CreatedAt)+" ago")
			return nil
		},
	}
}

// credentialFlags are how credentials are given to a command: on the command line, from a file, or from the
// environment. A secret should never be on the command line, where the shell's history and the list of processes keep it.
type credentialFlags struct {
	pairs, files []string
}

func (f *credentialFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&f.pairs, "credential", nil, "a credential as KEY=VALUE (repeatable). Fine for what is not secret; for a secret use --credential-file")
	cmd.Flags().StringArrayVar(&f.files, "credential-file", nil, "a credential read from a file, as KEY=PATH (repeatable); PATH - reads standard input")
}

// credentialEnvNames are the environment variables a credential can be given in: GEASS_CREDENTIAL_ and the key in
// capitals, with an underscore at each capital inside it (accessKeyId is GEASS_CREDENTIAL_ACCESS_KEY_ID), or the key in
// capitals as it is (GEASS_CREDENTIAL_ACCESSKEYID).
func credentialEnvNames(key string) []string {
	var snake strings.Builder
	for i, r := range key {
		if i > 0 && unicode.IsUpper(r) {
			snake.WriteByte('_')
		}
		snake.WriteRune(unicode.ToUpper(r))
	}
	names := []string{"GEASS_CREDENTIAL_" + snake.String()}
	if plain := "GEASS_CREDENTIAL_" + strings.ToUpper(key); plain != names[0] {
		names = append(names, plain)
	}
	return names
}

// givenCredentials collects the credentials of a cloud that were given: on the command line, in files, or in the
// environment. A key the cloud does not take is an error, because the API would drop it without a word. The values
// never appear in an error or a message.
func (a *App) givenCredentials(provider client.ComputeProvider, flags credentialFlags) (map[string]string, error) {
	values := map[string]string{}
	fromCommandLine := map[string]bool{}
	set := func(key, value string) error {
		if _, dup := values[key]; dup {
			return usageError("the credential %q is given more than once", key)
		}
		if _, known := provider.Field(key); !known {
			keys := make([]string, 0, len(provider.Fields))
			for _, field := range provider.Fields {
				keys = append(keys, field.Key)
			}
			return usageError("%s has no credential called %q: it takes %s", provider.Label, key, strings.Join(keys, ", "))
		}
		values[key] = value
		return nil
	}
	for _, pair := range flags.pairs {
		key, value, ok := strings.Cut(pair, "=")
		if key = strings.TrimSpace(key); !ok || key == "" {
			return nil, usageError("a --credential is written KEY=VALUE, such as token=...")
		}
		if err := set(key, value); err != nil {
			return nil, err
		}
		fromCommandLine[key] = true
	}
	stdinUsed := false
	for _, pair := range flags.files {
		key, path, ok := strings.Cut(pair, "=")
		if key = strings.TrimSpace(key); !ok || key == "" || path == "" {
			return nil, usageError("a --credential-file is written KEY=PATH, such as token=./token.txt (PATH - is standard input)")
		}
		var raw []byte
		var err error
		if path == "-" {
			if stdinUsed {
				return nil, usageError("only one credential can be read from standard input")
			}
			stdinUsed = true
			raw, err = io.ReadAll(a.In)
		} else {
			raw, err = os.ReadFile(path)
		}
		if err != nil {
			return nil, fmt.Errorf("could not read the file for the credential %q: %w", key, err)
		}
		// A file ends its last line, and a trailing newline is not part of a token.
		value := strings.TrimRight(string(raw), " \t\r\n")
		if value == "" {
			return nil, usageError("the file for the credential %q is empty", key)
		}
		if err := set(key, value); err != nil {
			return nil, err
		}
	}
	for _, field := range provider.Fields {
		if _, done := values[field.Key]; done {
			continue
		}
		for _, name := range credentialEnvNames(field.Key) {
			if value := strings.TrimSpace(a.Getenv(name)); value != "" {
				values[field.Key] = value
				break
			}
		}
	}
	for _, field := range provider.Fields {
		if field.Secret && fromCommandLine[field.Key] {
			a.note("warning: the credential %s was given on the command line, where your shell history and other users' process lists can see it. Next time give it with --credential-file %s=PATH, or in $%s.",
				field.Key, field.Key, credentialEnvNames(field.Key)[0])
		}
	}
	return values, nil
}

// completeCredentials asks a person for the required credentials that nobody gave, without showing a secret as it is
// typed, and fails with how to give them when it cannot ask.
func (a *App) completeCredentials(provider client.ComputeProvider, values map[string]string) error {
	var missing []client.ProviderField
	for _, field := range provider.Fields {
		if _, done := values[field.Key]; done || field.Optional {
			continue
		}
		// A key of several lines cannot be typed on one line: it comes from a file.
		if a.Interactive() && !field.Multiline {
			value, err := a.promptCredential(field)
			if err != nil {
				return err
			}
			if value != "" {
				values[field.Key] = value
				continue
			}
		}
		missing = append(missing, field)
	}
	if len(missing) == 0 {
		return nil
	}
	names := make([]string, 0, len(missing))
	for _, field := range missing {
		names = append(names, fmt.Sprintf("%s (%s)", field.Label, field.Key))
	}
	return usageError("%s needs %s. Give a secret with --credential-file KEY=PATH (PATH - is standard input) or in $%s, and any other with --credential KEY=VALUE",
		provider.Label, strings.Join(names, ", "), credentialEnvNames(missing[0].Key)[0])
}

// promptCredential asks a person for one credential. A secret is not shown as it is typed.
func (a *App) promptCredential(field client.ProviderField) (string, error) {
	prompt := fmt.Sprintf("%s (%s): ", field.Label, field.Key)
	if field.Secret {
		if a.ReadSecret == nil {
			return "", nil
		}
		value, err := a.ReadSecret(prompt)
		return strings.TrimSpace(value), err
	}
	_, _ = fmt.Fprint(a.Err, prompt)
	line, err := bufio.NewReader(a.In).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// normalizePurposes spells the purposes of a connection the way the API does.
func normalizePurposes(given []string) ([]string, error) {
	var purposes []string
	for _, word := range given {
		var purpose string
		switch strings.ToLower(strings.TrimSpace(word)) {
		case "compute":
			purpose = "Compute"
		case "storage":
			purpose = "Storage"
		case "database":
			purpose = "Database"
		default:
			return nil, usageError("%q is not a purpose: use Compute, Storage or Database", word)
		}
		if !slices.Contains(purposes, purpose) {
			purposes = append(purposes, purpose)
		}
	}
	return purposes, nil
}

func (a *App) connectionsAddCmd() *cobra.Command {
	var provider, region string
	var purposes []string
	var credentials credentialFlags
	cmd := &cobra.Command{
		Use:   "add NAME --provider CLOUD",
		Short: "Connect a cloud: store the credentials Geass uses there",
		Long: "Connect a cloud so that Geass can create machines there. The credentials a cloud takes are listed by `geass providers`.\n\n" +
			"Give each credential without putting a secret where others can read it: --credential-file KEY=PATH reads it from a file " +
			"(PATH - is standard input), and $GEASS_CREDENTIAL_KEY is read too, with an underscore at each capital in the key " +
			"(GEASS_CREDENTIAL_TOKEN, GEASS_CREDENTIAL_ACCESS_KEY_ID). --credential KEY=VALUE also works, but whatever is typed on a " +
			"command line stays in the shell's history. A required secret that is still missing is asked for, without showing it, when " +
			"a person is at the keyboard.\n\n" +
			"The cloud is asked at once whether it accepts the credentials. Use credentials that can do no more than create and delete " +
			"machines, in a project or an account Geass has to itself: the cloud bills that account for every machine a pool makes.",
		Example: "  geass connections add hetzner --provider Hetzner --credential-file token=./hetzner-token.txt\n" +
			"  GEASS_CREDENTIAL_TOKEN=$(pass hetzner/geass) geass connections add hetzner --provider Hetzner\n" +
			"  geass connections add aws --provider AWS --region eu-north-1 --credential accessKeyId=AKIA... --credential-file secretAccessKey=./secret.txt",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			providers, err := api.ComputeProviders(cmd.Context())
			if err != nil {
				return err
			}
			if strings.TrimSpace(provider) == "" {
				return usageError("say which cloud with --provider: %s (see: geass providers)", providerNames(providers))
			}
			cloud, ok := findProvider(providers, provider)
			if !ok {
				return usageError("Geass cannot create machines at %q: it can at %s", provider, providerNames(providers))
			}
			wanted, err := normalizePurposes(purposes)
			if err != nil {
				return err
			}
			if len(wanted) == 0 {
				wanted = []string{"Compute"}
			}
			if !slices.Contains(wanted, "Compute") {
				return usageError("this command makes connections that create machines, so Compute has to be one of the purposes; connections for storage or databases are made in the dashboard")
			}
			// AWS names a default region among its credentials, and the API reads one field for it and for the connection's own.
			if _, has := cloud.Field("region"); has && region != "" && !slices.ContainsFunc(credentials.pairs, func(pair string) bool { return strings.HasPrefix(pair, "region=") }) {
				credentials.pairs = append(slices.Clone(credentials.pairs), "region="+region)
			}
			values, err := a.givenCredentials(cloud, credentials)
			if err != nil {
				return err
			}
			if err := a.completeCredentials(cloud, values); err != nil {
				return err
			}
			if given, ok := values["region"]; ok && region != "" && given != region {
				return usageError("--region and the credential region disagree: they are the same setting")
			}
			// The API names a connection in lowercase, so it is read back by the name it was given.
			name := strings.ToLower(strings.TrimSpace(args[0]))
			if err := api.CreateCloudConnection(cmd.Context(), client.CloudConnectionRequest{Name: name, Provider: cloud.ID, Purposes: wanted, Region: region, Credentials: values}); err != nil {
				return err
			}
			a.printf("Added the connection %s to %s. Asking the cloud whether it accepts the credentials...\n", name, cloud.Label)
			return a.checkConnection(cmd.Context(), api, name, client.CloudConnection{})
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "the cloud (see: geass providers)")
	cmd.Flags().StringVar(&region, "region", "", "the connection's default region, used when a pool or a server names none")
	cmd.Flags().StringSliceVar(&purposes, "purpose", nil, "what it is for: Compute (the default), and for AWS also Storage and Database; repeatable")
	credentials.add(cmd)
	return cmd
}

// checkConnection waits a few seconds for the operator to say whether the cloud accepts a connection, and tells the
// person. before is the connection as it was before the check was asked for, so that an earlier answer is not taken for
// the new one. A cloud that refuses the credentials is an error, since the connection cannot be used until they are
// fixed; one that has not answered yet is only said.
func (a *App) checkConnection(ctx context.Context, api *client.Client, name string, before client.CloudConnection) error {
	var latest client.CloudConnection
	for range 20 {
		current, err := api.GetCloudConnection(ctx, name)
		if err != nil {
			return err
		}
		latest = current
		acceptedAgain := current.VerifiedAt != nil && (before.VerifiedAt == nil || current.VerifiedAt.After(*before.VerifiedAt))
		refusedAgain := !current.Available && current.Message != "" && (before.Available || current.Message != before.Message)
		switch {
		case acceptedAgain && current.Available:
			a.printf("%s accepts the credentials", current.Provider)
			if current.Account != "" {
				a.printf(" (account: %s)", current.Account)
			}
			a.printf(".\n")
			for _, warning := range current.Warnings {
				a.note("warning: %s", warning)
			}
			return nil
		case refusedAgain:
			return fmt.Errorf("%s did not accept the connection %s: %s. Change the credentials with `geass connections update %s`", current.Provider, name, current.Message, name)
		}
		a.Sleep(time.Second)
	}
	a.note("The cloud has not answered yet. See what it said with `geass connections show %s`.", name)
	if latest.Message != "" {
		a.note("Last known: %s", latest.Message)
	}
	return nil
}

func (a *App) connectionsUpdateCmd() *cobra.Command {
	var region string
	var purposes []string
	var credentials credentialFlags
	cmd := &cobra.Command{
		Use:   "update NAME",
		Short: "Change a connection's region or purposes, or replace credentials",
		Long: "Change a connection. What you do not name keeps its value, so one credential can be replaced without giving the others " +
			"again. Credentials are given as for `geass connections add` (--credential-file KEY=PATH, $GEASS_CREDENTIAL_KEY or " +
			"--credential KEY=VALUE), and nothing is asked for. After replacing credentials the cloud is asked at once whether it accepts them.",
		Example: "  geass connections update hetzner --credential-file token=./new-token.txt\n  geass connections update hetzner --region nbg1",
		Args:    usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			var change client.CloudConnectionChange
			if cmd.Flags().Changed("region") {
				change.Region = &region
			}
			if cmd.Flags().Changed("purpose") {
				wanted, err := normalizePurposes(purposes)
				if err != nil {
					return err
				}
				if len(wanted) == 0 {
					return usageError("--purpose needs at least one purpose: Compute, Storage or Database")
				}
				change.Purposes = &wanted
			}
			current, err := api.GetCloudConnection(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			providers, err := api.ComputeProviders(cmd.Context())
			if err != nil {
				return err
			}
			cloud, ok := findProvider(providers, current.Provider)
			if !ok {
				return fmt.Errorf("this connection is for %s, where Geass cannot create machines, so its credentials cannot be replaced here", current.Provider)
			}
			// Nothing is required of an update: a credential that nobody gave is one that is not changing.
			change.Credentials, err = a.givenCredentials(cloud, credentials)
			if err != nil {
				return err
			}
			if change.IsEmpty() {
				return usageError("name something to change: --region, --purpose, or a credential with --credential-file KEY=PATH")
			}
			if err := api.UpdateCloudConnection(cmd.Context(), args[0], change); err != nil {
				return err
			}
			a.printf("Updated the connection %s.\n", args[0])
			if len(change.Credentials) == 0 {
				return nil
			}
			keys := make([]string, 0, len(change.Credentials))
			for key := range change.Credentials {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			a.printf("Replaced the credentials: %s. Asking the cloud whether it accepts them...\n", strings.Join(keys, ", "))
			if err := api.VerifyCloudConnection(cmd.Context(), args[0]); err != nil {
				return err
			}
			return a.checkConnection(cmd.Context(), api, args[0], current)
		},
	}
	cmd.Flags().StringVar(&region, "region", "", "the connection's default region; empty clears it")
	cmd.Flags().StringSliceVar(&purposes, "purpose", nil, "what it is for, replacing what it was: Compute, Storage, Database; repeatable")
	credentials.add(cmd)
	return cmd
}

func (a *App) connectionsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove NAME",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove a connection and its stored credentials",
		Long: "Remove a connection and the Secret that holds its credentials. Geass refuses while a pool or a machine still uses it: " +
			"delete those first, so that their machines are deleted at the cloud while the connection can still do it. " +
			"Removing it does not revoke the credentials at the cloud: do that there as well.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.confirm(fmt.Sprintf("Remove the connection %q and its stored credentials?", args[0])); err != nil {
				return err
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			if err := api.DeleteCloudConnection(cmd.Context(), args[0]); err != nil {
				return err
			}
			a.printf("Removed the connection %s. Revoke its credentials at the cloud too, if they are no longer needed.\n", args[0])
			return nil
		},
	}
}

func (a *App) connectionsVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify NAME",
		Short: "Ask the cloud again whether it accepts a connection's credentials",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			before, err := api.GetCloudConnection(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := api.VerifyCloudConnection(cmd.Context(), args[0]); err != nil {
				return err
			}
			return a.checkConnection(cmd.Context(), api, args[0], before)
		},
	}
}
