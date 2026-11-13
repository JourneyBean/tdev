package terminal

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// StartRelay gives a local tmux client its own controlling PTY. tmux writes to
// that TTY directly, so redirecting only its stdout cannot filter its output.
// The returned wait function must be called once, including on setup failures
// after StartRelay succeeds. It drains output and restores the caller's TTY.
func StartRelay(cmd *exec.Cmd, in *os.File, out io.Writer) (func() error, error) {
	size, err := pty.GetsizeFull(in)
	if err != nil {
		return nil, err
	}
	state, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	master, err := pty.StartWithSize(cmd, size)
	if err != nil {
		_ = term.Restore(int(in.Fd()), state)
		return nil, err
	}
	fd := int(master.Fd())
	if err = unix.SetNonblock(fd, true); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = master.Close()
		_ = term.Restore(int(in.Fd()), state)
		return nil, err
	}
	done := make(chan struct{})
	inputDone, outputDone := make(chan error, 1), make(chan error, 1)
	resized := make(chan os.Signal, 1)
	resizeDone := make(chan struct{})
	signal.Notify(resized, syscall.SIGWINCH)
	go func() {
		defer close(resizeDone)
		for {
			select {
			case <-done:
				return
			case <-resized:
				if size, e := pty.GetsizeFull(in); e == nil {
					// Do not call master.Fd again: os.File may restore blocking
					// mode, which would prevent cancellation under input backpressure.
					_ = unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: size.Rows, Col: size.Cols, Xpixel: size.X, Ypixel: size.Y})
				}
			}
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			select {
			case <-done:
				inputDone <- nil
				return
			default:
			}
			poll := []unix.PollFd{{Fd: int32(in.Fd()), Events: unix.POLLIN}}
			if _, e := unix.Poll(poll, 100); e != nil {
				if errors.Is(e, unix.EINTR) {
					continue
				}
				inputDone <- e
				_ = cmd.Process.Kill()
				return
			}
			if poll[0].Revents == 0 {
				continue
			}
			n, e := unix.Read(int(in.Fd()), buf)
			if errors.Is(e, unix.EINTR) || errors.Is(e, unix.EAGAIN) {
				continue
			}
			if e != nil || n == 0 {
				inputDone <- e
				_ = cmd.Process.Kill()
				return
			}
			for data := buf[:n]; len(data) > 0; {
				select {
				case <-done:
					inputDone <- nil
					return
				default:
				}
				written, e := unix.Write(fd, data)
				if errors.Is(e, unix.EINTR) {
					continue
				}
				if errors.Is(e, unix.EAGAIN) {
					_, _ = unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}, 100)
					continue
				}
				if e != nil {
					inputDone <- e
					_ = cmd.Process.Kill()
					return
				}
				data = data[written:]
			}
		}
	}()
	go func() {
		buf := make([]byte, 32*1024)
		for {
			poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			_, e := unix.Poll(poll, 100)
			if errors.Is(e, unix.EINTR) {
				continue
			}
			if e != nil {
				outputDone <- e
				_ = cmd.Process.Kill()
				return
			}
			if poll[0].Revents == 0 {
				select {
				case <-done:
					outputDone <- nil
					return
				default:
					continue
				}
			}
			n, e := unix.Read(fd, buf)
			if n > 0 {
				if e = writeOutput(out, buf[:n]); e != nil {
					outputDone <- e
					_ = cmd.Process.Kill()
					return
				}
				continue
			}
			if errors.Is(e, unix.EINTR) || errors.Is(e, unix.EAGAIN) {
				continue
			}
			if errors.Is(e, unix.EIO) {
				e = nil
			} // PTY slave closed on Linux.
			outputDone <- e
			return
		}
	}()
	return func() error {
		err := cmd.Wait()
		close(done)
		signal.Stop(resized)
		<-resizeDone
		inputErr, outputErr := <-inputDone, <-outputDone
		_ = master.Close()
		restoreErr := term.Restore(int(in.Fd()), state)
		return errors.Join(err, inputErr, outputErr, restoreErr)
	}, nil
}
