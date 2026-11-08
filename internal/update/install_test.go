package update

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"tdev/internal/config"
)

func TestIdenticalInstallPreservesFilesAndRollback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx := context.Background()
	store := config.Store{Root: t.TempDir()}
	destination := filepath.Join(t.TempDir(), "tdev")
	var installed Receipt
	for _, version := range []string{"0.1.0", "0.1.1"} {
		dir, pub := releaseFixture(t, version)
		b, err := Load(ctx, dir, "", pub, version, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			t.Fatal(err)
		}
		installed, err = Install(ctx, store, b, destination)
		if err != nil {
			t.Fatal(err)
		}
		if version == "0.1.1" && installed.PreviousPath == "" {
			t.Fatal("changed binary has no rollback backup")
		}
		backupDir, err := backupDirectory(store, destination)
		if err != nil {
			t.Fatal(err)
		}
		if version == "0.1.1" && filepath.Dir(installed.PreviousPath) != backupDir {
			t.Fatalf("backup outside managed directory: %s", installed.PreviousPath)
		}
		entries, err := os.ReadDir(filepath.Dir(destination))
		if err != nil || len(entries) != 1 {
			t.Fatalf("installation directory contains backup files: %v, %v", entries, err)
		}
		snapshot := func() map[string]os.FileInfo {
			t.Helper()
			files := make(map[string]os.FileInfo)
			for _, root := range []string{filepath.Dir(destination), installationDirectory(destination)} {
				err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
					if os.IsNotExist(err) && path == root {
						return nil
					}
					if err != nil {
						return err
					}
					if !info.IsDir() {
						files[path] = info
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			return files
		}
		infos := snapshot()
		contents := make(map[string]string)
		for path := range infos {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			contents[path] = string(data)
		}
		for i := 0; i < 3; i++ {
			again, err := Install(ctx, store, b, destination)
			if err != nil || again != installed {
				t.Fatalf("repeat changed receipt: %+v, %v", again, err)
			}
			if current := snapshot(); len(current) != len(infos) {
				t.Fatalf("repeat added files: %v", current)
			}
			for path, before := range infos {
				after, err := os.Stat(path)
				if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
					t.Fatalf("repeat rewrote %s: %v", path, err)
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != contents[path] {
					t.Fatalf("repeat changed %s: %v", path, err)
				}
			}
		}
		// A new authenticated installation source updates the receipt without
		// replacing the binary or losing the previous version.
		b.SourceURL = "https://server.example.invalid/releases"
		updated, err := Install(ctx, store, b, destination)
		expected := installed
		expected.SourceURL = b.SourceURL
		if err != nil || updated != expected {
			t.Fatalf("source update: %+v, %v", updated, err)
		}
		saved, err := ReadReceipt(destination)
		if err != nil || saved != expected {
			t.Fatalf("saved source update: %+v, %v", saved, err)
		}
		if current := snapshot(); len(current) != len(infos) {
			t.Fatalf("source update added files: %v", current)
		}
		after, err := os.Stat(destination)
		if err != nil || !os.SameFile(infos[destination], after) {
			t.Fatal("source update replaced binary", err)
		}
	}
	if _, err := Rollback(ctx, store, destination); err != nil {
		t.Fatal(err)
	}
	if err := Candidate(ctx, destination, "0.1.0"); err != nil {
		t.Fatal("repeat install lost previous version", err)
	}
}

func TestIdenticalInstallAdoptsExistingBinary(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx := context.Background()
	dir, pub := releaseFixture(t, "0.1.0")
	b, err := Load(ctx, dir, "", pub, "", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(b.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range []string{"missing", "stale", "malformed"} {
		t.Run(receipt, func(t *testing.T) {
			store := config.Store{Root: t.TempDir()}
			destination := filepath.Join(t.TempDir(), "tdev")
			if err := os.WriteFile(destination, data, 0600); err != nil {
				t.Fatal(err)
			}
			switch receipt {
			case "stale":
				if err := config.AtomicWrite(ReceiptPath(destination), []byte(`{"destination":"`+destination+`","sha256":"stale","previous_path":"stale-backup"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := config.AtomicWrite(ReceiptPath(destination), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r, err := Install(ctx, store, b, destination)
			if receipt == "malformed" {
				if err == nil {
					t.Fatal("silently discarded malformed receipt")
				}
				return
			}
			if err != nil || r.PreviousPath != "" || r.PreviousSHA256 != "" || r.PreviousReceipt != "" {
				t.Fatalf("adoption created rollback state: %+v, %v", r, err)
			}
			info, err := os.Stat(destination)
			if err != nil || info.Mode().Perm() != 0755 {
				t.Fatal("executable permissions not repaired", err)
			}
			entries, err := os.ReadDir(filepath.Dir(destination))
			if err != nil || len(entries) != 1 {
				t.Fatalf("adoption added backup files: %v, %v", entries, err)
			}
			saved, err := ReadReceipt(destination)
			if err != nil || saved != r {
				t.Fatalf("adoption receipt: %+v, %v", saved, err)
			}
		})
	}
}

func TestRollbackLegacyBackupAndTargetIsolation(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx := context.Background()
	store := config.Store{Root: t.TempDir()}
	destination := filepath.Join(t.TempDir(), "tdev")
	otherDestination := filepath.Join(t.TempDir(), "tdev")
	var installed Receipt
	for _, version := range []string{"0.1.0", "0.1.1"} {
		dir, pub := releaseFixture(t, version)
		b, err := Load(ctx, dir, "", pub, "", runtime.GOOS, runtime.GOARCH)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{destination, otherDestination} {
			r, err := Install(ctx, store, b, target)
			if err != nil {
				t.Fatal(err)
			}
			if target == destination {
				installed = r
			}
		}
	}
	writeReceipt := func(r Receipt) {
		t.Helper()
		data, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := config.AtomicWrite(ReceiptPath(destination), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	other, err := ReadReceipt(otherDestination)
	if err != nil {
		t.Fatal(err)
	}
	wrongTarget := installed
	wrongTarget.PreviousPath = other.PreviousPath
	wrongTarget.PreviousReceipt = other.PreviousReceipt
	writeReceipt(wrongTarget)
	if _, err := Rollback(ctx, store, destination); err == nil {
		t.Fatal("accepted another installation's backup")
	}
	legacyPath := destination + ".backup-legacy"
	if err := os.Rename(installed.PreviousPath, legacyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(installed.PreviousReceipt, legacyPath+".json"); err != nil {
		t.Fatal(err)
	}
	installed.PreviousPath = legacyPath
	installed.PreviousReceipt = legacyPath + ".json"
	writeReceipt(installed)
	if _, err := Rollback(ctx, store, destination); err != nil {
		t.Fatal("legacy rollback failed", err)
	}
	if err := Candidate(ctx, destination, "0.1.0"); err != nil {
		t.Fatal(err)
	}
}
