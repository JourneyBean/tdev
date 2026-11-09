package forward

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"tdev/internal/config"
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

func Start(ctx context.Context, alias string, opts sshx.Options, defs []config.Forward, policy reconnect.Policy) *Group {
	ctx, cancel := context.WithCancel(ctx)
	g := &Group{cancel: cancel, Events: make(chan Event, len(defs)*4+1), failed: map[string]string{}, sockets: map[string]string{}}
	for _, f := range defs {
		g.wg.Add(1)
		go func(f config.Forward) { defer g.wg.Done(); g.run(ctx, alias, opts, f, policy) }(f)
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
func (g *Group) run(ctx context.Context, alias string, opts sshx.Options, f config.Forward, policy reconnect.Policy) {
	budget := reconnect.Budget{Policy: policy}
	established := false
	for {
		if ctx.Err() != nil {
			return
		}
		g.emit(ctx, f, "connecting", nil)
		m, err := sshx.Start(ctx, alias, opts, true)
		started := time.Time{}
		retryRemoteBind := false
		if err == nil {
			g.mu.Lock()
			g.sockets[f.ID] = m.Socket
			g.mu.Unlock()
			request, cancel := context.WithTimeout(ctx, opts.ConnectTimeout)
			err = m.Control(request, "forward", "-"+f.Direction, f.Spec())
			cancel()
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
					m.Close()
					g.mu.Lock()
					delete(g.sockets, f.ID)
					g.mu.Unlock()
					return
				case <-m.Closed():
					err = m.Err()
				}
			}
			m.Close()
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
		if err = budget.Next(ctx); err != nil {
			if ctx.Err() == nil {
				g.emit(ctx, f, "failed", err)
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
