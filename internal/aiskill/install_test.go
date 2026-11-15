package aiskill

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"tdev/internal/config"
)

func TestTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, env := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "PI_CODING_AGENT_DIR"} {
		t.Setenv(env, "")
	}
	targets, err := Targets("all", "")
	if err != nil || len(targets) != 3 {
		t.Fatalf("targets: %+v %v", targets, err)
	}
	for i, dir := range []string{".agents/skills/tdev", ".claude/skills/tdev", ".pi/agent/skills/tdev"} {
		if targets[i].Directory != filepath.Join(home, dir) {
			t.Fatal(targets)
		}
	}
	for _, item := range []struct{ agent, env string }{{"codex-legacy", "CODEX_HOME"}, {"cc", "CLAUDE_CONFIG_DIR"}, {"pi", "PI_CODING_AGENT_DIR"}} {
		root := filepath.Join(home, item.agent+" with spaces")
		t.Setenv(item.env, root)
		got, err := Targets(item.agent, "")
		if err != nil || got[0].Directory != filepath.Join(root, "skills/tdev") {
			t.Fatalf("override: %+v %v", got, err)
		}
		t.Setenv(item.env, "relative")
		if _, err = Targets(item.agent, ""); err == nil {
			t.Fatal("accepted relative environment root")
		}
		t.Setenv(item.env, "")
	}
	if _, err = Targets("unknown", ""); err == nil {
		t.Fatal("accepted unknown agent")
	}
	if _, err = Targets("all", "relative"); err == nil {
		t.Fatal("accepted relative custom root")
	}
	if _, err = Targets("cc", home); err == nil {
		t.Fatal("accepted conflicting selection")
	}
	// Once explicitly installed, legacy copies participate in automatic updates.
	legacy, err := Targets("codex-legacy", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Install(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	synced, err := Targets("all", "")
	if err != nil || len(synced) != 4 || synced[3] != legacy[0] {
		t.Fatalf("legacy update targets: %+v %v", synced, err)
	}
	t.Setenv("HOME", "relative")
	if _, err = Targets("all", ""); err == nil {
		t.Fatal("accepted relative HOME")
	}
	t.Setenv("HOME", home)
	custom, err := Targets("all", home)
	if err != nil || custom[0].Directory != filepath.Join(home, "tdev") {
		t.Fatalf("custom: %+v %v", custom, err)
	}
}

func TestInstallLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "tdev")
	content := Content()
	oldSource := []byte("---\nname: tdev\ndescription: Old bundled skill.\n---\nOld workflow.\n")
	old := append(append([]byte{}, oldSource...), []byte(fmt.Sprintf("\n%s%x -->\n", marker, sha256.Sum256(oldSource)))...)
	if !managed(content) || !managed(old) {
		t.Fatal("bundled content not recognized")
	}
	for _, step := range []struct {
		data   []byte
		status string
	}{{old, "installed"}, {content, "updated"}, {content, "unchanged"}, {old, "updated"}} {
		prior, _ := os.Stat(filepath.Join(dir, "SKILL.md"))
		got, err := install(ctx, dir, step.data)
		if err != nil || got != step.status {
			t.Fatalf("install: %s %v", got, err)
		}
		after, _ := os.Stat(filepath.Join(dir, "SKILL.md"))
		if got == "unchanged" && !os.SameFile(prior, after) {
			t.Fatal("unchanged file replaced")
		}
		data, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if !bytes.Equal(data, step.data) {
			t.Fatal("incorrect skill content")
		}
	}
	// Appending or editing a managed file preserves the user's version.
	for _, edited := range [][]byte{append(append([]byte{}, old...), 'x'), bytes.Replace(old, []byte("Old workflow"), []byte("My workflow"), 1), []byte("user skill")} {
		path := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(path, edited, 0600); err != nil {
			t.Fatal(err)
		}
		results, err := Install(ctx, []Target{{"conflict", dir}, {"next", filepath.Join(t.TempDir(), "tdev")}})
		if err == nil || results[0].Status != "failed" || results[1].Status != "installed" {
			t.Fatalf("conflict: %+v %v", results, err)
		}
		actual, _ := os.ReadFile(path)
		if !bytes.Equal(actual, edited) {
			t.Fatal("local edits overwritten")
		}
	}
}

func TestPreserveSymlinksAndDirectories(t *testing.T) {
	for _, kind := range []string{"skill-link", "file-link", "file-directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			other := t.TempDir()
			dir := filepath.Join(root, "tdev")
			original := filepath.Join(other, "SKILL.md")
			if err := os.WriteFile(original, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "skill-link" {
				if err := os.Symlink(other, dir); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "SKILL.md")
				if kind == "file-link" {
					if err := os.Symlink(original, path); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := install(context.Background(), dir, Content()); err == nil {
				t.Fatal("overwrote non-regular path")
			}
			actual, _ := os.ReadFile(original)
			if string(actual) != "keep" {
				t.Fatal("symlink target changed")
			}
		})
	}
}

func TestConcurrentInstallAndCancellation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tdev")
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := install(context.Background(), dir, Content()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	unlock, err := (config.Store{Root: dir}).Lock(context.Background(), "skill", "install")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err = install(ctx, dir, Content()); err == nil {
		t.Fatal("ignored lock cancellation")
	}
}

func TestDetectionIsReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	for _, key := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "PI_CODING_AGENT_DIR"} {
		t.Setenv(key, "")
	}
	targets, err := Targets("all", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := Detect(targets); len(got) != 0 {
		t.Fatalf("detected absent agents: %+v", got)
	}
	for i, name := range []string{"codex", "claude", "pi"} {
		bin := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", filepath.Dir(bin))
		got := Detect(targets)
		if len(got) != 1 || got[0] != targets[i] {
			t.Fatalf("command detection: %+v", got)
		}
	}
	t.Setenv("PATH", t.TempDir())
	for i, dir := range []string{".codex", ".claude", ".pi/agent"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if !Detected(targets[i]) {
			t.Fatalf("missed config directory: %s", dir)
		}
	}
	for _, target := range targets {
		if _, err := os.Stat(target.Directory); !os.IsNotExist(err) {
			t.Fatalf("detection wrote skill directory: %s %v", target.Directory, err)
		}
	}
	custom := filepath.Join(home, "custom-claude")
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	overridden, err := Targets("cc", "")
	if err != nil || Detected(overridden[0]) {
		t.Fatalf("unset custom config: %+v %v", overridden, err)
	}
	if err = os.Mkdir(custom, 0700); err != nil {
		t.Fatal(err)
	}
	if !Detected(overridden[0]) {
		t.Fatal("missed existing custom config")
	}
}
