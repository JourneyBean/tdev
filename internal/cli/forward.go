package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/forward"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
)

func (a *app) forwardCommand() {
	c := &cobra.Command{Use: "forward [project]", Short: "Supervise independent foreground TCP forwards", Args: cobra.MaximumNArgs(1), RunE: func(c *cobra.Command, args []string) error {
		arg := ""
		if len(args) > 0 {
			arg = args[0]
		}
		t, err := a.resolve(c.Context(), arg, true)
		if err != nil {
			return err
		}
		p, err := a.get(c.Context(), t)
		if err != nil {
			return err
		}
		var fs []config.Forward
		if c.Flags().Changed("local") || c.Flags().Changed("reverse") {
			fs, err = readForwards(c)
		} else {
			var effective config.Project
			effective, err = a.effective(t, p)
			fs = effective.Forwards
		}
		if err != nil {
			return err
		}
		if len(fs) == 0 {
			return config.Invalid("project has no saved forwards; use tdev forward add")
		}
		// Validate the full batch, including listener collisions, before starting any listeners.
		check := p
		check.Forwards = nil
		appendForwards(&check, fs)
		if err = check.Validate(); err != nil {
			return err
		}
		fs = check.Forwards
		policy, err := commandPolicy(c, t.Reconnect.Forward)
		if err != nil {
			return err
		}
		g := startProjectForwards(c.Context(), t, fs, policy)
		defer g.Close()
		states := map[string]forward.Event{}
		for e := range g.Events {
			states[e.ID] = e
			if !a.json && e.State != "connecting" {
				fmt.Fprintf(a.errOut, "%s: %s %s\n", e.Spec, e.State, e.Error)
			}
		}
		if failure := g.Failure(); failure != nil {
			return &protocol.Error{Code: "CONNECTION_FAILED", Exit: 6, Message: failure.Error(), Details: map[string]any{"forwards": states}}
		}
		if c.Context().Err() != nil {
			return protocol.Fail("CANCELLED", 130, "forwarding cancelled")
		}
		return nil
	}}
	a.root.AddCommand(c)
	a.forwardDefinitions(c)
	forwardFlags(c)
	retryFlags(c)
}

// Background forwarding events are written only to a private log, never to the live terminal.
func (a *app) openForwards(ctx context.Context, t target, fs []config.Forward) (func(), error) {
	if len(fs) == 0 {
		return func() {}, nil
	}
	dir := filepath.Join(a.store.Root, "runtime", "logs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	log, err := os.CreateTemp(dir, "open-*.jsonl")
	if err != nil {
		return nil, err
	}
	if err = log.Chmod(0600); err != nil {
		log.Close()
		return nil, err
	}
	g := startProjectForwards(ctx, t, fs, t.Reconnect.Forward)
	initial := make(chan forward.Event, len(fs))
	done := make(chan struct{})
	go func() {
		defer close(done)
		enc := json.NewEncoder(log)
		seen := map[string]bool{}
		for e := range g.Events {
			_ = enc.Encode(e)
			if !seen[e.ID] && (e.State == "ready" || e.State == "failed" || e.State == "waiting") {
				seen[e.ID] = true
				initial <- e
			}
		}
	}()
	cleanup := func() {
		g.Close()
		<-done
		_ = log.Close()
		if err := g.Failure(); err != nil {
			fmt.Fprintln(a.errOut, err)
		}
		fmt.Fprintln(a.errOut, "Forward diagnostics:", log.Name())
	}
	for range fs {
		select {
		case e := <-initial:
			if e.State != "ready" {
				fmt.Fprintf(a.errOut, "Forward unavailable (%s): %s; details: %s\n", e.Spec, e.Error, log.Name())
			}
		case <-ctx.Done():
			cleanup()
			return nil, ctx.Err()
		}
	}
	return cleanup, nil
}

func startProjectForwards(ctx context.Context, t target, fs []config.Forward, policy reconnect.Policy) *forward.Group {
	if t.Local {
		return forward.StartLocal(ctx, fs)
	}
	return forward.Start(ctx, t.Server.SSHAlias, t.SSH, fs, policy)
}
func (a *app) forwardDefinitions(parent *cobra.Command) {
	add := a.command(parent, "add <project>", "Save project forwarding definitions", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, err
		}
		p, err := a.get(ctx, t)
		if err != nil {
			return nil, err
		}
		rev := p.Revision
		if expected(c) > 0 && expected(c) != rev {
			return nil, protocol.Fail("CONFIG_CONFLICT", 4, "project configuration changed")
		}
		var override projectOverride
		if c.Flags().Changed("local") || c.Flags().Changed("reverse") {
			fs, err := readForwards(c)
			if err != nil {
				return nil, err
			}
			appendForwards(&p, fs)
		} else {
			if a.json || !terminalAvailable(a.in) {
				return nil, config.Invalid("specify -L or -R without a terminal")
			}
			f, o, err := a.forwardWizard(ctx, t, p.ID, config.Forward{}, t.Server.Projects[p.Name])
			if err != nil {
				return nil, err
			}
			p.Forwards = append(p.Forwards, f)
			override = o
			if err = a.confirm("Save this project forwarding definition?"); err != nil {
				return nil, err
			}
		}
		if _, err = mergeProject(p, override); err != nil {
			return nil, err
		}
		result, err := a.writeProject(ctx, t, p, rev)
		if err != nil {
			return nil, err
		}
		if override.ProjectID != "" {
			if err = a.saveOverride(ctx, t, p, func(o *projectOverride) error { *o = override; return nil }); err != nil {
				return nil, protocol.Fail("PARTIAL_SUCCESS", 9, "forward saved; client override failed: %v", err)
			}
		}
		return result, nil
	})
	forwardFlags(add)
	revision(add)
	remove := a.command(parent, "remove <project>", "Remove a saved project forwarding definition", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, err
		}
		p, err := a.get(ctx, t)
		if err != nil {
			return nil, err
		}
		id, _ := c.Flags().GetString("id")
		if id == "" {
			if a.json || !terminalAvailable(a.in) {
				return nil, config.Invalid("--id is required without a terminal")
			}
			for _, f := range p.Forwards {
				fmt.Fprintf(a.errOut, "%s -%s %s\n", f.ID, f.Direction, f.Spec())
			}
			id, err = a.prompt(ctx, "移除映射 ID", "")
			if err != nil {
				return nil, err
			}
		}
		if expected(c) > 0 && expected(c) != p.Revision {
			return nil, protocol.Fail("CONFIG_CONFLICT", 4, "project configuration changed")
		}
		found := false
		kept := []config.Forward{}
		for _, f := range p.Forwards {
			if f.ID == id {
				found = true
			} else {
				kept = append(kept, f)
			}
		}
		if !found {
			return nil, protocol.Fail("FORWARD_NOT_FOUND", 2, "forward not found")
		}
		p.Forwards = kept
		result, err := a.writeProject(ctx, t, p, p.Revision)
		if err != nil {
			return nil, err
		}
		if _, ok := t.Server.Projects[p.Name].Forwards[id]; ok {
			err = a.saveOverride(ctx, t, p, func(o *projectOverride) error { delete(o.Forwards, id); return nil })
			if err != nil {
				return nil, protocol.Fail("PARTIAL_SUCCESS", 9, "forward removed; client override cleanup failed: %v", err)
			}
		}
		return result, nil
	})
	remove.Flags().String("id", "", "stable forwarding definition ID")
	revision(remove)
}
