package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/daemon"
	"tdev/internal/forward"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
)

type temporaryForward struct {
	Name     string                `json:"name"`
	Server   string                `json:"server"`
	Forwards []daemon.ForwardState `json:"forwards"`
}

func (a *app) forwardCommand() {
	c := a.command(a.root, "forward [server]", "Start temporary TCP forwards on a server", cobra.MaximumNArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		foreground, _ := c.Flags().GetBool("foreground")
		if foreground {
			ctx = c.Context()
		}
		fs, err := readForwards(c)
		if err != nil {
			return nil, err
		}
		listeners := map[string]bool{}
		for _, f := range fs {
			key := fmt.Sprintf("%s/%s/%d", f.Direction, f.BindAddress, f.ListenPort)
			if listeners[key] {
				return nil, config.Invalid("duplicate forward listener %s", key)
			}
			listeners[key] = true
		}
		prefs, err := a.prefs()
		if err != nil {
			return nil, err
		}
		server := "local"
		if len(args) > 0 {
			server = args[0]
		} else if len(prefs.Servers) > 0 {
			if a.json || !terminalAvailable(a.in) {
				return nil, config.Invalid("specify a server for noninteractive forward")
			}
			server, err = a.chooseServer(ctx, prefs, "选择端口转发所在服务器")
			if err != nil {
				return nil, err
			}
		}
		t, err := a.serverTarget(prefs, server, "")
		if err != nil {
			return nil, err
		}
		t, err = daemonTarget(t)
		if err != nil {
			return nil, err
		}
		policy, err := commandPolicy(c, t.Reconnect.Forward)
		if err != nil {
			return nil, err
		}
		name := "forward-" + strings.ReplaceAll(config.NewID(), "-", "")
		req := daemon.Request{Op: "forward", Name: name, Server: server, Alias: t.Server.SSHAlias, SSH: t.SSH, Local: t.Local, Forwards: fs, Policy: policy}
		if foreground {
			owner, release, err := a.daemonLease(ctx, nil)
			if err != nil {
				return nil, err
			}
			defer release()
			req.Owner = owner
		}
		client, err := a.daemonClient(ctx)
		if err != nil {
			return nil, err
		}
		capabilities, err := client.Call(ctx, daemon.Request{Op: "status"})
		if err != nil {
			return nil, err
		}
		if !capabilities.TemporaryForwards {
			return nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "temporary forwards require the current daemon; run tdev daemon restart")
		}
		state, err := client.Call(ctx, req)
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = client.Call(cleanupCtx, daemon.Request{Op: "forward-stop", Server: server, Name: name})
			if ctx.Err() == context.Canceled {
				return nil, protocol.Fail("CANCELLED", 130, "forwarding cancelled")
			}
			return nil, err
		}
		result := temporaryForward{Name: name, Server: server, Forwards: []daemon.ForwardState{}}
		for _, f := range state.Forwards {
			if f.Name == name && f.Server == server {
				result.Forwards = append(result.Forwards, f)
			}
		}
		if !foreground {
			return result, nil
		}
		if !a.json {
			fmt.Fprintf(a.errOut, "%s/%s: ready (Ctrl-C to stop)\n", server, name)
		}
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil, protocol.Fail("CANCELLED", 130, "forwarding cancelled")
			case <-ticker.C:
				state, err = client.Call(ctx, daemon.Request{Op: "status"})
				if ctx.Err() != nil {
					return nil, protocol.Fail("CANCELLED", 130, "forwarding cancelled")
				}
				if err != nil {
					return nil, err
				}
				count := 0
				for _, f := range state.Forwards {
					if f.Name == name && f.Server == server {
						count++
						if f.State == "failed" {
							return nil, protocol.Fail("CONNECTION_FAILED", 6, "forward %s: %s", f.Spec, f.Error)
						}
					}
				}
				if count == 0 {
					return map[string]string{"status": "stopped"}, nil
				}
			}
		}
	})
	c.Flags().Bool("foreground", false, "keep running until cancelled; release forwards on exit")
	forwardFlags(c)
	retryFlags(c)
}

// stopTemporaryForward queries only the current client's existing supervisor.
func (a *app) stopTemporaryForward(ctx context.Context, arg string) (bool, any, error) {
	server, name, qualified := strings.Cut(arg, "/")
	if !qualified {
		name, server = arg, ""
	}
	if !strings.HasPrefix(name, "forward-") {
		return false, nil, nil
	}
	client, err := daemon.New(a.store.Root)
	if err != nil {
		return true, nil, err
	}
	state, err := client.Call(ctx, daemon.Request{Op: "status"})
	if err != nil {
		if protocol.Normalize(err).Code == "DAEMON_UNAVAILABLE" {
			return false, nil, nil
		}
		return true, nil, err
	}
	found := ""
	for _, f := range state.Forwards {
		if f.Name == name && (server == "" || f.Server == server) {
			if found != "" && found != f.Server {
				return true, nil, config.Invalid("qualify the forward name with its server")
			}
			found = f.Server
		}
	}
	if found == "" {
		return false, nil, nil
	}
	if err = a.confirm("Stop temporary forward " + found + "/" + name + "?"); err != nil {
		return true, nil, err
	}
	_, err = client.Call(ctx, daemon.Request{Op: "forward-stop", Name: name, Server: found})
	return true, map[string]string{"name": name, "server": found, "status": "stopped"}, err
}

// Background forwarding events are written only to a private log, never to the live terminal.
func (a *app) openForwards(ctx context.Context, t target, fs []config.Forward) (func(), error) {
	fs = automaticForwards(t, fs)
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

func automaticForwards(t target, fs []config.Forward) []config.Forward {
	if !t.Local {
		return fs
	}
	kept := make([]config.Forward, 0, len(fs))
	for _, f := range fs {
		switch strings.ToLower(f.TargetHost) {
		case "localhost", "127.0.0.1", "::1":
			continue
		}
		kept = append(kept, f)
	}
	return kept
}

func startProjectForwards(ctx context.Context, t target, fs []config.Forward, policy reconnect.Policy) *forward.Group {
	if t.Local {
		return forward.StartLocal(ctx, fs)
	}
	return forward.Start(ctx, t.Server.SSHAlias, t.SSH, fs, policy)
}
