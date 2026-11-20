package cli

import (
	"context"
	"slices"
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
	services, err := effectiveHTTP(p.HTTP, t.HTTP.DomainSuffix)
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
	if !state.WebSeparatePorts {
		return nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "web requires the current daemon; run tdev daemon restart")
	}
	state, err = client.Call(ctx, daemon.Request{Op: "web", ProjectID: p.ID, Server: t.Name, Name: p.Name, Alias: t.Server.SSHAlias, SSH: t.SSH, Local: t.Local, Policy: t.Reconnect.HTTP, HTTP: services, WebPort: t.HTTP.Port, WebHTTPSPort: t.HTTP.httpsPort()})
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

func effectiveHTTP(services []config.HTTPService, suffix string) ([]config.HTTPService, error) {
	resolved := slices.Clone(services)
	for i, h := range resolved {
		resolved[i] = h.WithDomainSuffix(suffix)
	}
	return resolved, config.ValidateHTTPRoutes(resolved)
}

func (a *app) webCommands() {
	g := group(a.root, "web", "Configure and manage Web routes")
	g.AddCommand(&cobra.Command{
		Use:   "setup [project]",
		Short: "Configure Web proxy settings or project routes interactively",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if a.json || !terminalAvailable(a.in) {
				return config.Invalid("web setup requires an interactive terminal")
			}
			return a.runWebSetup(c.Context(), args)
		},
	})
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
