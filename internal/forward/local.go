package forward

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"tdev/internal/config"
)

// Local projects use local TCP listeners and need no SSH connection.
func StartLocal(ctx context.Context, defs []config.Forward) *Group {
	ctx, cancel := context.WithCancel(ctx)
	g := &Group{cancel: cancel, Events: make(chan Event, len(defs)*4+1), failed: map[string]string{}}
	for _, f := range defs {
		g.wg.Add(1)
		go func(f config.Forward) { defer g.wg.Done(); g.runLocal(ctx, f) }(f)
	}
	go func() { g.wg.Wait(); close(g.Events) }()
	return g
}
func (g *Group) runLocal(ctx context.Context, f config.Forward) {
	g.emit(ctx, f, "connecting", nil)
	listener, err := net.Listen("tcp", net.JoinHostPort(f.BindAddress, strconv.Itoa(f.ListenPort)))
	if err != nil {
		g.emit(ctx, f, "failed", err)
		return
	}
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	defer stop()
	g.emit(ctx, f, "ready", nil)
	var connections sync.WaitGroup
	defer connections.Wait()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil {
				g.emit(ctx, f, "failed", err)
			}
			return
		}
		connections.Add(1)
		go func() {
			defer connections.Done()
			defer conn.Close()
			stopConn := context.AfterFunc(ctx, func() { conn.Close() })
			defer stopConn()
			target, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(f.TargetHost, strconv.Itoa(f.TargetPort)))
			if err != nil {
				return
			}
			defer target.Close()
			stopTarget := context.AfterFunc(ctx, func() { target.Close() })
			defer stopTarget()
			done := make(chan struct{})
			go func() {
				io.Copy(target, conn)
				if tcp, ok := target.(*net.TCPConn); ok {
					tcp.CloseWrite()
				}
				close(done)
			}()
			io.Copy(conn, target)
			if tcp, ok := conn.(*net.TCPConn); ok {
				tcp.CloseWrite()
			}
			<-done
		}()
	}
}
