package tmux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/protocol"
)

func fixture(t *testing.T) (Manager, config.Project, context.Context) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux integration requires tmux")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	m := Manager{Store: config.Store{Root: t.TempDir()}, Socket: "tdev-test-" + config.NewID()}
	p, err := m.Store.Create(ctx, "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.run(context.Background(), "kill-server") })
	return m, p, ctx
}
func TestConcurrentEnsureSaveAndStop(t *testing.T) {
	m, p, ctx := fixture(t)
	counter := filepath.Join(p.Directory, "once.txt")
	p, err := m.Store.Update(ctx, p.Name, 1, func(p *config.Project) error {
		p.Windows = append(p.Windows, config.Window{ID: "api", Name: "api", Directory: ".", Command: "printf x >> " + Quote(counter) + "; sleep 10"})
		f, _ := config.ParseForward("R", "9000:3000")
		p.Forwards = []config.Forward{f}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := m.Ensure(ctx, p)
			if err != nil || s.Status != "running" {
				t.Errorf("%+v %v", s, err)
			}
		}()
	}
	wg.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for {
		b, _ := os.ReadFile(counter)
		if string(b) == "x" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("startup count: %q", b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, err := m.Status(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	windows, err := m.Windows(ctx, s)
	if err != nil || len(windows) != 2 {
		t.Fatalf("windows %+v %v", windows, err)
	}
	if _, err = m.AddWindow(ctx, p, "temporary", false); err != nil {
		t.Fatal(err)
	}
	saved, err := m.Save(ctx, p, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Windows) != 2 || saved.Windows[1].Command != p.Windows[1].Command || len(saved.Forwards) != 1 {
		t.Fatalf("save changed startup definitions: %+v", saved)
	}
	if _, err = m.run(ctx, "split-window", "-d", "-t", windows[0].WindowID); err != nil {
		t.Fatal(err)
	}
	_, err = m.Save(ctx, saved, saved.Revision)
	if protocol.Normalize(err).Code != "UNSUPPORTED_LAYOUT" {
		t.Fatalf("multi pane: %v", err)
	}
	unchanged, err := m.Store.Get(p.Name)
	if err != nil || unchanged.Revision != saved.Revision {
		t.Fatalf("failed save mutated config: %+v %v", unchanged, err)
	}
	if _, err = m.Stop(ctx, p); err != nil {
		t.Fatal(err)
	}
	s, err = m.Status(ctx, p)
	if err != nil || s.Status != "missing" {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err = m.Store.Get(p.Name); err != nil {
		t.Fatal("stop deleted definition", err)
	}
}
func TestPersistentShellAndManualWindows(t *testing.T) {
	m, p, ctx := fixture(t)
	s, err := m.Ensure(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.AddWindow(ctx, p, "persistent", true)
	if err != nil || result["persisted"] != "true" {
		t.Fatalf("%+v %v", result, err)
	}
	p, err = m.Store.Get(p.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.run(ctx, "new-window", "-d", "-t", s.SessionID+":", "-n", "manual", "-c", p.Directory); err != nil {
		t.Fatal(err)
	}
	p, err = m.Save(ctx, p, p.Revision)
	if err != nil || len(p.Windows) != 3 {
		t.Fatalf("%+v %v", p, err)
	}
}
func TestIncompleteSessionNotReplayed(t *testing.T) {
	m, p, ctx := fixture(t)
	s, err := m.Ensure(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.run(ctx, "set-option", "-t", s.SessionID, "@tdev_initialization", "initializing")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Ensure(ctx, p)
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete session replayed: %v", err)
	}
}
func TestServiceExitRetainsOutputAndDoesNotRestart(t *testing.T) {
	m, p, ctx := fixture(t)
	p, err := m.Store.Update(ctx, p.Name, 1, func(p *config.Project) error { p.Windows[0].Command = "printf 'service-done\\n'; exit 7"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Ensure(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		out, err := m.run(ctx, "display-message", "-p", "-t", s.SessionID, "#{pane_dead}\t#{pane_dead_status}")
		if err != nil {
			t.Fatal(err)
		}
		if out == "1\t7" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("service not retained: %q", out)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = m.Ensure(ctx, p); err != nil {
		t.Fatal(err)
	}
	out, err := m.run(ctx, "capture-pane", "-p", "-S", "-", "-t", s.SessionID)
	if err != nil || !strings.Contains(out, "service-done") {
		t.Fatalf("output %q %v", out, err)
	}
}

func TestCJKMetadataInCLocale(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	m, p, ctx := fixture(t)
	dir := filepath.Join(p.Directory, "项目_日本語_한국어_🙂")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	name := "终端_日本語_한국어_🙂"
	p, err := m.Store.Update(ctx, p.Name, p.Revision, func(p *config.Project) error {
		p.Directory = dir
		p.Windows[0].Name = name
		p.Windows[0].Command = "printf '中文_日本語_한글_🙂\\n'; exit 0"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Ensure(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	windows, err := m.Windows(ctx, s)
	if err != nil || len(windows) != 1 || windows[0].Name != name {
		t.Fatalf("CJK window metadata corrupted: %+v %v", windows, err)
	}
	if windows[0].Directory != "" && windows[0].Directory != dir {
		t.Fatalf("CJK directory corrupted: %q", windows[0].Directory)
	}
	deadline := time.Now().Add(time.Second)
	for {
		output, err := m.run(ctx, "capture-pane", "-p", "-S", "-", "-t", s.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output, "中文_日本語_한글_🙂") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("UTF-8 service output corrupted: %q", output)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
