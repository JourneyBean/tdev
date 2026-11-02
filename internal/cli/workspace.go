package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
	"tdev/internal/tmux"
)

func (a *app) workspaceCommands() {
	ls := a.command(a.root, "ls", "List projects and live session status", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		p, err := a.prefs()
		if err != nil {
			return nil, err
		}
		all, _ := c.Flags().GetBool("all")
		results := map[string]any{}
		failures := map[string]string{}
		for n := range p.Servers {
			if !all && n != p.DefaultServer {
				continue
			}
			t, _ := a.serverTarget(p, n, "")
			data, err := a.call(ctx, t, nil, "project", "list", "--status")
			if err != nil {
				failures[n] = err.Error()
			} else {
				results[n] = data
			}
		}
		if len(failures) > 0 {
			return nil, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "some servers could not be listed", Details: map[string]any{"projects": results, "errors": failures}}
		}
		return results, nil
	})
	ls.Flags().Bool("all", false, "query every configured server")
	for _, op := range []string{"save", "stop", "sh"} {
		op := op
		c := a.command(a.root, op+" [project]", "Run project "+op, cobra.MaximumNArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
			arg := ""
			if len(args) > 0 {
				arg = args[0]
			}
			t, err := a.resolve(ctx, arg, true)
			if err != nil {
				return nil, err
			}
			if op == "stop" && arg != "" {
				if err = a.confirm(fmt.Sprintf("Stop %s and its foreground processes, disconnecting other clients?", arg)); err != nil {
					return nil, err
				}
			}
			p, err := a.get(ctx, t)
			if err != nil {
				return nil, err
			}
			if op == "sh" {
				if !t.Local && !a.json && !terminalAvailable(a.in) {
					return nil, config.Invalid("sh requires a terminal; --json creates a window without attaching")
				}
				save, _ := c.Flags().GetBool("save")
				argv := []string{"session", "add-window", p.Name, "--name", "shell"}
				if !save {
					argv = append(argv, "--temporary")
				}
				data, err := a.call(ctx, t, nil, argv...)
				if err != nil {
					return nil, err
				}
				var w map[string]string
				if err = json.Unmarshal(data, &w); err != nil {
					return nil, err
				}
				if t.Local {
					return w, a.manager().Select(ctx, w["window_id"])
				}
				if a.json {
					return w, nil
				}
				b, err := a.call(ctx, t, nil, "session", "status", p.Name)
				if err != nil {
					return nil, err
				}
				var s tmux.Status
				if err = json.Unmarshal(b, &s); err != nil {
					return nil, err
				}
				policy, err := commandPolicy(c, t.Reconnect.Terminal)
				if err != nil {
					return nil, err
				}
				return nil, a.attach(c.Context(), t, s, w["window_id"], &reconnect.Budget{Policy: policy})
			}
			argv := []string{"session", op, p.Name}
			if op == "save" {
				argv = append(argv, "--expected-revision", strconv.Itoa(p.Revision))
			}
			return a.call(ctx, t, nil, argv...)
		})
		if op == "sh" {
			c.Flags().Bool("save", false, "persist the new shell definition")
			retryFlags(c)
		}
	}
	open := &cobra.Command{Use: "open <project>", Short: "Ensure, restore saved forwarding and attach the workspace", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if a.json || !terminalAvailable(a.in) {
			return config.Invalid("open requires an interactive terminal")
		}
		t, err := a.resolve(c.Context(), args[0], false)
		if err != nil {
			return err
		}
		policy, err := commandPolicy(c, t.Reconnect.Terminal)
		if err != nil {
			return err
		}
		budget := &reconnect.Budget{Policy: policy}
		var p config.Project
		var s tmux.Status
		for {
			err = a.handshake(c.Context(), t, "attachment-v1")
			if err == nil {
				p, err = a.get(c.Context(), t)
			}
			if err == nil {
				var data json.RawMessage
				data, err = a.call(c.Context(), t, nil, "session", "ensure", p.Name)
				if err == nil {
					err = json.Unmarshal(data, &s)
				}
			}
			if err == nil {
				break
			}
			if !reconnect.Retryable(err) {
				return err
			}
			fmt.Fprintln(a.errOut, err)
			if err = budget.Next(c.Context()); err != nil {
				return err
			}
			t.Batch = true
		}
		closeForwards, err := a.openForwards(c.Context(), t, p.Forwards)
		if err != nil {
			return err
		}
		defer closeForwards()
		return a.attach(c.Context(), t, s, "", budget)
	}}
	a.root.AddCommand(open)
	retryFlags(open)
	doctor := a.command(a.root, "doctor [project]", "Check client/remote versions and optional dependencies", cobra.MaximumNArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		local := a.clientDoctor()
		if len(args) == 0 {
			return local, nil
		}
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, err
		}
		remote, err := a.call(ctx, t, nil, "doctor")
		if err != nil {
			return nil, err
		}
		return map[string]any{"client": local, "remote": remote}, nil
	})
	_ = doctor
}
