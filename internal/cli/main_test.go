package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	state, err := os.MkdirTemp("", "tdev-test-state-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = os.Setenv("XDG_STATE_HOME", state); err != nil {
		os.RemoveAll(state)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Preserve Go caches before isolating HOME for installation subprocesses.
	originalHome, _ := os.UserHomeDir()
	if os.Getenv("GOCACHE") == "" {
		if cache, e := os.UserCacheDir(); e == nil {
			_ = os.Setenv("GOCACHE", filepath.Join(cache, "go-build"))
		}
	}
	if os.Getenv("GOPATH") == "" {
		_ = os.Setenv("GOPATH", filepath.Join(originalHome, "go"))
	}
	home, err := os.MkdirTemp("", "tdev-test-home-")
	if err != nil {
		os.RemoveAll(state)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, key := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "PI_CODING_AGENT_DIR"} {
		_ = os.Unsetenv(key)
	}
	if err = os.Setenv("HOME", home); err != nil {
		os.RemoveAll(home)
		os.RemoveAll(state)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Agent executables on the developer's PATH must not influence prompts.
	var testPath []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		agentDir := false
		for _, name := range []string{"codex", "claude", "pi"} {
			if _, e := os.Stat(filepath.Join(dir, name)); e == nil {
				agentDir = true
			}
		}
		if agentDir {
			// Shadow only agent commands, preserving every other tool even
			// when agents share a directory with Go, SSH or tmux.
			entries, e := os.ReadDir(dir)
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				os.RemoveAll(home)
				os.RemoveAll(state)
				os.Exit(1)
			}
			shadow, e := os.MkdirTemp(home, "tool-path-")
			if e != nil {
				fmt.Fprintln(os.Stderr, e)
				os.RemoveAll(home)
				os.RemoveAll(state)
				os.Exit(1)
			}
			for _, entry := range entries {
				if entry.Name() == "codex" || entry.Name() == "claude" || entry.Name() == "pi" {
					continue
				}
				source, e := filepath.Abs(filepath.Join(dir, entry.Name()))
				if e == nil {
					e = os.Symlink(source, filepath.Join(shadow, entry.Name()))
				}
				if e != nil {
					fmt.Fprintln(os.Stderr, e)
					os.RemoveAll(home)
					os.RemoveAll(state)
					os.Exit(1)
				}
			}
			testPath = append(testPath, shadow)
		} else {
			testPath = append(testPath, dir)
		}
	}
	_ = os.Setenv("PATH", strings.Join(testPath, string(os.PathListSeparator)))
	code := m.Run()
	os.RemoveAll(home)
	os.RemoveAll(state)
	os.Exit(code)
}
