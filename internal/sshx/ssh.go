// Package sshx runs system OpenSSH. It does not implement SSH or manage authentication secrets.
package sshx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
)

type Options struct {
	ConfigFile          string        `yaml:"-"`
	ConnectTimeout      time.Duration `yaml:"connect_timeout"`
	ServerAliveInterval time.Duration `yaml:"server_alive_interval"`
	ServerAliveCountMax int           `yaml:"server_alive_count_max"`
}

func Defaults() Options {
	return Options{ConnectTimeout: 10 * time.Second, ServerAliveInterval: 15 * time.Second, ServerAliveCountMax: 3}
}
func (o Options) Validate() error {
	if o.ConnectTimeout <= 0 || o.ServerAliveInterval <= 0 || o.ServerAliveCountMax < 1 {
		return config.Invalid("SSH timeouts/count must be positive")
	}
	return nil
}
func (o Options) Args(batch bool) []string {
	args := []string{}
	if o.ConfigFile != "" {
		args = append(args, "-F", o.ConfigFile)
	}
	args = append(args, "-o", "LogLevel=INFO", "-o", "ConnectTimeout="+strconv.Itoa(max(1, int(o.ConnectTimeout.Seconds()))), "-o", "ServerAliveInterval="+strconv.Itoa(max(1, int(o.ServerAliveInterval.Seconds()))), "-o", "ServerAliveCountMax="+strconv.Itoa(o.ServerAliveCountMax), "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ClearAllForwardings=yes", "-o", "RemoteCommand=none", "-o", "RequestTTY=no")
	if batch {
		args = append(args, "-o", "BatchMode=yes")
	}
	return args
}
func Environment() []string {
	// Preserve LC_CTYPE/LC_ALL: SSH may forward these to remote applications via SendEnv.
	// Forcing C here breaks CJK input, display widths and tmux's machine-format output.
	return os.Environ()
}
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	c.Env = Environment()
	c.WaitDelay = time.Second
	return c
}
func Error(err error, stderr string) error {
	if err == nil {
		return nil
	}
	return protocol.Fail("CONNECTION_FAILED", 6, "OpenSSH: %v: %s", err, strings.TrimSpace(stderr))
}

type Buffer struct {
	mu sync.Mutex
	b  []byte
}

func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.b = append(b.b, p...)
	if len(b.b) > 64*1024 {
		b.b = b.b[len(b.b)-64*1024:]
	}
	return len(p), nil
}
func (b *Buffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.b) }

// Master owns a unique socket, one child process and no user-shared ControlMaster.
type Master struct {
	Socket, Dir string
	cmd         *exec.Cmd
	done        chan struct{}
	err         error
	stderr      Buffer
	close       sync.Once
}

func Start(ctx context.Context, alias string, opts Options, batch bool) (*Master, error) {
	if _, err := exec.LookPath("ssh"); err != nil {
		return nil, protocol.Fail("DEPENDENCY_MISSING", 7, "OpenSSH ssh is required")
	}
	dir, err := os.MkdirTemp("/tmp", "tdev-ssh-")
	if err != nil {
		return nil, err
	}
	m := &Master{Dir: dir, Socket: filepath.Join(dir, "m"), done: make(chan struct{})}
	// Ownership options must precede all other SSH options (OpenSSH uses first value).
	args := []string{"-M", "-S", m.Socket, "-o", "ControlMaster=yes", "-o", "ControlPersist=no"}
	args = append(args, opts.Args(batch)...)
	args = append(args, "-N", "-T", alias)
	// The owner closes the master explicitly, allowing forwarding cancellation to be acknowledged
	// before the transport goes away. Startup still observes ctx in the readiness loop.
	m.cmd = Command(context.Background(), "ssh", args...)
	m.cmd.Stderr = &m.stderr
	if err = m.cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	go func() { m.err = m.cmd.Wait(); close(m.done) }()
	readyCtx, cancel := context.WithTimeout(ctx, opts.ConnectTimeout+5*time.Second)
	defer cancel()
	for {
		select {
		case <-m.done:
			err = Error(m.err, m.stderr.String())
			if err == nil {
				err = protocol.Fail("CONNECTION_FAILED", 6, "SSH master closed before authentication")
			}
			m.Close()
			return nil, err
		default:
		}
		if _, err = os.Stat(m.Socket); err == nil {
			check := m.Control(readyCtx, "check")
			if check == nil {
				return m, nil
			}
		}
		if err = reconnect.Wait(readyCtx, 20*time.Millisecond); err != nil {
			m.Close()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, Error(readyCtx.Err(), m.stderr.String())
		}
	}
}

// Mux requests disable user config entirely: inherited LocalForward entries must not be replayed.
func (m *Master) Control(ctx context.Context, op string, extra ...string) error {
	args := []string{"-F", "/dev/null", "-S", m.Socket, "-o", "ControlMaster=no", "-O", op}
	args = append(args, extra...)
	args = append(args, "tdev-owned")
	c := Command(ctx, "ssh", args...)
	out, err := c.CombinedOutput()
	return Error(err, string(out))
}
func (m *Master) Args(tty bool, command string) []string {
	args := []string{"-F", "/dev/null", "-S", m.Socket, "-o", "ControlMaster=no"}
	if tty {
		args = append(args, "-tt")
	} else {
		args = append(args, "-T")
	}
	return append(args, "tdev-owned", command)
}
func (m *Master) Closed() <-chan struct{} { return m.done }
func (m *Master) Err() error {
	<-m.done
	err := Error(m.err, m.stderr.String())
	if err == nil {
		return protocol.Fail("CONNECTION_FAILED", 6, "SSH transport closed unexpectedly")
	}
	return protocol.Fail("CONNECTION_FAILED", 6, "Connection closed: %v", err)
}
func (m *Master) Close() {
	m.close.Do(func() {
		if m.cmd != nil && m.cmd.Process != nil {
			select {
			case <-m.done:
			default:
				shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
				_ = m.Control(shutdown, "exit")
				cancel()
			}
			_ = m.cmd.Process.Kill()
			<-m.done
		}
		_ = os.RemoveAll(m.Dir)
	})
}
func Run(ctx context.Context, alias string, opts Options, input io.Reader, command string) ([]byte, error) {
	c := Command(ctx, "ssh", append(opts.Args(true), "-T", alias, command)...)
	c.Stdin = input
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	return out, Error(err, stderr.String())
}
func CheckAlias(s string) error {
	if s == "" || strings.HasPrefix(s, "-") || !config.SafeText(s) || strings.ContainsAny(s, " /\\:") {
		return fmt.Errorf("invalid SSH alias")
	}
	return nil
}
