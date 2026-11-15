package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/testssh"
)

func TestHandwrittenClientRolesAndRealForward(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	ssh := testssh.Start(t)
	binary := buildCLI(t, "0.7.0-dev")
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			conn, err := backend.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	baseline, clientPort, hostPreference := freeLocalPort(t), freeLocalPort(t), freeLocalPort(t)
	root, remote := t.TempDir(), t.TempDir()
	clientConfigFile(t, remote, "config.yaml", fmt.Sprintf(`demo:
  dir: %q
  ports:
    - name: api
      local: %d
      remote: %d
client:
  overrides:
    local/demo:
      ports:
        api:
          local: %d
`, t.TempDir(), baseline, backend.Addr().(*net.TCPAddr).Port, hostPreference))
	clientConfigFile(t, root, "config.yaml", fmt.Sprintf(`demo:
  dir: %q
client:
  include: [./client.d/*.yaml]
`, t.TempDir()))
	fragment := clientConfigFile(t, root, "client.d/work.yaml", fmt.Sprintf(`servers:
  fixture:
    ssh_alias: %q
    remote_binary: %q
    remote_config_dir: %q
overrides:
  fixture/demo:
    ports:
      api:
        local: %d # client's preferred listener
`, ssh.Alias, binary, remote, clientPort))
	// Re-running setup must preserve a hand-authored, not-yet-bound override.
	executeBinary(t, binary, root, ssh.Config, "", 0, "setup", "--name", "fixture", "--ssh-alias", ssh.Alias, "--remote-binary", binary, "--remote-config-dir", remote)
	a := &app{store: config.Store{Root: root}, sshConfig: ssh.Config, timeout: 5 * time.Second}
	prefs, err := a.prefs()
	if err != nil {
		t.Fatal(err)
	}
	target, err := a.serverTarget(prefs, "fixture", "demo")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := a.get(context.Background(), target)
	if err != nil || raw.Forwards[0].ListenPort != baseline {
		t.Fatalf("server applied its own client override: %+v %v", raw, err)
	}
	executeBinary(t, binary, root, ssh.Config, "", 0, "open", "fixture/demo", "--only", "forward")
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", clientPort), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("echo")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(conn, response); err != nil || string(response) != "echo" {
		t.Fatalf("%q %v", response, err)
	}
	overrides, err := a.readOverrides()
	if err != nil || overrides.Servers["fixture"]["demo"].ProjectID != raw.ID {
		t.Fatal("missing project binding", err)
	}
	executeBinary(t, binary, root, ssh.Config, "", 0, "override", "fixture/demo", "--forward", "api", "--local-port", fmt.Sprint(freeLocalPort(t)))
	b, err := os.ReadFile(fragment)
	if err != nil || !strings.Contains(string(b), "# client's preferred listener") || strings.Contains(string(b), raw.ID) {
		t.Fatalf("invalid fragment writeback: %s %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(remote, "state", "client-bindings.json")); !os.IsNotExist(err) {
		t.Fatal("remote API applied host client preferences", err)
	}
}
