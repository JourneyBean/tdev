package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"tdev/internal/config"
	"tdev/internal/forward"
	"tdev/internal/protocol"
)

func (a *app) forwardCommand() {
	c := &cobra.Command{Use: "forward [project]", Short: "Supervise independent foreground TCP forwards", Args: cobra.MaximumNArgs(1), RunE: func(c *cobra.Command, args []string) error {
		arg := ""
		if len(args) > 0 {
			arg = args[0]
		}
		if arg != "" && !strings.Contains(arg, "/") {
			if _, err := config.ParseForward("L", arg); err == nil {
				_ = c.Flags().Set("local", arg)
				arg = ""
			}
		}
		fs, err := readForwards(c)
		if err != nil {
			return err
		}
		t, err := a.resolve(c.Context(), arg, true)
		if err != nil {
			return err
		}
		if t.Local {
			return config.Invalid("forwarding requires an explicit server/project on this machine")
		}
		p, err := a.get(c.Context(), t)
		if err != nil {
			return err
		}
		// Validate the full batch, including listener collisions, before saving or starting anything.
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
		save, _ := c.Flags().GetBool("save")
		saved := false
		if save {
			for _, f := range fs {
				if f.Direction == "R" {
					if err = a.confirm("Save reverse forwards? Multiple clients may compete for remote listeners; traffic reaches the client that establishes each listener."); err != nil {
						return err
					}
					break
				}
			}
			appendForwards(&p, fs)
			b, _ := yaml.Marshal(p)
			if _, err = a.call(c.Context(), t, b, "config", "set", p.Name, "--stdin", "--expected-revision", strconv.Itoa(p.Revision)); err != nil {
				return err
			}
			saved = true
			fmt.Fprintf(a.errOut, "Definitions saved on %s/%s. Listener status follows independently.\n", t.Name, p.Name)
		}
		g := forward.Start(c.Context(), t.Server.SSHAlias, t.SSH, fs, policy)
		defer g.Close()
		states := map[string]forward.Event{}
		for e := range g.Events {
			states[e.ID] = e
			if !a.json && e.State != "connecting" {
				fmt.Fprintf(a.errOut, "%s: %s %s\n", e.Spec, e.State, e.Error)
			}
		}
		if failure := g.Failure(); failure != nil {
			code := 6
			if saved {
				code = 9
			}
			return &protocol.Error{Code: map[bool]string{true: "PARTIAL_SUCCESS", false: "CONNECTION_FAILED"}[saved], Exit: code, Message: failure.Error(), Details: map[string]any{"saved": saved, "forwards": states}}
		}
		if c.Context().Err() != nil {
			return protocol.Fail("CANCELLED", 130, "forwarding cancelled; saved definitions are retained")
		}
		return nil
	}}
	a.root.AddCommand(c)
	forwardFlags(c)
	retryFlags(c)
	c.Flags().Bool("save", false, "save complete definitions separately from listener success")
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
	g := forward.Start(ctx, t.Server.SSHAlias, t.SSH, fs, t.Reconnect.Forward)
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
