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
	"tdev/internal/mount"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
)

func (a *app) daemonCommands() {
	g := group(a.root, "daemon", "Manage the local connection supervisor")
	for _, op := range []string{"start", "restart", "stop", "status"} {
		op := op
		a.command(g, op, "Daemon "+op, cobra.NoArgs, func(ctx context.Context, _ *cobra.Command, _ []string) (any, error) {
			client, err := daemon.New(a.store.Root)
			if err != nil {
				return nil, err
			}
			exe, err := os.Executable()
			if err != nil {
				return nil, err
			}
			return client.Manage(ctx, op, exe, a.store.Root)
		})
	}
	g.AddCommand(&cobra.Command{Use: "serve", Hidden: true, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		client, err := daemon.New(a.store.Root)
		if err != nil {
			return err
		}
		return client.Serve(c.Context(), a.serveDaemonService)
	}})
}

type mountServiceRequest struct {
	Target  target
	Project config.Project
	Path    string
}

func (a *app) serveDaemonService(ctx context.Context, r daemon.Request) (any, error) {
	if r.Op != "mount" && r.Op != "unmount" {
		return nil, config.Invalid("unknown daemon service")
	}
	var request mountServiceRequest
	if err := json.Unmarshal(r.Payload, &request); err != nil {
		return nil, config.Invalid("invalid mount request")
	}
	if err := request.Project.Validate(); err != nil {
		return nil, err
	}
	worker := *a
	worker.daemonWorker = true
	if r.Op == "unmount" {
		return mount.New(a.store).Unmount(ctx, request.Target.Name, request.Project.ID, request.Path)
	}
	result, err := worker.mountProject(ctx, request.Target, request.Project, request.Path)
	if err != nil {
		return nil, err
	}
	record, ok := result.(mount.Record)
	return daemon.MountResult{Data: result, Existing: ok && (record.Reused || record.External)}, nil
}

func (a *app) daemonLease(ctx context.Context, restore func(context.Context, string) error) (string, func(), error) {
	client, err := a.daemonClient(ctx)
	if err != nil {
		return "", nil, err
	}
	owner := "o" + strings.ReplaceAll(config.NewID(), "-", "")
	if _, err = client.Call(ctx, daemon.Request{Op: "lease-start", Owner: owner}); err != nil {
		return "", nil, err
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		restoreNeeded := false
		for {
			held := make(chan struct{})
			go func(current daemon.Client) {
				defer close(held)
				_, _ = current.Call(leaseCtx, daemon.Request{Op: "lease", Owner: owner})
			}(client)
			if restoreNeeded && restore != nil {
				if err := restore(leaseCtx, owner); err != nil {
					_, _ = a.recordDiagnostic("open_services", "", time.Now(), err, nil)
				}
			}
			<-held
			if leaseCtx.Err() != nil {
				return
			}
			for {
				if reconnect.Wait(leaseCtx, 500*time.Millisecond) != nil {
					return
				}
				var err error
				client, err = a.daemonClient(leaseCtx)
				if err != nil {
					continue
				}
				if _, err = client.Call(leaseCtx, daemon.Request{Op: "lease-start", Owner: owner}); err != nil {
					continue
				}
				restoreNeeded = true
				break
			}
		}
	}()
	return owner, func() {
		cancel()
		<-done
		releaseCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, err := client.Call(releaseCtx, daemon.Request{Op: "release", Owner: owner})
		if err != nil && protocol.Normalize(err).Code != "DAEMON_UNAVAILABLE" {
			fmt.Fprintln(a.errOut, "Resource cleanup:", err)
		}
	}, nil
}

func (a *app) daemonClient(ctx context.Context) (daemon.Client, error) {
	client, err := daemon.New(a.store.Root)
	if err != nil {
		return client, err
	}
	if _, err = client.Call(ctx, daemon.Request{Op: "status"}); err == nil {
		return client, nil
	}
	if protocol.Normalize(err).Code != "DAEMON_UNAVAILABLE" {
		return client, err
	}
	exe, err := os.Executable()
	if err != nil {
		return client, err
	}
	_, err = client.Manage(ctx, "ensure", exe, a.store.Root)
	return client, err
}

func (a *app) controlTransport(ctx context.Context, t target) (daemon.Transport, error) {
	var err error
	t, err = daemonTarget(t)
	if err != nil {
		return daemon.Transport{}, err
	}
	client, err := a.daemonClient(ctx)
	if err != nil {
		return daemon.Transport{}, err
	}
	// The daemon serializes its own requests; this lock also serializes terminal
	// authentication and adoption across independent CLI processes.
	unlock, err := (config.Store{Root: client.Dir}).Lock(ctx, daemon.ConnectionKey(t.Server.SSHAlias, t.SSH), "acquire")
	if err != nil {
		return daemon.Transport{}, err
	}
	defer unlock()
	request := daemon.Request{Op: "acquire", Alias: t.Server.SSHAlias, SSH: t.SSH}
	interactive := !t.Batch && terminalAvailable(a.in)
	if interactive {
		request.Op = "lookup"
	}
	state, err := client.Call(ctx, request)
	if err == nil && state.Socket != "" {
		return daemon.Transport{Socket: state.Socket}, nil
	}
	// Authenticate a cold interactive connection once, in the caller's terminal,
	// then hand it to the daemon. Background requests always use BatchMode.
	if !interactive || err != nil || ctx.Err() != nil {
		return daemon.Transport{}, err
	}
	authCtx, cancel := context.WithTimeout(ctx, t.SSH.ConnectTimeout+5*time.Second)
	defer cancel()
	transport, err := daemon.StartTransport(authCtx, client.Dir, t.Server.SSHAlias, t.SSH, false)
	if err != nil {
		return daemon.Transport{}, err
	}
	request.Op, request.Socket = "adopt", transport.Socket
	state, err = client.Call(ctx, request)
	if err != nil {
		transport.Close()
		return daemon.Transport{}, err
	}
	return daemon.Transport{Socket: state.Socket}, nil
}

func daemonTarget(t target) (target, error) {
	if t.SSH.ConfigFile != "" {
		path, err := filepath.Abs(t.SSH.ConfigFile)
		if err != nil {
			return t, err
		}
		t.SSH.ConfigFile = path
	}
	return t, nil
}

func (a *app) remoteArgs(ctx context.Context, t target, tty bool, command string) ([]string, error) {
	if !a.useDaemon {
		return append(t.SSH.Args(t.Batch), "-T", t.Server.SSHAlias, command), nil
	}
	m, err := a.controlTransport(ctx, t)
	if err != nil {
		return nil, err
	}
	return m.Args(tty, command), nil
}
