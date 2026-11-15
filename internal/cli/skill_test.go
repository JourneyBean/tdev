package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tdev/internal/aiskill"
	"tdev/internal/protocol"
)

func TestSkillCommands(t *testing.T) {
	root := t.TempDir()
	run := func(want int, args ...string) protocol.Response {
		t.Helper()
		var out, stderr bytes.Buffer
		code := Execute(append([]string{"--config-dir", t.TempDir(), "--json", "--skills", "detected", "skill"}, args...), strings.NewReader(""), &out, &stderr)
		var response protocol.Response
		if code != want || json.Unmarshal(out.Bytes(), &response) != nil {
			t.Fatalf("exit=%d out=%s err=%s", code, out.String(), stderr.String())
		}
		return response
	}
	run(0, "show")
	run(2, "install", "--agent", "unknown")
	run(0, "install", "--directory", root)
	path := filepath.Join(root, "tdev/SKILL.md")
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, aiskill.Content()) {
		t.Fatalf("installed skill: %v", err)
	}
	if err = os.WriteFile(path, []byte("user edits"), 0600); err != nil {
		t.Fatal(err)
	}
	response := run(9, "install", "--directory", root)
	if response.Error == nil || response.Error.Code != "PARTIAL_SUCCESS" || response.Error.Details == nil {
		t.Fatalf("lost conflict details: %+v", response)
	}
}

func TestSkillsNeedExplicitConsentWithoutTerminal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	var emptyOut, emptyErr bytes.Buffer
	if code := Execute([]string{"--config-dir", t.TempDir(), "--json", "--skills", "detected", "skill", "install"}, strings.NewReader(""), &emptyOut, &emptyErr); code != 0 {
		t.Fatalf("absent agents: %d %s %s", code, &emptyOut, &emptyErr)
	}
	for _, dir := range []string{".agents", ".claude", ".pi"} {
		if _, err := os.Stat(filepath.Join(home, dir)); !os.IsNotExist(err) {
			t.Fatalf("absent agent root created: %s %v", dir, err)
		}
	}
	for _, dir := range []string{".codex", ".claude", ".pi/agent"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"--json", "skill", "install", "-y"},
		{"skill", "install", "-y"},
		{"--json", "--skills", "none", "skill", "install"},
	} {
		var out, stderr bytes.Buffer
		if code := Execute(append([]string{"--config-dir", t.TempDir()}, args...), strings.NewReader("y\ny\ny\n"), &out, &stderr); code != 0 {
			t.Fatalf("skip: %d %s %s", code, &out, &stderr)
		}
		for _, dir := range []string{".agents", ".claude", ".pi/agent"} {
			if _, err := os.Stat(filepath.Join(home, dir, "skills/tdev")); !os.IsNotExist(err) {
				t.Fatalf("unapproved directory: %s %v", dir, err)
			}
		}
	}
	var out, stderr bytes.Buffer
	if code := Execute([]string{"--config-dir", t.TempDir(), "--json", "--skills", "detected", "skill", "install"}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatalf("opt-in: %d %s %s", code, &out, &stderr)
	}
	for _, dir := range []string{".agents", ".claude", ".pi/agent"} {
		if _, err := os.Stat(filepath.Join(home, dir, "skills/tdev/SKILL.md")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSkillPromptsPerAgent(t *testing.T) {
	binary := buildCLI(t, "0.7.0")
	home := t.TempDir()
	for _, dir := range []string{".codex", ".claude", ".pi/agent"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(binary, "--config-dir", t.TempDir(), "skill", "install", "-y")
	cmd.Env = append(os.Environ(), "HOME="+home, "TERM=xterm-256color")
	terminal := terminalCommand(t, cmd)
	for _, item := range []struct{ agent, answer string }{{"codex", "y"}, {"cc", "n"}, {"pi", ""}} {
		terminal.waitText(t, "Install or update tdev skill for "+item.agent+" at")
		terminal.write(t, item.answer+"\r")
	}
	terminal.exit(t, 0)
	if _, err := os.Stat(filepath.Join(home, ".agents/skills/tdev/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".claude", ".pi/agent"} {
		if _, err := os.Stat(filepath.Join(home, dir, "skills/tdev")); !os.IsNotExist(err) {
			t.Fatalf("declined skill written: %s %v", dir, err)
		}
	}
}
