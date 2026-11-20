package forward

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
	"tdev/internal/sshx"
)

type Event struct {
	ID    string `json:"id"`
	Spec  string `json:"spec"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}
type Group struct {
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	Events  chan Event
	mu      sync.Mutex
	failed  map[string]string
	sockets map[string]string
}

// Acquire returns the shared master socket and a signal that closes when the
// connection changes. With an authentication error the signal waits for login.
type Acquire func(context.Context) (string, <-chan struct{}, error)

type carrier struct {
	master  *sshx.Master
	changed <-chan struct{}
	close   func()
	err     func() error
	shared  bool
}

func Start(ctx context.Context, alias string, opts sshx.Options, defs []config.Forward, policy reconnect.Policy) *Group {
	return start(ctx, opts, defs, policy, func(ctx context.Context) (carrier, error) {
		m, err := sshx.Start(ctx, alias, opts, true)
		if err != nil {
			return carrier{}, err
		}
		return carrier{master: m, changed: m.Closed(), close: m.Close, err: m.Err}, nil
	})
}

func StartShared(ctx context.Context, opts sshx.Options, defs []config.Forward, policy reconnect.Policy, acquire Acquire) *Group {
	return start(ctx, opts, defs, policy, func(ctx context.Context) (carrier, error) {
		socket, changed, err := acquire(ctx)
		return carrier{
			master:  &sshx.Master{Socket: socket},
			changed: changed,
			close:   func() {},
			shared:  true,
			err: func() error {
				return protocol.Fail("CONNECTION_FAILED", 6, "Connection closed: SSH master disconnected")
			},
		}, err
	})
}

func start(ctx context.Context, opts sshx.Options, defs []config.Forward, policy reconnect.Policy, connect func(context.Context) (carrier, error)) *Group {
	ctx, cancel := context.WithCancel(ctx)
	g := &Group{cancel: cancel, Events: make(chan Event, len(defs)*4+1), failed: map[string]string{}, sockets: map[string]string{}}
	for _, f := range defs {
		g.wg.Add(1)
		go func() { defer g.wg.Done(); g.run(ctx, opts, f, policy, connect) }()
	}
	go func() { g.wg.Wait(); close(g.Events) }()
	return g
}
func (g *Group) emit(ctx context.Context, f config.Forward, state string, err error) {
	e := Event{ID: f.ID, Spec: "-" + f.Direction + " " + f.Spec(), State: state}
	if err != nil {
		e.Error = err.Error()
	}
	g.mu.Lock()
	if state == "failed" {
		g.failed[f.ID] = e.Error
	} else if state == "ready" {
		delete(g.failed, f.ID)
	}
	g.mu.Unlock()
	select {
	case g.Events <- e:
	case <-ctx.Done():
	}
}
func (g *Group) run(ctx context.Context, opts sshx.Options, f config.Forward, policy reconnect.Policy, connect func(context.Context) (carrier, error)) {
	budget := reconnect.Budget{Policy: policy}
	established := false
	for {
		if ctx.Err() != nil {
			return
		}
		g.emit(ctx, f, "connecting", nil)
		connection, err := connect(ctx)
		if err != nil && protocol.Normalize(err).Code == sshx.AuthRequired {
			g.emit(ctx, f, "unauthorized", err)
			if connection.changed == nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-connection.changed:
				continue
			}
		}
		m := connection.master
		started := time.Time{}
		retryRemoteBind := false
		if err == nil {
			g.mu.Lock()
			g.sockets[f.ID] = m.Socket
			g.mu.Unlock()
			request, cancel := context.WithTimeout(ctx, opts.ConnectTimeout)
			err = m.Control(request, "forward", "-"+f.Direction, f.Spec())
			cancel()
			// The master can disappear after acquisition and before the listener
			// request. Preserve real bind errors while retrying a lost transport.
			if err != nil && connection.shared && ctx.Err() == nil {
				check, cancel := context.WithTimeout(ctx, time.Second)
				if m.Control(check, "check") != nil && ctx.Err() == nil {
					err = connection.err()
				}
				cancel()
			}
			// After a broken connection the old sshd may still own our remote
			// listener. Retry its bind within the existing budget. Initial port
			// conflicts still fail immediately and never replace another listener.
			retryRemoteBind = established && f.Direction == "R" && err != nil && strings.Contains(err.Error(), "remote port forwarding failed for listen port")
			if err == nil {
				established = true
				started = time.Now()
				g.emit(ctx, f, "ready", nil)
				select {
				case <-ctx.Done():
					cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
					_ = m.Control(cancelCtx, "cancel", "-"+f.Direction, f.Spec())
					cancel()
					connection.close()
					g.mu.Lock()
					delete(g.sockets, f.ID)
					g.mu.Unlock()
					return
				case <-connection.changed:
					err = connection.err()
				}
			}
			connection.close()
			g.mu.Lock()
			delete(g.sockets, f.ID)
			g.mu.Unlock()
		}
		if ctx.Err() != nil {
			return
		}
		if !started.IsZero() {
			budget.Healthy(time.Since(started))
		}
		if !retryRemoteBind && !reconnect.Retryable(err) {
			g.emit(ctx, f, "failed", err)
			return
		}
		g.emit(ctx, f, "waiting", err)
		if retryErr := budget.Next(ctx); retryErr != nil {
			if ctx.Err() == nil {
				g.emit(ctx, f, "failed", fmt.Errorf("%w (last attempt: %v)", retryErr, err))
			}
			return
		}
	}
}

// Sockets returns the currently allocated OpenSSH masters, keyed by mapping ID.
func (g *Group) Sockets() map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	result := make(map[string]string, len(g.sockets))
	for id, socket := range g.sockets {
		result[id] = socket
	}
	return result
}
func (g *Group) Close() { g.cancel(); g.wg.Wait() }
func (g *Group) Failure() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.failed) == 0 {
		return nil
	}
	return fmt.Errorf("forwarding failed: %v", g.failed)
}
