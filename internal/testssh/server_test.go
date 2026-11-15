package testssh

import (
	"os/exec"
	"strings"
	"testing"
)

func TestIsolatedShellLocaleAndPrompt(t *testing.T) {
	t.Setenv("LANG", "C")
	t.Setenv("LC_ALL", "C")
	s := Start(t)
	cmd := exec.Command("ssh", "-F", s.Config, s.Alias, "bash -lic 'printf \"%s\\n\" \"$LC_ALL\" \"$PS1\"; locale charmap'")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "C.UTF-8\n$ \nUTF-8" {
		t.Fatalf("isolated shell locale/prompt: %q", out)
	}
}
