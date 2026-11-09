package tmux

import (
	"context"
	"fmt"
	"strings"
)

// Native alerts remain owned by tmux. Only bells in an unfocused current
// window need an additional flag, since tmux immediately acknowledges them.
const alertTitle = "#{?#{||:#{session_alerts},#{W:#{?@tdev_unfocused_bell,1,}}},[!] ,}#S:#W"

func (m Manager) configureAlerts(ctx context.Context, session string) error {
	unlock, err := m.Store.Lock(ctx, "tmux-alerts-"+strings.TrimPrefix(session, "$"), "runtime")
	if err != nil {
		return err
	}
	defer unlock()
	version, err := m.run(ctx, "show-options", "-qv", "-t", session, "@tdev_alerts_version")
	if err != nil || version == "1" {
		return err
	}
	// focus-events is necessarily server-wide; the title and hooks below are
	// session-local. Enable it before attaching so terminal reporting is requested.
	args := []string{"set-option", "-s", "focus-events", "on"}
	for _, opt := range [][2]string{{"set-titles", "on"}, {"set-titles-string", alertTitle}, {"bell-action", "any"}, {"@tdev_terminal_focused", "1"}} {
		args = append(args, ";", "set-option", "-t", session, opt[0], opt[1])
	}
	clear := "set-option -w -t " + Quote(session+":") + " @tdev_unfocused_bell 0"
	focused := "set-option -t " + Quote(session) + " @tdev_terminal_focused 1"
	hooks := [][2]string{
		{"alert-bell", `if-shell -F '#{&&:#{window_active},#{==:#{@tdev_terminal_focused},0}}' 'set-option -w @tdev_unfocused_bell 1'`},
		{"client-focus-out", "set-option -t " + Quote(session) + " @tdev_terminal_focused 0"},
		{"client-focus-in", focused + " ; " + clear},
		{"client-attached", focused},
		{"client-session-changed", focused},
		{"session-window-changed", clear},
		{"window-linked", "set-option -w monitor-bell on"},
	}
	for _, hook := range hooks {
		local, err := m.run(ctx, "show-hooks", "-t", session, hook[0])
		if err != nil {
			return err
		}
		if local == "" {
			// A local hook array shadows its global parent. Copy inherited
			// commands before appending ours, retaining their original indexes.
			inherited, err := m.run(ctx, "show-hooks", "-g", hook[0])
			if err != nil {
				return err
			}
			for _, line := range strings.Split(inherited, "\n") {
				if line == "" || line == hook[0] {
					continue
				}
				name, command, ok := strings.Cut(line, " ")
				if !ok {
					return fmt.Errorf("invalid tmux hook %s", hook[0])
				}
				args = append(args, ";", "set-hook", "-t", session, name, command)
			}
		}
		args = append(args, ";", "set-hook", "-a", "-t", session, hook[0], hook[1])
	}
	windows, err := m.run(ctx, "list-windows", "-t", session, "-F", "#{window_id}")
	if err != nil {
		return err
	}
	for _, window := range strings.Split(windows, "\n") {
		args = append(args, ";", "set-option", "-w", "-t", window, "monitor-bell", "on")
	}
	args = append(args, ";", "set-option", "-t", session, "@tdev_alerts_version", "1")
	_, err = m.run(ctx, args...)
	return err
}
