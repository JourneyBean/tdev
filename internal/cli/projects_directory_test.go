package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tdev/internal/config"
)

func TestProjectDirectoryCreation(t *testing.T) {
	home := t.TempDir()
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	a := &app{store: config.Store{Root: t.TempDir()}, timeout: 5 * time.Second}
	ctx, local := context.Background(), target{Local: true}
	dir := "~/中文 项目' $(false)/nested"
	if _, err := a.remoteDirectory(ctx, local, dir); !errors.Is(err, errProjectDirectoryMissing) {
		t.Fatal("expected missing directory", err)
	}
	got, err := a.createProjectDirectory(ctx, local, dir)
	want := filepath.Join(home, "中文 项目' $(false)", "nested")
	if err != nil || got != want {
		t.Fatal("create and resolve home path", got, err)
	}
	if got, err = a.createProjectDirectory(ctx, local, dir); err != nil || got != want {
		t.Fatal("existing directory should be reusable", got, err)
	}
	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "broken-link")
	if err := os.Symlink(filepath.Join(home, "absent"), link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, link} {
		if _, err := a.remoteDirectory(ctx, local, path); err == nil || errors.Is(err, errProjectDirectoryMissing) {
			t.Fatal("existing file or symlink classified as missing", path, err)
		}
	}
	for _, path := range []string{"relative/path", "", "~/bad\npath", filepath.Join(file, "child")} {
		if _, err := a.createProjectDirectory(ctx, local, path); err == nil {
			t.Fatal("invalid path or mkdir failure accepted", path)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.remoteDirectory(cancelled, local, filepath.Join(home, "absent")); err == nil || errors.Is(err, errProjectDirectoryMissing) {
		t.Fatal("shell failure classified as missing", err)
	}
}
