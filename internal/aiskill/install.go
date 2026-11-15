// Package aiskill distributes the versioned tdev skill with the executable.
package aiskill

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"tdev/internal/config"
)

//go:embed tdev/SKILL.md
var source []byte

const marker = "<!-- tdev-managed-sha256: "

// Content includes an integrity marker so updates preserve locally edited skills.
func Content() []byte {
	return append(append([]byte{}, source...), []byte(fmt.Sprintf("\n%s%x -->\n", marker, sha256.Sum256(source)))...)
}

type Target struct {
	Agent     string `json:"agent"`
	Directory string `json:"directory"`
}
type Result struct {
	Target
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Targets selects user-level discovery directories. directory is a skills root,
// not the tdev subdirectory, and allows other Agent Skills compatible tools.
func Targets(agent, directory string) ([]Target, error) {
	if directory != "" {
		if agent != "all" {
			return nil, config.Invalid("--directory cannot be combined with --agent")
		}
		if !filepath.IsAbs(directory) {
			return nil, config.Invalid("skill directory must be absolute")
		}
		return []Target{{"custom", filepath.Join(filepath.Clean(directory), "tdev")}}, nil
	}
	switch agent {
	case "all", "codex", "codex-legacy", "cc", "pi":
	default:
		return nil, config.Invalid("agent must be all, codex, codex-legacy, cc or pi")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(home) {
		return nil, config.Invalid("HOME must be absolute")
	}
	root := func(env, fallback string) (string, error) {
		if v := os.Getenv(env); v != "" {
			if !filepath.IsAbs(v) {
				return "", config.Invalid("%s must be absolute", env)
			}
			return filepath.Clean(v), nil
		}
		return filepath.Join(home, fallback), nil
	}
	agents := []string{agent}
	if agent == "all" {
		agents = []string{"codex", "cc", "pi"}
	}
	targets := []Target{}
	for _, a := range agents {
		var dir string
		switch a {
		case "codex":
			dir = filepath.Join(home, ".agents")
		case "codex-legacy":
			dir, err = root("CODEX_HOME", ".codex")
		case "cc":
			dir, err = root("CLAUDE_CONFIG_DIR", ".claude")
		case "pi":
			dir, err = root("PI_CODING_AGENT_DIR", ".pi/agent")
		}
		if err != nil {
			return nil, err
		}
		targets = append(targets, Target{a, filepath.Join(dir, "skills", "tdev")})
	}
	// Keep an explicitly installed legacy copy in sync on later upgrades.
	// Fresh installations use only the current Codex discovery location.
	if agent == "all" {
		legacy, e := root("CODEX_HOME", ".codex")
		if e != nil {
			return nil, e
		}
		legacy = filepath.Join(legacy, "skills", "tdev")
		if _, e = os.Lstat(legacy); e == nil && legacy != targets[0].Directory {
			targets = append(targets, Target{"codex-legacy", legacy})
		} else if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
	}
	return targets, nil
}

// Detected reports an agent command on PATH or an existing agent config directory.
// It is read-only: creating the skill directory must follow explicit consent.
func Detected(target Target) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	command, dirs := "", []string{filepath.Dir(filepath.Dir(target.Directory))}
	switch target.Agent {
	case "codex", "codex-legacy":
		command = "codex"
		root := os.Getenv("CODEX_HOME")
		if root == "" {
			root = filepath.Join(home, ".codex")
		}
		dirs = []string{root}
		if info, e := os.Stat(target.Directory); e == nil && info.IsDir() {
			return true
		}
	case "cc":
		command = "claude"
	case "pi":
		command = "pi"
	case "custom":
		return true
	}
	if command != "" {
		if _, err := exec.LookPath(command); err == nil {
			return true
		}
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func Detect(targets []Target) []Target {
	detected := []Target{}
	for _, target := range targets {
		if Detected(target) {
			detected = append(detected, target)
		}
	}
	return detected
}

// Install attempts every target even when another target fails.
func Install(ctx context.Context, targets []Target) ([]Result, error) {
	results := []Result{}
	var failures []error
	for _, target := range targets {
		status, err := install(ctx, target.Directory, Content())
		result := Result{Target: target, Status: status}
		if err != nil {
			result.Status, result.Error = "failed", err.Error()
			failures = append(failures, fmt.Errorf("%s: %w", target.Directory, err))
		}
		results = append(results, result)
	}
	return results, errors.Join(failures...)
}

func regular(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("preserving non-regular skill file %s", path)
	}
	return nil
}

func managed(data []byte) bool {
	i := bytes.LastIndex(data, []byte("\n"+marker))
	if i < 0 {
		return false
	}
	expected := fmt.Sprintf("\n%s%x -->\n", marker, sha256.Sum256(data[:i]))
	return string(data[i:]) == expected
}

func install(ctx context.Context, dir string, content []byte) (string, error) {
	// Parent roots may be symlinks (for example a user's dotfiles directory).
	// The skill directory and entrypoint themselves must remain owned files.
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("preserving non-directory skill path %s", dir)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	unlock, err := (config.Store{Root: dir}).Lock(ctx, "skill", "install")
	if err != nil {
		return "", err
	}
	defer unlock()
	path := filepath.Join(dir, "SKILL.md")
	if err = regular(path); err != nil {
		return "", err
	}
	prior, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if exists && bytes.Equal(prior, content) {
		return "unchanged", nil
	}
	if exists && !managed(prior) {
		return "", fmt.Errorf("preserving existing or modified skill; move %s aside and retry", path)
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = config.AtomicWrite(path, content, 0644); err != nil {
		return "", err
	}
	if exists {
		return "updated", nil
	}
	return "installed", nil
}
