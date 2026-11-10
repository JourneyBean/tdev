package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
	"tdev/internal/transfer"
)

// Manage serializes lifecycle operations from independent CLI processes.
func (c Client) Manage(ctx context.Context, op, executable, root string) (State, error) {
	unlock, err := (config.Store{Root: c.Dir}).Lock(ctx, "daemon", "lifecycle")
	if err != nil {
		return State{}, err
	}
	defer unlock()
	stopped := filepath.Join(c.Dir, "stopped")
	if op == "ensure" {
		if _, err := os.Stat(stopped); err == nil {
			return State{}, protocol.Fail("DAEMON_STOPPED", 6, "local daemon is stopped; run tdev daemon start")
		} else if !os.IsNotExist(err) {
			return State{}, err
		}
	}
	if op == "start" || op == "restart" {
		if err := os.Remove(stopped); err != nil && !os.IsNotExist(err) {
			return State{}, err
		}
	}
	state, probeErr := c.Call(ctx, Request{Op: "status"})
	if op == "status" {
		return state, probeErr
	}
	// Upgrade refresh must not start a stopped daemon, including when a stop
	// raced with binary replacement. Probe under the same lifecycle lock.
	if op == "refresh" {
		if ctx.Err() != nil {
			return State{}, ctx.Err()
		}
		if probeErr != nil && protocol.Normalize(probeErr).Code == "DAEMON_UNAVAILABLE" {
			return State{Protocol: Protocol}, nil
		}
		if probeErr == nil && state.BinarySHA256 != "" {
			hash, err := transfer.Hash(executable)
			if err != nil {
				return State{}, err
			}
			if hash == state.BinarySHA256 {
				return state, nil
			}
		}
		op = "restart"
	}
	if probeErr != nil && protocol.Normalize(probeErr).Code != "DAEMON_UNAVAILABLE" {
		return State{}, probeErr
	}
	if op == "stop" {
		if err = config.AtomicWrite(stopped, []byte("stopped\n"), 0600); err != nil {
			return State{}, err
		}
	}
	if op == "stop" || op == "restart" {
		if probeErr == nil {
			if _, err = c.Call(ctx, Request{Op: "stop", Preserve: op == "restart"}); err != nil {
				return State{}, err
			}
		}
		// Wait for the lifetime lock, including all transport cleanup, before restart.
		unlockServe, err := (config.Store{Root: c.Dir}).Lock(ctx, "daemon", "serve")
		if err != nil {
			return State{}, err
		}
		if op == "stop" {
			defer unlockServe()
			if err = c.closeTransports(); err != nil {
				return State{}, err
			}
			if err = config.AtomicWrite(filepath.Join(c.Dir, "desired.json"), []byte("{}\n"), 0600); err != nil {
				return State{}, err
			}
			return State{Protocol: Protocol}, nil
		}
		unlockServe()
	} else if probeErr == nil {
		return state, nil
	} else if protocol.Normalize(probeErr).Code != "DAEMON_UNAVAILABLE" {
		return State{}, probeErr
	}
	log, err := os.OpenFile(filepath.Join(c.Dir, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return State{}, err
	}
	defer log.Close()
	cmd := exec.Command(executable, "--config-dir", root, "daemon", "serve")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return State{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		if state, err = c.Call(ctx, Request{Op: "status"}); err == nil {
			return state, nil
		}
		select {
		case err := <-done:
			return State{}, protocol.Fail("DAEMON_UNAVAILABLE", 6, "daemon exited during startup: %v", err)
		default:
		}
		if err = reconnect.Wait(ctx, 20*time.Millisecond); err != nil {
			return State{}, err
		}
	}
}
