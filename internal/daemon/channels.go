package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"tdev/internal/config"
	"tdev/internal/protocol"
)

// Allocation describes a tdev operation, never its command line or environment.
// Its identity is local bookkeeping, independent of OpenSSH wire channel IDs.
type Allocation struct {
	ID        string    `json:"id"`
	Socket    string    `json:"socket"`
	Kind      string    `json:"kind"`
	Project   string    `json:"project,omitempty"`
	ClientPID int       `json:"client_pid"`
	Since     time.Time `json:"since"`
}

func (s *supervisor) holdChannel(w http.ResponseWriter, r *http.Request, a *Allocation) {
	var err error
	if a == nil || !config.ValidName(a.ID) || (a.Project != "" && !config.ValidName(a.Project)) || a.ClientPID <= 0 {
		err = config.Invalid("invalid channel allocation")
	} else if a.Kind != "api" && a.Kind != "terminal" && a.Kind != "bootstrap" {
		err = config.Invalid("invalid channel purpose")
	} else if filepath.Dir(filepath.Dir(a.Socket)) != s.client.Dir || !strings.HasPrefix(filepath.Base(filepath.Dir(a.Socket)), "ssh-") || filepath.Base(a.Socket) != "m" {
		err = config.Invalid("invalid channel transport")
	}
	s.mu.Lock()
	if err == nil && (s.stopping || s.ctx.Err() != nil || s.channels[a.ID].ID != "") {
		err = config.Invalid("channel unavailable")
	}
	if err == nil {
		record := *a
		record.Since = time.Now().UTC()
		s.channels[a.ID] = record
		s.wg.Add(1)
	}
	s.mu.Unlock()
	response := reply{State: State{Protocol: Protocol}}
	if err != nil {
		response.Error = protocol.Normalize(err)
		response.Exit = response.Error.Exit
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if err != nil {
		return
	}
	defer s.wg.Done()
	defer func() { s.mu.Lock(); delete(s.channels, a.ID); s.mu.Unlock() }()
	select {
	case <-r.Context().Done():
	case <-s.ctx.Done():
	}
}

// TrackChannel returns after the daemon acknowledges registration. The held
// socket removes the allocation even if the calling CLI is killed abruptly.
func (c Client) TrackChannel(ctx context.Context, allocation Allocation) (func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", c.Socket())
	}, DisableKeepAlives: true}
	cleanup := func() { cancel(); transport.CloseIdleConnections() }
	b, err := json.Marshal(Request{Op: "channel", Allocation: &allocation})
	if err != nil {
		cleanup()
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://daemon/control", bytes.NewReader(b))
	if err != nil {
		cleanup()
		return nil, err
	}
	res, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		cleanup()
		return nil, protocol.Fail("CONNECTION_FAILED", 6, "Connection closed while registering SSH channel: %v", err)
	}
	var response reply
	if err = json.NewDecoder(res.Body).Decode(&response); err != nil {
		res.Body.Close()
		cleanup()
		return nil, protocol.Fail("CONNECTION_FAILED", 6, "Connection closed while registering SSH channel: %v", err)
	}
	if response.Error != nil {
		res.Body.Close()
		cleanup()
		response.Error.Exit = response.Exit
		return nil, response.Error
	}
	return func() { cancel(); _ = res.Body.Close(); transport.CloseIdleConnections() }, nil
}
