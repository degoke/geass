package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/degoke/geass/pkg/client"
)

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// openBrowser opens an address in the person's browser, where there is one.
func openBrowser(address string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", address)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	default:
		command = exec.Command("xdg-open", address)
	}
	return command.Start()
}

func (a *App) loginCmd() *cobra.Command {
	var tokenStdin, noBrowser bool
	var name string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to a Geass dashboard",
		Long: "Sign in to a Geass dashboard.\n\n" +
			"With no token, geass shows a code and opens the dashboard, where you approve it; nothing secret is typed here.\n" +
			"For a script, give a token with --token, $GEASS_TOKEN, or --token-stdin (create one in the dashboard under Settings > Tokens).",
		Example: "  geass login --url https://geass.example.com\n  echo \"$TOKEN\" | geass login --url https://geass.example.com --token-stdin",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			config, path, err := a.loadConfig()
			if err != nil {
				return err
			}
			resolved := config.Resolve(Resolved{URL: a.flags.url, Token: a.flags.token}, a.flags.context, a.Getenv)
			if resolved.URL == "" {
				return usageError("say which Geass to sign in to: geass login --url https://your-geass-address")
			}
			// A token is only taken from where the person put one for this login: a flag, $GEASS_TOKEN, or
			// standard input. One saved from an earlier login is the very thing they are signing in again for.
			token := firstSet(a.flags.token, a.Getenv("GEASS_TOKEN"))
			if tokenStdin {
				line, _ := bufio.NewReader(a.In).ReadString('\n')
				token = strings.TrimSpace(line)
				if token == "" {
					return usageError("--token-stdin was given and nothing was on standard input")
				}
			}
			if token == "" {
				token, err = a.deviceLogin(cmd.Context(), resolved.URL, noBrowser)
				if err != nil {
					return err
				}
			}
			api := a.NewClient(resolved.URL, token)
			me, err := api.Me(cmd.Context())
			if err != nil {
				return fmt.Errorf("could not sign in to %s: %w", resolved.URL, err)
			}
			contextID := firstSet(name, a.flags.context, contextName(resolved.URL))
			saved := config.Contexts[contextID]
			saved.URL, saved.Token, saved.User, saved.Role = resolved.URL, token, me.Username, me.Role
			saved.ExpiresAt = me.ExpiresAt
			if config.Contexts == nil {
				config.Contexts = map[string]Context{}
			}
			config.Contexts[contextID] = saved
			config.Current = contextID
			if err := config.Save(path); err != nil {
				return fmt.Errorf("signed in, but could not save the login: %w", err)
			}
			if a.flags.json {
				signedIn := map[string]any{"context": contextID, "url": resolved.URL, "user": me.Username, "role": me.Role}
				if me.ExpiresAt != nil {
					signedIn["expiresAt"] = me.ExpiresAt
				}
				return a.printJSON(signedIn)
			}
			a.printf("Signed in to %s as %s (%s)\n", resolved.URL, me.Username, describeRole(me))
			return nil
		},
	}
	cmd.Flags().BoolVar(&tokenStdin, "token-stdin", false, "read the token from standard input")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "do not open the browser; print the address instead")
	cmd.Flags().StringVar(&name, "name", "", "what to call this login (defaults to the address)")
	return cmd
}

// deviceLogin signs in by having a person approve a code in the dashboard.
func (a *App) deviceLogin(ctx context.Context, address string, noBrowser bool) (string, error) {
	api := a.NewClient(address, "")
	host, _ := os.Hostname()
	login, err := api.StartDeviceLogin(ctx, "geass CLI on "+firstSet(host, "this machine"))
	if err != nil {
		return "", fmt.Errorf("could not start the sign-in: %w", err)
	}
	a.note("To sign in, open:\n\n    %s\n\nand check that the code is  %s", login.VerificationURI, login.UserCode)
	if !noBrowser && a.OpenBrowser != nil && a.Interactive() {
		if err := a.OpenBrowser(login.VerificationURIComplete); err != nil {
			a.note("(could not open the browser: %v)", err)
		}
	}
	a.note("\nWaiting for approval...")

	interval := time.Duration(max(login.Interval, 1)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, time.Duration(max(login.ExpiresIn, 60))*time.Second)
	defer cancel()
	for {
		a.Sleep(interval)
		token, err := api.PollDeviceLogin(ctx, login.DeviceCode)
		switch client.ErrorCode(err) {
		case "":
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return "", &exitError{ExitTimeout, errors.New("the sign-in was not approved in time; run `geass login` again")}
				}
				return "", err
			}
			return token.Token, nil
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return "", &exitError{ExitDenied, errors.New("the sign-in was refused in the dashboard")}
		case "expired_token":
			return "", &exitError{ExitTimeout, errors.New("the sign-in expired; run `geass login` again")}
		default:
			return "", err
		}
		if ctx.Err() != nil {
			return "", &exitError{ExitTimeout, errors.New("the sign-in was not approved in time; run `geass login` again")}
		}
	}
}

// describeExpiry says when a token stops working: the moment, and how long from now, or that it never does.
func describeExpiry(expires *time.Time, now time.Time) string {
	if expires == nil {
		return "never"
	}
	when := expires.UTC().Format("2006-01-02 15:04 UTC")
	remaining := expires.Sub(now)
	switch {
	case remaining <= 0:
		return fmt.Sprintf("%s (expired %s ago)", when, age(*expires))
	case remaining < time.Hour:
		return fmt.Sprintf("%s (in %d minutes)", when, int(remaining.Minutes()))
	case remaining < 48*time.Hour:
		return fmt.Sprintf("%s (in %d hours)", when, int(remaining.Hours()))
	default:
		return fmt.Sprintf("%s (in %d days)", when, int(remaining.Hours()/24))
	}
}

func describeRole(me client.Identity) string {
	if me.Scope == "project" {
		return fmt.Sprintf("project token for %s/%s", me.Project, me.Environment)
	}
	switch me.Role {
	case "admin":
		return "admin"
	case "observer":
		return "read-only, with logs"
	default:
		return "read-only"
	}
}

func (a *App) logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Forget the saved token for the current login",
		Long:  "Forget the saved token. The token itself stays valid until it expires; revoke it in the dashboard (Settings > Tokens) to end it now.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			config, path, err := a.loadConfig()
			if err != nil {
				return err
			}
			name := firstSet(a.flags.context, a.Getenv("GEASS_CONTEXT"), config.Current)
			saved, ok := config.Contexts[name]
			if !ok {
				a.printf("Not signed in.\n")
				return nil
			}
			saved.Token, saved.User, saved.Role, saved.ExpiresAt = "", "", "", nil
			config.Contexts[name] = saved
			if err := config.Save(path); err != nil {
				return err
			}
			a.printf("Signed out of %s.\n", saved.URL)
			return nil
		},
	}
}

func (a *App) whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show who you are signed in as, and what you may do",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, resolved, err := a.api()
			if err != nil {
				return err
			}
			me, err := api.Me(cmd.Context())
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printJSON(me)
			}
			a.printf("%s on %s\n", me.Username, resolved.URL)
			a.printf("access: %s\n", describeRole(me))
			if me.CanMutate {
				a.printf("can change things: yes\n")
			} else {
				a.printf("can change things: no\n")
			}
			// A token that never expires says so, so its silence is not read as not knowing.
			if me.Authentication == "token" {
				a.printf("expires: %s\n", describeExpiry(me.ExpiresAt, time.Now()))
			}
			return nil
		},
	}
}

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the version of this command line and of the Geass it talks to",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			server := ""
			if resolved, err := a.resolved(); err == nil && resolved.URL != "" {
				if version, err := a.NewClient(resolved.URL, resolved.Token).Version(cmd.Context()); err == nil {
					server = version
				}
			}
			if a.flags.json {
				return a.printJSON(map[string]string{"cli": a.Version, "api": server})
			}
			a.printf("geass %s\n", a.Version)
			if server != "" {
				a.printf("api %s\n", server)
			}
			return nil
		},
	}
}

func (a *App) configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Manage saved logins and defaults"}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "get-contexts",
			Short: "List saved logins",
			Args:  cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				config, _, err := a.loadConfig()
				if err != nil {
					return err
				}
				if a.flags.json {
					return a.printJSON(redacted(config))
				}
				rows := make([][]string, 0, len(config.Contexts))
				for _, name := range config.ContextNames() {
					saved := config.Contexts[name]
					current := ""
					if name == config.Current {
						current = "*"
					}
					expires := "-"
					if saved.ExpiresAt != nil {
						expires = saved.ExpiresAt.UTC().Format("2006-01-02")
					}
					rows = append(rows, []string{current, name, saved.URL, dash(saved.User), dash(saved.Project), dash(saved.Environment), expires})
				}
				a.printTable([]string{"", "NAME", "URL", "USER", "PROJECT", "ENVIRONMENT", "EXPIRES"}, rows)
				return nil
			},
		},
		&cobra.Command{
			Use:   "use-context NAME",
			Short: "Switch to another saved login",
			Args:  usageArgs(cobra.ExactArgs(1)),
			RunE: func(_ *cobra.Command, args []string) error {
				config, path, err := a.loadConfig()
				if err != nil {
					return err
				}
				if _, ok := config.Contexts[args[0]]; !ok {
					return &exitError{ExitNotFound, fmt.Errorf("no saved login called %q (have: %s)", args[0], strings.Join(config.ContextNames(), ", "))}
				}
				config.Current = args[0]
				if err := config.Save(path); err != nil {
					return err
				}
				a.printf("Using %s.\n", args[0])
				return nil
			},
		},
		a.setContextCmd(),
	)
	return cmd
}

func (a *App) setContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-context [NAME]",
		Short: "Set the default project and environment for a login (--project, --environment)",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(_ *cobra.Command, args []string) error {
			config, path, err := a.loadConfig()
			if err != nil {
				return err
			}
			name := firstSet(strings.Join(args, ""), a.flags.context, config.Current)
			saved, ok := config.Contexts[name]
			if !ok {
				return &exitError{ExitNotFound, fmt.Errorf("no saved login called %q; sign in first", name)}
			}
			if a.flags.project != "" {
				saved.Project = a.flags.project
			}
			if a.flags.environment != "" {
				saved.Environment = a.flags.environment
			}
			config.Contexts[name] = saved
			if err := config.Save(path); err != nil {
				return err
			}
			a.printf("%s now defaults to %s / %s.\n", name, dash(saved.Project), dash(saved.Environment))
			return nil
		},
	}
}

// redacted is the configuration without its tokens, which are not for printing.
func redacted(config Config) Config {
	out := Config{Current: config.Current, Contexts: map[string]Context{}}
	for name, saved := range config.Contexts {
		if saved.Token != "" {
			saved.Token = "(saved)"
		}
		out.Contexts[name] = saved
	}
	return out
}

// usageArgs makes an argument-count mistake a usage error.
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return &exitError{ExitUsage, err}
		}
		return nil
	}
}
