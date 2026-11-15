package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/sshx"
	"tdev/internal/testssh"
	"tdev/internal/tmux"
)

func TestRemoteHandwrittenConfigAndSharedFileSave(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	ssh := testssh.Start(t)
	binary := buildCLI(t, "0.7.0-dev")
	root, dir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := config.AtomicWrite(filepath.Join(root, "config.yaml"), []byte("projects:\n  include: [./projects/*.yaml]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "projects/work.yaml")
	text := fmt.Sprintf(`demo:
  dir: %q
  windows:
    - name: dev
      layout:
        row:
          - name: editor
            command: printf ready
          - name: shell
other: # preserve this project
  dir: %q
`, dir, dir)
	if err := config.AtomicWrite(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{store: config.Store{Root: t.TempDir()}, timeout: 5 * time.Second, json: true, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	opts := sshx.Defaults()
	opts.ConfigFile = ssh.Config
	target := target{Name: "dev", Project: "demo", Server: server{SSHAlias: ssh.Alias, RemoteBinary: binary, RemoteConfigDir: root}, SSH: opts}
	ctx := context.Background()
	initial, err := a.get(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.call(ctx, target, nil, "session", "ensure", "demo"); err != nil {
		t.Fatal(err)
	}
	// Moving the declaration must retain the running session's project identity.
	moved := filepath.Join(root, "projects/renamed.yaml")
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	again, err := a.get(ctx, target)
	if err != nil || again.ID != initial.ID || again.Revision != initial.Revision {
		t.Fatalf("%+v %v", again, err)
	}
	if _, err := a.call(ctx, target, nil, "session", "save", "demo", "--expected-revision", "1"); err != nil {
		t.Fatal(err)
	}
	saved, err := a.get(ctx, target)
	if err != nil || saved.ID != initial.ID || saved.Windows[0].Layout == nil {
		t.Fatalf("%+v %v", saved, err)
	}
	b, err := os.ReadFile(moved)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "other: # preserve this project") || strings.Contains(string(b), "schema_version:") || strings.Contains(string(b), "revision:") {
		t.Fatalf("unexpected source:\n%s", b)
	}
	// A hand edit increments the API revision while the session stays attached to its UID.
	b = bytes.Replace(b, []byte("printf ready"), []byte("printf changed"), 1)
	if err := config.AtomicWrite(moved, b, 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := a.get(ctx, target)
	if err != nil || changed.ID != initial.ID || changed.Revision != saved.Revision+1 {
		t.Fatalf("%+v %v", changed, err)
	}
	data, err := a.call(ctx, target, nil, "session", "ensure", "demo")
	if err != nil {
		t.Fatal(err)
	}
	var status tmux.Status
	if err := json.Unmarshal(data, &status); err != nil || status.Status != "running" {
		t.Fatalf("%s %v", data, err)
	}
	if _, err := a.call(ctx, target, nil, "session", "stop", "demo"); err != nil {
		t.Fatal(err)
	}
}
