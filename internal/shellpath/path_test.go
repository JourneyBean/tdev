package shellpath

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellSnippetQuotesAndDeduplicates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "项目 space'$`*[x]bin")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, shell := range []string{"sh", "bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skip("shell not installed")
			}
			script := Snippet(dir, shell) + "\n" + Snippet(dir, shell) + "\nprintf '%s' \"$PATH\""
			if shell == "fish" {
				script = Snippet(dir, shell) + "\n" + Snippet(dir, shell) + "\nstring join : $PATH"
			}
			cmd := exec.Command(binary, "-c", script)
			cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if strings.TrimSpace(string(out)) != dir+":/usr/bin:/bin" {
				t.Fatalf("incorrect PATH quoting/deduplication: %q", out)
			}
		})
	}
}

func TestWritePreservesContentBackupAndSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfile")
	original := []byte("# original\nexport EDITOR=vim\n")
	if err := os.WriteFile(target, original, 0640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".zshrc")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	block := Snippet("/home/developer/.local/bin", "zsh")
	first, err := Write(link, block)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(first.Backup)
	if err != nil || string(backup) != string(original) {
		t.Fatal("original content backup missing")
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced symlink")
	}
	info, _ = os.Stat(target)
	if info.Mode().Perm() != 0640 {
		t.Fatal("changed original permissions")
	}
	again, err := Write(link, block)
	if err != nil || again.Changed || again.Backup != "" {
		t.Fatalf("not idempotent: %+v %v", again, err)
	}
	content, _ := os.ReadFile(target)
	if !strings.HasPrefix(string(content), string(original)) || strings.Count(string(content), start) != 1 {
		t.Fatal("changed unrelated shell configuration")
	}
	if _, err := Merge([]byte(start+"\npartial"), block); err == nil {
		t.Fatal("accepted incomplete markers")
	}
}

func TestTargetShells(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	paths, err := Targets(home, "bash", "", "")
	if err != nil || len(paths) != 2 || paths[1] != filepath.Join(home, ".bash_profile") {
		t.Fatal(paths, err)
	}
	paths, err = Targets(home, "fish", "", filepath.Join(home, "配置"))
	if err != nil || paths[0] != filepath.Join(home, "配置/fish/conf.d/tdev.fish") {
		t.Fatal(paths, err)
	}
	if _, err := Targets(home, "unknown", "", ""); err == nil {
		t.Fatal("accepted unsupported shell")
	}
}
