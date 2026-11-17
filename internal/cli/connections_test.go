package cli

import (
	"strings"
	"testing"

	"tdev/internal/config"
	"tdev/internal/daemon"
	"tdev/internal/sshx"
	"tdev/internal/tmux"
	"tdev/internal/webproxy"
)

func TestConnectionRowsDeduplicateAndAttributeResources(t *testing.T) {
	a := ref("dev-host", "api", "session", "")
	b := ref("dev-host", "debug", "scratch", "")
	web := ref("local", "docs", "web", "app")
	snap := daemon.Inspection{PID: 100, RuntimeDir: "runtime", Connections: []daemon.ConnectionView{
		{ID: "one", Kind: "control", Socket: "master", Status: "active", Resources: []daemon.ResourceRef{a}, Inspection: sshx.Inspection{PID: 200, State: "master-running"}},
		{ID: "two", Kind: "retired", Socket: "master", Status: "closing", Resources: []daemon.ResourceRef{b}, Inspection: sshx.Inspection{PID: 200, State: "master-running"}},
		{ID: "web", Kind: "local-web", Status: "ready", Resources: []daemon.ResourceRef{web}},
		{ID: "tcp", Kind: "local-forward", Status: "ready", Resources: []daemon.ResourceRef{ref("local", "tools", "forward", "http")}},
	}}
	listing := processListing{Hosts: []processHost{{Server: "local", Inventory: daemon.Inventory{Daemons: []daemon.DaemonView{{State: "running", Inspection: snap}}}}}}
	rows := listing.connectionRows()
	if len(rows) != 2 {
		t.Fatalf("duplicate connections/processes: %+v", rows)
	}
	for _, r := range rows {
		if len(r.Resources) != 2 || r.Status != "running" {
			t.Fatalf("missing users or invalid state: %+v", r)
		}
	}
	// Wide resource columns refer to the exact IDs and PIDs used by connection ls.
	entry := serverListing{Connections: resourceConnections(snap)}
	id, pid := entry.wideCells([]daemon.ResourceRef{a})
	for _, r := range rows {
		if r.PID == 200 && (r.Connection != id || pid != "200") {
			t.Fatalf("inconsistent resource connection: %s %s %+v", id, pid, r)
		}
	}
	before := daemon.ConnectionID(snap.Connections[0], 100, "runtime")
	snap.Connections[0].PID++
	if daemon.ConnectionID(snap.Connections[0], 100, "runtime") == before {
		t.Fatal("replacement PID retained connection ID")
	}
	var out strings.Builder
	if err := listing.print(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "dev-host/scratch/debug") || !strings.Contains(out.String(), "CONNECTION") {
		t.Fatal(out.String())
	}
}

func TestOverviewAndSessionHaveDistinctResourceRows(t *testing.T) {
	zero := 0
	entry := serverListing{State: "synced", ActiveOnly: true, Projects: []discoveredProject{
		{Project: projectForListing("inactive"), Session: tmux.Status{Status: "missing"}},
		{Project: projectForListing("api"), Session: tmux.Status{Status: "running", SessionName: "tdev-project-api", Clients: &zero}},
	}, Forwards: []daemon.ForwardState{{Name: "forward-demo", State: "ready", Spec: "-L 127.0.0.1:8080:localhost:80"}}}
	var overview, session strings.Builder
	if err := (projectListing{"local": entry}).print(&overview); err != nil {
		t.Fatal(err)
	}
	items := filterListing(projectListing{"local": entry}, "")["local"].Items
	if len(items) != 2 || items[0]["name"] != "api" || items[1]["status"] != "running" {
		t.Fatalf("JSON items differ from overview: %+v", items)
	}
	entry.View = "session"
	if err := (projectListing{"local": entry}).print(&session); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(overview.String(), "inactive") || !strings.Contains(overview.String(), "forward-demo") {
		t.Fatal(overview.String())
	}
	if strings.Contains(session.String(), "forward-demo") || !strings.Contains(session.String(), "CLIENTS") || !strings.Contains(session.String(), "tdev-project-api") {
		t.Fatal(session.String())
	}
}

func projectForListing(name string) config.Project { return config.Project{Name: name} }

func TestOverviewShowsRunningEndpointsAfterDeclarationChanges(t *testing.T) {
	entry := serverListing{State: "synced", ActiveOnly: true, WebPort: 12345,
		Projects: []discoveredProject{{Project: config.Project{Name: "api", Forwards: []config.Forward{{ID: "http", Direction: "L", BindAddress: "127.0.0.1", ListenPort: 8081, TargetHost: "localhost", TargetPort: 80}}, HTTP: []config.HTTPService{{Name: "app", Hostname: "api.localhost", TargetHost: "new-target", TargetPort: 9001}}}, Session: tmux.Status{Status: "missing"}}},
		Forwards: []daemon.ForwardState{{State: "ready", Spec: "-L 127.0.0.1:8080:localhost:80", Resources: []daemon.ResourceRef{ref("dev-host", "api", "forward", "http")}}},
		Web:      []webproxy.State{{Project: "api", Name: "app", Hostname: "api.localhost", URL: "http://api.localhost:12345", State: "ready", TargetHost: "old-target", TargetPort: 9000}},
	}
	var out strings.Builder
	if err := (projectListing{"dev-host": entry}).print(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "127.0.0.1:8080 -> localhost:80") || !strings.Contains(out.String(), "api.localhost:12345 -> old-target:9000") || strings.Contains(out.String(), "new-target") {
		t.Fatal(out.String())
	}
	entry.View = "project"
	entry.ActiveOnly = false
	out.Reset()
	if err := (projectListing{"dev-host": entry}).print(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "127.0.0.1:8081") || !strings.Contains(out.String(), "new-target:9001") {
		t.Fatalf("project list lost declared endpoints: %s", out.String())
	}
	entry.View = ""
	entry.ActiveOnly = true
	// Removing the declaration retains the independently running work item.
	entry.Projects = nil
	out.Reset()
	if err := (projectListing{"dev-host": entry}).print(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "api") || !strings.Contains(out.String(), "old-target:9000") {
		t.Fatal(out.String())
	}
}
