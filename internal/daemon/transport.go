package daemon

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"tdev/internal/sshx"
)

// Transport is a detached system OpenSSH master supervised through its private socket.
// Clients close their channels; only the supervisor closes the transport.
type Transport struct{ Socket string }

func (t Transport) Control(ctx context.Context, op string, extra ...string) error {
	return (&sshx.Master{Socket: t.Socket}).Control(ctx, op, extra...)
}

func (t Transport) Args(tty bool, command string) []string {
	args := (&sshx.Master{Socket: t.Socket}).Args(tty, command)
	// A disappeared master must fail rather than silently authenticate a new connection.
	return append([]string{"-o", "ProxyCommand=false"}, args...)
}

func (t Transport) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = t.Control(ctx, "exit")
	_ = os.RemoveAll(filepath.Dir(t.Socket))
}

func (c Client) closeTransports() error {
	for _, pattern := range []string{"ssh-*/m", "tdev-ssh-*/m"} {
		paths, err := filepath.Glob(filepath.Join(c.Dir, pattern))
		if err != nil {
			return err
		}
		for _, path := range paths {
			(Transport{Socket: path}).Close()
		}
	}
	return nil
}

func StartTransport(ctx context.Context, dir, alias string, opts sshx.Options, batch bool) (Transport, error) {
	owned, err := os.MkdirTemp(dir, "ssh-")
	if err != nil {
		return Transport{}, err
	}
	t := Transport{Socket: filepath.Join(owned, "m")}
	args := []string{"-f", "-M", "-S", t.Socket, "-o", "ControlMaster=yes", "-o", "ControlPersist=yes"}
	args = append(args, opts.Args(batch)...)
	args = append(args, "-N", "-T", alias)
	cmd := sshx.Command(ctx, "ssh", args...)
	var stderr sshx.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		t.Close()
		return Transport{}, sshx.Error(err, stderr.String())
	}
	if err = t.Control(ctx, "check"); err != nil {
		t.Close()
		return Transport{}, err
	}
	return t, nil
}
