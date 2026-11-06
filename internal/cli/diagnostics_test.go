package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	"tdev/internal/config"
	"tdev/internal/protocol"
)

func diagnosticEvents(t *testing.T, root string) ([]diagnosticEvent, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "logs", "diagnostics.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var events []diagnosticEvent
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		var event diagnosticEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events, string(data)
}

func TestDiagnosticsCLIPrivacyAndJSON(t *testing.T) {
	root := t.TempDir()
	run(t, root, "", "version")
	if _, err := os.Stat(filepath.Join(root, "logs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("successful commands should not create logs", err)
	}
	code, response := run(t, root, "", "--debug", "api", "project", "get", "private-project-sentinel")
	if code != 3 || response.Error.Code != "PROJECT_NOT_FOUND" {
		t.Fatal(code, response)
	}
	events, data := diagnosticEvents(t, root)
	if strings.Contains(data, "private-project-sentinel") || events[len(events)-1].Operation != "tdev api project get" {
		t.Fatal("logs must contain command names without arguments", data)
	}
	run(t, root, "", "--debug", "version")
	events, _ = diagnosticEvents(t, root)
	if events[len(events)-1].Level != "debug" {
		t.Fatal("debug success not logged", events)
	}
	code, response = run(t, root, "", "api", "project", "get")
	events, _ = diagnosticEvents(t, root)
	if code != 2 || response.Error.Code != "INVALID_ARGUMENT" || events[len(events)-1].Code != response.Error.Code {
		t.Fatal("log code differs from final protocol error", code, response, events)
	}
	for _, item := range []struct {
		name string
		mode os.FileMode
	}{{"logs", 0700}, {"logs/diagnostics.jsonl", 0600}} {
		info, err := os.Stat(filepath.Join(root, item.name))
		if err != nil || info.Mode().Perm() != item.mode {
			t.Fatal("private log permissions", item.name, info, err)
		}
	}
}

func TestDiagnosticsRotationAndConcurrentWriters(t *testing.T) {
	root := t.TempDir()
	a := &app{store: config.Store{Root: root}, debug: true}
	path, err := a.recordDiagnostic("test", "", time.Now(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, diagnosticLimit), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := a.recordDiagnostic(fmt.Sprintf("rotate_%d", i), "", time.Now(), nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(path + "*")
	if err != nil || len(files) != 4 {
		t.Fatal("expected current file and three backups", files, err)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.recordDiagnostic("concurrent", "", time.Now(), nil, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	events, _ := diagnosticEvents(t, root)
	if len(events) != 7 {
		t.Fatal("lost or interleaved records", len(events))
	}
}

func TestDiagnosticsRefusesSymlinksAndPreservesCommandResult(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "untouched")
	if err := os.WriteFile(outside, []byte("sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "logs", "diagnostics.jsonl")); err != nil {
		t.Fatal(err)
	}
	code, response := run(t, root, "", "--debug", "version")
	if code != 0 || !response.OK {
		t.Fatal("logging failure changed JSON result", code, response)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "sentinel" {
		t.Fatal("followed diagnostic symlink", err)
	}
}

func TestCompletionFailureDiagnostics(t *testing.T) {
	root := t.TempDir()
	a := &app{store: config.Store{Root: root}, timeout: 20 * time.Millisecond}
	_, err := a.shell(context.Background(), target{Local: true}, "sleep 1")
	if diagnosticReason(err) != "deadline_exceeded" {
		t.Fatal("lost timeout cause", err)
	}
	m := &promptModel{input: textinput.New(), width: 160, height: 24, onFailure: a.completionFailure}
	m.Update(completionResult{err: err, started: time.Now().Add(-time.Second)})
	if !strings.Contains(m.View().Content, "deadline_exceeded") || !strings.Contains(m.View().Content, "diagnostics.jsonl") {
		t.Fatal("failure does not show reason and log location", m.View().Content)
	}
	_, err = a.shell(context.Background(), target{Local: true}, "printf 'private-token-sentinel Permission denied' >&2; exit 23")
	if err == nil {
		t.Fatal("expected shell failure")
	}
	events, data := diagnosticEvents(t, root)
	last := events[len(events)-1]
	if strings.Contains(data, "private-token-sentinel") || last.Reason != "permission_denied" || last.ExitCode == nil || *last.ExitCode != 23 {
		t.Fatal("shell diagnostics missing status or persisted raw stderr", data)
	}
	// Unknown output is classified without persisting its contents.
	a.completionFailure(protocol.Fail("CONNECTION_FAILED", 6, "private-secret-sentinel"), time.Now())
	_, data = diagnosticEvents(t, root)
	if strings.Contains(data, "private-secret-sentinel") {
		t.Fatal("raw error persisted")
	}
}
