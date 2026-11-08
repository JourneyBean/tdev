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

func TestInstallRetainsTwoPreviousVersions(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx := context.Background()
	store := config.Store{Root: t.TempDir()}
	destination := filepath.Join(t.TempDir(), "tdev")
	var latest Bundle
	for i, version := range []string{"0.1.0", "0.1.1", "0.1.2", "0.1.3", "0.1.4"} {
		dir, pub := releaseFixture(t, version)
		b, err := Load(ctx, dir, "", pub, version, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Install(ctx, store, b, destination); err != nil {
			t.Fatal(err)
		}
		latest = b
		want := i
		if want > 2 {
			want = 2
		}
		assertBackupCount(t, store, destination, want)
	}
	// Reinstalling current bytes preserves both historical versions.
	if _, err := Install(ctx, store, latest, destination); err != nil {
		t.Fatal(err)
	}
	assertBackupCount(t, store, destination, 2)
	for i, version := range []string{"0.1.3", "0.1.2"} {
		if _, err := Rollback(ctx, store, destination); err != nil {
			t.Fatal(err)
		}
		if err := Candidate(ctx, destination, version); err != nil {
			t.Fatal(err)
		}
		assertBackupCount(t, store, destination, 1-i)
	}
	if _, err := Rollback(ctx, store, destination); err == nil {
		t.Fatal("rolled back beyond retained history")
	}
	entries, err := os.ReadDir(filepath.Dir(destination))
	if err != nil || len(entries) != 1 || entries[0].Name() != "tdev" {
		t.Fatalf("installation directory contains state: %v, %v", entries, err)
	}
}

func assertBackupCount(t *testing.T, store config.Store, destination string, versions int) {
	t.Helper()
	dir, err := backupDirectory(store, destination)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) && versions == 0 {
		return
	}
	if err != nil || len(entries) != versions*2 {
		t.Fatalf("want %d backup pairs, got %v: %v", versions, entries, err)
	}
}

func TestPruningPreservesUnrelatedFilesAndOtherTargets(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx := context.Background()
	store := config.Store{Root: t.TempDir()}
	destination := filepath.Join(t.TempDir(), "tdev")
	other := filepath.Join(t.TempDir(), "tdev")
	for _, version := range []string{"0.1.0", "0.1.1", "0.1.2", "0.1.3"} {
		dir, pub := releaseFixture(t, version)
		b, err := Load(ctx, dir, "", pub, version, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{destination, other} {
			if _, err := Install(ctx, store, b, target); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir, err := backupDirectory(store, destination)
	if err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(dir, "tdev.backup-user-notes")
	if err := os.WriteFile(notes, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "tdev.backup-20000101T000000.000000000-12345678")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if err := pruneBackups(store, destination); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{notes, victim, link} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatal("removed unrelated file", err)
		}
	}
	assertBackupCount(t, store, other, 2)
	if _, err := Rollback(ctx, store, other); err != nil {
		t.Fatal("pruning affected another target", err)
	}
	if err := Candidate(ctx, other, "0.1.2"); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyMigrationLimitsRollbackHistory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx := context.Background()
	store := config.Store{Root: t.TempDir()}
	destination := filepath.Join(t.TempDir(), "tdev")
	var current Receipt
	var latest Bundle
	for i, version := range []string{"0.1.0", "0.1.1", "0.1.2", "0.1.3", "0.1.4"} {
		dir, pub := releaseFixture(t, version)
		b, err := Load(ctx, dir, "", pub, version, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			t.Fatal(err)
		}
		latest = b
		if i > 0 {
			path := destination + ".backup-fixture-" + version
			old, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, old, 0700); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(current)
			if err := os.WriteFile(path+".json", encoded, 0600); err != nil {
				t.Fatal(err)
			}
			current = Receipt{Destination: destination, PreviousPath: path, PreviousSHA256: current.SHA256, PreviousReceipt: path + ".json"}
		}
		current.Destination = destination
		current.Version = version
		current.SHA256 = b.Artifact.SHA256
		current.PublicKey = string(b.PublicKey)
		data, err := os.ReadFile(b.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0755); err != nil {
			t.Fatal(err)
		}
	}
	encoded, _ := json.Marshal(current)
	if err := os.WriteFile(legacyReceiptPath(destination), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(ctx, store, latest, destination); err != nil {
		t.Fatal(err)
	}
	assertBackupCount(t, store, destination, 2)
	entries, err := os.ReadDir(filepath.Dir(destination))
	if err != nil || len(entries) != 1 {
		t.Fatalf("legacy state remains in bin directory: %v, %v", entries, err)
	}
	for _, version := range []string{"0.1.3", "0.1.2"} {
		if _, err := Rollback(ctx, store, destination); err != nil {
			t.Fatal(err)
		}
		if err := Candidate(ctx, destination, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Rollback(ctx, store, destination); err == nil {
		t.Fatal("legacy migration retained more than two versions")
	}
}
