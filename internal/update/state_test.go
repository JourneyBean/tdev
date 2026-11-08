package update

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tdev/internal/config"
)

func TestStateRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	if got := StateRoot(); got != filepath.Join(home, ".local", "state", "tdev") {
		t.Fatal(got)
	}
	t.Setenv("XDG_STATE_HOME", "relative")
	if got := StateRoot(); got != filepath.Join(home, ".local", "state", "tdev") {
		t.Fatal(got)
	}
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if got := StateRoot(); got != filepath.Join(state, "tdev") {
		t.Fatal(got)
	}
}

func TestLegacyStateMigrationAndRepeatedRollback(t *testing.T) {
	for _, operation := range []string{"install", "rollback", "config-install", "config-rollback"} {
		t.Run(operation, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			ctx := context.Background()
			store := config.Store{Root: t.TempDir()}
			destination := filepath.Join(t.TempDir(), "custom-binary")
			var latest Bundle
			for _, version := range []string{"0.1.0", "0.1.1", "0.1.2"} {
				dir, pub := releaseFixture(t, version)
				b, err := Load(ctx, dir, "", pub, "", runtime.GOOS, runtime.GOARCH)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { b.Close() })
				if _, err := Install(ctx, store, b, destination); err != nil {
					t.Fatal(err)
				}
				latest = b
			}
			current, err := ReadReceipt(destination)
			if err != nil {
				t.Fatal(err)
			}
			// Reconstruct a complete legacy chain, including the historical receipts.
			var legacy func(Receipt) Receipt
			legacy = func(r Receipt) Receipt {
				if r.PreviousPath == "" {
					return r
				}
				old := r.PreviousPath
				next := destination + ".backup-" + filepath.Base(old)
				if strings.HasPrefix(operation, "config-") {
					next = filepath.Join(store.Root, "binary-backups", lockKey(destination), filepath.Base(old))
					if err := os.MkdirAll(filepath.Dir(next), 0700); err != nil {
						t.Fatal(err)
					}
				}
				if r.PreviousReceipt != "" {
					data, err := os.ReadFile(r.PreviousReceipt)
					if err != nil {
						t.Fatal(err)
					}
					var prior Receipt
					if err := json.Unmarshal(data, &prior); err != nil {
						t.Fatal(err)
					}
					prior = legacy(prior)
					encoded, _ := json.MarshalIndent(prior, "", "  ")
					if err := config.AtomicWrite(next+".json", encoded, 0600); err != nil {
						t.Fatal(err)
					}
					r.PreviousReceipt = next + ".json"
				}
				if err := os.Rename(old, next); err != nil {
					t.Fatal(err)
				}
				r.PreviousPath = next
				return r
			}
			current = legacy(current)
			encoded, _ := json.MarshalIndent(current, "", "  ")
			if err := config.AtomicWrite(legacyReceiptPath(destination), encoded, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(installationDirectory(destination)); err != nil {
				t.Fatal(err)
			}
			if got, err := ReadReceipt(destination); err != nil || got != current {
				t.Fatalf("legacy discovery: %+v %v", got, err)
			}
			if _, err := os.Stat(ReceiptPath(destination)); !os.IsNotExist(err) {
				t.Fatal("read migrated state")
			}
			if strings.HasSuffix(operation, "install") {
				if _, err := Install(ctx, store, latest, destination); err != nil {
					t.Fatal(err)
				}
				got, err := ReadReceipt(destination)
				if err != nil || filepath.Dir(got.PreviousPath) != filepath.Join(installationDirectory(destination), "backups") {
					t.Fatalf("migration: %+v %v", got, err)
				}
			}
			if _, err := Rollback(ctx, store, destination); err != nil {
				t.Fatal(err)
			}
			if err := Candidate(ctx, destination, "0.1.1"); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(filepath.Dir(destination))
			if err != nil || len(entries) != 1 {
				t.Fatalf("legacy files remain: %v %v", entries, err)
			}
			if _, err := Rollback(ctx, store, destination); err != nil {
				t.Fatal(err)
			}
			if err := Candidate(ctx, destination, "0.1.0"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationFailureRetainsLegacyState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	destination := filepath.Join(t.TempDir(), "tdev")
	data := []byte("current binary")
	if err := os.WriteFile(destination, data, 0700); err != nil {
		t.Fatal(err)
	}
	// Correct current digest, but a damaged backup: migration must stop before
	// publishing state or deleting the original receipt and backup.
	backup := destination + ".backup-old"
	if err := os.WriteFile(backup, []byte("damaged"), 0700); err != nil {
		t.Fatal(err)
	}
	r := Receipt{Destination: destination, SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), PreviousPath: backup, PreviousSHA256: "invalid"}
	encoded, _ := json.Marshal(r)
	if err := os.WriteFile(legacyReceiptPath(destination), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Rollback(context.Background(), config.Store{Root: t.TempDir()}, destination); err == nil {
		t.Fatal("accepted damaged backup")
	}
	for _, path := range []string{destination, backup, legacyReceiptPath(destination)} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(ReceiptPath(destination)); !os.IsNotExist(err) {
		t.Fatal("published failed migration")
	}
}
