package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.yaml.in/yaml/v3"
	"tdev/internal/protocol"
)

func writeConfig(t *testing.T, root, file, content string) string {
	t.Helper()
	path := filepath.Join(root, file)
	if err := AtomicWrite(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestDeclarationsAndInclude(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConfig(t, root, "config.yaml", "projects:\n  include:\n    - ./projects/*.yaml\n    - ./empty/*.yaml\nscratch:\n  dir: ~/scratch\n")
	writeConfig(t, root, "projects/work.yaml", `demo:
  dir: ~/projects/demo
  windows:
    - name: editor
      command: nvim .
    - name: web
      dir: frontend
      layout:
        row:
          - command: npm run dev
            weight: 2
          - column:
              - dir: backend
                command: uv run server.py
              - command: tail -f app.log
  ports:
    - "3000:127.0.0.1:3000"
    - "3001:other-host:3001"
    - name: callback
      direction: remote
      remote: 9000
      local: 3000
other:
  dir: /tmp/other
`)
	s := Store{Root: root}
	ps, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 || ps[0].Name != "demo" || ps[1].Name != "other" || ps[2].Name != "scratch" {
		t.Fatalf("%+v", ps)
	}
	p := ps[0]
	if p.Directory != filepath.Join(home, "projects/demo") || len(p.Windows) != 2 || p.Windows[1].Layout.Split != "horizontal" || p.Windows[1].Layout.Panes[1].Split != "vertical" {
		t.Fatalf("%+v", p)
	}
	if p.Windows[1].Layout.Panes[0].Directory != "frontend" || p.Windows[1].Layout.Panes[1].Panes[0].Directory != "backend" {
		t.Fatal("pane directory inheritance changed")
	}
	if len(p.Forwards) != 3 || p.Forwards[0].Spec() != "127.0.0.1:3000:127.0.0.1:3000" || p.Forwards[2].Direction != "R" || p.Forwards[2].ListenPort != 9000 || p.Forwards[2].TargetPort != 3000 || p.Forwards[2].ID != "callback" {
		t.Fatalf("%+v", p.Forwards)
	}
	if ps[2].Windows[0].Name != "main" {
		t.Fatal("missing default shell")
	}
	again, err := s.Get("demo")
	if err != nil || again.ID != p.ID || again.Revision != 1 {
		t.Fatalf("%+v %v", again, err)
	}
}
func TestInvalidConfigurationFiles(t *testing.T) {
	cases := []struct{ name, main, fragment string }{
		{"unknown", "", "demo:\n  dir: /tmp\n  typo: true\n"},
		{"uuid", "", "demo:\n  dir: /tmp\n  id: x\n"},
		{"duplicate-key", "", "demo:\n  dir: /tmp\n  dir: /other\n"},
		{"duplicate-project", "demo:\n  dir: /tmp\nprojects:\n  include: [./projects/*.yaml]\n", "demo:\n  dir: /other\n"},
		{"missing-include", "projects:\n  include: [./missing.yaml]\n", ""},
		{"self-include", "projects:\n  include: [./config.yaml]\n", ""},
		{"duplicate-include", "projects:\n  include: [./projects/*.yaml, ./projects/work.yaml]\n", "demo:\n  dir: /tmp\n"},
		{"nested-include", "", "projects:\n  include: [./more.yaml]\n"},
		{"wrong-direction", "", "demo:\n  dir: /tmp\n  ports:\n    - direction: sideways\n      local: 80\n      remote: 80\n"},
		{"invalid-port", "", "demo:\n  dir: /tmp\n  ports: [65536]\n"},
		{"port-typo", "", "demo:\n  dir: /tmp\n  ports:\n    - local: 80\n      remote: 80\n      typo: true\n"},
		{"duplicate-window", "", "demo:\n  dir: /tmp\n  windows: [{name: web}, {name: web}]\n"},
		{"split-leaf", "", "demo:\n  dir: /tmp\n  windows:\n    - name: web\n      layout:\n        row: [{}, {}]\n        command: ignored\n"},
		{"multi-document", "", "demo:\n  dir: /tmp\n---\nother:\n  dir: /tmp\n"},
		{"alias", "", "demo: &demo\n  dir: /tmp\nother: *demo\n"},
		{"relative-root", "", "demo:\n  dir: relative\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.main != "" {
				writeConfig(t, root, "config.yaml", tc.main)
			}
			if tc.fragment != "" {
				writeConfig(t, root, "projects/work.yaml", tc.fragment)
			}
			if _, err := (Store{Root: root}).List(); err == nil || protocol.Normalize(err).Code != "INVALID_CONFIG" {
				t.Fatalf("want configuration error, got %v", err)
			}
		})
	}
}
func TestSharedFileEditPreservesCommentsAndOtherProjects(t *testing.T) {
	root := t.TempDir()
	s := Store{Root: root}
	original := `# development projects
demo:
  dir: /tmp # root directory
  # windows stay ordered
  windows:
    - name: editor
      command: nvim . # editor command
  ports:
    - "3000:127.0.0.1:3000" # keep shorthand
other: # unrelated project
  dir: /tmp/other
`
	path := writeConfig(t, root, "config.yaml", original)
	p, err := s.Get("demo")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Update(context.Background(), "demo", p.Revision, func(p *Project) error { p.Windows[0].Command = "vim ."; return nil })
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	text := string(b)
	for _, want := range []string{"# development projects", "dir: /tmp # root directory", "# windows stay ordered", "command: vim . # editor command", `"3000:127.0.0.1:3000" # keep shorthand`, "other: # unrelated project\n  dir: /tmp/other"} {
		if !strings.Contains(text, want) {
			t.Fatalf("lost %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"schema_version:", "revision:", "id:"} {
		if strings.Contains(text, forbidden) {
			t.Fatal("internal field leaked", text)
		}
	}
	backup, _ := os.ReadFile(path + ".demo.rev-1.bak")
	if string(backup) != original {
		t.Fatal("backup did not preserve source")
	}
	other, err := s.Get("other")
	if err != nil || other.Revision != 1 {
		t.Fatalf("%+v %v", other, err)
	}
}
func TestManualEditRevisionMoveAndConflict(t *testing.T) {
	root := t.TempDir()
	s := Store{Root: root}
	path := writeConfig(t, root, "projects/a.yaml", "demo:\n  dir: /tmp\n")
	first, err := s.Get("demo")
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, root, "projects/a.yaml", "# comment\ndemo:\n  dir: /tmp\n")
	p, err := s.Get("demo")
	if err != nil || p.Revision != 1 || p.ID != first.ID {
		t.Fatalf("comment changed identity/revision: %+v %v", p, err)
	}
	if err := os.Rename(path, filepath.Join(root, "projects/b.yaml")); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, root, "projects/b.yaml", "demo:\n  dir: /tmp/changed\n")
	p, err = s.Get("demo")
	if err != nil || p.Revision != 2 || p.ID != first.ID {
		t.Fatalf("%+v %v", p, err)
	}
	_, err = s.Update(context.Background(), "demo", first.Revision, func(*Project) error { return nil })
	if protocol.Normalize(err).Code != "CONFIG_CONFLICT" {
		t.Fatal(err)
	}
	_, err = s.Update(context.Background(), "demo", p.Revision, func(*Project) error {
		writeConfig(t, root, "projects/b.yaml", "demo:\n  dir: /tmp/external\n")
		return nil
	})
	if err == nil || protocol.Normalize(err).Code != "CONFIG_CONFLICT" {
		t.Fatal("external edit overwritten", err)
	}
	latest, err := s.Get("demo")
	if err != nil || latest.Directory != "/tmp/external" {
		t.Fatalf("%+v %v", latest, err)
	}
}
func TestConcurrentUpdatesToDifferentProjects(t *testing.T) {
	root := t.TempDir()
	s := Store{Root: root}
	writeConfig(t, root, "config.yaml", "a:\n  dir: /tmp\nb:\n  dir: /tmp\n")
	var wg sync.WaitGroup
	for _, name := range []string{"a", "b"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, err := s.Update(context.Background(), name, 1, func(p *Project) error { p.Windows[0].Command = "echo " + name; return nil })
			if err != nil {
				t.Error(err)
			}
		}(name)
	}
	wg.Wait()
	for _, name := range []string{"a", "b"} {
		p, err := s.Get(name)
		if err != nil || p.Windows[0].Command != "echo "+name || p.Revision != 2 {
			t.Fatalf("%+v %v", p, err)
		}
	}
}
func TestLegacyMigrationPreservesIdentity(t *testing.T) {
	root := t.TempDir()
	s := Store{Root: root}
	p := Project{SchemaVersion: 1, Revision: 7, ID: NewID(), Name: "demo", Directory: "/tmp", Windows: []Window{{ID: "editor", Name: "editor", Directory: "."}}, Forwards: []Forward{}}
	b, _ := yaml.Marshal(p)
	path := writeConfig(t, root, "projects/old.yaml", string(b))
	got, err := s.Get("demo")
	if err != nil || got.ID != p.ID || got.Revision != 7 {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = s.Update(context.Background(), "demo", 7, func(p *Project) error { p.Windows[0].Command = "echo ready"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "schema_version:") || !strings.HasPrefix(string(b), "demo:") {
		t.Fatal(string(b))
	}
	again, err := s.Get("demo")
	if err != nil || again.ID != p.ID || again.Revision != 8 || again.Windows[0].ID != "editor" {
		t.Fatalf("%+v %v", again, err)
	}
}
func TestCreateInMainAndFragment(t *testing.T) {
	for _, main := range []bool{false, true} {
		t.Run(fmt.Sprint(main), func(t *testing.T) {
			root := t.TempDir()
			s := Store{Root: root}
			if main {
				writeConfig(t, root, "config.yaml", "existing:\n  dir: /tmp\n")
			}
			p, err := s.Create(context.Background(), "demo", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			again, err := s.Get("demo")
			if err != nil || again.ID != p.ID {
				t.Fatalf("%+v %v", again, err)
			}
			if main {
				if _, err := s.Get("existing"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Create(context.Background(), "projects", t.TempDir()); err == nil {
				t.Fatal("reserved name accepted")
			}
		})
	}
}

func TestDeclaredPortDefaultsAndExplicitHosts(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, "projects/ports.yaml", `demo:
  dir: /tmp
  ports:
    - "3000"
    - "3001:8000"
    - "3002:localhost:8000"
    - "[::1]:3003:[::1]:8000"
`)
	p, err := (Store{Root: root}).Get("demo")
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"127.0.0.1", "127.0.0.1", "localhost", "::1"} {
		if p.Forwards[i].TargetHost != want {
			t.Fatalf("port %d host %q, want %q", i, p.Forwards[i].TargetHost, want)
		}
	}
}
func TestGeneratedIdentitiesStayInternal(t *testing.T) {
	root := t.TempDir()
	s := Store{Root: root}
	p, err := s.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	generated := "p" + strings.ReplaceAll(NewID(), "-", "")
	f, _ := ParseForward("L", "8080:localhost:8000")
	p, err = s.Update(context.Background(), "demo", p.Revision, func(p *Project) error {
		p.Windows[0].Layout = &Pane{Split: "horizontal", Panes: []Pane{{ID: generated, Directory: "."}, {ID: "shell", Directory: "."}}}
		p.Forwards = []Forward{f}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "projects/demo.yaml")
	b, _ := os.ReadFile(path)
	for _, internal := range []string{p.ID, generated, f.ID} {
		if strings.Contains(string(b), internal) {
			t.Fatalf("internal identity leaked into declaration")
		}
	}
	// Editing only a comment must retain generated pane and forward IDs.
	b = append([]byte("# still the same project\n"), b...)
	if err := AtomicWrite(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	again, err := s.Get("demo")
	if err != nil || again.Revision != p.Revision || again.Windows[0].Layout.Panes[0].ID != generated || again.Forwards[0].ID != f.ID {
		t.Fatalf("%+v %v", again, err)
	}
}
func TestIncludeSymlinkWritesAndDiagnostics(t *testing.T) {
	root := t.TempDir()
	s := Store{Root: root}
	writeConfig(t, root, "config.yaml", "projects:\n  include: [./projects/work.yaml]\n")
	actual := writeConfig(t, root, "actual.yaml", "demo:\n  dir: /tmp\n")
	if err := os.MkdirAll(filepath.Join(root, "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "projects/work.yaml")
	if err := os.Symlink(actual, link); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(context.Background(), "demo", 1, func(p *Project) error { p.Windows[0].Command = "echo ready"; return nil }); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("include symlink replaced", err)
	}
	b, _ := os.ReadFile(actual)
	if !strings.Contains(string(b), "echo ready") {
		t.Fatal(string(b))
	}
	writeConfig(t, root, "config.yaml", "projects:\n  include: [./projects/work.yaml]\ndemo:\n  dir: /tmp\n")
	_, err := s.List()
	if err == nil || !strings.Contains(err.Error(), "config.yaml:") || !strings.Contains(err.Error(), "actual.yaml:") {
		t.Fatal("missing duplicate definition locations", err)
	}
}
