package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tdev/internal/config"
)

func TestLocalProjectForwardAppearsWithoutSession(t *testing.T) {
	t.Setenv("TDEV_TMUX_SOCKET", "tdev-resource-list-"+config.NewID())
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "forwarded") }))
	defer upstream.Close()
	target := strings.TrimPrefix(upstream.URL, "http://127.0.0.1:")
	port := freeLocalPort(t)
	root := t.TempDir()
	text := fmt.Sprintf("demo:\n  dir: /tmp\n  ports:\n    - name: http\n      local: %d\n      remote: %s\n      host: '::ffff:127.0.0.1'\n", port, target)
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	binary := buildCLI(t, "0.8.0-dev")
	t.Cleanup(func() { _ = exec.Command(binary, "--config-dir", root, "daemon", "stop").Run() })
	executeBinary(t, binary, root, "", "", 0, "open", "local/demo", "--only", "forward")
	res, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.Status)
	}
	for _, command := range [][]string{{"ls", "--wide"}, {"forward", "ls", "--wide"}, {"session", "ls"}, {"project", "ls"}} {
		args := append([]string{"--config-dir", root}, command...)
		output, err := exec.Command(binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v %s", command, err, output)
		}
		if command[0] == "session" {
			if strings.Contains(string(output), "demo") {
				t.Fatalf("TCP-only project appeared as a session: %s", output)
			}
		} else if !strings.Contains(string(output), "demo") {
			t.Fatalf("running work item missing: %s", output)
		}
		if command[len(command)-1] == "--wide" && (!strings.Contains(string(output), "daemon-") || !strings.Contains(string(output), "running")) {
			t.Fatalf("missing connection attribution: %s", output)
		}
	}
	response := executeBinary(t, binary, root, "", "", 0, "connection", "ls", "--server", "local")
	raw, _ := json.Marshal(response.Data)
	var listing processListing
	if err := json.Unmarshal(raw, &listing); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range listing.Connections {
		for _, r := range c.Resources {
			if r.Server == "local" && r.Name == "demo" && r.Kind == "forward" && r.Detail == "http" {
				found = true
				if c.PID <= 0 || c.Status != "running" {
					t.Fatalf("invalid carrier process: %+v", c)
				}
			}
		}
	}
	if !found {
		t.Fatalf("resource absent from connection inventory: %s", raw)
	}
}
