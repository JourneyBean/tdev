package cli

import (
	"context"
	"encoding/json"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
	"tdev/internal/tmux"
)

func (a *app) scratchCommand() {
	c := a.command(a.root, "scratch [server]", "Create a scratch tmux session", cobra.MaximumNArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		name, _ := c.Flags().GetString("name")
		dir, _ := c.Flags().GetString("dir")
		if !a.json && !terminalAvailable(a.in) {
			return nil, config.Invalid("scratch requires a terminal; use --json to create without attaching")
		}
		prefs, err := a.prefs()
		if err != nil {
			return nil, err
		}
		server := "local"
		if len(args) > 0 {
			server = args[0]
		} else if len(prefs.Servers) > 0 {
			if a.json {
				return nil, config.Invalid("specify a server for noninteractive scratch")
			}
			server, err = a.chooseServer(ctx, prefs, "选择临时会话所在服务器")
			if err != nil {
				return nil, err
			}
		}
		t, err := a.serverTarget(prefs, server, "")
		if err != nil {
			return nil, err
		}
		policy, err := commandPolicy(c, t.Reconnect.Terminal)
		if err != nil {
			return nil, err
		}
		argv := []string{"scratch", "create", "--name", name, "--dir", dir}
		data, err := a.call(ctx, t, nil, argv...)
		if err != nil {
			return nil, err
		}
		var s tmux.Scratch
		if err = json.Unmarshal(data, &s); err != nil {
			return nil, err
		}
		if a.json {
			return s, nil
		}
		t.Project = s.Name
		t.Scratch = true
		return nil, a.attach(c.Context(), t, s.Status, "", &reconnect.Budget{Policy: policy})
	})
	c.Flags().String("name", "", "scratch name (default: unique generated name)")
	c.Flags().String("dir", "", "directory on the target host (default: home)")
	retryFlags(c)
}

func (a *app) apiScratch(local *cobra.Command) {
	g := group(local, "scratch", "Manage live scratch sessions on this host")
	a.command(g, "list", "List scratch sessions", cobra.NoArgs, func(ctx context.Context, _ *cobra.Command, _ []string) (any, error) {
		return a.manager().Scratches(ctx)
	})
	create := a.command(g, "create", "Create a scratch session", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		name, _ := c.Flags().GetString("name")
		dir, _ := c.Flags().GetString("dir")
		return a.manager().CreateScratch(ctx, name, dir)
	})
	create.Flags().String("name", "", "scratch name")
	create.Flags().String("dir", "", "starting directory")
	a.command(g, "status <name>", "Read scratch identity", cobra.ExactArgs(1), func(ctx context.Context, _ *cobra.Command, args []string) (any, error) {
		return a.manager().ScratchStatus(ctx, args[0])
	})
	attach := &cobra.Command{Use: "attach <name>", Short: "Attach a scratch using its expected identity", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if a.json || !terminalAvailable(a.in) {
			return config.Invalid("scratch attach requires a terminal without --json")
		}
		sid, _ := c.Flags().GetString("session-id")
		generation, _ := c.Flags().GetString("generation")
		token, _ := c.Flags().GetString("token")
		return a.manager().Attach(c.Context(), config.Project{Name: args[0]}, sid, generation, "", token, a.in, a.out, a.errOut)
	}}
	g.AddCommand(attach)
	for _, flag := range []string{"session-id", "generation", "token"} {
		attach.Flags().String(flag, "", "expected attachment identity")
		_ = attach.MarkFlagRequired(flag)
	}
	status := a.command(g, "attachment <name>", "Check scratch client attachment", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		token, _ := c.Flags().GetString("token")
		return a.manager().Attachment(ctx, config.Project{Name: args[0]}, token)
	})
	status.Flags().String("token", "", "attachment identity")
	_ = status.MarkFlagRequired("token")
}

// A missing capability leaves project open compatible with older servers.
func (a *app) findScratch(ctx context.Context, t target) (*tmux.Scratch, error) {
	data, err := a.call(ctx, t, nil, "scratch", "status", t.Project)
	if err != nil {
		switch protocol.Normalize(err).Code {
		case "SESSION_NOT_FOUND", "PROTOCOL_INCOMPATIBLE", "DEPENDENCY_MISSING":
			return nil, nil
		}
		return nil, err
	}
	var s tmux.Scratch
	if err = json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
