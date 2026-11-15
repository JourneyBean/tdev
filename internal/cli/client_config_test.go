package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"tdev/internal/config"
	"tdev/internal/protocol"
)

func clientConfigFile(t *testing.T, root, file, content string) string {
	t.Helper()
	path := filepath.Join(root, file)
	if err := config.AtomicWrite(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestUnifiedClientAndLocalProject(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := clientConfigFile(t, root, "config.yaml", `demo:
  dir: /tmp
  ports:
    - name: web
      local: 3000
      remote: 3000
client:
  servers:
    dev:
      ssh_alias: dev-host
  overrides:
    local/demo:
      ports:
        web:
          local: 18080
      extra_ports:
        - "19090:127.0.0.1:9090"
      mount:
        path: ~/mnt/demo
    dev/demo:
      ports:
        web:
          enabled: false
`)
	a := &app{store: config.Store{Root: root}}
	prefs, err := a.prefs()
	if err != nil {
		t.Fatal(err)
	}
	if prefs.Servers["dev"].RemoteBinary != ".local/bin/tdev" {
		t.Fatal("missing concise server defaults")
	}
	projects, err := a.store.List()
	if err != nil || len(projects) != 1 || projects[0].Name != "demo" {
		t.Fatalf("%+v %v", projects, err)
	}
	target, err := a.serverTarget(prefs, "local", "demo")
	if err != nil {
		t.Fatal(err)
	}
	effective, err := a.effective(target, projects[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(effective.Forwards) != 2 || effective.Forwards[0].ListenPort != 18080 || projects[0].Forwards[0].ListenPort != 3000 {
		t.Fatalf("%+v", effective)
	}
	if target.Server.Projects["demo"].MountPath != filepath.Join(home, "mnt/demo") {
		t.Fatal("mount path did not expand on client")
	}
	bindings, err := a.readBindings()
	if err != nil || bindings["local/demo"] != projects[0].ID || bindings["dev/demo"] != "" {
		t.Fatalf("%v %v", bindings, err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), projects[0].ID) {
		t.Fatal("UUID leaked into user configuration")
	}
	// The target machine's API returns the shared definition, without client patches.
	code, r := run(t, root, "", "api", "project", "get", "demo")
	if code != 0 {
		t.Fatal(r)
	}
	raw, err := a.store.Get("demo")
	if err != nil || raw.Forwards[0].ListenPort != 3000 {
		t.Fatal("local override mutated definition", err)
	}
}
func TestSplitClientWritebackAndProjectCoexistence(t *testing.T) {
	root := t.TempDir()
	a := &app{store: config.Store{Root: root}}
	main := clientConfigFile(t, root, "config.yaml", `# primary
projects:
  include: [./projects/*.yaml]
client:
  include: [./client.d/*.yaml]
`)
	clientConfigFile(t, root, "projects/work.yaml", "demo:\n  dir: /tmp\n  ports:\n    - name: web\n      local: 3000\n      remote: 3000\n")
	fragment := clientConfigFile(t, root, "client.d/work.yaml", `# connection settings
servers:
  dev:
    ssh_alias: dev-host # keep alias comment
overrides:
  local/demo: # local preferences
    ports:
      web:
        local: 18080 # keep port comment
`)
	original, _ := os.ReadFile(main)
	code, r := run(t, root, "", "override", "local/demo", "--forward", "web", "--local-port", "18081")
	if code != 0 {
		t.Fatal(r)
	}
	b, _ := os.ReadFile(fragment)
	for _, want := range []string{"# connection settings", "# keep alias comment", "local/demo: # local preferences", "local: 18081 # keep port comment"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %q:\n%s", want, b)
		}
	}
	unchanged, _ := os.ReadFile(main)
	if string(unchanged) != string(original) {
		t.Fatal("editing fragment changed main config")
	}
	// Concurrent writers use a common lock for a main file containing both roles.
	clientConfigFile(t, root, "config.yaml", "demo:\n  dir: /tmp\n  ports:\n    - name: web\n      local: 3000\n      remote: 3000\nclient:\n  overrides:\n    local/demo:\n      ports:\n        web:\n          local: 18081\n")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := a.store.Update(context.Background(), "demo", 0, func(p *config.Project) error { p.Windows[0].Command = "echo updated"; return nil })
		if err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		code, r := run(t, root, "", "override", "local/demo", "--forward", "web", "--local-port", "18082")
		if code != 0 && r.Error.Code != "CONFIG_CONFLICT" {
			t.Error(r)
		}
	}()
	wg.Wait()
	p, err := a.store.Get("demo")
	if err != nil || p.Windows[0].Command != "echo updated" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := a.prefs(); err != nil {
		t.Fatal(err)
	}
}
func TestClientIdentityBindingAndReset(t *testing.T) {
	root := t.TempDir()
	a := &app{store: config.Store{Root: root}}
	clientConfigFile(t, root, "config.yaml", "client:\n  servers:\n    dev:\n      ssh_alias: dev-host\n  overrides:\n    dev/demo:\n      ports:\n        api:\n          local: 18080\n")
	prefs, err := a.prefs()
	if err != nil {
		t.Fatal(err)
	}
	target, err := a.serverTarget(prefs, "dev", "demo")
	if err != nil {
		t.Fatal(err)
	}
	p := overrideFixture(t)
	if _, err := a.effective(target, p); err != nil {
		t.Fatal(err)
	}
	p.ID = config.NewID()
	if _, err := a.effective(target, p); err == nil {
		t.Fatal("recreated project inherited stale override")
	}
	// Reset removes both the declaration and its internal UID binding.
	if err := a.saveOverride(context.Background(), target, p, func(o *projectOverride) error { *o = projectOverride{}; return nil }); err != nil {
		t.Fatal(err)
	}
	bindings, err := a.readBindings()
	if err != nil || bindings["dev/demo"] != "" {
		t.Fatal(bindings, err)
	}
	overrides, err := a.readOverrides()
	if err != nil || len(overrides.Servers["dev"]) != 0 {
		t.Fatal(overrides, err)
	}
	// Invalid overrides must not establish an identity binding.
	clientConfigFile(t, root, "config.yaml", "client:\n  servers:\n    dev:\n      ssh_alias: dev-host\n  overrides:\n    dev/demo:\n      ports:\n        missing:\n          local: 18080\n")
	prefs, err = a.prefs()
	if err != nil {
		t.Fatal(err)
	}
	target, _ = a.serverTarget(prefs, "dev", "demo")
	if _, err := a.effective(target, p); err == nil {
		t.Fatal("missing port accepted")
	}
	bindings, _ = a.readBindings()
	if len(bindings) != 0 {
		t.Fatal("invalid merge established a binding")
	}
}
func TestLegacyClientMigrationRetainsSettingsAndProjectDiscovery(t *testing.T) {
	root := t.TempDir()
	a := &app{store: config.Store{Root: root}}
	project, err := a.store.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oldClient := "schema_version: 1\nservers:\n  dev:\n    ssh_alias: dev-host\n    remote_binary: .local/bin/tdev\n"
	oldOverrides := fmt.Sprintf("schema_version: 1\nservers:\n  local:\n    demo:\n      project_id: %s\n      mount_path: /tmp/demo-mount\n", project.ID)
	clientConfigFile(t, root, "client.yaml", oldClient)
	clientConfigFile(t, root, "overrides.yaml", oldOverrides)
	if err := a.saveOverride(context.Background(), target{Name: "local", Local: true}, project, func(o *projectOverride) error { o.ProjectID = project.ID; o.MountPath = "/tmp/changed"; return nil }); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{"client.yaml": oldClient, "overrides.yaml": oldOverrides} {
		b, err := os.ReadFile(filepath.Join(root, file+".legacy.bak"))
		if err != nil || string(b) != want {
			t.Fatal("missing legacy backup", file, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), project.ID) || !strings.Contains(string(b), "./projects/*.yaml") {
		t.Fatal("migration leaked UUID or lost project discovery", string(b))
	}
	prefs, err := a.prefs()
	if err != nil || prefs.Servers["dev"].SSHAlias != "dev-host" {
		t.Fatal(prefs, err)
	}
	overrides, err := a.readOverrides()
	if err != nil || overrides.Servers["local"]["demo"].ProjectID != project.ID || overrides.Servers["local"]["demo"].MountPath != "/tmp/changed" {
		t.Fatal(overrides, err)
	}
	again, err := a.store.Get("demo")
	if err != nil || again.ID != project.ID {
		t.Fatalf("%+v %v", again, err)
	}
}
func TestInvalidClientDeclarations(t *testing.T) {
	for name, text := range map[string]string{
		"unknown":        "client:\n  typo: true\n",
		"target":         "client:\n  overrides:\n    demo: {}\n",
		"uuid":           "client:\n  overrides:\n    local/demo:\n      project_id: hidden\n",
		"port":           "client:\n  overrides:\n    local/demo:\n      ports:\n        web:\n          local: 65536\n",
		"relative-mount": "client:\n  overrides:\n    local/demo:\n      mount:\n        path: relative\n",
		"reverse-extra":  "client:\n  overrides:\n    local/demo:\n      extra_ports:\n        - direction: remote\n          local: 3000\n          remote: 3000\n",
		"missing-file":   "client:\n  include: [./missing.yaml]\n",
		"self-include":   "client:\n  include: [./config.yaml]\n",
		"bad-alias":      "client:\n  servers:\n    dev:\n      ssh_alias: '-bad'\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			clientConfigFile(t, root, "config.yaml", text)
			a := &app{store: config.Store{Root: root}}
			_, err := a.prefs()
			if err == nil || protocol.Normalize(err).Code != "INVALID_CONFIG" {
				t.Fatalf("%v", err)
			}
		})
	}
	for _, fragment := range []string{"include: [./another.yaml]\n", "servers:\n  dev:\n    ssh_alias: another\n"} {
		root := t.TempDir()
		clientConfigFile(t, root, "config.yaml", "client:\n  include: [./client.d/*.yaml]\n  servers:\n    dev:\n      ssh_alias: dev-host\n")
		clientConfigFile(t, root, "client.d/work.yaml", fragment)
		if _, err := (&app{store: config.Store{Root: root}}).prefs(); err == nil {
			t.Fatal("duplicate or recursive declaration accepted")
		}
	}
}

func TestEditingUnboundOverrideKeepsOtherPreferences(t *testing.T) {
	root := t.TempDir()
	clientConfigFile(t, root, "config.yaml", `demo:
  dir: /tmp
  ports:
    - name: web
      local: 3000
      remote: 3000
client:
  overrides:
    local/demo:
      ports:
        web:
          local: 18080
      mount:
        path: /tmp/demo-mount
`)
	a := &app{store: config.Store{Root: root}}
	p, err := a.store.Get("demo")
	if err != nil {
		t.Fatal(err)
	}
	p.Forwards = nil
	b, _ := json.Marshal(p)
	code, response := run(t, root, string(b), "edit", "local/demo", "--stdin", "--expected-revision", "1")
	if code != 0 {
		t.Fatal(response)
	}
	overrides, err := a.readOverrides()
	if err != nil {
		t.Fatal(err)
	}
	o := overrides.Servers["local"]["demo"]
	if len(o.Forwards) != 0 || o.MountPath != "/tmp/demo-mount" || o.ProjectID != p.ID {
		t.Fatalf("lost unbound preferences: %+v", o)
	}
}
