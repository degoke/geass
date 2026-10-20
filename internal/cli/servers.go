package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/degoke/geass/pkg/client"
)

// Servers added on their own: one machine made at a cloud, or one you have and Geass joins over SSH. A machine made at a
// cloud costs money until the server is removed. A pool is the way to have machines come and go with the load.

func (a *App) serversCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "servers",
		Aliases: []string{"server"},
		Short:   "Add a single server to the cluster, or remove one that was added",
		Long: "Add one worker to the cluster: a machine Geass creates at a cloud (--connection and --size), or a server you already " +
			"have, which Geass joins over SSH (--address and --ssh-user). A machine made at a cloud costs money until the server is " +
			"removed. For machines that come and go with the load, make a pool instead (`geass pools create`).\n\n" +
			"The servers of the cluster, whether added here, in a pool or with the cluster, are listed by `geass nodes list`. " +
			"Adding and removing servers needs an admin's token.",
	}
	cmd.AddCommand(a.serversAddCmd(), a.serversRemoveCmd())
	return cmd
}

func (a *App) serversAddCmd() *cobra.Command {
	var connection, region, size, image, address, sshUser, sshKeyFile, sshKeySecret, knownHostsFile, storage string
	var placementRegion, placementZone string
	var settings, tags, reservedTags []string
	var sshPort int
	cmd := &cobra.Command{
		Use:   "add [NAME]",
		Short: "Add a server: a machine made at a cloud, or one you have",
		Long: "Add a server. With --address it is a server you have, which Geass reaches over SSH as --ssh-user (root, or a user that can " +
			"sudo without a password) with the key in --ssh-key-file, and a NAME is required. Without it, Geass creates a machine at the cloud of " +
			"--connection with --size, and makes up a name when you give none: it costs money until the server is removed.\n\n" +
			"Storage is allowed on a single server unless you say --storage none.",
		Example: "  geass servers add --connection hetzner --region fsn1 --size cx23\n" +
			"  geass servers add edge-1 --address 198.51.100.7 --ssh-user root --ssh-key-file ~/.ssh/geass_ed25519 --tag edge",
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			server := client.ServerRequest{
				Connection: strings.TrimSpace(connection), Region: strings.TrimSpace(region), Size: strings.TrimSpace(size), Image: strings.TrimSpace(image),
				Address: strings.TrimSpace(address), SSHUser: strings.TrimSpace(sshUser), SSHPort: sshPort, SSHKeySecret: strings.TrimSpace(sshKeySecret),
				PlacementRegion: strings.TrimSpace(placementRegion), PlacementZone: strings.TrimSpace(placementZone), Tags: tags, ReservedTags: reservedTags,
			}
			if len(args) == 1 {
				server.Name = args[0]
			}
			if changed("storage") {
				choice, err := normalizeChoice("storage", storage, "None", "Allow")
				if err != nil {
					return err
				}
				server.Storage = choice
			}
			if changed("setting") {
				given, err := parseSettings(settings)
				if err != nil {
					return err
				}
				for key, value := range given {
					if value == "" {
						delete(given, key)
					}
				}
				server.Settings = given
			}
			ownMachine := server.Address != ""
			switch {
			case ownMachine && (server.Connection != "" || server.Size != ""):
				return usageError("a server is made at a cloud (--connection and --size) or is one you have (--address), not both")
			case ownMachine && server.Name == "":
				return usageError("give the server a name: geass servers add NAME --address %s ...", server.Address)
			case ownMachine && server.SSHUser == "":
				return usageError("a server you have needs --ssh-user: root, or a user that can sudo without a password")
			case ownMachine && sshKeyFile == "" && server.SSHKeySecret == "":
				return usageError("a server you have needs its SSH key: --ssh-key-file PATH (PATH - is standard input), or --ssh-key-secret naming a Secret that holds it")
			case !ownMachine && (server.Connection == "" || server.Size == ""):
				return usageError("say what to add: a machine at a cloud (--connection and --size, see `geass sizes`) or a server you have (--address, --ssh-user and --ssh-key-file)")
			}
			stdinUsed := false
			if ownMachine && sshKeyFile != "" {
				raw, err := a.readFileOrStdin(sshKeyFile, &stdinUsed)
				if err != nil {
					return fmt.Errorf("could not read the SSH key: %w", err)
				}
				server.SSHPrivateKey = strings.TrimSpace(string(raw))
				if server.SSHPrivateKey == "" {
					return usageError("the SSH key file is empty")
				}
			}
			if ownMachine && knownHostsFile != "" {
				raw, err := a.readFileOrStdin(knownHostsFile, &stdinUsed)
				if err != nil {
					return fmt.Errorf("could not read the known hosts: %w", err)
				}
				server.KnownHosts = strings.TrimSpace(string(raw))
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			name, err := api.AddServer(cmd.Context(), server)
			if err != nil {
				return err
			}
			if name == "" {
				name = server.Name
			}
			if ownMachine {
				a.printf("Added the server %s. Geass joins it to the cluster over SSH at %s: watch it with `geass nodes list`.\n", name, server.Address)
			} else {
				a.printf("Added the server %s. Geass is creating a machine of size %s at %s, which costs money until the server is removed: watch it with `geass nodes list`.\n", name, server.Size, server.Connection)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&connection, "connection", "", "for a machine at a cloud: the connection (see: geass connections list)")
	f.StringVar(&region, "region", "", "for a machine at a cloud: its region or location")
	f.StringVar(&size, "size", "", "for a machine at a cloud: the size (see: geass sizes)")
	f.StringVar(&image, "image", "", "for a machine at a cloud: the operating system image (default: the cloud's Ubuntu 24.04)")
	f.StringArrayVar(&settings, "setting", nil, "for a machine at a cloud: a setting of the cloud, KEY=VALUE (repeatable)")
	f.StringVar(&address, "address", "", "for a server you have: where Geass reaches it over SSH")
	f.StringVar(&sshUser, "ssh-user", "", "for a server you have: root, or a user that can sudo without a password")
	f.IntVar(&sshPort, "ssh-port", 0, "for a server you have: the SSH port (default 22)")
	f.StringVar(&sshKeyFile, "ssh-key-file", "", "for a server you have: the SSH private key, read from this file and stored as a Secret (PATH - is standard input)")
	f.StringVar(&sshKeySecret, "ssh-key-secret", "", "for a server you have: a Secret that already holds the key")
	f.StringVar(&knownHostsFile, "known-hosts-file", "", "for a server you have: a file with its host keys, to pin it")
	f.StringVar(&placementRegion, "placement-region", "", "the region the node carries, for placing projects")
	f.StringVar(&placementZone, "placement-zone", "", "the site the node carries")
	f.StringSliceVar(&tags, "tag", nil, "a node tag the node carries; repeatable")
	f.StringSliceVar(&reservedTags, "reserved-tag", nil, "a node tag that also keeps all other work off the node; repeatable")
	f.StringVar(&storage, "storage", "", "allow (the default for a single server) or none: no databases or volumes on this node")
	return cmd
}

func (a *App) serversRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove NAME",
		Aliases: []string{"rm", "delete"},
		Short:   "Remove a server that was added on its own",
		Long: "Remove a server that was added with `geass servers add`. Its services move to other nodes if they have room, and a machine " +
			"Geass made for it is deleted at the cloud. A node that belongs to a pool is removed by changing the pool, and the nodes " +
			"the cluster came with cannot be removed here.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.confirm(fmt.Sprintf("Remove the server %q? Its services move elsewhere, and a machine made for it is deleted at the cloud.", args[0])); err != nil {
				return err
			}
			api, _, err := a.api()
			if err != nil {
				return err
			}
			if err := api.RemoveServer(cmd.Context(), args[0]); err != nil {
				return err
			}
			a.printf("Removed the server %s. Check `geass nodes list` to see it go.\n", args[0])
			return nil
		},
	}
}
