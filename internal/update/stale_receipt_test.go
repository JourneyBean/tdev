package update

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"tdev/internal/config"
)

func TestLegacyStaleBackupReceipt(t *testing.T) {
	for _, location := range []string{"adjacent", "config", "managed"} {
		for _, operation := range []string{"install", "upgrade", "rollback"} {
			for _, damage := range []string{"stale", "malformed", "wrong-target", "binary"} {
				t.Run(location+"/"+operation+"/"+damage, func(t *testing.T) {
					t.Setenv("XDG_STATE_HOME", t.TempDir())
					ctx := context.Background()
					store := config.Store{Root: t.TempDir()}
					destination := filepath.Join(t.TempDir(), "tdev")
					dir, pub := releaseFixture(t, "0.1.0")
					bundle, err := Load(ctx, dir, "", pub, "", runtime.GOOS, runtime.GOARCH)
					if err != nil {
						t.Fatal(err)
					}
					defer bundle.Close()
					root, err := Install(ctx, store, bundle, destination)
					if err != nil {
						t.Fatal(err)
					}
					backup := destination + ".backup-old"
					if location == "config" {
						backup = filepath.Join(store.Root, "binary-backups", lockKey(destination), "tdev.backup-old")
					} else if location == "managed" {
						backup = filepath.Join(installationDirectory(destination), "backups", "tdev.backup-old")
					}
					old := []byte("externally replaced old binary")
					prior := Receipt{Destination: destination, SHA256: "stale", PreviousPath: destination + ".backup-older"}
					if damage == "wrong-target" {
						prior.Destination = destination + "-other"
					}
					priorData, _ := json.Marshal(prior)
					if damage == "malformed" {
						priorData = []byte("{")
					}
					root.PreviousPath = backup
					root.PreviousSHA256 = fmt.Sprintf("%x", sha256.Sum256(old))
					root.PreviousReceipt = backup + ".json"
					if damage == "binary" {
						root.PreviousSHA256 = "wrong-digest"
					}
					encoded, _ := json.Marshal(root)
					files := map[string][]byte{
						backup: old, backup + ".json": priorData,
						prior.PreviousPath:             []byte("older history to preserve"),
						legacyReceiptPath(destination): encoded,
					}
					for path, data := range files {
						if err := config.AtomicWrite(path, data, 0600); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Remove(ReceiptPath(destination)); err != nil {
						t.Fatal(err)
					}
					if operation == "upgrade" {
						dir, pub := releaseFixture(t, "0.1.1")
						bundle, err = Load(ctx, dir, "", pub, "", runtime.GOOS, runtime.GOARCH)
						if err != nil {
							t.Fatal(err)
						}
						defer bundle.Close()
					}
					if operation == "rollback" {
						_, err = Rollback(ctx, store, destination)
					} else {
						_, err = Install(ctx, store, bundle, destination)
					}
					wantSuccess := damage == "stale" && location != "managed"
					if (err == nil) != wantSuccess {
						t.Fatalf("success=%v, error=%v", wantSuccess, err)
					}
					for path, data := range files {
						if path == legacyReceiptPath(destination) && wantSuccess {
							continue
						}
						got, readErr := os.ReadFile(path)
						if readErr != nil || string(got) != string(data) {
							t.Fatalf("original changed: %s: %v", path, readErr)
						}
					}
					if !wantSuccess {
						if err := Candidate(ctx, destination, "0.1.0"); err != nil {
							t.Fatal("failed migration changed binary", err)
						}
						return
					}
					if operation == "upgrade" {
						if _, err := Rollback(ctx, store, destination); err != nil {
							t.Fatal(err)
						}
						if err := Candidate(ctx, destination, "0.1.0"); err != nil {
							t.Fatal(err)
						}
					}
					if operation != "rollback" {
						r, err := ReadReceipt(destination)
						if err != nil || r.PreviousReceipt != "" || r.PreviousPath == "" {
							t.Fatalf("stale receipt retained: %+v, %v", r, err)
						}
						if _, err := Rollback(ctx, store, destination); err != nil {
							t.Fatal(err)
						}
					}
					got, err := os.ReadFile(destination)
					if err != nil || string(got) != string(old) {
						t.Fatal("rollback bytes differ", err)
					}
					if _, err := ReadReceipt(destination); !os.IsNotExist(err) {
						t.Fatal("restored a stale receipt", err)
					}
				})
			}
		}
	}
}

func TestChangedBinaryUpgradeDoesNotCopyStaleReceipt(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx := context.Background()
	store := config.Store{Root: t.TempDir()}
	destination := filepath.Join(t.TempDir(), "tdev")
	dir, pub := releaseFixture(t, "0.1.0")
	bundle, err := Load(ctx, dir, "", pub, "", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if _, err := Install(ctx, store, bundle, destination); err != nil {
		t.Fatal(err)
	}
	old := []byte("external replacement")
	if err := os.WriteFile(destination, old, 0700); err != nil {
		t.Fatal(err)
	}
	r, err := Install(ctx, store, bundle, destination)
	if err != nil || r.PreviousReceipt != "" || r.PreviousPath == "" {
		t.Fatalf("copied stale receipt: %+v, %v", r, err)
	}
	if _, err := Rollback(ctx, store, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != string(old) {
		t.Fatal("external replacement not restored", err)
	}
}
