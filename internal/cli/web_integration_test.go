package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"tdev/internal/config"
	"tdev/internal/daemon"
	"tdev/internal/testssh"
	"testing"
	"time"
)

func TestWebServiceOnlyCLI(t *testing.T) {
	t.Setenv("TDEV_TMUX_SOCKET", "tdev-web-list-"+config.NewID())
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.Host+r.URL.Path) }))
	defer upstream.Close()
	_, targetText, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	target, _ := strconv.Atoi(targetText)
	root := t.TempDir()
	port := freeLocalPort(t)
	text := fmt.Sprintf("demo:\n  dir: /tmp\n  http:\n    - name: web\n      hostname: demo.localhost\n      target_host: 127.0.0.1\n      target_port: %d\nclient:\n  http:\n    port: %d\n", target, port)
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	binary := buildCLI(t, "0.8.0-dev")
	executeBinary(t, binary, root, "", "", 0, "open", "local/demo", "--only", "web")
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/check", port), nil)
	req.Host = "demo.localhost"
	res, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(body) != "demo.localhost/check" {
		t.Fatalf("%d %q", res.StatusCode, body)
	}
	state := executeBinary(t, binary, root, "", "", 0, "web", "status")
	if !strings.Contains(fmt.Sprint(state.Data), "demo.localhost") {
		t.Fatal(state)
	}
	for _, args := range [][]string{{"ls"}, {"session", "ls"}, {"web", "ls"}} {
		cmdArgs := append([]string{"--config-dir", root}, args...)
		output, err := exec.Command(binary, cmdArgs...).CombinedOutput()
		if args[0] == "session" {
			if err != nil || !strings.Contains(string(output), "CLIENTS") || strings.Contains(string(output), "demo.localhost") {
				t.Fatalf("session-only listing: %v %s", err, output)
			}
			continue
		}
		mapping := fmt.Sprintf("demo.localhost:%d -> 127.0.0.1:%d", port, target)
		if err != nil || !strings.Contains(string(output), mapping) || strings.Contains(string(output), "DIR") {
			t.Fatalf("%v listing: %v %s", args, err, output)
		}
		if args[0] == "web" && !strings.Contains(string(output), "running") {
			t.Fatalf("web ls omitted state: %s", output)
		}
	}
	// Service-only startup leaves tmux untouched.
	state = executeBinary(t, binary, root, "", "", 0, "api", "session", "status", "demo")
	if !strings.Contains(fmt.Sprint(state.Data), "missing") {
		t.Fatal(state)
	}
	executeBinary(t, binary, root, "", "", 0, "web", "stop", "local/demo")
	output, err := exec.Command(binary, "--config-dir", root, "web", "ls", "--include-inactive").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "stopped") {
		t.Fatalf("stopped web ls: %v %s", err, output)
	}
	state = executeBinary(t, binary, root, "", "", 0, "web", "status")
	if strings.Contains(fmt.Sprint(state.Data), "demo.localhost") {
		t.Fatal(state)
	}
}

func TestWebAutostartWithoutOpeningProjects(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.Host) }))
	defer upstream.Close()
	_, targetText, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	target, _ := strconv.Atoi(targetText)
	root := t.TempDir()
	port := freeLocalPort(t)
	configText := func(automatic bool) string {
		return fmt.Sprintf(`demo:
  dir: /tmp
  http:
    - name: web
      hostname: demo.localhost
      target_host: 127.0.0.1
      target_port: %d
      autostart: %v
    - name: manual
      hostname: manual.demo.localhost
      target_host: 127.0.0.1
      target_port: %d
      autostart: false
other:
  dir: /tmp
  http:
    - name: web
      hostname: other.localhost
      target_host: 127.0.0.1
      target_port: %d
client:
  http:
    port: %d
`, target, automatic, target, target, port)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(configText(true)), 0600); err != nil {
		t.Fatal(err)
	}
	binary := buildCLI(t, "0.8.0-dev")
	executeBinary(t, binary, root, "", "", 0, "daemon", "start")
	client, _ := daemon.New(root)
	waitRoutes := func(want int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			state, err := client.Call(context.Background(), daemon.Request{Op: "status"})
			if err == nil && len(state.Web) == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("routes: %+v %v", state, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitRoutes(2)
	// Both projects start without open; the disabled service remains absent.
	for _, host := range []string{"demo.localhost", "other.localhost"} {
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
		req.Host = host
		res, err := (&http.Client{Timeout: time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal(res.StatusCode)
		}
	}
	for _, project := range []string{"demo", "other"} {
		state := executeBinary(t, binary, root, "", "", 0, "api", "session", "status", project)
		if !strings.Contains(fmt.Sprint(state.Data), "missing") {
			t.Fatal(state)
		}
	}
	executeBinary(t, binary, root, "", "", 0, "web", "stop", "local/demo")
	waitRoutes(1)
	executeBinary(t, binary, root, "", "", 0, "daemon", "restart")
	waitRoutes(2)
	// A manual open adds disabled services to the automatically started subset.
	executeBinary(t, binary, root, "", "", 0, "open", "local/demo", "--only", "web")
	waitRoutes(3)
	executeBinary(t, binary, root, "", "", 0, "daemon", "restart")
	waitRoutes(2)
	executeBinary(t, binary, root, "", "", 0, "web", "stop", "local/demo")
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(configText(false)), 0600); err != nil {
		t.Fatal(err)
	}
	executeBinary(t, binary, root, "", "", 0, "daemon", "restart")
	waitRoutes(1)
	executeBinary(t, binary, root, "", "", 0, "daemon", "stop")
	// Explicit stop stays stopped until start; the next start uses current declarations.
	executeBinary(t, binary, root, "", "", 0, "daemon", "start")
	waitRoutes(1)
}

func TestWebAutostartRemoteDiscoveryAfterOfflineStartup(t *testing.T) {
	ssh := testssh.Start(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, r.Host) }))
	defer upstream.Close()
	_, targetText, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	target, _ := strconv.Atoi(targetText)
	root, remote := t.TempDir(), t.TempDir()
	port := freeLocalPort(t)
	binary := buildCLI(t, "0.8.0-dev")
	remoteText := fmt.Sprintf(`demo:
  dir: /tmp
  http:
    - name: web
      hostname: remote.localhost
      target_host: localhost
      target_port: %d
    - name: manual
      hostname: manual.remote.localhost
      target_host: localhost
      target_port: %d
      autostart: false
`, target, target)
	clientConfigFile(t, remote, "config.yaml", remoteText)
	clientConfigFile(t, root, "config.yaml", fmt.Sprintf(`localdemo:
  dir: /tmp
  http:
    - name: web
      hostname: local.localhost
      target_host: localhost
      target_port: %d
client:
  http:
    port: %d
  ssh:
    connect_timeout: 1s
    server_alive_interval: 1s
    server_alive_count_max: 1
  reconnect:
    http: {attempts: always, interval: 50ms}
  servers:
    fixture:
      ssh_alias: %q
      ssh_config: %q
      remote_binary: %q
      remote_config_dir: %q
`, target, port, ssh.Alias, ssh.Config, binary, remote))
	ssh.Offline(true)
	executeBinary(t, binary, root, ssh.Config, "", 0, "daemon", "start")
	client, _ := daemon.New(root)
	waitFor := func(fn func(daemon.State) bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			state, err := client.Call(context.Background(), daemon.Request{Op: "status"})
			if err == nil && fn(state) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("autostart state: %+v %v", state, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitFor(func(state daemon.State) bool {
		return len(state.Web) == 1 && state.Web[0].Hostname == "local.localhost" && len(state.Warnings) > 0
	})
	ssh.Offline(false)
	waitFor(func(state daemon.State) bool {
		if len(state.Web) != 2 || len(state.Warnings) != 0 {
			return false
		}
		for _, r := range state.Web {
			if r.State != "ready" {
				return false
			}
		}
		return true
	})
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
	req.Host = "remote.localhost"
	res, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(body) != "remote.localhost" {
		t.Fatalf("%d %q", res.StatusCode, body)
	}
	// Inspect the fixture's remote configuration and private tmux socket directly.
	t.Setenv("TDEV_TMUX_SOCKET", ssh.Socket)
	state := executeBinary(t, binary, remote, ssh.Config, "", 0, "api", "session", "status", "demo")
	if !strings.Contains(fmt.Sprint(state.Data), "missing") {
		t.Fatal(state)
	}
	// A manual open expands the automatic subset, and repeated opens reuse it.
	executeBinary(t, binary, root, ssh.Config, "", 0, "open", "fixture/demo", "--only", "web")
	executeBinary(t, binary, root, ssh.Config, "", 0, "open", "fixture/demo", "--only", "web")
	waitFor(func(state daemon.State) bool { return len(state.Web) == 3 })
}
