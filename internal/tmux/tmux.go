package tmux

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"tdev/internal/config"
	"tdev/internal/protocol"
)

type Manager struct {
	Store  config.Store
	Socket string
}
type Status struct {
	Project     string `json:"project"`
	Status      string `json:"status"`
	SessionID   string `json:"session_id,omitempty"`
	SessionName string `json:"session_name,omitempty"`
	Generation  string `json:"generation,omitempty"`
	Warning     string `json:"warning,omitempty"`
}
type LiveWindow struct {
	ID, Name, Directory, WindowID, Persist string
	Panes                                  int
	Layout                                 string
	Leaves                                 []livePane
}

func SessionName(p config.Project) string { return p.Name }
func Quote(s string) string               { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (m Manager) Args(args ...string) []string {
	if m.Socket != "" {
		return append([]string{"-u", "-L", m.Socket, "-f", "/dev/null"}, args...)
	}
	return append([]string{"-u"}, args...)
}
func (m Manager) run(ctx context.Context, args ...string) (string, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return "", protocol.Fail("DEPENDENCY_MISSING", 7, "tmux is required")
	}
	c := exec.CommandContext(ctx, "tmux", m.Args(args...)...)
	var stderr strings.Builder
	c.Stderr = &stderr
	b, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSuffix(string(b), "\n"), nil
}
func (m Manager) Status(ctx context.Context, p config.Project) (Status, error) {
	s := Status{Project: p.Name, Status: "missing"}
	name := SessionName(p)
	out, err := m.run(ctx, "list-sessions", "-F", "#{session_id}\t#{@tdev_project_id}\t#{@tdev_initialization}\t#{@tdev_generation}\t#{session_name}")
	if err != nil {
		// Only the documented no-server conditions mean missing; socket/permission failures remain errors.
		msg := err.Error()
		if strings.Contains(msg, "no server running") || strings.Contains(msg, "No such file or directory") {
			return s, nil
		}
		return s, err
	}
	var owned []string
	var occupied string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.SplitN(line, "\t", 5)
		if len(fields) != 5 {
			return s, fmt.Errorf("invalid tmux session metadata")
		}
		if fields[4] == name && fields[1] != p.ID {
			occupied = fields[0]
		}
		if fields[1] == p.ID {
			if owned != nil {
				s.Status = "unmanaged"
				s.Warning = "multiple tmux sessions carry this project's UUID; inspect their ownership"
				return s, nil
			}
			owned = fields
		}
	}
	if owned != nil {
		s.SessionID, s.Generation, s.SessionName = owned[0], owned[3], owned[4]
		s.Status = "running"
		if owned[2] != "ready" {
			s.Status = "incomplete"
		}
		if s.SessionName != name {
			s.Warning = fmt.Sprintf("project session is named %q; expected %q; matched by project UUID", s.SessionName, name)
		}
	} else if occupied != "" {
		s.Status = "unmanaged"
		s.SessionName = name
		s.Warning = fmt.Sprintf("tmux session %q (%s) has no matching project UUID; existing session preserved", name, occupied)
	}
	return s, nil
}

func unmanagedSession(s Status) error {
	return protocol.Fail("SESSION_UNMANAGED", 4, "%s; inspect the existing sessions before running this operation", s.Warning)
}

func (m Manager) Require(ctx context.Context, p config.Project) (Status, error) {
	s, err := m.Status(ctx, p)
	if err != nil {
		return s, err
	}
	if s.Status == "missing" {
		return s, protocol.Fail("SESSION_NOT_FOUND", 5, "project session is missing; run open first")
	}
	if s.Status == "unmanaged" {
		return s, unmanagedSession(s)
	}
	if s.Status != "running" {
		return s, protocol.Fail("SESSION_INCOMPLETE", 1, "session initialization is incomplete; inspect it before stopping and recreating")
	}
	return s, nil
}
func (m Manager) Ensure(ctx context.Context, p config.Project) (Status, error) {
	unlock, err := m.Store.Lock(ctx, p.Name, "session")
	if err != nil {
		return Status{}, err
	}
	defer unlock()
	s, err := m.Status(ctx, p)
	if err != nil {
		return s, err
	}
	name := SessionName(p)
	if s.Status == "unmanaged" && s.SessionName == name {
		// Keep an unrelated same-name session and create a separate owned session.
		name = "tdev-" + strings.ReplaceAll(p.ID, "-", "")
	} else if s.Status != "missing" {
		if s.Status == "running" && s.Generation == "" {
			if _, err = m.run(ctx, "set-option", "-t", s.SessionID, "@tdev_generation", config.NewID()); err != nil {
				return s, err
			}
		}
		return m.Require(ctx, p)
	}
	// Validate every directory before making any runtime changes.
	for _, w := range p.Windows {
		if w.Layout != nil {
			if err := validatePaneDirectories(p, *w.Layout); err != nil {
				return s, err
			}
		}
		info, err := os.Stat(config.WindowPath(p, w))
		if err != nil || !info.IsDir() {
			return s, config.Invalid("window directory missing: %s", config.WindowPath(p, w))
		}
	}
	id, err := m.run(ctx, "new-session", "-d", "-P", "-F", "#{session_id}", "-s", name, "-n", p.Windows[0].Name, "-c", config.WindowPath(p, p.Windows[0]))
	if err != nil {
		return s, err
	}
	// From here on any failure leaves a visible incomplete session; never replay commands automatically.
	for _, opt := range [][2]string{{"@tdev_project_id", p.ID}, {"@tdev_project_name", p.Name}, {"@tdev_schema_version", "1"}, {"@tdev_initialization", "initializing"}, {"@tdev_generation", config.NewID()}} {
		if _, err = m.run(ctx, "set-option", "-t", id, opt[0], opt[1]); err != nil {
			return s, err
		}
	}
	for i, w := range p.Windows {
		var wid string
		if i == 0 {
			wid, err = m.run(ctx, "display-message", "-p", "-t", id, "#{window_id}")
		} else {
			wid, err = m.run(ctx, "new-window", "-d", "-P", "-F", "#{window_id}", "-t", id+":", "-n", w.Name, "-c", config.WindowPath(p, w))
		}
		if err != nil {
			return s, err
		}
		if err = m.markWindow(ctx, wid, w.ID, true); err != nil {
			return s, err
		}
		pane, err := m.run(ctx, "display-message", "-p", "-t", wid, "#{pane_id}")
		if err != nil {
			return s, err
		}
		if w.Layout != nil {
			if _, err = m.run(ctx, "set-window-option", "-t", wid, "remain-on-exit", "on"); err != nil {
				return s, err
			}
			if err = m.restoreLayout(ctx, p, pane, *w.Layout); err != nil {
				return s, err
			}
			continue
		}
		if w.Command != "" {
			if _, err = m.run(ctx, "set-window-option", "-t", wid, "remain-on-exit", "on"); err != nil {
				return s, err
			}
		}
		if err = m.startPane(ctx, pane, w.ID, config.WindowPath(p, w), w.Command); err != nil {
			return s, err
		}
	}
	if _, err = m.run(ctx, "set-option", "-t", id, "@tdev_initialization", "ready"); err != nil {
		return s, err
	}
	return m.Status(ctx, p)
}
func (m Manager) markWindow(ctx context.Context, wid, id string, persist bool) error {
	value := "0"
	if persist {
		value = "1"
	}
	for _, opt := range [][2]string{{"@tdev_window_id", id}, {"@tdev_managed", "1"}, {"@tdev_persist", value}, {"automatic-rename", "off"}} {
		if _, err := m.run(ctx, "set-window-option", "-t", wid, opt[0], opt[1]); err != nil {
			return err
		}
	}
	return nil
}
func (m Manager) Stop(ctx context.Context, p config.Project) (Status, error) {
	unlock, err := m.Store.Lock(ctx, p.Name, "session")
	if err != nil {
		return Status{}, err
	}
	defer unlock()
	s, err := m.Status(ctx, p)
	if err != nil {
		return s, err
	}
	if s.Status == "missing" {
		return s, nil
	}
	if s.Status == "unmanaged" {
		return s, unmanagedSession(s)
	}
	_, err = m.run(ctx, "kill-session", "-t", s.SessionID)
	if err != nil {
		return s, err
	}
	return m.Status(ctx, p)
}
func (m Manager) Windows(ctx context.Context, s Status) ([]LiveWindow, error) {
	out, err := m.run(ctx, "list-windows", "-t", s.SessionID, "-F", "#{window_id}\t#{@tdev_window_id}\t#{window_name}\t#{window_panes}\t#{@tdev_persist}\t#{window_layout}")
	if err != nil {
		return nil, err
	}
	var windows []LiveWindow
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 6 {
			return nil, config.Invalid("window metadata contains unsupported control characters")
		}
		n, _ := strconv.Atoi(f[3])
		dir, err := m.run(ctx, "display-message", "-p", "-t", f[0], "#{pane_current_path}")
		if err != nil {
			return nil, err
		}
		if dir == "" {
			dir, err = m.run(ctx, "display-message", "-p", "-t", f[0], "#{pane_start_path}")
			if err != nil {
				return nil, err
			}
		}
		panes, err := m.run(ctx, "list-panes", "-t", f[0], "-F", "#{pane_id}\t#{@tdev_pane_id}\t#{?pane_current_path,#{pane_current_path},#{pane_start_path}}")
		if err != nil {
			return nil, err
		}
		w := LiveWindow{WindowID: f[0], ID: f[1], Name: f[2], Panes: n, Persist: f[4], Directory: dir, Layout: f[5]}
		for _, line := range strings.Split(panes, "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) != 3 || fields[2] == "" {
				return nil, config.Invalid("invalid pane metadata")
			}
			w.Leaves = append(w.Leaves, livePane{PaneID: fields[0], ID: fields[1], Directory: fields[2]})
		}
		windows = append(windows, w)
	}
	return windows, nil
}
func (m Manager) Save(ctx context.Context, p config.Project, expected int) (config.Project, error) {
	unlock, err := m.Store.Lock(ctx, p.Name, "session")
	if err != nil {
		return p, err
	}
	defer unlock()
	s, err := m.Require(ctx, p)
	if err != nil {
		return p, err
	}
	before, err := m.Windows(ctx, s)
	if err != nil {
		return p, err
	}
	old := map[string]config.Window{}
	for _, w := range p.Windows {
		old[w.ID] = w
	}
	var windows []config.Window
	adopt := map[string]string{}
	adoptPanes := map[string]string{}
	for _, w := range before {
		if w.Persist == "0" {
			continue
		}
		v, ok := old[w.ID]
		if !ok {
			v = config.Window{ID: "w" + strings.ReplaceAll(config.NewID(), "-", "")}
			adopt[w.WindowID] = v.ID
		}
		v.Name = w.Name
		v.Directory = w.Directory
		layout, err := savedLayout(w, v, adoptPanes)
		if err != nil {
			return p, err
		}
		if layout.Split == "" {
			v.Layout = nil
			v.Directory, v.Command = layout.Directory, layout.Command
			// Single-pane definitions use the window ID as their pane identity.
			adoptPanes[w.Leaves[0].PaneID] = v.ID
		} else {
			v.Layout = layout
			v.Command = ""
		}
		windows = append(windows, v)
	}
	after, err := m.Windows(ctx, s)
	if err != nil {
		return p, err
	}
	if !reflect.DeepEqual(before, after) {
		return p, protocol.Fail("SESSION_CONFLICT", 4, "session changed while collecting windows")
	}
	if expected == 0 {
		expected = p.Revision
	}
	saved, err := m.Store.Update(ctx, p.Name, expected, func(dst *config.Project) error { dst.Windows = windows; return nil })
	if err != nil {
		return p, err
	}
	for wid, id := range adopt {
		if err = m.markWindow(ctx, wid, id, true); err != nil {
			return saved, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "configuration saved but adopted window marker update failed: " + err.Error(), Details: saved}
		}
	}
	for pane, id := range adoptPanes {
		if _, err = m.run(ctx, "set-option", "-p", "-t", pane, "@tdev_pane_id", id); err != nil {
			return saved, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "configuration saved but pane marker update failed", Details: saved}
		}
	}
	return saved, nil
}
func (m Manager) AddWindow(ctx context.Context, p config.Project, name string, persist bool) (map[string]string, error) {
	unlock, err := m.Store.Lock(ctx, p.Name, "session")
	if err != nil {
		return nil, err
	}
	defer unlock()
	s, err := m.Require(ctx, p)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = "shell"
	}
	if !config.SafeText(name) {
		return nil, config.Invalid("invalid window name")
	}
	id := "w" + strings.ReplaceAll(config.NewID(), "-", "")
	wid, err := m.run(ctx, "new-window", "-d", "-P", "-F", "#{window_id}", "-t", s.SessionID+":", "-n", name, "-c", p.Directory)
	if err != nil {
		return nil, err
	}
	if err = m.markWindow(ctx, wid, id, false); err != nil {
		return nil, err
	}
	if _, err = m.run(ctx, "set-window-option", "-t", wid, "remain-on-exit", "off"); err != nil {
		return nil, err
	}
	result := map[string]string{"window_id": wid, "id": id, "session_id": s.SessionID, "persisted": "false"}
	if persist {
		_, err = m.Store.Update(ctx, p.Name, p.Revision, func(dst *config.Project) error {
			dst.Windows = append(dst.Windows, config.Window{ID: id, Name: name, Directory: "."})
			return nil
		})
		if err != nil {
			return result, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "window created but not saved: " + err.Error(), Details: result}
		}
		if err = m.markWindow(ctx, wid, id, true); err != nil {
			return result, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "window saved but runtime marker update failed: " + err.Error(), Details: result}
		}
		result["persisted"] = "true"
	}
	return result, nil
}
func (m Manager) Context(ctx context.Context) (config.Project, error) {
	if os.Getenv("TMUX") == "" {
		return config.Project{}, protocol.Fail("NO_PROJECT_CONTEXT", 2, "specify a project or run inside a managed tmux session")
	}
	out, err := m.run(ctx, "display-message", "-p", "#{@tdev_project_name}\t#{@tdev_project_id}")
	if err != nil {
		return config.Project{}, err
	}
	f := strings.Split(out, "\t")
	if len(f) != 2 || !config.ValidName(f[0]) {
		return config.Project{}, protocol.Fail("NO_PROJECT_CONTEXT", 2, "current session has no tdev project marker")
	}
	p, err := m.Store.Get(f[0])
	if err != nil {
		return p, err
	}
	if p.ID != f[1] {
		return p, protocol.Fail("SESSION_CONFLICT", 4, "project identity does not match session")
	}
	return p, nil
}
func (m Manager) CheckSaveContext(ctx context.Context, p config.Project) error {
	if os.Getenv("TMUX") == "" {
		return nil
	}
	current, err := m.Context(ctx)
	if err != nil {
		return err
	}
	if current.ID != p.ID {
		return protocol.Fail("SESSION_CONFLICT", 4, "current session belongs to %q, not %q", current.Name, p.Name)
	}
	return nil
}
func (m Manager) Select(ctx context.Context, wid string) error {
	_, err := m.run(ctx, "select-window", "-t", wid)
	return err
}
func (m Manager) Doctor(ctx context.Context) (any, error) {
	v, err := m.run(ctx, "-V")
	if err != nil {
		return nil, err
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	_, err = os.Stat(filepath.Clean(shell))
	return map[string]any{"tmux": v, "shell": shell, "config_root": m.Store.Root}, err
}
