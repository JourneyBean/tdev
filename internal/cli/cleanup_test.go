package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"tdev/internal/config"
)

func TestRetiredDefaultServerIsDiscarded(t *testing.T) {
	root := t.TempDir()
	// Existing client files remain readable; the retired field affects neither
	// server order nor serialization of the current configuration.
	input := `schema_version: 1
default_server: zulu
servers:
  zulu:
    ssh_alias: zulu
    remote_binary: .local/bin/tdev
  alpha:
    ssh_alias: alpha
    remote_binary: .local/bin/tdev
`
	if err := os.WriteFile(filepath.Join(root, "client.yaml"), []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{store: config.Store{Root: root}}
	prefs, err := a.prefs()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(orderedServers(prefs), ","); got != "alpha,zulu" {
		t.Fatal("old default influenced ordering", got)
	}
	encoded, err := yaml.Marshal(prefs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "default_server") {
		t.Fatal("old field was serialized", string(encoded))
	}
	code, response := run(t, root, "", "create", "demo", "--directory", t.TempDir())
	if code != 0 {
		t.Fatal(response)
	}
	code, response = run(t, root, "", "show", "demo")
	if code != 0 {
		t.Fatal(response)
	}
	data, _ := json.Marshal(response.Data)
	var shown struct {
		Target string `json:"target"`
	}
	if err = json.Unmarshal(data, &shown); err != nil || shown.Target != "local/demo" {
		t.Fatal("old default changed local routing", shown, err)
	}
	for _, bad := range []string{input + "default_server: alpha\n", input + "misspelled_setting: value\n"} {
		if err = os.WriteFile(filepath.Join(root, "client.yaml"), []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = a.prefs(); err == nil {
			t.Fatal("configuration validation was weakened")
		}
	}
}

func TestForwardRetiredFlagsAndNumericProject(t *testing.T) {
	t.Setenv("TMUX", "")
	root := t.TempDir()
	store := config.Store{Root: root}
	if _, err := store.Create(context.Background(), "8000", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "projects", "8000.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--save", "--save-remote"} {
		code, response := run(t, root, "", "forward", "8000", flag)
		if code != 2 || response.Error == nil || response.Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("retired flag accepted: %s %d %+v", flag, code, response)
		}
	}
	code, response := run(t, root, "", "forward", "8000")
	if code != 2 || response.Error == nil || response.Error.Code != "INVALID_CONFIG" || !strings.Contains(response.Error.Message, "project has no saved forwards") {
		t.Fatalf("numeric project was treated as a port: %d %+v", code, response)
	}
	after, err := os.ReadFile(filepath.Join(root, "projects", "8000.yaml"))
	if err != nil || string(before) != string(after) {
		t.Fatal("forward changed a saved definition", err)
	}
}
