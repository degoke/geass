// Package cli is the geass command line. It is a thin layer over pkg/client: every command is one or two calls
// to the v1 API, printed for a person (a table) or for a program (--json).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/degoke/geass/pkg/client"
)

// Exit codes. A script can tell what kind of failure it had from the code alone.
const (
	ExitOK           = 0
	ExitError        = 1
	ExitUsage        = 2
	ExitNotFound     = 3
	ExitDenied       = 4
	ExitDeployFailed = 5
	ExitTimeout      = 6
)

// App is the command line and everything it touches outside the process: where it prints, what it reads,
// how it reaches the API. Tests replace the parts that would reach the real world.
type App struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	Getenv func(string) string
	// ConfigPath is the configuration file; empty finds the default.
	ConfigPath string
	// NewClient makes the API client. Tests use it to point the command line at a test server.
	NewClient func(url, token string) *client.Client
	// OpenBrowser opens an address for the person; nil never does.
	OpenBrowser func(url string) error
	// Interactive says whether a person is at the keyboard, which is what lets a command ask a question.
	Interactive func() bool
	// Sleep waits; tests replace it so they do not.
	Sleep func(time.Duration)
	// Version is the command line's own version.
	Version string
	// PollInterval is how often --wait reads a service's status; zero uses the client's default.
	PollInterval time.Duration

	// Set by the flags of each run.
	flags struct {
		context, url, token, project, environment string
		json, yes                                 bool
	}
}

// NewApp is the command line as it runs for a person.
func NewApp(version string) *App {
	return &App{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Getenv: os.Getenv,
		NewClient: func(url, token string) *client.Client {
			c := client.New(url, token)
			c.UserAgent = "geass-cli/" + version
			return c
		},
		OpenBrowser: openBrowser,
		Interactive: stdinIsTerminal,
		Sleep:       time.Sleep,
		Version:     version,
	}
}

// exitError is an error that ends the program with a particular code.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func usageError(format string, args ...any) error {
	return &exitError{ExitUsage, fmt.Errorf(format, args...)}
}

// exitCodeFor is the code a failure ends the program with.
func exitCodeFor(err error) int {
	var coded *exitError
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &coded):
		return coded.code
	case client.IsNotFound(err):
		return ExitNotFound
	case client.IsUnauthorized(err), client.IsForbidden(err):
		return ExitDenied
	case errors.Is(err, client.ErrDeployFailed):
		return ExitDeployFailed
	case errors.Is(err, context.DeadlineExceeded):
		return ExitTimeout
	default:
		return ExitError
	}
}

// Run runs the command line with arguments, and returns the code to exit with.
func (a *App) Run(ctx context.Context, args []string) int {
	root := a.Root()
	root.SetArgs(args)
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	root.SetIn(a.In)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	code := exitCodeFor(err)
	if code == ExitError && isUsageError(err) {
		code = ExitUsage
	}
	_, _ = fmt.Fprintln(a.Err, "Error:", friendly(err))
	if code == ExitDenied && client.IsUnauthorized(err) {
		_, _ = fmt.Fprintln(a.Err, "Run `geass login` to sign in again.")
	}
	return code
}

// isUsageError spots the errors cobra makes for a command used wrongly, which have no type of their own.
func isUsageError(err error) bool {
	message := err.Error()
	for _, marker := range []string{"unknown command", "unknown flag", "unknown shorthand flag", "required flag", "accepts ", "requires at least", "requires at most", "invalid argument"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// friendly turns an error into what a person should read.
func friendly(err error) string {
	var apiErr *client.Error
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden {
		return apiErr.Message + " (your token may not do this)"
	}
	return err.Error()
}

// Root builds the command tree.
func (a *App) Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "geass",
		Short:         "Deploy and run services on Geass",
		Long:          "geass deploys and runs services, databases and buckets on a Geass cluster, and lets scripts and agents do the same.\n\nSign in with `geass login`. Every command takes --json for output a program can read.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       a.Version,
	}
	pf := root.PersistentFlags()
	pf.StringVar(&a.flags.context, "context", "", "which saved login to use (see: geass config get-contexts)")
	pf.StringVar(&a.flags.url, "url", "", "the Geass address, overriding the saved one ($GEASS_URL)")
	pf.StringVar(&a.flags.token, "token", "", "an API token, overriding the saved one ($GEASS_TOKEN)")
	pf.StringVarP(&a.flags.project, "project", "p", "", "the project ($GEASS_PROJECT)")
	pf.StringVarP(&a.flags.environment, "environment", "e", "", "the environment, such as production ($GEASS_ENVIRONMENT)")
	pf.BoolVar(&a.flags.json, "json", false, "print JSON, for programs")
	pf.BoolVarP(&a.flags.yes, "yes", "y", false, "do not ask before deleting")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &exitError{ExitUsage, err} })

	root.AddCommand(
		a.loginCmd(), a.logoutCmd(), a.whoamiCmd(), a.versionCmd(), a.configCmd(),
		a.projectsCmd(), a.appsCmd(), a.databasesCmd(), a.storesCmd(), a.nodesCmd(), a.complianceCmd(),
		a.deployCmd(), a.statusCmd(), a.logsCmd(), a.envCmd(), a.scaleCmd(), a.rollbackCmd(), a.metricsCmd(),
		a.functionsCmd(), a.apiCmd(), a.openapiCmd(), a.mcpCmd(),
	)
	return root
}

// ---- configuration and clients ----

func (a *App) configPath() (string, error) {
	if a.ConfigPath != "" {
		return a.ConfigPath, nil
	}
	return ConfigPath(a.Getenv)
}

func (a *App) loadConfig() (Config, string, error) {
	path, err := a.configPath()
	if err != nil {
		return Config{}, "", err
	}
	config, err := LoadConfig(path)
	return config, path, err
}

// resolved combines the flags, the environment and the saved login.
func (a *App) resolved() (Resolved, error) {
	config, _, err := a.loadConfig()
	if err != nil {
		return Resolved{}, err
	}
	return config.Resolve(Resolved{URL: a.flags.url, Token: a.flags.token, Project: a.flags.project, Environment: a.flags.environment}, a.flags.context, a.Getenv), nil
}

// api returns the client for the login in use, or an error that says how to get one.
func (a *App) api() (*client.Client, Resolved, error) {
	resolved, err := a.resolved()
	if err != nil {
		return nil, resolved, err
	}
	if resolved.URL == "" {
		return nil, resolved, &exitError{ExitDenied, errors.New("not signed in: run `geass login --url https://your-geass-address`")}
	}
	if resolved.Token == "" {
		return nil, resolved, &exitError{ExitDenied, fmt.Errorf("not signed in to %s: run `geass login`", resolved.URL)}
	}
	return a.NewClient(resolved.URL, resolved.Token), resolved, nil
}

// ---- printing ----

func (a *App) printJSON(value any) error {
	encoder := json.NewEncoder(a.Out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// printTable prints rows under headers, aligned, the way kubectl does.
func (a *App) printTable(headers []string, rows [][]string) {
	writer := tabwriter.NewWriter(a.Out, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, strings.Join(headers, "\t"))
	for _, row := range rows {
		_, _ = fmt.Fprintln(writer, strings.Join(row, "\t"))
	}
	_ = writer.Flush()
}

func (a *App) printf(format string, args ...any) { _, _ = fmt.Fprintf(a.Out, format, args...) }

// note prints a line of progress to the error stream, so it never gets mixed into output a program reads.
func (a *App) note(format string, args ...any) { _, _ = fmt.Fprintf(a.Err, format+"\n", args...) }

// confirm asks before something that cannot be undone. A script has to say --yes: with nobody to ask, doing
// it quietly would be the dangerous default.
func (a *App) confirm(question string) error {
	if a.flags.yes {
		return nil
	}
	if !a.Interactive() {
		return usageError("this deletes data; run it again with --yes to confirm")
	}
	a.note("%s [y/N]", question)
	var answer string
	if _, err := fmt.Fscanln(a.In, &answer); err != nil || !strings.EqualFold(strings.TrimSpace(answer), "y") && !strings.EqualFold(strings.TrimSpace(answer), "yes") {
		return &exitError{ExitError, errors.New("cancelled")}
	}
	return nil
}

func dash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func age(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	elapsed := time.Since(t)
	switch {
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds", int(elapsed.Seconds()))
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 48*time.Hour:
		return fmt.Sprintf("%dh", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("%dd", int(elapsed.Hours()/24))
	}
}
