package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/sshx"
	"tdev/internal/transfer"
)

type Receipt struct {
	Version         string `json:"version"`
	Destination     string `json:"destination"`
	SHA256          string `json:"sha256"`
	SourceURL       string `json:"source_url"`
	PublicKey       string `json:"public_key"`
	KeyFingerprint  string `json:"key_fingerprint"`
	PreviousPath    string `json:"previous_path,omitempty"`
	PreviousSHA256  string `json:"previous_sha256,omitempty"`
	PreviousReceipt string `json:"previous_receipt,omitempty"`
}

func StateRoot() string {
	if root := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(root) {
		return filepath.Join(root, "tdev")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "tdev")
}
func installationDirectory(destination string) string {
	hash := sha256.Sum256([]byte(destination))
	return filepath.Join(StateRoot(), "installations", hex.EncodeToString(hash[:]))
}
func ReceiptPath(destination string) string {
	return filepath.Join(installationDirectory(destination), "receipt.json")
}
func legacyReceiptPath(destination string) string { return destination + ".tdev-install.json" }
func ReadReceipt(destination string) (Receipt, error) {
	var r Receipt
	b, err := os.ReadFile(ReceiptPath(destination))
	if os.IsNotExist(err) {
		b, err = os.ReadFile(legacyReceiptPath(destination))
	}
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	if err == nil && r.Destination != destination {
		return r, VerifyError("installation receipt path mismatch")
	}
	return r, err
}
func DefaultDestination() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "bin", "tdev")
}
func Destination(path string) (string, error) {
	if path == "" {
		path = DefaultDestination()
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	return transfer.Canonical(path)
}
func Candidate(ctx context.Context, path, version string) error {
	cmd := sshx.Command(ctx, path, "version", "--json")
	out, err := cmd.Output()
	if err != nil {
		return VerifyError("candidate version check failed: %v", err)
	}
	var r struct {
		ProtocolVersion int  `json:"protocol_version"`
		OK              bool `json:"ok"`
		Data            struct {
			Version         string `json:"version"`
			ProtocolVersion int    `json:"protocol_version"`
			SchemaVersion   int    `json:"schema_version"`
		} `json:"data"`
	}
	if err = json.Unmarshal(out, &r); err != nil {
		return VerifyError("candidate returned invalid version protocol")
	}
	if !r.OK || r.ProtocolVersion != 1 || r.Data.ProtocolVersion != 1 || r.Data.SchemaVersion != 1 || strings.TrimPrefix(r.Data.Version, "v") != strings.TrimPrefix(version, "v") {
		return VerifyError("candidate version/protocol differs from signed manifest")
	}
	return nil
}
func lockKey(destination string) string {
	h := sha256.Sum256([]byte(destination))
	return "binary-" + hex.EncodeToString(h[:12])
}

// backupDirectory isolates rollback data for each canonical installation target.
func backupDirectory(store config.Store, destination string) (string, error) {
	return transfer.Canonical(filepath.Join(installationDirectory(destination), "backups"))
}

func Install(ctx context.Context, store config.Store, b Bundle, destination string) (Receipt, error) {
	var r Receipt
	var err error
	destination, err = Destination(destination)
	if err != nil {
		return r, err
	}
	r.Destination = destination
	if err = b.Check(); err != nil {
		return r, err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return r, err
	}
	unlock, err := (config.Store{Root: StateRoot()}).Lock(ctx, lockKey(destination), "install")
	if err != nil {
		return r, err
	}
	defer unlock()
	if err = migrateInstallation(store, destination); err != nil {
		return r, err
	}
	before, err := transfer.SnapshotPath(destination)
	if err != nil {
		return r, err
	}
	if before.Kind == "directory" {
		return r, VerifyError("binary destination is a directory")
	}
	// Execute a private copy of already verified bytes; a release directory may be read-only/non-executable.
	data, err := os.ReadFile(b.Path)
	if err != nil {
		return r, err
	}
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != b.Artifact.SHA256 {
		return r, VerifyError("candidate changed after verification")
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".tdev-candidate-")
	if err != nil {
		return r, err
	}
	candidate := f.Name()
	_ = f.Close()
	defer os.Remove(candidate)
	if err = config.AtomicWrite(candidate, data, 0700); err != nil {
		return r, err
	}
	if err = Candidate(ctx, candidate, b.Manifest.Version); err != nil {
		return r, err
	}
	r = Receipt{Version: b.Manifest.Version, Destination: destination, SHA256: b.Artifact.SHA256, SourceURL: b.SourceURL, PublicKey: string(b.PublicKey)}
	r.KeyFingerprint, err = Fingerprint(b.PublicKey)
	if err != nil {
		return r, err
	}
	unchanged := false
	if before.Kind == "file" {
		old, err := os.ReadFile(destination)
		if err != nil {
			return r, err
		}
		unchanged = bytes.Equal(old, data)
		if unchanged {
			prior, err := ReadReceipt(destination)
			if err != nil && !os.IsNotExist(err) {
				return r, err
			}
			if err == nil && prior.SHA256 == r.SHA256 {
				r.PreviousPath = prior.PreviousPath
				r.PreviousSHA256 = prior.PreviousSHA256
				r.PreviousReceipt = prior.PreviousReceipt
			}
		} else {
			backupDir, err := backupDirectory(store, destination)
			if err != nil {
				return r, err
			}
			if err = os.MkdirAll(backupDir, 0700); err != nil {
				return r, err
			}
			r.PreviousPath = filepath.Join(backupDir, "tdev.backup-"+time.Now().UTC().Format("20060102T150405.000000000")+"-"+config.NewID()[:8])
			if err = config.AtomicWrite(r.PreviousPath, old, 0700); err != nil {
				return r, err
			}
			oldHash := sha256.Sum256(old)
			r.PreviousSHA256 = hex.EncodeToString(oldHash[:])
			if prior, err := os.ReadFile(ReceiptPath(destination)); err == nil {
				r.PreviousReceipt = r.PreviousPath + ".json"
				if err = config.AtomicWrite(r.PreviousReceipt, prior, 0600); err != nil {
					return r, err
				}
			} else if !os.IsNotExist(err) {
				return r, err
			}
		}
	}
	now, err := transfer.SnapshotPath(destination)
	if err != nil {
		return r, err
	}
	if now != before {
		return r, protocol.Fail("DESTINATION_CHANGED", 4, "binary changed during installation")
	}
	// Keep identical installed bytes in place, repairing executable permissions when needed.
	if unchanged {
		info, err := os.Stat(destination)
		if err != nil {
			return r, err
		}
		if info.Mode().Perm() != 0755 {
			err = os.Chmod(destination, 0755)
			if err != nil {
				return r, err
			}
		}
	} else {
		if err = os.Chmod(candidate, 0755); err != nil {
			return r, err
		}
		if err = os.Rename(candidate, destination); err != nil {
			return r, err
		}
	}
	encoded, _ := json.MarshalIndent(r, "", "  ")
	priorReceipt, readErr := os.ReadFile(ReceiptPath(destination))
	if readErr != nil || !bytes.Equal(priorReceipt, encoded) {
		if err = config.AtomicWrite(ReceiptPath(destination), encoded, 0600); err != nil {
			return r, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "binary installed but receipt write failed: " + err.Error(), Details: r}
		}
	}
	if err = Candidate(ctx, destination, r.Version); err != nil {
		return r, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "installed binary verification failed; backup retained: " + err.Error(), Details: r}
	}
	if err = pruneBackups(store, destination); err != nil {
		return r, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "binary installed but backup cleanup failed: " + err.Error(), Details: r}
	}
	return r, nil
}
func Rollback(ctx context.Context, store config.Store, destination string) (any, error) {
	destination, err := Destination(destination)
	if err != nil {
		return nil, err
	}
	unlock, err := (config.Store{Root: StateRoot()}).Lock(ctx, lockKey(destination), "install")
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err = migrateInstallation(store, destination); err != nil {
		return nil, err
	}
	r, err := ReadReceipt(destination)
	if err != nil {
		return nil, err
	}
	if r.PreviousPath == "" || r.PreviousSHA256 == "" {
		return nil, VerifyError("no previous binary backup is recorded")
	}
	backupDir, err := backupDirectory(store, destination)
	if err != nil {
		return nil, err
	}
	managedBackup := filepath.Dir(r.PreviousPath) == backupDir && strings.HasPrefix(filepath.Base(r.PreviousPath), "tdev.backup-")
	// Receipts written by earlier versions still refer to adjacent backups.
	legacyBackup := filepath.Dir(r.PreviousPath) == filepath.Dir(destination) && strings.HasPrefix(r.PreviousPath, destination+".backup-")
	if !managedBackup && !legacyBackup {
		return nil, VerifyError("invalid backup path")
	}
	hash, err := transfer.Hash(destination)
	if err != nil {
		return nil, err
	}
	if hash != r.SHA256 {
		return nil, protocol.Fail("DESTINATION_CHANGED", 4, "installed binary changed outside tdev; refusing rollback")
	}
	backupHash, err := transfer.Hash(r.PreviousPath)
	if err != nil {
		return nil, err
	}
	if backupHash != r.PreviousSHA256 {
		return nil, VerifyError("backup digest mismatch")
	}
	data, err := os.ReadFile(r.PreviousPath)
	if err != nil {
		return nil, err
	}
	var prior []byte
	if r.PreviousReceipt != "" {
		if r.PreviousReceipt != r.PreviousPath+".json" {
			return nil, VerifyError("invalid backup receipt path")
		}
		prior, err = os.ReadFile(r.PreviousReceipt)
		if err != nil {
			return nil, err
		}
		var p Receipt
		if err = json.Unmarshal(prior, &p); err != nil || p.Destination != destination || p.SHA256 != backupHash {
			return nil, VerifyError("previous receipt does not match backup")
		}
	}
	if err = config.AtomicWrite(destination, data, 0755); err != nil {
		return nil, err
	}
	if len(prior) > 0 {
		err = config.AtomicWrite(ReceiptPath(destination), prior, 0600)
	} else {
		err = os.Remove(ReceiptPath(destination))
	}
	if err != nil {
		return nil, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "binary restored but receipt restoration failed: " + err.Error()}
	}
	actual, err := transfer.Hash(destination)
	if err != nil || actual != backupHash {
		return nil, fmt.Errorf("rollback verification failed: %v", err)
	}
	if err = pruneBackups(store, destination); err != nil {
		return nil, protocol.Fail("PARTIAL_SUCCESS", 9, "binary restored but backup cleanup failed: %v", err)
	}
	return map[string]string{"destination": destination, "restored_sha256": actual, "status": "rolled_back"}, nil
}
