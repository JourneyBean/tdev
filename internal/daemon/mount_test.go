package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"tdev/internal/protocol"
)

func TestMountOwnershipAndSafeRelease(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(c.Dir) })
	var unmounts atomic.Int32
	var busy atomic.Bool
	service := func(_ context.Context, r Request) (any, error) {
		if r.Op == "unmount" {
			unmounts.Add(1)
			if busy.Load() {
				return nil, protocol.Fail("MOUNT_BUSY", 4, "mount is busy")
			}
			return nil, nil
		}
		var p struct {
			Path     string
			Existing bool
		}
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return nil, err
		}
		return MountResult{Data: map[string]string{"path": p.Path}, Existing: p.Existing}, nil
	}
	serveTest(t, c, service)
	ctx := context.Background()
	call := func(r Request) {
		t.Helper()
		if _, err := c.Call(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	for _, owner := range []string{"window1", "window2"} {
		call(Request{Op: "lease-start", Owner: owner})
		call(Request{Op: "mount", Owner: owner, Payload: json.RawMessage(`{"Path":"/mount/demo"}`)})
	}
	call(Request{Op: "release", Owner: "window1"})
	if unmounts.Load() != 0 {
		t.Fatal("released another window's mount")
	}
	call(Request{Op: "release", Owner: "window2"})
	if unmounts.Load() != 1 {
		t.Fatal("last owner didn't unmount")
	}
	// An existing mount from outside this lease is retained.
	call(Request{Op: "lease-start", Owner: "existing"})
	call(Request{Op: "mount", Owner: "existing", Payload: json.RawMessage(`{"Path":"/mount/existing","Existing":true}`)})
	call(Request{Op: "release", Owner: "existing"})
	if unmounts.Load() != 1 {
		t.Fatal("existing mount was removed")
	}
	// Busy mounts use the existing safe unmount path and leave a visible warning.
	busy.Store(true)
	call(Request{Op: "lease-start", Owner: "busy"})
	call(Request{Op: "mount", Owner: "busy", Payload: json.RawMessage(`{"Path":"/mount/busy"}`)})
	call(Request{Op: "release", Owner: "busy"})
	state, err := c.Call(ctx, Request{Op: "status"})
	if err != nil || len(state.Warnings) != 1 || unmounts.Load() != 2 {
		t.Fatalf("safe release: %+v %v", state, err)
	}
}

func TestRecoveredEphemeralMountIsReleased(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(c.Dir) })
	// Reconstruct the ownership record left by an abruptly terminated daemon.
	if err = os.WriteFile(filepath.Join(c.Dir, "ephemeral-mounts.json"), []byte(`{"/mount/recovered":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	var unmounts atomic.Int32
	serveTest(t, c, func(_ context.Context, r Request) (any, error) {
		if r.Op == "unmount" {
			unmounts.Add(1)
			return nil, nil
		}
		return MountResult{Data: map[string]string{"path": "/mount/recovered"}, Existing: true}, nil
	})
	ctx := context.Background()
	for _, r := range []Request{{Op: "lease-start", Owner: "reconnected"}, {Op: "mount", Owner: "reconnected", Payload: json.RawMessage(`{"Path":"/mount/recovered"}`)}, {Op: "release", Owner: "reconnected"}} {
		if _, err = c.Call(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if unmounts.Load() != 1 {
		t.Fatal("reconnected open lost ownership of its temporary mount")
	}
}
