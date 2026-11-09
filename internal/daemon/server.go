package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tdev/internal/config"
	"tdev/internal/forward"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
	"tdev/internal/sshx"
)

type connection struct {
	mu              sync.Mutex
	request         Request
	transport       Transport
	err             error
	foregroundUntil time.Time
}

type managedForward struct {
	group    *forward.Group
	state    ForwardState
	ready    chan struct{}
	finished bool
	owners   map[string]bool
	durable  bool
}

type supervisor struct {
	client          Client
	ctx             context.Context
	stop            context.CancelFunc
	mu              sync.Mutex
	connections     map[string]*connection
	forwards        map[string]*managedForward
	wg              sync.WaitGroup
	desired         map[string]Request
	service         func(context.Context, Request) (any, error)
	stopping        bool
	leases          map[string]*lease
	mounts          map[string]*managedMount
	mountMu         sync.Mutex
	forwardMu       sync.Mutex
	warnings        []string
	ephemeralMounts map[string]bool
}

func identity(r Request) string {
	b, _ := json.Marshal([]any{r.Local, r.Alias, r.SSH})
	hash := sha256.Sum256(b)
	return fmt.Sprintf("%x", hash)
}

// Serve owns the lifetime lock, Unix socket and all control/forward transports.
func (c Client) Serve(ctx context.Context, services ...func(context.Context, Request) (any, error)) error {
	lockCtx, cancelLock := context.WithTimeout(ctx, time.Second)
	unlock, err := (config.Store{Root: c.Dir}).Lock(lockCtx, "daemon", "serve")
	cancelLock()
	if err != nil {
		return err
	}
	defer unlock()
	// Recover after abrupt daemon death: these sockets are in our private tree.
	// Remote tmux sessions survive closing their old transports.
	if err = c.closeTransports(); err != nil {
		return err
	}
	// Only the lifetime-lock owner may remove a stale socket.
	if err = os.Remove(c.Socket()); err != nil && !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", c.Socket())
	if err != nil {
		return err
	}
	defer os.Remove(c.Socket())
	if err = os.Chmod(c.Socket(), 0600); err != nil {
		listener.Close()
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &supervisor{client: c, ctx: ctx, stop: cancel, connections: map[string]*connection{}, forwards: map[string]*managedForward{}, desired: map[string]Request{}}
	s.leases = map[string]*lease{}
	s.mounts = map[string]*managedMount{}
	if err = s.loadMountOwnership(); err != nil {
		listener.Close()
		return err
	}
	if len(services) > 0 {
		s.service = services[0]
	}
	if err = s.restore(); err != nil {
		listener.Close()
		return err
	}
	server := &http.Server{Handler: s, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second}
	closed := make(chan struct{})
	go func() { <-ctx.Done(); _ = server.Close(); close(closed) }()
	err = server.Serve(listener)
	cancel()
	<-closed
	// Closing the HTTP server doesn't wait for handlers. The mutex prevents new
	// resource registrations after cancellation; watchers own final cleanup.
	s.mu.Lock()
	s.mu.Unlock()
	s.wg.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *supervisor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req Request
	var state State
	var err error
	if r.Method != "POST" || r.URL.Path != "/control" {
		http.NotFound(w, r)
		return
	}
	if err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err == nil {
		switch req.Op {
		case "status":
			state = s.status()
		case "stop":
			state = s.status()
			s.mu.Lock()
			s.stopping = true
			s.mu.Unlock()
			if !req.Preserve {
				s.mu.Lock()
				s.desired = map[string]Request{}
				err = s.save()
				s.mu.Unlock()
			}
			// Return the acknowledgement before closing client connections.
			defer s.stop()
		case "acquire", "adopt":
			state, err = s.acquire(req)
		case "lookup":
			state, err = s.lookup(req)
		case "forward":
			state, err = s.startForwards(r.Context(), req)
		case "lease-start":
			err = s.beginLease(req.Owner)
			state = State{Protocol: Protocol}
		case "lease":
			err = s.holdLease(r.Context(), req.Owner)
			state = State{Protocol: Protocol}
		case "release":
			s.releaseLease(req.Owner)
			state = State{Protocol: Protocol}
		case "mount":
			if s.service == nil {
				err = config.Invalid("daemon service unavailable")
			} else {
				s.mu.Lock()
				if s.ctx.Err() != nil || s.stopping {
					s.mu.Unlock()
					err = config.Invalid("daemon is stopping")
					break
				}
				s.wg.Add(1)
				s.mu.Unlock()
				defer s.wg.Done()
				state, err = s.startMount(r.Context(), req)
			}
		default:
			err = config.Invalid("unknown daemon operation")
		}
	}
	response := reply{State: state}
	if err != nil {
		response.Error = protocol.Normalize(err)
		response.Exit = response.Error.Exit
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *supervisor) status() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := State{Protocol: Protocol, PID: os.Getpid(), Connections: len(s.connections), Forwards: []ForwardState{}}
	state.Warnings = append([]string(nil), s.warnings...)
	for _, f := range s.forwards {
		state.Forwards = append(state.Forwards, f.state)
	}
	sort.Slice(state.Forwards, func(i, j int) bool { return state.Forwards[i].Spec < state.Forwards[j].Spec })
	return state
}

func (s *supervisor) acquire(req Request) (State, error) {
	if err := sshRequest(req); err != nil {
		return State{}, err
	}
	if req.Op == "adopt" {
		// Interactive authentication happens in the calling terminal. Only sockets
		// created in this daemon's private runtime tree may be handed to it.
		if filepath.Dir(filepath.Dir(req.Socket)) != s.client.Dir || !strings.HasPrefix(filepath.Base(filepath.Dir(req.Socket)), "ssh-") || filepath.Base(req.Socket) != "m" {
			return State{}, config.Invalid("invalid daemon transport socket")
		}
	}
	key := identity(req)
	s.mu.Lock()
	if s.ctx.Err() != nil || s.stopping {
		s.mu.Unlock()
		return State{}, protocol.Fail("DAEMON_UNAVAILABLE", 6, "daemon is stopping")
	}
	e := s.connections[key]
	if e == nil {
		persisted := req
		persisted.Op, persisted.Socket = "acquire", ""
		s.desired[key] = persisted
		if err := s.save(); err != nil {
			s.mu.Unlock()
			return State{}, err
		}
		e = &connection{request: req}
		s.connections[key] = e
		s.wg.Add(1)
		go s.watch(e)
	}
	s.mu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if s.ctx.Err() != nil {
		return State{}, s.ctx.Err()
	}
	if req.Op == "adopt" {
		candidate := Transport{Socket: req.Socket}
		if err := candidate.Control(s.ctx, "check"); err != nil {
			return State{}, err
		}
		if e.transport.Socket != "" {
			e.transport.Close()
		}
		e.transport = candidate
		e.err = nil
		e.foregroundUntil = time.Time{}
	}
	if err := s.ensure(e); err != nil {
		return State{}, err
	}
	return State{Protocol: Protocol, Socket: e.transport.Socket}, nil
}

func (s *supervisor) lookup(req Request) (State, error) {
	if err := sshRequest(req); err != nil {
		return State{}, err
	}
	s.mu.Lock()
	e := s.connections[identity(req)]
	stopping := s.stopping || s.ctx.Err() != nil
	s.mu.Unlock()
	if stopping {
		return State{}, protocol.Fail("DAEMON_UNAVAILABLE", 6, "daemon is stopping")
	}
	state := State{Protocol: Protocol}
	if e == nil {
		return state, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.transport.Socket != "" {
		ctx, cancel := context.WithTimeout(s.ctx, time.Second)
		defer cancel()
		if e.transport.Control(ctx, "check") == nil {
			state.Socket = e.transport.Socket
			return state, nil
		}
	}
	e.foregroundUntil = time.Now().Add(req.SSH.ConnectTimeout + 5*time.Second)
	return state, nil
}

func sshRequest(req Request) error {
	if err := sshx.CheckAlias(req.Alias); err != nil {
		return config.Invalid("invalid SSH alias")
	}
	return req.SSH.Validate()
}

func (s *supervisor) ensure(e *connection) error {
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	ctx, cancel := context.WithTimeout(s.ctx, e.request.SSH.ConnectTimeout+5*time.Second)
	defer cancel()
	if e.transport.Socket != "" {
		if err := e.transport.Control(ctx, "check"); err == nil {
			return nil
		}
		e.transport.Close()
		e.transport = Transport{}
	}
	e.transport, e.err = StartTransport(ctx, s.client.Dir, e.request.Alias, e.request.SSH, true)
	return e.err
}

func (s *supervisor) watch(e *connection) {
	defer s.wg.Done()
	defer func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.transport.Socket != "" {
			e.transport.Close()
		}
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			e.mu.Lock()
			if time.Now().After(e.foregroundUntil) && (e.err == nil || reconnect.Retryable(e.err)) {
				_ = s.ensure(e)
			}
			e.mu.Unlock()
		}
	}
}

func (s *supervisor) startForwards(ctx context.Context, req Request) (State, error) {
	s.forwardMu.Lock()
	defer s.forwardMu.Unlock()
	if !req.Local {
		if err := sshRequest(req); err != nil {
			return State{}, err
		}
	}
	if err := req.Policy.Validate(); err != nil {
		return State{}, err
	}
	// Parse every specification using the product validator before opening listeners.
	for _, f := range req.Forwards {
		if err := f.Validate(); err != nil {
			return State{}, err
		}
	}
	var entries []*managedForward
	s.mu.Lock()
	if s.ctx.Err() != nil || s.stopping {
		s.mu.Unlock()
		return State{}, protocol.Fail("DAEMON_UNAVAILABLE", 6, "daemon is stopping")
	}
	if req.Owner != "" && s.leases[req.Owner] == nil {
		s.mu.Unlock()
		return State{}, config.Invalid("lease expired")
	}
	for _, f := range req.Forwards {
		key := identity(req) + "/" + f.Direction + "/" + f.Spec()
		e := s.forwards[key]
		if e == nil || e.finished {
			owners := map[string]bool{}
			durable := false
			if e != nil {
				owners = e.owners
				durable = e.durable
			}
			var g *forward.Group
			opts := req.SSH
			opts.MasterDirectory = s.client.Dir
			if req.Local {
				g = forward.StartLocal(s.ctx, []config.Forward{f})
			} else {
				g = forward.Start(s.ctx, req.Alias, opts, []config.Forward{f}, req.Policy)
			}
			e = &managedForward{group: g, ready: make(chan struct{}), state: ForwardState{Spec: "-" + f.Direction + " " + f.Spec(), State: "connecting"}, owners: owners, durable: durable}
			s.forwards[key] = e
			s.wg.Add(1)
			go s.forwardEvents(e)
		}
		if req.Owner == "" {
			e.durable = true
			persisted := req
			persisted.Forwards = []config.Forward{f}
			s.desired[key] = persisted
			if err := s.save(); err != nil {
				s.mu.Unlock()
				return State{}, err
			}
		} else {
			e.owners[req.Owner] = true
		}
		entries = append(entries, e)
	}
	s.mu.Unlock()
	for _, e := range entries {
		select {
		case <-ctx.Done():
			return State{}, ctx.Err()
		case <-e.ready:
		}
		s.mu.Lock()
		state := e.state
		s.mu.Unlock()
		if state.State != "ready" {
			return State{}, protocol.Fail("CONNECTION_FAILED", 6, "forward %s: %s %s", state.Spec, state.State, state.Error)
		}
	}
	return s.status(), nil
}

// save is called with the supervisor mutex held. Only connection preferences
// and forwarding definitions are persisted, with the same privacy as client.yaml.
func (s *supervisor) save() error {
	b, err := json.Marshal(s.desired)
	if err != nil {
		return err
	}
	return config.AtomicWrite(filepath.Join(s.client.Dir, "desired.json"), b, 0600)
}

func (s *supervisor) restore() error {
	b, err := os.ReadFile(filepath.Join(s.client.Dir, "desired.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var requests map[string]Request
	if len(b) > 4<<20 {
		return config.Invalid("daemon state exceeds 4 MiB")
	}
	if err = json.Unmarshal(b, &requests); err != nil {
		return config.Invalid("invalid daemon state: %v", err)
	}
	s.desired = make(map[string]Request, len(requests))
	for key, req := range requests {
		s.desired[key] = req
	}
	// Restoration is asynchronous so offline targets cannot delay local readiness.
	for _, req := range requests {
		req := req
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			switch req.Op {
			case "acquire":
				_, _ = s.acquire(req)
			case "forward":
				_, _ = s.startForwards(s.ctx, req)
			}
		}()
	}
	return nil
}

func (s *supervisor) forwardEvents(e *managedForward) {
	defer s.wg.Done()
	defer e.group.Close()
	var ready sync.Once
	for event := range e.group.Events {
		s.mu.Lock()
		e.state.State, e.state.Error = event.State, event.Error
		s.mu.Unlock()
		if event.State != "connecting" {
			ready.Do(func() { close(e.ready) })
		}
	}
	s.mu.Lock()
	e.finished = true
	s.mu.Unlock()
	ready.Do(func() { close(e.ready) })
}
