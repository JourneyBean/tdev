package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/sshx"
	"tdev/internal/testssh"
	"tdev/internal/tmux"
)

func TestRemotePaneLayoutAndCapabilityGate(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.2.1-dev")
	store := config.Store{Root: t.TempDir()}
	p, err := store.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p.Windows[0].Layout = &config.Pane{Split: "vertical", Panes: []config.Pane{{ID: "a", Directory: ".", Command: "printf ready; sleep 15"}, {ID: "b", Directory: "."}}}
	a := &app{store: config.Store{Root: t.TempDir()}, timeout: 5 * time.Second, json: true, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	opts := sshx.Defaults()
	opts.ConfigFile = s.Config
	target := target{Name: "dev", Project: "demo", Server: server{SSHAlias: s.Alias, RemoteBinary: binary, RemoteConfigDir: store.Root}, SSH: opts}
	draft, _ := json.Marshal(p)
	if _, err = a.call(context.Background(), target, draft, "project", "set", "demo", "--stdin", "--expected-revision", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.call(context.Background(), target, nil, "session", "ensure", "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.call(context.Background(), target, nil, "session", "save", "demo", "--expected-revision", "2"); err != nil {
		t.Fatal(err)
	}
	saved, err := a.get(context.Background(), target)
	if err != nil || saved.Windows[0].Layout == nil || len(saved.Windows[0].Layout.Panes) != 2 || saved.Windows[0].Layout.Panes[0].Command != p.Windows[0].Layout.Panes[0].Command {
		t.Fatalf("remote layout lost: %+v %v", saved, err)
	}
	// Simulate a server with project-v1 but no pane-layout-v1. An incompatible
	// write must stop at the gate, before executing project set or reading stdin.
	wrapper := filepath.Join(t.TempDir(), "old-server")
	invoked := filepath.Join(t.TempDir(), "write-invoked")
	script := "#!/bin/sh\ncase \" $* \" in\n*' pane-layout-v1 '*) printf '%s\\n' '{\"protocol_version\":1,\"ok\":false,\"error\":{\"code\":\"PROTOCOL_INCOMPATIBLE\",\"message\":\"missing pane-layout-v1\"}}'; exit 8;;\n*' project set '*) touch " + tmux.Quote(invoked) + ";;\nesac\nexec " + tmux.Quote(binary) + " \"$@\"\n"
	if err = os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	target.Server.RemoteBinary = wrapper
	_, err = a.call(context.Background(), target, draft, "project", "set", "demo", "--stdin", "--expected-revision", "3")
	if err == nil || protocol.Normalize(err).Code != "PROTOCOL_INCOMPATIBLE" || !strings.Contains(err.Error(), "upgrade --remote dev") {
		t.Fatalf("missing capability error: %v", err)
	}
	if _, err = os.Stat(invoked); !os.IsNotExist(err) {
		t.Fatal("old server received write", err)
	}
}
