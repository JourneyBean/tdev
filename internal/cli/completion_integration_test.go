package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/testssh"
)

func TestShellCompletions(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.0.3-dev")
	root, remoteRoot := t.TempDir(), t.TempDir()
	store := config.Store{Root: remoteRoot}
	p, err := store.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(context.Background(), "demo", p.Revision, func(p *config.Project) error {
		p.Forwards = []config.Forward{{ID: "api", Direction: "L", BindAddress: "127.0.0.1", ListenPort: 8080, TargetHost: "localhost", TargetPort: 8000}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "fixture", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", remoteRoot)
	query := func(args ...string) string {
		t.Helper()
		argv := append([]string{"--config-dir", root, "--ssh-config", s.Config, "__complete"}, args...)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, binary, argv...).Output()
		if err != nil {
			t.Fatalf("completion %v: %v", args, err)
		}
		return string(out)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"op"}, "open\t"},
		{[]string{"open", "de"}, "demo\n"},
		{[]string{"open", "fixture/de"}, "fixture/demo\n"},
		{[]string{"create", "--server", "fi"}, "fixture\n"},
		{[]string{"upgrade", "--remote", "fi"}, "fixture\n"},
		{[]string{"create", "fi"}, "fixture/\n"},
		{[]string{"setup", "--ssh-alias", "fi"}, "fixture\n"},
		{[]string{"project", "window", "remove", "demo", "--id", "sh"}, "shell\n"},
		{[]string{"override", "demo", "--forward", "ap"}, "api\n"},
	} {
		if out := query(tc.args...); !strings.Contains(out, tc.want) {
			t.Fatalf("%v missing %q: %s", tc.args, tc.want, out)
		}
	}
	if _, err = store.Create(context.Background(), "new-project", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if out := query("open", "new"); !strings.Contains(out, "new-project\n") {
		t.Fatal("completion used stale projects", out)
	}
	if out := query("open", "demo", ""); !strings.Contains(out, ":4") {
		t.Fatal("unexpected file fallback after project", out)
	}
	for _, shell := range []string{"bash", "zsh"} {
		out, err := exec.Command(binary, "completion", shell).Output()
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "completion."+shell)
		if err = os.WriteFile(file, out, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = exec.LookPath(shell); err != nil {
			t.Logf("%s native load deferred to client", shell)
			continue
		}
		if out, err := exec.Command(shell, "-n", file).CombinedOutput(); err != nil {
			t.Fatalf("%s syntax: %v %s", shell, err, out)
		}
		if shell == "bash" {
			script := `source "$1"
COMP_WORDS=("$2" --config-dir "$3" --ssh-config "$4" open de)
COMP_CWORD=6
COMP_LINE="$2 --config-dir $3 --ssh-config $4 open de"
COMP_POINT=${#COMP_LINE}
__start_tdev
printf '%s\n' "${COMPREPLY[@]}"`
			out, err := exec.Command(shell, "--noprofile", "--norc", "-c", script, "bash", file, binary, root, s.Config).CombinedOutput()
			if err != nil || !strings.Contains(string(out), "demo") {
				t.Fatalf("bash TAB completion: %v %s", err, out)
			}
		} else {
			out, err := exec.Command(shell, "-f", "-c", `autoload -Uz compinit; compinit -D; source "$1"; (( $+functions[_tdev] ))`, "zsh", file).CombinedOutput()
			if err != nil {
				t.Fatalf("zsh completion registration: %v %s", err, out)
			}
		}
	}
	s.Offline(true)
	start := time.Now()
	if out := query("open", ""); strings.Contains(out, "demo") || time.Since(start) > 3*time.Second {
		t.Fatal("offline completion did not fail quietly within budget", out)
	}
}
