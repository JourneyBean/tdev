package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
	"tdev/internal/sshx"
	"tdev/internal/testssh"
)

func TestInspectionShowsRealSSHChannelsAndTheirRelease(t *testing.T) {
	s := testssh.Start(t)
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(c.Dir) })
	serveTest(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	opts := sshx.Defaults()
	opts.ConfigFile = s.Config
	state, err := c.Call(ctx, Request{Op: "acquire", Alias: s.Alias, SSH: opts})
	if err != nil {
		t.Fatal(err)
	}
	childCtx, stop := context.WithCancel(ctx)
	cmd := sshx.Command(childCtx, "ssh", (Transport{Socket: state.Socket}).Args(false, "cat")...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stop(); stdin.Close(); _ = cmd.Wait() })
	snapshot := func() ConnectionView {
		t.Helper()
		state, err := c.Call(ctx, Request{Op: "inspect"})
		if err != nil || state.Inspection == nil {
			t.Fatal("inspection missing", err)
		}
		for _, connection := range state.Inspection.Connections {
			if connection.Kind == "control" {
				return connection
			}
		}
		t.Fatal("control connection missing")
		return ConnectionView{}
	}
	registration := Allocation{ID: "testchannel", Kind: "terminal", Project: "demo", ClientPID: os.Getpid(), Socket: state.Socket}
	release, err := c.TrackChannel(ctx, registration)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	row := snapshot()
	if row.PID <= 0 || row.State != "master-running" || row.Alias != s.Alias || len(row.Allocations) != 1 || row.Allocations[0].ClientPID != os.Getpid() {
		t.Fatalf("unexpected master state: %+v", row)
	}
	if row.ChannelsStatus == "available" {
		wait(t, func() bool { return len(snapshot().Channels) > 0 })
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool { return len(snapshot().Channels) == 0 })
	release()
	wait(t, func() bool { return len(snapshot().Allocations) == 0 })
	// Inspection must not open another TCP connection.
	before := s.Accepted.Load()
	_ = snapshot()
	if s.Accepted.Load() != before {
		t.Fatal("snapshot created SSH transport")
	}
	port := freePort(t)
	definition, err := config.ParseForward("L", fmt.Sprintf("%d:%d", port, echo(t)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(ctx, Request{Op: "forward", Alias: s.Alias, SSH: opts, Forwards: []config.Forward{definition}, Policy: reconnect.Defaults().Forward}); err != nil {
		t.Fatal(err)
	}
	state, err = c.Call(ctx, Request{Op: "inspect"})
	if err != nil || state.Inspection == nil {
		t.Fatal("forward inspection failed", err)
	}
	found := false
	for _, row := range state.Inspection.Connections {
		if row.Kind == "forward" && row.Socket != "" && row.Socket != snapshot().Socket && row.PID > 0 && row.Status == "ready" && row.Durable && row.Alias == s.Alias {
			found = true
		}
	}
	if !found {
		t.Fatal("forward did not retain its independent master identity")
	}
	if !roundtrip(port) {
		t.Fatal("inspection disturbed forward data flow")
	}
}

func TestInventoryReportsLegacyDaemonWithoutInventingChannels(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "tdev-ps-legacy-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	dir := filepath.Join(base, fmt.Sprintf("tdev-daemon-%d-legacy", os.Getuid()))
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "daemon.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		response := reply{State: State{Protocol: Protocol, PID: 42}}
		if req.Op == "inspect" {
			response.Error = protocol.Normalize(protocol.Fail("INVALID_CONFIG", 2, "unknown daemon operation"))
			response.Exit = 2
		}
		_ = json.NewEncoder(w).Encode(response)
	})}
	defer server.Close()
	go server.Serve(listener)
	result, err := inventoryIn(context.Background(), base)
	if err != nil || len(result.Daemons) != 1 {
		t.Fatal("legacy inventory missing", err)
	}
	row := result.Daemons[0]
	if row.PID != 42 || row.State != "running-legacy" || len(row.Connections) != 0 || len(row.Warnings) == 0 {
		t.Fatal("legacy capability limit was lost")
	}
}

func TestChannelRegistrationCancellationAndValidation(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(c.Dir) })
	serveTest(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	a := Allocation{ID: "allocation", Kind: "terminal", Project: "demo", ClientPID: os.Getpid(), Socket: filepath.Join(c.Dir, "ssh-owned", "m")}
	release, err := c.TrackChannel(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	state, err := c.Call(context.Background(), Request{Op: "inspect"})
	if err != nil || state.Inspection == nil {
		t.Fatal("missing allocation snapshot", err)
	}
	if len(state.Inspection.Connections) != 1 || len(state.Inspection.Connections[0].Allocations) != 1 {
		t.Fatal("registration lost while transport is retiring")
	}
	a.ID = "outside"
	a.Socket = "/other/ssh-owned/m"
	if release, err := c.TrackChannel(ctx, a); err == nil {
		release()
		t.Fatal("foreign transport accepted")
	}
	a.ID, a.Socket, a.Kind = "unknown", filepath.Join(c.Dir, "ssh-owned", "m"), "private command"
	if release, err := c.TrackChannel(ctx, a); err == nil {
		release()
		t.Fatal("arbitrary command accepted as a channel purpose")
	}
	cancel() // Simulate a caller disappearing without a release request.
	wait(t, func() bool {
		state, err := c.Call(context.Background(), Request{Op: "inspect"})
		return err == nil && state.Inspection != nil && len(state.Inspection.Connections) == 0
	})
}

func TestInventoryUsesOnlyPrivateOwnedDirectoriesAndDoesNotStartServices(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "tdev-ps-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	private := filepath.Join(base, fmt.Sprintf("tdev-daemon-%d-test", os.Getuid()))
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	c := Client{Dir: private, Root: t.TempDir()}
	stop := serveTest(t, c)
	public := filepath.Join(base, fmt.Sprintf("tdev-daemon-%d-public", os.Getuid()))
	if err := os.Mkdir(public, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, fmt.Sprintf("tdev-daemon-%d-link", os.Getuid()))
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	result, err := inventoryIn(context.Background(), base)
	if err != nil || len(result.Daemons) != 1 || result.Daemons[0].ConfigRoot != c.Root || result.Daemons[0].State != "running" {
		t.Fatalf("unexpected inventory: %+v %v", result, err)
	}
	stop(false)
	result, err = inventoryIn(context.Background(), base)
	if err != nil || len(result.Daemons) != 0 {
		t.Fatal("stopped daemon was restarted or retained", err)
	}
}
