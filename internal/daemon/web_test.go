package daemon

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/reconnect"
	"tdev/internal/sshx"
	"tdev/internal/testssh"
)

func TestWebSSHRecoveryAndLifecycle(t *testing.T) {
	ssh := testssh.Start(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error" {
			w.WriteHeader(500)
		}
		fmt.Fprint(w, "web "+r.Host)
	}))
	defer upstream.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	target, _ := strconv.Atoi(port)
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(c.Dir) })
	stop := serveTest(t, c)
	opts := sshx.Defaults()
	opts.ConfigFile = ssh.Config
	opts.ConnectTimeout = time.Second
	opts.ServerAliveInterval = time.Second
	opts.ServerAliveCountMax = 1
	req := Request{Op: "web", Server: "dev", Name: "demo", Alias: ssh.Alias, SSH: opts, WebPort: freePort(t), WebHTTPSPort: freePort(t), Policy: reconnect.Policy{Attempts: -1, Interval: 50 * time.Millisecond}, HTTP: []config.HTTPService{{Name: "web", Hostname: "demo.localhost", TargetHost: "127.0.0.1", TargetPort: target}}}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if _, err = c.Call(ctx, req); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	probe := func(path string) int {
		r, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", req.WebPort, path), nil)
		r.Host = "demo.localhost"
		res, err := client.Do(r)
		if err != nil {
			return 0
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode == 200 && string(body) != "web demo.localhost" {
			t.Errorf("body: %q", body)
		}
		return res.StatusCode
	}
	wait(t, func() bool { return probe("/") == 200 })
	accepted := ssh.Accepted.Load()
	if probe("/error") != 500 {
		t.Fatal("application error changed")
	}
	if _, err = c.Call(ctx, req); err != nil {
		t.Fatal(err)
	}
	if ssh.Accepted.Load() != accepted {
		t.Fatal("application error or repeated open recreated SSH")
	}
	conflict := req
	conflict.Server = "other"
	if _, err = c.Call(ctx, conflict); err == nil {
		t.Fatal("accepted conflicting hostname")
	}
	if probe("/") != 200 {
		t.Fatal("conflict broke existing route")
	}
	// HTTP resources use no shell lease and remain when an unrelated lease ends.
	if _, err = c.Call(ctx, Request{Op: "lease-start", Owner: "shell"}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Call(ctx, Request{Op: "release", Owner: "shell"}); err != nil {
		t.Fatal(err)
	}
	if probe("/") != 200 {
		t.Fatal("shell release stopped web")
	}
	ssh.Offline(true)
	wait(t, func() bool { return probe("/") == 503 })
	ssh.Offline(false)
	wait(t, func() bool { return probe("/") == 200 })
	stop(true)
	stop = serveTest(t, c)
	wait(t, func() bool { return probe("/") == 200 })
	if _, err = c.Call(ctx, Request{Op: "web-stop", Server: "dev", Name: "demo"}); err != nil {
		t.Fatal(err)
	}
	state, err := c.Call(ctx, Request{Op: "status"})
	if err != nil || len(state.Web) != 0 {
		t.Fatalf("stop: %+v %v", state, err)
	}
	stop(true)
	serveTest(t, c)
	state, err = c.Call(ctx, Request{Op: "status"})
	if err != nil || len(state.Web) != 0 {
		t.Fatalf("stopped routes restored: %+v %v", state, err)
	}
}
