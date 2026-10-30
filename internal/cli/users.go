package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// The people who sign in to the dashboard with a password. A password is never taken as an argument, where `ps` and the
// shell history would show it: it is read from a file or from standard input, or Geass makes one up and prints it once.

const flagPasswordFile = "password-file"

func (a *App) usersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "users",
		Aliases: []string{"user"},
		Short:   "Manage who signs in to the dashboard with a password",
		Long: "List, add, change and remove the users who sign in to the dashboard with a password, and sign everyone out. It is the " +
			"same as Settings > Users in the dashboard. A user is an admin, who can change anything, or a viewer, who can only look.\n\n" +
			"These commands need an install-wide admin's token (`geass login` as an admin). API tokens are not users: they are managed " +
			"in the dashboard. Where an identity provider decides who signs in, people and their roles are managed there, and only " +
			"an admin can still use a password.",
	}
	cmd.AddCommand(a.usersListCmd(), a.usersAddCmd(), a.usersSetRoleCmd(), a.usersResetPasswordCmd(), a.usersRemoveCmd(), a.usersSignOutAllCmd())
	return cmd
}

func (a *App) usersListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the users and their roles",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, _, err := a.api()
			if err != nil {
				return err
			}
			users, err := api.Users(cmd.Context())
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.printItems(users.Items)
			}
			rows := make([][]string, 0, len(users.Items))
			for _, user := range users.Items {
				rows = append(rows, []string{user.Username, user.Role})
			}
			a.printTable([]string{"USERNAME", "ROLE"}, rows)
			if users.IdentityProvider {
				a.note("An identity provider decides who signs in: only an admin can use a password.")
			}
			return nil
		},
	}
}

func (a *App) usersAddCmd() *cobra.Command {
	var role, passwordFile string
	cmd := &cobra.Command{
		Use:   "add USERNAME",
		Short: "Add a user, with a password Geass makes up unless you give one",
		Long: "Add a user. Without --password-file Geass makes up a password and prints it: it is shown once and cannot be read again, " +
			"only reset. With --password-file the password is read from that file (- is standard input) and not printed. " +
			"A password has 12 to 72 characters. A username may use letters, digits and the characters . _ @ + -.",
		Example: "  geass users add mara --role admin\n" +
			"  printf '%s' \"$PASSWORD\" | geass users add ops --role viewer --password-file -",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, err := normalizeChoice("role", role, "admin", "viewer")
			if err != nil {
				return err
			}
			password, err := a.readPassword(passwordFile)
			if err != nil {
				return err
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			generated, err := api.AddUser(cmd.Context(), args[0], role, password)
			if err != nil {
				return err
			}
			return a.reportUser(args[0], role, generated, "Added")
		},
	}
	cmd.Flags().StringVar(&role, "role", "viewer", "admin or viewer")
	cmd.Flags().StringVar(&passwordFile, flagPasswordFile, "", "read the password from this file (- is standard input) instead of making one up")
	return cmd
}

func (a *App) usersSetRoleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-role USERNAME ROLE",
		Short: "Make a user an admin or a viewer",
		Long:  "Change a user's role to admin or viewer. It applies on the user's next request, with no new sign-in. The last admin cannot be made a viewer.",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, err := normalizeChoice("role", args[1], "admin", "viewer")
			if err != nil {
				return usageError("the role is admin or viewer, not %q", args[1])
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			if err := api.SetUserRole(cmd.Context(), args[0], role); err != nil {
				return err
			}
			a.printf("%s is now %s.\n", args[0], aOrAn(role))
			return nil
		},
	}
}

func aOrAn(role string) string {
	if role == "admin" {
		return "an admin"
	}
	return "a viewer"
}

func (a *App) usersResetPasswordCmd() *cobra.Command {
	var passwordFile string
	cmd := &cobra.Command{
		Use:   "reset-password USERNAME",
		Short: "Set a new password for a user and sign them out",
		Long: "Set a new password for a user. Without --password-file Geass makes one up and prints it once. The user is signed out " +
			"everywhere, and the old password stops working.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			password, err := a.readPassword(passwordFile)
			if err != nil {
				return err
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			generated, err := api.ResetUserPassword(cmd.Context(), args[0], password)
			if err != nil {
				return err
			}
			return a.reportUser(args[0], "", generated, "Reset the password of")
		},
	}
	cmd.Flags().StringVar(&passwordFile, flagPasswordFile, "", "read the password from this file (- is standard input) instead of making one up")
	return cmd
}

func (a *App) usersRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove USERNAME",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove a user and sign them out",
		Long:    "Remove a user. They are signed out everywhere. The last admin cannot be removed.",
		Args:    usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.confirm(fmt.Sprintf("Remove the user %q? They are signed out and cannot sign in again.", args[0])); err != nil {
				return err
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			if err := api.RemoveUser(cmd.Context(), args[0]); err != nil {
				return err
			}
			a.printf("Removed the user %s.\n", args[0])
			return nil
		},
	}
}

func (a *App) usersSignOutAllCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sign-out-all",
		Short: "Sign everyone out of the dashboard",
		Long: "End every dashboard session, of every user and of everyone who signed in through an identity provider. People sign in " +
			"again. API tokens, including the one this command line uses, keep working.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.confirm("Sign everyone out of the dashboard?"); err != nil {
				return err
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			if err := api.SignOutEveryone(cmd.Context()); err != nil {
				return err
			}
			a.printf("Signed everyone out of the dashboard.\n")
			return nil
		},
	}
}

// readPassword is the password in a file, or "" when no file is named, which asks Geass to make one up.
func (a *App) readPassword(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	stdinUsed := false
	raw, err := a.readFileOrStdin(path, &stdinUsed)
	if err != nil {
		return "", fmt.Errorf("could not read the password: %w", err)
	}
	// A file ends in a newline that is not part of the password.
	password := strings.TrimRight(string(raw), "\r\n")
	if password == "" {
		return "", errors.New("the password file is empty")
	}
	return password, nil
}

// reportUser says what was done, and prints a password that Geass made up: on standard output, so a script can take it, and
// with the warning on standard error.
func (a *App) reportUser(username, role, generated, did string) error {
	if a.flags.json {
		out := map[string]any{"username": username}
		if role != "" {
			out["role"] = role
		}
		if generated != "" {
			out["password"] = generated
		}
		return a.printJSON(out)
	}
	if generated == "" {
		a.printf("%s %s.\n", did, username)
		return nil
	}
	a.note("%s %s. The password is shown once and cannot be read again:", did, username)
	a.printf("%s\n", generated)
	return nil
}
