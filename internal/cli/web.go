package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/daemon"
	"tdev/internal/protocol"
	"tdev/internal/webproxy"
)

func (a *app) daemonWeb(ctx context.Context, t target, p config.Project) ([]webproxy.State, error) {
	if len(p.HTTP) == 0 {
		return []webproxy.State{}, nil
	}
	prefs, err := a.prefs()
	if err != nil {
		return nil, err
	}
	t, err = daemonTarget(t)
	if err != nil {
		return nil, err
	}
	client, err := a.daemonClient(ctx)
	if err != nil {
		return nil, err
	}
	state, err := client.Call(ctx, daemon.Request{Op: "status"})
	if err != nil {
		return nil, err
	}
	if !state.WebSupported {
		return nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "web requires the current daemon; run tdev daemon restart")
	}
	state, err = client.Call(ctx, daemon.Request{Op: "web", ProjectID: p.ID, Server: t.Name, Name: p.Name, Alias: t.Server.SSHAlias, SSH: t.SSH, Local: t.Local, Policy: t.Reconnect.HTTP, HTTP: p.HTTP, WebPort: prefs.HTTP.Port})
	if err != nil {
		return nil, err
	}
	routes := state.Web[:0]
	for _, r := range state.Web {
		if r.Server == t.Name && r.Project == p.Name {
			routes = append(routes, r)
		}
	}
	return routes, nil
}

func (a *app) webCommands() {
	g := group(a.root, "web", "Inspect and stop background HTTP routes")
	a.serviceListCommand(g, "web")
	a.command(g, "status", "List background HTTP routes", cobra.NoArgs, func(ctx context.Context, _ *cobra.Command, _ []string) (any, error) {
		client, err := daemon.New(a.store.Root)
		if err != nil {
			return nil, err
		}
		state, err := client.Call(ctx, daemon.Request{Op: "status"})
		if err != nil {
			return nil, err
		}
		return state.Web, nil
	})
	a.command(g, "stop <server/project>", "Stop a project's HTTP routes and tunnels", cobra.ExactArgs(1), func(ctx context.Context, _ *cobra.Command, args []string) (any, error) {
		server, project, ok := strings.Cut(args[0], "/")
		if !ok || !config.ValidName(server) || !config.ValidName(project) {
			return nil, config.Invalid("specify web server/project")
		}
		client, err := daemon.New(a.store.Root)
		if err != nil {
			return nil, err
		}
		_, err = client.Call(ctx, daemon.Request{Op: "web-stop", Server: server, Name: project})
		return map[string]string{"server": server, "project": project, "status": "stopped"}, err
	})
}
