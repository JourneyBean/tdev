package cli

import (
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/testssh"
	"tdev/internal/tmux"
)

func TestScratchCLI(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}
	socket := "tdev-scratch-test-" + config.NewID()
	t.Setenv("TDEV_TMUX_SOCKET", socket)
	t.Setenv("TMUX", "")
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "-f", "/dev/null", "kill-server").Run() })
	root, dir := t.TempDir(), t.TempDir()
	code, r := run(t, root, "", "scratch", "local", "--name", "debug", "--dir", dir)
	if code != 0 {
		t.Fatal(r)
	}
	data, _ := json.Marshal(r.Data)
	var s tmux.Scratch
	if json.Unmarshal(data, &s) != nil || s.Name != "debug" {
		t.Fatal(string(data))
	}
	code, r = run(t, root, "", "scratch", "local", "--name", "debug")
	if code != 4 {
		t.Fatal(code, r)
	}
	code, r = run(t, root, "", "scratch", "local", "--attach", "debug")
	if code != 2 || r.Error == nil || !strings.Contains(r.Error.Message, "unknown flag") {
		t.Fatal(code, r)
	}
	for _, kind := range []string{"", "scratch", "project"} {
		code, r = run(t, root, "", "ls", "--server", "local", "--type", kind)
		if code != 0 {
			t.Fatal(r)
		}
		data, _ = json.Marshal(r.Data)
		var listing projectListing
		if json.Unmarshal(data, &listing) != nil {
			t.Fatal(string(data))
		}
		want := 1
		if kind == "project" {
			want = 0
		}
		if len(listing["local"].Scratches) != want {
			t.Fatal(string(data))
		}
	}
	binary := buildCLI(t, "0.2.0")
	completion, e := exec.Command(binary, "--config-dir", root, "__complete", "open", "deb").Output()
	if e != nil || !strings.Contains(string(completion), "debug") {
		t.Fatal("scratch completion", string(completion), e)
	}

	completion, e = exec.Command(binary, "--config-dir", root, "__complete", "open", "local/deb").Output()
	if e != nil || !strings.Contains(string(completion), "local/debug") {
		t.Fatal("qualified scratch completion", string(completion), e)
	}
	mount := terminalCLI(t, binary, root, "", "open", "local/debug", "--mount")
	mount.exit(t, 2)
	attached := terminalCLI(t, binary, root, "", "open", "debug")
	attached.write(t, "printf 'SCRATCH_READY\\n'\r")
	attached.waitText(t, "SCRATCH_READY\r\n")
	attached.write(t, "tmux detach-client\r")
	attached.exit(t, 0)
	m := tmux.Manager{Store: config.Store{Root: root}, Socket: socket}
	if _, err := m.ScratchStatus(context.Background(), "debug"); err != nil {
		t.Fatal("detach removed scratch", err)
	}
	again := terminalCLI(t, binary, root, "", "open", "local/debug")
	again.write(t, "printf 'RESUMED_READY\\n'\r")
	again.waitText(t, "RESUMED_READY\r\n")
	again.write(t, "exit\r")
	again.exit(t, 0)
	all, err := m.Scratches(context.Background())
	if err != nil || len(all) != 0 {
		t.Fatal(all, err)
	}

	// Inside tmux, scratch switches the attached client to the newly created session.
	origin := terminalCLI(t, binary, root, "", "scratch", "local", "--name", "origin")
	origin.write(t, "printf 'ORIGIN_READY\\n'\r")
	origin.waitText(t, "ORIGIN_READY\r\n")
	original, err := m.ScratchStatus(context.Background(), "origin")
	if err != nil {
		t.Fatal(err)
	}
	origin.write(t, tmux.Quote(binary)+" --config-dir "+tmux.Quote(root)+" scratch local --name switched\r")
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, e := exec.Command("tmux", m.Args("list-clients", "-F", "#{session_name}")...).Output()
		if e == nil && strings.Contains(string(out), "tdev-scratch-switched") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scratch did not switch client: %s %v; terminal: %s", out, e, origin.output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	current, err := m.ScratchStatus(context.Background(), "origin")
	if err != nil || current.SessionID != original.SessionID || current.Generation != original.Generation {
		t.Fatal("source session changed", current, err)
	}
	origin.write(t, "printf 'SWITCHED_READY\\n'\r")
	origin.waitText(t, "SWITCHED_READY\r\n")
	origin.write(t, "exit\r")
	origin.exit(t, 0)
	projects, err := m.Store.List()
	if err != nil || len(projects) != 0 {
		t.Fatal(projects, err)
	}
}

func TestRemoteScratchCLI(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.2.0")
	root, remoteRoot, dir := t.TempDir(), t.TempDir(), t.TempDir()
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "dev", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", remoteRoot)
	executeBinary(t, binary, root, s.Config, "", 0, "scratch", "dev", "--name", "debug", "--dir", dir)
	r := executeBinary(t, binary, root, s.Config, "", 0, "ls", "--server", "dev", "--type", "scratch")
	data, _ := json.Marshal(r.Data)
	var listing projectListing
	if json.Unmarshal(data, &listing) != nil || len(listing["dev"].Scratches) != 1 {
		t.Fatal(string(data))
	}

	// Reuse the refreshed cache while the target is offline.
	s.Offline(true)
	cached := executeBinary(t, binary, root, s.Config, "", 9, "ls", "--server", "dev", "--type", "scratch")
	details, _ := json.Marshal(cached.Error.Details)
	var offline projectListing
	if json.Unmarshal(details, &offline) != nil || len(offline["dev"].Scratches) != 1 || offline["dev"].UpdatedAt.IsZero() || offline["dev"].State != "unreachable" {
		t.Fatal(string(details))
	}
	s.Offline(false)
	attached := terminalCLI(t, binary, root, s.Config, "open", "debug", "--reconnect-interval", "100ms", "--reconnect-attempts", "10")
	attached.write(t, "printf 'REMOTE_SCRATCH_READY\\n'\r")
	attached.waitText(t, "REMOTE_SCRATCH_READY\r\n")
	s.Offline(true)
	attached.waitText(t, "Connection lost.")
	s.Offline(false)
	attached.write(t, "printf 'SCRATCH_RECONNECTED\\n'\r")
	attached.waitText(t, "SCRATCH_RECONNECTED\r\n")

	attached.write(t, "tmux detach-client\r")
	attached.exit(t, 0)
	again := terminalCLI(t, binary, root, s.Config, "open", "dev/debug")
	again.write(t, "printf 'REMOTE_RESUMED_READY\\n'\r")
	again.waitText(t, "REMOTE_RESUMED_READY\r\n")
	again.write(t, "exit\r")
	again.exit(t, 0)
	executeBinary(t, binary, root, s.Config, "", 5, "api", "scratch", "status", "debug")
	stale := terminalCLI(t, binary, root, s.Config, "open", "debug")
	stale.exit(t, 5)
	// A new scratch absent from the cached index is discovered live by bare name.
	executeBinary(t, binary, root, s.Config, "", 0, "scratch", "dev", "--name", "fresh")
	fresh := terminalCLI(t, binary, root, s.Config, "open", "fresh")
	fresh.write(t, "printf 'FRESH_READY\\n'\r")
	fresh.waitText(t, "FRESH_READY\r\n")
	fresh.write(t, "exit\r")
	fresh.exit(t, 0)

	// Ensure creation did not register a project on the target.
	projects, err := (config.Store{Root: remoteRoot}).List()
	if err != nil || len(projects) != 0 {
		t.Fatal(projects, err)
	}
}

func TestOpenScratchConflict(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}
	socket := "tdev-conflict-" + config.NewID()
	t.Setenv("TDEV_TMUX_SOCKET", socket)
	t.Setenv("TMUX", "")
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "-f", "/dev/null", "kill-server").Run() })
	root, dir := t.TempDir(), t.TempDir()
	if code, r := run(t, root, "", "scratch", "local", "--name", "same"); code != 0 {
		t.Fatal(r)
	}
	if code, r := run(t, root, "", "create", "same", "--directory", dir); code != 0 {
		t.Fatal(r)
	}
	binary := buildCLI(t, "0.2.0")
	for _, name := range []string{"same", "local/same"} {
		opened := terminalCLI(t, binary, root, "", "open", name)
		opened.exit(t, 4)
	}
	m := tmux.Manager{Store: config.Store{Root: root}, Socket: socket}
	if _, err := m.ScratchStatus(context.Background(), "same"); err != nil {
		t.Fatal(err)
	}
	p, err := m.Store.Get("same")
	if err != nil {
		t.Fatal(err)
	}
	state, err := m.Status(context.Background(), p)
	if err != nil || state.Status != "missing" {
		t.Fatal("conflict started project", state, err)
	}
}
