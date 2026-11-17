package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"tdev/internal/config"
	"tdev/internal/reconnect"
)

func TestSharedForwardResourceOwnershipAndRecovery(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(c.Dir) })
	stop := serveTest(t, c)
	ctx := context.Background()
	call := func(r Request) State {
		t.Helper()
		s, e := c.Call(ctx, r)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	f, err := config.ParseForward("L", fmt.Sprintf("%d:%d", freePort(t), echo(t)))
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Op: "forward", Local: true, Policy: reconnect.Defaults().Forward, Forwards: []config.Forward{f}}
	first := ResourceRef{Server: "local", Name: "first", Kind: "forward", Detail: f.ID}
	req.Resources = []ResourceRef{first}
	call(req)
	// The same listener is shared by another project with its own forward ID.
	second := ResourceRef{Server: "local", Name: "second", Kind: "forward", Detail: "other"}
	req.Forwards[0].ID = "other"
	req.Resources = []ResourceRef{second}
	call(req)
	call(Request{Op: "lease-start", Owner: "foreground"})
	transient := ResourceRef{Server: "local", Name: "transient", Kind: "forward", Detail: "other"}
	req.Owner = "foreground"
	req.Resources = []ResourceRef{transient}
	call(req)
	refs := call(Request{Op: "status"}).Forwards[0].Resources
	if len(refs) != 3 {
		t.Fatalf("shared resource references: %+v", refs)
	}
	call(Request{Op: "release", Owner: "foreground"})
	if refs = call(Request{Op: "status"}).Forwards[0].Resources; len(refs) != 2 {
		t.Fatalf("released resource retained: %+v", refs)
	}
	snapshot := call(Request{Op: "inspect"}).Inspection
	if len(snapshot.Connections) != 1 || len(snapshot.Connections[0].Resources) != 2 {
		t.Fatalf("shared connection duplicated or missing users: %+v", snapshot)
	}
	stop(true)
	serveTest(t, c)
	wait(t, func() bool {
		s, e := c.Call(ctx, Request{Op: "status"})
		return e == nil && len(s.Forwards) == 1 && s.Forwards[0].State == "ready"
	})
	refs = call(Request{Op: "status"}).Forwards[0].Resources
	if len(refs) != 2 || refs[0] != first || refs[1] != second {
		t.Fatalf("recovered ownership: %+v", refs)
	}
}

// Old recovery records predate ResourceRef. Restoration must preserve listeners
// and temporary group identities without guessing project ownership.
func TestRestoreLegacyForwardRegistrationsWithoutResourceReferences(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(c.Dir) })
	ctx := context.Background()
	target := echo(t)
	requests := map[string]Request{}
	specs := map[string]bool{}
	for _, name := range []string{"", "forward-legacy"} {
		f, err := config.ParseForward("L", fmt.Sprintf("%d:%d", freePort(t), target))
		if err != nil {
			t.Fatal(err)
		}
		req := Request{Op: "forward", Local: true, Forwards: []config.Forward{f}, Policy: reconnect.Defaults().Forward}
		key := identity(req) + "/" + f.Direction + "/" + f.Spec()
		if name != "" {
			req.Name, req.Server = name, "local"
			key += "/local/" + name
		}
		requests[key] = req
		specs["-"+f.Direction+" "+f.Spec()] = true
	}
	data, err := json.Marshal(requests)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("Resources")) {
		t.Fatal("legacy recovery fixture contains new resource references")
	}
	if err := config.AtomicWrite(filepath.Join(c.Dir, "desired.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	serveTest(t, c)
	wait(t, func() bool {
		state, err := c.Call(ctx, Request{Op: "status"})
		if err != nil || len(state.Forwards) != 2 {
			return false
		}
		for _, f := range state.Forwards {
			if f.State != "ready" {
				return false
			}
		}
		return true
	})
	state, err := c.Call(ctx, Request{Op: "status"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range state.Forwards {
		if !specs[f.Spec] {
			t.Fatalf("listener changed during legacy restore: %+v", f)
		}
		if f.Name == "" && len(f.Resources) != 0 {
			t.Fatalf("anonymous project ownership was invented: %+v", f)
		}
		if f.Name == "forward-legacy" && (f.Server != "local" || len(f.Resources) != 1 || f.Resources[0].Kind != "temporary") {
			t.Fatalf("temporary identity lost: %+v", f)
		}
	}
	for _, req := range requests {
		if !roundtrip(req.Forwards[0].ListenPort) {
			t.Fatal("restored legacy listener did not forward traffic")
		}
	}
	saved, err := os.ReadFile(filepath.Join(c.Dir, "desired.json"))
	if err != nil {
		t.Fatal(err)
	}
	var restored map[string]Request
	if err := json.Unmarshal(saved, &restored); err != nil || len(restored) != len(requests) {
		t.Fatalf("recovery registrations changed: %v", err)
	}
	for key := range requests {
		if _, ok := restored[key]; !ok {
			t.Fatal("legacy recovery key changed")
		}
	}
}
