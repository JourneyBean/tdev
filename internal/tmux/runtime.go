package tmux

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/transfer"
)

const RuntimeConfigVersion = 2

type RuntimeSession struct {
	ID       string          `json:"session_id"`
	Name     string          `json:"session_name,omitempty"`
	Status   string          `json:"status"`
	Version  int             `json:"version"`
	Reattach bool            `json:"reattach_required,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Error    *protocol.Error `json:"error,omitempty"`
}

func (m Manager) configOwner() (string, error) {
	root, err := transfer.Canonical(m.Store.Root)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(root))), nil
}

func noServer(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "No such file or directory") || strings.Contains(err.Error(), "no sessions"))
}

// ReconcileRuntime only visits existing sessions belonging to this store.
// Legacy scratch sessions require explicit adoption because they have no root.
func (m Manager) ReconcileRuntime(ctx context.Context, adoptLegacyScratch bool) ([]RuntimeSession, error) {
	rows := []RuntimeSession{}
	projects, err := m.Store.List()
	if err != nil {
		return rows, err
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return rows, nil
	}
	ownedProjects := map[string]bool{}
	for _, p := range projects {
		ownedProjects[p.ID] = true
	}
	owner, err := m.configOwner()
	if err != nil {
		return rows, err
	}
	listing, err := m.run(ctx, "list-sessions", "-F", "#{session_id}\t#{session_name}\t#{@tdev_project_id}\t#{@tdev_scratch_name}\t#{@tdev_config_owner}\t#{@tdev_generation}\t#{@tdev_initialization}")
	if noServer(err) {
		return rows, nil
	}
	if err != nil {
		return rows, err
	}
	initialFocus, err := m.run(ctx, "show-options", "-sv", "focus-events")
	if err != nil {
		return rows, err
	}
	failed := false
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			return rows, fmt.Errorf("invalid tmux session metadata")
		}
		if f[2] == "" && f[3] == "" {
			continue
		}
		row := RuntimeSession{ID: f[0], Name: f[1], Status: "skipped"}
		switch {
		case f[4] != "" && f[4] != owner:
			row.Reason = "session belongs to another configuration root"
		case f[2] != "" && (!ownedProjects[f[2]] || f[6] != "ready"):
			row.Reason = "project definition is absent or session is incomplete"
		case f[2] == "" && (!config.ValidName(f[3]) || f[5] == ""):
			row.Reason = "scratch ownership metadata is incomplete"
		case f[2] == "" && f[4] == "" && !adoptLegacyScratch:
			row.Reason = "legacy scratch has no configuration owner; attach it or use --adopt-legacy-scratch"
		default:
			row, err = m.configureRuntime(ctx, f[0], f[5])
			row.Name = f[1]
			if initialFocus != "on" && row.Status == "applied" {
				clients, e := m.run(ctx, "list-clients", "-t", f[0], "-F", "#{client_name}")
				if e != nil && err == nil {
					err = e
				}
				row.Reattach = clients != ""
			}
			if err != nil {
				row.Status = "failed"
				row.Error = protocol.Normalize(err)
				failed = true
			}
		}
		rows = append(rows, row)
	}
	if failed {
		return rows, protocol.Fail("PARTIAL_SUCCESS", 9, "some tmux sessions could not be migrated")
	}
	return rows, nil
}

func (m Manager) configureRuntime(ctx context.Context, session string, generation ...string) (row RuntimeSession, err error) {
	row = RuntimeSession{ID: session, Status: "skipped"}
	socket, err := m.run(ctx, "display-message", "-p", "-t", session, "#{socket_path}")
	if err != nil {
		return row, err
	}
	lockHash := sha256.Sum256([]byte(socket + "/" + session))
	key := fmt.Sprintf("tmux-%x", lockHash[:16])
	// Shared state locks serialize the same server/session across config roots.
	unlock, err := (config.Store{Root: config.StateRoot()}).Lock(ctx, key, "runtime")
	if err != nil {
		return row, err
	}
	defer unlock()
	owner, err := m.configOwner()
	if err != nil {
		return row, err
	}
	get := func(option string) (string, error) { return m.run(ctx, "show-options", "-qv", "-t", session, option) }
	if len(generation) > 0 {
		current, e := get("@tdev_generation")
		if e != nil {
			return row, e
		}
		if current != generation[0] {
			return row, protocol.Fail("SESSION_NOT_FOUND", 5, "original tmux session ended; replacement preserved")
		}
	}
	currentOwner, err := get("@tdev_config_owner")
	if err != nil {
		return row, err
	}
	if currentOwner != "" && currentOwner != owner {
		return row, protocol.Fail("CONFIG_CONFLICT", 4, "tmux session belongs to another configuration root")
	}
	version, err := get("@tdev_runtime_config_version")
	if err != nil {
		return row, err
	}
	if version != "" {
		row.Version, err = strconv.Atoi(version)
		if err != nil || row.Version < 0 {
			return row, config.Invalid("invalid tmux runtime configuration version")
		}
		if row.Version > RuntimeConfigVersion {
			return row, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "tmux runtime configuration %d is newer than supported %d; configuration preserved", row.Version, RuntimeConfigVersion)
		}
		if row.Version == RuntimeConfigVersion {
			return row, nil
		}
	}
	legacy, err := get("@tdev_alerts_version")
	if err != nil {
		return row, err
	}
	if legacy != "" && legacy != "1" {
		return row, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "unsupported legacy tmux alerts configuration; configuration preserved")
	}
	hooks := alertHooks(session)
	type action struct{ name, command string }
	existing := map[string][]action{}
	for _, hook := range hooks {
		out, e := m.run(ctx, "show-hooks", "-t", session, hook[0])
		if e != nil {
			return row, e
		}
		if out == "" {
			out, e = m.run(ctx, "show-hooks", "-g", hook[0])
			if e != nil {
				return row, e
			}
		}
		for _, line := range strings.Split(out, "\n") {
			if line == "" || line == hook[0] {
				continue
			}
			name, command, ok := strings.Cut(line, " ")
			if !ok {
				return row, fmt.Errorf("invalid tmux hook %s", hook[0])
			}
			existing[hook[0]] = append(existing[hook[0]], action{name, command})
		}
	}
	index, err := get("@tdev_runtime_hook_index")
	if err != nil {
		return row, err
	}
	if index == "" {
		for n := 9000; n < 10000; n++ {
			candidate := strconv.Itoa(n)
			free := true
			for _, hook := range hooks {
				for _, a := range existing[hook[0]] {
					if a.name == hook[0]+"["+candidate+"]" {
						free = false
					}
				}
			}
			if free {
				index = candidate
				break
			}
		}
	}
	n, e := strconv.Atoi(index)
	if e != nil || n < 9000 || n >= 10000 {
		return row, config.Invalid("no valid tdev hook index available")
	}
	for _, hook := range hooks {
		for _, a := range existing[hook[0]] {
			if a.name == hook[0]+"["+index+"]" && a.command != hook[1] {
				return row, protocol.Fail("CONFIG_CONFLICT", 4, "tdev hook index contains a user action; configuration preserved")
			}
		}
	}
	focus, err := m.run(ctx, "show-options", "-sv", "focus-events")
	if err != nil {
		return row, err
	}
	clients, err := m.run(ctx, "list-clients", "-t", session, "-F", "#{client_name}")
	if err != nil {
		return row, err
	}
	// Existing client processes need the new output filter after this template
	// migration, even when their terminal already reports focus events.
	row.Reattach = clients != "" && (focus != "on" || row.Version < 2)
	// Save ownership before changing hooks; retries use the same reserved index.
	for _, opt := range [][2]string{{"@tdev_config_owner", owner}, {"@tdev_runtime_hook_index", index}} {
		if _, err = m.run(ctx, "set-option", "-t", session, opt[0], opt[1]); err != nil {
			return row, err
		}
	}
	if _, err = m.run(ctx, "set-option", "-s", "focus-events", "on"); err != nil {
		return row, err
	}
	for _, opt := range [][2]string{{"set-titles", "on"}, {"set-titles-string", alertTitle}, {"bell-action", "any"}} {
		if _, err = m.run(ctx, "set-option", "-t", session, opt[0], opt[1]); err != nil {
			return row, err
		}
	}
	focused, err := get("@tdev_terminal_focused")
	if err != nil {
		return row, err
	}
	if focused == "" {
		if _, err = m.run(ctx, "set-option", "-t", session, "@tdev_terminal_focused", "1"); err != nil {
			return row, err
		}
	}
	for _, hook := range hooks {
		for _, a := range existing[hook[0]] {
			if a.name == hook[0]+"["+index+"]" {
				continue
			}
			if a.command == hook[1] {
				// Only exact legacy tdev actions are removed.
				_, err = m.run(ctx, "set-hook", "-u", "-t", session, a.name)
			} else {
				_, err = m.run(ctx, "set-hook", "-t", session, a.name, a.command)
			}
			if err != nil {
				return row, err
			}
		}
		if _, err = m.run(ctx, "set-hook", "-t", session, hook[0]+"["+index+"]", hook[1]); err != nil {
			return row, err
		}
	}
	windows, err := m.run(ctx, "list-windows", "-t", session, "-F", "#{window_id}")
	if err != nil {
		return row, err
	}
	for _, window := range strings.Split(windows, "\n") {
		if _, err = m.run(ctx, "set-option", "-w", "-t", window, "monitor-bell", "on"); err != nil {
			return row, err
		}
	}
	for _, client := range strings.Split(clients, "\n") {
		if client != "" {
			if _, err = m.run(ctx, "refresh-client", "-t", client); err != nil {
				return row, err
			}
		}
	}
	// Keep the legacy marker frozen so old clients skip their initializer.
	if _, err = m.run(ctx, "set-option", "-t", session, "@tdev_alerts_version", "1"); err != nil {
		return row, err
	}
	if _, err = m.run(ctx, "set-option", "-t", session, "@tdev_runtime_config_version", strconv.Itoa(RuntimeConfigVersion)); err != nil {
		return row, err
	}
	row.Version = RuntimeConfigVersion
	row.Status = "applied"
	return row, nil
}
