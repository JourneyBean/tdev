package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tdev/internal/config"
	"tdev/internal/shellpath"
)

func TestCompletionInstallationPreservesShellFiles(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("build and startup validation runs in Linux fixture")
	}
	binary := buildCLI(t, "0.0.3-dev")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	quotedBinary := filepath.Join(home, "工具 space'$`", "tdev")
	if err := os.MkdirAll(filepath.Dir(quotedBinary), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(quotedBinary, data, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".bashrc", ".profile", "dotfile"} {
		if err = os.WriteFile(filepath.Join(home, name), []byte("# user preferences\nexport EDITOR=vim\n"), 0640); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Symlink(filepath.Join(home, "dotfile"), filepath.Join(home, ".zshrc")); err != nil {
		t.Fatal(err)
	}
	if _, err = shellpath.Write(filepath.Join(home, ".profile"), shellpath.Snippet("/home/developer/.local/bin", "sh")); err != nil {
		t.Fatal(err)
	}
	a := &app{store: config.Store{Root: filepath.Join(home, "config")}}
	result, err := a.installCompletion(context.Background(), quotedBinary, "all")
	if err != nil || len(result.Files) != 3 {
		t.Fatal(result, err)
	}
	for _, change := range result.Files {
		if !change.Changed || change.Backup == "" {
			t.Fatal("missing backup", change)
		}
		content, err := os.ReadFile(change.File)
		if err != nil || !strings.Contains(string(content), "# user preferences") || strings.Count(string(content), "# >>> tdev completion >>>") != 1 {
			t.Fatal("overwrote shell preferences", change, err)
		}
		backup, err := os.ReadFile(change.Backup)
		if err != nil || !strings.Contains(string(backup), "# user preferences") {
			t.Fatal("backup lost original", err)
		}
	}
	content, _ := os.ReadFile(filepath.Join(home, ".profile"))
	if !strings.Contains(string(content), "# >>> tdev PATH >>>") {
		t.Fatal("completion replaced PATH block")
	}
	info, _ := os.Lstat(filepath.Join(home, ".zshrc"))
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced shell symlink")
	}
	info, _ = os.Stat(filepath.Join(home, "dotfile"))
	if info.Mode().Perm() != 0640 {
		t.Fatal("changed startup file mode")
	}
	again, err := a.installCompletion(context.Background(), quotedBinary, "all")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range again.Files {
		if change.Changed || change.Backup != "" {
			t.Fatal("configuration was not idempotent", change)
		}
	}
	if _, err = a.installCompletion(context.Background(), quotedBinary, "fish"); err == nil {
		t.Fatal("accepted unsupported completion shell")
	}
	for _, shell := range []string{"bash", "zsh"} {
		if _, err = exec.LookPath(shell); err != nil {
			continue
		}
		var cmd *exec.Cmd
		if shell == "bash" {
			cmd = exec.Command(shell, "--noprofile", "--rcfile", filepath.Join(home, ".bashrc"), "-ic", "declare -F __start_tdev")
		} else {
			cmd = exec.Command(shell, "-f", "-ic", `source "$1"; (( $+functions[_tdev] ))`, "zsh", filepath.Join(home, ".zshrc"))
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s startup: %v %s", shell, err, out)
		}
	}
	if out, err := exec.Command("sh", "-n", filepath.Join(home, ".profile")).CombinedOutput(); err != nil {
		t.Fatalf("shared profile POSIX syntax: %v %s", err, out)
	}
	executeBinary(t, quotedBinary, a.store.Root, "", "", 0, "completion", "install", "--shell", "all")
}
