package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/daemon"
	"tdev/internal/protocol"
	"tdev/internal/update"
)

func TestUpgradeDaemonRestartFailureRetainsInstallationResult(t *testing.T) {
	root := t.TempDir()
	client, err := daemon.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(client.Dir) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	for {
		if _, err := client.Call(ctx, daemon.Request{Op: "status"}); err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("daemon did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var stdout bytes.Buffer
	a := &app{store: config.Store{Root: root}, timeout: 5 * time.Second, out: &stdout, json: true}
	installed := update.Receipt{Version: "0.4.2", Destination: "/managed/tdev"}
	err = a.finishUpgrade(ctx, filepath.Join(t.TempDir(), "missing-executable"), true, installed, nil)
	failure := protocol.Normalize(err)
	if failure.Code != "PARTIAL_SUCCESS" || failure.Exit != 9 || failure.Details != installed || stdout.Len() != 0 {
		t.Fatalf("restart failure lost committed installation result: %+v stdout=%s", failure, stdout.String())
	}
	// Rejected installation must not attempt daemon management or write success.
	rejected := update.VerifyError("invalid signature")
	if err := a.finishUpgrade(ctx, installed.Destination, true, installed, rejected); err != rejected || stdout.Len() != 0 {
		t.Fatalf("rejected installation: %v stdout=%s", err, stdout.String())
	}
}

func TestRemoteReceiptScriptUsesTargetStateAndLegacyFallback(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(t.TempDir(), "state with spaces")
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", state)
	destination := filepath.Join(home, "bin", "tdev ' $literal")
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		t.Fatal(err)
	}
	legacy := destination + ".tdev-install.json"
	if err := os.WriteFile(legacy, []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(want string) {
		t.Helper()
		cmd := exec.Command("sh", "-c", remoteReceiptScript(destination))
		// The client state directory must have no effect on target-side discovery.
		cmd.Env = append(os.Environ(), "XDG_STATE_HOME="+state)
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != want {
			t.Fatalf("got %q, %v; want %q", out, err, want)
		}
	}
	receipt := update.ReceiptPath(destination)
	run("legacy")
	if err := config.AtomicWrite(receipt, []byte("managed"), 0600); err != nil {
		t.Fatal(err)
	}
	run("managed")
}
