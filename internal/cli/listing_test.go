package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"tdev/internal/config"
	"tdev/internal/daemon"
	"tdev/internal/webproxy"
)

func TestListingTableAlignsTerminalColumns(t *testing.T) {
	rows := [][6]string{
		{"dev", "中文项目", "project", "unmanaged,warning", "127.0.0.1:8080 -> 127.0.0.1:80", "demo.localhost:12345 -> localhost:3000"},
		{"dev", "demo", "project", "inactive", "—", "—"},
		{"dev", "debug", "scratch", "running", "—", "—"},
	}
	var out strings.Builder
	if err := printListingTable(&out, rows); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != len(rows)+2 {
		t.Fatalf("unexpected table: %q", out.String())
	}
	// Each column starts at the same display cell, including rows with Chinese names.
	starts := [6]int{2, 10, 20, 29, 48, 80}
	for i, row := range append([][6]string{{"SERVER", "NAME", "TYPE", "STATUS", "FORWARD", "WEB"}}, rows...) {
		line := lines[0]
		if i > 0 {
			line = lines[i+1]
		}
		offset := 0
		for col, cell := range row {
			if cell == "" {
				continue
			}
			at := strings.Index(line[offset:], cell)
			if at >= 0 {
				at += offset
			}
			if at < 0 || ansi.StringWidth(line[:at]) != starts[col] {
				t.Fatalf("column %d misaligned in %q", col, line)
			}
			offset = at + len(cell)
		}
	}
}

func TestListingForwardsUseDedicatedColumn(t *testing.T) {
	listing := projectListing{"dev": {State: "synced", Projects: []discoveredProject{
		{Project: config.Project{Name: "demo", Directory: "/projects/demo", Forwards: []config.Forward{
			{ID: "web", Direction: "L", BindAddress: "127.0.0.1", ListenPort: 8080, TargetHost: "127.0.0.1", TargetPort: 80},
			{ID: "callback", Direction: "R", BindAddress: "127.0.0.1", ListenPort: 9000, TargetHost: "127.0.0.1", TargetPort: 9001},
			{ID: "ipv6", Direction: "L", BindAddress: "::1", ListenPort: 8081, TargetHost: "::1", TargetPort: 81},
		}}},
	}}}
	var out strings.Builder
	if err := listing.print(&out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "web") || strings.Contains(out.String(), "callback") {
		t.Fatalf("forward IDs should be hidden: %s", out.String())
	}
	lines := strings.Split(out.String(), "\n")
	column := strings.Index(lines[0], "FORWARD")
	for i, mapping := range []string{"127.0.0.1:8080 -> 127.0.0.1:80", "127.0.0.1:9000 <- 127.0.0.1:9001", "[::1]:8081 -> [::1]:81"} {
		if strings.Index(lines[i+2], mapping) != column {
			t.Fatalf("mapping outside FORWARDS column: %s", out.String())
		}
	}
	if strings.Contains(out.String(), "/projects/demo") || strings.TrimSpace(lines[3][:column]) != "" {
		t.Fatalf("unexpected project rows: %s", out.String())
	}
}

func TestListingWebMappingsAndServiceStates(t *testing.T) {
	entry := serverListing{State: "synced", WebPort: 12345, Projects: []discoveredProject{
		{Project: config.Project{Name: "demo", Directory: "/projects/hidden", HTTP: []config.HTTPService{
			{Name: "app", Hostname: "app.localhost", TargetHost: "service", TargetPort: 8765},
			{Name: "admin", Hostname: "admin.localhost", TargetHost: "::1", TargetPort: 9000},
		}}},
	}, Web: []webproxy.State{{Project: "demo", Name: "app", Hostname: "app.localhost", URL: "http://app.localhost:23456", State: "waiting", Error: "connection lost"}}}
	for _, view := range []string{"", "web"} {
		entry.View = view
		var out strings.Builder
		if err := (projectListing{"dev": entry}).print(&out); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"app.localhost:23456 -> service:8765", "admin.localhost:12345 -> [::1]:9000"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("missing %q: %s", want, out.String())
			}
		}
		if strings.Contains(out.String(), "/projects/hidden") {
			t.Fatal(out.String())
		}
		if view == "web" {
			for _, want := range []string{"waiting", "connection lost", "stopped"} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("missing %q: %s", want, out.String())
				}
			}
		}
	}
}

func TestServiceListingKeepsConfiguredAndLiveResources(t *testing.T) {
	entry := serverListing{View: "forward", State: "unreachable", Error: "offline",
		Projects: []discoveredProject{{Project: config.Project{Name: "demo", Forwards: []config.Forward{
			{Direction: "R", BindAddress: "::1", ListenPort: 9000, TargetHost: "::1", TargetPort: 9001},
		}}}},
		Forwards: []daemon.ForwardState{{Name: "forward-example", State: "waiting", Spec: "-L 127.0.0.1:8080:localhost:8000", Error: "connection lost"}},
	}
	var out strings.Builder
	if err := (projectListing{"dev": entry}).print(&out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"configured", "[::1]:9000 <- [::1]:9001", "forward-example", "waiting", "127.0.0.1:8080 -> localhost:8000", "connection lost", "offline"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
	entry.View = "web"
	entry.Projects = nil
	entry.Web = []webproxy.State{{Project: "removed", Name: "app", URL: "http://app.localhost:12345", State: "ready"}}
	out.Reset()
	if err := (projectListing{"dev": entry}).print(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "http://app.localhost:12345") || !strings.Contains(out.String(), "running") {
		t.Fatal(out.String())
	}
}
