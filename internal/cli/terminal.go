package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"golang.org/x/term"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
	"tdev/internal/sshx"
	"tdev/internal/tmux"
)

func (a *app) attach(ctx context.Context, t target, original tmux.Status, window string, budget *reconnect.Budget) error {
	if !terminalAvailable(a.in) {
		return config.Invalid("attach requires a terminal")
	}
	terminal := a.in.(*os.File)
	state, err := term.GetState(int(terminal.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(terminal.Fd()), state)
	failed := false
	for {
		if failed {
			fmt.Fprintf(a.errOut, "Connection lost. Retrying in %s [%d]. Press Ctrl-C to exit.\n", budget.Policy.Interval, budget.Used+1)
			if err = budget.Next(ctx); err != nil {
				return err
			}
			t.Batch = true
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		data, e := a.call(ctx, t, nil, "session", "status", t.Project)
		if e != nil {
			if reconnect.Retryable(e) {
				failed = true
				continue
			}
			return e
		}
		var current tmux.Status
		if e = json.Unmarshal(data, &current); e != nil {
			return e
		}
		if current.Status != "running" || current.Generation == "" || current.Generation != original.Generation || current.SessionID != original.SessionID {
			return protocol.Fail("SESSION_NOT_FOUND", 5, "original project session ended; reconnect will not recreate it or enter a replacement")
		}
		m, e := sshx.Start(ctx, t.Server.SSHAlias, t.SSH, t.Batch)
		if e != nil {
			if reconnect.Retryable(e) {
				failed = true
				continue
			}
			return e
		}
		token := "a" + config.NewID()
		command := remoteCommand(t, "local", "session", "attach", t.Project, "--session-id", current.SessionID, "--generation", current.Generation, "--token", token)
		if window != "" {
			command += " --window " + tmux.Quote(window)
		}
		cmd := sshx.Command(ctx, "ssh", m.Args(true, command)...)
		cmd.Stdin = a.in
		cmd.Stdout = a.out
		cmd.Stderr = a.errOut
		cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
		if e = cmd.Start(); e != nil {
			m.Close()
			return e
		}
		// Readiness is a structured query of the actual tmux client PID, not a TCP/probe success.
		readyCtx, stopReady := context.WithCancel(ctx)
		ready := make(chan time.Time, 1)
		readyDone := make(chan struct{})
		go func() {
			defer close(readyDone)
			batch := t
			batch.Batch = true
			for {
				b, err := a.call(readyCtx, batch, nil, "session", "attachment", t.Project, "--token", token)
				if err == nil {
					var attachment tmux.Attachment
					if json.Unmarshal(b, &attachment) == nil && attachment.Attached {
						ready <- time.Now()
						return
					}
				}
				if reconnect.Wait(readyCtx, 100*time.Millisecond) != nil {
					return
				}
			}
		}()
		e = cmd.Wait()
		_ = term.Restore(int(terminal.Fd()), state)
		stopReady()
		<-readyDone
		select {
		case since := <-ready:
			budget.Healthy(time.Since(since))
		default:
		}
		var transportErr error
		select {
		case <-m.Closed():
			transportErr = m.Err()
		default:
		}
		m.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e == nil {
			return nil
		}
		exit, ok := e.(*exec.ExitError)
		if ok && exit.ExitCode() == 5 {
			return protocol.Fail("SESSION_NOT_FOUND", 5, "original project session ended before attachment")
		}
		if !ok || exit.ExitCode() != 255 {
			return e
		}
		if transportErr != nil && !reconnect.Retryable(transportErr) {
			return transportErr
		}
		failed = true
	}
}
