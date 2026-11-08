package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
)

func TestNestedLayoutSaveRestore(t *testing.T) {
	m, p, ctx := fixture(t)
	for _, name := range []string{"front end", "后端", "logs"} {
		if err := os.Mkdir(filepath.Join(p.Directory, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	counter := filepath.Join(p.Directory, "starts")
	command := "printf x >> " + Quote(counter) + "; printf 'PANE_READY\\n'; sleep 15"
	p, err := m.Store.Update(ctx, p.Name, p.Revision, func(p *config.Project) error {
		p.Windows[0].Layout = &config.Pane{Split: "horizontal", Panes: []config.Pane{
			{ID: "front", Directory: "front end", Command: command, Weight: 2},
			{Split: "vertical", Weight: 1, Panes: []config.Pane{{ID: "back", Directory: "后端", Command: "printf 'BACKEND_READY\\n'; exit 7"}, {ID: "logs", Directory: "logs"}}},
		}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := m.Ensure(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	windows, err := m.Windows(ctx, status)
	if err != nil || len(windows) != 1 || windows[0].Panes != 3 {
		t.Fatalf("%+v %v", windows, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		output, err := m.run(ctx, "capture-pane", "-p", "-S", "-", "-t", windows[0].Leaves[1].PaneID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output, "BACKEND_READY") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("startup command did not run: %q; panes %+v", output, windows[0].Leaves)
		}
		time.Sleep(10 * time.Millisecond)
	}
	again, err := m.Ensure(ctx, p)
	if err != nil || again.SessionID != status.SessionID {
		t.Fatalf("ensure replayed: %+v %v", again, err)
	}
	if b, _ := os.ReadFile(counter); string(b) != "x" {
		t.Fatalf("startup count %q", b)
	}
	saved, err := m.Save(ctx, p, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	layout := saved.Windows[0].Layout
	if layout == nil || layout.Split != "horizontal" || layout.Panes[1].Split != "vertical" || layout.Panes[0].Command != command || layout.Panes[1].Panes[0].Command != p.Windows[0].Layout.Panes[1].Panes[0].Command {
		t.Fatalf("saved layout: %+v", layout)
	}
	if layout.Panes[0].Directory != filepath.Join(p.Directory, "front end") || layout.Panes[0].Weight <= layout.Panes[1].Weight {
		t.Fatalf("lost directories/proportions: %+v", layout)
	}
	// A manually added pane is saved with a fresh stable identity and no inferred command.
	newPane, err := m.run(ctx, "split-window", "-d", "-h", "-t", windows[0].Leaves[2].PaneID, "-c", p.Directory, "-P", "-F", "#{pane_id}")
	if err != nil {
		t.Fatal(err)
	}
	saved, err = m.Save(ctx, saved, saved.Revision)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := m.run(ctx, "show-options", "-p", "-v", "-t", newPane, "@tdev_pane_id")
	if err != nil || marker == "" {
		t.Fatalf("manual pane identity: %q %v", marker, err)
	}
	if _, err = m.Stop(ctx, saved); err != nil {
		t.Fatal(err)
	}
	restored, err := m.Ensure(ctx, saved)
	if err != nil {
		t.Fatal(err)
	}
	windows, err = m.Windows(ctx, restored)
	if err != nil || windows[0].Panes != 4 {
		t.Fatalf("restored: %+v %v", windows, err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		b, _ := os.ReadFile(counter)
		if string(b) == "xx" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("restart count: %q", b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	second, err := m.Save(ctx, saved, saved.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if second.Windows[0].Layout.Panes[0].Command != command {
		t.Fatal("command lost after restore")
	}
	for _, pane := range windows[0].Leaves {
		if pane.ID != "front" {
			if _, err = m.run(ctx, "kill-pane", "-t", pane.PaneID); err != nil {
				t.Fatal(err)
			}
		}
	}
	collapsed, err := m.Save(ctx, second, second.Revision)
	if err != nil || collapsed.Windows[0].Layout != nil || collapsed.Windows[0].Command != command {
		t.Fatalf("collapse lost command: %+v %v", collapsed.Windows, err)
	}
	collapsed, err = m.Save(ctx, collapsed, collapsed.Revision)
	if err != nil || collapsed.Windows[0].Command != command {
		t.Fatal("single-pane identity lost after collapse", err)
	}
}

func TestMissingPaneDirectoryLeavesSessionMissing(t *testing.T) {
	m, p, ctx := fixture(t)
	p.Windows[0].Layout = &config.Pane{Split: "vertical", Panes: []config.Pane{{ID: "ok", Directory: "."}, {ID: "missing", Directory: "missing"}}}
	if _, err := m.Ensure(ctx, p); err == nil {
		t.Fatal("missing pane directory accepted")
	}
	status, err := m.Status(ctx, p)
	if err != nil || status.Status != "missing" {
		t.Fatalf("session created before validation: %+v %v", status, err)
	}
}

func TestLegacySplitServiceRequiresPaneIdentity(t *testing.T) {
	m, p, ctx := fixture(t)
	p, err := m.Store.Update(ctx, p.Name, p.Revision, func(p *config.Project) error { p.Windows[0].Command = "sleep 15"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	status, err := m.Ensure(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	windows, err := m.Windows(ctx, status)
	if err != nil {
		t.Fatal(err)
	}
	pane := windows[0].Leaves[0].PaneID
	if _, err = m.run(ctx, "set-option", "-pu", "-t", pane, "@tdev_pane_id"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.run(ctx, "split-window", "-d", "-h", "-b", "-t", pane, "-c", p.Directory); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Save(ctx, p, p.Revision); err == nil {
		t.Fatal("guessed original service pane")
	}
	current, err := m.Store.Get(p.Name)
	if err != nil || current.Revision != p.Revision {
		t.Fatal("failed save changed configuration", err)
	}
	if _, err = m.run(ctx, "set-option", "-p", "-t", pane, "@tdev_pane_id", p.Windows[0].ID); err != nil {
		t.Fatal(err)
	}
	// An unmarked shell precedes the explicitly identified service pane.
	saved, err := m.Save(ctx, p, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Windows[0].Layout.Panes[0].Command != "" || saved.Windows[0].Layout.Panes[1].Command != "sleep 15" {
		t.Fatal("service command attached to wrong pane")
	}
}
