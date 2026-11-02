package mount

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/sshx"
)

func TestNonemptyAndOtherSourceProtected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "original"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	ran := false
	m := New(config.Store{Root: t.TempDir()})
	m.Inventory = func() ([]Entry, error) { return nil, nil }
	m.Run = func(context.Context, string, ...string) error { ran = true; return nil }
	if _, err := m.Mount(context.Background(), "srv", "demo", "id", "fixture", "/remote", path, sshx.Defaults(), nil); err == nil || ran {
		t.Fatal("nonempty mountpoint was used")
	}
	m.Inventory = func() ([]Entry, error) {
		return []Entry{{Path: path, Source: "other:/remote", Type: "fuse.sshfs", ID: "1"}}, nil
	}
	if _, err := m.Mount(context.Background(), "srv", "demo", "id", "fixture", "/remote", path, sshx.Defaults(), nil); err == nil || ran {
		t.Fatal("other mount source was reused")
	}
}
func TestOwnedIdentityBusyAndMetadataVerification(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mount")
	m := New(config.Store{Root: t.TempDir()})
	m.OS = "darwin"
	m.Start = nil
	m.Busy = nil
	m.Lookup = func(string) (string, error) { return "/test/sshfs", nil }
	var entries []Entry
	var options []string
	busy := true
	m.Inventory = func() ([]Entry, error) { return entries, nil }
	m.Run = func(ctx context.Context, name string, args ...string) error {
		if name == "sshfs" {
			options = append([]string{}, args...)
			token := ""
			for _, a := range args {
				if strings.HasPrefix(a, "fsname=") {
					token = strings.TrimPrefix(a, "fsname=")
				}
			}
			entries = []Entry{{Path: path, Source: token, Type: "macfuse", ID: "owned"}}
			return nil
		}
		if busy {
			return errors.New("resource busy")
		}
		entries = nil
		return nil
	}
	r, err := m.Mount(ctx, "srv", "demo", "id", "fixture", "/remote", path, sshx.Defaults(), func(context.Context, string) error { return nil })
	if err != nil || !r.MetadataVerified {
		t.Fatalf("%+v %v", r, err)
	}
	if !strings.Contains(strings.Join(options, " "), "noappledouble,noapplexattr") {
		t.Fatal("metadata options omitted")
	}
	again, err := m.Mount(ctx, "srv", "demo", "id", "fixture", "/remote", path, sshx.Defaults(), nil)
	if err != nil || again.Token != r.Token {
		t.Fatal("did not reuse same owned mount")
	}
	if _, err = m.Unmount(ctx, "srv", "demo", path); err == nil {
		t.Fatal("busy unmount succeeded")
	}
	if _, err = os.Stat(m.state(path)); err != nil {
		t.Fatal("busy state lost")
	}
	entries[0].ID = "replacement"
	busy = false
	if _, err = m.Unmount(ctx, "srv", "demo", path); err == nil {
		t.Fatal("unmounted replacement filesystem")
	}
	entries[0].ID = "owned"
	if _, err = m.Unmount(ctx, "srv", "demo", path); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("mount remained")
	}
}
func TestAmbiguousUnmount(t *testing.T) {
	m := New(config.Store{Root: t.TempDir()})
	for _, p := range []string{filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")} {
		r := Record{Path: p, Source: "fixture:/remote", Server: "srv", ProjectID: "id", ProjectName: "demo"}
		b, _ := json.Marshal(r)
		if err := config.AtomicWrite(m.state(p), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Unmount(context.Background(), "srv", "demo", ""); err == nil {
		t.Fatal("ambiguous unmount did not require a path")
	}
}

func TestForegroundFilesystemServiceSurvivesMountCommandContext(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "sshfs")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	p, err := launch(ctx, filepath.Join(t.TempDir(), "sshfs.log"), "-f")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	cancel()
	select {
	case <-p.Done:
		t.Fatal("mount command context killed persistent filesystem service")
	case <-time.After(50 * time.Millisecond):
	}
	p.Stop()
	select {
	case <-p.Done:
	default:
		t.Fatal("owned foreground service was not reaped")
	}
}
