package cli

import (
	"context"
	"encoding/json"
	"sort"

	"tdev/internal/config"
	"tdev/internal/daemon"
)

func (a *app) webAutostartSources() ([]daemon.WebSource, error) {
	prefs, err := a.prefs()
	if err != nil {
		return nil, err
	}
	sources := []daemon.WebSource{{Server: "local", Policy: prefs.Reconnect.HTTP}}
	names := make([]string, 0, len(prefs.Servers))
	for name := range prefs.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sources = append(sources, daemon.WebSource{Server: name, Policy: prefs.Servers[name].Reconnect.HTTP.apply(prefs.Reconnect.HTTP)})
	}
	return sources, nil
}

func (a *app) webAutostartProjects(ctx context.Context, server string) ([]daemon.Request, error) {
	// Discovery runs after local initialization on background workers and uses
	// the same noninteractive control transport as ordinary project API calls.
	worker := *a
	prefs, err := worker.prefs()
	if err != nil {
		return nil, err
	}
	t, err := worker.serverTarget(prefs, server, "")
	if err != nil {
		return nil, err
	}
	t.Batch = true
	t, err = daemonTarget(t)
	if err != nil {
		return nil, err
	}
	var projects []config.Project
	if t.Local {
		projects, err = worker.store.List()
	} else {
		var data json.RawMessage
		data, err = worker.call(ctx, t, nil, "project", "list")
		if err == nil {
			err = json.Unmarshal(data, &projects)
		}
	}
	if err != nil {
		return nil, err
	}
	requests := []daemon.Request{}
	for _, p := range projects {
		if err = p.Validate(); err != nil {
			return nil, err
		}
		services := []config.HTTPService{}
		for _, h := range p.HTTP {
			if h.AutoStart() {
				services = append(services, h.WithDomainSuffix(t.HTTP.DomainSuffix))
			}
		}
		if len(services) == 0 {
			continue
		}
		requests = append(requests, daemon.Request{Op: "web", Automatic: true, ProjectID: p.ID, Server: server, Name: p.Name, Alias: t.Server.SSHAlias, SSH: t.SSH, Local: t.Local, Policy: t.Reconnect.HTTP, HTTP: services, WebPort: t.HTTP.Port, WebHTTPSPort: t.HTTP.httpsPort()})
	}
	return requests, nil
}

func (a *app) serveWebDiscovery(ctx context.Context, r daemon.Request) (any, error) {
	switch r.Op {
	case "web-autostart-sources":
		return a.webAutostartSources()
	case "web-autostart-projects":
		return a.webAutostartProjects(ctx, r.Server)
	default:
		return nil, config.Invalid("unknown web discovery operation")
	}
}
