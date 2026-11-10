package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"tdev/internal/config"
	"tdev/internal/protocol"
)

func TestRuntimeMigrationPreservesStateAndRetries(t *testing.T) {
	m, p, ctx := fixture(t)
	s, err := m.Ensure(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		s, e := m.run(ctx, args...)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	pane := run("display-message", "-p", "-t", s.SessionID, "#{pane_id}:#{pane_pid}")
	// Reconstruct legacy v1, including duplicate actions left by an interrupted
	// initializer. A reserved-index collision must preserve user actions.
	run("set-option", "-u", "-t", s.SessionID, "@tdev_runtime_config_version")
	for _, hook := range alertHooks(s.SessionID) {
		run("set-hook", "-u", "-t", s.SessionID, hook[0]+"[9000]")
		run("set-hook", "-t", s.SessionID, hook[0]+"[0]", hook[1])
		run("set-hook", "-t", s.SessionID, hook[0]+"[1]", hook[1])
	}
	run("set-option", "-u", "-t", s.SessionID, "@tdev_runtime_hook_index")
	run("set-hook", "-t", s.SessionID, "alert-bell[9000]", "set-option @user_action preserved")
	run("set-option", "-t", s.SessionID, "@tdev_terminal_focused", "0")
	run("set-option", "-w", "-t", s.SessionID+":", "@tdev_unfocused_bell", "1")
	run("set-option", "-t", s.SessionID, "set-titles-string", "old-title")
	rows, err := m.ReconcileRuntime(ctx, false)
	if err != nil || len(rows) != 1 || rows[0].Status != "applied" {
		t.Fatalf("migration: %+v %v", rows, err)
	}
	if run("show-options", "-v", "-t", s.SessionID, "@tdev_terminal_focused") != "0" || run("show-options", "-wqv", "-t", s.SessionID+":", "@tdev_unfocused_bell") != "1" {
		t.Fatal("migration acknowledged focus/bell state")
	}
	if got := run("display-message", "-p", "-t", s.SessionID, "#{pane_id}:#{pane_pid}"); got != pane {
		t.Fatal("pane replaced", got)
	}
	if current, e := m.Status(ctx, p); e != nil || current.Generation != s.Generation {
		t.Fatal("session generation changed")
	}
	hooks := run("show-hooks", "-t", s.SessionID, "alert-bell")
	if strings.Count(hooks, "if-shell") != 1 || !strings.Contains(hooks, "alert-bell[9000] set-option @user_action preserved") || !strings.Contains(hooks, "alert-bell[9001]") {
		t.Fatal("legacy hooks or user collision mishandled", hooks)
	}
	// Simulate interruption after hook installation, before version commit.
	run("set-option", "-u", "-t", s.SessionID, "@tdev_runtime_config_version")
	if _, err = m.ReconcileRuntime(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := run("show-hooks", "-t", s.SessionID, "alert-bell"); got != hooks {
		t.Fatal("retry changed hooks", got)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := m.ReconcileRuntime(ctx, false); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if got := run("show-hooks", "-t", s.SessionID, "alert-bell"); got != hooks {
		t.Fatal("concurrent retry changed hooks", got)
	}
	// A user replacing an owned action causes a retryable conflict, not deletion.
	run("set-option", "-u", "-t", s.SessionID, "@tdev_runtime_config_version")
	run("set-hook", "-t", s.SessionID, "alert-bell[9001]", "set-option @custom protected")
	if _, e := m.ReconcileRuntime(ctx, false); e == nil {
		t.Fatal("owned hook collision accepted")
	}
	if run("show-options", "-qv", "-t", s.SessionID, "@tdev_runtime_config_version") != "" {
		t.Fatal("failed migration committed version")
	}
	run("set-hook", "-u", "-t", s.SessionID, "alert-bell[9001]")
	if _, e := m.ReconcileRuntime(ctx, false); e != nil {
		t.Fatal(e)
	}
}

func TestRuntimeScopeAndFutureVersion(t *testing.T) {
	m, p, ctx := fixture(t)
	s, err := m.Ensure(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		s, e := m.run(ctx, args...)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	other := run("new-session", "-d", "-P", "-F", "#{session_id}", "-s", "unmanaged")
	scratch, err := m.CreateScratch(ctx, "legacy", p.Directory)
	if err != nil {
		t.Fatal(err)
	}
	run("set-option", "-u", "-t", scratch.SessionID, "@tdev_config_owner")
	run("set-option", "-u", "-t", scratch.SessionID, "@tdev_runtime_config_version")
	foreign := Manager{Store: config.Store{Root: t.TempDir()}, Socket: m.Socket}
	if rows, e := foreign.ReconcileRuntime(ctx, false); e != nil || len(rows) != 2 || rows[0].Status != "skipped" || rows[1].Status != "skipped" {
		t.Fatalf("foreign scope %+v %v", rows, e)
	}
	rows, err := m.ReconcileRuntime(ctx, true)
	if err != nil || len(rows) != 2 {
		t.Fatalf("adopt %+v %v", rows, err)
	}
	if run("show-options", "-qv", "-t", other, "@tdev_runtime_config_version") != "" {
		t.Fatal("unmanaged session changed")
	}
	run("set-option", "-t", s.SessionID, "@tdev_runtime_config_version", "2")
	run("set-option", "-t", s.SessionID, "set-titles-string", "future-title")
	rows, err = m.ReconcileRuntime(ctx, false)
	if err == nil || protocol.Normalize(err).Code != "PARTIAL_SUCCESS" {
		t.Fatalf("future version %+v %v", rows, err)
	}
	if run("show-options", "-v", "-t", s.SessionID, "set-titles-string") != "future-title" || run("show-options", "-v", "-t", s.SessionID, "@tdev_runtime_config_version") != "2" {
		t.Fatal("future configuration overwritten")
	}
	// Attach's direct initializer also honors ownership and future versions.
	if e := m.configureAlerts(ctx, s.SessionID); e == nil || protocol.Normalize(e).Code != "PROTOCOL_INCOMPATIBLE" {
		t.Fatalf("future attach %v", e)
	}
	if e := foreign.configureAlerts(ctx, scratch.SessionID); e == nil || protocol.Normalize(e).Code != "CONFIG_CONFLICT" {
		t.Fatalf("foreign scratch %v", e)
	}
}

func TestRuntimeNoServerDoesNotStartOne(t *testing.T) {
	m, _, ctx := fixture(t)
	if rows, err := m.ReconcileRuntime(ctx, false); err != nil || len(rows) != 0 {
		t.Fatalf("absent server %+v %v", rows, err)
	}
	if _, err := m.run(ctx, "list-sessions"); !noServer(err) {
		t.Fatalf("started server: %v", err)
	}
	// A malformed project definition is a configuration failure.
	if err := os.MkdirAll(filepath.Join(m.Store.Root, "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Store.Root, "projects", "broken.yaml"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReconcileRuntime(context.Background(), false); err == nil {
		t.Fatal("invalid project ignored")
	}
}

func TestRuntimeConcurrentScratchAdoptionAcrossRoots(t *testing.T) {
	m, p, ctx := fixture(t)
	scratch, err := m.CreateScratch(ctx, "legacy", p.Directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range []string{"@tdev_config_owner", "@tdev_runtime_config_version"} {
		if _, err = m.run(ctx, "set-option", "-u", "-t", scratch.SessionID, option); err != nil {
			t.Fatal(err)
		}
	}
	other := Manager{Store: config.Store{Root: t.TempDir()}, Socket: m.Socket}
	results := make(chan error, 2)
	for _, manager := range []Manager{m, other} {
		go func(manager Manager) { _, err := manager.configureRuntime(ctx, scratch.SessionID); results <- err }(manager)
	}
	success, conflict := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else if protocol.Normalize(err).Code == "CONFIG_CONFLICT" {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("scratch adopted by multiple roots: success=%d conflict=%d", success, conflict)
	}
}
