package cli

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestPostInstallContinuesAfterTmuxFailure(t *testing.T) {
	root := t.TempDir()
	// A malformed projects directory makes the first hook fail before tmux access.
	if err := os.WriteFile(filepath.Join(root, "projects"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := daemon.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(client.Dir) })
	a := &app{store: config.Store{Root: root}, timeout: time.Second}
	report, err := a.postInstall(context.Background(), false)
	if err == nil || protocol.Normalize(err).Code != "PARTIAL_SUCCESS" || len(report.Hooks) != 2 || report.Hooks[0].Status != "failed" || report.Hooks[1].Status != "succeeded" {
		t.Fatalf("hooks did not finish independently: %+v %v", report, err)
	}
	if _, err := client.Call(context.Background(), daemon.Request{Op: "status"}); protocol.Normalize(err).Code != "DAEMON_UNAVAILABLE" {
		t.Fatalf("post-install started stopped daemon: %v", err)
	}
	installed := update.Receipt{Version: "0.5.0", Destination: "/managed/tdev"}
	_, failure := finishInstallation(installed, nil, err, protocol.Fail("PARTIAL_SUCCESS", 9, "hooks failed"))
	encoded, _ := json.Marshal(protocol.Normalize(failure).Details)
	if !bytes.Contains(encoded, []byte(`"destination":"/managed/tdev"`)) || !bytes.Contains(encoded, []byte(`"post_install"`)) {
		t.Fatalf("installation details lost: %s", encoded)
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
