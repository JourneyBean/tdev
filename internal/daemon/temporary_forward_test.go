package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tdev/internal/config"
	"tdev/internal/reconnect"
)

func TestTemporaryForwardRecoveryRetainsRegistrationOnFailure(t *testing.T) {
	client, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(client.Dir) })
	stop := serveTest(t, client)
	port := freePort(t)
	f, err := config.ParseForward("L", fmt.Sprintf("%d:%d", port, echo(t)))
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Op: "forward", Local: true, Server: "local", Name: "forward-recovery", Policy: reconnect.Defaults().Forward, Forwards: []config.Forward{f}}
	if _, err = client.Call(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	stop(true)
	occupied, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	serveTest(t, client)
	wait(t, func() bool {
		state, err := client.Call(context.Background(), Request{Op: "status"})
		return err == nil && len(state.Forwards) == 1 && state.Forwards[0].State == "failed"
	})
	data, err := os.ReadFile(filepath.Join(client.Dir, "desired.json"))
	if err != nil || !strings.Contains(string(data), req.Name) {
		t.Fatalf("failed recovery discarded registration: %s %v", data, err)
	}
	req.Op = "forward-stop"
	if _, err = client.Call(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(client.Dir, "desired.json"))
	if err != nil || strings.Contains(string(data), req.Name) {
		t.Fatalf("stop retained recovery registration: %s %v", data, err)
	}
}
