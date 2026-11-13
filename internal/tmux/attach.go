package tmux

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/terminal"
)

type Attachment struct {
	SessionID  string `json:"session_id"`
	Generation string `json:"generation"`
	PID        int    `json:"pid"`
	Attached   bool   `json:"attached"`
}

func (m Manager) attachmentPath(token string) (string, error) {
	if !config.ValidName(token) {
		return "", config.Invalid("invalid attachment token")
	}
	return filepath.Join(m.Store.Root, "runtime", "attachments", token+".json"), nil
}
func (m Manager) Attachment(ctx context.Context, p config.Project, token string) (Attachment, error) {
	var a Attachment
	path, err := m.attachmentPath(token)
	if err != nil {
		return a, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	if err = json.Unmarshal(b, &a); err != nil {
		return a, err
	}
	s, err := m.attachmentSession(ctx, p)
	if err != nil {
		return a, err
	}
	if a.SessionID != s.SessionID || a.Generation != s.Generation {
		return a, protocol.Fail("SESSION_NOT_FOUND", 5, "original session ended")
	}
	clients, err := m.run(ctx, "list-clients", "-t", s.SessionID, "-F", "#{client_pid}")
	if err != nil {
		return a, err
	}
	for _, pid := range strings.Split(clients, "\n") {
		if pid == strconv.Itoa(a.PID) {
			a.Attached = true
		}
	}
	return a, nil
}
func (m Manager) Attach(ctx context.Context, p config.Project, session, generation, window, token string, in io.Reader, out, stderr io.Writer) error {
	return m.attach(ctx, p, session, generation, window, token, in, out, stderr, false)
}

// AttachClient relays a local interactive attachment through a private PTY so
// the caller can filter titles. API attachments retain the native TTY path.
func (m Manager) AttachClient(ctx context.Context, p config.Project, session, generation, window, token string, in *os.File, out, stderr io.Writer) error {
	return m.attach(ctx, p, session, generation, window, token, in, out, stderr, true)
}

func (m Manager) attach(ctx context.Context, p config.Project, session, generation, window, token string, in io.Reader, out, stderr io.Writer, relay bool) error {
	s, err := m.attachmentSession(ctx, p)
	if err != nil {
		return err
	}
	if s.SessionID != session || s.Generation != generation {
		return protocol.Fail("SESSION_NOT_FOUND", 5, "original session ended; refusing to attach a replacement")
	}
	if err = m.configureAlerts(ctx, s.SessionID); err != nil {
		return err
	}
	path, err := m.attachmentPath(token)
	if err != nil {
		return err
	}
	args := []string{"attach-session", "-t", s.SessionID}
	if p.ID == "" && os.Getenv("TMUX") != "" {
		_, err = m.run(ctx, "switch-client", "-t", s.SessionID)
		return err
	}
	if window != "" {
		if !strings.HasPrefix(window, "@") {
			return config.Invalid("invalid window ID")
		}
		if _, err = strconv.Atoi(window[1:]); err != nil {
			return config.Invalid("invalid window ID")
		}
		args = append([]string{"select-window", "-t", window, ";"}, args...)
	}
	cmd := exec.CommandContext(ctx, "tmux", m.Args(args...)...)
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = stderr
	wait := cmd.Wait
	if relay {
		wait, err = terminal.StartRelay(cmd, in.(*os.File), out)
	} else {
		err = cmd.Start()
	}
	if err != nil {
		return err
	}
	a := Attachment{SessionID: s.SessionID, Generation: s.Generation, PID: cmd.Process.Pid}
	b, _ := json.Marshal(a)
	if err = config.AtomicWrite(path, b, 0600); err != nil {
		_ = cmd.Process.Kill()
		_ = wait()
		return fmt.Errorf("attach status registration failed: %w", err)
	}
	defer os.Remove(path)
	return wait()
}
