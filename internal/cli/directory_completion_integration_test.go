package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/sshx"
	"tdev/internal/testssh"
	"tdev/internal/tmux"
)

func TestRemoteDirectoryCompletionWithZsh(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		if os.Getenv("TDEV_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("zsh integration dependency missing")
		}
		t.Skip("zsh not installed")
	}
	s := testssh.Start(t)
	home := t.TempDir()
	empty := filepath.Join(home, "empty")
	for _, name := range []string{"empty", "projects", "项目 空格'$(false)", ".hidden"} {
		if err := os.Mkdir(filepath.Join(home, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// A forced command on this temporary test identity runs the exact command
	// received over real SSH through zsh, without changing the account's shell.
	wrapper := filepath.Join(s.Dir, "login-shell")
	script := "#!/bin/sh\nHOME=" + tmux.Quote(home) + "; export HOME\nexec " + tmux.Quote(zsh) + " -f -c \"$SSH_ORIGINAL_COMMAND\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(s.Dir, "client.pub")
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("command=\""+tmux.Quote(wrapper)+"\" "+string(key)), 0600); err != nil {
		t.Fatal(err)
	}
	options := sshx.Defaults()
	options.ConfigFile = s.Config
	target := target{Name: "fixture", Server: server{SSHAlias: s.Alias}, SSH: options, Batch: true}
	a := &app{store: config.Store{Root: t.TempDir()}, timeout: 5 * time.Second}
	_, err = a.shell(context.Background(), target, "for entry in "+tmux.Quote(empty)+"/*; do :; done")
	if diagnosticReason(err) != "shell_glob_no_match" {
		t.Fatal("fixture must reproduce zsh's unmatched glob failure", err)
	}
	for _, test := range []struct {
		prefix string
		want   []string
	}{
		{empty + "/", nil},
		{"~/proje", []string{"~/projects/"}},
		{"~/项目", []string{"~/项目 空格'$(false)/"}},
		{"~/.", []string{"~/.hidden/"}},
		{"~/missing", nil},
		{"~/", []string{"~/empty/", "~/projects/", "~/项目 空格'$(false)/"}},
	} {
		t.Run(test.prefix, func(t *testing.T) {
			got, err := a.completeRemoteDirectories(context.Background(), target, test.prefix)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("completion: got %q, want %q, error %v", got, test.want, err)
			}
		})
	}
}
