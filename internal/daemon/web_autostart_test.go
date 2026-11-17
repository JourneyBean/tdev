package daemon

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
)

func TestWebAutostartRetriesSourcesIndependently(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(c.Dir) })
	var online atomic.Bool
	var attempts atomic.Int32
	port := freePort(t)
	policy := reconnect.Policy{Attempts: -1, Interval: 20 * time.Millisecond}
	discovery := func(ctx context.Context, r Request) (any, error) {
		if r.Op == "web-autostart-sources" {
			return []WebSource{{Server: "fast", Policy: policy}, {Server: "offline", Policy: policy}}, nil
		}
		if r.Server == "offline" {
			attempts.Add(1)
			if !online.Load() {
				return nil, protocol.Fail("CONNECTION_FAILED", 6, "Connection refused")
			}
		}
		host := r.Server + ".localhost"
		return []Request{{Op: "web", Server: r.Server, Name: "demo", Local: true, WebPort: port, Policy: policy, HTTP: []config.HTTPService{{Name: "web", Hostname: host, TargetHost: "127.0.0.1", TargetPort: 5173}}}}, nil
	}
	stop := serveTest(t, c, nil, discovery)
	wait(t, func() bool {
		state, err := c.Call(context.Background(), Request{Op: "status"})
		return err == nil && len(state.Web) == 1 && attempts.Load() >= 2 && len(state.Warnings) > 0
	})
	online.Store(true)
	wait(t, func() bool {
		state, err := c.Call(context.Background(), Request{Op: "status"})
		return err == nil && len(state.Web) == 2 && len(state.Warnings) == 0
	})
	data, err := os.ReadFile(c.Dir + "/desired.json")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(data) > 0 && string(data) != "{}" {
		t.Fatalf("automatic routes should come from current project definitions: %s", data)
	}
	stop(true)
	// Automatic routes are rediscovered on a new daemon generation.
	serveTest(t, c, nil, discovery)
	wait(t, func() bool {
		state, err := c.Call(context.Background(), Request{Op: "status"})
		return err == nil && len(state.Web) == 2
	})
}

func TestWebAutostartStopWhileDiscoveryIsPending(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(c.Dir) })
	entered, release := make(chan struct{}), make(chan struct{})
	policy := reconnect.Defaults().HTTP
	port := freePort(t)
	discovery := func(ctx context.Context, r Request) (any, error) {
		if r.Op == "web-autostart-sources" {
			return []WebSource{{Server: "local", Policy: policy}}, nil
		}
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		requests := []Request{}
		for _, name := range []string{"stopped", "sentinel"} {
			requests = append(requests, Request{Op: "web", Server: "local", Name: name, Local: true, WebPort: port, Policy: policy, HTTP: []config.HTTPService{{Name: "web", Hostname: name + ".localhost", TargetHost: "localhost", TargetPort: 5173}}})
		}
		return requests, nil
	}
	serveTest(t, c, nil, discovery)
	<-entered
	if _, err = c.Call(context.Background(), Request{Op: "web-stop", Server: "local", Name: "stopped"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	wait(t, func() bool {
		state, err := c.Call(context.Background(), Request{Op: "status"})
		return err == nil && len(state.Web) == 1 && state.Web[0].Project == "sentinel"
	})
}
