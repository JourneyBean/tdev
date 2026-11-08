package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"tdev/internal/config"
)

func TestListingTableAlignsTerminalColumns(t *testing.T) {
	rows := [][6]string{
		{"dev", "中文项目", "project", "unmanaged,warning", "/projects/中文目录", "127.0.0.1:8080 -> 127.0.0.1:80"},
		{"dev", "demo", "project", "inactive", "/projects/demo", "—"},
		{"dev", "debug", "scratch", "running", "/projects/debug", "—"},
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
	starts := [6]int{2, 10, 20, 29, 48, 68}
	for i, row := range append([][6]string{{"SERVER", "NAME", "TYPE", "STATUS", "DIR", "FORWARDS"}}, rows...) {
		line := lines[0]
		if i > 0 {
			line = lines[i+1]
		}
		for col, cell := range row {
			if cell == "" {
				continue
			}
			at := strings.Index(line, cell)
			if at < 0 || ansi.StringWidth(line[:at]) != starts[col] {
				t.Fatalf("column %d misaligned in %q", col, line)
			}
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
	column := strings.Index(lines[0], "FORWARDS")
	for i, mapping := range []string{"127.0.0.1:8080 -> 127.0.0.1:80", "127.0.0.1:9000 <- 127.0.0.1:9001", "[::1]:8081 -> [::1]:81"} {
		if strings.Index(lines[i+2], mapping) != column {
			t.Fatalf("mapping outside FORWARDS column: %s", out.String())
		}
	}
	if !strings.Contains(lines[2], "/projects/demo") || strings.TrimSpace(lines[3][:column]) != "" {
		t.Fatalf("unexpected project rows: %s", out.String())
	}
}
