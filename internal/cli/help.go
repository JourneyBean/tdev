package cli

import "github.com/spf13/cobra"

// Keep the user workflow and the wire interface visible as separate entry points.
// Client preferences belong only to user commands, including their subcommands.
func (a *app) commandHelp() {
	a.root.AddGroup(
		&cobra.Group{ID: "workspace", Title: "Workspace commands:"},
		&cobra.Group{ID: "client", Title: "Client configuration and tools:"},
		&cobra.Group{ID: "protocol", Title: "Client/server machine interface:"},
	)
	a.root.SetHelpCommandGroupID("client")
	for _, c := range a.root.Commands() {
		switch c.Name() {
		case "api":
			c.GroupID = "protocol"
		case "setup", "override", "doctor", "version", "install", "upgrade", "path", "completion", "daemon", "help":
			c.GroupID = "client"
		default:
			c.GroupID = "workspace"
		}
		if c.Name() != "api" {
			for _, name := range []string{"yes", "ssh-config"} {
				c.PersistentFlags().AddFlag(a.root.Flags().Lookup(name))
			}
		}
	}
}
