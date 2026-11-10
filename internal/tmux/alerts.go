package tmux

import (
	"context"
	"strconv"
)

// Native alerts remain owned by tmux. Only bells in an unfocused current
// window need an additional flag, since tmux immediately acknowledges them.
const alertTitle = "#{?#{||:#{session_alerts},#{W:#{?@tdev_unfocused_bell,1,}}},[!] ,}#S:#W"

func (m Manager) configureAlerts(ctx context.Context, session string) error {
	_, err := m.configureRuntime(ctx, session)
	return err
}

// Commands use tmux's canonical show-hooks spelling so legacy actions can be
// recognized exactly, without deleting user actions that merely mention tdev.
func alertHooks(session string) [][2]string {
	focused := "set-option -t " + strconv.Quote(session) + " @tdev_terminal_focused 1"
	clear := "set-option -w -t " + strconv.Quote(session+":") + " @tdev_unfocused_bell 0"
	return [][2]string{
		{"alert-bell", `if-shell -F "#{&&:#{window_active},#{==:#{@tdev_terminal_focused},0}}" "set-option -w @tdev_unfocused_bell 1"`},
		{"client-focus-out", "set-option -t " + strconv.Quote(session) + " @tdev_terminal_focused 0"},
		{"client-focus-in", focused + " ; " + clear},
		{"client-attached", focused}, {"client-session-changed", focused},
		{"session-window-changed", clear}, {"window-linked", "set-option -w monitor-bell on"},
	}
}
