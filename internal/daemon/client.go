// Package daemon manages local OpenSSH transports over a private Unix socket.
package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
	"tdev/internal/sshx"
)

const Protocol = 1

type Request struct {
	Op       string
	Alias    string
	SSH      sshx.Options
	Socket   string
	Forwards []config.Forward
	Local    bool
	Policy   reconnect.Policy
	Preserve bool
	Payload  json.RawMessage
	Owner    string
}

type State struct {
	Protocol    int             `json:"protocol"`
	PID         int             `json:"pid"`
	Connections int             `json:"connections"`
	Forwards    []ForwardState  `json:"forwards"`
	Socket      string          `json:"socket,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Warnings    []string        `json:"warnings,omitempty"`
}

type ForwardState struct {
	Spec  string `json:"spec"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

type reply struct {
	State State
	Error *protocol.Error
	Exit  int
}

type Client struct{ Dir string }

// ConnectionKey isolates SSH aliases and profiles within one daemon.
func ConnectionKey(alias string, opts sshx.Options) string {
	return identity(Request{Alias: alias, SSH: opts})
}

func New(root string) (Client, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Client{}, err
	}
	hash := sha256.Sum256([]byte(abs))
	dir := filepath.Join("/tmp", fmt.Sprintf("tdev-daemon-%d-%x", os.Getuid(), hash[:10]))
	if err = os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return Client{}, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return Client{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
		return Client{}, config.Invalid("daemon runtime directory must be private and owned by this user")
	}
	return Client{Dir: dir}, nil
}

func (c Client) Socket() string { return filepath.Join(c.Dir, "daemon.sock") }

func (c Client) Call(ctx context.Context, request Request) (State, error) {
	b, err := json.Marshal(request)
	if err != nil {
		return State{}, err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", c.Socket())
	}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "POST", "http://daemon/control", bytes.NewReader(b))
	if err != nil {
		return State{}, err
	}
	res, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return State{}, protocol.Fail("DAEMON_UNAVAILABLE", 6, "local daemon unavailable: %v", err)
	}
	defer res.Body.Close()
	var response reply
	if err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&response); err != nil {
		return State{}, err
	}
	if response.Error != nil {
		response.Error.Exit = response.Exit
		return State{}, response.Error
	}
	if response.State.Protocol != Protocol {
		return State{}, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "restart the local daemon with this version of tdev")
	}
	return response.State, nil
}
