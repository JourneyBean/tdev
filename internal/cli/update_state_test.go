package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"tdev/internal/config"
	"tdev/internal/update"
)

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
