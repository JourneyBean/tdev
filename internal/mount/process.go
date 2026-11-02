package mount

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

type Process struct {
	PID  int
	Done <-chan struct{}
	Err  func() error
	Stop func()
}

func ProbeName(token string) string { return "tdev-probe-" + token + "-中文_日本語_한글_é" }

// SSHFS is itself the local filesystem service. Foreground mode avoids macFUSE's unsafe fork path;
// its own session and log descriptors let it survive the short-lived `tdev mount` command.
func launch(_ context.Context, logPath string, args ...string) (*Process, error) {
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	cmd := exec.Command("sshfs", args...)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	p := &Process{PID: cmd.Process.Pid, Done: done, Err: func() error { <-done; return waitErr }, Stop: func() { _ = cmd.Process.Kill(); <-done }}
	return p, nil
}
