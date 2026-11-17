package webproxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/reconnect"
	"tdev/internal/sshx"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
func startLocal(t *testing.T, handler http.Handler) (*Manager, string) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	port, _ := strconv.Atoi(p)
	m := New()
	t.Cleanup(func() { m.Stop("", "") })
	proxyPort := freePort(t)
	if err := m.Start(context.Background(), "local", "demo", "local", proxyPort, []config.HTTPService{{Name: "web", Hostname: "demo.localhost", TargetHost: "127.0.0.1", TargetPort: port}}, true, "", sshx.Options{}, reconnect.Defaults().HTTP); err != nil {
		t.Fatal(err)
	}
	return m, fmt.Sprintf("http://127.0.0.1:%d", proxyPort)
}
func fetch(t *testing.T, address, host, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", address+path, nil)
	req.Host = host
	client := &http.Client{Timeout: time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(body)
}
func TestRoutingAndConflicts(t *testing.T) {
	m, address := startLocal(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error" {
			w.WriteHeader(502)
		}
		fmt.Fprintf(w, "%s %s %s %s", r.Host, r.URL.RequestURI(), r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-For"))
	}))
	if code, body := fetch(t, address, "demo.localhost:17890", "/path?q=1"); code != 200 || !strings.Contains(body, "demo.localhost:17890 /path?q=1 demo.localhost:17890 127.0.0.1") {
		t.Fatalf("%d %s", code, body)
	}
	for _, tc := range []struct {
		host string
		code int
	}{{"unknown.localhost", 404}, {"evil.example.invalid", 400}, {"demo.localhost:bad:port", 400}, {"demo.localhost:abc", 400}, {"demo.localhost:0", 400}, {"[demo.localhost]:17890", 400}} {
		if code, _ := fetch(t, address, tc.host, "/"); code != tc.code {
			t.Fatalf("%s: %d", tc.host, code)
		}
	}
	if code, _ := fetch(t, address, "demo.localhost", "/error"); code != 502 {
		t.Fatalf("application 502 changed: %d", code)
	}
	original := m.States()[0]
	originalService := m.routes["demo.localhost"].service
	added := originalService
	added.Name, added.Hostname = "api", "api.demo.localhost"
	if err := m.Start(context.Background(), "local", "demo", "local", m.port, []config.HTTPService{originalService, added}, true, "", sshx.Options{}, reconnect.Defaults().HTTP); err != nil {
		t.Fatal(err)
	}
	if len(m.States()) != 2 {
		t.Fatal("manual open did not add its extra route")
	}

	h := config.HTTPService{Name: "web", Hostname: "demo.localhost", TargetHost: "127.0.0.1", TargetPort: 1}
	if err := m.Start(context.Background(), "other", "demo", "other", m.port, []config.HTTPService{h}, true, "", sshx.Options{}, reconnect.Defaults().HTTP); err == nil {
		t.Fatal("overwrote conflicting hostname")
	}
	if m.States()[1] != original {
		t.Fatal("conflict changed existing route")
	}
	if code, _ := fetch(t, address, "demo.localhost", "/"); code != 200 {
		t.Fatalf("conflict disrupted route: %d", code)
	}
	// An unavailable application returns 503 without changing SSH retry state.
	m.mu.RLock()
	r := m.routes["demo.localhost"]
	m.mu.RUnlock()
	r.mu.Lock()
	r.state = "waiting"
	r.mu.Unlock()
	if code, _ := fetch(t, address, "demo.localhost", "/"); code != 503 {
		t.Fatal(code)
	}
}

func TestStreamingAndCancellation(t *testing.T) {
	cancelled := make(chan struct{})
	_, address := startLocal(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", address, nil)
	req.Host = "demo.localhost"
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	line, err := bufio.NewReader(res.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("%q %v", line, err)
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream cancellation not delivered")
	}
}

func TestWebSocketUpgradeAndStopClosesConnection(t *testing.T) {
	m, address := startLocal(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "upgrade required", 400)
			return
		}
		c, b, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		fmt.Fprint(b, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n")
		_ = b.Flush()
		// Receive a masked text frame and respond with an unmasked text frame.
		frame := make([]byte, 8)
		if _, err = io.ReadFull(b, frame); err != nil {
			return
		}
		_, _ = c.Write([]byte{0x81, 2, frame[6] ^ frame[2], frame[7] ^ frame[3]})
		_, _ = io.Copy(io.Discard, c)
	}))
	c, err := net.DialTimeout("tcp", strings.TrimPrefix(address, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprint(c, "GET / HTTP/1.1\r\nHost: demo.localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	b := bufio.NewReader(c)
	res, err := http.ReadResponse(b, nil)
	if err != nil || res.StatusCode != 101 {
		t.Fatalf("upgrade: %v %v", res, err)
	}
	_, _ = c.Write([]byte{0x81, 0x82, 1, 2, 3, 4, 'h' ^ 1, 'i' ^ 2})
	frame := make([]byte, 4)
	if _, err = io.ReadFull(b, frame); err != nil || string(frame[2:]) != "hi" {
		t.Fatalf("frame %v %v", frame, err)
	}
	m.Stop("local", "demo")
	if _, err = b.ReadByte(); err == nil {
		t.Fatal("route stop retained upgraded connection")
	}
	if len(m.States()) != 0 {
		t.Fatal("stop retained route")
	}
	// The last route releases the fixed listening port.
	l, err := net.Listen("tcp4", strings.TrimPrefix(address, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
}
