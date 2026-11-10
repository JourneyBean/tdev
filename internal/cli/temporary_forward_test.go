package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"tdev/internal/config"
	"tdev/internal/daemon"
)

func TestEditProjectForwardsAndOverrides(t *testing.T) {
	root := t.TempDir()
	store := config.Store{Root: root}
	p, err := store.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	edit := func(p config.Project) {
		t.Helper()
		b, _ := json.Marshal(p)
		code, r := run(t, root, string(b), "edit", "local/demo", "--stdin", "--expected-revision", fmt.Sprint(p.Revision))
		if code != 0 {
			t.Fatalf("edit: %d %+v", code, r)
		}
	}
	f, _ := config.ParseForward("L", "8080:localhost:8000")
	p.Forwards = []config.Forward{f}
	edit(p)
	code, r := run(t, root, "", "override", "local/demo", "--forward", f.ID, "--local-port", "18080")
	if code != 0 {
		t.Fatal(r)
	}
	p, _ = store.Get("demo")
	p.Forwards[0].TargetPort = 8001
	edit(p)
	p, _ = store.Get("demo")
	if p.Forwards[0].ID != f.ID || p.Forwards[0].TargetPort != 8001 {
		t.Fatal(p)
	}
	code, r = run(t, root, "", "override", "local/demo")
	if code != 0 {
		t.Fatal(r)
	}
	p.Forwards = nil
	edit(p)
	code, r = run(t, root, "", "override", "local/demo")
	if code != 0 {
		t.Fatalf("deleted mapping left a stale override: %+v", r)
	}
	a := &app{store: store}
	overrides, err := a.readOverrides()
	if err != nil {
		t.Fatal(err)
	}
	if len(overrides.Servers["local"]["demo"].Forwards) != 0 {
		t.Fatal("deleted mapping override retained")
	}
}

func TestTemporaryForwardListingAndFilters(t *testing.T) {
	listing := projectListing{"dev": {State: "synced", Forwards: []daemon.ForwardState{{Name: "forward-example", Server: "dev", Spec: "-L 127.0.0.1:8080:localhost:8000", State: "waiting", Error: "connection lost"}}}}
	var out strings.Builder
	if err := listing.print(&out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"forward-example", "forward", "waiting", "-L 127.0.0.1:8080:localhost:8000", "connection lost"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
	for _, kind := range []string{"project", "scratch"} {
		if len(filterListing(listing, kind)["dev"].Forwards) != 0 {
			t.Fatalf("%s filter retained forwards", kind)
		}
	}
	if len(filterListing(listing, "forward")["dev"].Forwards) != 1 {
		t.Fatal("forward filter lost forwards")
	}
}

func TestTemporaryForwardValidation(t *testing.T) {
	for _, args := range [][]string{
		{"forward", "local"},
		{"forward", "local", "-L", "8080", "-L", "8080"},
		{"forward", "local/demo", "-L", "8080"},
		{"forward", "add", "demo", "-L", "8080"},
		{"forward", "remove", "demo", "--id", "api"},
	} {
		code, r := run(t, t.TempDir(), "", args...)
		if code != 2 || r.OK {
			t.Fatalf("invalid request %v: %d %+v", args, code, r)
		}
	}
}
